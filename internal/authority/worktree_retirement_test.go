package authority

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildWorktreeRetirementPlanContainsCandidateWithoutMutatingGit(t *testing.T) {
	fixture := newRetirementGitFixture(t)
	beforeRefs := retirementGitOutput(t, fixture.repo, "show-ref")
	beforeWorktrees := retirementGitOutput(t, fixture.repo, "worktree", "list", "--porcelain")

	plan, err := BuildWorktreeRetirementPlan(context.Background(), WorktreeRetirementPlanRequest{
		RepoRoot:       fixture.repo,
		WorktreePath:   fixture.worktree,
		CandidateRef:   fixture.candidateRef,
		AuthorityRef:   "refs/heads/main",
		PublicationRef: "refs/remotes/origin/main",
		OwnerProof:     validRetirementOwnerProof(),
		SourceProof:    validRetirementSourceProof(fixture.candidateTip),
		RecoveryRefs:   []string{"sha256:" + strings.Repeat("a", 64)},
	}, WorktreeRetirementDependencies{
		Git:      ExecWorktreeRetirementGit{},
		Liveness: staticRetirementLiveness{state: WorktreeLivenessUnused},
	}, time.Date(2026, 7, 15, 7, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Eligible || len(plan.BlockReasons) != 0 {
		t.Fatalf("eligible plan blocked: %+v", plan.BlockReasons)
	}
	if plan.CandidateRelation != WorktreeCandidateContained {
		t.Fatalf("candidate relation = %q, want %q", plan.CandidateRelation, WorktreeCandidateContained)
	}
	if plan.CandidateTip != fixture.candidateTip || plan.AuthorityTip != fixture.mainTip || plan.PublicationTip != fixture.mainTip {
		t.Fatalf("sealed tips = candidate=%s authority=%s publication=%s", plan.CandidateTip, plan.AuthorityTip, plan.PublicationTip)
	}
	if plan.Worktree == nil || plan.Worktree.Path != fixture.worktree || plan.Worktree.Head != fixture.candidateTip || plan.Worktree.BranchRef != fixture.candidateRef {
		t.Fatalf("worktree identity = %+v", plan.Worktree)
	}
	if plan.PlanDigest == "" || plan.PlanDigest != WorktreeRetirementPlanDigest(plan) {
		t.Fatalf("plan digest is not sealed: %q", plan.PlanDigest)
	}

	if after := retirementGitOutput(t, fixture.repo, "show-ref"); after != beforeRefs {
		t.Fatalf("plan mutated refs:\nbefore:\n%s\nafter:\n%s", beforeRefs, after)
	}
	if after := retirementGitOutput(t, fixture.repo, "worktree", "list", "--porcelain"); after != beforeWorktrees {
		t.Fatalf("plan mutated worktrees:\nbefore:\n%s\nafter:\n%s", beforeWorktrees, after)
	}
	if status := retirementGitOutput(t, fixture.worktree, "status", "--porcelain=v1", "--untracked-files=all"); status != "" {
		t.Fatalf("candidate worktree became dirty: %q", status)
	}
}

type staticRetirementLiveness struct {
	state WorktreeLivenessState
}

func (s staticRetirementLiveness) Observe(context.Context, string) WorktreeLivenessObservation {
	return WorktreeLivenessObservation{State: s.state, Evidence: "test-probe"}
}

type retirementGitFixture struct {
	repo         string
	worktree     string
	candidateRef string
	candidateTip string
	mainTip      string
}

func newRetirementGitFixture(t *testing.T) retirementGitFixture {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	retirementRunGit(t, repo, "init", "-b", "main")
	retirementRunGit(t, repo, "config", "user.email", "test@example.invalid")
	retirementRunGit(t, repo, "config", "user.name", "Compound Test")
	if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	retirementRunGit(t, repo, "add", "seed.txt")
	retirementRunGit(t, repo, "commit", "-m", "seed")
	candidateTip := retirementGitOutput(t, repo, "rev-parse", "HEAD")
	candidateRef := "refs/heads/candidate"
	retirementRunGit(t, repo, "branch", "candidate", candidateTip)
	worktree := filepath.Join(root, "candidate-worktree")
	retirementRunGit(t, repo, "worktree", "add", worktree, "candidate")
	worktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	repo, err = filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(repo, "main.txt"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	retirementRunGit(t, repo, "add", "main.txt")
	retirementRunGit(t, repo, "commit", "-m", "main descendant")
	mainTip := retirementGitOutput(t, repo, "rev-parse", "refs/heads/main")
	retirementRunGit(t, repo, "update-ref", "refs/remotes/origin/main", mainTip)
	return retirementGitFixture{repo: repo, worktree: worktree, candidateRef: candidateRef, candidateTip: candidateTip, mainTip: mainTip}
}

func validRetirementOwnerProof() WorktreeRetirementOwnerProof {
	return WorktreeRetirementOwnerProof{
		WorkID:          "work_owner_retirement",
		State:           "running",
		LeaseHolder:     "codex:test",
		LeaseClaimedAt:  "2026-07-15T06:59:00Z",
		LeaseWorkSHA256: strings.Repeat("b", 64),
		WorkDigest:      "sha256:" + strings.Repeat("c", 64),
	}
}

func validRetirementSourceProof(commit string) WorktreeRetirementSourceProof {
	return WorktreeRetirementSourceProof{
		WorkID:                "work_source_landed",
		State:                 "completed",
		ProofComplete:         true,
		ActionIntentIDs:       []string{"act_source"},
		PolicyDecisionID:      "pol_source",
		ReceiptID:             "rec_source",
		TransitionID:          "tr_source",
		CommitArtifactRef:     "commit:" + commit,
		CommitTip:             commit,
		LandCheckID:           "landcheck_123456789abc",
		LandCheckEvidenceRef:  "land-check:landcheck_123456789abc",
		LandCheckStatus:       "landed",
		LandCheckCandidateTip: commit,
		LandCheckFileDigest:   "sha256:" + strings.Repeat("d", 64),
	}
}

func retirementRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func retirementGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}
