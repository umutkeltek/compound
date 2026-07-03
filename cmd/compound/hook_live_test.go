package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/home"
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
