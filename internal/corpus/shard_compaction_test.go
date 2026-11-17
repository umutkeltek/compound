package corpus

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/sourceobs"
)

func compactionHash(n int) string { return fmt.Sprintf("sha256:aa%062x", n) }
func compactionManifest(ref string, dims int) Manifest {
	return Manifest{VectorShards: map[string]string{"aa": ref}, Items: []sourceobs.SourceManifestItem{{ID: "fixture", Embedding: &sourceobs.SourceManifestEmbedding{Dims: dims}}}}
}
func shardFiles(t *testing.T, dir string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	entries, err := os.ReadDir(filepath.Join(dir, "vector-shards"))
	if os.IsNotExist(err) {
		return out
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".bin") {
			info, err := entry.Info()
			if err != nil {
				t.Fatal(err)
			}
			out[entry.Name()] = info.Size()
		}
	}
	return out
}
func compactFixture(t *testing.T, dir string, m Manifest) ShardCompactionReport {
	t.Helper()
	unlock, err := lockCorpusIndex(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := writeJSONFile(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	return compactRedundantShards(context.Background(), dir, m, 3, ShardCompactionConfig{Enabled: true})
}
func TestShardCompactionBoundsRepeatedGrowthAndPreservesCurrentHistory(t *testing.T) {
	dir := t.TempDir()
	values := map[string][]float32{}
	var reclaimed int64
	for i := 1; i <= 13; i++ {
		values[compactionHash(i)] = []float32{1, 0, 0}
		unlock, err := lockCorpusIndex(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		ref, _, err := writeVectorShard(dir, values, 3)
		if err != nil {
			unlock()
			t.Fatal(err)
		}
		m := compactionManifest(ref, 3)
		if err := writeJSONFile(filepath.Join(dir, "manifest.json"), m); err != nil {
			unlock()
			t.Fatal(err)
		}
		r := compactRedundantShards(context.Background(), dir, m, 3, ShardCompactionConfig{Enabled: true})
		unlock()
		if r.Error != "" || r.Deferred || len(shardFiles(t, dir)) != 1 {
			t.Fatalf("step%d growth not bounded: %+v", i, r)
		}
		if i > 1 && r.ReclaimedFiles != 1 {
			t.Fatalf("step%d did not collect exactly old generation: %+v", i, r)
		}
		reclaimed += r.ReclaimedPayloadBytes
		got, _, err := readVectorShard(dir, ref, 3)
		if err != nil || len(got) != i {
			t.Fatalf("history vectors lost: %d %v", len(got), err)
		}
	}
	if reclaimed <= 0 {
		t.Fatal("empty compaction pass is not proof")
	}
}
func TestShardCompactionDefaultOffAndExactVectorRetention(t *testing.T) {
	dir := t.TempDir()
	if err := ensureShardAccessGate(dir); err != nil {
		t.Fatal(err)
	}
	a, b, c := compactionHash(1), compactionHash(2), compactionHash(3)
	ref, _, err := writeVectorShard(dir, map[string][]float32{a: {1, 0, 0}, b: {0, 1, 0}}, 3)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, _, _ := writeVectorShard(dir, map[string][]float32{a: {1, 0, 0}}, 3)
	unique, _, _ := writeVectorShard(dir, map[string][]float32{c: {1, 0, 0}}, 3)
	differentModel, _, _ := writeVectorShard(dir, map[string][]float32{a: {0, 1, 0}}, 3)
	signedZero, _, _ := writeVectorShard(dir, map[string][]float32{a: {1, math.Float32frombits(0x80000000), 0}}, 3)
	differentDims, _, _ := writeVectorShard(dir, map[string][]float32{a: {1, 0}}, 2)
	legacy := filepath.Join(dir, "vectors.json")
	if err := os.WriteFile(legacy, []byte("rollback legacy retained"), 0600); err != nil {
		t.Fatal(err)
	}
	m := compactionManifest(ref, 3)
	before := len(shardFiles(t, dir))
	r := compactRedundantShards(context.Background(), dir, m, 3, ShardCompactionConfig{})
	if r.Status != "disabled" || len(shardFiles(t, dir)) != before {
		t.Fatal("default-off configuration mutated cache")
	}
	r = compactFixture(t, dir, m)
	if r.ReclaimedFiles != 1 || r.RetainedUniqueFiles != 1 || r.RetainedDifferentVectorFiles != 2 || r.RetainedUnverifiedFiles != 1 {
		t.Fatalf("retention: %+v", r)
	}
	deleted, _ := vectorShardPath(dir, duplicate)
	if _, err := os.Stat(deleted); !os.IsNotExist(err) {
		t.Fatal("redundant subset retained")
	}
	for _, keep := range []string{ref, unique, differentModel, signedZero, differentDims} {
		p, _ := vectorShardPath(dir, keep)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("unproven/different-model vectors deleted: %s %v", keep, err)
		}
	}
	if body, err := os.ReadFile(legacy); err != nil || string(body) != "rollback legacy retained" {
		t.Fatal("legacy rollback changed")
	}
}
func TestShardCompactionBoundedPassAndCancellation(t *testing.T) {
	dir := t.TempDir()
	if err := ensureShardAccessGate(dir); err != nil {
		t.Fatal(err)
	}
	values := map[string][]float32{}
	ref := ""
	for i := 1; i <= 33; i++ {
		values[compactionHash(i)] = []float32{1, 0, 0}
		var err error
		ref, _, err = writeVectorShard(dir, values, 3)
		if err != nil {
			t.Fatal(err)
		}
	}
	m := compactionManifest(ref, 3)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := compactRedundantShards(ctx, dir, m, 3, ShardCompactionConfig{Enabled: true})
	if r.ReclaimedFiles != 0 || !r.Deferred {
		t.Fatalf("cancelled collector acted: %+v", r)
	}
	r = compactFixture(t, dir, m)
	if r.ReclaimedFiles != shardCompactMaxRemoved || !r.Deferred {
		t.Fatalf("unbounded collector: %+v", r)
	}
	r = compactFixture(t, dir, m)
	if r.ReclaimedFiles != 16 || len(shardFiles(t, dir)) != 1 {
		t.Fatalf("bounded retry did not finish: %+v", r)
	}
}
func TestShardReadOnlyLegacyGateAndUnsafeFiles(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireShardRead(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("read-only legacy query created a gate")
	}
	if err := ensureShardAccessGate(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "vector-shards"), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "original")
	if err := os.WriteFile(target, []byte("original source retained"), 0600); err != nil {
		t.Fatal(err)
	}
	name := strings.Repeat("a", 64) + ".bin"
	if err := os.Symlink(target, filepath.Join(dir, "vector-shards", name)); err != nil {
		t.Fatal(err)
	}
	r := compactFixture(t, dir, compactionManifest("", 3))
	if r.ReclaimedFiles != 0 {
		t.Fatalf("linked candidate was removed: %+v", r)
	}
	if body, err := os.ReadFile(target); err != nil || string(body) != "original source retained" {
		t.Fatal("symlink target changed")
	}
}
func TestShardCompactionRetainsIncompleteAndFailedPublications(t *testing.T) {
	dir := t.TempDir()
	if err := ensureShardAccessGate(dir); err != nil {
		t.Fatal(err)
	}
	a, b := compactionHash(1), compactionHash(2)
	old, _, _ := writeVectorShard(dir, map[string][]float32{a: {1, 0, 0}}, 3)
	current, _, _ := writeVectorShard(dir, map[string][]float32{a: {1, 0, 0}, b: {0, 1, 0}}, 3)
	m := compactionManifest(current, 3)
	m.VectorPending = 1
	if r := compactFixture(t, dir, m); r.ReclaimedFiles != 0 || r.Status != "incomplete_or_legacy" {
		t.Fatalf("partial publication collected: %+v", r)
	}
	m.VectorPending = 0
	m.VectorError = "interrupted"
	if r := compactFixture(t, dir, m); r.ReclaimedFiles != 0 {
		t.Fatalf("failed publication collected: %+v", r)
	}
	p, _ := vectorShardPath(dir, old)
	if _, err := os.Stat(p); err != nil {
		t.Fatal("rollback shard lost")
	}
}
func TestShardSnapshotReaderDefersCollectorAndDoesNotWaitForWriterInference(t *testing.T) {
	hp := t.TempDir()
	f := seedOwnedSnapshotLane(t, hp, "Cedar reader fixture")
	if _, err := Index(context.Background(), hp, f.lane, f.embedder, IndexOptions{Incremental: true}); err != nil {
		t.Fatal(err)
	}
	dir := IndexDir(hp, f.lane)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.VectorShards) != 1 {
		t.Fatal("fixture needs one shard")
	}
	var prefix, original string
	for k, v := range m.VectorShards {
		prefix, original = k, v
	}
	values, _, err := readVectorShard(dir, original, 3)
	if err != nil {
		t.Fatal(err)
	}
	extra := "sha256:" + prefix + strings.Repeat("1", 62)
	values[extra] = []float32{0, 1, 0}
	middle, _, err := writeVectorShard(dir, values, 3)
	if err != nil {
		t.Fatal(err)
	}
	m.VectorShards[prefix] = middle
	if err := writeJSONFile(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	sampled, resume := make(chan struct{}), make(chan struct{})
	oldRead := readShardForSnapshot
	readShardForSnapshot = func(d, r string, n int) (map[string][]float32, int64, error) {
		if r == middle {
			close(sampled)
			<-resume
		}
		return oldRead(d, r, n)
	}
	t.Cleanup(func() { readShardForSnapshot = oldRead })
	done := make(chan error, 1)
	go func() {
		_, err := QueryWithOptions(context.Background(), hp, f.lane, f.embedder, "Cedar", 1, QueryOptions{IncludeText: true})
		done <- err
	}()
	select {
	case <-sampled:
	case <-time.After(10 * time.Second):
		t.Fatal("reader did not sample old manifest")
	}
	unlock, err := lockCorpusIndex(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	values["sha256:"+prefix+strings.Repeat("2", 62)] = []float32{0, 0, 1}
	latest, _, err := writeVectorShard(dir, values, 3)
	if err != nil {
		unlock()
		t.Fatal(err)
	}
	m.VectorShards[prefix] = latest
	if err := writeJSONFile(filepath.Join(dir, "manifest.json"), m); err != nil {
		unlock()
		t.Fatal(err)
	}
	r := compactRedundantShards(context.Background(), dir, m, 3, ShardCompactionConfig{Enabled: true})
	unlock()
	if r.Status != "readers_busy" || r.ReclaimedFiles != 0 {
		t.Fatalf("sampled reader was not protected: %+v", r)
	}
	close(resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	readShardForSnapshot = oldRead
	r = compactFixture(t, dir, m)
	if r.ReclaimedFiles != 2 {
		t.Fatalf("quiescent collector did not finish: %+v", r)
	}
	// The long writer/inference lease must not exclude snapshot queries.
	unlock, err = lockCorpusIndex(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = QueryWithOptions(ctx, hp, f.lane, f.embedder, "Cedar", 1, QueryOptions{IncludeText: true})
	unlock()
	if err != nil {
		t.Fatalf("query waited behind writer/inference lease: %v", err)
	}
}
func TestShardCompactionWiredOnlyAfterDurableIndexPublication(t *testing.T) {
	hp := t.TempDir()
	f := seedOwnedSnapshotLane(t, hp, "Initial compaction fixture")
	f.lane.ShardCompaction.Enabled = true
	if _, err := Index(context.Background(), hp, f.lane, f.embedder, IndexOptions{Incremental: true}); err != nil {
		t.Fatal(err)
	}
	dir := IndexDir(hp, f.lane)
	before := shardFiles(t, dir)
	m, err := sourceobs.LoadSourceManifest(hp, f.lane.SourceFamily)
	if err != nil {
		t.Fatal(err)
	}
	store, err := home.NewArtifactStore(hp)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("Changed compaction fixture")
	ref, err := store.Put(body)
	if err != nil {
		t.Fatal(err)
	}
	item := m.Items[0]
	item.ID = "changed_compaction_item"
	item.FileHash = "sha256:" + fmt.Sprintf("%x", sha256Bytes(body))
	item.BodyRef = ref
	item.BodyBytes = int64(len(body))
	item.SnapshotTime = time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano)
	m.Items = []sourceobs.SourceManifestItem{item}
	if _, err := sourceobs.RecordSourceManifest(hp, m); err != nil {
		t.Fatal(err)
	}
	oldSync := syncCorpusDirectory
	oldManifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	syncCorpusDirectory = func(path string) error {
		if path == dir {
			if now, e := os.ReadFile(filepath.Join(dir, "manifest.json")); e == nil && string(now) != string(oldManifest) {
				return fmt.Errorf("injected manifest directory sync failure")
			}
		}
		return oldSync(path)
	}
	t.Cleanup(func() { syncCorpusDirectory = oldSync })
	if _, err := Index(context.Background(), hp, f.lane, f.embedder, IndexOptions{Incremental: true}); err == nil {
		t.Fatal("failed durable publication passed")
	}
	after := shardFiles(t, dir)
	for name := range before {
		if _, ok := after[name]; !ok {
			t.Fatalf("failed publication deleted %s", name)
		}
	}
}
func sha256Bytes(body []byte) []byte { sum := sha256.Sum256(body); return sum[:] }
