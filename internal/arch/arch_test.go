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
func TestDocLeakUsesSourceLeakTokensForPublicSourceSurfaces(t *testing.T) {
	repo := t.TempDir()
	for _, dir := range []string{"docs", "docs_private", "fixtures", "tools", filepath.Join("console", "src")} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "docs_private", "leak-tokens.txt"), []byte("docs-only-token\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs_private", "source-leak-tokens.txt"), []byte("private-product\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "fixtures", "example.yaml"), []byte("name: private-product\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "edge.test.ts"), []byte(`const product = "private-product";`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "console", "src", "page.tsx"), []byte(`export const label = "private-product";`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkDocLeak(repo, false)
	if len(findings) != 3 {
		t.Fatalf("findings = %+v, want fixture, tools, and console source leak failures", findings)
	}
	for _, want := range []string{
		"fixtures/example.yaml",
		"tools/edge.test.ts",
		filepath.Join("console", "src", "page.tsx"),
	} {
		var found bool
		for _, finding := range findings {
			if finding.Severity == Fail && strings.Contains(finding.Message, want) && strings.Contains(finding.Message, "private-product") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("findings should name %s token leak: %+v", want, findings)
		}
	}
}

// TestPrivateSourceBodyContractCatchesPlantedBodyAndClearsWhenRemoved is J16's
// falsifier. checkDocLeak already catches generic /Users/ path and email
// SHAPES, but only in docs/, and its source-dir pass only matches an
// operator-configured literal token list — neither ever reaches a semantic
// "this body came from brain" marker, nor a brain/operating-system path
// SHAPE landing in internal/cmd/fixtures/tools/console. This guard closes
// both gaps. The planted fixture uses a synthetic username (never the
// operator's real one) since it is written to a plain fixture file, not a
// _test.go file — the guard exempts _test.go from both checks so its own
// test source can safely describe the sentinel shapes without ever being
// live evidence against the real repo.
func TestPrivateSourceBodyContractCatchesPlantedBodyAndClearsWhenRemoved(t *testing.T) {
	repo := t.TempDir()
	srcDir := filepath.Join(repo, "internal", "planted")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plantPath := filepath.Join(srcDir, "body.md")
	planted := "source_of_truth: brain\n/Users/faketester/brain/private-note.md\n"
	if err := os.WriteFile(plantPath, []byte(planted), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := checkPrivateSourceBodyContract(repo)
	var failCount int
	for _, f := range findings {
		if f.Check == "private-source-body" && f.Severity == Fail {
			failCount++
		}
	}
	if failCount != 2 {
		t.Fatalf("expected 2 failures (source_of_truth marker + brain path prefix), got %+v", findings)
	}

	if err := os.Remove(plantPath); err != nil {
		t.Fatal(err)
	}
	clean := checkPrivateSourceBodyContract(repo)
	for _, f := range clean {
		if f.Severity == Fail {
			t.Fatalf("expected green after removing the plant, got %+v", clean)
		}
	}
}

// TestPrivateSourceBodyContractExemptsTestFiles proves the _test.go exemption
// is real: the same sentinel content in a _test.go file must not fail, since
// this guard's own tests need to describe/plant these shapes safely.
func TestPrivateSourceBodyContractExemptsTestFiles(t *testing.T) {
	repo := t.TempDir()
	srcDir := filepath.Join(repo, "internal", "planted")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plantPath := filepath.Join(srcDir, "fixture_test.go")
	planted := "source_of_truth: brain\n/Users/faketester/operating-system/notes.md\n"
	if err := os.WriteFile(plantPath, []byte(planted), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkPrivateSourceBodyContract(repo)
	for _, f := range findings {
		if f.Severity == Fail {
			t.Fatalf("expected _test.go files to be exempt, got %+v", findings)
		}
	}
}

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

// Spine is one envelope grammar plus object families the spine processes (LAYERS.md), NOT every
// package. If everything were spine, agents would treat everything as equally authoritative. This
// pins the split: exactly the ten spine packages, and the ten reclassified object families, so a
// silent re-widening of the spine fails here.
func TestSpineAndObjectFamilySplit(t *testing.T) {
	wantSpine := []string{"arch", "contract", "envelope", "harness", "home", "hook", "policy", "schema", "work", "writeback"}
	for _, pkg := range wantSpine {
		if Classification[pkg] != Spine {
			t.Errorf("%q must be classified Spine, got %q", pkg, Classification[pkg])
		}
		if !spineCore[pkg] {
			t.Errorf("%q must be in the spineCore allowlist", pkg)
		}
	}
	wantObjectFamily := []string{"capgraph", "director", "distiller", "localmodel", "observation", "panel", "product", "projection", "routeevidence", "runtimebinding"}
	for _, pkg := range wantObjectFamily {
		if Classification[pkg] != ObjectFamily {
			t.Errorf("%q must be classified ObjectFamily (a family the spine processes), got %q", pkg, Classification[pkg])
		}
		if spineCore[pkg] {
			t.Errorf("%q is an object family and must NOT be in the spineCore allowlist", pkg)
		}
	}
	var spineCount int
	for _, c := range Classification {
		if c == Spine {
			spineCount++
		}
	}
	if spineCount != len(wantSpine) {
		t.Errorf("Classification has %d Spine packages, want exactly %d (the envelope grammar); new packages are object-family/leaf/importer", spineCount, len(wantSpine))
	}
	if len(spineCore) != len(wantSpine) {
		t.Errorf("spineCore has %d entries, want exactly %d", len(spineCore), len(wantSpine))
	}
}

// The spine allowlist is a ratchet: spineCore and the Spine classification MUST name the same
// packages. Adding a package to spineCore without classifying it Spine (or classifying a package
// Spine without an allowlist entry) is drift and must FAIL the doctor. This exercises both
// directions against synthetic maps plus lockstep against the real maps.
func TestSpineCoreAllowlistRatchetCatchesDrift(t *testing.T) {
	if findings := checkSpineCoreDrift(Classification, spineCore); len(findings) != 0 {
		t.Fatalf("real spineCore and Classification must be in lockstep, got drift %+v", findings)
	}

	// Direction 1: a package is classified Spine but never allowlisted.
	findings := checkSpineCoreDrift(map[string]Class{"newpkg": Spine, "home": Spine}, map[string]bool{"home": true})
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one spine-allowlist failure", findings)
	}
	if !strings.Contains(findings[0].Message, "spineCore allowlist") ||
		!strings.Contains(findings[0].Message, "object-family/leaf/importer") {
		t.Fatalf("failure should point the author at the allowlist and object-family classes: %+v", findings)
	}

	// Direction 2: a spineCore entry is not classified Spine (map drift).
	findings = checkSpineCoreDrift(map[string]Class{"ghostcore": ObjectFamily}, map[string]bool{"ghostcore": true})
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one map-drift failure", findings)
	}
	if !strings.Contains(findings[0].Message, "map drift") || !strings.Contains(findings[0].Message, "lockstep") {
		t.Fatalf("failure should name the map drift and lockstep requirement: %+v", findings)
	}
}

