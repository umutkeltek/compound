package work

import (
	"strings"
	"testing"
	"time"

	"compound/internal/landlease"
	"compound/internal/runattempt"
)

const queuedProofOtherHolder = "claude_code:other-lane:work_other"

func queuedProofInput(t *testing.T) ProofResourceReserveInput {
	t.Helper()
	profile, err := NewProofResourceProfile(ProofResourceProfileCanonicalFull, "/synthetic/repo-alpha", "", []ProofResourceGateProfile{{
		Name:     "synthetic-gate-a",
		Command:  []string{"go", "test", "./internal/synthetic-a"},
		ProofRef: "synthetic-proof-a",
	}})
	if err != nil {
		t.Fatal(err)
	}
	return ProofResourceReserveInput{
		Actor:     proofActorAlpha,
		TTL:       5 * time.Minute,
		Workspace: "/synthetic/repo-alpha",
		Command:   "compound proof-check --wait 30s --out /synthetic/report-alpha.json",
		Profile:   profile,
	}
}

// A plain reserve against this lease refuses and leaves an abandoned RunAttempt.
// Queued, the reserve waits out the other lane and ends with exactly one attempt,
// running, and names the holder it waited on.
func TestReserveProofResourceWhenFreeQueuesInsteadOfAbandoningAnAttempt(t *testing.T) {
	s := newStore(t)
	now := time.Now().UTC()
	w := claimedProofWork(t, s, "queued proof work", proofActorAlpha, now)
	if _, err := landlease.Acquire(s.home, queuedProofOtherHolder, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	before := runAttemptCount(t, s, w.ID)
	released := make(chan error, 1)
	go func() {
		time.Sleep(300 * time.Millisecond)
		_, err := landlease.Release(s.home, queuedProofOtherHolder, time.Now())
		released <- err
	}()

	var progress strings.Builder
	result, err := s.ReserveProofResourceWhenFree(w.ID, queuedProofInput(t), 30*time.Second, landlease.RealClock{}, &progress)
	if releaseErr := <-released; releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if err != nil {
		t.Fatalf("queued reserve: %v", err)
	}
	if err := result.ExecutionBarrier.Close(); err != nil {
		t.Fatal(err)
	}
	if result.Reservation.Status != ProofResourceAcquired {
		t.Fatalf("reservation = %+v, want acquired", result.Reservation)
	}
	assertProofRunAttempt(t, s, result.Reservation, runattempt.StatusRunning)
	if added := runAttemptCount(t, s, w.ID) - before; added != 1 {
		t.Fatalf("reserve added %d run attempts, want only the acquired one", added)
	}
	if !strings.Contains(progress.String(), queuedProofOtherHolder) {
		t.Fatalf("progress %q does not name the holder it waited on", progress.String())
	}
}

func TestReserveProofResourceWhenFreeTimesOutWithoutMintingAnAttempt(t *testing.T) {
	s := newStore(t)
	now := time.Now().UTC()
	w := claimedProofWork(t, s, "queued proof work", proofActorAlpha, now)
	if _, err := landlease.Acquire(s.home, queuedProofOtherHolder, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	before := runAttemptCount(t, s, w.ID)
	_, err := s.ReserveProofResourceWhenFree(w.ID, queuedProofInput(t), time.Second, landlease.RealClock{}, nil)
	if err == nil || !strings.Contains(err.Error(), queuedProofOtherHolder) {
		t.Fatalf("want a wait timeout naming the holder, got %v", err)
	}
	if added := runAttemptCount(t, s, w.ID) - before; added != 0 {
		t.Fatalf("a timed-out wait minted %d run attempts", added)
	}
}

func runAttemptCount(t *testing.T, s *Store, workID string) int {
	t.Helper()
	w, err := s.Get(workID)
	if err != nil {
		t.Fatal(err)
	}
	return len(w.RunAttemptIDs)
}
