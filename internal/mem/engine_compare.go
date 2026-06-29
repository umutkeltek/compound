package mem

import (
	"fmt"
	"sort"
	"strings"
)

// EngineCompareReport proves a rented breadth engine can be swapped behind the
// memory seam and hydrate the same owned claim ids as the reference engine.
// It compares hydrated claims only; the ledger remains the only authority.
type EngineCompareReport struct {
	Query             string             `json:"query"`
	Reference         EngineDoctorReport `json:"reference"`
	Candidate         EngineDoctorReport `json:"candidate"`
	ReferenceClaimIDs []string           `json:"reference_claim_ids"`
	CandidateClaimIDs []string           `json:"candidate_claim_ids"`
	MissingClaimIDs   []string           `json:"missing_claim_ids,omitempty"`
	ExtraClaimIDs     []string           `json:"extra_claim_ids,omitempty"`
	LedgerUnchanged   bool               `json:"ledger_unchanged"`
	Match             bool               `json:"match"`
}

// EngineCompare runs the owned lexical reference engine and a rented candidate
// engine against the same ledger-gated query. A candidate passes only when it
// hydrates the same current claim ids and leaves the owned ledger byte-identical.
func (s *Store) EngineCompare(query string, k int, candidate Engine) (EngineCompareReport, error) {
	if strings.TrimSpace(query) == "" {
		return EngineCompareReport{}, fmt.Errorf("query required")
	}
	before, err := s.LedgerDigest()
	if err != nil {
		return EngineCompareReport{}, err
	}
	reference, err := s.EngineDoctor(query, k, &LexicalEngine{})
	if err != nil {
		return EngineCompareReport{}, err
	}
	candidateReport, err := s.EngineDoctor(query, k, candidate)
	if err != nil {
		return EngineCompareReport{}, err
	}
	after, err := s.LedgerDigest()
	if err != nil {
		return EngineCompareReport{}, err
	}
	report := EngineCompareReport{
		Query:             query,
		Reference:         reference,
		Candidate:         candidateReport,
		ReferenceClaimIDs: doctorClaimIDs(reference.Hits),
		CandidateClaimIDs: doctorClaimIDs(candidateReport.Hits),
		LedgerUnchanged:   before == after && reference.LedgerUnchanged && candidateReport.LedgerUnchanged,
	}
	report.MissingClaimIDs, report.ExtraClaimIDs = compareClaimIDSets(report.ReferenceClaimIDs, report.CandidateClaimIDs)
	report.Match = report.LedgerUnchanged && !candidateReport.Degraded && len(report.MissingClaimIDs) == 0 && len(report.ExtraClaimIDs) == 0
	return report, nil
}

func doctorClaimIDs(hits []EngineDoctorHit) []string {
	out := make([]string, 0, len(hits))
	seen := map[string]bool{}
	for _, hit := range hits {
		if hit.ClaimID == "" || seen[hit.ClaimID] {
			continue
		}
		seen[hit.ClaimID] = true
		out = append(out, hit.ClaimID)
	}
	sort.Strings(out)
	return out
}

func compareClaimIDSets(reference, candidate []string) (missing, extra []string) {
	ref := map[string]bool{}
	got := map[string]bool{}
	for _, id := range reference {
		ref[id] = true
	}
	for _, id := range candidate {
		got[id] = true
	}
	for _, id := range reference {
		if !got[id] {
			missing = append(missing, id)
		}
	}
	for _, id := range candidate {
		if !ref[id] {
			extra = append(extra, id)
		}
	}
	return missing, extra
}
