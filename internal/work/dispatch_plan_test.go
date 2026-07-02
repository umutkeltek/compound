package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/capgraph"
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

func TestDispatchClaimInjectsTaskScopedSkillOffersIntoWorkerPrompt(t *testing.T) {
	s := newStore(t)
	graph, err := capgraph.Open(s.home)
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.Add(capgraph.Node{ID: "skill:web-design-guidelines", Kind: "skill", Name: "web-design-guidelines", Triggers: []string{"frontend", "ui", "web"}}); err != nil {
		t.Fatal(err)
	}
	if err := graph.Add(capgraph.Node{ID: "skill:golang-testing", Kind: "skill", Name: "golang-testing", Triggers: []string{"go", "testing"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(CreateInput{
		Owner:            "compound-core",
		SuccessPredicate: "frontend ui page has visual screenshot proof",
		DesiredOutcome:   "build web ui",
	}, fixedNow); err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchClaim(fixedNow, DispatchPlanOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ContextPackID == "" || len(result.CapabilitiesOffered) == 0 {
		t.Fatalf("dispatch claim missing context/capability offers: %+v", result)
	}
	if !strings.Contains(result.WorkerPromptMD, "LOAD THESE SKILLS") {
		t.Fatalf("worker prompt missing skill block:\n%s", result.WorkerPromptMD)
	}
	if !strings.Contains(result.WorkerPromptMD, "capability://skill:web-design-guidelines") {
		t.Fatalf("worker prompt missing web skill:\n%s", result.WorkerPromptMD)
	}
	if strings.Contains(result.WorkerPromptMD, "capability://skill:golang-testing") {
		t.Fatalf("worker prompt should not surface unrelated Go skill:\n%s", result.WorkerPromptMD)
	}
	claimed, err := s.Get(result.ClaimedWorkID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.IDs.ContextPackID != result.ContextPackID {
		t.Fatalf("claimed context pack = %q, want %q", claimed.IDs.ContextPackID, result.ContextPackID)
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

func TestDispatchStepClosesBeforeAdvanceOrClaim(t *testing.T) {
	s := newStore(t)
	ready, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "ready waits behind close"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	claimedCandidate, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "claimed waits behind close"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(claimedCandidate.ID, "codex", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	closeCandidate := runningWork(t, s, "dispatch step closes first")
	trace := writeTrace(t, s, "trace_dispatch_step_close", closeCandidate.ID)
	if _, err := s.ObserveRun(closeCandidate.ID, ObserveRunInput{Actor: "codex", TraceID: trace.ID}, fixedNow); err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchStep(fixedNow, DispatchStepOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "closed" || result.Action != "close" || result.DispatchClose == nil || result.WorkID != closeCandidate.ID {
		t.Fatalf("dispatch step should close proof-ready work first: %+v", result)
	}
	afterClose, err := s.Get(closeCandidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterClose.State != "completed" || afterClose.IDs.TransitionID == "" {
		t.Fatalf("close candidate not completed: %+v", afterClose)
	}
	afterClaimed, err := s.Get(claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterClaimed.State != "claimed" {
		t.Fatalf("claimed work should wait behind close: %+v", afterClaimed)
	}
	afterReady, err := s.Get(ready.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterReady.State != "ready" {
		t.Fatalf("ready work should wait behind close: %+v", afterReady)
	}
}

func TestDispatchStepAdvancesBeforeClaim(t *testing.T) {
	s := newStore(t)
	ready, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "ready waits behind advance"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	claimedCandidate, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "claimed advances first"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(claimedCandidate.ID, "codex", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchStep(fixedNow, DispatchStepOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "running" || result.Action != "advance" || result.DispatchAdvance == nil || result.WorkID != claimed.ID {
		t.Fatalf("dispatch step should advance claimed work before claim: %+v", result)
	}
	afterClaimed, err := s.Get(claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterClaimed.State != "running" {
		t.Fatalf("claimed work not advanced: %+v", afterClaimed)
	}
	afterReady, err := s.Get(ready.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterReady.State != "ready" {
		t.Fatalf("ready work should remain ready: %+v", afterReady)
	}
}

func TestDispatchStepClaimsReadyWork(t *testing.T) {
	s := newStore(t)
	ready, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "dispatch step claims ready"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchStep(fixedNow, DispatchStepOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "claimed" || result.Action != "claim" || result.DispatchClaim == nil || result.WorkID != ready.ID {
		t.Fatalf("dispatch step should claim ready work: %+v", result)
	}
	if result.NextAction != "advance running or release to ready" || result.PostCounts == nil || result.PostCounts.InProgress != 1 {
		t.Fatalf("dispatch step post-state not surfaced: next=%q post=%+v", result.NextAction, result.PostCounts)
	}
	if !result.CanContinue || len(result.ContinueCommand) != 5 || result.ContinueCommand[2] != "dispatch-step" || result.ContinueCommand[4] != "codex" {
		t.Fatalf("dispatch step continuation not surfaced: can=%v command=%v", result.CanContinue, result.ContinueCommand)
	}
	after, err := s.Get(ready.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "claimed" || after.Authority.Owner != "codex" {
		t.Fatalf("ready work not claimed: %+v", after)
	}
}

func TestDispatchStepImportsSourcePacketAndClaimsIt(t *testing.T) {
	s := newStore(t)
	packetPath := filepath.Join("..", "..", "fixtures", "work", "goal-packet.md")
	before, err := os.ReadFile(packetPath)
	if err != nil {
		t.Fatal(err)
	}

	result, err := s.DispatchStep(fixedNow, DispatchStepOptions{
		Actor:            "codex",
		SourcePacketPath: packetPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "claimed" || result.Action != "claim" || result.DispatchClaim == nil {
		t.Fatalf("dispatch step should import and claim source packet: %+v", result)
	}
	source := result.DispatchClaim.Plan.Source
	if source == nil || source.Kind != "packet" || !source.Created {
		t.Fatalf("dispatch claim source missing: %+v", source)
	}
	if result.WorkID != source.WorkID || result.DispatchClaim.ClaimedWorkID != source.WorkID {
		t.Fatalf("claimed work mismatch: result=%+v source=%+v", result, source)
	}
	if result.NextAction != "advance running or release to ready" || result.PostCounts == nil || result.PostCounts.InProgress != 1 {
		t.Fatalf("source packet dispatch step post-state not surfaced: next=%q post=%+v", result.NextAction, result.PostCounts)
	}
	if !result.CanContinue || len(result.ContinueCommand) != 5 || result.ContinueCommand[2] != "dispatch-step" || result.ContinueCommand[4] != "codex" {
		t.Fatalf("source packet dispatch step continuation not surfaced: can=%v command=%v", result.CanContinue, result.ContinueCommand)
	}
	after, err := s.Get(source.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "claimed" || after.Authority.Owner != "codex" {
		t.Fatalf("packet work not claimed through policy path: %+v", after)
	}
	if _, err := s.readLease(source.WorkID); err != nil {
		t.Fatalf("dispatch step source packet claim must write lease: %v", err)
	}
	if _, err := os.Stat(result.DispatchClaim.DispatchPlanPath); err != nil {
		t.Fatalf("dispatch claim plan not recorded: %v", err)
	}
	if _, err := os.Stat(result.DispatchClaim.ResultPath); err != nil {
		t.Fatalf("dispatch claim result not recorded: %v", err)
	}
	if _, err := os.Stat(result.ResultPath); err != nil {
		t.Fatalf("dispatch step result not recorded: %v", err)
	}
	afterPacket, err := os.ReadFile(packetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterPacket) != string(before) {
		t.Fatalf("source packet was mutated")
	}
}

func TestDispatchStepReportsTerminalSourcePacket(t *testing.T) {
	s := newStore(t)
	packetPath := filepath.Join("..", "..", "fixtures", "work", "goal-packet.md")
	imported, err := s.ImportPacket(packetPath, PacketImportOptions{
		Owner: "compound-core",
		State: "ready",
	}, fixedNow)
	if err != nil {
		t.Fatalf("import packet: %v", err)
	}
	if _, err := s.Claim(imported.Work.ID, "codex", fixedNow.Add(time.Second)); err != nil {
		t.Fatalf("claim packet work: %v", err)
	}
	if _, err := s.Advance(imported.Work.ID, AdvanceInput{To: "running", RunID: "run_terminal_packet"}, fixedNow.Add(2*time.Second)); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := s.Advance(imported.Work.ID, AdvanceInput{To: "verifying", VerificationID: "packet-work-contract:terminal-source"}, fixedNow.Add(3*time.Second)); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}
	if _, err := s.Close(imported.Work.ID, CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "terminal packet proof",
		Changed:   true,
		ProofRefs: []string{"packet-work-contract:terminal-source"},
	}, fixedNow.Add(4*time.Second)); err != nil {
		t.Fatalf("close packet: %v", err)
	}
	result, err := s.DispatchStep(fixedNow.Add(5*time.Second), DispatchStepOptions{
		Actor:            "codex",
		SourcePacketPath: packetPath,
	})
	if err != nil {
		t.Fatalf("dispatch step: %v", err)
	}
	if result.Status != "source_packet_terminal" || result.Action != "none" || result.WorkID != imported.Work.ID {
		t.Fatalf("dispatch step should report terminal source packet: %+v", result)
	}
	if result.Source == nil || result.Source.Created || result.Source.WorkID != imported.Work.ID || result.Source.State != "completed" {
		t.Fatalf("terminal source packet not surfaced: %+v", result.Source)
	}
	if result.DispatchClaim == nil || result.DispatchClaim.Plan.Source == nil {
		t.Fatalf("dispatch claim source proof missing: %+v", result.DispatchClaim)
	}
	if result.WaitReason != "source_packet_terminal" || !contains(result.BlockedOn, "work.terminal:"+imported.Work.ID) {
		t.Fatalf("terminal source packet wait not surfaced: reason=%q blocked=%v", result.WaitReason, result.BlockedOn)
	}
	if result.CanContinue || len(result.ContinueCommand) != 0 {
		t.Fatalf("terminal source packet should not continue: can=%v command=%v", result.CanContinue, result.ContinueCommand)
	}
	if result.PostCounts == nil || result.PostCounts.Open != 0 || result.PostCounts.Terminal == 0 {
		t.Fatalf("terminal source packet post-state not surfaced: %+v", result.PostCounts)
	}
	if result.NextAction != "source packet already has terminal Compound work proof; no dispatch step needed" {
		t.Fatalf("terminal source packet next action = %q", result.NextAction)
	}
}

func TestDispatchStepRecordsNoAction(t *testing.T) {
	s := newStore(t)
	result, err := s.DispatchStep(fixedNow, DispatchStepOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "no_open_work" || result.Action != "none" || result.ResultPath == "" {
		t.Fatalf("dispatch step no-op result = %+v", result)
	}
	if result.NextAction != "workbench has no open work" || result.PostCounts == nil || result.PostCounts.Open != 0 {
		t.Fatalf("dispatch step no-op post-state not surfaced: next=%q post=%+v", result.NextAction, result.PostCounts)
	}
	if result.CanContinue || len(result.ContinueCommand) != 0 {
		t.Fatalf("dispatch step no-op should not continue: can=%v command=%v", result.CanContinue, result.ContinueCommand)
	}
	if result.WaitReason != "no_open_work" || !contains(result.BlockedOn, "work.ready_or_source_packet") {
		t.Fatalf("dispatch step no-op wait not surfaced: reason=%q blocked=%v", result.WaitReason, result.BlockedOn)
	}
	if _, err := os.Stat(result.ResultPath); err != nil {
		t.Fatalf("dispatch step no-op result not recorded: %v", err)
	}
}

func TestDispatchStepStopsAfterRunningOnlyAdvance(t *testing.T) {
	s := newStore(t)
	claimedCandidate, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "claimed advances then waits for run evidence"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(claimedCandidate.ID, "codex", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchStep(fixedNow, DispatchStepOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "running" || result.Action != "advance" || result.WorkID != claimed.ID {
		t.Fatalf("dispatch step should advance claimed work: %+v", result)
	}
	if result.CanContinue || len(result.ContinueCommand) != 0 {
		t.Fatalf("running-only post-state should stop for external run evidence: can=%v command=%v", result.CanContinue, result.ContinueCommand)
	}
	if result.NextAction != "advance verifying, completed, blocked, held, or failed" || result.PostCounts == nil || result.PostCounts.InProgress != 1 {
		t.Fatalf("running-only post-state not surfaced: next=%q post=%+v", result.NextAction, result.PostCounts)
	}
	if result.WaitReason != "run_evidence" || !contains(result.BlockedOn, "work.run_evidence:"+claimed.ID) {
		t.Fatalf("running-only wait not surfaced: reason=%q blocked=%v", result.WaitReason, result.BlockedOn)
	}
}

func TestDispatchStepWaitsForReviewWork(t *testing.T) {
	s := newStore(t)
	held, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "held work needs review", State: "held"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchStep(fixedNow, DispatchStepOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "needs_review" || result.Action != "none" || result.CanContinue {
		t.Fatalf("dispatch step should wait for review: %+v", result)
	}
	if result.WaitReason != "needs_review" || !contains(result.BlockedOn, "work.review:"+held.ID) {
		t.Fatalf("review wait not surfaced: reason=%q blocked=%v", result.WaitReason, result.BlockedOn)
	}
}

func TestDispatchStepWaitsForCloseProof(t *testing.T) {
	s := newStore(t)
	running := runningWork(t, s, "verifying waits for close proof")
	verifying, err := s.Advance(running.ID, AdvanceInput{To: "verifying", Actor: "codex"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchStep(fixedNow, DispatchStepOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "in_progress_only" || result.Action != "none" || result.CanContinue {
		t.Fatalf("dispatch step should wait for close proof: %+v", result)
	}
	if result.WaitReason != "close_proof" || !contains(result.BlockedOn, "work.close_proof:"+verifying.ID) {
		t.Fatalf("close proof wait not surfaced: reason=%q blocked=%v", result.WaitReason, result.BlockedOn)
	}
}

func TestDispatchStepWaitsForOtherActorClaim(t *testing.T) {
	s := newStore(t)
	created, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "other actor claim waits"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(created.ID, "claude", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchStep(fixedNow, DispatchStepOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "in_progress_only" || result.Action != "none" || result.CanContinue {
		t.Fatalf("dispatch step should wait for other actor claim: %+v", result)
	}
	if result.WaitReason != "claimed_by_other_actor" || !contains(result.BlockedOn, "work.claimed_by_other_actor:"+claimed.ID) {
		t.Fatalf("other actor wait not surfaced: reason=%q blocked=%v", result.WaitReason, result.BlockedOn)
	}
}

func TestDispatchStepRunClaimsAndAdvancesUntilRunEvidenceWait(t *testing.T) {
	s := newStore(t)
	ready, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "loop claims then advances"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchStepRun(fixedNow, DispatchStepRunOptions{
		DispatchStepOptions: DispatchStepOptions{Actor: "codex"},
		MaxSteps:            5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "waiting" || result.StepCount != 2 || result.CanContinue {
		t.Fatalf("dispatch step run should stop after claim+advance: %+v", result)
	}
	if result.Steps[0].Action != "claim" || result.Steps[1].Action != "advance" {
		t.Fatalf("dispatch step run actions = %+v", result.Steps)
	}
	if result.WaitReason != "run_evidence" || !contains(result.BlockedOn, "work.run_evidence:"+ready.ID) {
		t.Fatalf("dispatch step run wait not surfaced: reason=%q blocked=%v", result.WaitReason, result.BlockedOn)
	}
	if result.NextAction != "advance verifying, completed, blocked, held, or failed" {
		t.Fatalf("dispatch step run next action = %q", result.NextAction)
	}
	if _, err := os.Stat(result.ResultPath); err != nil {
		t.Fatalf("dispatch step run result not recorded: %v", err)
	}
	for _, step := range result.Steps {
		if _, err := os.Stat(step.ResultPath); err != nil {
			t.Fatalf("dispatch step result not recorded: %v", err)
		}
	}
	after, err := s.Get(ready.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "running" || after.IDs.TransitionID != "" {
		t.Fatalf("dispatch step run should stop at running without terminal proof: %+v", after)
	}
}

func TestDispatchStepRunStopsAtMaxStepsWithContinuation(t *testing.T) {
	s := newStore(t)
	ready, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "loop bounded by max"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchStepRun(fixedNow, DispatchStepRunOptions{
		DispatchStepOptions: DispatchStepOptions{Actor: "codex"},
		MaxSteps:            1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "max_steps_reached" || result.StepCount != 1 || !result.CanContinue {
		t.Fatalf("dispatch step run should stop at max with continuation: %+v", result)
	}
	if len(result.ContinueCommand) != 8 || result.ContinueCommand[4] != "codex" || result.ContinueCommand[5] != "--until-wait" || result.ContinueCommand[7] != "1" {
		t.Fatalf("dispatch step run continuation = %v", result.ContinueCommand)
	}
	if result.WaitReason != "" || len(result.BlockedOn) != 0 {
		t.Fatalf("max-step run should not claim a wait: reason=%q blocked=%v", result.WaitReason, result.BlockedOn)
	}
	after, err := s.Get(ready.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "claimed" {
		t.Fatalf("dispatch step run should stop after one claim: %+v", after)
	}
}

func TestDispatchStepRunImportsSourcePacketOnceThenAdvances(t *testing.T) {
	s := newStore(t)
	packetPath := filepath.Join("..", "..", "fixtures", "work", "goal-packet.md")
	before, err := os.ReadFile(packetPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DispatchStepRun(fixedNow, DispatchStepRunOptions{
		DispatchStepOptions: DispatchStepOptions{
			Actor:            "codex",
			SourcePacketPath: packetPath,
		},
		MaxSteps: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "waiting" || result.StepCount != 2 || result.Steps[0].DispatchClaim == nil || result.Steps[1].DispatchAdvance == nil {
		t.Fatalf("source packet dispatch run should claim then advance: %+v", result)
	}
	source := result.Steps[0].DispatchClaim.Plan.Source
	if source == nil || !source.Created || result.Steps[0].WorkID != source.WorkID || result.Steps[1].WorkID != source.WorkID {
		t.Fatalf("source packet dispatch run source mismatch: source=%+v result=%+v", source, result)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("source packet should be imported once, work count=%d", len(list))
	}
	after, err := s.Get(source.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "running" {
		t.Fatalf("source packet dispatch run should stop at running: %+v", after)
	}
	afterPacket, err := os.ReadFile(packetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterPacket) != string(before) {
		t.Fatalf("source packet was mutated")
	}
}
