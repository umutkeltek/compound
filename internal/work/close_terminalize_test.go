package work

import (
	"testing"
	"time"

	"compound/internal/runattempt"
)

// Falsifier for work_114bc68834fb: `work close` terminalizes the work's bound
// non-terminal RunAttempts by outcome. Mapping (settled design):
//
//	completed  -> succeeded
//	failed     -> failed
//	cancelled  -> abandoned
//
// Already-terminal attempts (succeeded/failed/abandoned/superseded) MUST be
// skipped — a re-close carrying the same outcome returns the existing terminal
// proof pair (idempotency, see TestCloseIsIdempotentOnSameOutcome) and must
// NOT rewrite the attempt status.

func closeTerminalizeWork(t *testing.T, s *Store, owner string, at time.Time) (string, []string) {
	t.Helper()
	w, err := s.Create(CreateInput{Owner: owner, SuccessPredicate: "close terminalizes run attempts"}, at)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(w.ID, "codex", at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "running", Actor: "codex"}, at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "verifying", VerificationID: "ver_" + w.ID}, at.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	rattIDs := append([]string{}, claimed.RunAttemptIDs...)
	return w.ID, rattIDs
}

// closeTerminalizeWorkClaimed keeps the work at "claimed" so the cancel path
// (verifying does NOT allow a legal transition to cancelled — see work.transitions).
func closeTerminalizeWorkClaimed(t *testing.T, s *Store, owner string, at time.Time) (string, []string) {
	t.Helper()
	w, err := s.Create(CreateInput{Owner: owner, SuccessPredicate: "close terminalizes run attempts (claimed)"}, at)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(w.ID, "codex", at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	rattIDs := append([]string{}, claimed.RunAttemptIDs...)
	return w.ID, rattIDs
}

// TestCloseCompletedTerminalizesRunAttemptSucceeded: closing with outcome=completed
// must terminalize every bound non-terminal RunAttempt as "succeeded".
func TestCloseCompletedTerminalizesRunAttemptSucceeded(t *testing.T) {
	s := newStore(t)
	id, rattIDs := closeTerminalizeWork(t, s, "close-completed-terminalizes", fixedNow)
	if len(rattIDs) == 0 {
		t.Fatal("setup: claim must mint a RunAttempt")
	}
	if _, err := s.Close(id, CloseInput{
		Outcome:   "completed",
		MoveType:  "outward_delta",
		Summary:   "completed close",
		Changed:   true,
		ProofRefs: []string{"check:close-completed"},
	}, fixedNow.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, rid := range rattIDs {
		ra, err := runattempt.Open(s.home).Get(rid)
		if err != nil {
			t.Fatalf("RunAttempt %s lookup after close: %v", rid, err)
		}
		if ra.Status != runattempt.StatusSucceeded {
			t.Fatalf("RunAttempt %s status = %q, want %q (completed close must terminalize as succeeded)", rid, ra.Status, runattempt.StatusSucceeded)
		}
		if ra.CompletedAt.IsZero() {
			t.Fatalf("RunAttempt %s has zero CompletedAt after terminalization", rid)
		}
	}
}

// TestCloseFailedTerminalizesRunAttemptFailed: closing with outcome=failed must
// terminalize every bound non-terminal RunAttempt as "failed".
func TestCloseFailedTerminalizesRunAttemptFailed(t *testing.T) {
	s := newStore(t)
	id, rattIDs := closeTerminalizeWork(t, s, "close-failed-terminalizes", fixedNow)
	if len(rattIDs) == 0 {
		t.Fatal("setup: claim must mint a RunAttempt")
	}
	if _, err := s.Close(id, CloseInput{
		Outcome:  "failed",
		MoveType: "outward_delta",
		Summary:  "failed close",
	}, fixedNow.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, rid := range rattIDs {
		ra, err := runattempt.Open(s.home).Get(rid)
		if err != nil {
			t.Fatalf("RunAttempt %s lookup after close: %v", rid, err)
		}
		if ra.Status != runattempt.StatusFailed {
			t.Fatalf("RunAttempt %s status = %q, want %q (failed close must terminalize as failed)", rid, ra.Status, runattempt.StatusFailed)
		}
		if ra.CompletedAt.IsZero() {
			t.Fatalf("RunAttempt %s has zero CompletedAt after terminalization", rid)
		}
	}
}

// TestCloseCancelledTerminalizesRunAttemptAbandoned: closing with outcome=cancelled
// must terminalize every bound non-terminal RunAttempt as "abandoned".
func TestCloseCancelledTerminalizesRunAttemptAbandoned(t *testing.T) {
	s := newStore(t)
	id, rattIDs := closeTerminalizeWorkClaimed(t, s, "close-cancelled-terminalizes", fixedNow)
	if len(rattIDs) == 0 {
		t.Fatal("setup: claim must mint a RunAttempt")
	}
	if _, err := s.Close(id, CloseInput{
		Outcome:  "cancelled",
		MoveType: "audit_only",
		Summary:  "cancelled close",
	}, fixedNow.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, rid := range rattIDs {
		ra, err := runattempt.Open(s.home).Get(rid)
		if err != nil {
			t.Fatalf("RunAttempt %s lookup after close: %v", rid, err)
		}
		if ra.Status != runattempt.StatusAbandoned {
			t.Fatalf("RunAttempt %s status = %q, want %q (cancelled close must terminalize as abandoned)", rid, ra.Status, runattempt.StatusAbandoned)
		}
		if ra.CompletedAt.IsZero() {
			t.Fatalf("RunAttempt %s has zero CompletedAt after terminalization", rid)
		}
	}
}

// TestCloseSkipsAlreadyTerminalRunAttempts: a RunAttempt that is ALREADY terminal
// (succeeded/failed/abandoned/superseded) when the close runs must NOT be
// rewritten. Close must skip it.
func TestCloseSkipsAlreadyTerminalRunAttempts(t *testing.T) {
	s := newStore(t)
	id, rattIDs := closeTerminalizeWork(t, s, "close-skips-terminal", fixedNow)
	if len(rattIDs) == 0 {
		t.Fatal("setup: claim must mint a RunAttempt")
	}
	// Plant an already-terminal RunAttempt on the work's RunAttemptIDs and on disk.
	preTerminalID := runattempt.MintDeterministic("close-skips-terminal", fixedNow)
	preTerminal := runattempt.RunAttempt{
		SchemaVersion: runattempt.SchemaVersion,
		ID:            preTerminalID,
		WorkID:        id,
		Actor:         "codex",
		Harness:       runattempt.HarnessPlaceholder,
		Origin:        runattempt.OriginClaimReservation,
		Status:        runattempt.StatusSucceeded,
		CreatedAt:     fixedNow.UTC(),
		CompletedAt:   fixedNow.Add(time.Hour).UTC(),
	}
	if _, err := runattempt.Open(s.home).MintWithID(preTerminalID, id, "codex", runattempt.HarnessPlaceholder, fixedNow); err != nil {
		t.Fatalf("plant pre-terminal RunAttempt: %v", err)
	}
	if _, err := runattempt.Open(s.home).Complete(preTerminalID, runattempt.StatusSucceeded, fixedNow.Add(time.Hour)); err != nil {
		t.Fatalf("plant pre-terminal RunAttempt terminal status: %v", err)
	}
	// Attach it to the work envelope (close will see it in w.RunAttemptIDs).
	w, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	w.RunAttemptIDs = append(w.RunAttemptIDs, preTerminalID)
	if err := s.writeJSON(s.workPath(id), &w); err != nil {
		t.Fatalf("attach pre-terminal RunAttempt to work: %v", err)
	}

	if _, err := s.Close(id, CloseInput{
		Outcome:   "completed",
		MoveType:  "outward_delta",
		Summary:   "close skips pre-terminal",
		Changed:   true,
		ProofRefs: []string{"check:close-skip"},
	}, fixedNow.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	// The pre-terminal attempt must still be succeeded and CompletedAt must be unchanged.
	after, err := runattempt.Open(s.home).Get(preTerminalID)
	if err != nil {
		t.Fatalf("pre-terminal RunAttempt lookup after close: %v", err)
	}
	if after.Status != runattempt.StatusSucceeded {
		t.Fatalf("pre-terminal RunAttempt status = %q, want %q (close must skip already-terminal)", after.Status, runattempt.StatusSucceeded)
	}
	if !after.CompletedAt.Equal(preTerminal.CompletedAt) {
		t.Fatalf("pre-terminal RunAttempt CompletedAt moved: was %v, now %v", preTerminal.CompletedAt, after.CompletedAt)
	}

	// The claim-minted RunAttempt must still have been terminalized.
	for _, rid := range rattIDs {
		ra, err := runattempt.Open(s.home).Get(rid)
		if err != nil {
			t.Fatalf("RunAttempt %s lookup after close: %v", rid, err)
		}
		if ra.Status != runattempt.StatusSucceeded {
			t.Fatalf("RunAttempt %s status = %q, want %q (completed close must terminalize as succeeded)", rid, ra.Status, runattempt.StatusSucceeded)
		}
	}
}

// TestCloseSkipRunAttemptTerminalizationLeavesAttemptMutable: a caller that owns
// its own RunAttempt lifecycle (binds close-produced proof refs to the attempt
// and completes it with its honest status AFTER the close, e.g. internal/panel
// apply's fail-soft flow) must be able to opt out. With
// SkipRunAttemptTerminalization=true the bound non-terminal attempt stays
// non-terminal after the close, so SetProofRefs + Complete still land. This is
// the falsifier for the 2026-08-07 regression where close pre-terminalized the
// attempt (completed→succeeded) and panel's post-close Complete refused.
func TestCloseSkipRunAttemptTerminalizationLeavesAttemptMutable(t *testing.T) {
	s := newStore(t)
	id, rattIDs := closeTerminalizeWork(t, s, "close-skip-runattempt-termination", fixedNow)
	if len(rattIDs) == 0 {
		t.Fatal("setup: claim must mint a RunAttempt")
	}
	if _, err := s.Close(id, CloseInput{
		Outcome:                       "completed",
		MoveType:                      "outward_delta",
		Summary:                       "close with caller-owned RunAttempt",
		Changed:                       true,
		ProofRefs:                     []string{"check:close-skip-runattempt-termination"},
		SkipRunAttemptTerminalization: true,
	}, fixedNow.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, rid := range rattIDs {
		ra, err := runattempt.Open(s.home).Get(rid)
		if err != nil {
			t.Fatalf("RunAttempt %s lookup after close: %v", rid, err)
		}
		if runattempt.IsTerminal(ra.Status) {
			t.Fatalf("RunAttempt %s status = %q, want non-terminal (opt-out must leave the attempt mutable for the caller's own completion)", rid, ra.Status)
		}
		// The caller's post-close proof-ref binding + completion must still land
		// (this is what internal/panel apply does after its fail-soft close).
		if _, err := runattempt.Open(s.home).SetProofRefs(rid, []string{"proof:post-close"}); err != nil {
			t.Fatalf("post-close SetProofRefs on opted-out RunAttempt %s: %v", rid, err)
		}
		if _, err := runattempt.Open(s.home).Complete(rid, runattempt.StatusFailed, fixedNow.Add(6*time.Minute)); err != nil {
			t.Fatalf("post-close Complete on opted-out RunAttempt %s: %v", rid, err)
		}
	}
}
