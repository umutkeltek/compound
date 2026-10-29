package sourceobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestApprovedDirectoryBoundedResumeRelocationAndConfinement(t *testing.T) {
	hp, root := t.TempDir(), t.TempDir()
	for i := 0; i < 130; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%03d.md", i)), []byte("Synthetic archive decision."), 0600); err != nil {
			t.Fatal(err)
		}
	}
	first, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "synthetic", root, "markdown", time.Now())
	if err != nil || first.Captured != 128 || first.Pending != 2 {
		t.Fatalf("first %+v %v", first, err)
	}
	second, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "synthetic", root, "markdown", time.Now())
	if err != nil || second.Captured != 2 || second.Unchanged != 128 || second.Pending != 0 {
		t.Fatalf("resume %+v %v", second, err)
	}
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	third, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "synthetic", moved, "markdown", time.Now())
	if err != nil || third.Captured != 0 || third.Unchanged != 130 {
		t.Fatalf("relocation %+v %v", third, err)
	}
	if err := os.Symlink(filepath.Join(moved, "000.md"), filepath.Join(moved, "escape.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "synthetic", moved, "markdown", time.Now()); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestApprovedDirectoryReobservedBodyIsNewOccurrence(t *testing.T) {
	hp, root := t.TempDir(), t.TempDir()
	path := filepath.Join(root, "decision.md")
	var firstID string
	for i, text := range []string{"Cedar", "Birch", "Cedar"} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		stamp := time.Now().Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "reversion", root, "markdown", time.Now()); err != nil {
			t.Fatal(err)
		}
		manifest, err := LoadSourceManifest(hp, "reversion")
		if err != nil {
			t.Fatal(err)
		}
		if len(manifest.Items) != i+1 {
			t.Fatalf("versions=%d want %d", len(manifest.Items), i+1)
		}
		active := 0
		for _, item := range manifest.Items {
			if item.VersionState != "current" {
				continue
			}
			active++
			if i == 0 {
				firstID = item.ID
			}
			if i == 2 && item.ID == firstID {
				t.Fatal("resurrected original occurrence")
			}
			if item.FileHash != hashBytes([]byte(text)) {
				t.Fatalf("wrong current body")
			}
		}
		if active != 1 {
			t.Fatalf("current=%d", active)
		}
	}
}

func TestApprovedDirectoryIncompleteTranscriptDoesNotPoisonHistory(t *testing.T) {
	hp, root := t.TempDir(), t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "partial", root, "codex", time.Now())
	if err != nil || r.Deferred != 1 || r.Captured != 0 {
		t.Fatalf("partial %+v %v", r, err)
	}
	if err := os.WriteFile(path, []byte("{\"type\":\"session_meta\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err = CaptureApprovedDirectoryBatch(context.Background(), hp, "partial", root, "codex", time.Now())
	if err != nil || r.Captured != 1 || r.Deferred != 0 {
		t.Fatalf("recovery %+v %v", r, err)
	}
}
