package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"compound/internal/authority/gitrepository"
	homepkg "compound/internal/home"
	"compound/internal/landlease"
)

const presubmitLeaseTTL = 2 * time.Minute

type presubmitOwnedProcessGroup interface {
	kill() error
	drained() (bool, error)
	reapEarly() bool
	close() error
}

var presubmitObserveGroup = func(group presubmitOwnedProcessGroup) (bool, error) { return group.drained() }
var presubmitOpenGroup = newPresubmitOwnedProcessGroup

type PresubmitCheckpoint struct {
	CommandSequence int       `json:"command_sequence,omitempty"`
	GroupStarted    bool      `json:"group_started,omitempty"`
	GroupClosed     bool      `json:"group_closed,omitempty"`
	Gate            string    `json:"gate"`
	Status          string    `json:"status"`
	At              time.Time `json:"at"`
	ProcessGroupID  int       `json:"process_group_id,omitempty"`
	OutputPaths     []string  `json:"output_paths,omitempty"`
}

type presubmitRuntime struct {
	store            *Store
	out              *PresubmitExecutionResult
	resource         *landlease.Resource
	barrier          *landlease.ExecutionBarrier
	leaseHeld        bool
	root             *os.Root
	parentRoot       *os.Root
	parentInfo       os.FileInfo
	outputInfo       os.FileInfo
	outputDir        string
	sequence         int
	nextGate         int
	completedGates   int
	groupStarted     bool
	groupClosed      bool
	checkpointRecord func(*PresubmitExecutionResult) error
	groupID          int
	stage            string
	outputPaths      []string
	stopHeartbeat    context.CancelFunc
	heartbeatDone    chan struct{}
	cancel           context.CancelCauseFunc
}

func (s *Store) acquirePresubmitRuntime(ctx context.Context, out *PresubmitExecutionResult, cancel context.CancelCauseFunc) (*presubmitRuntime, error) {
	rt := &presubmitRuntime{store: s, out: out, cancel: cancel, stage: "preparation", checkpointRecord: s.checkpointPresubmitExecution}
	out.Holder = "proof:presubmit:" + out.RunAttemptID + ":" + out.WorkID + ":" + out.Actor
	generation, err := landlease.ProofCoordinationGeneration(s.home)
	if err != nil {
		return rt, err
	}
	acquire := func(resource *landlease.Resource) error {
		rt.resource = resource
		out.ProofCoordination = resource.ProofIdentity()
		_, rt.barrier, err = resource.AcquireWithExecutionBarrierPrepared(out.Holder, presubmitLeaseTTL, time.Now(), func(outcome landlease.AcquireOutcome) error {
			if outcome.ExpiredPredecessor != nil && outcome.ExpiredPredecessor.Holder != out.Holder {
				return fmt.Errorf("exact predecessor recovery required before native admission: %s", outcome.ExpiredPredecessor.Holder)
			}
			out.LeasePrepared = true
			return s.checkpointPresubmitExecution(out)
		})
		if err != nil {
			return err
		}
		rt.leaseHeld = true
		return nil
	}
	if generation == "" {
		if out.Request.Wait > 0 {
			return rt, fmt.Errorf("presubmit --wait requires a coordinated FIFO proof domain")
		}
		err = acquire(landlease.ProofResource(s.home))
	} else {
		identity, identityErr := landlease.NewProofCoordinationIdentity(generation, landlease.ProofCapacitySystemFull, out.RepositoryFingerprint)
		if identityErr != nil {
			return rt, identityErr
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			return rt, fmt.Errorf("presubmit admission requires a bounded deadline")
		}
		admission, admissionErr := landlease.BeginProofAdmission(s.home, identity, deadline)
		if admissionErr != nil {
			return rt, admissionErr
		}
		err = withPresubmitAdmission(admission, func() error { return rt.admit(ctx, admission, acquire) })
	}
	if err != nil {
		return rt, err
	}
	if err = rt.check(ctx); err != nil {
		return rt, err
	}
	return rt, nil
}

