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

func TestDistillerCandidateShapeContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "distiller-shape-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("distiller shape contract check failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected distiller-shape-contract finding")
	}
}

func TestDistillerCandidateShapeContractFailsOnThinDocs(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "LLMExtractionResponse carries candidates with kind and confidence.\n"
	if err := os.WriteFile(filepath.Join(docs, "contracts.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := checkDistillerCandidateShapeContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "candidate shape guard") {
		t.Fatalf("failure should name missing shape guard: %+v", findings)
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
func TestKnowledgePageContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "knowledge-page-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("knowledge-page contract check failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected knowledge-page-contract finding")
	}
}

func TestKnowledgePageContractFailsOnVagueDocs(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "knowledge-base pages become Evidence / Claim / Position candidates.\n"
	if err := os.WriteFile(filepath.Join(docs, "source-adapters.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := checkKnowledgePageContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "memory import-page") {
		t.Fatalf("failure should name missing import-page contract: %+v", findings)
	}
}

func TestWorkPacketContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "packet-work-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("packet work contract check failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected packet-work-contract finding")
	}
}

func TestWorkPacketContractFailsOnPointerOnlyDocs(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "Active packets point agents to the current goal packet.\n"
	if err := os.WriteFile(filepath.Join(docs, "runtime.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkWorkPacketContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "work import-packet") {
		t.Fatalf("failure should name missing import-packet contract: %+v", findings)
	}
}

func TestWorkPacketContractRequiresNoProofMutationOrPromotion(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`work import-packet` creates a packet hash proposed WorkEnvelope from markdown.\n"
	if err := os.WriteFile(filepath.Join(docs, "contracts.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkWorkPacketContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "no packet mutation") {
		t.Fatalf("failure should name missing no-mutation/no-proof guard: %+v", findings)
	}
}

func TestMCPorterTopologyContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "mcporter-topology-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("mcporter topology contract check failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected mcporter-topology-contract finding")
	}
}

func TestMCPorterTopologyContractFailsOnNormalizedOnlyDocs(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`compound capability import-topology` maps normalized topology YAML or generated resolver JSON into CapabilityGraph nodes.\n"
	if err := os.WriteFile(filepath.Join(docs, "contracts.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := checkMCPorterTopologyContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "mcporter list --json") {
		t.Fatalf("failure should name missing mcporter catalog contract: %+v", findings)
	}
}

func TestMCPorterTopologyContractRequiresNoPromoteAndNoExecution(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`compound capability import-topology` adapts `mcporter list --json` output and marks auth/error/offline servers non-surfacing.\n"
	if err := os.WriteFile(filepath.Join(docs, "source-adapters.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := checkMCPorterTopologyContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "SourceObservation/no-promote") {
		t.Fatalf("failure should name missing source/no-promote guard: %+v", findings)
	}
}

func TestMemoryEngineContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "memory-engine-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("memory engine contract check failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected memory-engine-contract finding")
	}
}

func TestMemoryEngineContractFailsOnEngineDoctorOnlyDocs(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`memory engine-doctor` and `memory engine-compare` prove a rented engine leaves ledger bytes unchanged.\n"
	if err := os.WriteFile(filepath.Join(docs, "memory.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkMemoryEngineContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "memory doctor") {
		t.Fatalf("failure should name missing memory doctor contract: %+v", findings)
	}
}

func TestMemoryEngineContractRequiresLedgerAndAuthorityGuards(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`memory doctor` observes configured sidecar via `COMPOUND_MEM_ENGINE_BIN` sync/embedding and `engine-compare` checks swap parity.\n"
	if err := os.WriteFile(filepath.Join(docs, "memory.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkMemoryEngineContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "ledger_unchanged") {
		t.Fatalf("failure should name missing ledger/no-authority guard: %+v", findings)
	}
}

func TestHarnessEntrypointContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "harness-entrypoint-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("harness entrypoint contract check failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected harness-entrypoint-contract finding")
	}
}

func TestHarnessEntrypointContractFailsWithoutOSEntrypointDocs(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`compound harness observe` records built-in and normalized harness registries as SourceObservation/no-promote evidence without installing hooks.\n"
	if err := os.WriteFile(filepath.Join(docs, "runtime.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkHarnessEntrypointContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "--os-entrypoints") {
		t.Fatalf("failure should name missing os-entrypoints contract: %+v", findings)
	}
}

func TestHarnessEntrypointContractRejectsCapabilityOverclaim(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`compound harness observe --os-entrypoints file` records SourceObservation/no-promote evidence and installs no hooks; OS entrypoints imply hook blocking, MCP, skills, and receipts.\n"
	if err := os.WriteFile(filepath.Join(docs, "contracts.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkHarnessEntrypointContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "session-start-only") {
		t.Fatalf("failure should name missing session-start-only guard: %+v", findings)
	}
}

func TestProductContractContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "product-contract-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("product contract check failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected product-contract-contract finding")
	}
}

func TestProductContractContractFailsWithoutReleaseSource(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`product import --project-contract <project.yaml>` records deterministic identity surface channel privacy and never analytics signals or causality.\n"
	if err := os.WriteFile(filepath.Join(docs, "source-adapters.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkProductContractContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "--release") {
		t.Fatalf("failure should name missing release contract: %+v", findings)
	}
}

func TestProductContractContractRejectsAnalyticsCausalityOverclaim(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`product import --project-contract <project.yaml> --release <release.yaml>` records deterministic identity surface channel privacy and creates analytics signals with causal attribution.\n"
	if err := os.WriteFile(filepath.Join(docs, "contracts.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkProductContractContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "no analytics signals or causality") {
		t.Fatalf("failure should name analytics/causality guard: %+v", findings)
	}
}

func TestCapabilityEvaluationContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "capability-evaluation-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("capability evaluation contract failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected capability-evaluation-contract finding")
	}
}

func TestCapabilityEvaluationContractFailsWhenEvalCanSurface(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`compound capability import-evaluations` creates eval nodes with evaluated_by edges and SourceObservation/no-promote evidence.\n"
	if err := os.WriteFile(filepath.Join(docs, "contracts.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkCapabilityEvaluationContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "never surface") {
		t.Fatalf("failure should name non-surfacing guard: %+v", findings)
	}
}
