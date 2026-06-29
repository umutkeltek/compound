package product

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/mem"
	"compound/internal/writeback"
)

var tProduct = time.Date(2026, 6, 29, 14, 0, 0, 0, time.UTC)

func TestApplyImportsProductSignalAndHypothesisCandidate(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Load("../../fixtures/product/market-loop.yaml")
	if err != nil {
		t.Fatal(err)
	}

	report, err := Apply(home, store, bundle, "fixtures/product/market-loop.yaml", tProduct)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || report.EntityID != "product:focus-notes" || len(report.ClaimIDs) == 0 || len(report.SignalIDs) != 1 || len(report.HypothesisIDs) != 1 {
		t.Fatalf("report = %+v", report)
	}

	res, err := store.ResolveEntity("appstore:123456789")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "resolved" || res.Slug != "product:focus-notes" {
		t.Fatalf("product identifier should resolve, got %+v", res)
	}

	tl, err := LoadTimeline(home, "product:focus-notes")
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Signals) != 1 || tl.Signals[0].MetricName != "app_store_product_page_conversion" {
		t.Fatalf("signals = %+v", tl.Signals)
	}
	if len(tl.Hypotheses) != 1 || tl.Hypotheses[0].Verdict != candidates.Park {
		t.Fatalf("hypotheses = %+v", tl.Hypotheses)
	}

	if _, err := os.Stat(filepath.Join(home, candidates.InboxDir(candidates.ProductHypothesis), "hyp_focus_visuals_conversion.json")); err != nil {
		t.Fatal(err)
	}

	doctor, err := Doctor(home, "product:focus-notes")
	if err != nil {
		t.Fatal(err)
	}
	if doctor.Status != "needs_review" || doctor.SignalCount != 1 || doctor.HypothesisCount != 1 || doctor.PendingReviewCount != 1 {
		t.Fatalf("doctor = %+v", doctor)
	}
	if !NeedsWriteback(doctor) {
		t.Fatal("NeedsWriteback = false, want true")
	}
	gate := WritebackGate(doctor, "act_product_review", "pol_product_review", tProduct)
	if gate.CandidateKind != string(candidates.ProductHypothesis) || gate.OwnerTarget.Kind != "product_strategy" || gate.OwnerTarget.Ref != "product:focus-notes" {
		t.Fatalf("gate = %+v", gate)
	}
	checked, err := writeback.EvaluateGate(gate, nil, tProduct)
	if err != nil {
		t.Fatal(err)
	}
	if checked.Status != writeback.GateNoPolicy {
		t.Fatalf("gate status = %q, want %q", checked.Status, writeback.GateNoPolicy)
	}

	review, err := LoadReview("../../fixtures/product/review.yaml")
	if err != nil {
		t.Fatal(err)
	}
	reviewReport, err := ApplyReview(home, store, review, "fixtures/product/review.yaml", tProduct.Add(14*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if reviewReport.Status != "updated" || reviewReport.Result != "inconclusive" || reviewReport.EvidenceID == "" {
		t.Fatalf("review report = %+v", reviewReport)
	}
	reviewed, err := LoadTimeline(home, "product:focus-notes")
	if err != nil {
		t.Fatal(err)
	}
	if len(reviewed.Hypotheses) != 1 || reviewed.Hypotheses[0].Result != "inconclusive" {
		t.Fatalf("reviewed hypotheses = %+v", reviewed.Hypotheses)
	}
	if !hasString(reviewed.Hypotheses[0].EvidenceRefs, reviewReport.EvidenceID) || !hasString(reviewed.Hypotheses[0].EvidenceRefs, "product_review:review_focus_visuals_20260713") {
		t.Fatalf("review evidence refs = %+v", reviewed.Hypotheses[0].EvidenceRefs)
	}
	postReview, err := Doctor(home, "product:focus-notes")
	if err != nil {
		t.Fatal(err)
	}
	if postReview.Status != "ok" || postReview.PendingReviewCount != 0 {
		t.Fatalf("post-review doctor = %+v", postReview)
	}

	signalDoc, err := LoadSignal("../../fixtures/product/signal.asc.yaml")
	if err != nil {
		t.Fatal(err)
	}
	signalReport, err := ApplySignal(home, store, signalDoc, "fixtures/product/signal.asc.yaml", tProduct.Add(14*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if signalReport.Status != "ok" || len(signalReport.SignalIDs) != 1 || len(signalReport.EvidenceIDs) != 1 {
		t.Fatalf("signal report = %+v", signalReport)
	}
	withSignal, err := LoadTimeline(home, "product:focus-notes")
	if err != nil {
		t.Fatal(err)
	}
	if len(withSignal.Signals) != 2 {
		t.Fatalf("signals after standalone import = %+v", withSignal.Signals)
	}
	importedSignal := findSignal(withSignal.Signals, "sig_focus_conversion_14d_20260713")
	if importedSignal.ID == "" {
		t.Fatalf("standalone signal missing: %+v", withSignal.Signals)
	}
	if !hasString(importedSignal.EvidenceRefs, signalReport.EvidenceIDs[0]) || !hasString(importedSignal.EvidenceRefs, "fixture:asc-export") {
		t.Fatalf("signal evidence refs = %+v", importedSignal.EvidenceRefs)
	}
}

func TestValidateRejectsSignalForOtherProduct(t *testing.T) {
	b := validBundle()
	b.Signals[0].ProductID = "product:other"
	if err := Validate(b); err == nil {
		t.Fatal("signal for another product should be rejected")
	}
}

func TestValidateReviewRejectsUnknownResult(t *testing.T) {
	review := validReview()
	review.Result = "unknown"
	if err := ValidateReview(review); err == nil {
		t.Fatal("review with unknown result should be rejected")
	}
}

func TestValidateSignalDocumentRejectsMissingSource(t *testing.T) {
	doc := validSignalDocument()
	doc.Source.Kind = ""
	if err := ValidateSignalDocument(doc); err == nil {
		t.Fatal("signal document without source should be rejected")
	}
}

func validBundle() Bundle {
	return Bundle{
		SchemaVersion: SchemaVersion,
		Product: Product{
			ID:             "product:x",
			Name:           "X",
			OwnerEntity:    "project:x",
			Surfaces:       []Surface{{ID: "store", Kind: "app_store", Ref: "appstore:1"}},
			Channels:       []string{"app_store"},
			Positioning:    "Focused utility",
			PrivacyPosture: "local_first",
		},
		Signals: []Signal{{
			ID:           "sig_x",
			ProductID:    "product:x",
			Surface:      "store",
			ChangeEvent:  "changed screenshots",
			MetricName:   "conversion",
			Before:       0.1,
			After:        0.12,
			Window:       "7d",
			Confidence:   0.5,
			EvidenceRefs: []string{"fixture"},
		}},
		Hypotheses: []Hypothesis{{
			ID:                "hyp_x",
			ProductID:         "product:x",
			ChangeEvent:       "changed screenshots",
			ExpectedEffect:    "conversion improves",
			Metrics:           []string{"conversion"},
			ObservationWindow: "14d",
			Result:            "unknown",
			NextAction:        "wait",
			EvidenceRefs:      []string{"fixture"},
		}},
		EvidenceRefs: []string{"fixture"},
	}
}

func validReview() Review {
	return Review{
		SchemaVersion: ReviewSchemaVersion,
		ID:            "review_x",
		ProductID:     "product:x",
		HypothesisID:  "hyp_x",
		Result:        "inconclusive",
		Rationale:     "movement exists but evidence is not causal",
		Confidence:    0.5,
		EvidenceRefs:  []string{"fixture"},
	}
}

func validSignalDocument() SignalDocument {
	b := validBundle()
	return SignalDocument{
		SchemaVersion: SignalSchemaVersion,
		Source:        SignalSource{Kind: "app_store_connect", Ref: "fixture"},
		Signal:        b.Signals[0],
	}
}

func hasString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func findSignal(signals []Signal, id string) Signal {
	for _, s := range signals {
		if s.ID == id {
			return s
		}
	}
	return Signal{}
}
