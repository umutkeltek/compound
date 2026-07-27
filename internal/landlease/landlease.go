// Package landlease is a single-holder, TTL-bounded, steal-with-reason MUTEX for scarce repo-wide
// proof/landing resources — distinct from the per-work leases in internal/work. Concurrent executor
// lanes (multiple Claude/Codex harnesses driving ONE repo) consult it to serialize full proof
// batteries and the fast-forward-merge window instead of contending and producing ambiguous proof.
// It is a coordination primitive, not meaning authority: it never emits a PolicyDecision/Receipt.
// Governed consumers wrap its holder transitions in their own ActionIntent/PolicyDecision evidence.
//
// The lease is a JSON file at $COMPOUND_HOME/state/land-lease.json guarded by an exclusive OS advisory
// lock ($COMPOUND_HOME/lock/land-lease.lock, syscall.Flock LOCK_EX) so every read-modify-write is
// atomic across separate processes/harnesses. compound's supported runtime targets are Darwin and
// Linux — the same advisory-lock strategy as home.WithAutonomyAdmissionLock. All TTL/expiry math takes
// an INJECTED clock (now time.Time / a Clock in the wait loop); there are NO wall-clock calls in the
// decision logic (AGENTS.md fixture-time rule) — real time only enters at the CLI edge.
//
// Governance boundary: Acquire REFUSES a competing live holder, but that refusal is an advisory mutex
// hint, not a governed gate — it carries no PolicyDecision, Receipt, or undo, and compiles no operator
// ruling. The compound-land ceremony is the enforcement consumer that ACTS on the hint. See the
// two-class MintHome (organ:landlease = MintEvidence) in internal/arch/mint.go.
//
// The two invariants the falsifiers protect:
//   - A plain Acquire NEVER silently transfers a LIVE lease held by a different holder (it refuses);
//     it only ever succeeds on a free/expired lease or its OWN lease, and always writes a CLEAN record
//     (no steal fields). Taking over an EXPIRED lease is a clean acquire, not a steal.
//   - A transfer of a live lease happens ONLY through Steal, which REQUIRES a reason and RECORDS the
//     prior_holder + reason + stolen_at for audit.
package landlease

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ExecutionPendingError means lease time expired or a steal was requested,
// but at least one process still owns the predecessor's inherited execution
// barrier. Callers must not begin scarce work while this error is returned.
type ExecutionPendingError struct {
	Holder string
}

func (e *ExecutionPendingError) Error() string {
	return fmt.Sprintf("land-lease holder %q still has a live execution tree", e.Holder)
}

// ExecutionUnattestedError rejects state-only recovery of a proof holder
// created before the inherited barrier protocol. Expiry is not termination.
type ExecutionUnattestedError struct {
	Holder string
}

func (e *ExecutionUnattestedError) Error() string {
	return fmt.Sprintf("land-lease proof holder %q has no trusted execution termination barrier", e.Holder)
}

type ExecutionTerminationAttestation struct {
	BarrierID string `json:"barrier_id"`
	Holder    string `json:"holder"`
	Producer  string `json:"producer"`
}

// ExecutionBarrier is an advisory lock whose descriptor is inherited by every
// scarce subprocess. The kernel releases it only after the final duplicate
// exits or closes, so liveness does not rely on a reusable PID or RunAttempt
// status label.
type ExecutionBarrier struct {
	id   string
	file *os.File
}

func (b *ExecutionBarrier) ID() string {
	if b == nil {
		return ""
	}
	return b.id
}

func (b *ExecutionBarrier) DuplicateForChild() (*os.File, error) {
	if b == nil || b.file == nil {
		return nil, fmt.Errorf("land-lease execution barrier is closed")
	}
	fd, err := syscall.Dup(int(b.file.Fd()))
	if err != nil {
		return nil, fmt.Errorf("duplicate land-lease execution barrier: %w", err)
	}
	return os.NewFile(uintptr(fd), b.file.Name()), nil
}

// InheritableFile returns the owned descriptor for exec.Cmd.ExtraFiles.
// os/exec duplicates it into the child; the caller must not close this file
// separately from Close.
func (b *ExecutionBarrier) InheritableFile() *os.File {
	if b == nil {
		return nil
	}
	return b.file
}

func (b *ExecutionBarrier) Close() error {
	if b == nil || b.file == nil {
		return nil
	}
	file := b.file
	b.file = nil
	// Do not issue LOCK_UN: duplicates inherited by child processes share the
	// same open-file description, and an explicit unlock here would release
	// their protection too. Closing the final descriptor releases the lock.
	return file.Close()
}

const (
	// SchemaVersion of the persisted lease record.
	SchemaVersion = "compound.land_lease.v0.1"
	// DefaultTTLSeconds is the fallback landing window a Steal adopts when no prior lease record
	// supplies a TTL to inherit. A landing fast-forward is a short operation; five minutes is a
	// generous ceiling that still self-heals if a lane dies mid-land.
	DefaultTTLSeconds = 300
)

// relative paths beneath COMPOUND_HOME (this organ owns creating both dirs — generated runtime state
// lives under the home, never the repo source tree).
const (
	lockRelPath             = "lock/land-lease.lock"
	stateRelPath            = "state/land-lease.json"
	executionBarrierRelPath = "lock/land-lease-execution"
)

