package authority

import (
	"context"
	"os"
	"path/filepath"
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

	readOnly := map[string]bool{"rev-parse": true, "worktree": true, "status": true, "merge-base": true, "rev-list": true}
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