// The convergence debt is reported (observable), not hidden — the coherent treatment of a duplicate
// that cannot yet be retired.
func TestStatusHotPathFailsAboveLineBudget(t *testing.T) {
	repo := t.TempDir()
	statusDir := filepath.Join(repo, "docs", "architecture")
	if err := os.MkdirAll(statusDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < MaxStatusHotPathLines+1; i++ {
		b.WriteString("line\n")
	}
	if err := os.WriteFile(filepath.Join(statusDir, "status.md"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkStatusHotPath(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one status-hot-path failure", findings)
	}
	if !strings.Contains(findings[0].Message, "docs/architecture/status.md") {
		t.Fatalf("failure should name status.md: %+v", findings[0])
	}
}

func TestStatusHotPathFailsOnProofJournalEntries(t *testing.T) {
	repo := t.TempDir()
	statusDir := filepath.Join(repo, "docs", "architecture")
	if err := os.MkdirAll(statusDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# Package & Command Status\n\n- 2026-07-05: go test ./... passed for latest proof run\n"
	if err := os.WriteFile(filepath.Join(statusDir, "status.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkStatusHotPath(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one status proof-journal failure", findings)
	}
	if !strings.Contains(findings[0].Message, "proof-journal") {
		t.Fatalf("failure should name proof-journal drift: %+v", findings[0])
	}
}

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

func TestRetiredSurfaceReferencesRequireToolResolverRetirement(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "status.md"), []byte("capability graph replaced old matrix debt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := checkRetiredSurfaceReferences(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "tool-resolver") {
		t.Fatalf("failure should name tool-resolver retirement: %+v", findings)
	}

	text := "os-tool-resolver-json source_view_authority retired_as_authority to compound://registries/capability-graph; source remains evidence_only_input.\n"
	if err := os.WriteFile(filepath.Join(docs, "status.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings = checkRetiredSurfaceReferences(repo)
	if len(findings) != 1 || findings[0].Severity == Fail {
		t.Fatalf("findings = %+v, want non-failure", findings)
	}
}

func TestPrivateAdaptationSteeringResolved(t *testing.T) {
	findings := checkPrivateAdaptationSteering(repoRoot)
	if len(findings) != 1 || findings[0].Severity == Fail {
		t.Fatalf("adaptation steering check failed against real repo: %+v", findings)
	}
	if !strings.Contains(findings[0].Message, "context-aware OS adaptation") {
		t.Fatalf("finding should name consumed adaptation steering: %+v", findings)
	}
}

func TestPrivateAdaptationSteeringFailsOnShallowRule(t *testing.T) {
	repo := writeAdaptationSteeringFixtureRepo(t,
		"# COMPOUND Goal Prompt\n\nMove everything from operating-system into Compound.\n",
		"# Operating-System Adaptation Rule\n\nCopy useful instructions.\n",
		"# Superseded Goal Prompt Pointer\n\nThis is not a second goal prompt. Use codex-goal-prompt.md. Superseded.\n",
	)
	findings := checkPrivateAdaptationSteering(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	msg := findings[0].Message
	for _, want := range []string{"context-aware adaptation", "silence condition", "proof", "retirement"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("failure should name missing %q: %+v", want, findings)
		}
	}
}

func TestPrivateAdaptationSteeringFailsOnDuplicatePromptBody(t *testing.T) {
	repo := writeAdaptationSteeringFixtureRepo(t,
		goodAdaptationGoalPromptFixture(),
		goodAdaptationRuleFixture(),
		"# COMPOUND Goal Prompt\n\n## Goal Lanes\n\nThis stale file is pretending to be active again.\n",
	)
	findings := checkPrivateAdaptationSteering(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "second active goal prompt") || !strings.Contains(findings[0].Message, "pointer-only") {
		t.Fatalf("failure should name duplicate prompt and pointer-only contract: %+v", findings)
	}
}

func TestPrivateAdaptationSteeringFailsOnStalePromptHistory(t *testing.T) {
	repo := writeAdaptationSteeringFixtureRepo(t,
		goodAdaptationGoalPromptFixture()+"\n\n## Latest Verified State (old)\nold state\n\n## Latest Verified State (older)\nolder state\n",
		goodAdaptationRuleFixture(),
		"# Superseded Goal Prompt Pointer\n\nThis not second goal prompt. Use codex-goal-prompt.md. Superseded.\n",
	)
	findings := checkPrivateAdaptationSteering(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "too many Latest Verified State") {
		t.Fatalf("failure should name stale prompt history: %+v", findings)
	}
}

func TestWorkbenchActionCardContractResolved(t *testing.T) {
	findings := checkWorkbenchActionCardContract(repoRoot)
	if len(findings) != 1 || findings[0].Severity == Fail {
		t.Fatalf("workbench action card contract failed against real repo: %+v", findings)
	}
	if !strings.Contains(findings[0].Message, "required inputs validation stop conditions") {
		t.Fatalf("finding should name action-card gates: %+v", findings)
	}
}

func TestWorkProofChainContractResolved(t *testing.T) {
	findings := checkWorkProofChainContract(repoRoot)
	if len(findings) != 1 || findings[0].Severity == Fail {
		t.Fatalf("work proof-chain contract failed against real repo: %+v", findings)
	}
	if !strings.Contains(findings[0].Message, "receipt/transition") {
		t.Fatalf("finding should describe terminal receipt/transition proof: %+v", findings)
	}
}

func TestWorkbenchActionCardContractFailsOnShallowSurface(t *testing.T) {
	repo := writeWorkbenchActionCardFixtureRepo(t, map[string]string{
		"internal/work/workbench.go":      "type WorkbenchAdjacentAction struct { ID string; RequiresOperator bool }\n",
		"internal/work/workbench_test.go": "func TestAction(t *testing.T) { _ = \"operator required\" }\n",
		"cmd/compound/main.go":            "func render() { println(\"adjacent_non_packet_action\") }\n",
		"cmd/compound/main_test.go":       "func TestRender(t *testing.T) { _ = \"adjacent_non_packet_action\" }\n",
		"docs/architecture/status.md":     "Workbench shows operator-required adjacent actions.\n",
	})
	findings := checkWorkbenchActionCardContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	msg := findings[0].Message
	for _, want := range []string{"required_inputs", "action_expected_validation", "stop_conditions"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("failure should name missing %q: %+v", want, findings)
		}
	}
}

func TestConversationPreflightImportSafetyContractResolved(t *testing.T) {
	findings := checkConversationPreflightImportSafetyContract(repoRoot)
	if len(findings) != 1 || findings[0].Severity == Fail {
		t.Fatalf("conversation preflight import-safety contract failed against real repo: %+v", findings)
	}
	if !strings.Contains(findings[0].Message, "import_safety") {
		t.Fatalf("finding should name import_safety proof: %+v", findings)
	}
}

func TestConversationPreflightImportSafetyContractFailsOnShallowReport(t *testing.T) {
	repo := writeWorkbenchActionCardFixtureRepo(t, map[string]string{
		"tools/chatgpt_conversation_preflight.ts":      "type PreflightReport = { status: \"ok\" | \"fail\" };\n",
		"tools/chatgpt_conversation_preflight.test.ts": "test(\"preflight\", () => { expect(report.status).toBe(\"ok\"); });\n",
		"internal/work/workbench.go":                   "func action() { _ = \"conversation-preflight-private-export\" }\n",
		"docs/architecture/status.md":                  "ChatGPT capture edge note: conversation preflight exists.\n",
	})
	findings := checkConversationPreflightImportSafetyContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	msg := findings[0].Message
	for _, want := range []string{
		"ImportSafetyReport",
		"report.import_safety.private_evidence_count",
		"import_safety proves private bounded evidence/candidates and zero temp promoted claims",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("failure should name missing %q: %+v", want, findings)
		}
	}
}

func TestOperatorNeedsMeCandidateAuthorityContractResolved(t *testing.T) {
	findings := checkOperatorNeedsMeCandidateAuthorityContract(repoRoot)
	if len(findings) != 1 || findings[0].Severity == Fail {
		t.Fatalf("operator needs-me candidate authority contract failed against real repo: %+v", findings)
	}
	if !strings.Contains(findings[0].Message, "memory candidate authority") {
		t.Fatalf("finding should name memory candidate authority: %+v", findings)
	}
}

func TestOperatorNeedsMeCandidateAuthorityContractFailsOnRawMemoryCounts(t *testing.T) {
	repo := writeWorkbenchActionCardFixtureRepo(t, map[string]string{
		"cmd/compound/orient_operator.go":      "func candidateInboxSummary() { _ = \"raw inbox proposed\" }\n",
		"cmd/compound/orient_operator_test.go": "func TestOrientOperator(t *testing.T) { _ = \"candidate_actionable\" }\n",
		"docs/architecture/status.md":          "Operator orient lists candidate counts.\n",
	})
	findings := checkOperatorNeedsMeCandidateAuthorityContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	msg := findings[0].Message
	for _, want := range []string{
		"memoryCandidateInboxSummary",
		"TestOrientOperatorUsesMemoryCandidateAuthorityForNeedsMe",
		"Operator needs-me candidate authority note",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("failure should name missing %q: %+v", want, findings)
		}
	}
}

func TestOperatorOrientAdjacentProgressContractResolved(t *testing.T) {
	findings := checkOperatorOrientAdjacentProgressContract(repoRoot)
	if len(findings) != 1 || findings[0].Severity == Fail {
		t.Fatalf("operator orient adjacent progress contract failed against real repo: %+v", findings)
	}
	if !strings.Contains(findings[0].Message, "held-valve adjacent progress") {
		t.Fatalf("finding should name held-valve adjacent progress: %+v", findings)
	}
}

func TestOperatorOrientAdjacentProgressContractFailsOnShallowSurface(t *testing.T) {
	repo := writeWorkbenchActionCardFixtureRepo(t, map[string]string{
		"cmd/compound/orient_operator.go":      "type orientRunning struct { OpenCount int }\n",
		"cmd/compound/orient_operator_test.go": "func TestOrientOperator(t *testing.T) { _ = \"open\" }\n",
		"docs/architecture/status.md":          "Operator orientation lists open work.\n",
	})
	findings := checkOperatorOrientAdjacentProgressContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	msg := findings[0].Message
	for _, want := range []string{"adjacent_progress", "session_context", "TestOrientOperatorSurfacesHeldValveAdjacentProgress", "Operator orientation adjacent-progress doctor note"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("failure should name missing %q: %+v", want, findings)
		}
	}
}

func TestOperatorSourceControlProjectionContractResolved(t *testing.T) {
	findings := checkOperatorSourceControlProjectionContract(repoRoot)
	if len(findings) != 1 || findings[0].Severity == Fail {
		t.Fatalf("operator source-control projection contract failed against real repo: %+v", findings)
	}
	if !strings.Contains(findings[0].Message, "source-control cleanup reviews") {
		t.Fatalf("finding should name source-control cleanup reviews: %+v", findings)
	}
}

func TestOperatorSourceControlProjectionContractFailsOnShallowSurface(t *testing.T) {
	repo := writeWorkbenchActionCardFixtureRepo(t, map[string]string{
		"cmd/compound/orient_operator.go":      "type orientNeedsMe struct { ReviewRequired int }\n",
		"cmd/compound/orient_operator_test.go": "func TestOrientOperator(t *testing.T) { _ = \"git status\" }\n",
		"docs/architecture/status.md":          "Operator orientation says check git status manually.\n",
	})
	findings := checkOperatorSourceControlProjectionContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	msg := findings[0].Message
	for _, want := range []string{"source_control", "TestOrientOperatorCarriesSourceControlReviewProjection", "Operator source-control projection note"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("failure should name missing %q: %+v", want, findings)
		}
	}
}

