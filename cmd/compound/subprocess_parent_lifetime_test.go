package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const testSubprocessParentPIDEnv = "COMPOUND_TEST_CONTROLLER_PID"
const testSubprocessHardExpiryUnixEnv = "COMPOUND_TEST_HARD_EXPIRY_UNIX"
const testSubprocessShellParentGuard = `test_fixture_validate_parent() {
	[ -n "${COMPOUND_TEST_CONTROLLER_PID:-}" ] || exit 97
	[ "$PPID" -eq "$COMPOUND_TEST_CONTROLLER_PID" ] || exit 97
	case "${COMPOUND_TEST_HARD_EXPIRY_UNIX:-}" in
		''|*[!0-9]*) exit 97 ;;
	esac
	[ "$COMPOUND_TEST_HARD_EXPIRY_UNIX" -gt 0 ] || exit 97
}
test_fixture_parent_alive() {
	test_fixture_now_unix=$(date +%s) || return 1
	case "$test_fixture_now_unix" in
		''|*[!0-9]*) return 1 ;;
	esac
	[ "$test_fixture_now_unix" -lt "$COMPOUND_TEST_HARD_EXPIRY_UNIX" ] || return 1
	kill -0 "$COMPOUND_TEST_CONTROLLER_PID" 2>/dev/null
}
test_fixture_validate_parent
`

type testSubprocessParentLifetime struct {
	parentPID int
	deadline  time.Time
}

func TestTestSubprocessParentLifetimeRequiresConfiguredParent(t *testing.T) {
	t.Setenv(testSubprocessParentPIDEnv, "")
	lifetime, err := newTestSubprocessParentLifetime(2 * minimumProofCheckReservationTTL)
	if err == nil {
		t.Fatalf("missing %s returned lifetime %+v without error", testSubprocessParentPIDEnv, lifetime)
	}
	if err := (*testSubprocessParentLifetime)(nil).check(); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("nil parent lifetime check = %v, want required error", err)
	}
}

func TestTestSubprocessParentLifetimeExpiresWhileParentLives(t *testing.T) {
	parentPID := os.Getppid()
	t.Setenv(testSubprocessParentPIDEnv, strconv.Itoa(parentPID))
	lifetime, err := newTestSubprocessParentLifetime(10 * time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := lifetime.check(); err == nil || !strings.Contains(err.Error(), "hard expiry") {
		t.Fatalf("expired lifetime check = %v, want hard-expiry error", err)
	}
	if err := syscall.Kill(parentPID, 0); err != nil {
		t.Fatalf("parent %d was not alive at expiry: %v", parentPID, err)
	}
}

func TestTestSubprocessShellGuardExpiresWhileParentLives(t *testing.T) {
	parentPID := os.Getpid()
	deadline := time.Now().Add(1500 * time.Millisecond).Unix()
	command := exec.Command("/bin/sh", "-c", testSubprocessShellParentGuard+`while test_fixture_parent_alive; do sleep 0.05; done`)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Env = append(
		os.Environ(),
		testSubprocessParentPIDEnv+"="+strconv.Itoa(parentPID),
		testSubprocessHardExpiryUnixEnv+"="+strconv.FormatInt(deadline, 10),
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	pgid := command.Process.Pid
	done := make(chan error, 1)
	waited := make(chan struct{})
	go func() {
		done <- command.Wait()
		close(waited)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		select {
		case <-waited:
		case <-time.After(time.Second):
		}
	})

	select {
	case err := <-done:
		t.Fatalf("shell guard exited before its wall-clock deadline: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shell guard expiry exit: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shell guard did not exit after its wall-clock deadline")
	}
	if err := syscall.Kill(parentPID, 0); err != nil {
		t.Fatalf("parent %d was not alive at shell expiry: %v", parentPID, err)
	}
}

func newTestSubprocessParentLifetime(maxDuration time.Duration) (*testSubprocessParentLifetime, error) {
	rawParentPID := os.Getenv(testSubprocessParentPIDEnv)
	if rawParentPID == "" {
		return nil, fmt.Errorf("missing required %s", testSubprocessParentPIDEnv)
	}
	parentPID, err := strconv.Atoi(rawParentPID)
	if err != nil || parentPID <= 0 {
		return nil, fmt.Errorf("invalid %s %q", testSubprocessParentPIDEnv, rawParentPID)
	}
	if actual := os.Getppid(); actual != parentPID {
		return nil, fmt.Errorf("test subprocess parent = %d, want controller %d", actual, parentPID)
	}
	if maxDuration <= 0 {
		return nil, fmt.Errorf("test subprocess hard expiry must be positive: %s", maxDuration)
	}
	return &testSubprocessParentLifetime{
		parentPID: parentPID,
		deadline:  time.Now().Add(maxDuration),
	}, nil
}

func (lifetime *testSubprocessParentLifetime) check() error {
	if lifetime == nil {
		return fmt.Errorf("test subprocess parent lifetime is required")
	}
	if actual := os.Getppid(); actual != lifetime.parentPID {
		return fmt.Errorf("test subprocess controller %d exited (current parent %d)", lifetime.parentPID, actual)
	}
	if !time.Now().Before(lifetime.deadline) {
		return fmt.Errorf("test subprocess exceeded hard expiry while controller %d remained alive", lifetime.parentPID)
	}
	if err := syscall.Kill(lifetime.parentPID, 0); err != nil {
		return fmt.Errorf("probe test subprocess controller %d: %w", lifetime.parentPID, err)
	}
	return nil
}

func addTestSubprocessParentEnv(env map[string]string, parentPID int) {
	env[testSubprocessParentPIDEnv] = strconv.Itoa(parentPID)
	env[testSubprocessHardExpiryUnixEnv] = strconv.FormatInt(time.Now().Add(2*minimumProofCheckReservationTTL).Unix(), 10)
}

func publishTestPIDFile(path string, pid int) error {
	if pid <= 0 {
		return fmt.Errorf("publish test pid: invalid pid %d", pid)
	}
	tmpPath := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	defer os.Remove(tmpPath)
	if err := os.WriteFile(tmpPath, []byte(fmt.Sprintf("%d\n", pid)), 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
