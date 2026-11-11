package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/hookplan"
	"compound/internal/runattempt"
)

// --enforce-governed blocks only inside a governed run: the hook process
// carries its RunAttempt id. Everywhere else it is the synchronous observation
// posture, so an operator's own session on that lane profile is never gated.
func TestParseHookArgsEnforceGovernedFollowsTheRunAttemptID(t *testing.T) {
	args := []string{"pre-tool-use", "--live", "--harness", "claude_minimax", hookplan.EnforceGovernedFlag}
	restore := hookGetenv
	defer func() { hookGetenv = restore }()

	hookGetenv = func(string) string { return "" }
	parsed, err := parseHookArgs(args)
	if err != nil || parsed.Enforce || parsed.Spool {
		t.Fatalf("outside a governed run = %+v, %v; want synchronous observation", parsed, err)
	}

	hookGetenv = func(name string) string {
		if name == runattempt.LaunchEnvRunAttemptID {
			return "ratt_20261111T000000Z_00000000"
		}
		return ""
	}
	parsed, err = parseHookArgs(args)
	if err != nil || !parsed.Enforce {
		t.Fatalf("inside a governed run = %+v, %v; want enforce", parsed, err)
	}

	for _, bad := range [][]string{
		{"pre-tool-use", "--live", "--harness", "claude_minimax", hookplan.EnforceGovernedFlag, "--enforce"},
		{"pre-tool-use", "--live", "--harness", "claude_minimax", hookplan.EnforceGovernedFlag, "--spool"},
		{"post-tool-use", "--live", "--harness", "claude_minimax", hookplan.EnforceGovernedFlag},
		{"pre-tool-use", hookplan.EnforceGovernedFlag, "fixtures/hooks/pre-tool-use.claude.json"},
	} {
		if _, err := parseHookArgs(bad); err == nil {
			t.Fatalf("parseHookArgs(%v) accepted an invalid --enforce-governed form", bad)
		}
	}
}

// The doctor probes the real installed binary on both sides of the posture:
// without a RunAttempt id it must exit 0 after evaluating policy, and with one
// it must return the exact gate exit. A hook that ignored the id (always
// observing) or ignored the flag (always enforcing) fails one side.
func TestHookEnforcementProbesGovernedOnlyBothWays(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("builds the compound binary: skipped in -short mode")
	}
	real := buildRealCompound(t)
	configPath := filepath.Join(t.TempDir(), "settings.json")
	command := hookplan.GovernedOnlyHookCommandForEvent(real, "claude-minimax", "PreToolUse")
	config := map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{
		"matcher": "*",
		"hooks":   []any{map[string]any{"type": "command", "command": command}},
	}}}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	rows, failing := buildHookEnforcementRows([]doctorHook{{Harness: "claude-minimax", ConfigPath: configPath}})
	if failing != 0 || len(rows) != 1 {
		t.Fatalf("governed-only real binary: failing=%d rows=%+v", failing, rows)
	}
	if rows[0].Posture != hookplan.HookPostureGovernedOnly || rows[0].Status != "enforcing_in_governed_runs" || rows[0].Detail != "" {
		t.Fatalf("row = %+v, want a passing enforcing_in_governed_runs row", rows[0])
	}
	var out strings.Builder
	printHookEnforcement(&out, doctorReport{HookEnforcement: rows})
	if !strings.Contains(out.String(), "blocks only in governed runs") {
		t.Fatalf("doctor text does not say where it blocks:\n%s", out.String())
	}
}
