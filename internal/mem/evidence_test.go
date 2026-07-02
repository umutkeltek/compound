package mem

import "testing"

// Evidence is stored, retrievable, and content-addressed: recording the same content twice is
// idempotent (one item, stable id), and the item round-trips by id.
func TestEvidenceStoredAndIdempotent(t *testing.T) {
	s := newStore(t)
	e1, err := s.RecordEvidence(EvidenceItem{SourceKind: "codex", SourceID: "sess1", QuoteText: "we chose Go for the backend", Actor: "user"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if e1.ID == "" || e1.ContentHash == "" {
		t.Fatalf("recorded evidence must have an id + content hash; got %+v", e1)
	}
	// same content again → same id, no duplicate.
	e2, _ := s.RecordEvidence(EvidenceItem{SourceKind: "codex", SourceID: "sess1", QuoteText: "we chose Go for the backend", Actor: "user"}, t0)
	if e2.ID != e1.ID {
		t.Errorf("identical evidence must be content-addressed to the same id; %q != %q", e2.ID, e1.ID)
	}
	all, _ := s.Evidence()
	if len(all) != 1 {
		t.Errorf("recording identical evidence twice must store ONE item; got %d", len(all))
	}
	// round-trip by id.
	got, ok, _ := s.GetEvidence(e1.ID)
	if !ok || got.QuoteText != "we chose Go for the backend" {
		t.Errorf("evidence must round-trip by id; got ok=%v %+v", ok, got)
	}
	// different content → different id.
	e3, _ := s.RecordEvidence(EvidenceItem{SourceKind: "codex", SourceID: "sess1", QuoteText: "actually Rust now"}, t0)
	if e3.ID == e1.ID {
		t.Error("different content must get a different id")
	}
}

// Arbitrary source_kind values are accepted; empty source/evidence is rejected.
func TestEvidenceValidation(t *testing.T) {
	s := newStore(t)
	if e, err := s.RecordEvidence(EvidenceItem{SourceKind: "podcast", QuoteText: "x"}, t0); err != nil || e.SourceKind != "podcast" {
		t.Fatalf("arbitrary source_kind should be accepted: evidence=%+v err=%v", e, err)
	}
	if _, err := s.RecordEvidence(EvidenceItem{QuoteText: "x"}, t0); err == nil {
		t.Error("empty source_kind must be rejected")
	}
	if _, err := s.RecordEvidence(EvidenceItem{SourceKind: "Bad Kind!", QuoteText: "x"}, t0); err == nil {
		t.Error("non-slug source_kind must be rejected")
	}
	if _, err := s.RecordEvidence(EvidenceItem{SourceKind: "note"}, t0); err == nil {
		t.Error("evidence with neither quote_text nor source_id must be rejected")
	}
}
