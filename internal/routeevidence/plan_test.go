package routeevidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/candidates"
)

func TestRoutePolicyPlanRecordsReviewArtifactsWithoutMutatingTarget(t *testing.T) {
	home := t.TempDir()
	candidateID := "cand_route_policy"
	dir := filepath.Join(home, candidates.InboxDir(candidates.RouteObservation))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	candidate := routeCandidate{
		ID:           candidateID,
		Kind:         string(candidates.RouteObservation),
		Subject:      "task:cheap-extract",
		Statement:    "use cheap extraction lane for bounded candidate-only summaries when eval gate passes",
		Detail:       "model_router_cost_guard",
		Confidence:   0.87,
		EvidenceRefs: []string{"route:req_123", "transition:tr_route"},
		Verdict:      candidates.Park,
	}
	data, err := json.MarshalIndent(candidate, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, candidateID+".json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(t.TempDir(), "route-policy.json")
	original := []byte(`{"routes":[]}` + "\n")
	if err := os.WriteFile(targetPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildRoutePolicyPlan(home, candidateID, targetPath, t0)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != RoutePolicyPlanSchemaVersion ||
		plan.Status != "review_required" ||
		plan.CandidateID != candidateID ||
		plan.CandidateVerdict != candidates.Park ||
		plan.RouteKey != "task-cheap-extract" ||
		plan.PolicyDraft.PolicyID == "" ||
		!strings.Contains(plan.PolicyDraft.RoutePreference, "cheap extraction lane") ||
		len(plan.PolicyDraft.RequiredEvidence) != 4 ||
		!plan.TargetExists ||
		plan.TargetHash == "" {
		t.Fatalf("bad route policy plan: %+v", plan)
	}
	paths, err := RecordRoutePolicyPlan(home, plan)
	if err != nil {
		t.Fatal(err)
	}
	var recorded RoutePolicyPlan
	recordedData, err := os.ReadFile(paths.PlanPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(recordedData, &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.ID != plan.ID || recorded.PolicyDraft.PolicyID != plan.PolicyDraft.PolicyID {
		t.Fatalf("recorded plan mismatch: %+v", recorded)
	}
	review, err := os.ReadFile(paths.ReviewPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(review), "does not mutate gateway/provider config") ||
		!strings.Contains(string(review), "Required Evidence Before Apply") {
		t.Fatalf("review missing safety/procedure text:\n%s", review)
	}
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("target mutated:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("route-plan must not promote claims, stat err=%v", err)
	}
}
