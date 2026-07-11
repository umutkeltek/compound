package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
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
var receiptedActionBeforePublish = func(string) error { return nil }
var receiptedActionAfterWrite = func(string) error { return nil }
var writeReceiptedActionTemp = func(_ string, file *os.File, data []byte) error {
	return writeReceiptedActionTempFully(file, data)
}

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
	if err := w.Validate(); err != nil {
		return out, fmt.Errorf("work envelope invalid: %w", err)
	}
	if terminalStates[w.State] {
		return out, fmt.Errorf("work %s is %s (terminal); cannot record a receipted action", workID, w.State)
	}
	if expected := strings.TrimSpace(in.ExpectedWorkState); expected != "" && w.State != expected {
		return out, fmt.Errorf("work %s state %q no longer matches expected state %q", workID, w.State, expected)
	}
	requestedAction := in.Action
	if requestedAction.IDs.WorkID != workID {
		return out, fmt.Errorf("action work_id %q does not match work %q", requestedAction.IDs.WorkID, workID)
	}
	if err := requestedAction.Validate(); err != nil {
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
	action, actionPresent, err := s.preflightReceiptedAction(requestedAction)
	if err != nil {
		return out, err
	}
	transactionNow, err := time.Parse(time.RFC3339, action.CreatedAt)
	if err != nil {
		return out, fmt.Errorf("receipted action %s created_at invalid: %w", action.ID, err)
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
	decision := currentDecision
	decision.ID = decisionID
	decision.CreatedAt = action.CreatedAt
	decision.EvidenceRefs = mergeUnique(decision.EvidenceRefs, proofRefs)
	used := normalizeCapabilityRefs([]string{action.Capability})
	receipt := envelope.Receipt{
		Header:     newHeader(envelope.SVReceipt, receiptID, transactionNow, workID, w.IDs.ContextPackID, w.Privacy, w.Risk),
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
		Header:     newHeader(envelope.SVTransition, transitionID, transactionNow, workID, w.IDs.ContextPackID, w.Privacy, w.Risk),
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

	decisionPresent, err := s.preflightReceiptedEnvelope("decision", decision.ID, &decision, &envelope.PolicyDecision{})
	if err != nil {
		return out, err
	}
	receiptPresent, err := s.preflightReceiptedEnvelope("receipt", receipt.ID, &receipt, &envelope.Receipt{})
	if err != nil {
		return out, err
	}
	transitionPresent, err := s.preflightReceiptedEnvelope("transition", transition.ID, &transition, &envelope.Transition{})
	if err != nil {
		return out, err
	}

	workLinked := proofChainContains(w.RunIDs, action.ID)
	policyLinked := w.PolicyDecisionID == decision.ID
	if workLinked != policyLinked {
		return out, fmt.Errorf("receipted action %s conflicts with persisted work links", action.ID)
	}
	if strings.TrimSpace(w.IDs.TransitionID) != "" {
		return out, fmt.Errorf("receipted action %s conflicts with persisted work transition_id %s", action.ID, w.IDs.TransitionID)
	}
	expectedWork := w
	expectedWork.RunIDs = mergeUnique(expectedWork.RunIDs, []string{action.ID})
	expectedWork.PolicyDecisionID = decision.ID
	if err := expectedWork.Validate(); err != nil {
		return out, fmt.Errorf("receipted action %s expected work invalid: %w", action.ID, err)
	}
	expectedEvent := receiptedActionEvent(expectedWork, action, decision, receipt, transition, transactionNow)
	events, err := s.admissionEventsFor(workID)
	if err != nil {
		return out, fmt.Errorf("receipted action %s event preflight: %w", action.ID, err)
	}
	eventPresent, err := preflightReceiptedActionEvent(events, expectedEvent)
	if err != nil {
		return out, err
	}
	stages := []struct {
		name    string
		present bool
	}{
		{"action", actionPresent},
		{"decision", decisionPresent},
		{"receipt", receiptPresent},
		{"transition", transitionPresent},
		{"work", workLinked},
		{"event", eventPresent},
	}
	if err := validateReceiptedActionPrefix(action.ID, stages); err != nil {
		return out, err
	}
	replayed := false
	for _, stage := range stages {
		replayed = replayed || stage.present
	}

	receiptedActionBeforeCommit()
	for _, item := range []struct {
		name    string
		present bool
		id      string
		payload envelope.Envelope
	}{
		{"action", actionPresent, action.ID, &action},
		{"decision", decisionPresent, decision.ID, &decision},
		{"receipt", receiptPresent, receipt.ID, &receipt},
		{"transition", transitionPresent, transition.ID, &transition},
	} {
		if item.present {
			continue
		}
		if err := s.writeReceiptedActionBoundary(item.name, s.envPath(item.id), item.payload); err != nil {
			return out, err
		}
	}
	if !workLinked {
		if err := s.writeReceiptedActionBoundary("work", s.workPath(workID), &expectedWork); err != nil {
			return out, err
		}
	}
	if !eventPresent {
		if err := s.appendEventControlled(
			expectedEvent,
			func() error { return receiptedActionBeforePublish("event") },
			func() error { return receiptedActionAfterWrite("event") },
		); err != nil {
			return out, err
		}
	}
	return ReceiptedActionResult{Work: expectedWork, Action: action, Decision: decision, Receipt: receipt, Transition: transition, Replayed: replayed}, nil
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

func (s *Store) preflightReceiptedAction(requested envelope.ActionIntent) (envelope.ActionIntent, bool, error) {
	var stored envelope.ActionIntent
	err := s.readJSON(s.envPath(requested.ID), &stored)
	if errors.Is(err, os.ErrNotExist) {
		return requested, false, nil
	}
	if err != nil {
		return envelope.ActionIntent{}, false, fmt.Errorf("receipted action %s persisted action unreadable: %w", requested.ID, err)
	}
	if err := stored.Validate(); err != nil {
		return envelope.ActionIntent{}, false, fmt.Errorf("receipted action %s persisted action invalid: %w", requested.ID, err)
	}
	// The adapter rebuilds the same deterministic action on retry with a fresh
	// hook timestamp. CreatedAt is the only deliberately volatile field; the
	// first durable action fixes the transaction timestamp for the suffix.
	normalized := requested
	normalized.CreatedAt = stored.CreatedAt
	if !reflect.DeepEqual(stored, normalized) {
		return envelope.ActionIntent{}, false, fmt.Errorf("receipted action %s persisted action conflicts with requested transaction", requested.ID)
	}
	return stored, true, nil
}

func (s *Store) preflightReceiptedEnvelope(kind, id string, expected, target envelope.Envelope) (bool, error) {
	err := s.readJSON(s.envPath(id), target)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("receipted action %s persisted %s unreadable: %w", id, kind, err)
	}
	if err := target.Validate(); err != nil {
		return false, fmt.Errorf("receipted action %s persisted %s invalid: %w", id, kind, err)
	}
	if !reflect.DeepEqual(expected, target) {
		return false, fmt.Errorf("receipted action %s persisted %s conflicts with requested transaction", id, kind)
	}
	return true, nil
}

func validateReceiptedActionPrefix(actionID string, stages []struct {
	name    string
	present bool
}) error {
	missing := false
	for _, stage := range stages {
		if !stage.present {
			missing = true
			continue
		}
		if missing {
			return fmt.Errorf("receipted action %s persisted transaction is out of order at %s", actionID, stage.name)
		}
	}
	return nil
}

func (s *Store) writeReceiptedActionBoundary(name, path string, payload any) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := writeReceiptedActionTemp(name, tmp, data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := receiptedActionBeforePublish(name); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if err := syncReceiptedActionDir(dir); err != nil {
		return err
	}
	return receiptedActionAfterWrite(name)
}

func writeReceiptedActionTempFully(file *os.File, data []byte) error {
	for remaining := data; len(remaining) > 0; {
		n, err := file.Write(remaining)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		remaining = remaining[n:]
	}
	return nil
}

func syncReceiptedActionDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) {
		return err
	}
	return nil
}