// Lease is the persisted land-lease record. The steal-audit fields (PriorHolder/StealReason/StolenAt)
// are set ONLY on a lease produced by Steal; a plain Acquire always leaves them empty.
type Lease struct {
	TerminationRequestedBy   string `json:"termination_requested_by,omitempty"`
	TerminationRequestReason string `json:"termination_request_reason,omitempty"`
	TerminationRequestedAt   string `json:"termination_requested_at,omitempty"`
	SchemaVersion            string `json:"schema_version"`
	Holder                   string `json:"holder"`
	AcquiredAt               string `json:"acquired_at"` // RFC3339 (UTC)
	TTLSeconds               int64  `json:"ttl_seconds"`
	ExpiresAt                string `json:"expires_at"` // RFC3339 (UTC)
	PriorHolder              string `json:"prior_holder,omitempty"`
	StealReason              string `json:"steal_reason,omitempty"`
	StolenAt                 string `json:"stolen_at,omitempty"`
}

// live reports whether the lease is still held at now (now strictly before expiry). An unparseable
// ExpiresAt is treated as NOT live: a record whose expiry cannot be read is not a live lease anyone
// must respect, so it never wedges the landing window forever (it becomes cleanly acquirable).
func (l *Lease) live(now time.Time) bool {
	exp, err := time.Parse(time.RFC3339, l.ExpiresAt)
	if err != nil {
		return false
	}
	return now.Before(exp)
}

// remainingSeconds is whole seconds until expiry, floored at zero. An unparseable expiry yields 0.
func (l *Lease) remainingSeconds(now time.Time) int64 {
	exp, err := time.Parse(time.RFC3339, l.ExpiresAt)
	if err != nil {
		return 0
	}
	d := exp.Sub(now)
	if d <= 0 {
		return 0
	}
	return int64(d / time.Second)
}

// HeldError is the typed refusal returned by Acquire (and surfaced by AcquireWait on timeout) when a
// DIFFERENT holder holds a LIVE lease. Callers can inspect Holder to report who to wait on / steal from.
type HeldError struct {
	Holder    string
	ExpiresAt string
	Remaining int64
}

func (e *HeldError) Error() string {
	return fmt.Sprintf("land-lease held by %q until %s (%ds remaining)", e.Holder, e.ExpiresAt, e.Remaining)
}

// ReleaseResult reports the outcome of Release. Freed is true only when the caller HELD the lease and
// it was removed; a no-lease release is a Freed=false no-op (idempotent), and a non-holder release
// returns an error (never Freed) so someone else's lease is never silently dropped.
type ReleaseResult struct {
	Freed   bool   `json:"freed"`
	Holder  string `json:"holder,omitempty"`
	Message string `json:"message"`
}

// StatusResult is the read-model of the current lease. Held is true only for a LIVE (non-expired)
// lease; the record's fields (Holder/AcquiredAt/ExpiresAt + any steal audit) are surfaced even when
// expired so an operator can see what just aged out.
type StatusResult struct {
	TerminationRequestedBy   string `json:"termination_requested_by,omitempty"`
	TerminationRequestReason string `json:"termination_request_reason,omitempty"`
	TerminationRequestedAt   string `json:"termination_requested_at,omitempty"`
	SchemaVersion            string `json:"schema_version"`
	Held                     bool   `json:"held"`
	Holder                   string `json:"holder,omitempty"`
	AcquiredAt               string `json:"acquired_at,omitempty"`
	ExpiresAt                string `json:"expires_at,omitempty"`
	TTLSeconds               int64  `json:"ttl_seconds,omitempty"`
	RemainingSeconds         int64  `json:"remaining_seconds"`
	IsExpired                bool   `json:"is_expired"`
	PriorHolder              string `json:"prior_holder,omitempty"`
	StealReason              string `json:"steal_reason,omitempty"`
	StolenAt                 string `json:"stolen_at,omitempty"`
}

// AcquireOutcome exposes the predecessor observed inside the same advisory-lock transaction that
// installs Lease. ExpiredPredecessor is non-nil only when an existing non-live record was replaced;
// consumers can therefore record stale-owner recovery without a racy Status-before-Acquire read.
// The installed Lease remains a clean acquire: expiry recovery is not exceptional stealing.
type AcquireOutcome struct {
	Lease              Lease  `json:"lease"`
	ExpiredPredecessor *Lease `json:"expired_predecessor,omitempty"`
}

// Clock is the injected time source for the polling wait loop. The single-shot ops take a plain
// now time.Time; only AcquireWait needs to advance time while it blocks, which the Clock supplies.
// RealClock wires it to the wall clock at the CLI edge; tests inject a deterministic fake.
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

// RealClock is the production Clock: wall time + real sleeps. It exists only at the CLI edge — the
// decision logic never calls it directly.
type RealClock struct{}

func (RealClock) Now() time.Time        { return time.Now() }
func (RealClock) Sleep(d time.Duration) { time.Sleep(d) }

