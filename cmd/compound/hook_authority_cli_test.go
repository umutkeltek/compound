package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/contract"
	"compound/internal/hookplan"
)

func TestHooksAuthorityObserveLiveCLI(t *testing.T) {
	if os.Getenv("COMPOUND_HOOK_AUTHORITY_CLI_HELPER") == "1" {
		cmdContract([]string{"hooks-authority", "--observe-live"})
		os.Exit(0)
	}

	compoundHome := t.TempDir()
	harnessHome := t.TempDir()
	t.Setenv("HOME", harnessHome)
	registrationSource := filepath.Join("..", "..", "fixtures", "harness", "hook-registration.ts")
	importReport, err := contract.ImportHookRegistrationTS(compoundHome, registrationSource, time.Date(2026, 7, 10, 4, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ImportHookRegistrationTS() error = %v", err)
	}
	claudeSettings := filepath.Join(harnessHome, ".claude", "settings.json")
	writeHookAuthorityCLIConfig(t, claudeSettings, `{
  "hooks": {
    "SessionStart": [{"hooks":[{"type":"command","command":"\"$HOME/.claude/hooks/RecallContext.hook.ts\""}]}],
    "UserPromptSubmit": [{"hooks":[{"type":"command","command":"\"$HOME/.claude/hooks/IntentRecall.hook.ts\""}]}],
    "PreToolUse": [{"matcher":"Write|Edit|MultiEdit","hooks":[{"type":"command","command":"\"$HOME/.claude/hooks/PlacementGuard.hook.ts\""}]}],
    "SessionEnd": [{"hooks":[{"type":"command","command":"\"$HOME/.claude/hooks/SessionReceipt.hook.ts\""}]}]
  }
}`)
	writeHookAuthorityCLIConfig(t, filepath.Join(harnessHome, ".codex", "hooks.json"), `{
  "hooks": {
    "SessionStart": [{"hooks":[{"type":"command","command":"$HOME/.bun/bin/bun $HOME/.codex/hooks/RecallContext.hook.ts"}]}],
    "UserPromptSubmit": [{"hooks":[{"type":"command","command":"$HOME/.bun/bin/bun $HOME/.codex/hooks/IntentRecall.hook.ts"}]}]
  }
}`)
	settingsBefore, err := os.ReadFile(claudeSettings)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := contract.InstalledHooksPath(compoundHome)
	manifestBefore, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	command := exec.Command(os.Args[0], "-test.run=^TestHooksAuthorityObserveLiveCLI$")
	command.Env = append(os.Environ(),
		"COMPOUND_HOOK_AUTHORITY_CLI_HELPER=1",
		"COMPOUND_HOME="+compoundHome,
		"HOME="+harnessHome,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("hooks-authority --observe-live failed: %v\n%s", err, output)
	}
	var report struct {
		Privacy                 string                        `json:"privacy"`
		HookPlan                hookplan.HookPlan             `json:"hook_plan"`
		InstallationObservation hookplan.InstallationSnapshot `json:"installation_observation"`
		Reconciliation          hookplan.Reconciliation       `json:"reconciliation"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("parse hooks-authority output: %v\n%s", err, output)
	}
	if report.Privacy != "private" {
		t.Fatalf("hooks-authority privacy = %q, want private for path-bearing local report", report.Privacy)
	}
	if len(report.HookPlan.Bindings) != importReport.HookCount || len(report.InstallationObservation.Bindings) != 6 {
		t.Fatalf("hooks-authority desired/observed split = plan:%d observed:%d", len(report.HookPlan.Bindings), len(report.InstallationObservation.Bindings))
	}
	if cliHookPlanHasEvent(report.HookPlan, "claude_code", "SessionEnd") {
		t.Fatalf("hooks-authority promoted observed SessionEnd into desired plan: %+v", report.HookPlan.Bindings)
	}
	delta := cliHookReconciliationDelta(report.Reconciliation, "claude_code", "SessionEnd")
	if delta == nil || delta.State != hookplan.StateExtraUnowned {
		t.Fatalf("Claude SessionEnd delta = %+v, want extra_unowned", delta)
	}
	if strings.Contains(string(output), ".bun/bin/bun ") {
		t.Fatalf("hooks-authority leaked full hook command: %s", output)
	}
	assertCLIFileUnchanged(t, claudeSettings, settingsBefore)
	assertCLIFileUnchanged(t, manifestPath, manifestBefore)
}

func writeHookAuthorityCLIConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cliHookReconciliationDelta(reconciliation hookplan.Reconciliation, harnessName, event string) *hookplan.Delta {
	for i := range reconciliation.Deltas {
		if reconciliation.Deltas[i].Harness == harnessName && reconciliation.Deltas[i].Event == event {
			return &reconciliation.Deltas[i]
		}
	}
	return nil
}

func cliHookPlanHasEvent(plan hookplan.HookPlan, harnessName, event string) bool {
	for _, binding := range plan.Bindings {
		if binding.Harness == harnessName && binding.Event == event {
			return true
		}
	}
	return false
}

func assertCLIFileUnchanged(t *testing.T, path string, before []byte) {
	t.Helper()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("read-only hooks-authority changed %s", path)
	}
}
