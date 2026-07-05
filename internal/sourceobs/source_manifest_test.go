package sourceobs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSourceManifestDoctorAndDrilldown(t *testing.T) {
	home := t.TempDir()
	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "compound.md")
	if err := os.WriteFile(sourcePath, []byte("---\ntitle: Compound\n---\n# Compound\n\n## Facts\n- fact\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	manifest, err := BuildSourceManifest("brain_source_manifest", []string{sourcePath}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Items) != 1 {
		t.Fatalf("items = %d", len(manifest.Items))
	}
	item := manifest.Items[0]
	if item.BodyPolicy != "source_in_place" || item.FileHash == "" || item.SourceRef != sourcePath {
		t.Fatalf("bad item: %+v", item)
	}
	if !hasOverlay(item.StructureOverlays, "frontmatter") || !hasOverlay(item.StructureOverlays, "heading_index") || !hasOverlay(item.StructureOverlays, "facts_fence") {
		t.Fatalf("missing overlays: %+v", item.StructureOverlays)
	}
	report, err := RecordSourceManifest(home, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if report.ItemCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	before, err := os.ReadFile(report.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := BuildSourceManifest("brain_source_manifest", []string{sourcePath}, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecordSourceManifest(home, replay); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(report.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("unchanged source should produce idempotent manifest")
	}
	doctor, err := DoctorSourceManifest(home, "brain_source_manifest")
	if err != nil {
		t.Fatal(err)
	}
	if doctor.Status != "ok" || doctor.HashMismatchCount != 0 {
		t.Fatalf("doctor = %+v", doctor)
	}
	drilldown, err := DrilldownSourceManifest(home, "brain_source_manifest", item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !drilldown.SourceAvailable || !drilldown.HashMatches || drilldown.SourceRef != sourcePath {
		t.Fatalf("drilldown = %+v", drilldown)
	}
	if len(drilldown.DrilldownPath) != 3 || drilldown.DrilldownPath[0] != "source-manifest:brain_source_manifest#"+item.ID {
		t.Fatalf("drilldown path = %+v", drilldown.DrilldownPath)
	}
}

func TestSourceManifestDoctorFlagsHashMismatch(t *testing.T) {
	home := t.TempDir()
	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "compound.md")
	if err := os.WriteFile(sourcePath, []byte("# One\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := BuildSourceManifest("brain_source_manifest", []string{sourcePath}, time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecordSourceManifest(home, manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("# Two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doctor, err := DoctorSourceManifest(home, "brain_source_manifest")
	if err != nil {
		t.Fatal(err)
	}
	if doctor.Status != "fail" || doctor.HashMismatchCount != 1 {
		t.Fatalf("expected hash mismatch, got %+v", doctor)
	}
}

func hasOverlay(overlays []SourceStructureOverlay, kind string) bool {
	for _, overlay := range overlays {
		if overlay.Kind == kind {
			return true
		}
	}
	return false
}
