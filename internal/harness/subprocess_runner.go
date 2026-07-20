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

// buildCmdFn is the harness-specific argv constructor. Each adapter
// supplies its own -- the OS-entrypoints adapter passes Command+Args
// through verbatim; the Claude adapter assembles `claude --print ...`
// flags; the Codex adapter assembles `codex exec --prompt ...`. The
// shared runner does not know or care which.
type buildCmdFn func(context.Context, RunInput) *exec.Cmd

// runWithSubprocess is the shared subprocess execution core used by
// every harness adapter (os-entrypoints, claude, codex). It takes a
// harnessName (for the RunAttempt.Harness field and error-message
// prefix), a buildCmdFn (for the harness-specific argv), and the
// pre-validated RunInput.
//
// The contract this function implements is the heart of S1+S2+C.1: mint
// before side effect, populate S2 fields as data emerges, terminalize on
// every return path. Each adapter's Run() method is a thin wrapper that
// supplies the right harness name and buildCmd.
//
// All RunOutput fields are populated even on infrastructure failure
// (TranscriptPath, ExitCode) so callers have a complete audit trail.
func runWithSubprocess(
	ctx context.Context,
	store *runattempt.Store,
	harnessName string,
	in RunInput,
	build buildCmdFn,
	heartbeatInterval time.Duration,
) (RunOutput, error) {
	if err := validateSubprocessInput(harnessName, in); err != nil {
		return RunOutput{}, err
	}
	started := in.Now
	ra, err := store.Mint(in.WorkID, harnessName, harnessName, started)
	if err != nil {
		return RunOutput{}, fmt.Errorf("%s: mint: %w", harnessName, err)
	}
	if _, err := store.SetWorkspace(ra.ID, in.Workspace); err != nil {
		return RunOutput{}, fmt.Errorf("%s: set workspace: %w", harnessName, err)
	}
	if _, err := store.SetCommand(ra.ID, in.Command); err != nil {
		return RunOutput{}, fmt.Errorf("%s: set command: %w", harnessName, err)
	}
	transcriptPath := filepath.Join(filepath.Dir(storePath(store, ra)), ra.ID+".transcript")
	tf, err := os.Create(transcriptPath)
	if err != nil {
		return RunOutput{}, fmt.Errorf("%s: create transcript: %w", harnessName, err)
	}
	defer tf.Close()
	tw := bufio.NewWriter(tf)

	cmd := build(ctx, in)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return RunOutput{}, fmt.Errorf("%s: stdout pipe: %w", harnessName, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return RunOutput{}, fmt.Errorf("%s: stderr pipe: %w", harnessName, err)
	}
	if err := cmd.Start(); err != nil {
		return RunOutput{}, fmt.Errorf("%s: start: %w", harnessName, err)
	}
	if in.Stdin != nil {
		go func() { _, _ = io.Copy(io.Discard, in.Stdin) }()
	}

	stopCh := make(chan struct{})
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go heartbeatLoop(store, ra.ID, started, heartbeatInterval, stopCh, &hbWG)

	var capWG sync.WaitGroup
	capWG.Add(2)
	go func() { defer capWG.Done(); capture(stdout, "stdout", tw) }()
	go func() { defer capWG.Done(); capture(stderr, "stderr", tw) }()
	capWG.Wait()

	waitErr := waitWithTimeout(cmd, in.Timeout)
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
	if _, err := store.SetTranscript(ra.ID, transcriptPath); err != nil {
		return RunOutput{}, fmt.Errorf("%s: set transcript: %w", harnessName, err)
	}
	terminal, err := store.Complete(ra.ID, status, started.Add(time.Since(started)))
	if err != nil {
		return RunOutput{}, fmt.Errorf("%s: complete: %w", harnessName, err)
	}
	if in.ActionIntentID != "" {
		// Best-effort: action_intent_id binding is non-fatal. The RunAttempt
		// is terminal; an unattached intent is a logging gap, not a
		// rollback. Future slice: stricter coupling with explicit error
		// propagation.
		_ = store.AttachActionIntent(ra.ID, in.ActionIntentID)
	}
	return RunOutput{
		RunAttempt:     terminal,
		TranscriptPath: transcriptPath,
		ExitCode:       exitCode,
		TimedOut:       timedOut,
		Duration:       time.Since(started),
	}, nil
}

// validateSubprocessInput enforces the input contract shared across all
// subprocess adapters. Harness-specific adapters may add their own
// checks (e.g. Claude requires a Prompt) on top.
func validateSubprocessInput(harnessName string, in RunInput) error {
	if strings.TrimSpace(in.WorkID) == "" {
		return errors.New(harnessName + ": WorkID is required")
	}
	if strings.TrimSpace(harnessName) == "" {
		return errors.New("subprocess: harnessName is required")
	}
	if strings.TrimSpace(in.Workspace) == "" {
		return errors.New(harnessName + ": Workspace is required")
	}
	if strings.TrimSpace(in.Command) == "" {
		return errors.New(harnessName + ": Command is required")
	}
	if in.Now.IsZero() {
		return errors.New(harnessName + ": Now is required (caller-supplied for testability)")
	}
	return nil
}

func capture(r io.Reader, label string, w io.Writer) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		fmt.Fprintf(w, "%s: %s\n", label, scanner.Text())
	}
}

func waitWithTimeout(cmd *exec.Cmd, timeout time.Duration) error {
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

func heartbeatLoop(store *runattempt.Store, id string, start time.Time, interval time.Duration, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case t := <-ticker.C:
			if _, err := store.Heartbeat(id, start.Add(t.Sub(start))); err != nil {
				_ = err
			}
		}
	}
}

// storePath is the canonical JSON path for the minted RunAttempt. Used
// to colocate the transcript under the same directory.
func storePath(store *runattempt.Store, ra runattempt.RunAttempt) string {
	return filepath.Join(store.Home(), "run_attempts", ra.ID+".json")
}
