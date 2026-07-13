package work

import (
	"strings"
	"testing"
	"time"
)

func TestDispatchSurfacesExpectedBaseAndWorkerLandingContract(t *testing.T) {
	now := time.Date(2026, 7, 13, 21, 0, 0, 0, time.UTC)
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo, base := initLandingRepo(t)
	w, err := store.Create(CreateInput{
		Owner:            "codex",
		SuccessPredicate: "source-controlled work lands on main",
		GitDelivery:      localGitDelivery(repo, base),
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := store.DispatchPlan(now.Add(time.Minute), DispatchPlanOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Selected == nil || plan.Selected.WorkID != w.ID {
		t.Fatalf("dispatch did not select source-controlled work: %+v", plan.Selected)
	}
	if plan.Selected.ExpectedBaseSHA != base {
		t.Fatalf("dispatch expected_base_sha = %q, want %q", plan.Selected.ExpectedBaseSHA, base)
	}

	claimed, err := store.DispatchClaim(now.Add(2*time.Minute), DispatchPlanOptions{Actor: "codex", WorkID: w.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## SOURCE CONTROL CONTRACT",
		"target_worktree_root: " + w.GitDelivery.TargetWorktreeRoot,
		"target_local_ref: refs/heads/main",
		"expected_base_sha: " + base,
		"publication_mode: local_only",
		"commit:<full-sha>",
		"compound work land-check",
	} {
		if !strings.Contains(claimed.WorkerPromptMD, want) {
			t.Fatalf("worker prompt missing %q:\n%s", want, claimed.WorkerPromptMD)
		}
	}
}

func TestWorkbenchMakesVerifiedCodeAwaitingLandingVisible(t *testing.T) {
	now := time.Date(2026, 7, 13, 21, 0, 0, 0, time.UTC)
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo, base := initLandingRepo(t)
	tip := landingFeatureCommit(t, repo)
	landingGit(t, repo, "switch", "main")
	w, err := store.Create(CreateInput{
		Owner:            "codex",
		SuccessPredicate: "verified code cannot disappear outside main",
		GitDelivery:      localGitDelivery(repo, base),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(w.ID, "codex", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(w.ID, AdvanceInput{To: "running", Actor: "codex"}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(w.ID, AdvanceInput{
		To:             "verifying",
		Actor:          "codex",
		ArtifactID:     "commit:" + tip,
		VerificationID: "check:focused-tests",
	}, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}

	report, err := store.Workbench(now.Add(4*time.Minute), WorkbenchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Open) != 1 {
		t.Fatalf("open work = %d, want 1", len(report.Open))
	}
	item := report.Open[0]
	if item.ExpectedBaseSHA != base || item.CandidateTipSHA != tip {
		t.Fatalf("source-control identity missing from workbench: %+v", item)
	}
	if item.LandingStatus != "awaiting_local_land" || !strings.Contains(item.NextAction, "land-check") {
		t.Fatalf("verified code must be directly visible as awaiting landing: %+v", item)
	}
}
