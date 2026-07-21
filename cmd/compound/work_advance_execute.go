package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"compound/internal/harness"
	"compound/internal/runattempt"
	"compound/internal/work"
)

// runWorkAdvanceExecute is the --execute branch of compound work advance.
// It runs a registered harness adapter inline as part of the work
// advance so the work moves through running -> verifying (or failed) in
// a single CLI invocation.
//
// The flow:
//  1. Validate the work envelope + resolve the adapter + resolve the
//     workspace BEFORE any state change. An unknown harness, a missing
//     adapter, or an unresolvable workspace fails closed here -- the work
//     never moves, so it can never be left stuck in "running".
//  2. Advance to the user-specified --to state (typically "running").
//     RunAttemptIDs are empty here because the adapter has not yet
//     minted -- the validating envelope rule (ratt_ prefix) forbids
//     stale ids on the running state.
//  3. Mint a RunAttempt via the adapter, run the subprocess, capture
//     transcript. The adapter drives its own lifecycle (C.1).
//  4. Advance to "verifying" (succeeded) or close as "failed" (non-zero
//     exit, timeout, or infrastructure failure). The ratt_ id lands on
//     RunAttemptIDs on BOTH paths -- the failed-path forward-link is the
//     close-time RunAttemptIDs (see work.CloseInput.RunAttemptIDs). The
//     audit chain is complete either way.
//
// Exit code: 0 on succeeded, non-zero on failed/timeout/infra. Matches the
// compound harness run contract.
func runWorkAdvanceExecute(homePath string, store *work.Store, workID string, rest []string, existingRunAttempts []string, now time.Time) {
	if workID == "" {
		fmt.Fprintln(os.Stderr, "error: --execute requires a work_id")
		os.Exit(2)
	}
	harnessName := valueFlag(rest, "--harness")
	if harnessName == "" {
		fmt.Fprintln(os.Stderr, "error: --execute requires --harness <name>")
		os.Exit(2)
	}
	prompt := valueFlag(rest, "--prompt")
	actor := firstNonEmpty(valueFlag(rest, "--by"), valueFlag(rest, "--actor"))

	// Parse post-`--` for command + args. Everything before the `--` is
	// flag parsing; everything after is the subprocess argv. The first
	// post-`--` token is the program path; the rest are its args.
	restArgs := rest
	if idx := indexOf(rest, "--"); idx >= 0 {
		restArgs = rest[idx+1:]
	}
	var command string
	var commandArgs []string
	if len(restArgs) > 0 {
		command = restArgs[0]
		commandArgs = restArgs[1:]
	}
	if command == "" && prompt == "" {
		fmt.Fprintln(os.Stderr, "error: --execute requires either --prompt or a post-`--` command")
		os.Exit(2)
	}

	// Validate work envelope exists. Read-only check -- we are about to
	// advance, not admit. The returned envelope feeds workspace resolution.
	wenv, err := store.Get(workID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "work envelope %q: %v\n", workID, err)
		os.Exit(1)
	}

	// Resolve the adapter BEFORE any state change. An unknown harness name
	// or a missing adapter must fail closed here -- the old flow advanced
	// first and looked up second, so a typo in --harness left the work
	// stuck in "running" with no RunAttempt and no recovery path.
	runAttemptStore := runattempt.Open(homePath)
	registry := harness.NewAdapterRegistry(runAttemptStore)
	adapter, ok := registry.AdapterByName(harnessName)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown harness %q; available: %v\n", harnessName, registry.Names())
		os.Exit(1)
	}

	// Resolve the execution workspace BEFORE advancing. Never default to
	// COMPOUND_HOME -- running a real code-editing harness there edits the
	// state/config dir, not the project. See resolveExecuteWorkspace.
	workspace := resolveExecuteWorkspace(rest, wenv.GitDelivery)
	if workspace == "" {
		fmt.Fprintln(os.Stderr, "error: cannot determine execution workspace; pass --workspace <path>")
		os.Exit(2)
	}

	// Advance 1: to the user-specified state (default "running" if --to
	// was not passed).
	target := valueFlag(rest, "--to")
	if target == "" {
		target = "running"
	}
	note := valueFlag(rest, "--note")
	if note == "" {
		note = "harness execute: " + harnessName
	}
	w, err := store.Advance(workID, work.AdvanceInput{
		To:    target,
		Actor: actor,
		Note:  note,
	}, now)
	die(err)
	fmt.Printf("%s -> %s policy=%s\n", w.ID, w.State, w.PolicyDecisionID)

	// Run the adapter. On infrastructure failure (runErr != nil) the
	// adapter has already terminalized the RunAttempt failed
	// (subprocess_runner.failTerm) and out.RunAttempt is populated -- we
	// carry that ratt_ id onto the close so the failed run is reachable
	// from the envelope, not only from run_attempts/<id>.json.
	out, runErr := adapter.Run(context.Background(), harness.RunInput{
		WorkID:         workID,
		Harness:        harnessName,
		Workspace:      workspace,
		Command:        command,
		Args:           commandArgs,
		Prompt:         prompt,
		PermissionMode: valueFlag(rest, "--permission-mode"),
		Now:            time.Now().UTC(),
	})

	// S3 attribution gap (documented deviation): `actor` (the resolved lane
	// from --by/--actor) is known HERE, but it cannot be stamped onto this
	// run's RunAttempt. The adapter (internal/harness, owned by the concurrent
	// S2 lane) mints AND terminalizes the RunAttempt atomically inside Run --
	// out.RunAttempt is already succeeded/failed by this point, so
	// runattempt.SetAttribution's terminal-refusal guard rejects a post-run
	// stamp. harness.RunInput carries no lane/model/provider identity today,
	// and NewAdapterRegistry takes a concrete *runattempt.Store (no wrapper
	// seam). Additive fix (owned by the harness lane): accept a lane identity
	// on RunInput and stamp it pre-terminalization in subprocess_runner.go, at
	// which point the lane_id==actor assertion pinned in
	// work_advance_execute_test.go (currently t.Skip) goes green. Model/provider
	// are not exposed by any adapter config either, so they are not stampable
	// on this path regardless.

	allRunAttempts := append([]string(nil), existingRunAttempts...)
	if out.RunAttempt.ID != "" {
		allRunAttempts = append(allRunAttempts, out.RunAttempt.ID)
	}

	succeeded := runErr == nil && out.RunAttempt.Status == runattempt.StatusSucceeded
	if succeeded {
		// Advance 2: to verifying. "verifying" is a non-terminal state,
		// so Advance works. RunAttemptIDs + VerificationID close the
		// audit chain.
		w2, err := store.Advance(workID, work.AdvanceInput{
			To:             "verifying",
			Actor:          actor,
			RunAttemptIDs:  allRunAttempts,
			VerificationID: out.TranscriptPath,
			Note:           "harness " + harnessName + " " + out.RunAttempt.Status,
		}, time.Now().UTC())
		if err != nil {
			die(err)
		}
		fmt.Printf("%s -> %s\n", w2.ID, w2.State)
	} else {
		// Failed harness run -- subprocess non-zero exit, timeout, OR
		// infrastructure failure. Close the work as failed. Move type
		// "audit_only" matches the gate-blocked close path: nothing
		// landed, the audit trail is the run record + transcript.
		// RunAttemptIDs forward-links the (already-terminal) run so the
		// envelope points at the failed attempt -- the same shape as the
		// succeeded path, via the close-time field.
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "harness %s run: %v\n", harnessName, runErr)
		}
		closeIn := work.CloseInput{
			Outcome:       "failed",
			MoveType:      "audit_only",
			Actor:         actor,
			Summary:       "harness " + harnessName + " " + out.RunAttempt.Status,
			Changed:       true,
			RunAttemptIDs: allRunAttempts,
		}
		if out.TranscriptPath != "" {
			closeIn.ProofRefs = []string{out.TranscriptPath}
		}
		if _, err := store.Close(workID, closeIn, time.Now().UTC()); err != nil {
			die(err)
		}
		fmt.Printf("%s -> failed (audit_only)\n", workID)
	}

	// Print the RunAttempt summary on the same stdout stream as the
	// advance output. Operators can pipe to jq or grep for the id.
	fmt.Printf("run_attempt_id: %s\n", out.RunAttempt.ID)
	fmt.Printf("status: %s\n", out.RunAttempt.Status)
	fmt.Printf("harness: %s\n", out.RunAttempt.Harness)
	if out.TranscriptPath != "" {
		fmt.Printf("transcript: %s\n", out.TranscriptPath)
	}
	fmt.Printf("exit_code: %d\n", out.ExitCode)
	if out.TimedOut {
		fmt.Printf("timed_out: true\n")
	}
	fmt.Printf("duration_ms: %d\n", out.Duration.Milliseconds())

	// Non-zero exit on failed harness run -- the operator contract
	// matches `compound harness run`. A failed work lifecycle (state
	// "failed") is independent of the process exit code; we use the exit
	// code to signal "this whole thing did not succeed", which CI / make
	// / orchestration can react to.
	if !succeeded {
		os.Exit(1)
	}
}
