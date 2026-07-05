package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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

func writeCmdTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
