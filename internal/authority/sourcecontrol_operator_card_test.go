package authority

import (
	"testing"
	"time"

	"compound/internal/reviewartifact"
)

// TestAnnotateSourceControlCleanupQueueFillsFallback covers gap #1's
// source-control-cleanup area: AnnotateSourceControlCleanupQueue tracked
// readiness/actions but never computed the operator card, so every legacy
// plan rendered the generic fallback sentence.
func TestAnnotateSourceControlCleanupQueueFillsFallback(t *testing.T) {
	homePath := t.TempDir()
	plan := SourceControlReviewPlan{
		SchemaVersion: SourceControlReviewPlanSchemaVersion,
		ID:            "scrp_test",
		CreatedAt:     "2026-07-01T00:00:00Z",
		Status:        "review_required",
		RepoID:        "compound",
		GitRoot:       "/tmp/compound",
		Upstream:      "origin/main",
		Ahead:         3,
		ChangedPaths:  5,
		SourceStatus:  "dirty",
	}
	if _, err := reviewartifact.WriteJSON(homePath, SourceControlCleanupReviewArea, plan.ID+".json", plan, "  "); err != nil {
		t.Fatal(err)
	}
	report, err := reviewartifact.Queue(homePath, reviewartifact.QueueOptions{Area: SourceControlCleanupReviewArea}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Items[0].SummarySource != reviewartifact.SummarySourceFallback {
		t.Fatalf("precondition: legacy plan should start as fallback, got %+v", report.Items[0])
	}
	AnnotateSourceControlCleanupQueue(&report)
	reviewartifact.Recount(&report)

	item := report.Items[0]
	want := "Repo watch sees compound has 5 uncommitted files and 3 commits ahead of origin/main. Tidy it?"
	if item.OperatorSummary != want {
		t.Fatalf("OperatorSummary = %q, want %q", item.OperatorSummary, want)
	}
	if item.Band != reviewartifact.BandNeedsYou {
		t.Fatalf("Band = %q, want needs_you", item.Band)
	}
}

// TestOperatorCardForSourceControlReviewPlanStripsRepoPrefix pins a real
// live bug: repo_id carries a "repo:" machine prefix on disk (e.g.
// "repo:compound"), which must not leak into the sentence or context_label.
func TestOperatorCardForSourceControlReviewPlanStripsRepoPrefix(t *testing.T) {
	card, err := OperatorCardForSourceControlReviewPlan(SourceControlReviewPlan{
		RepoID:       "repo:compound",
		Upstream:     "origin/main",
		Ahead:        17,
		ChangedPaths: 95,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "Repo watch sees compound has 95 uncommitted files and 17 commits ahead of origin/main. Tidy it?"
	if card.Summary != want {
		t.Fatalf("Summary = %q, want %q", card.Summary, want)
	}
	if card.ContextLabel != "compound" {
		t.Fatalf("ContextLabel = %q, want %q", card.ContextLabel, "compound")
	}
}
