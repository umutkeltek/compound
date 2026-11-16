package product

import (
	"testing"
	"time"

	"compound/internal/mem"
)

// TestResultsDueListsEveryPendingHypothesisOnceWithItsSignals checks the
// read against the doctor counts Authority already reports: one row per
// hypothesis pending review, each carrying the signals that match it.
func TestResultsDueListsEveryPendingHypothesisOnceWithItsSignals(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Load("../../fixtures/product/market-loop.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(home, store, bundle, "fixtures/product/market-loop.yaml", tProduct); err != nil {
		t.Fatal(err)
	}
	signalDoc, err := LoadSignal("../../fixtures/product/signal.asc.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplySignal(home, store, signalDoc, "fixtures/product/signal.asc.yaml", tProduct.Add(14*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	authority, err := Authority(home, store)
	if err != nil {
		t.Fatal(err)
	}
	due, err := ResultsDue(home, store, tProduct.Add(30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if authority.PendingReviewCount == 0 || len(due) != authority.PendingReviewCount {
		t.Fatalf("results due = %d rows, want one per pending hypothesis (%d)", len(due), authority.PendingReviewCount)
	}
	signals, seen := 0, map[string]bool{}
	for _, row := range due {
		if row.ProductID == "" || row.HypothesisID == "" || seen[row.HypothesisID] {
			t.Fatalf("row without identity or repeated: %+v", row)
		}
		seen[row.HypothesisID] = true
		signals += len(row.SignalIDs)
	}
	if signals != authority.ReviewReadyCount {
		t.Fatalf("signals across rows = %d, want the doctor's review-ready pairs %d", signals, authority.ReviewReadyCount)
	}
}
