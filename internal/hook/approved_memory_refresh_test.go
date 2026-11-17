package hook

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"compound/internal/corpus"
	"compound/internal/envelope"
)

func TestApprovedMemoryTriggerUsesGovernedResumeAndIgnoresReplay(t *testing.T) {
	hp, cwd := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(hp, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := []byte("corpus_lanes:\n  - lane: fixture\n    source_family: fixture\n    privacy: private\n    source:\n      root: " + cwd + "\n      format: markdown\n")
	if err := os.WriteFile(filepath.Join(hp, "config", "memory-engine.yaml"), cfg, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(hp))
	id := "work_memory_refresh_" + hex.EncodeToString(sum[:])[:16]
	if err := os.MkdirAll(filepath.Join(hp, "work", "work"), 0700); err != nil {
		t.Fatal(err)
	}
	w := envelope.WorkEnvelope{Header: envelope.Header{ID: id}, Owner: memoryRefreshActor, State: "verifying"}
	body, _ := json.Marshal(w)
	if err := os.WriteFile(filepath.Join(hp, "work", "work", id+".json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	service := filepath.Join(filepath.Dir(binary), "compound-memory-service")
	if err := os.WriteFile(service, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(service) })
	old := startMemoryRefresh
	defer func() { startMemoryRefresh = old }()
	calls := 0
	startMemoryRefresh = func(launcher, home, workspace string, args []string) error {
		calls++
		joined := strings.Join(args, " ")
		for _, part := range []string{"tick --binary " + binary, "--memory-home " + hp, "--workspace " + cwd} {
			if !strings.Contains(joined, part) {
				t.Fatalf("missing %s: %s", part, joined)
			}
		}
		if launcher != service || home != hp || workspace != cwd {
			t.Fatal("wrong scope")
		}
		return nil
	}
	fx := Fixture{Live: true, Hook: "PostToolUse", Cwd: cwd}
	if got := refreshApprovedMemory(hp, fx, time.Now()); !strings.HasPrefix(got, "requested:") {
		t.Fatalf("trigger %s", got)
	}
	fx.Live = false
	refreshApprovedMemory(hp, fx, time.Now())
	fx.Live = true
	fx.Hook = "UserPromptSubmit"
	refreshApprovedMemory(hp, fx, time.Now())
	if calls != 1 {
		t.Fatalf("unexpected launches %d", calls)
	}
}

// memoryRefreshHome builds a home whose refresh is due at any time after
// checkedAt+5m: one configured lane last checked at checkedAt, and the refresh
// work item verifying under the automation actor with no lease.
func memoryRefreshHome(t *testing.T, checkedAt time.Time) (hp, cwd, id string) {
	t.Helper()
	hp, cwd = t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(hp, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := []byte("corpus_lanes:\n  - lane: fixture\n    source_family: fixture\n    privacy: private\n    source:\n      root: " + cwd + "\n      format: markdown\n")
	if err := os.WriteFile(filepath.Join(hp, "config", "memory-engine.yaml"), cfg, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(hp))
	id = "work_memory_refresh_" + hex.EncodeToString(sum[:])[:16]
	if err := os.MkdirAll(filepath.Join(hp, "work", "work"), 0700); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(envelope.WorkEnvelope{Header: envelope.Header{ID: id}, Owner: memoryRefreshActor, State: "verifying"})
	if err := os.WriteFile(filepath.Join(hp, "work", "work", id+".json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	setMemoryCoverage(t, hp, checkedAt)
	return hp, cwd, id
}

// setMemoryCoverage writes the fixture lane's coverage as a refresh would.
func setMemoryCoverage(t *testing.T, hp string, checkedAt time.Time) {
	t.Helper()
	path := filepath.Join(filepath.Dir(corpus.IndexDir(hp, corpus.LaneConfig{Lane: "fixture"})), "refresh.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"lane": "fixture", "status": "ok", "checked_at": checkedAt.UTC().Format(time.RFC3339Nano)})
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
}

func writeMemoryRefreshLease(t *testing.T, hp, id, by string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(hp, "work", "leases"), 0700); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"work_id": id, "by": by, "claimed_at": "2026-01-01T00:00:00Z"})
	if err := os.WriteFile(filepath.Join(hp, "work", "leases", id+".json"), body, 0600); err != nil {
		t.Fatal(err)
	}
}

// installTestMemoryService puts an executable service next to the test binary,
// where the hook looks for it.
func installTestMemoryService(t *testing.T) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	service := filepath.Join(filepath.Dir(binary), "compound-memory-service")
	if err := os.WriteFile(service, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(service) })
}

// countMemoryRefreshStarts replaces the launcher with a counter.
func countMemoryRefreshStarts(t *testing.T) *atomic.Int64 {
	t.Helper()
	old := startMemoryRefresh
	t.Cleanup(func() { startMemoryRefresh = old })
	var starts atomic.Int64
	startMemoryRefresh = func(string, string, string, []string) error {
		starts.Add(1)
		return nil
	}
	return &starts
}

func TestMemoryRefreshStartsOnceForTwoHundredSimultaneousCalls(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	hp, cwd, id := memoryRefreshHome(t, now.Add(-time.Hour))
	installTestMemoryService(t)
	starts := countMemoryRefreshStarts(t)
	fx := Fixture{Live: true, Hook: "PostToolUse", Cwd: cwd}
	statuses := make([]string, 200)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			statuses[i] = refreshApprovedMemory(hp, fx, now)
		}(i)
	}
	close(gate)
	wg.Wait()
	if got := starts.Load(); got != 1 {
		t.Fatalf("200 simultaneous calls started %d admissions, want 1", got)
	}
	requested := 0
	for _, status := range statuses {
		switch status {
		case "requested:" + id:
			requested++
		case "request_busy", "recently_requested":
		default:
			t.Fatalf("unexpected status %q", status)
		}
	}
	if requested != 1 {
		t.Fatalf("%d calls reported a request, want 1", requested)
	}
}

