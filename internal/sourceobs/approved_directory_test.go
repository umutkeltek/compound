package sourceobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// A live transcript must not store a whole new version on every turn, and holding
// it back must never block a correction or lose the last owned version.
func TestApprovedDirectoryLargeLiveFileEarnsVersionsByGrowthOrQuiet(t *testing.T) {
	hp, root := t.TempDir(), t.TempDir()
	path := filepath.Join(root, "session.md")
	line := strings.Repeat("Synthetic transcript turn.\n", 40)
	write := func(size int) {
		t.Helper()
		if err := os.WriteFile(path, []byte(strings.Repeat(line, size/len(line))), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	capture := func(now time.Time) CaptureBatchReport {
		t.Helper()
		report, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "synthetic", root, "markdown", now)
		if err != nil {
			t.Fatal(err)
		}
		return report
	}
	versions := func() int {
		t.Helper()
		manifest, err := LoadSourceManifest(hp, "synthetic")
		if err != nil {
			t.Fatal(err)
		}
		return len(manifest.Items)
	}

	start := 4 << 20
	write(start)
	if got := capture(time.Now()); got.Captured != 1 || versions() != 1 {
		t.Fatalf("first capture %+v versions %d", got, versions())
	}
	write(start + start/10) // a live session's next turns
	if got := capture(time.Now()); got.Captured != 0 || got.Pending != 1 || versions() != 1 {
		t.Fatalf("small growth earned a version: %+v versions %d", got, versions())
	}
	if got := capture(time.Now().Add(2 * time.Hour)); got.Captured != 1 || versions() != 2 {
		t.Fatalf("quiet file not captured: %+v versions %d", got, versions())
	}
	write(start * 2) // grew by more than a quarter while still live
	if got := capture(time.Now()); got.Captured != 1 || versions() != 3 {
		t.Fatalf("large growth not captured: %+v versions %d", got, versions())
	}
	write(start) // a rewrite that shrinks is a correction, not growth
	if got := capture(time.Now()); got.Captured != 1 || versions() != 4 {
		t.Fatalf("shrinking rewrite held back: %+v versions %d", got, versions())
	}
}
