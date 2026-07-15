package eventstore

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const appendLockName = ".append.lock"

type appendLock struct {
	file *os.File
}

func acquireAppendLock(segRoot string) (*appendLock, error) {
	if err := os.MkdirAll(segRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create event segment root: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(segRoot, appendLockName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open event append lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock event append stream: %w", err)
	}
	return &appendLock{file: file}, nil
}

func (l *appendLock) release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return fmt.Errorf("unlock event append stream: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close event append lock: %w", closeErr)
	}
	return nil
}
