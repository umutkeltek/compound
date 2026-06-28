package work

import (
	"testing"
	"time"

	"compound/internal/envelope"
)

var fixedNow = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s
}

// The headline proof for P3: one work_id threads create -> claim -> advance -> close,
// and close emits a Receipt and the apex Transition that BOTH carry that work_id and
// reference the chain (context pack -> runs -> artifacts).
func TestWorkLifecycleThreadsOneID(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "memory-lane", SuccessPredicate: "timeline shows old->new", ContextPackID: "ctx_abc123", DesiredOutcome: "alive memory"}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.State != "ready" {
		t.Fatalf("want ready, got %q", w.State)
	}
	id := w.ID

	if _, err := s.Claim(id, "claude_code", fixedNow); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.Advance(id, AdvanceInput{To: "running", RunID: "run_001", Note: "executing"}, fixedNow); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := s.Advance(id, AdvanceInput{To: "verifying", ArtifactID: "artifact_timeline", VerificationID: "ver_001"}, fixedNow); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}
	res, err := s.Close(id, CloseInput{Outcome: "completed", MoveType: "memory_delta", Summary: "timeline capability proven", Changed: true}, fixedNow)
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	// Every emitted envelope is valid and round-trips through the spine.
	for name, e := range map[string]envelope.Envelope{"work": &res.Work, "receipt": &res.Receipt, "transition": &res.Transition} {
		if err := e.Validate(); err != nil {
			t.Errorf("%s invalid: %v", name, err)
		}
		if err := envelope.RoundTrip(e); err != nil {
			t.Errorf("%s round-trip: %v", name, err)
		}
	}

	// The single work_id threads through receipt and transition.
	if res.Receipt.IDs.WorkID != id {
		t.Errorf("receipt work_id = %q, want %q", res.Receipt.IDs.WorkID, id)
	}
	if res.Transition.IDs.WorkID != id {
		t.Errorf("transition work_id = %q, want %q", res.Transition.IDs.WorkID, id)
	}
	if res.Work.State != "completed" {
		t.Errorf("final state = %q, want completed", res.Work.State)
	}
	if res.Work.IDs.TransitionID != res.Transition.ID {
		t.Errorf("work not linked to its closing transition")
	}

	// The transition references the chain: context pack (before), run (action), artifact+receipt (after).
	if got := res.Transition.BeforeRefs; len(got) != 1 || got[0] != "ctx_abc123" {
		t.Errorf("transition before_refs = %v, want [ctx_abc123]", got)
	}
	if !contains(res.Transition.ActionRefs, "run_001") {
		t.Errorf("transition action_refs = %v, want it to include run_001", res.Transition.ActionRefs)
	}
	if !contains(res.Transition.AfterRefs, "artifact_timeline") || !contains(res.Transition.AfterRefs, res.Receipt.ID) {
		t.Errorf("transition after_refs = %v, want artifact_timeline + receipt id", res.Transition.AfterRefs)
	}
	if res.Receipt.MoveType != "memory_delta" {
		t.Errorf("receipt move_type = %q", res.Receipt.MoveType)
	}

	// The lifecycle log records the full sequence for this work.
	_, evs, err := s.Show(id)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	gotStates := make([]string, len(evs))
	for i, e := range evs {
		gotStates[i] = e.To
	}
	want := []string{"ready", "claimed", "running", "verifying", "completed"}
	if len(gotStates) != len(want) {
		t.Fatalf("event states = %v, want %v", gotStates, want)
	}
	for i := range want {
		if gotStates[i] != want[i] {
			t.Errorf("event[%d] = %q, want %q", i, gotStates[i], want[i])
		}
	}
}

func TestIllegalTransitionRejected(t *testing.T) {
	s := newStore(t)
	w, _ := s.Create(CreateInput{Owner: "o", SuccessPredicate: "p"}, fixedNow)
	// ready cannot jump straight to completed.
	if _, err := s.Advance(w.ID, AdvanceInput{To: "completed"}, fixedNow); err == nil {
		t.Error("expected illegal transition ready->completed to be rejected")
	}
	// cannot close from ready either (no edge to a terminal except via cancel).
	if _, err := s.Close(w.ID, CloseInput{Outcome: "completed", MoveType: "outward_delta"}, fixedNow); err == nil {
		t.Error("expected close ready->completed to be rejected")
	}
	// ready CAN be cancelled.
	if _, err := s.Close(w.ID, CloseInput{Outcome: "cancelled", MoveType: "audit_only"}, fixedNow); err != nil {
		t.Errorf("ready->cancelled should be allowed: %v", err)
	}
}

func TestClaimConflict(t *testing.T) {
	s := newStore(t)
	w, _ := s.Create(CreateInput{Owner: "o", SuccessPredicate: "p"}, fixedNow)
	if _, err := s.Claim(w.ID, "agent_a", fixedNow); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, err := s.Claim(w.ID, "agent_b", fixedNow); err == nil {
		t.Error("expected second claimant to be rejected (claim before you touch)")
	}
	// Same actor re-claims idempotently.
	if _, err := s.Claim(w.ID, "agent_a", fixedNow); err != nil {
		t.Errorf("idempotent re-claim by same actor should succeed: %v", err)
	}
}

func TestCreateRequiresOwnerAndPredicate(t *testing.T) {
	s := newStore(t)
	if _, err := s.Create(CreateInput{Owner: "", SuccessPredicate: "p"}, fixedNow); err == nil {
		t.Error("create without owner should fail")
	}
	if _, err := s.Create(CreateInput{Owner: "o", SuccessPredicate: ""}, fixedNow); err == nil {
		t.Error("create without success_predicate should fail")
	}
}

func TestTerminalCannotAdvance(t *testing.T) {
	s := newStore(t)
	w, _ := s.Create(CreateInput{Owner: "o", SuccessPredicate: "p"}, fixedNow)
	if _, err := s.Close(w.ID, CloseInput{Outcome: "cancelled", MoveType: "audit_only"}, fixedNow); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "running"}, fixedNow); err == nil {
		t.Error("advancing a terminal work should fail")
	}
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}
