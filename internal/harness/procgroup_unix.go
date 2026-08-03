//go:build unix

package harness

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the subprocess in its own process group so the timeout
// kill can reach the WHOLE tree, not just the direct child. Without it an
// orphaned grandchild that inherited the stdout/stderr pipes keeps them open
// after the child dies: capture never sees EOF, capWG.Wait blocks forever,
// and the fresh heartbeats hide the stall from SweepStale.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessTree SIGKILLs the subprocess's whole process group. With
// Setpgid the child is its group leader (pgid == pid), so the negative-pid
// kill reaches every descendant holding the capture pipes. Falls back to
// killing only the direct child when the group signal fails (e.g. the group
// is already gone because the child exited between the timer firing and the
// kill).
func killProcessTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
