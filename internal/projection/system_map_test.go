package projection

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildSystemMapReturnsEmptyProjectionWithoutTopologyFile(t *testing.T) {
	report, err := BuildSystemMap(t.TempDir(), t.TempDir(), time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildSystemMap: %v", err)
	}
	if report.SchemaVersion != SystemMapSchemaVersion || len(report.Nodes) != 0 || len(report.Edges) != 0 {
		t.Fatalf("report = %+v, want empty system-map projection", report)
	}
	if report.GeneratedAt != "2026-07-05T12:00:00Z" {
		t.Fatalf("generated_at = %q", report.GeneratedAt)
	}
}

func TestBuildSystemMapHydratesTopologyFromHome(t *testing.T) {
	home := t.TempDir()
	writeProjectionFile(t, filepath.Join(home, "config", "system-topology.json"), `{
  "nodes": [
    {"id":"capability","name":"Capability","lane":"meaning","status":"live","projection":"capability-authority"},
    {"id":"memory","name":"Memory","lane":"storage","status":"live","projection":"ledger-stats"},
    {"id":"capture","name":"Capture","lane":"capture","status":"planned","projection":"knowledge-sources"}
  ],
  "edges": [
    {"from":"capture","to":"memory","label":"feeds","status":"live"}
  ]
}`)
	writeProjectionFile(t, filepath.Join(home, "registries", "capability-nodes.jsonl"), "{}\n{}\n")
	writeProjectionFile(t, filepath.Join(home, "registries", "capability-edges.jsonl"), "{}\n")
	writeProjectionFile(t, filepath.Join(home, "registries", "knowledge-sources.jsonl"), `{"source_kind":"chat"}
{"kind":"paper"}
`)
	writeProjectionFile(t, filepath.Join(home, "ledger", "claims.jsonl"), "{}\n")
	writeProjectionFile(t, filepath.Join(home, "ledger", "entities.jsonl"), "{}\n{}\n")
	writeProjectionFile(t, filepath.Join(home, "ledger", "evidence.jsonl"), "{}\n{}\n{}\n")

	report, err := BuildSystemMap(home, t.TempDir(), time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildSystemMap: %v", err)
	}
	if len(report.Nodes) != 3 || len(report.Edges) != 1 || len(report.SourceRefs) != 1 {
		t.Fatalf("report shape = %+v", report)
	}
	if report.Stats["meaning"].Live != 1 || report.Stats["capture"].NotLive != 1 {
		t.Fatalf("stats = %+v", report.Stats)
	}
	if report.Nodes[0].Volume != "2 nodes · 1 edges" {
		t.Fatalf("capability volume = %q", report.Nodes[0].Volume)
	}
	if report.Nodes[1].Volume != "1 claims · 2 entities · 3 evidence" {
		t.Fatalf("memory volume = %q", report.Nodes[1].Volume)
	}
	if report.Nodes[2].Volume != "2 sources" || len(report.Nodes[2].EvidenceRefs) != 2 {
		t.Fatalf("knowledge volume/refs = %+v", report.Nodes[2])
	}
}

func writeProjectionFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
