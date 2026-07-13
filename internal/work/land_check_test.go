package work

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
)

func TestLandCheckClassifiesReadyLandedAndStaleLineage(t *testing.T) {
	now := time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC)
	t.Run("ready then landed", func(t *testing.T) {
		repo, base := initLandingRepo(t)
		candidate := landingFeatureCommit(t, repo)
		landingGit(t, repo, "switch", "main")

		store, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		w, err := store.Create(CreateInput{
			Owner:            "codex",
			SuccessPredicate: "candidate reaches main",
			GitDelivery:      localGitDelivery(repo, base),
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		attachLandingCommit(t, store, w.ID, candidate, now.Add(30*time.Second))

		ready, err := store.LandCheck(w.ID, LandCheckOptions{
			RepoPath:        repo,
			CandidateTipSHA: candidate,
			TargetRef:       "main",
			Record:          true,
		}, now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if ready.Status != "ready_to_land" || ready.CandidateContainedInTarget || !ready.ExpectedBaseAncestorOfCandidate {
			t.Fatalf("ready report = %+v", ready)
		}
		if ready.EvidenceRef == "" || ready.ProofPath == "" {
			t.Fatalf("recorded check missing durable proof identity: %+v", ready)
		}
		if _, err := os.Stat(ready.ProofPath); err != nil {
			t.Fatalf("recorded proof missing: %v", err)
		}

		landingGit(t, repo, "merge", "--ff-only", candidate)
		landed, err := store.LandCheck(w.ID, LandCheckOptions{
			RepoPath:        repo,
			CandidateTipSHA: candidate,
			TargetRef:       "main",
			Record:          true,
		}, now.Add(2*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if landed.Status != "landed" || !landed.CandidateContainedInTarget || landed.TargetSHA != candidate {
			t.Fatalf("landed report = %+v", landed)
		}
	})

	t.Run("main moved", func(t *testing.T) {
		repo, base := initLandingRepo(t)
		candidate := landingFeatureCommit(t, repo)
		landingGit(t, repo, "switch", "main")
		writeLandingFile(t, filepath.Join(repo, "main.txt"), "main moved\n")
		landingGit(t, repo, "add", "main.txt")
		landingGit(t, repo, "commit", "-m", "main moved")

		store, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "stale candidate is explicit", GitDelivery: localGitDelivery(repo, base)}, now)
		if err != nil {
			t.Fatal(err)
		}
		attachLandingCommit(t, store, w.ID, candidate, now.Add(30*time.Second))
		report, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main"}, now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if report.Status != "rebase_required" || report.NextAction == "" {
			t.Fatalf("stale report = %+v", report)
		}
	})
}

func TestLandCheckFailsClosedForDirtyTargetAndMovedSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC)
	repo, base := initLandingRepo(t)
	candidate := landingFeatureCommit(t, repo)
	landingGit(t, repo, "switch", "main")

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "target remains exact", GitDelivery: localGitDelivery(repo, base)}, now)
	if err != nil {
		t.Fatal(err)
	}
	attachLandingCommit(t, store, w.ID, candidate, now.Add(30*time.Second))

	_, err = store.LandCheck(w.ID, LandCheckOptions{
		RepoPath:        repo,
		CandidateTipSHA: candidate,
		TargetRef:       "main",
		ExpectedMainSHA: strings.Repeat("f", 40),
	}, now.Add(time.Minute))
	if err == nil || !strings.Contains(err.Error(), "not delivery authority") {
		t.Fatalf("caller expected-main snapshot must not replace bound authority, got %v", err)
	}

	writeLandingFile(t, filepath.Join(repo, "dirty.txt"), "uncommitted\n")
	report, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main"}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "dirty_target" || report.CleanTarget {
		t.Fatalf("expected dirty_target, got %+v", report)
	}
}

