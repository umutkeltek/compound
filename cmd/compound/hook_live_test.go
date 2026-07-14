package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"compound/internal/authority"
	"compound/internal/contract"
	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/hook"
	"compound/internal/policy"
	"compound/internal/retrievalparity"
	"compound/internal/work"
)

func TestHookLiveSessionStartEmitsAdditionalContext(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	input := `{"hook_event_name":"SessionStart","session_id":"sess_live","cwd":"/repo/product","source":"startup"}`
	var code int
	out := captureStdout(t, func() {
		withStdin(t, input, func() {
			code = runHookCommand([]string{"session-start", "--live", "--harness", "claude-code"})
		})
	})
	if code != 0 {
		t.Fatalf("live session-start exit = %d\n%s", code, out)
	}
	var resp claudeLiveHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("parse live response: %v\n%s", err, out)
	}
	got := resp.HookSpecificOutput
	if got.HookEventName != "SessionStart" || strings.TrimSpace(got.AdditionalContext) == "" {
		t.Fatalf("live session-start response = %+v", got)
	}
}

func TestHookLiveSessionStartOrientErrorTripsBreaker(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	configDir := filepath.Join(homePath, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "repo-kinds.yaml"), []byte("schema_version: definitely-wrong\nrepos: []\n"), 0o644); err != nil {
		t.Fatalf("write malformed repo-kind registry: %v", err)
	}

	input := `{"hook_event_name":"SessionStart","session_id":"sess_breaker_error","cwd":"/repo/product","source":"startup"}`
	var code int
	out := captureStdout(t, func() {
		withStdin(t, input, func() {
			code = runHookCommand([]string{"session-start", "--live", "--harness", "claude-code"})
		})
	})
	if code != 0 {
		t.Fatalf("live session-start exit = %d, want fail-open 0\n%s", code, out)
	}
	var resp claudeLiveHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("parse live response: %v\n%s", err, out)
	}
	if resp.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("hook event name = %q, want SessionStart", resp.HookSpecificOutput.HookEventName)
	}
	if resp.HookSpecificOutput.AdditionalContext != "" {
		t.Fatalf("additionalContext = %q, want empty on breaker trip", resp.HookSpecificOutput.AdditionalContext)
	}
	stream, err := os.ReadFile(filepath.Join(homePath, "events", "hook-events.ndjson"))
	if err != nil {
		t.Fatalf("read hook event stream: %v", err)
	}
	if !strings.Contains(string(stream), `"breaker_tripped":true`) || !strings.Contains(string(stream), `"status":"fail_open"`) {
		t.Fatalf("hook event stream missing breaker telemetry:\n%s", string(stream))
	}
	if rows, err := retrievalparity.LoadJournal(homePath); err != nil {
		t.Fatalf("load breaker retrieval journal: %v", err)
	} else if len(rows) != 0 {
		t.Fatalf("breaker-tripped hook journaled retrieval evidence: %+v", rows)
	}
}

func TestHookLiveCodexSessionStartEmitsCodexContext(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	input := `{"hook_event_name":"SessionStart","session_id":"sess_codex_live","cwd":"/repo/control","prompt":"continue Compound hook adapter work"}`
	var code int
	out := captureStdout(t, func() {
		withStdin(t, input, func() {
			code = runHookCommand([]string{"session-start", "--live", "--harness", "codex"})
		})
	})
	if code != 0 {
		t.Fatalf("codex live session-start exit = %d\n%s", code, out)
	}
	var resp codexLiveHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("parse codex live response: %v\n%s", err, out)
	}
	if resp.SchemaVersion != "compound.codex_live_hook_response.v0.1" ||
		resp.HookName != "SessionStart" ||
		resp.Status != "ok" ||
		resp.EventID == "" ||
		strings.TrimSpace(resp.AdditionalContext) == "" {
		t.Fatalf("codex live session-start response = %+v", resp)
	}
}

