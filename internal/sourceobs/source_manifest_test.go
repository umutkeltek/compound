package sourceobs

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
)

func TestOwnedSourceManifestSurvivesOriginalLossAndMaterializesExactBytes(t *testing.T) {
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", time.Date(2026, 7, 14, 5, 30, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "compound.md")
	body := []byte("---\ntitle: Compound\n---\n# Compound\n\nPhysical custody is not semantic promotion.\n")
	if err := os.WriteFile(sourcePath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	manifest, err := BuildOwnedSourceManifest(homePath, "brain_custody_canary", []string{sourcePath}, time.Date(2026, 7, 14, 5, 31, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Items) != 1 {
		t.Fatalf("items = %d", len(manifest.Items))
	}
	item := manifest.Items[0]
	if item.BodyPolicy != "owned_snapshot" || !strings.HasPrefix(item.BodyRef, "artifact:sha256:") {
		t.Fatalf("owned item = %+v", item)
	}
	if item.BodyBytes != int64(len(body)) || item.MediaType != "text/markdown" {
		t.Fatalf("body metadata = %+v", item)
	}
	if _, err := RecordSourceManifest(homePath, manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}

	doctor, err := DoctorSourceManifest(homePath, manifest.SourceFamily)
	if err != nil {
		t.Fatal(err)
	}
	if doctor.Status != "ok" || !doctor.CustodyComplete || doctor.OwnedBodyCount != 1 {
		t.Fatalf("doctor after source loss = %+v", doctor)
	}
	if len(doctor.Items) != 1 || doctor.Items[0].BodyStatus != "ok" || doctor.Items[0].SourceStatus != "source_unavailable" {
		t.Fatalf("doctor item after source loss = %+v", doctor.Items)
	}

	target := filepath.Join(t.TempDir(), "restored", "compound.md")
	report, err := MaterializeSourceManifestItem(homePath, manifest.SourceFamily, item.ID, target)
	if err != nil {
		t.Fatal(err)
	}
	if report.BodyRef != item.BodyRef || report.Bytes != int64(len(body)) || report.TargetPath != target {
		t.Fatalf("materialize report = %+v", report)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("materialized bytes differ:\ngot  %q\nwant %q", got, body)
	}
}

func TestOwnedSourceManifestDoctorFailsMissingOrCorruptBody(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, path string)
		want   string
	}{
		{
			name: "missing",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
			want: "body_unavailable",
		},
		{
			name: "corrupt",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("corrupt"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "body_hash_mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			homePath := t.TempDir()
			if err := home.Init(homePath, "test", time.Now()); err != nil {
				t.Fatal(err)
			}
			sourcePath := filepath.Join(t.TempDir(), "source.md")
			if err := os.WriteFile(sourcePath, []byte("# body\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			manifest, err := BuildOwnedSourceManifest(homePath, "custody_failure", []string{sourcePath}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := RecordSourceManifest(homePath, manifest); err != nil {
				t.Fatal(err)
			}
			artifactPath := filepath.Join(homePath, "artifacts", "sha256", strings.TrimPrefix(manifest.Items[0].BodyRef, "artifact:sha256:"))
			tt.mutate(t, artifactPath)

			doctor, err := DoctorSourceManifest(homePath, manifest.SourceFamily)
			if err != nil {
				t.Fatal(err)
			}
			if doctor.Status != "fail" || doctor.CustodyComplete || doctor.HashMismatchCount != 1 {
				t.Fatalf("doctor = %+v", doctor)
			}
			if got := doctor.Items[0].BodyStatus; got != tt.want {
				t.Fatalf("body_status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOwnedSourceManifestDeduplicatesBodiesAndRefusesDestructiveMaterialize(t *testing.T) {
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	body := []byte("same body\n")
	var paths []string
	for _, name := range []string{"one.md", "two.md"} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	manifest, err := BuildOwnedSourceManifest(homePath, "dedupe", paths, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Items[0].BodyRef != manifest.Items[1].BodyRef {
		t.Fatalf("same bytes produced different refs: %q != %q", manifest.Items[0].BodyRef, manifest.Items[1].BodyRef)
	}
	if _, err := RecordSourceManifest(homePath, manifest); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.md")
	if err := os.WriteFile(target, []byte("different\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeSourceManifestItem(homePath, manifest.SourceFamily, manifest.Items[0].ID, target); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("destructive materialize err = %v", err)
	}
	if err := os.WriteFile(target, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeSourceManifestItem(homePath, manifest.SourceFamily, manifest.Items[0].ID, target); err != nil {
		t.Fatalf("byte-identical materialize must be idempotent: %v", err)
	}
}

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
	if doctor.CustodyComplete || doctor.OwnedBodyCount != 0 || doctor.InventoryOnlyCount != 1 {
		t.Fatalf("source-in-place must remain inventory-only: %+v", doctor)
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