func TestCompletedCodeWorkRequiresCurrentLandedProof(t *testing.T) {
	now := time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC)
	repo, base := initLandingRepo(t)
	candidate := landingFeatureCommit(t, repo)
	landingGit(t, repo, "switch", "main")

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "close only after containment", GitDelivery: localGitDelivery(repo, base)}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(w.ID, "codex", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(w.ID, AdvanceInput{To: "running", Actor: "codex"}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	attachLandingCommit(t, store, w.ID, candidate, now.Add(150*time.Second))

	if _, err := store.Close(w.ID, CloseInput{Outcome: "completed", MoveType: "outward_delta", Actor: "codex", Summary: "not landed"}, now.Add(3*time.Minute)); err == nil || !strings.Contains(err.Error(), "land-check") {
		t.Fatalf("completed without landing proof err = %v", err)
	}
	current, err := store.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != "running" {
		t.Fatalf("failed gate mutated work state to %q", current.State)
	}

	ready, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main", Record: true}, now.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Close(w.ID, CloseInput{Outcome: "completed", MoveType: "outward_delta", Actor: "codex", Summary: "still not landed", ProofRefs: []string{ready.EvidenceRef}}, now.Add(5*time.Minute)); err == nil || !strings.Contains(err.Error(), "not landed") {
		t.Fatalf("ready-only proof close err = %v", err)
	}

	landingGit(t, repo, "merge", "--ff-only", candidate)
	landed, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main", Record: true}, now.Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	closed, err := store.Close(w.ID, CloseInput{Outcome: "completed", MoveType: "outward_delta", Actor: "codex", Summary: "candidate is on main", ProofRefs: []string{landed.EvidenceRef}}, now.Add(7*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if closed.Work.State != "completed" || !contains(closed.Receipt.ProofRefs, landed.EvidenceRef) {
		t.Fatalf("closed result = %+v", closed)
	}
}

func TestCompletedCodeWorkRevalidatesLandingProofAtClose(t *testing.T) {
	now := time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC)
	repo, base := initLandingRepo(t)
	candidate := landingFeatureCommit(t, repo)
	landingGit(t, repo, "switch", "main")
	landingGit(t, repo, "merge", "--ff-only", candidate)

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "proof stays live", GitDelivery: localGitDelivery(repo, base)}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(w.ID, "codex", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(w.ID, AdvanceInput{To: "running", Actor: "codex"}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	attachLandingCommit(t, store, w.ID, candidate, now.Add(150*time.Second))
	proof, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main", Record: true}, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	writeLandingFile(t, filepath.Join(repo, "late-dirty.txt"), "dirty after proof\n")
	if _, err := store.Close(w.ID, CloseInput{Outcome: "completed", MoveType: "outward_delta", Actor: "codex", Summary: "must recheck", ProofRefs: []string{proof.EvidenceRef}}, now.Add(4*time.Minute)); err == nil || !strings.Contains(err.Error(), "dirty_target") {
		t.Fatalf("close with stale proof err = %v", err)
	}
}

func TestLandCheckRecordsMissingTipAndInvalidLineage(t *testing.T) {
	now := time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC)
	repo, base := initLandingRepo(t)
	landingGit(t, repo, "switch", "--orphan", "unrelated")
	if err := os.Remove(filepath.Join(repo, "README.md")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	writeLandingFile(t, filepath.Join(repo, "unrelated.txt"), "unrelated\n")
	landingGit(t, repo, "add", "-A")
	landingGit(t, repo, "commit", "-m", "unrelated")
	unrelated := landingGitOutput(t, repo, "rev-parse", "HEAD")
	landingGit(t, repo, "switch", "main")

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "bad lineage stays actionable", GitDelivery: localGitDelivery(repo, base)}, now)
	if err != nil {
		t.Fatal(err)
	}
	attachLandingCommit(t, store, w.ID, strings.Repeat("f", 40), now.Add(30*time.Second))

	missing, err := store.LandCheck(w.ID, LandCheckOptions{
		RepoPath:        repo,
		CandidateTipSHA: strings.Repeat("f", 40),
		TargetRef:       "main",
		Record:          true,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if missing.Status != "invalid_lineage" || missing.NextAction == "" || missing.EvidenceRef == "" {
		t.Fatalf("missing candidate report = %+v", missing)
	}

	attachLandingCommit(t, store, w.ID, unrelated, now.Add(90*time.Second))
	invalid, err := store.LandCheck(w.ID, LandCheckOptions{
		RepoPath:        repo,
		CandidateTipSHA: unrelated,
		TargetRef:       "main",
		Record:          true,
	}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if invalid.Status != "invalid_lineage" || invalid.ExpectedBaseAncestorOfCandidate || invalid.NextAction == "" {
		t.Fatalf("invalid lineage report = %+v", invalid)
	}

	if _, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: "abc123", TargetRef: "main"}, now.Add(3*time.Minute)); err == nil || !strings.Contains(err.Error(), "full") {
		t.Fatalf("short candidate SHA err = %v", err)
	}
}