func TestHookLiveSessionStartParityRecordsContextSpineRefs(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	parityDir := t.TempDir()
	input := `{"hook_event_name":"SessionStart","session_id":"sess_live_context","cwd":"/repo/control","prompt":"continue held-valve-adjacent Compound work"}`
	var code int
	out := captureStdout(t, func() {
		withStdin(t, input, func() {
			code = runHookCommand([]string{"session-start", "--live", "--harness", "claude-code", "--parity-log", parityDir})
		})
	})
	if code != 0 {
		t.Fatalf("live session-start exit = %d\n%s", code, out)
	}
	var resp claudeLiveHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("parse live response: %v\n%s", err, out)
	}
	if strings.TrimSpace(resp.HookSpecificOutput.AdditionalContext) == "" {
		t.Fatalf("live session-start did not emit additional context: %+v", resp.HookSpecificOutput)
	}
	entries, err := os.ReadDir(parityDir)
	if err != nil {
		t.Fatalf("read parity dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("parity entries = %d, want 1", len(entries))
	}
	parityBytes, err := os.ReadFile(filepath.Join(parityDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read parity record: %v", err)
	}
	var parity liveHookParityRecord
	if err := json.Unmarshal(parityBytes, &parity); err != nil {
		t.Fatalf("parse parity record: %v\n%s", err, string(parityBytes))
	}
	if parity.HookName != "SessionStart" ||
		parity.ContextRequestID == "" ||
		parity.ContractID == "" ||
		parity.ContextPackID == "" ||
		parity.ContextRequestRef == "" ||
		parity.ContractRef == "" ||
		parity.ContextPackRef == "" ||
		parity.InjectionRef == "" ||
		!stringSliceContains(parity.ProducedKinds, "context_request") ||
		!stringSliceContains(parity.ProducedKinds, "effective_contract") ||
		!stringSliceContains(parity.ProducedKinds, "context_pack") {
		t.Fatalf("session-start parity record lost context spine refs: %+v", parity)
	}
	for _, ref := range []string{parity.ContextRequestRef, parity.ContractRef, parity.ContextPackRef, parity.InjectionRef} {
		if strings.HasPrefix(ref, "/") || strings.Contains(ref, "..") {
			t.Fatalf("parity ref should be relative and home-scoped, got %q", ref)
		}
		if _, err := os.Stat(filepath.Join(homePath, ref)); err != nil {
			t.Fatalf("parity ref %q missing backing artifact: %v\nrecord=%s", ref, err, string(parityBytes))
		}
	}
}

func TestHookLiveUserPromptSubmitEmitsAdditionalContext(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	input := `{"hook_event_name":"UserPromptSubmit","session_id":"sess_live","cwd":"/repo/product","prompt":"tighten onboarding copy"}`
	out := captureStdout(t, func() {
		withStdin(t, input, func() {
			if code := runHookCommand([]string{"user-prompt-submit", "--live", "--harness", "claude-code"}); code != 0 {
				t.Fatalf("live user-prompt-submit exit = %d", code)
			}
		})
	})
	var resp claudeLiveHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("parse live response: %v\n%s", err, out)
	}
	if resp.HookSpecificOutput.HookEventName != "UserPromptSubmit" ||
		!strings.Contains(resp.HookSpecificOutput.AdditionalContext, "tighten onboarding copy") {
		t.Fatalf("live user-prompt-submit response = %+v", resp.HookSpecificOutput)
	}
}

