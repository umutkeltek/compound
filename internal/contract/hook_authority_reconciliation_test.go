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
	importReport, err := ImportHookRegistrationTS(compoundHome, registrationSource, time.Date(2026, 7, 10, 4, 0, 0, 0, time.UTC))
	if err != nil {
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
	if len(got.Plan.Bindings) != importReport.HookCount {
		t.Fatalf("desired HookPlan bindings = %+v, want %d imported authority rows", got.Plan.Bindings, importReport.HookCount)
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
		if binding.InvocationRevision == "" {
			t.Fatalf("observed hook lost privacy-safe invocation identity: %+v", binding)
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

func TestCanonicalHookInvocationRevisionIncludesArgumentsForEveryHandler(t *testing.T) {
	harnessHome := t.TempDir()
	tests := []struct {
		name       string
		desired    string
		observed   string
		driftedArg string
	}{
		{
			name:       "compound binary",
			desired:    `"$HOME/.local/bin/compound" hook pre-tool-use --live --harness codex`,
			observed:   filepath.Join(harnessHome, ".local", "bin", "compound") + ` hook pre-tool-use --live --harness codex`,
			driftedArg: filepath.Join(harnessHome, ".local", "bin", "compound") + ` hook session-start --live --harness codex`,
		},
		{
			name:       "typescript hook",
			desired:    `"$HOME/.claude/hooks/RecallContext.hook.ts" --mode compact`,
			observed:   `"` + filepath.Join(harnessHome, ".claude", "hooks", "RecallContext.hook.ts") + `" --mode compact`,
			driftedArg: `"` + filepath.Join(harnessHome, ".claude", "hooks", "RecallContext.hook.ts") + `" --mode full`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			desired := canonicalHookInvocationRevision(test.desired, harnessHome)
			observed := canonicalHookInvocationRevision(test.observed, harnessHome)
			drifted := canonicalHookInvocationRevision(test.driftedArg, harnessHome)
			if !strings.HasPrefix(desired, "sha256:") {
				t.Fatalf("canonical invocation revision = %q, want privacy-safe digest", desired)
			}
			if desired != observed {
				t.Fatalf("equivalent commands differ: desired=%q observed=%q", desired, observed)
			}
			if desired == drifted {
				t.Fatalf("different command arguments collapsed to %q", desired)
			}
		})
	}
}

func TestHookPlanSkipsRetiredInstalledRows(t *testing.T) {
	manifest := InstalledHooksManifest{
		SchemaVersion: installedHooksSchemaVersion,
		Hooks: []InstalledHook{
			{Harness: "claude_code", Name: "SessionStart", Event: "SessionStart", Ref: "handler://active", Status: "current"},
			{Harness: "claude_code", Name: "SessionEnd", Event: "SessionEnd", Ref: "handler://retired", Status: "retired"},
		},
	}
	plan, err := hookPlanFromInstalledManifest(manifest, t.TempDir())
	if err != nil {
		t.Fatalf("hookPlanFromInstalledManifest() error = %v", err)
	}
	if len(plan.Bindings) != 1 || plan.Bindings[0].Event != "SessionStart" {
		t.Fatalf("HookPlan bindings = %+v, want only current row", plan.Bindings)
	}
}

func TestReconcileInstalledHooksWithoutPlanStillReportsObservedExtras(t *testing.T) {
	compoundHome := t.TempDir()
	harnessHome := t.TempDir()
	writeHarnessHookConfig(t, filepath.Join(harnessHome, ".claude", "settings.json"), `{
  "hooks": {
    "SessionEnd": [{"hooks":[{"type":"command","command":"\"$HOME/.claude/hooks/SessionReceipt.hook.ts\""}]}]
  }
}`)

	got, err := ReconcileInstalledHooks(compoundHome, harnessHome, harness.DefaultRegistry())
	if err != nil {
		t.Fatalf("ReconcileInstalledHooks() error = %v", err)
	}
	if len(got.Plan.Bindings) != 0 || len(got.Observation.Bindings) != 1 {
		t.Fatalf("desired/observed split = plan:%+v observed:%+v", got.Plan.Bindings, got.Observation.Bindings)
	}
	if len(got.Reconciliation.Deltas) != 1 || got.Reconciliation.Deltas[0].State != hookplan.StateExtraUnowned {
		t.Fatalf("reconciliation = %+v, want observed hook extra_unowned", got.Reconciliation.Deltas)
	}
}

func TestCanonicalHookHandlerRefNormalizesCurrentHomeWithoutExplicitHarnessHome(t *testing.T) {
	harnessHome := t.TempDir()
	t.Setenv("HOME", harnessHome)
	absolute := canonicalHookHandlerRef(filepath.Join(harnessHome, ".local", "bin", "compound")+` hook pre-tool-use --live --harness codex`, "")
	portable := canonicalHookHandlerRef(`$HOME/.local/bin/compound hook pre-tool-use --live --harness codex`, harnessHome)
	if absolute != portable {
		t.Fatalf("implicit current-home normalization drifted: absolute=%q portable=%q", absolute, portable)
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
