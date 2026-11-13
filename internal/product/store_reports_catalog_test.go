package product

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shipped catalog loads, and every report family the store publishes is
// listed, including the ones nobody captures yet, so "not captured" shows.
func TestStoreReportCatalogEmbeddedListsCapturedAndUncaptured(t *testing.T) {
	cat, err := loadEmbeddedStoreReportCatalog()
	if err != nil {
		t.Fatal(err)
	}
	families := map[string]bool{}
	for _, r := range cat.Reports {
		families[r.Family] = true
	}
	for _, f := range storeReportFamilies {
		if !families[f] {
			t.Fatalf("family %s has no report", f)
		}
	}
	captured := map[string]bool{
		"asc_sales_summary_daily": true, "asc_finance_financial_monthly": true,
		"asc_analytics_discovery_engagement": true, "asc_analytics_downloads": true,
	}
	for id := range captured {
		r, ok := cat.report(id)
		if !ok || r.Capture.None() {
			t.Fatalf("%s must be listed with a capture: %+v", id, r)
		}
	}
	for _, id := range []string{
		"asc_subscription_daily", "asc_subscription_event_daily", "asc_subscriber_daily", "asc_finance_detail_monthly",
		"asc_pre_order_daily", "asc_offer_code_redemption_daily", "asc_win_back_eligibility_daily",
		"asc_analytics_sessions", "asc_analytics_installs_deletions", "asc_customer_reviews", "asc_ratings_summary",
	} {
		r, ok := cat.report(id)
		if !ok || !r.Capture.None() {
			t.Fatalf("%s must be listed as not captured: %+v", id, r)
		}
	}
	sales, _ := cat.report("asc_sales_summary_daily")
	if sales.Capture.Series == nil || sales.Capture.Series.DayScope != "account" || sales.Capture.Files == nil || sales.Exactness != "exact" {
		t.Fatalf("sales row = %+v", sales)
	}
	info := cat.info()
	if info.Reports != len(cat.Reports) || info.Captured < 4 || info.OverlayError != "" || info.SchemaVersion != StoreReportCatalogSchemaVersion {
		t.Fatalf("info = %+v", info)
	}
}

const storeReportTestRow = `
  - id: example_report
    provider: example_store
    family: sales
    title: Example
    question: What does it answer?
    grain: day
    published_after: {text: next day, lag_days: 1}
    kept_for: 1 year
    exactness: exact
    exactness_note: exact rows
    capture:
      series: {path: private/example/series.jsonl, app_field: app, day_field: date, day_scope: account}
    feeds: {kpis: [units], areas: [monetization_pricing]}
`

func buildStoreReportCatalogT(t *testing.T, rows string) (*storeReportCatalog, error) {
	t.Helper()
	growthCat, err := loadEmbeddedGrowthCatalog()
	if err != nil {
		t.Fatal(err)
	}
	f, err := parseStoreReportCatalogFile([]byte("schema_version: " + StoreReportCatalogSchemaVersion + "\nreports:" + rows))
	if err != nil {
		return nil, err
	}
	return buildStoreReportCatalog([]storeReportCatalogFile{f}, growthCat)
}

