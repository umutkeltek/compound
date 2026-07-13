package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/home"
	"compound/internal/policy"
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
