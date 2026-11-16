package sourceobs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApprovedDirectoryDetectsSameSizeTimestampReplacement(t *testing.T) {
	hp := t.TempDir()
	root := t.TempDir()
	path := filepath.Join(root, "record.md")
	if err := os.WriteFile(path, []byte("first value"), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "replaced", root, "markdown", time.Now()); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(root, "new.tmp")
	if err := os.WriteFile(replacement, []byte("other value"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	report, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "replaced", root, "markdown", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Captured != 1 {
		t.Fatalf("replacement skipped: %+v", report)
	}
}
func TestFingerprintDoesNotBypassLargeActiveTranscriptQuietWindow(t *testing.T) {
	hp := t.TempDir()
	root := t.TempDir()
	path := filepath.Join(root, "large.md")
	if err := os.WriteFile(path, []byte(strings.Repeat("synthetic data\n", 150000)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "active", root, "markdown", time.Now()); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("small live append")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	report, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "active", root, "markdown", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Captured != 0 || report.Pending != 1 {
		t.Fatalf("fingerprint made every live turn a whole-body snapshot: %+v", report)
	}
}

func TestLegacyOwnedFileAdoptsFingerprintWithoutRecopy(t *testing.T) {
	hp := t.TempDir()
	root := t.TempDir()
	path := filepath.Join(root, "legacy.md")
	if err := os.WriteFile(path, []byte("synthetic legacy knowledge"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "legacy-fingerprint", root, "markdown", time.Now()); err != nil {
		t.Fatal(err)
	}
	before, err := LoadSourceManifest(hp, "legacy-fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	ref := before.Items[0].BodyRef
	id := before.Items[0].ID
	before.Items[0].SourceFingerprint = ""
	if _, err := RecordSourceManifest(hp, before); err != nil {
		t.Fatal(err)
	}
	report, err := CaptureApprovedDirectoryBatch(context.Background(), hp, "legacy-fingerprint", root, "markdown", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	after, err := LoadSourceManifest(hp, "legacy-fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	if report.Captured != 0 || report.Unchanged != 1 || len(after.Items) != 1 || after.Items[0].BodyRef != ref || after.Items[0].ID != id || after.Items[0].SourceFingerprint == "" {
		t.Fatalf("legacy fingerprint adoption recopied/resurrected source: %+v %+v", report, after.Items)
	}
}
