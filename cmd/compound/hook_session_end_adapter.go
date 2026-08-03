package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/eventstore"
	"compound/internal/home"
)

const (
	sessionEndAdapterAttemptSchema       = "compound.session_end_adapter_attempt.v0.1"
	sessionEndAdapterOutcomeSchema       = "compound.session_end_adapter_outcome.v0.1"
	sessionEndAdapterStatusAccepted      = "accepted"
	sessionEndAdapterStatusSucceeded     = "succeeded"
	sessionEndAdapterStatusTimedOut      = "timed_out"
	sessionEndAdapterStatusFailed        = "failed"
	sessionEndAdapterDefaultTimeout      = 2 * time.Second
	sessionEndAdapterMaxTimeout          = 5 * time.Second
	sessionEndAdapterAcceptanceTimeout   = time.Second
	sessionEndAdapterMaxInputBytes       = 1 << 20
	sessionEndAdapterMaxChildOutputBytes = 256 << 10
	sessionEndAdapterWorkerOverrideEnv   = "COMPOUND_SESSION_END_ADAPTER_ALLOW_WORKER_OVERRIDE"
	sessionEndAdapterUsage               = "usage: compound hook session-end-adapter --live --harness claude-code [--timeout 2s]"
)

type sessionEndAdapterArgs struct {
	Harness string
	Timeout time.Duration
	Worker  string
}

type sessionEndAdapterInput struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	WorkID         string `json:"work_id"`
	CompoundWorkID string `json:"compound_work_id"`
}

type sessionEndAdapterAttempt struct {
	envelope.Header
	Harness   string   `json:"harness"`
	SessionID string   `json:"session_id,omitempty"`
	WorkID    string   `json:"work_id,omitempty"`
	InputHash string   `json:"input_hash"`
	InputRef  string   `json:"input_ref"`
	RetryArgv []string `json:"retry_argv"`
	TimeoutMS int64    `json:"timeout_ms"`
	Status    string   `json:"status"`
}

type sessionEndAdapterAcceptanceResult struct {
	Attempt sessionEndAdapterAttempt
	Err     error
}

