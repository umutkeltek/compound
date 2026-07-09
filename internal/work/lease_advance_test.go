package work

// Advance-gate lease enforcement. Store.Close got the lease-holder gate; Advance was the
// last ungated mutation verb left — a non-holder actor could walk a claimed envelope
// running->verifying->done-adjacent states, corrupting the holder's liveness signal. These
// tests pin the falsifier list for the Advance gate, mirroring lease_close_test.go's shape
// for Close.

import (
	"strings"
	"testing"
)

// Falsifier 1 (RED-first): a work claimed by one actor is advanceable by a different actor
// with no steal on today's code. On the gated code this must refuse, naming the holder and
// both remedies (--by <holder> or --steal --reason), and leave the work state untouched.
func TestAdvanceRefusesNonLeaseHolder(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "lane-ag", SuccessPredicate: "advance lease enforced"}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Claim(w.ID, "actor-a", fixedNow); err != nil {
		t.Fatalf("claim: %v", err)
	}
	_, err = s.Advance(w.ID, AdvanceInput{To: "running", Actor: "actor-b"}, fixedNow)
	if err == nil {
		t.Fatal("expected advance by non-holder actor-b to be refused (work held by actor-a)")
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
		t.Fatalf("refused advance must not mutate work state: got %q", after.State)
	}
}
