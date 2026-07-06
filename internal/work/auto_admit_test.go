package work

import (
	"strings"
	"testing"
	"time"

	"compound/internal/policy"
)

// makeProposedDirectorWork creates a director-proposed work of the given autonomy class,
// in state `proposed`, carrying the director provenance + trigger evidence refs the
// kill-switch keys on.
func makeProposedDirectorWork(t *testing.T, s *Store, class, id string, now time.Time) string {
	t.Helper()
	w, err := s.Create(CreateInput{
		WorkID:           id,
		Owner:            "compound-authority",
		Actor:            "director",
		SuccessPredicate: "review " + class + " signal " + id,
		State:            "proposed",
		EvidenceRefs:     []string{"proposed_by:director", "director_trigger:" + class + ":" + id},
	}, now)
	if err != nil {
		t.Fatalf("create proposed %s work: %v", class, err)
	}
	if w.State != "proposed" {
		t.Fatalf("director work should start proposed, got %q", w.State)
	}
	return w.ID
}

// TestKillSwitchHaltsAutoAdmit is the headline W9-J4 proof: a simulated trust-dial
// auto-admit of an ELIGIBLE class is denied while the policy-owned halt is engaged, a
// per-class disable blocks only its own class, and an unset halt lets the admit proceed.
// It fails if any auto-admit path ignores the flag.
func TestKillSwitchHaltsAutoAdmit(t *testing.T) {
	s := newStore(t)
	eligible := AutoAdmitInput{By: "director", EligibilityReason: "clean-window: 7 clean runs"}

	// Phase A — halt UNSET: an eligible auto-admit proceeds to admitted.
	jobA := makeProposedDirectorWork(t, s, "job_health", "work_kill_a", fixedNow)
	resA, err := s.AdmitAuto(jobA, eligible, fixedNow.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("unset halt must let eligible auto-admit proceed: %v", err)
	}
	if !resA.Admitted || resA.WorkState != "admitted" {
		t.Fatalf("unset halt auto-admit should reach admitted: %+v", resA)
	}
	if got, _ := s.Get(jobA); got.State != "admitted" {
		t.Fatalf("unset halt work should be admitted on disk, got %q", got.State)
	}

	// Phase B — GLOBAL halt: the same eligible auto-admit is denied and stays proposed.
	if err := policy.WriteAutonomyHalt(s.home, policy.AutonomyHalt{Global: true, Reason: "incident review"}); err != nil {
		t.Fatalf("engage global halt: %v", err)
	}
	jobB := makeProposedDirectorWork(t, s, "job_health", "work_kill_b", fixedNow)
	resB, err := s.AdmitAuto(jobB, eligible, fixedNow.Add(2*time.Minute))
	if err == nil {
		t.Fatalf("global halt must deny an eligible auto-admit")
	}
	if resB.Admitted || !resB.Halted {
		t.Fatalf("global halt result should be halted, not admitted: %+v", resB)
	}
	if got, _ := s.Get(jobB); got.State != "proposed" {
		t.Fatalf("global halt must leave work proposed, got %q", got.State)
	}

	// Phase C — PER-CLASS disable: only the disabled class is blocked; a sibling proceeds.
	if err := policy.WriteAutonomyHalt(s.home, policy.AutonomyHalt{
		DisabledClasses: map[string]string{"job_health": "noisy class parked"},
	}); err != nil {
		t.Fatalf("engage per-class halt: %v", err)
	}
	jobC := makeProposedDirectorWork(t, s, "job_health", "work_kill_c", fixedNow)
	srcC := makeProposedDirectorWork(t, s, "source_connection_readiness", "work_kill_src", fixedNow)

	resC, err := s.AdmitAuto(jobC, eligible, fixedNow.Add(3*time.Minute))
	if err == nil || resC.Admitted {
		t.Fatalf("per-class disable must block its own class: err=%v res=%+v", err, resC)
	}
	if got, _ := s.Get(jobC); got.State != "proposed" {
		t.Fatalf("disabled class must stay proposed, got %q", got.State)
	}

	resSrc, err := s.AdmitAuto(srcC, eligible, fixedNow.Add(4*time.Minute))
	if err != nil || !resSrc.Admitted {
		t.Fatalf("a non-disabled class must still auto-admit: err=%v res=%+v", err, resSrc)
	}
	if got, _ := s.Get(srcC); got.State != "admitted" {
		t.Fatalf("non-disabled class should be admitted, got %q", got.State)
	}
}

// TestKillSwitchDoesNotBlockOperatorAdmit is the required negative: a manual operator
// admit through the admission seam is UNAFFECTED by the kill-switch, even under a global
// halt. Governance halts autonomy, not the operator.
func TestKillSwitchDoesNotBlockOperatorAdmit(t *testing.T) {
	s := newStore(t)
	if err := policy.WriteAutonomyHalt(s.home, policy.AutonomyHalt{Global: true, Reason: "incident review"}); err != nil {
		t.Fatalf("engage global halt: %v", err)
	}
	id := makeProposedDirectorWork(t, s, "job_health", "work_op_admit", fixedNow)

	out, err := s.Advance(id, AdvanceInput{Actor: "operator-test", To: "admitted"}, fixedNow.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("operator admit must not be blocked by autonomy halt: %v", err)
	}
	if out.State != "admitted" {
		t.Fatalf("operator admit should reach admitted under halt, got %q", out.State)
	}
}

// TestKillSwitchHaltsAdvanceSeamDefenseInDepth proves the generic Advance seam carries its
// own halt check: an autonomous (non-operator) admission is stopped BY THE HALT, not merely
// by the operator-provenance guard, so no auto-admit path can route around the flag.
func TestKillSwitchHaltsAdvanceSeamDefenseInDepth(t *testing.T) {
	s := newStore(t)
	if err := policy.WriteAutonomyHalt(s.home, policy.AutonomyHalt{Global: true, Reason: "incident review"}); err != nil {
		t.Fatalf("engage global halt: %v", err)
	}
	id := makeProposedDirectorWork(t, s, "job_health", "work_did", fixedNow)

	_, err := s.Advance(id, AdvanceInput{Actor: "auto-admit:director", To: "admitted"}, fixedNow.Add(1*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "autonomy halted") {
		t.Fatalf("generic seam must halt an autonomous admission with an autonomy-halt error, got %v", err)
	}
	if got, _ := s.Get(id); got.State != "proposed" {
		t.Fatalf("halted advance must leave work proposed, got %q", got.State)
	}
}

// TestAutoAdmitRequiresEligibilityGrant proves J4 ships zero live autonomy: without a
// caller-supplied clean-window grant (which W9-J6 will provide), nothing auto-admits even
// with the halt unset.
func TestAutoAdmitRequiresEligibilityGrant(t *testing.T) {
	s := newStore(t)
	id := makeProposedDirectorWork(t, s, "job_health", "work_elig", fixedNow)

	res, err := s.AdmitAuto(id, AutoAdmitInput{By: "director"}, fixedNow.Add(1*time.Minute))
	if err == nil {
		t.Fatalf("auto-admit without a clean-window grant must be denied")
	}
	if res.Admitted {
		t.Fatalf("ineligible auto-admit must not admit: %+v", res)
	}
	if got, _ := s.Get(id); got.State != "proposed" {
		t.Fatalf("ineligible auto-admit must stay proposed, got %q", got.State)
	}
}
