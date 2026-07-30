package product

import (
	"math"
	"strings"
	"testing"
)

const ascFunnelFixture = "../../fixtures/product/asc-funnel-series.jsonl"

// TestLoadASCFunnelSeriesSignalAggregatesCompleteFunnelRowsOnly would fail if
// the aggregator summed every row regardless of coverage: the fixture plants a
// row with impressions=999999 and coverage.complete_funnel=false inside both
// windows. A naive sum would return values in the hundreds of thousands; the
// coverage-gated sum must return the small, real numbers.
func TestLoadASCFunnelSeriesSignalAggregatesCompleteFunnelRowsOnly(t *testing.T) {
	doc, err := LoadASCFunnelSeriesSignal(ascFunnelFixture, ASCFunnelSeriesSignalOptions{
		ProductID:    "product:focus-notes",
		AppID:        "123456789",
		Metric:       "impressions",
		BeforeWindow: "2026-07-01/2026-07-03",
		AfterWindow:  "2026-07-08/2026-07-10",
		ChangeEvent:  "Reworked App Store product page screenshots.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSignalDocument(doc); err != nil {
		t.Fatal(err)
	}
	s := doc.Signal
	if s.Before != 300 {
		t.Fatalf("before = %v, want 300 (100+200, excluding the incomplete-coverage 999999 row); a naive sum would have included it", s.Before)
	}
	if s.After != 400 {
		t.Fatalf("after = %v, want 400 (150+250, excluding the incomplete-coverage 999999 row)", s.After)
	}
	if !hasString(s.EvidenceRefs, "asc:funnel_series:before_complete_funnel_rows:2") {
		t.Fatalf("evidence refs missing before row count: %+v", s.EvidenceRefs)
	}
	if !hasString(s.EvidenceRefs, "asc:funnel_series:after_complete_funnel_rows:2") {
		t.Fatalf("evidence refs missing after row count: %+v", s.EvidenceRefs)
	}
}

// TestLoadASCFunnelSeriesSignalMatchesByAppIDNotDisplayName would fail if the
// adapter ever fell back to matching by name or slug. The fixture gives BOTH
// asc_app_id=123456789 and asc_app_id=987654321 the identical name
// "Neutral App A" on the SAME two dates (2026-07-01 and 2026-07-03), both with
// coverage.complete_funnel=true, so a name- or coverage-only match would blend
// in 987654321's decoy values (777, 888). Requesting app_id=123456789 must
// return only that app's own numbers (100, 200).
func TestLoadASCFunnelSeriesSignalMatchesByAppIDNotDisplayName(t *testing.T) {
	doc, err := LoadASCFunnelSeriesSignal(ascFunnelFixture, ASCFunnelSeriesSignalOptions{
		ProductID:    "product:focus-notes",
		AppID:        "123456789",
		Metric:       "impressions",
		BeforeWindow: "2026-07-01/2026-07-01",
		AfterWindow:  "2026-07-03/2026-07-03",
		ChangeEvent:  "Reworked App Store product page screenshots.",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 123456789's rows on these two dates are 100 and 200. If matching ever
	// degraded to using the shared "name" field (or dropped the app_id filter
	// entirely), this would instead pick up 987654321's decoy rows (777, 888),
	// which share the same name and the same true-coverage dates on purpose.
	if doc.Signal.Before != 100 || doc.Signal.After != 200 {
		t.Fatalf("before/after = %v/%v, want 100/200 (product 123456789's own rows, not the name-colliding 987654321 decoy rows which are 777/888)", doc.Signal.Before, doc.Signal.After)
	}
}

// TestLoadASCFunnelSeriesSignalRejectsIncompleteCoverageWindow would fail if
// coverage gating were dropped: asc_app_id=987654321's row on 2026-07-02 has
// coverage.complete_funnel=false (its neighboring 07-01/07-03 rows are true,
// so this isolates the single false day). A naive implementation would
// happily aggregate it anyway; this must error instead of returning a
// deflated (or worse, silently zero) rate.
func TestLoadASCFunnelSeriesSignalRejectsIncompleteCoverageWindow(t *testing.T) {
	_, err := LoadASCFunnelSeriesSignal(ascFunnelFixture, ASCFunnelSeriesSignalOptions{
		ProductID:    "product:other-app",
		AppID:        "987654321",
		Metric:       "impressions",
		BeforeWindow: "2026-07-02/2026-07-02",
		AfterWindow:  "2026-07-02/2026-07-02",
		ChangeEvent:  "Reworked App Store product page screenshots.",
	})
	if err == nil {
		t.Fatal("window with zero coverage.complete_funnel rows should be rejected, not silently aggregated")
	}
	if !strings.Contains(err.Error(), "coverage.complete_funnel") {
		t.Fatalf("error = %q, want it to name coverage.complete_funnel as the reason", err.Error())
	}
}

// TestLoadASCFunnelSeriesSignalTreatsMissingCoverageKeyAsIncomplete would fail
// if a pre-coverage-schema row (no "coverage" key at all, as the earliest
// artifact rows are) were treated as complete by default. It must decode to
// the zero value (complete_funnel=false) and be excluded, exactly like an
// explicit false.
func TestLoadASCFunnelSeriesSignalTreatsMissingCoverageKeyAsIncomplete(t *testing.T) {
	_, err := LoadASCFunnelSeriesSignal(ascFunnelFixture, ASCFunnelSeriesSignalOptions{
		ProductID:    "product:focus-notes",
		AppID:        "123456789",
		Metric:       "impressions",
		BeforeWindow: "2026-06-15/2026-06-15",
		AfterWindow:  "2026-07-01/2026-07-01",
		ChangeEvent:  "Reworked App Store product page screenshots.",
	})
	if err == nil {
		t.Fatal("a row with no coverage key at all should be treated as incomplete, not as a true complete_funnel row")
	}
	if !strings.Contains(err.Error(), "coverage.complete_funnel") {
		t.Fatalf("error = %q, want it to name coverage.complete_funnel as the reason", err.Error())
	}
}

// TestLoadASCFunnelSeriesSignalConversionRateCanExceedOne proves the
// documented trap is handled deliberately, not by accident: first_time_downloads
// can exceed product_page_views on a single day because iOS allows installing
// directly from the search card. The after window here sums to a rate > 1.0;
// this must succeed (not error) and must carry a confounder explaining why.
func TestLoadASCFunnelSeriesSignalConversionRateCanExceedOne(t *testing.T) {
	doc, err := LoadASCFunnelSeriesSignal(ascFunnelFixture, ASCFunnelSeriesSignalOptions{
		ProductID:    "product:focus-notes",
		AppID:        "123456789",
		Metric:       "product_page_conversion_rate",
		BeforeWindow: "2026-07-01/2026-07-03",
		AfterWindow:  "2026-07-08/2026-07-10",
		ChangeEvent:  "Reworked App Store product page screenshots.",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantBefore := 50.0 / 150.0 // (20+30) downloads / (50+100) page views
	wantAfter := 170.0 / 150.0 // (70+100) downloads / (60+90) page views, > 1.0
	if math.Abs(doc.Signal.Before-wantBefore) > 1e-9 {
		t.Fatalf("before rate = %v, want %v", doc.Signal.Before, wantBefore)
	}
	if math.Abs(doc.Signal.After-wantAfter) > 1e-9 {
		t.Fatalf("after rate = %v, want %v (>1.0 is expected here, not an error)", doc.Signal.After, wantAfter)
	}
	if doc.Signal.After <= 1.0 {
		t.Fatalf("test fixture no longer demonstrates the >1.0 trap; after rate = %v", doc.Signal.After)
	}
	if doc.Signal.MetricName != "app_store_product_page_conversion" {
		t.Fatalf("metric_name = %q, want app_store_product_page_conversion (the alias existing hypotheses already reference)", doc.Signal.MetricName)
	}
	foundConfounder := false
	for _, c := range doc.Signal.Confounders {
		if strings.Contains(c, "first_time_downloads can exceed product_page_views") {
			foundConfounder = true
		}
	}
	if !foundConfounder {
		t.Fatalf("confounders = %+v, want a note explaining rates above 1.0", doc.Signal.Confounders)
	}
}

func TestLoadASCFunnelSeriesSignalRejectsUnsupportedMetric(t *testing.T) {
	_, err := LoadASCFunnelSeriesSignal(ascFunnelFixture, ASCFunnelSeriesSignalOptions{
		ProductID:    "product:focus-notes",
		AppID:        "123456789",
		Metric:       "not_a_metric",
		BeforeWindow: "2026-07-01/2026-07-03",
		AfterWindow:  "2026-07-08/2026-07-10",
		ChangeEvent:  "Reworked App Store product page screenshots.",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("err = %v, want an unsupported-metric error", err)
	}
}
