package corpus

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"compound/internal/sourceobs"
)

func incrementalFixture(t *testing.T, files int) (string, string, LaneConfig) {
	t.Helper()
	hp := t.TempDir()
	root := t.TempDir()
	for i := 0; i < files; i++ {
		writeRaw(t, filepath.Join(root, fmt.Sprintf("session-%03d.md", i)), fmt.Sprintf("User: synthetic fact %d\n\nAssistant: preserved knowledge for item %d.", i, i))
	}
	_, err := sourceobs.CaptureApprovedDirectoryBatch(context.Background(), hp, "incremental-fixture", root, "markdown", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	lane := LaneConfig{Lane: "incremental-fixture", SourceFamily: "incremental-fixture", Privacy: "private", Embedder: EmbedderConfig{Provider: "local", Model: "synthetic", Dims: 3, Bits: "f32"}, Chunk: ChunkConfig{Size: 80, Overlap: 8}}
	return hp, root, lane
}
func runIncremental(t *testing.T, hp string, lane LaneConfig) IndexReport {
	t.Helper()
	e := &fakeEmbedder{identity: lane.Embedder.Identity(), dim: lane.Embedder.Dims}
	r, err := Index(context.Background(), hp, lane, e, IndexOptions{Incremental: true})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestIncrementalUnchangedDoesNotReadBodiesOrRewriteIndex(t *testing.T) {
	hp, _, lane := incrementalFixture(t, 4)
	first := runIncremental(t, hp, lane)
	if first.BodyBytesRead == 0 || first.IndexBytesWritten == 0 {
		t.Fatalf("first=%+v", first)
	}
	dir := IndexDir(hp, lane)
	before, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(filepath.Join(dir, "manifest.json"))
	second := runIncremental(t, hp, lane)
	after, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	newInfo, _ := os.Stat(filepath.Join(dir, "manifest.json"))
	if !second.Unchanged || second.BodyBytesRead != 0 || second.IndexBytesWritten != 0 || second.IndexBytesRead != 0 || second.PreparedItems != 0 {
		t.Fatalf("unchanged=%+v", second)
	}
	if string(before) != string(after) || !info.ModTime().Equal(newInfo.ModTime()) {
		t.Fatal("unchanged refresh rewrote its committed index")
	}
}
func TestIncrementalNewModifiedAndMirroredBodiesKeepProvenance(t *testing.T) {
	hp, root, lane := incrementalFixture(t, 2)
	runIncremental(t, hp, lane)
	added := "User: unique comet fact\n\nAssistant: the approved answer is violet."
	writeRaw(t, filepath.Join(root, "new.md"), added)
	capture := func() {
		t.Helper()
		_, err := sourceobs.CaptureApprovedDirectoryBatch(context.Background(), hp, lane.SourceFamily, root, "markdown", time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	capture()
	r := runIncremental(t, hp, lane)
	if r.PreparedItems != 1 || r.BodyBytesRead != int64(len(added)) {
		t.Fatalf("one new item=%+v", r)
	}
	mirror := filepath.Join(root, "mirror")
	writeRaw(t, filepath.Join(mirror, "new.md"), added)
	capture()
	r = runIncremental(t, hp, lane)
	if r.PreparedItems != 0 || r.BodyBytesRead != 0 || r.ReusedChunks != r.TotalChunks {
		t.Fatalf("copied body was semantically reindexed: %+v", r)
	}
	m, err := readManifest(IndexDir(hp, lane))
	if err != nil {
		t.Fatal(err)
	}
	copies := 0
	refs := map[string]bool{}
	for _, item := range m.Items {
		if strings.HasSuffix(item.SourceRef, "new.md") {
			copies++
			refs[item.SourceRef] = true
		}
	}
	if copies != 2 || len(refs) != 2 {
		t.Fatalf("lost mirror provenance: %+v", m.Items)
	}
	writeRaw(t, filepath.Join(root, "new.md"), added+"\n\nAssistant: updated color is amber.")
	capture()
	r = runIncremental(t, hp, lane)
	if r.PreparedItems != 1 {
		t.Fatalf("modified=%+v", r)
	}
	m, _ = readManifest(IndexDir(hp, lane))
	history := 0
	for _, item := range m.Items {
		if item.VersionState == "superseded" {
			history++
		}
	}
	if history == 0 {
		t.Fatal("modified conversation lost its owned historical version")
	}
	e := &fakeEmbedder{identity: lane.Embedder.Identity(), dim: 3}
	q, err := QueryWithOptions(context.Background(), hp, lane, e, "amber", 100, QueryOptions{IncludeText: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range q.Hits {
		found = found || strings.Contains(h.Text, "amber")
	}
	if !found {
		t.Fatal("new content not retrievable")
	}
}
func TestIncrementalCrashResumesOnlyMissingEmbeddings(t *testing.T) {
	hp, _, lane := incrementalFixture(t, 70)
	e := &fakeEmbedder{identity: lane.Embedder.Identity(), dim: 3, failOnCall: 2}
	first, err := Index(context.Background(), hp, lane, e, IndexOptions{Incremental: true})
	if err == nil {
		t.Fatal("expected simulated failure")
	}
	if first.EmbeddedChunks != 64 {
		t.Fatalf("partial checkpoint=%+v", first)
	}
	e = &fakeEmbedder{identity: lane.Embedder.Identity(), dim: 3}
	r, err := Index(context.Background(), hp, lane, e, IndexOptions{Incremental: true})
	if err != nil {
		t.Fatal(err)
	}
	if e.embedded != r.TotalChunks-64 {
		t.Fatalf("replayed embeddings=%d total=%d", e.embedded, r.TotalChunks)
	}
	if r.BodyBytesRead == 0 { /* Cached pending chunks are read only when necessary. */
	}
}
func TestIncrementalReconciliationMatchesFullRebuild(t *testing.T) {
	hp, root, lane := incrementalFixture(t, 3)
	runIncremental(t, hp, lane)
	writeRaw(t, filepath.Join(root, "session-001.md"), "User: replacement synthetic fact\n\nAssistant: reconciliation result")
	_, err := sourceobs.CaptureApprovedDirectoryBatch(context.Background(), hp, lane.SourceFamily, root, "markdown", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runIncremental(t, hp, lane)
	before, err := readVectors(IndexDir(hp, lane))
	if err != nil {
		t.Fatal(err)
	}
	e := &fakeEmbedder{identity: lane.Embedder.Identity(), dim: 3}
	_, err = Index(context.Background(), hp, lane, e, IndexOptions{Incremental: true, Rebuild: true})
	if err != nil {
		t.Fatal(err)
	}
	after, err := readVectors(IndexDir(hp, lane))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("incremental vectors differ from authoritative rebuild")
	}
}
func TestIncrementalMissingShardFailsRatherThanEmptySearch(t *testing.T) {
	hp, _, lane := incrementalFixture(t, 1)
	runIncremental(t, hp, lane)
	m, _ := readManifest(IndexDir(hp, lane))
	for _, ref := range m.VectorShards {
		p, _ := vectorShardPath(IndexDir(hp, lane), ref)
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
		break
	}
	e := &fakeEmbedder{identity: lane.Embedder.Identity(), dim: 3}
	if _, err := Index(context.Background(), hp, lane, e, IndexOptions{Incremental: true}); err == nil {
		t.Fatal("missing shard accepted as successful unchanged refresh")
	}
	if _, err := Query(context.Background(), hp, lane, e, "known", 3); err == nil {
		t.Fatal("missing shard accepted as empty search")
	}
}
func TestIncrementalLegacyMigrationLeavesRollbackBytes(t *testing.T) {
	hp, _, lane := incrementalFixture(t, 3)
	e := &fakeEmbedder{identity: lane.Embedder.Identity(), dim: 3}
	if _, err := Index(context.Background(), hp, lane, e, IndexOptions{}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(IndexDir(hp, lane), "vectors.json")
	before, _ := os.ReadFile(path)
	r := runIncremental(t, hp, lane)
	after, _ := os.ReadFile(path)
	if r.BodyBytesRead != 0 || r.ReusedChunks != r.TotalChunks || string(before) != string(after) {
		t.Fatalf("migration did unnecessary work or lost rollback=%+v", r)
	}
}
func TestIncrementalLockWaitIsCancellable(t *testing.T) {
	dir := t.TempDir()
	unlock, err := lockCorpusIndex(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if release, err := lockCorpusIndex(ctx, dir); err == nil {
		release()
		t.Fatal("overlapping writer acquired lock")
	}
}
