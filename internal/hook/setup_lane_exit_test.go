package hook

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A broken lane file is a row for `hook install` and `hook uninstall` with every
// harness, and a failed run only when the record-only lane set is named on its own.
func TestHookInstallFailsOnABrokenLaneOnlyWhenTheLanesAreNamed(t *testing.T) {
	m := newSetupMachine(t)
	m.withClaude(t)
	lane := filepath.Join(m.userHome, ".switchback", "config", "claude", "_providers", "kimi", "settings.json")
	if err := os.MkdirAll(filepath.Dir(lane), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lane, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	install := func(a []string, o, e *bytes.Buffer) int { return RunInstallCommand(a, o, e) }
	uninstall := func(a []string, o, e *bytes.Buffer) int { return RunUninstallCommand(a, o, e) }
	for name, cmd := range map[string]func([]string, *bytes.Buffer, *bytes.Buffer) int{"install": install, "uninstall": uninstall} {
		code, out, errOut := m.run(t, cmd, "--check")
		if code != 0 || !strings.Contains(out, "- kimi: error -> "+lane) || !strings.Contains(out, "claude-code:") {
			t.Fatalf("%s --check with every harness = %d\n%s%s", name, code, out, errOut)
		}
		if code, out, errOut := m.run(t, cmd, "--check", "--harness", "claude-provider-lanes"); code != 1 || !strings.Contains(out, "- kimi: error") {
			t.Fatalf("%s --check --harness claude-provider-lanes = %d, want 1\n%s%s", name, code, out, errOut)
		}
	}
}
