package gitrepository

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestIndexLockRecoverySerializesAtPinnedRepository(t *testing.T) {
	repo, now := indexLockFixture(t)
	state, err := InspectIndexLock(context.Background(), repo, now, idleIndexLock)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.Open(filepath.Join(repo, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := unix.Flock(int(dir.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer unix.Flock(int(dir.Fd()), unix.LOCK_UN)
	result, err := RecoverIndexLock(context.Background(), state, idleIndexLock, func() error { return nil })
	if err == nil || result.Changed || result.RecoveryDir != "" {
		t.Fatalf("concurrent repository recovery accepted: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git/index.lock")); err != nil {
		t.Fatal("concurrent refusal changed lock")
	}
}
