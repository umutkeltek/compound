package work

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A repository on a blocked volume never answers a Git read. Before the
// projection session bounded its reads, one such repository froze the whole
// Workbench: every row waited, and every console page that reads the board
// timed out with nothing. These tests pin the bounded behavior: the blocked
// repository's rows are unknown, every other row is exactly what it would be
// without it, and the governed land-check still reads the repository fresh.

// installBlockingGit puts a git wrapper first on PATH. It counts and delays
// only the reads whose arguments name blockedRoot; every other read runs the
// real git at once. FIXTURE_GIT_BLOCK_SECONDS sets the delay.
func installBlockingGit(t *testing.T, blockedRoot string) string {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	counter := filepath.Join(dir, "blocked-reads")
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"  case \"$a\" in\n" +
		"    '" + blockedRoot + "'|'" + blockedRoot + "'/*)\n" +
		"      echo read >> '" + counter + "'\n" +
		"      sleep \"${FIXTURE_GIT_BLOCK_SECONDS:-5}\"\n" +
		"      break ;;\n" +
		"  esac\n" +
		"done\n" +
		"exec '" + realGit + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return counter
}

func blockedReadCount(t *testing.T, counter string) int {
	t.Helper()
	body, err := os.ReadFile(counter)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(body), "read\n")
}

func TestWorkbenchDeliveryBoundsBlockedRepositoryWithoutTouchingHealthyRows(t *testing.T) {
	now := time.Date(2026, 11, 17, 21, 0, 0, 0, time.UTC)
	healthyRepo, healthyBase, healthyTip := sharedLandingReadyRepo(t)
	blockedRepo, blockedBase, blockedTip := sharedLandingReadyRepo(t)
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	healthy := landingDeliveryWork(t, store, localGitDelivery(healthyRepo, healthyBase), "healthy repository row", healthyTip, now)
	blockedA := landingDeliveryWork(t, store, localGitDelivery(blockedRepo, blockedBase), "blocked repository row a", blockedTip, now)
	blockedB := landingDeliveryWork(t, store, localGitDelivery(blockedRepo, blockedBase), "blocked repository row b", blockedTip, now)

	// The healthy row's verdict with no blocked repository in the request.
	var want WorkbenchItem
	var wantReport WorkbenchReport
	store.applyWorkbenchGitDeliveryWithFacts(&want, healthy, &wantReport, now.Add(5*time.Minute), newLandingGitFacts())
	if want.LandingStatus == "" || want.LandingStatus == "delivery_unknown" {
		t.Fatalf("healthy baseline = %q %q", want.LandingStatus, want.NextAction)
	}

	blockedRoot, err := filepath.EvalSymlinks(blockedRepo)
	if err != nil {
		t.Fatal(err)
	}
	counter := installBlockingGit(t, blockedRoot)
	t.Setenv("FIXTURE_GIT_BLOCK_SECONDS", "30")

	facts := newLandingGitFacts()
	if facts.readTimeout != landingProjectionReadTimeout || landingProjectionReadTimeout <= 0 {
		t.Fatalf("projection session bound = %s, want %s", facts.readTimeout, landingProjectionReadTimeout)
	}
	// Generous beside a healthy read under load, short beside the hang.
	facts.readTimeout = time.Second
	var report WorkbenchReport
	items := map[string]*WorkbenchItem{}
	started := time.Now()
	for _, w := range []string{blockedA.ID, healthy.ID, blockedB.ID} {
		current, err := store.Get(w)
		if err != nil {
			t.Fatal(err)
		}
		item := &WorkbenchItem{WorkID: current.ID, State: current.State, ArtifactIDs: current.ArtifactIDs}
		store.applyWorkbenchGitDeliveryWithFacts(item, current, &report, now.Add(5*time.Minute), facts)
		items[w] = item
	}
	elapsed := time.Since(started)

	// One bound per blocked repository, not one per row or per read: the
	// unbounded board waited 30s per read here, about fifteen reads per row.
	if elapsed > 10*time.Second {
		t.Fatalf("board with one blocked repository took %s; want about one %s bound", elapsed, facts.readTimeout)
	}
	if got := blockedReadCount(t, counter); got != 1 {
		t.Fatalf("blocked repository reads started = %d, want exactly 1 (later reads must short-circuit)", got)
	}
	for _, id := range []string{blockedA.ID, blockedB.ID} {
		item := items[id]
		if item.LandingStatus != "delivery_unknown" {
			t.Fatalf("blocked row %s = %q %q, want delivery_unknown", id, item.LandingStatus, item.NextAction)
		}
		if !strings.Contains(item.NextAction, "unknown, not failed") || !strings.Contains(item.NextAction, id) {
			t.Fatalf("blocked row %s next action = %q", id, item.NextAction)
		}
		if plan := dispatchClosePlanCandidate(*item, "codex", "completed", "outward_delta"); plan.Action != "await_proof" || plan.Command != nil {
			t.Fatalf("unknown delivery must not plan a close: %+v", plan)
		}
	}
	got := items[healthy.ID]
	if got.LandingStatus != want.LandingStatus || got.NextAction != want.NextAction {
		t.Fatalf("healthy row changed beside a blocked repository: got %q %q, want %q %q", got.LandingStatus, got.NextAction, want.LandingStatus, want.NextAction)
	}
	unknownLines := 0
	for _, line := range report.Integrity {
		if strings.Contains(line, "Git delivery unknown") {
			unknownLines++
		}
		if strings.HasPrefix(line, healthy.ID+":") {
			t.Fatalf("healthy row reported an integrity problem: %q", line)
		}
	}
	if unknownLines != 2 {
		t.Fatalf("integrity = %q, want one unknown line per blocked row", report.Integrity)
	}

	// The unknown mark lives only in that request's session. The governed
	// land-check (no session, no bound) reads the repository fresh once it
	// answers and returns its real verdict, never "unknown".
	t.Setenv("FIXTURE_GIT_BLOCK_SECONDS", "0")
	governed, err := store.LandCheck(blockedA.ID, LandCheckOptions{CandidateTipSHA: blockedTip}, now.Add(6*time.Minute))
	if err != nil {
		t.Fatalf("governed land-check on a slow repository: %v", err)
	}
	if governed.Status != "ready_to_land" && governed.Status != "missing_source_bound_proof" {
		t.Fatalf("governed land-check status = %q (%s)", governed.Status, governed.NextAction)
	}
}