// TestMemoryRefreshRequestProcessHelper is the body of one separate hook
// process for TestMemoryRefreshStartsOnceAcrossProcesses; alone it does nothing.
func TestMemoryRefreshRequestProcessHelper(t *testing.T) {
	hp := os.Getenv("COMPOUND_TEST_MEMORY_REFRESH_HOME")
	if hp == "" {
		return
	}
	now, err := time.Parse(time.RFC3339Nano, os.Getenv("COMPOUND_TEST_MEMORY_REFRESH_NOW"))
	if err != nil {
		t.Fatal(err)
	}
	starts := countMemoryRefreshStarts(t)
	fx := Fixture{Live: true, Hook: "PostToolUse", Cwd: os.Getenv("COMPOUND_TEST_MEMORY_REFRESH_CWD")}
	fmt.Println("ready")
	// The parent releases every process with one line, so they call together.
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	statuses := make([]string, 10)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i] = refreshApprovedMemory(hp, fx, now)
		}(i)
	}
	wg.Wait()
	fmt.Printf("starts=%d\n", starts.Load())
	for _, status := range statuses {
		fmt.Printf("status=%s\n", status)
	}
}

func TestMemoryRefreshStartsOnceAcrossProcesses(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	hp, cwd, id := memoryRefreshHome(t, now.Add(-time.Hour))
	installTestMemoryService(t)
	const processes = 20
	type helper struct {
		cmd    *exec.Cmd
		stdin  io.WriteCloser
		stdout *bufio.Reader
	}
	helpers := make([]helper, 0, processes)
	for i := 0; i < processes; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMemoryRefreshRequestProcessHelper$")
		cmd.Env = append(os.Environ(),
			"COMPOUND_TEST_MEMORY_REFRESH_HOME="+hp,
			"COMPOUND_TEST_MEMORY_REFRESH_CWD="+cwd,
			"COMPOUND_TEST_MEMORY_REFRESH_NOW="+now.Format(time.RFC3339Nano))
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		h := helper{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout)}
		t.Cleanup(func() { _ = h.cmd.Process.Kill(); _ = h.cmd.Wait() })
		if line, err := h.stdout.ReadString('\n'); err != nil || strings.TrimSpace(line) != "ready" {
			t.Fatalf("helper %d not ready: %q %v", i, line, err)
		}
		helpers = append(helpers, h)
	}
	for _, h := range helpers {
		if _, err := io.WriteString(h.stdin, "go\n"); err != nil {
			t.Fatal(err)
		}
	}
	starts, requested, calls := 0, 0, 0
	for i, h := range helpers {
		out, err := io.ReadAll(h.stdout)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.cmd.Wait(); err != nil {
			t.Fatalf("helper %d: %v\n%s", i, err, out)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if n, ok := strings.CutPrefix(line, "starts="); ok {
				var count int
				if _, err := fmt.Sscan(n, &count); err != nil {
					t.Fatal(err)
				}
				starts += count
			}
			if status, ok := strings.CutPrefix(line, "status="); ok {
				calls++
				switch status {
				case "requested:" + id:
					requested++
				case "request_busy", "recently_requested":
				default:
					t.Fatalf("unexpected status %q", status)
				}
			}
		}
	}
	if calls != processes*10 || starts != 1 || requested != 1 {
		t.Fatalf("%d calls in %d processes started %d admissions (%d requested), want 200 calls and 1 start", calls, processes, starts, requested)
	}
}

