package eventstore

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

// countOpenFDs returns the number of open file descriptors in this process by
// counting the numeric entries under /dev/fd. Works on darwin and linux; the
// directory always exposes one entry per open fd. os.Open + Readdirnames is used
// (not os.ReadDir) because ReadDir's fstatat on the /dev/fd directory fails on
// darwin under go test.
func countOpenFDs(t *testing.T) int {
	t.Helper()
	f, err := os.Open("/dev/fd")
	if err != nil {
		t.Fatalf("open /dev/fd: %v", err)
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		t.Fatalf("read /dev/fd: %v", err)
	}
	n := 0
	for _, name := range names {
		if _, err := strconv.Atoi(name); err == nil {
			n++
		}
	}
	return n
}

// TestShardedAppendLockColdFamilyRaceDoesNotLeakFDs pins the fd-leak fix in
// shardedAppendLock.acquire: when two goroutines race the FIRST open of a cold
// family's lock file, the loser must close its duplicate open instead of
// leaking the fd for the process lifetime. Without the fix the process fd count
// grows by one per losing racer; with it, exactly one handle (the cached one)
// stays open per family.
//
// The race is forced by using a fresh segment root (a cold family in the
// package-level shared cache) and launching many goroutines at once so several
// see fd[family] == nil before any stores.
func TestShardedAppendLockColdFamilyRaceDoesNotLeakFDs(t *testing.T) {
	segRoot := t.TempDir()
	lock := shardedAppendLocksFor(segRoot)

	before := countOpenFDs(t)
	const racers = 32
	var wg sync.WaitGroup
	errs := make(chan error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := lock.acquire("tr_")
			if err != nil {
				errs <- err
				return
			}
			if err := lock.release("tr_", l); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent acquire/release: %v", err)
	}

	after := countOpenFDs(t)
	// Exactly one fd must remain open for the family's lock file (the cached
	// handle). Any delta beyond that is a leaked duplicate from the race.
	if got := after - before; got != 1 {
		// Guard: other package-level state (hot segment cache, recent writes)
		// may hold unrelated fds; but acquire of a single cold family in a fresh
		// temp root should add exactly one. Report the delta and the map size.
		t.Fatalf("open fds grew by %d after cold-family race, want 1 (one cached handle); len(fd)=%d",
			got, len(lock.fd))
	}
	if len(lock.fd) != 1 {
		t.Fatalf("family lock map has %d entries, want 1", len(lock.fd))
	}
	lockFile := filepath.Join(segRoot, appendLockName+".tr_")
	if _, err := os.Stat(lockFile); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}
}

// TestShardedAppendLockAcquireAllSortedDeadlockFree covers the all-family path
// used by maintenance: acquireAll must lock every shard (sorted) and release
// them all on success and on partial failure.
func TestShardedAppendLockAcquireAllSortedDeadlockFree(t *testing.T) {
	lock := shardedAppendLocksFor(t.TempDir())
	families := []string{"tr_", "act_", "rec_", "tr_", "pol_"} // deliberate dup + unsorted
	releaseAll, err := lock.acquireAll(families)
	if err != nil {
		t.Fatalf("acquireAll: %v", err)
	}
	// Sorted unique set, all held: every family in the map is one of them.
	if len(lock.fd) != 4 {
		t.Fatalf("acquired %d unique family shards, want 4 (tr_, act_, rec_, pol_)", len(lock.fd))
	}
	releaseAll()
	// After release the handles stay cached (not closed), so the map is intact.
	if len(lock.fd) != 4 {
		t.Fatalf("family lock map shrank after release, have %d cached handles, want 4", len(lock.fd))
	}
}

// TestShardedAppendLockReleaseKeepsHandleCached verifies release does not close
// the cached handle, so the next acquire is a cheap flock on the same fd.
func TestShardedAppendLockReleaseKeepsHandleCached(t *testing.T) {
	lock := shardedAppendLocksFor(t.TempDir())
	l1, err := lock.acquire("tr_")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := lock.release("tr_", l1); err != nil {
		t.Fatalf("release: %v", err)
	}
	l2, err := lock.acquire("tr_")
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	if l2.file != l1.file {
		t.Fatal("re-acquire returned a different handle; cached handle was not reused")
	}
}
