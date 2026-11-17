package hook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"compound/internal/corpus"
	"compound/internal/work"
)

const memoryRefreshActor = "automation:memory-refresh"

// Request pacing for the whole home, across every session. After a start the
// next one waits memoryRefreshMinGap. While starts do not lead to a newer
// refresh, the wait doubles per start up to memoryRefreshMaxGap.
const (
	memoryRefreshMinGap        = time.Minute
	memoryRefreshMaxGap        = 30 * time.Minute
	memoryRefreshRequestSchema = "compound.memory_refresh_request.v0.1"
)

var startMemoryRefresh = func(binary, homePath, cwd string, args []string) error {
	cmd := exec.Command(binary, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	// The native work command owns the RunAttempt and captures the actual refresh
	// child's output. Detach only this admission command from the short-lived hook.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// A shared nonblocking probe observes the native service lock without changing it.
func memoryRefreshLaunchActive(hp string) (bool, error) {
	lock, err := os.Open(filepath.Join(hp, "lock", "memory-refresh-launch.lock"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer lock.Close()
	err = syscall.Flock(int(lock.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return true, nil
	}
	return false, err
}

// refreshApprovedMemory never captures/indexes in the prompt injection process.
// The existing native execution rail checks current policy and the exact Work
// generation before the child can mutate. Competing triggers lose that CAS.
//
// Every live tool call of every session reaches this function, so it starts
// nothing that admission must deny (a foreign lease) and starts at most one
// admission command per gap for the whole home (claimMemoryRefreshRequest).
// It never waits. It returns the status kept in Result.MemoryRefresh:
//
//	""                         not a live PostToolUse, Stop or SessionEnd hook, or no refresh due
//	configuration_unavailable  the memory configuration or coverage could not be read
//	work_unavailable           the work store could not be opened
//	setup_required             the refresh work item does not exist
//	work_<state>               the item is not verifying or not owned by the automation actor
//	lease_unavailable          the item's lease could not be read
//	lease_held                 another actor holds the item's lease, so admission would deny it
//	already_running            the service holds its launch lock; no request or retry is consumed
//	launch_state_unavailable   the service launch lock could not be observed
//	workspace_unavailable      the hook cwd is not a usable workspace
//	binary_unavailable         the running binary could not be resolved
//	service_unavailable        the memory service next to the binary is missing or not executable
//	request_busy               another hook process is taking the request stamp right now
//	recently_requested         the last start is less than the current gap ago
//	backing_off                earlier starts led to no newer refresh, so the gap is longer
//	request_state_unavailable  the request stamp could not be read or written
//	launch_failed              the admission command could not be started
//	requested:<work id>        the admission command was started
func refreshApprovedMemory(hp string, fx Fixture, now time.Time) string {
	if !fx.Live || (fx.Hook != "PostToolUse" && fx.Hook != "SessionEnd" && fx.Hook != "Stop") {
		return ""
	}
	source := ""
	if fx.Hook == "SessionEnd" {
		source = fx.TranscriptPath
	}
	due, err := corpus.RefreshDue(hp, now, source)
	if err != nil {
		return "configuration_unavailable"
	}
	if !due {
		return ""
	}
	sum := sha256.Sum256([]byte(hp))
	id := "work_memory_refresh_" + hex.EncodeToString(sum[:])[:16]
	store, err := work.OpenReadOnly(hp)
	if err != nil {
		return "work_unavailable"
	}
	w, err := store.Get(id)
	if err != nil {
		return "setup_required"
	}
	if w.Owner != memoryRefreshActor || w.State != "verifying" {
		return "work_" + w.State
	}
	// Admission refuses an advance by anyone but the lease holder. Starting one
	// anyway only queues a sure denial on the home's admission lock.
	lease, held, err := store.ReadLease(id)
	if err != nil {
		return "lease_unavailable"
	}
	if held && strings.TrimSpace(lease.By) != memoryRefreshActor {
		return "lease_held"
	}
	if active, err := memoryRefreshLaunchActive(hp); err != nil {
		return "launch_state_unavailable"
	} else if active {
		return "already_running"
	}
	cwd := fx.Cwd
	if !filepath.IsAbs(cwd) || filepath.Clean(cwd) == filepath.Clean(hp) {
		return "workspace_unavailable"
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return "workspace_unavailable"
	}
	binary, err := os.Executable()
	if err != nil {
		return "binary_unavailable"
	}
	service := filepath.Join(filepath.Dir(binary), "compound-memory-service")
	if st, err := os.Stat(service); err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
		return "service_unavailable"
	}
	args := []string{"tick", "--binary", binary, "--memory-home", hp, "--workspace", cwd}
	if status := claimMemoryRefreshRequest(hp, now); status != "" {
		return status
	}
	if err := startMemoryRefresh(service, hp, cwd, args); err != nil {
		return "launch_failed"
	}
	return fmt.Sprintf("requested:%s", id)
}

// memoryRefreshRequest is the request stamp shared by every hook process of a
// home: when the last admission command was started, and how many starts in a
// row led to no newer refresh.
type memoryRefreshRequest struct {
	SchemaVersion string `json:"schema_version"`
	RequestedAt   string `json:"requested_at"`
	Misses        int    `json:"misses"`
}

func memoryRefreshRequestDir(hp string) string {
	return filepath.Join(hp, "state", "runs", "memory-refresh-request")
}

// claimMemoryRefreshRequest decides whether this hook process may start the
// refresh admission now. It returns "" when it may, and otherwise the status to
// report; then nothing starts. Every error starts nothing.
//
// Race safety. The whole decision (read the stamp, compare, publish the next
// stamp) runs while this process holds an exclusive flock(2) on
// last-request.lock, asked with LOCK_NB. On Darwin and Linux a flock belongs to
// the open file description, so two opens of the file, in two processes or two
// goroutines, exclude each other and the kernel gives the lock to one at a time.
// A process that does not get it returns request_busy at once: the hook never
// waits. The kernel drops the lock when the descriptor closes or the process
// dies, so a killed hook cannot wedge it. The next stamp is a complete file in
// the same folder published by rename(2), which is atomic, so a reader sees the
// old stamp or the new one, never part of one. So two processes can never both
// start for the same stamp: whoever gets the lock second reads the first one's
// stamp. Go opens files close-on-exec, and the lock is released before the
// caller starts the child, so the child never holds it.
//
// Pacing. A start is allowed when the last one is at least the gap ago. The gap
// is memoryRefreshMinGap after a start that led to a newer refresh. When
// coverage did not move past the last start, that start was a miss: the gap
// doubles per miss in a row, up to memoryRefreshMaxGap.
func claimMemoryRefreshRequest(hp string, now time.Time) string {
	dir := memoryRefreshRequestDir(hp)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "request_state_unavailable"
	}
	lock, err := os.OpenFile(filepath.Join(dir, "last-request.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return "request_state_unavailable"
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return "request_busy"
		}
		return "request_state_unavailable"
	}
	path := filepath.Join(dir, "last-request.json")
	next := memoryRefreshRequest{SchemaVersion: memoryRefreshRequestSchema, RequestedAt: now.UTC().Format(time.RFC3339Nano)}
	body, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// The first request of this home.
	case err != nil:
		return "request_state_unavailable"
	default:
		var last memoryRefreshRequest
		if json.Unmarshal(body, &last) != nil || last.SchemaVersion != memoryRefreshRequestSchema || last.Misses < 0 {
			return "request_state_unavailable"
		}
		at, err := time.Parse(time.RFC3339Nano, last.RequestedAt)
		if err != nil {
			return "request_state_unavailable"
		}
		// RefreshDue calls a lane due when now-checked_at >= 5m. Asked at
		// at+5m it is false exactly when every lane was checked after the
		// last start, so this reads the coverage through its owner.
		missed, err := corpus.RefreshDue(hp, at.Add(5*time.Minute))
		if err != nil {
			return "configuration_unavailable"
		}
		gap := memoryRefreshMinGap
		if missed {
			gap = memoryRefreshGap(last.Misses)
			next.Misses = last.Misses + 1
		}
		if now.Sub(at) < gap {
			if missed && last.Misses > 0 {
				return "backing_off"
			}
			return "recently_requested"
		}
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return "request_state_unavailable"
	}
	tmp, err := os.CreateTemp(dir, ".last-request-*.tmp")
	if err != nil {
		return "request_state_unavailable"
	}
	_, writeErr := tmp.Write(raw)
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmp.Name())
		return "request_state_unavailable"
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return "request_state_unavailable"
	}
	return ""
}

// memoryRefreshGap is the wait after a start that was the given number of
// misses in a row: one minute, doubled per miss, at most thirty minutes.
func memoryRefreshGap(misses int) time.Duration {
	gap := memoryRefreshMinGap
	for i := 0; i < misses && gap < memoryRefreshMaxGap; i++ {
		gap *= 2
	}
	if gap > memoryRefreshMaxGap {
		gap = memoryRefreshMaxGap
	}
	return gap
}