func TestStoreReportCatalogValidation(t *testing.T) {
	if _, err := buildStoreReportCatalogT(t, storeReportTestRow); err != nil {
		t.Fatalf("valid row rejected: %v", err)
	}
	for name, tc := range map[string]struct{ from, to, want string }{
		"family":         {"family: sales", "family: gossip", "family"},
		"grain":          {"grain: day", "grain: hour", "grain"},
		"exactness":      {"exactness: exact", "exactness: roughly", "exactness"},
		"question":       {"question: What does it answer?", "question: ''", "question"},
		"absolute path":  {"path: private/example/series.jsonl", "path: /abs/series.jsonl", "relative"},
		"escaping path":  {"path: private/example/series.jsonl", "path: private/../../series.jsonl", "inside"},
		"day scope":      {"day_scope: account", "day_scope: planet", "day_scope"},
		"unknown kpi":    {"kpis: [units]", "kpis: [vibes]", "KPI"},
		"unknown area":   {"areas: [monetization_pricing]", "areas: [nowhere]", "growth area"},
		"negative lag":   {"lag_days: 1", "lag_days: -1", "lag_days"},
		"empty capture":  {"series: {path: private/example/series.jsonl, app_field: app, day_field: date, day_scope: account}", "{}", "empty"},
		"unknown key":    {"series: {path: private/example/series.jsonl, app_field: app, day_field: date, day_scope: account}", "ftp: {host: x}", "unknown key"},
		"no group":       {"series: {path: private/example/series.jsonl, app_field: app, day_field: date, day_scope: account}", "files: {globs: [private/x/*.tsv], period_pattern: 'x\\.tsv'}", "one group"},
		"capture string": {"capture:\n      series: {path: private/example/series.jsonl, app_field: app, day_field: date, day_scope: account}", "capture: sometimes", "none"},
	} {
		rows := strings.Replace(storeReportTestRow, tc.from, tc.to, 1)
		if rows == storeReportTestRow {
			t.Fatalf("%s: replacement did not apply", name)
		}
		_, err := buildStoreReportCatalogT(t, rows)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
	if _, err := buildStoreReportCatalogT(t, storeReportTestRow+storeReportTestRow); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("duplicate id: %v", err)
	}
	cat, err := buildStoreReportCatalogT(t, strings.Replace(storeReportTestRow, "capture:\n      series: {path: private/example/series.jsonl, app_field: app, day_field: date, day_scope: account}", "capture: none", 1))
	if err != nil || !cat.Reports[0].Capture.None() {
		t.Fatalf("capture none: %v %+v", err, cat)
	}
}

func writeStoreReportOverlay(t *testing.T, home, body string) {
	t.Helper()
	p := filepath.Join(home, filepath.FromSlash(StoreReportCatalogOverlayPath))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// An overlay adds a second store's report and changes one field of a shipped
// row; a broken overlay serves the shipped catalog and says why.
func TestStoreReportCatalogOverlayAddsAndBrokenOverlayFallsBack(t *testing.T) {
	home := t.TempDir()
	embedded, err := loadEmbeddedStoreReportCatalog()
	if err != nil {
		t.Fatal(err)
	}
	writeStoreReportOverlay(t, home, "reports:"+storeReportTestRow+`
  - id: asc_ratings_summary
    kept_for: Captured daily by the operator
`)
	cat, err := loadStoreReportCatalog(home)
	if err != nil {
		t.Fatal(err)
	}
	if cat.Overlay != StoreReportCatalogOverlayPath || cat.OverlayError != "" || len(cat.Reports) != len(embedded.Reports)+1 {
		t.Fatalf("overlay = %q err %q, reports %d (embedded %d)", cat.Overlay, cat.OverlayError, len(cat.Reports), len(embedded.Reports))
	}
	added, ok := cat.report("example_report")
	if !ok || added.Provider != "example_store" {
		t.Fatalf("added = %+v", added)
	}
	ratings, _ := cat.report("asc_ratings_summary")
	if ratings.KeptFor != "Captured daily by the operator" || ratings.Title != "Ratings summary" || !ratings.Capture.None() {
		t.Fatalf("overridden row = %+v", ratings)
	}

	writeStoreReportOverlay(t, home, "reports:\n  - id: asc_ratings_summary\n    family: gossip\n")
	cat, err = loadStoreReportCatalog(home)
	if err != nil {
		t.Fatal(err)
	}
	if cat.Overlay != "" || !strings.Contains(cat.OverlayError, StoreReportCatalogOverlayPath) || !strings.Contains(cat.OverlayError, "family") || len(cat.Reports) != len(embedded.Reports) {
		t.Fatalf("broken overlay: overlay %q err %q reports %d", cat.Overlay, cat.OverlayError, len(cat.Reports))
	}
	if r, _ := cat.report("asc_ratings_summary"); r.Family != "reviews" {
		t.Fatalf("broken overlay leaked into the served catalog: %+v", r)
	}
	writeStoreReportOverlay(t, home, "reports: [unclosed")
	if cat, err = loadStoreReportCatalog(home); err != nil || cat.OverlayError == "" {
		t.Fatalf("unparsable overlay: %v %q", err, cat.OverlayError)
	}
}