func TestLandCheckRequiresLatestAttachedCommitArtifact(t *testing.T) {
	now := time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC)
	repo, base := initLandingRepo(t)
	candidate := landingFeatureCommit(t, repo)
	landingGit(t, repo, "switch", "main")

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "only the attached final tip can land", GitDelivery: localGitDelivery(repo, base)}, now)
	if err != nil {
		t.Fatal(err)
	}

	missing, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main", Record: true}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if missing.Status != "missing_candidate_artifact" || missing.NextAction == "" {
		t.Fatalf("missing candidate artifact report = %+v", missing)
	}

	attachLandingCommit(t, store, w.ID, base, now.Add(2*time.Minute))
	mismatch, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main", Record: true}, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if mismatch.Status != "candidate_tip_mismatch" || !strings.Contains(mismatch.NextAction, "commit:"+candidate) {
		t.Fatalf("candidate mismatch report = %+v", mismatch)
	}

	attachLandingCommit(t, store, w.ID, candidate, now.Add(4*time.Minute))
	ready, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main"}, now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if ready.Status != "ready_to_land" {
		t.Fatalf("latest attached candidate report = %+v", ready)
	}

	landingGit(t, repo, "switch", "feature")
	writeLandingFile(t, filepath.Join(repo, "second.txt"), "second\n")
	landingGit(t, repo, "add", "second.txt")
	landingGit(t, repo, "commit", "-m", "second")
	newTip := landingGitOutput(t, repo, "rev-parse", "HEAD")
	landingGit(t, repo, "switch", "main")
	attachLandingCommit(t, store, w.ID, newTip, now.Add(6*time.Minute))
	old, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main"}, now.Add(7*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != "candidate_tip_mismatch" || !strings.Contains(old.NextAction, newTip) {
		t.Fatalf("old candidate report = %+v", old)
	}

	attachLandingArtifact(t, store, w.ID, "commit:abc123", now.Add(8*time.Minute))
	malformed, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: newTip, TargetRef: "main"}, now.Add(9*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if malformed.Status != "invalid_candidate_artifact" || malformed.NextAction == "" {
		t.Fatalf("malformed candidate artifact report = %+v", malformed)
	}
}

func TestRecordedLandCheckContentIsTamperEvident(t *testing.T) {
	now := time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*landingProofFixture, *LandCheckReport)
	}{
		{name: "repo path", mutate: func(f *landingProofFixture, p *LandCheckReport) {
			alias := filepath.Join(f.scratch, "repo-alias")
			if err := os.Symlink(f.repo, alias); err != nil {
				t.Fatal(err)
			}
			p.RepoPath = alias
		}},
		{name: "candidate tip", mutate: func(f *landingProofFixture, p *LandCheckReport) { p.CandidateTipSHA = f.base }},
		{name: "status", mutate: func(_ *landingProofFixture, p *LandCheckReport) { p.Status = "ready_to_land" }},
		{name: "expected base", mutate: func(_ *landingProofFixture, p *LandCheckReport) { p.ExpectedBaseSHA = strings.Repeat("a", 40) }},
		{name: "target ref", mutate: func(_ *landingProofFixture, p *LandCheckReport) { p.TargetRef = "refs/heads/main" }},
		{name: "target sha", mutate: func(f *landingProofFixture, p *LandCheckReport) { p.TargetSHA = f.base }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newLandingProofFixture(t, now)
			tampered := fixture.proof
			tc.mutate(&fixture, &tampered)
			if err := fixture.store.writeJSON(fixture.proof.ProofPath, &tampered); err != nil {
				t.Fatal(err)
			}
			_, err := fixture.store.Close(fixture.workID, CloseInput{
				Outcome:   "completed",
				MoveType:  "outward_delta",
				Actor:     "codex",
				Summary:   "tampered proof must not close",
				ProofRefs: []string{fixture.proof.EvidenceRef},
			}, now.Add(10*time.Minute))
			if err == nil || !strings.Contains(err.Error(), "content digest") {
				t.Fatalf("tampered %s close err = %v", tc.name, err)
			}
		})
	}
}

