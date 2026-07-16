package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"compound/internal/authority"
	"compound/internal/candidates"
	"compound/internal/capgraph"
	"compound/internal/contract"
	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/hook"
	"compound/internal/mem"
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

func TestHookLiveUserPromptSubmitReturnsExactRelevantPrivateContextWithoutWork(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	manifest, err := capgraph.LoadSkillManifest(filepath.Join("..", "..", "fixtures", "skills", "harness-split.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := capgraph.ApplySkillManifest(homePath, manifest, cliTestNow); err != nil {
		t.Fatal(err)
	}
	seedLivePromptContextMeaning(t, homePath)
	workStore, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	before, err := workStore.List()
	if err != nil {
		t.Fatal(err)
	}
	raw := "LIVE_PROMPT_PRIVACY_SENTINEL_88bd pricing prompt context should prefer concise progress updates"
	input := `{"hook_event_name":"UserPromptSubmit","session_id":"sess_live","cwd":"/repo/product","prompt":` + quoteJSON(raw) + `}`
	var stderr string
	out := captureStdout(t, func() {
		stderr = capturePromptHookStderr(t, func() {
			withStdin(t, input, func() {
				if code := runHookCommand([]string{"user-prompt-submit", "--live", "--harness", "claude-code"}); code != 0 {
					t.Fatalf("live user-prompt-submit exit = %d", code)
				}
			})
		})
	})
	context := parseNativePromptContext(t, out, "UserPromptSubmit")
	persisted := readOnlyPromptInjection(t, homePath)
	if context != persisted {
		t.Fatalf("stdout context differs from persisted injection\nstdout=%q\npersisted=%q", context, persisted)
	}
	for _, want := range []string{
		"pricing prompt context should prefer concise progress updates",
		"capability://skill:claude-pricing",
	} {
		if !strings.Contains(context, want) {
			t.Fatalf("prompt context missing relevant meaning %q:\n%s", want, context)
		}
	}
	for _, forbidden := range []string{raw, "UNRELATED_DATABASE_MARKER_141a", "candidate work"} {
		if strings.Contains(context, forbidden) {
			t.Fatalf("prompt context contains forbidden %q:\n%s", forbidden, context)
		}
	}
	if got := utf8.RuneCountInString(context); got > hook.MaxContextChars {
		t.Fatalf("prompt context chars=%d, max=%d", got, hook.MaxContextChars)
	}
	after, err := workStore.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("live prompt changed work count %d -> %d", len(before), len(after))
	}
	rows, err := retrievalparity.LoadJournal(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !strings.HasPrefix(rows[0].Query, "sha256:") || strings.Contains(rows[0].Query, raw) {
		t.Fatalf("prompt retrieval journal retained raw query: %+v", rows)
	}
	for _, forbidden := range []string{raw, "# Compound Context Injection", "pricing prompt context should prefer concise progress updates"} {
		if strings.Contains(stderr, forbidden) {
			t.Fatalf("stderr duplicated private prompt context %q:\n%s", forbidden, stderr)
		}
	}
	for _, want := range []string{"hook_", "ctx_", "chars=", "sha256:"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr diagnostic missing %q:\n%s", want, stderr)
		}
	}
	pattern := regexp.MustCompile(`^\[claude_code/ok\] UserPromptSubmit — context hook=hook_[0-9a-f]{12} pack=ctx_[0-9a-f]{12} chars=[0-9]+ sha256:[0-9a-f]{64}$`)
	if !pattern.MatchString(strings.TrimSpace(stderr)) {
		t.Fatalf("stderr diagnostic carried fields outside the content-free grammar:\n%s", stderr)
	}
	assertHomeOmitsString(t, homePath, raw)
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
		if row.Consumer != retrievalparity.ConsumerOrient || row.OrientInput == nil {
			t.Fatalf("live-hook row %d=%+v", i, row)
		}
		if inputs[i].hook == "user-prompt-submit" {
			if !strings.HasPrefix(row.Query, "sha256:") || strings.Contains(row.Query, inputs[i].prompt) {
				t.Fatalf("live prompt row %d retained non-opaque intent: %+v", i, row)
			}
		} else if row.Query != inputs[i].prompt {
			t.Fatalf("session-start row %d query=%q, want %q", i, row.Query, inputs[i].prompt)
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

func TestHookLiveCodexUserPromptSubmitEmitsNativeContext(t *testing.T) {
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
	context := parseNativePromptContext(t, out, "UserPromptSubmit")
	if context != readOnlyPromptInjection(t, homePath) || strings.Contains(context, "candidate work") {
		t.Fatalf("codex prompt did not return exact injection:\n%s", context)
	}
}

func TestHookLiveCodexUserPromptSubmitBreakerReturnsNativeEmptyContext(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homePath, "config", "repo-kinds.yaml"), []byte("schema_version: invalid\nrepos: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	input := `{"hook_event_name":"UserPromptSubmit","session_id":"sess_codex_prompt_breaker","cwd":"/repo/product","prompt":"breaker prompt"}`
	var code int
	out := captureStdout(t, func() {
		withStdin(t, input, func() {
			code = runHookCommand([]string{"user-prompt-submit", "--live", "--harness", "codex"})
		})
	})
	if code != 0 {
		t.Fatalf("breaker exit=%d, want fail-open 0\n%s", code, out)
	}
	if context := parseNativePromptContext(t, out, "UserPromptSubmit"); context != "" {
		t.Fatalf("breaker context=%q, want empty", context)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if works, err := store.List(); err != nil || len(works) != 0 {
		t.Fatalf("breaker work=%+v err=%v", works, err)
	}
	for _, dir := range []string{"requests", "packs", "injections"} {
		if entries, err := os.ReadDir(filepath.Join(homePath, "context", dir)); err == nil && len(entries) != 0 {
			t.Fatalf("breaker persisted partial %s: %+v", dir, entries)
		}
	}
	stream, err := os.ReadFile(filepath.Join(homePath, "events", "hook-events.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stream), `"breaker_tripped":true`) || !strings.Contains(string(stream), `"status":"fail_open"`) {
		t.Fatalf("breaker telemetry missing:\n%s", stream)
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
	actions := []envelope.ActionIntent{}
	for _, entry := range testEventEntries(t, homePath, "act_live_") {
		record, readErr := entry.Read()
		if readErr != nil {
			t.Fatal(readErr)
		}
		var action envelope.ActionIntent
		if json.Unmarshal(record.Bytes, &action) == nil && strings.HasPrefix(action.IDs.WorkID, workPrefix) && action.Action == "multi_edit_file" {
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

func parseNativePromptContext(t *testing.T, raw, wantEvent string) string {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &top); err != nil {
		t.Fatalf("parse native prompt response: %v\n%s", err, raw)
	}
	if len(top) != 1 || top["hookSpecificOutput"] == nil {
		t.Fatalf("prompt response must contain only native hookSpecificOutput, got keys=%v\n%s", sortedJSONKeys(top), raw)
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(top["hookSpecificOutput"], &nested); err != nil {
		t.Fatalf("parse hookSpecificOutput: %v\n%s", err, raw)
	}
	for _, forbidden := range []string{"additional_context", "hook_name", "event_id", "schema_version", "status"} {
		if _, ok := top[forbidden]; ok {
			t.Fatalf("prompt response retained private field %q: %s", forbidden, raw)
		}
	}
	var resp claudeLiveHookResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("parse typed native prompt response: %v\n%s", err, raw)
	}
	if resp.HookSpecificOutput.HookEventName != wantEvent {
		t.Fatalf("native hookEventName=%q, want %q", resp.HookSpecificOutput.HookEventName, wantEvent)
	}
	return resp.HookSpecificOutput.AdditionalContext
}

func sortedJSONKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func readOnlyPromptInjection(t *testing.T, homePath string) string {
	t.Helper()
	dir := filepath.Join(homePath, "context", "injections")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].IsDir() {
		t.Fatalf("prompt injection entries=%v, want exactly one file", entries)
	}
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertHomeOmitsString(t *testing.T, root, forbidden string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), forbidden) {
			return fmt.Errorf("forbidden prompt found in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func seedLivePromptContextMeaning(t *testing.T, homePath string) {
	t.Helper()
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := store.Capture([]mem.Candidate{
		{
			Entity: "prompt-context", Predicate: "authority_status",
			Object:     "pricing prompt context should prefer concise progress updates",
			Confidence: 0.99, Derivation: "deterministic", ClaimType: "architecture",
			PromotionBasis: candidates.BasisRuntimeProof, SourceTraceRef: "trace_live_prompt_relevant",
			SnippetHash: "sha256:live-prompt-relevant", Visibility: "operational",
		},
		{
			Entity: "database-maintenance", Predicate: "status",
			Object:     "UNRELATED_DATABASE_MARKER_141a database vacuum status",
			Confidence: 0.99, Derivation: "deterministic", ClaimType: "status",
			PromotionBasis: candidates.BasisRuntimeProof, SourceTraceRef: "trace_live_prompt_unrelated",
			SnippetHash: "sha256:live-prompt-unrelated", Visibility: "operational",
		},
	}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range captured {
		if _, err := store.Adjudicate(candidate.ID, candidates.Promote, "test", cliTestNow); err != nil {
			t.Fatal(err)
		}
	}
}

func capturePromptHookStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()
	defer func() { os.Stderr = old }()
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	output := <-done
	_ = r.Close()
	return output
}
