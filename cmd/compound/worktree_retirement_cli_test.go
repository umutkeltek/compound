package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/authority"
	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/work"
)

func TestAuthorityWorktreeRetireCLIPlansReviewsAppliesAndRecordsWorkChain(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	fixture := newWorktreeRetirementCLIFixture(t, homePath)
	previousLiveness := worktreeRetirementCLILiveness
	worktreeRetirementCLILiveness = cliUnusedWorktreeLiveness{}
	t.Cleanup(func() { worktreeRetirementCLILiveness = previousLiveness })

	var stdout, stderr bytes.Buffer
	code := runAuthorityWorktreeRetire(homePath, []string{
		"plan", "--repo", fixture.repo, "--worktree", fixture.worktree,
		"--ref", fixture.candidateRef, "--authority-ref", "refs/heads/main",
		"--publication-ref", "refs/remotes/origin/main",
		"--owner-work", fixture.ownerWorkID, "--source-work", fixture.sourceWorkID,
		"--recovery-ref", fixture.recoveryRef, "--json",
	}, &stdout, &stderr, cliTestNow.Add(10*time.Minute))
	if code != 0 {
		t.Fatalf("plan code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var planned struct {
		Plan  authority.WorktreeRetirementPlan      `json:"plan"`
		Paths authority.WorktreeRetirementPlanPaths `json:"paths"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &planned); err != nil {
		t.Fatalf("decode plan: %v\n%s", err, stdout.String())
	}
	if !planned.Plan.Eligible || planned.Plan.OwnerProof.WorkID != fixture.ownerWorkID || planned.Plan.SourceProof.WorkID != fixture.sourceWorkID {
		t.Fatalf("planned retirement = %+v", planned.Plan)
	}

	stdout.Reset()
	stderr.Reset()
	code = runAuthorityWorktreeRetire(homePath, []string{
		"review", planned.Paths.ReviewYAMLPath,
		"--decision", "authorize_retirement",
		"--rationale", "operator approves exact redundant branch and inactive clean worktree",
		"--reviewed-by", "operator:test", "--json",
	}, &stdout, &stderr, cliTestNow.Add(11*time.Minute))
	if code != 0 {
		t.Fatalf("review code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var reviewed authority.WorktreeRetirementReviewReport
	if err := json.Unmarshal(stdout.Bytes(), &reviewed); err != nil {
		t.Fatalf("decode review: %v\n%s", err, stdout.String())
	}
	if reviewed.Status != "authorized" || reviewed.PlanDigest != planned.Plan.PlanDigest {
		t.Fatalf("review=%+v", reviewed)
	}

	stdout.Reset()
	stderr.Reset()
	code = runAuthorityWorktreeRetire(homePath, []string{
		"apply", planned.Paths.PlanPath, "--actor", "codex:test", "--json",
	}, &stdout, &stderr, cliTestNow.Add(12*time.Minute))
	if code != 0 {
		t.Fatalf("apply code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var result authority.WorktreeRetirementResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode apply: %v\n%s", err, stdout.String())
	}
	if result.Status != "completed" || result.ActionIntentID == "" || result.PolicyDecisionID == "" || result.ReceiptID == "" || result.TransitionID == "" {
		t.Fatalf("apply result=%+v", result)
	}
	if _, err := os.Stat(fixture.worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree remains: %v", err)
	}
	if commandRefExists(fixture.repo, fixture.candidateRef) {
		t.Fatal("candidate ref remains")
	}

	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.Get(fixture.ownerWorkID)
	if err != nil {
		t.Fatal(err)
	}
	if !stringSliceContains(owner.RunIDs, result.ActionIntentID) || owner.PolicyDecisionID != result.PolicyDecisionID {
		t.Fatalf("owner work action chain runs=%v policy=%s", owner.RunIDs, owner.PolicyDecisionID)
	}
	for _, id := range []string{result.ActionIntentID, result.PolicyDecisionID, result.ReceiptID, result.TransitionID} {
		if _, err := os.Stat(filepath.Join(homePath, "events", id+".json")); err != nil {
			t.Fatalf("missing event %s: %v", id, err)
		}
	}
	var receipt envelope.Receipt
	readWorktreeRetirementCLIJSON(t, filepath.Join(homePath, "events", result.ReceiptID+".json"), &receipt)
	var transition envelope.Transition
	readWorktreeRetirementCLIJSON(t, filepath.Join(homePath, "events", result.TransitionID+".json"), &transition)
	if receipt.IDs.WorkID != fixture.ownerWorkID || transition.IDs.WorkID != fixture.ownerWorkID || !stringSliceContains(transition.ActionRefs, result.ActionIntentID) || !stringSliceContains(transition.AfterRefs, result.ReceiptID) {
		t.Fatalf("recorded chain receipt=%+v transition=%+v", receipt, transition)
	}
}

func TestAuthorityWorktreeRetireCLIPartialRetryRecordsDistinctHonestActions(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	fixture := newWorktreeRetirementCLIFixture(t, homePath)
	previousLiveness := worktreeRetirementCLILiveness
	previousGit := worktreeRetirementCLIGit
	worktreeRetirementCLILiveness = cliUnusedWorktreeLiveness{}
	worktreeRetirementCLIGit = &cliFailOnceRetirementRefDelete{delegate: authority.ExecWorktreeRetirementGit{}}
	t.Cleanup(func() {
		worktreeRetirementCLILiveness = previousLiveness
		worktreeRetirementCLIGit = previousGit
	})

	var stdout, stderr bytes.Buffer
	code := runAuthorityWorktreeRetire(homePath, []string{
		"plan", "--repo", fixture.repo, "--worktree", fixture.worktree, "--ref", fixture.candidateRef,
		"--authority-ref", "refs/heads/main", "--publication-ref", "refs/remotes/origin/main",
		"--owner-work", fixture.ownerWorkID, "--source-work", fixture.sourceWorkID,
		"--recovery-ref", fixture.recoveryRef, "--json",
	}, &stdout, &stderr, cliTestNow.Add(10*time.Minute))
	if code != 0 {
		t.Fatalf("plan code=%d stderr=%s", code, stderr.String())
	}
	var planned struct {
		Plan  authority.WorktreeRetirementPlan      `json:"plan"`
		Paths authority.WorktreeRetirementPlanPaths `json:"paths"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &planned); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = runAuthorityWorktreeRetire(homePath, []string{
		"review", planned.Paths.ReviewYAMLPath, "--decision", "authorize_retirement",
		"--rationale", "exact retry-safe retirement", "--reviewed-by", "operator:test", "--json",
	}, &stdout, &stderr, cliTestNow.Add(11*time.Minute))
	if code != 0 {
		t.Fatalf("review code=%d stderr=%s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = runAuthorityWorktreeRetire(homePath, []string{"apply", planned.Paths.PlanPath, "--actor", "codex:test", "--json"}, &stdout, &stderr, cliTestNow.Add(12*time.Minute))
	var partial authority.WorktreeRetirementResult
	if err := json.Unmarshal(stdout.Bytes(), &partial); err != nil {
		t.Fatalf("decode partial: %v\n%s", err, stdout.String())
	}
	if code != 1 || partial.Status != "partial" || partial.ReceiptID == "" || !commandRefExists(fixture.repo, fixture.candidateRef) {
		t.Fatalf("partial code=%d result=%+v stderr=%s", code, partial, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = runAuthorityWorktreeRetire(homePath, []string{"apply", planned.Paths.PlanPath, "--actor", "codex:test", "--json"}, &stdout, &stderr, cliTestNow.Add(13*time.Minute))
	var completed authority.WorktreeRetirementResult
	if err := json.Unmarshal(stdout.Bytes(), &completed); err != nil {
		t.Fatalf("decode completed: %v\n%s", err, stdout.String())
	}
	if code != 0 || completed.Status != "completed" || completed.ReceiptID == "" || completed.ActionIntentID == partial.ActionIntentID {
		t.Fatalf("retry code=%d completed=%+v partial=%+v stderr=%s", code, completed, partial, stderr.String())
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.Get(fixture.ownerWorkID)
	if err != nil {
		t.Fatal(err)
	}
	if !stringSliceContains(owner.RunIDs, partial.ActionIntentID) || !stringSliceContains(owner.RunIDs, completed.ActionIntentID) {
		t.Fatalf("partial/success actions not both linked: %v", owner.RunIDs)
	}
}

type cliUnusedWorktreeLiveness struct{}

func (cliUnusedWorktreeLiveness) Observe(context.Context, string) authority.WorktreeLivenessObservation {
	return authority.WorktreeLivenessObservation{State: authority.WorktreeLivenessUnused, Evidence: "synthetic CLI fixture"}
}

type cliFailOnceRetirementRefDelete struct {
	delegate authority.WorktreeRetirementGit
	failed   bool
}

func (r *cliFailOnceRetirementRefDelete) Run(ctx context.Context, repo string, args ...string) (string, error) {
	if !r.failed && len(args) >= 2 && args[0] == "update-ref" && args[1] == "-d" {
		r.failed = true
		return "", &authority.WorktreeRetirementCommandError{Argv: append([]string{"git"}, args...), ExitCode: 73, Output: "injected transient failure"}
	}
	return r.delegate.Run(ctx, repo, args...)
}

type worktreeRetirementCLIFixture struct {
	repo         string
	worktree     string
	candidateRef string
	candidateTip string
	ownerWorkID  string
	sourceWorkID string
	recoveryRef  string
}

func newWorktreeRetirementCLIFixture(t *testing.T, homePath string) worktreeRetirementCLIFixture {
	t.Helper()
	repo, base, candidate := initLandingCLIRepo(t)
	worktreePath := filepath.Join(filepath.Dir(repo), "candidate-worktree")
	runLandingCLIGit(t, repo, "worktree", "add", worktreePath, "candidate")
	runLandingCLIGit(t, repo, "merge", "--ff-only", "candidate")
	if err := os.WriteFile(filepath.Join(repo, "main-only.txt"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runLandingCLIGit(t, repo, "add", "main-only.txt")
	runLandingCLIGit(t, repo, "commit", "-m", "main descendant")
	mainTip := landingCLIOutput(t, repo, "rev-parse", "HEAD")
	runLandingCLIGit(t, repo, "update-ref", "refs/remotes/origin/main", mainTip)
	repo, _ = filepath.EvalSymlinks(repo)
	worktreePath, _ = filepath.EvalSymlinks(worktreePath)

	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.Create(work.CreateInput{
		Owner: "source-land", Actor: "codex:source", SuccessPredicate: "candidate landed and published",
		GitDelivery: &envelope.GitDeliveryContract{
			TargetWorktreeRoot: repo, TargetLocalRef: "refs/heads/main", ExpectedBaseSHA: base,
			PublicationMode: "required", PublicationRef: "refs/remotes/origin/main",
		},
	}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(source.ID, "codex:source", cliTestNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(source.ID, work.AdvanceInput{To: "running", Actor: "codex:source"}, cliTestNow.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(source.ID, work.AdvanceInput{To: "verifying", Actor: "codex:source", ArtifactID: "commit:" + candidate, VerificationID: "check:source-landed"}, cliTestNow.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	landed, err := store.LandCheck(source.ID, work.LandCheckOptions{CandidateTipSHA: candidate, Actor: "codex:source", Record: true}, cliTestNow.Add(4*time.Minute))
	if err != nil || landed.Status != "landed" {
		t.Fatalf("land-check=%+v err=%v", landed, err)
	}
	if _, err := store.Close(source.ID, work.CloseInput{
		Outcome: "completed", MoveType: "control_plane_repair", Actor: "codex:source", Summary: "candidate landed", Changed: true,
		ProofRefs: []string{landed.EvidenceRef, "check:source-landed"},
	}, cliTestNow.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	owner, err := store.Create(work.CreateInput{Owner: "workspace-reconciliation", Actor: "codex:test", SuccessPredicate: "redundant Git surfaces retired"}, cliTestNow.Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(owner.ID, "codex:test", cliTestNow.Add(7*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(owner.ID, work.AdvanceInput{To: "running", Actor: "codex:test"}, cliTestNow.Add(8*time.Minute)); err != nil {
		t.Fatal(err)
	}
	recoveryRef := writeWorktreeRetirementCLIRecoveryProof(t, filepath.Join(filepath.Dir(repo), "recovery.bundle"), []byte("candidate recovery body"))
	return worktreeRetirementCLIFixture{repo: repo, worktree: worktreePath, candidateRef: "refs/heads/candidate", candidateTip: candidate, ownerWorkID: owner.ID, sourceWorkID: source.ID, recoveryRef: recoveryRef}
}

func writeWorktreeRetirementCLIRecoveryProof(t *testing.T, path string, body []byte) string {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return "sha256-file:" + hex.EncodeToString(sum[:]) + ":" + path
}

func commandRefExists(repo, ref string) bool {
	cmd := exec.Command("git", "-C", repo, "rev-parse", "--verify", ref)
	return cmd.Run() == nil
}

func readWorktreeRetirementCLIJSON(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}
