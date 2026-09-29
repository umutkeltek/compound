//go:build darwin || linux

package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestJevReaperFailsClosedAndUsesOwnedSignalTarget(t *testing.T) {
	for _, tc := range []struct {
		name       string
		psFails    bool
		groupFails bool
		termFails  bool
		killFails  bool
		survives   bool
		nonLeader  bool
		wantError  bool
	}{
		{"ps_unavailable", true, false, false, false, false, false, true},
		{"group_inspection_error", false, true, false, false, false, false, true},
		{"term_error", false, false, true, false, false, false, true},
		{"force_kill_error", false, false, false, true, true, false, true},
		{"survivor", false, false, false, false, true, false, true},
		{"nonleader_exact_pid", false, false, false, false, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pidPath := filepath.Join(t.TempDir(), "owned.pid")
			if err := os.WriteFile(pidPath, []byte("4321"), 0o600); err != nil {
				t.Fatal(err)
			}
			alive := true
			var signalled []int
			ops := jevReaperOps{
				ps: func(_ int, field string) ([]byte, error) {
					if tc.psFails {
						return nil, errors.New("ps unavailable")
					}
					if field == "stat" {
						return []byte("S"), nil
					}
					return []byte("browser_harness.daemon --owned"), nil
				},
				groupInspect: func(int, string) (bool, bool, error) {
					if tc.groupFails {
						return false, false, errors.New("group inspection unavailable")
					}
					return alive, alive, nil
				},
				kill: func(target int, signal syscall.Signal) error {
					if signal == 0 {
						if alive {
							return nil
						}
						return syscall.ESRCH
					}
					signalled = append(signalled, target)
					if signal == syscall.SIGTERM && tc.termFails {
						return syscall.EPERM
					}
					if signal == syscall.SIGKILL && tc.killFails {
						return syscall.EPERM
					}
					if !tc.survives {
						alive = false
					}
					return nil
				},
				pgid: func(int) (int, error) {
					if tc.nonLeader {
						return 123, nil
					}
					return 4321, nil
				},
				sleep: func(time.Duration) {},
			}
			err := reapJevPIDWith(pidPath, "browser_harness.daemon", ops)
			if (err != nil) != tc.wantError {
				t.Fatalf("reaper error = %v, wantError=%t", err, tc.wantError)
			}
			if tc.nonLeader && (len(signalled) != 1 || signalled[0] != 4321) {
				t.Fatalf("nonleader signalled broad group: %v", signalled)
			}
			if tc.psFails && len(signalled) != 0 {
				t.Fatalf("unverified process was signalled: %v", signalled)
			}
		})
	}
}

func TestJevReaperProcessHelper(t *testing.T) {
	mode := os.Getenv("COMPOUND_TEST_REAPER_MODE")
	if mode == "" {
		return
	}
	if mode == "child" {
		signal.Ignore(syscall.SIGTERM)
		if err := os.WriteFile(os.Getenv("COMPOUND_TEST_REAPER_MARKER"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(2)
		}
		for {
			time.Sleep(time.Second)
		}
	}
	child := exec.Command(os.Args[0], "-test.run=^TestJevReaperProcessHelper$", "--", os.Getenv("COMPOUND_TEST_REAPER_GROUP_MARKER"))
	child.Env = append(os.Environ(), "COMPOUND_TEST_REAPER_MODE=child")
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGTERM)
	<-stopped
	os.Exit(0)
}