// Acquire takes (or refreshes) the land-lease for holder. It succeeds when there is no lease, when the
// existing lease is EXPIRED (a clean takeover — no steal), or when holder ALREADY holds it (idempotent
// refresh that extends the window). It REFUSES with a *HeldError when a DIFFERENT holder holds a LIVE
// lease — a plain Acquire never silently transfers a live lease (only Steal does). The write is atomic
// under the exclusive flock, and always produces a CLEAN record (steal-audit fields empty).
func Acquire(home, holder string, ttl time.Duration, now time.Time) (Lease, error) {
	outcome, err := AcquireWithOutcome(home, holder, ttl, now)
	return outcome.Lease, err
}

// AcquireWithOutcome has Acquire's exact mutual-exclusion semantics and additionally returns the
// expired/stale predecessor, when one existed, from inside the locked read-modify-write. This is
// the auditable stale-recovery seam for consumers that need to bind lease recovery to governed
// work evidence. Ordinary callers should keep using Acquire.
func AcquireWithOutcome(home, holder string, ttl time.Duration, now time.Time) (AcquireOutcome, error) {
	return acquireWithOutcomePrepared(home, holder, ttl, now, nil)
}

// AcquireWithOutcomePrepared is AcquireWithOutcome plus a pre-publication durability barrier.
// prepare runs under the same land-lease lock after the exact outcome is known and before the new
// lease is published. A prepare failure leaves the prior lease byte-identical.
func AcquireWithOutcomePrepared(
	home, holder string,
	ttl time.Duration,
	now time.Time,
	prepare func(AcquireOutcome) error,
) (AcquireOutcome, error) {
	if prepare == nil {
		return AcquireOutcome{}, fmt.Errorf("land-lease acquire prepare callback required")
	}
	return acquireWithOutcomePrepared(home, holder, ttl, now, prepare)
}

func acquireWithOutcomePrepared(
	home, holder string,
	ttl time.Duration,
	now time.Time,
	prepare func(AcquireOutcome) error,
) (AcquireOutcome, error) {
	holder = strings.TrimSpace(holder)
	if holder == "" {
		return AcquireOutcome{}, fmt.Errorf("land-lease holder required")
	}
	ttlSecs := int64(ttl / time.Second)
	if ttlSecs <= 0 {
		return AcquireOutcome{}, fmt.Errorf("land-lease ttl must be at least one second")
	}
	var outcome AcquireOutcome
	err := withLock(home, func() error {
		statePath, sErr := statePath(home)
		if sErr != nil {
			return sErr
		}
		current, exists, rErr := readLease(statePath)
		if rErr != nil {
			return rErr
		}
		if exists && current.live(now) && current.Holder != holder {
			return &HeldError{
				Holder:    current.Holder,
				ExpiresAt: current.ExpiresAt,
				Remaining: current.remainingSeconds(now),
			}
		}
		if exists && current.Holder != holder {
			pending, err := executionBarrierPending(home, current.Holder)
			if err != nil {
				return err
			}
			if pending {
				return &ExecutionPendingError{Holder: current.Holder}
			}
		}
		if exists && !current.live(now) {
			expired := *current
			outcome.ExpiredPredecessor = &expired
		}
		outcome.Lease = newLease(holder, ttlSecs, now)
		if prepare != nil {
			if err := prepare(outcome); err != nil {
				return err
			}
		}
		return writeLeaseAtomic(statePath, outcome.Lease)
	})
	if err != nil {
		return AcquireOutcome{}, err
	}
	return outcome, nil
}

// Refresh extends only an exact, still-LIVE holder. Unlike Acquire it never resurrects an expired
// record, so a proof heartbeat that arrives after its TTL cannot reopen an exclusive window that a
// successor was already entitled to take.
func Refresh(home, holder string, ttl time.Duration, now time.Time) (Lease, error) {
	holder = strings.TrimSpace(holder)
	if holder == "" {
		return Lease{}, fmt.Errorf("land-lease holder required")
	}
	ttlSecs := int64(ttl / time.Second)
	if ttlSecs <= 0 {
		return Lease{}, fmt.Errorf("land-lease ttl must be at least one second")
	}
	var refreshed Lease
	err := withLock(home, func() error {
		statePath, sErr := statePath(home)
		if sErr != nil {
			return sErr
		}
		current, exists, rErr := readLease(statePath)
		if rErr != nil {
			return rErr
		}
		if !exists {
			return fmt.Errorf("land-lease not held; refusing refresh for %q", holder)
		}
		if current.Holder != holder {
			return fmt.Errorf("land-lease held by %q, not %q — refusing refresh", current.Holder, holder)
		}
		if !current.live(now) {
			return fmt.Errorf("land-lease for %q expired at %s; refusing refresh", holder, current.ExpiresAt)
		}
		refreshed = newLease(holder, ttlSecs, now)
		return writeLeaseAtomic(statePath, refreshed)
	})
	if err != nil {
		return Lease{}, err
	}
	return refreshed, nil
}

