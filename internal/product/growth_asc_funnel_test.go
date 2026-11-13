package product

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ascFunnelCellSource is the report each analytics cell is published in.
var ascFunnelCellSource = map[string]struct{ key, name, population string }{
	"impressions":          {"app_store_discovery_and_engagement_standard", "App Store Discovery and Engagement Standard", "published_impression_counts"},
	"product_page_views":   {"app_store_discovery_and_engagement_standard", "App Store Discovery and Engagement Standard", "published_product_page_view_counts"},
	"first_time_downloads": {"app_downloads_standard", "App Downloads Standard", "published_first_time_download_counts"},
}

// ascFunnelLine builds one v2 analytics row. Cells in observed carry observed
// evidence; cells in unknown carry a value but "unknown" evidence.
func ascFunnelLine(appID, date string, observed, unknown map[string]float64) string {
	row := map[string]any{"schema_version": ascFunnelRowSchemaV2, "asc_app_id": appID, "date": date}
	evidence, sources := map[string]any{}, map[string]any{}
	add := func(cell string, v float64, status string) {
		src := ascFunnelCellSource[cell]
		row[cell] = v
		e := map[string]any{"status": status, "reason": nil, "population": src.population, "source_report_key": src.key, "matched_rows": 1}
		if status != "observed" {
			e["reason"] = "incomplete_snapshot"
		}
		evidence[cell] = e
		sources[src.key] = map[string]any{
			"report_id": "report-synthetic", "report_name": src.name, "instance_id": "instance-" + date,
			"processing_date": date, "granularity": "DAILY", "segment_inventory_complete": true,
			"expected_segment_ids": []string{"segment-1"},
			"segments":             []map[string]any{{"id": "segment-1", "sha256": strings.Repeat("b", 64), "byte_count": 10}},
		}
	}
	for cell, v := range observed {
		add(cell, v, "observed")
	}
	for cell, v := range unknown {
		add(cell, v, "unknown")
	}
	row["metric_evidence"], row["source_reports"] = evidence, sources
	line, _ := json.Marshal(row)
	return string(line)
}

func writeASCFunnelSeries(t *testing.T, home string, lines ...string) {
	t.Helper()
	p := filepath.Join(home, growthASCFunnelSeriesRel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Only an observed cell becomes a number. A cell marked unknown, a legacy row
// without cell state, and an app and day listed twice all stay missing.
func TestGrowthASCFunnelOnlyObservedCellsCount(t *testing.T) {
	home := t.TempDir()
	legacy, _ := json.Marshal(map[string]any{"asc_app_id": "900000001", "date": growthDay(4), "impressions": 400})
	writeASCFunnelSeries(t, home,
		ascFunnelLine("900000001", growthDay(2), map[string]float64{"impressions": 50}, map[string]float64{"product_page_views": 7}),
		ascFunnelLine("900000001", growthDay(3), nil, map[string]float64{"impressions": 70}),
		string(legacy),
		ascFunnelLine("900000001", growthDay(5), map[string]float64{"impressions": 11}, nil),
		ascFunnelLine("900000001", growthDay(5), map[string]float64{"impressions": 12}, nil),
		"not json",
		ascFunnelLine("900000001", growthDay(-1), map[string]float64{"impressions": 99}, nil), // after as-of
	)
	points, err := loadGrowthASCFunnelSeries(home)
	if err != nil {
		t.Fatal(err)
	}
	g := newGrowthGroup(0)
	applyGrowthASCFunnel(points, map[string]map[*growthGroup]bool{"900000001": {g: true}}, growthDay(0))
	impr := g.signals["app_store_connect_impressions"]
	if len(impr) != 1 || impr[growthDay(2)].v != 50 {
		t.Fatalf("impressions = %+v, want only the observed 50 on %s", impr, growthDay(2))
	}
	if ppv := g.signals["app_store_connect_product_page_views"]; len(ppv) != 0 {
		t.Fatalf("unknown product page views became numbers: %+v", ppv)
	}
	// An app id that maps to no app, or to two, takes nothing.
	other, second := newGrowthGroup(1), newGrowthGroup(2)
	applyGrowthASCFunnel(points, map[string]map[*growthGroup]bool{"900000001": {other: true, second: true}}, growthDay(0))
	if len(other.signals) != 0 || len(second.signals) != 0 {
		t.Fatal("an ambiguous app id must not feed either app")
	}
}

// The analytics ledger copy stopped 25 days ago, so the two areas analytics
// feeds read as stale. Reading the analytics series turns them measured, and
// the series wins on the one day both hold.
func TestGrowthASCFunnelTurnsStaleAnalyticsAreasMeasured(t *testing.T) {
	home := t.TempDir()
	applyGrowthBundle(t, home, "product:example-app", func(p *Product) {
		p.Profile = ProductProfile{Kinds: []string{"ios_app"}}
	})
	var ledger []Signal
	for _, metric := range []string{"app_store_connect_impressions", "app_store_connect_product_page_views", "app_store_connect_first_time_downloads"} {
		for off := 28; off >= 25; off-- {
			ledger = append(ledger, ascLedgerSignal("sig_"+metric+growthDay(off), "product:example-app", "900000001", metric, growthDay(off), 1))
		}
	}
	writeASCLedger(t, home, ledger...)

	before := growthApp(t, buildGrowthT(t, home, growthOpts()), "product:example-app")
	for _, area := range []string{"acquisition_search", "acquisition_listing"} {
		if st := growthAudit(t, before, area).Status; st != GrowthStale {
			t.Fatalf("%s without the analytics series = %s, want stale", area, st)
		}
	}

	lines := []string{ascFunnelLine("900000001", growthDay(25), map[string]float64{"impressions": 99, "product_page_views": 99, "first_time_downloads": 99}, nil)}
	for off := 10; off >= 3; off-- {
		lines = append(lines, ascFunnelLine("900000001", growthDay(off), map[string]float64{"impressions": 40, "product_page_views": 9, "first_time_downloads": 2}, nil))
	}
	// A fresher day whose downloads cell is not observed must not count.
	lines = append(lines, ascFunnelLine("900000001", growthDay(2), map[string]float64{"impressions": 40}, map[string]float64{"first_time_downloads": 500}))
	writeASCFunnelSeries(t, home, lines...)

	after := growthApp(t, buildGrowthT(t, home, growthOpts()), "product:example-app")
	for _, area := range []string{"acquisition_search", "acquisition_listing"} {
		if a := growthAudit(t, after, area); a.Status != GrowthMeasured {
			t.Fatalf("%s with the analytics series = %s (%s), want measured", area, a.Status, a.Summary)
		}
	}
	// Window days 0..29: ledger days 28, 27, 26 (1 each), day 25 from the series (99), days 10..3 (2 each).
	if k := after.Kpis["first_time_downloads"]; k.Value == nil || *k.Value != 3+99+16 || k.LatestDate != growthDay(3) {
		t.Fatalf("first-time downloads = %+v, want 118 with latest %s", k, growthDay(3))
	}
	for _, f := range after.Funnel {
		if f.ID == "impressions" && (f.Status != GrowthMeasured || f.AsOf != growthDay(2)) {
			t.Fatalf("impressions stage = %+v", f)
		}
	}
}
