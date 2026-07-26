package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"compound/internal/runattempt"
	"compound/internal/work"
)

// D1 regression guards. The sweep was always correct; the defect was that
// nothing called it unattended, so a delegate that died left its attempt
// reporting `running` forever and the executors/attention projections
// inherited that lie (55 phantom sessions, 2026-07-26).

// spawnDelegateForSweep spawns one delegate through the real CLI and returns
// the home, the spawned attempt id, and its session name.
func spawnDelegateForSweep(t *testing.T) (home, attemptID, session string, runner *delegateCLIFakeRunner) {
	t.Helper()
	allowDelegateCLIGuard(t, false)
	store, workID, homePath := claimedDelegateCLIWorkAt(t, "orchestrator:test")
	r := newDelegateCLIFakeRunner()
	out, _, code := runDelegateCLIForTest(t, "delegate", store, r, nil,
		workID, "--harness", "claude-zai", "--json")
	if code != 0 {
		t.Fatalf("spawn code=%d", code)
	}
	var spawned work.DelegateResult
	if err := json.Unmarshal([]byte(out), &spawned); err != nil || spawned.Binding == nil {
		t.Fatalf("decode spawn: %+v %v", spawned, err)
	}
	return homePath, spawned.RunAttemptID, spawned.Binding.Session, r
}

func attemptStatus(t *testing.T, home, id string) string {
	t.Helper()
	all, err := runattempt.Open(home).List()
	if err != nil {
		t.Fatal(err)
	}
	for _, ra := range all {
		if ra.ID == id {
			return string(ra.Status)
		}
	}
	t.Fatalf("attempt %s not found", id)
	return ""
}

// TestPreSpawnSweepTerminalizesAbandonedAttempt is the core D1 proof: an
// attempt whose pane is gone and which is past the staleness threshold must
// come back terminal, without any human running a sweep command.
func TestPreSpawnSweepTerminalizesAbandonedAttempt(t *testing.T) {
	home, attemptID, session, runner := spawnDelegateForSweep(t)

	if got := attemptStatus(t, home, attemptID); got == "abandoned" || got == "failed" {
		t.Fatalf("attempt was already terminal before the pane died: %s", got)
	}
	// The pane vanishes -- the exact 2026-07-26 shape. Set the status
	// explicitly rather than deleting the entry: the fake runner falls
	// back to its `live` map, so a delete still reports the session alive.
	runner.panes[session] = work.SessionStatus{Exists: false}

	var stderr bytes.Buffer
	// now is past DefaultSweepThreshold so the attempt is a candidate.
	future := time.Now().Add(runattempt.DefaultSweepThreshold + time.Hour)
	swept := sweepStaleRunAttemptsBestEffortWithRunner(home, runner, future, &stderr)

	if swept == 0 {
		t.Fatalf("pre-spawn sweep terminalized nothing; a dead delegate stays `running` forever. stderr=%q", stderr.String())
	}
	if got := attemptStatus(t, home, attemptID); got != "abandoned" {
		t.Fatalf("attempt status = %q, want abandoned", got)
	}
	if !strings.Contains(stderr.String(), "pre-spawn sweep") {
		t.Fatalf("sweep did real work but reported nothing to the operator: %q", stderr.String())
	}
}

// TestPreSpawnSweepProtectsLivePane is the safety half. Running this on every
// spawn is only acceptable because a healthy in-flight session is never
// touched; if that protection breaks, opportunistic sweeping becomes a way to
// kill your own running work.
func TestPreSpawnSweepProtectsLivePane(t *testing.T) {
	home, attemptID, session, runner := spawnDelegateForSweep(t)
	runner.panes[session] = work.SessionStatus{Exists: true, PaneDead: false}

	var stderr bytes.Buffer
	future := time.Now().Add(runattempt.DefaultSweepThreshold + time.Hour)
	sweepStaleRunAttemptsBestEffortWithRunner(home, runner, future, &stderr)

	if got := attemptStatus(t, home, attemptID); got == "abandoned" || got == "failed" {
		t.Fatalf("live delegate was terminalized (status=%q): opportunistic sweep would kill in-flight work", got)
	}
}

// TestPreSpawnSweepIsBestEffortOnBrokenHome pins the deliberate asymmetry
// with pane capture: capture fails closed (an unobservable spawn is the bug),
// but cleanup of unrelated old attempts must never block new work.
func TestPreSpawnSweepIsBestEffortOnBrokenHome(t *testing.T) {
	var stderr bytes.Buffer
	got := sweepStaleRunAttemptsBestEffortWithRunner(
		t.TempDir()+"/definitely-not-a-compound-home",
		newDelegateCLIFakeRunner(), time.Now(), &stderr)
	if got != 0 {
		t.Fatalf("swept %d attempts from a nonexistent home", got)
	}
	// Must not panic and must not os.Exit; reaching here is the assertion.
}