func (rt *presubmitRuntime) prepareOutput(ctx context.Context) error {
	s, out := rt.store, rt.out
	if err := rt.check(ctx); err != nil {
		return err
	}
	if err := validatePresubmitOutput(out.Request.OutPath, s.home); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(out.Request.OutPath))
	if err != nil {
		return fmt.Errorf("presubmit output root: %w", err)
	}
	rt.parentRoot = root
	rt.parentInfo, err = root.Stat(".")
	if err != nil {
		return err
	}
	rt.outputDir = filepath.Base(out.Request.OutPath) + ".presubmit-" + out.RunAttemptID
	if err = root.Mkdir(rt.outputDir, 0o700); err != nil {
		return fmt.Errorf("presubmit output directory: %w", err)
	}
	rt.root, err = root.OpenRoot(rt.outputDir)
	if err != nil {
		return err
	}
	rt.outputInfo, err = rt.root.Stat(".")
	if err != nil {
		return err
	}
	if err = rt.syncOutputDirectory(); err != nil {
		return err
	}
	rt.heartbeatDone = make(chan struct{})
	hbCtx, stop := context.WithCancel(ctx)
	rt.stopHeartbeat = stop
	go func() {
		defer close(rt.heartbeatDone)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		runPresubmitHeartbeat(ctx, hbCtx, ticker.C, func(checkCtx context.Context) error {
			e := rt.check(checkCtx)
			if e == nil {
				_, e = s.runAttempts().Heartbeat(out.RunAttemptID, time.Now())
			}
			if e == nil {
				_, e = rt.resource.Refresh(out.Holder, presubmitLeaseTTL, time.Now())
			}
			return e
		}, rt.cancel)
	}()
	return nil
}

func runPresubmitHeartbeat(ctx, stopCtx context.Context, ticks <-chan time.Time, refresh func(context.Context) error, cancel context.CancelCauseFunc) {
	for {
		select {
		case <-stopCtx.Done():
			return
		case <-ticks:
			// Stopping the heartbeat must not cancel an in-flight authority check.
			// The execution context still carries real cancellation and deadlines.
			if err := refresh(ctx); err != nil {
				cancel(fmt.Errorf("presubmit heartbeat: %w", err))
				return
			}
		}
	}
}

func (rt *presubmitRuntime) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	return rt.store.withLifecycleMutation(func() error { return rt.checkLocked(ctx) })
}

func (rt *presubmitRuntime) checkLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	w, err := rt.store.Get(rt.out.WorkID)
	if err != nil {
		return err
	}
	if !presubmitSameGeneration(w, *rt.out) {
		return fmt.Errorf("presubmit Work authority generation changed; refusing new subprocess")
	}
	if err = rt.store.requireProofWorkLeaseOwner(w.ID, rt.out.Actor, "presubmit subprocess"); err != nil {
		return err
	}
	if rt.leaseHeld {
		status, e := rt.resource.Status(time.Now())
		if e != nil {
			return e
		}
		if status.Holder != rt.out.Holder || !status.Held || status.IsExpired {
			return fmt.Errorf("presubmit exclusive resource changed or expired")
		}
	}
	return nil
}

