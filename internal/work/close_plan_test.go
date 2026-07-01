package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClosePlanEvaluatesPolicyWithoutClosing(t *testing.T) {
	s := newStore(t)
	workID := readyRunningWork(t, s)
	plan, err := s.ClosePlan(workID, fixedNow, ClosePlanOptions{
		Actor:    "codex",
		Outcome:  "completed",
		MoveType: "control_plane_repair",
		Record:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != "close_policy_allowed" || !plan.Policy.Allowed || plan.PlanPath == "" {
		t.Fatalf("close plan not allowed/recorded: %+v", plan)
	}
	if len(plan.CloseCommand) == 0 || plan.CloseCommand[0] != "compound" {
		t.Fatalf("close command missing: %+v", plan.CloseCommand)
	}
	if _, err := os.Stat(plan.PlanPath); err != nil {
		t.Fatalf("close plan artifact missing: %v", err)
	}
	after, err := s.Get(workID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "running" {
		t.Fatalf("close plan must not mutate work: %+v", after)
	}
}

func TestClosePlanSurfacesObservedRunEvidenceWithoutClosing(t *testing.T) {
	s := newStore(t)
	w := runningWork(t, s, "close plan uses observed evidence")
	trace := writeTrace(t, s, "trace_close_plan_observed", w.ID)
	observed, err := s.ObserveRun(w.ID, ObserveRunInput{
		Actor:   "codex",
		TraceID: trace.ID,
		Note:    "close plan should show this observation",
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.ClosePlan(w.ID, fixedNow, ClosePlanOptions{
		Actor:    "codex",
		Outcome:  "completed",
		MoveType: "control_plane_repair",
		Record:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != "close_policy_allowed" || !plan.Policy.Allowed || plan.PlanPath == "" {
		t.Fatalf("close plan not allowed/recorded: %+v", plan)
	}
	if !contains(plan.ProofRefs, trace.ID) || !contains(plan.VerificationIDs, trace.ID) {
		t.Fatalf("close plan missing verification proof: %+v", plan)
	}
	if len(plan.RunObservations) != 1 {
		t.Fatalf("run observations = %+v", plan.RunObservations)
	}
	if plan.RunObservations[0].ID != observed.ID || plan.RunObservations[0].TraceID != trace.ID || plan.RunObservations[0].Note != "close plan should show this observation" {
		t.Fatalf("run observation summary = %+v observed=%+v", plan.RunObservations[0], observed)
	}
	if !commandHasFlagValue(plan.CloseCommand, "--proof", trace.ID) {
		t.Fatalf("close command missing proof ref: %+v", plan.CloseCommand)
	}
	after, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "verifying" || after.IDs.TransitionID != "" {
		t.Fatalf("close plan must not close observed work: %+v", after)
	}
}

func TestCloseRecordsPolicyAction(t *testing.T) {
	s := newStore(t)
	workID := readyRunningWork(t, s)
	before, err := s.Get(workID)
	if err != nil {
		t.Fatal(err)
	}
	closed, err := s.Close(workID, CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Actor:     "codex",
		Summary:   "close governed",
		ProofRefs: []string{"proof:close-policy"},
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Work.State != "completed" || closed.Work.PolicyDecisionID == "" {
		t.Fatalf("close did not record policy: %+v", closed.Work)
	}
	if len(closed.Work.RunIDs) != len(before.RunIDs)+1 {
		t.Fatalf("close run chain = %v, before=%v", closed.Work.RunIDs, before.RunIDs)
	}
	closeActionID := closed.Work.RunIDs[len(before.RunIDs)]
	if _, err := os.Stat(s.envPath(closeActionID)); err != nil {
		t.Fatalf("close action envelope missing: %v", err)
	}
	if _, err := os.Stat(s.envPath(closed.Work.PolicyDecisionID)); err != nil {
		t.Fatalf("close policy envelope missing: %v", err)
	}
	if !contains(closed.Transition.ActionRefs, closeActionID) {
		t.Fatalf("transition missing close action ref: %+v", closed.Transition.ActionRefs)
	}
	if contains(closed.Receipt.Skills.Used, "work.close") {
		t.Fatalf("internal close capability leaked into used skills: %v", closed.Receipt.Skills.Used)
	}
}

func TestCloseBlockedByPolicyDoesNotMutate(t *testing.T) {
	s := newStore(t)
	workID := readyRunningWork(t, s)
	before, err := s.Get(workID)
	if err != nil {
		t.Fatal(err)
	}
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
	if _, err := s.Close(workID, CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Actor:     "codex",
		Summary:   "blocked close",
		ProofRefs: []string{"proof:close-policy"},
	}, fixedNow); err == nil {
		t.Fatal("close should be blocked by policy hold")
	}
	after, err := s.Get(workID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "running" || len(after.RunIDs) != len(before.RunIDs) || after.PolicyDecisionID != before.PolicyDecisionID || after.IDs.TransitionID != "" {
		t.Fatalf("blocked close mutated work: before=%+v after=%+v", before, after)
	}
}

func commandHasFlagValue(command []string, flag, value string) bool {
	for i := 0; i+1 < len(command); i++ {
		if command[i] != flag {
			continue
		}
		for _, part := range strings.Split(command[i+1], ",") {
			if part == value {
				return true
			}
		}
	}
	return false
}

func readyRunningWork(t *testing.T, s *Store) string {
	t.Helper()
	w, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "close policy work"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(w.ID, "codex", fixedNow); err != nil {
		t.Fatal(err)
	}
	running, err := s.Advance(w.ID, AdvanceInput{To: "running", Actor: "codex"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	return running.ID
}
