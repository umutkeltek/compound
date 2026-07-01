package capgraph

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSkillSurfacingMissesAppliesKnownMissesAndSkipsUnknown(t *testing.T) {
	home, store := newStore(t)
	if err := store.Add(Node{
		ID:       "skill:web-design-guidelines",
		Kind:     "skill",
		Name:     "Web Design Guidelines",
		Triggers: []string{"css"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := surfaced(store, "app store design review")["skill:web-design-guidelines"]; ok {
		t.Fatal("precondition: skill should not surface before miss feedback")
	}
	misses, err := LoadSkillSurfacingMisses(filepath.Join("..", "..", "fixtures", "capability-surfacing", "skill-surfacing-proposals.json"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := ApplySkillSurfacingMisses(home, misses, t0)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "review" || report.MissesRead != 2 || report.AppliedMisses != 1 || report.AppliedEdges == 0 {
		t.Fatalf("report = %+v", report)
	}
	if report.FalseTriggersRead != 0 || report.AppliedFalseTriggers != 0 {
		t.Fatalf("unexpected false-trigger counters = %+v", report)
	}
	if len(report.SkippedUnknown) != 1 || report.SkippedUnknown[0] != "production-audit" {
		t.Fatalf("skipped unknown = %+v", report.SkippedUnknown)
	}
	edges := readCapEdges(t, home)
	if !hasEdge(edges, EdMissedWhen, "context:app", "skill:web-design-guidelines") ||
		!hasEdge(edges, EdMissedWhen, "context:store", "skill:web-design-guidelines") {
		t.Fatalf("edges = %+v, want missed_when context report reason", edges)
	}
	if _, ok := surfaced(store, "app store design review")["skill:web-design-guidelines"]; !ok {
		t.Fatal("surfacing miss feedback should raise future surfacing for matching context")
	}
	replay, err := ApplySkillSurfacingMisses(home, misses, t0)
	if err != nil {
		t.Fatal(err)
	}
	if replay.AppliedEdges != 0 || replay.AppliedMisses != 0 {
		t.Fatalf("replay should be idempotent, got %+v", replay)
	}
}

func TestSkillSurfacingMissesAppliesFalseTriggers(t *testing.T) {
	home, store := newStore(t)
	nodeID := "tool:stitch.create-design-system-from-md"
	if err := store.Add(Node{
		ID:       nodeID,
		Kind:     "tool",
		Name:     "stitch.create_design_system_from_design_md",
		Triggers: []string{"design"},
	}); err != nil {
		t.Fatal(err)
	}
	task := "instruction design skill graph hook memory coherence"
	if _, ok := surfaced(store, task)[nodeID]; !ok {
		t.Fatal("precondition: generic design trigger should surface before false-trigger feedback")
	}
	path := filepath.Join(t.TempDir(), "skill-surfacing-feedback.json")
	if err := os.WriteFile(path, []byte(`{
  "generated": "2026-07-01T00:00:00Z",
  "false_triggers": [
    {
      "skill": "tool:stitch.create-design-system-from-md",
      "count": 1,
      "receipts": ["2026-07-01-instruction-design"],
      "reasons": ["instruction design skill graph hook memory coherence"],
      "maxConf": 1.0
    }
  ],
  "perReceipt": [
    {
      "dir": "2026-07-01-instruction-design",
      "kind": "human",
      "lanes": "codex",
      "false_triggered": [
        {
          "skill": "tool:stitch.create-design-system-from-md",
          "confidence": 1.0,
          "reason": "instruction design skill graph hook memory coherence"
        }
      ]
    }
  ]
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	misses, err := LoadSkillSurfacingMisses(path)
	if err != nil {
		t.Fatal(err)
	}
	report, err := ApplySkillSurfacingMisses(home, misses, t0)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || report.MissesRead != 0 || report.FalseTriggersRead != 1 || report.AppliedFalseTriggers != 1 || report.AppliedEdges == 0 {
		t.Fatalf("report = %+v", report)
	}
	edges := readCapEdges(t, home)
	if !hasEdge(edges, EdFalseTriggeredBy, "context:instruction", nodeID) ||
		!hasEdge(edges, EdFalseTriggeredBy, "context:design", nodeID) {
		t.Fatalf("edges = %+v, want false_triggered_by context feedback", edges)
	}
	if _, ok := surfaced(store, task)[nodeID]; ok {
		t.Fatal("false-trigger feedback should lower matching future surfacing below threshold")
	}
}
