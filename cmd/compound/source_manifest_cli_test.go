package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/home"
	"compound/internal/sourceobs"
)

func TestSourceManifestGbrainCapturesMatchingDumpIntoArtifactCustody(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}

	dump := []byte(strings.Repeat("postgres-custom-dump-block\n", 128*1024))
	dumpPath := filepath.Join(t.TempDir(), "gbrain.dump")
	if err := os.WriteFile(dumpPath, dump, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(dump)

	fixturePath := filepath.Join("..", "..", "fixtures", "sourceobs", "gbrain-db-export-synthetic.json")
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var export sourceobs.GbrainDBExport
	if err := json.Unmarshal(fixture, &export); err != nil {
		t.Fatal(err)
	}
	export.DumpRef = "sha256:" + hex.EncodeToString(sum[:])
	exportPath := filepath.Join(t.TempDir(), "gbrain-export.json")
	exportBody, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exportPath, exportBody, 0o600); err != nil {
		t.Fatal(err)
	}

	captureStdout(t, func() {
		cmdSource([]string{"manifest-create", "--family", "gbrain_db_export_manifest", "--from-export", exportPath, "--dump", dumpPath, "--json"})
	})
	manifestBody, err := os.ReadFile(sourceobs.SourceManifestPath(homePath, sourceobs.GbrainDBExportManifestFamily))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(manifestBody, &raw); err != nil {
		t.Fatal(err)
	}
	meta, _ := raw["family_meta"].(map[string]any)
	if got, _ := meta["dump_body_ref"].(string); got != "artifact:"+export.DumpRef {
		t.Fatalf("dump_body_ref = %q, want artifact:%s", got, export.DumpRef)
	}
	if got, _ := meta["dump_bytes"].(float64); int64(got) != int64(len(dump)) {
		t.Fatalf("dump_bytes = %.0f, want %d", got, len(dump))
	}

	doctor, err := sourceobs.DoctorSourceManifest(homePath, sourceobs.GbrainDBExportManifestFamily)
	if err != nil {
		t.Fatal(err)
	}
	if doctor.Status != "ok" || !doctor.CustodyComplete {
		t.Fatalf("owned dump doctor = %+v", doctor)
	}
	dumpCheck := sourceManifestDoctorCheckByName(doctor, "gbrain_dump_custody")
	if dumpCheck.Status != "ok" || !strings.Contains(dumpCheck.Detail, "restore/read cutover not proven") {
		t.Fatalf("dump custody check = %+v", dumpCheck)
	}

	if err := os.Remove(dumpPath); err != nil {
		t.Fatal(err)
	}
	recoveredPath := filepath.Join(t.TempDir(), "recovered.dump")
	materializeOut := captureStdout(t, func() {
		cmdSource([]string{"manifest-materialize-dump", sourceobs.GbrainDBExportManifestFamily, "--target", recoveredPath, "--json"})
	})
	var recovery sourceobs.SourceManifestMaterializeReport
	if err := json.Unmarshal([]byte(materializeOut), &recovery); err != nil {
		t.Fatalf("parse materialize report: %v\n%s", err, materializeOut)
	}
	if recovery.Bytes != int64(len(dump)) {
		t.Fatalf("recovered bytes = %d, want %d", recovery.Bytes, len(dump))
	}
	recovered, err := os.ReadFile(recoveredPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(recovered) != string(dump) {
		t.Fatal("materialized dump differs from deleted source")
	}

	artifactPath := filepath.Join(homePath, "artifacts", "sha256", strings.TrimPrefix("artifact:"+export.DumpRef, home.ArtifactRefPrefix))
	if err := os.WriteFile(artifactPath, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	doctor, err = sourceobs.DoctorSourceManifest(homePath, sourceobs.GbrainDBExportManifestFamily)
	if err != nil {
		t.Fatal(err)
	}
	if doctor.Status != "fail" || doctor.CustodyComplete {
		t.Fatalf("corrupt dump doctor = %+v", doctor)
	}
	if check := sourceManifestDoctorCheckByName(doctor, "gbrain_dump_custody"); check.Status != "fail" {
		t.Fatalf("corrupt dump custody check = %+v", check)
	}
	if err := os.Remove(artifactPath); err != nil {
		t.Fatal(err)
	}
	doctor, err = sourceobs.DoctorSourceManifest(homePath, sourceobs.GbrainDBExportManifestFamily)
	if err != nil {
		t.Fatal(err)
	}
	missingCheck := sourceManifestDoctorCheckByName(doctor, "gbrain_dump_custody")
	if doctor.Status != "fail" || missingCheck.Status != "fail" || !strings.Contains(missingCheck.Detail, filepath.Join(homePath, "artifacts", "sha256")) {
		t.Fatalf("missing dump doctor = %+v check=%+v", doctor, missingCheck)
	}
}

func sourceManifestDoctorCheckByName(report sourceobs.SourceManifestDoctorReport, name string) sourceobs.SourceManifestDoctorCheck {
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	return sourceobs.SourceManifestDoctorCheck{Name: name, Status: "missing"}
}

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
