package eventstore

import (
	"os"
	"sync"
	"time"
)

// hotSegmentCache memoizes the parsed frames of HOT (last-written) segments
// for the lifetime of one process, keyed by segment path — one live entry per
// family, so interleaved point reads across families (a proof chain reads
// act_/pol_/rec_/tr_ in sequence per item) all keep their memo. A single-slot
// version of this cache thrashed to a 100% miss rate on exactly that pattern
// (observed live 2026-07-16: 53.7MB hot transition segment × per-Get reparses
// = 158s CPU-bound attention). Sealed segments resolve through the sidecar
// index and never enter this cache.
//
// Stat-guarded: any size or mtime change (a concurrent append moves both)
// discards that path's memo, so a reader never serves a frame set older than
// the file it just statted. regularKind reads bypass the cache — their rooted
// validation is part of the read contract.
type hotSegmentCache struct {
	mu      sync.Mutex
	entries map[string]hotSegmentEntry
}

type hotSegmentEntry struct {
	size  int64
	mtime time.Time
	byID  map[string]parsedFrame
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
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	e.hotCache.mu.Lock()
	defer e.hotCache.mu.Unlock()
	if entry, ok := e.hotCache.entries[path]; ok && entry.size == st.Size() && entry.mtime.Equal(st.ModTime()) {
		return entry.byID, nil
	}
	notifyHotParse(path)
	frames, err := e.readSegment(path, true, "")
	if err != nil {
		return nil, err
	}
	byID := make(map[string]parsedFrame, len(frames))
	for _, frame := range frames {
		byID[frame.Frame.ID] = frame
	}
	if e.hotCache.entries == nil {
		e.hotCache.entries = map[string]hotSegmentEntry{}
	}
	e.hotCache.entries[path] = hotSegmentEntry{size: st.Size(), mtime: st.ModTime(), byID: byID}
	return byID, nil
}