// run owns every preparation and gate child under the same native budget and
// barrier. It records CommitLaunch only around the first actual cmd.Start.
func (rt *presubmitRuntime) run(ctx context.Context, cmd *exec.Cmd, executionTimeout time.Duration) (runErr error) {
	if err := rt.check(ctx); err != nil {
		return err
	}
	rt.sequence++
	rt.groupID = 0
	rt.groupStarted = false
	rt.groupClosed = false
	if err := rt.checkOutputCustody(); err != nil {
		return err
	}
	logName := fmt.Sprintf("%04d-%s.log", rt.sequence, proofGateSafeFileName(rt.stage))
	logFile, err := rt.root.OpenFile(logName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, logFile.Close()) }()
	fullPath := filepath.Join(filepath.Dir(rt.out.Request.OutPath), rt.outputDir, logName)
	rt.outputPaths = append(rt.outputPaths, fullPath)
	if err = rt.checkpoint(rt.stage, "started"); err != nil {
		return err
	}
	if err = rt.syncOutputDirectory(); err != nil {
		return err
	}
	stdoutDest, stderrDest := cmd.Stdout, cmd.Stderr
	if stdoutDest == nil {
		stdoutDest = io.Discard
	}
	if stderrDest == nil {
		stderrDest = io.Discard
	}
	raw := &presubmitSyncWriter{file: logFile}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return err
	}
	defer stdoutR.Close()
	defer stdoutW.Close()
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		return err
	}
	defer stderrR.Close()
	defer stderrW.Close()
	cmd.Stdout, cmd.Stderr = stdoutW, stderrW
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = nil
	cmd.WaitDelay = 2 * time.Second
	// A separate duplicate is not required for sequential starts; the parent's
	// barrier stays open until all registered children and capture are joined.
	cmd.ExtraFiles = append(cmd.ExtraFiles, rt.barrier.InheritableFiles()...)
	commandCtx := ctx
	cancelCommand := context.CancelCauseFunc(func(error) {})
	var deadline *time.Timer
	var group presubmitOwnedProcessGroup
	var groupOpenErr error
	var earlyWait <-chan error
	stopGroupKiller := func() {}
	defer func() {
		stopGroupKiller()
		if group != nil {
			runErr = errors.Join(runErr, group.close())
		}
		if deadline != nil {
			deadline.Stop()
		}
		cancelCommand(nil)
	}()

	var destinationMu sync.Mutex
	var copies sync.WaitGroup
	copies.Add(2)
	var copyMu sync.Mutex
	var copyErr, captureCloseErr error
	copyOutput := func(reader *os.File, destination io.Writer) {
		defer copies.Done()
		_, e := io.Copy(io.MultiWriter(raw, &presubmitLockedWriter{mu: &destinationMu, writer: destination}), reader)
		if e != nil {
			copyMu.Lock()
			copyErr = errors.Join(copyErr, e)
			copyMu.Unlock()
			rt.cancel(fmt.Errorf("presubmit raw output: %w", e))
		}
	}
	// Drain before Start: post-start authority persistence must never become
	// backpressure against a child whose execution clock is already running.
	go copyOutput(stdoutR, stdoutDest)
	go copyOutput(stderrR, stderrDest)
	var closeWriters sync.Once
	closeCaptureWriters := func() {
		closeWriters.Do(func() {
			captureCloseErr = errors.Join(stdoutW.Close(), stderrW.Close())
			if captureCloseErr != nil {
				rt.cancel(fmt.Errorf("presubmit capture writers: %w", captureCloseErr))
			}
		})
	}
	captureDone := make(chan struct{})
	watchdogDone := make(chan struct{})
	watchdogStarted := false
	startCaptureWatchdog := func() {
		watchdogStarted = true
		// An escaped pipe owner must not hold capture after cancellation.
		// Closing capture is recorded failure, never proof of quiescence.
		go func() {
			defer close(watchdogDone)
			select {
			case <-captureDone:
				return
			case <-commandCtx.Done():
			}
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			select {
			case <-captureDone:
			case <-timer.C:
				_ = stdoutR.Close()
				_ = stderrR.Close()
			}
		}()
	}
	var captureJoin sync.Once
	joinCapture := func() {
		captureJoin.Do(func() {
			closeCaptureWriters()
			copies.Wait()
			close(captureDone)
			if watchdogStarted {
				<-watchdogDone
			}
		})
	}
	// The callback may never run, or group registration/publication may fail.
	// Every path closes the parent's writers and joins its capture goroutines.
	defer func() {
		joinCapture()
		runErr = errors.Join(runErr, copyErr, captureCloseErr)
	}()
	start := func() error {
		// All admission and authority checks use the original operation deadline.
		if e := rt.checkLocked(ctx); e != nil {
			return e
		}
		if executionTimeout > 0 {
			commandCtx, cancelCommand = context.WithCancelCause(ctx)
			deadline = time.NewTimer(executionTimeout)
		}
		startCaptureWatchdog()
		startErr := cmd.Start()
		// Close only parent handles; child descriptors stay owned by capture.
		// Neither close nor copy failures may misclassify an actual launch.
		closeCaptureWriters()
		if cmd.Process != nil {
			rt.groupID, rt.groupStarted = cmd.Process.Pid, true
			group, groupOpenErr = presubmitOpenGroup(cmd.Process.Pid)
			if groupOpenErr != nil {
				rt.out.ContainmentUncertain = true
				rt.cancel(groupOpenErr)
				_ = cmd.Process.Kill()
			} else {
				// Linux keeps its group pidfd across early reap. Darwin keeps the leader
				// unreaped until the exact group drains. Supervision precedes every durable
				// post-start write, including CommitLaunch publication.
				if group.reapEarly() {
					done := make(chan error, 1)
					go func() { done <- cmd.Wait() }()
					earlyWait = done
				}
				stopGroupKiller = watchPresubmitCommandGroup(commandCtx, cancelCommand, deadline, group)
			}
		}
		// Only the actual Start result classifies committed versus start_error.
		return startErr
	}
	attempt, err := rt.store.runAttempts().Get(rt.out.RunAttemptID)
	if err != nil {
		return err
	}
	err = rt.store.withLifecycleMutation(func() error {
		if attempt.Launch == nil {
			_, e := rt.store.runAttempts().CommitLaunch(attempt.ID, time.Now(), start)
			return e
		}
		return start()
	})
	if err != nil {
		// CommitLaunch can fail publishing the disposition after Start succeeded.
		// Such a child still belongs to this owner and must be joined.
		if cmd.Process == nil {
			rt.groupClosed = true
			return errors.Join(err, rt.checkpoint(rt.stage, "failed"))
		}
	}
	if groupOpenErr != nil {
		return errors.Join(err, groupOpenErr)
	}
	startErr := err
	rt.groupID = cmd.Process.Pid
	startErr = errors.Join(startErr, rt.checkpoint(rt.stage, "running"))
	if startErr != nil {
		rt.cancel(startErr)
	}

	joinCapture()
	groupErr := rt.joinOwnedGroup(commandCtx, group)
	// Stop and join every numeric-PGID signaler before any Darwin reap.
	stopGroupKiller()
	var waitErr error
	if groupErr == nil {
		rt.groupClosed = true
		if earlyWait != nil {
			waitErr = <-earlyWait
		} else {
			waitErr = cmd.Wait()
		}
	}
	if groupErr != nil && earlyWait != nil {
		select {
		case waitErr = <-earlyWait:
		default:
		}
	}
	// On unresolved Darwin containment do not reap; Linux retains an exact
	// pidfd across any early reap. Neither path fabricates joined cleanup.
	// The failed CLI may exit; its exact durable holder remains fenced.

	status := "completed"
	if commandCtx.Err() != nil {
		status = "cancelled"
	}
	if rt.out.ContainmentUncertain {
		status = "containment-uncertain"
	}
	finalErr := errors.Join(startErr, waitErr, groupErr, context.Cause(commandCtx))
	checkpointErr := rt.checkpoint(rt.stage, status)
	return errors.Join(finalErr, checkpointErr)
}