func TestASCDailySignalsPublicBoundaryContractResolved(t *testing.T) {
	findings := checkASCDailySignalsPublicBoundaryContract(repoRoot)
	if len(findings) != 1 || findings[0].Severity == Fail {
		t.Fatalf("ASC daily signals public boundary contract failed against real repo: %+v", findings)
	}
	if !strings.Contains(findings[0].Message, "product-signal normalization") {
		t.Fatalf("finding should name product-signal normalization: %+v", findings)
	}
}

func TestASCDailySignalsPublicBoundaryContractFailsOnPrivateCollectorDependency(t *testing.T) {
	repo := writeWorkbenchActionCardFixtureRepo(t, map[string]string{
		"docs/operations/asc-daily-signals.md": "# ASC Daily Signals\n\nUse a named private collector and launchd for ASC. This requires launchd.\n",
		"docs/architecture/status.md":          "ASC daily source note: a private collector fetches app data.\n",
	})
	findings := checkASCDailySignalsPublicBoundaryContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	msg := findings[0].Message
	for _, want := range []string{
		"any collector can produce ASC-shaped output",
		"ASC daily signals public-boundary note",
		"still contains stale phrase \"requires launchd\"",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("failure should name missing %q: %+v", want, findings)
		}
	}
}

func TestReviewSurfaceCoverageContractResolved(t *testing.T) {
	findings := checkReviewSurfaceCoverageContract(repoRoot)
	if len(findings) != 1 || findings[0].Severity == Fail {
		t.Fatalf("review surface coverage contract failed against real repo: %+v", findings)
	}
	if !strings.Contains(findings[0].Message, "decision/action coverage") {
		t.Fatalf("finding should name review affordance coverage: %+v", findings)
	}
}

