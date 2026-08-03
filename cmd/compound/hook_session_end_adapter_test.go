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
	ID            string `json:"id"`
	AttemptID     string `json:"attempt_id"`
	InputRef      string `json:"input_ref"`
	Status        string `json:"status"`
}

func requireSessionEndAdapterDiagnosticValue(t *testing.T, output, marker string) string {
	t.Helper()
	index := strings.Index(output, marker)
	if index < 0 {
		t.Fatalf("SessionEnd adapter diagnostic omitted %q: %s", marker, output)
	}
	fields := strings.Fields(output[index+len(marker):])
	if len(fields) == 0 {
		t.Fatalf("SessionEnd adapter diagnostic has no value after %q: %s", marker, output)
	}
	return strings.Trim(fields[0], `"`)
}

func TestSessionEndAdapterPostKillReapWaitIsBounded(t *testing.T) {
	waited := make(chan error)
	started := time.Now()
	_, reaped := waitSessionEndAdapterReap(waited, 20*time.Millisecond)
	elapsed := time.Since(started)
	if reaped {
		t.Fatal("a worker that never reports Wait completion was reported as reaped")
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("post-kill reap wait was not bounded: %s", elapsed)
	}
}

func TestSessionEndAdapterOutcomeIdentityIncludesExecution(t *testing.T) {
	attemptID := "hook_sessend_attempt_fixed"
	first := sessionEndAdapterOutcomeID(attemptID, "sessend_exec_first")
	second := sessionEndAdapterOutcomeID(attemptID, "sessend_exec_second")
	if first == second {
		t.Fatalf("distinct executions collapsed onto one terminal outcome id: %s", first)
	}
}

