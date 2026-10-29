//go:build darwin

package work

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func presubmitGroupContainmentSupported() error { return nil }

// The command owner has not called Wait, so its direct child PID cannot be
// reused. Observing SZOMB does not reap that child. kern.proc.pgrp is an exact
// kernel group query, not a global process listing or stored-PID recovery.
func observePresubmitOwnedGroup(leaderPID int) (bool, error) {
	leader, err := unix.SysctlKinfoProc("kern.proc.pid", leaderPID)
	if err != nil {
		return false, fmt.Errorf("observe owned group leader: %w", err)
	}
	if int(leader.Proc.P_pid) != leaderPID || int(leader.Eproc.Ppid) != os.Getpid() || int(leader.Eproc.Pgid) != leaderPID {
		return false, fmt.Errorf("owned direct leader identity/group changed")
	}
	const darwinZombie = 5 // SZOMB in Darwin sys/proc.h.
	if leader.Proc.P_stat != darwinZombie {
		return false, nil
	}
	members, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", leaderPID)
	if err != nil {
		return false, fmt.Errorf("observe exact owned process group: %w", err)
	}
	for _, member := range members {
		if int(member.Eproc.Pgid) != leaderPID || member.Proc.P_pid <= 0 {
			return false, fmt.Errorf("exact group query returned an invalid or foreign member")
		}
		if int(member.Proc.P_pid) == leaderPID {
			if member.Proc.P_stat != darwinZombie {
				return false, fmt.Errorf("owned leader changed after zombie observation")
			}
			continue
		}
		// Even other zombies remain conservatively pending until their real parent
		// reaps them. A live member could otherwise fork after a partial sample.
		return false, nil
	}
	return true, nil
}

type darwinPresubmitGroup struct{ pid int }

func newPresubmitOwnedProcessGroup(pid int) (presubmitOwnedProcessGroup, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid owned group leader")
	}
	return &darwinPresubmitGroup{pid}, nil
}
func (g *darwinPresubmitGroup) kill() error            { return syscall.Kill(-g.pid, syscall.SIGKILL) }
func (g *darwinPresubmitGroup) drained() (bool, error) { return observePresubmitOwnedGroup(g.pid) }
func (g *darwinPresubmitGroup) reapEarly() bool        { return false }
func (g *darwinPresubmitGroup) close() error           { return nil }
