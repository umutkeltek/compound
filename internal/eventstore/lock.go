package eventstore

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
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

// shardedAppendLock is a per-family named lock so concurrent appends to
// DIFFERENT families (a proof chain writes act_, pol_, rec_, tr_ in sequence;
// hook events are their own family) do not serialize on one file. Prior to
// 2026-08-02 a single .append.lock flock guarded every put, and with ~29
// concurrent Claude sessions the hot PreToolUse path measured 11.4s/15.3s/41.5s
// per tool call under a 25-process queue on one inode (load 265). Sharding by
// family keeps the invariant the lock exists to protect — a family's segment
// is appended by at most one process at a time — while letting families make
// progress in parallel.
//
// PACKAGE-level fd cache, keyed by segment root: real consumers call
// eventstore.Open per operation (per tool call), so an engine-scoped fd map
// would open a fresh set of lock files per call and leak them in long-lived
// processes (the console supervisor, a drain daemon). Mirroring the hot segment
// cache, the map below holds ONE set of family lock fds per absolute segRoot
// for the process lifetime; the flock itself is cheap on an already-open fd.
type shardedAppendLock struct {
	dir string
	mu  sync.Mutex
	fd  map[string]*os.File
}

var sharedAppendLockCache = struct {
	mu     sync.Mutex
	byRoot map[string]*shardedAppendLock
}{byRoot: map[string]*shardedAppendLock{}}

// shardedAppendLocksFor returns the shared per-family lock set for a segment
// root, opening the first lock file lazily on acquire.
func shardedAppendLocksFor(segRoot string) *shardedAppendLock {
	sharedAppendLockCache.mu.Lock()
	defer sharedAppendLockCache.mu.Unlock()
	if l, ok := sharedAppendLockCache.byRoot[segRoot]; ok {
		return l
	}
	l := &shardedAppendLock{dir: segRoot, fd: map[string]*os.File{}}
	sharedAppendLockCache.byRoot[segRoot] = l
	return l
}

// acquire returns the same live file handle (held open) for a family, flocking
// it. The per-process map means one OPEN of each family's lock file, not one
// open+flock per put; the flock is what serializes across processes, and
// flock on an already-open fd is cheap.
func (s *shardedAppendLock) acquire(family string) (*appendLock, error) {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return nil, fmt.Errorf("create event segment root: %w", err)
	}
	s.mu.Lock()
	file := s.fd[family]
	s.mu.Unlock()
	if file == nil {
		var err error
		file, err = os.OpenFile(filepath.Join(s.dir, appendLockName+"."+family), os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return nil, fmt.Errorf("open event append lock: %w", err)
		}
		s.mu.Lock()
		s.fd[family] = file // first open wins; a racer closes its duplicate
		s.mu.Unlock()
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return nil, fmt.Errorf("lock event append stream: %w", err)
	}
	return &appendLock{file: file}, nil
}

// release unlocks the family's held file without closing it (the handle stays
// cached for the next append). Distinct from the plain appendLock.release,
// which closes — used by the non-sharded paths (rebaseline, scan) that open
// fresh each time.
func (s *shardedAppendLock) release(family string, l *appendLock) error {
	if l == nil || l.file == nil {
		return nil
	}
	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("unlock event append stream: %w", err)
	}
	return nil
}

// acquireAll locks every family shard in sorted order (deadlock-free across
// concurrent callers) and returns a release func. Maintenance paths that can
// rewrite more than one family's segments (engine.remove) use this so they stay
// mutually exclusive with put's per-family appends — a remove must never
// truncate a segment while a put appends to it.
func (s *shardedAppendLock) acquireAll(families []string) (func(), error) {
	names := append([]string(nil), families...)
	sort.Strings(names)
	type held struct {
		family string
		lock   *appendLock
	}
	var heldLocks []held
	releaseAll := func() {
		for i := len(heldLocks) - 1; i >= 0; i-- {
			_ = s.release(heldLocks[i].family, heldLocks[i].lock)
		}
	}
	for _, family := range names {
		l, err := s.acquire(family)
		if err != nil {
			releaseAll()
			return nil, err
		}
		heldLocks = append(heldLocks, held{family: family, lock: l})
	}
	return releaseAll, nil
}
