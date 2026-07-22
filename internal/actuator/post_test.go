package actuator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/policy"
)

func appendSignal(t *testing.T, home string, sig map[string]any) {
	t.Helper()
	dir := filepath.Join(home, "ledger")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(sig)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "product-signals.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
}

func TestPostStagesAndOpensLoop(t *testing.T) {
	home := t.TempDir()
	cfg := policy.DefaultConfig()
	res, err := Post(home, PostInput{
		WorkID:    "work_1",
		ProductID: "product:demo",
		Channel:   "distribution",
		Surface:   "x",
		Content:   "Ship your agent work that compounds.",
		Metric:    "posthog_signups",
		Window:    "7d",
	}, cfg, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Executed || res.Held {
		t.Fatalf("expected executed, got %+v", res)
	}
	if res.ReceiptRef == "" || res.TransitionRef == "" {
		t.Fatal("expected receipt + transition from the governed staging")
	}
	if _, err := os.Stat(res.PostPath); err != nil {
		t.Fatalf("staged post artifact missing: %v", err)
	}
	if res.HypothesisID == "" {
		t.Fatal("expected a minted observation hypothesis")
	}
	// The hypothesis must be readable from the candidates inbox (where the product loop reads).
	hypPath := filepath.Join(home, candidates.InboxDir(candidates.ProductHypothesis), res.HypothesisID+".json")
	if _, err := os.Stat(hypPath); err != nil {
		t.Fatalf("minted hypothesis not found at %s: %v", hypPath, err)
	}
}

func TestPostHoldsWhenBudgetExhausted(t *testing.T) {
	home := t.TempDir()
	cfg := policy.DefaultConfig()
	ledger := OpenLedger(home)
	budget := BudgetEnvelope{
		SchemaVersion: BudgetSchemaVersion, ID: "budget_product-demo", ProductID: "product:demo",
		CreatedAt: testNow.Format(time.RFC3339),
		Channels:  []ChannelLimits{{Channel: "distribution", WindowHours: 24, MaxActions: 1}},
	}
	if err := ledger.SaveBudget(budget); err != nil {
		t.Fatal(err)
	}
	in := PostInput{WorkID: "work_1", ProductID: "product:demo", Channel: "distribution", Surface: "x", Content: "one", Metric: "posthog_signups"}
	// First post spends the single allowed action.
	if _, err := Post(home, in, cfg, testNow); err != nil {
		t.Fatal(err)
	}
	// Second post must be HELD by the budget, not executed.
	in.Content = "two"
	res, err := Post(home, in, cfg, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Held {
		t.Fatalf("expected second post to be HELD by budget; got %+v", res)
	}
}

func TestObserveClosureOpenThenClosed(t *testing.T) {
	home := t.TempDir()
	cfg := policy.DefaultConfig()
	if _, err := Post(home, PostInput{
		WorkID: "work_1", ProductID: "product:demo", Channel: "distribution",
		Surface: "x", Content: "hello world", Metric: "posthog_signups", Window: "7d",
	}, cfg, testNow); err != nil {
		t.Fatal(err)
	}

	// Before any signal: the observation is OPEN.
	status, err := ObserveClosure(home, "product:demo", testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 1 || status[0].State != "open" {
		t.Fatalf("expected 1 open observation, got %+v", status)
	}

	// The world responds: a matching ProductSignal arrives on the metric.
	appendSignal(t, home, map[string]any{
		"id": "sig_demo_1", "product_id": "product:demo", "surface": "x",
		"change_event": "signup spike after post", "metric_name": "posthog_signups",
		"before": 5.0, "after": 12.0, "window": "7d", "confidence": 0.8,
		"evidence_refs": []string{"posthog:export:1"}, "observed_at": testNow.Add(24 * time.Hour).Format(time.RFC3339),
	})

	// After the signal: the loop CLOSED, with the observed delta attached.
	status, err = ObserveClosure(home, "product:demo", testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 1 {
		t.Fatalf("expected 1 observation, got %+v", status)
	}
	if status[0].State != "signal_received" {
		t.Fatalf("expected signal_received, got %s", status[0].State)
	}
	if status[0].MatchedSignalID != "sig_demo_1" || status[0].After != 12.0 {
		t.Fatalf("expected matched signal with delta, got %+v", status[0])
	}
}

func TestObserveClosureExpiresAfterWindow(t *testing.T) {
	home := t.TempDir()
	cfg := policy.DefaultConfig()
	if _, err := Post(home, PostInput{
		WorkID: "work_1", ProductID: "product:demo", Channel: "distribution",
		Surface: "x", Content: "unanswered move", Metric: "posthog_signups", Window: "7d",
	}, cfg, testNow); err != nil {
		t.Fatal(err)
	}

	// Still inside the 7d window: OPEN.
	status, err := ObserveClosure(home, "product:demo", testNow.Add(2*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 1 || status[0].State != "open" || status[0].Expired {
		t.Fatalf("expected open inside window, got %+v", status)
	}

	// Past the window with no signal: EXPIRED — surfaced as debt, not forgotten.
	status, err = ObserveClosure(home, "product:demo", testNow.Add(8*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 1 || status[0].State != "expired" || !status[0].Expired {
		t.Fatalf("expected expired after window, got %+v", status)
	}
}

func TestObserveClosureLateSignalStillCloses(t *testing.T) {
	home := t.TempDir()
	cfg := policy.DefaultConfig()
	if _, err := Post(home, PostInput{
		WorkID: "work_1", ProductID: "product:demo", Channel: "distribution",
		Surface: "x", Content: "late responder", Metric: "posthog_signups", Window: "7d",
	}, cfg, testNow); err != nil {
		t.Fatal(err)
	}
	// A signal that lands AFTER the window must still close the loop.
	appendSignal(t, home, map[string]any{
		"id": "sig_late", "product_id": "product:demo", "surface": "x",
		"change_event": "late signup bump", "metric_name": "posthog_signups",
		"before": 5.0, "after": 9.0, "window": "7d", "confidence": 0.6,
		"evidence_refs": []string{"posthog:export:late"}, "observed_at": testNow.Add(9 * 24 * time.Hour).Format(time.RFC3339),
	})
	status, err := ObserveClosure(home, "product:demo", testNow.Add(10*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 1 || status[0].State != "signal_received" || status[0].Expired {
		t.Fatalf("expected late signal to close, got %+v", status)
	}
	if status[0].MatchedSignalID != "sig_late" {
		t.Fatalf("expected sig_late matched, got %+v", status[0])
	}
}

// TestPostSurfacesConsumptionRecordFailure (F9.1): when a post stages + receipts but
// recording its budget consumption fails, Post must return an error naming the
// consumption failure and the receipt id — and still report the move happened
// (Executed=true, feedback loop closed) so the operator knows the staged post was NOT
// rolled back. The budget dir is made read-only so EvaluatePlan's reads
// (LoadBudget/Used) succeed but Record (a write) fails, isolating the record failure.
// This mirrors TestRunSurfacesConsumptionRecordFailure for the run path.
func TestPostSurfacesConsumptionRecordFailure(t *testing.T) {
	home := t.TempDir()
	budgetDir := filepath.Join(home, "actuator", "budget")
	if err := os.MkdirAll(budgetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(budgetDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(budgetDir, 0o755) })

	res, err := Post(home, PostInput{
		WorkID:    "work_rec",
		ProductID: "product:demo",
		Channel:   "distribution",
		Surface:   "x",
		Content:   "Ship your agent work that compounds.",
		Metric:    "posthog_signups",
		Window:    "7d",
	}, policy.DefaultConfig(), testNow)

	if err == nil {
		t.Fatal("a failed consumption record must surface as an error, not be silently discarded")
	}
	if !strings.Contains(err.Error(), "consumption") {
		t.Fatalf("error must name the consumption-record failure, got: %v", err)
	}
	if !res.Executed {
		t.Fatalf("the post DID stage + receipt; result must report Executed=true: %+v", res)
	}
	if res.ReceiptRef == "" {
		t.Fatalf("expected a populated receipt ref (the post landed), got: %+v", res)
	}
	if !strings.Contains(err.Error(), res.ReceiptRef) {
		t.Fatalf("error must report the receipt id %q, got: %v", res.ReceiptRef, err)
	}
	if res.HypothesisID == "" {
		t.Fatalf("the feedback loop must still close (hypothesis minted) despite the record failure: %+v", res)
	}
	if _, statErr := os.Stat(res.PostPath); statErr != nil {
		t.Fatalf("the staged post must remain on disk (no rollback), got: %v", statErr)
	}
}

func TestWindowDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"7d":  7 * 24 * time.Hour,
		"48h": 48 * time.Hour,
		"2w":  14 * 24 * time.Hour,
		"":    7 * 24 * time.Hour,
	}
	for in, want := range cases {
		if got := windowDuration(in); got != want {
			t.Errorf("windowDuration(%q) = %v, want %v", in, got, want)
		}
	}
}