func TestReviewSurfaceCoverageContractFailsOnRawArtifactList(t *testing.T) {
	repo := writeWorkbenchActionCardFixtureRepo(t, map[string]string{
		"internal/reviewartifact/queue.go":               "type QueueCounts struct { Total int `json:\"total\"` }\nfunc Queue() {}\n",
		"internal/reviewartifact/reviewartifact_test.go": "func TestQueue(t *testing.T) { _ = \"total\" }\n",
		"cmd/compound/main.go":                           "func cmdReviews() { println(\"reviews total\") }\n",
		"cmd/compound/main_test.go":                      "func TestReviews(t *testing.T) { _ = \"reviews total\" }\n",
		"docs/architecture/status.md":                    "Review queue lists review artifacts.\n",
	})
	findings := checkReviewSurfaceCoverageContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	msg := findings[0].Message
	for _, want := range []string{"decision_card_count", "review_surface", "review_required_missing_action", "fallback_summary_count"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("failure should name missing %q: %+v", want, findings)
		}
	}
}

func writeWorkbenchActionCardFixtureRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func writeAdaptationSteeringFixtureRepo(t *testing.T, prompt, rule, pointer string) string {
	t.Helper()
	repo := t.TempDir()
	planDir := filepath.Join(repo, "docs_private", "plan")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "codex-goal-prompt.md"), []byte(prompt), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "OPERATING-SYSTEM-ADAPTATION-RULE.md"), []byte(rule), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "codex-goal-prompt-compounding-runtime.md"), []byte(pointer), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func goodAdaptationGoalPromptFixture() string {
	return strings.Join([]string{
		"# COMPOUND Goal Prompt",
		"Absorb operating-system by context-aware adaptation, not copy/paste.",
		"Pick a consequential bounded move.",
		"Public Compound stays generic; private dogfood adapters feed generic doors.",
		"Held operator valves are constraints, not mission failure.",
		"Oracle is evidence, not authority.",
		"Do not report Compound as globally blocked while adjacent proof work exists.",
		"## Context-Aware Adaptation Rules",
		"Every adaptation names a silence condition, proof command, old-owner, and public/private boundary.",
		"Instruction tuning preserves trigger, action, boundary, and proof.",
	}, "\n")
}

func goodAdaptationRuleFixture() string {
	return strings.Join([]string{
		"# Operating-System Adaptation Rule",
		"## Instruction Tuning Standard",
		"- Trigger: when the rule should load.",
		"- Action: what changes.",
		"- Boundary: what it must not touch.",
		"- Silence condition: when it should not load.",
		"- Proof: the command/test/projection that consumes it.",
		"- Retirement: the old surface becomes evidence-only.",
		"Only merge and proof count as compounding system deltas.",
	}, "\n")
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

func TestHookSourceControlContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "hook-source-control-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("hook source-control contract check failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected hook-source-control-contract finding")
	}
}

