package capgraph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/candidates"
)

var t0 = time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) (string, *Store) {
	t.Helper()
	home := t.TempDir()
	s, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	return home, s
}

func surfaced(s *Store, task string) map[string]float64 {
	hits, _ := s.Surface(task)
	m := map[string]float64{}
	for _, h := range hits {
		m[h.Node.ID] = h.Score
	}
	return m
}

func TestSurfaceByTrigger(t *testing.T) {
	_, s := newStore(t)
	s.Add(Node{ID: "skill:pricing", Kind: "skill", Name: "pricing", Triggers: []string{"pricing", "paywall"}})
	s.Add(Node{ID: "skill:onboarding", Kind: "skill", Name: "onboarding", Triggers: []string{"signup"}})
	got := surfaced(s, "improve the pricing page")
	if _, ok := got["skill:pricing"]; !ok {
		t.Error("a node whose trigger matches the task should surface")
	}
	if _, ok := got["skill:onboarding"]; ok {
		t.Error("a node with no matching trigger should NOT surface")
	}
}

// The headline P8 proof: a missed_when observation CHANGES future surfacing probability — the
// graph learns which capability fits which context.
func TestMissedWhenChangesSurfacing(t *testing.T) {
	_, s := newStore(t)
	s.Add(Node{ID: "skill:cro", Kind: "skill", Name: "cro", Triggers: []string{"pricing"}}) // not "paywall"
	// before: "paywall" task does NOT surface the node (its only trigger is "pricing").
	if _, ok := surfaced(s, "fix the paywall")["skill:cro"]; ok {
		t.Fatal("precondition: node should not surface for 'paywall' before the observation")
	}
	// the distiller observed it was relevant-but-missed for the 'paywall' context.
	if _, err := s.Observe("skill:cro", EdMissedWhen, "paywall", 0.9, t0); err != nil {
		t.Fatal(err)
	}
	// after: the same task now surfaces it — surfacing probability changed.
	if _, ok := surfaced(s, "fix the paywall")["skill:cro"]; !ok {
		t.Error("after a missed_when observation, the node must surface for that context (the graph learned)")
	}
}

// A false_triggered_by observation lowers surfacing for that context.
func TestFalseTriggeredDownweights(t *testing.T) {
	_, s := newStore(t)
	s.Add(Node{ID: "skill:x", Kind: "skill", Name: "x", Triggers: []string{"deploy"}})
	if _, ok := surfaced(s, "deploy the app")["skill:x"]; !ok {
		t.Fatal("precondition: node surfaces for 'deploy'")
	}
	s.Observe("skill:x", EdFalseTriggeredBy, "deploy", 2.0, t0) // strong false signal
	if _, ok := surfaced(s, "deploy the app")["skill:x"]; ok {
		t.Error("a strong false_triggered_by should pull the node below the surfacing threshold")
	}
}

func TestRetiredNeverSurfaces(t *testing.T) {
	_, s := newStore(t)
	s.Add(Node{ID: "skill:old", Kind: "skill", Name: "old", Triggers: []string{"legacy"}})
	s.Add(Node{ID: "skill:old", Kind: "skill", Name: "old", Triggers: []string{"legacy"}, Retired: true}) // upsert: retire
	if _, ok := surfaced(s, "legacy thing")["skill:old"]; ok {
		t.Error("a retired node must never surface")
	}
}

// Surface OFFERS, it does not execute: it returns nodes and produces no side effects (no new
// edges/files), so a match can never auto-run a chain.
func TestSurfaceNeverExecutes(t *testing.T) {
	home, s := newStore(t)
	s.Add(Node{ID: "tool:rm", Kind: "tool", Name: "rm", Triggers: []string{"delete"}})
	before, _ := os.ReadDir(filepath.Join(home, "registries"))
	hits, err := s.Surface("delete the cache")
	if err != nil || len(hits) != 1 {
		t.Fatalf("surface should offer the tool, got %d (%v)", len(hits), err)
	}
	after, _ := os.ReadDir(filepath.Join(home, "registries"))
	if len(after) != len(before) {
		t.Error("Surface must have NO side effects (it offers, never executes)")
	}
}

// The distiller→graph bridge: an operationalized CapabilityObservation in the inbox is consumed
// into a feedback edge that changes surfacing.
func TestConsumeFromDistillerInbox(t *testing.T) {
	home, s := newStore(t)
	s.Add(Node{ID: "skill:cro", Kind: "skill", Name: "cro", Triggers: []string{"pricing"}})
	// write what the distiller operationalizes: subject=node, statement=context, detail=signal.
	dir := filepath.Join(home, candidates.InboxDir(candidates.CapabilityObs))
	os.MkdirAll(dir, 0o755)
	obs := map[string]any{"id": "cand_1", "kind": "capability_observation", "subject": "skill:cro", "statement": "paywall page", "detail": EdMissedWhen, "confidence": 0.9, "verdict": "operationalize"}
	b, _ := json.Marshal(obs)
	os.WriteFile(filepath.Join(dir, "cand_1.json"), b, 0o644)
	// a PARKED observation in the same inbox must NOT be applied (Consume respects adjudication).
	parked := map[string]any{"id": "cand_2", "kind": "capability_observation", "subject": "skill:other", "statement": "signup", "detail": EdMissedWhen, "confidence": 0.3, "verdict": "park"}
	pb, _ := json.Marshal(parked)
	os.WriteFile(filepath.Join(dir, "cand_2.json"), pb, 0o644)

	n, err := s.Consume(home, t0)
	if err != nil || n != 1 {
		t.Fatalf("consume should apply 1 (operationalized) observation, got %d (%v)", n, err)
	}
	// multi-word context "paywall page" must surface on a natural-language task (tokenized).
	if _, ok := surfaced(s, "improve the paywall page conversion")["skill:cro"]; !ok {
		t.Error("after consuming the observation, the node must surface for that (tokenized) context")
	}
	// re-consume must be IDEMPOTENT — surfacing score unchanged.
	score1 := surfaced(s, "fix the paywall")["skill:cro"]
	s.Consume(home, t0)
	if score2 := surfaced(s, "fix the paywall")["skill:cro"]; score2 != score1 {
		t.Errorf("re-consume must be idempotent: score %v -> %v", score1, score2)
	}
}