// watchPresubmitCommandGroup begins before post-start bookkeeping. A completed
// group disarms its execution clock; parent cancellation still governs the rest
// of the operation. A live group at expiry is cancelled and joined by run.
func watchPresubmitCommandGroup(ctx context.Context, cancel context.CancelCauseFunc, deadline *time.Timer, group presubmitOwnedProcessGroup) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(done)
		if deadline != nil {
			defer deadline.Stop()
			poll := time.NewTicker(10 * time.Millisecond)
			defer poll.Stop()
		execution:
			for {
				select {
				case <-stop:
					return
				case <-ctx.Done():
					break execution
				case <-poll.C:
					if empty, err := group.drained(); err == nil && empty {
						return
					}
				case <-deadline.C:
					if empty, err := group.drained(); err == nil && empty {
						return
					}
					cancel(context.DeadlineExceeded)
					break execution
				}
			}
		}
		select {
		case <-stop:
			return
		case <-ctx.Done():
		}
		_ = group.kill()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = group.kill()
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }); <-done }
}

type presubmitSyncWriter struct {
	mu   sync.Mutex
	file *os.File
}

func (w *presubmitSyncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.file.Write(p)
	if err == nil {
		err = w.file.Sync()
	}
	return n, err
}

func (rt *presubmitRuntime) checkpoint(gate, status string) error {
	rt.out.Checkpoints = append(rt.out.Checkpoints, PresubmitCheckpoint{CommandSequence: rt.sequence, GroupStarted: rt.groupStarted, GroupClosed: rt.groupClosed, Gate: gate, Status: status, At: time.Now().UTC(), ProcessGroupID: rt.groupID, OutputPaths: presubmitLatestOutput(rt.outputPaths)})
	return rt.checkpointRecord(rt.out)
}