func TestLandCheckRecordDoesNotAcceptTamperedExistingContent(t *testing.T) {
	now := time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC)
	repo, base := initLandingRepo(t)
	candidate := landingFeatureCommit(t, repo)
	landingGit(t, repo, "switch", "main")
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "record collision stays tamper evident", GitDelivery: localGitDelivery(repo, base)}, now)
	if err != nil {
		t.Fatal(err)
	}
	attachLandingCommit(t, store, w.ID, candidate, now.Add(time.Minute))
	checkAt := now.Add(2 * time.Minute)
	opts := LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main", Record: true}
	proof, err := store.LandCheck(w.ID, opts, checkAt)
	if err != nil {
		t.Fatal(err)
	}
	tampered := proof
	tampered.TargetSHA = candidate
	if err := store.writeJSON(proof.ProofPath, &tampered); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LandCheck(w.ID, opts, checkAt); err == nil || !strings.Contains(err.Error(), "tampered existing") {
		t.Fatalf("re-record over tampered proof err = %v", err)
	}
}

func TestLandingCloseGateLeavesLegacyAndFailedWorkCompatible(t *testing.T) {
	now := time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC)
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	legacy, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "legacy work still closes"}, now)
	if err != nil {
		t.Fatal(err)
	}
	activateLandingWork(t, store, legacy.ID, now)
	if _, err := store.Close(legacy.ID, CloseInput{Outcome: "completed", MoveType: "outward_delta", Actor: "codex", Summary: "legacy completed", ProofRefs: []string{"artifact:legacy"}}, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("legacy completion should not require landing proof: %v", err)
	}

	repo, base := initLandingRepo(t)
	code, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "failed code work reports failure", GitDelivery: localGitDelivery(repo, base)}, now.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	activateLandingWork(t, store, code.ID, now.Add(4*time.Minute))
	if _, err := store.Close(code.ID, CloseInput{Outcome: "failed", MoveType: "audit_only", Actor: "codex", Summary: "executor failed", ProofRefs: []string{"failure:executor"}}, now.Add(7*time.Minute)); err != nil {
		t.Fatalf("failed code work should not require landing proof: %v", err)
	}
}

func TestCompletedCommitArtifactWithoutGitDeliveryContractFailsClosed(t *testing.T) {
	now := time.Date(2026, 7, 13, 22, 0, 0, 0, time.UTC)
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "legacy code cannot disappear outside delivery authority"}, now)
	if err != nil {
		t.Fatal(err)
	}
	activateLandingWork(t, store, w.ID, now)
	attachLandingCommit(t, store, w.ID, strings.Repeat("a", 40), now.Add(3*time.Minute))

	if _, err := store.Close(w.ID, CloseInput{
		Outcome:   "completed",
		MoveType:  "outward_delta",
		Actor:     "codex",
		Summary:   "must fail without delivery authority",
		ProofRefs: []string{"check:focused-tests"},
	}, now.Add(4*time.Minute)); err == nil || !strings.Contains(err.Error(), "git_delivery") {
		t.Fatalf("commit artifact without git_delivery contract should fail closed, got %v", err)
	}
}

