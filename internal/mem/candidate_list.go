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
	ID              string                `json:"id"`
	State           string                `json:"state"`
	ReviewState     string                `json:"review_state,omitempty"`
	Entity          string                `json:"entity,omitempty"`
	Predicate       string                `json:"predicate,omitempty"`
	Object          string                `json:"object,omitempty"`
	ObservationDate string                `json:"observation_date,omitempty"`
	Confidence      float64               `json:"confidence,omitempty"`
	Derivation      string                `json:"derivation,omitempty"`
	ClaimType       string                `json:"claim_type,omitempty"`
	Kind            string                `json:"kind,omitempty"`
	Visibility      string                `json:"visibility,omitempty"`
	PromotionBasis  string                `json:"promotion_basis,omitempty"`
	Qualifiers      map[string]string     `json:"qualifiers,omitempty"`
	CreatedAt       string                `json:"created_at,omitempty"`
	SourceTraceRef  string                `json:"source_trace_ref,omitempty"`
	SnippetHash     string                `json:"snippet_hash,omitempty"`
	EvidenceRefs    []string              `json:"evidence_refs,omitempty"`
	Annotations     []CandidateAnnotation `json:"annotations,omitempty"`
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
		report.Items = append(report.Items, candidateListItem(c, reviewState, rawKind, opts.IncludeEvidence))
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

func candidateListItem(c Candidate, reviewState, rawKind string, includeEvidence bool) CandidateListItem {
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
		CreatedAt:       c.CreatedAt,
	}
	if includeEvidence {
		item.SourceTraceRef = c.SourceTraceRef
		item.SnippetHash = c.SnippetHash
		item.EvidenceRefs = []string{c.ID}
		if c.SourceTraceRef != "" {
			item.EvidenceRefs = append(item.EvidenceRefs, c.SourceTraceRef)
		}
		item.Annotations = append([]CandidateAnnotation(nil), c.Annotations...)
	}
	return item
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
	return out, nil
}
