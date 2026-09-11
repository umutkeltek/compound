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

// runAttemptStore is the RunAttempt persistence surface that
// runWithSubprocess depends on. *runattempt.Store satisfies it in
// production (every adapter passes its concrete store), so the runtime
// behavior is unchanged. The interface exists purely as a test seam: the
// subprocess_runner_test substitutes a fault-injecting wrapper to exercise
// infra-failure return paths -- in particular a store whose SetOrigin fails
// while every other write (including the terminalizing Complete /
// SetProofRefs) still lands, so the test can assert the freshly minted
// attempt is terminalized failed on disk rather than left stranded pending.
type runAttemptStore interface {
	MintWithActionIntent(workID, actor, harness, actionIntentID string, now time.Time) (runattempt.RunAttempt, error)
	SetOrigin(id, origin string) (runattempt.RunAttempt, error)
	SetPredecessor(id, predecessorID string) (runattempt.RunAttempt, error)
	SetWorkspace(id, workspace string) (runattempt.RunAttempt, error)
	SetCommand(id, command string) (runattempt.RunAttempt, error)
	SetExecutionPosture(id string, posture runattempt.ExecutionPosture) (runattempt.RunAttempt, error)
	SetExecutionSelectionSnapshot(id string, snap runattempt.ExecutionSelectionSnapshot) (runattempt.RunAttempt, error)
	SetDispatchIntent(id, laneID, requestedModelID, requestedProviderRoute string) (runattempt.RunAttempt, error)
	SetProofRefs(id string, refs []string) (runattempt.RunAttempt, error)
	SetTranscript(id, ref string) (runattempt.RunAttempt, error)
	Complete(id string, status string, now time.Time) (runattempt.RunAttempt, error)
	CommitLaunch(id string, now time.Time, start func() error) (runattempt.RunAttempt, error)
	Heartbeat(id string, now time.Time) (runattempt.RunAttempt, error)
	Home() string
}

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
//   - ActionIntentID binds atomically with Mint -- even the first post-mint
//     metadata failure retains the attempt->intent->policy audit chain.
//   - pending -> running + launch heartbeat and cmd.Start share the canonical
//     RunAttempt lock -- a stale sweep cannot abandon an attempt and then let
//     its child start.
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
	store runAttemptStore,
	harnessName string,
	in RunInput,
	build buildCmdFn,
	heartbeatInterval time.Duration,
) (RunOutput, error) {
	if err := validateSubprocessInput(harnessName, in); err != nil {
		return RunOutput{}, err
	}
	// A non-positive heartbeat interval panics time.NewTicker inside
	// heartbeatLoop. Clamp here -- the single funnel every adapter passes
	// through -- so a misconfigured adapter degrades to the default cadence
	// instead of crashing the run after the RunAttempt was minted.
	if heartbeatInterval <= 0 {
		heartbeatInterval = defaultHeartbeatInterval
	}
	started := in.Now
	ra, err := store.MintWithActionIntent(in.WorkID, harnessName, harnessName, in.ActionIntentID, started)
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
	//
	// Defined BEFORE the SetOrigin stamp below so an origin-stamp failure
	// terminalizes the same way every later infra failure does, instead of
	// returning bare RunOutput{} and stranding a pending ratt_ record.
	failTerm := func(reason string) RunOutput {
		if transcriptPath != "" {
			if f, ferr := os.OpenFile(transcriptPath, os.O_WRONLY|os.O_APPEND, 0o644); ferr == nil {
				_, _ = fmt.Fprintf(f, "infra_error: %s\n", reason)
				_ = f.Close()
				_, _ = store.SetTranscript(ra.ID, transcriptPath)
			}
		} else {
			refs := append([]string(nil), in.ProofRefs...)
			refs = append(refs, "infra_error:"+reason)
			_, _ = store.SetProofRefs(ra.ID, refs)
		}
		if t, cerr := store.Complete(ra.ID, runattempt.StatusFailed, started.Add(time.Since(started))); cerr == nil {
			ra = t
		}
		return RunOutput{RunAttempt: ra, TranscriptPath: transcriptPath, ExitCode: -1}
	}

	// F2: every subprocess-adapter mint is a real harness execution, never a
	// claim-time placeholder or a post-hoc observation. Stamp immediately
	// after Mint, before any other side effect, so F2 (supersede-at-attach,
	// internal/work) can recognize it when it attaches to a work envelope.
	// A stamp failure is a genuine infra failure: terminalize the freshly
	// minted attempt (never leave it pending) and return it alongside the
	// error, exactly like the SetWorkspace/SetCommand/... failures below.
	if stamped, oErr := store.SetOrigin(ra.ID, runattempt.OriginHarnessExecution); oErr != nil {
		return failTerm("stamp origin: " + oErr.Error()), fmt.Errorf("%s: stamp origin: %w", harnessName, oErr)
	} else {
		ra = stamped
	}
	// work_r7a_execution_selection_20260911: the wire stamps the per-run
	// selection snapshot (what this launch was asked to run, with honest
	// applied-effort + native-session evidence) BEFORE the subprocess
	// starts, so the historical record reflects the plan rather than the
	// outcome. Stamped right after SetOrigin / before any other side
	// effect, matching the existing ExecutionPosture pattern. Nil means the
	// caller did not provide a snapshot (direct harness runs, observe-run,
	// etc.) and the attempt proceeds without one — the validator will refuse
	// any backfill on a terminal attempt, so a missing snapshot stays
	// missing.
	if in.ExecutionSelectionSnapshot != nil {
		if stamped, sErr := store.SetExecutionSelectionSnapshot(ra.ID, *in.ExecutionSelectionSnapshot); sErr != nil {
			return failTerm("stamp execution selection snapshot: " + sErr.Error()), fmt.Errorf("%s: stamp execution selection snapshot: %w", harnessName, sErr)
		} else {
			ra = stamped
		}
	}

	if in.PredecessorID != "" {
		linked, err := store.SetPredecessor(ra.ID, in.PredecessorID)
		if err != nil {
			return failTerm("link retry predecessor: " + err.Error()), fmt.Errorf("%s: link retry predecessor: %w", harnessName, err)
		}
		ra = linked
	}
	if _, err := store.SetWorkspace(ra.ID, in.Workspace); err != nil {
		return failTerm("set workspace"), fmt.Errorf("%s: set workspace: %w", harnessName, err)
	}
	if _, err := store.SetCommand(ra.ID, in.Command); err != nil {
		return failTerm("set command"), fmt.Errorf("%s: set command: %w", harnessName, err)
	}
	if in.ExecutionPosture != nil {
		if _, err := store.SetExecutionPosture(ra.ID, *in.ExecutionPosture); err != nil {
			return failTerm("set execution posture"), fmt.Errorf("%s: set execution posture: %w", harnessName, err)
		}
	}
	if len(in.ProofRefs) > 0 {
		if _, err := store.SetProofRefs(ra.ID, append([]string(nil), in.ProofRefs...)); err != nil {
			return failTerm("set dispatch proof refs"), fmt.Errorf("%s: set dispatch proof refs: %w", harnessName, err)
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
	if in.LaneID != "" || in.RequestedModelID != "" || in.RequestedProviderRoute != "" {
		if _, err := store.SetDispatchIntent(ra.ID, in.LaneID, in.RequestedModelID, in.RequestedProviderRoute); err != nil {
			refs := append([]string(nil), in.ProofRefs...)
			refs = append(refs, "lane_id_stamp_error:"+err.Error())
			_, _ = store.SetProofRefs(ra.ID, refs)
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
	// Spawn the subprocess in its own process group (unix only; a no-op
	// elsewhere) so the timeout kill below reaps the whole tree, not just the
	// direct child -- see procgroup_unix.go for the orphaned-grandchild hang
	// this prevents.
	setProcessGroup(cmd)
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
	startCalled := false
	start := func() error {
		if startCalled {
			return errors.New("subprocess start operation may be called only once")
		}
		startCalled = true
		launched, launchErr := store.CommitLaunch(ra.ID, time.Now().UTC(), func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return cmd.Start()
		})
		if launched.ID != "" {
			ra = launched
		}
		return launchErr
	}
	if in.StartGuard != nil {
		err = in.StartGuard(ctx, start)
		if err == nil && !startCalled {
			err = errors.New("subprocess start guard returned without starting the command")
		}
	} else {
		err = start()
	}
	if err != nil {
		// A faulty guard could return an error after a successful start. The
		// production guard returns start's result directly, but contain any
		// future implementation bug here so no unobserved child is orphaned.
		if startCalled && cmd.Process != nil {
			killProcessTree(cmd)
			_ = cmd.Wait()
		}
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
	// regardless of how buildCmd constructed the command. The kill targets
	// the whole process group (setProcessGroup above): killing only the
	// direct child lets an orphaned grandchild keep the pipes open, which
	// re-hangs capWG.Wait with fresh heartbeats that hide the stall from
	// SweepStale. The timer also selects on stopCh so a process that exits
	// on its own never gets a spurious kill.
	// reapMu/reaped make "kill the tree" and "reap the child" mutually
	// exclusive. Timeout authority now outlives capture (see cmd.Wait below),
	// so without this guard the timer could fire after cmd.Wait released the
	// pid and SIGKILL a recycled pgid -- killing an unrelated process is a
	// worse failure than the hang this ordering fixes.
	var reapMu sync.Mutex
	reaped := false
	var timedOut atomic.Bool
	var cancelReason atomic.Value // empty, "timeout:exceeded", or "cancel:context_cancelled"
	cancelReason.Store("")
	if in.Timeout > 0 {
		go func() {
			t := time.NewTimer(in.Timeout)
			defer t.Stop()
			select {
			case <-t.C:
				reapMu.Lock()
				defer reapMu.Unlock()
				if reaped {
					return // already exited and reaped; the pid is not ours
				}
				timedOut.Store(true)
				cancelReason.Store("timeout:exceeded:" + in.Timeout.String())
				killProcessTree(cmd)
			case <-stopCh:
			}
		}()
	}
	// Honor caller-context cancellation the same way: a cancelled
	// downstream ctx MUST kill the tree (process group), not just the
	// direct child, so an orphaned grandchild holding the pipes open
	// does not survive. Preflight deadline (the heartbeat's `timeout
	// 600`) propagates as ctx cancellation from the seam; the runner
	// translates that into the same tree-kill the timeout would have
	// issued, and stamps `cancel:context_cancelled` as evidence on the
	// RunAttempt so the audit answers "why did this die early?". This
	// is what the unattended launch wire (DEFECT 4, AUT-P0-004)
	// relies on — the wire no longer discards cancellation by passing
	// context.Background() to adapter.Run.
	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				reapMu.Lock()
				defer reapMu.Unlock()
				if reaped {
					return
				}
				timedOut.Store(true)
				reason := "cancel:context_cancelled"
				if cerr := ctx.Err(); cerr != nil {
					reason = "cancel:context_cancelled:" + cerr.Error()
				}
				cancelReason.Store(reason)
				killProcessTree(cmd)
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
	// cmd.Wait BEFORE close(stopCh): draining capture must not disarm the
	// kill timer. Capture returning only proves the pipes EOF'd, which a
	// child can cause at will with `exec 1>&- 2>&-` while it keeps running.
	// Closing stopCh here would cancel the timer and leave cmd.Wait blocking
	// with no remaining authority to kill anything -- two closed file
	// descriptors would buy a subprocess unbounded life inside a governed
	// run. Timeout authority therefore lives until the process is actually
	// reaped; the timer's stopCh select still prevents a spurious kill on a
	// process that exited on its own.
	waitErr := cmd.Wait()
	// Mark the pid reaped before cancelling the timer, so a timer that fires
	// in this window observes reaped and declines to signal a freed pgid.
	reapMu.Lock()
	reaped = true
	reapMu.Unlock()
	close(stopCh) // stops BOTH the heartbeat loop and the timeout timer
	hbWG.Wait()
	if err := tw.Flush(); err != nil {
		// A failed flush loses buffered transcript lines but is not fatal --
		// the run still produced a result. Surface it on the transcript ref
		// so the audit is honest about the partial capture.
		refs := append([]string(nil), in.ProofRefs...)
		refs = append(refs, "transcript_flush_error:"+err.Error())
		_, _ = store.SetProofRefs(ra.ID, refs)
	}

	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	status := runattempt.StatusSucceeded
	if timedOut.Load() || exitCode != 0 || waitErr != nil {
		status = runattempt.StatusFailed
	}
	// Persist timeout / cancellation evidence as proof_refs so the
	// audit can distinguish a deadline kill from a context cancel from
	// an ordinary non-zero exit. Best-effort: a SetProofRefs failure
	// does not stop terminalization — the audit logs the reason in
	// the catch below via the failure record path.
	if reason, ok := cancelReason.Load().(string); ok && strings.TrimSpace(reason) != "" {
		refs := append([]string(nil), in.ProofRefs...)
		refs = append(refs, reason)
		if _, err := store.SetProofRefs(ra.ID, refs); err != nil {
			refs = append([]string(nil), in.ProofRefs...)
			refs = append(refs, "set_proof_refs_error:"+err.Error())
			_, _ = store.SetProofRefs(ra.ID, refs)
		}
	}
	if _, err := store.SetTranscript(ra.ID, transcriptPath); err != nil {
		// The run produced a transcript on disk but we could not pin it on
		// the attempt; terminalize failed so the audit is honest.
		return failTerm("set transcript"), fmt.Errorf("%s: set transcript: %w", harnessName, err)
	}
	terminal, err := store.Complete(ra.ID, status, started.Add(time.Since(started)))
	if err != nil {
		// Retry once: a transient store fault must not strand a pending
		// attempt that callers forward-link onto the envelope.
		terminal, err = store.Complete(ra.ID, status, started.Add(time.Since(started)))
	}
	if err != nil {
		// The run produced a result but the attempt would not terminalize.
		// failTerm best-effort records the reason and attempts a failed
		// Complete, so the returned attempt is terminal whenever ANY store
		// write still lands -- never a pending record the caller forward-links
		// as if the run were still in flight. The real exit code / timeout /
		// duration are preserved on top of failTerm's infra-failure defaults.
		out := failTerm("complete run attempt: " + err.Error())
		out.ExitCode = exitCode
		out.TimedOut = timedOut.Load()
		out.Duration = time.Since(started)
		return out, fmt.Errorf("%s: complete: %w", harnessName, err)
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

func heartbeatLoop(store runAttemptStore, id string, start time.Time, interval time.Duration, stop <-chan struct{}, wg *sync.WaitGroup) {
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
func storePath(store runAttemptStore, ra runattempt.RunAttempt) string {
	return filepath.Join(store.Home(), "run_attempts", ra.ID+".json")
}
