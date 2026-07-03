package mem

import (
	"strings"
	"testing"
)

func TestAdmitCandidateParksKnowledgeEvidence(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
		target string
	}{
		{name: "metadata import", source: "knowledge_import", want: "source metadata", target: AdmissionSurfaceSourceIndex},
		{name: "extraction output", source: "knowledge_extraction", want: "knowledge extraction", target: AdmissionSurfaceAnnotatedEvidence},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := Candidate{
				ID:             "cand_" + strings.ReplaceAll(tt.name, " ", "_"),
				Entity:         "knowledge:source:item",
				Predicate:      "reports_result",
				Object:         "grounded statement",
				Confidence:     0.8,
				Derivation:     "extracted",
				Qualifiers:     map[string]string{"source": tt.source},
				SourceTraceRef: "ev_source",
				SnippetHash:    "abc123",
				Visibility:     "candidate",
				State:          "proposed",
				CreatedAt:      rfc3339(t0),
			}

			admitted, decision := AdmitCandidate(candidate)
			if admitted.State != "parked" {
				t.Fatalf("admitted state = %q, want parked; decision=%+v", admitted.State, decision)
			}
			if !strings.Contains(admitted.Reason, tt.want) {
				t.Fatalf("reason = %q, want mention %q", admitted.Reason, tt.want)
			}
			if decision.ReviewRequired {
				t.Fatalf("knowledge evidence should not require operator review: %+v", decision)
			}
			if decision.TargetSurface != tt.target {
				t.Fatalf("target surface = %q, want %q", decision.TargetSurface, tt.target)
			}
		})
	}
}

func TestAdmitCandidateLeavesDurableMemoryProposed(t *testing.T) {
	candidate := Candidate{
		ID:             "cand_durable",
		Entity:         "project:compound",
		Predicate:      "decided",
		Object:         "operator decisions remain manually gated",
		Confidence:     0.9,
		Derivation:     "deterministic",
		Qualifiers:     map[string]string{"domain": "memory"},
		SourceTraceRef: "ev_durable",
		SnippetHash:    "def456",
		Visibility:     "operational",
		CreatedAt:      rfc3339(t0),
	}

	admitted, decision := AdmitCandidate(candidate)
	if admitted.State != "proposed" {
		t.Fatalf("admitted state = %q, want proposed; decision=%+v", admitted.State, decision)
	}
	if !decision.ReviewRequired || decision.TargetSurface != "operator_queue" {
		t.Fatalf("durable memory should remain operator-gated: %+v", decision)
	}
}

func TestRecordStableCandidateAppliesAdmission(t *testing.T) {
	store := newStore(t)
	candidate := Candidate{
		ID:             "cand_metadata_admission",
		Entity:         "knowledge:source:item",
		Predicate:      "reference_title",
		Object:         "Source Title",
		Confidence:     0.8,
		Derivation:     "deterministic",
		Qualifiers:     map[string]string{"source": "knowledge_import"},
		SourceTraceRef: "ev_metadata",
		SnippetHash:    "abc123",
		Visibility:     "candidate",
		State:          "proposed",
		CreatedAt:      rfc3339(t0),
	}

	created, err := store.RecordStableCandidate(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first stable candidate write should create")
	}
	candidates, err := store.AllCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if candidates[0].State != "parked" || !strings.Contains(candidates[0].Reason, "source metadata") {
		t.Fatalf("admitted candidate = %+v, want parked source metadata", candidates[0])
	}
	report, err := store.ListCandidates(t0, CandidateListOptions{State: "parked"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts.Matched != 1 || !strings.Contains(report.Items[0].Reason, "source metadata") {
		t.Fatalf("candidate list admission reason = %+v", report)
	}
}
