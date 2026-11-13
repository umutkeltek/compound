package product

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/mem"
)

// salesKindsLine is a sales series row from a collector that separates
// download kinds. final may be nil for a row that does not say.
func salesKindsLine(appID, date string, firstDownloads, redownloads, updates, iapUnits, proceeds float64, final *bool) string {
	row := map[string]any{
		"date": date, "apple_identifier": appID, "title": "Example App",
		"units": firstDownloads + redownloads + updates + iapUnits, "app_units": firstDownloads + redownloads + updates,
		"iap_units": iapUnits, "first_downloads": firstDownloads, "redownloads": redownloads, "updates": updates,
		"other_units": 0, "developer_proceeds_usd": proceeds,
	}
	if final != nil {
		row["final"] = *final
	}
	line, _ := json.Marshal(row)
	return string(line)
}

// registerExampleApps registers products and writes a ledger whose rows cite
// each product's app id, plus any extra rows.
func registerExampleApps(t *testing.T, home string, ids map[string]string, extra ...Signal) {
	t.Helper()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	ledger := append([]Signal{}, extra...)
	for _, product := range growthSortedKeys(ids) {
		appID := ids[product]
		if _, err := Apply(home, store, registryBundle(product, growthHumanize(product)), strings.TrimPrefix(product, "product:")+".yaml", tProduct); err != nil {
			t.Fatal(err)
		}
		ledger = append(ledger, ascLedgerSignal("sig_"+appID, product, appID, "app_store_connect_review_count", growthDay(200), 1))
	}
	writeASCLedger(t, home, ledger...)
}

// "units" is new downloads only. Updates, redownloads and in-app purchases
// have their own keys and never add to it; a series without download kinds
// gives "not known", never a sum of the other kinds.
func TestGrowthUnitsCountNewDownloadsOnly(t *testing.T) {
	home := t.TempDir()
	registerExampleApps(t, home, map[string]string{"product:example-app": "9000000001", "product:older-app": "9000000002"})
	writeASCSeries(t, home,
		salesKindsLine("9000000001", growthDay(2), 5, 2, 40, 3, 9.5, nil),
		ascSeriesLine("9000000002", growthDay(2), 4, 50, 47, 3), // older series row: no download kinds
	)
	g := buildGrowthT(t, home, growthOpts())
	app := growthApp(t, g, "product:example-app")
	for key, want := range map[string]float64{"units": 5, "iap_units": 3, "updates": 40, "redownloads": 2} {
		if k := app.Kpis[key]; k.Value == nil || *k.Value != want || !k.HasData || k.Unit != "count" {
			t.Fatalf("%s = %+v, want %v", key, k, want)
		}
	}
	older := growthApp(t, g, "product:older-app")
	for _, key := range []string{"units", "updates", "redownloads"} {
		if k := older.Kpis[key]; k.HasData || k.Value != nil {
			t.Fatalf("older series %s = %+v, want not known", key, k)
		}
	}
	if k := older.Kpis["iap_units"]; k.Value == nil || *k.Value != 3 {
		t.Fatalf("older series in-app units = %+v, want 3", k)
	}
	if k := g.Portfolio.Totals["units"]; k.Value == nil || *k.Value != 5 {
		t.Fatalf("portfolio units = %+v, want only the 5 new downloads", k)
	}
	p := app.Kpis["proceeds_usd"]
	if !p.Estimate || !strings.Contains(p.Note, "fixed rate") || p.Unit != "usd" || *p.Value != 9.5 {
		t.Fatalf("proceeds = %+v, want the same number marked as an estimate", p)
	}
	if k := app.Kpis["units"]; k.Estimate || k.Note != "" {
		t.Fatalf("units is the report's own count, not an estimate: %+v", k)
	}
	for _, f := range app.Funnel {
		if f.ID == "first_time_downloads" && (f.Value == nil || *f.Value != 5 || !strings.Contains(f.Note, "Sales report")) {
			t.Fatalf("downloads stage = %+v, want the Sales count and say so", f)
		}
	}
}