func (s *Store) checkpointPresubmitExecution(out *PresubmitExecutionResult) error {
	artifacts, err := homepkg.NewArtifactStore(s.home)
	if err != nil {
		return err
	}
	body, err := json.Marshal(out)
	if err != nil {
		return err
	}
	ref, err := artifacts.PutPrivate(body)
	if err != nil {
		return err
	}
	out.ArtifactRef = presubmitExecutionRefPrefix + ref
	attempt, err := s.runAttempts().Get(out.RunAttemptID)
	if err != nil {
		return err
	}
	_, err = s.runAttempts().SetProofRefs(out.RunAttemptID, append(attempt.ProofRefs, out.ArtifactRef))
	return err
}

func (rt *presubmitRuntime) finish() error {
	if rt.stopHeartbeat != nil {
		rt.stopHeartbeat()
		<-rt.heartbeatDone
	}
	var err error
	if rt.root != nil {
		err = errors.Join(err, rt.root.Close())
	}
	if rt.parentRoot != nil {
		err = errors.Join(err, rt.parentRoot.Close())
	}
	if rt.out.ContainmentUncertain {
		return errors.Join(err, fmt.Errorf("native containment unresolved; exact holder retained for external cleanup, no barrier-only recovery"))
	}
	if rt.barrier != nil {
		err = errors.Join(err, rt.barrier.Close())
		rt.barrier = nil
	}
	if rt.resource == nil || !rt.out.LeasePrepared {
		return err
	}
	status, statusErr := rt.resource.Status(time.Now())
	if statusErr != nil {
		return errors.Join(err, statusErr)
	}
	if status.Holder != rt.out.Holder {
		if !rt.leaseHeld {
			return err
		}
		return errors.Join(err, fmt.Errorf("presubmit holder changed; retaining exact execution journal"))
	}
	_, attestErr := rt.resource.AttestExecutionTerminated(rt.out.Holder)
	if attestErr != nil {
		return errors.Join(err, attestErr)
	}
	rt.out.Quiescent = true
	_, releaseErr := rt.resource.ReleaseAfter(rt.out.Holder, time.Now(), func() error {
		return rt.store.checkpointPresubmitExecution(rt.out)
	})
	if releaseErr == nil {
		rt.out.Released = true
	}
	return errors.Join(err, releaseErr)
}

type nativePresubmitRuntimeKey struct{}

func PresubmitGateStarted(ctx context.Context, step ProofCheckStep, report *ProofCheckReport) error {
	rt, ok := ctx.Value(nativePresubmitRuntimeKey{}).(*presubmitRuntime)
	if !ok {
		return nil
	}
	if err := rt.check(ctx); err != nil {
		return err
	}
	if err := rt.checkInventory(); err != nil {
		return err
	}
	if rt.nextGate != rt.completedGates {
		return fmt.Errorf("native presubmit previous gate not completed")
	}
	if rt.nextGate >= len(rt.out.GateInventory) || !samePresubmitStep(step, rt.out.GateInventory[rt.nextGate]) {
		return fmt.Errorf("native presubmit gate differs from sealed inventory")
	}
	if rt.nextGate == 0 {
		if err := gitrepository.RevalidateSnapshotContext(ctx, rt.out.Request.Repo, *rt.out.SourceSnapshot); err != nil {
			return err
		}
	}
	rt.nextGate++
	rt.stage = step.Name
	rt.outputPaths = nil
	rt.out.Report = report
	return rt.checkpoint(step.Name, "started")
}

