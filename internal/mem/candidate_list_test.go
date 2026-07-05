package mem

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/candidates"
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

func TestListCandidatesIncludeEvidenceAddsCardContext(t *testing.T) {
	s := newStore(t)
	ev, err := s.RecordEvidence(EvidenceItem{
		SourceKind: "chatgpt",
		SourceID:   "conversation:abc",
		QuoteText:  "On June 8, keep the old memory engine only as a temporary bridge.",
		EventTime:  "2026-06-08T09:00:00Z",
		Actor:      "user",
		Visibility: "private",
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := s.Capture([]Candidate{{
		Entity:          "project:compound",
		Predicate:       "memory_engine_strategy",
		Object:          "own the breadth engine; rent legacy indexes only as bridges",
		ObservationDate: "2026-07-01",
		Confidence:      0.93,
		Derivation:      "deterministic",
		PromotionBasis:  candidates.BasisExplicitConfirmation,
		SourceTraceRef:  ev.ID,
		SnippetHash:     ev.ContentHash,
	}}, t0)
	if err != nil {
		t.Fatal(err)
	}
	promoted, err := s.Adjudicate(prior[0].ID, "promote", "", t0)
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.Capture([]Candidate{{
		Entity:          "project:compound",
		Predicate:       "memory_engine_strategy",
		Object:          "keep the old memory engine as the source of truth",
		ObservationDate: "2026-06-08",
		Confidence:      0.72,
		Derivation:      "extracted",
		PromotionBasis:  candidates.BasisExplicitConfirmation,
		SourceTraceRef:  ev.ID,
		SnippetHash:     ev.ContentHash,
	}}, t0)
	if err != nil {
		t.Fatal(err)
	}

	report, err := s.ListCandidates(t0, CandidateListOptions{State: "proposed", IncludeEvidence: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts.Matched != 1 || report.Items[0].ID != next[0].ID {
		t.Fatalf("report items = %+v", report.Items)
	}
	data, err := json.Marshal(report.Items[0])
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ObservationDate string `json:"observation_date"`
		Evidence        struct {
			Ref       string `json:"ref"`
			Quote     string `json:"quote"`
			SourceID  string `json:"source_id"`
			EventTime string `json:"event_time"`
		} `json:"evidence"`
		BelievedBefore struct {
			ClaimID   string `json:"claim_id"`
			Object    string `json:"object"`
			CreatedAt string `json:"created_at"`
		} `json:"believed_before"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.ObservationDate != "2026-06-08" {
		t.Fatalf("observation date = %q", got.ObservationDate)
	}
	if got.Evidence.Ref != ev.ID || got.Evidence.SourceID != "conversation:abc" || got.Evidence.EventTime != "2026-06-08T09:00:00Z" {
		t.Fatalf("evidence context = %+v", got.Evidence)
	}
	if !strings.Contains(got.Evidence.Quote, "temporary bridge") {
		t.Fatalf("evidence quote = %q", got.Evidence.Quote)
	}
	if got.BelievedBefore.ClaimID != promoted.Claim.ID || got.BelievedBefore.Object != promoted.Claim.Object || got.BelievedBefore.CreatedAt == "" {
		t.Fatalf("believed_before = %+v, want claim %+v", got.BelievedBefore, promoted.Claim)
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
