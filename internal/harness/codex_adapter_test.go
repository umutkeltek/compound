package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/runattempt"
)

// TestCodexAdapterHappyPath: a fake `codex` script on PATH is invoked
// with `exec --prompt <prompt>`. The RunAttempt is succeeded and the
// argv log contains the expected flags.
func TestCodexAdapterHappyPath(t *testing.T) {
	argvLog := filepath.Join(t.TempDir(), "argv.log")
	binDir := fakeBinaryPath(t, "codex", argvLog, 0)
	withPATH(t, binDir)

	home := t.TempDir()
	store := runattempt.Open(home)
	a := NewCodexAdapter(store)

	out, err := a.Run(context.Background(), RunInput{
		WorkID:    "work_codex",
		Harness:   "codex",
		Workspace: home,
		Prompt:    "hello from codex",
		Now:       time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.RunAttempt.Status != runattempt.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", out.RunAttempt.Status)
	}
	if out.RunAttempt.Harness != "codex" {
		t.Fatalf("harness = %q, want %q", out.RunAttempt.Harness, "codex")
	}
	if out.RunAttempt.Command != "codex" {
		t.Fatalf("command = %q, want %q", out.RunAttempt.Command, "codex")
	}
	if out.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", out.ExitCode)
	}
	argvBytes, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("read argv log: %v", err)
	}
	argv := strings.TrimSpace(string(argvBytes))
	if !strings.Contains(argv, "exec") {
		t.Fatalf("argv log missing exec: %q", argv)
	}
	// The prompt is POSITIONAL: current Codex CLI rejects --prompt with
	// exit 2 (find_d435b414ef9b) — this assertion pins the real
	// invocation so reintroducing the flag fails here, not at dispatch.
	if strings.Contains(argv, "--prompt") {
		t.Fatalf("argv log contains rejected --prompt flag: %q", argv)
	}
	if !strings.Contains(argv, "hello from codex") {
		t.Fatalf("argv log missing positional prompt: %q", argv)
	}
}

// TestCodexAdapterFailedExit: a fake `codex` script that exits 1
// produces a failed RunAttempt.
func TestCodexAdapterFailedExit(t *testing.T) {
	argvLog := filepath.Join(t.TempDir(), "argv.log")
	binDir := fakeBinaryPath(t, "codex", argvLog, 1)
	withPATH(t, binDir)

	home := t.TempDir()
	store := runattempt.Open(home)
	a := NewCodexAdapter(store)

	out, err := a.Run(context.Background(), RunInput{
		WorkID:    "work_codex_fail",
		Harness:   "codex",
		Workspace: home,
		Prompt:    "this will fail",
		Now:       time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.RunAttempt.Status != runattempt.StatusFailed {
		t.Fatalf("status = %q, want failed", out.RunAttempt.Status)
	}
	if out.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1", out.ExitCode)
	}
}

// TestCodexAdapterName: trivial contract pin.
func TestCodexAdapterName(t *testing.T) {
	a := NewCodexAdapter(runattempt.Open(t.TempDir()))
	if a.Name() != "codex" {
		t.Fatalf("name = %q, want %q", a.Name(), "codex")
	}
}

// TestCodexAdapterPerInvocationCodexHome: buildCmd must produce a fresh
// CODEX_HOME for every invocation so concurrent codex execs (or an exec
// running while an interactive `codex` TUI is active) cannot share
// ~/.codex state. Hardening pin for the launch-isolation slice; two
// back-to-back buildCmd calls on the same adapter/workID MUST produce
// two distinct CODEX_HOME values, both rooted in os.TempDir(). This is
// the smallest possible assertion that catches a regression where
// someone reverts to inheriting the harness's ambient CODEX_HOME.
func TestCodexAdapterPerInvocationCodexHome(t *testing.T) {
	a := NewCodexAdapter(runattempt.Open(t.TempDir()))
	in := RunInput{
		WorkID:    "work_codex_isolation",
		Harness:   "codex",
		Workspace: t.TempDir(),
		Prompt:    "anything",
		Now:       time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
	}

	cmd1 := a.buildCmd(context.Background(), in)
	cmd2 := a.buildCmd(context.Background(), in)

	var home1, home2 string
	for _, kv := range cmd1.Env {
		if strings.HasPrefix(kv, "CODEX_HOME=") {
			home1 = strings.TrimPrefix(kv, "CODEX_HOME=")
			break
		}
	}
	for _, kv := range cmd2.Env {
		if strings.HasPrefix(kv, "CODEX_HOME=") {
			home2 = strings.TrimPrefix(kv, "CODEX_HOME=")
			break
		}
	}

	if home1 == "" {
		t.Fatalf("cmd1 env missing CODEX_HOME; env=%v", cmd1.Env)
	}
	if home2 == "" {
		t.Fatalf("cmd2 env missing CODEX_HOME; env=%v", cmd2.Env)
	}
	if home1 == home2 {
		t.Fatalf("CODEX_HOME collision: two invocations of buildCmd got the same dir %q", home1)
	}
	if !strings.HasPrefix(home1, os.TempDir()) {
		t.Fatalf("home1 = %q, want prefix %q", home1, os.TempDir())
	}
	if !strings.HasPrefix(home2, os.TempDir()) {
		t.Fatalf("home2 = %q, want prefix %q", home2, os.TempDir())
	}
	if _, err := os.Stat(home1); err != nil {
		t.Fatalf("home1 not created: %v", err)
	}
	if _, err := os.Stat(home2); err != nil {
		t.Fatalf("home2 not created: %v", err)
	}
}

// TestCodexAdapterCallerEnvOverridesCodexHome: a caller-supplied
// in.Env with CODEX_HOME= must win over the per-invocation default.
// Mirrors the env-precedence comment in buildCmd; catches a regression
// where someone reorders the appends and accidentally lets the default
// shadow a caller override.
func TestCodexAdapterCallerEnvOverridesCodexHome(t *testing.T) {
	a := NewCodexAdapter(runattempt.Open(t.TempDir()))
	override := t.TempDir()
	in := RunInput{
		WorkID:    "work_codex_override",
		Harness:   "codex",
		Workspace: t.TempDir(),
		Prompt:    "anything",
		Env:       []string{"CODEX_HOME=" + override},
		Now:       time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
	}
	cmd := a.buildCmd(context.Background(), in)

	// Walk env in order; the LAST CODEX_HOME= line wins (exec.Cmd uses
	// "last duplicate wins" per its docs).
	var last string
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "CODEX_HOME=") {
			last = strings.TrimPrefix(kv, "CODEX_HOME=")
		}
	}
	if last != override {
		t.Fatalf("last CODEX_HOME = %q, want caller override %q", last, override)
	}
}
