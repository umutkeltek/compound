package product

import (
	"strings"
	"testing"
)

// Every counted funnel stage sums the same days, the store KPIs' window, so a
// later stage cannot outgrow an earlier one because it covers more days. The
// downloads stage is named after what it counts and equals its tile.
func TestGrowthFunnelStagesShareOneWindow(t *testing.T) {
	home := t.TempDir()
	var usage []Signal
	for off := 5; off >= 1; off-- { // a rate: summing it would change its meaning
		usage = append(usage, ascLedgerSignal("sig_act"+growthDay(off), "product:example-app", "900000031", "posthog_activation_rate", growthDay(off), 0.4))
	}
	registerExampleApps(t, home, map[string]string{"product:example-app": "900000031"}, usage...)
	yes, no := true, false
	var sales, analytics []string
	for off := 40; off >= 0; off-- {
		iap, final := 1.0, &yes
		if off <= 2 { // not final yet
			iap, final = 5, &no
		}
		sales = append(sales, salesKindsLine("900000031", growthDay(off), 4, 0, 0, iap, 1, final))
		if off >= 1 {
			analytics = append(analytics, ascFunnelLine("900000031", growthDay(off), map[string]float64{"impressions": 100, "product_page_views": 10, "first_time_downloads": 3}, nil))
		}
	}
	writeASCSeries(t, home, sales...)
	writeASCFunnelSeries(t, home, analytics...)

	app := growthApp(t, buildGrowthT(t, home, growthOpts()), "product:example-app")
	stage := map[string]GrowthFunnelStage{}
	for _, f := range app.Funnel {
		stage[f.ID] = f
	}
	// Newest final sales day is day 3: the window is days 3..32 for every stage.
	from, to := growthDay(32), growthDay(3)
	for id, want := range map[string]float64{"impressions": 3000, "product_page_views": 300, "first_time_downloads": 120, "purchases": 30} {
		f := stage[id]
		if f.Value == nil || *f.Value != want || f.WindowStart != from || f.WindowEnd != to || f.DaysReported != 30 || f.Status != GrowthMeasured {
			t.Fatalf("%s = %+v, want %v summed over %s..%s", id, f, want, from, to)
		}
		if !strings.Contains(f.Note, from+" to "+to) {
			t.Fatalf("%s note %q does not name the window", id, f.Note)
		}
	}
	units, iap := app.Kpis["units"], app.Kpis["iap_units"]
	if d := stage["first_time_downloads"]; *d.Value != *units.Value || d.Label != "New downloads" || units.WindowStart != d.WindowStart || units.WindowEnd != d.WindowEnd {
		t.Fatalf("downloads stage %+v must restate the units tile %+v under the name of what it counts", d, units)
	}
	if p := stage["purchases"]; *p.Value != *iap.Value {
		t.Fatalf("purchases stage %v, want the in-app purchases tile %v", *p.Value, *iap.Value)
	}
	if a := stage["activation"]; a.Value == nil || *a.Value != 0.4 || a.WindowStart != "" || a.DaysReported != 0 || !strings.Contains(a.Note, "one reading") {
		t.Fatalf("activation = %+v, want its one newest reading, marked as one", a)
	}
}

// Without the Sales report's download kinds the stage falls back to analytics,
// sums the same window, and says it is the analytics count.
func TestGrowthFunnelDownloadsFromAnalyticsKeepTheirName(t *testing.T) {
	home := t.TempDir()
	registerExampleApps(t, home, map[string]string{"product:example-app": "900000032"})
	var analytics []string
	for off := 40; off >= 0; off-- {
		analytics = append(analytics, ascFunnelLine("900000032", growthDay(off), map[string]float64{"impressions": 50, "first_time_downloads": 2}, nil))
	}
	writeASCFunnelSeries(t, home, analytics...)
	app := growthApp(t, buildGrowthT(t, home, growthOpts()), "product:example-app")
	for _, f := range app.Funnel {
		if f.ID == "first_time_downloads" && (f.Label != "First-time downloads" || f.Value == nil || *f.Value != 60 || f.WindowEnd != growthDay(0) || !strings.Contains(f.Note, "App Store analytics")) {
			t.Fatalf("downloads stage = %+v, want analytics' 60 over the as-of window", f)
		}
		if f.ID == "impressions" && (f.Value == nil || *f.Value != 1500) {
			t.Fatalf("impressions = %+v, want 1500 over the same window", f)
		}
	}
}
