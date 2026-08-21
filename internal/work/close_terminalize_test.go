package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/runattempt"
)

func attachTerminalCloseHistoryAttempt(
	t *testing.T,
	s *Store,
	workID string,
	predecessorID string,
	status string,
	commitLaunch bool,
	at time.Time,
) runattempt.RunAttempt {
	t.Helper()
	attempts := runattempt.Open(s.home)
	ra, err := attempts.Mint(workID, "codex", "claude", at)
	if err != nil {
		t.Fatal(err)
	}
	if ra, err = attempts.SetOrigin(ra.ID, runattempt.OriginHarnessExecution); err != nil {
		t.Fatal(err)
	}
	if predecessorID != "" {
		if ra, err = attempts.SetPredecessor(ra.ID, predecessorID); err != nil {
			t.Fatal(err)
		}
	}
	if commitLaunch {
		if ra, err = attempts.CommitLaunch(ra.ID, at.Add(500*time.Millisecond), func() error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Advance(workID, AdvanceInput{
		To:            "verifying",
		Actor:         "codex",
		RunAttemptIDs: []string{ra.ID},
	}, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	ra, err = attempts.Complete(ra.ID, status, at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return ra
}

func runAttemptBytesForCloseTest(t *testing.T, home, id string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(home, "run_attempts", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestRunAttemptHasExecutedRequiresObservedBoundary(t *testing.T) {
	heartbeat := fixedNow.Add(time.Minute)
	completed := fixedNow.Add(2 * time.Minute)
	prepared := fixedNow.Add(30 * time.Second)
	committed := fixedNow.Add(time.Minute)
	tests := []struct {
		name string
		ra   runattempt.RunAttempt
		want bool
	}{
		{
			name: "origin only",
			ra:   runattempt.RunAttempt{Origin: runattempt.OriginHarnessExecution, CreatedAt: fixedNow},
		},
		{
			name: "prepared launch ignores heartbeat",
			ra: runattempt.RunAttempt{
				Origin:          runattempt.OriginHarnessExecution,
				CreatedAt:       fixedNow,
				LastHeartbeatAt: &heartbeat,
				Launch:          &runattempt.LaunchLifecycle{PreparedAt: &prepared},
			},
		},
		{
			name: "committed launch",
			ra: runattempt.RunAttempt{
				Origin:    runattempt.OriginHarnessExecution,
				CreatedAt: fixedNow,
				Launch:    &runattempt.LaunchLifecycle{PreparedAt: &prepared, CommittedAt: &committed},
			},
			want: true,
		},
		{
			name: "post-mint heartbeat without launch lifecycle",
			ra: runattempt.RunAttempt{
				Origin:          runattempt.OriginHarnessExecution,
				CreatedAt:       fixedNow,
				CompletedAt:     completed,
				LastHeartbeatAt: &heartbeat,
			},
			want: true,
		},
		{
			name: "heartbeat after completion",
			ra: runattempt.RunAttempt{
				Origin:          runattempt.OriginHarnessExecution,
				CreatedAt:       fixedNow,
				CompletedAt:     heartbeat,
				LastHeartbeatAt: &completed,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runAttemptHasExecuted(tt.ra); got != tt.want {
				t.Fatalf("runAttemptHasExecuted() = %v, want %v", got, tt.want)
			}
		})
	}
}

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
	// Terminalize the claim-minted attempt, which already has exact claim
	// action/policy authority. A free-floating body is not valid close proof.
	preTerminalID := rattIDs[0]
	preTerminal, err := runattempt.Open(s.home).Complete(preTerminalID, runattempt.StatusSucceeded, fixedNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("plant pre-terminal RunAttempt terminal status: %v", err)
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
}

func TestCloseRejectsTerminalWorkCloseAttemptWithStaleProofPair(t *testing.T) {
	s := newStore(t)
	workID, runAttemptIDs := closeTerminalizeWork(t, s, "close-rejects-stale-work-close-proof", fixedNow)
	if len(runAttemptIDs) != 1 {
		t.Fatalf("claim attempts = %v, want one reservation", runAttemptIDs)
	}
	if _, err := runattempt.Open(s.home).CompleteForWorkClose(
		runAttemptIDs[0],
		runattempt.StatusSucceeded,
		"rec_stale",
		"tr_stale",
		fixedNow.Add(4*time.Minute),
	); err != nil {
		t.Fatal(err)
	}

	_, err := s.Close(workID, CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "stale work-close ownership must not certify a new close",
		Changed:   true,
		ProofRefs: []string{"check:stale-work-close-proof"},
	}, fixedNow.Add(5*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "work.close proof pair") {
		t.Fatalf("Close error = %v, want stale work.close proof pair refusal", err)
	}
	work, getErr := s.Get(workID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if work.State != "verifying" {
		t.Fatalf("stale work.close proof refusal changed work state = %q, want verifying", work.State)
	}
}

func TestCloseCompletedAcceptsImmutableTerminalRetryHistory(t *testing.T) {
	s := newStore(t)
	workID, claimAttemptIDs := closeTerminalizeWork(t, s, "close-retry-history", fixedNow)
	if len(claimAttemptIDs) != 1 {
		t.Fatalf("claim attempts = %v, want one reservation", claimAttemptIDs)
	}
	attempts := runattempt.Open(s.home)
	claimAttempt, err := attempts.CompleteWithReason(
		claimAttemptIDs[0],
		runattempt.StatusAbandoned,
		"historical claim lane ended before retry execution",
		fixedNow.Add(4*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	failed := attachTerminalCloseHistoryAttempt(
		t, s, workID, "", runattempt.StatusFailed, true, fixedNow.Add(5*time.Minute),
	)
	succeeded := attachTerminalCloseHistoryAttempt(
		t, s, workID, failed.ID, runattempt.StatusSucceeded, true, fixedNow.Add(6*time.Minute),
	)

	before := map[string][]byte{
		claimAttempt.ID: runAttemptBytesForCloseTest(t, s.home, claimAttempt.ID),
		failed.ID:       runAttemptBytesForCloseTest(t, s.home, failed.ID),
		succeeded.ID:    runAttemptBytesForCloseTest(t, s.home, succeeded.ID),
	}
	closed, err := s.Close(workID, CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "a successful retry completed the work",
		Changed:   true,
		ProofRefs: []string{"check:terminal-retry-history"},
	}, fixedNow.Add(7*time.Minute))
	if err != nil {
		t.Fatalf("close rejected valid terminal retry history: %v", err)
	}
	if closed.Work.State != "completed" {
		t.Fatalf("closed work state = %q, want completed", closed.Work.State)
	}
	for id, want := range before {
		if got := runAttemptBytesForCloseTest(t, s.home, id); string(got) != string(want) {
			t.Fatalf("close rewrote immutable terminal retry history %s", id)
		}
	}
	report, err := s.ProofChainDoctor(fixedNow.Add(8*time.Minute), ProofChainDoctorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts.RunAttemptOutcomeMismatch != 0 || proofChainFindingContains(report, "run_attempt_outcome_mismatch") {
		t.Fatalf("doctor rejected valid terminal retry history: counts=%+v findings=%+v", report.Counts, report.Findings)
	}
}

func TestCloseCompletedRejectsUnretriedTerminalFailure(t *testing.T) {
	s := newStore(t)
	workID, _ := closeTerminalizeWork(t, s, "close-unretried-failure", fixedNow)
	failed := attachTerminalCloseHistoryAttempt(
		t, s, workID, "", runattempt.StatusFailed, true, fixedNow.Add(5*time.Minute),
	)
	before := runAttemptBytesForCloseTest(t, s.home, failed.ID)

	if _, err := s.Close(workID, CloseInput{
		Outcome:  "completed",
		MoveType: "control_plane_repair",
		Summary:  "an unrelated failed execution cannot prove completed work",
	}, fixedNow.Add(6*time.Minute)); err == nil {
		t.Fatal("close accepted an unretried terminal failure")
	}
	work, err := s.Get(workID)
	if err != nil {
		t.Fatal(err)
	}
	if work.State != "verifying" {
		t.Fatalf("refused close changed work state = %q, want verifying", work.State)
	}
	if got := runAttemptBytesForCloseTest(t, s.home, failed.ID); string(got) != string(before) {
		t.Fatal("refused close rewrote the unrelated terminal failure")
	}
}

func TestCloseRejectsCyclicRetryLineage(t *testing.T) {
	s := newStore(t)
	workID, _ := closeTerminalizeWork(t, s, "close-cyclic-retry-lineage", fixedNow)
	failed := attachTerminalCloseHistoryAttempt(
		t, s, workID, "", runattempt.StatusFailed, true, fixedNow.Add(5*time.Minute),
	)
	failed.PredecessorID = failed.ID
	writeRunAttemptForReconcileTest(t, s, failed)
	before := runAttemptBytesForCloseTest(t, s.home, failed.ID)

	_, err := s.Close(workID, CloseInput{
		Outcome:  "completed",
		MoveType: "control_plane_repair",
		Summary:  "cyclic retry lineage cannot excuse a contradictory terminal attempt",
	}, fixedNow.Add(6*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "retry lineage contains a cycle") {
		t.Fatalf("cyclic retry lineage error = %v, want cycle refusal", err)
	}
	work, getErr := s.Get(workID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if work.State != "verifying" {
		t.Fatalf("cycle refusal changed work state = %q, want verifying", work.State)
	}
	if got := runAttemptBytesForCloseTest(t, s.home, failed.ID); string(got) != string(before) {
		t.Fatal("cycle refusal rewrote the terminal attempt")
	}
}

func TestCloseCompletedRejectsOriginOnlyHarnessAsClaimReplacement(t *testing.T) {
	s := newStore(t)
	workID, claimAttemptIDs := closeTerminalizeWork(t, s, "close-origin-only-claim-replacement", fixedNow)
	attempts := runattempt.Open(s.home)
	if _, err := attempts.CompleteWithReason(
		claimAttemptIDs[0],
		runattempt.StatusAbandoned,
		"claim ended before any proved harness launch",
		fixedNow.Add(4*time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	attachTerminalCloseHistoryAttempt(
		t, s, workID, "", runattempt.StatusSucceeded, false, fixedNow.Add(5*time.Minute),
	)

	if _, err := s.Close(workID, CloseInput{
		Outcome:  "completed",
		MoveType: "control_plane_repair",
		Summary:  "origin stamping alone is not execution evidence",
	}, fixedNow.Add(6*time.Minute)); err == nil {
		t.Fatal("close accepted an origin-only harness as a real claim replacement")
	}
}

func TestCloseCompletedRejectsNoncausalRetryLineage(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(runattempt.RunAttempt, runattempt.RunAttempt) runattempt.RunAttempt
	}{
		{
			name: "padded predecessor",
			mutate: func(_ runattempt.RunAttempt, child runattempt.RunAttempt) runattempt.RunAttempt {
				child.PredecessorID = " " + child.PredecessorID + " "
				return child
			},
		},
		{
			name: "backdated child",
			mutate: func(parent, child runattempt.RunAttempt) runattempt.RunAttempt {
				child.CreatedAt = parent.CreatedAt.Add(-time.Second)
				return child
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			workID, _ := closeTerminalizeWork(t, s, "close-noncausal-retry-"+tt.name, fixedNow)
			failed := attachTerminalCloseHistoryAttempt(
				t, s, workID, "", runattempt.StatusFailed, true, fixedNow.Add(5*time.Minute),
			)
			succeeded := attachTerminalCloseHistoryAttempt(
				t, s, workID, failed.ID, runattempt.StatusSucceeded, true, fixedNow.Add(6*time.Minute),
			)
			writeRunAttemptForReconcileTest(t, s, tt.mutate(failed, succeeded))

			if _, err := s.Close(workID, CloseInput{
				Outcome:  "completed",
				MoveType: "control_plane_repair",
				Summary:  "only canonical forward retry lineage is admissible",
			}, fixedNow.Add(7*time.Minute)); err == nil {
				t.Fatal("close accepted noncausal retry lineage")
			}
		})
	}
}

func TestCloseRetryHistoryExceptionIsCompletedOnly(t *testing.T) {
	s := newStore(t)
	workID, _ := closeTerminalizeWork(t, s, "close-retry-exception-completed-only", fixedNow)
	succeeded := attachTerminalCloseHistoryAttempt(
		t, s, workID, "", runattempt.StatusSucceeded, true, fixedNow.Add(5*time.Minute),
	)
	attachTerminalCloseHistoryAttempt(
		t, s, workID, succeeded.ID, runattempt.StatusFailed, true, fixedNow.Add(6*time.Minute),
	)

	if _, err := s.Close(workID, CloseInput{
		Outcome:  "failed",
		MoveType: "audit_only",
		Summary:  "retry exception does not relax failed work outcome checks",
	}, fixedNow.Add(7*time.Minute)); err == nil {
		t.Fatal("failed close accepted a contradictory succeeded predecessor")
	}
}

func TestCloseCompletedRejectsFailedExecutionFollowedOnlyByClaimReservation(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "close-claim-only-retry", SuccessPredicate: "claim reservation is not a successful retry"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(w.ID, "codex", fixedNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	attempts := runattempt.Open(s.home)
	failed, err := attempts.Mint(w.ID, "codex", "claude", fixedNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if failed, err = attempts.SetOrigin(failed.ID, runattempt.OriginHarnessExecution); err != nil {
		t.Fatal(err)
	}
	if failed, err = attempts.CommitLaunch(failed.ID, fixedNow.Add(2*time.Minute+500*time.Millisecond), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{
		To:            "claimed",
		Actor:         "codex",
		RunAttemptIDs: []string{failed.ID},
	}, fixedNow.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := attempts.Complete(failed.ID, runattempt.StatusFailed, fixedNow.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{
		To:          "ready",
		Actor:       "codex",
		Steal:       true,
		StealReason: "retry after failed harness",
	}, fixedNow.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(w.ID, "codex", fixedNow.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "running", Actor: "codex"}, fixedNow.Add(7*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "verifying", Actor: "codex"}, fixedNow.Add(8*time.Minute)); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Close(w.ID, CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "a later claim reservation is not a successful harness retry",
		ProofRefs: []string{"repair:retry-claim-only"},
	}, fixedNow.Add(9*time.Minute)); err == nil {
		t.Fatal("close accepted a failed execution followed only by a claim reservation")
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
	requests := make([]RunAttemptTerminalizationRequest, 0, len(rattIDs))
	for _, rid := range rattIDs {
		requests = append(requests, RunAttemptTerminalizationRequest{
			RunAttemptID: rid,
			Owner:        runattempt.TerminalizationOwnerPanelApply,
			TargetStatus: runattempt.StatusFailed,
		})
	}
	if _, err := s.Close(id, CloseInput{
		Outcome:                       "completed",
		MoveType:                      "outward_delta",
		Summary:                       "close with caller-owned RunAttempt",
		Changed:                       true,
		ProofRefs:                     []string{"check:close-skip-runattempt-termination"},
		SkipRunAttemptTerminalization: true,
		RunAttemptTerminalizations:    requests,
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
		if ra.TerminalizationIntent == nil {
			t.Fatalf("RunAttempt %s has no durable caller-owned terminalization intent", rid)
		}
		// The caller's post-close proof-ref binding + completion must still land
		// (this is what internal/panel apply does after its fail-soft close).
		if _, err := runattempt.Open(s.home).SetProofRefs(rid, []string{"proof:post-close"}); err != nil {
			t.Fatalf("post-close SetProofRefs on opted-out RunAttempt %s: %v", rid, err)
		}
		if _, err := runattempt.Open(s.home).CompletePreparedTerminalization(rid, *ra.TerminalizationIntent, fixedNow.Add(6*time.Minute)); err != nil {
			t.Fatalf("post-close exact prepared completion on opted-out RunAttempt %s: %v", rid, err)
		}
	}
}
