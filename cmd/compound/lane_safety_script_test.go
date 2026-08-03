package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLaneSafetyCheckWarningOnlyExitsZero(t *testing.T) {
	repo := newLaneSafetyFixture(t, false)
	out, err := runLaneSafetyFixture(repo)
	if err != nil {
		t.Fatalf("lane safety warning-only run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "WARN:") {
		t.Fatalf("warning-only output missing WARN verdict:\n%s", out)
	}
}

func TestLaneSafetyCheckBuildFailureExitsNonZero(t *testing.T) {
	repo := newLaneSafetyFixture(t, true)
	out, err := runLaneSafetyFixture(repo)
	if err == nil {
		t.Fatalf("lane safety build failure unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(out, "UNSAFE:") {
		t.Fatalf("build-failure output missing UNSAFE verdict:\n%s", out)
	}
}

func newLaneSafetyFixture(t *testing.T, failBuild bool) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("lane safety script requires bash")
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(repoRoot, "scripts", "lane-safety-check.sh"))
	if err != nil {
		t.Fatal(err)
	}

	fixture := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fixture, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fixture, "cmd", "compound"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "scripts", "lane-safety-check.sh"), script, 0o755); err != nil {
		t.Fatal(err)
	}
	warningSource := "package fixture\n// filepath.Join(home, \"ledger\")\n"
	if err := os.WriteFile(filepath.Join(fixture, "warning.go"), []byte(warningSource), 0o644); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(fixture, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exitCode := "0"
	if failBuild {
		exitCode = "1"
	}
	fakeGo := "#!/usr/bin/env bash\nexit " + exitCode + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(fakeGo), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return fixture
}

func runLaneSafetyFixture(repo string) (string, error) {
	cmd := exec.Command("bash", filepath.Join(repo, "scripts", "lane-safety-check.sh"), repo)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
