package main

import (
	"encoding/json"
	"testing"

	"compound/internal/home"
)

func TestLivePreToolUseOnlyPublishesNativePermissionDecisionWhenEnforced(t *testing.T) {
	for _, harnessName := range []string{"codex", "claude-code"} {
		t.Run(harnessName, func(t *testing.T) {
			homePath := t.TempDir()
			t.Setenv("COMPOUND_HOME", homePath)
			if err := home.Init(homePath, "test", cliTestNow); err != nil {
				t.Fatalf("init home: %v", err)
			}

			input := `{"hook_event_name":"PreToolUse","session_id":"sess_observe_default_` +
				harnessName +
				`","cwd":` + quoteJSON(t.TempDir()) +
				`,"tool_name":"exec_command","tool_schema_revision":"builtin:exec-command-v1","tool_input":{"command":"mktemp -d /tmp/compound-permission-repro.XXXXXX"}}`

			var observeCode int
			observeOut := captureStdout(t, func() {
				withStdin(t, input, func() {
					observeCode = runHookCommand([]string{
						"pre-tool-use", "--live", "--harness", harnessName,
					})
				})
			})
			if observeCode != 0 {
				t.Fatalf("observe-only PreToolUse exit=%d, want 0\n%s", observeCode, observeOut)
			}
			var observed claudeLiveHookResponse
			if err := json.Unmarshal([]byte(observeOut), &observed); err != nil {
				t.Fatalf("parse observe-only response: %v\n%s", err, observeOut)
			}
			if got := observed.HookSpecificOutput.PermissionDecision; got != "" {
				t.Fatalf("observe-only native decision=%q, want empty: %+v", got, observed.HookSpecificOutput)
			}
			if !livePolicyReasonExists(t, homePath, "baseline for mutation_class") {
				t.Fatal("observe-only PreToolUse did not persist the held PolicyDecision evidence")
			}

			enforcedInput := `{"hook_event_name":"PreToolUse","session_id":"sess_observe_enforced_` +
				harnessName +
				`","cwd":` + quoteJSON(t.TempDir()) +
				`,"tool_name":"exec_command","tool_schema_revision":"builtin:exec-command-v1","tool_input":{"command":"mktemp -d /tmp/compound-permission-repro.XXXXXX"}}`
			var enforceCode int
			enforceOut := captureStdout(t, func() {
				withStdin(t, enforcedInput, func() {
					enforceCode = runHookCommand([]string{
						"pre-tool-use", "--live", "--harness", harnessName, "--enforce",
					})
				})
			})
			if enforceCode == 0 {
				t.Fatalf("enforced PreToolUse exit=0, want policy gate exit\n%s", enforceOut)
			}
			var enforced claudeLiveHookResponse
			if err := json.Unmarshal([]byte(enforceOut), &enforced); err != nil {
				t.Fatalf("parse enforced response: %v\n%s", err, enforceOut)
			}
			if got := enforced.HookSpecificOutput.PermissionDecision; got == "" {
				t.Fatalf("enforced native decision is empty: %+v", enforced.HookSpecificOutput)
			}
		})
	}
}