func TestHookSourceControlContractFailsOnRawTargetDocs(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`hook pre-tool-use` consumes source-control status for target paths through read-only git and does not mutate repos.\n"
	if err := os.WriteFile(filepath.Join(docs, "runtime.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkHookSourceControlContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "relative target normalization") {
		t.Fatalf("failure should name missing target normalization: %+v", findings)
	}
}

func TestHookLiveParityContextSpineContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "hook-live-parity-context-spine-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("hook live parity context-spine contract check failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected hook-live-parity-context-spine-contract finding")
	}
}

func TestHookLiveParityContextSpineContractFailsOnDocsOnlyClaim(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "hook-live-parity-context-spine-contract: `hook session-start --live --parity-log` records context request, effective contract, context pack, and injection refs.\n"
	if err := os.WriteFile(filepath.Join(docs, "runtime.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkHookLiveParityContextSpineContract(repo)
	if len(findings) != 1 || findings[0].Severity != Warn {
		t.Fatalf("findings = %+v, want one warning for missing live hook source", findings)
	}
	if !strings.Contains(findings[0].Message, "cmd/compound/hook_live.go") {
		t.Fatalf("failure should name missing live hook source: %+v", findings)
	}
}

func TestHookReadinessActionContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "hook-readiness-action-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("hook readiness action contract failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected hook-readiness-action-contract finding")
	}
}

func TestHookReadinessActionContractFailsOnProseOnlyActions(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs", "architecture")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "Hook readiness authority note: contract hooks-authority reports pre_tool_use_readiness actions before any registration flip.\n"
	if err := os.WriteFile(filepath.Join(docs, "status.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "internal", "contract"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal", "contract", "contract.go"), []byte("type InstalledHookRuntimeReadiness struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "internal", "envelope"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal", "envelope", "envelope.go"), []byte("type HookReadiness struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal", "contract", "contract_test.go"), []byte("func TestReadiness(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "cmd", "compound"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "cmd", "compound", "main_test.go"), []byte("func TestHooksAuthority(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "internal", "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal", "work", "workbench.go"), []byte("func Workbench() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "cmd", "compound", "proof_check.go"), []byte("func ProofCheck() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkHookReadinessActionContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "prove_fixture_hook") ||
		!strings.Contains(findings[0].Message, "native_blocking") ||
		!strings.Contains(findings[0].Message, "codex-pretooluse-native-gate-proof") ||
		!strings.Contains(findings[0].Message, "expected_exit_code") ||
		!strings.Contains(findings[0].Message, "operator_live_adapter_required") {
		t.Fatalf("failure should name missing action cards: %+v", findings)
	}
}

func TestStandingGrantContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "standing-grant-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("standing grant contract check failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected standing-grant-contract finding")
	}
}

func TestStandingGrantContractFailsOnUnscopedDocs(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "StandingGrant lets policy allow approved actions without asking.\n"
	if err := os.WriteFile(filepath.Join(docs, "runtime.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkStandingGrantContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "target_path/budget/blast/reversibility") {
		t.Fatalf("failure should name missing scoped envelope fields: %+v", findings)
	}
}

func TestPlacementGuardContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "placement-guard-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("placement guard contract check failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected placement-guard-contract finding")
	}
}

func TestPlacementGuardContractFailsOnHookOnlyDocs(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "PlacementGuard hook blocks writes by checking owner placement.\n"
	if err := os.WriteFile(filepath.Join(docs, "runtime.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkPlacementGuardContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "confidence:block") {
		t.Fatalf("failure should name missing confidence threshold: %+v", findings)
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

func TestProductLoopOpenHypothesisContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "product-loop-open-hypothesis-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("product loop open-hypothesis check failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected product-loop-open-hypothesis-contract finding")
	}
}

func TestOperatorProductLoopProjectionContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "operator-product-loop-projection-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("operator product-loop projection check failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected operator-product-loop-projection-contract finding")
	}
}

