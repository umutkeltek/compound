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

func TestImportAndObserveInstalledHookContentAreIndependentAttestations(t *testing.T) {
	compoundHome := t.TempDir()
	harnessHome := t.TempDir()
	t.Setenv("HOME", harnessHome)
	handlerPath := filepath.Join(harnessHome, ".claude", "hooks", "RecallContext.hook.ts")
	writeHarnessHookConfig(t, handlerPath, "#!/usr/bin/env bun\nprocess.stdout.write('{}')")
	sourcePath := filepath.Join(t.TempDir(), "hook-registration.ts")
	source := `export const HOOK_REGISTRATIONS = [
  { harness: "claude", event: "SessionStart", file: "RecallContext.hook.ts", command: ` + "`\"$HOME/.claude/hooks/RecallContext.hook.ts\"`" + ` },
];`
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	imported, err := ImportHookRegistrationTS(compoundHome, sourcePath, time.Date(2026, 7, 14, 20, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ImportHookRegistrationTS() error = %v", err)
	}
	if len(imported.Manifest.Hooks) != 1 || !strings.HasPrefix(imported.Manifest.Hooks[0].Revision, "sha256:") {
		t.Fatalf("imported handler content was not attested: %+v", imported.Manifest.Hooks)
	}
	desiredRevision := imported.Manifest.Hooks[0].Revision

	writeHarnessHookConfig(t, filepath.Join(harnessHome, ".claude", "settings.json"), `{
  "hooks": {"SessionStart": [{"hooks":[{"type":"command","command":"\"$HOME/.claude/hooks/RecallContext.hook.ts\""}]}]}
}`)
	ready, err := ReconcileInstalledHooks(compoundHome, harnessHome, harness.DefaultRegistry())
	if err != nil {
		t.Fatalf("ReconcileInstalledHooks() ready error = %v", err)
	}
	delta := hookReconciliationDelta(ready.Reconciliation, "claude_code", "SessionStart")
	if delta == nil || delta.State != hookplan.StateReady {
		t.Fatalf("matching independent content attestations = %+v, want ready", delta)
	}
	if len(ready.Observation.Bindings) != 1 || ready.Observation.Bindings[0].HandlerRevision != desiredRevision {
		t.Fatalf("observed handler attestation = %+v, want %s", ready.Observation.Bindings, desiredRevision)
	}

	writeHarnessHookConfig(t, handlerPath, "#!/usr/bin/env bun\nprocess.stdout.write('{\"drifted\":true}')")
	drifted, err := ReconcileInstalledHooks(compoundHome, harnessHome, harness.DefaultRegistry())
	if err != nil {
		t.Fatalf("ReconcileInstalledHooks() drift error = %v", err)
	}
	delta = hookReconciliationDelta(drifted.Reconciliation, "claude_code", "SessionStart")
	if delta == nil || delta.State != hookplan.StateDrifted {
		t.Fatalf("changed handler content = %+v, want drifted", delta)
	}

	if err := os.Remove(handlerPath); err != nil {
		t.Fatal(err)
	}
	missing, err := ReconcileInstalledHooks(compoundHome, harnessHome, harness.DefaultRegistry())
	if err != nil {
		t.Fatalf("ReconcileInstalledHooks() missing-handler error = %v", err)
	}
	delta = hookReconciliationDelta(missing.Reconciliation, "claude_code", "SessionStart")
	if delta == nil || delta.State != hookplan.StateDrifted {
		t.Fatalf("missing known handler = %+v, want drifted", delta)
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
		{
			// hookplan.HookCommand shell-quotes the binary path (install.go) so a spaced
			// install directory (e.g. an app-bundle path) shows up quoted in both the
			// portable $HOME-form authority ref and the literal absolute-path observation.
			// The two must canonicalize identically (quoting must not read as drift), and the
			// quoted path must resolve as ONE field so the trailing subcommand still
			// participates in drift detection (driftedArg still differs).
			name:       "compound binary with spaced install directory",
			desired:    `'$HOME/Compound Apps/compound' hook pre-tool-use --live --harness codex`,
			observed:   `'` + filepath.Join(harnessHome, "Compound Apps", "compound") + `' hook pre-tool-use --live --harness codex`,
			driftedArg: `'` + filepath.Join(harnessHome, "Compound Apps", "compound") + `' hook session-start --live --harness codex`,
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

// TestCanonicalHookCommandFieldsQuoteTransparentForSpacedPath is a direct falsifier: a quoted
// binary path containing a space (as hookplan.HookCommand now emits for a spaced install
// directory) must tokenize as ONE field, not split at the embedded space. Against the historical
// strings.Fields-based implementation this produced 7 bogus fields ("/tmp/Compound",
// "Apps/compound", "hook", ...) instead of the correct 6.
func TestCanonicalHookCommandFieldsQuoteTransparentForSpacedPath(t *testing.T) {
	cmd := `'/tmp/Compound Apps/compound' hook pre-tool-use --live --harness codex`
	got := canonicalHookCommandFields(cmd)
	want := []string{"/tmp/Compound Apps/compound", "hook", "pre-tool-use", "--live", "--harness", "codex"}
	if len(got) != len(want) {
		t.Fatalf("canonicalHookCommandFields(%q) = %#v (%d fields), want %#v (%d fields)", cmd, got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("canonicalHookCommandFields(%q)[%d] = %q, want %q (full: %#v)", cmd, i, got[i], want[i], got)
		}
	}
}

// TestCanonicalHookCommandFieldsUnquotedUnchanged is the backward-compat invariant: a command
// with no quotes and no spaces-inside-quotes must tokenize identically to the pre-fix
// strings.Fields + per-field quote-trim implementation, so existing installed handler refs and
// invocation revisions never drift because of this change.
func TestCanonicalHookCommandFieldsUnquotedUnchanged(t *testing.T) {
	commands := []string{
		"/usr/local/bin/compound hook pre-tool-use --live --harness codex",
		"$HOME/.bun/bin/bun $HOME/.codex/hooks/RecallContext.hook.ts",
		"compound hook pre-tool-use --live --harness claude-code",
		"",
		"   ",
	}
	for _, cmd := range commands {
		got := canonicalHookCommandFields(cmd)
		want := legacyHookCommandFields(cmd)
		if len(got) != len(want) {
			t.Fatalf("canonicalHookCommandFields(%q) = %#v, want %#v (unquoted backward-compat break)", cmd, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("canonicalHookCommandFields(%q) = %#v, want %#v (unquoted backward-compat break)", cmd, got, want)
			}
		}
	}
}

// legacyHookCommandFields is the pre-fix tokenizer (plain strings.Fields + per-field quote-trim),
// kept here only to pin down the backward-compat invariant above.
func legacyHookCommandFields(command string) []string {
	fields := strings.Fields(command)
	for i := range fields {
		fields[i] = strings.Trim(fields[i], "\"'`")
	}
	return fields
}

// TestCanonicalHookHandlerRefQuoteTransparentForSpacedInstallPath proves the quote-transparent
// tokenizer composes with $HOME normalization: a quoted spaced install path canonicalizes to the
// SAME handler ref whether it's given as a portable "$HOME/..." reference or an absolute path, so
// a correctly-quoted install (hookplan.HookCommand) is never read as drift merely because of how
// its path happens to be represented.
func TestCanonicalHookHandlerRefQuoteTransparentForSpacedInstallPath(t *testing.T) {
	harnessHome := t.TempDir()
	spacedPath := filepath.Join(harnessHome, "Compound Apps", "compound")
	absolute := `'` + spacedPath + `' hook pre-tool-use --live --harness codex`
	portable := `'$HOME/Compound Apps/compound' hook pre-tool-use --live --harness codex`

	got := canonicalHookHandlerRef(absolute, harnessHome)
	want := canonicalHookHandlerRef(portable, harnessHome)
	if got != want {
		t.Fatalf("quoted spaced install path canonicalized differently: absolute=%q portable=%q (quoting introduced false drift)", got, want)
	}
	if got == "" {
		t.Fatal("handler ref empty for spaced install path")
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
