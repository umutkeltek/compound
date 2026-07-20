package runattempt

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Status enumerates the lifecycle of a RunAttempt. S1 only ever produces "pending" /
// "running" / "succeeded" / "failed"; later slices add heartbeat-driven transitions and
// terminal proof attachment.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

// HarnessPlaceholder is the stand-in harness identifier used by writers that do not yet have a
// real harness adapter (today: panel.Apply). It is a named constant rather than an open string so
// the future harness-adapter slice has a single replacement site; the deferred context_pack_ref /
// workspace / command / heartbeat / transcript_ref / diff_ref / test_ref / proof_refs fields
// (see the package doc in id.go) land with that adapter, not before.
const HarnessPlaceholder = "fake"

// ErrNotFound is returned when a Get lookup cannot resolve the requested id.
var ErrNotFound = errors.New("runattempt not found")

// ErrAlreadyBound is returned by AttachActionIntent when the RunAttempt is already bound to a
// DIFFERENT ActionIntent. Attaching the same intent twice is a no-op success (retry-safe); a
// mismatched rebind is a bug, not a refresh.
var ErrAlreadyBound = errors.New("runattempt already bound to a different action_intent_id")

// RunAttempt is the S1 type. Fields are deliberately minimal: enough to bind a RunAttempt to
// a work_id and to record who/what ran and when. Transcript, diff, test, proof refs are not
// part of S1 -- they land when a real harness adapter actually produces them.
type RunAttempt struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	WorkID        string `json:"work_id"`
	// ActionIntentID binds this RunAttempt to the ActionIntent that authorized and represents it,
	// closing the attempt→intent→policy-decision audit chain. Empty on Mint (Mint runs before the
	// Advance that creates the ActionIntent — see panel.Apply); attached post-Advance via
	// AttachActionIntent. omitempty so pre-existing ratt_ records re-marshal byte-identical.
	ActionIntentID string    `json:"action_intent_id,omitempty"`
	Actor          string    `json:"actor"`
	Harness        string    `json:"harness"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	CompletedAt    time.Time `json:"completed_at,omitempty"`
}

// Store persists RunAttempts under <home>/run_attempts/<id>.json with atomic rename and a
// per-process index cache so multiple Get calls in one tick don't re-read disk.
type Store struct {
	home string

	mu    sync.RWMutex
	cache map[string]RunAttempt
}

// Open returns a Store rooted at home. It does NOT create directories -- Get / Complete
// lazily mkdir on first write, matching the pattern used by internal/landlease and
// internal/work so a fresh home stays clean until something is actually persisted.
func Open(home string) *Store {
	return &Store{home: home, cache: map[string]RunAttempt{}}
}

// Mint creates and persists a new RunAttempt bound to workID, returning the stored artifact.
// The id format is enforced by Valid -- if Mint ever produces a non-conforming id (regression
// guard), the validator will reject it before it reaches the envelope.
func (s *Store) Mint(workID, actor, harness string, now time.Time) (RunAttempt, error) {
	if strings.TrimSpace(workID) == "" {
		return RunAttempt{}, fmt.Errorf("runattempt mint: work_id is required")
	}
	if strings.TrimSpace(actor) == "" {
		return RunAttempt{}, fmt.Errorf("runattempt mint: actor is required")
	}
	if strings.TrimSpace(harness) == "" {
		return RunAttempt{}, fmt.Errorf("runattempt mint: harness is required")
	}
	id, err := Mint(now)
	if err != nil {
		return RunAttempt{}, err
	}
	ra := RunAttempt{
		SchemaVersion: SchemaVersion,
		ID:            id,
		WorkID:        workID,
		Actor:         actor,
		Harness:       harness,
		Status:        StatusPending,
		CreatedAt:     now.UTC(),
	}
	if err := s.write(ra); err != nil {
		return RunAttempt{}, err
	}
	return ra, nil
}

// Get returns the RunAttempt with the given id, loading from disk if not yet cached.
func (s *Store) Get(id string) (RunAttempt, error) {
	if !Valid(id) {
		return RunAttempt{}, fmt.Errorf("runattempt get: id %q does not match %s pattern", id, Prefix)
	}
	s.mu.RLock()
	if ra, ok := s.cache[id]; ok {
		s.mu.RUnlock()
		return ra, nil
	}
	s.mu.RUnlock()
	path := s.path(id)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return RunAttempt{}, ErrNotFound
		}
		return RunAttempt{}, fmt.Errorf("runattempt get %s: %w", id, err)
	}
	var ra RunAttempt
	if err := json.Unmarshal(data, &ra); err != nil {
		return RunAttempt{}, fmt.Errorf("runattempt get %s: decode: %w", id, err)
	}
	s.mu.Lock()
	s.cache[id] = ra
	s.mu.Unlock()
	return ra, nil
}

// Complete transitions a RunAttempt to terminal status (succeeded or failed) and records
// the completion timestamp. Status must be one of StatusSucceeded or StatusFailed; pending
// and running are not terminal and Complete refuses them.
func (s *Store) Complete(id string, status string, now time.Time) (RunAttempt, error) {
	if status != StatusSucceeded && status != StatusFailed {
		return RunAttempt{}, fmt.Errorf("runattempt complete %s: status %q is not terminal", id, status)
	}
	ra, err := s.Get(id)
	if err != nil {
		return RunAttempt{}, err
	}
	if ra.Status == StatusSucceeded || ra.Status == StatusFailed {
		return RunAttempt{}, fmt.Errorf("runattempt complete %s: already terminal (%s)", id, ra.Status)
	}
	ra.Status = status
	ra.CompletedAt = now.UTC()
	if err := s.write(ra); err != nil {
		return RunAttempt{}, err
	}
	return ra, nil
}

// AttachActionIntent binds a RunAttempt to the ActionIntent that authorized it, closing the
// attempt→intent→policy-decision audit chain (the RunAttempt otherwise binds to WorkID only).
//
// It is separate from Complete on purpose: binding is retry-idempotent and may run more than once
// (panel.Apply recovers the intent id from the advanced envelope's last ActionIntentIDs element),
// whereas Complete is a one-shot terminal transition. Mint stays before Advance (panel/apply.go),
// preserving the intentional ordering that puts the run id on the envelope before any artifact is
// written; AttachActionIntent is the post-Advance step that adds the binding.
//
// Behavior:
//   - id must be a Valid ratt_ id (else a wrapped error).
//   - actionIntentID must have the act_ prefix (mirrors the ratt_ rule philosophy).
//   - If the RunAttempt is already bound to the SAME actionIntentID, this is a no-op success.
//   - If it is bound to a DIFFERENT actionIntentID, returns ErrAlreadyBound.
//   - If the RunAttempt is already terminal (succeeded/failed), returns an error — binding after
//     terminalization is a bug.
//
// Known limitation: a single Advance carrying multiple RunAttemptIDs would bind all of them to
// the same act_ id. Today only panel.Apply writes RunAttempts and it mints one per Advance, so
// this is a non-issue; revisit when a real multi-attempt harness adapter lands.
func (s *Store) AttachActionIntent(id, actionIntentID string) error {
	if !Valid(id) {
		return fmt.Errorf("runattempt attach %s: id does not match %s pattern", id, Prefix)
	}
	if !strings.HasPrefix(actionIntentID, "act_") {
		return fmt.Errorf("runattempt attach %s: action_intent_id %q must start with act_", id, actionIntentID)
	}
	ra, err := s.Get(id)
	if err != nil {
		return err
	}
	if ra.Status == StatusSucceeded || ra.Status == StatusFailed {
		return fmt.Errorf("runattempt attach %s: already terminal (%s); binding after terminalization is a bug", id, ra.Status)
	}
	if ra.ActionIntentID == actionIntentID {
		return nil // idempotent retry
	}
	if ra.ActionIntentID != "" {
		return fmt.Errorf("runattempt attach %s: %w (have %q, want %q)", id, ErrAlreadyBound, ra.ActionIntentID, actionIntentID)
	}
	ra.ActionIntentID = actionIntentID
	return s.write(ra)
}

func (s *Store) path(id string) string {
	return filepath.Join(s.home, "run_attempts", id+".json")
}

// write persists ra atomically (write to .tmp, fsync, rename) so a partial write cannot
// leave a half-decoded file in place. Same shape as internal/work.eventLogWrites and
// internal/landlease.writeAtomic so a crash mid-write loses at most one RunAttempt.
func (s *Store) write(ra RunAttempt) error {
	dir := filepath.Join(s.home, "run_attempts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("runattempt write: mkdir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(ra, "", "  ")
	if err != nil {
		return fmt.Errorf("runattempt write: encode %s: %w", ra.ID, err)
	}
	tmp := s.path(ra.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("runattempt write: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path(ra.ID)); err != nil {
		return fmt.Errorf("runattempt write: rename %s: %w", tmp, err)
	}
	s.mu.Lock()
	s.cache[ra.ID] = ra
	s.mu.Unlock()
	return nil
}
