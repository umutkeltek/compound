package mem

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const CandidateListSchemaVersion = "compound.memory_candidate_list.v0.1"

type CandidateListOptions struct {
	State           string
	Kind            string
	Limit           int
	IncludeEvidence bool
}

type CandidateListReport struct {
	SchemaVersion string                 `json:"schema_version"`
	AuthorityRef  string                 `json:"authority_ref"`
	GeneratedAt   string                 `json:"generated_at"`
	Counts        CandidateListCounts    `json:"counts"`
	Items         []CandidateListItem    `json:"items"`
	Warnings      []string               `json:"warnings,omitempty"`
	Filters       map[string]interface{} `json:"filters,omitempty"`
}

type CandidateListCounts struct {
	Total   int            `json:"total"`
	Matched int            `json:"matched"`
	ByState map[string]int `json:"by_state"`
}

type CandidateListItem struct {
	ID              string            `json:"id"`
	State           string            `json:"state"`
	ReviewState     string            `json:"review_state,omitempty"`
	Entity          string            `json:"entity,omitempty"`
	Predicate       string            `json:"predicate,omitempty"`
	Object          string            `json:"object,omitempty"`
	ObservationDate string            `json:"observation_date,omitempty"`
	Confidence      float64           `json:"confidence,omitempty"`
	Derivation      string            `json:"derivation,omitempty"`
	ClaimType       string            `json:"claim_type,omitempty"`
	Kind            string            `json:"kind,omitempty"`
	Visibility      string            `json:"visibility,omitempty"`
	PromotionBasis  string            `json:"promotion_basis,omitempty"`
	Qualifiers      map[string]string `json:"qualifiers,omitempty"`
	Reason          string            `json:"reason,omitempty"`
	// AdjudicatedBy/AdjudicatedAt mirror Candidate's operator-ratification
	// provenance (see mem.Candidate) so `memory candidates --json` — the
	// projection the console standing-law queue reads — can distinguish a
	// candidate the operator explicitly ruled on (even "Keep aside") from one
	// still sitting untouched since ingestion.
	AdjudicatedBy  string                 `json:"adjudicated_by,omitempty"`
	AdjudicatedAt  string                 `json:"adjudicated_at,omitempty"`
	CreatedAt      string                 `json:"created_at,omitempty"`
	SourceTraceRef string                 `json:"source_trace_ref,omitempty"`
	SnippetHash    string                 `json:"snippet_hash,omitempty"`
	EvidenceRefs   []string               `json:"evidence_refs,omitempty"`
	Evidence       *CandidateListEvidence `json:"evidence,omitempty"`
	BelievedBefore *CandidateListBelief   `json:"believed_before,omitempty"`
	Annotations    []CandidateAnnotation  `json:"annotations,omitempty"`
}

