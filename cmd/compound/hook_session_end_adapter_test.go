package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"compound/internal/eventstore"
	"compound/internal/home"
)

const sessionEndAdapterOutcomeEvidenceSchema = "compound.session_end_adapter_outcome.v0.1"

type sessionEndAdapterAttemptEvidence struct {
	SchemaVersion string   `json:"schema_version"`
	ID            string   `json:"id"`
	Status        string   `json:"status"`
	SessionID     string   `json:"session_id"`
	InputRef      string   `json:"input_ref"`
	RetryArgv     []string `json:"retry_argv"`
}

type sessionEndAdapterOutcomeEvidence struct {
	SchemaVersion string `json:"schema_version"`
	AttemptID     string `json:"attempt_id"`
	InputRef      string `json:"input_ref"`
	Status        string `json:"status"`
}

func TestSessionEndAdapterRejectsWorkerOverrideOutsideTestTemp(t *testing.T) {
	t.Setenv(sessionEndAdapterWorkerOverrideEnv, "1")
	_, err := parseSessionEndAdapterArgs([]string{
		"--live", "--harness", "claude-code", "--worker", "/usr/bin/true",
	})
	if err == nil || !strings.Contains(err.Error(), "test temporary directory") {
		t.Fatalf("non-temporary worker override error = %v", err)
	}
}

func TestSessionEndAdapterBoundsAcceptancePersistenceBeforeStartingWorker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFO persistence fixture requires a POSIX host")
	}

	compoundHome := t.TempDir()
	workerMarker := filepath.Join(t.TempDir(), "worker-started")
	workerPath := filepath.Join(t.TempDir(), "observable-session-end-worker.sh")
	worker := fmt.Sprintf("#!/bin/sh\ntouch %q\n", workerMarker)
	if err := os.WriteFile(workerPath, []byte(worker), 0o755); err != nil {
		t.Fatal(err)
	}
	input := `{"hook_event_name":"SessionEnd","session_id":"sess_slow_acceptance","cwd":"/repo/example"}`
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(input)))
	artifactDir := filepath.Join(compoundHome, "artifacts", "sha256")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("mkfifo", filepath.Join(artifactDir, digest)).CombinedOutput(); err != nil {
		t.Fatalf("mkfifo: %v: %s", err, output)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		"go", "run", ".", "hook", "session-end-adapter",
		"--live", "--harness", "claude-code",
		"--timeout", "100ms", "--worker", workerPath,
	)
	command.Env = append(
		os.Environ(),
		"COMPOUND_HOME="+compoundHome,
		sessionEndAdapterWorkerOverrideEnv+"=1",
	)
	command.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	started := time.Now()
	err := command.Run()
	elapsed := time.Since(started)
	if ctx.Err() != nil {
		t.Fatalf("SessionEnd acceptance persistence remained blocked until its test guard fired after %s: %v", elapsed, ctx.Err())
	}
	if err != nil {
		t.Fatalf("SessionEnd adapter exit = %v after %s\nstdout=%s\nstderr=%s", err, elapsed, stdout.String(), stderr.String())
	}
	if elapsed > 10*time.Second {
		t.Fatalf("SessionEnd acceptance exceeded its bounded handoff window: %s", elapsed)
	}
	var response claudeLiveHookResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil || response.HookSpecificOutput.HookEventName != "SessionEnd" {
		t.Fatalf("SessionEnd acceptance failure did not emit valid hook JSON: err=%v response=%+v stdout=%s", err, response, stdout.String())
	}
	if !strings.Contains(stderr.String(), "acceptance persistence timed out") || !strings.Contains(stderr.String(), "sha256:"+digest) {
		t.Fatalf("SessionEnd acceptance timeout was not explicit: %s", stderr.String())
	}
	if _, err := os.Stat(workerMarker); !os.IsNotExist(err) {
		t.Fatalf("SessionEnd worker started before durable acceptance: stat err=%v", err)
	}
}