// Store KPIs end their window on the Sales report's newest final day, so days
// Apple has not published or may still correct are neither summed nor read as
// missing, and the previous window shifts the same way.
func TestGrowthStoreKpisEndOnNewestFinalDay(t *testing.T) {
	home := t.TempDir()
	var clicks []Signal
	for off := 2; off >= 0; off-- {
		clicks = append(clicks, ascLedgerSignal("sig_click"+growthDay(off), "product:example-app", "9000000001", "gsc_clicks", growthDay(off), 1))
	}
	registerExampleApps(t, home, map[string]string{"product:example-app": "9000000001"}, clicks...)
	yes, no := true, false
	var lines []string
	for off := 70; off >= 0; off-- {
		v, final := 1.0, &yes
		switch {
		case off <= 2:
			v, final = 100, &no // not final yet
		case off >= 60 && off <= 62:
			v = 10
		}
		lines = append(lines, salesKindsLine("9000000001", growthDay(off), v, 0, 0, 0, v, final))
	}
	writeASCSeries(t, home, lines...)

	g := buildGrowthT(t, home, growthOpts())
	u := growthApp(t, g, "product:example-app").Kpis["units"]
	// Window days 3..32 (30 x 1); previous days 33..62 (27 x 1 + 3 x 10).
	if u.Value == nil || *u.Value != 30 || u.Previous == nil || *u.Previous != 57 || u.LatestDate != growthDay(3) || u.DaysReported != 30 || u.DaysInWindow != 30 {
		t.Fatalf("units = %+v, want 30 (prev 57) ending on %s with every day reported", u, growthDay(3))
	}
	if p := g.Portfolio.Totals["proceeds_usd"]; p.Value == nil || *p.Value != 30 || p.LatestDate != growthDay(3) {
		t.Fatalf("portfolio proceeds = %+v, want the same final-day window", p)
	}
	if c := growthApp(t, g, "product:example-app").Kpis["web_clicks"]; c.Value == nil || *c.Value != 3 {
		t.Fatalf("a non-store KPI keeps the as-of window: %+v", c)
	}

	// A source whose newest final day is more than 4 days old keeps today's
	// behaviour: the window ends on as-of.
	lines = lines[:0]
	for off := 40; off >= 0; off-- {
		final := &no
		if off >= 6 {
			final = &yes
		}
		lines = append(lines, salesKindsLine("9000000001", growthDay(off), 1, 0, 0, 0, 1, final))
	}
	writeASCSeries(t, home, lines...)
	u = growthApp(t, buildGrowthT(t, home, growthOpts()), "product:example-app").Kpis["units"]
	if u.LatestDate != growthDay(0) || *u.Value != 30 {
		t.Fatalf("stale final day: units = %+v, want the window to end on as-of", u)
	}

	// A no-sales day in the days index is final too, even with no series row.
	writeASCSeries(t, home, salesKindsLine("9000000001", growthDay(6), 1, 0, 0, 0, 1, &yes))
	writeASCDaysIndex(t, home, `{"date":"`+growthDay(4)+`","status":"no_sales","final":true}`, `{"date":"`+growthDay(1)+`","status":"reported","final":false}`)
	u = growthApp(t, buildGrowthT(t, home, growthOpts()), "product:example-app").Kpis["units"]
	if u.LatestDate != growthDay(4) || *u.Value != 1 || u.DaysReported != 1 {
		t.Fatalf("index final day: units = %+v, want the window to end on %s", u, growthDay(4))
	}
}

func writeASCDaysIndex(t *testing.T, home string, lines ...string) {
	t.Helper()
	writeHomeFile(t, home, growthASCSalesDaysIndexRel, strings.Join(lines, "\n")+"\n")
}

// writeHomeFile writes body to rel under home, making its folders.
func writeHomeFile(t *testing.T, home, rel, body string) {
	t.Helper()
	p := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
