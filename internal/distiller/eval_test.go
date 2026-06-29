package distiller

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestEvaluateCaseExpectedExtractorErrors(t *testing.T) {
	tests := []struct {
		name string
		file string
		want string
	}{
		{
			name: "promotion basis",
			file: "llm-bad-promotion-basis-eval.json",
			want: "attempted promotion_basis",
		},
		{
			name: "unknown kind",
			file: "llm-bad-unknown-kind-eval.json",
			want: "unknown kind",
		},
		{
			name: "missing confidence",
			file: "llm-bad-missing-confidence-eval.json",
			want: "missing confidence",
		},
		{
			name: "bad confidence",
			file: "llm-bad-confidence-eval.json",
			want: "outside [0,1]",
		},
		{
			name: "malformed json",
			file: "llm-bad-malformed-eval.json",
			want: "parse llm extraction response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join("..", "..", "fixtures", "distiller", tt.file)
			report, err := EvaluateCaseFile(path, t0)
			if err != nil {
				t.Fatalf("evaluate case: %v", err)
			}
			if report.Status != "pass" {
				t.Fatalf("status = %q checks=%+v", report.Status, report.Checks)
			}
			if !strings.Contains(report.Run.Error, tt.want) {
				t.Fatalf("run error = %q, want containing %q", report.Run.Error, tt.want)
			}
			if report.Run.DecisionCount != 0 {
				t.Fatalf("decision_count = %d, want 0", report.Run.DecisionCount)
			}
			if report.Run.CurrentClaims != 0 {
				t.Fatalf("current_claims = %d, want 0", report.Run.CurrentClaims)
			}
			if report.Run.LiveModelCalls != 0 {
				t.Fatalf("live_model_calls = %d, want 0", report.Run.LiveModelCalls)
			}
			if !checkPassed(report.Checks, "error") {
				t.Fatalf("expected error check pass, got %+v", report.Checks)
			}
		})
	}
}

func TestEvaluateCaseUnexpectedExtractorErrorFailsHard(t *testing.T) {
	path := filepath.Join("..", "..", "fixtures", "distiller", "llm-bad-promotion-basis-eval.json")
	ec, err := LoadEvalCase(path)
	if err != nil {
		t.Fatalf("load case: %v", err)
	}
	ec.Expect.ErrorContains = ""

	report, err := EvaluateCase(filepath.Dir(path), ec, t0)
	if err == nil {
		t.Fatalf("expected hard eval error, got report %+v", report)
	}
	if !strings.Contains(err.Error(), "attempted promotion_basis") {
		t.Fatalf("err = %v, want attempted promotion_basis", err)
	}
	if !checkFailed(report.Checks, "error") {
		t.Fatalf("expected error check failure, got %+v", report.Checks)
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

func checkPassed(checks []EvalCheck, name string) bool {
	for _, check := range checks {
		if check.Name == name && check.Status == "pass" {
			return true
		}
	}
	return false
}

func checkFailed(checks []EvalCheck, name string) bool {
	for _, check := range checks {
		if check.Name == name && check.Status == "fail" {
			return true
		}
	}
	return false
}
