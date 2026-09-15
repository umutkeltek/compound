package executewire

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"compound/internal/work"
)

// R6P (2026-09-11): the detached governed door.
//
// LaunchDetachedViaDoor is the production binding for work's
// executeStageDetachedLaunchFn. The seam has already claimed the envelope
// under the tick actor; this function spawns the SAME governed door a human
// dispatches (`compound work advance <id> --to running --by <actor> --execute
// --harness <lane> --permission-mode bypass --prompt <p>`) as its own process
// group, wrapped in GNU timeout so the run carries a finite budget that
// outlives the heartbeat tick, with stdin closed and stdout/stderr custodied
// in a log beside the RunAttempt launch evidence. It returns as soon as the
// door has started. The door owns claimed -> running -> verifying/failed;
// nothing here mints, advances or finalizes.
//
// Cancellation: `kill -TERM -<pgid>` (the sidecar carries pgid). The door's
// launch context cancels on SIGTERM and terminates the lane's process group,
// so the attempt is finalized failed with cancellation evidence rather than
// orphaned. The timeout wrapper does exactly that at the budget.

// DefaultDetachedLaunchBudget bounds one detached door. It is the budget
// the timeout wrapper enforces; the door's own launch deadline (if any)
// still applies inside it.
var DefaultDetachedLaunchBudget = 45 * time.Minute

// DetachedDoorBinary resolves the compound binary the door runs as. Tests
// swap it for a script that records its argv.
var DetachedDoorBinary = func() (string, error) { return os.Executable() }

