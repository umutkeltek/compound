package mem

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListCandidatesFiltersCountsAndEvidenceGate(t *testing.T) {
	s := newStore(t)

	proposed := cand("entity:one", "status", "proposed", "")
	parked := cand("entity:two", "status", "parked", "")
	promoted := cand("entity:three", "status", "promoted", "")
	rejected := cand("entity:four", "status", "rejected", "")
	got, err := s.Capture([]Candidate{proposed, parked, promoted, rejected}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Adjudicate(got[1].ID, "park", "", t0); err != nil {
		t.Fatal(err)
	}
	promotedResult, err := s.Adjudicate(got[2].ID, "promote", "", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Adjudicate(got[3].ID, "reject", "", t0); err != nil {
		t.Fatal(err)
	}
	tagRawCandidateKind(t, s, got[1].ID, "doctrine")

	seq, err := s.nextSeq()
	if err != nil {
		t.Fatal(err)
	}
	if err := appendJSONL(s.edgesLog(), Edge{
		Seq:          seq,
		Kind:         EdgeKindNeedsReview,
		From:         "review_fixture",
		To:           promotedResult.Claim.ID,
		Confidence:   0.9,
		EvidenceRefs: []string{"review_fixture"},
		CreatedAt:    rfc3339(t0),
	}); err != nil {
		t.Fatal(err)
	}

	report, err := s.ListCandidates(t0, CandidateListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts.Total != 4 || report.Counts.Matched != 4 {
		t.Fatalf("counts = %+v", report.Counts)
	}
	for state, want := range map[string]int{"proposed": 1, "parked": 1, "promoted": 1, "rejected": 1} {
		if got := report.Counts.ByState[state]; got != want {
			t.Fatalf("by_state[%s] = %d, want %d; counts=%+v", state, got, want, report.Counts)
		}
	}
	if report.Items[0].SourceTraceRef != "" || report.Items[0].SnippetHash != "" || len(report.Items[0].EvidenceRefs) != 0 {
		t.Fatalf("evidence leaked by default: %+v", report.Items[0])
	}
	limitedReport, err := s.ListCandidates(t0, CandidateListOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if limitedReport.Counts.Total != 4 || len(limitedReport.Items) != 1 || limitedReport.Counts.ByState["promoted"] != 1 {
		t.Fatalf("limit truncated counts: %+v items=%d", limitedReport.Counts, len(limitedReport.Items))
	}

	kindReport, err := s.ListCandidates(t0, CandidateListOptions{Kind: "doctrine"})
	if err != nil {
		t.Fatal(err)
	}
	if kindReport.Counts.Matched != 1 || kindReport.Items[0].ID != got[1].ID || kindReport.Items[0].Kind != "doctrine" {
		t.Fatalf("kind filter report = %+v", kindReport)
	}

	reviewReport, err := s.ListCandidates(t0, CandidateListOptions{State: ClaimStateNeedsReview})
	if err != nil {
		t.Fatal(err)
	}
	if reviewReport.Counts.Matched != 1 || reviewReport.Items[0].ID != got[2].ID || reviewReport.Items[0].ReviewState != ClaimStateNeedsReview {
		t.Fatalf("needs_review report = %+v", reviewReport)
	}

	evidenceReport, err := s.ListCandidates(t0, CandidateListOptions{State: "proposed", IncludeEvidence: true})
	if err != nil {
		t.Fatal(err)
	}
	if evidenceReport.Counts.Matched != 1 || evidenceReport.Items[0].SourceTraceRef == "" || len(evidenceReport.Items[0].EvidenceRefs) == 0 {
		t.Fatalf("include evidence report = %+v", evidenceReport)
	}
}

func TestListCandidatesAppliesAdmissionToLegacySourceRows(t *testing.T) {
	s := newStore(t)
	legacy := Candidate{
		ID:             "cand_legacy_source_metadata",
		Entity:         "knowledge:source:item",
		Predicate:      "title",
		Object:         "Legacy source title",
		Confidence:     0.8,
		Derivation:     "deterministic",
		Qualifiers:     map[string]string{"source": "knowledge_import"},
		SourceTraceRef: "ev_legacy_source",
		SnippetHash:    "abc123",
		Visibility:     "candidate",
		State:          "proposed",
		CreatedAt:      rfc3339(t0),
	}
	writeRawCandidate(t, s, legacy)

	proposed, err := s.ListCandidates(t0, CandidateListOptions{State: "proposed"})
	if err != nil {
		t.Fatal(err)
	}
	if proposed.Counts.Matched != 0 || proposed.Counts.ByState["proposed"] != 0 {
		t.Fatalf("proposed report = %+v, want legacy source row admitted away from operator queue", proposed.Counts)
	}

	parked, err := s.ListCandidates(t0, CandidateListOptions{State: "parked"})
	if err != nil {
		t.Fatal(err)
	}
	if parked.Counts.Matched != 1 || parked.Counts.ByState["parked"] != 1 {
		t.Fatalf("parked report = %+v, want legacy source row counted as parked", parked.Counts)
	}
	if parked.Items[0].State != "parked" || !strings.Contains(parked.Items[0].Reason, "source metadata") {
		t.Fatalf("parked item = %+v, want admitted source metadata", parked.Items[0])
	}

	var persisted Candidate
	data, err := os.ReadFile(filepath.Join(s.candDir(), legacy.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.State != "proposed" || persisted.Reason != "" {
		t.Fatalf("persisted candidate mutated by list projection: %+v", persisted)
	}
}

func tagRawCandidateKind(t *testing.T, s *Store, id, kind string) {
	t.Helper()
	path := filepath.Join(s.candDir(), id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	payload["kind"] = kind
	data, err = json.MarshalIndent(payload, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeRawCandidate(t *testing.T, s *Store, c Candidate) {
	t.Helper()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.candDir(), c.ID+".json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