func TestHookLiveContextRetrievalJournalsAfterSuccessfulBreakers(t *testing.T) {
	homePath := t.TempDir()
	repoPath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	inputs := []struct {
		hook   string
		event  string
		prompt string
		mode   string
	}{
		{hook: "session-start", event: "SessionStart", prompt: "synthetic startup retrieval", mode: "observe"},
		{hook: "user-prompt-submit", event: "UserPromptSubmit", prompt: "synthetic prompt retrieval", mode: "execute"},
	}
	for i, input := range inputs {
		body := `{"hook_event_name":` + quoteJSON(input.event) +
			`,"session_id":` + quoteJSON(fmt.Sprintf("sess_retrieval_%d", i)) +
			`,"cwd":` + quoteJSON(repoPath) +
			`,"prompt":` + quoteJSON(input.prompt) + `}`
		withStdin(t, body, func() {
			if code := runHookCommand([]string{input.hook, "--live", "--harness", "codex"}); code != 0 {
				t.Fatalf("%s exit=%d", input.hook, code)
			}
		})
	}
	rows, err := retrievalparity.LoadJournal(homePath)
	if err != nil {
		t.Fatalf("load live-hook retrieval journal: %v", err)
	}
	if len(rows) != len(inputs) {
		t.Fatalf("live-hook retrieval rows=%d, want %d: %+v", len(rows), len(inputs), rows)
	}
	for i, row := range rows {
		if row.Consumer != retrievalparity.ConsumerOrient || row.Query != inputs[i].prompt || row.OrientInput == nil {
			t.Fatalf("live-hook row %d=%+v", i, row)
		}
		if row.OrientInput.Harness != "codex" || row.OrientInput.Cwd != repoPath || row.OrientInput.Mode != inputs[i].mode {
			t.Fatalf("live-hook row %d tuple=%+v", i, row.OrientInput)
		}
	}
}

func TestLiveHookParityJournalUsesCapturedRetrievalLatency(t *testing.T) {
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	res := hook.Result{Context: &hook.ContextObservation{
		Input:            contract.Input{Harness: "codex", Cwd: "/repo/product", Task: "synthetic delayed persistence", Mode: "observe"},
		Pack:             envelope.ContextPack{ActiveMemory: []string{"claim://claim_aaaaaaaaaaaa"}},
		RetrievalLatency: 4 * time.Millisecond,
	}}

	time.Sleep(20 * time.Millisecond)
	recordLiveHookRetrieval(homePath, hookCommandArgs{Live: true, HookName: "session-start"}, res, cliTestNow)
	rows, err := retrievalparity.LoadJournal(homePath)
	if err != nil {
		t.Fatalf("load retrieval journal: %v", err)
	}
	if len(rows) != 1 || rows[0].LatencyMS != 4 {
		t.Fatalf("delayed journal row=%+v, want captured retrieval latency 4ms", rows)
	}
}

func TestHookLiveCodexUserPromptSubmitEmitsCodexContext(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	input := `{"hook_event_name":"UserPromptSubmit","session_id":"sess_codex_prompt","cwd":"/repo/product","prompt":"tighten onboarding copy"}`
	var code int
	out := captureStdout(t, func() {
		withStdin(t, input, func() {
			code = runHookCommand([]string{"user-prompt-submit", "--live", "--harness", "codex"})
		})
	})
	if code != 0 {
		t.Fatalf("codex live user-prompt-submit exit = %d\n%s", code, out)
	}
	var resp codexLiveHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("parse codex live response: %v\n%s", err, out)
	}
	if resp.SchemaVersion != "compound.codex_live_hook_response.v0.1" ||
		resp.HookName != "UserPromptSubmit" ||
		resp.Status != "ok" ||
		resp.EventID == "" ||
		!strings.Contains(resp.AdditionalContext, "tighten onboarding copy") {
		t.Fatalf("codex live user-prompt-submit response = %+v", resp)
	}
}

