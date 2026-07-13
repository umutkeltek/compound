package work

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
			ExpectedBaseSHA:  base,
		}, now)
		if err != nil {
			t.Fatal(err)
		}

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
		w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "stale candidate is explicit", ExpectedBaseSHA: base}, now)
		if err != nil {
			t.Fatal(err)
		}
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
	w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "target remains exact", ExpectedBaseSHA: base}, now)
	if err != nil {
		t.Fatal(err)
	}

	report, err := store.LandCheck(w.ID, LandCheckOptions{
		RepoPath:        repo,
		CandidateTipSHA: candidate,
		TargetRef:       "main",
		ExpectedMainSHA: strings.Repeat("f", 40),
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "main_moved" {
		t.Fatalf("expected main_moved, got %+v", report)
	}

	writeLandingFile(t, filepath.Join(repo, "dirty.txt"), "uncommitted\n")
	report, err = store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main"}, now.Add(2*time.Minute))
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
	w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "close only after containment", ExpectedBaseSHA: base}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(w.ID, "codex", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(w.ID, AdvanceInput{To: "running", Actor: "codex"}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Close(w.ID, CloseInput{Outcome: "completed", MoveType: "code_delta", Actor: "codex", Summary: "not landed"}, now.Add(3*time.Minute)); err == nil || !strings.Contains(err.Error(), "land-check") {
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
	if _, err := store.Close(w.ID, CloseInput{Outcome: "completed", MoveType: "code_delta", Actor: "codex", Summary: "still not landed", ProofRefs: []string{ready.EvidenceRef}}, now.Add(5*time.Minute)); err == nil || !strings.Contains(err.Error(), "not landed") {
		t.Fatalf("ready-only proof close err = %v", err)
	}

	landingGit(t, repo, "merge", "--ff-only", candidate)
	landed, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main", Record: true}, now.Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	closed, err := store.Close(w.ID, CloseInput{Outcome: "completed", MoveType: "code_delta", Actor: "codex", Summary: "candidate is on main", ProofRefs: []string{landed.EvidenceRef}}, now.Add(7*time.Minute))
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
	w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "proof stays live", ExpectedBaseSHA: base}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(w.ID, "codex", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(w.ID, AdvanceInput{To: "running", Actor: "codex"}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	proof, err := store.LandCheck(w.ID, LandCheckOptions{RepoPath: repo, CandidateTipSHA: candidate, TargetRef: "main", Record: true}, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	writeLandingFile(t, filepath.Join(repo, "late-dirty.txt"), "dirty after proof\n")
	if _, err := store.Close(w.ID, CloseInput{Outcome: "completed", MoveType: "code_delta", Actor: "codex", Summary: "must recheck", ProofRefs: []string{proof.EvidenceRef}}, now.Add(4*time.Minute)); err == nil || !strings.Contains(err.Error(), "dirty_target") {
		t.Fatalf("close with stale proof err = %v", err)
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
