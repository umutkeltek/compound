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

func TestSynthesisSweepReopensPromotedClaimOnContradictingEvidence(t *testing.T) {
	s := newStore(t)

	original := cand("project:api", "runtime", "Go", "")
	original.ClaimType = "decision"
	original.Qualifiers = map[string]string{"domain": "language"}
	promotedCandidates, err := s.Capture([]Candidate{original}, t0)
	if err != nil {
		t.Fatal(err)
	}
	promoted, err := s.Adjudicate(promotedCandidates[0].ID, "promote", "work_promote", t0)
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Claim == nil {
		t.Fatal("promote should write claim")
	}

	contradicting := cand("project:api", "implementation_language", "Rust", "")
	contradicting.ClaimType = "decision"
	contradicting.Qualifiers = map[string]string{"domain": "language"}
	contradicting.Confidence = 0.55
	contradictingCandidates, err := s.Capture([]Candidate{contradicting}, t0)
	if err != nil {
		t.Fatal(err)
	}

	report, err := s.SynthesisSweep([]string{"contradict"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if report.ContradictionProposalCount != 1 {
		t.Fatalf("ContradictionProposalCount = %d, want 1: %+v", report.ContradictionProposalCount, report.ContradictionProposals)
	}
	if report.ReviewDebtCount != 1 || len(report.ReopenedClaims) != 1 {
		t.Fatalf("review debt = %d reopened=%+v, want one reopened claim", report.ReviewDebtCount, report.ReopenedClaims)
	}
	if report.ContradictionProposals[0].CandidateIDs[0] != contradictingCandidates[0].ID {
		t.Fatalf("contradiction should link candidate %q: %+v", contradictingCandidates[0].ID, report.ContradictionProposals[0])
	}
	if report.ReopenedClaims[0].State != ClaimStateNeedsReview ||
		report.ReopenedClaims[0].ReviewRef != report.ContradictionProposals[0].ID ||
		len(report.ReopenedClaims[0].CandidateIDs) != 1 ||
		report.ReopenedClaims[0].CandidateIDs[0] != contradictingCandidates[0].ID {
		t.Fatalf("reopened claim marker incomplete: %+v", report.ReopenedClaims[0])
	}

	claims, err := s.loadClaims()
	if err != nil {
		t.Fatal(err)
	}
	var reopened Claim
	for _, claim := range claims {
		if claim.ID == promoted.Claim.ID {
			reopened = claim
			break
		}
	}
	if reopened.ID == "" {
		t.Fatalf("missing promoted claim %s in %+v", promoted.Claim.ID, claims)
	}
	if reopened.State != ClaimStateNeedsReview {
		t.Fatalf("claim state = %q, want %q", reopened.State, ClaimStateNeedsReview)
	}
	if len(reopened.ReviewRefs) != 1 || reopened.ReviewRefs[0] != report.ContradictionProposals[0].ID {
		t.Fatalf("claim review refs = %+v, want contradiction %s", reopened.ReviewRefs, report.ContradictionProposals[0].ID)
	}
	if reopened.ConfidenceDelta < -0.351 || reopened.ConfidenceDelta > -0.349 {
		t.Fatalf("confidence delta = %.2f, want -0.35", reopened.ConfidenceDelta)
	}

	defaultRecall, err := s.Recall("project:api", RecallOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultRecall.Claims) != 0 {
		t.Fatalf("default recall should not keep needs_review claim authoritative: %+v", defaultRecall.Claims)
	}
	reviewRecall, err := s.Recall("project:api", RecallOpts{IncludeNeedsReview: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(reviewRecall.Claims) != 1 || reviewRecall.Claims[0].State != ClaimStateNeedsReview {
		t.Fatalf("IncludeNeedsReview recall = %+v, want reopened claim visible", reviewRecall.Claims)
	}

	timeline, err := s.Timeline("project:api")
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) != 1 ||
		timeline[0].State != ClaimStateNeedsReview ||
		len(timeline[0].ReviewRefs) != 1 ||
		timeline[0].ReviewRefs[0] != report.ContradictionProposals[0].ID {
		t.Fatalf("timeline should expose reopened claim review state: %+v", timeline)
	}

	again, err := s.SynthesisSweep([]string{"contradict"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if again.ReviewDebtCount != 0 || len(again.ReopenedClaims) != 0 {
		t.Fatalf("second contradiction sweep should not duplicate review markers: %+v", again.ReopenedClaims)
	}
}
