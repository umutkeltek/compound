package product

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// A synthetic store account, as-of growthAsOf (2026-11-12):
//   - app 900000001 (product:example-app, sku EXAMPLE_SKU) has one update
//     every day from offset 74 to offset 1, except offset 20 (never
//     captured: a gap) and offset 30 (Apple answered "no sales");
//   - its in-app purchase 910000001 sells on 08-30, 09-25, 09-26 and 10-10;
//     the series files the 09-25 sale under the purchase's own id;
//   - app 900000002 (product:second-app) has free first downloads on offsets
//     6..4 and a paid download in EUR on 10-05;
//   - app 900000004 sells with no product; 900000005 is claimed by two
//     products; the private map names 900000003 for an unregistered product.
type fxSale struct {
	day, appleID, parent, sku, ptype, currency string
	units, proceeds                            float64
}

const (
	fxApp1, fxApp2, fxIAP, fxOrphan = "900000001", "900000002", "910000001", "900000004"
	fxSalesRun                      = "private/metrics/raw/asc-sales-backfill/2026-11-12"
)

func fxSales() []fxSale {
	var out []fxSale
	for off := 74; off >= 1; off-- {
		if off == 20 || off == 30 {
			continue
		}
		out = append(out, fxSale{day: growthDay(off), appleID: fxApp1, sku: "EXAMPLE_SKU", ptype: "7", currency: "USD", units: 1})
	}
	for _, d := range []string{"2026-08-30", "2026-09-25", "2026-09-26", "2026-10-10"} {
		out = append(out, fxSale{day: d, appleID: fxIAP, parent: "EXAMPLE_SKU", sku: "EXAMPLE_IAP", ptype: "IA1", currency: "USD", units: 1, proceeds: 0.70})
	}
	for _, off := range []int{10, 9, 8, 7} {
		out = append(out, fxSale{day: growthDay(off), appleID: fxApp1, sku: "EXAMPLE_SKU", ptype: "1", currency: "USD", units: 1})
	}
	for _, off := range []int{6, 5, 4} {
		out = append(out, fxSale{day: growthDay(off), appleID: fxApp2, sku: "SECOND_SKU", ptype: "1", currency: "USD", units: 1})
	}
	out = append(out,
		fxSale{day: "2026-10-05", appleID: fxApp2, sku: "SECOND_SKU", ptype: "1", currency: "EUR", units: 1, proceeds: 0.60},
		fxSale{day: growthDay(15), appleID: fxOrphan, sku: "ORPHAN_SKU", ptype: "1", currency: "USD", units: 2},
	)
	return out
}

func writeFxRawSales(t *testing.T, home string, sales []fxSale) {
	t.Helper()
	header := "Provider\tSKU\tTitle\tProduct Type Identifier\tUnits\tDeveloper Proceeds\tBegin Date\tCurrency of Proceeds\tApple Identifier\tParent Identifier"
	byDay := map[string][]string{}
	for _, s := range sales {
		d, _ := parseGrowthDay(s.day)
		byDay[s.day] = append(byDay[s.day], strings.Join([]string{"APPLE", s.sku, "Example App", s.ptype, fmt.Sprint(s.units), fmt.Sprintf("%.2f", s.proceeds), d.Format("01/02/2006"), s.currency, s.appleID, s.parent}, "\t"))
	}
	for day, rows := range byDay {
		writeHomeFile(t, home, filepath.FromSlash(fxSalesRun+"/sales-"+day+".tsv"), "\xef\xbb\xbf"+header+"\n"+strings.Join(rows, "\n")+"\n")
	}
	writeHomeFile(t, home, filepath.FromSlash(fxSalesRun+"/sales-"+growthDay(30)+".no-sales.json"), `{"date":"`+growthDay(30)+`","status":"no_sales"}`)
}