func TestLandCheckRejectsWrongRepoSecondCloneAndWrongBranch(t *testing.T) {
	now := time.Date(2026, 7, 13, 22, 0, 0, 0, time.UTC)
	repo, base := initLandingRepo(t)
	candidate := landingFeatureCommit(t, repo)
	landingGit(t, repo, "switch", "main")
	secondClone := filepath.Join(t.TempDir(), "second-clone")
	landingGit(t, filepath.Dir(secondClone), "clone", repo, secondClone)

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{
		Owner:            "codex",
		SuccessPredicate: "only the bound target worktree can certify delivery",
		GitDelivery:      localGitDelivery(repo, base),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	attachLandingCommit(t, store, w.ID, candidate, now.Add(time.Minute))

	if _, err := store.LandCheck(w.ID, LandCheckOptions{
		RepoPath:        secondClone,
		CandidateTipSHA: candidate,
		TargetRef:       "refs/heads/main",
	}, now.Add(2*time.Minute)); err == nil || !strings.Contains(err.Error(), "target_worktree_root") {
		t.Fatalf("second clone must not certify bound work, got %v", err)
	}
	if _, err := store.LandCheck(w.ID, LandCheckOptions{
		RepoPath:        repo,
		CandidateTipSHA: candidate,
		TargetRef:       "refs/heads/feature",
	}, now.Add(3*time.Minute)); err == nil || !strings.Contains(err.Error(), "target_local_ref") {
		t.Fatalf("feature ref must not replace the bound target branch, got %v", err)
	}

	landingGit(t, repo, "switch", "feature")
	report, err := store.LandCheck(w.ID, LandCheckOptions{CandidateTipSHA: candidate}, now.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "wrong_target_worktree" {
		t.Fatalf("feature-branch worktree must not certify the bound main target: %+v", report)
	}
}

func TestPublicationProofIsAttachedDiscoverableAndRequiredForDispatchClose(t *testing.T) {
	now := time.Date(2026, 7, 13, 22, 0, 0, 0, time.UTC)
	repo, base := initLandingRepo(t)
	remote := filepath.Join(t.TempDir(), "origin.git")
	landingGit(t, filepath.Dir(remote), "init", "--bare", remote)
	landingGit(t, repo, "remote", "add", "origin", remote)
	landingGit(t, repo, "push", "-u", "origin", "main")
	candidate := landingFeatureCommit(t, repo)
	landingGit(t, repo, "switch", "main")

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{
		Owner:            "codex",
		SuccessPredicate: "candidate is on bound main and origin main before close",
		GitDelivery:      publishedGitDelivery(repo, base),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	activateLandingWork(t, store, w.ID, now)
	if _, err := store.Advance(w.ID, AdvanceInput{
		To:             "verifying",
		Actor:          "codex",
		ArtifactID:     "commit:" + candidate,
		VerificationID: "check:focused-tests",
	}, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	landingGit(t, repo, "merge", "--ff-only", candidate)

	localOnly, err := store.LandCheck(w.ID, LandCheckOptions{CandidateTipSHA: candidate, Record: true, Actor: "codex"}, now.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if localOnly.Status != "awaiting_publication" || !localOnly.CandidateContainedInTarget || localOnly.CandidateContainedInPublication {
		t.Fatalf("local-main-only state must remain actionable: %+v", localOnly)
	}
	beforePublish, err := store.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if contains(beforePublish.EvidenceRefs, localOnly.EvidenceRef) {
		t.Fatalf("non-successful land check must not become close proof: %+v", beforePublish.EvidenceRefs)
	}
	bench, err := store.Workbench(now.Add(5*time.Minute), WorkbenchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	item := findWorkbenchItem(t, bench.Open, w.ID)
	if item.LandingStatus != "awaiting_publication" {
		t.Fatalf("workbench must surface publication debt: %+v", item)
	}
	blocked, err := store.DispatchClosePlan(now.Add(6*time.Minute), DispatchClosePlanOptions{Actor: "codex", WorkID: w.ID})
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Selected != nil || blocked.ClosePlan != nil || blocked.Status != "awaiting_proof" {
		t.Fatalf("dispatch must not offer close before discoverable current delivery proof: %+v", blocked)
	}

	landingGit(t, repo, "push", "origin", "main")
	proof, err := store.LandCheck(w.ID, LandCheckOptions{CandidateTipSHA: candidate, Record: true, Actor: "codex"}, now.Add(7*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if proof.Status != "landed" || !proof.CandidateContainedInPublication {
		t.Fatalf("published proof = %+v", proof)
	}
	afterPublish, err := store.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(afterPublish.EvidenceRefs, proof.EvidenceRef) {
		t.Fatalf("successful proof must be indexed on its WorkEnvelope: %+v", afterPublish.EvidenceRefs)
	}
	bench, err = store.Workbench(now.Add(8*time.Minute), WorkbenchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	item = findWorkbenchItem(t, bench.Open, w.ID)
	if item.LandingStatus != "ready_to_close" || !contains(item.EvidenceRefs, proof.EvidenceRef) {
		t.Fatalf("workbench must discover current delivery proof: %+v", item)
	}
	ready, err := store.DispatchClosePlan(now.Add(9*time.Minute), DispatchClosePlanOptions{Actor: "codex", WorkID: w.ID})
	if err != nil {
		t.Fatal(err)
	}
	if ready.Selected == nil || ready.ClosePlan == nil || !contains(ready.ClosePlan.ProofRefs, proof.EvidenceRef) {
		t.Fatalf("dispatch close must consume the attached delivery proof: %+v", ready)
	}
}

func activateLandingWork(t *testing.T, store *Store, workID string, now time.Time) {
	t.Helper()
	if _, err := store.Claim(workID, "codex", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(workID, AdvanceInput{To: "running", Actor: "codex"}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

type landingProofFixture struct {
	store     *Store
	repo      string
	base      string
	candidate string
	workID    string
	proof     LandCheckReport
	scratch   string
}

func newLandingProofFixture(t *testing.T, now time.Time) landingProofFixture {
	t.Helper()
	repo, base := initLandingRepo(t)
	candidate := landingFeatureCommit(t, repo)
	landingGit(t, repo, "switch", "main")
	landingGit(t, repo, "merge", "--ff-only", candidate)
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "proof content remains exact", GitDelivery: localGitDelivery(repo, base)}, now)
	if err != nil {
		t.Fatal(err)
	}
	activateLandingWork(t, store, w.ID, now)
	attachLandingCommit(t, store, w.ID, candidate, now.Add(3*time.Minute))
	proof, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main", Record: true}, now.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if proof.Status != "landed" {
		t.Fatalf("fixture proof = %+v", proof)
	}
	return landingProofFixture{store: store, repo: repo, base: base, candidate: candidate, workID: w.ID, proof: proof, scratch: t.TempDir()}
}

func attachLandingCommit(t *testing.T, store *Store, workID, sha string, now time.Time) {
	t.Helper()
	attachLandingArtifact(t, store, workID, "commit:"+sha, now)
}

func attachLandingArtifact(t *testing.T, store *Store, workID, artifact string, now time.Time) {
	t.Helper()
	w, err := store.Get(workID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(workID, AdvanceInput{To: w.State, Actor: "codex", ArtifactID: artifact}, now); err != nil {
		t.Fatal(err)
	}
}

func initLandingRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	landingGit(t, repo, "init", "-b", "main")
	landingGit(t, repo, "config", "user.email", "test@example.invalid")
	landingGit(t, repo, "config", "user.name", "Compound Test")
	writeLandingFile(t, filepath.Join(repo, "README.md"), "base\n")
	landingGit(t, repo, "add", "README.md")
	landingGit(t, repo, "commit", "-m", "base")
	return repo, landingGitOutput(t, repo, "rev-parse", "HEAD")
}

func landingFeatureCommit(t *testing.T, repo string) string {
	t.Helper()
	landingGit(t, repo, "switch", "-c", "feature")
	writeLandingFile(t, filepath.Join(repo, "feature.txt"), "feature\n")
	landingGit(t, repo, "add", "feature.txt")
	landingGit(t, repo, "commit", "-m", "feature")
	return landingGitOutput(t, repo, "rev-parse", "HEAD")
}

func landingGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func landingGitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func writeLandingFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func localGitDelivery(repo, base string) *envelope.GitDeliveryContract {
	return &envelope.GitDeliveryContract{
		TargetWorktreeRoot: repo,
		TargetLocalRef:     "refs/heads/main",
		ExpectedBaseSHA:    base,
		PublicationMode:    "local_only",
	}
}

func publishedGitDelivery(repo, base string) *envelope.GitDeliveryContract {
	return &envelope.GitDeliveryContract{
		TargetWorktreeRoot: repo,
		TargetLocalRef:     "refs/heads/main",
		ExpectedBaseSHA:    base,
		PublicationMode:    "required",
		PublicationRef:     "refs/remotes/origin/main",
	}
}
