package landlease

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// synthetic holders/reasons — no personal data (public repo).
const (
	laneAlpha = "lane-alpha"
	laneBeta  = "lane-beta"
	laneGamma = "lane-gamma"
)

// baseNow is a fixed injected instant; all tests do TTL/expiry math off this, never the wall clock.
var baseNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// fakeClock is a deterministic Clock for the wait loop: each Sleep advances virtual time by exactly
// the requested poll, so a blocking AcquireWait is testable single-threaded with no real sleeping.
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time        { return c.t }
func (c *fakeClock) Sleep(d time.Duration) { c.t = c.t.Add(d) }

// Falsifier 1: two DIFFERENT holders cannot hold at once. Acquire(A) then Acquire(B) is refused while
// A's lease is live; after Release(A) Acquire(B) succeeds.
func TestTwoDifferentHoldersCannotHoldAtOnce(t *testing.T) {
	home := t.TempDir()
	ttl := 60 * time.Second

	if _, err := Acquire(home, laneAlpha, ttl, baseNow); err != nil {
		t.Fatalf("A acquire: %v", err)
	}

	// B is refused while A's lease is live, and told who holds it.
	_, err := Acquire(home, laneBeta, ttl, baseNow.Add(10*time.Second))
	var held *HeldError
	if !errors.As(err, &held) {
		t.Fatalf("B acquire on A's live lease must be refused with *HeldError, got %v", err)
	}
	if held.Holder != laneAlpha {
		t.Fatalf("HeldError.Holder = %q, want %q", held.Holder, laneAlpha)
	}

	// After Release(A), B acquires.
	if _, err := Release(home, laneAlpha, baseNow.Add(11*time.Second)); err != nil {
		t.Fatalf("release A: %v", err)
	}
	lease, err := Acquire(home, laneBeta, ttl, baseNow.Add(12*time.Second))
	if err != nil {
		t.Fatalf("B acquire after A released: %v", err)
	}
	if lease.Holder != laneBeta {
		t.Fatalf("after release, holder = %q, want %q", lease.Holder, laneBeta)
	}
}

// Falsifier 2: TTL expiry lets B acquire WITHOUT a steal, and the record is a CLEAN acquire (no steal
// fields). Before expiry B is refused; only crossing expiry flips it.
func TestExpiredLeaseAllowsCleanAcquire(t *testing.T) {
	home := t.TempDir()
	ttl := 30 * time.Second

	if _, err := Acquire(home, laneAlpha, ttl, baseNow); err != nil {
		t.Fatalf("A acquire: %v", err)
	}

	// One second before expiry: still A's — B refused. Proves it is EXPIRY that flips it, not time alone.
	beforeExpiry := baseNow.Add(29 * time.Second)
	if _, err := Acquire(home, laneBeta, ttl, beforeExpiry); err == nil {
		t.Fatalf("B must be refused while A's lease is still live (1s before expiry)")
	}

	// One second past expiry: B acquires cleanly.
	afterExpiry := baseNow.Add(31 * time.Second)
	lease, err := Acquire(home, laneBeta, ttl, afterExpiry)
	if err != nil {
		t.Fatalf("B acquire after A expired: %v", err)
	}
	if lease.Holder != laneBeta {
		t.Fatalf("holder = %q, want %q", lease.Holder, laneBeta)
	}
	if lease.PriorHolder != "" || lease.StealReason != "" || lease.StolenAt != "" {
		t.Fatalf("expired takeover must be a CLEAN acquire, not a steal: %+v", lease)
	}

	// Status confirms the persisted record carries no steal audit.
	st, err := Status(home, afterExpiry)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.PriorHolder != "" || st.StealReason != "" || st.StolenAt != "" {
		t.Fatalf("status shows steal fields on a clean expired-takeover acquire: %+v", st)
	}
	if !st.Held || st.Holder != laneBeta {
		t.Fatalf("status after clean acquire: held=%v holder=%q", st.Held, st.Holder)
	}
}