// fxSeriesFromRaw builds the series a correct collector writes, then files
// the 09-25 in-app sale under the purchase's own id.
func fxSeriesFromRaw(sales []fxSale) []string {
	type cell struct {
		kinds    map[string]float64
		units    float64
		proceeds map[string]float64
	}
	cells := map[string]*cell{}
	for _, s := range sales {
		app := s.appleID
		if s.parent == "EXAMPLE_SKU" && s.day != "2026-09-25" {
			app = fxApp1
		}
		key := s.day + "|" + app
		c := cells[key]
		if c == nil {
			c = &cell{kinds: map[string]float64{}, proceeds: map[string]float64{}}
			cells[key] = c
		}
		c.units += s.units
		c.kinds[salesKind(s.ptype)] += s.units
		if s.proceeds != 0 {
			c.proceeds[s.currency] += s.proceeds * s.units
		}
	}
	var lines []string
	for _, key := range growthSortedKeys(cells) {
		day, app, _ := strings.Cut(key, "|")
		c := cells[key]
		t, _ := parseGrowthDay(day)
		row := map[string]any{
			"date": day, "apple_identifier": app, "title": "Example App", "units": c.units,
			"app_units": c.units - c.kinds["iap_units"], "developer_proceeds_usd": c.proceeds["USD"],
			"proceeds_by_currency": c.proceeds, "final": growthAsOf.Sub(t) >= 72*time.Hour,
		}
		for _, k := range salesKinds {
			row[k] = c.kinds[k]
		}
		b, _ := json.Marshal(row)
		lines = append(lines, string(b))
	}
	return lines
}

func writeFxFinance(t *testing.T, home, label string, rows ...[]string) {
	t.Helper()
	header := "Start Date\tEnd Date\tUPC\tQuantity\tPartner Share\tExtended Partner Share\tPartner Share Currency\tSales or Return\tApple Identifier\tProduct Type Identifier"
	lines := []string{header}
	for _, r := range rows {
		lines = append(lines, strings.Join(r, "\t"))
	}
	lines = append(lines, "", fmt.Sprintf("Total_Rows\t%d", len(rows)), "Country Of Sale\tPartner Share Currency\tQuantity\tExtended Partner Share")
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(strings.Join(lines, "\n") + "\n"))
	zw.Close()
	writeHomeFile(t, home, filepath.FromSlash("private/metrics/raw/asc-finance/2026-11-12/financial-"+label+".txt.gz"), buf.String())
}

func storeFixtureHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	applyGrowthBundle(t, home, "product:example-app", func(p *Product) { p.Profile = ProductProfile{Kinds: []string{"ios_app"}} })
	applyGrowthBundle(t, home, "product:second-app", func(p *Product) {
		p.Profile = ProductProfile{Kinds: []string{"ios_app"}}
		p.ReleaseIdentity.AppStoreConnectAppID = fxApp2
	})
	applyGrowthBundle(t, home, "product:waiting-app", func(p *Product) { p.Profile = ProductProfile{Kinds: []string{"ios_app"}} })
	writeASCLedger(t, home,
		ascLedgerSignal("sig_1", "product:example-app", fxApp1, "app_store_connect_review_count", growthDay(200), 1),
		ascLedgerSignal("sig_2", "product:second-app", fxApp2, "app_store_connect_review_count", growthDay(200), 1),
		ascLedgerSignal("sig_3", "product:example-app", "900000005", "app_store_connect_review_count", growthDay(201), 1),
		ascLedgerSignal("sig_4", "product:second-app", "900000005", "app_store_connect_review_count", growthDay(201), 1),
	)
	writeHomeFile(t, home, filepath.FromSlash(StoreAppsMapRel), `{"apps":[{"asc_app_id":900000001,"product_id":"product:example-app"},{"asc_app_id":"900000003","product_id":"product:not-registered"}]}`)
	sales := fxSales()
	writeFxRawSales(t, home, sales)
	writeASCSeries(t, home, fxSeriesFromRaw(sales)...)
	writeASCDaysIndex(t, home, `{"date":"`+growthDay(30)+`","status":"no_sales","final":true}`)
	// Finance: 2026-12 lacks the 09-26 sale, which lands in 2027-01 (a
	// carry-over); 2027-01 claims three EUR downloads where Sales has one.
	writeFxFinance(t, home, "2026-12",
		[]string{"08/30/2026", "09/26/2026", "", "2", "0.70", "1.40", "USD", "S", fxIAP, "IA1"})
	writeFxFinance(t, home, "2027-01",
		[]string{"09/27/2026", "10/31/2026", "", "2", "0.70", "1.40", "USD", "S", fxIAP, "IA1"},
		[]string{"09/27/2026", "10/31/2026", "", "3", "0.60", "1.80", "EUR", "S", fxApp2, "1"})
	// Analytics: app 1 shows 10 first-time downloads over offsets 10..6
	// where Sales has 4; app 2 agrees with Sales; app 2's discovery cells
	// stop at offset 35.
	var funnel []string
	for off := 50; off >= 4; off-- {
		cells := map[string]float64{"impressions": 30}
		if off <= 10 && off >= 6 {
			cells["first_time_downloads"] = 2
		}
		funnel = append(funnel, ascFunnelLine(fxApp1, growthDay(off), cells, nil))
	}
	for off := 40; off >= 35; off-- {
		funnel = append(funnel, ascFunnelLine(fxApp2, growthDay(off), map[string]float64{"impressions": 5}, nil))
	}
	for off := 6; off >= 4; off-- {
		funnel = append(funnel, ascFunnelLine(fxApp2, growthDay(off), map[string]float64{"first_time_downloads": 1}, nil))
	}
	writeASCFunnelSeries(t, home, funnel...)
	return home
}

