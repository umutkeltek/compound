package corpus

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const shardReaderProtocol = "compound.corpus.shard-reader.v1"
const shardAccessLockName = ".shard-access.lock"

// The writer's long refresh lease serializes producers. This independent gate
// protects only manifest-to-shard loading and short collection, so queries can
// keep reading the committed generation while a provider call is in flight.
func ensureShardAccessGate(dir string) error {
	path := filepath.Join(dir, shardAccessLockName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return validateShardGate(path, f)
}

func validateShardGate(path string, f *os.File) error {
	named, err := os.Lstat(path)
	if err != nil {
		return err
	}
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !named.Mode().IsRegular() || !os.SameFile(named, opened) {
		return fmt.Errorf("corpus shard access gate is linked or replaced")
	}
	return nil
}

// Read-only consumers never create a lock. An absent gate is a legacy layout:
// compaction must remain disabled until a disabled writer has initialized the
// gate and all old/unleased consumers have been reloaded and independently read back.
func acquireShardRead(ctx context.Context, dir string) (func(), error) {
	path := filepath.Join(dir, shardAccessLockName)
	if ctx == nil {
		ctx = context.Background()
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if os.IsNotExist(err) {
		return func() {}, nil
	}
	if err != nil {
		return nil, err
	}
	if err = validateShardGate(path, f); err != nil {
		f.Close()
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// Collection never waits behind readers. A busy read is retained debt, not an
// invitation to unlink a path that the sampled manifest still needs.
func tryShardCollection(dir string) (func(), bool, error) {
	path := filepath.Join(dir, shardAccessLockName)
	f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, false, err
	}
	if err = validateShardGate(path, f); err != nil {
		f.Close()
		return nil, false, err
	}
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
		f.Close()
		return nil, true, nil
	}
	if err != nil {
		f.Close()
		return nil, false, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, false, nil
}
