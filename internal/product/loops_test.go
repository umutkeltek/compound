package product

import (
	"testing"
	"time"

	"compound/internal/mem"
)

var loopTestNow = time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)

func TestBuildLoopModelJoinsProductFeedbackLoop(t *testing.T) {
	home := seedLoopModelHome(t)
	model, err := BuildLoopModel(home, LoopOptions{Recent: 1, StaleAfter: 48 * time.Hour, Now: loopTestNow})
	if err != nil {
		t.Fatal(err)
	}
	loop := findProductLoop(model.Products, "product:loop-demo")
	if loop.ProductID == "" {
		t.Fatalf("loop products = %+v", model.Products)
	}
	gscSignals := loop.SignalsByMetric["gsc_clicks"]
	if len(gscSignals) != 1 || gscSignals[0].SignalID != "sig_loop_gsc_new" {
		t.Fatalf("gsc signals = %+v", gscSignals)
	}
	if gscSignals[0].Provider != "google_search_console" || gscSignals[0].TransitionRef == "" {
		t.Fatalf("gsc signal provider/transition = %+v", gscSignals[0])
	}
	if !hasLoopHypothesis(loop.OpenHypotheses, "hyp_loop_search_ready") || !hasLoopHypothesis(loop.OpenHypotheses, "hyp_loop_activation_missing") {
		t.Fatalf("open hypotheses = %+v", loop.OpenHypotheses)
	}
	if hasLoopHypothesis(loop.OpenHypotheses, "hyp_loop_reviewed_conversion") {
		t.Fatalf("reviewed hypothesis should not remain open: %+v", loop.OpenHypotheses)
	}
	if len(loop.Reviews) != 1 || loop.Reviews[0].ID != "review_loop_conversion" || loop.Reviews[0].Verdict != "supported" || loop.Reviews[0].SignalID != "sig_loop_conversion_reviewed" {
		t.Fatalf("reviews = %+v", loop.Reviews)
	}
	if loop.OpenSourceIssues.Count != 1 || len(loop.OpenSourceIssues.Refs) != 1 {
		t.Fatalf("source issues = %+v", loop.OpenSourceIssues)
	}
	for _, gap := range []string{"signals_without_hypothesis", "hypothesis_without_signal", "review_ready"} {
		if !hasString(loop.LoopGaps, gap) {
			t.Fatalf("gap %q missing from %+v", gap, loop.LoopGaps)
		}
	}
	if loop.Freshness.LatestSignalAt != "2026-07-02T11:00:00Z" || loop.Freshness.LatestReviewAt != "2026-07-02T08:00:00Z" || loop.Freshness.Stale {
		t.Fatalf("freshness = %+v", loop.Freshness)
	}
}

