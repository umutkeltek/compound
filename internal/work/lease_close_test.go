package work

// Close-gate lease enforcement (2026-07-09 packet). Tonight's twin-collisions (see
// docs_private/packets/2026-07-09-close-gate-lease.md) showed leases exist but nothing
// enforces them at close: a non-holder actor could close a work another actor's executor
// was still mid-build on. These tests pin the falsifier list from that packet.

import (
	"strings"
	"testing"
)

// Falsifier 1 (RED-first): a work claimed by one actor is closeable by a different actor
// with no steal on today's code. This is the exact regression of tonight's canon_class
// close. On the gated code this must refuse, naming the holder and the remedy paths.
func TestCloseRefusesNonLeaseHolder(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "lane-w", SuccessPredicate: "lease enforced"}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Claim(w.ID, "actor-a", fixedNow); err != nil {
		t.Fatalf("claim: %v", err)
	}
	_, err = s.Close(w.ID, CloseInput{Outcome: "cancelled", MoveType: "audit_only", Actor: "actor-b"}, fixedNow)
	if err == nil {
		t.Fatal("expected close by non-holder actor-b to be refused (work held by actor-a)")
	}
	if !strings.Contains(err.Error(), "actor-a") {
		t.Fatalf("refusal error must name the holder actor-a: %v", err)
	}
	if !strings.Contains(err.Error(), "--steal") {
		t.Fatalf("refusal error must name the --steal remedy: %v", err)
	}
	after, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "claimed" {
		t.Fatalf("refused close must not mutate work state: got %q", after.State)
	}
}
