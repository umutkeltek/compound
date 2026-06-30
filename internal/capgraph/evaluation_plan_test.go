package capgraph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCapabilityEvaluationPlanRecordsReviewArtifactsWithoutMutatingGraph(t *testing.T) {
	home := t.TempDir()
	set, err := LoadCapabilityEvaluations(filepath.Join("..", "..", "fixtures", "capability-evaluations", "web-design-guidelines.json"))
	if err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(t.TempDir(), "capability-manifest.yaml")
	original := []byte("existing: manifest\n")
	if err := os.WriteFile(targetPath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildCapabilityEvaluationPlan(home, set, "web-design-guidelines", targetPath, t0)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != CapabilityEvaluationPlanSchemaVersion || plan.Status != "review_required" || plan.Capability != "web-design-guidelines" {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.NodeDraft.NodeID != "skill:web-design-guidelines" || plan.NodeDraft.Kind != "skill" || len(plan.NodeDraft.RequiredEvidence) == 0 {
		t.Fatalf("node draft = %+v", plan.NodeDraft)
	}
	if !plan.TargetExists || plan.TargetHash == "" || len(plan.EvidenceRefs) < 2 {
		t.Fatalf("target/evidence not preserved: %+v", plan)
	}
	paths, err := RecordCapabilityEvaluationPlan(home, plan)
	if err != nil {
		t.Fatal(err)
	}
	var stored CapabilityEvaluationPlan
	data, err := os.ReadFile(paths.PlanPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.ID != plan.ID || stored.NodeDraft.NodeID != plan.NodeDraft.NodeID {
		t.Fatalf("stored plan = %+v", stored)
	}
	if _, err := os.Stat(filepath.Join(home, "registries", "capability-nodes.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("evaluation-plan must not mutate capability nodes, stat err=%v", err)
	}
	manifest, err := LoadSkillManifest(paths.ManifestDraftPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != SkillManifestSchemaVersion || manifest.ID != "skill_manifest:"+plan.ID || len(manifest.Skills) != 1 {
		t.Fatalf("manifest = %+v", manifest)
	}
	if manifest.Skills[0].ID != "skill:web-design-guidelines" || manifest.Skills[0].Status != "needs_review" {
		t.Fatalf("manifest skill = %+v", manifest.Skills[0])
	}
	report, err := ApplySkillManifest(home, manifest, t0)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || len(report.NodeIDs) != 1 {
		t.Fatalf("manifest report = %+v", report)
	}
	store, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := store.Surface("web design guidelines")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("needs_review draft import must not surface: %+v", hits)
	}
	review, err := os.ReadFile(paths.ReviewPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Capability Evaluation Intake: web-design-guidelines",
		"does not mutate CapabilityGraph",
		"status `needs_review`",
		"Required Evidence Before Import",
		"one validation receipt or eval showing useful behavior",
	} {
		if !strings.Contains(string(review), want) {
			t.Fatalf("review missing %q:\n%s", want, string(review))
		}
	}
	if got := string(mustReadFile(t, targetPath)); got != string(original) {
		t.Fatalf("target mutated: %q", got)
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("evaluation-plan must not promote claims, stat err=%v", err)
	}
}

func TestCapabilityEvaluationPlanRejectsAlreadyKnownCapability(t *testing.T) {
	home, store := newStore(t)
	if err := store.Add(Node{ID: "skill:web-design-guidelines", Kind: "skill", Name: "Web Design Guidelines", Triggers: []string{"web", "design"}}); err != nil {
		t.Fatal(err)
	}
	set, err := LoadCapabilityEvaluations(filepath.Join("..", "..", "fixtures", "capability-evaluations", "web-design-guidelines.json"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = BuildCapabilityEvaluationPlan(home, set, "web-design-guidelines", "", t0)
	if err == nil || !strings.Contains(err.Error(), "already known") {
		t.Fatalf("err = %v, want already known", err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
