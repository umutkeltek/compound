package lanes

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRefreshCLIRefusesUnreadableManifest(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "installed-hooks.yaml"), []byte("hooks: [invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RunCLIFromArgs([]string{"--home", home, "--base", t.TempDir(), "--binary", "missing"}, time.Now()); err == nil {
		t.Fatal("refresh CLI reported success after manifest read failed")
	}
}

func TestRefreshCLIRefusesIncompleteLane(t *testing.T) {
	home, base := t.TempDir(), t.TempDir()
	binary := filepath.Join(t.TempDir(), "compound-synthetic")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	writeInstalledHooksManifest(t, home, map[string]string{"codex": "binary:" + binary})
	if err := RunCLIFromArgs([]string{"--home", home, "--base", base, "--binary", binary}, time.Now()); err == nil {
		t.Fatal("refresh CLI reported success for an unbound installed lane")
	}
}

func TestRefreshCLIAllowsEmptyFreshHome(t *testing.T) {
	if err := RunCLIFromArgs([]string{"--home", t.TempDir(), "--base", t.TempDir(), "--binary", "unused"}, time.Now()); err != nil {
		t.Fatalf("fresh home has no required bindings: %v", err)
	}
}
