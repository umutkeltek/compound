package arch

import (
	"os"
	"path/filepath"
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

func TestRetiredSurfaceReferencesResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "retired-surface" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("retired surface check failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected retired-surface finding")
	}
}

func TestRetiredSurfaceReferencesFailOnPendingSkillMatrix(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "roadmap.md"), []byte("pending old skill matrix\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := checkRetiredSurfaceReferences(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
}

func TestDistillerDuplicateContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "distiller-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("distiller contract check failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected distiller-contract finding")
	}
}

func TestDistillerDuplicateContractFailsOnMemoryOnlyDocs(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`distiller run` now records duplicate memory outputs as idempotent `NoPromoteDecision` rows.\n"
	if err := os.WriteFile(filepath.Join(docs, "status.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := checkDistillerDuplicateContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "non-memory duplicate candidate") {
		t.Fatalf("failure should name missing candidate coverage: %+v", findings)
	}
}

func TestDistillerDuplicateContractRequiresCandidateCoverage(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "status.md"), []byte("distiller duplicate handling exists\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := checkDistillerDuplicateContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "non-memory duplicate candidate") {
		t.Fatalf("failure should name missing candidate coverage: %+v", findings)
	}
}

func TestLLMExtractionGroundingContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "llm-grounding-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("llm grounding contract check failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected llm-grounding-contract finding")
	}
}

func TestLLMExtractionGroundingContractFailsOnGenericExtractorDocs(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "OpenAICompatibleExtraction validates returned LLMExtractionResponse through LLMExtractor.\n"
	if err := os.WriteFile(filepath.Join(docs, "contracts.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := checkLLMExtractionGroundingContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "request-grounded LLM extraction") {
		t.Fatalf("failure should name missing request grounding: %+v", findings)
	}
}

func TestLLMExtractionGroundingContractRequiresEvidenceAndSourceRefs(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "Eval/live extraction is request-grounded, but this stale doc omits the evidence gate.\n"
	if err := os.WriteFile(filepath.Join(docs, "contracts.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := checkLLMExtractionGroundingContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "evidence_refs/source_refs") {
		t.Fatalf("failure should name missing refs: %+v", findings)
	}
}

func TestRouteOutcomeContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "route-outcome-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("route outcome contract check failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected route-outcome-contract finding")
	}
}

func TestRouteOutcomeContractFailsOnMetadataOnlyDocs(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`compound route import` records ExecutionRouteEvidence cost and selected target metadata.\n"
	if err := os.WriteFile(filepath.Join(docs, "status.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkRouteOutcomeContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "receipt-linked route outcome review") {
		t.Fatalf("failure should name missing outcome review: %+v", findings)
	}
}

func TestRouteOutcomeContractRequiresStateDeltaAndNoPromote(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`compound route outcome-review` joins route evidence to a Receipt and does not mutate gateway/provider/model-router config.\n"
	if err := os.WriteFile(filepath.Join(docs, "contracts.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkRouteOutcomeContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "state_delta/no-promote") {
		t.Fatalf("failure should name missing state/no-promote guard: %+v", findings)
	}
}
