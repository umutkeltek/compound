package mem

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s
}

func cand(entity, pred, obj, obs string) Candidate {
	return Candidate{Entity: entity, Predicate: pred, Object: obj, ObservationDate: obs, Confidence: 0.9, Derivation: "extracted", SourceTraceRef: "trace_x", SnippetHash: "deadbeef"}
}

// The headline P6 proof: a fact changes over time; the ledger keeps BOTH versions; as-of
// recall returns the right one at each seq; the old claim is superseded by an EDGE, not
// deleted; promotion emits a Transition. This is delta-aware memory on the owned ledger.
func TestDeltaLoop(t *testing.T) {
	s := newStore(t)
	e, p := "project:healthapp", "backend_language"

	c1, _ := s.Capture([]Candidate{cand(e, p, "node", "2026-01-01T00:00:00Z")}, t0)
	r1, err := s.Adjudicate(c1[0].ID, "promote", "", t0)
	if err != nil || r1.Claim == nil {
		t.Fatalf("promote 1: %v", err)
	}
	if r1.Superseded != "" {
		t.Errorf("first claim should supersede nothing")
	}
	seq1 := r1.Claim.Seq

	c2, _ := s.Capture([]Candidate{cand(e, p, "go", "2026-06-01T00:00:00Z")}, t0)
	r2, err := s.Adjudicate(c2[0].ID, "promote", "", t0)
	if err != nil || r2.Claim == nil {
		t.Fatalf("promote 2: %v", err)
	}
	// The change is a supersession (edge), and a Transition records old -> new.
	if r2.Superseded != r1.Claim.ID {
		t.Errorf("second claim should supersede the first; got %q", r2.Superseded)
	}
	if r2.Transition == nil {
		t.Fatal("promotion should emit a Transition")
	}
	if err := r2.Transition.Validate(); err != nil {
		t.Fatalf("emitted transition invalid: %v", err)
	}
	if len(r2.Transition.Projections.Memory) == 0 || r2.Transition.BeforeRefs[0] != r1.Claim.ID {
		t.Errorf("transition should project memory and reference the superseded claim")
	}

	// Recall NOW → only the new value.
	now, _ := s.Recall(e, RecallOpts{})
	if len(now.Claims) != 1 || now.Claims[0].Object != "go" {
		t.Fatalf("recall now should return only 'go'; got %+v", objs(now.Claims))
	}
	// Recall AS OF seq1 → the OLD value (MVCC-by-fold: at that point 'node' was current).
	past, _ := s.Recall(e, RecallOpts{AsOfSeq: seq1})
	if len(past.Claims) != 1 || past.Claims[0].Object != "node" {
		t.Fatalf("recall as-of seq1 should return 'node'; got %+v", objs(past.Claims))
	}
	// History is retained: timeline shows both, oldest -> newest.
	tl, _ := s.Timeline(e)
	if len(tl) != 2 || tl[0].Object != "node" || tl[1].Object != "go" {
		t.Fatalf("timeline should show node -> go; got %+v", objs(tl))
	}
}

func TestCandidateFirstNoCanonWithoutPromote(t *testing.T) {
	s := newStore(t)
	c, _ := s.Capture([]Candidate{cand("e", "p", "v", "")}, t0)
	if _, err := s.Adjudicate(c[0].ID, "park", "", t0); err != nil {
		t.Fatal(err)
	}
	if claims, _ := s.loadClaims(); len(claims) != 0 {
		t.Errorf("park must not write a claim; got %d", len(claims))
	}
	c2, _ := s.Capture([]Candidate{cand("e", "p", "v", "")}, t0)
	if _, err := s.Adjudicate(c2[0].ID, "reject", "", t0); err != nil {
		t.Fatal(err)
	}
	if claims, _ := s.loadClaims(); len(claims) != 0 {
		t.Errorf("reject must not write a claim; got %d", len(claims))
	}
}

func TestDuplicateRejected(t *testing.T) {
	s := newStore(t)
	c1, _ := s.Capture([]Candidate{cand("e", "p", "same", "")}, t0)
	s.Adjudicate(c1[0].ID, "promote", "", t0)
	c2, _ := s.Capture([]Candidate{cand("e", "p", "same", "")}, t0)
	r, _ := s.Adjudicate(c2[0].ID, "promote", "", t0)
	if r.Verdict != "reject" {
		t.Errorf("promoting an already-current value should be rejected as duplicate; got %q", r.Verdict)
	}
	if claims, _ := s.loadClaims(); len(claims) != 1 {
		t.Errorf("duplicate must not append a second claim; got %d", len(claims))
	}
}

func TestRecallRelevanceGate(t *testing.T) {
	s := newStore(t)
	c := cand("e", "secret", "v", "")
	c.Visibility = "retired"
	got, _ := s.Capture([]Candidate{c}, t0)
	s.Adjudicate(got[0].ID, "promote", "", t0)
	// retired is hidden from default recall...
	def, _ := s.Recall("e", RecallOpts{})
	if len(def.Claims) != 0 || def.Filtered != 1 {
		t.Errorf("retired claim should be gated out of default recall; got %d claims, %d filtered", len(def.Claims), def.Filtered)
	}
	// ...but available when explicitly included.
	inc, _ := s.Recall("e", RecallOpts{IncludeRetired: true})
	if len(inc.Claims) != 1 {
		t.Errorf("retired claim should appear with IncludeRetired; got %d", len(inc.Claims))
	}
}

func TestDoctorReportsHops(t *testing.T) {
	s := newStore(t)
	c, _ := s.Capture([]Candidate{cand("e", "p", "v", "")}, t0)
	s.Adjudicate(c[0].ID, "promote", "", t0)
	r := s.Doctor()
	if !r.OK {
		t.Errorf("doctor should be OK after a clean promote; note=%q", r.Note)
	}
	want := map[string]string{"capture": "ok", "promotion": "ok", "sync/embedding": "skipped", "retrieval": "ok", "authority": "ok"}
	for _, h := range r.Hops {
		if exp, ok := want[h.Name]; ok && h.Status != exp {
			t.Errorf("hop %q status = %q, want %q (%s)", h.Name, h.Status, exp, h.Detail)
		}
	}
}

func objs(cs []Claim) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Object
	}
	return out
}
