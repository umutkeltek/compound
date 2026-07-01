package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/candidates"
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

func TestPanelPlanRunShowCLIHappyPath(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	captureStdout(t, func() {
		cmdRoute([]string{"import-model-capabilities", filepath.Join("..", "..", "fixtures", "panel", "model-capabilities.yaml")})
	})
	captureStdout(t, func() {
		cmdPanel([]string{"import-lenses", filepath.Join("..", "..", "fixtures", "panel", "lens-manifest.yaml")})
	})
	seedPanelCLITarget(t, homePath, "mcand_example_api_clean")
	seedPanelCLITarget(t, homePath, "mcand_example_api_dissent")

	planOut := captureStdout(t, func() {
		cmdPanel([]string{"plan", "--target-kind", "memory_candidate", "--target", "mcand_example_api_clean", "--target", "mcand_example_api_dissent", "--state", "parked", "--not-privacy-sensitive", "synthetic fixtures", "--limit", "2", "--max-items", "2", "--json"})
	})
	var planned panel.Run
	if err := json.Unmarshal([]byte(planOut), &planned); err != nil {
		t.Fatalf("parse plan JSON: %v\n%s", err, planOut)
	}
	if planned.Status != "planned" || planned.Route.JudgeLane.ModelID != "panel/cheap-judge" {
		t.Fatalf("planned run = %+v", planned)
	}

	runOut := captureStdout(t, func() {
		cmdPanel([]string{"run", "--plan", planned.ID, "--fixtures", filepath.Join("..", "..", "fixtures", "panel"), "--json"})
	})
	var completed panel.Run
	if err := json.Unmarshal([]byte(runOut), &completed); err != nil {
		t.Fatalf("parse run JSON: %v\n%s", err, runOut)
	}
	if completed.Status != "verdicts_recorded" || len(completed.Findings) != 2 {
		t.Fatalf("completed run = %+v", completed)
	}

	showOut := captureStdout(t, func() { cmdPanel([]string{"show", planned.ID}) })
	for _, want := range []string{"- status: verdicts_recorded", "decision=promote", "verdict:", "omitted: panel/free-judge-tripwire reason=free lane may not judge"} {
		if !strings.Contains(showOut, want) {
			t.Fatalf("show output missing %q:\n%s", want, showOut)
		}
	}
}

func seedPanelCLITarget(t *testing.T, homePath, id string) {
	t.Helper()
	data := readFile(t, filepath.Join("..", "..", "fixtures", "panel", "targets", id+".json"))
	var payload map[string]any
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		t.Fatalf("decode target fixture %s: %v", id, err)
	}
	if err := candidates.WriteCandidate(homePath, candidates.Memory, id, payload); err != nil {
		t.Fatalf("write candidate %s: %v", id, err)
	}
}