func PresubmitGateCompleted(ctx context.Context, step ProofCheckStep, report *ProofCheckReport) error {
	rt, ok := ctx.Value(nativePresubmitRuntimeKey{}).(*presubmitRuntime)
	if !ok {
		return nil
	}
	if rt.nextGate == 0 || rt.completedGates != rt.nextGate-1 || !samePresubmitStep(step, rt.out.GateInventory[rt.completedGates]) {
		return fmt.Errorf("native presubmit completion differs from current gate")
	}
	rt.completedGates++
	rt.out.Report = report
	status := step.Status
	if ctx.Err() != nil {
		status = "cancelled"
	}
	if rt.out.ContainmentUncertain {
		status = "containment-uncertain"
	}
	return rt.checkpoint(step.Name, status)
}

// BindPresubmitCommandRunner is intentionally context-scoped; ordinary and
// canonical callers retain their existing subprocess execution path.
func bindPresubmitCommandRunner(ctx context.Context, rt *presubmitRuntime) context.Context {
	ctx = context.WithValue(ctx, nativePresubmitRuntimeKey{}, rt)
	return gitrepository.ContextWithLaunchTimeoutCommandRunner(ctx, rt.run)
}

type presubmitLockedWriter struct {
	mu     *sync.Mutex
	writer io.Writer
}

func (w *presubmitLockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}

func validatePresubmitOutput(path, homePath string) error {
	realParent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return err
	}
	path = filepath.Join(realParent, filepath.Base(path))
	if info, e := os.Lstat(path); e == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return fmt.Errorf("presubmit output must be a regular, nonsymlink report")
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	for dir := realParent; ; dir = filepath.Dir(dir) {
		manifest, e := homepkg.LoadManifest(dir)
		if e == nil && manifest.SchemaVersion == homepkg.SchemaVersion {
			return fmt.Errorf("presubmit output cannot target COMPOUND_HOME")
		}
		if dir == filepath.Clean(homePath) || strings.HasSuffix(dir, string(os.PathSeparator)+"artifacts"+string(os.PathSeparator)+"refs") {
			return fmt.Errorf("presubmit output cannot target runtime/canonical artifacts")
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return nil
}

func presubmitLatestOutput(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	return []string{paths[len(paths)-1]}
}

func (rt *presubmitRuntime) joinOwnedGroup(ctx context.Context, group presubmitOwnedProcessGroup) error {
	var cancelledAt time.Time
	for {
		drained, err := presubmitObserveGroup(group)
		if err != nil {
			rt.out.ContainmentUncertain = true
			rt.cancel(err)
			_ = group.kill() // direct leader has not been reaped.
			return err
		}
		if drained {
			return nil
		}
		if ctx.Err() != nil {
			if cancelledAt.IsZero() {
				cancelledAt = time.Now()
			}
			if time.Since(cancelledAt) >= 2*time.Second {
				rt.out.ContainmentUncertain = true
				return fmt.Errorf("owned process group did not provably drain after cancellation")
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (rt *presubmitRuntime) syncOutputDirectory() error {
	dir, err := rt.root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (rt *presubmitRuntime) checkOutputCustody() error {
	visible, err := os.Stat(filepath.Dir(rt.out.Request.OutPath))
	if err != nil {
		return err
	}
	if !os.SameFile(visible, rt.parentInfo) {
		return fmt.Errorf("presubmit output parent identity changed")
	}
	info, err := rt.parentRoot.Lstat(rt.outputDir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, rt.outputInfo) {
		return fmt.Errorf("presubmit output directory identity changed")
	}
	return nil
}
