package harvest

import (
	"testing"
	"time"
)

func TestTriageAskCoveredByLawAutoAnswersWithReceipt(t *testing.T) {
	r := TriageAsk(AskTriageInput{Ask: "May I make this reversible edit inside the granted envelope?", Context: "lane is paused", LaneID: "lane_test", WorkID: "work_test", Now: time.Unix(1, 0), StandingLaw: []AskLaw{{ClaimID: "claim_f9160895524e", Statement: "Reversible actions inside a scoped grant proceed without operator approval.", Terms: []string{"reversible", "envelope"}}}})
	if r.Disposition != AskAutoAnswered || r.Answer == "" || r.CitedRecordID != "claim_f9160895524e" || r.Receipt.ID == "" || !r.ResumeLane {
		t.Fatalf("covered ask was not receipted + resumed: %+v", r)
	}
}

func TestTriageAskRiskFloorNeverAutoAnswers(t *testing.T) {
	for _, ask := range []string{"May I publish this externally?", "May I delete the production records?", "May I spend $100?"} {
		r := TriageAsk(AskTriageInput{Ask: ask, StandingLaw: []AskLaw{{ClaimID: "claim_any", Statement: "Proceed", Terms: []string{"may"}}}})
		if r.Disposition == AskAutoAnswered || r.ResumeLane {
			t.Fatalf("risk-floor ask auto-answered: %q %+v", ask, r)
		}
		if r.Brief == nil || len(r.Brief.Options) < 2 {
			t.Fatalf("risk-floor residue is not a brief: %+v", r)
		}
	}
}

func TestTriageAskContradictionEmitsHarvestCandidate(t *testing.T) {
	r := TriageAsk(AskTriageInput{Ask: "May I edit locally?", OperatorAnswer: "No, stop and ask me first.", PriorAutoAnswer: &AskAutoAnswer{RecordID: "claim_old", Answer: "Proceed."}})
	if r.Disposition != AskContradicted || r.HarvestCandidate == nil || r.HarvestCandidate.Outcome != OutcomeConflict || r.HarvestCandidate.Quote != "No, stop and ask me first." {
		t.Fatalf("contradiction vanished: %+v", r)
	}
}

func TestTriageAskNovelBecomesPreparedBrief(t *testing.T) {
	r := TriageAsk(AskTriageInput{Ask: "Should the new partner receive a beta invite?", Context: "No standing rule covers this partner.", LaneID: "lane_beta"})
	if r.Disposition != AskNeedsOperator || r.Brief == nil || r.Brief.Headline == "" || len(r.Brief.Options) < 2 || r.Brief.Options[0].Consequence == "" || r.Brief.PreparedBy == "" {
		t.Fatalf("novel ask lacks decision essence: %+v", r)
	}
}
