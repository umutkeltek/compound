package eventstore

import (
	"encoding/json"
	"testing"
)

// The hot-segment memo must never serve stale truth: an append moves the
// segment's size/mtime, and the very next point read must see the new record.
// This fails if hotFrames trusts its memo without the stat guard — and it
// also fails if the memo path cannot find records at all (the cache must be
// a pure accelerator, invisible to Get semantics).
func TestHotSegmentCacheServesFreshAppends(t *testing.T) {
	store := openTestStore(t)

	first := mustPutTestRecord(t, store, "tr_hotcache00001")
	rec, err := store.Read("tr_hotcache00001")
	if err != nil {
		t.Fatalf("first get: %v", err)
	}
	assertSameRecord(t, rec.Bytes, first)

	// Append a second record: size changes, memo must invalidate.
	second := mustPutTestRecord(t, store, "tr_hotcache00002")
	rec2, err := store.Read("tr_hotcache00002")
	if err != nil {
		t.Fatalf("get after append must see the new record (stale memo?): %v", err)
	}
	assertSameRecord(t, rec2.Bytes, second)

	// And the first record still resolves through the refreshed memo.
	recAgain, err := store.Read("tr_hotcache00001")
	if err != nil {
		t.Fatalf("re-get of first record: %v", err)
	}
	assertSameRecord(t, recAgain.Bytes, first)
}

func mustPutTestRecord(t *testing.T, store *Store, id string) []byte {
	t.Helper()
	record, err := json.Marshal(map[string]string{
		"id":             id,
		"schema_version": "compound.transition.v0.1",
		"created_at":     "2026-07-16T00:00:00Z",
		"note":           "hotcache falsifier " + id,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutJSON(id, json.RawMessage(record), ""); err != nil {
		t.Fatalf("put %s: %v", id, err)
	}
	return record
}

func assertSameRecord(t *testing.T, got []byte, want []byte) {
	t.Helper()
	var g, w map[string]any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if g["id"] != w["id"] || g["note"] != w["note"] {
		t.Fatalf("record mismatch: got %v want %v", g, w)
	}
}

// Interleaved point reads across FAMILIES must each keep their memo: a proof
// chain reads act_/pol_/rec_/tr_ in sequence per item, and a single-slot
// cache thrashes to a 100% miss rate on that pattern (the live 158s
// regression). This fails if hotFrames evicts one family's memo when another
// family is read.
func TestHotSegmentCacheSurvivesCrossFamilyInterleaving(t *testing.T) {
	store := openTestStore(t)
	mustPutTestRecord(t, store, "tr_interleave001")
	if _, err := store.PutJSON("pol_interleave01", json.RawMessage(`{"id":"pol_interleave01","schema_version":"compound.policy_decision.v0.1","created_at":"2026-07-16T00:00:00Z"}`), ""); err != nil {
		t.Fatal(err)
	}
	// Prime both family memos.
	if _, err := store.Read("tr_interleave001"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read("pol_interleave01"); err != nil {
		t.Fatal(err)
	}
	eng := store.engine
	eng.hotCache.mu.Lock()
	live := len(eng.hotCache.entries)
	eng.hotCache.mu.Unlock()
	if live < 2 {
		t.Fatalf("cross-family reads must keep one memo entry per family, got %d live entries (single-slot thrash)", live)
	}
}
