package mem

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Evidence source kinds are OPEN by design: any medium may record evidence through the
// generic knowledge intake (new medium = 1 edge + 0 core changes), so there is no allowlist.
// A kind is a lowercase slug so provenance stays greppable and typo-resistant in shape.
var evidenceSourceKindPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]*$`)

// EvidenceItem is immutable, citable raw material — never truth on its own, but the thing a claim
// points at. The spine-native convergence of the Gen-1 memory.EvidenceItem: mem previously kept
// only a SnippetHash on the candidate, with no stored, retrievable evidence object. The id is
// CONTENT-ADDRESSED, so recording the same evidence twice is idempotent and an evidence ref is stable.
type EvidenceItem struct {
	ID          string `json:"id"` // ev_<content-hash prefix>
	SourceKind  string `json:"source_kind"`
	SourceID    string `json:"source_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	QuoteText   string `json:"quote_text,omitempty"`
	ContentHash string `json:"content_hash"`
	EventTime   string `json:"event_time,omitempty"` // when the discussed thing happened
	ObservedAt  string `json:"observed_at"`          // when compound ingested it
	Actor       string `json:"actor,omitempty"`      // user | assistant | tool | system | agent
	Visibility  string `json:"visibility,omitempty"`
}

func (s *Store) evidenceLog() string { return s.home + "/ledger/evidence.jsonl" }

// RecordEvidence stores a citable evidence item, content-addressed and idempotent (same
// source+content → same id → no duplicate append). Returns the stored item (with id).
func (s *Store) RecordEvidence(e EvidenceItem, now time.Time) (EvidenceItem, error) {
	if strings.TrimSpace(e.QuoteText) == "" && strings.TrimSpace(e.SourceID) == "" {
		return EvidenceItem{}, fmt.Errorf("evidence needs quote_text or source_id")
	}
	e.SourceKind = strings.TrimSpace(e.SourceKind)
	if e.SourceKind == "" {
		return EvidenceItem{}, fmt.Errorf("evidence source_kind required")
	}
	if !evidenceSourceKindPattern.MatchString(e.SourceKind) {
		return EvidenceItem{}, fmt.Errorf("evidence source_kind %q must be a lowercase slug (a-z0-9._:-)", e.SourceKind)
	}
	sum := sha256.Sum256([]byte(e.SourceKind + "\x00" + e.SourceID + "\x00" + e.QuoteText))
	e.ContentHash = hex.EncodeToString(sum[:])
	e.ID = "ev_" + e.ContentHash[:12]
	if e.Visibility == "" {
		e.Visibility = "operational"
	}
	if existing, ok, err := s.GetEvidence(e.ID); err != nil {
		return EvidenceItem{}, err
	} else if ok {
		return existing, nil // idempotent: identical evidence already recorded
	}
	e.ObservedAt = rfc3339(now)
	return e, appendJSONL(s.evidenceLog(), &e)
}

// Evidence loads all evidence items (append-only, last-wins by id, deterministic order).
func (s *Store) Evidence() ([]EvidenceItem, error) {
	byID := map[string]EvidenceItem{}
	var order []string
	err := readJSONL(s.evidenceLog(), func(line []byte) error {
		var e EvidenceItem
		if json.Unmarshal(line, &e) != nil || e.ID == "" {
			return nil
		}
		if _, seen := byID[e.ID]; !seen {
			order = append(order, e.ID)
		}
		byID[e.ID] = e
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]EvidenceItem, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out, nil
}

// GetEvidence returns one evidence item by id.
func (s *Store) GetEvidence(id string) (EvidenceItem, bool, error) {
	items, err := s.Evidence()
	if err != nil {
		return EvidenceItem{}, false, err
	}
	for _, e := range items {
		if e.ID == id {
			return e, true, nil
		}
	}
	return EvidenceItem{}, false, nil
}
