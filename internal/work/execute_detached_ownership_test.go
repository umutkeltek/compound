package work

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Review repair 2026-09-19 follow-up (detached): recovery may not classify a
// reservation lost on evidence it has not made durable.
//
// The old lost path failed the envelope FIRST and then wrote the sidecar with
// the error discarded (`_ = WriteDetachedLaunch`). A failed envelope behind a
// sidecar that still reads `reserved` is the delayed-owner hole read from the
// other end: the launcher's own pre-check consults that sidecar, sees
// `reserved`, and starts a child for work that is already closed failed.
//
// The order is now revoke-durably, stamp-durably, then close — and neither
// durable step may be skipped or its error swallowed.

// reserveDetachedLaunchForOwnership claims an envelope and leaves a reserved
// sidecar with no started pid: the shape a delayed launcher leaves behind.
func reserveDetachedLaunchForOwnership(t *testing.T, s *Store, workID, launchID string) {
	t.Helper()
	seedReadyExecuteWork(t, s, workID)
	if _, err := s.Claim(workID, "claude-minimax", fixedNow); err != nil {
		t.Fatal(err)
	}
	if err := WriteDetachedLaunch(s.home, DetachedLaunch{
		WorkID:    workID,
		LaunchID:  launchID,
		Lane:      "claude-minimax",
		Actor:     "claude-minimax",
		LogPath:   t.TempDir() + "/" + workID + ".door.log",
		StartedAt: fixedNow.UTC().Format(time.RFC3339),
		Outcome:   DetachedOutcomeReserved,
	}); err != nil {
		t.Fatal(err)
	}
}

// Falsifier 6 — an undurable revoke must classify nothing. Both durable steps
// are exercised: the atomic decision itself, and the sidecar stamp that makes
// the decision readable to every other party.
func TestReconcileClassifiesNothingOnAnUndurableRevoke(t *testing.T) {
	t.Run("the decision cannot be taken", func(t *testing.T) {
		s := newStore(t)
		reserveDetachedLaunchForOwnership(t, s, "work_undurable_decision", "dl_undurable_decision")
		prevAlive := detachedProcessAlive
		detachedProcessAlive = func(int) bool { return false }
		prevCreate := detachedDecisionCreateFn
		detachedDecisionCreateFn = func(string) (*os.File, error) { return nil, os.ErrPermission }
		t.Cleanup(func() {
			detachedProcessAlive = prevAlive
			detachedDecisionCreateFn = prevCreate
		})

		verdicts := s.ReconcileDetachedLaunches(fixedNow.Add(24 * time.Hour))
		if len(verdicts) != 1 || verdicts[0].Outcome != DetachedOutcomeReserved {
			t.Fatalf("a revoke that cannot be made durable must classify nothing; got %+v", verdicts)
		}
		if !strings.Contains(verdicts[0].Detail, "could not be made durable") {
			t.Fatalf("the verdict must report the undurable revoke, got %q", verdicts[0].Detail)
		}
		w, _ := s.Get("work_undurable_decision")
		if w.State != "claimed" {
			t.Fatalf("envelope state = %q; an unclassified reservation must stay claimed", w.State)
		}
		sidecar, _ := ReadDetachedLaunch(s.home, "work_undurable_decision")
		if sidecar.Outcome != DetachedOutcomeReserved {
			t.Fatalf("sidecar outcome = %q; nothing may be stamped without a durable revoke", sidecar.Outcome)
		}
	})

	t.Run("the sidecar stamp cannot be made durable", func(t *testing.T) {
		s := newStore(t)
		reserveDetachedLaunchForOwnership(t, s, "work_undurable_stamp", "dl_undurable_stamp")
		prevAlive := detachedProcessAlive
		detachedProcessAlive = func(int) bool { return false }
		prevWrite := reconcileWriteDetachedLaunch
		reconcileWriteDetachedLaunch = func(string, DetachedLaunch) error { return os.ErrPermission }
		t.Cleanup(func() {
			detachedProcessAlive = prevAlive
			reconcileWriteDetachedLaunch = prevWrite
		})

		verdicts := s.ReconcileDetachedLaunches(fixedNow.Add(24 * time.Hour))
		if len(verdicts) != 1 || verdicts[0].Outcome != DetachedOutcomeReserved {
			t.Fatalf("an undurable sidecar stamp must classify nothing; got %+v", verdicts)
		}
		if !strings.Contains(verdicts[0].Detail, "sidecar stamp is not") {
			t.Fatalf("the verdict must report the undurable stamp, got %q", verdicts[0].Detail)
		}
		w, _ := s.Get("work_undurable_stamp")
		if w.State != "claimed" {
			t.Fatalf("envelope state = %q; the envelope must NOT be failed behind an unstamped sidecar", w.State)
		}
		// The revoke itself IS durable, and it was taken before any close was
		// attempted — that ordering is what a later tick resumes from.
		token, err := readDetachedOwnershipToken(detachedOwnershipTokenPath(s.home, "dl_undurable_stamp"))
		if err != nil {
			t.Fatalf("the revoke must be durable before the close is attempted: %v", err)
		}
		if token.Decision != DetachedDecisionRevoke {
			t.Fatalf("token decision = %q, want revoke", token.Decision)
		}
	})
}

