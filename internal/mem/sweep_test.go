package mem

import "testing"

func TestSynthesisSweepFindsEchoAndContradictionProposals(t *testing.T) {
	s := newStore(t)

	first := cand("project:api", "runtime", "Go", "")
	first.ClaimType = "decision"
	first.Qualifiers = map[string]string{"domain": "language"}
	second := cand("project:api", "implementation_language", " go ", "")
	second.ClaimType = "decision"
	second.Qualifiers = map[string]string{"domain": "language"}
	if _, err := s.Capture([]Candidate{first, second}, t0); err != nil {
		t.Fatal(err)
	}

	if err := appendJSONL(s.claimsLog(), &Claim{
		ID:         "claim_go",
		Seq:        1,
		Entity:     "project:api",
		Predicate:  "runtime",
		Object:     "Go",
		ClaimType:  "decision",
		Qualifiers: map[string]string{"domain": "language"},
		Confidence: 0.9,
		Visibility: "public",
		CreatedAt:  rfc3339(t0),
	}); err != nil {
		t.Fatal(err)
	}
	if err := appendJSONL(s.claimsLog(), &Claim{
		ID:         "claim_rust",
		Seq:        2,
		Entity:     "project:api",
		Predicate:  "implementation_language",
		Object:     "Rust",
		ClaimType:  "decision",
		Qualifiers: map[string]string{"domain": "language"},
		Confidence: 0.8,
		Visibility: "public",
		CreatedAt:  rfc3339(t0),
	}); err != nil {
		t.Fatal(err)
	}

	report, err := s.SynthesisSweep([]string{"cluster", "contradict"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if report.ClusterCount != 1 {
		t.Fatalf("ClusterCount = %d, want 1", report.ClusterCount)
	}
	if report.EchoFoldProposalCount != 1 {
		t.Fatalf("EchoFoldProposalCount = %d, want 1: %+v", report.EchoFoldProposalCount, report.EchoFoldProposals)
	}
	if report.ContradictionProposalCount != 1 {
		t.Fatalf("ContradictionProposalCount = %d, want 1: %+v", report.ContradictionProposalCount, report.ContradictionProposals)
	}
}