func TestJevReaperStopsOwnedGroupAfterLeaderExits(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child.pid")
	cmd := exec.Command(os.Args[0], "-test.run=^TestJevReaperProcessHelper$")
	cmd.Env = append(os.Environ(), "COMPOUND_TEST_REAPER_MODE=leader", "COMPOUND_TEST_REAPER_MARKER="+marker)
	cmd.Env = append(cmd.Env, "COMPOUND_TEST_REAPER_GROUP_MARKER=TestJevReaperProcessHelper")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	}()
	deadline := time.Now().Add(5 * time.Second)
	var childPID int
	for {
		data, err := os.ReadFile(marker)
		if err == nil {
			childPID, err = strconv.Atoi(string(data))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owned child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	pidPath := filepath.Join(t.TempDir(), "owned.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reapJevPID(pidPath, "TestJevReaperProcessHelper"); err != nil {
		t.Fatal(err)
	}
	if alive, err := jevLiveGroupMembers(cmd.Process.Pid); err != nil || alive {
		t.Fatalf("owned process group survived: alive=%t err=%v", alive, err)
	}
	if err := syscall.Kill(childPID, 0); err == nil {
		state, stateErr := exec.Command("ps", "-p", strconv.Itoa(childPID), "-o", "stat=").Output()
		if stateErr != nil || !strings.HasPrefix(strings.TrimSpace(string(state)), "Z") {
			t.Fatalf("child remained live after cleanup: state=%q err=%v", state, stateErr)
		}
	}
}

func TestJevReaperStopsBoundGroupWhenLeaderAlreadyExited(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child.pid")
	groupMarker := "--user-data-dir=" + filepath.Join(t.TempDir(), "chrome")
	cmd := exec.Command(os.Args[0], "-test.run=^TestJevReaperProcessHelper$")
	cmd.Env = append(os.Environ(), "COMPOUND_TEST_REAPER_MODE=leader", "COMPOUND_TEST_REAPER_MARKER="+marker, "COMPOUND_TEST_REAPER_GROUP_MARKER="+groupMarker)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owned child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(cmd.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(t.TempDir(), "chrome.pid")
	binding, err := json.Marshal(map[string]any{"pid": cmd.Process.Pid, "pgid": cmd.Process.Pid, "marker": groupMarker})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidPath, binding, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reapJevPID(pidPath, groupMarker); err != nil {
		t.Fatal(err)
	}
	if alive, err := jevLiveGroupMembers(cmd.Process.Pid); err != nil || alive {
		t.Fatalf("pre-exited leader's group survived: alive=%t err=%v", alive, err)
	}
}

func TestJevReaperRefusesAbsentUnboundOrUninspectableGroup(t *testing.T) {
	for _, tc := range []struct {
		name, binding string
		inspectErr    bool
	}{
		{"plain_pid", "4321", false},
		{"wrong_marker", `{"pid":4321,"pgid":4321,"marker":"foreign"}`, false},
		{"inspection_error", `{"pid":4321,"pgid":4321,"marker":"owned"}`, true},
		{"unmatched_member", `{"pid":4321,"pgid":4321,"marker":"owned"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pidPath := filepath.Join(t.TempDir(), "chrome.pid")
			if err := os.WriteFile(pidPath, []byte(tc.binding), 0o600); err != nil {
				t.Fatal(err)
			}
			signals := 0
			ops := jevReaperOps{
				ps: func(int, string) ([]byte, error) { return nil, syscall.ESRCH },
				groupInspect: func(int, string) (bool, bool, error) {
					if tc.inspectErr {
						return false, false, errors.New("inspection failed")
					}
					return true, false, nil
				},
				kill: func(int, syscall.Signal) error { signals++; return nil },
			}
			if err := reapJevPIDWith(pidPath, "owned", ops); err == nil || signals != 0 {
				t.Fatalf("unverified group signalled: err=%v signals=%d", err, signals)
			}
		})
	}
}

// Opt-in because this uses real Chrome and resolves the pinned upstream uv
// dependency. It proves a long-running browser/daemon is reaped on timeout,
// rather than relying only on the instant synthetic stdio helper.
func TestJevRealBrowserCancellationCleansChrome(t *testing.T) {
	if os.Getenv("COMPOUND_JEV_REAL_PROCESS_TEST") != "1" {
		t.Skip("set COMPOUND_JEV_REAL_PROCESS_TEST=1 for real browser timeout proof")
	}
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv unavailable")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sessionDir, err := newJevSession()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupJevSession(sessionDir)
	script := `import os, runpy, signal, time
signal.signal(signal.SIGTERM, lambda _sig, _frame: (_ for _ in ()).throw(SystemExit(143)))
ns = runpy.run_path("jev_bridge.py")
with ns["isolated_chrome"]("https://example.com/") as chrome_pid:
    from jev_ultrafast.browser import Browser
    browser = Browser("https://example.com/")
    browser.observe(screenshot=False)
    print("READY:" + str(chrome_pid) + ":" + os.environ["BH_HOME"], flush=True)
    time.sleep(60)
`
	cmd := exec.CommandContext(ctx, "uv", "run", "--no-project", "--with", "git+https://github.com/browser-use/jev-ultrafast.git@"+jevRevision, "python", "-c", script)
	configureJevProcess(cmd)
	cmd.Env = append(os.Environ(), "COMPOUND_JEV_SESSION_DIR="+sessionDir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = nil // no page content or provider credentials are supplied
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
		} else {
			ready <- ""
		}
	}()
	var line string
	select {
	case line = <-ready:
	case <-time.After(45 * time.Second):
		cancel()
		_ = cmd.Wait()
		t.Fatal("real browser did not reach ready state")
	}
	parts := strings.SplitN(line, ":", 3)
	if len(parts) != 3 || parts[0] != "READY" {
		cancel()
		_ = cmd.Wait()
		t.Fatalf("real browser start failed: %q", line)
	}
	pid, err := strconv.Atoi(parts[1])
	if err != nil {
		cancel()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	cancel()
	if err := cmd.Wait(); err == nil {
		t.Fatal("cancelled process returned success")
	}
	var daemonPID int
	if entries, err := os.ReadDir(filepath.Join(sessionDir, "runtime")); err == nil {
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".pid") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(sessionDir, "runtime", entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			var record struct {
				PID int `json:"pid"`
			}
			if json.Unmarshal(data, &record) == nil {
				daemonPID = record.PID
			} else {
				daemonPID, _ = strconv.Atoi(strings.Fields(string(data))[0])
			}
		}
	}
	if err := cleanupJevSession(sessionDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(parts[2]); !os.IsNotExist(err) {
		t.Fatalf("browser temp state survived cancellation: %v", err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("owned Chrome pid %d survived cancellation: %v", pid, err)
	}
	if daemonPID > 0 {
		if err := syscall.Kill(daemonPID, 0); !errors.Is(err, syscall.ESRCH) {
			t.Fatalf("owned Browser Harness daemon %d survived cancellation: %v", daemonPID, err)
		}
	}
}
