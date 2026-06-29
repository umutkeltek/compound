package product

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/mem"
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
}

func TestValidateRejectsSignalForOtherProduct(t *testing.T) {
	b := validBundle()
	b.Signals[0].ProductID = "product:other"
	if err := Validate(b); err == nil {
		t.Fatal("signal for another product should be rejected")
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