type sessionEndAdapterOutcome struct {
	envelope.Header
	AttemptID  string `json:"attempt_id"`
	InputRef   string `json:"input_ref"`
	Status     string `json:"status"`
	ErrorClass string `json:"error_class,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

type sessionEndAdapterOutcomeResult struct {
	Outcome sessionEndAdapterOutcome
	Err     error
}

type sessionEndAdapterWorkerResult struct {
	Stdout   []byte
	Stderr   []byte
	Err      error
	TimedOut bool
	Duration time.Duration
}

func runSessionEndAdapterCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	parsed, err := parseSessionEndAdapterArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	input, err := readSessionEndAdapterInput(stdin)
	if err != nil {
		writeSessionEndAdapterResponse(stdout)
		fmt.Fprintln(stderr, "session-end adapter: invalid live input:", err)
		return 0
	}
	var native sessionEndAdapterInput
	if err := json.Unmarshal(input, &native); err != nil {
		writeSessionEndAdapterResponse(stdout)
		fmt.Fprintln(stderr, "session-end adapter: parse live input:", err)
		return 0
	}
	if strings.TrimSpace(native.HookEventName) != "SessionEnd" {
		writeSessionEndAdapterResponse(stdout)
		fmt.Fprintf(stderr, "session-end adapter: hook_event_name=%q, want SessionEnd\n", native.HookEventName)
		return 0
	}
	if strings.TrimSpace(native.SessionID) == "" && strings.TrimSpace(native.WorkID) == "" && strings.TrimSpace(native.CompoundWorkID) == "" {
		writeSessionEndAdapterResponse(stdout)
		fmt.Fprintln(stderr, "session-end adapter: live input needs session_id, work_id, or compound_work_id")
		return 0
	}

	homePath, err := home.Resolve("")
	if err != nil {
		writeSessionEndAdapterResponse(stdout)
		fmt.Fprintln(stderr, "session-end adapter: resolve Compound home:", err)
		return 0
	}
	now := time.Now()
	inputHash := sessionEndAdapterInputHash(input)
	acceptance := persistSessionEndAdapterAcceptanceBounded(
		homePath,
		parsed,
		native,
		input,
		inputHash,
		now,
		sessionEndAdapterAcceptanceTimeout,
	)
	if acceptance.Err != nil {
		writeSessionEndAdapterResponse(stdout)
		fmt.Fprintf(
			stderr,
			"session-end adapter: acceptance persistence %s; worker not started; input_hash=%s\n",
			acceptance.Err,
			inputHash,
		)
		return 0
	}

	worker := runSessionEndAdapterWorker(parsed.Worker, parsed.Harness, input, parsed.Timeout)
	if len(worker.Stderr) > 0 {
		_, _ = stderr.Write(worker.Stderr)
		if worker.Stderr[len(worker.Stderr)-1] != '\n' {
			_, _ = io.WriteString(stderr, "\n")
		}
	}
	if worker.TimedOut {
		outcome := persistSessionEndAdapterOutcomeBounded(
			homePath,
			acceptance.Attempt,
			sessionEndAdapterStatusTimedOut,
			"deadline_exceeded",
			worker.Duration,
			time.Now(),
			sessionEndAdapterAcceptanceTimeout,
		)
		writeSessionEndAdapterResponse(stdout)
		fmt.Fprintf(
			stderr,
			"session-end adapter: worker timed out after %s; retry evidence=%s input_ref=%s retry=%q\n",
			parsed.Timeout,
			acceptance.Attempt.ID,
			acceptance.Attempt.InputRef,
			strings.Join(acceptance.Attempt.RetryArgv, " "),
		)
		writeSessionEndAdapterOutcomeError(stderr, outcome, acceptance.Attempt)
		return 0
	}
	if worker.Err != nil {
		outcome := persistSessionEndAdapterOutcomeBounded(
			homePath,
			acceptance.Attempt,
			sessionEndAdapterStatusFailed,
			"worker_exit",
			worker.Duration,
			time.Now(),
			sessionEndAdapterAcceptanceTimeout,
		)
		writeSessionEndAdapterResponse(stdout)
		fmt.Fprintf(
			stderr,
			"session-end adapter: worker failed: %v; retry evidence=%s input_ref=%s retry=%q\n",
			worker.Err,
			acceptance.Attempt.ID,
			acceptance.Attempt.InputRef,
			strings.Join(acceptance.Attempt.RetryArgv, " "),
		)
		writeSessionEndAdapterOutcomeError(stderr, outcome, acceptance.Attempt)
		return 0
	}
	var response claudeLiveHookResponse
	if err := json.Unmarshal(worker.Stdout, &response); err != nil || response.HookSpecificOutput.HookEventName != "SessionEnd" {
		outcome := persistSessionEndAdapterOutcomeBounded(
			homePath,
			acceptance.Attempt,
			sessionEndAdapterStatusFailed,
			"invalid_worker_output",
			worker.Duration,
			time.Now(),
			sessionEndAdapterAcceptanceTimeout,
		)
		writeSessionEndAdapterResponse(stdout)
		fmt.Fprintf(
			stderr,
			"session-end adapter: worker emitted invalid hook JSON; retry evidence=%s input_ref=%s retry=%q\n",
			acceptance.Attempt.ID,
			acceptance.Attempt.InputRef,
			strings.Join(acceptance.Attempt.RetryArgv, " "),
		)
		writeSessionEndAdapterOutcomeError(stderr, outcome, acceptance.Attempt)
		return 0
	}
	outcome := persistSessionEndAdapterOutcomeBounded(
		homePath,
		acceptance.Attempt,
		sessionEndAdapterStatusSucceeded,
		"",
		worker.Duration,
		time.Now(),
		sessionEndAdapterAcceptanceTimeout,
	)
	if outcome.Err != nil {
		writeSessionEndAdapterResponse(stdout)
		writeSessionEndAdapterOutcomeError(stderr, outcome, acceptance.Attempt)
		return 0
	}
	_, _ = stdout.Write(worker.Stdout)
	if len(worker.Stdout) == 0 || worker.Stdout[len(worker.Stdout)-1] != '\n' {
		_, _ = io.WriteString(stdout, "\n")
	}
	return 0
}

func persistSessionEndAdapterOutcomeBounded(
	homePath string,
	attempt sessionEndAdapterAttempt,
	status string,
	errorClass string,
	duration time.Duration,
	now time.Time,
	timeout time.Duration,
) sessionEndAdapterOutcomeResult {
	result := make(chan sessionEndAdapterOutcomeResult, 1)
	go func() {
		outcome, err := persistSessionEndAdapterOutcome(homePath, attempt, status, errorClass, duration, now)
		result <- sessionEndAdapterOutcomeResult{Outcome: outcome, Err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case terminal := <-result:
		return terminal
	case <-timer.C:
		return sessionEndAdapterOutcomeResult{Err: fmt.Errorf("timed out after %s", timeout)}
	}
}

func persistSessionEndAdapterOutcome(
	homePath string,
	attempt sessionEndAdapterAttempt,
	status string,
	errorClass string,
	duration time.Duration,
	now time.Time,
) (sessionEndAdapterOutcome, error) {
	switch status {
	case sessionEndAdapterStatusSucceeded, sessionEndAdapterStatusTimedOut, sessionEndAdapterStatusFailed:
	default:
		return sessionEndAdapterOutcome{}, fmt.Errorf("unknown SessionEnd adapter outcome %q", status)
	}
	events, err := eventstore.Open(homePath)
	if err != nil {
		return sessionEndAdapterOutcome{}, err
	}
	outcome := sessionEndAdapterOutcome{
		Header: envelope.Header{
			SchemaVersion: sessionEndAdapterOutcomeSchema,
			ID:            "sessend_outcome_" + shortHash(attempt.ID),
			CreatedAt:     now.UTC().Format(time.RFC3339Nano),
			Producer:      envelope.Producer{Kind: "hook_adapter", Name: "compound"},
			IDs:           attempt.IDs,
			Authority:     envelope.Authority{SourceClass: "evidence", Owner: "compound", Status: "current"},
			Privacy:       "private",
			Risk:          "tier0",
			EvidenceRefs:  []string{"compound://events/" + attempt.ID, attempt.InputRef},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		AttemptID:  attempt.ID,
		InputRef:   attempt.InputRef,
		Status:     status,
		ErrorClass: errorClass,
		DurationMS: duration.Milliseconds(),
	}
	if _, err := events.PutJSON(outcome.ID, outcome, "  "); err != nil {
		return sessionEndAdapterOutcome{}, err
	}
	return outcome, nil
}

func writeSessionEndAdapterOutcomeError(stderr io.Writer, outcome sessionEndAdapterOutcomeResult, attempt sessionEndAdapterAttempt) {
	if outcome.Err == nil {
		return
	}
	fmt.Fprintf(
		stderr,
		"session-end adapter: terminal outcome persistence failed: %v; acceptance %s remains pending with input_ref=%s\n",
		outcome.Err,
		attempt.ID,
		attempt.InputRef,
	)
}

func parseSessionEndAdapterArgs(args []string) (sessionEndAdapterArgs, error) {
	parsed := sessionEndAdapterArgs{Timeout: sessionEndAdapterDefaultTimeout}
	live := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--live":
			live = true
		case "--harness":
			if i+1 >= len(args) {
				return sessionEndAdapterArgs{}, fmt.Errorf(sessionEndAdapterUsage)
			}
			i++
			parsed.Harness = strings.TrimSpace(args[i])
		case "--timeout":
			if i+1 >= len(args) {
				return sessionEndAdapterArgs{}, fmt.Errorf(sessionEndAdapterUsage)
			}
			i++
			timeout, err := time.ParseDuration(strings.TrimSpace(args[i]))
			if err != nil || timeout < 10*time.Millisecond || timeout > sessionEndAdapterMaxTimeout {
				return sessionEndAdapterArgs{}, fmt.Errorf("session-end adapter: --timeout must be between 10ms and %s", sessionEndAdapterMaxTimeout)
			}
			parsed.Timeout = timeout
		case "--worker":
			if i+1 >= len(args) {
				return sessionEndAdapterArgs{}, fmt.Errorf(sessionEndAdapterUsage)
			}
			i++
			parsed.Worker = strings.TrimSpace(args[i])
		default:
			return sessionEndAdapterArgs{}, fmt.Errorf(sessionEndAdapterUsage)
		}
	}
	if !live || parsed.Harness != "claude-code" {
		return sessionEndAdapterArgs{}, fmt.Errorf(sessionEndAdapterUsage)
	}
	if parsed.Worker == "" {
		worker, err := os.Executable()
		if err != nil {
			return sessionEndAdapterArgs{}, fmt.Errorf("session-end adapter: resolve worker executable: %w", err)
		}
		parsed.Worker = worker
		return parsed, nil
	}
	if os.Getenv(sessionEndAdapterWorkerOverrideEnv) != "1" {
		return sessionEndAdapterArgs{}, fmt.Errorf("session-end adapter: --worker is disabled outside an explicit test override")
	}
	if !sessionEndAdapterRunningUnderTest() {
		return sessionEndAdapterArgs{}, fmt.Errorf("session-end adapter: --worker is available only to a test binary")
	}
	if err := validateSessionEndAdapterWorker(parsed.Worker); err != nil {
		return sessionEndAdapterArgs{}, err
	}
	return parsed, nil
}

func validateSessionEndAdapterWorker(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("session-end adapter: --worker must be an absolute path")
	}
	rel, err := filepath.Rel(os.TempDir(), filepath.Clean(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("session-end adapter: --worker must live under the test temporary directory")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("session-end adapter: inspect --worker: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("session-end adapter: --worker must be a regular, non-symlink executable")
	}
	return nil
}

func sessionEndAdapterRunningUnderTest() bool {
	executable, err := os.Executable()
	if err != nil {
		return false
	}
	normalized := filepath.ToSlash(executable)
	return strings.HasSuffix(executable, ".test") || strings.Contains(normalized, "/go-build")
}

func readSessionEndAdapterInput(stdin io.Reader) ([]byte, error) {
	input, err := io.ReadAll(io.LimitReader(stdin, sessionEndAdapterMaxInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(input) == 0 {
		return nil, fmt.Errorf("empty stdin")
	}
	if len(input) > sessionEndAdapterMaxInputBytes {
		return nil, fmt.Errorf("stdin exceeds %d bytes", sessionEndAdapterMaxInputBytes)
	}
	return input, nil
}

func persistSessionEndAdapterAcceptanceBounded(
	homePath string,
	parsed sessionEndAdapterArgs,
	native sessionEndAdapterInput,
	input []byte,
	inputHash string,
	now time.Time,
	timeout time.Duration,
) sessionEndAdapterAcceptanceResult {
	result := make(chan sessionEndAdapterAcceptanceResult, 1)
	go func() {
		attempt, err := persistSessionEndAdapterAcceptance(homePath, parsed, native, input, inputHash, now)
		result <- sessionEndAdapterAcceptanceResult{Attempt: attempt, Err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case accepted := <-result:
		return accepted
	case <-timer.C:
		return sessionEndAdapterAcceptanceResult{Err: fmt.Errorf("timed out after %s", timeout)}
	}
}

func persistSessionEndAdapterAcceptance(
	homePath string,
	parsed sessionEndAdapterArgs,
	native sessionEndAdapterInput,
	input []byte,
	inputHash string,
	now time.Time,
) (sessionEndAdapterAttempt, error) {
	events, err := eventstore.Open(homePath)
	if err != nil {
		return sessionEndAdapterAttempt{}, err
	}
	if err := events.Preflight(); err != nil {
		return sessionEndAdapterAttempt{}, err
	}
	artifacts, err := home.NewArtifactStore(homePath)
	if err != nil {
		return sessionEndAdapterAttempt{}, err
	}
	inputRef, err := artifacts.Put(input)
	if err != nil {
		return sessionEndAdapterAttempt{}, err
	}
	workID := strings.TrimSpace(native.WorkID)
	if workID == "" {
		workID = strings.TrimSpace(native.CompoundWorkID)
	}
	attempt := sessionEndAdapterAttempt{
		Header: envelope.Header{
			SchemaVersion: sessionEndAdapterAttemptSchema,
			ID:            "sessend_" + shortHash(parsed.Harness, inputHash, now.UTC().Format(time.RFC3339Nano)),
			CreatedAt:     now.UTC().Format(time.RFC3339Nano),
			Producer:      envelope.Producer{Kind: "hook_adapter", Name: "compound"},
			IDs:           envelope.IDs{WorkID: workID, SessionID: strings.TrimSpace(native.SessionID)},
			Authority:     envelope.Authority{SourceClass: "evidence", Owner: "compound", Status: "current"},
			Privacy:       "private",
			Risk:          "tier0",
			EvidenceRefs:  []string{inputRef},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Harness:   parsed.Harness,
		SessionID: strings.TrimSpace(native.SessionID),
		WorkID:    workID,
		InputHash: inputHash,
		InputRef:  inputRef,
		RetryArgv: []string{"compound", "hook", "session-end", "--live", "--harness", parsed.Harness},
		TimeoutMS: parsed.Timeout.Milliseconds(),
		Status:    sessionEndAdapterStatusAccepted,
	}
	if _, err := events.PutJSON(attempt.ID, attempt, "  "); err != nil {
		return sessionEndAdapterAttempt{}, err
	}
	return attempt, nil
}

func runSessionEndAdapterWorker(workerPath, harnessName string, input []byte, timeout time.Duration) sessionEndAdapterWorkerResult {
	started := time.Now()
	command := exec.Command(workerPath, "hook", "session-end", "--live", "--harness", harnessName)
	setSessionEndAdapterProcessGroup(command)
	command.Stdin = bytes.NewReader(input)
	stdout := newSessionEndAdapterCapture(sessionEndAdapterMaxChildOutputBytes)
	stderr := newSessionEndAdapterCapture(sessionEndAdapterMaxChildOutputBytes)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return sessionEndAdapterWorkerResult{Err: err, Duration: time.Since(started)}
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-waited:
		return sessionEndAdapterWorkerResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Err: err, Duration: time.Since(started)}
	case <-timer.C:
		killSessionEndAdapterProcessGroup(command)
		err := <-waited
		return sessionEndAdapterWorkerResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Err: err, TimedOut: true, Duration: time.Since(started)}
	}
}

type sessionEndAdapterCapture struct {
	buf       bytes.Buffer
	remaining int
}

func newSessionEndAdapterCapture(limit int) *sessionEndAdapterCapture {
	return &sessionEndAdapterCapture{remaining: limit}
}

func (w *sessionEndAdapterCapture) Write(p []byte) (int, error) {
	written := len(p)
	if w.remaining <= 0 {
		return written, nil
	}
	keep := len(p)
	if keep > w.remaining {
		keep = w.remaining
	}
	_, _ = w.buf.Write(p[:keep])
	w.remaining -= keep
	return written, nil
}

func (w *sessionEndAdapterCapture) Bytes() []byte {
	return append([]byte(nil), w.buf.Bytes()...)
}

func writeSessionEndAdapterResponse(stdout io.Writer) {
	_ = json.NewEncoder(stdout).Encode(claudeLiveHookResponse{
		HookSpecificOutput: claudeHookSpecificOutput{HookEventName: "SessionEnd"},
	})
}

func sessionEndAdapterInputHash(input []byte) string {
	sum := sha256.Sum256(input)
	return "sha256:" + hex.EncodeToString(sum[:])
}
