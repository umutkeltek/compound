package hookplan

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

// TestHookCommandQuotesSpacedPath proves HookCommand shell-quotes a binaryPath containing a space
// (e.g. an app-bundle install path) so a real POSIX shell execs the FULL path as one token instead
// of silently truncating at the first space. It fails against a naive
// fmt.Sprintf("%s ...", binaryPath) because sh -c would then try (and fail) to
// execute the truncated prefix.
func TestHookCommandQuotesSpacedPath(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH to prove real shell word-splitting")
	}
	dir := t.TempDir()
	spacedDir := filepath.Join(dir, "Compound Apps")
	if err := os.MkdirAll(spacedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(spacedDir, "compound")
	outFile := filepath.Join(dir, "invoked-as.txt")
	// A stand-in "compound" binary that records the exact path it was invoked as ($0), so we can
	// tell whether the shell resolved the first word of the emitted command to the full spaced
	// path or to a truncated prefix.
	script := "#!/bin/sh\nprintf '%s' \"$0\" > \"" + outFile + "\"\n"
	if err := os.WriteFile(binaryPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := HookCommand(binaryPath, "codex")
	if !strings.Contains(cmd, hookCommandMarker) {
		t.Fatalf("HookCommand() = %q, missing marker %q", cmd, hookCommandMarker)
	}

	if err := exec.Command("sh", "-c", cmd).Run(); err != nil {
		t.Fatalf("sh -c %q failed (spaced binary path was split at the space): %v", cmd, err)
	}
	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("stand-in binary never ran (spaced path split by the shell): %v", err)
	}
	if string(got) != binaryPath {
		t.Fatalf("shell resolved the hook binary to %q, want the exact spaced path %q", got, binaryPath)
	}
}

