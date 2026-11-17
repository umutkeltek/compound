package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/harness"
	"compound/internal/hookplan"
)

// withScript puts a fake harness command on the synthetic PATH that prints
// body whatever it is asked, the way a harness answers its own listing command.
// PATH holds only the synthetic bin, so the script uses the shell's printf.
func (m setupMachine) withScript(t *testing.T, name, body string) {
	t.Helper()
	if strings.Contains(body, "'") {
		t.Fatal("script body must not hold a single quote")
	}
	script := "#!/bin/sh\nprintf '%s\\n' '" + body + "'\n"
	if err := os.WriteFile(filepath.Join(m.bin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// `init --check` takes each wired harness's line from the harness check: a
// plugin OpenCode reports as failed is not wired, however good the file looks.
func TestInitCheckReportsAPluginTheHarnessFailedToLoad(t *testing.T) {
	m := newSetupMachine(t)
	m.withFolder(t, ".config", "opencode")
	if code, out, errOut := m.run(t, initCmd, "--harness", "opencode"); code != 0 {
		t.Fatalf("init exit %d: %s%s", code, out, errOut)
	}
	plugin := filepath.Join(m.userHome, ".config", "opencode", "plugins", "compound.ts")

	m.withScript(t, "opencode", `{"data":[{"id":"compound","source":{"type":"local"},"state":{"status":"failed","error":"Plugin must export default definition"}}]}`)
	code, out, _ := m.run(t, initCmd, "--check", "--harness", "opencode")
	if code != 1 || !strings.Contains(out, "OpenCode: not working: OpenCode reports the Compound plugin failed Plugin must export default definition ("+plugin+")") ||
		!strings.Contains(out, "  fix: ") || !strings.Contains(out, "compound harness check") || strings.Contains(out, "OpenCode: wired") {
		t.Fatalf("a failed plugin must not read as wired (exit %d):\n%s", code, out)
	}

	m.withScript(t, "opencode", `{"data":[{"id":"compound","state":{"status":"active"}}]}`)
	code, out, _ = m.run(t, initCmd, "--check", "--harness", "opencode")
	if code != 0 || !strings.Contains(out, "OpenCode: wired ("+plugin+")\n") {
		t.Fatalf("an active plugin is wired (exit %d):\n%s", code, out)
	}
}

func TestInitCheckReportsCodexHooksTurnedOff(t *testing.T) {
	m := newSetupMachine(t)
	m.withCommand(t, "codex")
	if code, out, errOut := m.run(t, initCmd, "--harness", "codex"); code != 0 {
		t.Fatalf("init exit %d: %s%s", code, out, errOut)
	}
	m.withScript(t, "codex", "goals    stable  true\nhooks    stable  false")
	code, out, _ := m.run(t, initCmd, "--check", "--harness", "codex")
	if code != 1 || !strings.Contains(out, "Codex: not working: SessionStart runs the Compound hook, but codex features: hooks false") ||
		!strings.Contains(out, "fix: set hooks = true under [features]") {
		t.Fatalf("hooks off must not read as wired (exit %d):\n%s", code, out)
	}
	m.withScript(t, "codex", "hooks    stable  true")
	if code, out, _ := m.run(t, initCmd, "--check", "--harness", "codex"); code != 0 || !strings.Contains(out, "Codex: wired (") || strings.Contains(out, "not verified") {
		t.Fatalf("hooks on (exit %d):\n%s", code, out)
	}
}

// When the harness cannot be asked, the line stays wired but says so.
func TestInitCheckSaysWhenTheHarnessCouldNotBeAsked(t *testing.T) {
	m := newSetupMachine(t)
	m.withFolder(t, ".config", "opencode")
	if code, _, _ := m.run(t, initCmd, "--harness", "opencode"); code != 0 {
		t.Fatal("init failed")
	}
	code, out, _ := m.run(t, initCmd, "--check", "--harness", "opencode")
	if code != 0 || !strings.Contains(out, "OpenCode: wired (") || !strings.Contains(out, "not verified: ") || !strings.Contains(out, "not on PATH") {
		t.Fatalf("an unasked plugin (exit %d):\n%s", code, out)
	}
}

// The check's ownership markers are prefixes of the installers' versioned
// markers, so a new installer version is still recognised as Compound's.
func TestWiringMarkersMatchTheInstallers(t *testing.T) {
	if !strings.HasPrefix(hookplan.OpenCodePluginMarker, harness.OpenCodePluginMarker) {
		t.Fatalf("installer marker %q does not start with %q", hookplan.OpenCodePluginMarker, harness.OpenCodePluginMarker)
	}
	if !strings.HasPrefix(hookplan.OmpShimMarker, harness.OmpShimMarker) {
		t.Fatalf("installer marker %q does not start with %q", hookplan.OmpShimMarker, harness.OmpShimMarker)
	}
	if !strings.Contains(hookplan.HookCommandForEvent("/bin/compound", "codex", "SessionStart"), harness.SessionStartHookMarker) {
		t.Fatalf("the installed SessionStart command must carry %q", harness.SessionStartHookMarker)
	}
	if got := hookplan.OpenCodePluginRelPath; "~/"+filepath.ToSlash(got) != harness.DefaultRegistry().Harnesses["opencode"].Wiring.Context.Path {
		t.Fatalf("the OpenCode plugin path in the wiring facts drifted from the installer's %q", got)
	}
	if got := hookplan.OmpShimRelPath; "~/"+filepath.ToSlash(got) != harness.DefaultRegistry().Harnesses["oh_my_pi"].Wiring.Context.Path {
		t.Fatalf("the omp shim path in the wiring facts drifted from the installer's %q", got)
	}
}
