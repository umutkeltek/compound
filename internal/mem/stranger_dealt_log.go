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
	"os"
	"path/filepath"
	"strings"
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

// AppendStrangerDealtRow appends one dealt-card row to the JSONL log, creating the parent
// directory if needed. It never writes to the ledger, candidates/, or claims/ — the same
// write-boundary invariant (F3) the S3a report artifact honors.
func AppendStrangerDealtRow(homePath string, row StrangerDealtRow) error {
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
