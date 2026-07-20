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
//  1. Advance to the user-specified --to state (typically "running").
//     RunAttemptIDs are empty here because the adapter has not yet
//     minted -- the validating envelope rule (ratt_ prefix) forbids
//     stale ids on the running state.
//  2. Mint a RunAttempt via the adapter, run the subprocess, capture
//     transcript. The adapter drives its own lifecycle (C.1).
//  3. Advance to "verifying" (succeeded) or "failed" (non-zero exit
//     or timeout). The ratt_ id lands on RunAttemptIDs; the transcript
//     path lands on VerificationID. The audit chain is complete.
//
// Exit code: 0 on succeeded, non-zero on failed/timeout. Matches the
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
	// advance, not admit.
	if _, err := store.Get(workID); err != nil {
		fmt.Fprintf(os.Stderr, "work envelope %q: %v\n", workID, err)
		os.Exit(1)
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

	// Run the adapter.
	runAttemptStore := runattempt.Open(homePath)
	r := harness.NewAdapterRegistry(runAttemptStore)
	a, ok := r.AdapterByName(harnessName)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown harness %q; available: %v\n", harnessName, r.Names())
		os.Exit(1)
	}

	out, err := a.Run(context.Background(), harness.RunInput{
		WorkID:    workID,
		Harness:   harnessName,
		Workspace: homePath,
		Command:   command,
		Args:      commandArgs,
		Prompt:    prompt,
		Now:       time.Now().UTC(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "harness %s run: %v\n", harnessName, err)
		os.Exit(1)
	}

	// Advance 2: to verifying (succeeded) or close as failed (not).
	// "verifying" is a non-terminal state, so Advance works.
	// "failed" is terminal, so we must use Close (the work state
	// machine enforces: terminalStates cannot be reached via Advance;
	// use Close so the receipt + transition proof are emitted).
	allRunAttempts := append([]string(nil), existingRunAttempts...)
	allRunAttempts = append(allRunAttempts, out.RunAttempt.ID)
	if out.RunAttempt.Status == runattempt.StatusSucceeded {
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
		// Failed harness run -> close the work as failed. Move type
		// "audit_only" matches the gate-blocked close path: nothing was
		// produced, the audit trail is the run record + the transcript.
		// CloseInput does not yet carry RunAttemptIDs (a future slice
		// adds that field); the audit chain is intact via the run
		// record on disk and the proof_refs (transcript path).
		_, err := store.Close(workID, work.CloseInput{
			Outcome:   "failed",
			MoveType:  "audit_only",
			Actor:     actor,
			Summary:   "harness " + harnessName + " " + out.RunAttempt.Status,
			Changed:   true,
			ProofRefs: []string{out.TranscriptPath},
		}, time.Now().UTC())
		if err != nil {
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
	// "failed") is independent of the process exit code; we use the
	// exit code to signal "this whole thing did not succeed", which
	// CI / make / orchestration can react to.
	if out.RunAttempt.Status != runattempt.StatusSucceeded {
		os.Exit(1)
	}
}
