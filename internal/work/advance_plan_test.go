package work

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAdvancePlanEvaluatesPolicyWithoutAdvancing(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "advance plan previews running"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(w.ID, "codex", fixedNow); err != nil {
		t.Fatal(err)
	}
	plan, err := s.AdvancePlan(w.ID, fixedNow, AdvancePlanOptions{Actor: "codex", To: "running", Record: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != "advance_policy_allowed" || !plan.Policy.Allowed || plan.ToState != "running" {
		t.Fatalf("advance plan policy = %+v", plan)
	}
	if len(plan.AdvanceCommand) == 0 || plan.AdvanceCommand[0] != "compound" {
		t.Fatalf("advance command missing: %+v", plan.AdvanceCommand)
	}
	if plan.PlanPath == "" {
		t.Fatalf("advance plan was not recorded: %+v", plan)
	}
	if _, err := os.Stat(plan.PlanPath); err != nil {
		t.Fatalf("advance plan artifact missing: %v", err)
	}
	after, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "claimed" {
		t.Fatalf("advance plan must not mutate work, state=%s", after.State)
	}
}

func TestAdvanceRecordsPolicyAction(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "advance records policy"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(w.ID, "codex", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	advanced, err := s.Advance(w.ID, AdvanceInput{To: "running", Actor: "codex", RunID: "run_real"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if advanced.State != "running" || advanced.PolicyDecisionID == "" {
		t.Fatalf("advance did not record policy: %+v", advanced)
	}
	if len(advanced.RunIDs) != len(claimed.RunIDs)+2 || !contains(advanced.RunIDs, "run_real") {
		t.Fatalf("advance run chain = %v, claimed=%v", advanced.RunIDs, claimed.RunIDs)
	}
	advanceActionID := advanced.RunIDs[len(claimed.RunIDs)]
	if _, err := os.Stat(s.envPath(advanceActionID)); err != nil {
		t.Fatalf("advance action envelope missing: %v", err)
	}
	if _, err := os.Stat(s.envPath(advanced.PolicyDecisionID)); err != nil {
		t.Fatalf("advance policy envelope missing: %v", err)
	}
	closed, err := s.Close(w.ID, CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "advance governed",
		ProofRefs: []string{"proof:advance-policy"},
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if contains(closed.Receipt.Skills.Used, "work.advance") {
		t.Fatalf("internal advance capability leaked into used skills: %v", closed.Receipt.Skills.Used)
	}
}

func TestAdvanceBlockedByPolicyDoesNotMutate(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "advance hold"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(w.ID, "codex", fixedNow)
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
	if _, err := s.Advance(w.ID, AdvanceInput{To: "running", Actor: "codex"}, fixedNow); err == nil {
		t.Fatal("advance should be blocked by policy hold")
	}
	after, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "claimed" || len(after.RunIDs) != len(claimed.RunIDs) || after.PolicyDecisionID != claimed.PolicyDecisionID {
		t.Fatalf("blocked advance mutated work: before=%+v after=%+v", claimed, after)
	}
}
