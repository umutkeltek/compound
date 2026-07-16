package work

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

	// Unrelated WIP in the target worktree must NOT fabricate a blocked status:
	// the candidate simply is not on main yet (ready_to_land), and CleanTarget
	// still honestly reports the whole-tree dirt.
	writeLandingFile(t, filepath.Join(repo, "dirty.txt"), "uncommitted\n")
	tolerated, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main"}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if tolerated.Status != "ready_to_land" || tolerated.CleanTarget {
		t.Fatalf("unrelated dirt must not gate an untouched landing: %+v", tolerated)
	}

	// A real uncommitted change to a file the envelope OWNS still fails closed.
	writeLandingFile(t, filepath.Join(repo, "feature.txt"), "conflicting local edit\n")
	report, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main"}, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "dirty_target" || report.CleanTarget {
		t.Fatalf("expected dirty_target for owned-path dirt, got %+v", report)
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
	bench, err := store.Workbench(now.Add(8*time.Minute), WorkbenchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	terminal := findWorkbenchItem(t, bench.Recent, w.ID)
	if terminal.NextAction != "terminal; mine receipt and transition if useful" || terminal.LandingStatus == "ready_to_close" {
		t.Fatalf("terminal delivery work must not be offered for close again: %+v", terminal)
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
	// An uncommitted change to a file the envelope OWNS invalidates the recorded
	// landing proof live: close must refuse rather than land over a conflicting
	// local edit to the work's own files.
	writeLandingFile(t, filepath.Join(repo, "feature.txt"), "conflicting local edit after proof\n")
	plan, err := store.ClosePlan(w.ID, now.Add(4*time.Minute), ClosePlanOptions{
		Actor:    "codex",
		Outcome:  "completed",
		MoveType: "outward_delta",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != "not_closeable" || len(plan.CloseCommand) != 0 || !strings.Contains(plan.NoAction, "dirty_target") {
		t.Fatalf("close plan must live-revalidate recorded landing proof: %+v", plan)
	}
	if _, err := store.Close(w.ID, CloseInput{Outcome: "completed", MoveType: "outward_delta", Actor: "codex", Summary: "must recheck", ProofRefs: []string{proof.EvidenceRef}}, now.Add(4*time.Minute)); err == nil || !strings.Contains(err.Error(), "dirty_target") {
		t.Fatalf("close with stale proof err = %v", err)
	}
}

// TestLandCheckReportsTruthfulContainmentUnderUnrelatedDirt is the core falsifier
// for the land-check containment lie: a commit provably on the target branch must
// report candidate_contained_in_target=true even when the target worktree carries
// unrelated preserved WIP -- and even when a real owned-path conflict blocks the
// status, the containment field reports ancestry, never cleanliness.
func TestLandCheckReportsTruthfulContainmentUnderUnrelatedDirt(t *testing.T) {
	now := time.Date(2026, 7, 16, 20, 0, 0, 0, time.UTC)
	repo, base := initLandingRepo(t)
	candidate := landingFeatureCommit(t, repo)
	landingGit(t, repo, "switch", "main")
	landingGit(t, repo, "merge", "--ff-only", candidate)

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "containment never lies under unrelated dirt", GitDelivery: localGitDelivery(repo, base)}, now)
	if err != nil {
		t.Fatal(err)
	}
	attachLandingCommit(t, store, w.ID, candidate, now.Add(30*time.Second))

	// Preserved unrelated WIP: the always-dirty repo / shared-checkout case.
	writeLandingFile(t, filepath.Join(repo, "unrelated-wip.txt"), "neighbor lane WIP\n")

	landed, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main"}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if landed.Status != "landed" {
		t.Fatalf("unrelated dirt must not block a landed commit: %+v", landed)
	}
	if !landed.CandidateContainedInTarget || !landed.CandidateContainedInPublication {
		t.Fatalf("containment fields lied under unrelated dirt: %+v", landed)
	}
	if landed.CleanTarget {
		t.Fatalf("CleanTarget must honestly report the whole-tree dirt: %+v", landed)
	}

	// A real owned-path conflict blocks the status, but containment stays truthful.
	writeLandingFile(t, filepath.Join(repo, "feature.txt"), "conflicting local edit\n")
	blocked, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main"}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Status != "dirty_target" {
		t.Fatalf("owned-path dirt must fail closed: %+v", blocked)
	}
	if !blocked.CandidateContainedInTarget {
		t.Fatalf("blocked status still fabricated containment=false: %+v", blocked)
	}
}

