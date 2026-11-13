package product

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/mem"
)

func writeASCLedger(t *testing.T, home string, sigs ...Signal) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "ledger"), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, s := range sigs {
		line, _ := json.Marshal(s)
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(signalsLog(home), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ascLedgerSignal(id, product, appID, metric, date string, v float64) Signal {
	return Signal{
		ID: id, ProductID: product, MetricName: metric, After: v,
		Window: "daily snapshot " + date, ObservedAt: date + "T00:00:00Z",
		EvidenceRefs: []string{"asc:app:" + appID},
	}
}

func writeASCSeries(t *testing.T, home string, lines ...string) {
	t.Helper()
	p := filepath.Join(home, growthASCSalesSeriesRel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ascSeriesLine(appID, date string, proceeds, units, appUnits, iapUnits float64) string {
	line, _ := json.Marshal(ascSalesSeriesRow{
		Date: date, AppleIdentifier: appID, Title: "Synthetic",
		Units: units, AppUnits: appUnits, IAPUnits: iapUnits, DeveloperProceedsUSD: proceeds,
	})
	return string(line)
}

func growthPoint(t *testing.T, series []GrowthPoint, date string) *float64 {
	t.Helper()
	for _, p := range series {
		if p.Date == date {
			return p.Value
		}
	}
	t.Fatalf("series has no point for %s", date)
	return nil
}

// The ledger holds a one-time copy that ends 25 days ago; the daily series
// keeps going. The projection must follow the series, let it win on a shared
// day, and leave unreported days missing.
func TestGrowthReadsASCSalesSeriesPastTheLedger(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(home, store, registryBundle("product:alpha", "Alpha"), "a.yaml", tProduct); err != nil {
		t.Fatal(err)
	}
	writeASCLedger(t, home,
		ascLedgerSignal("sig_1", "product:alpha", "111", "app_store_connect_developer_proceeds_usd", growthDay(26), 4),
		ascLedgerSignal("sig_2", "product:alpha", "111", "app_store_connect_developer_proceeds_usd", growthDay(25), 1),
	)
	writeASCSeries(t, home,
		ascSeriesLine("111", growthDay(25), 2, 1, 0, 1),
		ascSeriesLine("111", growthDay(3), 5, 3, 2, 1),
		"not json",
		ascSeriesLine("111", growthDay(2), 7, 4, 3, 1),
		ascSeriesLine("111", growthDay(-1), 100, 100, 100, 100), // after as-of: ignored
	)

	g := buildGrowthT(t, home, growthOpts())
	app := growthApp(t, g, "product:alpha")

	var asc *GrowthSourceFreshness
	for i := range app.Freshness.Sources {
		if app.Freshness.Sources[i].Source == "app_store_connect" {
			asc = &app.Freshness.Sources[i]
		}
	}
	if asc == nil || asc.LatestDate != growthDay(2) || asc.Stale {
		t.Fatalf("App Store freshness = %+v, want latest %s and not stale", asc, growthDay(2))
	}

	series := app.Series["proceeds_usd"]
	if v := growthPoint(t, series, growthDay(25)); v == nil || *v != 2 {
		t.Fatalf("shared day = %v, want the series value 2", v)
	}
	if v := growthPoint(t, series, growthDay(26)); v == nil || *v != 4 {
		t.Fatalf("ledger-only day = %v, want 4", v)
	}
	for _, d := range []int{1, 0, 10} {
		if v := growthPoint(t, series, growthDay(d)); v != nil {
			t.Fatalf("unreported day %s = %v, want missing", growthDay(d), *v)
		}
	}

	kpi := app.Kpis["proceeds_usd"]
	// 30-day window: ledger-only day 26 (4) + shared day 25 (series 2) + days 3 and 2 (5, 7).
	if kpi.Value == nil || *kpi.Value != 18 || kpi.DaysReported != 4 || kpi.LatestDate != growthDay(2) {
		t.Fatalf("proceeds KPI = %+v, want 4+2+5+7=18 over 4 reported days, latest %s", kpi, growthDay(2))
	}
	// This series predates download kinds: its "units" add downloads,
	// updates and purchases together, so new downloads are not known.
	if u := app.Kpis["units"]; u.HasData || u.Value != nil {
		t.Fatalf("units KPI = %+v, want not known from a series without first_downloads", u)
	}
	if u := app.Kpis["iap_units"]; u.Value == nil || *u.Value != 3 {
		t.Fatalf("in-app units KPI = %+v, want 1+1+1=3", u)
	}

	var purchases *GrowthFunnelStage
	for i := range app.Funnel {
		if app.Funnel[i].ID == "purchases" {
			purchases = &app.Funnel[i]
		}
	}
	if purchases == nil || purchases.Status == "blind" {
		t.Fatalf("purchases step = %+v, want it measured from the series' in-app purchase units", purchases)
	}
}

// An app id nobody cites must not invent an app, and an id two apps cite must
// not be guessed.
func TestGrowthASCSalesSkipsUnknownAndAmbiguousIDs(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []Bundle{registryBundle("product:alpha", "Alpha"), registryBundle("product:beta", "Beta")} {
		if _, err := Apply(home, store, b, "r.yaml", tProduct); err != nil {
			t.Fatal(err)
		}
	}
	writeASCLedger(t, home,
		ascLedgerSignal("sig_1", "product:alpha", "222", "app_store_connect_units", growthDay(20), 1),
		ascLedgerSignal("sig_2", "product:beta", "222", "app_store_connect_units", growthDay(20), 1),
	)
	writeASCSeries(t, home,
		ascSeriesLine("222", growthDay(1), 50, 50, 50, 0),
		ascSeriesLine("999", growthDay(1), 70, 70, 70, 0),
	)

	g := buildGrowthT(t, home, growthOpts())
	if len(g.Apps) != 2 {
		ids := []string{}
		for _, a := range g.Apps {
			ids = append(ids, a.ProductID)
		}
		t.Fatalf("apps = %v, want only alpha and beta", ids)
	}
	for _, id := range []string{"product:alpha", "product:beta"} {
		app := growthApp(t, g, id)
		if v := growthPoint(t, app.Series["proceeds_usd"], growthDay(1)); v != nil {
			t.Fatalf("%s took a row from an ambiguous app id: %v", id, *v)
		}
	}
}
