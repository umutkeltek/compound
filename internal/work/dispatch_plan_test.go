package work

import (
	"os"
	"testing"
)

func TestDispatchPlanSelectsReadyWorkWithoutClaiming(t *testing.T) {
	s := newStore(t)
	held, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "held needs review", State: "held"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := s.Create(CreateInput{
		Owner:            "compound-core",
		SuccessPredicate: "ready work gets dispatched",
		DesiredOutcome:   "prove dispatch proposal",
		EvidenceRefs:     []string{"test://dispatch"},
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.DispatchPlan(fixedNow, DispatchPlanOptions{Actor: "codex", Record: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != "ready_to_claim" || plan.Selected == nil {
		t.Fatalf("plan should select claimable work: %+v", plan)
	}
	if plan.Selected.WorkID != ready.ID || plan.Selected.Action != "claim" {
		t.Fatalf("selected = %+v, ready=%s held=%s", plan.Selected, ready.ID, held.ID)
	}
	if got := plan.Selected.Command; len(got) != 6 || got[0] != "compound" || got[2] != "claim" || got[3] != ready.ID || got[4] != "--by" || got[5] != "codex" {
		t.Fatalf("claim command = %v", got)
	}
	if plan.PlanPath == "" {
		t.Fatalf("recorded plan missing path: %+v", plan)
	}
	if _, err := os.Stat(plan.PlanPath); err != nil {
		t.Fatalf("dispatch plan not recorded: %v", err)
	}
	after, err := s.Get(ready.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "ready" {
		t.Fatalf("dispatch plan must not claim work, state=%s", after.State)
	}
	if _, err := s.readLease(ready.ID); err == nil {
		t.Fatalf("dispatch plan must not write lease for %s", ready.ID)
	}
}

func TestDispatchPlanNoopsWhenNoOpenWork(t *testing.T) {
	s := newStore(t)
	plan, err := s.DispatchPlan(fixedNow, DispatchPlanOptions{Actor: "codex", Record: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != "no_open_work" || plan.NoAction == "" || plan.Selected != nil || len(plan.Candidates) != 0 {
		t.Fatalf("empty workbench plan = %+v", plan)
	}
	if plan.PlanPath == "" {
		t.Fatalf("recorded no-op plan missing path: %+v", plan)
	}
}
