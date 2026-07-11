package work

import (
	"fmt"
	"os"
	"strings"
	"time"

	"compound/internal/envelope"
	homepkg "compound/internal/home"
	"compound/internal/mem"
	"compound/internal/policy"
)

// ReceiptedActionInput describes one bounded action outcome on an open
// WorkEnvelope. The action may advance the run without claiming terminal task
// success; work close remains the only writer of WorkEnvelope.ids.transition_id.
type ReceiptedActionInput struct {
	Action                     envelope.ActionIntent
	OperationalDecisionClaimID string
	ExpectedWorkState          string
	Summary                    string
	MoveType                   string
	ProofRefs                  []string
}

type ReceiptedActionResult struct {
	Work       envelope.WorkEnvelope   `json:"work"`
	Action     envelope.ActionIntent   `json:"action"`
	Decision   envelope.PolicyDecision `json:"decision"`
	Receipt    envelope.Receipt        `json:"receipt"`
	Transition envelope.Transition     `json:"transition"`
	Replayed   bool                    `json:"replayed"`
}

var receiptedActionBeforeCommit = func() {}

func (s *Store) RecordReceiptedAction(workID string, in ReceiptedActionInput, now time.Time) (ReceiptedActionResult, error) {
	var out ReceiptedActionResult
	var recordErr error
	err := homepkg.WithAutonomyAdmissionLock(s.home, func() error {
		out, recordErr = s.recordReceiptedActionLocked(workID, in, now)
		return recordErr
	})
	return out, err
}

