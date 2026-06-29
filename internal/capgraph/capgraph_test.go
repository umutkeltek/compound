package capgraph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestApplySkillManifestImportsSkillsAndEdges(t *testing.T) {
	home, s := newStore(t)
	manifest := SkillManifest{
		SchemaVersion: SkillManifestSchemaVersion,
		ID:            "skill_manifest_fixture",
		AdapterID:     "fixture.skill-manifest",
		SourceSystem:  "fixture-os",
		ObservedAt:    "2026-06-29T00:00:00Z",
		SourceRef: SkillSourceRef{
			Path:        "fixtures/skills/skill-manifest.yaml",
			Kind:        "generated_view",
			Owner:       "capability-fixture",
			SourceClass: "generated",
			Revision:    "rev_fixture",
		},
		EvidenceRefs: []string{"artifact_sha256:skill-fixture"},
		Skills: []SkillManifestEntry{
			{
				ID:                  "skill:pricing",
				Name:                "Pricing",
				Status:              "active",
				Triggers:            []string{"pricing", "paywall"},
				Capabilities:        []string{"monetization"},
				CompatibleHarnesses: []string{"codex", "claude_code"},
				Requires:            []string{"tool:analytics"},
				ValidatedBy:         []string{"eval:pricing-copy"},
				Weight:              2,
			},
			{
				ID:             "lens:paywall-risk",
				Kind:           "lens",
				Name:           "Paywall Risk Lens",
				Status:         "active",
				Triggers:       []string{"paywall"},
				Specializes:    []string{"skill:pricing"},
				ConflictsWith:  []string{"skill:old-paywall"},
				ReplacementFor: []string{"skill:old-paywall"},
			},
			{
				ID:       "skill:old-paywall",
				Name:     "Old Paywall",
				Status:   "retired",
				Triggers: []string{"paywall"},
			},
		},
	}
	report, err := ApplySkillManifest(home, manifest, t0)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || len(report.NodeIDs) != 3 || len(report.EdgeIDs) != 7 {
		t.Fatalf("report = %+v", report)
	}
	nodes, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	if byID["skill:pricing"].Kind != "skill" || byID["skill:pricing"].Weight != 2 {
		t.Fatalf("pricing node = %+v", byID["skill:pricing"])
	}
	if byID["lens:paywall-risk"].Kind != "lens" {
		t.Fatalf("lens node = %+v", byID["lens:paywall-risk"])
	}
	if !byID["skill:old-paywall"].Retired {
		t.Fatalf("old skill should be retired: %+v", byID["skill:old-paywall"])
	}
	hits := surfaced(s, "improve paywall monetization")
	if _, ok := hits["skill:pricing"]; !ok {
		t.Fatal("imported skill should surface for paywall monetization task")
	}
	if _, ok := hits["lens:paywall-risk"]; !ok {
		t.Fatal("imported lens should surface for paywall task")
	}
	if _, ok := hits["skill:old-paywall"]; ok {
		t.Fatal("retired imported skill must not surface")
	}
	edges := readCapEdges(t, home)
	if !hasEdge(edges, EdRequires, "skill:pricing", "tool:analytics") ||
		!hasEdge(edges, EdValidatedBy, "skill:pricing", "eval:pricing-copy") ||
		!hasEdge(edges, EdSurfacesWith, "skill:pricing", "harness:codex") ||
		!hasEdge(edges, EdSurfacesWith, "skill:pricing", "harness:claude_code") ||
		!hasEdge(edges, EdSpecializes, "lens:paywall-risk", "skill:pricing") ||
		!hasEdge(edges, EdConflictsWith, "lens:paywall-risk", "skill:old-paywall") ||
		!hasEdge(edges, EdRetiredBy, "skill:old-paywall", "lens:paywall-risk") {
		t.Fatalf("edges = %+v", edges)
	}
}

func TestValidateSkillManifestRejectsUnknownKind(t *testing.T) {
	manifest := validSkillManifestForTest()
	manifest.Skills[0].Kind = "plugin"
	report := ValidateSkillManifest(manifest)
	if report.Status != "fail" {
		t.Fatalf("status = %s, want fail", report.Status)
	}
}

func TestLoadSkillManifestRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(`
schema_version: compound.skill_manifest.v0.1
id: skill_manifest_fixture
adapter_id: fixture.skill-manifest
source_system: fixture-os
observed_at: "2026-06-29T00:00:00Z"
source_ref:
  path: fixtures/skills/skill-manifest.yaml
  kind: generated_view
  owner: capability-fixture
  source_class: generated
  revision: rev_fixture
evidence_refs:
  - artifact_sha256:skill-fixture
skills:
  - id: skill:pricing
    name: Pricing
    status: active
    triggers:
      - pricing
    hidden_payload: should-not-pass
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSkillManifest(path); err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}

func TestApplySkillManifestDoesNotSurfaceNeedsReviewSkill(t *testing.T) {
	home, s := newStore(t)
	manifest := validSkillManifestForTest()
	manifest.Skills[0].Status = "needs_review"
	if _, err := ApplySkillManifest(home, manifest, t0); err != nil {
		t.Fatal(err)
	}
	if hits := surfaced(s, "pricing"); hits["skill:pricing"] != 0 {
		t.Fatalf("needs_review skill surfaced: %+v", hits)
	}
}

func TestValidateSkillManifestRejectsUnknownStatus(t *testing.T) {
	manifest := validSkillManifestForTest()
	manifest.Skills[0].Status = "ambient"
	report := ValidateSkillManifest(manifest)
	if report.Status != "fail" {
		t.Fatalf("status = %s, want fail", report.Status)
	}
}

func TestValidateSkillManifestRejectsBadObservedAt(t *testing.T) {
	manifest := validSkillManifestForTest()
	manifest.ObservedAt = "today"
	report := ValidateSkillManifest(manifest)
	if report.Status != "fail" {
		t.Fatalf("status = %s, want fail", report.Status)
	}
}

func validSkillManifestForTest() SkillManifest {
	return SkillManifest{
		SchemaVersion: SkillManifestSchemaVersion,
		ID:            "skill_manifest_fixture",
		AdapterID:     "fixture.skill-manifest",
		SourceSystem:  "fixture-os",
		ObservedAt:    "2026-06-29T00:00:00Z",
		SourceRef: SkillSourceRef{
			Path:        "fixtures/skills/skill-manifest.yaml",
			Kind:        "generated_view",
			Owner:       "capability-fixture",
			SourceClass: "generated",
			Revision:    "rev_fixture",
		},
		EvidenceRefs: []string{"artifact_sha256:skill-fixture"},
		Skills: []SkillManifestEntry{{
			ID:       "skill:pricing",
			Name:     "Pricing",
			Status:   "active",
			Triggers: []string{"pricing"},
		}},
	}
}

func readCapEdges(t *testing.T, home string) []Edge {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "registries", "capability-edges.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var edges []Edge
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var edge Edge
		if err := json.Unmarshal([]byte(line), &edge); err != nil {
			t.Fatalf("parse edge %q: %v", line, err)
		}
		edges = append(edges, edge)
	}
	return edges
}

func hasEdge(edges []Edge, kind, from, to string) bool {
	for _, edge := range edges {
		if edge.Kind == kind && edge.From == from && edge.To == to {
			return true
		}
	}
	return false
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
