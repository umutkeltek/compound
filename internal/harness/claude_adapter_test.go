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

// fakeBinaryPath creates a temp directory containing a shell script named
// `name` (e.g. "claude") that writes its argv to argvLogPath and exits
// with exitCode. The returned path is suitable for prepending to PATH so
// exec.CommandContext finds the fake instead of the real binary.
//
// This pattern is the standard "mock a subprocess via PATH" idiom. The
// fake uses #!/bin/sh so it works on Linux and macOS; CI on Windows is
// out of scope for the harness adapter (the OSEntrypointsAdapter
// doesn't claim Windows support).
func fakeBinaryPath(t *testing.T, name, argvLogPath string, exitCode int) string {
	t.Helper()
	binDir := t.TempDir()
	script := `#!/bin/sh
echo "$@" >> ` + argvLogPath + `
exit ` + intToStr(exitCode) + `
`
	scriptPath := filepath.Join(binDir, name)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
	return binDir
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + intToStr(-n)
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

// withPATH prepends dir to PATH for the duration of t.
func withPATH(t *testing.T, dir string) {
	t.Helper()
	old := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+old)
}

// TestClaudeAdapterHappyPath: a fake `claude` script on PATH is invoked
// with --print --dangerously-skip-permissions <prompt>. The RunAttempt
// is succeeded, harness="claude", command="claude", and the argv log
// contains the expected flags.
func TestClaudeAdapterHappyPath(t *testing.T) {
	argvLog := filepath.Join(t.TempDir(), "argv.log")
	binDir := fakeBinaryPath(t, "claude", argvLog, 0)
	withPATH(t, binDir)

	home := t.TempDir()
	store := runattempt.Open(home)
	a := NewClaudeAdapter(store)

	out, err := a.Run(context.Background(), RunInput{
		WorkID:    "work_claude",
		Harness:   "claude",
		Workspace: home,
		Prompt:    "hello from claude",
		Now:       time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.RunAttempt.Status != runattempt.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", out.RunAttempt.Status)
	}
	if out.RunAttempt.Harness != "claude" {
		t.Fatalf("harness = %q, want %q", out.RunAttempt.Harness, "claude")
	}
	if out.RunAttempt.Command != "claude" {
		t.Fatalf("command = %q, want %q (adapter overrides Command to its Name)", out.RunAttempt.Command, "claude")
	}
	if out.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", out.ExitCode)
	}
	// Verify the argv log has the expected flags + prompt.
	argvBytes, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("read argv log: %v", err)
	}
	argv := strings.TrimSpace(string(argvBytes))
	if !strings.Contains(argv, "--print") {
		t.Fatalf("argv log missing --print: %q", argv)
	}
	if !strings.Contains(argv, "--dangerously-skip-permissions") {
		t.Fatalf("argv log missing --dangerously-skip-permissions: %q", argv)
	}
	if !strings.Contains(argv, "hello from claude") {
		t.Fatalf("argv log missing prompt: %q", argv)
	}
}

// TestClaudeAdapterFailedExit: a fake `claude` script that exits 1
// produces a failed RunAttempt with exit code 1.
func TestClaudeAdapterFailedExit(t *testing.T) {
	argvLog := filepath.Join(t.TempDir(), "argv.log")
	binDir := fakeBinaryPath(t, "claude", argvLog, 1)
	withPATH(t, binDir)

	home := t.TempDir()
	store := runattempt.Open(home)
	a := NewClaudeAdapter(store)

	out, err := a.Run(context.Background(), RunInput{
		WorkID:    "work_claude_fail",
		Harness:   "claude",
		Workspace: home,
		Prompt:    "this will fail",
		Now:       time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.RunAttempt.Status != runattempt.StatusFailed {
		t.Fatalf("status = %q, want failed (claude exits 1)", out.RunAttempt.Status)
	}
	if out.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1", out.ExitCode)
	}
}

// TestClaudeAdapterName: trivial contract pin. Pinned because the
// Registry slice (C.3) keys on this.
func TestClaudeAdapterName(t *testing.T) {
	a := NewClaudeAdapter(runattempt.Open(t.TempDir()))
	if a.Name() != "claude" {
		t.Fatalf("name = %q, want %q", a.Name(), "claude")
	}
}

// TestClaudeAdapterPromptFromArgs: when Prompt is empty, the adapter
// uses Args[0] as the prompt. Pinned: callers that already have argv
// prepared shouldn't have to duplicate into a Prompt field.
func TestClaudeAdapterPromptFromArgs(t *testing.T) {
	argvLog := filepath.Join(t.TempDir(), "argv.log")
	binDir := fakeBinaryPath(t, "claude", argvLog, 0)
	withPATH(t, binDir)

	home := t.TempDir()
	store := runattempt.Open(home)
	a := NewClaudeAdapter(store)

	_, err := a.Run(context.Background(), RunInput{
		WorkID:    "work_claude_args",
		Harness:   "claude",
		Workspace: home,
		Args:      []string{"prompt from args"},
		Now:       time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	argvBytes, _ := os.ReadFile(argvLog)
	if !strings.Contains(string(argvBytes), "prompt from args") {
		t.Fatalf("argv log missing Args[0] prompt: %q", argvBytes)
	}
}