func (s *Store) recordReceiptedActionLocked(workID string, in ReceiptedActionInput, now time.Time) (ReceiptedActionResult, error) {
	var out ReceiptedActionResult
	if now.IsZero() {
		now = time.Now()
	}
	w, err := s.Get(workID)
	if err != nil {
		return out, err
	}
	if terminalStates[w.State] {
		return out, fmt.Errorf("work %s is %s (terminal); cannot record a receipted action", workID, w.State)
	}
	if expected := strings.TrimSpace(in.ExpectedWorkState); expected != "" && w.State != expected {
		return out, fmt.Errorf("work %s state %q no longer matches expected state %q", workID, w.State, expected)
	}
	action := in.Action
	if action.IDs.WorkID != workID {
		return out, fmt.Errorf("action work_id %q does not match work %q", action.IDs.WorkID, workID)
	}
	if err := action.Validate(); err != nil {
		return out, fmt.Errorf("action intent invalid: %w", err)
	}
	claimID := strings.TrimSpace(in.OperationalDecisionClaimID)
	if claimID == "" {
		return out, fmt.Errorf("operational decision claim id required")
	}
	if err := currentReceiptedActionLaw(s.home, claimID); err != nil {
		return out, err
	}
	cfg, _, err := policy.LoadConfig(s.home)
	if err != nil {
		return out, err
	}
	if reason := policy.AutonomyOperatorOnlyReason(action, cfg); reason != "" {
		return out, fmt.Errorf("cannot record autonomous receipted action: %s", reason)
	}
	currentDecision := policy.Evaluate(action, cfg, now)
	if !policy.Allowed(currentDecision) {
		return out, fmt.Errorf("cannot record receipted action %s: current policy %s (%s)", action.ID, currentDecision.Outcome, strings.Join(currentDecision.Reasons, "; "))
	}
	if action.MutationClass != "read" && strings.TrimSpace(action.RollbackPlan) == "" {
		return out, fmt.Errorf("receipted action %s requires rollback_plan", action.ID)
	}

	proofRefs := mergeUnique(in.ProofRefs, action.EvidenceRefs, []string{"claim:" + claimID, "work:" + workID})
	moveType := strings.TrimSpace(in.MoveType)
	if moveType == "" {
		moveType = "control_plane_repair"
	}
	if !oneOf(moveType, envelope.MoveTypes) {
		return out, fmt.Errorf("receipted action move_type must be one of %v, got %q", envelope.MoveTypes, moveType)
	}
	if strings.TrimSpace(in.Summary) == "" {
		return out, fmt.Errorf("receipted action summary required")
	}

	decisionID := idFor("pol_", "receipted-action", workID, action.ID, claimID)
	receiptID := idFor("rec_", "receipted-action", workID, action.ID, claimID)
	transitionID := idFor("tr_", "receipted-action", workID, action.ID, claimID)
	if receiptedActionArtifactsExist(s, action.ID, decisionID, receiptID, transitionID) {
		return s.replayReceiptedAction(w, in, action, decisionID, receiptID, transitionID, proofRefs, moveType, now)
	}
	if receiptedActionAnyArtifactExists(s, action.ID, decisionID, receiptID, transitionID) {
		return out, fmt.Errorf("receipted action %s has an incomplete persisted transaction", action.ID)
	}

	receiptedActionBeforeCommit()
	decision := currentDecision
	decision.ID = decisionID
	decision.EvidenceRefs = mergeUnique(decision.EvidenceRefs, proofRefs)
	used := normalizeCapabilityRefs([]string{action.Capability})
	receipt := envelope.Receipt{
		Header:     newHeader(envelope.SVReceipt, receiptID, now, workID, w.IDs.ContextPackID, w.Privacy, w.Risk),
		Outcome:    "completed",
		MoveType:   moveType,
		StateDelta: envelope.StateDelta{Changed: true, Summary: strings.TrimSpace(in.Summary), FilesChanged: []string{}, Artifacts: []string{}, Verification: []string{}},
		Cost:       envelope.ReceiptCost{ModelCalls: []string{}},
		Skills: envelope.ReceiptSkills{
			Used: used, Missed: []string{}, Candidate: []string{}, UsedDeclared: true,
		},
		Memory:    envelope.ReceiptMemory{Candidates: []string{}, Promoted: []string{}, Parked: []string{}},
		Next:      envelope.ReceiptNext{SuggestedWork: []string{}},
		ProofRefs: proofRefs,
	}
	receipt.EvidenceRefs = proofRefs
	if lane := strings.TrimSpace(action.Producer.LaneID); lane != "" {
		receipt.Producer.LaneID = lane
	}
	if err := receipt.Validate(); err != nil {
		return out, fmt.Errorf("emitted receipted-action receipt invalid: %w", err)
	}
	beforeRefs := mergeUnique([]string{w.IDs.ContextPackID, "claim:" + claimID})
	transition := envelope.Transition{
		Header:     newHeader(envelope.SVTransition, transitionID, now, workID, w.IDs.ContextPackID, w.Privacy, w.Risk),
		BeforeRefs: beforeRefs,
		ActionRefs: []string{action.ID},
		AfterRefs:  []string{receipt.ID},
		StateDelta: receipt.StateDelta.Summary,
		Projections: envelope.Projections{
			Memory: []string{}, Skill: used, Product: []string{}, Cost: []string{}, Risk: []string{}, Taste: []string{}, Routing: []string{}, HumanAttention: []string{},
			Typed: closeProjectionRefs(receipt, w.IDs.ContextPackID),
		},
		SecondOrder: envelope.SecondOrder{FutureBehaviorChanged: true},
	}
	transition.EvidenceRefs = proofRefs
	if lane := strings.TrimSpace(action.Producer.LaneID); lane != "" {
		transition.Producer.LaneID = lane
	}
	if err := transition.Validate(); err != nil {
		return out, fmt.Errorf("emitted receipted-action transition invalid: %w", err)
	}
	for _, item := range []struct {
		id      string
		payload any
	}{{action.ID, &action}, {decision.ID, &decision}, {receipt.ID, &receipt}, {transition.ID, &transition}} {
		if err := s.writeJSON(s.envPath(item.id), item.payload); err != nil {
			return out, err
		}
	}
	w.RunIDs = mergeUnique(w.RunIDs, []string{action.ID})
	w.PolicyDecisionID = decision.ID
	if err := s.writeJSON(s.workPath(workID), &w); err != nil {
		return out, err
	}
	if err := s.appendEvent(receiptedActionEvent(w, action, decision, receipt, transition, now)); err != nil {
		return out, err
	}
	return ReceiptedActionResult{Work: w, Action: action, Decision: decision, Receipt: receipt, Transition: transition}, nil
}

func currentReceiptedActionLaw(homePath, claimID string) error {
	store, err := mem.Open(homePath)
	if err != nil {
		return err
	}
	claims, err := store.AllCurrentOperationalDecisions()
	if err != nil {
		return err
	}
	for _, claim := range mem.AuthoritativeActiveOperationalDecisions(claims) {
		if claim.ID == claimID {
			return nil
		}
	}
	return fmt.Errorf("claim %s is not a current authoritative operational decision", claimID)
}

