package work

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"compound/internal/proofscope"
)

func presubmitBudgetGoTest(name, ref string, pkgs ...string) ProofCheckStep {
	return ProofCheckStep{Name: name, Command: append([]string{"go", "test", "-short", "-count=1", ProofCheckPresubmitTestTimeout}, pkgs...), ProofRef: ref}
}

// presubmitBudgetSteps is the real gate order: build, vet, doctor, then the
// changed package's own gate, then the budgeted gates, then redact.
func presubmitBudgetSteps() []ProofCheckStep {
	return []ProofCheckStep{
		{Name: "go-build", Command: []string{"go", "run", "./scripts/testbattery", "build"}, ProofRef: "presubmit:go-build-inventory"},
		{Name: "go-vet", Command: []string{"go", "run", "./scripts/testbattery", "vet"}, ProofRef: "presubmit:go-vet-inventory"},
		{Name: "architecture-doctor", Command: []string{"compound", "architecture", "doctor", "--strict"}, ProofRef: "presubmit:architecture-doctor-strict"},
		presubmitBudgetGoTest("go-test-changed", "presubmit:go-test-short-changed", "compound/internal/changed"),
		presubmitBudgetGoTest("go-test", "presubmit:go-test-short-affected", "compound/internal/arch", "compound/internal/dep"),
		presubmitBudgetGoTest("go-test-work", "presubmit:go-test-short-affected-work", "compound/internal/work"),
		{Name: "redact-public", Command: []string{"redact"}, ProofRef: "presubmit:redact-public-clean"},
	}
}

func presubmitBudgetReport() ProofCheckReport {
	plan := proofscope.PresubmitPlan{Scope: proofscope.Scope{Mode: proofscope.Affected, Changed: []string{"compound/internal/changed"}}}
	return ProofCheckReport{Status: "ok", ProfileKind: ProofCheckProfilePresubmit, Presubmit: &ProofCheckPresubmit{Plan: plan}}
}

// TestPresubmitBudgetExhaustedAfterChangedPackagesDefersOnlyDependents: the
// changed package runs to completion even though it alone outlasts the budget;
// the budget then defers only the dependents; build, vet, doctor and redact
// are never budgeted; the run is ok (exit 0) because everything that ran is green.
func TestPresubmitBudgetExhaustedAfterChangedPackagesDefersOnlyDependents(t *testing.T) {
	if ProofCheckPresubmitTestBudget != 10*time.Minute {
		t.Fatalf("presubmit test budget = %s; operator decision is 10m", ProofCheckPresubmitTestBudget)
	}
	const budget = 40 * time.Millisecond
	var ran []string
	run := func(ctx context.Context, step ProofCheckStep) ProofCheckStep {
		ran = append(ran, step.Name)
		switch step.Name {
		case "go-test-changed":
			// Longer than the whole budget: it must neither be cut nor deferred.
			select {
			case <-time.After(3 * budget):
			case <-ctx.Done():
				step.Status, step.OutputTail = "failed", "changed gate was cut by the budget"
				return step
			}
		case "go-test":
			<-ctx.Done() // a slow dependent: only the budget ends it
			step.Status, step.ExitCode, step.OutputTail = "failed", -1, "ok  \tcompound/internal/arch\t0.1s\nsignal: killed"
			return step
		}
		step.Status = "ok"
		return step
	}
	report, err := runNonCertifyingProofGates(context.Background(), presubmitBudgetReport(), presubmitBudgetSteps(), run, budget)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"go-build", "go-vet", "architecture-doctor", "go-test-changed", "go-test", "redact-public"}; !reflect.DeepEqual(ran, want) {
		t.Fatalf("ran %v; want %v (a budgeted gate past the spent budget must not start; non-test gates always run)", ran, want)
	}
	if report.Status != "ok" || report.Steps[3].Status != "ok" {
		t.Fatalf("status = %s changed gate = %+v; budget hit after the changed package must be ok (exit 0)", report.Status, report.Steps[3])
	}
	want := []string{"compound/internal/arch", "compound/internal/dep", "compound/internal/work"}
	// The sealed plan stays untouched: a native run compares it byte for byte.
	if !reflect.DeepEqual(report.PresubmitBudgetDeferred, want) || !reflect.DeepEqual(report.Presubmit, presubmitBudgetReport().Presubmit) {
		t.Fatalf("budget deferred = %v presubmit = %+v; want only dependents %v and the sealed plan untouched", report.PresubmitBudgetDeferred, report.Presubmit, want)
	}
	for _, step := range report.Steps[4:6] {
		if step.Status != "deferred" || !strings.Contains(step.SkipReason, "post-land verifier verdict") || contains(report.ProofRefs, step.ProofRef) {
			t.Fatalf("deferred gate = %+v refs=%v: must be deferred, explained, and never cited as proof", step, report.ProofRefs)
		}
	}
	line := ProofCheckPresubmitVerdictLine(report, "/tmp/out.json")
	if !strings.HasPrefix(line, "presubmit: ok ") || !strings.Contains(line, " deferred=3 ") || strings.Count(line, "deferred") != 1 {
		t.Fatalf("verdict line = %q; want ok with exactly one deferred=<count> field", line)
	}
}

