package mem

import "testing"

func TestRecordStableCandidateReusesExistingCandidate(t *testing.T) {
	store := newStore(t)
	candidate := Candidate{
		ID:             "cand_stable_test",
		Entity:         "knowledge:source:item",
		Predicate:      "reference_metadata",
		Object:         "title=Stable",
		Confidence:     0.8,
		Derivation:     "deterministic",
		SourceTraceRef: "ev_stable",
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
	reused, err := store.RecordStableCandidate(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if reused {
		t.Fatal("second stable candidate write should reuse existing record")
	}
	candidates, err := store.AllCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ID != candidate.ID {
		t.Fatalf("candidates = %+v", candidates)
	}
}
