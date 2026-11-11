package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/harness"
	"compound/internal/hookplan"
)

// A provider lane whose own sessions are watch-only is reinstalled as
// blocking-in-governed-runs-only. Governed readiness reads that lane from the
// profile the lane launcher actually starts, and must still find a ready blocking
// PreToolUse: otherwise fixing the profile path would refuse every governed run
// on the lane. Before the path fix, readiness read a stale profile instead.
func TestGovernedOnlyLaneStaysReadyForGovernedExecution(t *testing.T) {
	compoundHome := t.TempDir()
	harnessHome := t.TempDir()
	binary := filepath.Join(harnessHome, ".local", "bin", "compound")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings, ok := hookplan.LaneHookSettingsPath(harnessHome, "claude_minimax")
	if !ok {
		t.Fatal("claude_minimax is a provider lane")
	}
	if want := filepath.Join(harnessHome, ".switchback", "config", "claude", "_providers", "minimax-lane", "settings.json"); settings != want {
		t.Fatalf("lane settings path = %q, want %q", settings, want)
	}
	watchOnly := map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{
		"matcher": "*",
		"hooks": []any{map[string]any{
			"type":    "command",
			"command": hookplan.HookCommandForEvent(binary, "claude-minimax", "PreToolUse"),
		}},
	}}}}
	data, err := json.Marshal(watchOnly)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, data, 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := hookplan.InstallHooks(harnessHome, binary, "global", []string{"claude-minimax"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ImportHookInstallReport(compoundHome, harnessHome, report, time.Now()); err != nil {
		t.Fatalf("import governed-only install report: %v", err)
	}

	manifest, err := LoadInstalledHooksManifest(compoundHome)
	if err != nil {
		t.Fatal(err)
	}
	status := ""
	for _, row := range InstalledHookGateReadiness(manifest, map[string]bool{"claude_minimax": true}) {
		if row.Harness == "claude_minimax" && row.Hook == "PreToolUse" {
			status = row.Status
		}
	}
	if status != "native_blocking" {
		t.Fatalf("claude_minimax PreToolUse gate status = %q, want native_blocking", status)
	}

	state, err := ReconcileInstalledHooks(compoundHome, harnessHome, harness.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, delta := range state.Reconciliation.Deltas {
		if delta.Harness != "claude_minimax" || delta.Event != "PreToolUse" {
			continue
		}
		found = true
		if delta.State != hookplan.StateReady {
			t.Fatalf("claude_minimax PreToolUse reconciliation = %+v, want ready", delta)
		}
	}
	if !found {
		t.Fatal("no claude_minimax PreToolUse reconciliation: the observed lane profile was not read")
	}
}

func TestImportRejectsGovernedOnlyCommandMarkedNonBlocking(t *testing.T) {
	compoundHome := t.TempDir()
	harnessHome := t.TempDir()
	binary := filepath.Join(harnessHome, ".local", "bin", "compound")
	settings, _ := hookplan.LaneHookSettingsPath(harnessHome, "claude_minimax")
	watchOnly := `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":` +
		jsonString(t, hookplan.HookCommandForEvent(binary, "claude-minimax", "PreToolUse")) + `}]}]}}`
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(watchOnly), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := hookplan.InstallHooks(harnessHome, binary, "global", []string{"claude-minimax"}, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := range report.Entries[0].Bindings {
		if report.Entries[0].Bindings[i].Event == "PreToolUse" {
			report.Entries[0].Bindings[i].Blocking = false
		}
	}
	if _, err := ImportHookInstallReport(compoundHome, harnessHome, report, time.Now()); err == nil ||
		!strings.Contains(err.Error(), "blocking metadata does not match") {
		t.Fatalf("import error = %v, want blocking metadata mismatch", err)
	}
}

func jsonString(t *testing.T, s string) string {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