func TestBoundedLandingReadLeavesUnboundedSessionsUnchanged(t *testing.T) {
	var nilFacts *landingGitFacts
	value, err := boundedLandingRead(nilFacts, "/repo.invalid", func() (string, error) {
		time.Sleep(50 * time.Millisecond)
		return "answered", nil
	})
	if err != nil || value != "answered" {
		t.Fatalf("nil session read = %q %v", value, err)
	}
	unbounded := newLandingGitFacts()
	unbounded.readTimeout = 0
	value, err = boundedLandingRead(unbounded, "/repo.invalid", func() (string, error) {
		time.Sleep(50 * time.Millisecond)
		return "answered", nil
	})
	if err != nil || value != "answered" || len(unbounded.unavailable) != 0 {
		t.Fatalf("unbounded session read = %q %v %v", value, err, unbounded.unavailable)
	}

	bounded := newLandingGitFacts()
	bounded.readTimeout = 20 * time.Millisecond
	release := make(chan struct{})
	defer close(release)
	_, err = boundedLandingRead(bounded, "/slow.invalid", func() (string, error) {
		<-release
		return "late", nil
	})
	var unavailable *landingRepositoryUnavailableError
	if err == nil || !strings.Contains(err.Error(), "/slow.invalid") {
		t.Fatalf("bounded slow read err = %v", err)
	}
	if !errors.As(err, &unavailable) {
		t.Fatalf("bounded slow read err type = %T", err)
	}
	called := false
	if _, err := boundedLandingRead(bounded, "/slow.invalid", func() (string, error) {
		called = true
		return "", nil
	}); err == nil || called {
		t.Fatalf("second read of an unavailable repository must short-circuit: err=%v called=%v", err, called)
	}
	if value, err := boundedLandingRead(bounded, "/fast.invalid", func() (string, error) { return "fast", nil }); err != nil || value != "fast" {
		t.Fatalf("other repository read = %q %v", value, err)
	}
}
