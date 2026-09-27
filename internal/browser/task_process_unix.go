//go:build darwin || linux

package browser

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func jevProcessSupported() bool { return true }

// uv and Python share an owned process group. On timeout, SIGTERM lets the
// Python context manager close its Chrome and Browser Harness daemon before
// CommandContext's hard WaitDelay expires.
func configureJevProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 30 * time.Second
}

func newJevSession() (string, error) {
	return os.MkdirTemp("/tmp", "cj-")
}

// The controller owns detached Chrome and Browser Harness processes even when
// uv/Python exits without running the bridge's finally block.
func cleanupJevSession(dir string) error {
	if filepath.Dir(dir) != "/tmp" || !strings.HasPrefix(filepath.Base(dir), "cj-") {
		return errors.New("refusing to clean unowned Jev session")
	}
	var failures []error
	for _, entry := range []struct{ path, marker string }{
		{filepath.Join(dir, "chrome.pid"), "--user-data-dir=" + filepath.Join(dir, "chrome")},
	} {
		if err := reapJevPID(entry.path, entry.marker); err != nil {
			failures = append(failures, err)
		}
	}
	runtimeDir := filepath.Join(dir, "runtime")
	entries, readErr := os.ReadDir(runtimeDir)
	if readErr != nil && !os.IsNotExist(readErr) {
		failures = append(failures, readErr)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pid") {
			continue
		}
		if err := reapJevPID(filepath.Join(runtimeDir, entry.Name()), "browser_harness.daemon"); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) != 0 {
		return errors.Join(failures...)
	}
	return os.RemoveAll(dir)
}

func reapJevPID(path, marker string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	value := strings.TrimSpace(string(data))
	var record struct {
		PID int `json:"pid"`
	}
	if json.Unmarshal(data, &record) == nil && record.PID > 0 {
		value = strconv.Itoa(record.PID)
	} else {
		fields := strings.Fields(value)
		if len(fields) == 0 {
			return fmt.Errorf("empty owned process PID in %s", path)
		}
		value = fields[0]
	}
	pid, err := strconv.Atoi(value)
	if err != nil || pid <= 1 {
		return fmt.Errorf("invalid owned process PID in %s", path)
	}
	state, err := exec.Command("ps", "-p", value, "-o", "stat=").Output()
	if err != nil || strings.HasPrefix(strings.TrimSpace(string(state)), "Z") {
		return nil // Exited or waiting for its parent to reap it.
	}
	command, err := exec.Command("ps", "-p", value, "-o", "command=").Output()
	if err != nil || len(command) == 0 { // Already gone.
		return nil
	}
	if !strings.Contains(string(command), marker) {
		return nil // Never signal a process whose identity no longer matches.
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	for i := 0; i < 20; i++ {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	return nil
}
