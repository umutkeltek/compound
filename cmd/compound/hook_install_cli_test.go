package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdHookInstallHelpDoesNotMutateHarnessConfig(t *testing.T) {
	base := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		cmdHookInstall([]string{
			"--help",
			"--base", base,
			"--binary", binary,
			"--harness", "codex",
		})
	})

	if !strings.Contains(out, "usage: compound hook install") {
		t.Fatalf("help output missing usage: %s", out)
	}
	if _, err := os.Stat(filepath.Join(base, ".codex", "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("help mutated Codex hooks config: %v", err)
	}
}