// A legacy id-less reservation has no launch id to name a decision with, so no
// ownership guarantee is claimed for it: it keeps its pre-2026-09-19 path and
// leaves no token behind. Stated here so the guarantee is not read as covering
// sidecars it cannot cover.
func TestLegacyIdlessReservationTakesNoOwnershipDecision(t *testing.T) {
	s := newStore(t)
	seedReadyExecuteWork(t, s, "work_legacy_no_token")
	if _, err := s.Claim("work_legacy_no_token", "claude-minimax", fixedNow); err != nil {
		t.Fatal(err)
	}
	if err := WriteDetachedLaunch(s.home, DetachedLaunch{
		WorkID:    "work_legacy_no_token",
		Lane:      "claude-minimax",
		Actor:     "claude-minimax",
		LogPath:   t.TempDir() + "/work_legacy_no_token.door.log",
		StartedAt: fixedNow.UTC().Format(time.RFC3339),
		Outcome:   DetachedOutcomeReserved,
	}); err != nil {
		t.Fatal(err)
	}
	prevAlive := detachedProcessAlive
	detachedProcessAlive = func(int) bool { return false }
	prevCreate := detachedDecisionCreateFn
	detachedDecisionCreateFn = func(string) (*os.File, error) {
		t.Fatal("a legacy id-less reservation must not take an ownership decision")
		return nil, nil
	}
	t.Cleanup(func() {
		detachedProcessAlive = prevAlive
		detachedDecisionCreateFn = prevCreate
	})

	verdicts := s.ReconcileDetachedLaunches(fixedNow.Add(24 * time.Hour))
	if len(verdicts) != 1 || verdicts[0].Outcome != DetachedOutcomeDoorLost {
		t.Fatalf("a legacy expired reservation must keep classifying door_lost; got %+v", verdicts)
	}
}

// A launcher that wins the decision and then dies before cmd.Start() would
// otherwise hold its envelope claimed forever: recovery can never win that
// launch id again. DetachedStartCommitGrace is the only escape, and it must
// NOT fire while a start is still plausible.
func TestWonStartDecisionBlocksRecoveryUntilTheCommitGraceExpires(t *testing.T) {
	s := newStore(t)
	reserveDetachedLaunchForOwnership(t, s, "work_won_start", "dl_won_start")
	if decision, err := DecideDetachedLaunchOwnership(s.home, "work_won_start", "dl_won_start", DetachedDecisionStart, fixedNow); err != nil || decision != DetachedDecisionStart {
		t.Fatalf("the launcher must win an untaken decision: %q %v", decision, err)
	}
	prevAlive := detachedProcessAlive
	detachedProcessAlive = func(int) bool { return false }
	t.Cleanup(func() { detachedProcessAlive = prevAlive })

	// Past the reservation grace, inside the start-commit grace: recovery
	// observes the launcher's decision and waits.
	verdicts := s.ReconcileDetachedLaunches(fixedNow.Add(DetachedReservationGrace + time.Minute))
	if len(verdicts) != 1 || verdicts[0].Outcome != DetachedOutcomeReserved {
		t.Fatalf("recovery must not classify a launch that won the start decision; got %+v", verdicts)
	}
	if !strings.Contains(verdicts[0].Detail, "won the start decision") {
		t.Fatalf("the verdict must report the winner's decision, got %q", verdicts[0].Detail)
	}
	if w, _ := s.Get("work_won_start"); w.State != "claimed" {
		t.Fatalf("envelope state = %q; a committed launch keeps its envelope", w.State)
	}

	// Past the start-commit grace with still no started pid in the log: the
	// launcher died before it started anything, so the envelope is recovered.
	verdicts = s.ReconcileDetachedLaunches(fixedNow.Add(DetachedStartCommitGrace + time.Minute))
	if len(verdicts) != 1 || verdicts[0].Outcome != DetachedOutcomeDoorLost {
		t.Fatalf("a won decision with no started pid past the commit grace must be recoverable; got %+v", verdicts)
	}
	if !strings.Contains(verdicts[0].Detail, "logged no started pid") {
		t.Fatalf("the verdict must say why the committed launch was classified, got %q", verdicts[0].Detail)
	}
}