func TestSessionEndAdapterTimesOutSlowWorkerWithoutCancellingHookAndKeepsRetryEvidence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell worker fixture requires a POSIX host")
	}

	compoundHome := t.TempDir()
	workerPath := filepath.Join(t.TempDir(), "slow-session-end-worker.sh")
	if err := os.WriteFile(workerPath, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	input := `{"hook_event_name":"SessionEnd","session_id":"sess_slow_worker","cwd":"/repo/example","transcript_path":"/repo/example/transcript.jsonl"}`

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		"go", "run", ".", "hook", "session-end-adapter",
		"--live", "--harness", "claude-code",
		"--timeout", "100ms", "--worker", workerPath,
	)
	command.Env = append(
		os.Environ(),
		"COMPOUND_HOME="+compoundHome,
		sessionEndAdapterWorkerOverrideEnv+"=1",
	)
	command.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	started := time.Now()
	err := command.Run()
	elapsed := time.Since(started)
	if ctx.Err() != nil {
		t.Fatalf("SessionEnd adapter remained blocked until its test guard fired after %s: %v\n%s", elapsed, ctx.Err(), stderr.String())
	}
	if err != nil {
		t.Fatalf("SessionEnd adapter exit = %v after %s\nstdout=%s\nstderr=%s", err, elapsed, stdout.String(), stderr.String())
	}
	if elapsed > 10*time.Second {
		t.Fatalf("SessionEnd adapter exceeded its bounded handoff window: %s", elapsed)
	}

	var response claudeLiveHookResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("SessionEnd adapter did not emit valid Claude hook JSON: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	if response.HookSpecificOutput.HookEventName != "SessionEnd" {
		t.Fatalf("SessionEnd adapter response = %+v", response)
	}
	if !strings.Contains(stderr.String(), "timed out") || !strings.Contains(stderr.String(), home.ArtifactRefPrefix) {
		t.Fatalf("SessionEnd adapter hid its timeout/retry evidence: %s", stderr.String())
	}

	store, err := eventstore.Open(compoundHome)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := store.Scan(eventstore.ScanOptions{Prefixes: []string{"sessend_"}, RegularOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("durable SessionEnd records = %d, want acceptance + terminal outcome", len(entries))
	}
	var attempt sessionEndAdapterAttemptEvidence
	var outcome sessionEndAdapterOutcomeEvidence
	for _, entry := range entries {
		record, err := entry.Read()
		if err != nil {
			t.Fatal(err)
		}
		var header struct {
			SchemaVersion string `json:"schema_version"`
		}
		if err := json.Unmarshal(record.Bytes, &header); err != nil {
			t.Fatal(err)
		}
		switch header.SchemaVersion {
		case sessionEndAdapterAttemptSchema:
			if err := json.Unmarshal(record.Bytes, &attempt); err != nil {
				t.Fatal(err)
			}
		case sessionEndAdapterOutcomeEvidenceSchema:
			if err := json.Unmarshal(record.Bytes, &outcome); err != nil {
				t.Fatal(err)
			}
		}
	}
	if attempt.Status != "accepted" || attempt.SessionID != "sess_slow_worker" || attempt.InputRef == "" {
		t.Fatalf("durable SessionEnd handoff = %+v", attempt)
	}
	if outcome.Status != "timed_out" || outcome.AttemptID != attempt.ID || outcome.InputRef != attempt.InputRef {
		t.Fatalf("durable SessionEnd terminal outcome = %+v, acceptance=%+v", outcome, attempt)
	}
	if !strings.Contains(strings.Join(attempt.RetryArgv, " "), "hook session-end --live --harness claude-code") {
		t.Fatalf("durable SessionEnd handoff lost retry argv: %+v", attempt.RetryArgv)
	}
	artifacts, err := home.NewArtifactStore(compoundHome)
	if err != nil {
		t.Fatal(err)
	}
	storedInput, err := artifacts.Get(attempt.InputRef)
	if err != nil {
		t.Fatal(err)
	}
	if string(storedInput) != input {
		t.Fatalf("durable SessionEnd input changed:\nwant=%s\ngot=%s", input, storedInput)
	}
}

func TestSessionEndAdapterMarksSuccessfulWorkerProcessing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell worker fixture requires a POSIX host")
	}

	compoundHome := t.TempDir()
	workerPath := filepath.Join(t.TempDir(), "successful-session-end-worker.sh")
	worker := "#!/bin/sh\nprintf '%s\\n' '{\"hookSpecificOutput\":{\"hookEventName\":\"SessionEnd\"}}'\n"
	if err := os.WriteFile(workerPath, []byte(worker), 0o755); err != nil {
		t.Fatal(err)
	}
	input := `{"hook_event_name":"SessionEnd","session_id":"sess_successful_worker","cwd":"/repo/example"}`

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		"go", "run", ".", "hook", "session-end-adapter",
		"--live", "--harness", "claude-code",
		"--timeout", "1s", "--worker", workerPath,
	)
	command.Env = append(
		os.Environ(),
		"COMPOUND_HOME="+compoundHome,
		sessionEndAdapterWorkerOverrideEnv+"=1",
	)
	command.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("SessionEnd adapter exit = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	var response claudeLiveHookResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil || response.HookSpecificOutput.HookEventName != "SessionEnd" {
		t.Fatalf("SessionEnd success response invalid: err=%v response=%+v stdout=%s", err, response, stdout.String())
	}

	store, err := eventstore.Open(compoundHome)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := store.Scan(eventstore.ScanOptions{Prefixes: []string{"sessend_"}, RegularOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var attempt sessionEndAdapterAttemptEvidence
	var outcome sessionEndAdapterOutcomeEvidence
	for _, entry := range entries {
		record, err := entry.Read()
		if err != nil {
			t.Fatal(err)
		}
		var header struct {
			SchemaVersion string `json:"schema_version"`
		}
		if err := json.Unmarshal(record.Bytes, &header); err != nil {
			t.Fatal(err)
		}
		switch header.SchemaVersion {
		case sessionEndAdapterAttemptSchema:
			_ = json.Unmarshal(record.Bytes, &attempt)
		case sessionEndAdapterOutcomeEvidenceSchema:
			_ = json.Unmarshal(record.Bytes, &outcome)
		}
	}
	if attempt.Status != "accepted" || attempt.InputRef == "" {
		t.Fatalf("successful SessionEnd acceptance missing: %+v", attempt)
	}
	if outcome.Status != "succeeded" || outcome.AttemptID != attempt.ID || outcome.InputRef != attempt.InputRef {
		t.Fatalf("successful SessionEnd outcome = %+v, acceptance=%+v", outcome, attempt)
	}
}