type CandidateListEvidence struct {
	Ref        string `json:"ref,omitempty"`
	SourceKind string `json:"source_kind,omitempty"`
	SourceID   string `json:"source_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Quote      string `json:"quote,omitempty"`
	EventTime  string `json:"event_time,omitempty"`
	ObservedAt string `json:"observed_at,omitempty"`
	Actor      string `json:"actor,omitempty"`
	Visibility string `json:"visibility,omitempty"`
}

type CandidateListBelief struct {
	ClaimID      string   `json:"claim_id,omitempty"`
	Entity       string   `json:"entity,omitempty"`
	Predicate    string   `json:"predicate,omitempty"`
	Object       string   `json:"object,omitempty"`
	ValidFrom    string   `json:"valid_from,omitempty"`
	CreatedAt    string   `json:"created_at,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

func (s *Store) ListCandidates(now time.Time, opts CandidateListOptions) (CandidateListReport, error) {
	state := strings.TrimSpace(opts.State)
	kind := strings.TrimSpace(opts.Kind)
	if state != "" && !validCandidateListState(state) {
		return CandidateListReport{}, fmt.Errorf("state must be one of proposed|parked|promoted|rejected|no_promote|needs_review, got %q", state)
	}
	if opts.Limit < 0 {
		return CandidateListReport{}, fmt.Errorf("limit must be non-negative")
	}

	cands, err := s.AllCandidates()
	if err != nil {
		return CandidateListReport{}, err
	}
	for i := range cands {
		cands[i], _ = AdmitCandidate(cands[i])
	}
	reviewByCandidate, warnings := s.reviewStateByCandidate()
	rawKinds, kindWarnings := s.rawCandidateKinds()
	warnings = append(warnings, kindWarnings...)
	report := CandidateListReport{
		SchemaVersion: CandidateListSchemaVersion,
		AuthorityRef:  "compound://memory/candidates",
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Counts: CandidateListCounts{
			Total:   len(cands),
			ByState: map[string]int{},
		},
		Items:    []CandidateListItem{},
		Warnings: warnings,
	}
	if state != "" || kind != "" || opts.Limit > 0 || opts.IncludeEvidence {
		report.Filters = map[string]interface{}{}
		if state != "" {
			report.Filters["state"] = state
		}
		if kind != "" {
			report.Filters["kind"] = kind
		}
		if opts.Limit > 0 {
			report.Filters["limit"] = opts.Limit
		}
		if opts.IncludeEvidence {
			report.Filters["include_evidence"] = true
		}
	}

	for _, c := range cands {
		report.Counts.ByState[c.State]++
	}
	for _, c := range cands {
		reviewState := reviewByCandidate[c.ID]
		rawKind := rawKinds[c.ID]
		if state != "" {
			if state == ClaimStateNeedsReview {
				if reviewState != ClaimStateNeedsReview {
					continue
				}
			} else if c.State != state {
				continue
			}
		}
		if kind != "" && rawKind != kind && c.ClaimType != kind {
			continue
		}
		item, err := s.candidateListItem(c, reviewState, rawKind, opts.IncludeEvidence)
		if err != nil {
			return report, err
		}
		report.Items = append(report.Items, item)
		if opts.Limit > 0 && len(report.Items) >= opts.Limit {
			break
		}
	}
	report.Counts.Matched = len(report.Items)
	return report, nil
}

func validCandidateListState(state string) bool {
	switch state {
	case "proposed", "parked", "promoted", "rejected", "no_promote", ClaimStateNeedsReview:
		return true
	default:
		return false
	}
}

func (s *Store) candidateListItem(c Candidate, reviewState, rawKind string, includeEvidence bool) (CandidateListItem, error) {
	item := CandidateListItem{
		ID:              c.ID,
		State:           c.State,
		ReviewState:     reviewState,
		Entity:          c.Entity,
		Predicate:       c.Predicate,
		Object:          c.Object,
		ObservationDate: c.ObservationDate,
		Confidence:      c.Confidence,
		Derivation:      c.Derivation,
		ClaimType:       c.ClaimType,
		Kind:            rawKind,
		Visibility:      c.Visibility,
		PromotionBasis:  c.PromotionBasis,
		Qualifiers:      c.Qualifiers,
		Reason:          c.Reason,
		AdjudicatedBy:   c.AdjudicatedBy,
		AdjudicatedAt:   c.AdjudicatedAt,
		CreatedAt:       c.CreatedAt,
	}
	if includeEvidence {
		item.SourceTraceRef = c.SourceTraceRef
		item.SnippetHash = c.SnippetHash
		item.EvidenceRefs = []string{c.ID}
		if c.SourceTraceRef != "" {
			item.EvidenceRefs = append(item.EvidenceRefs, c.SourceTraceRef)
			if ev, ok, err := s.GetEvidence(c.SourceTraceRef); err != nil {
				return item, err
			} else if ok {
				item.Evidence = &CandidateListEvidence{
					Ref:        ev.ID,
					SourceKind: ev.SourceKind,
					SourceID:   ev.SourceID,
					SessionID:  ev.SessionID,
					Quote:      ev.QuoteText,
					EventTime:  ev.EventTime,
					ObservedAt: ev.ObservedAt,
					Actor:      ev.Actor,
					Visibility: ev.Visibility,
				}
			}
		}
		item.Annotations = append([]CandidateAnnotation(nil), c.Annotations...)
		if c.State != ClaimStatePromoted {
			if prior, ok, err := s.currentClusterClaim(c, 0); err != nil {
				return item, err
			} else if ok {
				item.BelievedBefore = &CandidateListBelief{
					ClaimID:      prior.ID,
					Entity:       prior.Entity,
					Predicate:    prior.Predicate,
					Object:       prior.Object,
					ValidFrom:    prior.ValidFrom,
					CreatedAt:    prior.CreatedAt,
					EvidenceRefs: append([]string(nil), prior.EvidenceRefs...),
				}
			}
		}
	}
	return item, nil
}

func (s *Store) rawCandidateKinds() (map[string]string, []string) {
	out := map[string]string{}
	entries, err := os.ReadDir(s.candDir())
	if err != nil {
		return out, []string{"candidate kind scan unavailable: " + err.Error()}
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var meta struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		}
		data, err := os.ReadFile(filepath.Join(s.candDir(), entry.Name()))
		if err != nil {
			return out, []string{entry.Name() + ": candidate kind unreadable: " + err.Error()}
		}
		if err := json.Unmarshal(data, &meta); err != nil {
			return out, []string{entry.Name() + ": candidate kind invalid: " + err.Error()}
		}
		id := strings.TrimSpace(meta.ID)
		if id == "" {
			id = strings.TrimSuffix(entry.Name(), ".json")
		}
		if meta.Kind != "" {
			out[id] = meta.Kind
		}
	}
	return out, nil
}

func (s *Store) reviewStateByCandidate() (map[string]string, []string) {
	out := map[string]string{}
	claims, err := s.loadClaimsAsOf(0)
	if err != nil {
		return out, []string{"claim review state unavailable: " + err.Error()}
	}
	sort.Slice(claims, func(i, j int) bool {
		return claims[i].Seq < claims[j].Seq
	})
	for _, claim := range claims {
		if claim.State != ClaimStateNeedsReview {
			continue
		}
		for _, ref := range claim.EvidenceRefs {
			if strings.HasPrefix(ref, "cand_") {
				out[ref] = ClaimStateNeedsReview
			}
		}
	}
	candidateMarks, err := s.candidateReviewMarksAsOf(0)
	if err != nil {
		return out, []string{"candidate review state unavailable: " + err.Error()}
	}
	for candidateID, mark := range candidateMarks {
		if len(mark.Refs) == 0 {
			continue
		}
		out[candidateID] = ClaimStateNeedsReview
	}
	return out, nil
}
