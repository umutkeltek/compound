package work

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCloseRejectsTargetRefMoveBeforePreparedReferenceTransaction(t *testing.T) {
	now := time.Date(2026, 7, 14, 3, 0, 0, 0, time.UTC)
	fixture := newLandingProofFixture(t, now)
	lateTip := landingEmptyDescendant(t, fixture.repo, "late-target")
	before := snapshotLifecycleAuthority(t, fixture.store)

	realStart := startGitReferenceTransaction
	startGitReferenceTransaction = func(repo string, refs []gitReferenceExpectation) (gitReferenceTransaction, error) {
		cmd := exec.Command("git", "-C", repo, "update-ref", "refs/heads/main", lateTip)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, &testGitCommandError{args: "update-ref refs/heads/main", err: err, output: string(out)}
		}
		return realStart(repo, refs)
	}
	t.Cleanup(func() { startGitReferenceTransaction = realStart })

	_, err := fixture.store.Close(fixture.workID, CloseInput{
		Outcome:   "completed",
		MoveType:  "outward_delta",
		Actor:     "codex",
		Summary:   "target movement must abort close",
		ProofRefs: []string{fixture.proof.EvidenceRef},
	}, now.Add(10*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "prepare Git reference transaction") {
		t.Fatalf("target move immediately before prepare must fail CAS, got %v", err)
	}
	after := snapshotLifecycleAuthority(t, fixture.store)
	assertLifecycleAuthorityUnchanged(t, before, after)
}

func TestCloseRejectsPublicationRefMoveBeforePreparedReferenceTransaction(t *testing.T) {
	now := time.Date(2026, 7, 14, 3, 30, 0, 0, time.UTC)
	fixture := newPublishedLandingProofFixture(t, now)
	lateTip := landingEmptyDescendant(t, fixture.repo, "late-publication")
	before := snapshotLifecycleAuthority(t, fixture.store)

	realStart := startGitReferenceTransaction
	startGitReferenceTransaction = func(repo string, refs []gitReferenceExpectation) (gitReferenceTransaction, error) {
		cmd := exec.Command("git", "-C", repo, "update-ref", "refs/remotes/origin/main", lateTip)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, &testGitCommandError{args: "update-ref refs/remotes/origin/main", err: err, output: string(out)}
		}
		return realStart(repo, refs)
	}
	t.Cleanup(func() { startGitReferenceTransaction = realStart })

	_, err := fixture.store.Close(fixture.workID, CloseInput{
		Outcome:   "completed",
		MoveType:  "outward_delta",
		Actor:     "codex",
		Summary:   "publication movement must abort close",
		ProofRefs: []string{fixture.proof.EvidenceRef},
	}, now.Add(10*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "prepare Git reference transaction") {
		t.Fatalf("publication move immediately before prepare must fail CAS, got %v", err)
	}
	after := snapshotLifecycleAuthority(t, fixture.store)
	assertLifecycleAuthorityUnchanged(t, before, after)
}

func TestPreparedReferenceTransactionLocksRefsThroughTerminalPersistence(t *testing.T) {
	now := time.Date(2026, 7, 14, 4, 0, 0, 0, time.UTC)
	fixture := newLandingProofFixture(t, now)
	lateTip := landingEmptyDescendant(t, fixture.repo, "concurrent-target")
	prepared := make(chan struct{})
	release := make(chan struct{})

	realStart := startGitReferenceTransaction
	startGitReferenceTransaction = func(repo string, refs []gitReferenceExpectation) (gitReferenceTransaction, error) {
		tx, err := realStart(repo, refs)
		if err != nil {
			return nil, err
		}
		close(prepared)
		<-release
		return tx, nil
	}
	t.Cleanup(func() { startGitReferenceTransaction = realStart })

	type closeResult struct {
		result CloseResult
		err    error
	}
	done := make(chan closeResult, 1)
	go func() {
		result, err := fixture.store.Close(fixture.workID, CloseInput{
			Outcome:   "completed",
			MoveType:  "outward_delta",
			Actor:     "codex",
			Summary:   "refs remain fixed through close persistence",
			ProofRefs: []string{fixture.proof.EvidenceRef},
		}, now.Add(10*time.Minute))
		done <- closeResult{result: result, err: err}
	}()

	select {
	case <-prepared:
	case <-time.After(5 * time.Second):
		t.Fatal("close did not prepare Git reference transaction")
	}
	cmd := exec.Command("git", "-C", fixture.repo, "update-ref", "refs/heads/main", lateTip)
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "cannot lock ref") {
		close(release)
		t.Fatalf("lock-respecting concurrent ref mutation must fail while close is prepared: err=%v output=%s", err, out)
	}
	if got := landingGitOutput(t, fixture.repo, "rev-parse", "refs/heads/main"); got != fixture.candidate {
		close(release)
		t.Fatalf("failed concurrent mutation moved target to %s, want %s", got, fixture.candidate)
	}
	close(release)

	select {
	case closed := <-done:
		if closed.err != nil {
			t.Fatal(closed.err)
		}
		if closed.result.Work.State != "completed" {
			t.Fatalf("close result = %+v", closed.result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("close did not finish after releasing prepared transaction block")
	}
	if got := landingGitOutput(t, fixture.repo, "rev-parse", "refs/heads/main"); got != fixture.candidate {
		t.Fatalf("close committed against moved target %s, want %s", got, fixture.candidate)
	}
}

func newPublishedLandingProofFixture(t *testing.T, now time.Time) landingProofFixture {
	t.Helper()
	repo, base := initLandingRepo(t)
	remote := filepath.Join(t.TempDir(), "origin.git")
	landingGit(t, filepath.Dir(remote), "init", "--bare", remote)
	landingGit(t, repo, "remote", "add", "origin", remote)
	landingGit(t, repo, "push", "-u", "origin", "main")
	candidate := landingFeatureCommit(t, repo)
	landingGit(t, repo, "switch", "main")
	landingGit(t, repo, "merge", "--ff-only", candidate)
	landingGit(t, repo, "push", "origin", "main")

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{
		Owner:            "codex",
		SuccessPredicate: "published refs remain fixed through terminal close",
		GitDelivery:      publishedGitDelivery(repo, base),
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
		t.Fatalf("published fixture proof = %+v", proof)
	}
	return landingProofFixture{store: store, repo: repo, base: base, candidate: candidate, workID: w.ID, proof: proof, scratch: t.TempDir()}
}

func landingEmptyDescendant(t *testing.T, repo, branch string) string {
	t.Helper()
	landingGit(t, repo, "switch", "-c", branch)
	landingGit(t, repo, "commit", "--allow-empty", "-m", branch)
	tip := landingGitOutput(t, repo, "rev-parse", "HEAD")
	landingGit(t, repo, "switch", "main")
	return tip
}

type testGitCommandError struct {
	args   string
	err    error
	output string
}

func (e *testGitCommandError) Error() string {
	return e.args + ": " + e.err.Error() + ": " + strings.TrimSpace(e.output)
}
