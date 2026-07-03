package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/envelope"
)

func TestCreatePlanRecordsPolicyWithoutCreatingWork(t *testing.T) {
	s := newStore(t)
	plan, err := s.CreatePlan(CreateInput{
		Owner:            "compound-core",
		Actor:            "codex",
		SuccessPredicate: "create plan stays proposal-only",
		DesiredOutcome:   "show admission readiness",
		EvidenceRefs:     []string{"test://create-plan"},
	}, fixedNow, CreatePlanOptions{Record: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != "create_policy_allowed" || !plan.Policy.Allowed {
		t.Fatalf("plan policy not allowed: %+v", plan)
	}
	if plan.ActionIntent.Capability != "work.create" || plan.Decision.ActionIntentID != plan.ActionIntent.ID {
		t.Fatalf("intent/decision not threaded: intent=%s decision=%s", plan.ActionIntent.ID, plan.Decision.ActionIntentID)
	}
	if plan.PlanPath == "" {
		t.Fatalf("missing recorded plan path: %+v", plan)
	}
	if _, err := os.Stat(plan.PlanPath); err != nil {
		t.Fatalf("recorded plan missing: %v", err)
	}
	if _, err := s.Get(plan.WorkID); err == nil {
		t.Fatalf("create-plan must not write work %s", plan.WorkID)
	}
	events, err := s.eventsFor(plan.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("create-plan must not write lifecycle events: %+v", events)
	}
}

func TestCreatePlanCommandCarriesPlannedWorkID(t *testing.T) {
	s := newStore(t)
	plan, err := s.CreatePlan(CreateInput{
		Owner:            "compound-core",
		Actor:            "codex",
		SuccessPredicate: "created work uses planned identity",
		DesiredOutcome:   "preserve planned fields",
		NonGoals:         []string{"do not recompute identity"},
		EvidenceRefs:     []string{"test://planned-work"},
		ContextPackID:    "ctx_planned",
	}, fixedNow, CreatePlanOptions{Record: true})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(plan.CreateCommand, "--work-id") || !contains(plan.CreateCommand, plan.WorkID) {
		t.Fatalf("create command must preserve planned work id %s: %v", plan.WorkID, plan.CreateCommand)
	}
	for _, want := range []string{"--outcome", "preserve planned fields", "--non-goal", "do not recompute identity", "--evidence-ref", "test://planned-work", "--context-pack", "ctx_planned"} {
		if !contains(plan.CreateCommand, want) {
			t.Fatalf("create command missing %q: %v", want, plan.CreateCommand)
		}
	}
}

func TestCreatePreservesExplicitWorkID(t *testing.T) {
	s := newStore(t)
	plannedID := "work_planned_identity"
	w, err := s.Create(CreateInput{
		WorkID:           plannedID,
		Owner:            "compound-core",
		Actor:            "codex",
		SuccessPredicate: "created work uses planned identity",
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if w.ID != plannedID {
		t.Fatalf("created work id = %q, want planned id %q", w.ID, plannedID)
	}
	if _, err := s.Get(plannedID); err != nil {
		t.Fatalf("planned work id was not persisted: %v", err)
	}
	events, err := s.eventsFor(plannedID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].WorkID != plannedID || events[0].To != "ready" {
		t.Fatalf("create event not threaded through planned id: %+v", events)
	}
}

func TestCreateRejectsInvalidExplicitWorkID(t *testing.T) {
	s := newStore(t)
	_, err := s.Create(CreateInput{
		WorkID:           "planned_identity",
		Owner:            "compound-core",
		Actor:            "codex",
		SuccessPredicate: "created work validates planned identity",
	}, fixedNow)
	if err == nil || !strings.Contains(err.Error(), `must start with "work_"`) {
		t.Fatalf("expected work id prefix validation, got %v", err)
	}
}

func TestCreateRecordsAdmissionPolicyBeforeWork(t *testing.T) {
	s := newStore(t)
	writeContextPack(t, s, "ctx_create")
	w, err := s.Create(CreateInput{
		Owner:            "compound-core",
		Actor:            "codex",
		SuccessPredicate: "create records policy chain",
		ContextPackID:    "ctx_create",
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if w.PolicyDecisionID == "" || len(w.RunIDs) != 1 {
		t.Fatalf("created work missing admission policy/action: %+v", w)
	}
	var ai envelope.ActionIntent
	if err := s.readJSON(s.envPath(w.RunIDs[0]), &ai); err != nil {
		t.Fatalf("admission action missing: %v", err)
	}
	if ai.Capability != "work.create" || ai.IDs.WorkID != w.ID {
		t.Fatalf("admission action not bound to work: %+v", ai)
	}
	if _, err := os.Stat(s.envPath(w.PolicyDecisionID)); err != nil {
		t.Fatalf("admission policy missing: %v", err)
	}
	_, events, err := s.Show(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || !contains(events[0].Attached, ai.ID) || !contains(events[0].Attached, w.PolicyDecisionID) || !contains(events[0].Attached, "ctx_create") {
		t.Fatalf("create event missing policy/context attachments: %+v", events)
	}
}

func TestCreateRejectsMissingContextPack(t *testing.T) {
	s := newStore(t)
	_, err := s.Create(CreateInput{
		Owner:            "compound-core",
		Actor:            "codex",
		SuccessPredicate: "create refuses missing context pack",
		ContextPackID:    "ctx_missing",
	}, fixedNow)
	if err == nil || !strings.Contains(err.Error(), `context_pack_id "ctx_missing"`) {
		t.Fatalf("expected missing context pack error, got %v", err)
	}
	works, listErr := s.List()
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(works) != 0 {
		t.Fatalf("missing context pack create must not persist work: %+v", works)
	}
}

func TestCreateRejectsContextPackMissingEffectiveContract(t *testing.T) {
	s := newStore(t)
	writeContextPackWithContractID(t, s, "ctx_with_missing_contract", "contract_missing")
	_, err := s.Create(CreateInput{
		Owner:            "compound-core",
		Actor:            "codex",
		SuccessPredicate: "create refuses context pack with missing contract",
		ContextPackID:    "ctx_with_missing_contract",
	}, fixedNow)
	if err == nil || !strings.Contains(err.Error(), `contract_id "contract_missing"`) {
		t.Fatalf("expected missing effective contract error, got %v", err)
	}
	works, listErr := s.List()
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(works) != 0 {
		t.Fatalf("missing effective contract create must not persist work: %+v", works)
	}
}

func TestCreateAcceptsContextPackWithEffectiveContract(t *testing.T) {
	s := newStore(t)
	writeEffectiveContract(t, s, "contract_present")
	writeContextPackWithContractID(t, s, "ctx_with_contract", "contract_present")
	w, err := s.Create(CreateInput{
		Owner:            "compound-core",
		Actor:            "codex",
		SuccessPredicate: "create accepts context pack with matching contract",
		ContextPackID:    "ctx_with_contract",
	}, fixedNow)
	if err != nil {
		t.Fatalf("create with context pack contract: %v", err)
	}
	if w.IDs.ContextPackID != "ctx_with_contract" {
		t.Fatalf("work context_pack_id = %q, want ctx_with_contract", w.IDs.ContextPackID)
	}
}

func TestCreateBlockedByPolicyDoesNotMutate(t *testing.T) {
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
	w, err := s.Create(CreateInput{Owner: "compound-core", Actor: "codex", SuccessPredicate: "held create"}, fixedNow)
	if err == nil {
		t.Fatal("create should be blocked by policy hold")
	}
	if w.ID == "" {
		t.Fatal("blocked create should return candidate work id for diagnostics")
	}
	if _, err := s.Get(w.ID); err == nil {
		t.Fatalf("blocked create wrote work %s", w.ID)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("blocked create wrote work entries: %+v", list)
	}
	events, err := s.eventsFor(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("blocked create wrote lifecycle events: %+v", events)
	}
}
