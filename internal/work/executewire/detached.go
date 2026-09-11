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

// LaunchDetachedViaDoor spawns the detached door for an already-claimed
// envelope and records the sidecar. See the file comment for the contract.
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
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = strings.TrimSpace(wenv.GitDelivery.TargetWorktreeRoot)
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+home)
	cmd.Stdin = devnull
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	setProcessGroup(cmd)
	fmt.Fprintf(logFile, "%s detached door start work=%s lane=%s actor=%s budget=%s argv=%q\n", now.UTC().Format(time.RFC3339), workID, lane, actor, budget, argv)
	if err := cmd.Start(); err != nil {
		return launch, fmt.Errorf("detached launch: start door: %w", err)
	}
	pid := cmd.Process.Pid
	launch = work.DetachedLaunch{
		SchemaVersion: work.DetachedLaunchSchemaVersion,
		WorkID:        workID,
		Lane:          lane,
		Actor:         actor,
		PID:           pid,
		PGID:          pid, // Setpgid makes the child its own group leader.
		Command:       argv,
		LogPath:       logPath,
		BudgetSeconds: int(budget.Seconds()),
		StartedAt:     now.UTC().Format(time.RFC3339),
		Outcome:       work.DetachedOutcomeRunning,
	}
	if err := work.WriteDetachedLaunch(home, launch); err != nil {
		// The door is running; do not lose its identity. Report loudly.
		return launch, fmt.Errorf("detached launch: door pid %d started but the sidecar could not be written: %w", pid, err)
	}
	// Do not wait: the tick returns, the door lives on. Release drops the
	// parent's handle so the child is reparented and reaped by init.
	_ = cmd.Process.Release()
	return launch, nil
}
