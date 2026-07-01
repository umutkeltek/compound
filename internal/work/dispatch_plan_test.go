package work

import (
	"os"
	"path/filepath"
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

func TestDispatchPlanImportsSourcePacketAndSelectsReadyWork(t *testing.T) {
	s := newStore(t)
	packetPath := filepath.Join("..", "..", "fixtures", "work", "goal-packet.md")
	plan, err := s.DispatchPlan(fixedNow, DispatchPlanOptions{
		Actor:            "codex",
		Record:           true,
		SourcePacketPath: packetPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Source == nil || plan.Source.Kind != "packet" || !plan.Source.Created {
		t.Fatalf("source import missing: %+v", plan.Source)
	}
	if plan.Status != "ready_to_claim" || plan.Selected == nil {
		t.Fatalf("packet-sourced work should be claimable in plan: %+v", plan)
	}
	if plan.Selected.WorkID != plan.Source.WorkID || plan.Selected.Action != "claim" {
		t.Fatalf("selected = %+v, source = %+v", plan.Selected, plan.Source)
	}
	if len(plan.Selected.Command) != 6 || plan.Selected.Command[0] != "compound" || plan.Selected.Command[2] != "claim" {
		t.Fatalf("claim command = %v", plan.Selected.Command)
	}
	w, err := s.Get(plan.Source.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	if w.State != "ready" {
		t.Fatalf("source packet work state = %q, want ready", w.State)
	}
	if _, err := s.readLease(w.ID); err == nil {
		t.Fatal("dispatch plan must not claim packet-sourced work")
	}
	if plan.PlanPath == "" {
		t.Fatalf("recorded packet plan missing path: %+v", plan)
	}
}

func TestDispatchClaimClaimsSelectedReadyWork(t *testing.T) {
	s := newStore(t)
	ready, err := s.Create(CreateInput{
		Owner:            "compound-core",
		SuccessPredicate: "dispatch claim selects work",
		DesiredOutcome:   "prove dispatch claim",
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchClaim(fixedNow, DispatchPlanOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "claimed" || result.ClaimedWorkID != ready.ID || result.PolicyDecisionID == "" || result.ActionIntentID == "" {
		t.Fatalf("dispatch claim result = %+v, ready=%s", result, ready.ID)
	}
	if result.DispatchPlanPath == "" || result.ResultPath == "" {
		t.Fatalf("dispatch claim must record plan and result: %+v", result)
	}
	if _, err := os.Stat(result.DispatchPlanPath); err != nil {
		t.Fatalf("dispatch plan not recorded: %v", err)
	}
	if _, err := os.Stat(result.ResultPath); err != nil {
		t.Fatalf("dispatch claim result not recorded: %v", err)
	}
	after, err := s.Get(ready.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "claimed" || after.Authority.Owner != "codex" || after.PolicyDecisionID != result.PolicyDecisionID {
		t.Fatalf("work not claimed through policy path: %+v result=%+v", after, result)
	}
	if _, err := s.readLease(ready.ID); err != nil {
		t.Fatalf("dispatch claim must write lease: %v", err)
	}
	if _, err := os.Stat(s.envPath(result.ActionIntentID)); err != nil {
		t.Fatalf("claim action envelope missing: %v", err)
	}
	if _, err := os.Stat(s.envPath(result.PolicyDecisionID)); err != nil {
		t.Fatalf("claim policy envelope missing: %v", err)
	}
}

func TestDispatchClaimBlockedByPolicyDoesNotClaim(t *testing.T) {
	s := newStore(t)
	if err := os.MkdirAll(filepath.Join(s.home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	policyYAML := []byte(`class_outcome:
  local_reversible:
    outcome: hold
`)
	if err := os.WriteFile(filepath.Join(s.home, "config", "policy.yaml"), policyYAML, 0o644); err != nil {
		t.Fatal(err)
	}
	ready, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "policy hold blocks dispatch claim"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchClaim(fixedNow, DispatchPlanOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "claim_policy_blocked" || result.ClaimedWorkID != "" || result.Selected != nil {
		t.Fatalf("blocked dispatch claim result = %+v", result)
	}
	if result.ResultPath == "" || result.DispatchPlanPath == "" {
		t.Fatalf("blocked dispatch claim should record plan/result evidence: %+v", result)
	}
	after, err := s.Get(ready.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "ready" || len(after.RunIDs) != 0 || after.PolicyDecisionID != "" {
		t.Fatalf("blocked dispatch claim mutated work: %+v", after)
	}
	if _, err := s.readLease(ready.ID); err == nil {
		t.Fatal("blocked dispatch claim must not write lease")
	}
}