// TestCompletedCodeWorkClosesOverPreservedUnrelatedDirt is the falsifier for the
// shared-checkout hostage class: a published, landed envelope must close even when
// a neighbor lane's live unrelated WIP dirties the shared checkout after the proof
// was recorded, because close live-revalidates land-check.
func TestCompletedCodeWorkClosesOverPreservedUnrelatedDirt(t *testing.T) {
	now := time.Date(2026, 7, 16, 20, 0, 0, 0, time.UTC)
	repo, base := initLandingRepo(t)
	candidate := landingFeatureCommit(t, repo)
	landingGit(t, repo, "switch", "main")
	landingGit(t, repo, "merge", "--ff-only", candidate)

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "published close is not hostage to a neighbor's WIP", GitDelivery: localGitDelivery(repo, base)}, now)
	if err != nil {
		t.Fatal(err)
	}
	activateLandingWork(t, store, w.ID, now)
	attachLandingCommit(t, store, w.ID, candidate, now.Add(150*time.Second))
	proof, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main", Record: true}, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if proof.Status != "landed" {
		t.Fatalf("fixture proof = %+v", proof)
	}

	// A neighbor lane's live, unrelated WIP dirties the shared checkout after the
	// proof was recorded. Close live-revalidates -- and must still close.
	writeLandingFile(t, filepath.Join(repo, "neighbor-wip.sh"), "#!/bin/sh\necho neighbor\n")

	plan, err := store.ClosePlan(w.ID, now.Add(4*time.Minute), ClosePlanOptions{Actor: "codex", Outcome: "completed", MoveType: "outward_delta"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status == "not_closeable" || strings.Contains(plan.NoAction, "dirty_target") {
		t.Fatalf("unrelated neighbor WIP must not hold a published close hostage: %+v", plan)
	}
	closed, err := store.Close(w.ID, CloseInput{Outcome: "completed", MoveType: "outward_delta", Actor: "codex", Summary: "landed; neighbor WIP preserved", ProofRefs: []string{proof.EvidenceRef}}, now.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("close over unrelated dirt: %v", err)
	}
	if closed.Work.State != "completed" {
		t.Fatalf("close result = %+v", closed)
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
		{name: "repository fingerprint", mutate: func(_ *landingProofFixture, p *LandCheckReport) { p.RepositoryFingerprint = strings.Repeat("a", 64) }},
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

func TestLegacyLandCheckV03RequiresFreshCheckWithoutTamperAccusation(t *testing.T) {
	// Frozen output from the v0.3 sealer before repository_fingerprint entered
	// LandCheckReport. Its digest is valid for that historical payload shape.
	const legacyJSON = `{"schema_version":"compound.work_land_check.v0.3","id":"landcheck_305a66dfcdec","generated_at":"2026-07-13T22:00:00Z","work_id":"work_legacy_landcheck","status":"landed","repo_path":"/repo/product","candidate_tip_sha":"1111111111111111111111111111111111111111","candidate_artifact_ref":"commit:1111111111111111111111111111111111111111","attached_candidate_tip_sha":"1111111111111111111111111111111111111111","expected_base_sha":"0000000000000000000000000000000000000000","target_ref":"main","target_branch_ref":"refs/heads/main","target_sha":"1111111111111111111111111111111111111111","publication_mode":"local_only","candidate_exists":true,"expected_base_exists":true,"clean_target":true,"target_branch_worktree":true,"expected_base_ancestor_of_candidate":true,"target_ancestor_of_candidate":true,"candidate_contained_in_target":true,"candidate_contained_in_publication":true,"content_sha256":"305a66dfcdecdedab0e88c8b48143412b0ad58d8f95e17dc75c8f21c20658c11","recorded":true,"evidence_ref":"land-check:landcheck_305a66dfcdec","proof_path":"/compound/home/work/land-checks/landcheck_305a66dfcdec.json"}`
	var legacy LandCheckReport
	if err := json.Unmarshal([]byte(legacyJSON), &legacy); err != nil {
		t.Fatal(err)
	}
	w := envelope.WorkEnvelope{
		Header: newHeader(envelope.SVWork, legacy.WorkID, time.Date(2026, 7, 13, 21, 0, 0, 0, time.UTC), "", "", "private", "tier1"),
		Owner:  "codex", State: "verifying", SuccessPredicate: "fresh repository identity proof is required",
		GitDelivery: &envelope.GitDeliveryContract{
			TargetWorktreeRoot: "/repo/product", RepositoryFingerprint: strings.Repeat("a", 64), TargetLocalRef: "refs/heads/main",
			ExpectedBaseSHA: strings.Repeat("0", 40), PublicationMode: "local_only",
		},
		ArtifactIDs: []string{"commit:" + strings.Repeat("1", 40)},
	}
	err := validateRecordedLandCheck(w, legacy.EvidenceRef, legacy.ID, legacy.ProofPath, legacy)
	if err == nil {
		t.Fatal("legacy v0.3 land-check unexpectedly satisfied current close proof")
	}
	message := err.Error()
	if !strings.Contains(message, "legacy") || !strings.Contains(message, "fresh") || !strings.Contains(message, "v0.4") {
		t.Fatalf("legacy land-check classification = %v", err)
	}
	if strings.Contains(message, "content digest") || strings.Contains(message, "modified after recording") || strings.Contains(message, "tamper") {
		t.Fatalf("valid historical v0.3 proof was falsely accused of tampering: %v", err)
	}
}

func TestRepositoryFingerprintLandCheckUsesV04Schema(t *testing.T) {
	fixture := newLandingProofFixture(t, time.Date(2026, 7, 14, 6, 0, 0, 0, time.UTC))
	if fixture.proof.SchemaVersion != "compound.work_land_check.v0.4" {
		t.Fatalf("repository fingerprint changed sealed land-check content without a schema bump: %s", fixture.proof.SchemaVersion)
	}
	if fixture.proof.RepositoryFingerprint == "" {
		t.Fatal("v0.4 land-check did not seal repository fingerprint")
	}
}

func TestLandCheckV04FieldSetRequiresSchemaBump(t *testing.T) {
	if LandCheckSchemaVersion != "compound.work_land_check.v0.4" {
		t.Fatalf("update the sealed field-set fixture for new schema %s", LandCheckSchemaVersion)
	}
	typeOfReport := reflect.TypeOf(LandCheckReport{})
	fields := make([]string, 0, typeOfReport.NumField())
	for i := 0; i < typeOfReport.NumField(); i++ {
		name := strings.Split(typeOfReport.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			fields = append(fields, name)
		}
	}
	const v04Fields = "schema_version,id,generated_at,work_id,status,repo_path,candidate_tip_sha,candidate_artifact_ref,attached_candidate_tip_sha,expected_base_sha,repository_fingerprint,target_ref,target_branch_ref,expected_main_sha,target_sha,publication_mode,publication_ref,publication_sha,candidate_exists,expected_base_exists,clean_target,target_branch_worktree,expected_base_ancestor_of_candidate,target_ancestor_of_candidate,candidate_contained_in_target,candidate_contained_in_publication,next_action,content_sha256,recorded,evidence_ref,proof_path"
	if got := strings.Join(fields, ","); got != v04Fields {
		t.Fatalf("LandCheckReport field set changed under sealed v0.4; bump schema and add a previous-version fixture:\n got: %s\nwant: %s", got, v04Fields)
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

func TestCompletedCommitProofWithoutGitDeliveryContractFailsClosed(t *testing.T) {
	now := time.Date(2026, 7, 13, 22, 0, 0, 0, time.UTC)
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "close proof cannot hide source-controlled work"}, now)
	if err != nil {
		t.Fatal(err)
	}
	activateLandingWork(t, store, w.ID, now)

	if _, err := store.Close(w.ID, CloseInput{
		Outcome:   "completed",
		MoveType:  "outward_delta",
		Actor:     "codex",
		Summary:   "must fail without delivery authority",
		ProofRefs: []string{"commit:" + strings.Repeat("a", 40)},
	}, now.Add(4*time.Minute)); err == nil || !strings.Contains(err.Error(), "git_delivery") {
		t.Fatalf("commit proof without git_delivery contract should fail closed, got %v", err)
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

func TestLandCheckRejectsSamePathRepositoryReplacement(t *testing.T) {
	now := time.Date(2026, 7, 14, 2, 0, 0, 0, time.UTC)
	repo, base := initLandingRepo(t)
	landingGit(t, repo, "remote", "add", "origin", "https://source.example.invalid/acme/product.git")
	candidate := landingFeatureCommit(t, repo)
	landingGit(t, repo, "switch", "main")
	landingGit(t, repo, "merge", "--ff-only", candidate)

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{
		Owner:            "codex",
		SuccessPredicate: "repository identity remains bound at the target path",
		GitDelivery:      localGitDelivery(repo, base),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	activateLandingWork(t, store, w.ID, now)
	attachLandingCommit(t, store, w.ID, candidate, now.Add(3*time.Minute))
	proof, err := store.LandCheck(w.ID, LandCheckOptions{CandidateTipSHA: candidate, Record: true}, now.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if proof.Status != "landed" {
		t.Fatalf("initial repository proof = %+v", proof)
	}

	replacementParent := t.TempDir()
	replacement := filepath.Join(replacementParent, "replacement")
	landingGit(t, replacementParent, "clone", "--no-local", repo, replacement)
	landingGit(t, replacement, "remote", "set-url", "origin", "https://other.example.invalid/acme/product.git")
	retired := repo + "-retired"
	if err := os.Rename(repo, retired); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, repo); err != nil {
		t.Fatal(err)
	}

	if _, err := store.LandCheck(w.ID, LandCheckOptions{CandidateTipSHA: candidate}, now.Add(5*time.Minute)); err == nil || !strings.Contains(err.Error(), "repository fingerprint") {
		t.Fatalf("same-path replacement with another origin must fail repository identity, got %v", err)
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
