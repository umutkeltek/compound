package authority

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// notLiveWindow is a runtime-relative future offset so fixture files (created
// "now" by git) read as far older than the activity window — no fixed fixture
// date, so no staleness time bomb.
func inventoryFutureNow() time.Time { return time.Now().Add(48 * time.Hour) }

// W1 + W2 + W3 + W4 + main class: one ground-truth build over a fixture that
// would misclassify under a wrong implementation. Crucially W1 asserts the
// stalled worktree is NOT landed_clean (the data-loss guard).
func TestBuildWorktreeInventoryClassifiesGroundTruth(t *testing.T) {
	fix := newWorktreeInventoryFixture(t)
	live := inventoryFakeLiveness{active: map[string]bool{fix.laneLive: true}}

	inv, err := BuildWorktreeInventory(context.Background(), WorktreeInventoryDependencies{
		Git: ExecWorktreeRetirementGit{}, Liveness: live,
	}, fix.repo, WorktreeInventoryOptions{Now: inventoryFutureNow()})
	if err != nil {
		t.Fatalf("build inventory: %v", err)
	}
	if inv.SchemaVersion != WorktreeInventorySchemaVersion || inv.MainTip != fix.mainTip {
		t.Fatalf("envelope header wrong: schema=%q main=%q", inv.SchemaVersion, inv.MainTip)
	}

	byBranch := map[string]WorktreeInventoryRow{}
	var mainRow, probeRow *WorktreeInventoryRow
	for i := range inv.Rows {
		row := inv.Rows[i]
		if row.IsMain {
			mainRow = &inv.Rows[i]
			continue
		}
		if row.Detached {
			probeRow = &inv.Rows[i]
			continue
		}
		byBranch[row.Branch] = row
	}

	if mainRow == nil || mainRow.Class != WorktreeClassMain {
		t.Fatalf("main row = %+v", mainRow)
	}
	// W4: detached HEAD -> probe, even when dirty.
	if probeRow == nil || probeRow.Class != WorktreeClassProbe {
		t.Fatalf("probe row = %+v", probeRow)
	}
	if probeRow.DirtyFiles == 0 {
		t.Fatalf("probe fixture should be dirty to prove class survives dirtiness: %+v", probeRow)
	}

	// W1: unique commit + dirty, dead, no recent activity -> stalled_unlanded,
	// and explicitly NOT landed_clean.
	stalled := byBranch["lane-stalled"]
	if stalled.Class != WorktreeClassStalledUnlanded {
		t.Fatalf("lane-stalled class = %q, want stalled_unlanded; row=%+v", stalled.Class, stalled)
	}
	if stalled.Class == WorktreeClassLandedClean {
		t.Fatal("data-loss guard breached: unlanded worktree read as landed_clean")
	}
	if stalled.AheadCommits < 1 || stalled.DirtyFiles < 1 {
		t.Fatalf("lane-stalled should hold unique commit + dirty file: %+v", stalled)
	}

	// W2: contained + clean + not live -> landed_clean.
	if got := byBranch["lane-landed"].Class; got != WorktreeClassLandedClean {
		t.Fatalf("lane-landed class = %q, want landed_clean; row=%+v", got, byBranch["lane-landed"])
	}

	// W3: identical containment/cleanliness as W2 but liveness observes open
	// handles -> live (liveness overrides containment).
	liveRow := byBranch["lane-live"]
	if liveRow.Class != WorktreeClassLive {
		t.Fatalf("lane-live class = %q, want live; row=%+v", liveRow.Class, liveRow)
	}
	if liveRow.Class == WorktreeClassLandedClean {
		t.Fatal("live lane read as landed_clean — liveness must override containment")
	}

	// Counts reflect the classes.
	if inv.Counts.Main != 1 || inv.Counts.Probe != 1 || inv.Counts.StalledUnlanded != 1 ||
		inv.Counts.LandedClean != 1 || inv.Counts.Live != 1 {
		t.Fatalf("counts = %+v", inv.Counts)
	}
}