// RearmExpiredExact reopens a short exclusive window for one retained finalizer after its
// execution has terminated. It is deliberately narrower than Refresh or Acquire: the caller must
// supply the full expired Status snapshot, the persisted record must still match it exactly under
// the lease lock, and the holder's execution barrier must attest clear. A successor, refresh,
// termination request, or any other record change therefore makes recovery fail closed.
func RearmExpiredExact(home string, expected StatusResult, now time.Time) (Lease, error) {
	if expected.Held || !expected.IsExpired || strings.TrimSpace(expected.Holder) == "" {
		return Lease{}, fmt.Errorf("land-lease exact expired status required for finalizer recovery")
	}
	var rearmed Lease
	err := withLock(home, func() error {
		statePath, err := statePath(home)
		if err != nil {
			return err
		}
		current, exists, err := readLease(statePath)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("land-lease expected expired holder %q, but no lease exists", expected.Holder)
		}
		if !leaseMatchesStatus(*current, expected, now) {
			return fmt.Errorf("land-lease record changed after expired holder %q was validated", expected.Holder)
		}
		if current.live(now) {
			return fmt.Errorf("land-lease for %q is not expired at recovery time", expected.Holder)
		}
		pending, err := executionBarrierPending(home, current.Holder)
		if err != nil {
			return err
		}
		if pending {
			return &ExecutionPendingError{Holder: current.Holder}
		}
		ttlSecs := current.TTLSeconds
		if ttlSecs <= 0 {
			ttlSecs = DefaultTTLSeconds
		}
		rearmed = *current
		rearmed.AcquiredAt = now.UTC().Format(time.RFC3339)
		rearmed.TTLSeconds = ttlSecs
		rearmed.ExpiresAt = now.UTC().Add(time.Duration(ttlSecs) * time.Second).Format(time.RFC3339)
		return writeLeaseAtomic(statePath, rearmed)
	})
	if err != nil {
		return Lease{}, err
	}
	return rearmed, nil
}

func leaseMatchesStatus(lease Lease, status StatusResult, now time.Time) bool {
	return lease.SchemaVersion == status.SchemaVersion &&
		lease.Holder == status.Holder &&
		lease.AcquiredAt == status.AcquiredAt &&
		lease.ExpiresAt == status.ExpiresAt &&
		lease.TTLSeconds == status.TTLSeconds &&
		lease.PriorHolder == status.PriorHolder &&
		lease.StealReason == status.StealReason &&
		lease.StolenAt == status.StolenAt &&
		lease.TerminationRequestedBy == status.TerminationRequestedBy &&
		lease.TerminationRequestReason == status.TerminationRequestReason &&
		lease.TerminationRequestedAt == status.TerminationRequestedAt &&
		lease.live(now) == status.Held &&
		(!lease.live(now)) == status.IsExpired &&
		lease.remainingSeconds(now) == status.RemainingSeconds
}

// errWatchUnsupported is returned by newLeaseWatcher on a platform with no supported file-event
// primitive (compound targets Darwin/kqueue and Linux/inotify). A waiter that cannot build a watcher
// degrades cleanly to the clock-driven bounded poll — the watch is a fast path, never the sole liveness.
var errWatchUnsupported = errors.New("land-lease: no supported file watcher on this platform")

// leaseWatcher is a BOUNDED OS file-event wait over the land-lease state DIRECTORY. wait blocks until the
// directory changes (the atomic temp-file rename in writeLeaseAtomic, or the os.Remove in Release, mutates
// the directory) OR the supplied bound elapses — whichever is first — then returns so the caller
// re-attempts Acquire. It is an OPTIMIZATION: a missed/coalesced event is harmless because every wait is
// bounded and the caller always re-checks the real lease under the flock afterward, so the watcher can
// NEVER wedge a waiter past its deadline. close releases the OS resources. Real implementations live in the
// platform files (watch_darwin.go / watch_linux.go); watch_other.go degrades to the poll.
type leaseWatcher interface {
	wait(bound time.Duration) error
	close() error
}

// watcherFactory builds a leaseWatcher for the state directory. Production wires newLeaseWatcher (a real
// kqueue/inotify watcher); tests inject a fake or nil (nil ⇒ pure clock-driven poll). A factory error is
// non-fatal — the caller degrades to the bounded poll.
type watcherFactory func(stateDir string) (leaseWatcher, error)

// AcquireWait waits for the land-lease to become free/held-by-us, or the timeout to elapse. A waiter
// blocked on a live lease wakes the MOMENT the holder Releases (or Steals/expires-and-is-taken) via an OS
// file-watch of the state directory (kqueue on Darwin, inotify on Linux), instead of on its next fixed
// poll tick — while a BOUNDED poll remains the safety net so a missed event or an unsupported platform can
// never hang the waiter past its timeout. It returns promptly on the first successful acquire. A non-"held"
// error (e.g. a disk error) is surfaced immediately rather than retried. All deadline/bound math takes the
// INJECTED Clock, so the loop is deterministic under a fake clock (production passes RealClock{}); the
// file-watch itself is real OS I/O layered on top as a fast path. A poll <= 0 falls back to a one-second
// cadence. There is no provider/LLM/network call. The signature is unchanged, so the CLI caller is untouched.
func AcquireWait(home, holder string, ttl time.Duration, clk Clock, timeout, poll time.Duration) (Lease, error) {
	return acquireWait(home, holder, ttl, clk, timeout, poll, newLeaseWatcher)
}

