package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/home"
	"compound/internal/runattempt"
	"compound/internal/work"
)

// TestWorkAdvanceExecute: --execute runs the harness adapter inline as
// part of the work advance. The full chain exercised:
//
//   1. operator claims the work
//   2. advance --to running --execute --harness <name> -- <cmd>
//      - CLI mints a RunAttempt via the adapter
//      - CLI calls work.Advance(To: "running", RunAttemptIDs: [ratt_])
//      - CLI calls adapter.Run() synchronously
//      - CLI calls work.Advance(To: "verifying"|"failed") based on
//        the adapter's terminal status
//   3. work ends in "verifying" (or "failed" on non-zero exit)
//
// Falsifier: removing the inline adapter.Run call turns this RED with
// "expected state=verifying, got: claimed" (the work never moved past
// claimed because the verifying-advance is gated on the adapter
// completing). Removing the second Advance call (verifying) turns
// the state check RED the same way.
func TestWorkAdvanceExecute(t *testing.T) {
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
		SuccessPredicate: "advance --execute integration test",
	}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		cmdWork([]string{
			"advance", w.ID,
			"--to", "running",
			"--execute",
			"--harness", "os-harness-entrypoints",
			"--", "echo", "advance-execute-smoke",
		})
	})

	// The output must show the RunAttempt was minted and run.
	if !strings.Contains(out, "run_attempt_id: ratt_") {
		t.Fatalf("expected run_attempt_id in output, got:\n%s", out)
	}
	if !strings.Contains(out, "status: succeeded") {
		t.Fatalf("expected 'status: succeeded' in output, got:\n%s", out)
	}

	// The work should have advanced to verifying (echo exits 0).
	final, err := ws.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != "verifying" {
		t.Fatalf("expected state=verifying, got: %s (advance --execute must move the work through running -> verifying)", final.State)
	}
	if len(final.RunAttemptIDs) != 1 {
		t.Fatalf("expected exactly 1 RunAttemptID on the work, got %d (list: %v)", len(final.RunAttemptIDs), final.RunAttemptIDs)
	}
	// The RunAttempt itself should be succeeded with the transcript
	// ref attached.
	store := runattempt.Open(homePath)
	ra, err := store.Get(final.RunAttemptIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if ra.Status != runattempt.StatusSucceeded {
		t.Fatalf("expected RunAttempt succeeded, got: %s", ra.Status)
	}
	if ra.TranscriptRef == "" {
		t.Fatalf("RunAttempt must have transcript_ref after --execute")
	}
}

// TestWorkAdvanceExecuteFailedExit: a subprocess that exits non-zero
// advances the work to "failed" state. Pinned: --execute surfaces
// subprocess failure as a work-lifecycle failure, not as a silently
// passed advance.
func TestWorkAdvanceExecuteFailedExit(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	ws, _ := work.Open(homePath)
	w, _ := ws.Create(work.CreateInput{
		Owner:            "codex",
		SuccessPredicate: "advance --execute failed-exit path",
	}, cliTestNow)
	if _, err := ws.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatal(err)
	}

	// Subprocess-driven: os.Exit kills the test process, so spawn the
	// binary instead. The CLI's exit code for a failed harness run is
	// non-zero (the operator contract); we don't assert on exit code
	// here, just on the persisted state.
	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	cmd := exec.Command("go", "run", "./cmd/compound", "work", "advance", w.ID,
		"--to", "running", "--execute", "--harness", "os-harness-entrypoints",
		"--", "false")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	_, err := cmd.CombinedOutput()
	// err != nil is the expected outcome (non-zero exit on failed run);
	// we don't assert on it because the subprocess contract is the
	// load-bearing test below, not the exit code itself.
	_ = err

	// Re-load the work to confirm it advanced to "failed".
	ws2, _ := work.Open(homePath)
	final, getErr := ws2.Get(w.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if final.State != "failed" {
		t.Fatalf("expected state=failed on non-zero exit, got: %s", final.State)
	}
	// The failed-path closes via CloseInput (failed is a terminal
	// state; Advance can't go there). The RunAttempt itself is
	// still persisted to <home>/run_attempts/ -- the S2 spine records
	// the failure; the audit chain is intact via the RunAttempt on
	// disk + the work's Receipt/Transition proof carrying the
	// transcript path. The audit check below verifies the RunAttempt
	// itself; the work's proof chain is covered by the receipt/transition
	// contract that runs in closeLocked.
	runAttemptFiles, _ := filepath.Glob(filepath.Join(homePath, "run_attempts", "ratt_*.json"))
	if len(runAttemptFiles) != 1 {
		t.Fatalf("expected exactly 1 RunAttempt file, got %d (close path should still have minted one)", len(runAttemptFiles))
	}
	store := runattempt.Open(homePath)
	id := strings.TrimSuffix(filepath.Base(runAttemptFiles[0]), ".json")
	ra, _ := store.Get(id)
	if ra.Status != runattempt.StatusFailed {
		t.Fatalf("expected RunAttempt failed, got: %s", ra.Status)
	}
}
