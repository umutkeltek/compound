package panel

import (
	"strings"
	"testing"

	"compound/internal/reviewartifact"
)

func TestRecordVerdictHumanReviewRecordsNeedsHuman(t *testing.T) {
	home := t.TempDir()
	path := writeVerdictReviewRecordFixture(t, home, "panel_verdict_human_guard", "needs_human")

	report, err := RecordVerdictHumanReview(home, path, "owner reviewed panel request; keep parked for manual evidence", fixedPanelNow())
	if err != nil {
		t.Fatalf("record human review: %v", err)
	}
	if report.Status != "recorded" || report.Decision != "needs_human" || report.ReviewPath == "" {
		t.Fatalf("report = %+v, want recorded needs_human with review path", report)
	}
	if !strings.HasSuffix(report.ReviewPath, "panel_verdict_human_guard.reviewed.json") {
		t.Fatalf("review path = %q, want verdict id reviewed sidecar", report.ReviewPath)
	}
	if len(report.Boundaries) == 0 || !strings.Contains(strings.Join(report.Boundaries, " "), "no panel apply") {
		t.Fatalf("report should preserve no-mutation boundaries: %+v", report)
	}
	loaded, err := LoadVerdictHumanReviewReport(report.ReviewPath)
	if err != nil {
		t.Fatalf("load human review report: %v", err)
	}
	if loaded.ID != report.ID || loaded.ReviewPath != report.ReviewPath {
		t.Fatalf("loaded report = %+v, want %+v", loaded, report)
	}
}

func TestRecordVerdictHumanReviewRejectsMutatingDecision(t *testing.T) {
	home := t.TempDir()
	path := writeVerdictReviewRecordFixture(t, home, "panel_verdict_promote_guard", "promote")

	_, err := RecordVerdictHumanReview(home, path, "recording promote through human review must fail", fixedPanelNow())
	if err == nil || !strings.Contains(err.Error(), "separate owner mutation path") {
		t.Fatalf("err = %v, want separate owner mutation guard", err)
	}
}

func writeVerdictReviewRecordFixture(t *testing.T, home, id, decision string) string {
	t.Helper()
	paths, err := reviewartifact.Write(home, verdictReviewArea, id, verdictArtifactPayload{
		SchemaVersion: "compound.panel_verdict.v0.1",
		ID:            id,
		CreatedAt:     fixedPanelNow().UTC().Format("2006-01-02T15:04:05Z"),
		RunID:         "prun_review_record_fixture",
		Target: TargetInput{
			Kind:    "memory_candidate",
			Ref:     "mcand_review_record_fixture",
			Summary: "candidate needs manual adjudication evidence",
		},
		LensFindings: []LensFinding{{
			LensRef:          "lens:privacy-risk",
			RouteEvidenceRef: "route_privacy",
			Status:           "failed",
		}},
		Synthesis: Synthesis{
			SchemaVersion: SynthesisSchemaVersion,
			Decision:      decision,
			Confidence:    0.61,
			Rationale:     "fixture rationale",
		},
		EvidenceRefs: []string{"route_privacy", "route_privacy"},
	}, " ", "# Panel verdict\n")
	if err != nil {
		t.Fatal(err)
	}
	return paths.PlanPath
}
