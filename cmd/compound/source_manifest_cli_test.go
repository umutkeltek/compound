package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/home"
	"compound/internal/sourceobs"
)

func TestSourceManifestGbrainFromExportCLI(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	exportPath := filepath.Join("..", "..", "fixtures", "sourceobs", "gbrain-db-export-synthetic.json")
	createOut := captureStdout(t, func() {
		cmdSource([]string{"manifest-create", "--family", "gbrain_db_export_manifest", "--from-export", exportPath, "--json"})
	})
	var createReport sourceobs.SourceManifestReport
	if err := json.Unmarshal([]byte(createOut), &createReport); err != nil {
		t.Fatalf("parse create report: %v\n%s", err, createOut)
	}
	if createReport.ItemCount != 3 {
		t.Fatalf("create report = %+v", createReport)
	}

	showOut := captureStdout(t, func() {
		cmdSource([]string{"manifest-show", "gbrain_db_export_manifest", "--json"})
	})
	var manifest sourceobs.SourceManifest
	if err := json.Unmarshal([]byte(showOut), &manifest); err != nil {
		t.Fatalf("parse manifest: %v\n%s", err, showOut)
	}
	if manifest.FamilyMeta == nil || manifest.FamilyMeta.GenerationLabel != "current-2026-07-09" {
		t.Fatalf("expected generation_label in show, got %+v", manifest.FamilyMeta)
	}
	if len(manifest.Items) != 3 {
		t.Fatalf("items = %d", len(manifest.Items))
	}

	textOut := captureStdout(t, func() {
		cmdSource([]string{"manifest-show", "gbrain_db_export_manifest"})
	})
	if !strings.Contains(textOut, "generation_label: current-2026-07-09") {
		t.Fatalf("text show missing generation_label:\n%s", textOut)
	}

	doctorOut := captureStdout(t, func() {
		cmdSource([]string{"manifest-doctor", "gbrain_db_export_manifest", "--json"})
	})
	var doctor sourceobs.SourceManifestDoctorReport
	if err := json.Unmarshal([]byte(doctorOut), &doctor); err != nil {
		t.Fatalf("parse doctor: %v\n%s", err, doctorOut)
	}
	if doctor.Status != "ok" {
		t.Fatalf("doctor = %+v", doctor)
	}
}

func TestSourceManifestCLICommands(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	sourceDir := filepath.Join(t.TempDir(), "brain", "projects")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(sourceDir, "compound.md")
	if err := os.WriteFile(sourcePath, []byte("---\ntitle: Compound\n---\n# Compound\n\n## Facts\n- fact\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	createOut := captureStdout(t, func() {
		cmdSource([]string{"manifest-create", "--family", "brain_source_manifest", sourcePath, "--json"})
	})
	var createReport sourceobs.SourceManifestReport
	if err := json.Unmarshal([]byte(createOut), &createReport); err != nil {
		t.Fatalf("parse create report: %v\n%s", err, createOut)
	}
	if createReport.ItemCount != 1 || createReport.AuthorityRef == "" || createReport.ManifestPath == "" {
		t.Fatalf("create report = %+v", createReport)
	}

	showOut := captureStdout(t, func() {
		cmdSource([]string{"manifest-show", "brain_source_manifest", "--json"})
	})
	var manifest sourceobs.SourceManifest
	if err := json.Unmarshal([]byte(showOut), &manifest); err != nil {
		t.Fatalf("parse manifest: %v\n%s", err, showOut)
	}
	if len(manifest.Items) != 1 || manifest.Items[0].SourceRef != sourcePath {
		t.Fatalf("manifest = %+v", manifest)
	}
	if manifest.Items[0].BodyPolicy != "owned_snapshot" || manifest.Items[0].BodyRef == "" {
		t.Fatalf("generic CLI capture must default to Compound-owned custody: %+v", manifest.Items[0])
	}

	doctorOut := captureStdout(t, func() {
		cmdSource([]string{"manifest-doctor", "brain_source_manifest", "--json"})
	})
	var doctor sourceobs.SourceManifestDoctorReport
	if err := json.Unmarshal([]byte(doctorOut), &doctor); err != nil {
		t.Fatalf("parse doctor: %v\n%s", err, doctorOut)
	}
	if doctor.Status != "ok" || doctor.HashMismatchCount != 0 || !doctor.CustodyComplete {
		t.Fatalf("doctor = %+v", doctor)
	}
	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(t.TempDir(), "restored", "compound.md")
	materializeOut := captureStdout(t, func() {
		cmdSourceManifestMaterialize([]string{"brain_source_manifest", "--item", manifest.Items[0].ID, "--target", targetPath, "--json"})
	})
	var materialize sourceobs.SourceManifestMaterializeReport
	if err := json.Unmarshal([]byte(materializeOut), &materialize); err != nil {
		t.Fatalf("parse materialize report: %v\n%s", err, materializeOut)
	}
	if materialize.BodyRef != manifest.Items[0].BodyRef || materialize.TargetPath != targetPath || materialize.Bytes == 0 {
		t.Fatalf("materialize report = %+v", materialize)
	}
	restored, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(restored), "# Compound") {
		t.Fatalf("restored body = %q", restored)
	}

	drilldownOut := captureStdout(t, func() {
		cmdSource([]string{"manifest-drilldown", "brain_source_manifest", "--item", manifest.Items[0].ID, "--json"})
	})
	var drilldown sourceobs.SourceManifestDrilldown
	if err := json.Unmarshal([]byte(drilldownOut), &drilldown); err != nil {
		t.Fatalf("parse drilldown: %v\n%s", err, drilldownOut)
	}
	if !drilldown.SourceAvailable || !drilldown.HashMatches || len(drilldown.DrilldownPath) != 3 {
		if !drilldown.BodyAvailable || !drilldown.BodyMatches {
			t.Fatalf("drilldown lost both source and owned body = %+v", drilldown)
		}
	}
}

func TestSourceManifestCLISourceInPlaceIsExplicitAndCustodyIncomplete(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(t.TempDir(), "legacy.md")
	if err := os.WriteFile(sourcePath, []byte("# Legacy bridge\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		cmdSource([]string{"manifest-create", "--family", "legacy_bridge", sourcePath, "--source-in-place", "--json"})
	})
	manifest, err := sourceobs.LoadSourceManifest(homePath, "legacy_bridge")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Items[0].BodyPolicy != "source_in_place" || manifest.Items[0].BodyRef != "" {
		t.Fatalf("legacy item = %+v", manifest.Items[0])
	}
	doctor, err := sourceobs.DoctorSourceManifest(homePath, "legacy_bridge")
	if err != nil {
		t.Fatal(err)
	}
	if doctor.Status != "ok" || doctor.CustodyComplete || doctor.InventoryOnlyCount != 1 {
		t.Fatalf("legacy doctor = %+v", doctor)
	}
}
