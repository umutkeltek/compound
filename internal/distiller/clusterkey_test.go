package distiller

import (
	"testing"

	"compound/internal/mem"
)

func TestCritiqueUsesTopicAxisForMemoryDuplicates(t *testing.T) {
	prior := PriorState{Current: []mem.Claim{{
		ID:         "claim_lang",
		Entity:     "svc",
		Predicate:  "runtime",
		Object:     "Go",
		Confidence: 0.9,
		ClaimType:  "decision",
		Qualifiers: map[string]string{"domain": "language"},
	}}}
	got := critique(Candidate{
		Kind:       KindMemory,
		Entity:     "svc",
		Predicate:  "implementation_language",
		Object:     " go ",
		Confidence: 0.9,
		ClaimType:  "decision",
		Qualifiers: map[string]string{"domain": "language"},
	}, prior)
	if !got.Duplicate || got.DuplicateClaimID != "claim_lang" {
		t.Fatalf("critique = %+v, want duplicate claim_lang", got)
	}
}
