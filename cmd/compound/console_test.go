package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
