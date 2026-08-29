package main

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/runattempt"
	"compound/internal/work"
)

const delegateNoImplicitSweepChildEnv = "COMPOUND_TEST_DELEGATE_NO_IMPLICIT_SWEEP"
const delegateNoImplicitSweepModeEnv = "COMPOUND_TEST_DELEGATE_NO_IMPLICIT_SWEEP_MODE"

// Delegate cleanup has an explicit owner (`compound run-attempt sweep`). No
// delegate command may opportunistically mutate unrelated attempts, including
// an explicitly opted-in invocation that parses but fails semantic preflight.
// This subprocess exercises the real cmdWork wiring above the parser seam.
func TestWorkDelegateCommandsNeverImplicitlySweepRunAttempts(t *testing.T) {
	if workID := os.Getenv(delegateNoImplicitSweepChildEnv); workID != "" {
		var args []string
		switch os.Getenv(delegateNoImplicitSweepModeEnv) {
		case "status":
			args = []string{"delegate", "status", workID, "--json"}
		case "dry-run":
			args = []string{"delegate", workID, "--harness", "claude-zai", "--dry-run", "--json"}
		case "default-off":
			args = []string{"delegate", workID, "--harness", "claude-zai", "--json"}
		case "semantic-refusal":
			args = []string{"delegate", workID, "--harness", "not-a-real-harness", work.ExperimentalDelegateMutationsFlag, "--json"}
		default:
			t.Fatal("unknown delegate no-sweep child mode")
		}
		cmdWork(args)
		return
	}

	tests := []struct {
		mode     string
		wantExit int
	}{
		{mode: "status", wantExit: 0},
		{mode: "dry-run", wantExit: 1},
		{mode: "default-off", wantExit: 2},
		{mode: "semantic-refusal", wantExit: 1},
	}
	for _, tc := range tests {
		t.Run(tc.mode, func(t *testing.T) {
			now := time.Now().UTC()
			homePath := t.TempDir()
			if err := home.Init(homePath, "test", now); err != nil {
				t.Fatalf("init home: %v", err)
			}
			store, err := work.Open(homePath)
			if err != nil {
				t.Fatal(err)
			}
			w, err := store.Create(work.CreateInput{
				Owner: "orchestrator:test", SuccessPredicate: "delegate command cannot sweep unrelated attempts", State: "ready",
			}, now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Claim(w.ID, "orchestrator:test", now); err != nil {
				t.Fatal(err)
			}
			stale, err := runattempt.Open(homePath).Mint(
				w.ID,
				"orchestrator:test",
				"claude-zai",
				now.Add(-runattempt.DefaultSweepThreshold-time.Hour),
			)
			if err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command(os.Args[0], "-test.run=^TestWorkDelegateCommandsNeverImplicitlySweepRunAttempts$")
			cmd.Env = append(os.Environ(),
				"COMPOUND_HOME="+homePath,
				delegateNoImplicitSweepChildEnv+"="+w.ID,
				delegateNoImplicitSweepModeEnv+"="+tc.mode,
			)
			output, runErr := cmd.CombinedOutput()
			gotExit := 0
			if runErr != nil {
				exitErr, ok := runErr.(*exec.ExitError)
				if !ok {
					t.Fatalf("delegate %s subprocess: %v\n%s", tc.mode, runErr, output)
				}
				gotExit = exitErr.ExitCode()
			}
			if gotExit != tc.wantExit {
				t.Fatalf("exit=%d want=%d err=%v\n%s", gotExit, tc.wantExit, runErr, output)
			}
			got, err := runattempt.Open(homePath).Get(stale.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != runattempt.StatusPending {
				t.Fatalf("delegate %s changed unrelated stale attempt %s from pending to %s\n%s", tc.mode, stale.ID, got.Status, output)
			}
		})
	}
}
