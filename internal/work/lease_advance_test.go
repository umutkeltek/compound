package work

// Advance-gate lease enforcement. Store.Close got the lease-holder gate; Advance was the
// last ungated mutation verb left — a non-holder actor could walk a claimed envelope
// running->verifying->done-adjacent states, corrupting the holder's liveness signal. These
// tests pin the falsifier list f1-f7 for the Advance gate, mirroring lease_close_test.go's
// shape for Close.

import (
	"strings"
	"testing"
)

// Falsifier 1 (RED-first): a work claimed by one actor is advanceable by a different actor
// with no steal on ungated code. On the gated code this must refuse, naming the holder and
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
	if !strings.Contains(err.Error(), "--by") || !strings.Contains(err.Error(), "--steal") {
		t.Fatalf("refusal error must name both remedies (--by / --steal): %v", err)
	}
	after, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "claimed" {
		t.Fatalf("refused advance must not mutate work state: got %q", after.State)
	}
	_, evs, err := s.Show(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evs {
		if strings.Contains(e.Note, "refused") {
			found = true
			if e.By != "actor-b" {
				t.Fatalf("refusal event must be attributed to the refused actor actor-b, got %q", e.By)
			}
		}
	}
	if !found {
		t.Fatal("trail missing a refusal event")
	}
}

// Falsifier 2: --steal with an empty reason refuses exactly like a plain non-holder advance
// (no mutation).
func TestAdvanceStealWithoutReasonRefused(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "lane-ag", SuccessPredicate: "steal without reason"}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Claim(w.ID, "actor-a", fixedNow); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "running", Actor: "actor-b", Steal: true}, fixedNow); err == nil {
		t.Fatal("expected --steal with empty reason to be refused")
	}
	after, err := s.Get(w.ID)
	if err != nil || after.State != "claimed" {
		t.Fatalf("empty-reason steal attempt must not mutate work: state=%q err=%v", after.State, err)
	}
}

// Falsifier 3: a valid steal (--steal --reason "<non-empty>") proceeds; the trail carries
// the prior holder, the reason string, and the stealing actor.
func TestAdvanceStealOverridesLeaseHolder(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "lane-ag", SuccessPredicate: "steal path"}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Claim(w.ID, "actor-a", fixedNow); err != nil {
		t.Fatalf("claim: %v", err)
	}
	advanced, err := s.Advance(w.ID, AdvanceInput{
		To:          "running",
		Actor:       "actor-b",
		Steal:       true,
		StealReason: "actor-a's executor died mid-build",
	}, fixedNow)
	if err != nil {
		t.Fatalf("expected steal with non-empty reason to succeed: %v", err)
	}
	if advanced.State != "running" {
		t.Fatalf("stolen advance should land the new state, got %q", advanced.State)
	}
	_, evs, err := s.Show(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	var stealNote string
	for _, e := range evs {
		if strings.Contains(e.Note, "stolen") {
			stealNote = e.Note
			if e.By != "actor-b" {
				t.Fatalf("steal event must be attributed to the stealing actor actor-b, got %q", e.By)
			}
		}
	}
	if stealNote == "" {
		t.Fatal("trail missing a steal event")
	}
	if !strings.Contains(stealNote, "actor-a") {
		t.Fatalf("steal event must name the prior holder actor-a: %q", stealNote)
	}
	if !strings.Contains(stealNote, "actor-a's executor died mid-build") {
		t.Fatalf("steal event must carry the reason string: %q", stealNote)
	}
}

// Falsifier 4 (part 1): claimed by A, advanced by A — byte-identical to pre-gate behavior,
// no extra refusal/steal events land on the trail.
func TestAdvanceSameActorPassesUnchanged(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "lane-ag", SuccessPredicate: "same actor"}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Claim(w.ID, "actor-a", fixedNow); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "running", Actor: "actor-a"}, fixedNow); err != nil {
		t.Fatalf("same-actor advance should succeed: %v", err)
	}
	_, evs, err := s.Show(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantStates := []string{"ready", "claimed", "running"}
	if len(evs) != len(wantStates) {
		t.Fatalf("same-actor advance must add no gate events beyond the normal chain: got %d events %+v, want %d", len(evs), evs, len(wantStates))
	}
	for i, want := range wantStates {
		if evs[i].To != want {
			t.Errorf("event[%d].To = %q, want %q", i, evs[i].To, want)
		}
		if strings.Contains(evs[i].Note, "refused") || strings.Contains(evs[i].Note, "stolen") {
			t.Errorf("same-actor advance must not add a lease-gate event: %+v", evs[i])
		}
	}
}

// Falsifier 4 (part 2): advance on a never-claimed work by anyone succeeds exactly as
// before — no lease means no gate interference, and no new envelopes are written for the
// no-op gate path.
func TestAdvanceUnclaimedWorkUnaffected(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "lane-ag", SuccessPredicate: "never claimed"}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	advanced, err := s.Advance(w.ID, AdvanceInput{To: "held", Actor: "whoever"}, fixedNow)
	if err != nil {
		t.Fatalf("unclaimed advance should succeed unconditionally: %v", err)
	}
	if advanced.State != "held" {
		t.Fatalf("unclaimed advance should land the new state, got %q", advanced.State)
	}
	_, evs, err := s.Show(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[0].To != "ready" || evs[1].To != "held" {
		t.Fatalf("unclaimed advance must be the plain create+advance chain: %+v", evs)
	}
}

// Falsifier 6: AdmitAuto's applyAdvance direct path is provably NOT behind the gate. The
// gate lives in Advance's guard chain (autonomyHaltGuard, editableSurfaceGuard,
// heldReleaseOperatorError, directorProposedReleaseOperatorError, leaseHolderAdvanceGate)
// and applyAdvance itself stays ungated on purpose — AdmitAuto calls applyAdvance directly
// (see AdmitAuto in auto_admit.go), so a clean-window eligible auto-admit succeeds with no
// Steal/StealReason set at all, proposed->admitted, exactly as the pre-existing
// auto_admit_test.go suite (left unmodified by this change) already proves.
func TestAdvanceAutoAdmitBypassesLeaseGateByDesign(t *testing.T) {
	s := newStore(t)
	eligible := typedJobHealthEligibility(t, s, fixedNow)
	id := makeProposedDirectorWork(t, s, "job_health", "work_lease_gate_bypass", fixedNow)
	res, err := s.AdmitAuto(id, eligible, fixedNow)
	if err != nil {
		t.Fatalf("AdmitAuto must not be gated by the advance lease gate: %v", err)
	}
	if !res.Admitted || res.WorkState != "admitted" {
		t.Fatalf("AdmitAuto through applyAdvance should reach admitted ungated: %+v", res)
	}
}

// Falsifier 7 (close-gate regression net): proven by lease_close_test.go and
// close_plan_test.go, both left byte-for-byte unmodified by this change, continuing to pass
// in the same `go test ./internal/work/...` run as these advance-gate tests.
