package arch

import (
	"strings"
	"testing"
)

// repoRoot relative to internal/arch/ (where the test runs).
const repoRoot = "../.."

// The doctor must be GREEN against the real repo: candidate map intact, every internal/ package
// classified, public docs clean. If someone adds an unclassified package or leaks a private path
// into a tracked doc, THIS test fails in CI — that is the enforcement.
func TestDoctorGreenAgainstRealRepo(t *testing.T) {
	r := Doctor(repoRoot, false)
	if !r.OK {
		for _, f := range r.Findings {
			if f.Severity == Fail {
				t.Errorf("doctor FAIL [%s] %s", f.Check, f.Message)
			}
		}
	}
}

// The candidate-map check is pure (no fs) — every kind has one home dir, created by home, with a
// consumer. This locks the B1 invariant.
func TestCandidateMapInvariantPure(t *testing.T) {
	r := Doctor("", false)
	for _, f := range r.Findings {
		if f.Severity == Fail {
			t.Errorf("pure candidate-map FAIL: %s", f.Message)
		}
	}
}

// Every internal/ package the doctor sees is classified — i.e. classification is total. (Belt to
// the real-repo test's suspenders: assert the Info line, not just absence of Fail.)
func TestClassificationIsTotal(t *testing.T) {
	r := Doctor(repoRoot, false)
	var classified bool
	for _, f := range r.Findings {
		if f.Check == "classification" && f.Severity == Fail {
			t.Errorf("unclassified package: %s", f.Message)
		}
		if f.Check == "classification" && f.Severity == Info && strings.Contains(f.Message, "classified") {
			classified = true
		}
	}
	if !classified {
		t.Error("expected a 'all packages classified' info finding")
	}
}

// The convergence debt is reported (observable), not hidden — the coherent treatment of a duplicate
// that cannot yet be retired.
func TestConvergenceDebtIsObservable(t *testing.T) {
	r := Doctor("", false)
	for _, f := range r.Findings {
		if f.Check == "convergence" {
			return // present
		}
	}
	t.Error("convergence debt must be reported as a finding")
}

// The ratchet is honest: --strict does NOT fail now (debt is at budget, not over). If a future
// change adds a converge→retire package the count exceeds the budget and --strict fails; when a
// package converges, lower ConvergePendingBudget to lock the gain.
func TestConvergenceRatchetHoldsAtBudget(t *testing.T) {
	r := Doctor(repoRoot, true)
	for _, f := range r.Findings {
		if f.Check == "convergence" && f.Severity == Fail {
			t.Errorf("strict convergence FAIL (debt grew past budget %d): %s", ConvergePendingBudget, f.Message)
		}
	}
}