// Falsifier 3: Steal is recorded and gated. A plain Acquire never transfers A's LIVE lease; only Steal
// does, only with a reason, and it records prior_holder + reason + stolen_at.
func TestStealIsRecordedAndPlainAcquireNeverTransfers(t *testing.T) {
	home := t.TempDir()
	ttl := 120 * time.Second

	if _, err := Acquire(home, laneAlpha, ttl, baseNow); err != nil {
		t.Fatalf("A acquire: %v", err)
	}

	// A plain Acquire(B) on A's LIVE lease must REFUSE (never a silent transfer).
	_, err := Acquire(home, laneBeta, ttl, baseNow.Add(5*time.Second))
	var held *HeldError
	if !errors.As(err, &held) {
		t.Fatalf("plain Acquire on a live lease must refuse, got %v", err)
	}
	// ...and the persisted record still shows A with no steal fields.
	st, err := Status(home, baseNow.Add(5*time.Second))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Holder != laneAlpha || st.PriorHolder != "" || st.StealReason != "" {
		t.Fatalf("a live lease was silently altered by a plain Acquire: %+v", st)
	}

	// Steal transfers to B and records the audit trail.
	reason := "alpha wedged past its window; beta taking the land slot"
	stolen, err := Steal(home, laneBeta, reason, baseNow.Add(6*time.Second))
	if err != nil {
		t.Fatalf("steal: %v", err)
	}
	if stolen.Holder != laneBeta {
		t.Fatalf("stolen holder = %q, want %q", stolen.Holder, laneBeta)
	}
	if stolen.PriorHolder != laneAlpha {
		t.Fatalf("stolen prior_holder = %q, want %q", stolen.PriorHolder, laneAlpha)
	}
	if stolen.StealReason != reason {
		t.Fatalf("stolen steal_reason = %q, want %q", stolen.StealReason, reason)
	}
	if stolen.StolenAt == "" {
		t.Fatalf("steal must record stolen_at")
	}
	// The stolen lease inherited A's TTL.
	if stolen.TTLSeconds != 120 {
		t.Fatalf("stolen ttl = %d, want inherited 120", stolen.TTLSeconds)
	}

	// Steal WITHOUT a reason is refused (the gate).
	if _, err := Steal(home, laneGamma, "   ", baseNow.Add(7*time.Second)); err == nil {
		t.Fatalf("steal without a reason must be refused")
	}
	// The refused steal changed nothing: B still holds.
	st2, err := Status(home, baseNow.Add(7*time.Second))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st2.Holder != laneBeta {
		t.Fatalf("refused reasonless steal altered the lease: holder=%q", st2.Holder)
	}
}

// Falsifier 4: Release is holder-scoped. Release(B) on A's lease is refused (A untouched); Release(A)
// frees it.
func TestReleaseIsHolderScoped(t *testing.T) {
	home := t.TempDir()
	ttl := 60 * time.Second

	if _, err := Acquire(home, laneAlpha, ttl, baseNow); err != nil {
		t.Fatalf("A acquire: %v", err)
	}

	// Non-holder release is refused and never frees A's lease.
	if _, err := Release(home, laneBeta, baseNow.Add(1*time.Second)); err == nil {
		t.Fatalf("non-holder release must be refused")
	}
	st, err := Status(home, baseNow.Add(1*time.Second))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !st.Held || st.Holder != laneAlpha {
		t.Fatalf("non-holder release freed or altered the lease: %+v", st)
	}

	// Holder release frees it.
	res, err := Release(home, laneAlpha, baseNow.Add(2*time.Second))
	if err != nil {
		t.Fatalf("holder release: %v", err)
	}
	if !res.Freed {
		t.Fatalf("holder release should report Freed=true: %+v", res)
	}
	st2, err := Status(home, baseNow.Add(2*time.Second))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st2.Held {
		t.Fatalf("lease should be free after holder release: %+v", st2)
	}

	// A release with nothing held is an idempotent no-op, not an error.
	res2, err := Release(home, laneBeta, baseNow.Add(3*time.Second))
	if err != nil {
		t.Fatalf("release on empty lease should be a no-op, got %v", err)
	}
	if res2.Freed {
		t.Fatalf("release on empty lease must not report Freed")
	}
}

