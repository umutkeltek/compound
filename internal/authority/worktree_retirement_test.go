package authority

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func TestWorktreeRetirementReviewedReportIsCreateExclusiveAndIdempotentForSameContent(t *testing.T) {
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
	review, err := LoadWorktreeRetirementReview(paths.ReviewYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	review.Decision = "authorize_retirement"
	review.Rationale = "retire exact redundant Git surfaces"
	review.ReviewedBy = "operator:test"
	review.ReviewedAt = "2026-07-15T07:01:00Z"
	now := time.Date(2026, 7, 15, 7, 1, 0, 0, time.UTC)
	report, err := RecordWorktreeRetirementReview(home, review, paths.ReviewYAMLPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecordWorktreeRetirementReview(home, review, paths.ReviewYAMLPath, now); err != nil {
		t.Fatalf("same-content reviewed report replay rejected: %v", err)
	}
	tampered := []byte("different reviewed report bytes\n")
	if err := os.WriteFile(report.ReviewPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordWorktreeRetirementReview(home, review, paths.ReviewYAMLPath, now); err == nil || !strings.Contains(err.Error(), "different") {
		t.Fatalf("different existing reviewed report bytes overwritten: %v", err)
	}
	got, err := os.ReadFile(report.ReviewPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(tampered) {
		t.Fatalf("different reviewed report bytes were replaced: %q", got)
	}
}

func TestWorktreeRetirementReviewRejectsDifferentSecondReviewWithoutMutatingFirstSource(t *testing.T) {
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
	first, err := LoadWorktreeRetirementReview(paths.ReviewYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	first.Decision = "authorize_retirement"
	first.Rationale = "retire exact redundant Git surfaces"
	first.ReviewedBy = "operator:first"
	first.ReviewedAt = "2026-07-15T07:01:00Z"
	firstReport, err := RecordWorktreeRetirementReview(home, first, paths.ReviewYAMLPath, time.Date(2026, 7, 15, 7, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	firstSource, err := os.ReadFile(firstReport.SourcePath)
	if err != nil {
		t.Fatal(err)
	}

	second := first
	second.Rationale = "different reviewed content must not replace the first authorization source"
	second.ReviewedAt = "2026-07-15T07:02:00Z"
	if _, err := RecordWorktreeRetirementReview(home, second, paths.ReviewYAMLPath, time.Date(2026, 7, 15, 7, 2, 0, 0, time.UTC)); err == nil || !strings.Contains(err.Error(), "different") {
		t.Fatalf("different second review was not rejected: %v", err)
	}
	got, err := os.ReadFile(firstReport.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, firstSource) {
		t.Fatalf("rejected second review changed first source bytes:\nfirst:\n%s\nafter:\n%s", firstSource, got)
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
		RecoveryRefs:   []string{fixture.recoveryRef},
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
	if len(plan.EvidenceRefs) == 0 || !containsStringValue(plan.EvidenceRefs, fixture.recoveryRef) {
		t.Fatalf("plan evidence refs do not seal recovery proof: %v", plan.EvidenceRefs)
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
		{name: "missing receipt", mutate: func(r *WorktreeRetirementPlanRequest) { r.SourceProof.ReceiptID = "" }, want: "source_work_proof_invalid"},
		{name: "missing transition", mutate: func(r *WorktreeRetirementPlanRequest) { r.SourceProof.TransitionID = "" }, want: "source_work_proof_invalid"},
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

func TestWorktreeRetirementPlanAlwaysRefusesMainCandidateEvenWhenAuthorityIsAnotherHead(t *testing.T) {
	fixture := newRetirementGitFixture(t)
	retirementRunGit(t, fixture.repo, "update-ref", "refs/heads/trunk", fixture.mainTip)
	req := retirementPlanRequest(fixture)
	req.WorktreePath = ""
	req.CandidateRef = "refs/heads/main"
	req.AuthorityRef = "refs/heads/trunk"
	req.SourceProof = validRetirementSourceProof(fixture.mainTip)
	plan, err := BuildWorktreeRetirementPlan(context.Background(), req, WorktreeRetirementDependencies{
		Git: ExecWorktreeRetirementGit{}, Liveness: staticRetirementLiveness{state: WorktreeLivenessUnused},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Eligible || !retirementPlanBlockedBy(plan, "candidate_ref_main_protected") {
		t.Fatalf("main candidate blockers = %v", plan.BlockReasons)
	}
}

func TestWorktreeRetirementPlanValidatesContentAddressedRecoveryProof(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, *WorktreeRetirementPlanRequest)
	}{
		{name: "invalid", mutate: func(_ *testing.T, r *WorktreeRetirementPlanRequest) { r.RecoveryRefs = []string{"sha256:deadbeef"} }},
		{name: "missing", mutate: func(t *testing.T, r *WorktreeRetirementPlanRequest) {
			path := filepath.Join(t.TempDir(), "missing.bundle")
			r.RecoveryRefs = []string{"sha256-file:" + strings.Repeat("a", 64) + ":" + path}
		}},
		{name: "tampered", mutate: func(t *testing.T, r *WorktreeRetirementPlanRequest) {
			path := filepath.Join(t.TempDir(), "recovery.bundle")
			if err := os.WriteFile(path, []byte("actual"), 0o600); err != nil {
				t.Fatal(err)
			}
			r.RecoveryRefs = []string{"sha256-file:" + strings.Repeat("a", 64) + ":" + path}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newRetirementGitFixture(t)
			req := retirementPlanRequest(fixture)
			tc.mutate(t, &req)
			plan, err := BuildWorktreeRetirementPlan(context.Background(), req, WorktreeRetirementDependencies{
				Git: ExecWorktreeRetirementGit{}, Liveness: staticRetirementLiveness{state: WorktreeLivenessUnused},
			}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if plan.Eligible || !retirementPlanBlockedBy(plan, "recovery_evidence_invalid") {
				t.Fatalf("recovery plan eligible=%v blockers=%v", plan.Eligible, plan.BlockReasons)
			}
		})
	}
}

func TestWorktreeRetirementPlanIDSealsCompleteCanonicalPayload(t *testing.T) {
	fixture := newRetirementGitFixture(t)
	now := time.Date(2026, 7, 15, 7, 0, 0, 0, time.UTC)
	first, err := BuildWorktreeRetirementPlan(context.Background(), retirementPlanRequest(fixture), WorktreeRetirementDependencies{
		Git: ExecWorktreeRetirementGit{}, Liveness: staticRetirementLiveness{state: WorktreeLivenessUnused},
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	secondPath := filepath.Join(filepath.Dir(fixture.repo), "second-recovery.bundle")
	secondRef := writeRetirementRecoveryProof(t, secondPath, []byte("second recovery body"))
	secondReq := retirementPlanRequest(fixture)
	secondReq.RecoveryRefs = []string{secondRef}
	second, err := BuildWorktreeRetirementPlan(context.Background(), secondReq, WorktreeRetirementDependencies{
		Git: ExecWorktreeRetirementGit{}, Liveness: staticRetirementLiveness{state: WorktreeLivenessUnused},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatalf("plan id did not change with sealed recovery/evidence payload: %s", first.ID)
	}

	tampered := first
	tampered.ID = "wtr_000000000000"
	tampered.PlanDigest = WorktreeRetirementPlanDigest(tampered)
	if err := ValidateWorktreeRetirementPlan(tampered); err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("recomputed digest accepted wrong content id: %v", err)
	}
}

func TestWorktreeRetirementArtifactsAreCreateExclusiveAndIdempotentForSameContent(t *testing.T) {
	fixture := newRetirementGitFixture(t)
	plan, err := BuildWorktreeRetirementPlan(context.Background(), retirementPlanRequest(fixture), WorktreeRetirementDependencies{
		Git: ExecWorktreeRetirementGit{}, Liveness: staticRetirementLiveness{state: WorktreeLivenessUnused},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	paths, err := RecordWorktreeRetirementPlan(home, plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecordWorktreeRetirementPlan(home, plan); err != nil {
		t.Fatalf("same-content plan replay rejected: %v", err)
	}
	tampered := []byte("different plan bytes\n")
	if err := os.WriteFile(paths.PlanPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordWorktreeRetirementPlan(home, plan); err == nil || !strings.Contains(err.Error(), "different") {
		t.Fatalf("different existing plan bytes overwritten: %v", err)
	}
	got, err := os.ReadFile(paths.PlanPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(tampered) {
		t.Fatalf("different plan bytes were replaced: %q", got)
	}
}

func TestWorktreeRetirementPlanSealsCandidateRelationToSourceCommit(t *testing.T) {
	for _, tc := range []struct {
		name         string
		fixture      func(*testing.T) retirementGitFixture
		sourceTip    func(retirementGitFixture) string
		wantRelation string
		wantEligible bool
	}{
		{name: "candidate ancestor of source", fixture: newRetirementGitFixture, sourceTip: func(f retirementGitFixture) string { return f.mainTip }, wantRelation: WorktreeCandidateContained, wantEligible: true},
		{name: "candidate patch equivalent to source", fixture: func(t *testing.T) retirementGitFixture { return newDivergentRetirementGitFixture(t, true) }, sourceTip: func(f retirementGitFixture) string { return f.mainTip }, wantRelation: WorktreeCandidatePatchEquivalent, wantEligible: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := tc.fixture(t)
			req := retirementPlanRequest(fixture)
			req.SourceProof = validRetirementSourceProof(tc.sourceTip(fixture))
			plan, err := BuildWorktreeRetirementPlan(context.Background(), req, WorktreeRetirementDependencies{
				Git: ExecWorktreeRetirementGit{}, Liveness: staticRetirementLiveness{state: WorktreeLivenessUnused},
			}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if plan.SourceRelation != tc.wantRelation || plan.Eligible != tc.wantEligible {
				t.Fatalf("source relation=%q eligible=%v blockers=%v", plan.SourceRelation, plan.Eligible, plan.BlockReasons)
			}
		})
	}
}

func TestWorktreeRetirementPlanSupportsWorktreeOnlyAndBranchOnlyTargets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*WorktreeRetirementPlanRequest)
	}{
		{name: "worktree only", mutate: func(r *WorktreeRetirementPlanRequest) { r.CandidateRef = "" }},
		{name: "branch only", mutate: func(r *WorktreeRetirementPlanRequest) { r.WorktreePath = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newRetirementGitFixture(t)
			req := retirementPlanRequest(fixture)
			tc.mutate(&req)
			plan, err := BuildWorktreeRetirementPlan(context.Background(), req, WorktreeRetirementDependencies{
				Git: ExecWorktreeRetirementGit{}, Liveness: staticRetirementLiveness{state: WorktreeLivenessUnused},
			}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if !plan.Eligible {
				t.Fatalf("target blocked: %v", plan.BlockReasons)
			}
		})
	}
}

func TestWorktreeRetirementPlanRefusesMissingAndPrunableWorktree(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, retirementGitFixture) string
		want    string
	}{
		{name: "missing", prepare: func(t *testing.T, f retirementGitFixture) string {
			return filepath.Join(t.TempDir(), "missing-worktree")
		}, want: "worktree_not_registered_metadata_repair_required"},
		{name: "prunable", prepare: func(t *testing.T, f retirementGitFixture) string {
			if err := os.RemoveAll(f.worktree); err != nil {
				t.Fatal(err)
			}
			return f.worktree
		}, want: "worktree_prunable_metadata_repair_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newRetirementGitFixture(t)
			req := retirementPlanRequest(fixture)
			req.WorktreePath = tc.prepare(t, fixture)
			plan, err := BuildWorktreeRetirementPlan(context.Background(), req, WorktreeRetirementDependencies{
				Git: ExecWorktreeRetirementGit{}, Liveness: staticRetirementLiveness{state: WorktreeLivenessUnused},
			}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if plan.Eligible || !retirementPlanBlockedBy(plan, tc.want) {
				t.Fatalf("missing/prunable blockers=%v", plan.BlockReasons)
			}
		})
	}
}

func TestLSOFWorktreeRetirementLivenessRequiresExactStdoutStderrExitContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result WorktreeRetirementProcessResult
		err    error
		want   WorktreeLivenessState
	}{
		{name: "exit one no output is unused", result: WorktreeRetirementProcessResult{ExitCode: 1}, want: WorktreeLivenessUnused},
		{name: "exit zero matching stdout is active", result: WorktreeRetirementProcessResult{Stdout: "COMMAND PID NAME\nagent 7 /tmp/work/file", ExitCode: 0}, want: WorktreeLivenessActive},
		{name: "exit one no stdout ignores stderr prose", result: WorktreeRetirementProcessResult{Stderr: "warning text", ExitCode: 1}, want: WorktreeLivenessUnused},
		{name: "exit zero empty is unknown", result: WorktreeRetirementProcessResult{ExitCode: 0}, want: WorktreeLivenessUnknown},
		{name: "exit one stdout is unknown", result: WorktreeRetirementProcessResult{Stdout: "unexpected", ExitCode: 1}, want: WorktreeLivenessUnknown},
		{name: "other exit is unknown", result: WorktreeRetirementProcessResult{ExitCode: 2}, want: WorktreeLivenessUnknown},
		{name: "spawn error is unknown", err: errors.New("spawn failed"), want: WorktreeLivenessUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := LSOFWorktreeRetirementLiveness{Runner: staticRetirementProcessRunner{result: tc.result, err: tc.err}}
			if got := probe.Observe(context.Background(), "/tmp/synthetic-worktree"); got.State != tc.want {
				t.Fatalf("liveness=%+v want=%s", got, tc.want)
			}
		})
	}
}

type staticRetirementProcessRunner struct {
	result WorktreeRetirementProcessResult
	err    error
}

func (r staticRetirementProcessRunner) Run(context.Context, string, ...string) (WorktreeRetirementProcessResult, error) {
	return r.result, r.err
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
	recoveryRef  string
}

func retirementPlanRequest(f retirementGitFixture) WorktreeRetirementPlanRequest {
	return WorktreeRetirementPlanRequest{
		RepoRoot: f.repo, WorktreePath: f.worktree, CandidateRef: f.candidateRef,
		AuthorityRef: "refs/heads/main", PublicationRef: "refs/remotes/origin/main",
		OwnerProof: validRetirementOwnerProof(), SourceProof: validRetirementSourceProof(f.candidateTip),
		RecoveryRefs: []string{f.recoveryRef},
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
	recoveryRef := writeRetirementRecoveryProof(t, filepath.Join(root, "recovery.bundle"), []byte("candidate recovery body"))
	return retirementGitFixture{repo: repo, worktree: worktree, candidateRef: candidateRef, candidateTip: candidateTip, mainTip: mainTip, recoveryRef: recoveryRef}
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
	recoveryRef := writeRetirementRecoveryProof(t, filepath.Join(root, "recovery.bundle"), []byte("divergent candidate recovery body"))
	return retirementGitFixture{repo: repo, worktree: worktree, candidateRef: "refs/heads/candidate", candidateTip: candidateTip, mainTip: mainTip, recoveryRef: recoveryRef}
}

func writeRetirementRecoveryProof(t *testing.T, path string, body []byte) string {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return "sha256-file:" + digestRetirementHex(body) + ":" + path
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
