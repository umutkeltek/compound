//go:build unix

package hook

import (
	"os"
	"syscall"
)

// lockSpoolFile takes an exclusive advisory lock. wait=false fails at once
// when another process holds it.
func lockSpoolFile(file *os.File, wait bool) error {
	how := syscall.LOCK_EX
	if !wait {
		how |= syscall.LOCK_NB
	}
	return syscall.Flock(int(file.Fd()), how)
}

func unlockSpoolFile(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
