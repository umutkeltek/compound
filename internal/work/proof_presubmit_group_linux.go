//go:build linux

package work

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Linux v6.9 include/uapi/linux/pidfd.h and kernel/signal.c: this selects
// PIDTYPE_PGID on the pidfd's referenced struct pid, not a numeric PID lookup.
const presubmitPidfdSignalProcessGroup = 1 << 2

func presubmitGroupContainmentSupported() error {
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		return fmt.Errorf("native presubmit requires Linux pidfd process-group support (kernel 6.9+): %w", err)
	}
	defer unix.Close(fd)
	err = unix.PidfdSendSignal(fd, 0, nil, presubmitPidfdSignalProcessGroup)
	if err == nil || errors.Is(err, unix.ESRCH) {
		return nil
	}
	return fmt.Errorf("native presubmit requires Linux PIDFD_SIGNAL_PROCESS_GROUP (kernel 6.9+); no numeric fallback: %w", err)
}

type linuxPresubmitGroup struct{ fd int }

func newPresubmitOwnedProcessGroup(pid int) (presubmitOwnedProcessGroup, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return nil, fmt.Errorf("open exact owned group pidfd before reap: %w", err)
	}
	if err = unix.PidfdSendSignal(fd, 0, nil, presubmitPidfdSignalProcessGroup); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("validate fresh owned process-group pidfd: %w", err)
	}
	return &linuxPresubmitGroup{fd: fd}, nil
}

func (g *linuxPresubmitGroup) kill() error {
	return unix.PidfdSendSignal(g.fd, unix.SIGKILL, nil, presubmitPidfdSignalProcessGroup)
}
func (g *linuxPresubmitGroup) drained() (bool, error) {
	err := unix.PidfdSendSignal(g.fd, 0, nil, presubmitPidfdSignalProcessGroup)
	if errors.Is(err, unix.ESRCH) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("observe exact pidfd process group: %w", err)
	}
	return false, nil
}
func (g *linuxPresubmitGroup) reapEarly() bool { return true }
func (g *linuxPresubmitGroup) close() error    { return unix.Close(g.fd) }
