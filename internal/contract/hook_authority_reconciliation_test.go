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

func TestReconcileInstalledHooksReportsLiveClaudeSessionEndAsExtraUnowned(t *testing.T) {
	compoundHome := t.TempDir()
	harnessHome := t.TempDir()
	t.Setenv("HOME", harnessHome)
	registrationSource := filepath.Join("..", "..", "fixtures", "harness", "hook-registration.ts")
	if _, err := ImportHookRegistrationTS(compoundHome, registrationSource, time.Date(2026, 7, 10, 4, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("ImportHookRegistrationTS() error = %v", err)
	}
	writeHarnessHookConfig(t, filepath.Join(harnessHome, ".claude", "settings.json"), `{
  "hooks": {
    "SessionStart": [{"hooks":[{"type":"command","command":"\"$HOME/.claude/hooks/RecallContext.hook.ts\""}]}],
    "UserPromptSubmit": [{"hooks":[{"type":"command","command":"\"$HOME/.claude/hooks/IntentRecall.hook.ts\""}]}],
    "PreToolUse": [{"matcher":"Write|Edit|MultiEdit","hooks":[{"type":"command","command":"\"$HOME/.claude/hooks/PlacementGuard.hook.ts\""}]}],
    "SessionEnd": [{"hooks":[{"type":"command","command":"\"$HOME/.claude/hooks/SessionReceipt.hook.ts\""}]}]
  }
}`)
	writeHarnessHookConfig(t, filepath.Join(harnessHome, ".codex", "hooks.json"), `{
  "hooks": {
    "SessionStart": [{"hooks":[{"type":"command","command":"$HOME/.bun/bin/bun $HOME/.codex/hooks/RecallContext.hook.ts"}]}],
    "UserPromptSubmit": [{"hooks":[{"type":"command","command":"$HOME/.bun/bin/bun $HOME/.codex/hooks/IntentRecall.hook.ts"}]}]
  }
}`)

	got, err := ReconcileInstalledHooks(compoundHome, harnessHome, harness.DefaultRegistry())
	if err != nil {
		t.Fatalf("ReconcileInstalledHooks() error = %v", err)
	}
	if len(got.Plan.Bindings) != 5 {
		t.Fatalf("desired HookPlan bindings = %+v, want five authority rows", got.Plan.Bindings)
	}
	if hookPlanHasEvent(got.Plan, "claude_code", "SessionEnd") {
		t.Fatalf("observed SessionEnd was promoted into desired plan: %+v", got.Plan.Bindings)
	}
	if len(got.Observation.Bindings) != 6 {
		t.Fatalf("observed installation bindings = %+v, want six live registrations", got.Observation.Bindings)
	}
	for _, binding := range got.Observation.Bindings {
		if binding.HandlerRevision != "" {
			t.Fatalf("config command digest masqueraded as handler revision: %+v", binding)
		}
	}
	delta := hookReconciliationDelta(got.Reconciliation, "claude_code", "SessionEnd")
	if delta == nil || delta.State != hookplan.StateExtraUnowned {
		t.Fatalf("Claude SessionEnd delta = %+v, want extra_unowned", delta)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal reconciliation report: %v", err)
	}
	if strings.Contains(string(encoded), ".bun/bin/bun ") || strings.Contains(string(encoded), `\"$HOME`) {
		t.Fatalf("reconciliation leaked a full harness command: %s", encoded)
	}
}

func writeHarnessHookConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir harness config: %v", err)
	}
	if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
		t.Fatalf("write harness config: %v", err)
	}
}

func hookPlanHasEvent(plan hookplan.HookPlan, harnessName, event string) bool {
	for _, binding := range plan.Bindings {
		if binding.Harness == harnessName && binding.Event == event {
			return true
		}
	}
	return false
}

func hookReconciliationDelta(reconciliation hookplan.Reconciliation, harnessName, event string) *hookplan.Delta {
	for i := range reconciliation.Deltas {
		if reconciliation.Deltas[i].Harness == harnessName && reconciliation.Deltas[i].Event == event {
			return &reconciliation.Deltas[i]
		}
	}
	return nil
}
