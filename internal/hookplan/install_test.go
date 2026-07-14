package hookplan

import (
	"encoding/json"
	"os"
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

func TestInstallHooksBothHarnesses(t *testing.T) {
	base := t.TempDir()
	rep, err := InstallHooks(base, "/usr/local/bin/compound", "global", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 2 {
		t.Fatalf("expected both harnesses, got %d entries", len(rep.Entries))
	}
	for _, e := range rep.Entries {
		if e.Action != "installed" {
			t.Fatalf("%s action = %q, want installed", e.Harness, e.Action)
		}
	}
	// Claude gets MultiEdit; Codex does not.
	claude := readJSON(t, filepath.Join(base, ".claude", "settings.json"))
	if got := firstMatcher(t, claude); got != "Write|Edit|MultiEdit" {
		t.Fatalf("claude matcher = %q", got)
	}
	codex := readJSON(t, filepath.Join(base, ".codex", "hooks.json"))
	if got := firstMatcher(t, codex); got != "Write|Edit" {
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
