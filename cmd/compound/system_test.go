package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/projection"
)

func TestSystemMapJSONCommand(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	writeCmdTestFile(t, filepath.Join(homePath, "config", "system-topology.json"), `{
  "nodes": [
    {"id":"runtime","name":"Runtime","lane":"meaning","status":"live","projection":"static"}
  ],
  "edges": []
}`)

	out := captureStdout(t, func() {
		cmdSystem([]string{"map", "--json"})
	})
	var report projection.SystemMapProjection
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse system map json: %v\n%s", err, out)
	}
	if report.SchemaVersion != projection.SystemMapSchemaVersion || len(report.Nodes) != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.Nodes[0].ID != "runtime" || report.Stats["meaning"].Live != 1 {
		t.Fatalf("node/stats = %+v %+v", report.Nodes, report.Stats)
	}
}

func TestSystemStatsJSONCommand(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	writeCmdTestFile(t, filepath.Join(homePath, "ledger", "claims.jsonl"), "{}\n{}\n")
	writeCmdTestFile(t, filepath.Join(homePath, "ledger", "entities.jsonl"), "{}\n")
	writeCmdTestFile(t, filepath.Join(homePath, "ledger", "evidence.jsonl"), "{}\n")
	writeCmdTestFile(t, filepath.Join(homePath, "ledger", "execution-route-evidence.jsonl"), "{}\n")
	writeCmdTestFile(t, filepath.Join(homePath, "ledger", "no-promote-decisions.jsonl"), "{}\n")
	writeCmdTestFile(t, filepath.Join(homePath, "ledger", "knowledge-imports.jsonl"), "{}\n")
	writeCmdTestFile(t, filepath.Join(homePath, "registries", "knowledge-sources.jsonl"), `{"source_kind":"chatgpt"}`+"\n"+`{"source_kind":"arxiv"}`+"\n")
	if err := os.MkdirAll(filepath.Join(homePath, "inbox", "external-repo-intake", "repo-one"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() { cmdSystem([]string{"stats", "--json"}) })
	var report projection.SystemStatsProjection
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse system stats json: %v\n%s", err, out)
	}
	if report.SchemaVersion != projection.SystemStatsSchemaVersion || report.Claims != 2 || report.KnowledgeSources != 2 || report.IntakeRepos != 1 {
		t.Fatalf("report = %+v", report)
	}
	if len(report.KnowledgeSourceKinds) != 2 {
		t.Fatalf("source kinds = %+v", report.KnowledgeSourceKinds)
	}
}

func TestSystemMemoryGraphAndLineageJSONCommands(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	writeCmdTestFile(t, filepath.Join(homePath, "ledger", "claims.jsonl"),
		`{"id":"claim_a","entity":"product:demo","predicate":"has_signal","object":"downloads rose","evidence_refs":["ev_a"]}`+"\n")
	writeCmdTestFile(t, filepath.Join(homePath, "ledger", "edges.jsonl"), "")
	writeCmdTestFile(t, filepath.Join(homePath, "ledger", "evidence.jsonl"),
		`{"id":"ev_a","source_kind":"observation","source_ref":"obs_a","quote_text":"downloads rose"}`+"\n")
	writeCmdTestFile(t, filepath.Join(homePath, "ledger", "adjudications.jsonl"), "")

	graphOut := captureStdout(t, func() { cmdSystem([]string{"memory-graph", "product:demo", "--json"}) })
	var graph projection.MemoryEgoGraphProjection
	if err := json.Unmarshal([]byte(graphOut), &graph); err != nil {
		t.Fatalf("parse memory graph json: %v\n%s", err, graphOut)
	}
	if graph.SchemaVersion != projection.MemoryEgoGraphSchemaVersion || !graph.Found || len(graph.Nodes) != 1 {
		t.Fatalf("graph = %+v", graph)
	}

	lineageOut := captureStdout(t, func() { cmdSystem([]string{"lineage", "ev_a", "--json"}) })
	var lineage projection.LineageProjection
	if err := json.Unmarshal([]byte(lineageOut), &lineage); err != nil {
		t.Fatalf("parse lineage json: %v\n%s", err, lineageOut)
	}
	if lineage.SchemaVersion != projection.LineageSchemaVersion || !lineage.Found || len(lineage.Steps) < 2 {
		t.Fatalf("lineage = %+v", lineage)
	}
}

// cmdInventoryLongID is a long fake id: CLI text may show only its last four characters.
const cmdInventoryLongID = "aws:account:9c1f7e3b5a2d4c6e8f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e9b2d4f6a8c0e2wq4"

func cmdInventoryObservation(observedAt time.Time) string {
	at := observedAt.UTC().Format(time.RFC3339)
	return `{"schema_version":"compound.system_inventory_observation.v0.1","observed_at":"` + at + `",
 "collector":{"name":"example-collector","version":"0.0.1"},
 "sources":[{"id":"cloud","provider":"aws","state":"ok","detail":"","observed_at":"` + at + `","duration_ms":10},
            {"id":"dns","provider":"dnsco","state":"needs_sign_in","detail":"token expired","observed_at":"` + at + `","duration_ms":5}],
 "resources":[
  {"id":"` + cmdInventoryLongID + `","kind":"account","provider":"aws","label":"Example account","parent":"","source":"cloud","places":[],"links":[],
   "facts":{"identity_ok":{"value":true,"unit":"bool","state":"ok","detail":"","observed_at":"` + at + `"},
            "resources":{"value":7,"unit":"count","state":"ok","detail":"","observed_at":"` + at + `"},
            "credit_left":{"value":null,"unit":"usd","state":"needs_owner","detail":"owner only","observed_at":"` + at + `"}}},
  {"id":"node:example-1","kind":"host","provider":"examplehv","label":"lab-node","parent":"","source":"cloud","places":[],"links":[],
   "facts":{"disk_pct":{"value":91,"unit":"pct","state":"ok","detail":"","observed_at":"` + at + `"}}}
 ]}`
}

func TestSystemInventoryRefreshCommandStoresSnapshot(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	file := filepath.Join(t.TempDir(), "observation.json")
	observedAt := time.Now().Add(-time.Hour)
	writeCmdTestFile(t, file, cmdInventoryObservation(observedAt))

	out := captureStdout(t, func() {
		cmdSystem([]string{"inventory", "refresh", "--collector-json", file, "--json"})
	})
	var res projection.InventoryRefreshResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse refresh json: %v\n%s", err, out)
	}
	if res.SchemaVersion != projection.InventoryRefreshSchemaVersion || res.Resources != 2 || res.Sources != 2 {
		t.Fatalf("refresh result = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(homePath, "observed", "inventory", "snapshot.json")); err != nil {
		t.Fatalf("snapshot not stored: %v", err)
	}
	text := captureStdout(t, func() {
		cmdSystem([]string{"inventory", "refresh", "--collector-json", file})
	})
	if !strings.Contains(text, "inventory imported: 2 resources") || !strings.Contains(text, "previous snapshot kept") {
		t.Fatalf("refresh text = %q", text)
	}
}

func writeCmdTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
