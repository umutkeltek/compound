package distiller

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"gopkg.in/yaml.v3"
)

func TestImprovementPlanRecordsReviewArtifactsWithoutMutatingTarget(t *testing.T) {
	tests := []struct {
		name       string
		kind       string
		inboxKind  candidates.Kind
		subject    string
		statement  string
		verdict    string
		wantArea   string
		wantPrefix string
	}{
		{
			name:       "method upgrade",
			kind:       KindMethodUpgrade,
			inboxKind:  candidates.MethodUpgrade,
			subject:    "distill.memory.dates",
			statement:  "ground relative dates against trace timestamp before emitting memory candidates",
			verdict:    Park,
			wantArea:   "method",
			wantPrefix: "method_distill-memory-dates_",
		},
		{
			name:       "policy patch",
			kind:       KindPolicyPatch,
			inboxKind:  candidates.PolicyPatch,
			subject:    "distill.promotion.policy",
			statement:  "reject model-supplied promotion_basis unless source adapter already proved basis",
			verdict:    Park,
			wantArea:   "policy",
			wantPrefix: "policy_distill-promotion-policy_",
		},
		{
			name:       "source observation no-promote method upgrade",
			kind:       KindMethodUpgrade,
			inboxKind:  candidates.MethodUpgrade,
			subject:    "conversation_mining",
			statement:  "review mined conversation quality and lossiness before changing extraction",
			verdict:    candidates.NoPromote,
			wantArea:   "method",
			wantPrefix: "method_conversation-mining_",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			candidateID := "cand_" + strings.ReplaceAll(tt.wantArea, " ", "_")
			dir := filepath.Join(home, candidates.InboxDir(tt.inboxKind))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			candidate := Candidate{
				ID:           candidateID,
				Kind:         tt.kind,
				Subject:      tt.subject,
				Statement:    tt.statement,
				Detail:       "self_improvement",
				Confidence:   0.9,
				EvidenceRefs: []string{"trace:distiller", "eval:before"},
				Verdict:      tt.verdict,
			}
			data, err := json.MarshalIndent(candidate, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, candidateID+".json"), append(data, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
			targetPath := filepath.Join(t.TempDir(), "distiller-policy.md")
			original := []byte("# Distiller Policy\n\n")
			if err := os.WriteFile(targetPath, original, 0o644); err != nil {
				t.Fatal(err)
			}

			plan, err := BuildImprovementPlan(home, candidateID, targetPath, t0)
			if err != nil {
				t.Fatal(err)
			}
			if plan.SchemaVersion != ImprovementPlanSchemaVersion ||
				plan.Status != "review_required" ||
				plan.CandidateID != candidateID ||
				plan.CandidateKind != tt.kind ||
				plan.CandidateVerdict != tt.verdict ||
				plan.Draft.Area != tt.wantArea ||
				!strings.HasPrefix(plan.Draft.ImprovementID, tt.wantPrefix) ||
				!strings.Contains(plan.Draft.ProposedChange, strings.Fields(tt.statement)[0]) ||
				!plan.TargetExists ||
				plan.TargetHash == "" {
				t.Fatalf("bad improvement plan: %+v", plan)
			}
			paths, err := RecordImprovementPlan(home, plan)
			if err != nil {
				t.Fatal(err)
			}
			reviewData, err := os.ReadFile(paths.ReviewPath)
			if err != nil {
				t.Fatal(err)
			}
			review := string(reviewData)
			if !strings.Contains(review, "does not mutate distiller policy") ||
				!strings.Contains(review, "Required Evidence Before Apply") {
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
				t.Fatalf("improvement-plan must not promote claims, stat err=%v", err)
			}
		})
	}
}

func TestImprovementReviewRequiresEditedDecisionAndProofWithoutMutatingTruth(t *testing.T) {
	home := t.TempDir()
	candidateID := "cand_conversation_mining"
	dir := filepath.Join(home, candidates.InboxDir(candidates.MethodUpgrade))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{
		ID:           candidateID,
		Kind:         KindMethodUpgrade,
		Subject:      "conversation_mining",
		Statement:    "review mined conversation quality and lossiness before changing extraction",
		Detail:       "self_improvement",
		Confidence:   0.86,
		EvidenceRefs: []string{"source_observation:convmin", "no_promote:nop"},
		Verdict:      candidates.NoPromote,
	}
	data, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, candidateID+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildImprovementPlan(home, candidateID, "", time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	paths, err := RecordImprovementPlan(home, plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadImprovementReview(paths.ReviewYAMLPath); err == nil || !strings.Contains(err.Error(), "decision must") {
		t.Fatalf("unedited review YAML should fail decision validation, err=%v", err)
	}

	var review ImprovementReview
	reviewData, err := os.ReadFile(paths.ReviewYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(reviewData, &review); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reviewData), "eval_proof_refs") {
		t.Fatalf("review YAML draft should expose eval_proof_refs:\n%s", reviewData)
	}
	review.Decision = ImprovementReviewDecisionAccept
	review.Rationale = "Replay showed the current conversation-mining import is source-only; this method change narrows future extraction review without promoting facts."
	review.Confidence = 0.78
	data, err = yaml.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	editedPath := filepath.Join(home, "edited.review.yaml")
	if err := os.WriteFile(editedPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadImprovementReview(editedPath); err == nil || !strings.Contains(err.Error(), "eval_proof_refs") {
		t.Fatalf("accept review without proof should fail, err=%v", err)
	}

	review.EvalProofRefs = []string{"distiller_eval:conversation-mining-review-replay"}
	data, err = yaml.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(editedPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadImprovementReview(editedPath)
	if err != nil {
		t.Fatal(err)
	}
	report, err := RecordImprovementReview(home, loaded, time.Date(2026, 6, 30, 12, 10, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "reviewed" || report.Decision != ImprovementReviewDecisionAccept || report.ReviewedPath == "" {
		t.Fatalf("review report = %+v", report)
	}
	var reviewed ReviewedImprovement
	reviewedData, err := os.ReadFile(report.ReviewedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(reviewedData, &reviewed); err != nil {
		t.Fatal(err)
	}
	if reviewed.Status != "accepted" ||
		reviewed.PlanID != plan.ID ||
		reviewed.CandidateID != candidateID ||
		reviewed.ImprovementID != plan.Draft.ImprovementID ||
		len(reviewed.EvalProofRefs) != 1 {
		t.Fatalf("reviewed artifact = %+v", reviewed)
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("improvement-review must not promote claims, stat err=%v", err)
	}
}
