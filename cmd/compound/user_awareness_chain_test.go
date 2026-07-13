package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/breadth"
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

const userAwarenessEmbedderIdentity = "fixture:user-awareness:4:f32"

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

type userAwarenessEmbedder struct{}

func (userAwarenessEmbedder) Identity() string { return userAwarenessEmbedderIdentity }
func (userAwarenessEmbedder) Dim() int         { return 4 }
func (userAwarenessEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, 0, len(texts))
	for _, text := range texts {
		lower := strings.ToLower(text)
		if strings.Contains(lower, "concise") || strings.Contains(lower, "progress updates") {
			vectors = append(vectors, []float32{1, 0, 0, 0})
			continue
		}
		vectors = append(vectors, []float32{0, 1, 0, 0})
	}
	return vectors, nil
}

func TestUserAwarenessBreadthSidecar(t *testing.T) {
	if os.Getenv("COMPOUND_TEST_USER_AWARENESS_BREADTH") != "1" {
		return
	}
	requestLog, err := os.OpenFile(os.Getenv("COMPOUND_TEST_USER_AWARENESS_REQUEST_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	responseLog, err := os.OpenFile(os.Getenv("COMPOUND_TEST_USER_AWARENESS_RESPONSE_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		_ = requestLog.Close()
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	err = breadth.RunProtocol(
		io.TeeReader(os.Stdin, requestLog),
		io.MultiWriter(os.Stdout, responseLog),
		breadth.ProtocolConfig{
			Embedder:     userAwarenessEmbedder{},
			CompoundHome: os.Getenv("COMPOUND_HOME"),
			Stderr:       os.Stderr,
		},
	)
	_ = requestLog.Close()
	_ = responseLog.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func configureUserAwarenessBreadthSidecar(t *testing.T, requestLog, responseLog string) {
	t.Helper()
	t.Setenv("COMPOUND_TEST_USER_AWARENESS_BREADTH", "1")
	t.Setenv("COMPOUND_TEST_USER_AWARENESS_REQUEST_LOG", requestLog)
	t.Setenv("COMPOUND_TEST_USER_AWARENESS_RESPONSE_LOG", responseLog)
	t.Setenv("COMPOUND_MEM_ENGINE_EMBEDDER", userAwarenessEmbedderIdentity)
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", os.Args[0]+" -test.run=^TestUserAwarenessBreadthSidecar$")
}

func assertUserAwarenessProtocolCycle(t *testing.T, requestLog, responseLog string, minCycles int) {
	t.Helper()
	requests := readUserAwarenessProtocolLog[mem.Request](t, requestLog)
	responses := readUserAwarenessProtocolLog[mem.Response](t, responseLog)
	if len(requests) != len(responses) {
		t.Fatalf("protocol transcript requests=%d responses=%d", len(requests), len(responses))
	}
	counts := map[string]int{}
	for i, request := range requests {
		response := responses[i]
		if response.RequestID != request.RequestID || !response.OK {
			t.Fatalf("protocol exchange %d request=%+v response=%+v", i, request, response)
		}
		counts[request.Op]++
		if request.Op == mem.OpCapabilities && (response.Ready || response.CorpusGen != 0) {
			t.Fatalf("restarted breadth engine must begin at generation zero, got ready=%t generation=%d", response.Ready, response.CorpusGen)
		}
	}
	for _, op := range []string{mem.OpCapabilities, mem.OpIndex, mem.OpQuery} {
		if counts[op] < minCycles {
			t.Fatalf("protocol op %s count=%d, want at least %d (all=%v)", op, counts[op], minCycles, counts)
		}
	}
}

func readUserAwarenessProtocolLog[T any](t *testing.T, path string) []T {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rows []T
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var row T
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatalf("parse protocol log %s: %v\n%s", path, err, scanner.Text())
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func assertUserAwarenessBreadthIndex(t *testing.T, homePath, claimID string) string {
	t.Helper()
	manifests, err := filepath.Glob(filepath.Join(homePath, "indexes", "breadth", "*", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 1 {
		t.Fatalf("real breadth manifests = %v, want exactly one", manifests)
	}
	raw, err := os.ReadFile(manifests[0])
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		SchemaVersion string `json:"schema_version"`
		CorpusGen     int64  `json:"corpus_gen"`
		ClaimCount    int    `json:"claim_count"`
		EmbeddedCount int    `json:"embedded_count"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != "compound.breadth.index.v1" || manifest.CorpusGen <= 0 || manifest.ClaimCount != 1 || manifest.EmbeddedCount != 1 {
		t.Fatalf("breadth manifest = %+v", manifest)
	}
	vectors, err := os.ReadFile(filepath.Join(filepath.Dir(manifests[0]), "vectors.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(vectors, []byte(claimID)) {
		t.Fatalf("real vectors.bin does not contain promoted ledger claim %s", claimID)
	}
	return filepath.Dir(manifests[0])
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
