package hook

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/hookplan"
)

func (m setupMachine) withFolder(t *testing.T, rel ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(append([]string{m.userHome}, rel...)...), 0o755); err != nil {
		t.Fatal(err)
	}
}

// OpenCode and oh-my-pi have no settings file; init writes Compound's own hook file into the
// folder each one loads on its own. One init wires both, a second reports them, --check exits
// 0, and uninstall by name takes the file out again.
func TestInitWiresOpenCodeAndOhMyPiFromTheirFolders(t *testing.T) {
	m := newSetupMachine(t)
	m.withFolder(t, ".config", "opencode")
	m.withFolder(t, ".omp")
	code, out, errOut := m.run(t, initCmd)
	if code != 0 {
		t.Fatalf("init exit %d: %s%s", code, out, errOut)
	}
	plugin := filepath.Join(m.userHome, ".config", "opencode", "plugins", "compound.ts")
	shim := filepath.Join(m.userHome, ".omp", "agent", "hooks", "pre", "compound.ts")
	for _, want := range []string{
		"Claude Code: not found", "Codex: not found",
		"OpenCode: wired (" + plugin + ")", "oh-my-pi: wired (" + shim + ")",
		"Next: start OpenCode", "no session receipt", "Undo: compound hook uninstall --harness opencode",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("init output missing %q:\n%s", want, out)
		}
	}
	if body, err := os.ReadFile(plugin); err != nil || !strings.HasPrefix(string(body), hookplan.OpenCodePluginMarker) {
		t.Fatalf("plugin not written as compound-owned: %v", err)
	}
	if body, err := os.ReadFile(shim); err != nil || !strings.Contains(string(body), hookplan.OmpShimMarker) {
		t.Fatalf("omp shim not written as compound-owned: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.userHome, ".config", "opencode", "opencode.jsonc")); !os.IsNotExist(err) {
		t.Fatalf("init must not write OpenCode config; the plugins folder is auto-loaded: %v", err)
	}

	code, out, _ = m.run(t, initCmd)
	if code != 0 || !strings.Contains(out, "OpenCode: already wired") || !strings.Contains(out, "oh-my-pi: already wired") {
		t.Fatalf("second init (exit %d):\n%s", code, out)
	}
	code, out, _ = m.run(t, initCmd, "--check")
	if code != 0 || !strings.Contains(out, "OpenCode: wired (") || !strings.Contains(out, "oh-my-pi: wired (") {
		t.Fatalf("--check after init (exit %d):\n%s", code, out)
	}

	uninstall := func(a []string, o, e *bytes.Buffer) int { return RunUninstallCommand(a, o, e) }
	code, out, errOut = m.run(t, uninstall, "--harness", "opencode")
	if code != 0 || !strings.Contains(out, "opencode: removed") {
		t.Fatalf("uninstall opencode (exit %d):\n%s%s", code, out, errOut)
	}
	if _, err := os.Stat(plugin); !os.IsNotExist(err) {
		t.Fatalf("plugin still there after uninstall: %v", err)
	}
	if _, err := os.Stat(shim); err != nil {
		t.Fatal("uninstall --harness opencode removed the omp shim too")
	}
	code, out, _ = m.run(t, initCmd, "--check")
	if code != 1 || !strings.Contains(out, "OpenCode: not wired") || !strings.Contains(out, "Run `compound init` to finish.") {
		t.Fatalf("--check after uninstall must exit 1 (exit %d):\n%s", code, out)
	}
}

// Every roster name is a valid --harness, a command on PATH counts as installed, and a name
// outside the roster is refused with the roster in the message.
func TestInitHarnessFilterCoversTheWholeRoster(t *testing.T) {
	m := newSetupMachine(t)
	m.withCommand(t, "omp")
	code, out, errOut := m.run(t, initCmd, "--harness", "oh-my-pi")
	if code != 0 || !strings.Contains(out, "oh-my-pi: wired (") || strings.Contains(out, "Claude Code") || strings.Contains(out, "OpenCode") {
		t.Fatalf("--harness oh-my-pi must wire only omp (exit %d):\n%s%s", code, out, errOut)
	}
	code, _, errOut = m.run(t, initCmd, "--harness", "cursor")
	if code != 2 || !strings.Contains(errOut, "claude-code, codex, opencode, oh-my-pi") {
		t.Fatalf("an unknown harness must be refused with the roster (exit %d): %s", code, errOut)
	}
}

// A hand-written plugin at OpenCode's path is never overwritten; init says so and exits 1.
func TestInitRefusesToClobberAForeignOpenCodePlugin(t *testing.T) {
	m := newSetupMachine(t)
	m.withFolder(t, ".config", "opencode", "plugins")
	plugin := filepath.Join(m.userHome, ".config", "opencode", "plugins", "compound.ts")
	if err := os.WriteFile(plugin, []byte("export default async () => ({})\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := m.run(t, initCmd)
	if code != 1 || !strings.Contains(errOut, "not compound-owned") {
		t.Fatalf("init over a foreign plugin (exit %d): %s", code, errOut)
	}
	if body, _ := os.ReadFile(plugin); !strings.HasPrefix(string(body), "export default") {
		t.Fatal("the foreign plugin was changed")
	}
}