func receiptedActionArtifactsExist(s *Store, ids ...string) bool {
	for _, id := range ids {
		if _, err := os.Stat(s.envPath(id)); err != nil {
			return false
		}
	}
	return true
}

func receiptedActionAnyArtifactExists(s *Store, ids ...string) bool {
	for _, id := range ids {
		if _, err := os.Stat(s.envPath(id)); err == nil {
			return true
		}
	}
	return false
}

func (s *Store) replayReceiptedAction(w envelope.WorkEnvelope, in ReceiptedActionInput, action envelope.ActionIntent, decisionID, receiptID, transitionID string, proofRefs []string, moveType string, now time.Time) (ReceiptedActionResult, error) {
	var storedAction envelope.ActionIntent
	var decision envelope.PolicyDecision
	var receipt envelope.Receipt
	var transition envelope.Transition
	for _, item := range []struct {
		id     string
		target any
	}{{action.ID, &storedAction}, {decisionID, &decision}, {receiptID, &receipt}, {transitionID, &transition}} {
		if err := s.readJSON(s.envPath(item.id), item.target); err != nil {
			return ReceiptedActionResult{}, err
		}
	}
	if !sameReceiptedAction(storedAction, action) || decision.ActionIntentID != action.ID || receipt.IDs.WorkID != w.ID ||
		transition.IDs.WorkID != w.ID || receipt.MoveType != moveType || receipt.StateDelta.Summary != strings.TrimSpace(in.Summary) ||
		strings.Join(receipt.ProofRefs, "\x00") != strings.Join(proofRefs, "\x00") || !proofChainContains(transition.AfterRefs, receipt.ID) {
		return ReceiptedActionResult{}, fmt.Errorf("receipted action %s replay conflicts with persisted transaction", action.ID)
	}
	for _, item := range []envelope.Envelope{&storedAction, &decision, &receipt, &transition} {
		if err := item.Validate(); err != nil {
			return ReceiptedActionResult{}, fmt.Errorf("receipted action %s replay artifact invalid: %w", action.ID, err)
		}
	}
	w.RunIDs = mergeUnique(w.RunIDs, []string{action.ID})
	w.PolicyDecisionID = decision.ID
	if err := s.writeJSON(s.workPath(w.ID), &w); err != nil {
		return ReceiptedActionResult{}, err
	}
	events, err := s.eventsFor(w.ID)
	if err != nil {
		return ReceiptedActionResult{}, err
	}
	if !receiptedActionEventExists(events, receipt.ID, transition.ID) {
		if err := s.appendEvent(receiptedActionEvent(w, storedAction, decision, receipt, transition, now)); err != nil {
			return ReceiptedActionResult{}, err
		}
	}
	return ReceiptedActionResult{Work: w, Action: storedAction, Decision: decision, Receipt: receipt, Transition: transition, Replayed: true}, nil
}

func sameReceiptedAction(a, b envelope.ActionIntent) bool {
	return a.ID == b.ID && a.IDs.WorkID == b.IDs.WorkID && a.IDs.SessionID == b.IDs.SessionID &&
		a.Capability == b.Capability && a.Action == b.Action && a.Target == b.Target &&
		a.MutationClass == b.MutationClass && a.Reversibility == b.Reversibility && a.BlastRadius == b.BlastRadius &&
		a.RollbackPlan == b.RollbackPlan
}

func receiptedActionEvent(w envelope.WorkEnvelope, action envelope.ActionIntent, decision envelope.PolicyDecision, receipt envelope.Receipt, transition envelope.Transition, now time.Time) Event {
	return Event{TS: ts(now), WorkID: w.ID, From: w.State, To: w.State, By: action.Actor, Note: "receipted action recorded: " + action.Action + " (policy " + decision.Outcome + ")", Attached: []string{action.ID, decision.ID, receipt.ID, transition.ID}}
}

func receiptedActionEventExists(events []Event, receiptID, transitionID string) bool {
	for _, event := range events {
		if proofChainContains(event.Attached, receiptID) && proofChainContains(event.Attached, transitionID) {
			return true
		}
	}
	return false
}