func TestBuildLoopModelStaleWindowAndProductFilter(t *testing.T) {
	home := seedLoopModelHome(t)
	model, err := BuildLoopModel(home, LoopOptions{ProductID: "product:loop-demo", Recent: 10, StaleDays: 7, Now: loopTestNow.Add(8 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Products) != 1 || model.Products[0].ProductID != "product:loop-demo" {
		t.Fatalf("filtered products = %+v", model.Products)
	}
	if !model.Products[0].Freshness.Stale {
		t.Fatalf("freshness should be stale after stale window: %+v", model.Products[0].Freshness)
	}
	missing, err := BuildLoopModel(home, LoopOptions{ProductID: "product:missing", Now: loopTestNow})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing.Products) != 0 {
		t.Fatalf("missing product filter = %+v", missing.Products)
	}
}

func seedLoopModelHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	bundle := Bundle{
		SchemaVersion: SchemaVersion,
		Product: Product{
			ID:             "product:loop-demo",
			Name:           "Loop Demo",
			OwnerEntity:    "project:loop-demo",
			Surfaces:       []Surface{{ID: "landing", Kind: "web", Ref: "https://example.invalid/loop-demo"}},
			Channels:       []string{"web"},
			Positioning:    "Synthetic feedback-loop test surface.",
			PrivacyPosture: "test_fixture",
		},
		Hypotheses: []Hypothesis{
			{
				ID:                "hyp_loop_search_ready",
				ProductID:         "product:loop-demo",
				ChangeEvent:       "Updated search title copy.",
				ExpectedEffect:    "Search clicks increase.",
				Metrics:           []string{"gsc_clicks"},
				ObservationWindow: "7d after change",
				Result:            "unknown",
				NextAction:        "review search clicks after window",
				EvidenceRefs:      []string{"fixture:search-title"},
			},
			{
				ID:                "hyp_loop_activation_missing",
				ProductID:         "product:loop-demo",
				ChangeEvent:       "Changed activation flow.",
				ExpectedEffect:    "Activation improves.",
				Metrics:           []string{"activation_rate"},
				ObservationWindow: "7d after change",
				Result:            "unknown",
				NextAction:        "wait for activation evidence",
				EvidenceRefs:      []string{"fixture:activation-plan"},
			},
			{
				ID:                "hyp_loop_reviewed_conversion",
				ProductID:         "product:loop-demo",
				ChangeEvent:       "Updated conversion screenshot.",
				ExpectedEffect:    "Conversion improves.",
				Metrics:           []string{"app_store_product_page_conversion"},
				ObservationWindow: "7d after change",
				Result:            "unknown",
				NextAction:        "review conversion evidence",
				EvidenceRefs:      []string{"fixture:conversion-plan"},
			},
		},
		EvidenceRefs: []string{"fixture:loop-product"},
	}
	if _, err := Apply(home, store, bundle, "fixture/product.yaml", loopTestNow.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	other := bundle
	other.Product.ID = "product:other-loop"
	other.Product.Name = "Other Loop"
	other.Product.Surfaces = []Surface{{ID: "landing", Kind: "web", Ref: "https://example.invalid/other-loop"}}
	other.Hypotheses = nil
	if _, err := Apply(home, store, other, "fixture/other-product.yaml", loopTestNow.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	applyLoopSignal(t, home, store, SignalDocument{
		SchemaVersion: SignalSchemaVersion,
		Source:        SignalSource{Kind: "google_search_console", Ref: "gsc:site:example.invalid:old"},
		Signal: Signal{
			ID:          "sig_loop_gsc_old",
			ProductID:   "product:loop-demo",
			Surface:     "landing",
			ChangeEvent: "Updated search title copy.",
			MetricName:  "gsc_clicks",
			Before:      100,
			After:       110,
			Window:      "2026-06-30/2026-07-01",
			Confidence:  0.5,
			EvidenceRefs: []string{
				"fixture:gsc-old",
			},
			ObservedAt: "2026-07-01T10:00:00Z",
		},
	})
	applyLoopSignal(t, home, store, SignalDocument{
		SchemaVersion: SignalSchemaVersion,
		Source:        SignalSource{Kind: "google_search_console", Ref: "gsc:site:example.invalid:new"},
		Signal: Signal{
			ID:          "sig_loop_gsc_new",
			ProductID:   "product:loop-demo",
			Surface:     "landing",
			ChangeEvent: "Updated search title copy.",
			MetricName:  "gsc_clicks",
			Before:      100,
			After:       140,
			Window:      "2026-07-01/2026-07-02",
			Confidence:  0.6,
			EvidenceRefs: []string{
				"fixture:gsc-new",
			},
			ObservedAt: "2026-07-02T10:00:00Z",
		},
	})
	applyLoopSignal(t, home, store, SignalDocument{
		SchemaVersion: SignalSchemaVersion,
		Source:        SignalSource{Kind: "posthog", Ref: "posthog:project:loop"},
		Signal: Signal{
			ID:           "sig_loop_unmatched",
			ProductID:    "product:loop-demo",
			Surface:      "landing",
			ChangeEvent:  "Published unmatched pricing copy.",
			MetricName:   "custom_unmatched_metric",
			Before:       4,
			After:        5,
			Window:       "2026-07-02",
			Confidence:   0.4,
			EvidenceRefs: []string{"fixture:unmatched"},
			ObservedAt:   "2026-07-02T11:00:00Z",
		},
	})
	applyLoopSignal(t, home, store, SignalDocument{
		SchemaVersion: SignalSchemaVersion,
		Source:        SignalSource{Kind: "app_store_connect", Ref: "asc:app:loop"},
		Signal: Signal{
			ID:           "sig_loop_conversion_reviewed",
			ProductID:    "product:loop-demo",
			Surface:      "app-store",
			ChangeEvent:  "Updated conversion screenshot.",
			MetricName:   "app_store_product_page_conversion",
			Before:       0.10,
			After:        0.14,
			Window:       "2026-06-25/2026-07-01",
			Confidence:   0.7,
			EvidenceRefs: []string{"fixture:conversion"},
			ObservedAt:   "2026-07-01T09:00:00Z",
		},
	})
	review := Review{
		SchemaVersion: ReviewSchemaVersion,
		ID:            "review_loop_conversion",
		ProductID:     "product:loop-demo",
		HypothesisID:  "hyp_loop_reviewed_conversion",
		Result:        "supported",
		Rationale:     "Reviewed conversion movement with confounders noted.",
		Confidence:    0.7,
		NextAction:    "keep measuring one more window",
		EvidenceRefs:  []string{"product_signal:sig_loop_conversion_reviewed", "fixture:review"},
		ReviewedAt:    "2026-07-02T08:00:00Z",
	}
	if _, err := ApplyReview(home, store, review, "fixture/review.yaml", loopTestNow.Add(-4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RecordSignalDoctorNoPromote(home, SignalDoctorReport{
		SourceRef:     "fixture:bad-source",
		SourceHash:    "hash_loop_bad_source",
		SchemaVersion: SignalExportSchemaVersion,
		Status:        "fail",
		ProductID:     "product:loop-demo",
		SourceKind:    "google_search_console",
		Checks:        []SignalCheck{{Name: "strict_contract", Status: "fail", Detail: "synthetic invalid field"}},
	}, loopTestNow); err != nil {
		t.Fatal(err)
	}
	return home
}

func applyLoopSignal(t *testing.T, home string, store *mem.Store, doc SignalDocument) {
	t.Helper()
	if _, err := ApplySignal(home, store, doc, "fixture/"+doc.Signal.ID+".yaml", loopTestNow); err != nil {
		t.Fatal(err)
	}
}

func findProductLoop(products []ProductLoop, id string) ProductLoop {
	for _, product := range products {
		if product.ProductID == id {
			return product
		}
	}
	return ProductLoop{}
}

func hasLoopHypothesis(hypotheses []LoopHypothesis, id string) bool {
	for _, hypothesis := range hypotheses {
		if hypothesis.ID == id {
			return true
		}
	}
	return false
}
