package capgraph

import (
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
	if len(report.SkippedUnknown) != 1 || report.SkippedUnknown[0] != "production-audit" {
		t.Fatalf("skipped unknown = %+v", report.SkippedUnknown)
	}
	edges := readCapEdges(t, home)
	if !hasEdge(edges, EdMissedWhen, "context:app", "skill:web-design-guidelines") ||
		!hasEdge(edges, EdMissedWhen, "context:store", "skill:web-design-guidelines") {
		t.Fatalf("edges = %+v, want missed_when context from report reason", edges)
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
