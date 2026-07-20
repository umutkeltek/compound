package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/runattempt"
	"compound/internal/work"
)

// TestHarnessExecutorsList: compound harness executors lists the three
// built-in adapters (claude, codex, os-harness-entrypoints). Pinned so
// the CLI surface always reflects the registry; if a built-in is
// accidentally removed, the test fails and the operator sees the gap.
func TestHarnessExecutorsList(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdHarness([]string{"executors"})
	})
	for _, want := range []string{"claude", "codex", "os-harness-entrypoints"} {
		if !strings.Contains(out, want) {
			t.Fatalf("executors list missing %q:\n%s", want, out)
		}
	}
}

// TestHarnessExecutorsListJSON: --json flag produces a JSON object
// with the executors array. Pinned so the operator can pipe the list
// into jq / other tooling.
func TestHarnessExecutorsListJSON(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	cmdHarness([]string{"executors", "--json"})
	w.Close()
	chunk := make([]byte, 4096)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
		}
		if err != nil {
			break
		}
	}
	os.Stdout = old
	var got struct {
		Executors []string `json:"executors"`
	}
	if err := json.Unmarshal([]byte(buf.String()), &got); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, buf.String())
	}
	if len(got.Executors) < 3 {
		t.Fatalf("expected at least 3 executors, got %v", got.Executors)
	}
}

// TestHarnessRunOSEntrypoints: drives the os-harness-entrypoints
// adapter with a real echo command. The work envelope must exist (we
// create one in the test). The RunAttempt terminalizes succeeded.
//
// Falsifier: removing the adapter.Run call turns this RED with
// "expected succeeded status, got: ..." -- the test exercises the
// full Mint->SetWorkspace->SetCommand->spawn->capture->Complete chain
// end-to-end through the CLI surface.
func TestHarnessRunOSEntrypoints(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	ws, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w, err := ws.Create(work.CreateInput{
		Owner:            "codex",
		SuccessPredicate: "harness run smoke test",
	}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdHarness([]string{"run", "--harness", "os-harness-entrypoints", "--work", w.ID, "--workspace", homePath, "--", "echo", "harness-run-smoke"})
	})
	if !strings.Contains(out, "status: succeeded") {
		t.Fatalf("expected succeeded status, got:\n%s", out)
	}
	if !strings.Contains(out, "harness: os-harness-entrypoints") {
		t.Fatalf("expected harness=os-harness-entrypoints, got:\n%s", out)
	}
	if !strings.Contains(out, "run_attempt_id: ratt_") {
		t.Fatalf("expected run_attempt_id, got:\n%s", out)
	}
	// RunAttempt JSON was persisted to <home>/run_attempts/<id>.json.
	runAttemptFiles, _ := filepath.Glob(filepath.Join(homePath, "run_attempts", "ratt_*.json"))
	if len(runAttemptFiles) != 1 {
		t.Fatalf("expected exactly 1 RunAttempt file, got %d", len(runAttemptFiles))
	}
	// Re-load the persisted RunAttempt to confirm the S2 fields landed
	// (workspace, command, status, transcript_ref).
	store := runattempt.Open(homePath)
	id := strings.TrimSuffix(filepath.Base(runAttemptFiles[0]), ".json")
	ra, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if ra.Status != runattempt.StatusSucceeded {
		t.Fatalf("persisted status = %q, want succeeded", ra.Status)
	}
	if ra.Workspace != homePath {
		t.Fatalf("persisted workspace = %q, want %q", ra.Workspace, homePath)
	}
	if ra.Command != "echo" {
		t.Fatalf("persisted command = %q, want %q", ra.Command, "echo")
	}
	if ra.TranscriptRef == "" {
		t.Fatalf("persisted transcript_ref must be set after a clean run")
	}
}

// TestHarnessRunFailedExit: a subprocess that exits non-zero (`false`)
// produces a failed RunAttempt and the CLI exits non-zero. Spawned as
// a subprocess so we can capture the exit code; in-process tests
// can't recover from os.Exit.
//
// Pinned: the shell sees a non-zero exit when the harness run failed,
// which is the natural operator contract (CI / make / orchestration
// can react to a non-zero status).
func TestHarnessRunFailedExit(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	ws, _ := work.Open(homePath)
	w, _ := ws.Create(work.CreateInput{
		Owner:            "codex",
		SuccessPredicate: "harness run failed-exit path",
	}, cliTestNow)
	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	cmd := exec.Command("go", "run", "./cmd/compound", "harness", "run",
		"--harness", "os-harness-entrypoints", "--work", w.ID, "--", "false")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit, got 0. output:\n%s", out)
	}
	if !strings.Contains(string(out), "status: failed") {
		t.Fatalf("expected 'status: failed' in output, got:\n%s", out)
	}
}

// TestHarnessRunJSON: --json flag produces machine-readable output.
// Pinned so the operator can pipe into jq / other tooling.
func TestHarnessRunJSON(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	ws, _ := work.Open(homePath)
	w, _ := ws.Create(work.CreateInput{
		Owner:            "codex",
		SuccessPredicate: "harness run json output",
	}, cliTestNow)
	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	cmd := exec.Command("go", "run", "./cmd/compound", "harness", "run",
		"--harness", "os-harness-entrypoints", "--work", w.ID, "--json",
		"--", "echo", "json-smoke")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("json run: %v\n%s", err, out)
	}
	var got struct {
		RunAttemptID string `json:"run_attempt_id"`
		Status       string `json:"status"`
		Harness      string `json:"harness"`
		ExitCode     int    `json:"exit_code"`
		TimedOut     bool   `json:"timed_out"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, out)
	}
	if got.Status != runattempt.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", got.Status)
	}
	if got.Harness != "os-harness-entrypoints" {
		t.Fatalf("harness = %q, want os-harness-entrypoints", got.Harness)
	}
	if !strings.HasPrefix(got.RunAttemptID, "ratt_") {
		t.Fatalf("run_attempt_id = %q, want ratt_ prefix", got.RunAttemptID)
	}
}

var _ = time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