// TestPresubmitChangedPackageNotRunIsNotOK: a changed-package gate that did not
// run green fails the presubmit, and the budget never defers it.
func TestPresubmitChangedPackageNotRunIsNotOK(t *testing.T) {
	for name, changedStatus := range map[string]string{"skipped": "skipped", "deferred": "deferred", "timed out": "failed"} {
		t.Run(name, func(t *testing.T) {
			run := func(ctx context.Context, step ProofCheckStep) ProofCheckStep {
				step.Status = "ok"
				if step.Name == "go-test-changed" {
					step.Status = changedStatus
				}
				return step
			}
			report, err := runNonCertifyingProofGates(context.Background(), presubmitBudgetReport(), presubmitBudgetSteps(), run, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if report.Status != "failed" || contains(report.ProofRefs, "presubmit:go-test-short-changed") {
				t.Fatalf("status = %s refs = %v; a changed package that did not run green must not be ok", report.Status, report.ProofRefs)
			}
			if line := ProofCheckPresubmitVerdictLine(report, ""); !strings.HasPrefix(line, "presubmit: FAILED ") {
				t.Fatalf("verdict line = %q", line)
			}
		})
	}
	// Even a budget already spent before the changed gate does not defer it.
	var ran []string
	run := func(ctx context.Context, step ProofCheckStep) ProofCheckStep {
		ran = append(ran, step.Name)
		step.Status = "ok"
		return step
	}
	steps := presubmitBudgetSteps()
	steps[3], steps[4] = steps[4], steps[3] // a dependent gate before the changed gate
	report, err := runNonCertifyingProofGates(context.Background(), presubmitBudgetReport(), steps, run, time.Nanosecond)
	if err != nil || report.Status != "ok" || !contains(ran, "go-test-changed") || contains(report.PresubmitBudgetDeferred, "compound/internal/changed") {
		t.Fatalf("ran %v deferred %v status %s err %v: the changed gate must run outside the budget", ran, report.PresubmitBudgetDeferred, report.Status, err)
	}
}

// TestPresubmitTestBudgetNeverHidesARedGate: a failure printed before the cut,
// or a failure inside the budget, stays failed and fails the run.
func TestPresubmitTestBudgetNeverHidesARedGate(t *testing.T) {
	for name, run := range map[string]func(context.Context, ProofCheckStep) ProofCheckStep{
		"failure printed before the budget cut": func(ctx context.Context, step ProofCheckStep) ProofCheckStep {
			if step.Name == "go-test" {
				<-ctx.Done()
				step.Status, step.OutputTail = "failed", "--- FAIL: TestX (0.01s)\nFAIL\tcompound/internal/dep\t0.2s\nsignal: killed"
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
			report, err := runNonCertifyingProofGates(context.Background(), presubmitBudgetReport(), presubmitBudgetSteps(), run, 40*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			if report.Status != "failed" || report.Steps[4].Status != "failed" {
				t.Fatalf("status = %s gate = %+v; a red gate must stay red", report.Status, report.Steps[4])
			}
			if line := ProofCheckPresubmitVerdictLine(report, ""); !strings.HasPrefix(line, "presubmit: FAILED ") || !strings.Contains(line, "first_failed=go-test") {
				t.Fatalf("verdict line = %q", line)
			}
		})
	}
}

// TestPresubmitGatesRunChangedPackagesFirst: the gate list puts the changed
// packages in their own gates ahead of every budgeted gate.
func TestPresubmitGatesRunChangedPackagesFirst(t *testing.T) {
	plan := proofscope.PresubmitPlan{
		Scope: proofscope.Scope{Mode: proofscope.Affected, Changed: []string{"compound/internal/a", "compound/internal/work"}},
		Runs: []proofscope.PresubmitRun{
			{Packages: []string{"compound/internal/a", "compound/internal/arch", "compound/internal/b", "compound/internal/work"}},
			{Packages: []string{"compound/cmd/compound"}, Run: proofscope.PresubmitCLIReaders},
		},
	}
	got := map[string][]string{}
	var order []string
	for _, step := range ProofCheckPresubmitGates(strings.Repeat("a", 40), strings.Repeat("b", 40), "compound", plan, nil) {
		if strings.HasPrefix(step.Name, "go-test") {
			order = append(order, step.Name)
			got[step.Name] = step.Command[5:]
		}
	}
	wantOrder := []string{"go-test-changed", "go-test-changed-work", "go-test", "go-test-cli-readers-cli"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("gate order = %v; want %v", order, wantOrder)
	}
	if !reflect.DeepEqual(got["go-test-changed"], []string{"compound/internal/a"}) || !reflect.DeepEqual(got["go-test-changed-work"], []string{"compound/internal/work"}) ||
		!reflect.DeepEqual(got["go-test"], []string{"compound/internal/arch", "compound/internal/b"}) {
		t.Fatalf("gate packages = %v", got)
	}
}

// TestPresubmitFullScopePlanIsNeverBudgeted: a full-scope plan has no
// changed-package gates, so the budget must not apply at all; every go-test
// gate runs and none is deferred, even when the budget is already exhausted.
func TestPresubmitFullScopePlanIsNeverBudgeted(t *testing.T) {
	report := presubmitBudgetReport()
	report.Presubmit.Plan.Scope = proofscope.Scope{Mode: proofscope.Full}
	steps := presubmitBudgetSteps()
	ran := map[string]bool{}
	run := func(ctx context.Context, step ProofCheckStep) ProofCheckStep {
		ran[step.Name] = true
		step.Status = "ok"
		return step
	}
	out, err := runNonCertifyingProofGates(context.Background(), report, steps, run, time.Nanosecond)
	if err != nil {
		t.Fatalf("full-scope run: %v", err)
	}
	for _, step := range out.Steps {
		if step.Status == "deferred" {
			t.Fatalf("full-scope plan deferred %s: %s", step.Name, step.SkipReason)
		}
		if strings.HasPrefix(step.Name, "go-test") && !ran[step.Name] {
			t.Fatalf("full-scope plan did not run %s", step.Name)
		}
	}
	if len(out.PresubmitBudgetDeferred) != 0 {
		t.Fatalf("full-scope plan recorded budget deferrals: %v", out.PresubmitBudgetDeferred)
	}
	if out.Status != "ok" {
		t.Fatalf("full-scope plan status %q", out.Status)
	}
}
