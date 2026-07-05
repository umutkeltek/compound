package projection

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildMemoryEgoGraphProjectsLedgerClaims(t *testing.T) {
	home := t.TempDir()
	writeProjectionTestFile(t, filepath.Join(home, "ledger", "claims.jsonl"),
		`{"id":"claim_a","entity":"product:demo","predicate":"has_signal","object":"downloads rose","confidence":0.91,"evidence_refs":["ev_a"]}`+"\n"+
			`{"id":"claim_b","entity":"product:demo","predicate":"has_hypothesis","object":"listing changed","confidence":0.82}`+"\n"+
			`{"id":"claim_c","entity":"other","predicate":"supports","object":"demo"}`+"\n")
	writeProjectionTestFile(t, filepath.Join(home, "ledger", "edges.jsonl"),
		`{"seq":1,"from":"claim_a","to":"claim_c","kind":"supports"}`+"\n")

	graph, err := BuildMemoryEgoGraph(home, "product:demo", 60)
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Found || graph.ResolvedAs != "entity product:demo" {
		t.Fatalf("graph resolution = %+v", graph)
	}
	if len(graph.Nodes) != 3 || len(graph.Links) != 1 {
		t.Fatalf("graph nodes/links = %d/%d %+v", len(graph.Nodes), len(graph.Links), graph)
	}
}

func TestBuildLineageProjectsEvidenceToClaim(t *testing.T) {
	home := t.TempDir()
	writeProjectionTestFile(t, filepath.Join(home, "ledger", "evidence.jsonl"),
		`{"id":"ev_a","source_kind":"observation","source_ref":"obs_a","quote_text":"downloads rose","observed_at":"2026-07-05T00:00:00Z"}`+"\n")
	writeProjectionTestFile(t, filepath.Join(home, "ledger", "claims.jsonl"),
		`{"id":"claim_a","entity":"product:demo","predicate":"has_signal","object":"downloads rose","evidence_refs":["ev_a"],"created_at":"2026-07-05T00:01:00Z"}`+"\n")
	writeProjectionTestFile(t, filepath.Join(home, "ledger", "adjudications.jsonl"), "")

	lineage, err := BuildLineage(home, "ev_a")
	if err != nil {
		t.Fatal(err)
	}
	if !lineage.Found || lineage.ResolvedAs != "evidence ev_a" {
		t.Fatalf("lineage resolution = %+v", lineage)
	}
	if len(lineage.Steps) < 3 {
		t.Fatalf("lineage steps = %+v", lineage.Steps)
	}
	if lineage.Steps[0].Kind != "evidence" || lineage.Steps[1].Kind != "source" || lineage.Steps[2].Kind != "claim" {
		t.Fatalf("lineage order = %+v", lineage.Steps)
	}
}

func writeProjectionTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