// The activity path (not lsof) must also promote a worktree to live: a clean,
// contained worktree whose files were just touched reads live within the window.
func TestBuildWorktreeInventoryRecentActivityClassifiesLive(t *testing.T) {
	fix := newWorktreeInventoryFixture(t)
	// Dead liveness everywhere: only activity mtime can make a row live here.
	inv, err := BuildWorktreeInventory(context.Background(), WorktreeInventoryDependencies{
		Git: ExecWorktreeRetirementGit{}, Liveness: inventoryFakeLiveness{},
	}, fix.repo, WorktreeInventoryOptions{Now: time.Now(), ActivityWindow: 60 * time.Minute})
	if err != nil {
		t.Fatalf("build inventory: %v", err)
	}
	landed := inventoryRowByBranch(t, inv, "lane-landed")
	if landed.Class != WorktreeClassLive {
		t.Fatalf("freshly created clean worktree should read live via activity, got %q; row=%+v", landed.Class, landed)
	}
	if !landed.ActivityRecent {
		t.Fatalf("activity_recent should be set: %+v", landed)
	}
}

// F2 clean-main-quiet: the primary checkout is still observed through the
// shared liveness seam, but a clean, unused main row carries no live signal and
// keeps its leave-alone action.
func TestBuildWorktreeInventoryF2CleanMainStaysQuiet(t *testing.T) {
	fix := newWorktreeInventoryFixture(t)
	now := time.Now().UTC().Truncate(time.Second).Add(2 * time.Second)

	inv, err := BuildWorktreeInventory(context.Background(), WorktreeInventoryDependencies{
		Git: ExecWorktreeRetirementGit{}, Liveness: inventoryFakeLiveness{},
	}, fix.repo, WorktreeInventoryOptions{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	main := inventoryMainRow(t, inv)
	if main.Live || main.ActivityRecent || main.DirtyFiles != 0 {
		t.Fatalf("clean main must stay quiet: %+v", main)
	}
	if main.Liveness.State != WorktreeLivenessUnused {
		t.Fatalf("main liveness was not observed through the shared seam: %+v", main.Liveness)
	}
	if main.NextAction != "leave alone — primary checkout" {
		t.Fatalf("clean main next_action = %q", main.NextAction)
	}
}

// F3 commit-recency-age: a clean linked worktree with an artificially old
// .git entry still reports its recent HEAD commit as the newest age evidence.
func TestBuildWorktreeInventoryF3CleanWorktreeAgeUsesHeadCommitTime(t *testing.T) {
	fix := newWorktreeInventoryFixture(t)
	now := time.Now().UTC().Truncate(time.Second).Add(2 * time.Second)
	old := now.Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(fix.laneLanded, ".git"), old, old); err != nil {
		t.Fatal(err)
	}

	inv, err := BuildWorktreeInventory(context.Background(), WorktreeInventoryDependencies{
		Git: ExecWorktreeRetirementGit{}, Liveness: inventoryFakeLiveness{},
	}, fix.repo, WorktreeInventoryOptions{Now: now, ActivityWindow: 60 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	landed := inventoryRowByBranch(t, inv, "lane-landed")
	if landed.AgeSeconds == nil || *landed.AgeSeconds >= int64((60*time.Minute).Seconds()) {
		t.Fatalf("clean worktree age must reflect recent HEAD commit, not old .git entry: %+v", landed)
	}
	if !landed.ActivityRecent {
		t.Fatalf("recent HEAD commit must be activity evidence: %+v", landed)
	}
}

// W5: read-only proof. A recording fake wraps real git; the full build may only
// invoke read verbs, and a `worktree` call may only be `worktree list`.
func TestBuildWorktreeInventoryIsReadOnly(t *testing.T) {
	fix := newWorktreeInventoryFixture(t)
	rec := &inventoryRecordingGit{delegate: ExecWorktreeRetirementGit{}}

	beforeRefs := retirementGitOutput(t, fix.repo, "show-ref")
	beforeWorktrees := retirementGitOutput(t, fix.repo, "worktree", "list", "--porcelain")

	if _, err := BuildWorktreeInventory(context.Background(), WorktreeInventoryDependencies{
		Git: rec, Liveness: inventoryFakeLiveness{},
	}, fix.repo, WorktreeInventoryOptions{Now: inventoryFutureNow()}); err != nil {
		t.Fatalf("build inventory: %v", err)
	}

	readOnly := map[string]bool{"rev-parse": true, "worktree": true, "status": true, "show": true, "merge-base": true, "rev-list": true}
	mutatingTokens := map[string]bool{
		"add": true, "commit": true, "rm": true, "update-ref": true, "checkout": true,
		"switch": true, "reset": true, "branch": true, "stash": true, "clean": true,
		"gc": true, "prune": true, "apply": true, "merge": true, "rebase": true, "push": true,
	}
	if len(rec.calls) == 0 {
		t.Fatal("recording git captured zero calls")
	}
	for _, call := range rec.calls {
		verb := inventoryGitVerb(call)
		if !readOnly[verb] {
			t.Fatalf("non-whitelisted git verb %q in call %v", verb, call)
		}
		if verb == "worktree" && !inventoryArgsContain(call, "list") {
			t.Fatalf("worktree call is not a read-only list: %v", call)
		}
		for _, arg := range call {
			if mutatingTokens[arg] {
				t.Fatalf("mutating git token %q appeared in call %v", arg, call)
			}
		}
	}

	if after := retirementGitOutput(t, fix.repo, "show-ref"); after != beforeRefs {
		t.Fatalf("inventory mutated refs:\nbefore:\n%s\nafter:\n%s", beforeRefs, after)
	}
	if after := retirementGitOutput(t, fix.repo, "worktree", "list", "--porcelain"); after != beforeWorktrees {
		t.Fatalf("inventory mutated worktree registry")
	}
}

// W7: an unreadable fact for one worktree isolates that row as unknown (naming
// the failing fact) while every other row still renders.
func TestBuildWorktreeInventoryUnreadableFactIsolatedAsUnknown(t *testing.T) {
	fix := newWorktreeInventoryFixture(t)
	rec := &inventoryRecordingGit{delegate: ExecWorktreeRetirementGit{}, failRevListFor: fix.stalledHead}

	inv, err := BuildWorktreeInventory(context.Background(), WorktreeInventoryDependencies{
		Git: rec, Liveness: inventoryFakeLiveness{},
	}, fix.repo, WorktreeInventoryOptions{Now: inventoryFutureNow()})
	if err != nil {
		t.Fatalf("build inventory: %v", err)
	}

	bad := inventoryRowByBranch(t, inv, "lane-stalled")
	if bad.Class != WorktreeClassUnknown {
		t.Fatalf("row with failing rev-list should be unknown, got %q; row=%+v", bad.Class, bad)
	}
	if !strings.Contains(bad.Reason, "rev-list") {
		t.Fatalf("unknown reason should name the failing fact (rev-list): %q", bad.Reason)
	}
	// The other clean worktree still classifies normally — one bad worktree does
	// not kill the projection.
	if got := inventoryRowByBranch(t, inv, "lane-landed").Class; got != WorktreeClassLandedClean {
		t.Fatalf("sibling worktree class = %q, want landed_clean", got)
	}
}

// I1 (identity exact-evidence): a row whose branch ref matches a nonterminal
// envelope's worktree-branch: artifact gains work_id+state; a row with no
// artifact stays honestly bare, and a single match notes no ambiguity.
func TestJoinWorktreeEnvelopeIdentityExactEvidence(t *testing.T) {
	rows := []WorktreeInventoryRow{
		{BranchRef: "refs/heads/lane-a", Branch: "lane-a", Reason: "r-a"},
		{BranchRef: "refs/heads/lane-b", Branch: "lane-b", Reason: "r-b"},
	}
	joinWorktreeEnvelopeIdentity(rows, []WorktreeEnvelopeArtifact{
		{WorkID: "work_a", State: "running", BranchRef: "refs/heads/lane-a", CreatedAt: "2026-07-17T10:00:00Z"},
	})
	if rows[0].WorkID != "work_a" || rows[0].WorkState != "running" {
		t.Fatalf("lane-a should carry exact identity: %+v", rows[0])
	}
	if strings.Contains(rows[0].Reason, "ambiguous") {
		t.Fatalf("single match must not note ambiguity: %q", rows[0].Reason)
	}
	if rows[1].WorkID != "" || rows[1].WorkState != "" {
		t.Fatalf("lane-b has no artifact and must stay honestly bare: %+v", rows[1])
	}
}

// I2 (dangling tolerated): a worktree-branch: ref matching no row — including
// the known dangling refs/heads/(pending-dispatch) on work_2f9b20dc6661 — is
// silently ignored: no error, no annotation, no row touched.
func TestJoinWorktreeEnvelopeIdentityIgnoresDanglingRefs(t *testing.T) {
	rows := []WorktreeInventoryRow{{BranchRef: "refs/heads/lane-a", Reason: "r-a"}}
	before := rows[0]
	joinWorktreeEnvelopeIdentity(rows, []WorktreeEnvelopeArtifact{
		{WorkID: "work_dangling", State: "running", BranchRef: "refs/heads/(pending-dispatch)", CreatedAt: "2026-07-17T10:00:00Z"},
		{WorkID: "work_other", State: "running", BranchRef: "refs/heads/lane-z", CreatedAt: "2026-07-17T10:00:00Z"},
	})
	if !reflect.DeepEqual(rows[0], before) {
		t.Fatalf("non-matching refs (incl. the known dangling pending-dispatch) must not touch any row: %+v", rows[0])
	}
}

// I3a (main-root ambiguity): a shared canonical delivery root is exact routing
// evidence, but it is not enough to infer which of several running envelopes is
// actively editing the primary checkout. Singular identity therefore stays
// empty and the reason says that no active editor was inferred.
func TestJoinWorktreeEnvelopeIdentityMainRootAmbiguityDoesNotInferEditor(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	rows := []WorktreeInventoryRow{{
		Path: root, BranchRef: "refs/heads/main", IsMain: true, Reason: "primary checkout",
	}}
	joinWorktreeEnvelopeIdentity(rows, []WorktreeEnvelopeArtifact{
		{WorkID: "work_old", State: "running", Holder: "codex:old", TargetWorktreeRoot: root, CreatedAt: "2026-07-17T09:00:00Z"},
		{WorkID: "work_new", State: "running", Holder: "codex:new", TargetWorktreeRoot: root, CreatedAt: "2026-07-17T11:00:00Z"},
	})
	if rows[0].WorkID != "" || rows[0].WorkState != "" || rows[0].WorkHolder != "" {
		t.Fatalf("ambiguous main-root evidence must not infer singular work identity: %+v", rows[0])
	}
	if !strings.Contains(rows[0].Reason, "active editor was not inferred") {
		t.Fatalf("reason must state that the active editor was not inferred: %q", rows[0].Reason)
	}
}

// I3b (branch-artifact ambiguity): when two nonterminal envelopes name one branch, the newest
// (CreatedAt, then WorkID) wins and the row's reason notes the ambiguity naming
// the winner.
func TestJoinWorktreeEnvelopeIdentityNewestWinsOnAmbiguity(t *testing.T) {
	rows := []WorktreeInventoryRow{{BranchRef: "refs/heads/lane-a", Reason: "r-a"}}
	joinWorktreeEnvelopeIdentity(rows, []WorktreeEnvelopeArtifact{
		{WorkID: "work_old", State: "claimed", BranchRef: "refs/heads/lane-a", CreatedAt: "2026-07-17T09:00:00Z"},
		{WorkID: "work_new", State: "running", BranchRef: "refs/heads/lane-a", CreatedAt: "2026-07-17T11:00:00Z"},
	})
	if rows[0].WorkID != "work_new" || rows[0].WorkState != "running" {
		t.Fatalf("newest nonterminal envelope must win: %+v", rows[0])
	}
	if !strings.Contains(rows[0].Reason, "ambiguous") || !strings.Contains(rows[0].Reason, "work_new") {
		t.Fatalf("ambiguity must be noted in the reason naming the winner: %q", rows[0].Reason)
	}
}

// Keep annotation is a pure annotation: a kept row KEEPS its ground-truth class
// (W-regression: a kept stalled_unlanded stays stalled_unlanded) and only its
// next_action defers to the operator decision. Unkept rows are untouched.
func TestAnnotateWorktreeKeepsDefersNextActionKeepsClass(t *testing.T) {
	rows := []WorktreeInventoryRow{
		{BranchRef: "refs/heads/lane-a", Class: WorktreeClassStalledUnlanded, NextAction: "unlanded work — check lane liveness before salvage or keep"},
		{BranchRef: "refs/heads/lane-b", Class: WorktreeClassLandedClean, NextAction: "retire via compound authority worktree-retire (governed)"},
	}
	kept := annotateWorktreeKeeps(rows, map[string]WorktreeKeepAnnotation{
		"refs/heads/lane-a": {DecisionID: "keep-1", Revisit: "auth refactor lands"},
	})
	if kept != 1 {
		t.Fatalf("kept count = %d, want 1", kept)
	}
	if rows[0].Class != WorktreeClassStalledUnlanded {
		t.Fatalf("kept stalled_unlanded must stay stalled_unlanded: %+v", rows[0])
	}
	if !rows[0].Kept || rows[0].KeepDecisionID != "keep-1" || rows[0].KeepRevisit != "auth refactor lands" {
		t.Fatalf("kept row annotation wrong: %+v", rows[0])
	}
	if !strings.Contains(rows[0].NextAction, "kept by operator decision keep-1") || !strings.Contains(rows[0].NextAction, "revisit: auth refactor lands") {
		t.Fatalf("kept next_action wrong: %q", rows[0].NextAction)
	}
	if rows[1].Kept || rows[1].NextAction == "" || strings.Contains(rows[1].NextAction, "kept by operator") {
		t.Fatalf("unkept row must be untouched: %+v", rows[1])
	}
}

// End-to-end through BuildWorktreeInventory over a real fixture: keeps annotate
// (class unchanged, counts.kept incremented, next_action deferred), identity
// joins the exact envelope, the dangling ref is ignored, and readable keeps omit
// the keeps_readable field.
func TestBuildWorktreeInventoryAppliesLedgerJoins(t *testing.T) {
	fix := newWorktreeInventoryFixture(t)
	readable := true
	inv, err := BuildWorktreeInventory(context.Background(), WorktreeInventoryDependencies{
		Git: ExecWorktreeRetirementGit{}, Liveness: inventoryFakeLiveness{active: map[string]bool{fix.laneLive: true}},
	}, fix.repo, WorktreeInventoryOptions{
		Now: inventoryFutureNow(),
		Keeps: map[string]WorktreeKeepAnnotation{
			"refs/heads/lane-stalled": {DecisionID: "keep-stalled", Revisit: "salvage decided"},
		},
		KeepsReadable: &readable,
		Envelopes: []WorktreeEnvelopeArtifact{
			{WorkID: "work_live", State: "running", BranchRef: "refs/heads/lane-live", CreatedAt: "2026-07-17T10:00:00Z"},
			{WorkID: "work_dangling", State: "running", BranchRef: "refs/heads/(pending-dispatch)", CreatedAt: "2026-07-17T10:00:00Z"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	stalled := inventoryRowByBranch(t, inv, "lane-stalled")
	if stalled.Class != WorktreeClassStalledUnlanded {
		t.Fatalf("kept annotation must not change class, got %q: %+v", stalled.Class, stalled)
	}
	if !stalled.Kept || stalled.KeepDecisionID != "keep-stalled" || !strings.Contains(stalled.NextAction, "kept by operator decision keep-stalled") {
		t.Fatalf("lane-stalled should be annotated kept: %+v", stalled)
	}
	if inv.Counts.Kept != 1 {
		t.Fatalf("counts.kept = %d, want 1", inv.Counts.Kept)
	}
	live := inventoryRowByBranch(t, inv, "lane-live")
	if live.WorkID != "work_live" || live.WorkState != "running" {
		t.Fatalf("lane-live should carry exact identity: %+v", live)
	}
	landed := inventoryRowByBranch(t, inv, "lane-landed")
	if landed.WorkID != "" || landed.Kept {
		t.Fatalf("lane-landed has no envelope and no keep — must be bare: %+v", landed)
	}
	if inv.KeepsReadable != nil {
		t.Fatalf("readable keeps must omit keeps_readable, got %v", *inv.KeepsReadable)
	}
}

// K3 projection: unreadable keeps surface keeps_readable:false and annotate
// nothing — honest, not guessed.
func TestBuildWorktreeInventoryUnreadableKeepsFailClosed(t *testing.T) {
	fix := newWorktreeInventoryFixture(t)
	unreadable := false
	inv, err := BuildWorktreeInventory(context.Background(), WorktreeInventoryDependencies{
		Git: ExecWorktreeRetirementGit{}, Liveness: inventoryFakeLiveness{},
	}, fix.repo, WorktreeInventoryOptions{
		Now:           inventoryFutureNow(),
		Keeps:         map[string]WorktreeKeepAnnotation{"refs/heads/lane-landed": {DecisionID: "keep-x", Revisit: "x"}},
		KeepsReadable: &unreadable,
	})
	if err != nil {
		t.Fatal(err)
	}
	if inv.KeepsReadable == nil || *inv.KeepsReadable {
		t.Fatalf("unreadable keeps must surface keeps_readable:false: %v", inv.KeepsReadable)
	}
	if inv.Counts.Kept != 0 {
		t.Fatalf("unreadable keeps must annotate nothing: kept=%d", inv.Counts.Kept)
	}
	if inventoryRowByBranch(t, inv, "lane-landed").Kept {
		t.Fatalf("unreadable keeps must not annotate any row")
	}
}

// --- fixtures & fakes ---

type worktreeInventoryFixture struct {
	repo        string
	mainTip     string
	laneStalled string
	laneLanded  string
	laneLive    string
	probe       string
	stalledHead string
}

func newWorktreeInventoryFixture(t *testing.T) worktreeInventoryFixture {
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
	mainTip := retirementGitOutput(t, repo, "rev-parse", "refs/heads/main")

	// lane-stalled: one unique commit + a dirty file (would misclassify as
	// landed_clean under a wrong implementation).
	laneStalled := filepath.Join(root, "wt-stalled")
	retirementRunGit(t, repo, "worktree", "add", "-b", "lane-stalled", laneStalled, "main")
	if err := os.WriteFile(filepath.Join(laneStalled, "unique.txt"), []byte("unique\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	retirementRunGit(t, laneStalled, "add", "unique.txt")
	retirementRunGit(t, laneStalled, "commit", "-m", "unlanded work")
	stalledHead := retirementGitOutput(t, laneStalled, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(laneStalled, "wip.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// lane-landed: branch at main, clean -> contained + clean.
	laneLanded := filepath.Join(root, "wt-landed")
	retirementRunGit(t, repo, "worktree", "add", "-b", "lane-landed", laneLanded, "main")

	// lane-live: identical to lane-landed; only liveness will differ.
	laneLive := filepath.Join(root, "wt-live")
	retirementRunGit(t, repo, "worktree", "add", "-b", "lane-live", laneLive, "main")

	// probe: detached HEAD, dirtied.
	probe := filepath.Join(root, "wt-probe")
	retirementRunGit(t, repo, "worktree", "add", "--detach", probe, "main")
	if err := os.WriteFile(filepath.Join(probe, "scratch.txt"), []byte("scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	return worktreeInventoryFixture{
		repo:        evalInventoryPath(t, repo),
		mainTip:     mainTip,
		laneStalled: evalInventoryPath(t, laneStalled),
		laneLanded:  evalInventoryPath(t, laneLanded),
		laneLive:    evalInventoryPath(t, laneLive),
		probe:       evalInventoryPath(t, probe),
		stalledHead: stalledHead,
	}
}

func evalInventoryPath(t *testing.T, path string) string {
	t.Helper()
	if c, err := filepath.EvalSymlinks(path); err == nil {
		return c
	}
	return path
}

func inventoryRowByBranch(t *testing.T, inv WorktreeInventory, branch string) WorktreeInventoryRow {
	t.Helper()
	for _, row := range inv.Rows {
		if row.Branch == branch {
			return row
		}
	}
	t.Fatalf("no row for branch %q", branch)
	return WorktreeInventoryRow{}
}

func inventoryMainRow(t *testing.T, inv WorktreeInventory) WorktreeInventoryRow {
	t.Helper()
	for _, row := range inv.Rows {
		if row.IsMain {
			return row
		}
	}
	t.Fatal("no main row")
	return WorktreeInventoryRow{}
}

type inventoryFakeLiveness struct {
	active map[string]bool
}

func (f inventoryFakeLiveness) Observe(_ context.Context, path string) WorktreeLivenessObservation {
	key := path
	if c, err := filepath.EvalSymlinks(path); err == nil {
		key = c
	}
	if f.active[key] {
		return WorktreeLivenessObservation{State: WorktreeLivenessActive, Evidence: "fake: open handles"}
	}
	return WorktreeLivenessObservation{State: WorktreeLivenessUnused, Evidence: "fake: no handles"}
}

type inventoryRecordingGit struct {
	delegate       WorktreeRetirementGit
	mu             sync.Mutex
	calls          [][]string
	failRevListFor string
}

func (g *inventoryRecordingGit) Run(ctx context.Context, repo string, args ...string) (string, error) {
	g.mu.Lock()
	g.calls = append(g.calls, append([]string{}, args...))
	g.mu.Unlock()
	if g.failRevListFor != "" && len(args) > 0 && args[0] == "rev-list" {
		for _, a := range args {
			if strings.Contains(a, g.failRevListFor) {
				return "", &WorktreeRetirementCommandError{
					Argv: append([]string{"git", "rev-list"}, args...), ExitCode: 128, Output: "fatal: bad revision (injected)",
				}
			}
		}
	}
	return g.delegate.Run(ctx, repo, args...)
}

func inventoryGitVerb(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

func inventoryArgsContain(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
