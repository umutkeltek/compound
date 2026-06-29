package distiller

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/candidates"
)

func TestImprovementPlanRecordsReviewArtifactsWithoutMutatingTarget(t *testing.T) {
	tests := []struct {
		name       string
		kind       string
		inboxKind  candidates.Kind
		subject    string
		statement  string
		wantArea   string
		wantPrefix string
	}{
		{
			name:       "method upgrade",
			kind:       KindMethodUpgrade,
			inboxKind:  candidates.MethodUpgrade,
			subject:    "distill.memory.dates",
			statement:  "ground relative dates against trace timestamp before emitting memory candidates",
			wantArea:   "method",
			wantPrefix: "method_distill-memory-dates_",
		},
		{
			name:       "policy patch",
			kind:       KindPolicyPatch,
			inboxKind:  candidates.PolicyPatch,
			subject:    "distill.promotion.policy",
			statement:  "reject model-supplied promotion_basis unless source adapter already proved basis",
			wantArea:   "policy",
			wantPrefix: "policy_distill-promotion-policy_",
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
				Verdict:      Park,
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
				plan.CandidateVerdict != Park ||
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