// acquireWait is the testable core of AcquireWait: the watcher is INJECTED (factory) so deterministic tests
// exercise the bounded-poll path with a fake clock (nil / erroring factory, or a fake watcher) while the
// real kqueue/inotify path is proven by an integration test. The watcher is created LAZILY on first
// contention (no syscall cost on an uncontended acquire) and torn down on return. The clock-injected
// `bound` is the safety net: no single wait can exceed one poll interval or overshoot the deadline, even
// with the watch armed — so a broken/missed watch can never hang the waiter.
func acquireWait(home, holder string, ttl time.Duration, clk Clock, timeout, poll time.Duration, factory watcherFactory) (Lease, error) {
	if clk == nil {
		return Lease{}, fmt.Errorf("land-lease AcquireWait requires a clock")
	}
	if poll <= 0 {
		poll = time.Second
	}
	statePath, err := statePath(home)
	if err != nil {
		return Lease{}, err
	}
	stateDir := filepath.Dir(statePath)

	var w leaseWatcher
	watcherTried := false
	defer func() {
		if w != nil {
			_ = w.close()
		}
	}()

	deadline := clk.Now().Add(timeout)
	for {
		lease, err := Acquire(home, holder, ttl, clk.Now())
		if err == nil {
			return lease, nil
		}
		var held *HeldError
		if !errors.As(err, &held) {
			return Lease{}, err // a real error, not contention — surface it
		}
		now := clk.Now()
		if !now.Before(deadline) {
			return Lease{}, fmt.Errorf("land-lease acquire timed out after %s waiting for holder %q: %w", timeout, held.Holder, err)
		}
		// Bound this wait: at most one poll interval, and never past the deadline. This bound is the
		// SAFETY NET — even with the watch armed, no single wait can exceed it, so a missed/coalesced
		// event can never hang the waiter.
		bound := poll
		if remaining := deadline.Sub(now); remaining < bound {
			bound = remaining
		}
		// Lazily arm the watcher on first contention (fast path); on failure, stay on the clock poll.
		if factory != nil && !watcherTried {
			watcherTried = true
			_ = os.MkdirAll(stateDir, 0o755) // best-effort: the dir must exist to attach the watch
			if ww, werr := factory(stateDir); werr == nil {
				w = ww
			}
		}
		if w != nil {
			// FAST PATH: block in the kernel until the state directory changes OR bound elapses. On a
			// watcher error mid-flight, drop it and fall back to the clock-driven bounded sleep so the
			// loop can never hang on a broken watch.
			if werr := w.wait(bound); werr != nil {
				_ = w.close()
				w = nil
				clk.Sleep(bound)
			}
		} else {
			clk.Sleep(bound) // SAFETY NET: deterministic, clock-injected bound (no busy-spin)
		}
	}
}

// Release frees the lease ONLY when holder currently holds it and its execution barrier is no longer
// pending. A release by a NON-holder is refused with an error (someone else's live lease is never
// silently dropped); a release when nothing is held is an idempotent Freed=false no-op. A held lease
// is removed (Status then shows nothing held).
func Release(home, holder string, now time.Time) (ReleaseResult, error) {
	holder = strings.TrimSpace(holder)
	if holder == "" {
		return ReleaseResult{}, fmt.Errorf("land-lease holder required")
	}
	var result ReleaseResult
	err := withLock(home, func() error {
		statePath, sErr := statePath(home)
		if sErr != nil {
			return sErr
		}
		current, exists, rErr := readLease(statePath)
		if rErr != nil {
			return rErr
		}
		if !exists {
			result = ReleaseResult{Freed: false, Message: "no land-lease held"}
			return nil
		}
		if current.Holder != holder {
			return fmt.Errorf("land-lease held by %q, not %q — refusing to release someone else's lease", current.Holder, holder)
		}
		pending, err := executionBarrierPending(home, current.Holder)
		if err != nil {
			return err
		}
		if pending {
			return &ExecutionPendingError{Holder: current.Holder}
		}
		if err := os.Remove(statePath); err != nil {
			return err
		}
		if err := syncDirectory(filepath.Dir(statePath)); err != nil {
			return err
		}
		result = ReleaseResult{Freed: true, Holder: holder, Message: "land-lease released"}
		return nil
	})
	if err != nil {
		return ReleaseResult{}, err
	}
	return result, nil
}

