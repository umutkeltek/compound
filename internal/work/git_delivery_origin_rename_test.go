package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Synthetic origins: a hosted repository renamed from org/a to org/b.
const (
	originRenameRemoteA = "git@example.invalid:org/a.git"
	originRenameRemoteB = "git@example.invalid:org/b.git"
)

type originRenameFixture struct {
	store            *Store
	repo             string
	base             string
	candidate        string
	workID           string
	boundFingerprint string
}

// newOriginRenameFixture binds a running Work to a repository whose origin is
// remote A, with its landed candidate attached and published on origin/main.
func newOriginRenameFixture(t *testing.T, now time.Time) originRenameFixture {
	t.Helper()
	repo, base, candidate := sharedLandingLandedRepo(t)
	landingGit(t, repo, "remote", "add", "origin", originRenameRemoteA)
	landingGit(t, repo, "update-ref", "refs/remotes/origin/main", candidate)
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{
		Owner:            "codex",
		SuccessPredicate: "delivery survives a renamed origin only through the governed migration",
		GitDelivery:      publishedGitDelivery(repo, base),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	activateLandingWork(t, store, w.ID, now)
	attachLandingCommit(t, store, w.ID, candidate, now.Add(3*time.Minute))
	return originRenameFixture{store: store, repo: repo, base: base, candidate: candidate, workID: w.ID, boundFingerprint: w.GitDelivery.RepositoryFingerprint}
}

// (1) A Work bound under origin A is refused once the origin is renamed to B,
// with today's message, and every reader names the migration as next action.
func TestOriginRenameStrandedWorkNamesTheMigration(t *testing.T) {
	now := time.Date(2026, 11, 7, 3, 0, 0, 0, time.UTC)
	f := newOriginRenameFixture(t, now)
	landingGit(t, f.repo, "remote", "set-url", "origin", originRenameRemoteB)

	_, err := f.store.LandCheck(f.workID, LandCheckOptions{CandidateTipSHA: f.candidate}, now.Add(4*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "no longer matches bound fingerprint "+f.boundFingerprint) {
		t.Fatalf("land-check after origin rename must refuse with the fingerprint message, got %v", err)
	}
	wantCommand := "compound work advance " + f.workID + " --to running"
	for _, want := range []string{"origin was renamed", wantCommand, "--publication-ref refs/remotes/origin/main", "--previous-origin"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("land-check refusal must name the migration (%q): %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "--local-only") {
		t.Errorf("migration command must keep the bound publication mode: %v", err)
	}

	report, err := f.store.Workbench(now.Add(5*time.Minute), WorkbenchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	item := findWorkbenchItem(t, report.Open, f.workID)
	if item.LandingStatus != "repository_fingerprint_changed" || !strings.Contains(item.NextAction, wantCommand) || !strings.Contains(item.NextAction, "--previous-origin") {
		t.Errorf("workbench must name the origin-rename migration: status=%q next=%q", item.LandingStatus, item.NextAction)
	}
	plan, err := f.store.DispatchPlan(now.Add(6*time.Minute), DispatchPlanOptions{Actor: "codex", WorkID: f.workID})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 1 || plan.Candidates[0].Action != "review" || len(plan.Candidates[0].Command) != 0 || !strings.Contains(plan.Candidates[0].NextAction, "--previous-origin") {
		t.Errorf("dispatch must route the stranded Work to the migration, not execution: %+v", plan.Candidates)
	}
}

// (2) The migration accepts when only the origin differs, may be run by the
// executor holding the Work, records old and new on the lifecycle event, and
// is reversible by the same door.
func TestOriginRenameMigrationRebindsOnlyTheFingerprint(t *testing.T) {
	now := time.Date(2026, 11, 7, 3, 0, 0, 0, time.UTC)
	f := newOriginRenameFixture(t, now)
	before, err := f.store.Get(f.workID)
	if err != nil {
		t.Fatal(err)
	}
	_, beforeEvents, err := f.store.Show(f.workID)
	if err != nil {
		t.Fatal(err)
	}
	landingGit(t, f.repo, "remote", "set-url", "origin", originRenameRemoteB)
	requested := publishedGitDelivery(f.repo, f.base)
	requested.RepositoryFingerprint = strings.Repeat("f", 64)

	plan, err := f.store.AdvancePlan(f.workID, now.Add(4*time.Minute), AdvancePlanOptions{Actor: "codex", To: "running", GitDelivery: requested, PreviousOrigin: originRenameRemoteA})
	if err != nil {
		t.Fatalf("plan origin migration: %v", err)
	}
	if plan.GitDelivery == nil || plan.GitDelivery.RepositoryFingerprint == f.boundFingerprint || plan.GitDelivery.RepositoryFingerprint == requested.RepositoryFingerprint {
		t.Fatalf("plan must derive the new fingerprint from the repository: %+v", plan.GitDelivery)
	}
	if !sameGitDeliveryIgnoringFingerprint(*before.GitDelivery, *plan.GitDelivery) {
		t.Fatalf("migration changed delivery authority beyond the fingerprint: before=%+v after=%+v", *before.GitDelivery, *plan.GitDelivery)
	}
	if !commandHasFlagValue(plan.AdvanceCommand, "--previous-origin", originRenameRemoteA) || !plan.Policy.Allowed {
		t.Fatalf("plan must carry the exact migration command under an allowing policy: %+v", plan)
	}
	newFingerprint := plan.GitDelivery.RepositoryFingerprint
	for _, want := range []string{f.boundFingerprint, originRenameRemoteA, newFingerprint, originRenameRemoteB} {
		if !strings.Contains(plan.ActionIntent.Preview, want) || !strings.Contains(plan.ActionIntent.RollbackPlan, originRenameRemoteB) {
			t.Fatalf("migration action must name old and new identity (%q): %+v", want, plan.ActionIntent)
		}
	}

	migrated, err := f.store.Advance(f.workID, AdvanceInput{To: "running", Actor: "codex", GitDelivery: requested, PreviousOrigin: originRenameRemoteA}, now.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("executor origin migration: %v", err)
	}
	if migrated.State != "running" || migrated.GitDelivery.RepositoryFingerprint != newFingerprint || !sameGitDeliveryIgnoringFingerprint(*before.GitDelivery, *migrated.GitDelivery) {
		t.Fatalf("migrated work = %+v", migrated)
	}
	if err := f.store.validateBoundGitDelivery(migrated); err != nil {
		t.Fatalf("migrated binding is not authoritative: %v", err)
	}
	_, events, err := f.store.Show(f.workID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != len(beforeEvents)+1 {
		t.Fatalf("migration must append exactly one lifecycle event: before=%d after=%d", len(beforeEvents), len(events))
	}
	last := events[len(events)-1]
	want := RepositoryOriginRename{PreviousFingerprint: f.boundFingerprint, PreviousOrigin: originRenameRemoteA, Fingerprint: newFingerprint, Origin: originRenameRemoteB}
	if last.OriginRename == nil || *last.OriginRename != want || last.By != "codex" || last.From != "running" || last.To != "running" {
		t.Fatalf("lifecycle event must record old and new identity: %+v rename=%+v", last, last.OriginRename)
	}
	if !strings.Contains(last.Note, "origin renamed") || !strings.Contains(last.Note, originRenameRemoteA) || !strings.Contains(last.Note, originRenameRemoteB) {
		t.Fatalf("lifecycle note must say what moved in plain words: %q", last.Note)
	}

	// Renaming back migrates back through the same door.
	landingGit(t, f.repo, "remote", "set-url", "origin", originRenameRemoteA)
	restored, err := f.store.Advance(f.workID, AdvanceInput{To: "running", Actor: "codex", GitDelivery: requested, PreviousOrigin: originRenameRemoteB}, now.Add(6*time.Minute))
	if err != nil {
		t.Fatalf("reverse origin migration: %v", err)
	}
	if restored.GitDelivery.RepositoryFingerprint != f.boundFingerprint {
		t.Fatalf("reverse migration must restore the original fingerprint: %+v", restored.GitDelivery)
	}
}

// (3) The migration refuses a different repository at the same path, a wrong
// previous origin, field drift, a lifecycle transition, and a plain rebind.
func TestOriginRenameMigrationRefusesEverythingButARename(t *testing.T) {
	now := time.Date(2026, 11, 7, 3, 0, 0, 0, time.UTC)
	refuse := func(t *testing.T, f originRenameFixture, in AdvanceInput, want ...string) {
		t.Helper()
		_, beforeEvents, err := f.store.Show(f.workID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.store.Advance(f.workID, in, now.Add(5*time.Minute))
		if err == nil {
			t.Fatal("migration must be refused")
		}
		for _, w := range want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("refusal must say %q: %v", w, err)
			}
		}
		after, afterEvents, err := f.store.Show(f.workID)
		if err != nil {
			t.Fatal(err)
		}
		if after.GitDelivery.RepositoryFingerprint != f.boundFingerprint || len(afterEvents) != len(beforeEvents) {
			t.Fatalf("refused migration mutated the work: %+v events %d -> %d", after.GitDelivery, len(beforeEvents), len(afterEvents))
		}
	}

	t.Run("different repository at the same path", func(t *testing.T) {
		f := newOriginRenameFixture(t, now)
		other := filepath.Join(t.TempDir(), "other")
		landingGit(t, filepath.Dir(other), "clone", "--no-local", f.repo, other)
		landingGit(t, other, "config", "user.email", "test@example.invalid")
		landingGit(t, other, "config", "user.name", "Compound Test")
		emptyTree := landingGitOutput(t, other, "hash-object", "-w", "-t", "tree", "/dev/null")
		otherRoot := landingGitOutput(t, other, "commit-tree", emptyTree, "-m", "unrelated lineage")
		landingGit(t, other, "replace", "--graft", f.base, otherRoot)
		landingGit(t, other, "remote", "set-url", "origin", originRenameRemoteB)
		landingGit(t, other, "update-ref", "refs/remotes/origin/main", f.candidate)
		if err := os.Rename(f.repo, f.repo+"-retired"); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(other, f.repo); err != nil {
			t.Fatal(err)
		}
		refuse(t, f, AdvanceInput{To: "running", Actor: "codex", GitDelivery: publishedGitDelivery(f.repo, f.base), PreviousOrigin: originRenameRemoteA},
			"lineage roots "+otherRoot, "not reproduce bound fingerprint "+f.boundFingerprint, "different repository", "not allowed")
	})
	t.Run("wrong previous origin", func(t *testing.T) {
		f := newOriginRenameFixture(t, now)
		landingGit(t, f.repo, "remote", "set-url", "origin", originRenameRemoteB)
		refuse(t, f, AdvanceInput{To: "running", Actor: "codex", GitDelivery: publishedGitDelivery(f.repo, f.base), PreviousOrigin: "git@example.invalid:org/c.git"},
			"not reproduce bound fingerprint", "not allowed")
	})
	t.Run("unchanged origin", func(t *testing.T) {
		f := newOriginRenameFixture(t, now)
		refuse(t, f, AdvanceInput{To: "running", Actor: "codex", GitDelivery: publishedGitDelivery(f.repo, f.base), PreviousOrigin: originRenameRemoteB}, "unchanged")
	})
	t.Run("delivery field drift", func(t *testing.T) {
		f := newOriginRenameFixture(t, now)
		landingGit(t, f.repo, "remote", "set-url", "origin", originRenameRemoteB)
		drifted := publishedGitDelivery(f.repo, f.candidate)
		refuse(t, f, AdvanceInput{To: "running", Actor: "codex", GitDelivery: drifted, PreviousOrigin: originRenameRemoteA}, "exactly match")
	})
	t.Run("lifecycle transition", func(t *testing.T) {
		f := newOriginRenameFixture(t, now)
		landingGit(t, f.repo, "remote", "set-url", "origin", originRenameRemoteB)
		refuse(t, f, AdvanceInput{To: "verifying", Actor: "codex", GitDelivery: publishedGitDelivery(f.repo, f.base), PreviousOrigin: originRenameRemoteA}, "same-state")
	})
	t.Run("previous origin without delivery flags", func(t *testing.T) {
		f := newOriginRenameFixture(t, now)
		landingGit(t, f.repo, "remote", "set-url", "origin", originRenameRemoteB)
		refuse(t, f, AdvanceInput{To: "running", Actor: "codex", PreviousOrigin: originRenameRemoteA}, "--previous-origin requires")
	})
	t.Run("plain rebind stays refused", func(t *testing.T) {
		f := newOriginRenameFixture(t, now)
		landingGit(t, f.repo, "remote", "set-url", "origin", originRenameRemoteB)
		refuse(t, f, AdvanceInput{To: "running", Actor: "operator:test", GitDelivery: publishedGitDelivery(f.repo, f.base)}, "rebinding is not allowed", "--previous-origin")
	})
	t.Run("pre-fingerprint backfill stays operator-only", func(t *testing.T) {
		store, legacy, repo, base := preFingerprintGitDeliveryFixture(t, now, "running", "codex")
		_, err := store.Advance(legacy.ID, AdvanceInput{To: legacy.State, Actor: "codex", GitDelivery: localGitDelivery(repo, base), PreviousOrigin: originRenameRemoteA}, now.Add(3*time.Minute))
		if err == nil || !strings.Contains(err.Error(), "no bound repository fingerprint") {
			t.Fatalf("an unbound fingerprint is not an origin rename: %v", err)
		}
	})
}

// (4) After the migration the Work lands normally.
func TestOriginRenameMigrationRestoresLandCheck(t *testing.T) {
	now := time.Date(2026, 11, 7, 3, 0, 0, 0, time.UTC)
	f := newOriginRenameFixture(t, now)
	landingGit(t, f.repo, "remote", "set-url", "origin", originRenameRemoteB)
	if _, err := f.store.Advance(f.workID, AdvanceInput{To: "running", Actor: "codex", GitDelivery: publishedGitDelivery(f.repo, f.base), PreviousOrigin: originRenameRemoteA}, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	completeSourceBoundProofForWork(t, f.store, f.workID, "codex", f.repo, now.Add(5*time.Minute))
	report, err := f.store.LandCheck(f.workID, LandCheckOptions{CandidateTipSHA: f.candidate, Record: true}, now.Add(6*time.Minute))
	if err != nil {
		t.Fatalf("land-check after migration: %v", err)
	}
	if report.Status != "landed" || report.RepositoryFingerprint == f.boundFingerprint || report.EvidenceRef == "" {
		t.Fatalf("migrated work must land under its new fingerprint: %+v", report)
	}
}
