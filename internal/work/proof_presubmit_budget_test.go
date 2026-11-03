package work

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func presubmitBudgetSteps() []ProofCheckStep {
	return []ProofCheckStep{
		{Name: "gofmt", Command: []string{"gofmt", "-l"}, ProofRef: "presubmit:gofmt-check"},
		{Name: "go-test", Command: []string{"go", "test", "-short", "-count=1", ProofCheckPresubmitTestTimeout, "compound/internal/a", "compound/internal/b"}, ProofRef: "presubmit:go-test-short-affected"},
		{Name: "go-test-work", Command: []string{"go", "test", "-short", "-count=1", ProofCheckPresubmitTestTimeout, "compound/internal/work"}, ProofRef: "presubmit:go-test-short-affected-work"},
		{Name: "redact-public", Command: []string{"redact"}, ProofRef: "presubmit:redact-public-clean"},
	}
}

func presubmitBudgetReport() ProofCheckReport {
	return ProofCheckReport{Status: "ok", ProfileKind: ProofCheckProfilePresubmit, Presubmit: &ProofCheckPresubmit{}}
}

// TestPresubmitTestBudgetDefersWhatDoesNotFit: a go-test gate the budget cuts
// before any failure, and every later go-test gate, are deferred and named; the
// non-test gates still run; the run stays ok only because everything that ran
// is green.
func TestPresubmitTestBudgetDefersWhatDoesNotFit(t *testing.T) {
	if ProofCheckPresubmitTestBudget != 10*time.Minute {
		t.Fatalf("presubmit test budget = %s; operator decision is 10m", ProofCheckPresubmitTestBudget)
	}
	var ran []string
	run := func(ctx context.Context, step ProofCheckStep) ProofCheckStep {
		ran = append(ran, step.Name)
		if step.Name == "go-test" {
			<-ctx.Done() // a slow package: only the budget ends it
			step.Status, step.ExitCode, step.OutputTail = "failed", -1, "ok  \tcompound/internal/a\t0.1s\nsignal: killed"
			return step
		}
		step.Status = "ok"
		return step
	}
	report, err := runNonCertifyingProofGates(context.Background(), presubmitBudgetReport(), presubmitBudgetSteps(), run, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ran, []string{"gofmt", "go-test", "redact-public"}) {
		t.Fatalf("ran %v: a gate past the spent budget must not start, non-test gates must still run", ran)
	}
	if report.Status != "ok" {
		t.Fatalf("status = %s; deferral is not failure when everything that ran is green", report.Status)
	}
	want := []string{"compound/internal/a", "compound/internal/b", "compound/internal/work"}
	if !reflect.DeepEqual(report.Presubmit.Deferred(), want) || report.Presubmit.TestBudget != "50ms" {
		t.Fatalf("deferred = %v budget = %q; want %v", report.Presubmit.Deferred(), report.Presubmit.TestBudget, want)
	}
	for _, step := range report.Steps[1:3] {
		if step.Status != "deferred" || !strings.Contains(step.SkipReason, "verifier post-land verdict") || contains(report.ProofRefs, step.ProofRef) {
			t.Fatalf("deferred gate = %+v refs=%v: must be deferred, explained, and never cited as proof", step, report.ProofRefs)
		}
	}
	line := ProofCheckPresubmitVerdictLine(report, "/tmp/out.json")
	if !strings.Contains(line, "presubmit: ok ") || !strings.Contains(line, " deferred=compound/internal/a,compound/internal/b,compound/internal/work ") || !strings.Contains(line, " deferred_gates=2 ") {
		t.Fatalf("verdict line = %q", line)
	}
}

// TestPresubmitTestBudgetNeverHidesARedGate: a failure printed before the cut,
// or a failure inside the budget, stays failed and fails the run.
func TestPresubmitTestBudgetNeverHidesARedGate(t *testing.T) {
	for name, run := range map[string]func(context.Context, ProofCheckStep) ProofCheckStep{
		"failure printed before the budget cut": func(ctx context.Context, step ProofCheckStep) ProofCheckStep {
			if step.Name == "go-test" {
				<-ctx.Done()
				step.Status, step.OutputTail = "failed", "--- FAIL: TestX (0.01s)\nFAIL\tcompound/internal/a\t0.2s\nsignal: killed"
				return step
			}
			step.Status = "ok"
			return step
		},
		"failure inside the budget": func(ctx context.Context, step ProofCheckStep) ProofCheckStep {
			if step.Name == "go-test" {
				step.Status, step.ExitCode = "failed", 1
				return step
			}
			step.Status = "ok"
			return step
		},
	} {
		t.Run(name, func(t *testing.T) {
			report, err := runNonCertifyingProofGates(context.Background(), presubmitBudgetReport(), presubmitBudgetSteps(), run, 50*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			if report.Status != "failed" || report.Steps[1].Status != "failed" {
				t.Fatalf("status = %s gate = %+v; a red gate must stay red", report.Status, report.Steps[1])
			}
			if line := ProofCheckPresubmitVerdictLine(report, ""); !strings.HasPrefix(line, "presubmit: FAILED ") || !strings.Contains(line, "first_failed=go-test") {
				t.Fatalf("verdict line = %q", line)
			}
		})
	}
}

// TestPresubmitTestBudgetUnderBudgetDefersNothing: a green run inside the
// budget reports deferred=none.
func TestPresubmitTestBudgetUnderBudgetDefersNothing(t *testing.T) {
	run := func(ctx context.Context, step ProofCheckStep) ProofCheckStep {
		step.Status = "ok"
		return step
	}
	report, err := runNonCertifyingProofGates(context.Background(), presubmitBudgetReport(), presubmitBudgetSteps(), run, time.Minute)
	if err != nil || report.Status != "ok" || len(report.Presubmit.Deferred()) != 0 {
		t.Fatalf("report = %+v err = %v", report, err)
	}
	if line := ProofCheckPresubmitVerdictLine(report, ""); !strings.Contains(line, " deferred=none ") || !strings.Contains(line, " deferred_gates=0 ") {
		t.Fatalf("verdict line = %q", line)
	}
}
