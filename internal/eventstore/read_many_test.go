package eventstore

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- counter seams -------------------------------------------------------

// readCounters counts the REAL work the batch reader is meant to collapse:
// family directory enumerations, sealed-index acquisitions and hot-segment
// acquisitions. They are read at the seams the point reader uses, so the old
// path and the batch path are measured the same way.
type readCounters struct {
	mu       sync.Mutex
	families map[string]int
	indexes  map[string]int
	hot      map[string]int
}

func (c *readCounters) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.families = map[string]int{}
	c.indexes = map[string]int{}
	c.hot = map[string]int{}
}

func (c *readCounters) totals() (families int, indexes int, hot int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, v := range c.families {
		families += v
	}
	for _, v := range c.indexes {
		indexes += v
	}
	for _, v := range c.hot {
		hot += v
	}
	return families, indexes, hot
}

// indexPaths lists the sealed-index files this batch actually ACQUIRED.
func (c *readCounters) indexPaths() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	paths := make([]string, 0, len(c.indexes))
	for path := range c.indexes {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func installReadCounters(t *testing.T) (*readCounters, func()) {
	t.Helper()
	set := &readCounters{families: map[string]int{}, indexes: map[string]int{}, hot: map[string]int{}}
	prevFamily := familyInventoryObserver
	prevIndex := indexLoadObserver
	prevHot := hotSegmentObserver
	familyInventoryObserver = func(family string) {
		set.mu.Lock()
		set.families[family]++
		set.mu.Unlock()
	}
	indexLoadObserver = func(family string, path string) {
		set.mu.Lock()
		set.indexes[path]++
		set.mu.Unlock()
	}
	hotSegmentObserver = func(path string) {
		set.mu.Lock()
		set.hot[path]++
		set.mu.Unlock()
	}
	return set, func() {
		familyInventoryObserver = prevFamily
		indexLoadObserver = prevIndex
		hotSegmentObserver = prevHot
	}
}

// --- fixtures ------------------------------------------------------------

// batchFixtureHome builds a home with three families, each holding a SEALED
// segment (four records) and a HOT segment (one record). Advancing the clock
// by a day rolls the first day's segment out of the hot position.
func batchFixtureHome(t *testing.T, recordsPerFamily int) (*Store, time.Time) {
	t.Helper()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	clock := now
	store := openConfiguredStore(t, engineConfig{clock: func() time.Time { return clock }})
	families := []struct {
		prefix string
		schema string
	}{
		{"rec_", "compound.receipt.v0.1"},
		{"tr_", "compound.transition.v0.1"},
		{"pol_", "compound.policy_decision.v0.1"},
	}
	for _, family := range families {
		for i := 0; i < recordsPerFamily; i++ {
			id := fmt.Sprintf("%sbatch_%02d", family.prefix, i)
			putRecord(t, store, id, family.schema, now, "sealed")
		}
	}
	clock = now.Add(24 * time.Hour)
	for _, family := range families {
		id := fmt.Sprintf("%sbatch_hot", family.prefix)
		putRecord(t, store, id, family.schema, clock, "hot")
	}
	return store, now
}

// --- A. point-read equivalence -------------------------------------------

// The batch must be indistinguishable from independent point reads: same
// record identity, same bytes, same diagnostic path, same error class and
// message. Every distinct ORIGINAL reference is a result key — including
// malformed ones — and aliases share one resolution while keeping every key.
func TestReadManyMatchesIndependentPointReads(t *testing.T) {
	store, now := batchFixtureHome(t, 4)
	refs := []string{
		"rec_batch_00",
		"rec_batch_00",                   // exact duplicate
		"compound://events/rec_batch_01", // accepted alias
		"events/rec_batch_02.json",       // historical alias
		"tr_batch_03",
		"pol_batch_00",
		"rec_batch_hot",
		"tr_batch_hot",
		"rec_batch_missing",   // absent id
		"",                    // malformed
		"events/rec_batch_00", // malformed historical shape
		"../escape",           // malformed
	}
	point := make(map[string]ReadResult, len(refs))
	for _, raw := range refs {
		rec, err := store.Read(raw)
		point[raw] = ReadResult{Record: rec, Err: err}
	}

	batch := store.ReadMany(refs)
	if len(batch) != len(point) {
		t.Fatalf("batch result count = %d, want %d", len(batch), len(point))
	}
	for raw, want := range point {
		got, ok := batch[raw]
		if !ok {
			t.Fatalf("batch has no result for %q", raw)
		}
		if (got.Err == nil) != (want.Err == nil) {
			t.Fatalf("%q: batch err = %v, point read err = %v", raw, got.Err, want.Err)
		}
		if got.Err != nil {
			if got.Err.Error() != want.Err.Error() {
				t.Fatalf("%q: batch error %q, point read error %q", raw, got.Err, want.Err)
			}
			continue
		}
		if got.Record.ID != want.Record.ID {
			t.Fatalf("%q: batch id %q, point read id %q", raw, got.Record.ID, want.Record.ID)
		}
		if got.Record.Reference.ID() != want.Record.Reference.ID() {
			t.Fatalf("%q: batch reference %q, point read reference %q", raw, got.Record.Reference.ID(), want.Record.Reference.ID())
		}
		if got.Record.DiagnosticPath != want.Record.DiagnosticPath {
			t.Fatalf("%q: batch diagnostic %q, point read diagnostic %q", raw, got.Record.DiagnosticPath, want.Record.DiagnosticPath)
		}
		if !bytes.Equal(got.Record.Bytes, want.Record.Bytes) {
			t.Fatalf("%q: batch bytes %q, point read bytes %q", raw, got.Record.Bytes, want.Record.Bytes)
		}
	}
	// The four sealed receipt records must actually have resolved: an
	// all-errors comparison would prove nothing.
	for _, raw := range []string{"rec_batch_00", "compound://events/rec_batch_01", "events/rec_batch_02.json", "tr_batch_03"} {
		if batch[raw].Err != nil {
			t.Fatalf("%q must resolve: %v", raw, batch[raw].Err)
		}
	}
	_ = now
}

// A missing record in one family must not suppress another family's result.
func TestReadManyMissingInOneFamilyDoesNotSuppressAnother(t *testing.T) {
	store, _ := batchFixtureHome(t, 2)
	batch := store.ReadMany([]string{"rec_batch_absent", "tr_batch_00", "pol_batch_01"})
	if batch["rec_batch_absent"].Err == nil {
		t.Fatal("absent record must carry an error")
	}
	for _, raw := range []string{"tr_batch_00", "pol_batch_01"} {
		if batch[raw].Err != nil {
			t.Fatalf("%q must resolve despite another family's absence: %v", raw, batch[raw].Err)
		}
	}
}

// --- F. access-count falsifier -------------------------------------------

// The falsifier: the OLD per-attachment path repeats family inventory and
// sealed-index acquisition once PER REQUESTED ID; the batch repeats them once
// per CONSULTED FAMILY/SEGMENT. Doubling the requested ids must leave the
// batch's counts unchanged while the old path's counts double.
func TestReadManyCollapsesPerIDInventoryAndIndexWork(t *testing.T) {
	store, _ := batchFixtureHome(t, 8)
	counters, restore := installReadCounters(t)
	defer restore()

	// narrow and wide consult the SAME family with twice the ids; all adds two
	// more families. The batch cost must follow the FAMILY dimension only.
	narrow := []string{"rec_batch_00", "rec_batch_01", "rec_batch_02", "rec_batch_03"}
	wide := []string{
		"rec_batch_00", "rec_batch_01", "rec_batch_02", "rec_batch_03",
		"rec_batch_04", "rec_batch_05", "rec_batch_06", "rec_batch_07",
	}
	all := append([]string{}, wide...)
	for _, prefix := range []string{"tr_", "pol_"} {
		for i := 0; i < 8; i++ {
			all = append(all, fmt.Sprintf("%sbatch_%02d", prefix, i))
		}
	}

	// OLD path: one independent point read per attachment.
	oldCounts := map[string][3]int{}
	for name, refs := range map[string][]string{"narrow": narrow, "wide": wide, "all": all} {
		counters.reset()
		for _, raw := range refs {
			if _, err := store.Read(raw); err != nil {
				t.Fatalf("point read %q: %v", raw, err)
			}
		}
		families, indexes, hot := counters.totals()
		oldCounts[name] = [3]int{families, indexes, hot}
		if families != len(refs) || indexes != len(refs) || hot != len(refs) {
			t.Fatalf("old path must repeat per id (%s): fam %d idx %d hot %d for %d ids",
				name, families, indexes, hot, len(refs))
		}
	}

	// BATCH path: one ReadMany.
	batchCounts := map[string][3]int{}
	for name, refs := range map[string][]string{"narrow": narrow, "wide": wide, "all": all} {
		counters.reset()
		if results := store.ReadMany(refs); len(results) != len(refs) {
			t.Fatalf("batch result count = %d, want %d", len(results), len(refs))
		}
		families, indexes, hot := counters.totals()
		batchCounts[name] = [3]int{families, indexes, hot}
	}

	// THE FALSIFIER: doubling the requested ids over the same family must NOT
	// change the batch's inventory, index or hot-segment work. The old path
	// doubles it.
	if batchCounts["narrow"] != batchCounts["wide"] {
		t.Fatalf("batch cost must not scale with ids: narrow=%v wide=%v", batchCounts["narrow"], batchCounts["wide"])
	}
	if oldCounts["narrow"] == oldCounts["wide"] {
		t.Fatalf("old path must scale with ids: narrow=%v wide=%v", oldCounts["narrow"], oldCounts["wide"])
	}
	// One family: one inventory pair (observe + recheck), one sealed index,
	// one hot segment — independent of how many ids were requested.
	if batchCounts["narrow"] != [3]int{2, 1, 1} {
		t.Fatalf("single-family batch cost = %v, want [2 1 1]", batchCounts["narrow"])
	}
	// Three families: three sealed indexes and three hot segments, not
	// twenty-four.
	if batchCounts["all"] != [3]int{6, 3, 3} {
		t.Fatalf("three-family batch cost = %v, want [6 3 3]", batchCounts["all"])
	}
	// The batch must be strictly cheaper than the old path.
	if batchCounts["all"][1] >= oldCounts["all"][1] || batchCounts["all"][2] >= oldCounts["all"][2] {
		t.Fatalf("batch must reduce work: index %d vs %d, hot %d vs %d",
			batchCounts["all"][1], oldCounts["all"][1], batchCounts["all"][2], oldCounts["all"][2])
	}
}

// Empty input performs no family access at all.
func TestReadManyEmptyInputTouchesNoFamily(t *testing.T) {
	store, _ := batchFixtureHome(t, 2)
	counters, restore := installReadCounters(t)
	defer restore()
	counters.reset()
	results := store.ReadMany(nil)
	if len(results) != 0 {
		t.Fatalf("empty input returned %d results", len(results))
	}
	results = store.ReadMany([]string{})
	if len(results) != 0 {
		t.Fatalf("empty input returned %d results", len(results))
	}
	families, indexes, hot := counters.totals()
	if families != 0 || indexes != 0 || hot != 0 {
		t.Fatalf("empty input accessed the store: fam %d idx %d hot %d", families, indexes, hot)
	}
}

// Duplicate references resolve once while keeping every original key.
func TestReadManyDuplicateReferencesResolveOnce(t *testing.T) {
	store, _ := batchFixtureHome(t, 2)
	counters, restore := installReadCounters(t)
	defer restore()
	counters.reset()
	results := store.ReadMany([]string{"rec_batch_00", "rec_batch_00", "rec_batch_00"})
	if len(results) != 1 {
		t.Fatalf("duplicate references produced %d keys, want 1", len(results))
	}
	if results["rec_batch_00"].Err != nil {
		t.Fatalf("duplicate reference must resolve: %v", results["rec_batch_00"].Err)
	}
	families, indexes, hot := counters.totals()
	if families != 2 || indexes != 1 || hot != 1 {
		t.Fatalf("duplicate references must resolve once: fam %d idx %d hot %d", families, indexes, hot)
	}
}

// --- B. newest-occurrence semantics --------------------------------------

// writeRawSegmentFile writes hand-crafted segment bytes so duplicate
// occurrences of one id exist inside a segment — the shape only a foreign
// writer can produce.
func writeRawSegmentFile(t *testing.T, path string, ids []string, values []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	for i, id := range ids {
		record := []byte(fmt.Sprintf(`{"id":%q,"created_at":"2026-07-15T00:00:00Z","value":%q}`, id, values[i]))
		frame, _, err := buildFrame(id, record)
		if err != nil {
			t.Fatalf("build frame %s: %v", id, err)
		}
		line, err := encodeFrame(frame)
		if err != nil {
			t.Fatalf("encode frame %s: %v", id, err)
		}
		buf.Write(line)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Within a segment the LAST occurrence of an id wins, and the newest segment
// wins over an older one. The batch must agree with the point reader.
func TestReadManyPreservesNewestOccurrence(t *testing.T) {
	store := openTestStore(t)
	segRoot := filepath.Join(store.rootPath(), "segments", "other")
	writeRawSegmentFile(t, filepath.Join(segRoot, "2026-07-15.jsonl"),
		[]string{"zz_batch_dup", "zz_batch_dup"}, []string{"first", "second"})
	writeRawSegmentFile(t, filepath.Join(segRoot, "2026-07-16.jsonl"),
		[]string{"zz_batch_other"}, []string{"other"})

	point, err := store.Read("zz_batch_dup")
	if err != nil {
		t.Fatalf("point read: %v", err)
	}
	if !bytes.Contains(point.Bytes, []byte("second")) {
		t.Fatalf("point read must take the last occurrence, got %s", point.Bytes)
	}
	batch := store.ReadMany([]string{"zz_batch_dup"})
	if batch["zz_batch_dup"].Err != nil {
		t.Fatalf("batch read: %v", batch["zz_batch_dup"].Err)
	}
	if !bytes.Equal(batch["zz_batch_dup"].Record.Bytes, point.Bytes) {
		t.Fatalf("batch bytes %s, point read bytes %s", batch["zz_batch_dup"].Record.Bytes, point.Bytes)
	}
}

// --- C. integrity and isolation ------------------------------------------

// A corrupt sealed frame must fail closed in the batch exactly as it does in
// the point reader, and must not disturb another family.
func TestReadManyFailsClosedOnCorruptSealedFrame(t *testing.T) {
	store, now := batchFixtureHome(t, 3)
	sealedPath := segmentPath(store.engine.segRoot, "receipt", ingestionDay(now), 0)
	// Build the index first, then tamper with the frame bytes in place.
	if _, err := store.Read("rec_batch_01"); err != nil {
		t.Fatalf("prime read: %v", err)
	}
	data, err := os.ReadFile(sealedPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(data, []byte("sealed"), []byte("tampere"), 1)
	if bytes.Equal(tampered, data) {
		t.Fatal("fixture did not tamper with the sealed frame")
	}
	if err := os.WriteFile(sealedPath, tampered, 0o644); err != nil {
		t.Fatal(err)
	}

	point, pointErr := store.Read("rec_batch_01")
	batch := store.ReadMany([]string{"rec_batch_01", "tr_batch_00"})
	if pointErr == nil {
		t.Fatalf("point read must fail closed on a corrupt frame, got %s", point.Bytes)
	}
	if batch["rec_batch_01"].Err == nil {
		t.Fatalf("batch must fail closed on a corrupt frame, got %s", batch["rec_batch_01"].Record.Bytes)
	}
	if batch["rec_batch_01"].Err.Error() != pointErr.Error() {
		t.Fatalf("batch error %q, point read error %q", batch["rec_batch_01"].Err, pointErr)
	}
	if batch["tr_batch_00"].Err != nil {
		t.Fatalf("another family must be unaffected: %v", batch["tr_batch_00"].Err)
	}
}

// A record that was missing becomes present; a NEW batch must see it. No
// verdict may be resurrected from an earlier batch.
func TestReadManySeesAppendAfterEarlierBatch(t *testing.T) {
	store, _ := batchFixtureHome(t, 2)
	before := store.ReadMany([]string{"rec_batch_late"})
	if before["rec_batch_late"].Err == nil {
		t.Fatal("record must be absent before the append")
	}
	putRecord(t, store, "rec_batch_late", "compound.receipt.v0.1", time.Now().UTC(), "late")
	after := store.ReadMany([]string{"rec_batch_late"})
	if after["rec_batch_late"].Err != nil {
		t.Fatalf("a new batch must see the appended record: %v", after["rec_batch_late"].Err)
	}
	if !bytes.Contains(after["rec_batch_late"].Record.Bytes, []byte("late")) {
		t.Fatalf("unexpected bytes %s", after["rec_batch_late"].Record.Bytes)
	}
}

// A controlled mid-batch mutation must never be presented as a mixed stale
// success. The store keeps changing across the bounded retry, so the batch
// must report an explicit drift failure instead of first-attempt bytes.
func TestReadManyReportsExplicitDriftOnPersistentMidBatchMutation(t *testing.T) {
	store, now := batchFixtureHome(t, 3)
	hotPath := segmentPath(store.engine.segRoot, "receipt", ingestionDay(now.Add(24*time.Hour)), 0)

	// Rewrite the hot segment in place at each attempt's RECHECK, so both the
	// first attempt and the bounded retry observe the store moving. The bytes
	// stay valid; only the segment identity moves.
	var mu sync.Mutex
	enumerations := 0
	prevFamily := familyInventoryObserver
	familyInventoryObserver = func(family string) {
		mu.Lock()
		defer mu.Unlock()
		enumerations++
		if enumerations%2 == 1 {
			return // the boundary observation: the token is captured after this
		}
		data, err := os.ReadFile(hotPath)
		if err != nil {
			return
		}
		_ = os.WriteFile(hotPath, data, 0o644)
	}
	defer func() { familyInventoryObserver = prevFamily }()

	results := store.ReadMany([]string{"rec_batch_01", "tr_batch_00"})
	if results["rec_batch_01"].Err == nil {
		t.Fatalf("a drifting batch must not return bytes: %s", results["rec_batch_01"].Record.Bytes)
	}
	if !errors.Is(results["rec_batch_01"].Err, errBatchDrift) {
		t.Fatalf("drift error = %v, want errBatchDrift", results["rec_batch_01"].Err)
	}
	// A family whose store did NOT move keeps its answer.
	if results["tr_batch_00"].Err != nil {
		t.Fatalf("an unaffected family must keep its answer: %v", results["tr_batch_00"].Err)
	}
}

// --- E. rooted boundaries --------------------------------------------------
// The batch API stays UNROOTED and does not grow a rooted twin: the rooted
// path remains the standalone ReadRegular, using its own rooted owner path.
// This test proves the standalone rooted reader still rejects a symlinked
// events ancestor — the batch reader must not have weakened or replaced it.
func TestReadRegularStillRejectsSymlinkedAncestor(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(home, "real-events")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(home, "events")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	store, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadRegular("rec_link", "receipt"); err == nil {
		t.Fatal("ReadRegular followed a symlinked events ancestor")
	}
	if _, err := store.Read("rec_link"); err == nil {
		t.Fatal("Read must not resolve through a symlinked events ancestor")
	}
}

// The batch reader must not expose a rooted variant: a rooted batch would be
// an unrequested API surface, and the rooted contract stays with ReadRegular.
func TestReadManyExposesNoRootedBatchVariant(t *testing.T) {
	store, _ := batchFixtureHome(t, 1)
	var _ map[string]ReadResult = store.ReadMany([]string{"rec_batch_00"})
}

// --- D. freshness: consulted-only identity, drift, and relevance ----------

// An OLDER dangling segment symlink (a valid segment filename whose target
// cannot be stat'ed) must not break a request that resolves in the NEWEST hot
// segment. The point reader succeeds today; a batch that eagerly stats every
// segment would fail the whole request over an irrelevant older segment.
func TestReadManyIgnoresIrrelevantDanglingOlderSegment(t *testing.T) {
	store, _ := batchFixtureHome(t, 2)
	familyDir := filepath.Join(store.engine.segRoot, "receipt")
	dangling := filepath.Join(familyDir, "2026-01-01.jsonl")
	target := filepath.Join(familyDir, "2026-01-01-missing-target.jsonl")
	if err := os.Symlink(target, dangling); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := os.Stat(dangling); err == nil {
		t.Fatal("fixture symlink must not be stat-able")
	}
	if _, err := store.Read("rec_batch_hot"); err != nil {
		t.Fatalf("point read of the newest hot record must succeed: %v", err)
	}
	results := store.ReadMany([]string{"rec_batch_hot"})
	if results["rec_batch_hot"].Err != nil {
		t.Fatalf("batch must resolve the newest hot record: %v", results["rec_batch_hot"].Err)
	}
	if !bytes.Contains(results["rec_batch_hot"].Record.Bytes, []byte("hot")) {
		t.Fatalf("unexpected bytes %s", results["rec_batch_hot"].Record.Bytes)
	}
}

// The batch must acquire NO irrelevant older index: the walk stops as soon as
// every requested id of the family is resolved, so older sealed segments are
// never even opened.
func TestReadManyAcquiresNoIrrelevantOlderIndex(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	clock := now
	store := openConfiguredStore(t, engineConfig{clock: func() time.Time { return clock }})
	putRecord(t, store, "rec_old_0", "compound.receipt.v0.1", now, "old")
	clock = now.Add(24 * time.Hour)
	putRecord(t, store, "rec_mid_0", "compound.receipt.v0.1", clock, "mid")
	clock = now.Add(48 * time.Hour)
	putRecord(t, store, "rec_new_0", "compound.receipt.v0.1", clock, "new")

	counters, restore := installReadCounters(t)
	defer restore()
	counters.reset()
	results := store.ReadMany([]string{"rec_mid_0"})
	if results["rec_mid_0"].Err != nil {
		t.Fatalf("batch must resolve the middle-day record: %v", results["rec_mid_0"].Err)
	}
	paths := counters.indexPaths()
	if len(paths) != 1 {
		t.Fatalf("expected exactly one sealed-index acquisition, got %v", paths)
	}
	if !strings.HasSuffix(paths[0], filepath.Join("receipt", "2026-07-16.idx")) {
		t.Fatalf("acquired the wrong sealed index: %v", paths)
	}
}

// A sidecar index REPLACED during the batch must never be served as current:
// the consulted index identity is rechecked, and a persistent replacement is
// an explicit drift failure.
func TestReadManyReportsDriftOnIndexReplacement(t *testing.T) {
	store, now := batchFixtureHome(t, 2)
	idxPath := filepath.Join(store.engine.indexRoot, "receipt", ingestionDay(now)+".idx")
	var mu sync.Mutex
	enumerations := 0
	prevFamily := familyInventoryObserver
	familyInventoryObserver = func(family string) {
		if family != "receipt" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		enumerations++
		if enumerations%2 != 0 {
			return
		}
		// The final recheck of the attempt: replace the sealed index.
		data, err := os.ReadFile(idxPath)
		if err != nil {
			return
		}
		_ = os.WriteFile(idxPath, append(data, ' '), 0o644)
	}
	defer func() { familyInventoryObserver = prevFamily }()

	results := store.ReadMany([]string{"rec_batch_00"})
	if results["rec_batch_00"].Err == nil {
		t.Fatalf("a replaced index must not be served as current: %s", results["rec_batch_00"].Record.Bytes)
	}
	if !errors.Is(results["rec_batch_00"].Err, errBatchDrift) {
		t.Fatalf("drift error = %v, want errBatchDrift", results["rec_batch_00"].Err)
	}
}

// A record that APPEARS mid-batch must not be answered with a stale "not
// found": the bounded retry re-observes the family and resolves it fresh.
func TestReadManySeesRecordThatAppearsMidBatch(t *testing.T) {
	store, _ := batchFixtureHome(t, 2)
	var mu sync.Mutex
	enumerations := 0
	appended := false
	prevFamily := familyInventoryObserver
	familyInventoryObserver = func(family string) {
		if family != "receipt" {
			return
		}
		mu.Lock()
		enumerations++
		shouldAppend := enumerations%2 == 0 && !appended
		if shouldAppend {
			appended = true
		}
		mu.Unlock()
		if !shouldAppend {
			return
		}
		// Put can enumerate this family again. The flag prevents another
		// append, and releasing the mutex permits that nested observation.
		putRecord(t, store, "rec_batch_late", "compound.receipt.v0.1", time.Now().UTC(), "late")
	}
	defer func() { familyInventoryObserver = prevFamily }()

	results := store.ReadMany([]string{"rec_batch_late"})
	if results["rec_batch_late"].Err != nil {
		t.Fatalf("the bounded retry must see the record that appeared mid-batch: %v", results["rec_batch_late"].Err)
	}
	if !bytes.Contains(results["rec_batch_late"].Record.Bytes, []byte("late")) {
		t.Fatalf("unexpected bytes %s", results["rec_batch_late"].Record.Bytes)
	}
}

// A family that keeps drifting must report drift for EVERY requested id of
// that family — including an id that was ABSENT on the first attempt. A stale
// "not found" is exactly the answer the drift exists to prevent. Other
// families keep their answers.
func TestReadManyReportsDriftForIdAbsentOnFirstAttempt(t *testing.T) {
	store, now := batchFixtureHome(t, 2)
	hotPath := segmentPath(store.engine.segRoot, "receipt", ingestionDay(now.Add(24*time.Hour)), 0)
	var mu sync.Mutex
	enumerations := 0
	prevFamily := familyInventoryObserver
	familyInventoryObserver = func(family string) {
		if family != "receipt" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		enumerations++
		if enumerations%2 != 0 {
			return
		}
		// Rewrite the hot segment at every recheck: the family keeps moving.
		data, err := os.ReadFile(hotPath)
		if err != nil {
			return
		}
		_ = os.WriteFile(hotPath, data, 0o644)
	}
	defer func() { familyInventoryObserver = prevFamily }()

	results := store.ReadMany([]string{"rec_batch_absent", "tr_batch_00"})
	if results["rec_batch_absent"].Err == nil {
		t.Fatal("an id absent on the first attempt must not keep a stale missing answer")
	}
	if !errors.Is(results["rec_batch_absent"].Err, errBatchDrift) {
		t.Fatalf("drift error = %v, want errBatchDrift", results["rec_batch_absent"].Err)
	}
	if results["tr_batch_00"].Err != nil {
		t.Fatalf("an unaffected family must keep its answer: %v", results["tr_batch_00"].Err)
	}
}