func TestMemoryRefreshDoesNotStartWhatAForeignLeaseMustDeny(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	hp, cwd, id := memoryRefreshHome(t, now.Add(-time.Hour))
	installTestMemoryService(t)
	starts := countMemoryRefreshStarts(t)
	fx := Fixture{Live: true, Hook: "PostToolUse", Cwd: cwd}
	writeMemoryRefreshLease(t, hp, id, "lane:other")
	for i := 0; i < 50; i++ {
		if got := refreshApprovedMemory(hp, fx, now.Add(time.Duration(i)*time.Hour)); got != "lease_held" {
			t.Fatalf("foreign lease status %q, want lease_held", got)
		}
	}
	if got := starts.Load(); got != 0 {
		t.Fatalf("foreign lease started %d admissions", got)
	}
	if _, err := os.Stat(filepath.Join(memoryRefreshRequestDir(hp), "last-request.json")); !os.IsNotExist(err) {
		t.Fatalf("a refused call must not take the request stamp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hp, "work", "leases", id+".json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := refreshApprovedMemory(hp, fx, now); got != "lease_unavailable" || starts.Load() != 0 {
		t.Fatalf("unreadable lease: status %q starts %d", got, starts.Load())
	}
	writeMemoryRefreshLease(t, hp, id, memoryRefreshActor)
	if got := refreshApprovedMemory(hp, fx, now); got != "requested:"+id || starts.Load() != 1 {
		t.Fatalf("automation lease: status %q starts %d, want one request", got, starts.Load())
	}
}

func TestMemoryRefreshPacingDoublesUntilCoverageMoves(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	hp, cwd, id := memoryRefreshHome(t, t0.Add(-time.Hour))
	installTestMemoryService(t)
	starts := countMemoryRefreshStarts(t)
	fx := Fixture{Live: true, Hook: "PostToolUse", Cwd: cwd}
	at := t0
	call := func(offset time.Duration, want string) {
		t.Helper()
		before := starts.Load()
		got := refreshApprovedMemory(hp, fx, at.Add(offset))
		if want == "start" {
			if got != "requested:"+id || starts.Load() != before+1 {
				t.Fatalf("at +%s after the last start: status %q, want a start", offset, got)
			}
			at = at.Add(offset)
			return
		}
		if got != want || starts.Load() != before {
			t.Fatalf("at +%s after the last start: status %q starts +%d, want %s and no start", offset, got, starts.Load()-before, want)
		}
	}
	call(0, "start")
	call(10*time.Second, "recently_requested")
	call(61*time.Second, "start")
	// No newer refresh: each wait doubles, and the cap holds at thirty minutes.
	for _, gap := range []time.Duration{2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 30 * time.Minute, 30 * time.Minute} {
		call(gap-time.Second, "backing_off")
		call(gap, "start")
	}
	// A refresh runs a second after the last start. The next due call starts at
	// once and the wait is one minute again.
	setMemoryCoverage(t, hp, at.Add(time.Second))
	call(5*time.Minute+time.Second, "start")
	call(59*time.Second, "recently_requested")
	call(time.Minute, "start")
	if got := starts.Load(); got != 10 {
		t.Fatalf("starts %d, want 10", got)
	}
}

func TestMemoryRefreshStampFailureStartsNothing(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	installTestMemoryService(t)
	starts := countMemoryRefreshStarts(t)
	for name, spoil := range map[string]func(t *testing.T, dir string){
		"corrupt stamp": func(t *testing.T, dir string) {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "last-request.json"), []byte("{\"requested_at\":"), 0600); err != nil {
				t.Fatal(err)
			}
		},
		"stamp of another schema": func(t *testing.T, dir string) {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "last-request.json"), []byte(`{"requested_at":"2026-03-01T11:00:00Z","misses":0}`), 0600); err != nil {
				t.Fatal(err)
			}
		},
		"stamp folder is a file": func(t *testing.T, dir string) {
			if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dir, []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
		},
		"stamp is a folder": func(t *testing.T, dir string) {
			if err := os.MkdirAll(filepath.Join(dir, "last-request.json"), 0700); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			hp, cwd, _ := memoryRefreshHome(t, now.Add(-time.Hour))
			spoil(t, memoryRefreshRequestDir(hp))
			before := starts.Load()
			got := refreshApprovedMemory(hp, Fixture{Live: true, Hook: "PostToolUse", Cwd: cwd}, now)
			if got != "request_state_unavailable" || starts.Load() != before {
				t.Fatalf("status %q starts +%d, want request_state_unavailable and no start", got, starts.Load()-before)
			}
		})
	}
	t.Run("read-only stamp folder", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root writes through folder permissions")
		}
		hp, cwd, _ := memoryRefreshHome(t, now.Add(-time.Hour))
		dir := memoryRefreshRequestDir(hp)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
		before := starts.Load()
		got := refreshApprovedMemory(hp, Fixture{Live: true, Hook: "PostToolUse", Cwd: cwd}, now)
		if got != "request_state_unavailable" || starts.Load() != before {
			t.Fatalf("status %q starts +%d, want request_state_unavailable and no start", got, starts.Load()-before)
		}
	})
}

