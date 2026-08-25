package mem

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PendingState is the reconciled pending-inbox count — the honest backlog
// signal. The raw candidate directory mixes open (proposed) candidates with
// every candidate already closed (parked / promoted / rejected /
// no_promote), so a file count read as "backlog" is a fiction (lived
// 2026-08-25: doctor reported 133k backlog; 34 were actually pending).
//
// PendingState is written ONLY by a full-budget walk that read every inbox
// entry — the promote-driver sweep at walkedAll, or an explicit reconcile —
// never per-write deltas: candidate files have too many writers (capture,
// every adjudication door, page import, stable-candidate adapters) for a
// counter maintained at the write sites to stay truthful. Between
// reconciliations the state is advisory; AsOf names its age so a reader can
// tell a fresh count from a stale one.
type PendingState struct {
	Pending      int    `json:"pending"`                 // candidates in state proposed at reconciliation time
	Files        int    `json:"files"`                   // total candidate files in the inbox directory
	Corrupt      int    `json:"corrupt,omitempty"`       // inbox files unreadable at reconciliation time
	AsOf         string `json:"as_of"`                   // RFC3339 reconciliation time
	ReconciledBy string `json:"reconciled_by,omitempty"` // who reconciled ("promote-driver sweep", "manual", ...)
}

func (s *Store) pendingStatePath() string {
	return filepath.Join(s.home, "state", "inbox-pending.json")
}

// ReadPendingState returns the last reconciled pending count. A missing file
// is not an error in the caller's sense — it means "never reconciled" — but
// it is returned as one here so callers must consciously choose their
// fallback; the sentinel is os.IsNotExist-checkable via the wrapped path
// error readJSON produces ("not found: inbox-pending.json").
func (s *Store) ReadPendingState() (PendingState, error) {
	var st PendingState
	err := readJSON(s.pendingStatePath(), &st)
	return st, err
}

// WritePendingState atomically replaces the reconciled state (stage to a
// temp file in the same directory, rename over). A torn state file must
// never read as a confident-but-wrong count, so the write is atomic even
// though the value itself is advisory between reconciliations.
func (s *Store) WritePendingState(st PendingState) error {
	path := s.pendingStatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".inbox-pending-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(b); err != nil {
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
	return os.Rename(tmpName, path)
}

// ReconcilePendingState walks the full inbox exactly once and records the
// true pending count. The census decodes ONLY each candidate's state field —
// a full Candidate decode of a 133k inbox just to count proposed entries was
// the cost this reconciliation exists to avoid — and one unreadable file is
// counted as corrupt, never fatal: the reconciliation must not abort where
// the bounded sweep isolates. Callers are the promote-driver sweep (already
// walking) and explicit maintenance, never the doctor.
func (s *Store) ReconcilePendingState(reconciledBy string, now time.Time) (PendingState, error) {
	pending, files, corrupt, err := s.pendingInboxCensus()
	if err != nil {
		return PendingState{}, err
	}
	st := PendingState{
		Pending:      pending,
		Files:        files,
		Corrupt:      corrupt,
		AsOf:         rfc3339(now),
		ReconciledBy: reconciledBy,
	}
	if err := s.WritePendingState(st); err != nil {
		return PendingState{}, err
	}
	return st, nil
}

// pendingInboxCensus counts the inbox in one bounded-memory walk: per file it
// decodes only {"state"}, counting proposed entries. An absent inbox is an
// empty census, not an error. files counts every .json entry; corrupt counts
// the entries that would not decode.
func (s *Store) pendingInboxCensus() (pending, files, corrupt int, err error) {
	dirEntries, err := os.ReadDir(s.candDir())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, 0, 0, nil
		}
		return 0, 0, 0, err
	}
	var names []string
	for _, entry := range dirEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		files++
		var state struct {
			State string `json:"state"`
		}
		if err := readJSON(filepath.Join(s.candDir(), name), &state); err != nil {
			corrupt++
			continue
		}
		if state.State == "proposed" {
			pending++
		}
	}
	return pending, files, corrupt, nil
}

// pendingStateFresh reports whether a reconciled pending count is still
// authoritative for the doctor: reconciled recently enough AND against the
// SAME inbox generation. Two independent lies defeat a stale count: age (the
// reconciliation predates the freshness window) and corpus movement (the
// file count has changed since — new candidates arrived or files vanished,
// so the recorded pending number no longer describes this inbox). Either
// flips the count to "unknown" so the doctor warns instead of re-serving a
// confident-but-wrong backlog. An unparseable AsOf is unknown by definition.
func pendingStateFresh(st PendingState, fileCount int, now time.Time) bool {
	asOf, err := time.Parse(time.RFC3339, strings.TrimSpace(st.AsOf))
	if err != nil {
		return false
	}
	if now.Sub(asOf) > memDoctorThresholds.PendingStateFreshFor {
		return false
	}
	return st.Files == fileCount
}