func TestOperatorProductLoopProjectionContractFailsWithoutReadModelConsumer(t *testing.T) {
	repo := t.TempDir()
	mustWriteArchFixture(t, repo, "cmd/compound/orient_operator.go", strings.Join([]string{
		"ProductLoops *orientProductLoops",
		"json:\"product_loops,omitempty\"",
		"OpenHypothesesByMissingSignalSource",
		"ClosedReviewsByNextAction",
		"product_loops: products=%d open_hypothesis_debt=%d closed_review_actions=%d",
		"compound\", \"product\", \"loops\"",
	}, "\n"))
	mustWriteArchFixture(t, repo, "cmd/compound/orient_operator_test.go", strings.Join([]string{
		"TestOrientOperatorCarriesProductLoopEvidenceDebtProjection",
		"seedOrientProductLoopProjectionHome",
		"missing_signal_source=posthog",
		"closed_review_next=keep screenshot one more window",
		"compound product loops",
	}, "\n"))
	mustWriteArchFixture(t, repo, "docs/architecture/status.md", strings.Join([]string{
		"Operator product-loop projection note",
		"`compound orient --operator --json.product_loops`",
		"`product.BuildLoopModel` read model",
		"`compound product loops` as drill-down",
	}, "\n"))
	findings := checkOperatorProductLoopProjectionContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want missing product.BuildLoopModel failure", findings)
	}
	if !strings.Contains(findings[0].Message, "product.BuildLoopModel") ||
		!strings.Contains(findings[0].Message, "buildOrientProductLoops") {
		t.Fatalf("failure should name missing read-model consumer: %+v", findings)
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

func TestSourceObservationVerdictContractResolved(t *testing.T) {
	findings := checkSourceObservationVerdictContract(repoRoot)
	if len(findings) != 1 || findings[0].Severity == Fail {
		t.Fatalf("source observation verdict contract failed against real repo: %+v", findings)
	}
}

func TestPanelCapabilityFeedbackContractResolved(t *testing.T) {
	findings := checkPanelCapabilityFeedbackContract(repoRoot)
	if len(findings) != 1 || findings[0].Severity == Fail {
		t.Fatalf("panel capability feedback contract failed against real repo: %+v", findings)
	}
}

func TestExternalRepoContextEvaluationContractResolved(t *testing.T) {
	findings := checkExternalRepoContextEvaluationContract(repoRoot)
	if len(findings) != 1 || findings[0].Severity == Fail {
		t.Fatalf("external repo context-evaluation contract failed against real repo: %+v", findings)
	}
}

func TestExternalRepoContextEvaluationContractFailsWithoutDecisionCard(t *testing.T) {
	repo := t.TempDir()
	mustWriteArchFixture(t, repo, "internal/capgraph/external_repo_intake.go", strings.Join([]string{
		"ExternalRepoContextEvaluationStep",
		"EvaluationPlan",
		"externalRepoContextEvaluationPlan",
		"existing-owner-fit",
		"capability-shape-fit",
		"risk-cost-fit",
		"Context-Aware Evaluation Plan",
		"silence:",
		"proof:",
		"retirement:",
	}, "\n"))
	mustWriteArchFixture(t, repo, "internal/capgraph/external_repo_review.go", strings.Join([]string{
		"evaluation lens: ",
		"evaluation_step_count",
		"no clone/install/serve/auth/graph mutation/memory promotion",
		"externalRepoIntakeDecisionChoices",
	}, "\n"))
	mustWriteArchFixture(t, repo, "internal/capgraph/external_repo_intake_test.go", strings.Join([]string{
		"item.EvaluationPlan",
		"hasExternalRepoEvaluationStep",
		"harness-fit",
		"DecisionCard.Meta",
		"evaluation lens: harness-fit",
	}, "\n"))
	mustWriteArchFixture(t, repo, "internal/capgraph/external_repo_evaluation_plan.go", strings.Join([]string{
		"contextPlan := externalRepoContextEvaluationPlan(report.Repo, match)",
		"externalRepoContextEvaluationSignals(contextPlan)",
		"context_evaluation_proofs",
		"context_evaluation_retirement",
		"context_evaluation_step_count",
	}, "\n"))
	mustWriteArchFixture(t, repo, "internal/capgraph/evaluation_plan.go", strings.Join([]string{
		"signalStringSlice(eval.Signals, \"context_evaluation_lenses\")",
		"context lens ",
		"silence condition reviewed",
		"context evaluation proof: ",
	}, "\n"))
	mustWriteArchFixture(t, repo, "internal/capgraph/evaluation_review_queue.go", strings.Join([]string{
		"signalStringSlice(plan.Signals, \"context_evaluation_lenses\")",
		"context_evaluation_silence",
		"context_evaluation_retirement",
		"indexedSignal",
		"evaluation lens: ",
	}, "\n"))
	mustWriteArchFixture(t, repo, "docs/architecture/status.md", strings.Join([]string{
		"External repo context-evaluation note",
		"`compound reviews --area external-repo-intake --json`",
		"context-aware evaluation plan",
		"trigger/action/boundary/silence/proof/retirement",
		"capability-evaluation plans and decision cards",
		"`context lens ...` review gates",
		"`evaluation lens: ...` reasons",
		"no clone/install/serve/auth/graph mutation/memory promotion",
	}, "\n"))
	findings := checkExternalRepoContextEvaluationContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "externalRepoIntakeDecisionCard") {
		t.Fatalf("failure should name missing decision-card builder: %+v", findings)
	}
}

func TestExternalRepoContextEvaluationContractFailsWithoutCapabilityEvaluationPropagation(t *testing.T) {
	repo := t.TempDir()
	mustWriteArchFixture(t, repo, "internal/capgraph/external_repo_intake.go", strings.Join([]string{
		"ExternalRepoContextEvaluationStep",
		"EvaluationPlan",
		"externalRepoContextEvaluationPlan",
		"existing-owner-fit",
		"capability-shape-fit",
		"risk-cost-fit",
		"Context-Aware Evaluation Plan",
		"silence:",
		"proof:",
		"retirement:",
	}, "\n"))
	mustWriteArchFixture(t, repo, "internal/capgraph/external_repo_review.go", strings.Join([]string{
		"externalRepoIntakeDecisionCard",
		"evaluation lens: ",
		"evaluation_step_count",
		"no clone/install/serve/auth/graph mutation/memory promotion",
		"externalRepoIntakeDecisionChoices",
	}, "\n"))
	mustWriteArchFixture(t, repo, "internal/capgraph/external_repo_intake_test.go", strings.Join([]string{
		"item.EvaluationPlan",
		"hasExternalRepoEvaluationStep",
		"harness-fit",
		"DecisionCard.Meta",
		"evaluation lens: harness-fit",
	}, "\n"))
	mustWriteArchFixture(t, repo, "internal/capgraph/external_repo_evaluation_plan.go", strings.Join([]string{
		"contextPlan := externalRepoContextEvaluationPlan(report.Repo, match)",
		"externalRepoContextEvaluationSignals(contextPlan)",
		"context_evaluation_proofs",
		"context_evaluation_retirement",
		"context_evaluation_step_count",
	}, "\n"))
	mustWriteArchFixture(t, repo, "internal/capgraph/evaluation_plan.go", strings.Join([]string{
		"signalStringSlice(eval.Signals, \"context_evaluation_lenses\")",
		"context lens ",
		"silence condition reviewed",
		"context evaluation proof: ",
	}, "\n"))
	mustWriteArchFixture(t, repo, "internal/capgraph/evaluation_review_queue.go", strings.Join([]string{
		"signalStringSlice(plan.Signals, \"context_evaluation_lenses\")",
		"context_evaluation_silence",
		"context_evaluation_retirement",
		"indexedSignal",
		"evaluation lens: ",
	}, "\n"))
	mustWriteArchFixture(t, repo, "docs/architecture/status.md", strings.Join([]string{
		"External repo context-evaluation note",
		"`compound reviews --area external-repo-intake --json`",
		"context-aware evaluation plan",
		"trigger/action/boundary/silence/proof/retirement",
		"capability-evaluation plans and decision cards",
		"`context lens ...` review gates",
		"`evaluation lens: ...` reasons",
		"no clone/install/serve/auth/graph mutation/memory promotion",
	}, "\n"))

	findings := checkExternalRepoContextEvaluationContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "external_repo_evaluation_plan.go") ||
		!strings.Contains(findings[0].Message, "context_evaluation_lenses") {
		t.Fatalf("failure should name missing capability-evaluation context propagation: %+v", findings)
	}
}

