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
	return reapJevPIDWith(path, marker, jevReaperOps{
		ps: func(pid int, field string) ([]byte, error) {
			return exec.Command("ps", "-p", strconv.Itoa(pid), "-o", field+"=").Output()
		},
		kill:  syscall.Kill,
		pgid:  syscall.Getpgid,
		sleep: time.Sleep,
	})
}

type jevReaperOps struct {
	ps    func(int, string) ([]byte, error)
	kill  func(int, syscall.Signal) error
	pgid  func(int) (int, error)
	sleep func(time.Duration)
}

func reapJevPIDWith(path, marker string, ops jevReaperOps) error {
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
	state, err := ops.ps(pid, "stat")
	if err != nil {
		return fmt.Errorf("inspect owned process %d state: %w", pid, err)
	}
	if strings.HasPrefix(strings.TrimSpace(string(state)), "Z") {
		return nil
	}
	command, err := ops.ps(pid, "command")
	if err != nil {
		return fmt.Errorf("inspect owned process %d command: %w", pid, err)
	}
	if len(command) == 0 {
		return fmt.Errorf("owned process %d has no observable command", pid)
	}
	if !strings.Contains(string(command), marker) {
		return fmt.Errorf("owned process %d identity no longer matches %q", pid, marker)
	}
	groupID, err := ops.pgid(pid)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect owned process %d group: %w", pid, err)
	}
	target := pid
	if groupID == pid {
		target = -pid
	}
	if err := ops.kill(target, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("terminate owned process %d: %w", pid, err)
	}
	for i := 0; i < 20; i++ {
		if gone, err := jevProcessGone(pid, ops); err != nil {
			return err
		} else if gone {
			return nil
		}
		ops.sleep(100 * time.Millisecond)
	}
	command, err = ops.ps(pid, "command")
	if err != nil {
		return fmt.Errorf("recheck owned process %d identity: %w", pid, err)
	}
	if !strings.Contains(string(command), marker) {
		return fmt.Errorf("owned process %d identity changed before force stop", pid)
	}
	if err := ops.kill(target, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("force stop owned process %d: %w", pid, err)
	}
	for i := 0; i < 20; i++ {
		if gone, err := jevProcessGone(pid, ops); err != nil {
			return err
		} else if gone {
			return nil
		}
		ops.sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("owned process %d survived SIGKILL", pid)
}

func jevProcessGone(pid int, ops jevReaperOps) (bool, error) {
	if err := ops.kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true, nil
	} else if err != nil {
		return false, fmt.Errorf("probe owned process %d: %w", pid, err)
	}
	state, err := ops.ps(pid, "stat")
	if err != nil {
		return false, fmt.Errorf("inspect owned process %d state: %w", pid, err)
	}
	return strings.HasPrefix(strings.TrimSpace(string(state)), "Z"), nil
}
