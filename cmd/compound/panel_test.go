package main

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"compound/internal/home"
	"compound/internal/panel"
)

func TestPanelLensesCLIJSON(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	manifest := filepath.Join("..", "..", "fixtures", "panel", "lens-manifest.yaml")
	captureStdout(t, func() { cmdPanel([]string{"import-lenses", manifest}) })

	out := captureStdout(t, func() { cmdPanel([]string{"lenses", "--target-kind", "memory_candidate", "--json"}) })
	var memorySelection panel.LensSelection
	if err := json.Unmarshal([]byte(out), &memorySelection); err != nil {
		t.Fatalf("parse memory lens JSON: %v\n%s", err, out)
	}
	if len(memorySelection.Included) != 6 || len(memorySelection.Excluded) != 0 {
		t.Fatalf("memory_candidate selection = %+v, want six included", memorySelection)
	}

	out = captureStdout(t, func() { cmdPanel([]string{"lenses", "--target-kind", "external_repo", "--json"}) })
	var externalSelection panel.LensSelection
	if err := json.Unmarshal([]byte(out), &externalSelection); err != nil {
		t.Fatalf("parse external lens JSON: %v\n%s", err, out)
	}
	if len(externalSelection.Included) != 1 || externalSelection.Included[0].NodeID != "lens:privacy-risk" {
		t.Fatalf("external_repo included = %+v, want only privacy-risk", externalSelection.Included)
	}
	reasons := map[string]string{}
	for _, excluded := range externalSelection.Excluded {
		reasons[excluded.Ref] = excluded.Reason
	}
	for _, id := range []string{"lens:duplicate-risk", "lens:evidence-strength", "lens:contradiction-with-existing-memory", "lens:fossilization-risk", "lens:inversion-falsification"} {
		if reasons[id] != "trigger_mismatch" {
			t.Fatalf("%s exclusion reason = %q, want trigger_mismatch (all reasons: %+v)", id, reasons[id], reasons)
		}
	}
}
