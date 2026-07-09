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

	doctorOut := captureStdout(t, func() {
		cmdSource([]string{"manifest-doctor", "brain_source_manifest", "--json"})
	})
	var doctor sourceobs.SourceManifestDoctorReport
	if err := json.Unmarshal([]byte(doctorOut), &doctor); err != nil {
		t.Fatalf("parse doctor: %v\n%s", err, doctorOut)
	}
	if doctor.Status != "ok" || doctor.HashMismatchCount != 0 {
		t.Fatalf("doctor = %+v", doctor)
	}

	drilldownOut := captureStdout(t, func() {
		cmdSource([]string{"manifest-drilldown", "brain_source_manifest", "--item", manifest.Items[0].ID, "--json"})
	})
	var drilldown sourceobs.SourceManifestDrilldown
	if err := json.Unmarshal([]byte(drilldownOut), &drilldown); err != nil {
		t.Fatalf("parse drilldown: %v\n%s", err, drilldownOut)
	}
	if !drilldown.SourceAvailable || !drilldown.HashMatches || len(drilldown.DrilldownPath) != 3 {
		t.Fatalf("drilldown = %+v", drilldown)
	}
}
