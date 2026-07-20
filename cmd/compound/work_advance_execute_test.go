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
//  1. operator claims the work
//  2. advance --to running --execute --harness <name> -- <cmd>
//     - CLI mints a RunAttempt via the adapter
//     - CLI calls work.Advance(To: "running", RunAttemptIDs: [ratt_])
//     - CLI calls adapter.Run() synchronously
//     - CLI calls work.Advance(To: "verifying"|"failed") based on
//     the adapter's terminal status
//  3. work ends in "verifying" (or "failed" on non-zero exit)
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
	// C1: Claim mints a RunAttempt, and advance --execute mints a second
	// (the one that actually ran). So the work carries exactly 2: the
	// claim-mint first, the executed-mint last.
	if len(final.RunAttemptIDs) != 2 {
		t.Fatalf("expected 2 RunAttemptIDs on the work (claim-mint + execute-mint), got %d (list: %v)", len(final.RunAttemptIDs), final.RunAttemptIDs)
	}
	// The EXECUTED RunAttempt (the last one — the claim-mint is first) is
	// the one that ran: succeeded with the transcript ref attached.
	store := runattempt.Open(homePath)
	ra, err := store.Get(final.RunAttemptIDs[len(final.RunAttemptIDs)-1])
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
	// The failed close must forward-link the (terminal) RunAttempt onto
	// the envelope via CloseInput.RunAttemptIDs -- the audit chain reaches
	// the failed run from the work, not only from run_attempts/<id>.json.
	// C1: Claim mints a RunAttempt too, so the work carries 2 (claim-mint
	// + the failed execute-mint forward-linked by the close).
	if len(final.RunAttemptIDs) != 2 {
		t.Fatalf("expected failed-close to leave 2 RunAttemptIDs on the work (claim-mint + failed execute-mint), got %d (%v)", len(final.RunAttemptIDs), final.RunAttemptIDs)
	}
	// The failed-path closes via CloseInput (failed is a terminal
	// state; Advance can't go there). The RunAttempt itself is
	// still persisted to <home>/run_attempts/ -- the S2 spine records
	// the failure; the audit chain is intact via the RunAttempt on
	// disk + the work's Receipt/Transition proof carrying the
	// transcript path. The audit check below verifies the RunAttempt
	// itself; the work's proof chain is covered by the receipt/transition
	// contract that runs in closeLocked.
	// Two RunAttempt files: the claim-mint (C1) and the failed execute-mint.
	runAttemptFiles, _ := filepath.Glob(filepath.Join(homePath, "run_attempts", "ratt_*.json"))
	if len(runAttemptFiles) != 2 {
		t.Fatalf("expected 2 RunAttempt files (claim-mint + failed execute-mint), got %d", len(runAttemptFiles))
	}
	// The FAILED run-attempt is the execute-mint; files sort by timestamped
	// id, so the claim-mint (earlier cliTestNow) is first and the later
	// execute-mint is last.
	store := runattempt.Open(homePath)
	id := strings.TrimSuffix(filepath.Base(runAttemptFiles[len(runAttemptFiles)-1]), ".json")
	ra, _ := store.Get(id)
	if ra.Status != runattempt.StatusFailed {
		t.Fatalf("expected the executed RunAttempt to be failed, got: %s", ra.Status)
	}
}

// TestWorkAdvanceExecuteUnknownHarnessNoStateChange: an unknown --harness
// name fails CLOSED -- the work never advances to "running". Pinned: the
// old flow advanced to running first and looked the adapter up second, so
// a typo in --harness left the work stuck in "running" with no RunAttempt
// and no recovery. Spawned as a subprocess because the CLI os.Exit on the
// unknown-harness path.
func TestWorkAdvanceExecuteUnknownHarnessNoStateChange(t *testing.T) {
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	ws, _ := work.Open(homePath)
	w, _ := ws.Create(work.CreateInput{
		Owner:            "codex",
		SuccessPredicate: "unknown-harness must not move the work",
	}, cliTestNow)
	if _, err := ws.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatal(err)
	}

	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	cmd := exec.Command("go", "run", "./cmd/compound", "work", "advance", w.ID,
		"--to", "running", "--execute", "--harness", "no-such-harness",
		"--", "echo", "hi")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit on unknown harness, got 0. output:\n%s", out)
	}
	if !strings.Contains(string(out), "unknown harness") {
		t.Fatalf("expected 'unknown harness' in stderr, got:\n%s", out)
	}

	// The work must NOT have advanced -- still "claimed". C1: Claim itself
	// mints a RunAttempt, so exactly ONE run-attempt file (the claim's)
	// exists; the unknown-harness advance must mint none ADDITIONAL (teeth:
	// if the advance minted, there would be 2).
	ws2, _ := work.Open(homePath)
	final, getErr := ws2.Get(w.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if final.State != "claimed" {
		t.Fatalf("unknown harness must fail before any state change; state = %q, want claimed (the old flow left it stuck in running)", final.State)
	}
	runAttemptFiles, _ := filepath.Glob(filepath.Join(homePath, "run_attempts", "ratt_*.json"))
	if len(runAttemptFiles) != 1 {
		t.Fatalf("unknown harness must mint no RunAttempt beyond the claim's; expected exactly 1 file (the claim-mint), got %d", len(runAttemptFiles))
	}
}
