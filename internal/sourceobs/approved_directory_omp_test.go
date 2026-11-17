package sourceobs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
)

func writeSizedOMPTranscript(t *testing.T, root, name string, size int, stamp time.Time) {
	t.Helper()
	prefix := `{"type":"message","message":{"role":"user","content":"`
	suffix := "\"}}\n"
	body := prefix + strings.Repeat("x", size-len(prefix)-len(suffix)) + suffix
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func assertOwnedOMPFiles(t *testing.T, hp string, expected map[string]int64) {
	t.Helper()
	manifest, err := LoadSourceManifest(hp, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Items) != len(expected) {
		t.Fatalf("lost or duplicated owned coverage: got %d want %d", len(manifest.Items), len(expected))
	}
	store, err := home.NewArtifactStore(hp)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range manifest.Items {
		name := filepath.Base(item.SourceRef)
		want, ok := expected[name]
		if !ok || seen[name] || item.BodyPolicy != "owned_snapshot" || item.BodyBytes != want {
			t.Fatalf("unexpected owned item: %+v", item)
		}
		seen[name] = true
		if bytes, err := store.Verify(item.BodyRef); err != nil || bytes != want {
			t.Fatalf("owned body %s: bytes=%d want=%d error=%v", name, bytes, want, err)
		}
	}
}

func TestOMPBatchByteBoundaryAndOwnedContinuation(t *testing.T) {
	hp, root := t.TempDir(), t.TempDir()
	now := time.Now()
	stamp := now.Add(-2 * time.Hour)
	writeSizedOMPTranscript(t, root, "a.jsonl", 16<<20, stamp)
	writeSizedOMPTranscript(t, root, "b.jsonl", 16<<20, stamp)
	writeSizedOMPTranscript(t, root, "c.jsonl", 256, stamp)
	first, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "synthetic", root, "omp", now)
	if err != nil || first.Captured != 2 || first.Pending != 1 {
		t.Fatalf("exact32MiB boundary: %+v %v", first, err)
	}
	assertOwnedOMPFiles(t, hp, map[string]int64{"a.jsonl": 16 << 20, "b.jsonl": 16 << 20})
	second, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "synthetic", root, "omp", now)
	if err != nil || second.Captured != 1 || second.Unchanged != 2 || second.Pending != 0 {
		t.Fatalf("resume after byte boundary: %+v %v", second, err)
	}
	assertOwnedOMPFiles(t, hp, map[string]int64{"a.jsonl": 16 << 20, "b.jsonl": 16 << 20, "c.jsonl": 256})
	unchanged, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "synthetic", root, "omp", now)
	if err != nil || unchanged.Captured != 0 || unchanged.Unchanged != 3 || unchanged.Pending != 0 {
		t.Fatalf("completed checkpoint repeated: %+v %v", unchanged, err)
	}
	other, err := CaptureApprovedDirectoryBatch(context.Background(), t.TempDir(), "synthetic", root, "claude", now)
	if err != nil || other.Captured != 3 || other.Pending != 0 {
		t.Fatalf("OMP limit leaked to another format: %+v %v", other, err)
	}
}

func TestOMPFirstFileLargerThanBatchStillProgresses(t *testing.T) {
	hp, root := t.TempDir(), t.TempDir()
	now := time.Now()
	stamp := now.Add(-2 * time.Hour)
	writeSizedOMPTranscript(t, root, "a.jsonl", (32<<20)+1, stamp)
	writeSizedOMPTranscript(t, root, "b.jsonl", 256, stamp)
	first, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "synthetic", root, "omp", now)
	if err != nil || first.Captured != 1 || first.Pending != 1 || first.Oversized != 0 {
		t.Fatalf("first larger file starved: %+v %v", first, err)
	}
	assertOwnedOMPFiles(t, hp, map[string]int64{"a.jsonl": (32 << 20) + 1})
	second, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "synthetic", root, "omp", now)
	if err != nil || second.Captured != 1 || second.Unchanged != 1 || second.Pending != 0 {
		t.Fatalf("continuation after larger file: %+v %v", second, err)
	}
	assertOwnedOMPFiles(t, hp, map[string]int64{"a.jsonl": (32 << 20) + 1, "b.jsonl": 256})
}