func TestHookLivePreToolUseMapsClaudeWriteInput(t *testing.T) {
	homePath := t.TempDir()
	repo := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	target := filepath.Join(repo, "README.md")
	input := `{"hook_event_name":"PreToolUse","session_id":"sess_live","cwd":` +
		quoteJSON(repo) + `,"tool_name":"Write","tool_input":{"file_path":` + quoteJSON(target) + `}}`
	var code int
	out := captureStdout(t, func() {
		withStdin(t, input, func() {
			code = runHookCommand([]string{"pre-tool-use", "--live", "--harness", "claude-code"})
		})
	})
	if code != 0 {
		t.Fatalf("live pre-tool-use exit = %d\n%s", code, out)
	}
	var resp claudeLiveHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("parse live response: %v\n%s", err, out)
	}
	if resp.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Fatalf("live pre-tool-use response = %+v", resp.HookSpecificOutput)
	}
	if _, err := os.Stat(filepath.Join(homePath, "events")); err != nil {
		t.Fatalf("live hook should persist event artifacts in temp home: %v", err)
	}
}

func TestHookLiveCodexPreToolUseEmitsNativeAllowForApplyPatch(t *testing.T) {
	homePath := t.TempDir()
	repo := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	patch := "*** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** End Patch"
	input := `{"hook_event_name":"PreToolUse","session_id":"sess_codex_pretool","cwd":` +
		quoteJSON(repo) + `,"tool_name":"apply_patch","tool_input":{"command":` + quoteJSON(patch) + `}}`
	var code int
	out := captureStdout(t, func() {
		withStdin(t, input, func() {
			code = runHookCommand([]string{"pre-tool-use", "--live", "--harness", "codex"})
		})
	})
	if code != 0 {
		t.Fatalf("codex live pre-tool-use exit = %d\n%s", code, out)
	}
	var resp claudeLiveHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("parse codex live response: %v\n%s", err, out)
	}
	if resp.HookSpecificOutput.HookEventName != "PreToolUse" || resp.HookSpecificOutput.PermissionDecision != "" {
		t.Fatalf("codex native allow response = %+v", resp.HookSpecificOutput)
	}
	if _, err := os.Stat(filepath.Join(homePath, "events")); err != nil {
		t.Fatalf("codex live hook should persist event artifacts in temp home: %v", err)
	}
}

