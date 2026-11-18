package work

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDeliveryPlanAcceptsLaterVerifyingReceiptCitation(t *testing.T) {
	f := newPrivateDeliveryFixture(t)
	w, err := f.s.Advance(f.w.ID, AdvanceInput{
		Actor: f.w.Owner, To: "verifying", ArtifactID: f.ref, VerificationID: f.evidence.Consumer.ReceiptID,
	}, fixedNow.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.s.AdvancePlan(w.ID, fixedNow.Add(5*time.Minute), AdvancePlanOptions{
		Actor: w.Owner, To: w.State, NonSourceDelivery: f.bindingInput().NonSourceDelivery,
	})
	if err != nil {
		t.Fatalf("delivery plan rejected a later governed receipt citation: %v", err)
	}
}

func TestReceiptCitationWithOtherAdvanceEvidence(t *testing.T) {
	f := newPrivateDeliveryFixture(t)
	const contextID = "ctx_receipt_citation"
	writeContextPack(t, f.s, contextID)
	w, err := f.s.Advance(f.w.ID, AdvanceInput{
		Actor: f.w.Owner, To: "verifying", ArtifactID: f.ref, VerificationID: f.evidence.Consumer.ReceiptID,
		RunAttemptIDs: []string{"ratt_citation_observation"}, ContextPackID: contextID,
	}, fixedNow.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.validateDeliveryOutcomeEvidence(w, f.evidence.Consumer); err != nil {
		t.Fatalf("receipt with context and run attachment: %v", err)
	}
}

func TestReceiptCitationBeforeLaterReceiptedAction(t *testing.T) {
	f := newPrivateDeliveryFixture(t)
	w, err := f.s.Advance(f.w.ID, AdvanceInput{
		Actor: f.w.Owner, To: "verifying", VerificationID: f.evidence.Consumer.ReceiptID,
	}, fixedNow.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	at := fixedNow.Add(5 * time.Minute)
	claim := receiptedActionRuling(t, f.s, "subsequent-citation-work", "", at)
	input := receiptedActionTestInput(w, claim, "act_later_read", "Record subsequent fixture work", at)
	input.ExpectedWorkState = "verifying"
	out, err := f.s.RecordReceiptedAction(w.ID, input, at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.validateDeliveryOutcomeEvidence(out.Work, f.evidence.Consumer); err != nil {
		t.Fatalf("receipt citation before a later receipted action: %v", err)
	}
}

func TestHistoricalReceiptCitationRejectsConflictingEvents(t *testing.T) {
	f := newPrivateDeliveryFixture(t)
	w, err := f.s.Advance(f.w.ID, AdvanceInput{
		Actor: f.w.Owner, To: "verifying", ArtifactID: f.ref, VerificationID: f.evidence.Consumer.ReceiptID,
	}, fixedNow.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	proof, err := f.s.validateDeliveryOutcomeEvidence(w, f.evidence.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	_, history, err := f.s.Show(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	producer := slices.IndexFunc(history, func(event Event) bool { return slices.Contains(event.Attached, proof.Action.ID) })
	citation := len(history) - 1
	for _, tc := range []struct {
		name string
		edit func([]Event) []Event
	}{
		{"missing producer", func(e []Event) []Event { return slices.Delete(e, producer, producer+1) }},
		{"duplicate producer", func(e []Event) []Event { return append(e, e[producer]) }},
		{"malformed producer", func(e []Event) []Event { e[producer].Attached = e[producer].Attached[:3]; return e }},
		{"wrong producer note", func(e []Event) []Event { e[producer].Note = "unrelated"; return e }},
		{"prior citation", func(e []Event) []Event { e[citation], e[producer] = e[producer], e[citation]; return e }},
		{"duplicate citation", func(e []Event) []Event { return append(e, e[citation]) }},
		{"foreign work", func(e []Event) []Event { e[citation].WorkID = "work_other"; return e }},
		{"wrong actor", func(e []Event) []Event { e[citation].By = "other"; return e }},
		{"different from state", func(e []Event) []Event { e[citation].From = "verifying"; return e }},
		{"borrowed policy", func(e []Event) []Event { e[citation].Attached[1] = e[producer-1].Attached[1]; return e }},
		{"missing action", func(e []Event) []Event { e[citation].Attached[0] = "act_unowned"; return e }},
		{"changed timestamp", func(e []Event) []Event { e[citation].TS = "42"; return e }},
		{"missing preimage", func(e []Event) []Event { e[citation].PreviousWorkSHA256 = ""; return e }},
		{"wrong postimage", func(e []Event) []Event { e[citation].WorkSHA256 = "sha256:" + strings.Repeat("a", 64); return e }},
		{"unknown artifact", func(e []Event) []Event { e[citation].Attached[2] = "artifact_unowned"; return e }},
		{"surplus verification", func(e []Event) []Event {
			e[citation].Attached = append(e[citation].Attached, proof.Receipt.ID)
			return e
		}},
		{"cites producer action too", func(e []Event) []Event { e[citation].Attached[2] = proof.Action.ID; return e }},
		{"cites producer policy too", func(e []Event) []Event { e[citation].Attached[2] = proof.Decision.ID; return e }},
		{"cites producer transition too", func(e []Event) []Event { e[citation].Attached[2] = proof.Transition.ID; return e }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := slices.Clone(history)
			for i := range events {
				events[i].Attached = slices.Clone(events[i].Attached)
			}
			_, err := f.s.historicalReceiptedActionEvent(tc.edit(events), proof.Action, proof.Decision, proof.Receipt, proof.Transition)
			if err == nil {
				t.Fatal("accepted a conflicting lifecycle history")
			}
		})
	}
	// A pending/incomplete publication must continue to reject any overlap,
	// including this fully valid downstream citation.
	if _, err := preflightReceiptedActionEvent(history, history[producer]); err == nil {
		t.Fatal("incomplete publication ignored an overlapping event")
	}
}

func TestReceiptCitationCannotBorrowAnotherVerifyingAdvance(t *testing.T) {
	f := newPrivateDeliveryFixture(t)
	original := f.evidence.Consumer.ReceiptID
	w, err := f.s.Advance(f.w.ID, AdvanceInput{Actor: f.w.Owner, To: "verifying", VerificationID: original}, fixedNow.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Advance(w.ID, AdvanceInput{Actor: w.Owner, To: "verifying", VerificationID: "rec_other_fixture"}, fixedNow.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_, events, err := f.s.Show(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := f.s.validateDeliveryOutcomeEvidence(w, f.evidence.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	for _, swap := range []bool{false, true} {
		last := len(events) - 1
		events[last].Attached[2] = original
		if swap {
			events[last-1].Attached[2] = "rec_other_fixture"
		}
		if _, err := f.s.historicalReceiptedActionEvent(events, proof.Action, proof.Decision, proof.Receipt, proof.Transition); err == nil {
			t.Errorf("accepted rewritten verification on a genuine second advance (swapped=%v)", swap)
		}
	}
}
