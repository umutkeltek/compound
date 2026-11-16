package product

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/mem"
)

// growthAsOf is injected; no growth test reads the wall clock.
var growthAsOf = time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)

func growthOpts() GrowthOptions {
	return GrowthOptions{Now: growthAsOf.Add(9 * time.Hour), AsOf: growthAsOf}
}

func growthDay(offset int) string { return growthAsOf.AddDate(0, 0, -offset).Format("2006-01-02") }

type growthRow struct {
	product, metric string
	date            string
	value           float64
	observed        string
}

func writeGrowthSignals(t *testing.T, home string, rows ...growthRow) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "ledger"), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i, r := range rows {
		prefix := "daily snapshot "
		if strings.HasPrefix(r.metric, "gsc_") {
			prefix = "snapshot "
		}
		observed := r.observed
		if observed == "" {
			observed = "2026-11-01T00:00:00Z"
		}
		line, _ := json.Marshal(Signal{
			ID: fmt.Sprintf("sig_%d", i), ProductID: r.product, MetricName: r.metric,
			Before: 0, After: r.value, Window: prefix + r.date, ObservedAt: observed,
		})
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(signalsLog(home), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeGrowthAds(t *testing.T, home string, rows ...AdRow) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "ledger"), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i, r := range rows {
		r.ID = fmt.Sprintf("adrow_%d", i)
		if r.Platform == "" {
			r.Platform = "apple_search_ads"
		}
		if r.CampaignID == "" {
			r.CampaignID = "camp1"
		}
		line, _ := json.Marshal(r)
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(adsRowsLog(home), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// daily returns one row per day for offsets from..to (days before as-of).
func growthDaily(product, metric string, from, to int, value float64) []growthRow {
	var out []growthRow
	for off := from; off >= to; off-- {
		out = append(out, growthRow{product: product, metric: metric, date: growthDay(off), value: value})
	}
	return out
}

func buildGrowthT(t *testing.T, home string, opts GrowthOptions) Growth {
	t.Helper()
	g, err := BuildGrowth(home, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	return g
}

func growthApp(t *testing.T, g Growth, id string) GrowthApp {
	t.Helper()
	for _, a := range g.Apps {
		if a.ProductID == id {
			return a
		}
	}
	t.Fatalf("app %s not found in %v", id, func() []string {
		var ids []string
		for _, a := range g.Apps {
			ids = append(ids, a.ProductID)
		}
		return ids
	}())
	return GrowthApp{}
}

func growthAudit(t *testing.T, a GrowthApp, area string) GrowthAudit {
	t.Helper()
	for _, x := range a.Audit {
		if x.AreaID == area {
			return x
		}
	}
	t.Fatalf("area %s missing", area)
	return GrowthAudit{}
}

func TestGrowthMergesSpellingVariantsAndAcceptsAliases(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	b := registryBundle("product:alpha_app", "Alpha App")
	if _, err := Apply(home, store, b, "alpha.yaml", tProduct); err != nil {
		t.Fatal(err)
	}
	// The hyphen spelling carries more rows, but the registry id still wins.
	rows := growthDaily("product:alpha-app", growthSalesFirstDownloads, 5, 0, 2)
	rows = append(rows, growthRow{product: "product:alpha_app", metric: growthSalesFirstDownloads, date: growthDay(9), value: 1})
	writeGrowthSignals(t, home, rows...)

	g := buildGrowthT(t, home, growthOpts())
	if g.Portfolio.AppCount != 1 || len(g.Apps) != 1 {
		t.Fatalf("want one merged app, got %d", len(g.Apps))
	}
	app := g.Apps[0]
	if app.ProductID != "product:alpha_app" || app.Name != "Alpha App" {
		t.Fatalf("canonical = %s / %s", app.ProductID, app.Name)
	}
	if len(app.Aliases) != 1 || app.Aliases[0] != "product:alpha-app" {
		t.Fatalf("aliases = %v", app.Aliases)
	}
	if got := *app.Kpis["units"].Value; got != 13 {
		t.Fatalf("units across both spellings = %v, want 13", got)
	}
	for _, q := range []string{"product:alpha-app", "alpha-app", "Alpha_App", "product:alpha_app"} {
		o := growthOpts()
		o.ProductID = q
		one := buildGrowthT(t, home, o)
		if len(one.Apps) != 1 || one.Apps[0].ProductID != "product:alpha_app" {
			t.Fatalf("--product %q resolved to %v", q, one.Apps)
		}
	}
	o := growthOpts()
	o.ProductID = "product:nope"
	if _, err := BuildGrowth(home, o); err == nil {
		t.Fatal("unknown product must error")
	}
}

func TestGrowthUnregisteredSpellingsPickMostRows(t *testing.T) {
	home := t.TempDir()
	rows := growthDaily("product:gamma-web", "gsc_clicks", 4, 0, 1)
	rows = append(rows, growthRow{product: "product:gamma_web", metric: "gsc_clicks", date: growthDay(8), value: 1})
	writeGrowthSignals(t, home, rows...)
	g := buildGrowthT(t, home, growthOpts())
	if len(g.Apps) != 1 || g.Apps[0].ProductID != "product:gamma-web" || g.Apps[0].Name != "Gamma Web" {
		t.Fatalf("apps = %+v", g.Apps)
	}
}

// A feed that stops must read as missing, never as zero revenue. This test
// fails if the series were zero-filled.
func TestGrowthSeriesNeverZeroFills(t *testing.T) {
	home := t.TempDir()
	rows := growthDaily("product:alpha", "app_store_connect_developer_proceeds_usd", 40, 30, 5)
	writeGrowthSignals(t, home, rows...)
	g := buildGrowthT(t, home, growthOpts())
	app := growthApp(t, g, "product:alpha")
	pts := app.Series["proceeds_usd"]
	if len(pts) != 120 {
		t.Fatalf("series length = %d", len(pts))
	}
	if pts[len(pts)-1].Date != growthDay(0) || pts[0].Date != growthDay(119) {
		t.Fatalf("series range %s..%s", pts[0].Date, pts[len(pts)-1].Date)
	}
	have, missing := 0, 0
	for _, p := range pts {
		if p.Value == nil {
			missing++
			continue
		}
		have++
		if *p.Value != 5 {
			t.Fatalf("value %v on %s", *p.Value, p.Date)
		}
	}
	if have != 11 || missing != 109 {
		t.Fatalf("have %d missing %d", have, missing)
	}
	if pts[len(pts)-1].Value != nil {
		t.Fatal("last day has no row and must be null, not zero")
	}
	if pts2 := g.Portfolio.Series["proceeds_usd"]; pts2[len(pts2)-1].Value != nil {
		t.Fatal("portfolio series must be null when no app reported")
	}
}

func TestGrowthKpiWindowPreviousAndDedupe(t *testing.T) {
	home := t.TempDir()
	m := "app_store_connect_developer_proceeds_usd"
	var rows []growthRow
	// window (offsets 0..29): rows on offsets 1, 3, 5 worth 10 each
	for _, off := range []int{1, 3, 5} {
		rows = append(rows, growthRow{product: "product:alpha", metric: m, date: growthDay(off), value: 10})
	}
	// previous window (offsets 30..59): two rows worth 4
	for _, off := range []int{31, 40} {
		rows = append(rows, growthRow{product: "product:alpha", metric: m, date: growthDay(off), value: 4})
	}
	// re-import of offset 1: the later import wins, not a double count
	rows = append(rows, growthRow{product: "product:alpha", metric: m, date: growthDay(1), value: 12, observed: "2026-11-05T00:00:00Z"})
	writeGrowthSignals(t, home, rows...)
	g := buildGrowthT(t, home, growthOpts())
	k := growthApp(t, g, "product:alpha").Kpis["proceeds_usd"]
	if k.Value == nil || *k.Value != 32 {
		t.Fatalf("value = %v, want 32 (12+10+10)", k.Value)
	}
	if k.Previous == nil || *k.Previous != 8 {
		t.Fatalf("previous = %v, want 8", k.Previous)
	}
	if k.DaysReported != 3 || k.DaysInWindow != 30 || !k.HasData || k.LatestDate != growthDay(1) || k.Unit != "usd" {
		t.Fatalf("kpi = %+v", k)
	}
	if units := growthApp(t, g, "product:alpha").Kpis["units"]; units.HasData || units.Value != nil {
		t.Fatalf("absent metric must be null: %+v", units)
	}
	// narrower window changes the math
	o := growthOpts()
	o.WindowDays = 2
	k2 := *buildGrowthT(t, home, o).Apps[0].Kpis["proceeds_usd"].Value
	if k2 != 12 {
		t.Fatalf("2-day window = %v, want 12", k2)
	}
}

func TestGrowthAdKpisAndCPA(t *testing.T) {
	home := t.TempDir()
	writeGrowthSignals(t, home)
	writeGrowthAds(t, home,
		AdRow{Currency: "USD", ProductID: "product:alpha", Date: growthDay(2), Spend: 6, Conversions: 2, ImportedAt: "2026-11-01T00:00:00Z"},
		AdRow{Currency: "USD", ProductID: "product:alpha", Date: growthDay(3), Spend: 4, Conversions: 0, ImportedAt: "2026-11-01T00:00:00Z"},
		AdRow{Currency: "USD", ProductID: "product:alpha", Date: growthDay(3), Spend: 4, Conversions: 0, ImportedAt: "2026-11-01T00:00:00Z"}, // exact re-import
		AdRow{Currency: "USD", ProductID: "product:beta-app", Date: growthDay(2), Spend: 3, Conversions: 0, ImportedAt: "2026-11-01T00:00:00Z"},
	)
	g := buildGrowthT(t, home, growthOpts())
	a := growthApp(t, g, "product:alpha").Kpis
	if *a["ad_spend_usd"].Value != 10 || *a["ad_conversions"].Value != 2 || *a["ad_cpa_usd"].Value != 5 {
		t.Fatalf("ads kpis = %+v", a)
	}
	b := growthApp(t, g, "product:beta-app").Kpis
	if b["ad_cpa_usd"].Value != nil {
		t.Fatalf("cpa with zero conversions must be null: %+v", b["ad_cpa_usd"])
	}
	if got := *g.Portfolio.Totals["ad_spend_usd"].Value; got != 13 {
		t.Fatalf("portfolio spend = %v", got)
	}
}

func TestGrowthAuditStatuses(t *testing.T) {
	home := t.TempDir()
	var rows []growthRow
	// ios app: units fresh and plenty, impressions absent, redownloads stale.
	rows = append(rows, growthDaily("product:ios-one", "app_store_connect_units", 9, 0, 1)...)
	rows = append(rows, growthDaily("product:ios-one", "app_store_connect_developer_proceeds_usd", 9, 0, 3)...)
	rows = append(rows, growthRow{product: "product:ios-one", metric: "app_store_connect_redownloads", date: growthDay(60), value: 1})
	// web app: search console rows, fresh for web_traffic.
	rows = append(rows, growthDaily("product:web_two", "gsc_clicks", 6, 0, 7)...)
	rows = append(rows, growthDaily("product:web_two", "gsc_impressions", 6, 0, 70)...)
	// stale web: last row 20 days ago.
	rows = append(rows, growthDaily("product:stale-web", "gsc_clicks", 25, 20, 1)...)
	writeGrowthSignals(t, home, rows...)
	g := buildGrowthT(t, home, growthOpts())

	ios := growthApp(t, g, "product:ios-one")
	if ios.Kind != "ios_app" {
		t.Fatalf("kind = %s", ios.Kind)
	}
	if got := growthAudit(t, ios, "acquisition_search").Status; got != GrowthBlind {
		t.Fatalf("aso = %s, want blind", got)
	}
	if got := growthAudit(t, ios, "activation_onboarding").Status; got != GrowthBlind {
		t.Fatalf("onboarding = %s", got)
	}
	if got := growthAudit(t, ios, "monetization_pricing").Status; got != GrowthPartial {
		t.Fatalf("pricing is never better than partial, got %s", got)
	}
	retention := growthAudit(t, ios, "retention_repeat_use")
	if retention.Status != GrowthStale || !strings.Contains(retention.NextMove, growthDay(60)) {
		t.Fatalf("retention = %+v", retention)
	}
	if got := growthAudit(t, ios, "opportunity_competitors"); got.Status != GrowthBlind || !strings.Contains(got.Summary, "No data source") {
		t.Fatalf("competitors = %+v", got)
	}

	web := growthApp(t, g, "product:web_two")
	if web.Kind != "website" {
		t.Fatalf("kind = %s", web.Kind)
	}
	wt := growthAudit(t, web, "acquisition_search")
	if wt.Status != GrowthMeasured || !strings.Contains(wt.Summary, "clicks from web search") {
		t.Fatalf("acquisition_search on a website = %+v", wt)
	}
	// The App Store forms never judge a website: the store-only areas fall back
	// to their base question and never ask for App Store data.
	for _, id := range []string{"acquisition_listing", "acquisition_paid", "monetization_purchase_prompt", "customer_ratings", "brand_trust"} {
		got := growthAudit(t, web, id)
		if strings.Contains(got.Summary+got.NextMove, "App Store") || strings.Contains(got.Summary+got.NextMove, "Apple") {
			t.Fatalf("%s on a website uses the App Store form: %+v", id, got)
		}
	}
	// The score is the measured share of the areas the profile can judge.
	judged := len(g.Areas) - web.Coverage.NotApplicable - web.Coverage.Deferred - web.Coverage.Unknown
	if web.Coverage.Unknown == 0 {
		t.Fatal("a website with no revenue on file cannot tell whether monetization applies")
	}
	wantScore := float64(web.Coverage.Measured) / float64(judged)
	if d := web.Coverage.Score - wantScore; d > 0.001 || d < -0.001 {
		t.Fatalf("score %v want %v", web.Coverage.Score, wantScore)
	}

	st := growthApp(t, g, "product:stale-web")
	if got := growthAudit(t, st, "acquisition_search"); got.Status != GrowthStale || !strings.Contains(got.NextMove, growthDay(20)) {
		t.Fatalf("stale web_traffic = %+v", got)
	}
	if !st.Freshness.Stale || st.Freshness.Sources[0].Source != "search_console" || st.Freshness.Sources[0].DaysSince != 20 {
		t.Fatalf("freshness = %+v", st.Freshness)
	}
	if len(web.NextMoves) != growthMaxMoves {
		t.Fatalf("next_moves = %+v", web.NextMoves)
	}
	// No revenue on file hides whether monetization applies, so the profile
	// comes first; then the open gap that the most applicable areas need,
	// worked out here from the product's own audit and the catalog's needs so
	// a catalog edit does not move the expectation.
	cat, err := loadEmbeddedGrowthCatalog()
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, a := range web.Audit {
		status[a.AreaID] = a.Status
	}
	wantID, wantN := "", -1
	for _, a := range web.Audit {
		if (a.Status != GrowthBlind && a.Status != GrowthPartial) || len(a.BlockedBy) > 0 {
			continue
		}
		n := 0
		for _, id := range cat.dependents(a.AreaID) {
			switch status[id] {
			case GrowthMeasured, GrowthPartial, GrowthStale, GrowthBlind:
				n++
			}
		}
		if n > wantN {
			wantID, wantN = a.AreaID, n
		}
	}
	if web.NextMoves[0].Kind != "set_profile" || web.NextMoves[1].AreaID != wantID || web.NextMoves[1].Unblocks != wantN || wantN <= 0 {
		t.Fatalf("first moves = %+v, want set_profile, then %s unblocking %d", web.NextMoves, wantID, wantN)
	}
}

func TestGrowthUnitEconomicsPartialWithoutAdRevenue(t *testing.T) {
	cases := []struct {
		name       string
		hasRevenue bool
		revenue    float64
		want       string
	}{
		{"no revenue linked", false, 0, GrowthPartial},
		{"has_revenue but zero", true, 0, GrowthPartial},
		{"revenue linked", true, 9, GrowthMeasured},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeGrowthSignals(t, home, growthDaily("product:alpha", "app_store_connect_developer_proceeds_usd", 9, 0, 3)...)
			writeGrowthAds(t, home,
				AdRow{Currency: "USD", ProductID: "product:alpha", Date: growthDay(1), Spend: 5, HasRevenue: tc.hasRevenue, Revenue: tc.revenue},
				AdRow{Currency: "USD", ProductID: "product:alpha", Date: growthDay(2), Spend: 5, HasRevenue: tc.hasRevenue, Revenue: tc.revenue},
				AdRow{Currency: "USD", ProductID: "product:alpha", Date: growthDay(3), Spend: 5, HasRevenue: tc.hasRevenue, Revenue: tc.revenue},
			)
			g := buildGrowthT(t, home, growthOpts())
			got := growthAudit(t, g.Apps[0], "operations_unit_economics")
			if got.Status != tc.want {
				t.Fatalf("unit_economics = %+v, want %s", got, tc.want)
			}
		})
	}
}

func TestGrowthAllocationClasses(t *testing.T) {
	home := t.TempDir()
	var rows []growthRow
	// fix_data: earned, but the sales feed stopped 25 days ago.
	rows = append(rows, growthDaily("product:fixme", "app_store_connect_developer_proceeds_usd", 40, 25, 9)...)
	// grow: proceeds in the window, fresh.
	rows = append(rows, growthDaily("product:growme", "app_store_connect_developer_proceeds_usd", 5, 0, 2)...)
	rows = append(rows, growthDaily("product:growme", "app_store_connect_units", 5, 0, 1)...)
	// measure: web traffic, no proceeds.
	rows = append(rows, growthDaily("product:measureme", "gsc_clicks", 10, 0, 30)...)
	// hold: a lone review count and nothing else.
	rows = append(rows, growthRow{product: "product:holdme", metric: "app_store_connect_review_count", date: growthDay(2), value: 1})
	writeGrowthSignals(t, home, rows...)
	g := buildGrowthT(t, home, growthOpts())
	want := map[string]string{
		"product:fixme": GrowthFocusFixData, "product:growme": GrowthFocusGrow,
		"product:measureme": GrowthFocusMeasure, "product:holdme": GrowthFocusHold,
	}
	got := map[string]GrowthAllocation{}
	for _, al := range g.Portfolio.Allocation {
		got[al.ProductID] = al
		if al.Rule == "" || al.Reason == "" {
			t.Fatalf("allocation needs rule and reason: %+v", al)
		}
	}
	for id, focus := range want {
		if got[id].Focus != focus {
			t.Fatalf("%s focus = %s, want %s (%s)", id, got[id].Focus, focus, got[id].Reason)
		}
	}
	// ranked by 90-day proceeds (fixme 144, growme 22), then clicks.
	if got["product:fixme"].Rank != 1 || got["product:growme"].Rank != 2 || got["product:measureme"].Rank != 3 || got["product:holdme"].Rank != 4 {
		t.Fatalf("ranks = %+v", got)
	}
	if !strings.Contains(got["product:fixme"].Reason, growthDay(25)) {
		t.Fatalf("fix_data reason must cite the stop date: %s", got["product:fixme"].Reason)
	}
}

func TestGrowthAppsWithoutDataAndExperiments(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"product:empty", "product:tested"} {
		b := registryBundle(id, strings.TrimPrefix(id, "product:"))
		if id == "product:tested" {
			b.Product.Profile = ProductProfile{Objectives: []string{"revenue"}}
		}
		if _, err := Apply(home, store, b, id+".yaml", tProduct); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(home, candidates.InboxDir(candidates.ProductHypothesis))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	h := HypothesisCandidate{ID: "hyp_1", ProductID: "product:tested", ChangeEvent: "Rework the paywall copy", ExpectedEffect: "more purchases", Metrics: []string{"posthog_paywall_views"}}
	b, _ := json.Marshal(h)
	if err := os.WriteFile(filepath.Join(dir, "hyp_1.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	g := buildGrowthT(t, home, growthOpts())
	if len(g.Portfolio.AppsWithoutData) != 2 {
		t.Fatalf("apps_without_data = %v", g.Portfolio.AppsWithoutData)
	}
	tested := growthApp(t, g, "product:tested")
	if tested.Hypotheses.Open != 1 {
		t.Fatalf("hypotheses = %+v", tested.Hypotheses)
	}
	if got := growthAudit(t, tested, "analytics_experiment_design").Status; got != GrowthPartial {
		t.Fatalf("experiments with only an open hypothesis = %s", got)
	}
	if got := growthAudit(t, tested, "monetization_purchase_prompt").Status; got != GrowthPartial {
		t.Fatalf("a paywall hypothesis must count as partial, got %s", got)
	}
	empty := growthApp(t, g, "product:empty")
	if got := growthAudit(t, empty, "analytics_experiment_design").Status; got != GrowthBlind {
		t.Fatalf("experiments with nothing = %s", got)
	}
	for _, k := range empty.Kpis {
		if k.HasData || k.Value != nil {
			t.Fatalf("empty app kpi has data: %+v", k)
		}
	}
}

func TestGrowthValidateRejectsBrokenShape(t *testing.T) {
	home := t.TempDir()
	writeGrowthSignals(t, home, growthDaily("product:alpha", "gsc_clicks", 3, 0, 1)...)
	g := buildGrowthT(t, home, growthOpts())
	raw, _ := json.Marshal(g)
	var m Growth
	_ = json.Unmarshal(raw, &m)
	if len(m.Areas) < 25 || len(m.Groups) != 12 || len(m.Apps[0].Funnel) != 7 {
		t.Fatalf("catalog shape wrong: %d areas, %d groups", len(m.Areas), len(m.Groups))
	}
	cases := map[string]func(*Growth){
		"schema":   func(x *Growth) { x.SchemaVersion = "v0" },
		"as_of":    func(x *Growth) { x.AsOf = "yesterday" },
		"audit":    func(x *Growth) { x.Apps[0].Audit = x.Apps[0].Audit[:len(x.Apps[0].Audit)-1] },
		"group":    func(x *Growth) { x.Areas[0].Group = "nowhere" },
		"series":   func(x *Growth) { x.Apps[0].Series["units"] = nil },
		"coverage": func(x *Growth) { x.Apps[0].Coverage.Blind += 3 },
		"status":   func(x *Growth) { x.Apps[0].Audit[0].Status = "great" },
	}
	for name, mutate := range cases {
		c := Growth{}
		_ = json.Unmarshal(raw, &c)
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Fatalf("%s: Validate accepted a broken projection", name)
		}
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"value":null`) {
		t.Fatal("missing values must serialize as null")
	}
}

// The projection must never write to the home it reads.
func TestGrowthIsReadOnly(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(home, store, registryBundle("product:alpha", "Alpha"), "a.yaml", tProduct); err != nil {
		t.Fatal(err)
	}
	writeGrowthSignals(t, home, growthDaily("product:alpha", "app_store_connect_units", 5, 0, 1)...)
	writeGrowthAds(t, home, AdRow{ProductID: "product:alpha", Date: growthDay(1), Spend: 1})
	snapshot := func() []string {
		var out []string
		_ = filepath.Walk(home, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				out = append(out, fmt.Sprintf("%s %d %d", p, info.Size(), info.ModTime().UnixNano()))
			}
			return nil
		})
		sort.Strings(out)
		return out
	}
	before := snapshot()
	buildGrowthT(t, home, growthOpts())
	after := snapshot()
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("growth changed the home:\nbefore %v\nafter  %v", before, after)
	}
}

func TestGrowthOptionsValidate(t *testing.T) {
	if err := (GrowthOptions{Now: growthAsOf, AsOf: growthAsOf, WindowDays: -1, SeriesDays: 1, StaleDays: 1}).Validate(); err == nil {
		t.Fatal("negative window must fail")
	}
	if err := normalizeGrowthOptionsForTest().Validate(); err != nil {
		t.Fatal(err)
	}
}

func normalizeGrowthOptionsForTest() GrowthOptions {
	o := normalizeGrowthOptions(GrowthOptions{Now: growthAsOf})
	if o.WindowDays != 30 || o.SeriesDays != 120 || o.StaleDays != 14 {
		panic("defaults changed")
	}
	return o
}

// Mixed-source areas: the summary and next move must name the input that is
// actually stale, with its own date, never the newest date across inputs.
func TestGrowthStaleNamesTheStaleInput(t *testing.T) {
	home := t.TempDir()
	rows := growthDaily("product:alpha", "app_store_connect_developer_proceeds_usd", 40, 26, 3)
	rows = append(rows, growthDaily("product:alpha", "app_store_connect_units", 40, 26, 1)...)
	rows = append(rows, growthRow{product: "product:alpha", metric: "app_store_connect_redownloads", date: growthDay(60), value: 1})
	writeGrowthSignals(t, home, rows...)
	var ads []AdRow
	for off := 5; off >= 0; off-- {
		ads = append(ads, AdRow{Currency: "USD", ProductID: "product:alpha", Date: growthDay(off), Spend: 2, HasRevenue: true})
	}
	writeGrowthAds(t, home, ads...)
	app := buildGrowthT(t, home, growthOpts()).Apps[0]

	ue := growthAudit(t, app, "operations_unit_economics")
	if ue.Status != GrowthStale {
		t.Fatalf("unit_economics = %+v", ue)
	}
	wantSummary := fmt.Sprintf("Sales data stopped on %s (26 days ago); ad spend is current; ads have no revenue linked.", growthDay(26))
	if ue.Summary != wantSummary {
		t.Fatalf("summary = %q\nwant      %q", ue.Summary, wantSummary)
	}
	if strings.Contains(ue.Summary, growthDay(0)) || !strings.Contains(ue.NextMove, "App Store Connect sales") || !strings.Contains(ue.NextMove, growthDay(26)) {
		t.Fatalf("next_move = %q", ue.NextMove)
	}

	ret := growthAudit(t, app, "retention_repeat_use")
	if ret.Status != GrowthStale {
		t.Fatalf("retention = %+v", ret)
	}
	// Redownloads come from the Sales report, so the stopped input is the sales feed.
	if strings.Contains(ret.NextMove, "PostHog") || !strings.Contains(ret.NextMove, "App Store Connect sales") || !strings.Contains(ret.NextMove, growthDay(60)) {
		t.Fatalf("retention next_move names the wrong source: %q", ret.NextMove)
	}
	if !strings.Contains(ret.Summary, growthDay(60)) || !strings.Contains(ret.Summary, "App Store Connect sales") {
		t.Fatalf("retention summary = %q", ret.Summary)
	}
}

func TestGrowthFunnelPurchasesNeedRealPurchaseMetric(t *testing.T) {
	purchases := func(g Growth) GrowthFunnelStage {
		for _, f := range g.Apps[0].Funnel {
			if f.ID == "purchases" {
				return f
			}
		}
		t.Fatal("no purchases stage")
		return GrowthFunnelStage{}
	}
	// units alone count free downloads: blind, value withheld.
	home := t.TempDir()
	writeGrowthSignals(t, home, growthDaily("product:alpha", "app_store_connect_units", 5, 0, 1)...)
	p := purchases(buildGrowthT(t, home, growthOpts()))
	if p.Status != GrowthBlind || p.Value != nil || p.Note != "Units are tracked, but purchases are not separated from free downloads." {
		t.Fatalf("purchases from units = %+v", p)
	}
	// IAP units are real purchases.
	home = t.TempDir()
	rows := growthDaily("product:alpha", "app_store_connect_units", 5, 0, 1)
	rows = append(rows, growthRow{product: "product:alpha", metric: "app_store_connect_iap_units", date: growthDay(1), value: 1})
	writeGrowthSignals(t, home, rows...)
	p = purchases(buildGrowthT(t, home, growthOpts()))
	if p.Status != GrowthMeasured || p.Value == nil || *p.Value != 1 || !strings.HasPrefix(p.Note, "1 purchase from ") || p.WindowEnd != growthDay(0) || p.DaysReported != 1 {
		t.Fatalf("purchases from iap units = %+v", p)
	}
}

func TestGrowthSingularCounts(t *testing.T) {
	for _, tc := range []struct {
		v         float64
		one, many string
		want      string
	}{{1, "unit", "units", "1 unit"}, {0, "unit", "units", "0 units"}, {2, "unit", "units", "2 units"}, {1, "open hypothesis", "open hypotheses", "1 open hypothesis"}} {
		if got := growthCount(tc.v, tc.one, tc.many); got != tc.want {
			t.Fatalf("got %q want %q", got, tc.want)
		}
	}
	home := t.TempDir()
	dir := filepath.Join(home, candidates.InboxDir(candidates.ProductHypothesis))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(HypothesisCandidate{ID: "hyp_1", ProductID: "product:alpha", ChangeEvent: "x", Metrics: []string{"gsc_clicks"}})
	if err := os.WriteFile(filepath.Join(dir, "hyp_1.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	writeGrowthSignals(t, home, growthRow{product: "product:alpha", metric: growthSalesFirstDownloads, date: growthDay(1), value: 1}, growthRow{product: "product:alpha", metric: "app_store_connect_developer_proceeds_usd", date: growthDay(1), value: 5})
	app := buildGrowthT(t, home, growthOpts()).Apps[0]
	if s := growthAudit(t, app, "analytics_experiment_design").Summary; !strings.Contains(s, "1 open hypothesis and 0 reviews") {
		t.Fatalf("experiments summary = %q", s)
	}
	if s := growthAudit(t, app, "monetization_pricing").Summary; !strings.Contains(s, "on 1 new download in the Sales report, 30 days") {
		t.Fatalf("pricing summary = %q", s)
	}
}

// Some apps promise users no third-party analytics, so blind text must never
// name or prescribe a vendor.
func TestGrowthBlindTextIsVendorNeutral(t *testing.T) {
	home := t.TempDir()
	writeGrowthSignals(t, home,
		growthRow{product: "product:alpha", metric: "app_store_connect_units", date: growthDay(1), value: 1},
		growthRow{product: "product:web_two", metric: "gsc_clicks", date: growthDay(1), value: 1})
	g := buildGrowthT(t, home, growthOpts())
	blind := 0
	for _, app := range g.Apps {
		for _, a := range app.Audit {
			if a.Status != GrowthBlind {
				continue
			}
			blind++
			for _, text := range []string{a.Summary, a.NextMove} {
				if strings.Contains(strings.ToLower(text), "posthog") {
					t.Fatalf("%s/%s names a vendor: %q", app.ProductID, a.AreaID, text)
				}
			}
			switch a.AreaID {
			case "activation_onboarding":
				if a.Summary != "Nothing measures onboarding today." || a.NextMove != growthPrivacyMove {
					t.Fatalf("onboarding = %+v", a)
				}
			case "acquisition_organic":
				if a.Summary != "No posted content is recorded for this app." || a.NextMove != "Record posts and their results so reach can be compared." {
					t.Fatalf("organic = %+v", a)
				}
			}
		}
	}
	if blind == 0 {
		t.Fatal("fixture should have blind areas")
	}
}

// applyGrowthBundle registers one synthetic product with the given profile,
// learned facts and essence.
func applyGrowthBundle(t *testing.T, home, id string, edit func(*Product)) {
	t.Helper()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	b := registryBundle(id, growthHumanize(id))
	b.Product.Surfaces = []Surface{{ID: "docs", Kind: "docs", Ref: "https://docs.example.invalid/" + strings.TrimPrefix(id, "product:")}}
	if edit != nil {
		edit(&b.Product)
	}
	if _, err := Apply(home, store, b, strings.TrimPrefix(id, "product:")+".yaml", tProduct); err != nil {
		t.Fatal(err)
	}
}

func TestGrowthProfileDeclaredRoundTripsThroughFacts(t *testing.T) {
	home := t.TempDir()
	applyGrowthBundle(t, home, "product:declared", func(p *Product) {
		p.Profile = ProductProfile{
			Kinds: []string{"web_app", "api"}, Audiences: []string{"developer"}, RevenueModels: []string{"usage"},
			Objectives: []string{"adoption"},
			Deferred:   []ProfileDeferral{{Area: "monetization", Reason: "free while in beta", Until: "2027-01-01"}},
		}
		p.Essence = Essence{Audience: "Teams that ship small services", Stage: "beta"}
	})
	app := growthApp(t, buildGrowthT(t, home, growthOpts()), "product:declared")
	p := app.Profile
	if strings.Join(p.Kinds.Values, ",") != "web_app,api" || p.Kinds.Source != GrowthProfileDeclared || app.Kind != "web_app" {
		t.Fatalf("kinds = %+v, kind = %s", p.Kinds, app.Kind)
	}
	if p.Audiences.Source != GrowthProfileDeclared || p.RevenueModels.Values[0] != "usage" || p.Objectives.Values[0] != "adoption" {
		t.Fatalf("profile = %+v", p)
	}
	if p.Stage.Source != GrowthProfileDeclared || p.Stage.Values[0] != "beta" || len(p.Unknown) != 0 {
		t.Fatalf("stage = %+v unknown = %v", p.Stage, p.Unknown)
	}
	if len(p.Deferred) != 1 || p.Deferred[0].Area != "monetization" || p.Deferred[0].Until != "2027-01-01" {
		t.Fatalf("deferred = %+v", p.Deferred)
	}
	// Store-only funnel stages do not apply to a product that is not sold in a store.
	for _, f := range app.Funnel {
		if f.ID == "impressions" && f.Status != GrowthNotApplicable {
			t.Fatalf("impressions stage for a web app = %+v", f)
		}
	}
}

func TestGrowthProfileRejectsUndeclaredValues(t *testing.T) {
	for name, prof := range map[string]ProductProfile{
		"kind":     {Kinds: []string{"smart_fridge"}},
		"audience": {Audiences: []string{"everyone"}},
		"deferral": {Deferred: []ProfileDeferral{{Area: "Monetization!", Reason: "later"}}},
		"until":    {Deferred: []ProfileDeferral{{Area: "monetization", Reason: "later", Until: "soon"}}},
		"reason":   {Deferred: []ProfileDeferral{{Area: "monetization"}}},
	} {
		b := registryBundle("product:bad", "Bad")
		b.Product.Profile = prof
		if err := Validate(b); err == nil {
			t.Fatalf("%s: Validate accepted %+v", name, prof)
		}
	}
	b := registryBundle("product:bad", "Bad")
	b.Product.Learned = []LearnedFact{{At: growthAsOf, Fact: "x", EvidenceRef: "fixture:x", Area: "Not An Id"}}
	if err := Validate(b); err == nil {
		t.Fatal("a learned area that is not an id must fail")
	}
}

// A set-aside entry must name a catalog area or group; a typo would
// otherwise be ignored without a word. The error suggests the nearest id.
func TestGrowthProfileRejectsUnknownDeferral(t *testing.T) {
	for _, tc := range []struct{ area, near string }{
		{"monetisation", "monetization"},
		{"acquisition_payd", "acquisition_paid"},
	} {
		b := registryBundle("product:bad", "Bad")
		b.Product.Profile = ProductProfile{Deferred: []ProfileDeferral{{Area: tc.area, Reason: "later"}}}
		err := Validate(b)
		if err == nil || !strings.Contains(err.Error(), `"`+tc.area+`"`) || !strings.Contains(err.Error(), "did you mean "+tc.near) {
			t.Fatalf("deferral %q: err = %v, want a suggestion of %s", tc.area, err, tc.near)
		}
	}
	for _, area := range []string{"monetization", "acquisition_paid"} {
		b := registryBundle("product:good", "Good")
		b.Product.Profile = ProductProfile{Deferred: []ProfileDeferral{{Area: area, Reason: "later"}}}
		if err := Validate(b); err != nil {
			t.Fatalf("deferral %q: %v", area, err)
		}
	}
	// A contract without a profile imports exactly as before.
	if err := Validate(registryBundle("product:plain", "Plain")); err != nil {
		t.Fatal(err)
	}
}

func TestGrowthProfileInference(t *testing.T) {
	home := t.TempDir()
	rows := growthDaily("product:both", "app_store_connect_developer_proceeds_usd", 3, 0, 2)
	rows = append(rows, growthDaily("product:both", "gsc_clicks", 3, 0, 5)...)
	rows = append(rows, growthDaily("product:both", "revenuecat_active_subscriptions", 3, 0, 9)...)
	rows = append(rows, growthDaily("product:quiet", "posthog_sessions", 3, 0, 1)...)
	writeGrowthSignals(t, home, rows...)
	g := buildGrowthT(t, home, growthOpts())
	both := growthApp(t, g, "product:both").Profile
	if strings.Join(both.Kinds.Values, ",") != "ios_app,website" || both.Kinds.Source != GrowthProfileInferred {
		t.Fatalf("kinds = %+v", both.Kinds)
	}
	if strings.Join(both.RevenueModels.Values, ",") != "subscription" || both.Objectives.Values[0] != "revenue" || both.Objectives.Source != GrowthProfileInferred {
		t.Fatalf("revenue = %+v objectives = %+v", both.RevenueModels, both.Objectives)
	}
	if both.Audiences.Source != GrowthProfileUnknown || strings.Join(both.Unknown, ",") != "audiences,stage" {
		t.Fatalf("audiences must never be inferred: %+v unknown %v", both.Audiences, both.Unknown)
	}
	quiet := growthApp(t, g, "product:quiet")
	if quiet.Kind != "unknown" || quiet.Profile.Kinds.Source != GrowthProfileUnknown || len(quiet.Profile.Kinds.Values) != 0 {
		t.Fatalf("quiet profile = %+v", quiet.Profile)
	}
	if strings.Join(quiet.Profile.Unknown, ",") != "kinds,audiences,revenue_models,objectives,stage" {
		t.Fatalf("unknown = %v", quiet.Profile.Unknown)
	}
	if len(g.ProfileDims["kinds"]) == 0 || len(g.ProfileDims["objectives"]) == 0 {
		t.Fatalf("profile_dimensions = %v", g.ProfileDims)
	}
}

func TestGrowthApplicabilityFromProfile(t *testing.T) {
	home := t.TempDir()
	applyGrowthBundle(t, home, "product:adopt", func(p *Product) {
		p.Profile = ProductProfile{Kinds: []string{"web_app"}, Objectives: []string{"adoption"}}
	})
	applyGrowthBundle(t, home, "product:silent", nil)
	applyGrowthBundle(t, home, "product:paused", func(p *Product) {
		p.Profile = ProductProfile{Kinds: []string{"web_app"}, Objectives: []string{"revenue"}, Deferred: []ProfileDeferral{
			{Area: "monetization", Reason: "free during the beta"},
			{Area: "brand_trust", Reason: "too early", Until: growthDay(-30)},
			{Area: "acquisition_paid", Reason: "no budget this quarter", Until: growthDay(30)},
		}}
	})
	g := buildGrowthT(t, home, growthOpts())
	cat, _ := loadEmbeddedGrowthCatalog()
	adopt, silent, paused := growthApp(t, g, "product:adopt"), growthApp(t, g, "product:silent"), growthApp(t, g, "product:paused")
	monetization := 0
	for _, area := range cat.Areas {
		if area.Group != "monetization" {
			continue
		}
		monetization++
		if a := growthAudit(t, adopt, area.ID); a.Status != GrowthNotApplicable || !strings.Contains(a.Reason, "objectives") || !strings.Contains(a.Reason, "adoption") {
			t.Fatalf("%s for an adoption product = %+v", area.ID, a)
		}
		if a := growthAudit(t, silent, area.ID); a.Status != GrowthUnknown || !strings.Contains(a.Reason, "objectives") {
			t.Fatalf("%s with no profile and no data = %+v", area.ID, a)
		}
		if a := growthAudit(t, paused, area.ID); a.Status != GrowthDeferred || !strings.Contains(a.Reason, "free during the beta") {
			t.Fatalf("%s deferred by group = %+v", area.ID, a)
		}
	}
	if monetization == 0 {
		t.Fatal("catalog has no monetization areas")
	}
	if a := growthAudit(t, paused, "brand_trust"); a.Status != GrowthDeferred || !strings.Contains(a.Reason, "Until "+growthDay(-30)) {
		t.Fatalf("brand_trust deferral = %+v", a)
	}
	if a := growthAudit(t, paused, "acquisition_paid"); a.Status == GrowthDeferred {
		t.Fatalf("a deferral whose date passed must lapse: %+v", a)
	}
	if adopt.Coverage.NotApplicable != monetization || silent.Coverage.Unknown != monetization || paused.Coverage.Deferred != monetization+1 {
		t.Fatalf("coverage adopt %+v silent %+v paused %+v", adopt.Coverage, silent.Coverage, paused.Coverage)
	}
}

func TestGrowthKindFormsAcrossKinds(t *testing.T) {
	home := t.TempDir()
	rows := growthDaily("product:phone", "app_store_connect_impressions", 6, 0, 40)
	rows = append(rows, growthDaily("product:site", "gsc_clicks", 6, 0, 3)...)
	rows = append(rows, growthDaily("product:both", "app_store_connect_units", 6, 0, 1)...)
	rows = append(rows, growthDaily("product:both", "gsc_clicks", 6, 0, 2)...)
	writeGrowthSignals(t, home, rows...)
	g := buildGrowthT(t, home, growthOpts())

	phone := growthAudit(t, growthApp(t, g, "product:phone"), "acquisition_search")
	if phone.Title != "App Store search" || strings.Join(phone.Forms, ",") != "App Store search" || phone.Status != GrowthMeasured || phone.Basis != GrowthBasisMetric {
		t.Fatalf("app = %+v", phone)
	}
	if strings.Join(phone.Skills, ",") != "aso" || phone.Branch != "distribution" {
		t.Fatalf("app form skills and branch = %+v", phone)
	}
	site := growthAudit(t, growthApp(t, g, "product:site"), "acquisition_search")
	if site.Title != "Search engines" || site.Status != GrowthMeasured || len(site.Evidence) == 0 || !strings.HasPrefix(site.Evidence[0].Metric, "gsc_") {
		t.Fatalf("website = %+v", site)
	}
	both := growthAudit(t, growthApp(t, g, "product:both"), "acquisition_search")
	if both.Status != GrowthPartial || len(both.Forms) != 2 || both.Title != "Search visibility" {
		t.Fatalf("both kinds = %+v", both)
	}
	if !strings.Contains(both.Summary, "Nothing measures App Store search yet.") || !strings.Contains(both.NextMove, "App Store Connect analytics") {
		t.Fatalf("partial must name the missing form: %+v", both)
	}
}

const growthRecordAreas = `areas:
  - id: alpha_audience
    group: alpha
    title: Audience
    question: "Do we know who it is for?"
    measure: the audience
    evidence: {record: [essence.audience]}
  - id: beta_assessed
    group: beta
    title: Assessed
    question: "Has anyone assessed it?"
    measure: the assessed thing
    needs: [alpha_audience]
    skills: [customer-research]
`

func TestGrowthRecordAndAssessmentEvidence(t *testing.T) {
	cat, err := buildTestGrowthCatalog(t, growthTestCore, growthRecordAreas)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	applyGrowthBundle(t, home, "product:known", func(p *Product) {
		p.Essence = Essence{Audience: "Night-shift nurses who chart by hand"}
		p.Learned = []LearnedFact{{At: growthAsOf.AddDate(0, 0, -10), Fact: "Five of six interviews named charting time first.", EvidenceRef: "fixture:interviews", Area: "beta_assessed"}}
	})
	applyGrowthBundle(t, home, "product:old", func(p *Product) {
		p.Learned = []LearnedFact{{At: growthAsOf.AddDate(0, 0, -200), Fact: "Checked once.", EvidenceRef: "fixture:old", Area: "beta_assessed"}}
	})
	applyGrowthBundle(t, home, "product:blank", nil)
	o := growthOpts()
	o.catalog = cat
	g := buildGrowthT(t, home, o)

	known := growthApp(t, g, "product:known")
	if a := growthAudit(t, known, "alpha_audience"); a.Status != GrowthMeasured || a.Basis != GrowthBasisRecord || !strings.HasPrefix(a.Summary, "On file: Night-shift nurses") {
		t.Fatalf("record on file = %+v", a)
	}
	if a := growthAudit(t, known, "beta_assessed"); a.Status != GrowthMeasured || a.Basis != GrowthBasisAssessment || !strings.HasPrefix(a.Summary, "Assessed on "+growthDay(10)) {
		t.Fatalf("fresh assessment = %+v", a)
	}
	if a := growthAudit(t, growthApp(t, g, "product:old"), "beta_assessed"); a.Status != GrowthStale || !strings.Contains(a.Summary, growthDay(200)) {
		t.Fatalf("old assessment = %+v", a)
	}
	blank := growthApp(t, g, "product:blank")
	if a := growthAudit(t, blank, "alpha_audience"); a.Status != GrowthBlind || a.NextMove != "Write it in the product contract." || a.Basis != GrowthBasisNone {
		t.Fatalf("record absent = %+v", a)
	}
	b := growthAudit(t, blank, "beta_assessed")
	if b.Status != GrowthBlind || b.NextMove != "Run customer-research and record what it finds." || strings.Join(b.BlockedBy, ",") != "alpha_audience" {
		t.Fatalf("unassessed = %+v", b)
	}
	if a := growthAudit(t, known, "beta_assessed"); len(a.BlockedBy) != 0 {
		t.Fatalf("a need that is on file must not block: %+v", a)
	}
}

// growthTwelveGroupAreas gives every embedded group two areas: one free, one
// that needs a single root area in the last group.
func growthTwelveGroupAreas(t *testing.T) string {
	t.Helper()
	cat, err := loadEmbeddedGrowthCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("areas:\n")
	row := func(id, group, extra string) {
		fmt.Fprintf(&b, "  - {id: %s, group: %s, title: %s, question: \"%s?\", measure: %s, skills: [cro]%s}\n", id, group, id, id, id, extra)
	}
	for _, g := range cat.Groups {
		if g.ID == "operations" {
			row("operations_root", g.ID, "")
		} else if g.ID == "analytics" {
			row("analytics_one", g.ID, ", evidence: {metrics: [gsc_clicks], source: Search Console}")
		} else {
			row(g.ID+"_one", g.ID, "")
		}
		row(g.ID+"_two", g.ID, ", needs: [operations_root]")
	}
	return b.String()
}

func TestGrowthNextMovesRanking(t *testing.T) {
	raw, err := growthCatalogFS.ReadFile("growth_catalog/00-core.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := buildTestGrowthCatalog(t, string(raw), growthTwelveGroupAreas(t))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	writeGrowthSignals(t, home, growthDaily("product:alpha", "gsc_clicks", 40, 30, 4)...)
	o := growthOpts()
	o.catalog = cat
	app := buildGrowthT(t, home, o).Apps[0]
	moves := app.NextMoves
	if len(moves) != growthMaxMoves {
		t.Fatalf("moves = %+v", moves)
	}
	if moves[0].Kind != "refresh_data" || moves[0].AreaID != "analytics_one" {
		t.Fatalf("stale data must come first: %+v", moves[0])
	}
	if moves[1].AreaID != "operations_root" || moves[1].Unblocks != 12 || moves[1].Kind != "assess" || moves[1].Title != "Assess: operations_root" {
		t.Fatalf("the area most others need must come next, even from the last group: %+v", moves[1])
	}
	groups := map[string]bool{}
	for _, m := range moves {
		area, _ := cat.area(m.AreaID)
		groups[area.Group] = true
		if strings.HasSuffix(m.AreaID, "_two") {
			t.Fatalf("a blocked area came before what it needs: %+v", moves)
		}
		if m.Branch != cat.branch(area) || strings.Join(m.Skills, ",") != "cro" || m.Brief != growthBrief("product:alpha", m.AreaID) {
			t.Fatalf("move must carry the catalog branch, skills and brief: %+v", m)
		}
	}
	if len(groups) < 3 {
		t.Fatalf("picks come from too few groups: %v", groups)
	}
}

func TestGrowthNextMovesBlockedAfterNeedsAndSetProfile(t *testing.T) {
	cat, err := buildTestGrowthCatalog(t, growthTestCore, growthRecordAreas)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	applyGrowthBundle(t, home, "product:blank", nil)
	o := growthOpts()
	o.catalog = cat
	moves := buildGrowthT(t, home, o).Apps[0].NextMoves
	if len(moves) != 2 || moves[0].AreaID != "alpha_audience" || moves[0].Kind != "record_answer" || moves[0].Title != "Record the answer: Audience" {
		t.Fatalf("moves = %+v", moves)
	}
	if moves[1].AreaID != "beta_assessed" || !strings.HasPrefix(moves[1].Why, "Needs Audience first.") || strings.Join(moves[1].BlockedBy, ",") != "alpha_audience" {
		t.Fatalf("blocked move must follow its need and name it: %+v", moves[1])
	}

	// No profile and no data: monetization is unknown and the profile move exists.
	home = t.TempDir()
	applyGrowthBundle(t, home, "product:silent", nil)
	app := buildGrowthT(t, home, growthOpts()).Apps[0]
	var set *GrowthNextMove
	for i := range app.NextMoves {
		if app.NextMoves[i].Kind == "set_profile" {
			set = &app.NextMoves[i]
		}
	}
	if set == nil || set.AreaID != "" || !strings.Contains(set.Why, "objectives") || !strings.Contains(set.Brief, "compound product import") {
		t.Fatalf("set_profile move = %+v in %+v", set, app.NextMoves)
	}
}

func TestGrowthAreaBrief(t *testing.T) {
	home := t.TempDir()
	writeGrowthSignals(t, home, growthDaily("product:alpha", "app_store_connect_impressions", 5, 0, 10)...)
	o := growthOpts()
	o.ProductID, o.Area = "alpha", "activation_onboarding"
	b, err := BuildGrowthArea(home, o)
	if err != nil {
		t.Fatal(err)
	}
	if b.SchemaVersion != GrowthAreaSchemaVersion || b.ProductID != "product:alpha" || b.Area.ID != "activation_onboarding" || b.Area.Branch != "activation" {
		t.Fatalf("brief = %+v", b)
	}
	if b.Audit.Status != GrowthBlind || strings.Join(b.Audit.BlockedBy, ",") != "experience_journeys" || strings.Join(b.Area.Skills, ",") != "onboarding" {
		t.Fatalf("audit = %+v area = %+v", b.Audit, b.Area)
	}
	if len(b.Needs) != 1 || b.Needs[0].AreaID != "experience_journeys" || b.Needs[0].Status != GrowthBlind || b.Needs[0].Title == "" {
		t.Fatalf("needs = %+v", b.Needs)
	}
	if !strings.Contains(b.Record, "area: activation_onboarding") || !strings.Contains(b.Record, "compound product import") {
		t.Fatalf("record = %q", b.Record)
	}
	var text strings.Builder
	RenderGrowthAreaText(&text, b)
	if lines := strings.Count(text.String(), "\n"); lines < 6 || lines > 12 || !strings.Contains(text.String(), "needs first: Main tasks (blind)") {
		t.Fatalf("text brief (%d lines):\n%s", lines, text.String())
	}

	o.Area = "experience_journeys"
	j, err := BuildGrowthArea(home, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Unblocks) < 3 || j.Unblocks[0].AreaID != "activation_onboarding" {
		t.Fatalf("unblocks = %+v", j.Unblocks)
	}

	var usage *GrowthUsageError
	o.Area = "onbording"
	if _, err := BuildGrowthArea(home, o); !errors.As(err, &usage) || !strings.Contains(err.Error(), "activation_onboarding") {
		t.Fatalf("unknown area must be a usage error with a suggestion: %v", err)
	}
	o.ProductID, o.Area = "", "activation_onboarding"
	if _, err := BuildGrowthArea(home, o); !errors.As(err, &usage) {
		t.Fatalf("--area without --product must be a usage error: %v", err)
	}
}

// The text brief names at most four of the areas an area unblocks, then
// counts the rest.
func TestGrowthAreaTextCapsUnblocks(t *testing.T) {
	refs := func(n int) []GrowthAreaRef {
		out := []GrowthAreaRef{}
		for i := 1; i <= n; i++ {
			out = append(out, GrowthAreaRef{AreaID: fmt.Sprintf("alpha_%d", i), Title: fmt.Sprintf("Area %d", i), Status: GrowthBlind})
		}
		return out
	}
	line := func(n int) string {
		var text strings.Builder
		RenderGrowthAreaText(&text, GrowthAreaBrief{Unblocks: refs(n)})
		for _, l := range strings.Split(text.String(), "\n") {
			if strings.HasPrefix(l, "unblocks: ") {
				return l
			}
		}
		return ""
	}
	four := "unblocks: Area 1 (blind), Area 2 (blind), Area 3 (blind), Area 4 (blind)"
	if got := line(13); got != four+" and 9 more" {
		t.Fatalf("13 areas: %q", got)
	}
	if got := line(5); got != four+" and 1 more" {
		t.Fatalf("5 areas: %q", got)
	}
	if got := line(4); got != four {
		t.Fatalf("4 areas: %q", got)
	}
}
