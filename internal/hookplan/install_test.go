package hookplan

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
// of silently truncating at the first space — the bug that made the PreToolUse mutation-policy
// guard fail open. It fails against a naive fmt.Sprintf("%s ...", binaryPath) because sh -c would
// then try (and fail) to exec the truncated prefix.
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
	for _, e := range rep.Entries {
		if e.Action != "installed" {
			t.Fatalf("%s action = %q, want installed", e.Harness, e.Action)
		}
	}
	// Universal enforcement is intentionally matcher-wide. Tool-specific
	// authorization belongs in Compound's deterministic action monitor, not in
	// harness config where new tools could bypass the gate.
	claude := readJSON(t, filepath.Join(base, ".claude", "settings.json"))
	if got := firstMatcher(t, claude); got != "*" {
		t.Fatalf("claude matcher = %q", got)
	}
	zai := readJSON(t, filepath.Join(ClaudeZaiProfileDir(base), "settings.json"))
	if got := firstMatcher(t, zai); got != "*" {
		t.Fatalf("claude-zai matcher = %q", got)
	}
	codex := readJSON(t, filepath.Join(base, ".codex", "hooks.json"))
	if got := firstMatcher(t, codex); got != "*" {
		t.Fatalf("codex matcher = %q", got)
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
	pre := got["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 1 {
		t.Fatalf("update must replace in place, got %d entries", len(pre))
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
	if _, err := os.Stat(filepath.Join(base, ".claude", "settings.local.json")); err != nil {
		t.Fatalf("project scope must write settings.local.json: %v", err)
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
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse"} {
		groups, ok := hooks[event].([]any)
		if !ok || len(groups) == 0 {
			t.Fatalf("claude-zai settings missing %s: %#v", event, hooks[event])
		}
		wantCommand := HookCommandForEvent("/opt/Compound Apps/compound", "claude-zai", event)
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
