package eventstore

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// hotSegmentCache memoizes the parsed frames of HOT (last-written) segments for
// the lifetime of one process, keyed by the family's segment DIRECTORY — exactly
// one live entry per family. Interleaved point reads across families (a proof
// chain reads act_/pol_/rec_/tr_ in sequence per item) each keep their memo,
// while a day (or seq) rollover REPLACES the family's entry with the new hot
// segment instead of stranding the prior one. A single-slot cache thrashed to a
// 100% miss rate on the cross-family pattern (observed live 2026-07-16: 53.7MB
// hot transition segment × per-Get reparses = 158s CPU-bound attention); a
// path-keyed cache fixed the thrash but leaked one dead ~day-of-frames entry per
// family per rollover in long-lived processes (console supervisor RSS creep).
// Family-dir keying bounds the memo to families×1 regardless of process age.
// Sealed segments resolve through the sidecar index and never enter this cache.
//
// Stat-guarded: the entry records the hot segment path plus its size+mtime; a
// different path (rollover/rotation) or any size/mtime move (a concurrent
// append moves both) is a miss, so a reader never serves a frame set older than
// the file it just statted. regularKind reads bypass the cache — their rooted
// validation is part of the read contract.
//
// PACKAGE-level, not engine-scoped: real consumers (routeevidence's
// RecordTransition, most CLI verbs) call eventstore.Open per operation, so an
// engine-scoped memo dies with every call and a 5000-row all-dup import still
// full-parsed the hot segment once per row live (2026-07-17) while the
// single-store falsifier stayed green. Keys are absolute paths, so distinct
// homes in one process cannot collide, and the stat guard — not cache scope —
// is what keeps a shared entry correct.
type hotSegmentCache struct {
	mu      sync.Mutex
	entries map[string]hotSegmentEntry
}

var sharedHotCache hotSegmentCache

type hotSegmentEntry struct {
	path  string
	size  int64
	mtime time.Time
	byID  map[string]parsedFrame
	// divergent holds the ids that have intra-divergent duplicate frames in
	// this hot segment — an id whose occurrences do not all share the same
	// SHA256. nil (never an allocated empty map) in the healthy case, since
	// put() itself refuses to append a conflicting frame; only hand-crafted
	// or foreign-writer segments produce entries here. checkID's put-path hot
	// branch consults this to preserve base checkID's "scan every occurrence,
	// conflict on any hash mismatch" semantics from a single memo lookup
	// (see hotFramesAndDivergent).
	divergent map[string]struct{}
}

// segmentParseObserver is a test-only seam fired on every FULL parse of a hot
// (index-0) segment — both a memo-miss reparse here and the regularKind bypass
// in segmentFrame (scan.go). It is nil in production and never surfaces in the
// public API; a falsifier installs it to prove a projection's thousands of
// Gets collapse to one parse per hot segment (counter-seam precedent:
// work_13c545e392ed).
var segmentParseObserver func(path string)

func notifyHotParse(path string) {
	if segmentParseObserver != nil {
		segmentParseObserver(path)
	}
}

// hotFrames returns the id→frame map for the given hot segment, parsing the
// file only when that path's memo is absent or stale. Later duplicates
// overwrite earlier ones, matching segmentFrame's newest-occurrence-wins
// reverse scan.
func (e *engine) hotFrames(path string) (map[string]parsedFrame, error) {
	entry, err := e.hotSegmentEntryFor(path)
	if err != nil {
		return nil, err
	}
	return entry.byID, nil
}

// hotFramesAndDivergent is hotFrames plus the entry's divergent-id set, used
// by checkID's put-path hot branch: one memo lookup (fresh: pure map read;
// stale/cold: the same single full parse hotFrames would have done) yields
// everything checkID needs to reproduce base checkID's per-frame conflict
// scan without re-reading the segment itself.
func (e *engine) hotFramesAndDivergent(path string) (map[string]parsedFrame, map[string]struct{}, error) {
	entry, err := e.hotSegmentEntryFor(path)
	if err != nil {
		return nil, nil, err
	}
	return entry.byID, entry.divergent, nil
}

// hotSegmentFresh reports whether the memo already reflects path at its
// CURRENT size+mtime, without parsing. repairTail's happy-path interior sweep
// uses this to skip re-verifying bytes a prior hotSegmentEntryFor call in
// this same process already decoded successfully and that are stat-unchanged
// since (a hotSegmentEntry is only ever stored after every frame in the file
// decoded without error).
func (e *engine) hotSegmentFresh(path string) (bool, error) {
	st, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	key := filepath.Dir(path)
	sharedHotCache.mu.Lock()
	defer sharedHotCache.mu.Unlock()
	entry, ok := sharedHotCache.entries[key]
	return ok && entry.path == path && entry.size == st.Size() && entry.mtime.Equal(st.ModTime()), nil
}

// hotSegmentEntryFor returns the memoized entry for path's family directory,
// reparsing only when the memo is absent or its {path, size, mtime} stat
// guard no longer matches path's current stat. The returned entry (and its
// byID/divergent maps) is never mutated in place after being stored — callers
// read it outside the lock.
func (e *engine) hotSegmentEntryFor(path string) (hotSegmentEntry, error) {
	st, err := os.Stat(path)
	if err != nil {
		return hotSegmentEntry{}, err
	}
	key := filepath.Dir(path)
	sharedHotCache.mu.Lock()
	defer sharedHotCache.mu.Unlock()
	if entry, ok := sharedHotCache.entries[key]; ok && entry.path == path && entry.size == st.Size() && entry.mtime.Equal(st.ModTime()) {
		return entry, nil
	}
	notifyHotParse(path)
	frames, err := e.readSegment(path, true, "")
	if err != nil {
		return hotSegmentEntry{}, err
	}
	byID := make(map[string]parsedFrame, len(frames))
	var divergent map[string]struct{}
	for _, frame := range frames {
		if prev, ok := byID[frame.Frame.ID]; ok && prev.Frame.SHA256 != frame.Frame.SHA256 {
			if divergent == nil {
				divergent = make(map[string]struct{})
			}
			divergent[frame.Frame.ID] = struct{}{}
		}
		byID[frame.Frame.ID] = frame
	}
	entry := hotSegmentEntry{path: path, size: st.Size(), mtime: st.ModTime(), byID: byID, divergent: divergent}
	if sharedHotCache.entries == nil {
		sharedHotCache.entries = map[string]hotSegmentEntry{}
	}
	sharedHotCache.entries[key] = entry
	return entry, nil
}