func TestExternalRepoIntakeStateProjectionContractResolved(t *testing.T) {
	findings := checkExternalRepoIntakeStateProjectionContract(repoRoot)
	if len(findings) != 1 || findings[0].Severity == Fail {
		t.Fatalf("external repo intake state projection contract failed against real repo: %+v", findings)
	}
}

func TestExternalRepoIntakeStateProjectionContractFailsWithoutOpenOwnerDebt(t *testing.T) {
	repo := t.TempDir()
	mustWriteArchFixture(t, repo, "internal/capgraph/external_repo_intake.go", strings.Join([]string{
		`json:"open_review_count"`,
		"annotateExternalRepoDecisionState",
		"countPendingExternalRepoOwnerReviews",
		"ExternalRepoCapabilityEvaluationStatus",
		"ExternalRepoCapabilityGraphStatus",
	}, "\n"))
	mustWriteArchFixture(t, repo, "internal/capgraph/external_repo_intake_test.go", strings.Join([]string{
		"default report should keep raw decision count but clear owner-review debt",
		`CapabilityEvaluationStatus != "pending"`,
		`CapabilityGraphStatus != "blocked_on_capability_evaluation"`,
	}, "\n"))
	mustWriteArchFixture(t, repo, "cmd/compound/capability.go", strings.Join([]string{
		"keep_needs_review_decision",
		"high_value: %s decision=%s lane=%s owner_review=%s capability_evaluation=%s graph=%s owner=%s",
	}, "\n"))
	mustWriteArchFixture(t, repo, "cmd/compound/main_test.go", strings.Join([]string{
		"default external-intake text should distinguish raw decision count from open owner-review debt",
		"owner_review=recorded capability_evaluation=pending graph=blocked_on_capability_evaluation",
	}, "\n"))
	mustWriteArchFixture(t, repo, "docs/architecture/status.md", strings.Join([]string{
		"Default `capability external-intake` separates raw `keep_needs_review` classification from actual `open_owner_reviews`",
		"high-value rows show owner-review, capability-evaluation, and graph status",
		"instead of calling recorded/imported rows review-next work",
	}, "\n"))
	findings := checkExternalRepoIntakeStateProjectionContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "open_owner_reviews") {
		t.Fatalf("failure should name missing open owner-review debt label: %+v", findings)
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

func TestCapabilityEvaluationContractFailsWithoutReviewGate(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`compound capability import-evaluations` imports evidence-only eval records with evaluated_by edges, SourceObservation/no-promote evidence, and eval records never surface as agent offers.\n"
	if err := os.WriteFile(filepath.Join(docs, "contracts.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkCapabilityEvaluationContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "evaluation-review") || !strings.Contains(findings[0].Message, "import-skills") {
		t.Fatalf("failure should name review gate and import boundary: %+v", findings)
	}
}

func TestCapabilityEvaluationContractFailsWhenReviewMutatesGraph(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := strings.Join([]string{
		"`compound capability import-evaluations` imports evidence-only eval records with evaluated_by edges, SourceObservation/no-promote evidence, and eval records never surface as agent offers.",
		"`compound capability evaluation-plan` writes a .review.yaml review YAML.",
		"`compound capability evaluation-review <review.yaml>` validates an edited decision with rationale and confidence, writes a reviewed manifest without mutating graph state, but evaluation-review mutates graph state directly.",
		"Only `import-skills` mutates the graph after a reviewed manifest exists.",
	}, "\n")
	if err := os.WriteFile(filepath.Join(docs, "contracts.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkCapabilityEvaluationContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "evaluation-review mutates") {
		t.Fatalf("failure should name direct mutation contradiction: %+v", findings)
	}
}

func TestConversationMiningContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "conversation-mining-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("conversation mining contract failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected conversation-mining-contract finding")
	}
}

func TestConversationMiningContractFailsWithoutGates(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`compound distiller import-conversation-mining` imports mined conversations for Dream.\n"
	if err := os.WriteFile(filepath.Join(docs, "contracts.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkConversationMiningContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "lossiness-aware") || !strings.Contains(findings[0].Message, "improvement-plan") || !strings.Contains(findings[0].Message, "memory promotion") {
		t.Fatalf("failure should name missing lossiness/no-promotion guards: %+v", findings)
	}
}

func TestConversationCoverageContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "conversation-coverage-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("conversation coverage contract failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected conversation-coverage-contract finding")
	}
}

func TestConversationCoverageContractFailsWithoutGates(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`compound distiller import-conversation-coverage` imports coverage rows for Dream.\n"
	if err := os.WriteFile(filepath.Join(docs, "contracts.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkConversationCoverageContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "summary-only") || !strings.Contains(findings[0].Message, "no row import") || !strings.Contains(findings[0].Message, "memory promotion") {
		t.Fatalf("failure should name coverage gates: %+v", findings)
	}
}

func TestDecisionSourceAuthorityContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "decision-source-authority-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("decision source authority contract failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected decision-source-authority-contract finding")
	}
}

