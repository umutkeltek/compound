package work

import (
	"testing"
	"time"
)

func TestCloseStampsProducerLaneIDOnReceiptAndTransition(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "operator", SuccessPredicate: "fixture proof exists"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(w.ID, "fixture/lane", fixedNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "running", Actor: "fixture/lane"}, fixedNow.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	res, err := s.Close(w.ID, CloseInput{
		Outcome:   "completed",
		MoveType:  "outward_delta",
		Actor:     "fixture/lane",
		Summary:   "fixture proof landed",
		ProofRefs: []string{"proof:fixture"},
	}, fixedNow.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if res.Receipt.Producer.LaneID != "fixture/lane" {
		t.Fatalf("receipt producer lane_id = %q, want fixture/lane", res.Receipt.Producer.LaneID)
	}
	if res.Transition.Producer.LaneID != "fixture/lane" {
		t.Fatalf("transition producer lane_id = %q, want fixture/lane", res.Transition.Producer.LaneID)
	}
}

func TestActionExecutionReceiptDraftStampsProducerLaneID(t *testing.T) {
	s := newStore(t)
	_, workID := runningActionContextWork(t, s)
	plan, err := s.ActionExecutionPlan(workID, fixedNow.Add(20*time.Minute), ActionExecutionPlanOptions{Actor: "fixture/lane", Record: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ReceiptDraft.Producer.LaneID != "fixture/lane" {
		t.Fatalf("receipt draft producer lane_id = %q, want fixture/lane", plan.ReceiptDraft.Producer.LaneID)
	}
}
