package hookplan

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func noCommands(string) (string, error) { return "", errors.New("not found") }

func TestDetectSetupHarnessesFindsFoldersAndCommands(t *testing.T) {
	base := t.TempDir()
	got := DetectSetupHarnesses(base, noCommands)
	for _, h := range got {
		if h.Present {
			t.Fatalf("an empty machine has no harness, got %+v", h)
		}
	}
	if err := os.MkdirAll(filepath.Join(base, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	onPath := func(name string) (string, error) {
		if name == "codex" {
			return "/synthetic/bin/codex", nil
		}
		return "", errors.New("not found")
	}
	byName := map[string]SetupHarness{}
	for _, h := range DetectSetupHarnesses(base, onPath) {
		byName[h.Name] = h
	}
	if h := byName["claude-code"]; !h.Present || !h.Wireable || h.Evidence != filepath.Join(base, ".claude") {
		t.Fatalf("claude-code from its folder: %+v", h)
	}
	if h := byName["codex"]; !h.Present || h.Evidence != "/synthetic/bin/codex" {
		t.Fatalf("codex from its command: %+v", h)
	}
	if h := byName["opencode"]; h.Present || h.Wireable {
		t.Fatalf("opencode absent and never wired by init: %+v", h)
	}
	for _, h := range got {
		if strings.Contains(h.Name, "zai") || strings.Contains(h.Name, "minimax") || strings.Contains(h.Name, "qwen") {
			t.Fatalf("setup roster must not carry provider profiles: %+v", h)
		}
	}
}

func TestHarnessConfigPresentNeedsTheHarnessFolder(t *testing.T) {
	base := t.TempDir()
	if HarnessConfigPresent(base, "claude-zai", "global") || HarnessConfigPresent(base, "codex", "global") {
		t.Fatal("no folder, not present")
	}
	if err := os.MkdirAll(filepath.Join(base, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !HarnessConfigPresent(base, "codex", "global") {
		t.Fatal("codex folder exists")
	}
}

func TestDefaultSetupBaseProjectScopeIsTheCurrentProject(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	got, err := DefaultSetupBase("project")
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(project); mustEval(t, got) != want {
		t.Fatalf("project scope base = %q, want the current project %q", got, project)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got, _ := DefaultSetupBase("global"); got != home {
		t.Fatalf("global scope base = %q, want home %q", got, home)
	}
}

func mustEval(t *testing.T, path string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestUninstallHooksKeepsEveryOtherKeyAndHook(t *testing.T) {
	base := t.TempDir()
	settings := filepath.Join(base, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := `{"theme":"dark","permissions":{"allow":["Bash(ls)"]},"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/usr/local/bin/my-guard"}]}]}}`
	if err := os.WriteFile(settings, []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallHooks(base, "/opt/compound/compound", "global", []string{"claude-code"}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustRead(t, settings), "hook session-start --live") {
		t.Fatal("install did not wire Compound")
	}

	checked, err := UninstallHooks(base, "/opt/compound/compound", "global", []string{"claude-code"}, "", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if checked.Entries[0].Action != "would remove" || !strings.Contains(mustRead(t, settings), "hook session-start --live") {
		t.Fatalf("check must only report: %+v", checked.Entries)
	}

	var published HookInstallReport
	rep, err := UninstallHooks(base, "/opt/compound/compound", "global", []string{"claude-code"}, "", false, func(r HookInstallReport) error {
		published = r
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Action != "removed" || len(rep.Entries[0].Bindings) == 0 {
		t.Fatalf("uninstall report = %+v", rep.Entries)
	}
	if len(published.Entries) != 1 || published.Entries[0].Action != "removed" {
		t.Fatalf("authority publication = %+v", published.Entries)
	}
	m := readJSON(t, settings)
	if m["theme"] != "dark" || m["permissions"] == nil {
		t.Fatalf("unrelated keys changed: %v", m)
	}
	body := mustRead(t, settings)
	if strings.Contains(body, "--live") || !strings.Contains(body, "/usr/local/bin/my-guard") {
		t.Fatalf("uninstall must remove only Compound's hooks:\n%s", body)
	}

	again, err := UninstallHooks(base, "/opt/compound/compound", "global", []string{"claude-code"}, "", false, func(HookInstallReport) error {
		t.Fatal("nothing changed, nothing to publish")
		return nil
	})
	if err != nil || again.Entries[0].Action != "absent" {
		t.Fatalf("second uninstall = %+v, %v", again.Entries, err)
	}
}

func TestUninstallHooksDropsTheEmptyHooksObjectAndCreatesNoFile(t *testing.T) {
	base := t.TempDir()
	settings := filepath.Join(base, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"model":"sonnet"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallHooks(base, "/opt/compound/compound", "global", []string{"claude-code"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := UninstallHooks(base, "/opt/compound/compound", "global", []string{"claude-code", "codex"}, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m := readJSON(t, settings)
	if _, ok := m["hooks"]; ok || m["model"] != "sonnet" || len(m) != 1 {
		t.Fatalf("after install+uninstall the file should hold only the user's key, got %v", m)
	}
	if _, err := os.Stat(filepath.Join(base, ".codex")); !os.IsNotExist(err) {
		t.Fatalf("uninstall created a codex folder: %v", err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
