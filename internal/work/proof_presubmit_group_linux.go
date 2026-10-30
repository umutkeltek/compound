//go:build linux

package work

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

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

// presubmitRecordedGroupMembers counts processes whose /proc stat names the
// journaled group. A recorded number has no pidfd, so it is never signalled.
// Every member counts, including zombies and an unrelated group that reused
// the number. Only a process that exits during the scan is skipped.
func presubmitRecordedGroupMembers(pgid int) (int, error) {
	if pgid <= 1 {
		return 0, fmt.Errorf("invalid recorded process group %d", pgid)
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, fmt.Errorf("list kernel process table: %w", err)
	}
	count := 0
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		stat, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ESRCH) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("read process %d stat: %w", pid, err)
		}
		// Fields after the command name: state, ppid, pgrp.
		end := bytes.LastIndexByte(stat, ')')
		fields := strings.Fields(string(stat[end+1:]))
		if end < 0 || len(fields) < 3 {
			return 0, fmt.Errorf("malformed process %d stat", pid)
		}
		group, err := strconv.Atoi(fields[2])
		if err != nil {
			return 0, fmt.Errorf("malformed process %d group: %w", pid, err)
		}
		if group == pgid {
			count++
		}
	}
	return count, nil
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
