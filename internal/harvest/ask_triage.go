package harvest

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"compound/internal/hook"
)

func TriageTranscript(path string, in AskTriageInput) (AskTriageResult, error) {
	ask, err := hook.LastBlockingAsk(path)
	if err != nil {
		return AskTriageResult{}, err
	}
	in.Ask, in.Context = ask.Text, ask.Context
	return TriageAsk(in), nil
}

type AskDisposition string

const (
	AskAutoAnswered  AskDisposition = "auto_answered"
	AskNeedsOperator AskDisposition = "needs_operator"
	AskContradicted  AskDisposition = "contradicted"
)

type AskLaw struct {
	ClaimID, Statement string
	Terms              []string
}
type AskAutoAnswer struct{ RecordID, Answer string }
type AskBriefOption struct{ Label, Consequence string }
type AskBrief struct {
	Headline, Context, Recommendation, PreparedBy string
	Options                                       []AskBriefOption
}
type AskReceipt struct {
	ID, WorkID, LaneID, Disposition, Answer, CitedRecordID, CreatedAt string
	ProofRefs                                                         []string
}
type AskHarvestCandidate struct {
	Outcome                   Outcome
	Quote, ConflictsWithClaim string
}
type AskTriageInput struct {
	Ask, Context, LaneID, WorkID, OperatorAnswer string
	Now                                          time.Time
	StandingLaw                                  []AskLaw
	Evidence                                     []AskLaw
	PriorAutoAnswer                              *AskAutoAnswer
}
type AskTriageResult struct {
	Disposition           AskDisposition
	Answer, CitedRecordID string
	ResumeLane            bool
	Receipt               AskReceipt
	Brief                 *AskBrief
	HarvestCandidate      *AskHarvestCandidate
}

var askRiskFloor = []string{"publish", "public", "external", "delete", "destroy", "irreversible", "spend", "purchase", "payment", "credential", "secret", "production"}

// TriageAsk is the additive ask-side of ruling harvest. It accepts an already
// extracted blocking question, so transcript parsing stays owned by hook. Risk
// is checked before recall: matching law can never lower the envelope floor.
func TriageAsk(in AskTriageInput) AskTriageResult {
	if in.PriorAutoAnswer != nil && strings.TrimSpace(in.OperatorAnswer) != "" {
		return AskTriageResult{Disposition: AskContradicted, HarvestCandidate: &AskHarvestCandidate{Outcome: OutcomeConflict, Quote: strings.TrimSpace(in.OperatorAnswer), ConflictsWithClaim: in.PriorAutoAnswer.RecordID}}
	}
	if !askExitsEnvelope(in.Ask) {
		if law, ok := bestAskLaw(in.Ask+" "+in.Context, append(in.StandingLaw, in.Evidence...)); ok {
			now := in.Now
			if now.IsZero() {
				now = time.Now()
			}
			answer := law.Statement + " (" + law.ClaimID + ")"
			sum := sha256.Sum256([]byte(in.LaneID + "\x00" + in.WorkID + "\x00" + in.Ask + "\x00" + law.ClaimID))
			return AskTriageResult{Disposition: AskAutoAnswered, Answer: answer, CitedRecordID: law.ClaimID, ResumeLane: true, Receipt: AskReceipt{ID: fmt.Sprintf("askrec_%x", sum[:6]), WorkID: in.WorkID, LaneID: in.LaneID, Disposition: string(AskAutoAnswered), Answer: answer, CitedRecordID: law.ClaimID, CreatedAt: now.UTC().Format(time.RFC3339), ProofRefs: []string{"claim:" + law.ClaimID}}}
		}
	}
	return AskTriageResult{Disposition: AskNeedsOperator, Brief: prepareAskBrief(in)}
}

func askExitsEnvelope(ask string) bool {
	lower := strings.ToLower(ask)
	for _, term := range askRiskFloor {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

func bestAskLaw(text string, laws []AskLaw) (AskLaw, bool) {
	lower := strings.ToLower(text)
	best, score := AskLaw{}, 0
	for _, law := range laws {
		n := 0
		for _, term := range law.Terms {
			if strings.Contains(lower, strings.ToLower(term)) {
				n++
			}
		}
		if n > score && law.ClaimID != "" && law.Statement != "" {
			best, score = law, n
		}
	}
	return best, score > 0
}

func prepareAskBrief(in AskTriageInput) *AskBrief {
	headline := strings.TrimSpace(in.Ask)
	if headline == "" {
		headline = "A lane needs an operator ruling"
	}
	return &AskBrief{Headline: headline, Context: strings.TrimSpace(in.Context), Options: []AskBriefOption{{Label: "Allow", Consequence: "The lane may continue with this specific request."}, {Label: "Hold", Consequence: "The lane remains paused and the envelope does not expand."}}, Recommendation: "Hold unless the request is clearly inside the intended envelope.", PreparedBy: "compound ask-triage"}
}
