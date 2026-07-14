package home

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorAndBackupRejectUnknownTopLevelRoot(t *testing.T) {
	h := filepath.Join(t.TempDir(), ".compound")
	if err := Init(h, "local", archiveTestNow); err != nil {
		t.Fatal(err)
	}
	writeHomeFile(t, h, "invented-feature-root/state.json", `{"unowned":true}`)

	report := Doctor(h)
	if report.OK {
		t.Fatalf("doctor accepted an unowned top-level root: %+v", report)
	}

	_, err := Backup(h, filepath.Join(t.TempDir(), "backup.tar.gz"), archiveTestNow)
	if err == nil || !strings.Contains(err.Error(), "invented-feature-root") {
		t.Fatalf("backup must reject the same unowned root, got %v", err)
	}
}

func TestCompactPreservesBehaviorBearingProjectionExceptions(t *testing.T) {
	h := filepath.Join(t.TempDir(), ".compound")
	if err := Init(h, "local", archiveTestNow); err != nil {
		t.Fatal(err)
	}
	protected := []string{
		"projections/account-projection.json",
		"projections/narrate-thread-seen.json",
		"projections/orient-operator-last-seq.json",
		"projections/director/runs/run.json",
		"projections/source-intake/reports/report.json",
	}
	for _, rel := range protected {
		writeHomeFile(t, h, rel, "keep")
	}
	writeHomeFile(t, h, "projections/rebuildable-view.json", "drop")

	report, err := Compact(h, CompactOptions{Rebuildable: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.RemovedFiles != 1 {
		t.Fatalf("compact removed behavior-bearing state: %+v", report)
	}
	for _, rel := range protected {
		if _, err := os.Stat(filepath.Join(h, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("protected habitat %s was compacted: %v", rel, err)
		}
	}
	assertMissing(t, filepath.Join(h, "projections", "rebuildable-view.json"))
}

func TestBackupRestoresBehaviorBearingProjectionExceptionsOnly(t *testing.T) {
	h := filepath.Join(t.TempDir(), ".compound")
	if err := Init(h, "local", archiveTestNow); err != nil {
		t.Fatal(err)
	}
	writeHomeFile(t, h, "projections/account-projection.json", "keep")
	writeHomeFile(t, h, "projections/rebuildable-view.json", "drop")

	archivePath := filepath.Join(t.TempDir(), "backup.tar.gz")
	if _, err := Backup(h, archivePath, archiveTestNow); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), ".compound-restored")
	if _, err := RestoreArchive(archivePath, restored, archiveTestNow); err != nil {
		t.Fatal(err)
	}
	if got := mustReadHomeFile(t, restored, "projections/account-projection.json"); got != "keep" {
		t.Fatalf("restored behavior-bearing projection = %q", got)
	}
	assertMissing(t, filepath.Join(restored, "projections", "rebuildable-view.json"))
}
