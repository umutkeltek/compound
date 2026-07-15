package authority

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorktreeRetirementReviewSealsExactEligiblePlanAndOperatorYAML(t *testing.T) {
	fixture := newRetirementGitFixture(t)
	plan, err := BuildWorktreeRetirementPlan(context.Background(), retirementPlanRequest(fixture), WorktreeRetirementDependencies{
		Git: ExecWorktreeRetirementGit{}, Liveness: staticRetirementLiveness{state: WorktreeLivenessUnused},
	}, time.Date(2026, 7, 15, 7, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	paths, err := RecordWorktreeRetirementPlan(home, plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.PlanPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.ReviewYAMLPath); err != nil {
		t.Fatal(err)
	}

	review, err := LoadWorktreeRetirementReview(paths.ReviewYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	review.Decision = "authorize_retirement"
	review.Rationale = "exact redundant branch and clean inactive worktree may be retired"
	review.ReviewedBy = "operator:test"
	review.ReviewedAt = "2026-07-15T07:01:00Z"
	report, err := RecordWorktreeRetirementReview(home, review, paths.ReviewYAMLPath, time.Date(2026, 7, 15, 7, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "authorized" || report.Decision != "authorize_retirement" || report.PlanDigest != plan.PlanDigest || report.ReviewedBy != "operator:test" {
		t.Fatalf("review report = %+v", report)
	}
	if !validRetirementDigest(report.SourceDigest) || !validRetirementDigest(report.AuthorizationDigest) {
		t.Fatalf("review seals missing: %+v", report)
	}
	if _, err := ValidateAuthorizedWorktreeRetirement(paths.PlanPath, report.ReviewPath); err != nil {
		t.Fatalf("fresh authorization rejected: %v", err)
	}

	data, err := os.ReadFile(paths.PlanPath)
	if err != nil {
		t.Fatal(err)
	}
	var tampered WorktreeRetirementPlan
	if err := json.Unmarshal(data, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered.CandidateTip = strings.Repeat("f", 40)
	writeRetirementJSON(t, paths.PlanPath, tampered)
	if _, err := ValidateAuthorizedWorktreeRetirement(paths.PlanPath, report.ReviewPath); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("tampered plan authorization error = %v", err)
	}
}

func TestWorktreeRetirementReviewRejectsIneligibleAndNonOperatorAuthorization(t *testing.T) {
	fixture := newRetirementGitFixture(t)
	req := retirementPlanRequest(fixture)
	req.RecoveryRefs = nil
	blocked, err := BuildWorktreeRetirementPlan(context.Background(), req, WorktreeRetirementDependencies{
		Git: ExecWorktreeRetirementGit{}, Liveness: staticRetirementLiveness{state: WorktreeLivenessUnused},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	paths, err := RecordWorktreeRetirementPlan(home, blocked)
	if err != nil {
		t.Fatal(err)
	}
	review, err := LoadWorktreeRetirementReview(paths.ReviewYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	review.Decision = "authorize_retirement"
	review.Rationale = "should remain blocked"
	review.ReviewedBy = "codex:test"
	if _, err := RecordWorktreeRetirementReview(home, review, paths.ReviewYAMLPath, time.Now()); err == nil || !strings.Contains(err.Error(), "operator") {
		t.Fatalf("nonoperator review error = %v", err)
	}
	review.ReviewedBy = "operator:test"
	if _, err := RecordWorktreeRetirementReview(home, review, paths.ReviewYAMLPath, time.Now()); err == nil || !strings.Contains(err.Error(), "eligible") {
		t.Fatalf("ineligible review error = %v", err)
	}
}

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

func TestWorktreeRetirementPlanAllowsPatchEquivalentAndRefusesUniqueCandidate(t *testing.T) {
	for _, tc := range []struct {
		name         string
		equivalent   bool
		wantRelation string
		wantEligible bool
	}{
		{name: "patch equivalent", equivalent: true, wantRelation: WorktreeCandidatePatchEquivalent, wantEligible: true},
		{name: "unique", equivalent: false, wantRelation: WorktreeCandidateUnique, wantEligible: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newDivergentRetirementGitFixture(t, tc.equivalent)
			plan, err := BuildWorktreeRetirementPlan(context.Background(), retirementPlanRequest(fixture), WorktreeRetirementDependencies{
				Git: ExecWorktreeRetirementGit{}, Liveness: staticRetirementLiveness{state: WorktreeLivenessUnused},
			}, time.Date(2026, 7, 15, 7, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			if plan.CandidateRelation != tc.wantRelation || plan.Eligible != tc.wantEligible {
				t.Fatalf("relation=%q eligible=%v blockers=%v", plan.CandidateRelation, plan.Eligible, plan.BlockReasons)
			}
			if !tc.wantEligible && !retirementPlanBlockedBy(plan, "candidate_contains_unique_commits") {
				t.Fatalf("unique candidate blockers = %v", plan.BlockReasons)
			}
		})
	}
}

func TestWorktreeRetirementPlanFailsClosedOnDirtyLockAndLiveness(t *testing.T) {
	for _, tc := range []struct {
		name      string
		prepare   func(*testing.T, retirementGitFixture)
		liveness  WorktreeLivenessState
		wantBlock string
	}{
		{name: "tracked dirty", prepare: func(t *testing.T, f retirementGitFixture) {
			if err := os.WriteFile(filepath.Join(f.worktree, "seed.txt"), []byte("dirty\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, liveness: WorktreeLivenessUnused, wantBlock: "worktree_dirty"},
		{name: "untracked dirty", prepare: func(t *testing.T, f retirementGitFixture) {
			if err := os.WriteFile(filepath.Join(f.worktree, "untracked.txt"), []byte("dirty\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, liveness: WorktreeLivenessUnused, wantBlock: "worktree_dirty"},
		{name: "locked", prepare: func(t *testing.T, f retirementGitFixture) {
			retirementRunGit(t, f.repo, "worktree", "lock", "--reason", "owner active", f.worktree)
		}, liveness: WorktreeLivenessUnused, wantBlock: "worktree_locked_manual_unlock_required"},
		{name: "live", liveness: WorktreeLivenessActive, wantBlock: "worktree_live"},
		{name: "unknown", liveness: WorktreeLivenessUnknown, wantBlock: "worktree_liveness_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newRetirementGitFixture(t)
			if tc.prepare != nil {
				tc.prepare(t, fixture)
			}
			plan, err := BuildWorktreeRetirementPlan(context.Background(), retirementPlanRequest(fixture), WorktreeRetirementDependencies{
				Git: ExecWorktreeRetirementGit{}, Liveness: staticRetirementLiveness{state: tc.liveness},
			}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if plan.Eligible || !retirementPlanBlockedBy(plan, tc.wantBlock) {
				t.Fatalf("plan eligible=%v blockers=%v, want %q", plan.Eligible, plan.BlockReasons, tc.wantBlock)
			}
		})
	}
}

func TestWorktreeRetirementPlanRefusesAuthorityDriftAndIncompleteProofs(t *testing.T) {
	fixture := newRetirementGitFixture(t)
	req := retirementPlanRequest(fixture)
	retirementRunGit(t, fixture.repo, "update-ref", req.PublicationRef, fixture.candidateTip)
	plan, err := BuildWorktreeRetirementPlan(context.Background(), req, WorktreeRetirementDependencies{
		Git: ExecWorktreeRetirementGit{}, Liveness: staticRetirementLiveness{state: WorktreeLivenessUnused},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Eligible || !retirementPlanBlockedBy(plan, "authority_publication_tip_mismatch") {
		t.Fatalf("authority drift blockers = %v", plan.BlockReasons)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*WorktreeRetirementPlanRequest)
		want   string
	}{
		{name: "owner not running", mutate: func(r *WorktreeRetirementPlanRequest) { r.OwnerProof.State = "claimed" }, want: "owner_work_not_running_with_exact_lease"},
		{name: "source nonterminal", mutate: func(r *WorktreeRetirementPlanRequest) { r.SourceProof.State = "running" }, want: "source_work_proof_invalid"},
		{name: "incomplete chain", mutate: func(r *WorktreeRetirementPlanRequest) { r.SourceProof.ProofComplete = false }, want: "source_work_proof_invalid"},
		{name: "missing landcheck", mutate: func(r *WorktreeRetirementPlanRequest) { r.SourceProof.LandCheckID = "" }, want: "source_work_proof_invalid"},
		{name: "mismatched commit", mutate: func(r *WorktreeRetirementPlanRequest) { r.SourceProof.LandCheckCandidateTip = strings.Repeat("e", 40) }, want: "source_work_proof_invalid"},
		{name: "missing recovery", mutate: func(r *WorktreeRetirementPlanRequest) { r.RecoveryRefs = nil }, want: "recovery_evidence_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fresh := newRetirementGitFixture(t)
			req := retirementPlanRequest(fresh)
			tc.mutate(&req)
			plan, err := BuildWorktreeRetirementPlan(context.Background(), req, WorktreeRetirementDependencies{
				Git: ExecWorktreeRetirementGit{}, Liveness: staticRetirementLiveness{state: WorktreeLivenessUnused},
			}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if plan.Eligible || !retirementPlanBlockedBy(plan, tc.want) {
				t.Fatalf("eligible=%v blockers=%v, want %q", plan.Eligible, plan.BlockReasons, tc.want)
			}
		})
	}
}

func TestWorktreeRetirementPlanRejectsMalformedOrProtectedTargets(t *testing.T) {
	fixture := newRetirementGitFixture(t)
	req := retirementPlanRequest(fixture)
	req.CandidateRef = "candidate"
	if _, err := BuildWorktreeRetirementPlan(context.Background(), req, WorktreeRetirementDependencies{
		Git: ExecWorktreeRetirementGit{}, Liveness: staticRetirementLiveness{state: WorktreeLivenessUnused},
	}, time.Now()); err == nil || !strings.Contains(err.Error(), "full canonical") {
		t.Fatalf("malformed ref error = %v", err)
	}

	req = retirementPlanRequest(fixture)
	req.WorktreePath = fixture.repo
	req.CandidateRef = req.AuthorityRef
	req.SourceProof = validRetirementSourceProof(fixture.mainTip)
	plan, err := BuildWorktreeRetirementPlan(context.Background(), req, WorktreeRetirementDependencies{
		Git: ExecWorktreeRetirementGit{}, Liveness: staticRetirementLiveness{state: WorktreeLivenessUnused},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Eligible || !retirementPlanBlockedBy(plan, "canonical_main_worktree_cannot_be_retired") || !retirementPlanBlockedBy(plan, "candidate_ref_is_authority_or_publication") {
		t.Fatalf("protected target blockers = %v", plan.BlockReasons)
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

func retirementPlanRequest(f retirementGitFixture) WorktreeRetirementPlanRequest {
	return WorktreeRetirementPlanRequest{
		RepoRoot: f.repo, WorktreePath: f.worktree, CandidateRef: f.candidateRef,
		AuthorityRef: "refs/heads/main", PublicationRef: "refs/remotes/origin/main",
		OwnerProof: validRetirementOwnerProof(), SourceProof: validRetirementSourceProof(f.candidateTip),
		RecoveryRefs: []string{"sha256:" + strings.Repeat("a", 64)},
	}
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

func newDivergentRetirementGitFixture(t *testing.T, equivalent bool) retirementGitFixture {
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
	retirementRunGit(t, repo, "branch", "candidate")
	worktree := filepath.Join(root, "candidate-worktree")
	retirementRunGit(t, repo, "worktree", "add", worktree, "candidate")
	if err := os.WriteFile(filepath.Join(worktree, "change.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	retirementRunGit(t, worktree, "add", "change.txt")
	retirementRunGit(t, worktree, "commit", "-m", "candidate change")
	candidateTip := retirementGitOutput(t, worktree, "rev-parse", "HEAD")
	mainBody := "main-only\n"
	if equivalent {
		mainBody = "candidate\n"
	}
	if err := os.WriteFile(filepath.Join(repo, "change.txt"), []byte(mainBody), 0o644); err != nil {
		t.Fatal(err)
	}
	retirementRunGit(t, repo, "add", "change.txt")
	retirementRunGit(t, repo, "commit", "-m", "main change")
	mainTip := retirementGitOutput(t, repo, "rev-parse", "HEAD")
	retirementRunGit(t, repo, "update-ref", "refs/remotes/origin/main", mainTip)
	worktree, _ = filepath.EvalSymlinks(worktree)
	repo, _ = filepath.EvalSymlinks(repo)
	return retirementGitFixture{repo: repo, worktree: worktree, candidateRef: "refs/heads/candidate", candidateTip: candidateTip, mainTip: mainTip}
}

func retirementPlanBlockedBy(plan WorktreeRetirementPlan, code string) bool {
	for _, reason := range plan.BlockReasons {
		if strings.Contains(reason, code) {
			return true
		}
	}
	return false
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

func writeRetirementJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
