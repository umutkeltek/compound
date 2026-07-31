package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/routeevidence"
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