func TestDecisionSourceAuthorityContractFailsWithoutCoverageAndRuntimeDedupe(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`decisions import --policy-dir <dir>` imports DecisionLifecycle records.\n"
	if err := os.WriteFile(filepath.Join(docs, "contracts.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkDecisionSourceAuthorityContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "source-authority coverage") || !strings.Contains(findings[0].Message, "runtime decision:// dedupe") {
		t.Fatalf("failure should name missing authority/runtime guards: %+v", findings)
	}
}

func TestDistillerImprovementReviewContractFailsWithoutGate(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`compound distiller improvement-plan` writes a review note but applies method changes.\n"
	if err := os.WriteFile(filepath.Join(docs, "contracts.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkDistillerImprovementReviewContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "eval_proof_refs") || !strings.Contains(findings[0].Message, "gated review YAML") {
		t.Fatalf("failure should name missing review/proof guards: %+v", findings)
	}
}

func TestEpistemicsDoctrineResolved(t *testing.T) {
	findings := checkEpistemicsDoctrine(repoRoot)
	if len(findings) != 1 || findings[0].Severity == Fail {
		t.Fatalf("epistemics doctrine check failed against real repo: %+v", findings)
	}
}

func TestEpistemicsDoctrineFailsWhenDocMissing(t *testing.T) {
	repo := writeEpistemicsFixtureRepo(t, epistemicsFixtureRows())
	if err := os.Remove(filepath.Join(repo, "docs", "architecture", "epistemics.md")); err != nil {
		t.Fatal(err)
	}
	findings := checkEpistemicsDoctrine(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want missing-doc failure", findings)
	}
	if !strings.Contains(findings[0].Message, "epistemics.md") {
		t.Fatalf("failure should name epistemics.md: %+v", findings)
	}
}

func TestEpistemicsDoctrineFailsWithTooFewRows(t *testing.T) {
	repo := writeEpistemicsFixtureRepo(t, epistemicsFixtureRows()[:6])
	findings := checkEpistemicsDoctrine(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want row-count failure", findings)
	}
	if !strings.Contains(findings[0].Message, "6 doctrine row") {
		t.Fatalf("failure should name parsed row count: %+v", findings)
	}
}

func TestEpistemicsDoctrineFailsForPhantomPackage(t *testing.T) {
	rows := epistemicsFixtureRows()
	rows[2] = "| 3 | contradiction review | internal/ghost | compound synthesis sweep |"
	repo := writeEpistemicsFixtureRepo(t, rows)
	findings := checkEpistemicsDoctrine(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want phantom package failure", findings)
	}
	if !strings.Contains(findings[0].Message, "internal/ghost") || !strings.Contains(findings[0].Message, "not found") {
		t.Fatalf("failure should name missing internal package: %+v", findings)
	}
}

func TestEpistemicsDoctrineFailsForPhantomCommandVerb(t *testing.T) {
	rows := epistemicsFixtureRows()
	rows[4] = "| 5 | route evidence is not task success | internal/routeevidence | compound ghost-command run |"
	repo := writeEpistemicsFixtureRepo(t, rows)
	findings := checkEpistemicsDoctrine(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want phantom command failure", findings)
	}
	if !strings.Contains(findings[0].Message, "ghost-command") || !strings.Contains(findings[0].Message, "cmd/compound/main.go") {
		t.Fatalf("failure should name missing command case: %+v", findings)
	}
}

func writeEpistemicsFixtureRepo(t *testing.T, rows []string) string {
	t.Helper()
	repo := t.TempDir()
	for _, dir := range []string{
		"cmd/compound",
		"docs/architecture",
		"internal/arch",
		"internal/candidates",
		"internal/capgraph",
		"internal/contract",
		"internal/distiller",
		"internal/envelope",
		"internal/harness",
		"internal/knowledge",
		"internal/mem",
		"internal/panel",
		"internal/product",
		"internal/routeevidence",
		"internal/work",
	} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mainGo := `package main
func main() {
	switch "" {
	case "architecture":
	case "capability":
	case "contract":
	case "memory":
	case "route":
	case "synthesis":
	case "workbench":
	}
}
`
	if err := os.WriteFile(filepath.Join(repo, "cmd", "compound", "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("| n | principle | implementing_object | proof_command |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, row := range rows {
		b.WriteString(row)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "architecture", "epistemics.md"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func epistemicsFixtureRows() []string {
	return []string{
		"| 1 | expectations become contracts | internal/contract, internal/harness | compound contract compile |",
		"| 2 | one root identity closes proof | internal/work, internal/envelope | compound workbench |",
		"| 3 | contradiction reopens review | internal/mem | compound synthesis sweep |",
		"| 4 | capability offers do not execute | internal/capgraph, internal/panel | compound capability surface |",
		"| 5 | route evidence is not task success | internal/routeevidence, internal/product | compound route outcome-review |",
		"| 6 | history becomes evidence | internal/knowledge, internal/distiller, internal/candidates | compound memory recall |",
		"| 7 | public core gated intake | internal/arch, internal/capgraph | compound architecture doctor |",
	}
}

func TestPluginServingContractResolved(t *testing.T) {
	r := Doctor(repoRoot, false)
	var saw bool
	for _, f := range r.Findings {
		if f.Check != "plugin-serving-contract" {
			continue
		}
		saw = true
		if f.Severity == Fail {
			t.Fatalf("plugin serving contract failed against real repo: %s", f.Message)
		}
	}
	if !saw {
		t.Fatal("expected plugin-serving-contract finding")
	}
}

func TestPluginServingContractFailsWhenDocsMakePluginsExecutable(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "`compound capability import-skills --os-plugin-board-json <skills-selection-board.json>` imports plugins as active offers.\n"
	if err := os.WriteFile(filepath.Join(docs, "contracts.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := checkPluginServingContract(repo)
	if len(findings) != 1 || findings[0].Severity != Fail {
		t.Fatalf("findings = %+v, want one failure", findings)
	}
	if !strings.Contains(findings[0].Message, "retired adapter") || !strings.Contains(findings[0].Message, "non-surfacing") {
		t.Fatalf("failure should name retired/non-surfacing plugin boundary: %+v", findings)
	}
}

func mustWriteArchFixture(t *testing.T, repo, rel, text string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