func TestInstallHooksAllHarnesses(t *testing.T) {
	base := t.TempDir()
	rep, err := InstallHooks(base, "/usr/local/bin/compound", "global", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 3 {
		t.Fatalf("expected all managed harnesses, got %d entries", len(rep.Entries))
	}
	reportJSON, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var publicReport struct {
		Entries []struct {
			Matcher  string `json:"matcher"`
			Command  string `json:"command"`
			Bindings []struct {
				Event   string `json:"event"`
				Matcher string `json:"matcher"`
				Command string `json:"command"`
			} `json:"bindings"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(reportJSON, &publicReport); err != nil {
		t.Fatal(err)
	}
	for i, entry := range publicReport.Entries {
		harnessName := rep.Entries[i].Harness
		if len(entry.Bindings) != 4 {
			t.Fatalf("%s reports %d bindings, want all 4 lifecycle events", harnessName, len(entry.Bindings))
		}
		foundPreTool := false
		for _, binding := range entry.Bindings {
			if binding.Event == "PreToolUse" {
				foundPreTool = true
				if harnessName == claudeZaiHarness {
					if binding.Matcher != entry.Matcher || binding.Command != entry.Command ||
						!strings.Contains(binding.Command, " --enforce") {
						t.Fatalf("isolated guard projection drifted from binding: entry=%+v binding=%+v", entry, binding)
					}
				} else {
					if entry.Matcher != "" || entry.Command != "" {
						t.Fatalf("%s observation-only posture advertised a blocking guard: %+v", harnessName, entry)
					}
					if strings.Contains(binding.Command, " --enforce") {
						t.Fatalf("%s observation-only PreToolUse command enforces: %q", harnessName, binding.Command)
					}
				}
			}
		}
		if !foundPreTool {
			t.Fatalf("%s omitted PreToolUse action observation: %+v", harnessName, entry.Bindings)
		}
	}
	for _, e := range rep.Entries {
		if e.Action != "installed" {
			t.Fatalf("%s action = %q, want installed", e.Harness, e.Action)
		}
		if e.Harness == "codex" || e.Harness == "claude-code" {
			if e.Matcher != "" || e.Command != "" {
				t.Fatalf("%s observation-only install advertised a guard: %+v", e.Harness, e)
			}
		} else if e.Matcher != "*" || !strings.Contains(e.Command, " --enforce") {
			t.Fatalf("%s blocking guard report = %+v", e.Harness, e)
		}
	}
	// PreToolUse observation and isolated-profile enforcement are intentionally
	// matcher-wide so newly added tools still produce action evidence.
	claude := readJSON(t, filepath.Join(base, ".claude", "settings.json"))
	if got := firstMatcher(t, claude); got != "*" {
		t.Fatalf("claude matcher = %q", got)
	}
	zai := readJSON(t, filepath.Join(ClaudeZaiProfileDir(base), "settings.json"))
	if got := firstMatcher(t, zai); got != "*" {
		t.Fatalf("claude-zai matcher = %q", got)
	}
	codex := readJSON(t, filepath.Join(base, ".codex", "hooks.json"))
	for harnessName, cfg := range map[string]map[string]any{
		"claude-code": claude,
		"claude-zai":  zai,
		"codex":       codex,
	} {
		entry := hookInstallEntryForHarness(t, rep, harnessName)
		for _, binding := range entry.Bindings {
			if !hasHookCommand(cfg, binding.Event, binding.Command) {
				t.Fatalf("%s install did not consume semantic %s binding", harnessName, binding.Event)
			}
		}
	}
}

func TestInstallHooksPreflightsEveryHarnessConfigBeforeWriting(t *testing.T) {
	base := t.TempDir()
	claudePath := filepath.Join(base, ".claude", "settings.json")
	codexPath := filepath.Join(base, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(codexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	claudeBefore := []byte("{\n  \"permissions\": {\"defaultMode\": \"bypassPermissions\"}\n}\n")
	codexBefore := []byte("{not-json\n")
	if err := os.WriteFile(claudePath, claudeBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, codexBefore, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallHooks(base, "/bin/compound", "global", nil, false); err == nil {
		t.Fatal("all-harness install accepted an invalid later Codex config")
	}

	claudeAfter, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(claudeAfter) != string(claudeBefore) {
		t.Fatalf("Claude config changed before later-harness validation completed:\nbefore=%s\nafter=%s", claudeBefore, claudeAfter)
	}
	zaiPath := filepath.Join(ClaudeZaiProfileDir(base), "settings.json")
	if _, err := os.Stat(zaiPath); !os.IsNotExist(err) {
		t.Fatalf("isolated Claude config was written before later-harness validation completed: %v", err)
	}
	codexAfter, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(codexAfter) != string(codexBefore) {
		t.Fatalf("invalid Codex config changed during failed preflight:\nbefore=%s\nafter=%s", codexBefore, codexAfter)
	}
}

func TestInstallHooksRollsBackEveryNativeConfigWhenPublishFails(t *testing.T) {
	base := t.TempDir()
	claudePath := filepath.Join(base, ".claude", "settings.json")
	codexPath := filepath.Join(base, ".codex", "hooks.json")
	for _, path := range []string{claudePath, codexPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	claudeBefore := []byte(`{
  "permissions": {"defaultMode": "bypassPermissions"},
  "hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "my-own-claude-hook"}]}]}
}
`)
	codexBefore := []byte(`{
  "custom_setting": true,
  "hooks": {"PreToolUse": [{"matcher": "shell", "hooks": [{"type": "command", "command": "my-own-codex-hook"}]}]}
}
`)
	if err := os.WriteFile(claudePath, claudeBefore, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, codexBefore, 0o640); err != nil {
		t.Fatal(err)
	}

	publishErr := errors.New("synthetic authority publication failure")
	publisherCalled := false
	_, err := InstallHooksWithConfigDirAndPublish(
		base,
		"/bin/compound",
		"global",
		nil,
		"",
		false,
		func(report HookInstallReport) error {
			publisherCalled = true
			if len(report.Entries) != 3 {
				t.Fatalf("publisher received %d entries, want one complete all-harness report", len(report.Entries))
			}
			for _, entry := range report.Entries {
				data, readErr := os.ReadFile(entry.ConfigPath)
				if readErr != nil {
					t.Fatalf("publisher could not read committed %s config: %v", entry.Harness, readErr)
				}
				if got := hookConfigRevision(data); got != entry.ConfigRevision {
					t.Fatalf("%s committed revision = %s, report says %s", entry.Harness, got, entry.ConfigRevision)
				}
			}
			return publishErr
		},
	)
	if !errors.Is(err, publishErr) {
		t.Fatalf("install error = %v, want publication failure", err)
	}
	if !publisherCalled {
		t.Fatal("publisher was not called after native configs committed")
	}

	for _, fixture := range []struct {
		path string
		body []byte
		mode os.FileMode
	}{
		{path: claudePath, body: claudeBefore, mode: 0o600},
		{path: codexPath, body: codexBefore, mode: 0o640},
	} {
		after, readErr := os.ReadFile(fixture.path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(after) != string(fixture.body) {
			t.Fatalf("rollback changed %s:\nbefore=%s\nafter=%s", fixture.path, fixture.body, after)
		}
		info, statErr := os.Stat(fixture.path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if got := info.Mode().Perm(); got != fixture.mode {
			t.Fatalf("rollback mode for %s = %o, want %o", fixture.path, got, fixture.mode)
		}
	}
	zaiPath := filepath.Join(ClaudeZaiProfileDir(base), "settings.json")
	if _, statErr := os.Stat(zaiPath); !os.IsNotExist(statErr) {
		t.Fatalf("rollback left previously absent isolated config at %s: %v", zaiPath, statErr)
	}
}

func TestInstallHooksRollbackDoesNotClobberConcurrentConfigChange(t *testing.T) {
	base := t.TempDir()
	concurrent := []byte("{\n  \"concurrent_user_edit\": true\n}\n")
	publishErr := errors.New("synthetic authority publication failure")
	var configPath string
	_, err := InstallHooksWithConfigDirAndPublish(
		base,
		"/bin/compound",
		"global",
		[]string{"codex"},
		"",
		false,
		func(report HookInstallReport) error {
			configPath = report.Entries[0].ConfigPath
			if writeErr := os.WriteFile(configPath, concurrent, 0o644); writeErr != nil {
				t.Fatal(writeErr)
			}
			return publishErr
		},
	)
	if !errors.Is(err, publishErr) {
		t.Fatalf("install error = %v, want publication failure", err)
	}
	if err == nil || !strings.Contains(err.Error(), "changed after commit") {
		t.Fatalf("install error = %v, want rollback concurrency conflict", err)
	}
	after, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("rollback removed concurrent edit: %v", readErr)
	}
	if string(after) != string(concurrent) {
		t.Fatalf("rollback clobbered concurrent edit:\nwant=%s\ngot=%s", concurrent, after)
	}
}

func TestCodexInstallReplacesOnlyCompoundPreToolUseBindingWithObservation(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{
  "hooks": {
    "PreToolUse": [
      {"matcher": "*", "hooks": [{"type": "command", "command": "'/old/compound' hook pre-tool-use --live --harness codex"}]},
      {"matcher": "apply_patch", "hooks": [{"type": "command", "command": "my-own-hook"}]}
    ]
  }
}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallHooks(base, "/bin/compound", "global", []string{"codex"}, false); err != nil {
		t.Fatal(err)
	}

	got := readJSON(t, path)
	hooks := got["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("Codex reconciliation must preserve user hook plus one Compound observation: %+v", pre)
	}
	foundUser, foundCompoundObservation := false, false
	for _, group := range pre {
		command := firstCommand(group)
		switch {
		case command == "my-own-hook":
			foundUser = true
		case strings.Contains(command, hookCommandMarker) && !strings.Contains(command, " --enforce"):
			foundCompoundObservation = true
		}
	}
	if !foundUser || !foundCompoundObservation {
		t.Fatalf("Codex reconciliation user=%t observation=%t: %+v", foundUser, foundCompoundObservation, pre)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse"} {
		if !hasHookCommand(got, event, HookCommandForEvent("/bin/compound", "codex", event)) {
			t.Fatalf("Codex reconciliation lost desired %s binding", event)
		}
	}
}

func TestIsolatedBlockingPreToolUseCommandMakesEnforcementExplicit(t *testing.T) {
	bindings, _, err := compiledManagedHookBindings("/bin/compound", claudeZaiHarness, "global")
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range bindings {
		if binding.Event != "PreToolUse" {
			continue
		}
		if !strings.Contains(binding.Command, " --enforce") {
			t.Fatalf("blocking PreToolUse command = %q, want explicit --enforce", binding.Command)
		}
		return
	}
	t.Fatal("isolated Claude posture omitted PreToolUse")
}

func TestInstallerAcceptsOnlyExplicitInteractiveObservationalDefaults(t *testing.T) {
	observational := CompiledPosture{
		Intentions: []CompiledIntention{
			{Intention: IntentionPrepareContext, Disposition: DispositionNative, EnforcementStrength: "context"},
			{Intention: IntentionObserveAction, Disposition: DispositionNative, EnforcementStrength: "observe"},
			{Intention: IntentionObserveOutcome, Disposition: DispositionNative, EnforcementStrength: "observe"},
		},
	}
	for _, harnessName := range []string{"codex", "claude-code"} {
		observational.CompiledFrom.PostureID = "hpost_default_" + strings.ReplaceAll(harnessName, "-", "_")
		if err := validateInstallableDefaultPosture(harnessName, observational); err != nil {
			t.Fatalf("explicit %s observational default rejected: %v", harnessName, err)
		}
	}
	observational.CompiledFrom.PostureID = "hpost_default_claude_zai"
	if err := validateInstallableDefaultPosture(claudeZaiHarness, observational); err == nil {
		t.Fatal("isolated non-mutation-ready Claude posture was silently accepted")
	}
	downgraded := observational
	downgraded.Intentions = append(downgraded.Intentions, CompiledIntention{
		Intention: IntentionGuardMutation, Disposition: DispositionUnsupported,
	})
	if err := validateInstallableDefaultPosture("codex", downgraded); err == nil {
		t.Fatal("unsupported guard was mistaken for an interactive observational default")
	}
	fallback := observational
	fallback.Intentions = append([]CompiledIntention(nil), observational.Intentions...)
	fallback.Intentions[0].Disposition = DispositionFallback
	if err := validateInstallableDefaultPosture("codex", fallback); err == nil {
		t.Fatal("degraded action observation was mistaken for the native default")
	}
}

func TestClaudeZaiInstallBindingsComeFromSemanticPosture(t *testing.T) {
	t.Parallel()

	bindings, compiled, err := compiledManagedHookBindings("/fixture/compound", claudeZaiHarness, "global")
	if err != nil {
		t.Fatal(err)
	}
	if !compiled.MutationReady {
		t.Fatalf("attested claude-zai posture is not mutation ready: %+v", compiled)
	}
	gotEvents := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		gotEvents = append(gotEvents, binding.Event)
	}
	wantEvents := []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse"}
	if !slices.Equal(gotEvents, wantEvents) {
		t.Fatalf("installed native events = %v, want compiled %v", gotEvents, wantEvents)
	}
}

func TestInstallHooksNoClobber(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing config: unrelated top-level keys AND an unrelated user PreToolUse hook.
	existing := `{
  "permissions": {"allow": ["Bash(ls)"]},
  "hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "my-own-hook"}]}]}
}`
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallHooks(base, "/bin/compound", "global", []string{"claude-code"}, false); err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, path)
	// Unrelated key preserved.
	if _, ok := got["permissions"]; !ok {
		t.Fatal("no-clobber broken: permissions key dropped")
	}
	// Both the user's hook AND compound's are present.
	pre := got["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("expected user hook + compound hook = 2 PreToolUse entries, got %d", len(pre))
	}
	foundUser, foundCompound := false, false
	for _, e := range pre {
		cmd := firstCommand(e)
		if cmd == "my-own-hook" {
			foundUser = true
		}
		if strings.Contains(cmd, hookCommandMarker) {
			foundCompound = true
		}
	}
	if !foundUser || !foundCompound {
		t.Fatalf("no-clobber broken: user=%v compound=%v", foundUser, foundCompound)
	}
}

func TestInstallHooksIdempotentAndUpdate(t *testing.T) {
	base := t.TempDir()
	if _, err := InstallHooks(base, "/bin/compound", "global", []string{"codex"}, false); err != nil {
		t.Fatal(err)
	}
	// Second identical install -> ok, no duplicate.
	rep, err := InstallHooks(base, "/bin/compound", "global", []string{"codex"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Entries[0].Action != "ok" {
		t.Fatalf("re-install action = %q, want ok", rep.Entries[0].Action)
	}
	// New binary path -> update in place (not a second entry).
	rep, err = InstallHooks(base, "/opt/compound", "global", []string{"codex"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Entries[0].Action != "updated" {
		t.Fatalf("moved-binary action = %q, want updated", rep.Entries[0].Action)
	}
	got := readJSON(t, filepath.Join(base, ".codex", "hooks.json"))
	sessionStart := got["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(sessionStart) != 1 {
		t.Fatalf("update must replace in place, got %d entries", len(sessionStart))
	}
	if command := firstCommand(sessionStart[0]); command != HookCommandForEvent("/opt/compound", "codex", "SessionStart") {
		t.Fatalf("updated SessionStart command = %q", command)
	}
}

func TestInstallHooksCheckModeDoesNotWrite(t *testing.T) {
	base := t.TempDir()
	rep, err := InstallHooks(base, "/bin/compound", "global", []string{"claude-code"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Entries[0].Action != "absent" {
		t.Fatalf("check on a fresh base = %q, want absent", rep.Entries[0].Action)
	}
	if _, err := os.Stat(filepath.Join(base, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("check mode must not write the config")
	}
}

func TestInstallHooksProjectScope(t *testing.T) {
	base := t.TempDir()
	if _, err := InstallHooks(base, "/bin/compound", "project", []string{"claude-code"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, ".claude", claudeSettingsFile("project"))); err != nil {
		t.Fatalf("project scope must write the local settings overlay: %v", err)
	}
}

func TestInstallClaudeZaiProfileFullRosterNoClobberAndIdempotent(t *testing.T) {
	base := t.TempDir()
	profileDir := filepath.Join(base, "zai-profile")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(profileDir, "settings.json")
	existing := `{
  "permissions": {"defaultMode": "bypassPermissions"},
  "hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "my-own-hook"}]}]}
}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := InstallHooksWithConfigDir(base, "/opt/Compound Apps/compound", "global", []string{"claude-zai"}, profileDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 1 || first.Entries[0].Harness != "claude-zai" || first.Entries[0].Action != "installed" {
		t.Fatalf("first claude-zai install = %+v", first)
	}
	cfg := readJSON(t, settingsPath)
	if _, ok := cfg["permissions"]; !ok {
		t.Fatal("claude-zai install clobbered permissions")
	}
	hooks, ok := cfg["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("settings hooks = %#v", cfg["hooks"])
	}
	wantCommands := map[string]string{}
	for _, binding := range first.Entries[0].Bindings {
		wantCommands[binding.Event] = binding.Command
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse"} {
		groups, ok := hooks[event].([]any)
		if !ok || len(groups) == 0 {
			t.Fatalf("claude-zai settings missing %s: %#v", event, hooks[event])
		}
		wantCommand := wantCommands[event]
		found := false
		for _, group := range groups {
			if firstCommand(group) == wantCommand {
				found = true
			}
		}
		if !found {
			t.Fatalf("claude-zai %s missing command %q: %#v", event, wantCommand, groups)
		}
	}
	if got := matcherForEvent(t, hooks, "PreToolUse"); got != "*" {
		t.Fatalf("claude-zai PreToolUse matcher = %q", got)
	}
	if got := matcherForEvent(t, hooks, "PostToolUse"); got != "*" {
		t.Fatalf("claude-zai PostToolUse matcher = %q", got)
	}

	second, err := InstallHooksWithConfigDir(base, "/opt/Compound Apps/compound", "global", []string{"claude-zai"}, profileDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Entries[0].Action != "ok" {
		t.Fatalf("idempotent claude-zai install action = %q, want ok", second.Entries[0].Action)
	}
	if got := len(hooksForEvent(t, readJSON(t, settingsPath), "SessionStart")); got != 2 {
		t.Fatalf("idempotent install duplicated SessionStart hooks: got %d groups, want user + Compound", got)
	}
}

func hookInstallEntryForHarness(t *testing.T, report HookInstallReport, harnessName string) HookInstallEntry {
	t.Helper()
	for _, entry := range report.Entries {
		if entry.Harness == harnessName {
			return entry
		}
	}
	t.Fatalf("install report omitted harness %q: %+v", harnessName, report.Entries)
	return HookInstallEntry{}
}

func matcherForEvent(t *testing.T, hooks map[string]any, event string) string {
	t.Helper()
	groups, ok := hooks[event].([]any)
	if !ok {
		t.Fatalf("%s groups = %#v", event, hooks[event])
	}
	for _, group := range groups {
		entry, _ := group.(map[string]any)
		if strings.Contains(firstCommand(group), "--harness claude-zai") {
			matcher, _ := entry["matcher"].(string)
			return matcher
		}
	}
	return ""
}

func hooksForEvent(t *testing.T, cfg map[string]any, event string) []any {
	t.Helper()
	hooks, ok := cfg["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("settings hooks = %#v", cfg["hooks"])
	}
	groups, ok := hooks[event].([]any)
	if !ok {
		t.Fatalf("%s groups = %#v", event, hooks[event])
	}
	return groups
}

func firstMatcher(t *testing.T, cfg map[string]any) string {
	t.Helper()
	pre := cfg["hooks"].(map[string]any)["PreToolUse"].([]any)
	return pre[0].(map[string]any)["matcher"].(string)
}

func firstCommand(entry any) string {
	em, ok := entry.(map[string]any)
	if !ok {
		return ""
	}
	inner, _ := em["hooks"].([]any)
	for _, h := range inner {
		if hm, ok := h.(map[string]any); ok {
			if cmd, _ := hm["command"].(string); cmd != "" {
				return cmd
			}
		}
	}
	return ""
}

func hasHookCommand(cfg map[string]any, event, command string) bool {
	hooks, _ := cfg["hooks"].(map[string]any)
	groups, _ := hooks[event].([]any)
	for _, group := range groups {
		if firstCommand(group) == command {
			return true
		}
	}
	return false
}
