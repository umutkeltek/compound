package routeevidence

import (
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/reviewartifact"
)

func TestPromotionReviewQueueUsesArchivedMatchingHoldSidecar(t *testing.T) {
	homePath := t.TempDir()
	now := time.Date(2026, 7, 2, 16, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "test", now); err != nil {
		t.Fatalf("init home: %v", err)
	}
	review := PromotionReview{
		SchemaVersion:   PromotionReviewSchemaVersion,
		ID:              "route_promotion_archived_hold",
		CreatedAt:       now.Format(time.RFC3339),
		Status:          "review_required",
		Recommendation:  promotionRecommendationHoldInsufficient,
		Reason:          "samples 2 below min_samples 10",
		LaneID:          "auto/judge",
		CapabilityClass: "classify-extract",
		EvidenceRefs:    []string{"route_a"},
	}
	paths, err := reviewartifact.Write(homePath, routePromotionReviewArea, review.ID, review, " ", "# Route promotion\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecordPromotionHoldReview(homePath, paths.PlanPath, "hold until the promotion criteria window is satisfied", now); err != nil {
		t.Fatalf("record hold review: %v", err)
	}
	if _, err := home.ArchiveOperationalHistory(homePath, home.OperationalArchiveOptions{Kinds: []string{"review"}}, now.Add(time.Minute)); err != nil {
		t.Fatalf("archive review: %v", err)
	}
	review.CreatedAt = now.Add(2 * time.Minute).Format(time.RFC3339)
	if _, err := reviewartifact.Write(homePath, routePromotionReviewArea, review.ID, review, " ", "# Route promotion\n"); err != nil {
		t.Fatal(err)
	}

	report, err := reviewartifact.Queue(homePath, reviewartifact.QueueOptions{Area: routePromotionReviewArea}, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	AnnotatePromotionReviewQueue(&report)
	reviewartifact.Recount(&report)
	if report.Counts.Total != 1 || report.Counts.ReviewRequired != 0 || report.Counts.OK != 1 {
		t.Fatalf("matching archived hold sidecar should clear review debt: %+v items=%+v", report.Counts, report.Items)
	}
	if report.Items[0].Status != "recorded" || report.Items[0].Readiness != "review_recorded" {
		t.Fatalf("matching archived hold sidecar should mark recorded: %+v", report.Items[0])
	}
}

func TestPromotionReviewQueueRejectsArchivedMismatchedHoldSidecar(t *testing.T) {
	homePath := t.TempDir()
	now := time.Date(2026, 7, 2, 16, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "test", now); err != nil {
		t.Fatalf("init home: %v", err)
	}
	review := PromotionReview{
		SchemaVersion:   PromotionReviewSchemaVersion,
		ID:              "route_promotion_archived_mismatch",
		CreatedAt:       now.Format(time.RFC3339),
		Status:          "review_required",
		Recommendation:  promotionRecommendationHoldInsufficient,
		Reason:          "samples 2 below min_samples 10",
		LaneID:          "auto/judge",
		CapabilityClass: "classify-extract",
	}
	paths, err := reviewartifact.Write(homePath, routePromotionReviewArea, review.ID, review, " ", "# Route promotion\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecordPromotionHoldReview(homePath, paths.PlanPath, "hold until the promotion criteria window is satisfied", now); err != nil {
		t.Fatalf("record hold review: %v", err)
	}
	if _, err := home.ArchiveOperationalHistory(homePath, home.OperationalArchiveOptions{Kinds: []string{"review"}}, now.Add(time.Minute)); err != nil {
		t.Fatalf("archive review: %v", err)
	}
	review.Reason = "evidence window 3 days satisfies the old hold reason no longer applies"
	if _, err := reviewartifact.Write(homePath, routePromotionReviewArea, review.ID, review, " ", "# Route promotion\n"); err != nil {
		t.Fatal(err)
	}

	report, err := reviewartifact.Queue(homePath, reviewartifact.QueueOptions{Area: routePromotionReviewArea}, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	AnnotatePromotionReviewQueue(&report)
	reviewartifact.Recount(&report)
	if report.Counts.Total != 1 || report.Counts.ReviewRequired != 1 || report.Counts.OK != 0 {
		t.Fatalf("mismatched archived hold sidecar should not clear review debt: %+v items=%+v", report.Counts, report.Items)
	}
}

func TestPromotionReviewQueueEmitsCleanRecordAction(t *testing.T) {
	homePath := t.TempDir()
	now := time.Date(2026, 7, 9, 16, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "test", now); err != nil {
		t.Fatalf("init home: %v", err)
	}
	review := PromotionReview{
		SchemaVersion:   PromotionReviewSchemaVersion,
		ID:              "route_promotion_clean_action",
		CreatedAt:       now.Format(time.RFC3339),
		Status:          "review_required",
		Recommendation:  promotionRecommendationHoldFailing,
		Reason:          "18.2% failed_status above 5% floor",
		LaneID:          "lane/extract",
		CapabilityClass: "classify-extract",
		EvidenceRefs:    []string{"route_b"},
	}
	if _, err := reviewartifact.Write(homePath, routePromotionReviewArea, review.ID, review, " ", "# Route promotion\n"); err != nil {
		t.Fatal(err)
	}

	report, err := reviewartifact.Queue(homePath, reviewartifact.QueueOptions{Area: routePromotionReviewArea}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	AnnotatePromotionReviewQueue(&report)
	reviewartifact.Recount(&report)
	if len(report.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(report.Items))
	}
	item := report.Items[0]
	if item.Readiness != "needs_review" {
		t.Fatalf("readiness = %q, want needs_review", item.Readiness)
	}
	if len(item.Actions) != 1 {
		t.Fatalf("review_required promotion review should carry exactly one clean action: %+v", item.Actions)
	}
	action := item.Actions[0]
	if action.ID != "route_promotion_record" || !action.RequiresReview {
		t.Fatalf("action = %+v, want id=route_promotion_record requires_review=true", action)
	}
	if len(action.Argv) == 0 || action.Argv[0] != "compound" {
		t.Fatalf("action argv must start with compound: %v", action.Argv)
	}
	for _, token := range action.Argv {
		if token == "/bin/sh" || token == "-lc" || token == "--rationale" {
			t.Fatalf("clean argv must omit shell tokens and --rationale (renderer appends it): %v", action.Argv)
		}
	}
	if action.Argv[len(action.Argv)-1] != item.JSONPath {
		t.Fatalf("action --review path %q should reference the item artifact %q", action.Argv[len(action.Argv)-1], item.JSONPath)
	}
}
