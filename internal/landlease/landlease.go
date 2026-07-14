// Package landlease is a single-holder, TTL-bounded, steal-with-reason MUTEX on the ACT of landing to
// main — distinct from the per-work leases in internal/work. Concurrent executor lanes (multiple
// Claude/Codex harnesses driving ONE repo) consult it to serialize the fast-forward-merge window
// instead of colliding and retrying. It is a COORDINATION HINT layer: it never mutates repo state and
// emits no PolicyDecision/Receipt — it only records WHO currently holds the right to land.
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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

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
	lockRelPath  = "lock/land-lease.lock"
	stateRelPath = "state/land-lease.json"
)

// Lease is the persisted land-lease record. The steal-audit fields (PriorHolder/StealReason/StolenAt)
// are set ONLY on a lease produced by Steal; a plain Acquire always leaves them empty.
type Lease struct {
	SchemaVersion string `json:"schema_version"`
	Holder        string `json:"holder"`
	AcquiredAt    string `json:"acquired_at"` // RFC3339 (UTC)
	TTLSeconds    int64  `json:"ttl_seconds"`
	ExpiresAt     string `json:"expires_at"` // RFC3339 (UTC)
	PriorHolder   string `json:"prior_holder,omitempty"`
	StealReason   string `json:"steal_reason,omitempty"`
	StolenAt      string `json:"stolen_at,omitempty"`
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
	SchemaVersion    string `json:"schema_version"`
	Held             bool   `json:"held"`
	Holder           string `json:"holder,omitempty"`
	AcquiredAt       string `json:"acquired_at,omitempty"`
	ExpiresAt        string `json:"expires_at,omitempty"`
	TTLSeconds       int64  `json:"ttl_seconds,omitempty"`
	RemainingSeconds int64  `json:"remaining_seconds"`
	IsExpired        bool   `json:"is_expired"`
	PriorHolder      string `json:"prior_holder,omitempty"`
	StealReason      string `json:"steal_reason,omitempty"`
	StolenAt         string `json:"stolen_at,omitempty"`
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
	holder = strings.TrimSpace(holder)
	if holder == "" {
		return Lease{}, fmt.Errorf("land-lease holder required")
	}
	ttlSecs := int64(ttl / time.Second)
	if ttlSecs <= 0 {
		return Lease{}, fmt.Errorf("land-lease ttl must be at least one second")
	}
	var acquired Lease
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
		acquired = newLease(holder, ttlSecs, now)
		return writeLeaseAtomic(statePath, acquired)
	})
	if err != nil {
		return Lease{}, err
	}
	return acquired, nil
}

// AcquireWait polls Acquire until the lease is free/held-by-us or the timeout elapses. It returns
// promptly on the first successful acquire. The poll is CHEAP — it just re-reads the lock file; there
// is no provider/LLM call. A non-"held" error (e.g. a disk error) is surfaced immediately rather than
// retried. Time advances through the injected Clock, so the loop is deterministic under a fake clock
// (production passes RealClock{}). A poll <= 0 falls back to a one-second cadence.
func AcquireWait(home, holder string, ttl time.Duration, clk Clock, timeout, poll time.Duration) (Lease, error) {
	if clk == nil {
		return Lease{}, fmt.Errorf("land-lease AcquireWait requires a clock")
	}
	if poll <= 0 {
		poll = time.Second
	}
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
		if !clk.Now().Before(deadline) {
			return Lease{}, fmt.Errorf("land-lease acquire timed out after %s waiting for holder %q: %w", timeout, held.Holder, err)
		}
		clk.Sleep(poll)
	}
}

// Release frees the lease ONLY when holder currently holds it. A release by a NON-holder is refused
// with an error (someone else's live lease is never silently dropped); a release when nothing is held
// is an idempotent Freed=false no-op. A held lease is removed (Status then shows nothing held).
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
		if err := os.Remove(statePath); err != nil {
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

// Steal transfers the lease to newHolder and RECORDS prior_holder + reason + stolen_at for audit. A
// reason is REQUIRED — this is the gate that distinguishes an auditable steal from a plain Acquire
// (which never transfers a live lease). The stolen lease inherits the prior record's TTL (or the
// package default when there is no prior record), timed from now.
func Steal(home, newHolder, reason string, now time.Time) (Lease, error) {
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
	return os.Rename(tmpName, statePath)
}
