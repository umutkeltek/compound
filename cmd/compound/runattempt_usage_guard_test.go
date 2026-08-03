package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/runattempt"
)

const runAttemptUsageChildArgsEnv = "COMPOUND_TEST_RUN_ATTEMPT_USAGE_ARGS"

// A mutating sweep must treat help as read-only and malformed arguments as a
// usage error. Every case runs against a real stale attempt: if validation
// happens after the sweep, the stored status exposes the mutation even when the
// command printed a plausible banner or error.
func TestRunAttemptSweepValidatesArgumentsBeforeMutation(t *testing.T) {
	if encoded := os.Getenv(runAttemptUsageChildArgsEnv); encoded != "" {
		raw, err := base64.RawStdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		var args []string
		if err := json.Unmarshal(raw, &args); err != nil {
			t.Fatal(err)
		}
		cmdRunAttempt(args)
		return
	}

	tests := []struct {
		name     string
		args     []string
		wantExit int
	}{
		{name: "long help", args: []string{"sweep", "--help"}, wantExit: 0},
		{name: "short help", args: []string{"sweep", "-h"}, wantExit: 0},
		{name: "unknown flag", args: []string{"sweep", "--bogus"}, wantExit: 2},
		{name: "threshold missing", args: []string{"sweep", "--threshold"}, wantExit: 2},
		{name: "threshold consumes flag", args: []string{"sweep", "--threshold", "--json"}, wantExit: 2},
		{name: "home missing", args: []string{"sweep", "--home"}, wantExit: 2},
		{name: "home consumes flag", args: []string{"sweep", "--home", "--json"}, wantExit: 2},
		{name: "negative threshold", args: []string{"sweep", "--threshold", "-5m"}, wantExit: 2},
		{name: "zero threshold", args: []string{"sweep", "--threshold", "0"}, wantExit: 2},
		{name: "unexpected positional", args: []string{"sweep", "surprise"}, wantExit: 2},
		{name: "list unknown flag", args: []string{"list", "--bogus"}, wantExit: 2},
		{name: "list home missing", args: []string{"list", "--home"}, wantExit: 2},
		{name: "list home consumes flag", args: []string{"list", "--home", "--json"}, wantExit: 2},
		{name: "list unexpected positional", args: []string{"list", "surprise"}, wantExit: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			homePath := t.TempDir()
			if err := home.Init(homePath, "test", cliTestNow); err != nil {
				t.Fatalf("init home: %v", err)
			}
			store := runattempt.Open(homePath)
			attempt, err := store.Mint("work_guard", "actor:test", "claude-code", cliTestNow.Add(-24*time.Hour))
			if err != nil {
				t.Fatalf("mint attempt: %v", err)
			}

			raw, err := json.Marshal(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestRunAttemptSweepValidatesArgumentsBeforeMutation$")
			cmd.Env = append(os.Environ(),
				"COMPOUND_HOME="+homePath,
				runAttemptUsageChildArgsEnv+"="+base64.RawStdEncoding.EncodeToString(raw),
			)
			output, runErr := cmd.CombinedOutput()
			gotExit := 0
			if runErr != nil {
				exitErr, ok := runErr.(*exec.ExitError)
				if !ok {
					t.Fatalf("run child: %v\n%s", runErr, output)
				}
				gotExit = exitErr.ExitCode()
			}
			if gotExit != tt.wantExit {
				t.Fatalf("exit=%d, want %d; output:\n%s", gotExit, tt.wantExit, output)
			}
			stored, err := store.Get(attempt.ID)
			if err != nil {
				t.Fatalf("read attempt: %v", err)
			}
			if stored.Status != runattempt.StatusPending {
				t.Fatalf("%v mutated stale attempt to %q; output:\n%s", tt.args, stored.Status, output)
			}
			if tt.wantExit == 2 && !strings.Contains(string(output), "usage: compound run-attempt") {
				t.Fatalf("usage error omitted command help:\n%s", output)
			}
		})
	}
}