func TestLiveActionIntentBindsCanonicalInputToSessionAndFullTargetSet(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** Add File: SECOND.md\n+second\n*** End Patch"
	in := claudeLiveHookInput{
		SessionID: "sess_reviewed_dirty_a",
		Cwd:       "/repo/demo",
		ToolName:  "apply_patch",
		ToolInput: map[string]any{"command": patch},
	}
	actions, err := liveActionIntents("codex", in, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 2 {
		t.Fatalf("actions=%d, want two targets", len(actions))
	}
	wantRef := ""
	for _, action := range actions {
		for _, ref := range action.EvidenceRefs {
			if strings.HasPrefix(ref, "mutation-input:sha256:") {
				wantRef = ref
			}
		}
	}
	if wantRef == "" || !containsAllActionEvidence(actions, wantRef) {
		t.Fatalf("batch actions do not share one mutation-input hash: %+v", actions)
	}

	otherSession := in
	otherSession.SessionID = "sess_reviewed_dirty_b"
	otherActions, err := liveActionIntents("codex", otherSession, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if containsAllActionEvidence(otherActions, wantRef) {
		t.Fatalf("another session reused mutation-input hash %q", wantRef)
	}

	partial := in
	partial.ToolInput = map[string]any{"command": "*** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** End Patch"}
	partialActions, err := liveActionIntents("codex", partial, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if containsAllActionEvidence(partialActions, wantRef) {
		t.Fatalf("partial target set reused full-batch mutation-input hash %q", wantRef)
	}
}

func TestHookLiveCodexPreToolUseAllowsOnlyExactRecordedDirtyBatch(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(work.CreateInput{
		WorkID: "work_owner_reviewed_dirty", Owner: "codex", Actor: "orchestrator", State: "ready",
		SuccessPredicate: "apply the exact reviewed dirty batch", DesiredOutcome: "resolve reviewed conflicts",
	}, cliTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim("work_owner_reviewed_dirty", "orchestrator", cliTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance("work_owner_reviewed_dirty", work.AdvanceInput{To: "running", Actor: "orchestrator"}, cliTestNow); err != nil {
		t.Fatal(err)
	}

	repo := initCompoundCommandGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "SECOND.md"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patch := "*** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** Update File: SECOND.md\n@@\n-old\n+new\n*** End Patch"
	exactInput := codexApplyPatchInput("sess_reviewed_dirty_exact", repo, patch)
	first := runCodexPreToolUse(t, exactInput)
	if first.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("unreviewed dirty batch decision = %+v, want deny", first.HookSpecificOutput)
	}

	actions := readLiveMutationActions(t, homePath, "work_live_")
	if len(actions) != 2 {
		t.Fatalf("persisted live mutation actions=%d, want 2: %+v", len(actions), actions)
	}
	binding := bindingFromPersistedActions(t, actions)
	snapshot, err := authority.ReadGitSnapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := authority.BuildSourceControlMutationReviewPlan(snapshot, "work_owner_reviewed_dirty", binding, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := authority.RecordSourceControlReviewPlan(homePath, plan)
	if err != nil {
		t.Fatal(err)
	}
	review := authority.SourceControlReview{
		SchemaVersion: authority.SourceControlReviewSchemaVersion,
		ID:            plan.ReviewDraft.ReviewID, PlanID: plan.ID, RepoID: plan.RepoID,
		Decision: "authorize_mutation", Rationale: "operator reviewed this exact dirty payload",
		Confidence: 1, ReviewedBy: "operator:test", EvidenceRefs: append([]string{}, plan.EvidenceRefs...),
	}
	if _, err := authority.RecordSourceControlReview(homePath, review, paths.ReviewYAMLPath, cliTestNow); err != nil {
		t.Fatal(err)
	}

	exact := runCodexPreToolUse(t, exactInput)
	if exact.HookSpecificOutput.PermissionDecision == "deny" {
		t.Fatalf("exact reviewed dirty batch remained denied: %+v", exact.HookSpecificOutput)
	}
	partialPatch := "*** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** End Patch"
	partial := runCodexPreToolUse(t, codexApplyPatchInput("sess_reviewed_dirty_exact", repo, partialPatch))
	if partial.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("partial target set decision = %+v, want deny", partial.HookSpecificOutput)
	}
	otherSession := runCodexPreToolUse(t, codexApplyPatchInput("sess_reviewed_dirty_other", repo, patch))
	if otherSession.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("other session decision = %+v, want deny", otherSession.HookSpecificOutput)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("drifted after review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := runCodexPreToolUse(t, exactInput)
	if stale.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("stale fingerprint decision = %+v, want deny", stale.HookSpecificOutput)
	}
}

func codexApplyPatchInput(sessionID, cwd, patch string) string {
	return `{"hook_event_name":"PreToolUse","session_id":` + quoteJSON(sessionID) + `,"cwd":` +
		quoteJSON(cwd) + `,"tool_name":"apply_patch","tool_input":{"command":` + quoteJSON(patch) + `}}`
}

func runCodexPreToolUse(t *testing.T, input string) claudeLiveHookResponse {
	t.Helper()
	var code int
	out := captureStdout(t, func() {
		withStdin(t, input, func() {
			code = runHookCommand([]string{"pre-tool-use", "--live", "--harness", "codex"})
		})
	})
	if code != 0 {
		t.Fatalf("Codex PreToolUse exit=%d\n%s", code, out)
	}
	var response claudeLiveHookResponse
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatalf("parse Codex PreToolUse response: %v\n%s", err, out)
	}
	return response
}

func readLiveMutationActions(t *testing.T, homePath, workPrefix string) []envelope.ActionIntent {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(homePath, "events"))
	if err != nil {
		t.Fatal(err)
	}
	actions := []envelope.ActionIntent{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "act_live_") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(homePath, "events", entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		var action envelope.ActionIntent
		if json.Unmarshal(data, &action) == nil && strings.HasPrefix(action.IDs.WorkID, workPrefix) && action.Action == "multi_edit_file" {
			actions = append(actions, action)
		}
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].Target.Path < actions[j].Target.Path })
	return actions
}

