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
	"compound/internal/authority/gitrepository"
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
	if !stringSliceContains(owner.ActionIntentIDs, result.ActionIntentID) || owner.PolicyDecisionID != result.PolicyDecisionID {
		t.Fatalf("owner work action chain runs=%v policy=%s", owner.ActionIntentIDs, owner.PolicyDecisionID)
	}
	for _, id := range []string{result.ActionIntentID, result.PolicyDecisionID, result.ReceiptID, result.TransitionID} {
		if !testEventExists(t, homePath, id) {
			t.Fatalf("missing event %s", id)
		}
	}
	var receipt envelope.Receipt
	readTestEventJSON(t, homePath, result.ReceiptID, &receipt)
	var transition envelope.Transition
	readTestEventJSON(t, homePath, result.TransitionID, &transition)
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
	if !stringSliceContains(owner.ActionIntentIDs, partial.ActionIntentID) || !stringSliceContains(owner.ActionIntentIDs, completed.ActionIntentID) {
		t.Fatalf("partial/success actions not both linked: %v", owner.ActionIntentIDs)
	}
}

func TestAuthorityWorktreeRetireCLIReviewPreservesGeneratedTemplate(t *testing.T) {
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
		Paths authority.WorktreeRetirementPlanPaths `json:"paths"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &planned); err != nil {
		t.Fatalf("decode plan: %v\n%s", err, stdout.String())
	}
	templateBefore, err := os.ReadFile(planned.Paths.ReviewYAMLPath)
	if err != nil {
		t.Fatal(err)
	}

	reviewArgs := []string{
		"review", planned.Paths.ReviewYAMLPath,
		"--decision", "authorize_retirement",
		"--rationale", "preserve the generated template while recording reviewed content",
		"--reviewed-by", "operator:test", "--json",
	}
	stdout.Reset()
	stderr.Reset()
	code = runAuthorityWorktreeRetire(homePath, reviewArgs, &stdout, &stderr, cliTestNow.Add(11*time.Minute))
	if code != 0 {
		t.Fatalf("review code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	firstOutput := append([]byte(nil), stdout.Bytes()...)
	var firstReport authority.WorktreeRetirementReviewReport
	if err := json.Unmarshal(firstOutput, &firstReport); err != nil {
		t.Fatalf("decode first review: %v\n%s", err, firstOutput)
	}
	templateAfter, err := os.ReadFile(planned.Paths.ReviewYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(templateAfter, templateBefore) {
		t.Fatalf("review command overwrote generated template:\nbefore:\n%s\nafter:\n%s", templateBefore, templateAfter)
	}
	firstSource, err := os.ReadFile(firstReport.SourcePath)
	if err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = runAuthorityWorktreeRetire(homePath, reviewArgs, &stdout, &stderr, cliTestNow.Add(12*time.Minute))
	if code != 0 {
		t.Fatalf("same review replay code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var replayed authority.WorktreeRetirementReviewReport
	if err := json.Unmarshal(stdout.Bytes(), &replayed); err != nil {
		t.Fatalf("decode replayed review: %v\n%s", err, stdout.String())
	}
	if replayed.ID != firstReport.ID || !bytes.Equal(stdout.Bytes(), firstOutput) {
		t.Fatalf("same review replay changed report: first=%+v replayed=%+v", firstReport, replayed)
	}
	templateAfterReplay, err := os.ReadFile(planned.Paths.ReviewYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(templateAfterReplay, templateBefore) {
		t.Fatalf("same review replay changed generated template")
	}
	sourceAfterReplay, err := os.ReadFile(firstReport.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sourceAfterReplay, firstSource) {
		t.Fatalf("same review replay changed reviewed source")
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
	completeSourceBoundProofForCLITest(t, store, homePath, source.ID, "codex:source", worktreePath, cliTestNow.Add(3*time.Minute+time.Second))
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

func completeDetachedSourceBoundProofForCLITest(
	t *testing.T,
	store *work.Store,
	homePath string,
	workID string,
	actor string,
	repo string,
	candidate string,
	now time.Time,
) {
	t.Helper()
	proofWorktree := filepath.Join(t.TempDir(), "proof-worktree")
	runLandingCLIGit(t, repo, "worktree", "add", "--detach", proofWorktree, candidate)
	completeSourceBoundProofForCLITest(t, store, homePath, workID, actor, proofWorktree, now)
	runLandingCLIGit(t, repo, "worktree", "remove", proofWorktree)
}

func completeSourceBoundProofForCLITest(
	t *testing.T,
	store *work.Store,
	homePath string,
	workID string,
	actor string,
	workspace string,
	now time.Time,
) {
	t.Helper()
	item, err := store.Get(workID)
	if err != nil {
		t.Fatal(err)
	}
	if item.GitDelivery == nil {
		t.Fatalf("work %s has no git delivery contract", workID)
	}
	if item.State == "ready" {
		item, err = store.Claim(workID, actor, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	source, err := gitrepository.CaptureCleanSnapshot(workspace, item.GitDelivery.ExpectedBaseSHA)
	if err != nil {
		t.Fatal(err)
	}
	gates := []work.ProofResourceGateProfile{{
		Name:     "synthetic-gate",
		Command:  []string{"go", "test", "./internal/synthetic"},
		ProofRef: "synthetic-proof",
	}}
	profile, err := work.NewProofResourceProfileWithSource(
		work.ProofResourceProfileCanonicalFull,
		workspace,
		source,
		gates,
	)
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := store.ReserveProofResource(workID, work.ProofResourceReserveInput{
		Actor:     actor,
		TTL:       time.Minute,
		Workspace: workspace,
		Command:   "compound proof-check --base " + source.BaseSHA + " --out /synthetic/worktree-retirement.json",
		Profile:   profile,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := reserved.ExecutionBarrier.Close(); err != nil {
		t.Fatal(err)
	}
	reportBody, err := json.Marshal(map[string]any{
		"schema_version":  "compound.proof_check_report.v0.3",
		"profile_kind":    work.ProofResourceProfileCanonicalFull,
		"status":          "ok",
		"generated_at":    now.Add(time.Second).Format(time.RFC3339),
		"repo":            workspace,
		"base_ref":        source.BaseSHA,
		"source_snapshot": source,
		"dry_run":         false,
		"proof_refs":      []string{"synthetic-proof"},
		"steps": []map[string]any{{
			"name":      "synthetic-gate",
			"command":   []string{"go", "test", "./internal/synthetic"},
			"proof_ref": "synthetic-proof",
			"status":    "ok",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := home.NewArtifactStore(homePath)
	if err != nil {
		t.Fatal(err)
	}
	reportRef, err := artifacts.Put(reportBody)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteProofResource(workID, work.ProofResourceCompleteInput{
		Actor:             actor,
		ReservationID:     reserved.Reservation.ID,
		ReportArtifactRef: reportRef,
		ProducerExitKnown: true,
		ProducerExitCode:  0,
	}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
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
