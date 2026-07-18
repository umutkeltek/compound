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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StrangerDealtLogRelPath is the home-relative path of the append-only JSONL dealt-log (D5).
const StrangerDealtLogRelPath = "state/runs/stranger-card-deals.jsonl"

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
	path := strangerDealtLogPath(homePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(&row)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

// FinalizeStrangerDeal is the single production commit door for a prepared stranger card.
// Candidate state and cooldown are re-read while Store's cross-process lock is held, then the
// row is appended inside the same critical section. The bool is false when another orient won
// the cooldown race or the candidate stopped being proposed/parked; callers must suppress the
// prepared card in either case.
func (s *Store) FinalizeStrangerDeal(row StrangerDealtRow, now time.Time, cooldown time.Duration) (bool, error) {
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
		if err := appendStrangerDealtRow(s.home, row); err != nil {
			return fmt.Errorf("append stranger deal: %w", err)
		}
		finalized = true
		return nil
	})
	return finalized, err
}
