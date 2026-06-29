package distiller

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEvaluateCaseFilePassesFixture(t *testing.T) {
	path := filepath.Join("..", "..", "fixtures", "distiller", "llm-offline-eval.json")
	report, err := EvaluateCaseFile(path, t0)
	if err != nil {
		t.Fatalf("evaluate case: %v", err)
	}
	if report.Status != "pass" {
		t.Fatalf("status = %q checks=%+v", report.Status, report.Checks)
	}
	if report.Run.DecisionCount != 3 {
		t.Fatalf("decision_count = %d, want 3", report.Run.DecisionCount)
	}
	if report.Run.CurrentClaims != 0 {
		t.Fatalf("current_claims = %d, want 0", report.Run.CurrentClaims)
	}
	if report.Run.LiveModelCalls != 0 {
		t.Fatalf("live_model_calls = %d, want 0", report.Run.LiveModelCalls)
	}
	if report.Request.PriorCoverage.MethodUpgrades != 1 || report.Request.PriorCoverage.CapabilityObs != 1 {
		t.Fatalf("prior coverage = %+v", report.Request.PriorCoverage)
	}
	for _, want := range []string{"work:work_distill_fixture", "context_pack:ctx_distill_fixture", "receipt:rec_distill_fixture", "raw_trace:trace_fixture"} {
		if !hasString(report.Request.SourceRefs, want) {
			t.Fatalf("source refs missing %q: %v", want, report.Request.SourceRefs)
		}
	}
}

func TestEvaluateCaseFailsWrongExpectation(t *testing.T) {
	path := filepath.Join("..", "..", "fixtures", "distiller", "llm-offline-eval.json")
	ec, err := LoadEvalCase(path)
	if err != nil {
		t.Fatalf("load case: %v", err)
	}
	ec.Expect.Decisions[0].Verdict = Promote
	report, err := EvaluateCase(filepath.Dir(path), ec, t0)
	if err != nil {
		t.Fatalf("evaluate case: %v", err)
	}
	if report.Status != "fail" {
		t.Fatalf("status = %q, want fail", report.Status)
	}
	if !checkFailed(report.Checks, "decisions") {
		t.Fatalf("expected decisions check failure, got %+v", report.Checks)
	}
}

func TestLoadEvalCaseRejectsMalformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	data, err := json.Marshal(EvalCase{SchemaVersion: EvalCaseSchemaVersion, ID: "bad"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEvalCase(path); err == nil {
		t.Fatal("expected malformed eval case to fail")
	}
}

func checkFailed(checks []EvalCheck, name string) bool {
	for _, check := range checks {
		if check.Name == name && check.Status == "fail" {
			return true
		}
	}
	return false
}