func preflightReceiptedActionEvent(events []Event, expected Event) (bool, error) {
	matching := 0
	for _, event := range events {
		if !receiptedActionEventTouches(event, expected.Attached) {
			continue
		}
		if !sameReceiptedActionEvent(event, expected) {
			return false, fmt.Errorf("receipted action %s persisted event conflicts with requested transaction", expected.Attached[0])
		}
		matching++
	}
	if matching > 1 {
		return false, fmt.Errorf("receipted action %s has %d duplicate persisted events", expected.Attached[0], matching)
	}
	return matching == 1, nil
}

func receiptedActionEventTouches(event Event, refs []string) bool {
	for _, attached := range event.Attached {
		if proofChainContains(refs, attached) {
			return true
		}
	}
	return false
}

func sameReceiptedActionEvent(a, b Event) bool {
	// Event time was nanosecond precision before recovery fixed its transaction
	// clock to ActionIntent.created_at. Every authority/link field must match;
	// TS is provenance-only and may differ across the runtime cutover.
	a.TS = b.TS
	return reflect.DeepEqual(a, b)
}

func receiptedActionEvent(w envelope.WorkEnvelope, action envelope.ActionIntent, decision envelope.PolicyDecision, receipt envelope.Receipt, transition envelope.Transition, now time.Time) Event {
	return Event{TS: ts(now), WorkID: w.ID, From: w.State, To: w.State, By: action.Actor, Note: "receipted action recorded: " + action.Action + " (policy " + decision.Outcome + ")", Attached: []string{action.ID, decision.ID, receipt.ID, transition.ID}}
}
