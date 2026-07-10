package home

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// WithAutonomyAdmissionLock serializes the evidence snapshot and admission
// decision with the canonical writers that can change that decision: work
// lifecycle, decision grants, route evidence/router authority, and the halt.
// Advisory locks are released with the file descriptor after a crash.
// Compound's supported runtime targets are Darwin and Linux.
func WithAutonomyAdmissionLock(homePath string, fn func() error) error {
	homePath = strings.TrimSpace(homePath)
	if homePath == "" {
		return fmt.Errorf("COMPOUND_HOME required")
	}
	if fn == nil {
		return fmt.Errorf("autonomy admission lock callback required")
	}
	lockPath := filepath.Join(homePath, "lock", "autonomy-admission.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return fn()
}
