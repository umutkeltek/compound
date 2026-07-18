// stranger_dealt_log.go — work_c13acdc090b5 (S3b, D5): the weak-tie "stranger card" dealt-log.
//
// The dealer (cmd/compound/orient_operator.go, --operator path only) picks which un-promoted
// candidate to deal; this file is the shared, ledger-adjacent-never artifact both sides read
// and write: the dealer reads it to honor the 7-day re-deal cooldown (F7) and appends to it
// after a successful deal, and the S3a consumption report (cause_consumption.go) reads it to
// join dealt candidates against later Cause-transitions (D2/F8's dealt->caused kill-condition
// meter). Same write-class as the S3a report artifact: state/runs/, never the ledger.
package mem

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StrangerDealtLogRelPath is the home-relative path of the append-only JSONL dealt-log (D5).
const StrangerDealtLogRelPath = "state/runs/stranger-card-deals.jsonl"

// ErrStrangerDealPairedCommit distinguishes a cursor-side failure from a deal-log failure so
// the operator report can preserve its cursor error contract after compensating the append.
var ErrStrangerDealPairedCommit = errors.New("stranger deal paired commit failed")

// StrangerDealtRow is one dealt stranger card: ts is the deal time (RFC3339 UTC), card_id is
// the orientTensionCard.ID the operator saw, candidate_ref is the dealt Candidate.ID, and topic
// is that candidate's own topic (CandidateListItem.Topic()) at deal time.
type StrangerDealtRow struct {
	Ts           string `json:"ts"`
	CardID       string `json:"card_id"`
	CandidateRef string `json:"candidate_ref"`
	Topic        string `json:"topic,omitempty"`
}

func strangerDealtLogPath(homePath string) string {
	return filepath.Join(homePath, filepath.FromSlash(StrangerDealtLogRelPath))
}

// ReadStrangerDealtLog reads every dealt-card row ever appended. A log that does not exist yet
// (no stranger card has ever been dealt) returns an empty slice, nil error — the normal
// first-run case. A present-but-corrupt log (a line that fails to parse as StrangerDealtRow)
// returns a non-nil error so callers can fail closed: the dealer degrades to NO card + no crash
// (F7) rather than risk silently ignoring a row that might be holding an active cooldown, and
// the S3a report degrades to skipping the dealt->caused join for that run rather than bricking
// the whole consumption sweep.
func ReadStrangerDealtLog(homePath string) ([]StrangerDealtRow, error) {
	data, err := os.ReadFile(strangerDealtLogPath(homePath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	out := make([]StrangerDealtRow, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row StrangerDealtRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// appendStrangerDealtRow is the raw append primitive used by FinalizeStrangerDeal while its
// Store lock is held, plus same-package fixtures that seed the consumption join.
func appendStrangerDealtRow(homePath string, row StrangerDealtRow) error {
	_, err := appendStrangerDealtRowReversible(homePath, row)
	return err
}

// appendStrangerDealtRowReversible appends one durable row and returns the compensating
// operation needed by FinalizeStrangerDeal when the caller's paired commit fails. Store's
// memory lock prevents another production deal append from interleaving before compensation.
func appendStrangerDealtRowReversible(homePath string, row StrangerDealtRow) (func() error, error) {
	path := strangerDealtLogPath(homePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	priorSize := int64(0)
	priorExists := false
	if info, err := os.Stat(path); err == nil {
		priorSize = info.Size()
		priorExists = true
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	b, err := json.Marshal(&row)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	payload := append(b, '\n')
	n, writeErr := f.Write(payload)
	syncErr := f.Sync()
	closeErr := f.Close()
	rollback := func() error {
		if priorExists {
			return os.Truncate(path, priorSize)
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if writeErr != nil || n != len(payload) || syncErr != nil || closeErr != nil {
		writeFailure := writeErr
		switch {
		case writeFailure == nil && n != len(payload):
			writeFailure = fmt.Errorf("short write: wrote %d of %d bytes", n, len(payload))
		case writeFailure == nil && syncErr != nil:
			writeFailure = syncErr
		case writeFailure == nil:
			writeFailure = closeErr
		}
		if rollbackErr := rollback(); rollbackErr != nil {
			return nil, fmt.Errorf("append stranger deal: %w; rollback append: %v", writeFailure, rollbackErr)
		}
		return nil, writeFailure
	}
	return rollback, nil
}

// FinalizeStrangerDeal is the single production commit door for a prepared stranger card.
// Candidate state and cooldown are re-read while Store's cross-process lock is held, then the
// row is appended inside the same critical section. The bool is false when another orient won
// the cooldown race or the candidate stopped being proposed/parked; callers must suppress the
// prepared card in either case.
func (s *Store) FinalizeStrangerDeal(row StrangerDealtRow, now time.Time, cooldown time.Duration, pairedCommit ...func() error) (bool, error) {
	candidateRef := strings.TrimSpace(row.CandidateRef)
	if candidateRef == "" {
		return false, fmt.Errorf("stranger deal candidate_ref is required")
	}
	if strings.TrimSpace(row.CardID) == "" {
		return false, fmt.Errorf("stranger deal card_id is required")
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(row.Ts)); err != nil {
		return false, fmt.Errorf("stranger deal ts: %w", err)
	}
	if now.IsZero() {
		return false, fmt.Errorf("stranger deal finalize time is required")
	}
	if cooldown <= 0 {
		return false, fmt.Errorf("stranger deal cooldown must be positive")
	}
	if len(pairedCommit) > 1 || (len(pairedCommit) == 1 && pairedCommit[0] == nil) {
		return false, fmt.Errorf("stranger deal accepts at most one non-nil paired commit")
	}

	finalized := false
	err := s.withLock(func() error {
		candidate, err := s.getCandidate(candidateRef)
		if err != nil {
			return fmt.Errorf("revalidate stranger candidate %s: %w", candidateRef, err)
		}
		if candidate.State != "proposed" && candidate.State != "parked" {
			return nil
		}
		if topic := strings.TrimSpace(row.Topic); topic != "" && candidate.Topic() != topic {
			return nil
		}

		dealt, err := ReadStrangerDealtLog(s.home)
		if err != nil {
			return fmt.Errorf("recheck stranger cooldown: %w", err)
		}
		for _, existing := range dealt {
			if strings.TrimSpace(existing.CandidateRef) != candidateRef {
				continue
			}
			dealtAt, err := time.Parse(time.RFC3339, strings.TrimSpace(existing.Ts))
			if err != nil {
				return fmt.Errorf("recheck stranger cooldown for %s: %w", candidateRef, err)
			}
			if now.Sub(dealtAt) < cooldown {
				return nil
			}
		}
		rollback, err := appendStrangerDealtRowReversible(s.home, row)
		if err != nil {
			return fmt.Errorf("append stranger deal: %w", err)
		}
		if len(pairedCommit) == 1 {
			if err := pairedCommit[0](); err != nil {
				if rollbackErr := rollback(); rollbackErr != nil {
					return fmt.Errorf("%w: %v; rollback deal append: %v", ErrStrangerDealPairedCommit, err, rollbackErr)
				}
				return fmt.Errorf("%w: %v", ErrStrangerDealPairedCommit, err)
			}
		}
		finalized = true
		return nil
	})
	return finalized, err
}