// ReleaseAfter runs finalize while holder is still the exact current LIVE lease holder, and removes
// the lease only when finalize succeeds. The finalizer therefore persists authoritative proof/work
// evidence inside the exclusive window: a failed finalizer leaves the lease untouched and prevents a
// successor from starting against a partially-persisted result.
//
// ReleaseAfter is deliberately stricter than Release: a missing or expired lease is an error, because
// a governed proof completion cannot become valid merely because its old record still names holder.
// Callers must not recursively call landlease operations from finalize; the advisory flock is held.
func ReleaseAfter(home, holder string, now time.Time, finalize func() error) (ReleaseResult, error) {
	holder = strings.TrimSpace(holder)
	if holder == "" {
		return ReleaseResult{}, fmt.Errorf("land-lease holder required")
	}
	if finalize == nil {
		return ReleaseResult{}, fmt.Errorf("land-lease release finalizer required")
	}
	var result ReleaseResult
	err := withLock(home, func() error {
		statePath, sErr := statePath(home)
		if sErr != nil {
			return sErr
		}
		current, exists, rErr := readLease(statePath)
		if rErr != nil {
			return rErr
		}
		if !exists {
			return fmt.Errorf("land-lease not held; refusing finalized release for %q", holder)
		}
		if current.Holder != holder {
			return fmt.Errorf("land-lease held by %q, not %q — refusing to finalize or release someone else's lease", current.Holder, holder)
		}
		if !current.live(now) {
			return fmt.Errorf("land-lease for %q expired at %s; refusing late finalized release", holder, current.ExpiresAt)
		}
		if err := finalize(); err != nil {
			return err
		}
		if err := os.Remove(statePath); err != nil {
			return err
		}
		if err := syncDirectory(filepath.Dir(statePath)); err != nil {
			return err
		}
		result = ReleaseResult{Freed: true, Holder: holder, Message: "land-lease finalized and released"}
		return nil
	})
	if err != nil {
		return ReleaseResult{}, err
	}
	return result, nil
}

// Steal transfers the lease to newHolder and RECORDS prior_holder + reason + stolen_at for audit. A
// reason is REQUIRED — this is the gate that distinguishes an auditable steal from a plain Acquire
// (which never transfers a live lease). The stolen lease inherits the prior record's TTL (or the
// package default when there is no prior record), timed from now.
func Steal(home, newHolder, reason string, now time.Time) (Lease, error) {
	return stealExpected(home, newHolder, "", reason, now, false, nil)
}

// StealExpected has Steal's transfer semantics but refuses unless the current holder exactly matches
// expectedPriorHolder. A governed caller can therefore read the prior holder, include it and the
// exceptional reason in its ActionIntent/PolicyDecision, then perform the transfer without a
// time-of-check/time-of-use substitution.
func StealExpected(home, newHolder, expectedPriorHolder, reason string, now time.Time) (Lease, error) {
	expectedPriorHolder = strings.TrimSpace(expectedPriorHolder)
	if expectedPriorHolder == "" {
		return Lease{}, fmt.Errorf("land-lease expected prior holder required")
	}
	return stealExpected(home, newHolder, expectedPriorHolder, reason, now, true, nil)
}

// StealExpectedPrepared is StealExpected plus a pre-publication durability barrier. prepare sees
// the exact audited transfer while the expected-holder check is still protected by the lease lock.
func StealExpectedPrepared(
	home, newHolder, expectedPriorHolder, reason string,
	now time.Time,
	prepare func(Lease) error,
) (Lease, error) {
	expectedPriorHolder = strings.TrimSpace(expectedPriorHolder)
	if expectedPriorHolder == "" {
		return Lease{}, fmt.Errorf("land-lease expected prior holder required")
	}
	if prepare == nil {
		return Lease{}, fmt.Errorf("land-lease steal prepare callback required")
	}
	return stealExpected(home, newHolder, expectedPriorHolder, reason, now, true, prepare)
}

func stealExpected(
	home, newHolder, expectedPriorHolder, reason string,
	now time.Time,
	requireExpected bool,
	prepare func(Lease) error,
) (Lease, error) {
	newHolder = strings.TrimSpace(newHolder)
	if newHolder == "" {
		return Lease{}, fmt.Errorf("land-lease steal requires a holder")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Lease{}, fmt.Errorf("land-lease steal requires a reason (a steal is auditable; use acquire for an uncontested lease)")
	}
	var stolen Lease
	err := withLock(home, func() error {
		statePath, sErr := statePath(home)
		if sErr != nil {
			return sErr
		}
		current, exists, rErr := readLease(statePath)
		if rErr != nil {
			return rErr
		}
		if requireExpected {
			if !exists {
				return fmt.Errorf("land-lease expected holder %q, but no lease exists", expectedPriorHolder)
			}
			if current.Holder != expectedPriorHolder {
				return fmt.Errorf("land-lease holder changed from expected %q to %q; refusing governed steal", expectedPriorHolder, current.Holder)
			}
		}
		if exists {
			pending, err := executionBarrierPending(home, current.Holder)
			if err != nil {
				return err
			}
			if pending {
				return &ExecutionPendingError{Holder: current.Holder}
			}
		}
		ttlSecs := int64(DefaultTTLSeconds)
		priorHolder := ""
		if exists {
			priorHolder = current.Holder
			if current.TTLSeconds > 0 {
				ttlSecs = current.TTLSeconds
			}
		}
		stolen = newLease(newHolder, ttlSecs, now)
		stolen.PriorHolder = priorHolder
		stolen.StealReason = reason
		stolen.StolenAt = now.UTC().Format(time.RFC3339)
		if prepare != nil {
			if err := prepare(stolen); err != nil {
				return err
			}
		}
		return writeLeaseAtomic(statePath, stolen)
	})
	if err != nil {
		return Lease{}, err
	}
	return stolen, nil
}

