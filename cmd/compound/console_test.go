package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConsoleCompoundBinPrefersStablePathForTemporaryExecutable(t *testing.T) {
	stable := filepath.Join(t.TempDir(), "bin", "compound")
	got := resolveConsoleCompoundBin(filepath.Join(string(filepath.Separator), "tmp", "compound-dashboard"), func(name string) (string, error) {
		if name != "compound" {
			t.Fatalf("lookup name = %q", name)
		}
		return stable, nil
	})
	if got != stable {
		t.Fatalf("compound bin = %q, want %q", got, stable)
	}
}

func TestConsoleCompoundBinKeepsStableExecutable(t *testing.T) {
	current := filepath.Join(string(filepath.Separator), "usr", "local", "bin", "compound")
	got := resolveConsoleCompoundBin(current, func(name string) (string, error) {
		t.Fatalf("stable executable should not need PATH lookup")
		return "", os.ErrNotExist
	})
	if got != current {
		t.Fatalf("compound bin = %q, want %q", got, current)
	}
}

func TestConsoleOperatorDryRunWiresLocalMutationEnv(t *testing.T) {
	root := t.TempDir()
	consoleDir := filepath.Join(root, "console")
	if err := os.MkdirAll(consoleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consoleDir, "package.json"), []byte(`{"scripts":{"dev":"next dev -p 3777"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	homePath := filepath.Join(root, ".compound")
	t.Setenv("COMPOUND_HOME", homePath)

	out := captureStdout(t, func() {
		cmdConsole([]string{"--operator", "--console-dir", consoleDir, "--dry-run", "--json"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("console dry-run missing JSON:\n%s", out)
	}
	var plan consoleLaunchPlan
	if err := json.Unmarshal([]byte(out[start:]), &plan); err != nil {
		t.Fatalf("parse console launch plan: %v\n%s", err, out)
	}
	if plan.Mode != "operator" || !plan.DryRun || !plan.MutationsEnabled || plan.PrivateFallbacks {
		t.Fatalf("operator launch flags = %+v", plan)
	}
	if got, want := plan.Command, []string{"bun", "run", "dev"}; !sameStringSlice(got, want) {
		t.Fatalf("command = %v, want %v", got, want)
	}
	if plan.ConsoleDir != consoleDir {
		t.Fatalf("console_dir = %q, want %q", plan.ConsoleDir, consoleDir)
	}
	if plan.Env["COMPOUND_HOME"] != homePath ||
		plan.Env["COMPOUND_CONSOLE_MUTATIONS"] != "1" ||
		strings.TrimSpace(plan.Env["COMPOUND_BIN"]) == "" ||
		plan.Env["COMPOUND_CONSOLE_PRIVATE_FALLBACKS"] != "" {
		t.Fatalf("env = %+v", plan.Env)
	}
}

func TestConsoleOperatorDryRunSurfacesPinnedHomeIdentity(t *testing.T) {
	root := t.TempDir()
	consoleDir := filepath.Join(root, "console")
	if err := os.MkdirAll(consoleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consoleDir, "package.json"), []byte(`{"scripts":{"dev":"next dev -p 3777"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	homePath := filepath.Join(root, ".compound")
	t.Setenv("COMPOUND_HOME", homePath)

	// Dry-run WITHOUT --json prints the human launch line: it must name the exact
	// home this instance is pinned to so an operator driving several ports can
	// tell which instance a click lands on. A regression that drops the home from
	// the line reintroduces the "which instance am I on?" blind spot the
	// 2026-07-11 breach exploited.
	out := captureStdout(t, func() {
		cmdConsole([]string{"--operator", "--console-dir", consoleDir, "--dry-run"})
	})
	wantHash := consoleHomeShortHash(homePath)
	for _, want := range []string{"home ", ".compound", "#" + wantHash, homePath} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run launch line missing %q:\n%s", want, out)
		}
	}
	if len(wantHash) != 7 {
		t.Fatalf("home short hash = %q, want 7 hex chars", wantHash)
	}
}

func TestConsoleLaunchPlanPinsResolvedHome(t *testing.T) {
	root := t.TempDir()
	consoleDir := filepath.Join(root, "console")
	if err := os.MkdirAll(consoleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consoleDir, "package.json"), []byte(`{"scripts":{"dev":"next dev -p 3777"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	scratchA := filepath.Join(root, "scratch-a", ".compound")
	t.Setenv("COMPOUND_HOME", scratchA)

	plan, err := buildConsoleLaunchPlan(consoleDir, false, true)
	if err != nil {
		t.Fatal(err)
	}
	// The child process is pinned to the started home by construction: the launch
	// env carries the resolved home, and consoleLaunchEnv injects exactly that —
	// even overriding a DIFFERENT COMPOUND_HOME that happened to be in the parent
	// environment. This is what stops a clone from inheriting/writing another home.
	if plan.Env["COMPOUND_HOME"] != scratchA {
		t.Fatalf("plan home = %q, want pinned %q", plan.Env["COMPOUND_HOME"], scratchA)
	}
	if !filepath.IsAbs(plan.Env["COMPOUND_HOME"]) {
		t.Fatalf("pinned home must be absolute, got %q", plan.Env["COMPOUND_HOME"])
	}
	t.Setenv("COMPOUND_HOME", filepath.Join(root, "scratch-b", ".compound"))
	env := consoleLaunchEnv(plan.Env)
	var got string
	for _, entry := range env {
		if strings.HasPrefix(entry, "COMPOUND_HOME=") {
			got = strings.TrimPrefix(entry, "COMPOUND_HOME=")
		}
	}
	if got != scratchA {
		t.Fatalf("launch env COMPOUND_HOME = %q, want %q (parent env must not leak a different home)", got, scratchA)
	}
}

func TestConsoleOperatorDryRunCanEnablePrivateFallbacks(t *testing.T) {
	root := t.TempDir()
	consoleDir := filepath.Join(root, "console")
	if err := os.MkdirAll(consoleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consoleDir, "package.json"), []byte(`{"scripts":{"dev":"next dev -p 3777"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMPOUND_HOME", filepath.Join(root, ".compound"))

	plan, err := buildConsoleLaunchPlan(consoleDir, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.PrivateFallbacks || plan.Env["COMPOUND_CONSOLE_PRIVATE_FALLBACKS"] != "1" {
		t.Fatalf("private fallback env missing: %+v", plan)
	}
}
