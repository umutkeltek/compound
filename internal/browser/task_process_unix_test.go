//go:build darwin || linux

package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

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