// Status returns the current lease read-model at now. A missing lease is a clean "nothing held".
func Status(home string, now time.Time) (StatusResult, error) {
	result := StatusResult{SchemaVersion: SchemaVersion}
	err := withLock(home, func() error {
		statePath, sErr := statePath(home)
		if sErr != nil {
			return sErr
		}
		current, exists, rErr := readLease(statePath)
		if rErr != nil {
			return rErr
		}
		if !exists {
			return nil
		}
		result.Holder = current.Holder
		result.AcquiredAt = current.AcquiredAt
		result.ExpiresAt = current.ExpiresAt
		result.TTLSeconds = current.TTLSeconds
		result.PriorHolder = current.PriorHolder
		result.StealReason = current.StealReason
		result.StolenAt = current.StolenAt
		result.TerminationRequestedBy = current.TerminationRequestedBy
		result.TerminationRequestReason = current.TerminationRequestReason
		result.TerminationRequestedAt = current.TerminationRequestedAt
		result.RemainingSeconds = current.remainingSeconds(now)
		result.Held = current.live(now)
		result.IsExpired = exists && !current.live(now)
		return nil
	})
	if err != nil {
		return StatusResult{}, err
	}
	return result, nil
}

// newLease builds a CLEAN lease record (no steal fields) for holder, timed from now. Timestamps are
// canonicalized to UTC RFC3339, and ExpiresAt is derived from the same whole-second TTL that is stored,
// so ttl_seconds and expires_at can never disagree.
func newLease(holder string, ttlSecs int64, now time.Time) Lease {
	nowUTC := now.UTC()
	return Lease{
		SchemaVersion: SchemaVersion,
		Holder:        holder,
		AcquiredAt:    nowUTC.Format(time.RFC3339),
		TTLSeconds:    ttlSecs,
		ExpiresAt:     nowUTC.Add(time.Duration(ttlSecs) * time.Second).Format(time.RFC3339),
	}
}

// withLock runs fn while holding an EXCLUSIVE advisory lock on the home's land-lease lock file, so a
// read-modify-write of the state JSON is atomic across separate processes/harnesses. The lock is
// released with the descriptor even on a crash (advisory flock semantics). Mirrors
// home.WithAutonomyAdmissionLock; Darwin + Linux are compound's supported runtime targets.
func withLock(home string, fn func() error) error {
	home = strings.TrimSpace(home)
	if home == "" {
		return fmt.Errorf("COMPOUND_HOME required")
	}
	lockPath := filepath.Join(home, lockRelPath)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return fn()
}

func statePath(home string) (string, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		return "", fmt.Errorf("COMPOUND_HOME required")
	}
	return filepath.Join(home, stateRelPath), nil
}

// readLease reads the current lease. A missing file is (nil, false, nil) — no lease held. A malformed
// file is a hard error (the caller holds the lock, so it is not a torn write): a broken lease must be
// surfaced, not silently overwritten.
func readLease(statePath string) (*Lease, bool, error) {
	data, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var lease Lease
	if err := json.Unmarshal(data, &lease); err != nil {
		return nil, false, fmt.Errorf("parse land-lease state %s: %w", statePath, err)
	}
	return &lease, true, nil
}

// writeLeaseAtomic writes the lease via a temp file + rename so a reader (or a crash) never sees a
// torn record. The caller already holds the exclusive flock, so no concurrent writer can race the
// rename; the temp+rename only guards against a half-written file being observed.
func writeLeaseAtomic(statePath string, lease Lease) error {
	data, err := json.MarshalIndent(lease, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(statePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "land-lease-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed away; cleans up on any error path
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, statePath); err != nil {
		return err
	}
	return syncDirectory(dir)
}

// AcquireWithExecutionBarrierPrepared is AcquireWithOutcomePrepared plus an
// inherited process-tree barrier. Ordinary acquire and steal consult the same
// barrier before replacing this holder.
func AcquireWithExecutionBarrierPrepared(
	home, holder string,
	ttl time.Duration,
	now time.Time,
	prepare func(AcquireOutcome) error,
) (AcquireOutcome, *ExecutionBarrier, error) {
	if prepare == nil {
		return AcquireOutcome{}, nil, fmt.Errorf("land-lease acquire prepare callback required")
	}
	var barrier *ExecutionBarrier
	outcome, err := acquireWithOutcomePrepared(
		home,
		holder,
		ttl,
		now,
		func(outcome AcquireOutcome) error {
			var err error
			barrier, err = lockExecutionBarrier(home, holder)
			if err != nil {
				return err
			}
			return prepare(outcome)
		},
	)
	if err != nil {
		if barrier != nil {
			_ = barrier.Close()
		}
		return AcquireOutcome{}, nil, err
	}
	return outcome, barrier, nil
}

// StealExpectedWithExecutionBarrierPrepared performs an exact governed steal
// only after the predecessor execution barrier is released, and installs a
// new inherited barrier before the successor lease becomes visible.
func StealExpectedWithExecutionBarrierPrepared(
	home, newHolder, expectedPriorHolder, reason string,
	now time.Time,
	prepare func(Lease) error,
) (Lease, *ExecutionBarrier, error) {
	newHolder = strings.TrimSpace(newHolder)
	expectedPriorHolder = strings.TrimSpace(expectedPriorHolder)
	reason = strings.TrimSpace(reason)
	if newHolder == "" || expectedPriorHolder == "" || reason == "" || prepare == nil {
		return Lease{}, nil, fmt.Errorf("land-lease governed barrier steal requires holder, expected prior holder, reason, and prepare callback")
	}
	var stolen Lease
	var barrier *ExecutionBarrier
	err := withLock(home, func() error {
		statePath, err := statePath(home)
		if err != nil {
			return err
		}
		current, exists, err := readLease(statePath)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("land-lease expected holder %q, but no lease exists", expectedPriorHolder)
		}
		if current.Holder != expectedPriorHolder {
			return fmt.Errorf("land-lease holder changed from expected %q to %q; refusing governed steal", expectedPriorHolder, current.Holder)
		}
		pending, err := executionBarrierPending(home, current.Holder)
		if err != nil {
			return err
		}
		if pending {
			return &ExecutionPendingError{Holder: current.Holder}
		}
		ttlSecs := current.TTLSeconds
		if ttlSecs <= 0 {
			ttlSecs = DefaultTTLSeconds
		}
		stolen = newLease(newHolder, ttlSecs, now)
		stolen.PriorHolder = current.Holder
		stolen.StealReason = reason
		stolen.StolenAt = now.UTC().Format(time.RFC3339)
		barrier, err = lockExecutionBarrier(home, newHolder)
		if err != nil {
			return err
		}
		if err := prepare(stolen); err != nil {
			return err
		}
		return writeLeaseAtomic(statePath, stolen)
	})
	if err != nil {
		if barrier != nil {
			_ = barrier.Close()
		}
		return Lease{}, nil, err
	}
	return stolen, barrier, nil
}

