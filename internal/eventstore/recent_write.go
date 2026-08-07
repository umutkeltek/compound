package eventstore

import (
	"sync"
)

// recentWriteCache memoizes, in-process, the ids most recently appended to each
// family's HOT segment, together with each id's frame SHA256. Its purpose is the
// O(1) dedup fast path for put(): the same hook event (PostToolUse) fires once
// per tool call across concurrent lanes, and a dedup checkID against the hot
// segment is a cold full-parse (measured: an 8MB hot hook segment reparsed per
// put). Checking the recent cache first means an id appended moments ago by this
// process is rejected as already-present without touching the segment at all.
// Storing the hash keeps the fast path EXACT: a same-id-different-content put
// (a genuine conflict) does not match and falls through to the durable checkID
// scan, which errors.
//
// The cache is an authority for the decision it makes: a HIT rejects the put as
// already-present without consulting the store. The conservative direction is
// the MISS — cold start, another process's append, a rollover, or an eviction
// all fall through to the durable checkID path the engine already took, so the
// cache can only ever under-report writes, never invent one. The one accepted
// limitation is cross-process remove: only the process that performed a durable
// remove forgets that id (scan.go), so a foreign process that removes an id this
// process appended is invisible here, and a same-id+same-hash re-put after that
// foreign remove is mis-deduped as already-present. Remove-then-re-put of the
// same id with byte-identical content is rare, and the false positive is a
// created:false no-op, never data corruption — the cost of a durable probe on
// the hot path.
//
// The cache is keyed by STORE ROOT + family so distinct homes in one process
// (the test suite, a console supervisor) cannot collide, and bounded per family
// so one hot family (hook) cannot evict another (transition) that is only read
// in the same process. It is package-level (like the hot segment cache it
// complements) because real consumers call eventstore.Open per operation; an
// engine-scoped map would die with every call and fast-path nothing.
type recentWriteCache struct {
	mu    sync.Mutex
	byKey map[string]map[string]string // storeRoot+"\x00"+family -> id -> sha256
	order map[string][]string          // same key -> insertion order, LRU trimming
	max   int
}

var sharedRecentWrites = newRecentWriteCache(4096)

func newRecentWriteCache(max int) *recentWriteCache {
	if max <= 0 {
		max = 4096
	}
	return &recentWriteCache{
		byKey: map[string]map[string]string{},
		order: map[string][]string{},
		max:   max,
	}
}

// remember records id as recently appended to family of the store rooted at
// storeRoot, with its frame SHA256. Bounded: when a key's bucket exceeds max/2
// entries, the oldest half is evicted so the hot family's bucket stays small and
// the hint stays O(1).
func (c *recentWriteCache) remember(storeRoot, family, id, sha256 string) {
	if storeRoot == "" || family == "" || id == "" {
		return
	}
	key := storeRoot + "\x00" + family
	c.mu.Lock()
	defer c.mu.Unlock()
	byID, ok := c.byKey[key]
	if !ok {
		byID = map[string]string{}
		c.byKey[key] = byID
	}
	if _, dup := byID[id]; dup {
		return
	}
	byID[id] = sha256
	order := c.order[key]
	if len(order) >= c.max/2 {
		// Evict the oldest half before inserting so a burst can't double the
		// cap between trims.
		keep := order[len(order)-len(order)/2:]
		for _, old := range order[:len(order)-len(keep)] {
			delete(byID, old)
		}
		order = keep
	}
	c.order[key] = append(order, id)
}

// seen reports whether id was recently appended by this process to family of the
// store rooted at storeRoot with exactly the given frame SHA256. A hash mismatch
// is a miss (falls through to checkID), so the fast path can never mask a
// genuine content conflict.
func (c *recentWriteCache) seen(storeRoot, family, id, sha256 string) bool {
	if storeRoot == "" || family == "" || id == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	prior, ok := c.byKey[storeRoot+"\x00"+family][id]
	return ok && prior == sha256
}

// forget drops id from the recent-write view of a store/family. Called after a
// durable remove so a later re-put of a deleted id is NOT short-circuited as
// "already present" — the cache must never disagree with the store about an id
// the store no longer holds.
func (c *recentWriteCache) forget(storeRoot, family, id string) {
	if storeRoot == "" || family == "" || id == "" {
		return
	}
	key := storeRoot + "\x00" + family
	c.mu.Lock()
	defer c.mu.Unlock()
	byID := c.byKey[key]
	if byID == nil {
		return
	}
	if _, ok := byID[id]; !ok {
		return
	}
	delete(byID, id)
	order := c.order[key]
	for i, oid := range order {
		if oid == id {
			c.order[key] = append(order[:i], order[i+1:]...)
			break
		}
	}
	if len(byID) == 0 {
		delete(c.byKey, key)
		delete(c.order, key)
	}
}

// resetSharedRecentWrites clears the entire recent-write view. Test-only: lets a
// falsifier measure the durable dedup path (sealed index / hot memo) without the
// in-process fast-path short-circuiting the re-put it is counting.
func resetSharedRecentWrites() {
	sharedRecentWrites.mu.Lock()
	defer sharedRecentWrites.mu.Unlock()
	sharedRecentWrites.byKey = map[string]map[string]string{}
	sharedRecentWrites.order = map[string][]string{}
}
