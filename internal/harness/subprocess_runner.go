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
	"sync/atomic"
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
// Hardening guarantees (pinned by subprocess_runner_test.go):
//   - ActionIntentID binds BEFORE Complete -- AttachActionIntent refuses a
//     terminal attempt, so binding post-Complete was a guaranteed no-op.
//   - RunInput.Stdin is connected to the subprocess (never discarded).
//   - Transcript writes are serialized -- stdout and stderr capture
//     goroutines share one bufio.Writer through a mutex.
//   - Timeout is owned HERE, not delegated to buildCmd: a kill timer
//     forces the subprocess to exit at RunInput.Timeout so capture drains
//     even if a future adapter forgets exec.CommandContext.
//   - Any infrastructure failure AFTER Mint terminalizes the RunAttempt
//     failed (never leaves a pending record) and records the reason.
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

	// transcriptPath is "" until the transcript file is created below; the
	// failTerm closure reads it at call time so the reason is recorded
	// inline when a transcript exists and as a proof_ref otherwise.
	transcriptPath := ""

	// failTerm terminalizes the minted RunAttempt as failed when an
	// infrastructure step blows up after Mint. It best-effort records the
	// reason (appended to the transcript when one exists, else a proof_ref)
	// so the audit shows why the attempt never produced a result, then
	// completes failed. Returns the RunOutput the caller returns alongside
	// its wrapped error -- per the Adapter contract an infra failure
	// returns BOTH the terminal RunAttempt AND an error.
	failTerm := func(reason string) RunOutput {
		if transcriptPath != "" {
			if f, ferr := os.OpenFile(transcriptPath, os.O_WRONLY|os.O_APPEND, 0o644); ferr == nil {
				_, _ = fmt.Fprintf(f, "infra_error: %s\n", reason)
				_ = f.Close()
				_, _ = store.SetTranscript(ra.ID, transcriptPath)
			}
		} else {
			_, _ = store.SetProofRefs(ra.ID, []string{"infra_error:" + reason})
		}
		if t, cerr := store.Complete(ra.ID, runattempt.StatusFailed, started.Add(time.Since(started))); cerr == nil {
			ra = t
		}
		return RunOutput{RunAttempt: ra, TranscriptPath: transcriptPath, ExitCode: -1}
	}

	if _, err := store.SetWorkspace(ra.ID, in.Workspace); err != nil {
		return failTerm("set workspace"), fmt.Errorf("%s: set workspace: %w", harnessName, err)
	}
	if _, err := store.SetCommand(ra.ID, in.Command); err != nil {
		return failTerm("set command"), fmt.Errorf("%s: set command: %w", harnessName, err)
	}
	// Bind the authorizing ActionIntent BEFORE any side effect and BEFORE
	// terminalization. AttachActionIntent refuses a terminal attempt, so
	// binding after Complete (the old order) was a guaranteed silent no-op
	// that broke the attempt->intent->policy-decision audit chain.
	if in.ActionIntentID != "" {
		if err := store.AttachActionIntent(ra.ID, in.ActionIntentID); err != nil {
			return failTerm("attach action intent"), fmt.Errorf("%s: attach action intent: %w", harnessName, err)
		}
	}

	// Stamp the resolved lane identity onto the attempt pre-terminalization.
	// SetAttribution refuses a terminal attempt, so this MUST run before
	// Complete (a post-Complete stamp is a guaranteed silent no-op -- the
	// same reason AttachActionIntent binds above, not after Complete). A
	// stamp failure is NON-FATAL: lane_id is audit metadata, not a run
	// precondition, so an attribution error is annotated best-effort (same
	// proof_ref idiom as a transcript-flush failure) and the run proceeds
	// and terminalizes regardless. An empty LaneID stamps nothing.
	if in.LaneID != "" {
		if _, err := store.SetAttribution(ra.ID, in.LaneID, "", ""); err != nil {
			_, _ = store.SetProofRefs(ra.ID, []string{"lane_id_stamp_error:" + err.Error()})
		}
	}

	transcriptPath = filepath.Join(filepath.Dir(storePath(store, ra)), ra.ID+".transcript")
	tf, err := os.Create(transcriptPath)
	if err != nil {
		return failTerm("create transcript"), fmt.Errorf("%s: create transcript: %w", harnessName, err)
	}
	defer tf.Close()
	tw := &lockedWriter{w: bufio.NewWriter(tf)}

	cmd := build(ctx, in)
	// Connect stdin to the subprocess. The old code ran
	// io.Copy(io.Discard, in.Stdin) in a goroutine, silently throwing the
	// caller's input away.
	if in.Stdin != nil {
		cmd.Stdin = in.Stdin
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return failTerm("stdout pipe"), fmt.Errorf("%s: stdout pipe: %w", harnessName, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return failTerm("stderr pipe"), fmt.Errorf("%s: stderr pipe: %w", harnessName, err)
	}
	if err := cmd.Start(); err != nil {
		return failTerm("start: " + err.Error()), fmt.Errorf("%s: start: %w", harnessName, err)
	}

	stopCh := make(chan struct{})
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go heartbeatLoop(store, ra.ID, started, heartbeatInterval, stopCh, &hbWG)

	var capWG sync.WaitGroup
	capWG.Add(2)
	go func() { defer capWG.Done(); capture(stdout, "stdout", tw) }()
	go func() { defer capWG.Done(); capture(stderr, "stderr", tw) }()

	// The shared runner owns timeout enforcement so a future adapter that
	// forgets exec.CommandContext cannot hang forever inside capWG.Wait.
	// The kill timer forces the subprocess to exit at RunInput.Timeout,
	// which EOFs the capture pipes, so capWG.Wait below is bounded
	// regardless of how buildCmd constructed the command. The timer also
	// selects on stopCh so a process that exits on its own never gets a
	// spurious kill.
	var timedOut atomic.Bool
	if in.Timeout > 0 {
		go func() {
			t := time.NewTimer(in.Timeout)
			defer t.Stop()
			select {
			case <-t.C:
				timedOut.Store(true)
				_ = cmd.Process.Kill()
			case <-stopCh:
			}
		}()
	}

	// Wait for capture to drain. Bounded: the process either exited on its
	// own or was killed by the timeout goroutine above; either way the
	// stdout/stderr pipes EOF and the capture goroutines return. Doing this
	// BEFORE cmd.Wait honors the exec package's "all reads complete before
	// Wait" contract for StdoutPipe/StderrPipe.
	capWG.Wait()
	close(stopCh) // stops BOTH the heartbeat loop and the timeout timer
	hbWG.Wait()
	waitErr := cmd.Wait()
	if err := tw.Flush(); err != nil {
		// A failed flush loses buffered transcript lines but is not fatal --
		// the run still produced a result. Surface it on the transcript ref
		// so the audit is honest about the partial capture.
		_, _ = store.SetProofRefs(ra.ID, []string{"transcript_flush_error:" + err.Error()})
	}

	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	status := runattempt.StatusSucceeded
	if timedOut.Load() || exitCode != 0 || waitErr != nil {
		status = runattempt.StatusFailed
	}
	if _, err := store.SetTranscript(ra.ID, transcriptPath); err != nil {
		// The run produced a transcript on disk but we could not pin it on
		// the attempt; terminalize failed so the audit is honest.
		return failTerm("set transcript"), fmt.Errorf("%s: set transcript: %w", harnessName, err)
	}
	terminal, err := store.Complete(ra.ID, status, started.Add(time.Since(started)))
	if err != nil {
		return RunOutput{RunAttempt: ra, TranscriptPath: transcriptPath, ExitCode: exitCode, TimedOut: timedOut.Load(), Duration: time.Since(started)},
			fmt.Errorf("%s: complete: %w", harnessName, err)
	}
	return RunOutput{
		RunAttempt:     terminal,
		TranscriptPath: transcriptPath,
		ExitCode:       exitCode,
		TimedOut:       timedOut.Load(),
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

// lockedWriter serializes transcript writes from the concurrent stdout
// and stderr capture goroutines. bufio.Writer is not safe for concurrent
// use, so unsynchronized writes could interleave or corrupt the
// transcript. Pinned by TestRunnerConcurrentTranscriptWrites.
type lockedWriter struct {
	mu sync.Mutex
	w  *bufio.Writer
}

func (lw *lockedWriter) writeLine(label, line string) {
	lw.mu.Lock()
	fmt.Fprintf(lw.w, "%s: %s\n", label, line)
	lw.mu.Unlock()
}

func (lw *lockedWriter) Flush() error {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.w.Flush()
}

func capture(r io.Reader, label string, lw *lockedWriter) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		lw.writeLine(label, scanner.Text())
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
