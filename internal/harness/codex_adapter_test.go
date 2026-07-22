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