func lockExecutionBarrier(home, holder string) (*ExecutionBarrier, error) {
	id := executionBarrierID(holder)
	dir := filepath.Join(home, executionBarrierRelPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, id+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock land-lease execution barrier %s: %w", id, err)
	}
	return &ExecutionBarrier{id: id, file: file}, nil
}

func executionBarrierID(holder string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(holder)))
	return fmt.Sprintf("lleb_%x", sum[:12])
}

func executionBarrierPending(home, holder string) (bool, error) {
	holder = strings.TrimSpace(holder)
	if holder == "" {
		return false, nil
	}
	path := filepath.Join(home, executionBarrierRelPath, executionBarrierID(holder)+".lock")
	file, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if errors.Is(err, os.ErrNotExist) {
		if strings.HasPrefix(holder, "proof:") {
			return false, &ExecutionUnattestedError{Holder: holder}
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return true, nil
		}
		return false, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
		return false, err
	}
	return false, nil
}

// AttestExecutionTerminated is the trusted production evidence producer. It
// succeeds only for an installed exact barrier after the kernel permits an
// exclusive nonblocking lock, which means the final inherited descriptor has
// closed.
func AttestExecutionTerminated(home, holder string) (ExecutionTerminationAttestation, error) {
	pending, err := executionBarrierPending(home, holder)
	if err != nil {
		return ExecutionTerminationAttestation{}, err
	}
	if pending {
		return ExecutionTerminationAttestation{}, &ExecutionPendingError{Holder: holder}
	}
	return ExecutionTerminationAttestation{
		BarrierID: executionBarrierID(holder),
		Holder:    strings.TrimSpace(holder),
		Producer:  "compound.landlease.execution-barrier",
	}, nil
}

// RequestExecutionTermination records an auditable cancellation request while
// leaving the current holder and its barrier intact. A cooperative proof
// supervisor observes the request, cancels and waits for its process group,
// and only then can the requester retry the governed transfer.
func RequestExecutionTermination(
	home, expectedHolder, requestedBy, reason string,
	now time.Time,
) (Lease, error) {
	expectedHolder = strings.TrimSpace(expectedHolder)
	requestedBy = strings.TrimSpace(requestedBy)
	reason = strings.TrimSpace(reason)
	if expectedHolder == "" || requestedBy == "" || reason == "" {
		return Lease{}, fmt.Errorf("land-lease termination request requires expected holder, requester, and reason")
	}
	var updated Lease
	err := withLock(home, func() error {
		statePath, err := statePath(home)
		if err != nil {
			return err
		}
		current, exists, err := readLease(statePath)
		if err != nil {
			return err
		}
		if !exists || current.Holder != expectedHolder {
			return fmt.Errorf("land-lease holder changed while requesting termination of %q", expectedHolder)
		}
		current.TerminationRequestedBy = requestedBy
		current.TerminationRequestReason = reason
		current.TerminationRequestedAt = now.UTC().Format(time.RFC3339Nano)
		updated = *current
		return writeLeaseAtomic(statePath, updated)
	})
	return updated, err
}

// ExecutionPending exposes the shared read-only process-tree barrier probe to
// sweep and UI projections. It never changes lease or barrier state.
func ExecutionPending(home, holder string) (bool, error) {
	return executionBarrierPending(home, holder)
}

func syncDirectory(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) {
		return err
	}
	return nil
}