func TestMemoryRefreshFreshFailureKeepsBackoff(t *testing.T) {
	now := time.Now().UTC()
	hp, cwd, id := memoryRefreshHome(t, now)
	installTestMemoryService(t)
	starts := countMemoryRefreshStarts(t)
	fx := Fixture{Live: true, Hook: "PostToolUse", Cwd: cwd}
	path := filepath.Join(filepath.Dir(corpus.IndexDir(hp, corpus.LaneConfig{Lane: "fixture"})), "refresh.json")
	failed := func(at time.Time) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"lane": "fixture", "status": "index_failed", "checked_at": at.Format(time.RFC3339Nano)})
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	failed(now)
	for _, tc := range []struct {
		offset time.Duration
		want   string
	}{
		{0, "requested:" + id},
		{59 * time.Second, "recently_requested"},
		{time.Minute, "requested:" + id},
		{179 * time.Second, "backing_off"},
		{3 * time.Minute, "requested:" + id},
	} {
		failed(now.Add(tc.offset))
		if got := refreshApprovedMemory(hp, fx, now.Add(tc.offset)); got != tc.want {
			t.Fatalf("fresh failed source at +%s: got %s, want %s", tc.offset, got, tc.want)
		}
	}
	if got := starts.Load(); got != 3 {
		t.Fatalf("starts=%d, want 3", got)
	}
}

func TestMemoryRefreshHeldLaunchLockDoesNotConsumeRetry(t *testing.T) {
	now := time.Now().UTC()
	hp, cwd, id := memoryRefreshHome(t, now.Add(-time.Hour))
	installTestMemoryService(t)
	starts := countMemoryRefreshStarts(t)
	dir := filepath.Join(hp, "lock")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, "memory-refresh-launch.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	fx := Fixture{Live: true, Hook: "PostToolUse", Cwd: cwd}
	for i := 0; i < 50; i++ {
		if got := refreshApprovedMemory(hp, fx, now.Add(time.Duration(i)*time.Minute)); got != "already_running" {
			t.Fatalf("held launch lock: got %s", got)
		}
	}
	if got := starts.Load(); got != 0 {
		t.Fatalf("busy launcher started %d commands", got)
	}
	if _, err := os.Stat(filepath.Join(memoryRefreshRequestDir(hp), "last-request.json")); !os.IsNotExist(err) {
		t.Fatalf("active launch consumed a request: %v", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if got := refreshApprovedMemory(hp, fx, now); got != "requested:"+id || starts.Load() != 1 {
		t.Fatalf("released launch lock: got %s starts=%d", got, starts.Load())
	}
}