func bindingFromPersistedActions(t *testing.T, actions []envelope.ActionIntent) authority.SourceControlMutationBinding {
	t.Helper()
	if len(actions) == 0 {
		t.Fatal("persisted actions required")
	}
	binding := authority.SourceControlMutationBinding{
		ActionWorkID: actions[0].IDs.WorkID, Capability: actions[0].Capability,
		Action: actions[0].Action, MutationClass: actions[0].MutationClass,
	}
	for _, action := range actions {
		if action.IDs.WorkID != binding.ActionWorkID || action.Capability != binding.Capability || action.Action != binding.Action || action.MutationClass != binding.MutationClass {
			t.Fatalf("batch action semantics differ: %+v", actions)
		}
		binding.TargetPaths = append(binding.TargetPaths, action.Target.Path)
		binding.ActionIntentIDs = append(binding.ActionIntentIDs, action.ID)
		for _, ref := range action.EvidenceRefs {
			if strings.HasPrefix(ref, "mutation-input:sha256:") {
				if binding.MutationInputHash != "" && binding.MutationInputHash != ref {
					t.Fatalf("batch input hashes differ: %+v", actions)
				}
				binding.MutationInputHash = ref
			}
		}
	}
	if binding.MutationInputHash == "" {
		t.Fatal("persisted live actions missing mutation-input hash")
	}
	return binding
}

func containsAllActionEvidence(actions []envelope.ActionIntent, want string) bool {
	if len(actions) == 0 {
		return false
	}
	for _, action := range actions {
		found := false
		for _, ref := range action.EvidenceRefs {
			if ref == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestHookLiveCodexPreToolUseDeniesWhenAnyApplyPatchTargetIsMisplacedCanon(t *testing.T) {
	homePath := t.TempDir()
	repo := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homePath, "config", "policy.yaml"), []byte("placement_guard_mode: block\n"), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	rules := policy.PlacementRules{Owners: []policy.PlacementOwnerRule{{
		ID: "decisions", Owner: "decisions", Status: "active",
		CanonicalWriteGlobs: []string{filepath.Join(repo, "decisions", "**")},
		CanonFilenameGlobs:  []string{"DEC-*.md"}, Confidence: "block",
	}}}
	rulesJSON, err := json.Marshal(rules)
	if err != nil {
		t.Fatalf("marshal placement rules: %v", err)
	}
	rulesSource := filepath.Join(t.TempDir(), "placement-rules.json")
	if err := os.WriteFile(rulesSource, rulesJSON, 0o644); err != nil {
		t.Fatalf("write placement rules: %v", err)
	}
	if _, err := policy.ImportPlacementRules(homePath, rulesSource, cliTestNow); err != nil {
		t.Fatalf("import placement rules: %v", err)
	}

	patch := "*** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** Add File: DEC-9.md\n+decision\n*** End Patch"
	input := `{"hook_event_name":"PreToolUse","session_id":"sess_codex_block","cwd":` +
		quoteJSON(repo) + `,"tool_name":"apply_patch","tool_input":{"command":` + quoteJSON(patch) + `}}`
	var code int
	out := captureStdout(t, func() {
		withStdin(t, input, func() {
			code = runHookCommand([]string{"pre-tool-use", "--live", "--harness", "codex"})
		})
	})
	if code != 0 {
		t.Fatalf("codex native hook exit = %d, want JSON decision on exit 0\n%s", code, out)
	}
	var resp claudeLiveHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("parse codex native response: %v\n%s", err, out)
	}
	got := resp.HookSpecificOutput
	if got.HookEventName != "PreToolUse" || got.PermissionDecision != "deny" || !strings.Contains(strings.ToLower(got.PermissionDecisionReason), "placement") {
		t.Fatalf("codex native deny response = %+v", got)
	}
	stream, err := os.ReadFile(filepath.Join(homePath, "events", "hook-events.ndjson"))
	if err != nil {
		t.Fatalf("read hook events: %v", err)
	}
	if !strings.Contains(string(stream), `"status":"blocked"`) {
		t.Fatalf("hook event did not prove native block:\n%s", string(stream))
	}
}

func TestHookLiveCodexPreToolUseDeniesUnparseableApplyPatch(t *testing.T) {
	homePath := t.TempDir()
	repo := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	input := `{"hook_event_name":"PreToolUse","session_id":"sess_codex_unparsed","cwd":` +
		quoteJSON(repo) + `,"tool_name":"apply_patch","tool_input":{"command":"new-wire-format"}}`
	var code int
	out := captureStdout(t, func() {
		withStdin(t, input, func() {
			code = runHookCommand([]string{"pre-tool-use", "--live", "--harness", "codex"})
		})
	})
	if code != 0 {
		t.Fatalf("unparseable Codex hook exit = %d, want native JSON deny\n%s", code, out)
	}
	var resp claudeLiveHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("parse native response: %v\n%s", err, out)
	}
	got := resp.HookSpecificOutput
	if got.PermissionDecision != "deny" || !strings.Contains(got.PermissionDecisionReason, "security") {
		t.Fatalf("unparseable patch response = %+v, want fail-closed deny", got)
	}
}

