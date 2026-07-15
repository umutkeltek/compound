package authority

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/policy"
)

func TestApplyWorktreeRetirementRemovesNonForceThenCASDeletesAndIsIdempotent(t *testing.T) {
	fixture := newRetirementGitFixture(t)
	home, paths, report := authorizedRetirementFixture(t, fixture)
	runner := &recordingRetirementGit{delegate: ExecWorktreeRetirementGit{}}
	recorded := 0
	deps := WorktreeRetirementApplyDependencies{
		Git: runner, Liveness: staticRetirementLiveness{state: WorktreeLivenessUnused},
		Evaluate: func(action envelope.ActionIntent, now time.Time) (envelope.PolicyDecision, error) {
			return policy.Evaluate(action, policy.DefaultConfig(), now), nil
		},
		RecordOutcome: func(outcome WorktreeRetirementActionOutcome, _ time.Time) (WorktreeRetirementActionProof, error) {
			recorded++
			if outcome.Outcome != "completed" || !outcome.Changed || outcome.Action.Capability != "source-control.worktree-retire" || outcome.Action.Action != "retire_redundant_git_surface" {
				t.Fatalf("action outcome = %+v", outcome)
			}
			return WorktreeRetirementActionProof{ActionIntentID: outcome.Action.ID, PolicyDecisionID: outcome.Decision.ID, ReceiptID: "rec_retirement", TransitionID: "tr_retirement"}, nil
		},
	}
	request := WorktreeRetirementApplyRequest{
		HomePath: home, PlanPath: paths.PlanPath, ReviewReportPath: report.ReviewPath,
		Actor: "codex:test", OwnerProof: validRetirementOwnerProof(), SourceProof: validRetirementSourceProof(fixture.candidateTip),
	}
	result, err := ApplyWorktreeRetirement(context.Background(), request, deps, time.Date(2026, 7, 15, 7, 2, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || !result.WorktreeRemoved || !result.RefDeleted || result.ReceiptID != "rec_retirement" || result.TransitionID != "tr_retirement" {
		t.Fatalf("apply result = %+v", result)
	}
	if _, err := os.Stat(fixture.worktree); !os.IsNotExist(err) {
		t.Fatalf("retired worktree still exists: %v", err)
	}
	if retirementGitRefExists(fixture.repo, fixture.candidateRef) {
		t.Fatal("candidate ref still exists")
	}
	if got := retirementGitOutput(t, fixture.repo, "rev-parse", "refs/heads/main"); got != fixture.mainTip {
		t.Fatalf("authority ref moved to %s", got)
	}
	if got := retirementGitOutput(t, fixture.repo, "rev-parse", "refs/remotes/origin/main"); got != fixture.mainTip {
		t.Fatalf("publication ref moved to %s", got)
	}
	assertRetirementMutationArgv(t, runner.calls, fixture)
	mutations := runner.mutationCount()

	replayed, err := ApplyWorktreeRetirement(context.Background(), request, deps, time.Date(2026, 7, 15, 7, 3, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || replayed.Status != "completed" || runner.mutationCount() != mutations || recorded != 1 {
		t.Fatalf("idempotent replay=%+v mutations=%d/%d recorded=%d", replayed, runner.mutationCount(), mutations, recorded)
	}
}

func TestApplyWorktreeRetirementResumesAfterPersistedWorktreeRemoval(t *testing.T) {
	fixture := newRetirementGitFixture(t)
	home, paths, report := authorizedRetirementFixture(t, fixture)
	crash := errors.New("injected crash after durable worktree removal")
	runner := &recordingRetirementGit{delegate: ExecWorktreeRetirementGit{}}
	records := 0
	deps := successfulRetirementApplyDependencies(runner, &records)
	deps.AfterPhase = func(phase string) error {
		if phase == WorktreeRetirementPhaseWorktreeRemoved {
			return crash
		}
		return nil
	}
	req := WorktreeRetirementApplyRequest{
		HomePath: home, PlanPath: paths.PlanPath, ReviewReportPath: report.ReviewPath,
		Actor: "codex:test", OwnerProof: validRetirementOwnerProof(), SourceProof: validRetirementSourceProof(fixture.candidateTip),
	}
	if result, err := ApplyWorktreeRetirement(context.Background(), req, deps, time.Now()); !errors.Is(err, crash) || result.Phase != WorktreeRetirementPhaseWorktreeRemoved {
		t.Fatalf("crash result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(fixture.worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree removal not durable: %v", err)
	}
	if !retirementGitRefExists(fixture.repo, fixture.candidateRef) {
		t.Fatal("ref deleted before durable phase boundary")
	}
	if records != 0 {
		t.Fatalf("crash incorrectly recorded terminal action: %d", records)
	}

	deps.AfterPhase = nil
	resumed, err := ApplyWorktreeRetirement(context.Background(), req, deps, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status != "completed" || !resumed.WorktreeRemoved || !resumed.RefDeleted || records != 1 {
		t.Fatalf("resumed result=%+v records=%d", resumed, records)
	}
	if runner.worktreeRemoveCount() != 1 || runner.refDeleteCount() != 1 {
		t.Fatalf("resume repeated destructive phase: calls=%+v", runner.calls)
	}
}

func TestApplyWorktreeRetirementResumesPreparedIntentWithoutGitMutation(t *testing.T) {
	fixture := newRetirementGitFixture(t)
	home, paths, report := authorizedRetirementFixture(t, fixture)
	crash := errors.New("injected crash after durable prepared intent")
	runner := &recordingRetirementGit{delegate: ExecWorktreeRetirementGit{}}
	records := 0
	deps := successfulRetirementApplyDependencies(runner, &records)
	deps.AfterPhase = func(phase string) error {
		if phase == WorktreeRetirementPhasePrepared {
			return crash
		}
		return nil
	}
	req := WorktreeRetirementApplyRequest{
		HomePath: home, PlanPath: paths.PlanPath, ReviewReportPath: report.ReviewPath,
		Actor: "codex:test", OwnerProof: validRetirementOwnerProof(), SourceProof: validRetirementSourceProof(fixture.candidateTip),
	}
	if result, err := ApplyWorktreeRetirement(context.Background(), req, deps, time.Now()); !errors.Is(err, crash) || result.Phase != WorktreeRetirementPhasePrepared {
		t.Fatalf("prepared crash result=%+v err=%v", result, err)
	}
	if runner.mutationCount() != 0 || !retirementGitRefExists(fixture.repo, fixture.candidateRef) {
		t.Fatalf("prepared crash mutated Git: %+v", runner.calls)
	}
	deps.AfterPhase = nil
	resumed, err := ApplyWorktreeRetirement(context.Background(), req, deps, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status != "completed" || records != 1 || runner.worktreeRemoveCount() != 1 || runner.refDeleteCount() != 1 {
		t.Fatalf("prepared resume result=%+v calls=%+v records=%d", resumed, runner.calls, records)
	}
}

func TestApplyWorktreeRetirementCASDriftLeavesRefAndRecordsPartialOutcome(t *testing.T) {
	fixture := newRetirementGitFixture(t)
	home, paths, report := authorizedRetirementFixture(t, fixture)
	runner := &casDriftRetirementGit{delegate: ExecWorktreeRetirementGit{}, ref: fixture.candidateRef, replacement: fixture.mainTip}
	records := 0
	deps := successfulRetirementApplyDependencies(runner, &records)
	result, err := ApplyWorktreeRetirement(context.Background(), WorktreeRetirementApplyRequest{
		HomePath: home, PlanPath: paths.PlanPath, ReviewReportPath: report.ReviewPath,
		Actor: "codex:test", OwnerProof: validRetirementOwnerProof(), SourceProof: validRetirementSourceProof(fixture.candidateTip),
	}, deps, time.Now())
	if err == nil || result.Status != "partial" || !result.WorktreeRemoved || result.RefDeleted {
		t.Fatalf("CAS drift result=%+v err=%v", result, err)
	}
	if got := retirementGitOutput(t, fixture.repo, "rev-parse", fixture.candidateRef); got != fixture.mainTip {
		t.Fatalf("CAS drift deleted or rewrote ref to %s", got)
	}
	if records != 1 {
		t.Fatalf("partial outcome records=%d", records)
	}
}

func TestApplyWorktreeRetirementRetriesPartialOnlyWhileExactRefRemains(t *testing.T) {
	fixture := newRetirementGitFixture(t)
	home, paths, report := authorizedRetirementFixture(t, fixture)
	injected := errors.New("transient update-ref failure")
	runner := &failOnceRefDeleteGit{delegate: ExecWorktreeRetirementGit{}, err: injected}
	records := 0
	deps := successfulRetirementApplyDependencies(runner, &records)
	req := WorktreeRetirementApplyRequest{
		HomePath: home, PlanPath: paths.PlanPath, ReviewReportPath: report.ReviewPath,
		Actor: "codex:test", OwnerProof: validRetirementOwnerProof(), SourceProof: validRetirementSourceProof(fixture.candidateTip),
	}
	first, err := ApplyWorktreeRetirement(context.Background(), req, deps, time.Now())
	if !errors.Is(err, injected) || first.Status != "partial" || !retirementGitRefExists(fixture.repo, fixture.candidateRef) {
		t.Fatalf("partial first=%+v err=%v", first, err)
	}
	second, err := ApplyWorktreeRetirement(context.Background(), req, deps, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != "completed" || retirementGitRefExists(fixture.repo, fixture.candidateRef) || runner.removeCalls != 1 || runner.deleteCalls != 2 || records != 2 {
		t.Fatalf("partial retry=%+v remove=%d delete=%d records=%d", second, runner.removeCalls, runner.deleteCalls, records)
	}
}

func TestApplyWorktreeRetirementRefusesPolicyAndStateDriftBeforeMutation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*WorktreeRetirementApplyDependencies)
		want    string
	}{
		{name: "liveness drift", prepare: func(d *WorktreeRetirementApplyDependencies) {
			d.Liveness = staticRetirementLiveness{state: WorktreeLivenessActive}
		}, want: "drift"},
		{name: "policy hold", prepare: func(d *WorktreeRetirementApplyDependencies) {
			d.Evaluate = func(action envelope.ActionIntent, now time.Time) (envelope.PolicyDecision, error) {
				decision := policy.Evaluate(action, policy.DefaultConfig(), now)
				decision.Outcome = "hold"
				decision.Obligations = nil
				return decision, nil
			}
		}, want: "policy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newRetirementGitFixture(t)
			home, paths, report := authorizedRetirementFixture(t, fixture)
			runner := &recordingRetirementGit{delegate: ExecWorktreeRetirementGit{}}
			records := 0
			deps := successfulRetirementApplyDependencies(runner, &records)
			tc.prepare(&deps)
			_, err := ApplyWorktreeRetirement(context.Background(), WorktreeRetirementApplyRequest{
				HomePath: home, PlanPath: paths.PlanPath, ReviewReportPath: report.ReviewPath,
				Actor: "codex:test", OwnerProof: validRetirementOwnerProof(), SourceProof: validRetirementSourceProof(fixture.candidateTip),
			}, deps, time.Now())
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if runner.mutationCount() != 0 || records != 0 || !retirementGitRefExists(fixture.repo, fixture.candidateRef) {
				t.Fatalf("refusal mutated state: calls=%+v records=%d", runner.calls, records)
			}
		})
	}
}

type retirementGitCall struct {
	repo string
	args []string
}

type recordingRetirementGit struct {
	delegate WorktreeRetirementGit
	calls    []retirementGitCall
}

type casDriftRetirementGit struct {
	delegate    WorktreeRetirementGit
	ref         string
	replacement string
	drifted     bool
}

func (r *casDriftRetirementGit) Run(ctx context.Context, repo string, args ...string) (string, error) {
	if !r.drifted && len(args) >= 2 && args[0] == "update-ref" && args[1] == "-d" {
		r.drifted = true
		if _, err := r.delegate.Run(ctx, repo, "update-ref", r.ref, r.replacement); err != nil {
			return "", err
		}
	}
	return r.delegate.Run(ctx, repo, args...)
}

type failOnceRefDeleteGit struct {
	delegate    WorktreeRetirementGit
	err         error
	failed      bool
	removeCalls int
	deleteCalls int
}

func (r *failOnceRefDeleteGit) Run(ctx context.Context, repo string, args ...string) (string, error) {
	if len(args) >= 2 && args[0] == "worktree" && args[1] == "remove" {
		r.removeCalls++
	}
	if len(args) >= 2 && args[0] == "update-ref" && args[1] == "-d" {
		r.deleteCalls++
		if !r.failed {
			r.failed = true
			return "", r.err
		}
	}
	return r.delegate.Run(ctx, repo, args...)
}

func (r *recordingRetirementGit) Run(ctx context.Context, repo string, args ...string) (string, error) {
	r.calls = append(r.calls, retirementGitCall{repo: repo, args: append([]string{}, args...)})
	return r.delegate.Run(ctx, repo, args...)
}

func (r *recordingRetirementGit) mutationCount() int {
	return r.worktreeRemoveCount() + r.refDeleteCount()
}
func (r *recordingRetirementGit) worktreeRemoveCount() int {
	n := 0
	for _, call := range r.calls {
		if len(call.args) >= 2 && call.args[0] == "worktree" && call.args[1] == "remove" {
			n++
		}
	}
	return n
}
func (r *recordingRetirementGit) refDeleteCount() int {
	n := 0
	for _, call := range r.calls {
		if len(call.args) >= 2 && call.args[0] == "update-ref" && call.args[1] == "-d" {
			n++
		}
	}
	return n
}

func authorizedRetirementFixture(t *testing.T, fixture retirementGitFixture) (string, WorktreeRetirementPlanPaths, WorktreeRetirementReviewReport) {
	t.Helper()
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
	report, err := RecordWorktreeRetirementReview(home, review, paths.ReviewYAMLPath, time.Date(2026, 7, 15, 7, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return home, paths, report
}

func successfulRetirementApplyDependencies(runner WorktreeRetirementGit, records *int) WorktreeRetirementApplyDependencies {
	return WorktreeRetirementApplyDependencies{
		Git: runner, Liveness: staticRetirementLiveness{state: WorktreeLivenessUnused},
		Evaluate: func(action envelope.ActionIntent, now time.Time) (envelope.PolicyDecision, error) {
			return policy.Evaluate(action, policy.DefaultConfig(), now), nil
		},
		RecordOutcome: func(outcome WorktreeRetirementActionOutcome, _ time.Time) (WorktreeRetirementActionProof, error) {
			*records++
			return WorktreeRetirementActionProof{ActionIntentID: outcome.Action.ID, PolicyDecisionID: outcome.Decision.ID, ReceiptID: "rec_retirement", TransitionID: "tr_retirement"}, nil
		},
	}
}

func retirementGitRefExists(repo, ref string) bool {
	cmd := ExecWorktreeRetirementGit{}
	_, err := cmd.Run(context.Background(), repo, "rev-parse", "--verify", ref)
	return err == nil
}

func assertRetirementMutationArgv(t *testing.T, calls []retirementGitCall, fixture retirementGitFixture) {
	t.Helper()
	var remove, delete bool
	for _, call := range calls {
		joined := strings.Join(call.args, " ")
		if strings.Contains(joined, "--force") {
			t.Fatalf("dangerous retirement argv: git %s", joined)
		}
		if len(call.args) > 0 {
			for _, forbidden := range []string{"reset", "checkout", "clean", "stash", "merge", "rebase", "push", "install"} {
				if call.args[0] == forbidden {
					t.Fatalf("dangerous retirement argv: git %s", joined)
				}
			}
		}
		if len(call.args) > 1 && call.args[0] == "worktree" && call.args[1] == "unlock" {
			t.Fatalf("dangerous retirement argv: git %s", joined)
		}
		if len(call.args) == 3 && call.args[0] == "worktree" && call.args[1] == "remove" && call.args[2] == fixture.worktree {
			remove = true
		}
		if len(call.args) == 4 && call.args[0] == "update-ref" && call.args[1] == "-d" && call.args[2] == fixture.candidateRef && call.args[3] == fixture.candidateTip {
			delete = true
		}
	}
	if !remove || !delete {
		t.Fatalf("missing non-force/CAS mutation argv: %+v", calls)
	}
}
