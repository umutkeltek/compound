package work

import (
	"testing"
	"time"
)

func TestContinueRunAdmitsPreparesClaimsAndAdvancesUntilWait(t *testing.T) {
	s := newStore(t)
	plan, err := s.CreatePlan(CreateInput{
		WorkID:           "work_continue_preview",
		Owner:            "compound-core",
		Actor:            "codex",
		State:            "proposed",
		SuccessPredicate: "continue admits and starts suggested work",
		DesiredOutcome:   "safe lifecycle plumbing reaches running",
		EvidenceRefs:     []string{"test://continue-preview"},
	}, fixedNow, CreatePlanOptions{
		Record:        true,
		Capability:    "product.action_review",
		Action:        "plan_work_from_reviewed_product_action",
		TargetKind:    "product_action_review",
		TargetID:      "continue_demo",
		MutationClass: "local_reversible",
		Reversibility: "easy",
		BlastRadius:   "local",
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := s.ContinueRun(fixedNow.Add(time.Minute), ContinueRunOptions{
		ContinueOptions: ContinueOptions{Actor: "codex", ExpectWorkID: plan.WorkID},
		MaxSteps:        10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "waiting" || run.StepCount != 4 || run.WaitReason != "run_evidence" {
		t.Fatalf("continue run = %+v", run)
	}
	wantActions := []string{"admit", "prepare", "claim", "advance"}
	for i, want := range wantActions {
		if run.Steps[i].Action != want || run.Steps[i].WorkID != plan.WorkID {
			t.Fatalf("step %d = %+v, want action=%s work=%s", i, run.Steps[i], want, plan.WorkID)
		}
	}
	if !contains(run.BlockedOn, "work.run_evidence:"+plan.WorkID) {
		t.Fatalf("continue run blocked_on = %+v", run.BlockedOn)
	}
	after, err := s.Get(plan.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "running" || after.Authority.Owner != "codex" {
		t.Fatalf("continued work = %+v", after)
	}
}

func TestContinueLeavesOperatorAdmissionPreviewWaiting(t *testing.T) {
	s := newStore(t)
	plan, err := s.CreatePlan(CreateInput{
		WorkID:           "work_continue_operator_preview",
		Owner:            "compound-core",
		Actor:            "codex",
		State:            "proposed",
		SuccessPredicate: "operator input remains a needs-me preview",
	}, fixedNow, CreatePlanOptions{
		Record:        true,
		Capability:    "product.action_review",
		Action:        "plan_work_from_private_config",
		TargetKind:    "private_config",
		TargetID:      "asc",
		MutationClass: "local_reversible",
		Reversibility: "easy",
		BlastRadius:   "local",
		ActionContext: &WorkActionContext{
			Kind:             "workbench_review_card",
			Title:            "ASC private config",
			ActionSummary:    "operator supplies private config",
			RequiresOperator: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := s.ContinueRun(fixedNow.Add(time.Minute), ContinueRunOptions{
		ContinueOptions: ContinueOptions{Actor: "codex", ExpectWorkID: plan.WorkID},
		MaxSteps:        5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.StepCount != 1 || run.Steps[0].Action != "none" || run.WaitReason != "needs_review" {
		t.Fatalf("operator preview should wait: %+v", run)
	}
	if _, err := s.Get(plan.WorkID); err == nil {
		t.Fatalf("operator-required preview should not be admitted")
	}
}

func TestContinueExpectWorkPinsPrepareTarget(t *testing.T) {
	s := newStore(t)
	first, err := s.Create(CreateInput{Owner: "compound-core", State: "proposed", SuccessPredicate: "first proposed work"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(CreateInput{Owner: "compound-core", State: "proposed", SuccessPredicate: "second proposed work"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}

	step, err := s.Continue(fixedNow.Add(time.Minute), ContinueOptions{Actor: "codex", ExpectWorkID: second.ID})
	if err != nil {
		t.Fatal(err)
	}
	if step.Action != "prepare" || step.WorkID != second.ID {
		t.Fatalf("expected pinned prepare for %s, got %+v", second.ID, step)
	}
	firstAfter, err := s.Get(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondAfter, err := s.Get(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstAfter.State != "proposed" || secondAfter.State != "ready" {
		t.Fatalf("pinning failed: first=%s second=%s", firstAfter.State, secondAfter.State)
	}
}
