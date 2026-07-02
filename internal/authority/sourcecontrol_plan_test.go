package authority

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/reviewartifact"
)

func TestSourceControlReviewPlanRecordsReviewWithoutMutatingRepo(t *testing.T) {
	repo := initGitRepo(t)
	writeFile(t, filepath.Join(repo, "README.md"), "changed\n")
	writeFile(t, filepath.Join(repo, "scratch.txt"), "scratch\n")

	before := gitStatusShort(t, repo)
	snap, _, _, _, err := ImportGitSnapshot(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSourceControlReviewPlan(snap, "work_dirty_gate", time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != SourceControlReviewPlanSchemaVersion ||
		plan.Status != "review_required" ||
		plan.WorkID != "work_dirty_gate" ||
		plan.ChangedPaths != 2 ||
		plan.ReviewDraft.Verification == "" {
		t.Fatalf("bad source-control review plan: %+v", plan)
	}
	home := t.TempDir()
	paths, err := RecordSourceControlReviewPlan(home, plan)
	if err != nil {
		t.Fatal(err)
	}
	review, err := os.ReadFile(paths.ReviewPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(review)
	for _, want := range []string{"does not stage", "Required Evidence", "work_dirty_gate"} {
		if !strings.Contains(text, want) {
			t.Fatalf("review missing %q:\n%s", want, text)
		}
	}
	if _, err := os.Stat(paths.PlanPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("dirty-plan must not promote claims, stat err=%v", err)
	}
	after := gitStatusShort(t, repo)
	if before != after {
		t.Fatalf("repo status mutated\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestRecordSourceControlReviewAnnotatesQueue(t *testing.T) {
	repo := initGitRepo(t)
	writeFile(t, filepath.Join(repo, "README.md"), "changed\n")
	writeFile(t, filepath.Join(repo, "scratch.txt"), "scratch\n")
	before := gitStatusShort(t, repo)
	snap, _, _, _, err := ImportGitSnapshot(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSourceControlReviewPlan(snap, "work_dirty_gate", time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	paths, err := RecordSourceControlReviewPlan(home, plan)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := reviewartifact.Queue(home, reviewartifact.QueueOptions{Area: SourceControlCleanupReviewArea}, time.Date(2026, 6, 29, 12, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	AnnotateSourceControlCleanupQueue(&queue)
	reviewartifact.Recount(&queue)
	if queue.Counts.ReviewRequired != 1 || queue.Items[0].Readiness != "needs_review" {
		t.Fatalf("unedited source-control review queue = %+v", queue)
	}
	reviewYAML := fmt.Sprintf(`schema_version: %s
id: %s
plan_id: %s
repo_id: %q
decision: parked
rationale: "only scratch work remains parked outside this change"
confidence: 0.9
next_action: "leave untracked scratch parked until owner classifies it"
evidence_refs:
 - %q
`, SourceControlReviewSchemaVersion, plan.ReviewDraft.ReviewID, plan.ID, plan.RepoID, plan.EvidenceRefs[0])
	if err := os.WriteFile(paths.ReviewYAMLPath, []byte(reviewYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	queue, err = reviewartifact.Queue(home, reviewartifact.QueueOptions{Area: SourceControlCleanupReviewArea}, time.Date(2026, 6, 29, 12, 2, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	AnnotateSourceControlCleanupQueue(&queue)
	reviewartifact.Recount(&queue)
	if queue.Counts.ReviewRequired != 1 || queue.Items[0].Readiness != "ready_to_record" || len(queue.Items[0].Actions) != 1 {
		t.Fatalf("edited source-control review queue = %+v", queue)
	}
	review, err := LoadSourceControlReview(paths.ReviewYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	record, err := RecordSourceControlReview(home, review, paths.ReviewYAMLPath, time.Date(2026, 6, 29, 12, 3, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "recorded" || record.Decision != "parked" || record.ReviewPath == "" {
		t.Fatalf("bad source-control review record: %+v", record)
	}
	queue, err = reviewartifact.Queue(home, reviewartifact.QueueOptions{Area: SourceControlCleanupReviewArea}, time.Date(2026, 6, 29, 12, 4, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	AnnotateSourceControlCleanupQueue(&queue)
	reviewartifact.Recount(&queue)
	if queue.Counts.OK != 1 || queue.Counts.ReviewRequired != 0 || queue.Items[0].Status != "recorded" || queue.Items[0].Readiness != "review_recorded" {
		t.Fatalf("recorded source-control review queue = %+v", queue)
	}
	after := gitStatusShort(t, repo)
	if before != after {
		t.Fatalf("source-control review mutated repo\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestSourceControlReviewPlanRequiresDirtySnapshot(t *testing.T) {
	repo := initGitRepo(t)
	snap, _, _, _, err := ImportGitSnapshot(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSourceControlReviewPlan(snap, "", time.Now()); err == nil || !strings.Contains(err.Error(), "dirty source-control snapshot required") {
		t.Fatalf("err = %v, want dirty snapshot requirement", err)
	}
}

func gitStatusShort(t *testing.T, repo string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "status", "--porcelain=v1", "-b")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status failed: %v\n%s", err, string(out))
	}
	return string(out)
}