// AcquireWait: A holds; the injected clock advances past A's expiry during the poll loop, and B
// acquires CLEANLY (no steal) within the timeout.
func TestAcquireWaitAcquiresAfterHolderExpires(t *testing.T) {
	home := t.TempDir()
	ttl := 10 * time.Second

	if _, err := Acquire(home, laneAlpha, ttl, baseNow); err != nil {
		t.Fatalf("A acquire: %v", err)
	}
	clk := &fakeClock{t: baseNow}
	lease, err := AcquireWait(home, laneBeta, ttl, clk, 60*time.Second, 2*time.Second)
	if err != nil {
		t.Fatalf("AcquireWait should succeed once A expires within the timeout: %v", err)
	}
	if lease.Holder != laneBeta {
		t.Fatalf("waiter holder = %q, want %q", lease.Holder, laneBeta)
	}
	if lease.PriorHolder != "" || lease.StealReason != "" {
		t.Fatalf("waiter should CLEAN-acquire an expired lease, not steal: %+v", lease)
	}
}

// AcquireWait: A holds far longer than the wait window; B times out instead of stealing or blocking
// forever.
func TestAcquireWaitTimesOut(t *testing.T) {
	home := t.TempDir()
	ttl := 3600 * time.Second // A holds for an hour

	if _, err := Acquire(home, laneAlpha, ttl, baseNow); err != nil {
		t.Fatalf("A acquire: %v", err)
	}
	clk := &fakeClock{t: baseNow}
	_, err := AcquireWait(home, laneBeta, ttl, clk, 30*time.Second, 5*time.Second)
	if err == nil {
		t.Fatalf("AcquireWait must time out while A holds well past the wait window")
	}
	// A still holds — the timed-out waiter changed nothing.
	st, err := Status(home, clk.Now())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Holder != laneAlpha {
		t.Fatalf("a timed-out wait must not disturb the holder: %+v", st)
	}
}

// Same-holder Acquire is an idempotent refresh that extends the window without a steal.
func TestSameHolderAcquireRefreshesWindow(t *testing.T) {
	home := t.TempDir()
	ttl := 60 * time.Second

	first, err := Acquire(home, laneAlpha, ttl, baseNow)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	refreshed, err := Acquire(home, laneAlpha, ttl, baseNow.Add(30*time.Second))
	if err != nil {
		t.Fatalf("same-holder refresh must succeed: %v", err)
	}
	if refreshed.PriorHolder != "" || refreshed.StealReason != "" {
		t.Fatalf("refresh must be a clean record, not a steal: %+v", refreshed)
	}
	firstExp, _ := time.Parse(time.RFC3339, first.ExpiresAt)
	refExp, _ := time.Parse(time.RFC3339, refreshed.ExpiresAt)
	if !refExp.After(firstExp) {
		t.Fatalf("refresh should extend expiry: first=%s refreshed=%s", first.ExpiresAt, refreshed.ExpiresAt)
	}
}

// The persisted lease is well-formed JSON with the schema version and derived fields consistent.
func TestPersistedLeaseShape(t *testing.T) {
	home := t.TempDir()
	if _, err := Acquire(home, laneAlpha, 45*time.Second, baseNow); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, "state", "land-lease.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var lease Lease
	if err := json.Unmarshal(data, &lease); err != nil {
		t.Fatalf("state must be valid JSON: %v", err)
	}
	if lease.SchemaVersion != SchemaVersion {
		t.Fatalf("schema_version = %q, want %q", lease.SchemaVersion, SchemaVersion)
	}
	if lease.TTLSeconds != 45 {
		t.Fatalf("ttl_seconds = %d, want 45", lease.TTLSeconds)
	}
	// expires_at must equal acquired_at + ttl_seconds (they can never disagree).
	acq, err := time.Parse(time.RFC3339, lease.AcquiredAt)
	if err != nil {
		t.Fatalf("acquired_at not RFC3339: %v", err)
	}
	exp, err := time.Parse(time.RFC3339, lease.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at not RFC3339: %v", err)
	}
	if got := exp.Sub(acq); got != 45*time.Second {
		t.Fatalf("expires_at - acquired_at = %s, want 45s", got)
	}
}