func TestHookLiveSessionEndRecordsEventAndParity(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	parityDir := t.TempDir()
	input := `{"hook_event_name":"SessionEnd","session_id":"sess_live_end","cwd":"/repo/product","transcript_path":"/tmp/claude-transcript.jsonl"}`
	var code int
	out := captureStdout(t, func() {
		withStdin(t, input, func() {
			code = runHookCommand([]string{"session-end", "--live", "--harness", "claude-code", "--parity-log", parityDir})
		})
	})
	if code != 0 {
		t.Fatalf("live session-end exit = %d\n%s", code, out)
	}
	var resp claudeLiveHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("parse live response: %v\n%s", err, out)
	}
	if resp.HookSpecificOutput.HookEventName != "SessionEnd" {
		t.Fatalf("live session-end response = %+v", resp.HookSpecificOutput)
	}
	stream, err := os.ReadFile(filepath.Join(homePath, "events", "hook-events.ndjson"))
	if err != nil {
		t.Fatalf("read hook event stream: %v", err)
	}
	if !strings.Contains(string(stream), `"hook_name":"SessionEnd"`) || !strings.Contains(string(stream), `"session_id":"sess_live_end"`) {
		t.Fatalf("hook event stream missing session-end event:\n%s", string(stream))
	}
	entries, err := os.ReadDir(parityDir)
	if err != nil {
		t.Fatalf("read parity dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("parity entries = %d, want 1", len(entries))
	}
	parityBytes, err := os.ReadFile(filepath.Join(parityDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read parity file: %v", err)
	}
	var parity liveHookParityRecord
	if err := json.Unmarshal(parityBytes, &parity); err != nil {
		t.Fatalf("parse parity file: %v\n%s", err, string(parityBytes))
	}
	if parity.HookName != "SessionEnd" || parity.SessionID != "sess_live_end" || parity.WorkID != "" || len(parity.ProducedKinds) != 0 {
		t.Fatalf("parity record = %+v, want session-only SessionEnd", parity)
	}
}

func withStdin(t *testing.T, raw string, fn func()) {
	t.Helper()
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	if _, err := w.WriteString(raw); err != nil {
		t.Fatalf("write stdin pipe: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close stdin writer: %v", err)
	}
	os.Stdin = r
	defer func() {
		os.Stdin = old
		_ = r.Close()
	}()
	fn()
}

func quoteJSON(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}
