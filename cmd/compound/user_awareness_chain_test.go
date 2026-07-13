package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/candidates"
	"compound/internal/contract"
	"compound/internal/distiller"
	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/home"
	"compound/internal/hook"
	"compound/internal/mem"
	"compound/internal/policy"
	"compound/internal/work"
)

func TestUserAwarenessChainSurvivesDisposableStateAndKeepsPromptSubmitModelFree(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", "")
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	workStore, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	deps := hook.Deps{
		Registry: harness.DefaultRegistry(),
		Orient:   contract.DefaultPolicy(),
		Policy:   policy.DefaultConfig(),
		Store:    workStore,
		HomePath: homePath,
	}

	preferenceText := "I prefer concise progress updates while implementation is running."
	transcriptPath := filepath.Join(homePath, "synthetic-session.jsonl")
	transcript := strings.Join([]string{
		`{"type":"user","timestamp":"2026-07-13T01:00:00Z","message":{"role":"user","content":[{"type":"text","text":"` + preferenceText + `"}]}}`,
		`{"type":"user","timestamp":"2026-07-13T01:00:01Z","message":{"role":"user","content":[{"type":"text","text":"<task-notification>Background agent completed.</task-notification>"}]}}`,
		`{"type":"assistant","timestamp":"2026-07-13T01:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"I will keep the receipt private."}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	sessionEnd, err := hook.Run(hook.Fixture{
		Hook:           "session-end",
		Harness:        "codex",
		SessionID:      "sess_user_awareness_chain",
		Cwd:            "/repo/compound",
		TranscriptPath: transcriptPath,
	}, deps, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessionEnd.Produced) != 1 || sessionEnd.Produced[0].Kind() != "receipt" {
		t.Fatalf("session end produced = %+v", sessionEnd.Produced)
	}
	receipt := sessionEnd.Produced[0].(*envelope.Receipt)
	if receipt.Privacy != "private" {
		t.Fatalf("SessionEnd receipt privacy = %q, want private", receipt.Privacy)
	}
	if len(receipt.EvidenceRefs) != 1 || !strings.HasPrefix(receipt.EvidenceRefs[0], "transcript-span:sha256:") {
		t.Fatalf("receipt span evidence = %v", receipt.EvidenceRefs)
	}
	spanRef := receipt.EvidenceRefs[0]
	if strings.Contains(spanRef, preferenceText) || strings.Contains(spanRef, transcriptPath) {
		t.Fatalf("hashed span leaked raw transcript material: %q", spanRef)
	}

	memoryStore, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := distiller.LoadPriorStateFromHome(homePath, memoryStore, distiller.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	request, err := distiller.BuildLLMExtractionRequestFromSources(
		"session-receipt:"+receipt.ID,
		distiller.LLMExtractionSources{Receipt: receipt},
		distiller.DefaultPolicy(),
		prior,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !stringSliceContains(request.SourceRefs, spanRef) {
		t.Fatalf("grounded request source refs = %v, want %s", request.SourceRefs, spanRef)
	}
	if request.SourcePrivacy != "private" {
		t.Fatalf("grounded request source privacy = %q, want private", request.SourcePrivacy)
	}
	confidence := 0.96
	response := distiller.LLMExtractionResponse{
		SchemaVersion: distiller.LLMExtractionSchemaVersion,
		TraceRef:      request.TraceRef,
		Extractor:     "offline-fixture",
		Model:         "offline-fixture-no-provider",
		Candidates: []distiller.LLMCandidate{{
			Kind:         distiller.KindMemory,
			Entity:       "user-profile:communication-style",
			Predicate:    "preference.communication",
			Object:       "prefer concise progress updates while implementation is running",
			Confidence:   &confidence,
			Derivation:   "llm_extracted",
			ClaimType:    "preference",
			EvidenceRefs: []string{spanRef},
		}},
	}
	responseJSON, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	distilled, err := distiller.Run(
		homePath,
		memoryStore,
		responseJSON,
		distiller.DefaultPolicy(),
		distiller.NewGroundedLLMExtractor(request),
		cliTestNow,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(distilled.Decisions) != 1 || distilled.Decisions[0].Verdict != candidates.Park || distilled.Decisions[0].ClaimID != "" {
		t.Fatalf("unratified extraction decision = %+v", distilled.Decisions)
	}
	parked, err := memoryStore.AllCandidates()
	if err != nil || len(parked) != 1 {
		t.Fatalf("parked candidates = %+v, err=%v", parked, err)
	}
	if parked[0].PromotionBasis != "" || parked[0].Visibility != "private" || !stringSliceContains(parked[0].EvidenceRefs, spanRef) {
		t.Fatalf("parked candidate gained authority or lost grounding: %+v", parked[0])
	}

	confirmation, err := memoryStore.RecordOperatorConfirmation(parked[0].ID, "Keep this as my communication preference.", "operator:synthetic", cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	promoted, err := memoryStore.AdjudicateExplicitConfirmation(parked[0].ID, confirmation.ID, "work_user_awareness_chain", cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Claim == nil || promoted.Claim.PromotionBasis != candidates.BasisExplicitConfirmation ||
		promoted.Claim.Visibility != "private" || confirmation.Visibility != "private" ||
		!stringSliceContains(promoted.Claim.EvidenceRefs, spanRef) ||
		!stringSliceContains(promoted.Claim.EvidenceRefs, confirmation.ID) {
		t.Fatalf("confirmed claim = %+v", promoted.Claim)
	}
	claimRef := "claim://" + promoted.Claim.ID

	matchingInput := contract.Input{
		Harness:     "codex",
		Cwd:         "/repo/compound",
		Task:        "give concise progress updates while implementation is running",
		RepoKind:    "control-plane",
		RepoKindVia: "flag",
		Mode:        "execute",
	}
	claudeInput := matchingInput
	claudeInput.Harness = "claude_code"
	protocolLogDir := t.TempDir()
	preRequests := filepath.Join(protocolLogDir, "pre-delete-requests.ndjson")
	preResponses := filepath.Join(protocolLogDir, "pre-delete-responses.ndjson")
	configureUserAwarenessBreadthSidecar(t, preRequests, preResponses)
	assertPackIncludesUserAwarenessClaim(t, homePath, matchingInput, claimRef)
	assertPackIncludesUserAwarenessClaim(t, homePath, claudeInput, claimRef)
	assertUserAwarenessProtocolCycle(t, preRequests, preResponses, 2)
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", "")
	unrelated := matchingInput
	unrelated.Task = "optimize database migration throughput"
	assertPackExcludesUserAwarenessClaim(t, homePath, unrelated, claimRef)

	indexDir := assertUserAwarenessBreadthIndex(t, homePath, promoted.Claim.ID)
	candidatePath := filepath.Join(homePath, candidates.InboxDir(candidates.Memory), parked[0].ID+".json")
	if err := os.Remove(candidatePath); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(indexDir); err != nil {
		t.Fatal(err)
	}
	remainingCandidates, err := memoryStore.AllCandidates()
	if err != nil || len(remainingCandidates) != 0 {
		t.Fatalf("candidate deletion failed: %+v err=%v", remainingCandidates, err)
	}
	if _, err := os.Stat(indexDir); !os.IsNotExist(err) {
		t.Fatalf("disposable breadth index still exists after exact deletion: %s (stat=%v)", indexDir, err)
	}

	postRequests := filepath.Join(protocolLogDir, "post-delete-requests.ndjson")
	postResponses := filepath.Join(protocolLogDir, "post-delete-responses.ndjson")
	configureUserAwarenessBreadthSidecar(t, postRequests, postResponses)
	assertPackIncludesUserAwarenessClaim(t, homePath, matchingInput, claimRef)
	assertPackIncludesUserAwarenessClaim(t, homePath, claudeInput, claimRef)
	assertUserAwarenessProtocolCycle(t, postRequests, postResponses, 2)
	rebuiltIndexDir := assertUserAwarenessBreadthIndex(t, homePath, promoted.Claim.ID)
	if rebuiltIndexDir != indexDir {
		t.Fatalf("restarted engine rebuilt unexpected index path %q, want %q", rebuiltIndexDir, indexDir)
	}
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", "")
	assertPackExcludesUserAwarenessClaim(t, homePath, unrelated, claimRef)

	assertNoUserPromptModelArtifacts(t, homePath)
	promptResult, err := hook.Run(hook.Fixture{
		Hook:      "user-prompt-submit",
		Harness:   "codex",
		SessionID: "sess_user_awareness_chain",
		TurnID:    "turn_after_confirmation",
		Cwd:       "/repo/compound",
		Task:      matchingInput.Task,
		Mode:      "execute",
		RepoKind:  "control-plane",
		Prompt:    "Please continue.",
	}, deps, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, produced := range promptResult.Produced {
		if produced.Kind() == "receipt" || produced.Kind() == "memory_candidate" {
			t.Fatalf("UserPromptSubmit produced forbidden meaning/model artifact %s", produced.Kind())
		}
	}
	assertNoUserPromptModelArtifacts(t, homePath)
}

func assertPackIncludesUserAwarenessClaim(t *testing.T, homePath string, in contract.Input, claimRef string) {
	t.Helper()
	pack, err := contract.PackWithCapabilities(in, contract.DefaultPolicy(), homePath, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if !stringSliceContains(pack.ActiveMemory, claimRef) {
		t.Fatalf("%s pack active_memory = %v, want %s", in.Harness, pack.ActiveMemory, claimRef)
	}
}

func assertPackExcludesUserAwarenessClaim(t *testing.T, homePath string, in contract.Input, claimRef string) {
	t.Helper()
	pack, err := contract.PackWithCapabilities(in, contract.DefaultPolicy(), homePath, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if stringSliceContains(pack.ActiveMemory, claimRef) {
		t.Fatalf("unrelated pack active_memory = %v, must exclude %s", pack.ActiveMemory, claimRef)
	}
}

func assertNoUserPromptModelArtifacts(t *testing.T, homePath string) {
	t.Helper()
	for _, path := range []string{
		filepath.Join(homePath, "reviews", "distiller-requests"),
		filepath.Join(homePath, "ledger", "execution-route-evidence.jsonl"),
		filepath.Join(homePath, "ledger", "provider-calls.ndjson"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("UserPromptSubmit must not create distiller/provider artifact %s (stat=%v)", path, err)
		}
	}
}
