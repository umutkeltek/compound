package eventstore

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
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

// --- E. rooted boundaries --------------------------------------------------

// The rooted twin must keep the rooted rejection: a symlinked events ancestor
// never qualifies as authority evidence.
func TestReadManyRegularRejectsSymlinkedAncestor(t *testing.T) {
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
	results := store.ReadManyRegular([]string{"rec_link"}, "receipt")
	if results["rec_link"].Err == nil {
		t.Fatal("ReadManyRegular followed a symlinked events ancestor")
	}
}
