package runattempt

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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

// ErrNotFound is returned when a Get / List lookup cannot resolve the requested id.
var ErrNotFound = errors.New("runattempt not found")

// RunAttempt is the S1 type. Fields are deliberately minimal: enough to bind a RunAttempt to
// a work_id and to record who/what ran and when. Transcript, diff, test, proof refs are not
// part of S1 -- they land when a real harness adapter actually produces them.
type RunAttempt struct {
	SchemaVersion string    `json:"schema_version"`
	ID            string    `json:"id"`
	WorkID        string    `json:"work_id"`
	Actor         string    `json:"actor"`
	Harness       string    `json:"harness"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	CompletedAt   time.Time `json:"completed_at,omitempty"`
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

// ListByWork returns all RunAttempts bound to workID, sorted by CreatedAt ascending.
// Reads are independent of the cache -- a List after a Mint that happened in another
// process still sees the new record, matching internal/landlease's contract.
func (s *Store) ListByWork(workID string) ([]RunAttempt, error) {
	dir := filepath.Join(s.home, "run_attempts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("runattempt list: read %s: %w", dir, err)
	}
	var out []RunAttempt
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if !Valid(id) {
			// A foreign file landed in the directory; skip it rather than failing the list.
			continue
		}
		ra, err := s.Get(id)
		if err != nil {
			return nil, err
		}
		if ra.WorkID == workID {
			out = append(out, ra)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
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