// DetachedTimeoutBinary resolves the GNU timeout wrapper. A missing wrapper
// is a refusal, not a silent unbounded launch.
var DetachedTimeoutBinary = func() (string, error) {
	for _, name := range []string{"gtimeout", "timeout"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errors.New("detached launch: no GNU timeout wrapper (gtimeout/timeout) on PATH; refusing an unbounded door")
}

// DetachedLaunchArgv is the exact command a detached door runs, so tests and
// operators can read it without spawning anything.
func DetachedLaunchArgv(timeoutBin, doorBin string, budget time.Duration, workID, lane, prompt, actor string) []string {
	seconds := int(budget.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return []string{
		timeoutBin, "--signal=TERM", "--kill-after=60", strconv.Itoa(seconds),
		doorBin, "work", "advance", workID,
		"--to", "running", "--by", actor, "--execute",
		"--harness", lane, "--permission-mode", "bypass",
		"--prompt", prompt,
	}
}

// writeDetachedLaunch is the sidecar write seam (tests inject failures at
// the exact steps the start/record contract cares about).
var writeDetachedLaunch = work.WriteDetachedLaunch

// LaunchDetachedViaDoor spawns the detached door for an already-claimed
// envelope and records the sidecar. The record protocol is start/record-safe:
//
//	1. a RESERVED sidecar (no pid) is written BEFORE the start — a failure
//	   there is a true not-started refusal (no child exists, plain error);
//	2. the guarded start; a start failure removes the reserved sidecar
//	   best-effort (nothing started);
//	3. after a successful start the pid line is appended to the door log
//	   (the log fd predates the start, so it usually survives the same
//	   filesystem failure that ate the sidecar);
//	4. the sidecar is updated to running+pid. A failure there (after one
//	   retry) returns a *work.DetachedLaunchRecordError carrying the started
//	   identity: the caller must treat the door as owning the envelope, never
//	   close the work as failed-to-spawn, and let a reconcile tick adopt the
//	   reserved record from the log.
func LaunchDetachedViaDoor(store *work.Store, workID, lane, prompt, actor string, now time.Time) (work.DetachedLaunch, error) {
	var launch work.DetachedLaunch
	if store == nil {
		return launch, errors.New("detached launch: nil work store")
	}
	workID, lane, actor = strings.TrimSpace(workID), strings.TrimSpace(lane), strings.TrimSpace(actor)
	if workID == "" || lane == "" || actor == "" {
		return launch, fmt.Errorf("detached launch: workID=%q lane=%q actor=%q; all required", workID, lane, actor)
	}
	wenv, err := store.Get(workID)
	if err != nil {
		return launch, fmt.Errorf("detached launch: load envelope %s: %w", workID, err)
	}
	if !strings.HasPrefix(wenv.State, "claimed") {
		return launch, fmt.Errorf("detached launch: envelope %s is %s, expected claimed (the seam claims before it hands off)", workID, wenv.State)
	}
	if wenv.GitDelivery == nil || strings.TrimSpace(wenv.GitDelivery.TargetWorktreeRoot) == "" {
		return launch, errors.New("workspace_unresolvable:envelope " + workID + " has no git_delivery.target_worktree_root")
	}
	if err := AssertLaunchContextInstalled(); err != nil {
		return launch, err
	}
	timeoutBin, err := DetachedTimeoutBinary()
	if err != nil {
		return launch, err
	}
	doorBin, err := DetachedDoorBinary()
	if err != nil {
		return launch, fmt.Errorf("detached launch: resolve door binary: %w", err)
	}
	home := store.HomePath()
	dir := work.DetachedLaunchDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return launch, fmt.Errorf("detached launch: %w", err)
	}
	logPath := filepath.Join(dir, workID+".door.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return launch, fmt.Errorf("detached launch: open door log: %w", err)
	}
	defer logFile.Close()
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return launch, fmt.Errorf("detached launch: %w", err)
	}
	defer devnull.Close()

	budget := DefaultDetachedLaunchBudget
	argv := DetachedLaunchArgv(timeoutBin, doorBin, budget, workID, lane, prompt, actor)
	// 1. Durable reserved launch identity BEFORE the process exists. A
	// failure here is a clean not-started refusal: no child, no mutation.
	reserved := work.DetachedLaunch{
		SchemaVersion: work.DetachedLaunchSchemaVersion,
		WorkID:        workID,
		Lane:          lane,
		Actor:         actor,
		Command:       argv,
		LogPath:       logPath,
		BudgetSeconds: int(budget.Seconds()),
		StartedAt:     now.UTC().Format(time.RFC3339),
		Outcome:       work.DetachedOutcomeReserved,
	}
	if err := writeDetachedLaunch(home, reserved); err != nil {
		return launch, fmt.Errorf("detached launch: the reserved sidecar could not be written, so no door was started: %w", err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = strings.TrimSpace(wenv.GitDelivery.TargetWorktreeRoot)
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+home)
	cmd.Stdin = devnull
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	setProcessGroup(cmd)
	fmt.Fprintf(logFile, "%s detached door start work=%s lane=%s actor=%s budget=%s argv=%q\n", now.UTC().Format(time.RFC3339), workID, lane, actor, budget, argv)
	// 2. Guarded start. Nothing started means the reserved record goes away
	// best-effort; a stale reserved record would otherwise read as owned.
	if err := cmd.Start(); err != nil {
		_ = os.Remove(filepath.Join(dir, workID+".json"))
		return launch, fmt.Errorf("detached launch: start door: %w", err)
	}
	pid := cmd.Process.Pid
	launch = reserved
	launch.PID = pid
	launch.PGID = pid // Setpgid makes the child its own group leader.
	launch.Outcome = work.DetachedOutcomeRunning
	// 3. Started-identity evidence in the log, before any sidecar attempt:
	// the log fd is already open, so this line is the recovery authority a
	// reconcile tick adopts when step 4 keeps failing.
	fmt.Fprintf(logFile, "%s %s%d pgid=%d\n", now.UTC().Format(time.RFC3339), work.DetachedDoorStartedLogLine, pid, pid)
	// 4. Record the started identity. One retry; a persistent failure is
	// reported as recovery_required, never as failed-to-spawn.
	var writeErr error
	for attempt := 0; attempt < 2; attempt++ {
		writeErr = writeDetachedLaunch(home, launch)
		if writeErr == nil {
			break
		}
	}
	if writeErr != nil {
		// The door is running; do not lose its identity. Report loudly and
		// typed so the caller cannot mistake this for a no-start refusal.
		return launch, &work.DetachedLaunchRecordError{Launch: launch, Err: writeErr}
	}
	// Do not wait: the tick returns, the door lives on. Release drops the
	// parent's handle so the child is reparented and reaped by init.
	_ = cmd.Process.Release()
	return launch, nil
}
