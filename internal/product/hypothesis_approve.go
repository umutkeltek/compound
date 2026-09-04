package product

import (
	"fmt"
	"strings"
	"time"

	"compound/internal/candidates"
)

// HypothesisDraftExpectedEffectPlaceholder is the literal, non-invented
// expected_effect a director-drafted hypothesis candidate carries until the
// operator writes the real sentence (see MintHypothesisDraftCandidate on
// feat/director-hypothesis-draft-20260904). The literal text is the package
// guarantee: a draft never invents a number or a causal claim, so a plan whose
// hypothesis IS this string is a plan for an experiment nobody stated.
//
// MERGE NOTE: feat/director-hypothesis-draft-20260904 declares the identical
// constant in hypothesis_draft.go. When that branch lands, keep exactly one
// declaration (the values are byte-identical); the duplicate declaration is a
// compile error by design, so the shared string can never silently fork.
const HypothesisDraftExpectedEffectPlaceholder = "OPERATOR MUST WRITE: what should %s do after %s?"

// HypothesisDraftExpectedEffectPlaceholderPrefix is the fixed head every
// placeholder-formatted expected_effect starts with. Gates match on this
// prefix rather than the full template so the refusal also catches hand-typed
// variants of the same placeholder. The coupling test in
// hypothesis_approve_test.go asserts the prefix still heads the template:
// renaming the template literal without moving this prefix turns that test red
// instead of silently unlocking the gates.
const HypothesisDraftExpectedEffectPlaceholderPrefix = "OPERATOR MUST WRITE"

// HypothesisApproveInput is the operator's approval write for one parked
// product_hypothesis draft. ExpectedEffect is the sentence the operator owes;
// Metrics and ObservationWindow are optional refinements of the draft's own
// fields; ReviewedBy names the approving human (empty defaults to "operator").
type HypothesisApproveInput struct {
	CandidateID       string
	ExpectedEffect    string
	Metrics           []string
	ObservationWindow string
	ReviewedBy        string
}

// HypothesisApproveResult is the receipted outcome of one approval.
type HypothesisApproveResult struct {
	Candidate    HypothesisCandidate
	ApprovalID   string // id of the approval act recorded on the loop transition
	TransitionID string
}

