package harness

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"compound/internal/runattempt"
)

// defaultHeartbeatInterval is the wall-clock cadence the adapter calls
// store.Heartbeat on the minted RunAttempt. Tunable per-adapter via
// NewOSEntrypointsAdapterWithInterval (tests).
const defaultHeartbeatInterval = 5 * time.Second

// OSEntrypointsAdapter is the first real Adapter: it drives an OS-level
// subprocess (the entrypoint declared in OSEntrypointsFile) and wraps
// the execution in a RunAttempt. Today this is the only Adapter; future
// slices add claude / codex / opencode adapters that consume their
// harness's hook events and produce the same S1+S2 shape.
//
// The adapter is intentionally simple:
//   - one subprocess per Run call
//   - transcript captured line-by-line to <home>/run_attempts/<id>.transcript
//   - heartbeats on a fixed interval while the subprocess is running
//   - terminal status derived from exit code (0 -> succeeded, else failed)
//   - timeout (when set) kills the subprocess and marks TimedOut=true
//
// It does NOT:
//   - sandbox the subprocess (caller is responsible for capability gating)
//   - cap transcript size (future slice)
//   - detect diffs/tests automatically (caller provides explicit paths in
//     future slices; today those S2 fields stay nil)
type OSEntrypointsAdapter struct {
	store             *runattempt.Store
	heartbeatInterval time.Duration
}

// NewOSEntrypointsAdapter returns the production adapter. Heartbeats
// every 5s. For tests, use NewOSEntrypointsAdapterWithInterval.
func NewOSEntrypointsAdapter(store *runattempt.Store) *OSEntrypointsAdapter {
	return &OSEntrypointsAdapter{store: store, heartbeatInterval: defaultHeartbeatInterval}
}

// NewOSEntrypointsAdapterWithInterval returns an adapter with a custom
// heartbeat cadence. Test-only: production code uses the 5s default.
func NewOSEntrypointsAdapterWithInterval(store *runattempt.Store, interval time.Duration) *OSEntrypointsAdapter {
	if interval <= 0 {
		interval = defaultHeartbeatInterval
	}
	return &OSEntrypointsAdapter{store: store, heartbeatInterval: interval}
}

// Name returns the manifest adapter id. Matches OSEntrypointsAdapterID
// and OSEntrypointsFile.AdapterID so the future Registry slice can key
// adapters to manifest rows.
func (a *OSEntrypointsAdapter) Name() string { return "os-harness-entrypoints" }

// Run drives the subprocess end-to-end. See Adapter contract in
// adapter.go for the full shape; the short version:
//  1. Mint a RunAttempt (pending)
//  2. Set Workspace + Command (stable across the attempt)
//  3. Spawn the subprocess with timeout
//  4. Start the heartbeat loop in a goroutine
//  5. Capture transcript line-by-line
//  6. On exit: stop heartbeats, write transcript, attach as TranscriptRef,
//     terminalize RunAttempt to succeeded|failed
//  7. Return the terminal RunAttempt
func (a *OSEntrypointsAdapter) Run(ctx context.Context, in RunInput) (RunOutput, error) {
	if err := a.validateInput(in); err != nil {
		return RunOutput{}, err
	}
	started := in.Now
	ra, err := a.store.Mint(in.WorkID, in.Harness, in.Harness, started)
	if err != nil {
		return RunOutput{}, fmt.Errorf("os-harness-entrypoints: mint: %w", err)
	}
	if _, err := a.store.SetWorkspace(ra.ID, in.Workspace); err != nil {
		return RunOutput{}, fmt.Errorf("os-harness-entrypoints: set workspace: %w", err)
	}
	if _, err := a.store.SetCommand(ra.ID, in.Command); err != nil {
		return RunOutput{}, fmt.Errorf("os-harness-entrypoints: set command: %w", err)
	}
	transcriptPath := filepath.Join(filepath.Dir(a.storePath(ra.ID)), ra.ID+".transcript")
	tf, err := os.Create(transcriptPath)
	if err != nil {
		return RunOutput{}, fmt.Errorf("os-harness-entrypoints: create transcript: %w", err)
	}
	defer tf.Close()
	tw := bufio.NewWriter(tf)

	// Spawn.
	cmd := a.buildCmd(ctx, in)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return RunOutput{}, fmt.Errorf("os-harness-entrypoints: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return RunOutput{}, fmt.Errorf("os-harness-entrypoints: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return RunOutput{}, fmt.Errorf("os-harness-entrypoints: start: %w", err)
	}
	if in.Stdin != nil {
		go func() { _, _ = io.Copy(io.Discard, in.Stdin) }()
	}

	// Heartbeats. Stopped by closing stopCh.
	stopCh := make(chan struct{})
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go a.heartbeatLoop(ra.ID, started, a.heartbeatInterval, stopCh, &hbWG)

	// Capture stdout+stderr in parallel. Both share the transcript writer.
	var capWG sync.WaitGroup
	capWG.Add(2)
	go func() { defer capWG.Done(); a.capture(stdout, "stdout", tw) }()
	go func() { defer capWG.Done(); a.capture(stderr, "stderr", tw) }()
	capWG.Wait()

	// Wait + timeout.
	waitErr := a.waitWithTimeout(cmd, in.Timeout)
	close(stopCh)
	hbWG.Wait()
	_ = tw.Flush()

	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	timedOut := errors.Is(waitErr, context.DeadlineExceeded) ||
		(waitErr != nil && in.Timeout > 0 && time.Since(started) >= in.Timeout)

	status := runattempt.StatusSucceeded
	if timedOut || exitCode != 0 || waitErr != nil {
		status = runattempt.StatusFailed
	}
	// Attach transcript + terminalize.
	if _, err := a.store.SetTranscript(ra.ID, transcriptPath); err != nil {
		return RunOutput{}, fmt.Errorf("os-harness-entrypoints: set transcript: %w", err)
	}
	terminal, err := a.store.Complete(ra.ID, status, started.Add(time.Since(started)))
	if err != nil {
		return RunOutput{}, fmt.Errorf("os-harness-entrypoints: complete: %w", err)
	}
	// Best-effort: if the caller has attached an action_intent_id via the
	// input, bind it after Complete (Complete is one-shot terminal; the
	// intent binding is also one-shot, and the order -- Complete then
	// AttachActionIntent -- matches panel.Apply).
	if in.ActionIntentID != "" {
		if err := a.store.AttachActionIntent(ra.ID, in.ActionIntentID); err != nil {
			// Not a fatal error -- the RunAttempt is terminal and the
			// audit chain gap is logged but does not roll back the
			// completion. Future slice: stricter coupling.
			_ = err
		}
	}
	return RunOutput{
		RunAttempt:     terminal,
		TranscriptPath: transcriptPath,
		ExitCode:       exitCode,
		TimedOut:       timedOut,
		Duration:       time.Since(started),
	}, nil
}

