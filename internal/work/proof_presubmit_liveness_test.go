package work

import (
	"context"
	"testing"
	"time"

	homepkg "compound/internal/home"
	"compound/internal/landlease"
)

// The exclusive proof lease lives two minutes. Other governed writers hold the
// work-admission lock far longer than that under load (a work close, a memory
// refresh cycle), so a heartbeat that waits behind that lock before it refreshes
// expires its own lease and the runtime kills a healthy gate. The lease refresh
// must stay alive while someone else holds the lock.
func TestPresubmitHeartbeatRefreshesLeaseWhileAdmissionLockIsHeld(t *testing.T) {
	now := time.Now()
	s := newExecutorTestStore(t, now)
	w, err := s.Create(CreateInput{Owner: "codex", SuccessPredicate: "presubmit heartbeat liveness"}, now)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := s.runAttempts().Mint(w.ID, "codex", "proof-check-presubmit", now)
	if err != nil {
		t.Fatal(err)
	}
	holder := "proof:presubmit:" + attempt.ID + ":" + w.ID + ":codex"
	resource := landlease.ProofResource(s.home)
	if _, err := resource.Acquire(holder, presubmitLeaseTTL, now); err != nil {
		t.Fatal(err)
	}
	before, err := resource.Status(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	beforeExpiry, err := time.Parse(time.RFC3339Nano, before.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	// Another process holds the work-admission lock for the whole observation.
	held, release := make(chan struct{}), make(chan struct{})
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- homepkg.WithAutonomyAdmissionLock(s.home, func() error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	rt := &presubmitRuntime{
		store:             s,
		out:               &PresubmitExecutionResult{WorkID: w.ID, Actor: "codex", RunAttemptID: attempt.ID, Holder: holder},
		resource:          resource,
		leaseHeld:         true,
		cancel:            cancel,
		heartbeatInterval: 20 * time.Millisecond,
	}
	rt.startHeartbeat(ctx)
	refreshed := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		st, err := resource.Status(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		expiry, err := time.Parse(time.RFC3339Nano, st.ExpiresAt)
		if err != nil {
			t.Fatal(err)
		}
		if st.Holder == holder && expiry.After(beforeExpiry) {
			refreshed = true
			break
		}
	}
	cancelledEarly := ctx.Err() != nil
	cause := context.Cause(ctx)
	close(release)
	if err := <-lockDone; err != nil {
		t.Fatal(err)
	}
	rt.stopHeartbeat()
	<-rt.heartbeatDone
	if cancelledEarly {
		t.Fatalf("heartbeat cancelled the gate while the admission lock was merely busy: %v", cause)
	}
	if !refreshed {
		t.Fatalf("heartbeat never refreshed the exclusive proof lease while the work-admission lock was held; expires_at stayed %s", before.ExpiresAt)
	}
}