// HypothesisApprove is the governed door around the placeholder gate: the one
// verb that receives the operator's expected_effect sentence and writes it
// onto a parked hypothesis draft, receipted by one product_hypothesis_approve
// loop transition instead of a silent hand-edit of the candidate JSON.
//
// Three refusals are load-bearing (falsifiers 3, 4, and the verdict gate):
//   - an expected_effect that still starts with the operator placeholder is
//     refused — approving a draft by handing back its own blank is a no-op
//     wearing a receipt;
//   - a candidate whose verdict is not `park` is refused — only a parked draft
//     awaits this door; operationalize/reject are other verbs' words;
//   - a candidate whose result is already recorded is refused — the loop is
//     closed and the review verdict (not approval) is its door.
//
// The verdict deliberately STAYS `park` and the result stays `unknown`:
// `verdict == park && result == unknown` is the predicate that keeps a
// hypothesis in PendingReview (DoctorWithOptions), which feeds review-ready
// matching, and isOpenLoopHypothesis closes on a recorded result — flipping to
// `operationalize` here would silently drop the approved hypothesis OUT of the
// measurement path. Approval writes the sentence; measurement and the review
// verdict remain the loop's own later steps.
func HypothesisApprove(homePath string, in HypothesisApproveInput, now time.Time) (HypothesisApproveResult, error) {
	homePath = strings.TrimSpace(homePath)
	if homePath == "" {
		return HypothesisApproveResult{}, fmt.Errorf("COMPOUND_HOME required")
	}
	candidateID := strings.TrimSpace(in.CandidateID)
	if candidateID == "" {
		return HypothesisApproveResult{}, fmt.Errorf("candidate id required")
	}
	expectedEffect := strings.TrimSpace(in.ExpectedEffect)
	if expectedEffect == "" {
		return HypothesisApproveResult{}, fmt.Errorf("expected_effect required: the operator sentence is the hypothesis")
	}
	if strings.HasPrefix(expectedEffect, HypothesisDraftExpectedEffectPlaceholderPrefix) {
		return HypothesisApproveResult{}, fmt.Errorf("expected_effect is still the operator placeholder %q: write what the metric should do after the change, in your own words", HypothesisDraftExpectedEffectPlaceholderPrefix)
	}
	actor := strings.TrimSpace(in.ReviewedBy)
	if actor == "" {
		actor = "operator"
	}
	candidate, err := loadHypothesisCandidate(homePath, candidateID)
	if err != nil {
		return HypothesisApproveResult{}, err
	}
	if strings.TrimSpace(candidate.Verdict) != candidates.Park {
		return HypothesisApproveResult{}, fmt.Errorf("product hypothesis %s verdict is %q: only a parked hypothesis can be approved", candidateID, candidate.Verdict)
	}
	if result := strings.TrimSpace(candidate.Result); result != "" && result != "unknown" {
		return HypothesisApproveResult{}, fmt.Errorf("product hypothesis %s already has result %q: the loop is closed; approval is not the review door", candidateID, candidate.Result)
	}
	if strings.TrimSpace(candidate.ProductID) == "" {
		return HypothesisApproveResult{}, fmt.Errorf("product hypothesis %s is missing product_id: cannot receipt an approval on the product loop", candidateID)
	}
	candidate.ExpectedEffect = expectedEffect
	if metrics := compact(in.Metrics); len(metrics) > 0 {
		candidate.Metrics = metrics
	}
	if window := strings.TrimSpace(in.ObservationWindow); window != "" {
		candidate.ObservationWindow = window
	}
	// PendingReview matches `result == "unknown"` exactly; normalizing an empty
	// result keeps the freshly approved hypothesis inside the measurement path
	// for older candidate shapes that predate the drafts.
	if strings.TrimSpace(candidate.Result) == "" {
		candidate.Result = "unknown"
	}
	// verdict intentionally untouched: it stays park (see function comment).
	// NextAction must stay non-empty — the PendingReview predicate requires it
	// — and must point forward at the loop's own next steps.
	candidate.NextAction = fmt.Sprintf("approved by %s: import before/after metric evidence and record the product review", actor)
	approvalID := stableID("pha_", candidateID, expectedEffect, actor, now.UTC().Format(time.RFC3339Nano))
	if err := writeHypothesisCandidate(homePath, candidate); err != nil {
		return HypothesisApproveResult{}, err
	}
	tr, err := BuildLoopTransition(LoopTransitionInput{
		Kind:         "product_hypothesis_approve",
		ProductID:    candidate.ProductID,
		ObjectID:     approvalID,
		Actor:        actor,
		SourceTrace:  "product_hypothesis_approve:" + candidateID,
		BeforeRefs:   compact(append([]string{"product_hypothesis:" + candidateID}, candidate.EvidenceRefs...)),
		AfterRefs:    compact(append([]string{"product_hypothesis:" + candidateID, "product_hypothesis_approve:" + approvalID}, candidate.EvidenceRefs...)),
		EvidenceRefs: candidate.EvidenceRefs,
		Summary:      fmt.Sprintf("product hypothesis %s approved by %s: expected_effect written, verdict stays park so the hypothesis remains in pending review; no product surface changed", candidateID, actor),
	}, now)
	if err != nil {
		return HypothesisApproveResult{}, err
	}
	if err := RecordTransition(homePath, tr); err != nil {
		return HypothesisApproveResult{}, err
	}
	return HypothesisApproveResult{Candidate: candidate, ApprovalID: approvalID, TransitionID: tr.ID}, nil
}
