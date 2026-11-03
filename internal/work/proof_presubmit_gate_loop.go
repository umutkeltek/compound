package work

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// ProofCheckPresubmitTestBudget is the presubmit's total wall-clock budget for
// its go-test gates (operator decision 2026-11-03). Each package keeps its own
// -timeout. A gate that cannot start inside the budget, or that the budget cuts
// before any test failure printed, is reported as deferred: its packages are
// named in the verdict line and the --out report and run on the post-land verifier
// verifier. Nothing is silently skipped, and exit 0 still requires every gate
// that ran to be green.
const ProofCheckPresubmitTestBudget = 10 * time.Minute

var errPresubmitTestBudget = errors.New("presubmit test budget spent")

// presubmitTestFailure matches go test output that proves a failure already
// happened before a budget cut: such a gate stays failed, never deferred.
var presubmitTestFailure = regexp.MustCompile(`(?m)^(--- FAIL|FAIL\t|panic: )`)

// RunNonCertifyingProofGates preserves existing development/presubmit selection.
// Native execution adds durable checkpoints without changing the gate commands.
func RunNonCertifyingProofGates(ctx context.Context, report ProofCheckReport, steps []ProofCheckStep, run func(context.Context, ProofCheckStep) ProofCheckStep) (ProofCheckReport, error) {
	return runNonCertifyingProofGates(ctx, report, steps, run, ProofCheckPresubmitTestBudget)
}

func runNonCertifyingProofGates(ctx context.Context, report ProofCheckReport, steps []ProofCheckStep, run func(context.Context, ProofCheckStep) ProofCheckStep, budget time.Duration) (ProofCheckReport, error) {
	_, native := PresubmitExecutionFromContext(ctx)
	budgeted := report.Presubmit != nil && budget > 0
	if budgeted {
		report.Presubmit.TestBudget = budget.String()
	}
	var testSpent time.Duration
	for _, step := range steps {
		if native && ctx.Err() != nil {
			report.Status = "failed"
			return report, context.Cause(ctx)
		}
		if err := PresubmitGateStarted(ctx, step, &report); err != nil {
			report.Status = "failed"
			return report, err
		}
		result := step
		switch {
		case report.DryRun:
			result.Status = "planned"
		case budgeted && strings.HasPrefix(step.Name, "go-test"):
			result, testSpent = runBudgetedPresubmitTestGate(ctx, &report, step, run, budget, testSpent)
		default:
			result = run(ctx, step)
		}
		if result.Status == "failed" {
			report.Status = "failed"
		}
		if result.Status == "ok" || result.Status == "planned" {
			report.ProofRefs = append(report.ProofRefs, result.ProofRef)
		}
		report.Steps = append(report.Steps, result)
		if err := PresubmitGateCompleted(ctx, result, &report); err != nil {
			report.Status = "failed"
			return report, err
		}
	}
	return report, nil
}

// runBudgetedPresubmitTestGate runs one go-test gate inside what remains of the
// budget and returns the result plus the budget spent so far.
func runBudgetedPresubmitTestGate(ctx context.Context, report *ProofCheckReport, step ProofCheckStep, run func(context.Context, ProofCheckStep) ProofCheckStep, budget, spent time.Duration) (ProofCheckStep, time.Duration) {
	remaining := budget - spent
	if remaining <= 0 {
		return deferPresubmitTestGate(report, step, fmt.Sprintf("presubmit test budget %s spent before this gate; the verifier post-land verdict covers it", budget)), spent
	}
	gateCtx, cancel := context.WithTimeoutCause(ctx, remaining, errPresubmitTestBudget)
	started := time.Now()
	result := run(gateCtx, step)
	cut := errors.Is(context.Cause(gateCtx), errPresubmitTestBudget) && ctx.Err() == nil
	cancel()
	spent += time.Since(started)
	if cut && result.Status == "failed" && !presubmitGateShowsTestFailure(result) {
		return deferPresubmitTestGate(report, result, fmt.Sprintf("presubmit test budget %s cut this gate before any test failed; the verifier post-land verdict covers it", budget)), spent
	}
	return result, spent
}

func deferPresubmitTestGate(report *ProofCheckReport, step ProofCheckStep, reason string) ProofCheckStep {
	step.Status, step.SkipReason, step.ExitCode = "deferred", reason, 0
	for _, arg := range step.Command {
		if strings.HasPrefix(arg, modulePrefix) && !stringIn(report.Presubmit.BudgetDeferred, arg) {
			report.Presubmit.BudgetDeferred = append(report.Presubmit.BudgetDeferred, arg)
		}
	}
	return step
}

const modulePrefix = "compound/"

func presubmitGateShowsTestFailure(step ProofCheckStep) bool {
	for _, line := range step.FailureLines {
		if presubmitTestFailure.MatchString(line) {
			return true
		}
	}
	if presubmitTestFailure.MatchString(step.OutputTail) {
		return true
	}
	if step.OutputPath != "" {
		if body, err := os.ReadFile(step.OutputPath); err == nil && presubmitTestFailure.Match(body) {
			return true
		}
	}
	return false
}