func storeOpts(check bool) StoreReportsOptions {
	return StoreReportsOptions{Now: growthAsOf.Add(9 * time.Hour), AsOf: growthAsOf, Check: check}
}

func buildStoreReportsT(t *testing.T, home string, opts StoreReportsOptions) StoreReports {
	t.Helper()
	m, err := BuildStoreReports(home, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	return m
}

func storeApp(t *testing.T, m StoreReports, id string) StoreReportsApp {
	t.Helper()
	for _, a := range m.Apps {
		if a.ProductID == id {
			return a
		}
	}
	t.Fatalf("app %s missing", id)
	return StoreReportsApp{}
}

func storeCov(t *testing.T, a StoreReportsApp, report string) StoreReportCoverage {
	t.Helper()
	for _, r := range a.Reports {
		if r.ReportID == report {
			return r
		}
	}
	t.Fatalf("report %s missing for %s", report, a.ProductID)
	return StoreReportCoverage{}
}

func TestAppleFiscalCalendarMatchesFinancePeriods(t *testing.T) {
	for label, want := range map[string][2]string{
		"2026-10": {"2026-06-28", "2026-08-01"}, "2026-11": {"2026-08-02", "2026-08-29"},
		"2026-12": {"2026-08-30", "2026-09-26"}, "2027-01": {"2026-09-27", "2026-10-31"},
	} {
		span, ok := appleFiscalPeriod(label)
		if !ok || growthDayStr(span.start) != want[0] || growthDayStr(span.end) != want[1] {
			t.Fatalf("%s = %s..%s, want %s..%s", label, growthDayStr(span.start), growthDayStr(span.end), want[0], want[1])
		}
		if got := appleFiscalLabel(span.start); got != label {
			t.Fatalf("label of %s = %s, want %s", want[0], got, label)
		}
		if got := appleFiscalLabel(span.end); got != label {
			t.Fatalf("label of %s = %s, want %s", want[1], got, label)
		}
	}
}

// A day inside the publishing lag is not final, never missing; an older day
// nobody captured is a gap; a no-sales day is answered; a report nobody
// captures says so; a product with no app id does not apply.
func TestStoreReportsCoverageStates(t *testing.T) {
	m := buildStoreReportsT(t, storeFixtureHome(t), storeOpts(false))
	app := storeApp(t, m, "product:example-app")
	sales := storeCov(t, app, "asc_sales_summary_daily")
	if sales.State != StoreReportGaps || sales.Scope != "account" {
		t.Fatalf("sales = %+v, want gaps", sales)
	}
	if !equalStrings(sales.MissingDays, []string{growthDay(20)}) {
		t.Fatalf("missing = %v, want only %s (the no-sales day %s is answered)", sales.MissingDays, growthDay(20), growthDay(30))
	}
	for _, off := range []int{0, 1, 2} {
		if !containsString(sales.NotFinalDays, growthDay(off)) || containsString(sales.MissingDays, growthDay(off)) {
			t.Fatalf("day %s inside the lag must be not final, not missing: %+v", growthDay(off), sales)
		}
	}
	if fin := storeCov(t, app, "asc_finance_financial_monthly"); fin.State != StoreReportFresh || fin.NewestDay != "2027-01" || !containsString(fin.NotFinalDays, "2027-02") {
		t.Fatalf("finance = %+v, want fresh through 2027-01 with 2027-02 not final", fin)
	}
	if sub := storeCov(t, app, "asc_subscription_daily"); sub.State != StoreReportNotCaptured || sub.Scope != "none" {
		t.Fatalf("uncaptured report = %+v", sub)
	}
	if de := storeCov(t, app, "asc_analytics_discovery_engagement"); de.State != StoreReportFresh || de.NewestDay != growthDay(4) {
		t.Fatalf("app 1 analytics = %+v, want fresh", de)
	}
	if de := storeCov(t, storeApp(t, m, "product:second-app"), "asc_analytics_discovery_engagement"); de.State != StoreReportStale || de.NewestDay != growthDay(35) {
		t.Fatalf("app 2 analytics = %+v, want stale at %s", de, growthDay(35))
	}
	waiting := storeApp(t, m, "product:waiting-app")
	if len(waiting.AppIDs) != 0 || waiting.Summary.NotApplicable == 0 || waiting.Summary.Fresh+waiting.Summary.Gaps+waiting.Summary.Stale != 0 {
		t.Fatalf("product without app id = %+v", waiting.Summary)
	}
	// Reports whose capture holds nothing here are not "unused"; a captured
	// report that feeds nothing is.
	if len(m.Portfolio.UnusedCaptures) != 0 {
		t.Fatalf("unused captures = %v", m.Portfolio.UnusedCaptures)
	}
	for _, r := range m.Portfolio.Reach {
		if r.ReportID == "asc_sales_summary_daily" && (r.AppsWithRows != 4 || r.LinkedApps != 2) {
			t.Fatalf("sales reach = %+v, want 4 app ids with rows, 2 linked", r)
		}
	}
}

// A captured report no reader uses is listed, from the operator's overlay.
func TestStoreReportsListsCapturesNoReaderUses(t *testing.T) {
	home := storeFixtureHome(t)
	writeStoreReportOverlay(t, home, `reports:
  - id: example_trends_daily
    provider: app_store_connect
    family: sales
    title: Sales trends copy
    question: The same daily summary, fetched by a second job.
    grain: day
    published_after: {text: next day, lag_days: 2}
    kept_for: 1 year
    exactness: exact
    exactness_note: Same rows as the daily summary.
    capture:
      files: {globs: [private/metrics/raw/asc-sales-backfill/*/sales-*.tsv], period_pattern: 'sales-(\d{4}-\d{2}-\d{2})\.tsv$'}
    feeds: {kpis: [], areas: []}
`)
	m := buildStoreReportsT(t, home, storeOpts(false))
	if m.Catalog.OverlayError != "" || !equalStrings(m.Portfolio.UnusedCaptures, []string{"example_trends_daily"}) {
		t.Fatalf("overlay error %q, unused captures = %v", m.Catalog.OverlayError, m.Portfolio.UnusedCaptures)
	}
}

// Every app id the captures hold maps to exactly one product or is listed; a
// product that looks like a store app without an app id is listed; two
// products claiming one id is a conflict, and growth uses neither.
func TestStoreReportsIdentity(t *testing.T) {
	home := storeFixtureHome(t)
	id := buildStoreReportsT(t, home, storeOpts(false)).Identity
	link := map[string]StoreAppLink{}
	for _, a := range id.Apps {
		link[a.AppID] = a
	}
	if a := link[fxApp1]; a.Status != "linked" || a.ProductID != "product:example-app" || !equalStrings(a.Sources, []string{storeIDLedgerRef, storeIDPrivateMap}) || !containsString(a.SeenIn, "asc_sales_summary_daily") {
		t.Fatalf("app 1 = %+v", a)
	}
	if a := link[fxApp2]; a.Status != "linked" || !containsString(a.Sources, storeIDReleaseIdentity) {
		t.Fatalf("app 2 = %+v, want the release identity among its sources", a)
	}
	if a := link[fxOrphan]; a.Status != "no_product" || !containsString(id.AppsWithoutProduct, fxOrphan) {
		t.Fatalf("app id with no product = %+v", a)
	}
	if a := link["900000003"]; a.Status != "no_product" || !equalStrings(a.Claims, []string{"product:not-registered"}) {
		t.Fatalf("private map entry for an unknown product = %+v", a)
	}
	if a := link["900000005"]; a.Status != "conflict" || !equalStrings(a.Claims, []string{"product:example-app", "product:second-app"}) || !equalStrings(id.Conflicts, []string{"900000005"}) {
		t.Fatalf("conflict = %+v (conflicts %v)", a, id.Conflicts)
	}
	if !equalStrings(id.ProductsWithoutApp, []string{"product:waiting-app"}) {
		t.Fatalf("products without app id = %v", id.ProductsWithoutApp)
	}
	g := buildGrowthT(t, home, growthOpts())
	if u := growthApp(t, g, "product:example-app").Kpis["units"]; u.Value == nil || *u.Value != 4 {
		t.Fatalf("growth must use the linked app's series: units = %+v", u)
	}
}

// Growth carries the fast-path report summary per app and never runs the raw
// checks.
func TestGrowthCarriesStoreReportSummary(t *testing.T) {
	g := buildGrowthT(t, storeFixtureHome(t), growthOpts())
	r := growthApp(t, g, "product:example-app").Reports
	if r.Gaps < 1 || r.NotCaptured < 10 || r.Command != "compound product reports --product product:example-app --check" {
		t.Fatalf("reports = %+v", r)
	}
	if r.Checks.Failed != 1 || r.Checks.NotRun != 3 || r.Checks.OK != 0 {
		t.Fatalf("checks = %+v, want days_final failed (the gap day) and three not run", r.Checks)
	}
	w := growthApp(t, g, "product:waiting-app").Reports
	if w.NotApplicable == 0 || w.Checks.NotRun != 4 {
		t.Fatalf("product without app id: %+v", w)
	}
}

func storeChecks(m StoreReports, check, status string) []StoreReportCheck {
	var out []StoreReportCheck
	for _, c := range m.Checks {
		if c.Check == check && (status == "" || c.Status == status) {
			out = append(out, c)
		}
	}
	return out
}

func TestStoreReportsChecks(t *testing.T) {
	home := storeFixtureHome(t)
	fast := buildStoreReportsT(t, home, storeOpts(false))
	if n := len(storeChecks(fast, "series_equals_raw", StoreCheckNotRun)); n != 1 || len(storeChecks(fast, "series_equals_raw", StoreCheckFailed)) != 0 {
		t.Fatalf("without --check the raw checks must not run: %+v", fast.Checks)
	}
	m := buildStoreReportsT(t, home, storeOpts(true))

	// days_final: the never-captured day fails and is named; the no-sales day does not.
	df := storeChecks(m, "days_final", StoreCheckFailed)
	if len(df) != 1 || df[0].Period != growthDay(20) {
		t.Fatalf("days_final failures = %+v, want only %s", df, growthDay(20))
	}

	// series_equals_raw: the in-app sale filed under its own id fails on its day.
	var days []string
	for _, c := range storeChecks(m, "series_equals_raw", StoreCheckFailed) {
		days = appendUnique(days, c.Period)
		if c.AppID == fxApp1 && c.Field == "iap_units" && (*c.Expected != 1 || *c.Actual != 0 || c.ProductID != "product:example-app") {
			t.Fatalf("iap failure = %+v", c)
		}
	}
	if !equalStrings(days, []string{"2026-09-25"}) {
		t.Fatalf("series_equals_raw failed days = %v, want only 2026-09-25", days)
	}
	if ok := storeChecks(m, "series_equals_raw", StoreCheckOK); len(ok) == 0 {
		t.Fatal("apps without a misfiled row must pass")
	}

	// sales_equals_finance: the 09-26 sale carried into 2027-01 is ok, both
	// months; the EUR mismatch fails.
	fin := map[string]StoreReportCheck{}
	for _, c := range storeChecks(m, "sales_equals_finance", "") {
		fin[c.AppID+" "+c.Field+" "+c.Period[:7]] = c
	}
	if c := fin[fxApp1+" proceeds:USD 2026-12"]; c.Status != StoreCheckOK || !strings.Contains(c.Note, "2026-09-26") || !strings.Contains(c.Note, "carried to 2027-01") {
		t.Fatalf("2026-12 carry-out = %+v", c)
	}
	if c := fin[fxApp1+" proceeds:USD 2027-01"]; c.Status != StoreCheckOK || !strings.Contains(c.Note, "includes sales from 2026-09-26") {
		t.Fatalf("2027-01 carry-in = %+v", c)
	}
	if c := fin[fxApp2+" proceeds:EUR 2027-01"]; c.Status != StoreCheckFailed || *c.Expected != 1.8 || *c.Actual != 0.6 {
		t.Fatalf("EUR mismatch = %+v", c)
	}

	// downloads_agree: 10 analytics downloads against 4 in Sales fails; app 2 agrees.
	var dlFailed, dlOK []StoreReportCheck
	dlFailed, dlOK = storeChecks(m, "downloads_agree", StoreCheckFailed), storeChecks(m, "downloads_agree", StoreCheckOK)
	if len(dlFailed) != 1 || dlFailed[0].AppID != fxApp1 || *dlFailed[0].Actual != 10 || *dlFailed[0].Expected != 4 {
		t.Fatalf("downloads failures = %+v", dlFailed)
	}
	if len(dlOK) != 1 || dlOK[0].AppID != fxApp2 {
		t.Fatalf("downloads ok = %+v", dlOK)
	}

	// Failures come first, and the per-app summary counts each check once.
	if m.Checks[0].Status != StoreCheckFailed {
		t.Fatalf("first check = %+v, want a failure", m.Checks[0])
	}
	if s := storeApp(t, m, "product:example-app").Summary.Checks; s.Failed != 3 || s.OK != 1 || s.NotRun != 0 {
		t.Fatalf("example-app checks = %+v, want 3 failed (raw, downloads, days) and finance ok", s)
	}
	if m.Apps[0].ProductID != "product:example-app" {
		t.Fatalf("apps with failures sort first: %s", m.Apps[0].ProductID)
	}
	var text bytes.Buffer
	RenderStoreReportsText(&text, m)
	out := text.String()
	if !strings.Contains(out, "FAILED series_equals_raw product:example-app 2026-09-25") || strings.Index(out, "FAILED") > strings.Index(out, "Example App [") {
		t.Fatalf("text must list failures first:\n%s", out)
	}
}

// Same input, same output, with the raw checks on.
func TestStoreReportsSameInputSameBytes(t *testing.T) {
	home := storeFixtureHome(t)
	build := func() []byte {
		raw, err := json.Marshal(buildStoreReportsT(t, home, storeOpts(true)))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	first := build()
	for i := 0; i < 6; i++ {
		if again := build(); !bytes.Equal(first, again) {
			n := 0
			for n < len(first) && n < len(again) && first[n] == again[n] {
				n++
			}
			t.Fatalf("build %d differs at byte %d: %s", i+2, n, again[max(0, n-80):min(len(again), n+80)])
		}
	}
}

func TestStoreReportsReadOnly(t *testing.T) {
	home := storeFixtureHome(t)
	before := storeHomeFingerprint(t, home)
	buildStoreReportsT(t, home, storeOpts(true))
	if after := storeHomeFingerprint(t, home); after != before {
		t.Fatalf("product reports changed the home:\n%s\n---\n%s", before, after)
	}
}

func storeHomeFingerprint(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && !strings.Contains(p, string(filepath.Separator)+"lock"+string(filepath.Separator)) {
			lines = append(lines, fmt.Sprintf("%s %d %d", p, info.Size(), info.ModTime().UnixNano()))
		}
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
