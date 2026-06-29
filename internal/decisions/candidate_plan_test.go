package decisions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
)

func TestDecisionCandidatePlanRecordsReviewArtifactsWithoutMutatingTarget(t *testing.T) {
	home := t.TempDir()
	candidateID := "cand_decision_review"
	dir := filepath.Join(home, candidates.InboxDir(candidates.Decision))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	candidate := decisionCandidateRecord{
		ID:         candidateID,
		Kind:       string(candidates.Decision),
		Subject:    "decision:route-free-model-judging",
		Statement:  "Use paid frontier lanes for final judgment only after cheap lanes produce evidence.",
		Detail:     "Keeps routing cost controlled while preserving quality gates.",
		Confidence: 0.86,
		Qualifiers: map[string]string{
			"kind":           KindOperational,
			"owner":          "compound",
			"scope":          "routing",
			"falsifier":      "cheap lanes repeatedly miss material defects",
			"review_trigger": "after three route-evidence regressions",
		},
		EvidenceRefs: []string{"trace:decision", "receipt:routing"},
		Verdict:      candidates.Park,
	}
	data, err := json.MarshalIndent(candidate, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, candidateID+".json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(t.TempDir(), "DEC-routing.md")
	original := []byte("# Existing Decision\n\n")
	if err := os.WriteFile(targetPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildDecisionCandidatePlan(home, candidateID, targetPath, time.Date(2026, 6, 29, 23, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != DecisionCandidatePlanSchemaVersion ||
		plan.Status != "review_required" ||
		plan.CandidateID != candidateID ||
		plan.CandidateVerdict != candidates.Park ||
		plan.DecisionKey != "route-free-model-judging" ||
		plan.LifecycleDraft.DecisionID != "route-free-model-judging" ||
		plan.LifecycleDraft.EntityID != "decision:route-free-model-judging" ||
		plan.LifecycleDraft.Status != StatusProposed ||
		plan.LifecycleDraft.Falsifier != "cheap lanes repeatedly miss material defects" ||
		plan.LifecycleDraft.ReviewTrigger != "after three route-evidence regressions" ||
		plan.TargetHash == "" {
		t.Fatalf("bad decision candidate plan: %+v", plan)
	}
	paths, err := RecordDecisionCandidatePlan(home, plan)
	if err != nil {
		t.Fatal(err)
	}
	planData, err := os.ReadFile(paths.PlanPath)
	if err != nil {
		t.Fatal(err)
	}
	var recorded DecisionCandidatePlan
	if err := json.Unmarshal(planData, &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.ID != plan.ID || recorded.LifecycleDraft.EntityID != "decision:route-free-model-judging" {
		t.Fatalf("recorded plan = %+v", recorded)
	}
	review, err := os.ReadFile(paths.ReviewPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(review), "does not mutate decision source files") ||
		!strings.Contains(string(review), "## Draft DecisionLifecycle") {
		t.Fatalf("review missing guardrails:\n%s", review)
	}
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("target mutated:\n%s", got)
	}
}

func TestDecisionCandidatePlanRequiresParkedDecisionCandidate(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, candidates.InboxDir(candidates.Decision))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	candidate := decisionCandidateRecord{
		ID:      "cand_rejected_decision",
		Kind:    string(candidates.Decision),
		Subject: "decision:rejected",
		Verdict: candidates.Reject,
	}
	data, err := json.MarshalIndent(candidate, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, candidate.ID+".json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildDecisionCandidatePlan(home, candidate.ID, "", time.Date(2026, 6, 29, 23, 31, 0, 0, time.UTC)); err == nil ||
		!strings.Contains(err.Error(), "want park") {
		t.Fatalf("expected parked-verdict error, got %v", err)
	}
}
