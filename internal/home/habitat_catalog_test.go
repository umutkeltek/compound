package home

import (
	"archive/tar"
	"compress/gzip"
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
	writeHomeFile(t, h, "close-plans/legacy-plan.json", "bridge")

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
	if got := mustReadHomeFile(t, restored, "close-plans/legacy-plan.json"); got != "bridge" {
		t.Fatalf("restored bridge body = %q", got)
	}
}

func TestVerifyArchiveRejectsUnknownHabitatBeforeRestore(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "unknown-root.tar.gz")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	entry, err := writeBytesEntry(tw, "invented-feature-root/state.json", []byte("unowned"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArchiveManifest(tw, ArchiveManifest{
		SchemaVersion: ArchiveSchemaVersion,
		Kind:          ArchiveKindBackup,
		CreatedAt:     archiveTestNow.UTC().Format("2006-01-02T15:04:05Z"),
		Entries:       []ArchiveEntry{entry},
	}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := VerifyArchive(archivePath); err == nil || !strings.Contains(err.Error(), "invented-feature-root") {
		t.Fatalf("archive verification accepted unknown habitat: %v", err)
	}
}

func TestHabitatCatalogClassifiesEveryKnownRootAndMakesBridgeDebtExplicit(t *testing.T) {
	entries := HabitatEntries()
	if len(entries) == 0 {
		t.Fatal("habitat catalog is empty")
	}
	knownRoots := []string{
		"action-execution-plans", "advance-plans", "archives", "artifacts", "bench-suites",
		"claim-plans", "close-plans", "config", "context", "continue-runs", "continues",
		"contracts", "create-plans", "credit-harvest", "decisions", "dispatch-advances",
		"dispatch-claims", "dispatch-plans", "dispatch-step-runs", "dispatch-steps", "distill",
		"events", "evidence", "exports", "imports", "inbox", "indexes", "lanebeliefs",
		"ledger", "lock", "logs", "observed", "packs", "panel-runs", "private",
		"projections", "registries", "reports", "reviewed-fixtures", "reviews",
		"run-observations", "runs", "runtime", "snapshots", "sources", "state", "tmp", "work",
	}
	for _, root := range knownRoots {
		entry, ok := HabitatForPath(root)
		if !ok || entry.Prefix != root {
			t.Fatalf("known root %q is not owned by its catalog entry: %+v ok=%t", root, entry, ok)
		}
	}
	for _, entry := range entries {
		if entry.Status == HabitatStatusBridge && (entry.CanonicalPrefix == "" || entry.RetirementCondition == "") {
			t.Fatalf("bridge %s hides migration debt: %+v", entry.ID, entry)
		}
		if entry.Compact && entry.Class != HabitatClassRebuildable && entry.Class != HabitatClassTransient {
			t.Fatalf("behavior-bearing habitat is compactable: %+v", entry)
		}
		if strings.HasPrefix(entry.Prefix, "projections/") && entry.Backup && entry.Compact {
			t.Fatalf("protected projection cannot also be compactable: %+v", entry)
		}
	}
}

func TestHabitatCatalogOwnsCanonicalSourceManifestsAndNamesLegacyRetirement(t *testing.T) {
	canonical, ok := HabitatForPath("sources/manifests/family.json")
	if !ok || canonical.Prefix != "sources/manifests" || canonical.Status != HabitatStatusActive || canonical.Class != HabitatClassEvidence || !canonical.Backup || canonical.Compact || !canonical.PlacementAllowed {
		t.Fatalf("canonical SourceManifest habitat is not durable evidence: %+v ok=%t", canonical, ok)
	}

	legacy, ok := HabitatForPath("registries/source-manifests/family.json")
	if !ok || legacy.Prefix != "registries/source-manifests" || legacy.Status != HabitatStatusBridge || legacy.CanonicalPrefix != "sources/manifests" || legacy.PlacementAllowed || legacy.RetirementCondition == "" {
		t.Fatalf("legacy SourceManifest path hides retirement debt: %+v ok=%t", legacy, ok)
	}
}

func TestHabitatCatalogProjectionsCannotMutateAuthority(t *testing.T) {
	entries := HabitatEntries()
	dirs := HabitatDirs()
	placementDirs := HabitatPlacementDirs()
	if len(entries) == 0 || len(dirs) == 0 || len(placementDirs) == 0 {
		t.Fatal("habitat catalog projections are empty")
	}

	entries[0].Prefix = "mutated-entry"
	dirs[0] = "mutated-dir"
	placementDirs[0] = "mutated-placement-dir"

	if _, ok := HabitatForPath("mutated-entry"); ok {
		t.Fatal("HabitatEntries returned mutable catalog authority")
	}
	for _, dir := range HabitatDirs() {
		if dir == "mutated-dir" {
			t.Fatal("HabitatDirs returned mutable catalog authority")
		}
	}
	for _, dir := range HabitatPlacementDirs() {
		if dir == "mutated-placement-dir" {
			t.Fatal("HabitatPlacementDirs returned mutable catalog authority")
		}
	}
}

func TestHabitatPathMakesLegacyWriterDebtExplicitAndRejectsTraversal(t *testing.T) {
	h := filepath.Join(string(os.PathSeparator), "owned", ".compound")
	got, err := HabitatPath(h, HabitatClosePlans, "plan.json")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(h, "close-plans", "plan.json")
	if got != want {
		t.Fatalf("close-plan habitat path = %q, want %q", got, want)
	}
	entry, ok := HabitatForPath("close-plans/plan.json")
	if !ok || entry.Status != HabitatStatusBridge || entry.CanonicalPrefix != "work/plans/close" {
		t.Fatalf("close-plan writer lost bridge debt: %+v ok=%t", entry, ok)
	}
	if _, err := HabitatPath(h, HabitatClosePlans, "../escape.json"); err == nil {
		t.Fatal("habitat path accepted traversal")
	}
}