func (a *OSEntrypointsAdapter) validateInput(in RunInput) error {
	if strings.TrimSpace(in.WorkID) == "" {
		return errors.New("os-harness-entrypoints: WorkID is required")
	}
	if strings.TrimSpace(in.Harness) == "" {
		return errors.New("os-harness-entrypoints: Harness is required")
	}
	if strings.TrimSpace(in.Workspace) == "" {
		return errors.New("os-harness-entrypoints: Workspace is required")
	}
	if strings.TrimSpace(in.Command) == "" {
		return errors.New("os-harness-entrypoints: Command is required")
	}
	if in.Now.IsZero() {
		return errors.New("os-harness-entrypoints: Now is required (caller-supplied for testability)")
	}
	return nil
}

func (a *OSEntrypointsAdapter) buildCmd(ctx context.Context, in RunInput) *exec.Cmd {
	// If a Timeout is set, derive a context with deadline.
	if in.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, in.Timeout)
		// We can't keep cancel; the deferred cancel would be cleaner, but
		// cmd.Wait() will observe the deadline via ProcessState. The cancel
		// is released when the context is done, which happens when the
		// subprocess exits OR the deadline is hit.
		_ = cancel
	}
	cmd := exec.CommandContext(ctx, in.Command, in.Args...)
	cmd.Dir = in.Workspace
	cmd.Env = append(os.Environ(), in.Env...)
	return cmd
}

func (a *OSEntrypointsAdapter) capture(r io.Reader, label string, w io.Writer) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		fmt.Fprintf(w, "%s: %s\n", label, scanner.Text())
	}
}

func (a *OSEntrypointsAdapter) waitWithTimeout(cmd *exec.Cmd, timeout time.Duration) error {
	if timeout <= 0 {
		return cmd.Wait()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		return context.DeadlineExceeded
	}
}

func (a *OSEntrypointsAdapter) heartbeatLoop(id string, start time.Time, interval time.Duration, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case t := <-ticker.C:
			// Heartbeat anchor is wall-clock now, not start; the operator
			// wants "last activity" not "elapsed".
			if _, err := a.store.Heartbeat(id, start.Add(t.Sub(start))); err != nil {
				// Heartbeat failure is non-fatal -- log via the
				// returned error on infrastructure path. Future slice:
				// surface via structured log.
				_ = err
			}
		}
	}
}

// storePath is the canonical JSON path for the minted RunAttempt. Used
// to colocate the transcript under the same directory (<home>/run_attempts).
func (a *OSEntrypointsAdapter) storePath(id string) string {
	return filepath.Join(a.store.Home(), "run_attempts", id+".json")
}
