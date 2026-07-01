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
	ready, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "policy hold blocks dispatch claim"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	baselineRuns := append([]string{}, ready.RunIDs...)
	baselinePolicy := ready.PolicyDecisionID
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
	if after.State != "ready" || len(after.RunIDs) != len(baselineRuns) || after.PolicyDecisionID != baselinePolicy {
		t.Fatalf("blocked dispatch claim mutated work: %+v", after)
	}
	if _, err := s.readLease(ready.ID); err == nil {
		t.Fatal("blocked dispatch claim must not write lease")
	}
}

func TestDispatchAdvanceAdvancesOwnClaimedWork(t *testing.T) {
	s := newStore(t)
	ready, err := s.Create(CreateInput{
		Owner:            "compound-core",
		SuccessPredicate: "dispatch advance starts claimed work",
		DesiredOutcome:   "prove dispatch advance",
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(ready.ID, "codex", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchAdvance(fixedNow, DispatchPlanOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "running" || result.AdvancedWorkID != ready.ID || result.AdvancedState != "running" {
		t.Fatalf("dispatch advance result = %+v, ready=%s", result, ready.ID)
	}
	if result.Selected == nil || result.Selected.Action != "advance_running" || result.Selected.AdvancePolicy == nil || !result.Selected.AdvancePolicy.Allowed {
		t.Fatalf("dispatch advance selected item missing policy: %+v", result.Selected)
	}
	if result.ResultPath == "" {
		t.Fatalf("dispatch advance should record result: %+v", result)
	}
	if _, err := os.Stat(result.ResultPath); err != nil {
		t.Fatalf("dispatch advance result not recorded: %v", err)
	}
	after, err := s.Get(ready.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "running" || after.PolicyDecisionID != result.PolicyDecisionID || len(after.RunIDs) != len(claimed.RunIDs)+1 {
		t.Fatalf("work not advanced through policy path: before=%+v after=%+v result=%+v", claimed, after, result)
	}
	if after.IDs.TransitionID != "" {
		t.Fatalf("dispatch advance must not close work: %+v", after.IDs)
	}
}

func TestDispatchAdvanceIgnoresOtherActorClaim(t *testing.T) {
	s := newStore(t)
	ready, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "other actor claim is not advanced"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(ready.ID, "agent_a", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchAdvance(fixedNow, DispatchPlanOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "claimed_by_other" || result.AdvancedWorkID != "" || result.Selected != nil {
		t.Fatalf("other-actor dispatch advance result = %+v", result)
	}
	after, err := s.Get(ready.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "claimed" || len(after.RunIDs) != len(claimed.RunIDs) || after.PolicyDecisionID != claimed.PolicyDecisionID {
		t.Fatalf("other-actor dispatch advance mutated work: %+v", after)
	}
}

func TestDispatchAdvanceBlockedByPolicyDoesNotAdvance(t *testing.T) {
	s := newStore(t)
	ready, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "policy hold blocks dispatch advance"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(ready.ID, "codex", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	baselineRuns := append([]string{}, claimed.RunIDs...)
	baselinePolicy := claimed.PolicyDecisionID
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
	result, err := s.DispatchAdvance(fixedNow, DispatchPlanOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "advance_policy_blocked" || result.AdvancedWorkID != "" || result.Selected != nil {
		t.Fatalf("blocked dispatch advance result = %+v", result)
	}
	if result.ResultPath == "" {
		t.Fatalf("blocked dispatch advance should record result: %+v", result)
	}
	after, err := s.Get(ready.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "claimed" || len(after.RunIDs) != len(baselineRuns) || after.PolicyDecisionID != baselinePolicy {
		t.Fatalf("blocked dispatch advance mutated work: %+v", after)
	}
}

func TestDispatchClosePlanRecordsClosePlanWithoutClosing(t *testing.T) {
	s := newStore(t)
	w := runningWork(t, s, "dispatch close-plan uses observed proof")
	trace := writeTrace(t, s, "trace_dispatch_close_plan_observed", w.ID)
	observed, err := s.ObserveRun(w.ID, ObserveRunInput{
		Actor:   "codex",
		TraceID: trace.ID,
		Note:    "ready for close planning",
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchClosePlan(fixedNow, DispatchClosePlanOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "close_policy_allowed" || result.Selected == nil || result.Selected.WorkID != w.ID {
		t.Fatalf("dispatch close-plan result = %+v, work=%s", result, w.ID)
	}
	if result.Selected.ClosePolicy == nil || !result.Selected.ClosePolicy.Allowed {
		t.Fatalf("selected item missing allowed close policy: %+v", result.Selected)
	}
	if result.ClosePlan == nil || result.ClosePlanID == "" || result.ClosePlanPath == "" || result.ResultPath == "" {
		t.Fatalf("dispatch close-plan did not record close plan/result: %+v", result)
	}
	if _, err := os.Stat(result.ClosePlanPath); err != nil {
		t.Fatalf("close plan not recorded: %v", err)
	}
	if _, err := os.Stat(result.ResultPath); err != nil {
		t.Fatalf("dispatch close-plan result not recorded: %v", err)
	}
	if !contains(result.ClosePlan.ProofRefs, trace.ID) || len(result.ClosePlan.RunObservations) != 1 || result.ClosePlan.RunObservations[0].ID != observed.ID {
		t.Fatalf("close plan missing observed proof: proof=%v observations=%+v", result.ClosePlan.ProofRefs, result.ClosePlan.RunObservations)
	}
	after, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "verifying" || after.IDs.TransitionID != "" {
		t.Fatalf("dispatch close-plan must not close work: %+v", after)
	}
}

func TestDispatchClosePlanRequiresProofEvidence(t *testing.T) {
	s := newStore(t)
	w := runningWork(t, s, "dispatch close-plan waits for proof")
	verifying, err := s.Advance(w.ID, AdvanceInput{To: "verifying", Actor: "codex"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchClosePlan(fixedNow, DispatchClosePlanOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "awaiting_proof" || result.Selected != nil || result.ClosePlan != nil {
		t.Fatalf("dispatch close-plan should wait for proof: %+v", result)
	}
	if result.ResultPath == "" {
		t.Fatalf("awaiting-proof dispatch close-plan should record result: %+v", result)
	}
	after, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "verifying" || len(after.RunIDs) != len(verifying.RunIDs) || after.IDs.TransitionID != "" {
		t.Fatalf("awaiting-proof dispatch close-plan mutated work: %+v", after)
	}
}

func TestDispatchClosePlanBlockedByPolicyRecordsPlanWithoutClosing(t *testing.T) {
	s := newStore(t)
	w := runningWork(t, s, "dispatch close-plan policy hold")
	trace := writeTrace(t, s, "trace_dispatch_close_plan_blocked", w.ID)
	observed, err := s.ObserveRun(w.ID, ObserveRunInput{Actor: "codex", TraceID: trace.ID}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	baselineRuns := append([]string{}, observed.Work.RunIDs...)
	baselinePolicy := observed.Work.PolicyDecisionID
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
	result, err := s.DispatchClosePlan(fixedNow, DispatchClosePlanOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "close_policy_blocked" || result.Selected == nil || result.ClosePlan == nil || result.ClosePlan.Policy.Allowed {
		t.Fatalf("blocked dispatch close-plan result = %+v", result)
	}
	if result.ClosePlanPath == "" || result.ResultPath == "" {
		t.Fatalf("blocked dispatch close-plan should record close plan/result: %+v", result)
	}
	after, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "verifying" || len(after.RunIDs) != len(baselineRuns) || after.PolicyDecisionID != baselinePolicy || after.IDs.TransitionID != "" {
		t.Fatalf("blocked dispatch close-plan mutated work: %+v", after)
	}
}

func TestDispatchCloseClosesProofReadyWork(t *testing.T) {
	s := newStore(t)
	w := runningWork(t, s, "dispatch close terminalizes observed proof")
	trace := writeTrace(t, s, "trace_dispatch_close_observed", w.ID)
	observed, err := s.ObserveRun(w.ID, ObserveRunInput{Actor: "codex", TraceID: trace.ID}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchClose(fixedNow, DispatchCloseOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "closed" || result.ClosedWorkID != w.ID || result.FinalState != "completed" {
		t.Fatalf("dispatch close result = %+v, work=%s", result, w.ID)
	}
	if result.DispatchClosePlanID == "" || result.DispatchClosePlanPath == "" || result.ResultPath == "" {
		t.Fatalf("dispatch close did not record plan/result: %+v", result)
	}
	if result.ClosePlanID == "" || result.ReceiptID == "" || result.TransitionID == "" {
		t.Fatalf("dispatch close missing close proof ids: %+v", result)
	}
	if result.CloseResult == nil || !contains(result.CloseResult.Receipt.StateDelta.Verification, trace.ID) {
		t.Fatalf("dispatch close missing receipt verification proof: %+v", result.CloseResult)
	}
	after, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "completed" || after.IDs.TransitionID != result.TransitionID || len(after.RunIDs) != len(observed.Work.RunIDs)+1 {
		t.Fatalf("dispatch close did not terminalize through work close: %+v", after)
	}
}

func TestDispatchCloseRequiresProofEvidence(t *testing.T) {
	s := newStore(t)
	w := runningWork(t, s, "dispatch close waits for proof")
	verifying, err := s.Advance(w.ID, AdvanceInput{To: "verifying", Actor: "codex"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchClose(fixedNow, DispatchCloseOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "awaiting_proof" || result.ClosedWorkID != "" || result.CloseResult != nil {
		t.Fatalf("dispatch close should wait for proof: %+v", result)
	}
	if result.DispatchClosePlanPath == "" || result.ResultPath == "" {
		t.Fatalf("dispatch close should record no-op plan/result: %+v", result)
	}
	after, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "verifying" || len(after.RunIDs) != len(verifying.RunIDs) || after.IDs.TransitionID != "" {
		t.Fatalf("awaiting-proof dispatch close mutated work: %+v", after)
	}
}

func TestDispatchCloseBlockedByPolicyDoesNotClose(t *testing.T) {
	s := newStore(t)
	w := runningWork(t, s, "dispatch close policy hold")
	trace := writeTrace(t, s, "trace_dispatch_close_blocked", w.ID)
	observed, err := s.ObserveRun(w.ID, ObserveRunInput{Actor: "codex", TraceID: trace.ID}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	baselineRuns := append([]string{}, observed.Work.RunIDs...)
	baselinePolicy := observed.Work.PolicyDecisionID
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
	result, err := s.DispatchClose(fixedNow, DispatchCloseOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "close_policy_blocked" || result.ClosedWorkID != "" || result.CloseResult != nil {
		t.Fatalf("blocked dispatch close result = %+v", result)
	}
	if result.ClosePlanID == "" || result.DispatchClosePlanPath == "" || result.ResultPath == "" {
		t.Fatalf("blocked dispatch close should record close-plan and result: %+v", result)
	}
	after, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "verifying" || len(after.RunIDs) != len(baselineRuns) || after.PolicyDecisionID != baselinePolicy || after.IDs.TransitionID != "" {
		t.Fatalf("blocked dispatch close mutated work: %+v", after)
	}
}
