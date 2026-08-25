package mem

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CandidateLite is the narrow decode shape the promote-driver walker needs:
// enough to decide basis-gated promotion and exact duplicate collapse
// WITHOUT the full Candidate decode. Fields mirror the candidate JSON;
// anything absent here is re-read live by the mutation doors (Adjudicate
// re-reads before writing, so a lite-driven decision never writes on stale
// data — the door re-validates against the live file).
type CandidateLite struct {
	ID             string            `json:"id"`
	State          string            `json:"state"`
	PromotionBasis string            `json:"promotion_basis,omitempty"`
	Entity         string            `json:"entity"`
	Predicate      string            `json:"predicate"`
	Object         string            `json:"object"`
	CreatedAt      string            `json:"created_at"`
	Qualifiers     map[string]string `json:"qualifiers,omitempty"`
}

// ListCandidatesLite walks the inbox in stable id order decoding ONLY the
// lite fields, bounded by a scan budget and resumable by cursor — the same
// discipline as pendingInboxSnapshot, widened for the promote-driver's
// pre-sweep phases (basis promotion and exact duplicate collapse must see
// proposed candidates before the disposition sweep closes them).
//
// corrupt names undecodable files (never silently skipped, never aborts the
// walk). lastWalked is the last directory entry walked — valid or corrupt —
// and atEnd reports whether the walk reached the directory's final entry.
// Pagination progress and candidate validity are separate dimensions: a
// cursor keyed on decoded candidates alone traps a pass on a page ending in
// an unreadable file.
func (s *Store) ListCandidatesLite(afterID string, scanLimit int) (entries []CandidateLite, corrupt []string, lastWalked string, atEnd bool, err error) {
	dirEntries, err := os.ReadDir(s.candDir())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// An absent inbox (read-only open on a home that never captured)
			// is an empty walk, not a failure.
			return nil, nil, "", true, nil
		}
		return nil, nil, "", false, err
	}
	var names []string
	for _, entry := range dirEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, nil, "", true, nil
	}
	lastID := strings.TrimSuffix(names[len(names)-1], ".json")
	if afterID != "" && lastID <= afterID {
		return nil, nil, "", true, nil
	}
	budget := scanLimit
	for _, name := range names {
		if budget == 0 && scanLimit > 0 {
			break
		}
		fileID := strings.TrimSuffix(name, ".json")
		if afterID != "" && fileID <= afterID {
			continue
		}
		var lite CandidateLite
		if err := readJSON(filepath.Join(s.candDir(), name), &lite); err != nil {
			corrupt = append(corrupt, fileID)
		} else {
			entries = append(entries, lite)
		}
		lastWalked = fileID
		atEnd = fileID == lastID
		if scanLimit > 0 {
			budget--
		}
	}
	return entries, corrupt, lastWalked, atEnd, nil
}