func TestSessionEndAdapterCommitAtDeadlineRetainsPendingReplayIdentity(t *testing.T) {
	compoundHome := t.TempDir()
	input := []byte("{\"hook_event_name\":\"SessionEnd\",\"session_id\":\"sess_commit_at_deadline\"}\n")
	parsed := sessionEndAdapterArgs{Harness: "claude-code", Timeout: time.Second, Worker: "/unused"}
	native := sessionEndAdapterInput{HookEventName: "SessionEnd", SessionID: "sess_commit_at_deadline"}
	prepared := prepareSessionEndAdapterAcceptance(parsed, native, input, time.Unix(1_800_000_000, 123).UTC())

	committed := make(chan struct{})
	release := make(chan struct{})
	result := persistSessionEndAdapterAcceptanceBounded(
		compoundHome,
		prepared,
		input,
		500*time.Millisecond,
		func() {
			close(committed)
			<-release
		},
	)
	defer close(release)
	select {
	case <-committed:
	default:
		t.Fatal("deadline fired before the acceptance commit boundary was reached")
	}
	if result.Err == nil || !result.TimedOut {
		t.Fatalf("commit-at-deadline result=%+v, want truthful timeout/unknown state", result)
	}
	if result.Attempt.ID != prepared.ID || result.Attempt.InputRef != prepared.InputRef {
		t.Fatalf("pending identity was lost: result=%+v prepared=%+v", result.Attempt, prepared)
	}
	wantRetry := []string{"compound", "hook", "session-end-adapter", "--replay", prepared.ID}
	if strings.Join(result.Attempt.RetryArgv, "\x00") != strings.Join(wantRetry, "\x00") {
		t.Fatalf("pending retry argv=%q want=%q", result.Attempt.RetryArgv, wantRetry)
	}

	events, err := eventstore.Open(compoundHome)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := events.Read(prepared.ID); err != nil {
		t.Fatalf("committed acceptance is not exact-ID readable after deadline: %v", err)
	}
	artifacts, err := home.NewArtifactStore(compoundHome)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := artifacts.Get(prepared.InputRef)
	if err != nil {
		t.Fatalf("committed exact input is not replayable after deadline: %v", err)
	}
	if !bytes.Equal(stored, input) {
		t.Fatalf("pending input changed: want=%q got=%q", input, stored)
	}
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

func TestSessionEndAdapterRetryArgvReplaysAcceptedExactInputByID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell worker fixture requires a POSIX host")
	}
	t.Setenv(sessionEndAdapterWorkerOverrideEnv, "1")
	compoundHome := t.TempDir()
	t.Setenv("COMPOUND_HOME", compoundHome)

	slowWorker := filepath.Join(t.TempDir(), "slow-session-end-worker.sh")
	if err := os.WriteFile(slowWorker, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	input := "{\n  \"hook_event_name\": \"SessionEnd\",\n  \"session_id\": \"sess_exact_replay\",\n  \"unknown_future_field\": [1, 2, 3]\n}\n"
	var liveStdout, liveStderr bytes.Buffer
	if code := runSessionEndAdapterCommand([]string{
		"--live", "--harness", "claude-code", "--timeout", "1s", "--worker", slowWorker,
	}, strings.NewReader(input), &liveStdout, &liveStderr); code != 0 {
		t.Fatalf("live adapter exit=%d stdout=%s stderr=%s", code, liveStdout.String(), liveStderr.String())
	}

	const evidenceMarker = "retry evidence="
	after, ok := strings.CutPrefix(strings.TrimSpace(liveStderr.String()), "session-end adapter: worker timed out")
	if !ok || !strings.Contains(after, evidenceMarker) {
		t.Fatalf("timeout omitted retry identity: %s", liveStderr.String())
	}
	attemptID := strings.Fields(strings.SplitN(after, evidenceMarker, 2)[1])[0]
	if !strings.HasPrefix(attemptID, "hook_sessend_attempt_") {
		t.Fatalf("attempt id %q is not routed through the hook family", attemptID)
	}

	events, err := eventstore.Open(compoundHome)
	if err != nil {
		t.Fatal(err)
	}
	record, err := events.Read(attemptID)
	if err != nil {
		t.Fatalf("read accepted attempt by exact id: %v", err)
	}
	var attempt sessionEndAdapterAttemptEvidence
	if err := json.Unmarshal(record.Bytes, &attempt); err != nil {
		t.Fatal(err)
	}
	wantRetry := []string{"compound", "hook", "session-end-adapter", "--replay", attemptID}
	if strings.Join(attempt.RetryArgv, "\x00") != strings.Join(wantRetry, "\x00") {
		t.Fatalf("retry argv=%q want=%q", attempt.RetryArgv, wantRetry)
	}

	workerInput := filepath.Join(t.TempDir(), "replayed-input.json")
	successWorker := filepath.Join(t.TempDir(), "successful-session-end-worker.sh")
	worker := fmt.Sprintf("#!/bin/sh\nhead -c %d > %q\nprintf '%%s\\n' '{\"hookSpecificOutput\":{\"hookEventName\":\"SessionEnd\"}}'\n", len(input), workerInput)
	if err := os.WriteFile(successWorker, []byte(worker), 0o755); err != nil {
		t.Fatal(err)
	}
	var replayStdout, replayStderr bytes.Buffer
	replayArgs := append(append([]string(nil), attempt.RetryArgv[3:]...), "--worker", successWorker)
	if code := runSessionEndAdapterCommand(replayArgs, strings.NewReader("must-not-be-consumed"), &replayStdout, &replayStderr); code != 0 {
		partial, partialErr := os.ReadFile(workerInput)
		t.Fatalf("replay exit=%d stdout=%s stderr=%s worker_input_bytes=%d worker_input_err=%v", code, replayStdout.String(), replayStderr.String(), len(partial), partialErr)
	}
	gotInput, err := os.ReadFile(workerInput)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotInput) != input {
		t.Fatalf("replay changed accepted stdin:\nwant=%q\ngot=%q", input, gotInput)
	}
	var outcome sessionEndAdapterOutcomeEvidence
	if err := json.Unmarshal(replayStdout.Bytes(), &outcome); err != nil {
		t.Fatalf("decode replay outcome: %v stdout=%s", err, replayStdout.String())
	}
	if outcome.Status != sessionEndAdapterStatusSucceeded || !strings.HasPrefix(outcome.ID, "hook_sessend_outcome_") {
		t.Fatalf("replay outcome=%+v", outcome)
	}
	if _, err := events.Read(outcome.ID); err != nil {
		t.Fatalf("read replay outcome by exact id: %v", err)
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
	attemptID := requireSessionEndAdapterDiagnosticValue(t, stderr.String(), "retry evidence=")
	outcomeID := requireSessionEndAdapterDiagnosticValue(t, stderr.String(), "terminal=")
	attemptRecord, err := store.Read(attemptID)
	if err != nil {
		t.Fatalf("read timeout acceptance by exact id: %v", err)
	}
	outcomeRecord, err := store.Read(outcomeID)
	if err != nil {
		t.Fatalf("read timeout outcome by exact id: %v", err)
	}
	var attempt sessionEndAdapterAttemptEvidence
	var outcome sessionEndAdapterOutcomeEvidence
	if err := json.Unmarshal(attemptRecord.Bytes, &attempt); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(outcomeRecord.Bytes, &outcome); err != nil {
		t.Fatal(err)
	}
	if attempt.Status != "accepted" || attempt.SessionID != "sess_slow_worker" || attempt.InputRef == "" {
		t.Fatalf("durable SessionEnd handoff = %+v", attempt)
	}
	if outcome.Status != "timed_out" || outcome.AttemptID != attempt.ID || outcome.InputRef != attempt.InputRef {
		t.Fatalf("durable SessionEnd terminal outcome = %+v, acceptance=%+v", outcome, attempt)
	}
	if strings.Join(attempt.RetryArgv, "\x00") != strings.Join([]string{"compound", "hook", "session-end-adapter", "--replay", attempt.ID}, "\x00") {
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
	attemptID := requireSessionEndAdapterDiagnosticValue(t, stderr.String(), "acceptance=")
	outcomeID := requireSessionEndAdapterDiagnosticValue(t, stderr.String(), "terminal=")
	attemptRecord, err := store.Read(attemptID)
	if err != nil {
		t.Fatalf("read successful acceptance by exact id: %v", err)
	}
	outcomeRecord, err := store.Read(outcomeID)
	if err != nil {
		t.Fatalf("read successful outcome by exact id: %v", err)
	}
	var attempt sessionEndAdapterAttemptEvidence
	var outcome sessionEndAdapterOutcomeEvidence
	if err := json.Unmarshal(attemptRecord.Bytes, &attempt); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(outcomeRecord.Bytes, &outcome); err != nil {
		t.Fatal(err)
	}
	if attempt.Status != "accepted" || attempt.InputRef == "" {
		t.Fatalf("successful SessionEnd acceptance missing: %+v", attempt)
	}
	if outcome.Status != "succeeded" || outcome.AttemptID != attempt.ID || outcome.InputRef != attempt.InputRef {
		t.Fatalf("successful SessionEnd outcome = %+v, acceptance=%+v", outcome, attempt)
	}
}
