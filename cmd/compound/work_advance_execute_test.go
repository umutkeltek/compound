package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"compound/internal/actuator"
	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/home"
	"compound/internal/policy"
	"compound/internal/routeevidence"
	"compound/internal/runattempt"
	"compound/internal/work"
)

type claimedExecutionFixture struct {
	homePath string
	store    *work.Store
	workID   string
}

func initializedExecutionHome(t *testing.T) string {
	t.Helper()
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	return homePath
}

func newClaimedExecutionFixture(t *testing.T, owner, successPredicate string) claimedExecutionFixture {
	t.Helper()
	homePath := initializedExecutionHome(t)
	t.Setenv("COMPOUND_HOME", homePath)
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(work.CreateInput{Owner: owner, SuccessPredicate: successPredicate}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(w.ID, owner, cliTestNow); err != nil {
		t.Fatal(err)
	}
	return claimedExecutionFixture{homePath: homePath, store: store, workID: w.ID}
}

func (f claimedExecutionFixture) executionAttempt(t *testing.T) runattempt.RunAttempt {
	t.Helper()
	final, err := f.store.Get(f.workID)
	if err != nil {
		t.Fatal(err)
	}
	if len(final.RunAttemptIDs) < 2 {
		t.Fatalf("expected claim-mint + execute-mint, got %v", final.RunAttemptIDs)
	}
	attempt, err := runattempt.Open(f.homePath).Get(final.RunAttemptIDs[len(final.RunAttemptIDs)-1])
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}

func reportRevokedExecutionAttempt(store *work.Store, workID, executionAttemptID string) {
	for _, warning := range actuator.ReportRevokedExecutionAttempt(store, workID, executionAttemptID) {
		fmt.Fprintln(os.Stderr, warning)
	}
}

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

// Two executors may read the same claimed envelope, but claimed -> running is
// a one-shot execution reservation. The first real process stays running while
// the second arrives, falsifying the old same-state running -> running path
// that minted and launched a second adapter against the same workspace.
func TestWorkAdvanceExecuteConcurrentProcessesLaunchOnce(t *testing.T) {
	fixture := newClaimedExecutionFixture(t, "executor", "one governed subprocess launch")

	fixtureDir := t.TempDir()
	launchLog := filepath.Join(fixtureDir, "launches.log")
	releasePath := filepath.Join(fixtureDir, "release")
	scriptPath := filepath.Join(fixtureDir, "hold-launch.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$$\" >> \"$COMPOUND_LAUNCH_LOG\"\nwhile [ ! -f \"$COMPOUND_LAUNCH_RELEASE\" ]; do sleep 0.01; done\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	release := func() { _ = os.WriteFile(releasePath, []byte("release\n"), 0o644) }
	t.Cleanup(release)

	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	newExecution := func() *exec.Cmd {
		cmd := exec.Command(compoundTestBinary(t), "work", "advance", fixture.workID,
			"--to", "running", "--execute", "--harness", "os-harness-entrypoints",
			"--workspace", fixtureDir, "--by", "executor", "--", scriptPath)
		cmd.Dir = repoRoot
		cmd.Env = append(os.Environ(),
			"COMPOUND_HOME="+fixture.homePath,
			"COMPOUND_LAUNCH_LOG="+launchLog,
			"COMPOUND_LAUNCH_RELEASE="+releasePath,
		)
		return cmd
	}
	type processResult struct {
		output []byte
		err    error
	}
	start := func(cmd *exec.Cmd) <-chan processResult {
		done := make(chan processResult, 1)
		go func() {
			out, err := cmd.CombinedOutput()
			done <- processResult{output: out, err: err}
		}()
		return done
	}

	firstDone := start(newExecution())
	deadline := time.Now().Add(10 * time.Second)
	for {
		body, _ := os.ReadFile(launchLog)
		if len(strings.Fields(string(body))) >= 1 {
			break
		}
		if time.Now().After(deadline) {
			release()
			t.Fatal("first execution did not reach the held subprocess")
		}
		time.Sleep(10 * time.Millisecond)
	}
	secondDone := start(newExecution())

	var secondEarly *processResult
	probeDeadline := time.Now().Add(2 * time.Second)
	for secondEarly == nil && time.Now().Before(probeDeadline) {
		select {
		case result := <-secondDone:
			secondEarly = &result
		default:
			body, _ := os.ReadFile(launchLog)
			if len(strings.Fields(string(body))) >= 2 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		body, _ := os.ReadFile(launchLog)
		if len(strings.Fields(string(body))) >= 2 {
			break
		}
	}
	release()
	first := <-firstDone
	second := processResult{}
	if secondEarly != nil {
		second = *secondEarly
	} else {
		second = <-secondDone
	}

	body, err := os.ReadFile(launchLog)
	if err != nil {
		t.Fatal(err)
	}
	if launches := len(strings.Fields(string(body))); launches != 1 {
		t.Fatalf("governed subprocess launches = %d, want exactly one; log=%q first=%v/%s second=%v/%s", launches, body, first.err, first.output, second.err, second.output)
	}
	successes := 0
	if first.err == nil {
		successes++
	}
	if second.err == nil {
		successes++
	}
	if successes != 1 {
		t.Fatalf("concurrent execute successes = %d, want one; first=%v/%s second=%v/%s", successes, first.err, first.output, second.err, second.output)
	}
	runAttemptFiles, _ := filepath.Glob(filepath.Join(fixture.homePath, "run_attempts", "ratt_*.json"))
	if len(runAttemptFiles) != 2 {
		t.Fatalf("concurrent execute attempts = %d, want claim reservation + one execution; files=%v", len(runAttemptFiles), runAttemptFiles)
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
	cmd := exec.Command(compoundTestBinary(t), "work", "advance", w.ID,
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

// A claim reservation can be swept after the claim but before the governed
// adapter launches. That sweep is evidence that the reservation itself never
// executed; a later real harness attempt owns the task outcome. Failure
// finalization must therefore bind the real attempt and close the work instead
// of treating the abandoned reservation as a contradictory failed outcome.
func TestWorkAdvanceExecuteFailureAfterSweptClaimReservationClosesFailed(t *testing.T) {
	fixture := newClaimedExecutionFixture(t, "executor", "failed execution survives a swept claim reservation")
	claimed, err := fixture.store.Get(fixture.workID)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed.RunAttemptIDs) != 1 {
		t.Fatalf("claim run attempts = %v, want one reservation", claimed.RunAttemptIDs)
	}
	claimAttemptID := claimed.RunAttemptIDs[0]
	attempts := runattempt.Open(fixture.homePath)
	sweep, err := attempts.SweepStale(30*time.Minute, cliTestNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if sweep.AutoClosed != 1 || len(sweep.AutoClosedIDs) != 1 || sweep.AutoClosedIDs[0] != claimAttemptID {
		t.Fatalf("sweep report = %+v, want exact claim reservation auto-closed", sweep)
	}
	sweptBefore, err := attempts.Get(claimAttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if sweptBefore.Status != runattempt.StatusAbandoned || sweptBefore.SweepClassification != runattempt.SweepClassificationSideEffectFree {
		t.Fatalf("swept claim reservation = %+v, want abandoned/side_effect_free", sweptBefore)
	}

	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	command := func() *exec.Cmd {
		cmd := exec.Command(compoundTestBinary(t), "work", "advance", fixture.workID,
			"--to", "running", "--execute", "--harness", "os-harness-entrypoints",
			"--workspace", repoRoot, "--by", "executor", "--", "false")
		cmd.Dir = repoRoot
		cmd.Env = append(os.Environ(), "COMPOUND_HOME="+fixture.homePath)
		return cmd
	}
	out, runErr := command().CombinedOutput()
	if runErr == nil {
		t.Fatalf("failing governed command exited zero:\n%s", out)
	}
	if !strings.Contains(string(out), fixture.workID+" -> failed (audit_only)") {
		t.Fatalf("governed failure did not truthfully report failed/audit_only:\n%s", out)
	}

	final, err := fixture.store.Get(fixture.workID)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != "failed" {
		t.Fatalf("work state = %q, want failed; output:\n%s", final.State, out)
	}
	if len(final.RunAttemptIDs) != 2 || final.RunAttemptIDs[0] != claimAttemptID {
		t.Fatalf("work run attempts = %v, want swept reservation followed by actual execution", final.RunAttemptIDs)
	}
	executionAttemptID := final.RunAttemptIDs[1]
	execution, err := attempts.Get(executionAttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != runattempt.StatusFailed || execution.Origin != runattempt.OriginHarnessExecution ||
		execution.Launch == nil || execution.Launch.CommittedAt == nil {
		t.Fatalf("actual execution = %+v, want committed failed harness execution", execution)
	}

	sweptAfter, err := attempts.Get(claimAttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sweptAfter, sweptBefore) {
		t.Fatalf("failed close rewrote sweep evidence:\nbefore=%+v\nafter=%+v", sweptBefore, sweptAfter)
	}
	if final.IDs.TransitionID == "" || !testEventExists(t, fixture.homePath, final.IDs.TransitionID) {
		t.Fatalf("failed close transition %q is not durable", final.IDs.TransitionID)
	}
	var transition envelope.Transition
	readTestEventJSON(t, fixture.homePath, final.IDs.TransitionID, &transition)
	if transition.IDs.WorkID != fixture.workID {
		t.Fatalf("transition work_id = %q, want %q", transition.IDs.WorkID, fixture.workID)
	}
	receiptID := ""
	for _, ref := range transition.AfterRefs {
		if strings.HasPrefix(ref, "rec_") {
			receiptID = ref
			break
		}
	}
	if receiptID == "" || !testEventExists(t, fixture.homePath, receiptID) {
		t.Fatalf("transition after_refs = %v, want durable terminal receipt", transition.AfterRefs)
	}
	var receipt envelope.Receipt
	readTestEventJSON(t, fixture.homePath, receiptID, &receipt)
	if receipt.IDs.WorkID != fixture.workID || receipt.Outcome != "failed" || receipt.MoveType != "audit_only" {
		t.Fatalf("terminal receipt = %+v, want failed/audit_only for %s", receipt, fixture.workID)
	}
	proofReport, err := fixture.store.ProofChainDoctor(time.Now(), work.ProofChainDoctorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if proofReport.Status != "ok" || len(proofReport.Items) != 1 || !proofReport.Items[0].ProofComplete || len(proofReport.Items[0].Problems) != 0 {
		t.Fatalf("terminal proof chain = %+v, want one complete item", proofReport)
	}

	// A retry cannot launch another harness or rewrite the terminal proof.
	// The exact terminal envelope and two-attempt chain remain authoritative.
	retryOut, retryErr := command().CombinedOutput()
	if retryErr == nil {
		t.Fatalf("retry against failed work exited zero:\n%s", retryOut)
	}
	afterRetry, err := fixture.store.Get(fixture.workID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterRetry, final) {
		t.Fatalf("retry mutated terminal work:\nbefore=%+v\nafter=%+v", final, afterRetry)
	}
	runAttemptFiles, _ := filepath.Glob(filepath.Join(fixture.homePath, "run_attempts", "ratt_*.json"))
	if len(runAttemptFiles) != 2 {
		t.Fatalf("retry minted another RunAttempt; files=%v", runAttemptFiles)
	}
}

// A start error is still the real harness attempt's outcome. It carries no
// committed launch evidence, but it must replace the claim placeholder for
// failed-close consistency and leave the same complete terminal proof chain.
func TestWorkAdvanceExecuteStartErrorAfterSweptClaimReservationClosesFailed(t *testing.T) {
	fixture := newClaimedExecutionFixture(t, "executor", "start error survives a swept claim reservation")
	claimed, err := fixture.store.Get(fixture.workID)
	if err != nil {
		t.Fatal(err)
	}
	claimAttemptID := claimed.RunAttemptIDs[0]
	attempts := runattempt.Open(fixture.homePath)
	if _, err := attempts.SweepStale(30*time.Minute, cliTestNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	sweptBefore, err := attempts.Get(claimAttemptID)
	if err != nil {
		t.Fatal(err)
	}

	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	missingCommand := filepath.Join(t.TempDir(), "missing-governed-command")
	cmd := exec.Command(compoundTestBinary(t), "work", "advance", fixture.workID,
		"--to", "running", "--execute", "--harness", "os-harness-entrypoints",
		"--workspace", repoRoot, "--by", "executor", "--", missingCommand)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+fixture.homePath)
	out, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("start-error governed command exited zero:\n%s", out)
	}
	if !strings.Contains(string(out), fixture.workID+" -> failed (audit_only)") {
		t.Fatalf("start error did not truthfully report failed/audit_only:\n%s", out)
	}

	final, err := fixture.store.Get(fixture.workID)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != "failed" || len(final.RunAttemptIDs) != 2 || final.RunAttemptIDs[0] != claimAttemptID {
		t.Fatalf("terminal work = %+v, want failed with swept reservation + start-error attempt", final)
	}
	execution, err := attempts.Get(final.RunAttemptIDs[1])
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != runattempt.StatusFailed || execution.Origin != runattempt.OriginHarnessExecution ||
		execution.Launch == nil || execution.Launch.StartErrorAt == nil || execution.Launch.CommittedAt != nil {
		t.Fatalf("actual execution = %+v, want failed harness start_error without committed launch", execution)
	}
	sweptAfter, err := attempts.Get(claimAttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sweptAfter, sweptBefore) {
		t.Fatalf("start-error close rewrote sweep evidence:\nbefore=%+v\nafter=%+v", sweptBefore, sweptAfter)
	}
	proofReport, err := fixture.store.ProofChainDoctor(time.Now(), work.ProofChainDoctorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if proofReport.Status != "ok" || len(proofReport.Items) != 1 || !proofReport.Items[0].ProofComplete || len(proofReport.Items[0].Problems) != 0 {
		t.Fatalf("start-error proof chain = %+v, want one complete item", proofReport)
	}
}

// TestWorkAdvanceExecuteUnknownHarnessNoStateChange: an unknown --harness
// name fails CLOSED -- the work never advances to "running". Pinned: the
// old flow advanced to running first and looked the adapter up second, so
// a typo in --harness left the work stuck in "running" with no RunAttempt
// and no recovery. Spawned as a subprocess because the CLI os.Exit on the
// unknown-harness path.
func TestWorkAdvanceExecuteUnknownHarnessNoStateChange(t *testing.T) {
	t.Parallel()

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
	cmd := exec.Command(compoundTestBinary(t), "work", "advance", w.ID,
		"--to", "running", "--execute", "--harness", "no-such-harness",
		"--", "echo", "hi")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit on unknown harness, got 0. output:\n%s", out)
	}
	// The refusal must name the gate, the rejected harness, and what it protected --
	// not just report a symptom. A bare "unknown harness: x" leaves the reader to
	// guess whether the envelope advanced and which names are valid.
	for _, want := range []string{
		"gate adapter_resolution",
		"no-such-harness",
		"registered adapters:",
		"tried in order:",
		"prevents:",
	} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("expected %q in the adapter-resolution refusal, got:\n%s", want, out)
		}
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

// TestWorkAdvanceExecuteMissingLaneTupleNoStateChange proves tuple-backed
// adapters preflight before the governed running advance. Enrollment alone is
// not availability: if Compound has no tuple for the requested lane, the work
// stays claimed and no execution RunAttempt is minted.
func TestWorkAdvanceExecuteMissingLaneTupleNoStateChange(t *testing.T) {
	t.Parallel()

	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	ws, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w, err := ws.Create(work.CreateInput{
		Owner:            "codex",
		SuccessPredicate: "missing lane tuple leaves work claimed",
	}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatal(err)
	}

	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	cmd := exec.Command(compoundTestBinary(t), "work", "advance", w.ID,
		"--to", "running", "--execute", "--harness", "claude_minimax",
		"--workspace", repoRoot, "--prompt", "execute fixture")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	out, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("expected missing tuple to fail, output:\n%s", out)
	}
	if !strings.Contains(string(out), "model-router authority") && !strings.Contains(string(out), "no Compound lane tuple") {
		t.Fatalf("missing tuple error is not actionable:\n%s", out)
	}

	final, err := ws.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != "claimed" {
		t.Fatalf("missing tuple changed work state to %q, want claimed", final.State)
	}
	runAttemptFiles, _ := filepath.Glob(filepath.Join(homePath, "run_attempts", "ratt_*.json"))
	if len(runAttemptFiles) != 1 {
		t.Fatalf("missing tuple minted an execution RunAttempt; files=%v", runAttemptFiles)
	}
}

// TestWorkAdvanceExecuteWalledDeclaredCapacityNoStateChange proves declared
// quota is enforced before governed execution begins. A fresh, exhausted
// CodexBar row leaves work claimed and produces no attempt beyond the claim
// reservation even though the tuple and executable both resolve.
func TestWorkAdvanceExecuteWalledDeclaredCapacityNoStateChange(t *testing.T) {
	t.Parallel()

	homePath := t.TempDir()
	userHome := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	source := filepath.Join(t.TempDir(), "minimax.yaml")
	manifest := `version: 1
purpose: fixture
capability_classes: [build-implement]
models:
  fixture-minimax:
    lane: fixture minimax
    harness: claude_minimax
    provider_route: minimax
    tier: cheap
    packet_ceiling: multi-file
    aliases: [sh]
    capacity_source:
      kind: codexbar
      freshness_minutes: 10
      ref: codexbar://provider/minimax
    capabilities: {build-implement: {level: capable, evidence: fixture}}
routing_defaults:
  build-implement: [fixture-minimax]
`
	if err := os.WriteFile(source, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := routeevidence.ImportModelCapabilitiesYAML(homePath, source, now); err != nil {
		t.Fatal(err)
	}
	usagePath := filepath.Join(userHome, "Library", "Application Support", "CodexBar", "usage-history.jsonl")
	if err := os.MkdirAll(filepath.Dir(usagePath), 0o755); err != nil {
		t.Fatal(err)
	}
	row := fmt.Sprintf("{\"provider\":\"minimax\",\"windowKind\":\"weekly\",\"windowMinutes\":10080,\"usedPercent\":100,\"resetsAt\":%q,\"sampledAt\":%q}\n",
		now.Add(24*time.Hour).Format(time.RFC3339), now.Format(time.RFC3339))
	if err := os.WriteFile(usagePath, []byte(row), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w, err := ws.Create(work.CreateInput{Owner: "codex", SuccessPredicate: "exhausted declared capacity leaves work claimed"}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatal(err)
	}

	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	cmd := exec.Command(compoundTestBinary(t), "work", "advance", w.ID,
		"--to", "running", "--execute", "--harness", "claude_minimax",
		"--workspace", repoRoot, "--prompt", "execute fixture")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "HOME="+userHome, "COMPOUND_HOME="+homePath)
	out, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("expected walled declared capacity to fail, output:\n%s", out)
	}
	if !strings.Contains(string(out), "capacity walled") {
		t.Fatalf("walled capacity error is not actionable:\n%s", out)
	}
	final, err := ws.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != "claimed" {
		t.Fatalf("walled capacity changed work state to %q, want claimed", final.State)
	}
	runAttemptFiles, _ := filepath.Glob(filepath.Join(homePath, "run_attempts", "ratt_*.json"))
	if len(runAttemptFiles) != 1 {
		t.Fatalf("walled capacity minted an execution RunAttempt; files=%v", runAttemptFiles)
	}
}

// TestWorkAdvanceExecuteAcceptsWorkspaceAndPermissionMode: the two flags
// the execute branch reads (resolveExecuteWorkspace, RunInput.PermissionMode)
// must be accepted by the advance flag validator. Falsifier: dropping either
// from advanceValueFlags turns this RED with `unknown flag --workspace`
// (exit 2) before the execute branch ever runs — the live defect this pins
// was found by an end-to-end rail verification on 2026-07-21.
func TestWorkAdvanceExecuteAcceptsWorkspaceAndPermissionMode(t *testing.T) {
	t.Parallel()

	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	ws, _ := work.Open(homePath)
	w, _ := ws.Create(work.CreateInput{
		Owner:            "codex",
		SuccessPredicate: "--workspace and --permission-mode reach the execute branch",
	}, cliTestNow)
	if _, err := ws.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()

	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	cmd := exec.Command(compoundTestBinary(t), "work", "advance", w.ID,
		"--to", "running", "--execute", "--harness", "os-harness-entrypoints",
		"--workspace", workspace, "--permission-mode", "plan",
		"--", "echo", "workspace-flag-smoke")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("advance --execute with --workspace/--permission-mode must succeed, got err=%v output:\n%s", err, out)
	}
	if !strings.Contains(string(out), "status: succeeded") {
		t.Fatalf("expected 'status: succeeded' in output, got:\n%s", out)
	}

	// The flag must REACH RunInput, not merely be tolerated: the executed
	// RunAttempt (last id; the claim-mint is first) records the workspace.
	final, err := ws.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	store := runattempt.Open(homePath)
	ra, err := store.Get(final.RunAttemptIDs[len(final.RunAttemptIDs)-1])
	if err != nil {
		t.Fatal(err)
	}
	if ra.Workspace != workspace {
		t.Fatalf("RunAttempt.Workspace = %q, want the --workspace value %q", ra.Workspace, workspace)
	}
}

type governedClaudeExecutionFixture struct {
	claimedExecutionFixture
	argvLog   string
	harness   string
	workspace string
}

func newGovernedClaudeExecutionFixture(t *testing.T, successPredicate string) governedClaudeExecutionFixture {
	t.Helper()
	return newGovernedClaudeLaneExecutionFixture(t, successPredicate, "claude", "claude")
}

func newGovernedClaudeLaneExecutionFixture(t *testing.T, successPredicate, harnessName, executableName string) governedClaudeExecutionFixture {
	t.Helper()
	fixture := governedClaudeExecutionFixture{
		claimedExecutionFixture: newClaimedExecutionFixture(t, "codex", successPredicate),
		argvLog:                 filepath.Join(t.TempDir(), "claude-argv.log"),
		harness:                 harnessName,
		workspace:               t.TempDir(),
	}
	binDir := t.TempDir()
	fakeClaude := filepath.Join(binDir, executableName)
	if err := os.WriteFile(fakeClaude, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$COMPOUND_TEST_CLAUDE_ARGV\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMPOUND_TEST_CLAUDE_ARGV", fixture.argvLog)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return fixture
}

func (f governedClaudeExecutionFixture) advanceArgs(permissionMode, prompt string) []string {
	args := []string{
		"advance", f.workID,
		"--to", "running",
		"--execute",
		"--harness", f.harness,
		"--workspace", f.workspace,
		"--by", "codex",
	}
	if permissionMode != "" {
		args = append(args, "--permission-mode", permissionMode)
	}
	return append(args, "--prompt", prompt)
}

func (f governedClaudeExecutionFixture) run(t *testing.T, permissionMode, prompt string) string {
	t.Helper()
	return captureStdout(t, func() { cmdWork(f.advanceArgs(permissionMode, prompt)) })
}

func (f governedClaudeExecutionFixture) command(t *testing.T, permissionMode, prompt string) *exec.Cmd {
	t.Helper()
	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	cmd := exec.Command(compoundTestBinary(t), append([]string{"work"}, f.advanceArgs(permissionMode, prompt)...)...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+f.homePath)
	return cmd
}

func (f governedClaudeExecutionFixture) argv(t *testing.T) string {
	t.Helper()
	argv, err := os.ReadFile(f.argvLog)
	if err != nil {
		t.Fatal(err)
	}
	return string(argv)
}

func stubDelegateHarnessReadiness(t *testing.T, readiness work.DelegateHarnessReadinessFunc) {
	t.Helper()
	original := work.DelegateHarnessReadiness
	work.DelegateHarnessReadiness = readiness
	t.Cleanup(func() { work.DelegateHarnessReadiness = original })
}

// TestWorkAdvanceExecuteDerivesClaudeBypassFromGovernedReadiness proves the
// public execution rail turns an omitted Claude permission mode into a
// prompt-free bypass only after the claimed work's prospective running
// decision and harness authorities are ready. The fake Claude executable
// records the real argv assembled by the adapter, so this test exercises the
// same `compound work advance --execute` seam callers use rather than a
// permission helper in isolation.
//
// RED (2026-08-04): the execute rail forwarded an empty PermissionMode and the
// Claude adapter converted it to `--permission-mode plan`; the readiness guard
// was never consulted. That kept governed unattended work read-only even when
// its envelope and mutation-blocking authorities were current.
func TestWorkAdvanceExecuteDerivesClaudeBypassFromGovernedReadiness(t *testing.T) {
	fixture := newGovernedClaudeExecutionFixture(t, "governed Claude execution derives prompt-free bypass")
	readinessCalls := 0
	stubDelegateHarnessReadiness(t, func(work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
		readinessCalls++
		return work.DelegateHarnessReadinessResult{
			Ready:    true,
			Evidence: governedClaudeReadyEvidenceFixture(),
		}, nil
	})
	cliOut := fixture.run(t, "", "apply the claimed change")

	argv := fixture.argv(t)
	if !strings.Contains(argv, "--dangerously-skip-permissions") {
		t.Fatalf("governed ready Claude argv = %q, want --dangerously-skip-permissions", argv)
	}
	if strings.Contains(argv, "--permission-mode plan") || strings.Contains(argv, "--permission-mode acceptEdits") {
		t.Fatalf("governed ready Claude argv retained an interactive/read-only posture: %q", argv)
	}
	if readinessCalls != 2 {
		t.Fatalf("governed Claude readiness calls = %d, want pre-advance selection + immediate pre-start revalidation", readinessCalls)
	}
	policyMarker := "policy="
	policyOffset := strings.Index(cliOut, policyMarker)
	if policyOffset < 0 {
		t.Fatalf("running advance output omitted policy decision:\n%s", cliOut)
	}
	policyFields := strings.Fields(cliOut[policyOffset+len(policyMarker):])
	if len(policyFields) == 0 || !strings.HasPrefix(policyFields[0], "pol_") {
		t.Fatalf("running advance output carried invalid policy decision:\n%s", cliOut)
	}
	runningPolicyID := policyFields[0]
	if !testEventExists(t, fixture.homePath, runningPolicyID) {
		t.Fatalf("running PolicyDecision %s was not durably recorded", runningPolicyID)
	}

	attempt := fixture.executionAttempt(t)
	if attempt.ExecutionPosture == nil {
		t.Fatal("execute-mint RunAttempt omitted execution_posture")
	}
	if got := attempt.ExecutionPosture.PermissionMode; got != "bypass" {
		t.Fatalf("recorded permission_mode = %q, want bypass", got)
	}
	if got := attempt.ExecutionPosture.PolicyDecisionID; got != runningPolicyID {
		t.Fatalf("recorded policy_decision_id = %q, want actual running decision %q", got, runningPolicyID)
	}
	recordedAuthorities := map[string]string{}
	for _, item := range attempt.ExecutionPosture.AuthorityEvidence {
		recordedAuthorities[item.Kind] = item.Ref + "@" + item.Revision
	}
	for kind, want := range map[string]string{
		"permission_authority":       "compound://config/harness-permissions/claude_code@sha256:permission-authority",
		"permission_posture":         "harness-posture://claude_code@sha256:permission-posture",
		"blocking_hook_plan":         "hook://claude_code/PreToolUse@sha256:hook-plan",
		"blocking_hook_installation": "harness-installation://live@sha256:hook-installation",
	} {
		if got := recordedAuthorities[kind]; got != want {
			t.Fatalf("recorded authority %s = %q, want %q (all=%v)", kind, got, want, recordedAuthorities)
		}
	}
}

// A tuple-backed lane must attest the identity resolved by adapter preflight,
// not the generic Claude alias the direct adapter uses. This pins the Qwen
// harness, model, provider route, executable, and prompt on both readiness
// checks and on the persisted execution attempt.
func TestWorkAdvanceExecuteTupleBackedQwenUsesResolvedReadinessIdentity(t *testing.T) {
	fixture := newGovernedClaudeLaneExecutionFixture(
		t,
		"tuple-backed Qwen uses its resolved readiness identity",
		"claude_qwen",
		"claude-qwen",
	)
	importQwenTupleFixture(t, fixture.homePath, "fixture-qwen", "qwen")

	readinessCalls := 0
	stubDelegateHarnessReadiness(t, func(req work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
		readinessCalls++
		if req.Harness != "claude_qwen" || req.RequestedModelID != "fixture-qwen" || req.RequestedProviderRoute != "qwen" {
			t.Fatalf("Qwen readiness identity = harness:%q model:%q provider:%q", req.Harness, req.RequestedModelID, req.RequestedProviderRoute)
		}
		if filepath.Base(req.Command) != "claude-qwen" || !slices.Equal(req.Args, []string{"execute through Qwen"}) {
			t.Fatalf("Qwen readiness activation = command:%q args:%q", req.Command, req.Args)
		}
		return work.DelegateHarnessReadinessResult{
			Ready:    true,
			Evidence: governedClaudeReadyEvidenceFor("claude_qwen"),
		}, nil
	})

	fixture.run(t, "", "execute through Qwen")
	if readinessCalls != 2 {
		t.Fatalf("Qwen readiness calls = %d, want pre-advance selection + immediate pre-start revalidation", readinessCalls)
	}
	if argv := fixture.argv(t); !strings.Contains(argv, "--dangerously-skip-permissions") {
		t.Fatalf("ready Qwen argv = %q, want --dangerously-skip-permissions", argv)
	}
	attempt := fixture.executionAttempt(t)
	if attempt.Harness != "claude_qwen" || attempt.RequestedModelID != "fixture-qwen" || attempt.RequestedProviderRoute != "qwen" {
		t.Fatalf("persisted Qwen identity = harness:%q model:%q provider:%q", attempt.Harness, attempt.RequestedModelID, attempt.RequestedProviderRoute)
	}
	if attempt.ExecutionPosture == nil || len(attempt.ExecutionPosture.AuthorityEvidence) != 4 {
		t.Fatalf("persisted Qwen execution posture = %+v, want four current authorities", attempt.ExecutionPosture)
	}
}

func TestWorkAdvanceExecuteTupleRotationBeforeStartLaunchesNoChildAndClosesFailed(t *testing.T) {
	fixture := newGovernedClaudeLaneExecutionFixture(
		t,
		"tuple rotation at the public rail fails before child start",
		"claude_qwen",
		"claude-qwen",
	)
	importQwenTupleFixture(t, fixture.homePath, "fixture-qwen-before", "qwen-before")
	readinessCalls := 0
	stubDelegateHarnessReadiness(t, func(work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
		readinessCalls++
		if readinessCalls == 1 {
			// Preflight already resolved the first tuple. Rotate the authority
			// before the running advance reaches the final guarded cmd.Start.
			importQwenTupleFixture(t, fixture.homePath, "fixture-qwen-after", "qwen-after")
		}
		return work.DelegateHarnessReadinessResult{
			Ready:    true,
			Evidence: governedClaudeReadyEvidenceFor("claude_qwen"),
		}, nil
	})

	type exitSignal struct{ code int }
	originalExit := workAdvanceExecuteExit
	workAdvanceExecuteExit = func(code int) { panic(exitSignal{code: code}) }
	t.Cleanup(func() { workAdvanceExecuteExit = originalExit })
	exitCode := 0
	func() {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			signal, ok := recovered.(exitSignal)
			if !ok {
				panic(recovered)
			}
			exitCode = signal.code
		}()
		cmdWork(fixture.advanceArgs("", "must not reach rotated Qwen child"))
	}()
	if exitCode != 1 {
		t.Fatalf("tuple rotation exit code = %d, want 1", exitCode)
	}
	if readinessCalls != 1 {
		t.Fatalf("tuple rotation readiness calls = %d, want only pre-admission call before lane refusal", readinessCalls)
	}
	if _, err := os.Stat(fixture.argvLog); !os.IsNotExist(err) {
		t.Fatalf("rotated tuple launched child; argv marker stat err=%v", err)
	}
	final, err := fixture.store.Get(fixture.workID)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != "failed" || len(final.RunAttemptIDs) < 2 {
		t.Fatalf("tuple rotation final work = state:%s attempts:%v, want failed with forward-linked execution attempt", final.State, final.RunAttemptIDs)
	}
	attempt := fixture.executionAttempt(t)
	if attempt.Status != runattempt.StatusFailed {
		t.Fatalf("tuple rotation attempt status = %q, want failed", attempt.Status)
	}
	if attempt.RequestedModelID != "fixture-qwen-before" || attempt.RequestedProviderRoute != "qwen-before" {
		t.Fatalf("failed attempt lost originally reserved tuple: model=%q provider=%q", attempt.RequestedModelID, attempt.RequestedProviderRoute)
	}
}

func TestWorkAdvanceExecuteTupleBackedQwenRejectsDifferentHarnessAuthority(t *testing.T) {
	fixture := newGovernedClaudeLaneExecutionFixture(
		t,
		"tuple-backed Qwen rejects authority for another Claude-family harness",
		"claude_qwen",
		"claude-qwen",
	)
	importQwenTupleFixture(t, fixture.homePath, "fixture-qwen", "qwen")
	readinessCalls := 0
	stubDelegateHarnessReadiness(t, func(work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
		readinessCalls++
		return work.DelegateHarnessReadinessResult{
			Ready:    true,
			Evidence: governedClaudeReadyEvidenceFor("claude_code"),
		}, nil
	})

	fixture.run(t, "", "remain read-only on mismatched authority")
	argv := fixture.argv(t)
	if !strings.Contains(argv, "--permission-mode plan") || strings.Contains(argv, "--dangerously-skip-permissions") {
		t.Fatalf("Qwen with claude_code authority argv = %q, want read-only plan", argv)
	}
	if readinessCalls != 1 {
		t.Fatalf("mismatched Qwen authority readiness calls = %d, want one pre-admission refusal", readinessCalls)
	}
	attempt := fixture.executionAttempt(t)
	if attempt.ExecutionPosture == nil || attempt.ExecutionPosture.PermissionMode != "plan" || len(attempt.ExecutionPosture.AuthorityEvidence) != 0 {
		t.Fatalf("mismatched Qwen authority posture = %+v, want plan without mutation authority", attempt.ExecutionPosture)
	}
}

// TestWorkAdvanceExecuteAutonomyHaltKeepsOmittedClaudeModeInPlan pins the
// lane-level kill switch. A class halt for build-implement must prevent
// automatic bypass even when permission and blocking-hook authorities are
// otherwise current. The safe fallback still executes the read-only plan
// posture, so the work receives analysis without a mutation-capable process.
func TestWorkAdvanceExecuteAutonomyHaltKeepsOmittedClaudeModeInPlan(t *testing.T) {
	fixture := newGovernedClaudeExecutionFixture(t, "build halt keeps Claude read-only")
	if err := policy.WriteAutonomyHalt(fixture.homePath, policy.AutonomyHalt{
		DisabledClasses: map[string]string{"build-implement": "incident review"},
	}); err != nil {
		t.Fatal(err)
	}
	readinessCalled := false
	stubDelegateHarnessReadiness(t, func(work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
		readinessCalled = true
		return work.DelegateHarnessReadinessResult{Ready: true, Evidence: governedClaudeReadyEvidenceFixture()}, nil
	})

	fixture.run(t, "", "analyze without mutating")
	argv := fixture.argv(t)
	if !strings.Contains(argv, "--permission-mode plan") || strings.Contains(argv, "--dangerously-skip-permissions") {
		t.Fatalf("halted build-implement Claude argv = %q, want read-only plan", argv)
	}
	if readinessCalled {
		t.Fatal("build-implement halt must stop automatic bypass before harness readiness is consulted")
	}
	ra := fixture.executionAttempt(t)
	if ra.ExecutionPosture == nil || ra.ExecutionPosture.PermissionMode != "plan" || len(ra.ExecutionPosture.AuthorityEvidence) != 0 {
		t.Fatalf("halted execution posture = %+v, want plan with no bypass authority evidence", ra.ExecutionPosture)
	}
}

func governedClaudeReadyEvidenceFixture() []work.DelegateHarnessReadinessEvidence {
	return governedClaudeReadyEvidenceFor("claude_code")
}

func governedClaudeReadyEvidenceFor(harnessName string) []work.DelegateHarnessReadinessEvidence {
	return []work.DelegateHarnessReadinessEvidence{
		{Kind: "permission_authority", Ref: "compound://config/harness-permissions/" + harnessName, Revision: "sha256:permission-authority"},
		{Kind: "permission_posture", Ref: "harness-posture://" + harnessName, Revision: "sha256:permission-posture"},
		{Kind: "blocking_hook_plan", Ref: "hook://" + harnessName + "/PreToolUse", Revision: "sha256:hook-plan"},
		{Kind: "blocking_hook_installation", Ref: "harness-installation://live", Revision: "sha256:hook-installation"},
	}
}

func TestGovernedClaudeEvidenceRefRequiresCanonicalMutationBlockingHook(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  string
		want bool
	}{
		{name: "unmatched PreToolUse", ref: "hook://claude_qwen/PreToolUse", want: true},
		{name: "matcher-qualified PreToolUse", ref: "hook://claude_qwen/PreToolUse?matcher=684888c0ebb1", want: true},
		{name: "different valid matcher digest", ref: "hook://claude_qwen/PreToolUse?matcher=0123456789ab", want: false},
		{name: "non-blocking event", ref: "hook://claude_qwen/SessionStart", want: false},
		{name: "event lookalike", ref: "hook://claude_qwen/PreToolUseExtra", want: false},
		{name: "empty matcher", ref: "hook://claude_qwen/PreToolUse?matcher=", want: false},
		{name: "short matcher", ref: "hook://claude_qwen/PreToolUse?matcher=0123456789a", want: false},
		{name: "non-hex matcher", ref: "hook://claude_qwen/PreToolUse?matcher=0123456789az", want: false},
		{name: "extra query", ref: "hook://claude_qwen/PreToolUse?matcher=0123456789ab&other=value", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := actuator.GovernedClaudeEvidenceRefMatchesHarness("claude_qwen", "blocking_hook_plan", tt.ref); got != tt.want {
				t.Fatalf("governedClaudeEvidenceRefMatchesHarness(%q) = %t, want %t", tt.ref, got, tt.want)
			}
		})
	}
}

func importQwenTupleFixture(t *testing.T, homePath, modelID, provider string) {
	t.Helper()
	manifestPath := filepath.Join(t.TempDir(), "model-capabilities.yaml")
	manifest := fmt.Sprintf(`version: 1
purpose: fixture
capability_classes: [build-implement]
models:
  %s:
    lane: fixture qwen lane
    harness: claude_qwen
    provider_route: %s
    tier: cheap
    packet_ceiling: multi-file
    aliases: [claude-qwen]
    capabilities: {build-implement: {level: capable, evidence: fixture}}
routing_defaults:
  build-implement: [%s]
`, modelID, provider, modelID)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := routeevidence.ImportModelCapabilitiesYAML(homePath, manifestPath, cliTestNow); err != nil {
		t.Fatal(err)
	}
}

func governedClaudeGuardReservation(t *testing.T, homePath string) (string, string, string) {
	t.Helper()
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(work.CreateInput{
		Owner:            "codex",
		SuccessPredicate: "guard starts only while this exact running reservation remains current",
	}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(created.ID, "codex", cliTestNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	running, err := store.Advance(created.ID, work.AdvanceInput{To: "running", Actor: "codex"}, cliTestNow.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if running.PolicyDecisionID == "" || len(running.ActionIntentIDs) == 0 {
		t.Fatalf("running reservation identity is incomplete: policy=%q actions=%v", running.PolicyDecisionID, running.ActionIntentIDs)
	}
	return running.ID, running.PolicyDecisionID, running.ActionIntentIDs[len(running.ActionIntentIDs)-1]
}

func TestGovernedClaudeStartGuardRejectsChangedAuthorityBeforeLaunch(t *testing.T) {
	homePath := initializedExecutionHome(t)
	workID, policyID, actionIntentID := governedClaudeGuardReservation(t, homePath)
	expected := governedClaudeReadyEvidenceFixture()
	changed := governedClaudeReadyEvidenceFixture()
	changed[1].Revision = "sha256:replaced-permission-posture"

	stubDelegateHarnessReadiness(t, func(work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
		return work.DelegateHarnessReadinessResult{Ready: true, Evidence: changed}, nil
	})

	started := false
	guard := actuator.NewGovernedClaudeStartGuard(homePath, "claude", work.DelegateHarnessReadinessRequest{
		HomePath: homePath,
		WorkID:   workID,
		Harness:  "claude_code",
		Command:  "claude",
		Now:      cliTestNow,
	}, expected, policyID, actionIntentID)
	err := guard(context.Background(), func() error {
		started = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "authority revisions changed before launch") {
		t.Fatalf("changed launch authority error = %v", err)
	}
	if started {
		t.Fatal("changed launch authority crossed the guarded start boundary")
	}
}

func TestGovernedClaudeStartGuardRejectsHaltBeforeLaunch(t *testing.T) {
	homePath := initializedExecutionHome(t)
	workID, policyID, actionIntentID := governedClaudeGuardReservation(t, homePath)
	if err := policy.WriteAutonomyHalt(homePath, policy.AutonomyHalt{
		DisabledClasses: map[string]string{"build-implement": "incident response"},
	}); err != nil {
		t.Fatal(err)
	}
	stubDelegateHarnessReadiness(t, func(work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
		t.Fatal("a launch-time halt must refuse before re-reading harness readiness")
		return work.DelegateHarnessReadinessResult{}, nil
	})

	started := false
	guard := actuator.NewGovernedClaudeStartGuard(homePath, "claude", work.DelegateHarnessReadinessRequest{
		HomePath: homePath,
		WorkID:   workID,
		Harness:  "claude_code",
		Command:  "claude",
		Now:      cliTestNow,
	}, governedClaudeReadyEvidenceFixture(), policyID, actionIntentID)
	err := guard(context.Background(), func() error {
		started = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "autonomy halted before launch") {
		t.Fatalf("launch-time halt error = %v", err)
	}
	if started {
		t.Fatal("launch-time halt crossed the guarded start boundary")
	}
}

func TestGovernedClaudeStartGuardRejectsChangedTupleBeforeLaunch(t *testing.T) {
	homePath := initializedExecutionHome(t)
	workID, policyID, actionIntentID := governedClaudeGuardReservation(t, homePath)
	binDir := t.TempDir()
	fakeQwen := filepath.Join(binDir, "claude-qwen")
	if err := os.WriteFile(fakeQwen, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	importQwenTupleFixture(t, homePath, "fixture-qwen-a", "qwen-a")
	registry := harness.NewAdapterRegistry(runattempt.Open(homePath))
	adapter, ok := registry.AdapterByName("claude_qwen")
	if !ok {
		t.Fatal("claude_qwen adapter is absent")
	}
	preflight, ok := adapter.(harness.PreflightAdapter)
	if !ok {
		t.Fatal("claude_qwen adapter has no preflight")
	}
	resolved, err := preflight.Preflight(context.Background(), harness.RunInput{
		WorkID: workID, Harness: "claude_qwen", Workspace: t.TempDir(),
		PermissionMode: "bypass", LaneID: "codex", Prompt: "execute through Qwen", Now: cliTestNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := work.DelegateHarnessReadinessRequest{
		HomePath:               homePath,
		WorkID:                 resolved.WorkID,
		Workspace:              resolved.Workspace,
		Harness:                resolved.Harness,
		LaneID:                 resolved.LaneID,
		RequestedModelID:       resolved.RequestedModelID,
		RequestedProviderRoute: resolved.RequestedProviderRoute,
		Command:                resolved.Command,
		Args:                   []string{resolved.Prompt},
		Now:                    cliTestNow,
	}

	importQwenTupleFixture(t, homePath, "fixture-qwen-b", "qwen-b")
	stubDelegateHarnessReadiness(t, func(work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
		return work.DelegateHarnessReadinessResult{Ready: true, Evidence: governedClaudeReadyEvidenceFor("claude_qwen")}, nil
	})

	started := false
	guard := actuator.NewGovernedClaudeStartGuard(homePath, "claude_qwen", request, governedClaudeReadyEvidenceFor("claude_qwen"), policyID, actionIntentID)
	err = guard(context.Background(), func() error {
		started = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "revalidate governed Claude lane before launch") {
		t.Fatalf("changed Qwen tuple error = %v", err)
	}
	if started {
		t.Fatal("changed Qwen tuple crossed the guarded start boundary")
	}
}

func TestGovernedClaudeLaneStartGuardRejectsChangedTupleInPlanMode(t *testing.T) {
	homePath := initializedExecutionHome(t)
	workID, policyID, actionIntentID := governedClaudeGuardReservation(t, homePath)
	binDir := t.TempDir()
	fakeQwen := filepath.Join(binDir, "claude-qwen")
	if err := os.WriteFile(fakeQwen, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	importQwenTupleFixture(t, homePath, "fixture-plan-qwen-a", "qwen-plan-a")
	registry := harness.NewAdapterRegistry(runattempt.Open(homePath))
	adapter, ok := registry.AdapterByName("claude_qwen")
	if !ok {
		t.Fatal("claude_qwen adapter is absent")
	}
	preflight, ok := adapter.(harness.PreflightAdapter)
	if !ok {
		t.Fatal("claude_qwen adapter has no preflight")
	}
	resolved, err := preflight.Preflight(context.Background(), harness.RunInput{
		WorkID: workID, Harness: "claude_qwen", Workspace: t.TempDir(),
		PermissionMode: "plan", LaneID: "codex", Prompt: "plan through Qwen", Now: cliTestNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := work.DelegateHarnessReadinessRequest{
		HomePath:               homePath,
		WorkID:                 resolved.WorkID,
		Workspace:              resolved.Workspace,
		Harness:                resolved.Harness,
		LaneID:                 resolved.LaneID,
		RequestedModelID:       resolved.RequestedModelID,
		RequestedProviderRoute: resolved.RequestedProviderRoute,
		Command:                resolved.Command,
		Args:                   []string{resolved.Prompt},
		Now:                    cliTestNow,
	}
	importQwenTupleFixture(t, homePath, "fixture-plan-qwen-b", "qwen-plan-b")

	started := false
	guard := actuator.NewGovernedClaudeLaneStartGuard(homePath, "claude_qwen", request, policyID, actionIntentID)
	err = guard(context.Background(), func() error {
		started = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "revalidate governed Claude lane before launch") {
		t.Fatalf("changed plan-mode Qwen tuple error = %v", err)
	}
	if started {
		t.Fatal("changed plan-mode Qwen tuple crossed the guarded start boundary")
	}
}

func TestGovernedClaudeStartGuardRejectsRevokedExecutionReservationBeforeLaunch(t *testing.T) {
	homePath := initializedExecutionHome(t)
	workID, policyID, actionIntentID := governedClaudeGuardReservation(t, homePath)
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(workID, work.AdvanceInput{To: "held", Actor: "codex", Note: "operator revoked launch"}, cliTestNow.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	stubDelegateHarnessReadiness(t, func(work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
		return work.DelegateHarnessReadinessResult{Ready: true, Evidence: governedClaudeReadyEvidenceFixture()}, nil
	})

	started := false
	guard := actuator.NewGovernedClaudeStartGuard(homePath, "claude", work.DelegateHarnessReadinessRequest{
		HomePath: homePath,
		WorkID:   workID,
		Harness:  "claude_code",
		Command:  "claude",
		Now:      cliTestNow,
	}, governedClaudeReadyEvidenceFixture(), policyID, actionIntentID)
	err = guard(context.Background(), func() error {
		started = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "execution reservation changed before launch") {
		t.Fatalf("revoked execution reservation error = %v", err)
	}
	if started {
		t.Fatal("revoked execution reservation crossed the guarded start boundary")
	}
}

func TestGovernedExecutionReservationGuardAppliesToEveryHarness(t *testing.T) {
	t.Parallel()

	homePath := initializedExecutionHome(t)
	workID, policyID, actionIntentID := governedClaudeGuardReservation(t, homePath)
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(workID, work.AdvanceInput{To: "held", Actor: "codex", Note: "revoke generic harness start"}, cliTestNow.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	started := false
	guard := actuator.NewGovernedExecutionReservationStartGuard(homePath, workID, policyID, actionIntentID)
	err = guard(context.Background(), func() error {
		started = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "execution reservation changed before launch") {
		t.Fatalf("generic reservation guard error = %v", err)
	}
	if started {
		t.Fatal("generic reservation guard started after revocation")
	}
}

func TestReserveGovernedHarnessLaunchRejectsRotatedClaimedGeneration(t *testing.T) {
	fixture := newClaimedExecutionFixture(t, "codex", "stale claimed preflight never launches")
	staleClaimed, err := fixture.store.Get(fixture.workID)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := fixture.store.Advance(fixture.workID, work.AdvanceInput{
		To: "claimed", Actor: "codex", Note: "newer claimed context generation",
	}, cliTestNow.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if rotated.PolicyDecisionID == staleClaimed.PolicyDecisionID || slices.Equal(rotated.ActionIntentIDs, staleClaimed.ActionIntentIDs) {
		t.Fatal("same-state claimed update did not rotate the policy/action generation")
	}

	adapter := harness.NewOSEntrypointsAdapter(runattempt.Open(fixture.homePath))
	_, err = actuator.ReserveGovernedHarnessLaunch(actuator.GovernedHarnessReservationInput{
		HomePath:    fixture.homePath,
		Store:       fixture.store,
		ClaimedWork: staleClaimed,
		Adapter:     adapter,
		RunInput: harness.RunInput{
			WorkID: fixture.workID, Harness: adapter.Name(), Workspace: t.TempDir(), Command: "/usr/bin/true", Now: cliTestNow,
		},
		Actor: "codex", Target: "running", Note: "stale reservation must fail", Now: cliTestNow.Add(2 * time.Second),
	})
	if !errors.Is(err, work.ErrExecutionReservationChanged) {
		t.Fatalf("stale claimed generation reservation error = %v", err)
	}
	got, getErr := fixture.store.Get(fixture.workID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if !reflect.DeepEqual(got, rotated) {
		t.Fatalf("stale reservation mutated the newer claimed generation:\nbefore=%+v\nafter=%+v", rotated, got)
	}
	attempts, listErr := runattempt.Open(fixture.homePath).List()
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(attempts) != 1 {
		t.Fatalf("stale reservation minted a child attempt; got %d attempts, want the claim attempt only", len(attempts))
	}
}

func TestReportRevokedExecutionAttemptDoesNotRotateNewerHeldGeneration(t *testing.T) {
	homePath := initializedExecutionHome(t)
	workID, _, runningActionIntentID := governedClaudeGuardReservation(t, homePath)
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	held, err := store.Advance(workID, work.AdvanceInput{To: "held", Actor: "codex", Note: "newer operator decision"}, cliTestNow.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	attemptStore := runattempt.Open(homePath)
	attempt, err := attemptStore.MintWithActionIntent(workID, "codex", "os-harness-entrypoints", runningActionIntentID, cliTestNow.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attemptStore.Complete(attempt.ID, runattempt.StatusFailed, cliTestNow.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	stderr := captureStderr(t, func() {
		reportRevokedExecutionAttempt(store, workID, attempt.ID)
	})
	if !strings.Contains(stderr, "remains reverse-linked only") || !strings.Contains(stderr, "preserving its state, policy, and action identity") {
		t.Fatalf("nonterminal revocation exception was not surfaced:\n%s", stderr)
	}

	got, err := store.Get(workID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, held) {
		t.Fatalf("revoked attempt report mutated newer held generation:\nbefore=%+v\nafter=%+v", held, got)
	}
	if slices.Contains(got.RunAttemptIDs, attempt.ID) {
		t.Fatalf("revoked attempt unexpectedly forward-linked into newer generation: %v", got.RunAttemptIDs)
	}
	persisted, err := attemptStore.Get(attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.WorkID != workID || persisted.ActionIntentID != runningActionIntentID {
		t.Fatalf("nonterminal exception lost reverse link: work=%q action=%q", persisted.WorkID, persisted.ActionIntentID)
	}
}

func TestReportRevokedExecutionAttemptSurfacesImmutableTerminalReverseLinkException(t *testing.T) {
	homePath := initializedExecutionHome(t)
	workID, _, runningActionIntentID := governedClaudeGuardReservation(t, homePath)
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	attemptStore := runattempt.Open(homePath)
	attempt, err := attemptStore.MintWithActionIntent(workID, "codex", "os-harness-entrypoints", runningActionIntentID, cliTestNow.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attemptStore.Complete(attempt.ID, runattempt.StatusFailed, cliTestNow.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Close(workID, work.CloseInput{
		Outcome: "failed", MoveType: "audit_only", Actor: "codex",
		Summary: "newer terminal operator decision", Changed: true,
	}, cliTestNow.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	stderr := captureStderr(t, func() {
		reportRevokedExecutionAttempt(store, workID, attempt.ID)
	})
	if !strings.Contains(stderr, "remains reverse-linked only") || !strings.Contains(stderr, "terminal receipt/work hashes are immutable") {
		t.Fatalf("terminal revocation exception was not surfaced:\n%s", stderr)
	}
	got, err := store.Get(workID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "failed" || slices.Contains(got.RunAttemptIDs, attempt.ID) {
		t.Fatalf("terminal revocation attachment rewrote immutable work: state=%s attempts=%v", got.State, got.RunAttemptIDs)
	}
	persisted, err := attemptStore.Get(attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.WorkID != workID || persisted.ActionIntentID != runningActionIntentID {
		t.Fatalf("terminal exception lost reverse link: work=%q action=%q", persisted.WorkID, persisted.ActionIntentID)
	}
}

func TestWorkAdvanceExecuteMissingOrStaleAuthorityKeepsClaudeInPlan(t *testing.T) {
	tests := []struct {
		name      string
		readiness work.DelegateHarnessReadinessResult
	}{
		{
			name: "missing blocking hook installation",
			readiness: work.DelegateHarnessReadinessResult{
				Ready:    true,
				Evidence: governedClaudeReadyEvidenceFixture()[:3],
			},
		},
		{
			name: "stale permission posture",
			readiness: work.DelegateHarnessReadinessResult{
				Ready:    false,
				Details:  []string{"permission_posture: managed settings differ from authority"},
				Evidence: governedClaudeReadyEvidenceFixture(),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newGovernedClaudeExecutionFixture(t, "unready authority keeps Claude read-only")
			readinessCalled := false
			stubDelegateHarnessReadiness(t, func(work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
				readinessCalled = true
				return tc.readiness, nil
			})

			fixture.run(t, "", "plan the claimed change")
			argv := fixture.argv(t)
			if !strings.Contains(argv, "--permission-mode plan") || strings.Contains(argv, "--dangerously-skip-permissions") {
				t.Fatalf("unready Claude argv = %q, want read-only plan", argv)
			}
			if !readinessCalled {
				t.Fatal("omitted Claude mode did not consult harness readiness")
			}
			ra := fixture.executionAttempt(t)
			if ra.ExecutionPosture == nil || ra.ExecutionPosture.PermissionMode != "plan" || len(ra.ExecutionPosture.AuthorityEvidence) != 0 {
				t.Fatalf("unready execution posture = %+v, want plan with no bypass authority evidence", ra.ExecutionPosture)
			}
		})
	}
}

// Explicit Claude modes do not bypass the governed rail. `default` can
// restore an unattended permission prompt and `acceptEdits` can mutate files,
// so both require the same current permission + blocking-hook authority as
// bypass. When that authority is stale, the rail keeps the execution useful
// but read-only by resolving the mode to plan.
func TestWorkAdvanceExecuteUnreadyExplicitMutationModesResolveToPlan(t *testing.T) {
	for _, requestedMode := range []string{"default", "acceptEdits", "bypass"} {
		t.Run(requestedMode, func(t *testing.T) {
			fixture := newGovernedClaudeExecutionFixture(t, "stale authority cannot launch an explicit mutation mode")
			stubDelegateHarnessReadiness(t, func(work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
				return work.DelegateHarnessReadinessResult{
					Ready:   false,
					Details: []string{"permission_posture: managed settings differ from authority"},
				}, nil
			})

			fixture.run(t, requestedMode, "stay read-only while authority is stale")
			argv := fixture.argv(t)
			if !strings.Contains(argv, "--permission-mode plan") {
				t.Fatalf("unready explicit %s argv = %q, want read-only plan", requestedMode, argv)
			}
			if strings.Contains(argv, "--dangerously-skip-permissions") || strings.Contains(argv, "--permission-mode "+requestedMode) {
				t.Fatalf("unready explicit %s retained mutation/prompt posture: %q", requestedMode, argv)
			}
		})
	}
}

func TestWorkAdvanceExecuteExplicitClaudePlanRemainsPlan(t *testing.T) {
	fixture := newGovernedClaudeExecutionFixture(t, "explicit Claude plan remains read-only")
	stubDelegateHarnessReadiness(t, func(work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
		t.Fatal("explicit plan must not consult mutation readiness")
		return work.DelegateHarnessReadinessResult{}, nil
	})

	fixture.run(t, "plan", "plan only")
	argv := fixture.argv(t)
	if !strings.Contains(argv, "--permission-mode plan") || strings.Contains(argv, "--dangerously-skip-permissions") {
		t.Fatalf("explicit plan Claude argv = %q, want plan", argv)
	}
}

func TestWorkAdvanceExecuteHeldPolicyDoesNotLaunchClaude(t *testing.T) {
	fixture := newGovernedClaudeExecutionFixture(t, "held running advance launches nothing")
	policyBody := []byte("class_outcome:\n  local_reversible:\n    outcome: hold\n")
	if err := os.WriteFile(filepath.Join(fixture.homePath, "config", "policy.yaml"), policyBody, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := fixture.command(t, "", "must not launch")
	out, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("held running advance unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(string(out), "policy hold") {
		t.Fatalf("held running advance did not name policy hold:\n%s", out)
	}
	if _, err := os.Stat(fixture.argvLog); !os.IsNotExist(err) {
		t.Fatalf("held running advance launched Claude; argv log stat err=%v", err)
	}
	final, err := fixture.store.Get(fixture.workID)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != "claimed" || len(final.RunAttemptIDs) != 1 {
		t.Fatalf("held running advance mutated execution state: state=%s attempts=%v", final.State, final.RunAttemptIDs)
	}
}

// TestWorkAdvanceExecuteBindsActionIntentID: F3 -- the executed RunAttempt
// (the exec-mint) is bound to the ActionIntent that authorized the
// running-advance, closing the attempt->intent->policy-decision audit
// chain. Falsifier: without RunInput.ActionIntentID wired, the minted
// RunAttempt's action_intent_id is empty; this pins that it is an
// act_-prefixed id AND that id is present in the work envelope's
// action_intent_ids (zip27 audit round F3).
func TestWorkAdvanceExecuteBindsActionIntentID(t *testing.T) {
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
		SuccessPredicate: "advance --execute binds action_intent_id",
	}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatal(err)
	}

	_ = captureStdout(t, func() {
		cmdWork([]string{
			"advance", w.ID,
			"--to", "running",
			"--execute",
			"--harness", "os-harness-entrypoints",
			"--", "echo", "action-intent-bind-smoke",
		})
	})

	final, err := ws.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(final.ActionIntentIDs) == 0 {
		t.Fatalf("expected at least one ActionIntentID on the work envelope")
	}

	store := runattempt.Open(homePath)
	ra, err := store.Get(final.RunAttemptIDs[len(final.RunAttemptIDs)-1])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ra.ActionIntentID, "act_") {
		t.Fatalf("exec-mint RunAttempt action_intent_id = %q, want an act_-prefixed id", ra.ActionIntentID)
	}
	if !slices.Contains(final.ActionIntentIDs, ra.ActionIntentID) {
		t.Fatalf("exec-mint RunAttempt action_intent_id %q not present in work envelope action_intent_ids %v", ra.ActionIntentID, final.ActionIntentIDs)
	}
}

// TestWorkAdvanceExecuteRejectsNonRunningTo: F6 -- --execute only ever
// advances through "running" (the adapter mint + verifying/failed advance
// below assumes it). A --to value other than "running" must be rejected
// BEFORE any state change, adapter resolution, or RunAttempt mint.
// Falsifier: prior behavior accepted any --to value and drove the adapter
// against a state the exec flow never expects (zip27 audit round F6).
func TestWorkAdvanceExecuteRejectsNonRunningTo(t *testing.T) {
	t.Parallel()

	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	ws, _ := work.Open(homePath)
	w, _ := ws.Create(work.CreateInput{
		Owner:            "codex",
		SuccessPredicate: "--execute --to verifying must be rejected before any state change",
	}, cliTestNow)
	if _, err := ws.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatal(err)
	}

	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	cmd := exec.Command(compoundTestBinary(t), "work", "advance", w.ID,
		"--to", "verifying", "--execute", "--harness", "os-harness-entrypoints",
		"--", "echo", "hi")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit for --execute --to verifying, got 0. output:\n%s", out)
	}
	if !strings.Contains(string(out), "--execute requires --to running") {
		t.Fatalf("expected '--execute requires --to running' in stderr, got:\n%s", out)
	}

	// The work must NOT have advanced -- still "claimed". Only the
	// claim-mint RunAttempt exists; the rejected --to must mint no
	// RunAttempt (F6 fires before any adapter resolution / advance side
	// effect).
	ws2, _ := work.Open(homePath)
	final, getErr := ws2.Get(w.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if final.State != "claimed" {
		t.Fatalf("--execute --to verifying must fail before any state change; state = %q, want claimed", final.State)
	}
	runAttemptFiles, _ := filepath.Glob(filepath.Join(homePath, "run_attempts", "ratt_*.json"))
	if len(runAttemptFiles) != 1 {
		t.Fatalf("rejected --to must mint no RunAttempt beyond the claim's; expected exactly 1 file (the claim-mint), got %d", len(runAttemptFiles))
	}
}

// TestWorkAdvanceExecuteStampsLaneIDOnExecAttempt: the exec-rail's minted
// RunAttempt (the execute-mint, not the claim-mint) carries lane_id = the
// resolved --by actor. This is the S3b harness-exec stamping falsifier.
//
// Falsifier (RED→GREEN, confirmed): with RunInput carrying no lane
// identity, the exec-mint lane_id = "" (no stamp). The additive fix --
// RunInput.LaneID populated from --by/--actor here and stamped via
// SetAttribution pre-terminalization in runWithSubprocess -- turns it
// GREEN. The claim-mint (work.Claim) is not lane-stamped on this path; the
// execute-mint is the LAST id, so the assertion targets it.
func TestWorkAdvanceExecuteStampsLaneIDOnExecAttempt(t *testing.T) {
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
		SuccessPredicate: "advance --execute stamps lane_id",
	}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatal(err)
	}
	_ = captureStdout(t, func() {
		cmdWork([]string{
			"advance", w.ID,
			"--to", "running",
			"--execute",
			"--by", "codex",
			"--harness", "os-harness-entrypoints",
			"--", "echo", "lane-stamp-smoke",
		})
	})
	final, err := ws.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(final.RunAttemptIDs) < 2 {
		t.Fatalf("expected claim-mint + execute-mint, got %d RunAttemptIDs", len(final.RunAttemptIDs))
	}
	store := runattempt.Open(homePath)
	// The execute-mint is the LAST id (the claim-mint is first).
	ra, err := store.Get(final.RunAttemptIDs[len(final.RunAttemptIDs)-1])
	if err != nil {
		t.Fatal(err)
	}
	if ra.LaneID != "codex" {
		t.Fatalf("exec-mint RunAttempt lane_id = %q, want %q (the resolved --by actor)", ra.LaneID, "codex")
	}
}

// TestWorkAdvanceExecuteNoSeparatorLeaksNoFlagTail: with neither --prompt
// nor a post-`--` command, --execute must refuse before any state change.
//
// Falsifier (RED→GREEN): the parse defaulted restArgs to the WHOLE flag
// tail when no `--` was present, so command became "--to", the guard never
// fired, and the harness launched with the CLI's own flags as its argv.
// That burns the work item: the launch fails instantly and a failed run
// terminalizes it (failed is terminal — no recovery path).
func TestWorkAdvanceExecuteNoSeparatorLeaksNoFlagTail(t *testing.T) {
	t.Parallel()

	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	ws, _ := work.Open(homePath)
	w, err := ws.Create(work.CreateInput{
		Owner:            "codex",
		SuccessPredicate: "--execute with no --prompt and no post-`--` command refuses instead of leaking the flag tail",
	}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatal(err)
	}

	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	cmd := exec.Command(compoundTestBinary(t), "work", "advance", w.ID,
		"--to", "running", "--by", "codex", "--execute", "--harness", "os-harness-entrypoints")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	out, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("expected non-zero exit for --execute with no --prompt and no post-`--` command, got 0. output:\n%s", out)
	}
	if !strings.Contains(string(out), "--execute requires either --prompt or a post-`--` command") {
		t.Fatalf("expected the --prompt|command guard refusal in stderr, got:\n%s", out)
	}

	// The refusal must fire BEFORE any state change: still "claimed", and
	// only the claim-mint RunAttempt exists.
	ws2, _ := work.Open(homePath)
	final, getErr := ws2.Get(w.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if final.State != "claimed" {
		t.Fatalf("no-separator --execute must refuse before any state change; state = %q, want claimed", final.State)
	}
	runAttemptFiles, _ := filepath.Glob(filepath.Join(homePath, "run_attempts", "ratt_*.json"))
	if len(runAttemptFiles) != 1 {
		t.Fatalf("refusal must mint no RunAttempt beyond the claim's; want exactly 1 file, got %d", len(runAttemptFiles))
	}
}

// TestWorkAdvanceExecuteMissingWorkspaceDirNoStateChange: a resolved workspace
// that does not exist on disk must refuse BEFORE the governed running advance.
//
// Falsifier (RED->GREEN): the ladder returned the resolved path without ever
// stat-ing it, and Preflight never touches Workspace either. So the order was
// resolve -> preflight -> Advance(running) -> adapter.Run, and cmd.Start()
// failed with "chdir: no such file or directory" only AFTER the state change.
// failTerm then terminalized the RunAttempt and the CLI closed the envelope
// "failed (audit_only)" -- and failed is terminal, so the work item was burned
// with no recovery path. This is not hypothetical: wave10 envelopes still
// record .claude/worktrees roots that were retired, so dispatching one without
// an explicit --workspace destroyed it in a single invocation.
//
// The teeth are the post-conditions, not the exit code: an exit-2 refusal that
// still advanced the envelope would be the same bug wearing a better message.
func TestWorkAdvanceExecuteMissingWorkspaceDirNoStateChange(t *testing.T) {
	t.Parallel()

	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	ws, _ := work.Open(homePath)
	w, err := ws.Create(work.CreateInput{
		Owner:            "codex",
		SuccessPredicate: "a missing execution workspace must not move the work",
	}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatal(err)
	}

	// A path that is guaranteed absent: inside the per-test temp dir, never created.
	missing := filepath.Join(t.TempDir(), "retired-worktree-root")

	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	cmd := exec.Command(compoundTestBinary(t), "work", "advance", w.ID,
		"--to", "running", "--by", "codex", "--execute",
		"--harness", "os-harness-entrypoints",
		"--workspace", missing,
		"--", "echo", "hi")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	out, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("expected non-zero exit for a missing workspace dir, got 0. output:\n%s", out)
	}

	// The refusal must name the gate and the path it rejected, so the reader is
	// not left guessing which of the three ladder rungs produced it.
	for _, want := range []string{
		"gate workspace_existence",
		missing,
		"tried in order:",
		"prevents:",
	} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("expected %q in the workspace-existence refusal, got:\n%s", want, out)
		}
	}

	ws2, _ := work.Open(homePath)
	final, getErr := ws2.Get(w.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if final.State != "claimed" {
		t.Fatalf("a missing workspace must refuse before any state change; state = %q, want claimed (the burn left it terminal 'failed')", final.State)
	}
	// A burn closed the envelope, and a close emits a Transition. No transition
	// means nothing was terminalized.
	if final.IDs.TransitionID != "" {
		t.Fatalf("a missing workspace must not terminalize the envelope; transition_id = %q, want empty", final.IDs.TransitionID)
	}
	runAttemptFiles, _ := filepath.Glob(filepath.Join(homePath, "run_attempts", "ratt_*.json"))
	if len(runAttemptFiles) != 1 {
		t.Fatalf("refusal must mint no RunAttempt beyond the claim's; want exactly 1 file, got %d", len(runAttemptFiles))
	}
}

// TestWorkAdvanceExecuteExistingWorkspaceStillRuns is the paired falsifier that
// must stay GREEN: the existence gate must reject only what is absent. If this
// goes red, the guard has stopped being a guard and become a wall.
func TestWorkAdvanceExecuteExistingWorkspaceStillRuns(t *testing.T) {
	t.Parallel()

	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	ws, _ := work.Open(homePath)
	w, err := ws.Create(work.CreateInput{
		Owner:            "codex",
		SuccessPredicate: "an existing workspace still executes",
	}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatal(err)
	}

	present := t.TempDir()
	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	cmd := exec.Command(compoundTestBinary(t), "work", "advance", w.ID,
		"--to", "running", "--by", "codex", "--execute",
		"--harness", "os-harness-entrypoints",
		"--workspace", present,
		"--", "echo", "hi")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("an existing workspace must still execute, got error %v. output:\n%s", runErr, out)
	}
	if strings.Contains(string(out), "gate workspace_existence") {
		t.Fatalf("the existence gate must not fire for a directory that exists. output:\n%s", out)
	}
}

// TestWorkAdvanceExecuteWorkspaceFileNotDirNoStateChange: a path that exists but
// is a regular file is equally unusable as a working directory (chdir fails the
// same way), so it must take the same fail-closed path rather than reaching Run.
func TestWorkAdvanceExecuteWorkspaceFileNotDirNoStateChange(t *testing.T) {
	t.Parallel()

	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	ws, _ := work.Open(homePath)
	w, err := ws.Create(work.CreateInput{
		Owner:            "codex",
		SuccessPredicate: "a file-as-workspace must not move the work",
	}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatal(err)
	}

	filePath := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	cmd := exec.Command(compoundTestBinary(t), "work", "advance", w.ID,
		"--to", "running", "--by", "codex", "--execute",
		"--harness", "os-harness-entrypoints",
		"--workspace", filePath,
		"--", "echo", "hi")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	out, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("expected non-zero exit for a file-as-workspace, got 0. output:\n%s", out)
	}
	if !strings.Contains(string(out), "gate workspace_existence") {
		t.Fatalf("expected the workspace-existence refusal for a non-directory, got:\n%s", out)
	}

	ws2, _ := work.Open(homePath)
	final, getErr := ws2.Get(w.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if final.State != "claimed" {
		t.Fatalf("a file-as-workspace must refuse before any state change; state = %q, want claimed", final.State)
	}
}
