package work

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"compound/internal/envelope"
)

const receiptedActionReservationSchemaVersion = "compound.receipted_action_reservation.v0.2"
const receiptedActionReservationLegacySchemaVersion = "compound.receipted_action_reservation.v0.1"
const receiptedActionCompletionSchemaVersion = "compound.receipted_action_completion.v0.1"

// receiptedActionReservation closes the gap between governed admission and an
// adapter's external publication. While present, the work lifecycle cannot
// advance past the exact action that the adapter is authorized to finalize.
type receiptedActionReservation struct {
	SchemaVersion     string `json:"schema_version"`
	WorkID            string `json:"work_id"`
	ActionID          string `json:"action_intent_id"`
	PolicyDecisionID  string `json:"policy_decision_id"`
	ReceiptID         string `json:"receipt_id"`
	TransitionID      string `json:"transition_id"`
	AuthorizedAt      string `json:"authorized_at"`
	TransactionDigest string `json:"transaction_digest"`
	// ReauthorizationDigest excludes only the current policy decision and its
	// policy-authority proof ref. It lets a retry replace an unpublished
	// intermediate authority while proving the action and outcome stayed exact.
	ReauthorizationDigest string `json:"reauthorization_digest,omitempty"`
	// Input is additive recovery evidence for an unpublished intermediate
	// authority. Its transaction digest is verified before any caller can use it.
	Input *ReceiptedActionInput `json:"input,omitempty"`
}

// receiptedActionCompletion is the immutable replay identity written only
// after every receipted-action artifact has converged. Its output digests bind
// the complete schema-valid Receipt, Transition, and lifecycle Event instead
// of a selected field subset.
type receiptedActionCompletion struct {
	SchemaVersion     string `json:"schema_version"`
	WorkID            string `json:"work_id"`
	ActionID          string `json:"action_intent_id"`
	PolicyDecisionID  string `json:"policy_decision_id"`
	ReceiptID         string `json:"receipt_id"`
	TransitionID      string `json:"transition_id"`
	AuthorizedAt      string `json:"authorized_at"`
	TransactionDigest string `json:"transaction_digest"`
	ReceiptSHA256     string `json:"receipt_sha256"`
	TransitionSHA256  string `json:"transition_sha256"`
	EventSHA256       string `json:"event_sha256"`
}

// ReserveReceiptedAction atomically revalidates governed admission and creates
// the work-owned blocker that must exist before an adapter publishes mutation.
func (s *Store) ReserveReceiptedAction(workID string, in ReceiptedActionInput, authorizedAt time.Time, authorize func() error) error {
	if authorize == nil {
		return fmt.Errorf("receipted action reservation authorization is required")
	}
	return s.withLifecycleMutation(func() error {
		w, err := s.recoverPendingClaimBeforeMutation(workID)
		if err != nil {
			return err
		}
		if err := validateReceiptedActionReservationAdmission(s, w, workID, in, authorizedAt); err != nil {
			return err
		}
		if pending, present, _, err := s.preflightReceiptedActionPending(workID); err != nil {
			return err
		} else if present {
			return fmt.Errorf("work %s has pending receipted action %s; cannot reserve %s", workID, pending.ActionID, in.Action.ID)
		}
		if _, present, err := s.preflightReceiptedAction(in.Action); err != nil {
			return err
		} else if present {
			return fmt.Errorf("receipted action %s was already published before reservation", in.Action.ID)
		}
		expected, err := newReceiptedActionReservation(workID, in, authorizedAt)
		if err != nil {
			return err
		}
		actual, present, path, err := s.preflightReceiptedActionReservation(workID)
		if err != nil {
			return err
		}
		if present {
			if !receiptedActionReservationEqual(actual, expected) {
				return fmt.Errorf("work %s receipted action reservation %s conflicts with requested transaction", workID, actual.ActionID)
			}
			return nil
		}
		if err := authorize(); err != nil {
			return err
		}
		if err := ensureReceiptedActionPendingDir(filepath.Dir(path)); err != nil {
			return err
		}
		return s.writeReceiptedActionBoundary("reservation", path, &expected)
	})
}

// ReauthorizeReceiptedActionReservation atomically replaces an unpublished
// reservation for the same action after current policy authority changes. The
// lifecycle blocker is never removed between revisions, so partial external
// progress cannot become detached from its work transaction.
func (s *Store) ReauthorizeReceiptedActionReservation(workID string, previous ReceiptedActionInput, previousAt time.Time, next ReceiptedActionInput, nextAt time.Time, authorize func() error) error {
	if authorize == nil {
		return fmt.Errorf("receipted action reauthorization is required")
	}
	if nextAt.Before(previousAt) {
		return fmt.Errorf("receipted action reauthorization time must be monotonic")
	}
	previousStable, err := receiptedActionReauthorizationDigest(workID, previous)
	if err != nil {
		return err
	}
	nextStable, err := receiptedActionReauthorizationDigest(workID, next)
	if err != nil {
		return err
	}
	if previousStable != nextStable {
		return fmt.Errorf("receipted action reauthorization must preserve the exact action and non-authority outcome")
	}
	return s.withLifecycleMutation(func() error {
		w, err := s.recoverPendingClaimBeforeMutation(workID)
		if err != nil {
			return err
		}
		if previous.Action.ID != next.Action.ID || previous.Action.IDs.WorkID != workID || next.Action.IDs.WorkID != workID {
			return fmt.Errorf("receipted action reauthorization must preserve the same work and action")
		}
		if err := validateReceiptedActionReservationAdmission(s, w, workID, next, nextAt); err != nil {
			return err
		}
		if pending, present, _, err := s.preflightReceiptedActionPending(workID); err != nil {
			return err
		} else if present {
			return fmt.Errorf("cannot reauthorize receipted action reservation %s after receipt publication began", pending.ActionID)
		}
		if _, present, err := s.preflightReceiptedAction(next.Action); err != nil {
			return err
		} else if present {
			return fmt.Errorf("cannot reauthorize receipted action reservation %s after receipt publication began", next.Action.ID)
		}
		priorReservation, err := newReceiptedActionReservation(workID, previous, previousAt)
		if err != nil {
			return err
		}
		nextReservation, err := newReceiptedActionReservation(workID, next, nextAt)
		if err != nil {
			return err
		}
		actual, present, path, err := s.preflightReceiptedActionReservation(workID)
		if err != nil {
			return err
		}
		if !present {
			return fmt.Errorf("work %s has no receipted action reservation for %s", workID, next.Action.ID)
		}
		if actualAt, parseErr := time.Parse(time.RFC3339Nano, actual.AuthorizedAt); parseErr != nil {
			return parseErr
		} else if nextAt.Before(actualAt) {
			return fmt.Errorf("receipted action reauthorization time must be monotonic with installed reservation")
		}
		if !receiptedActionReservationEqual(actual, priorReservation) && !receiptedActionReservationEqual(actual, nextReservation) &&
			(actual.ReauthorizationDigest == "" || actual.ReauthorizationDigest != nextReservation.ReauthorizationDigest) {
			return fmt.Errorf("work %s receipted action reservation %s conflicts with reauthorization", workID, actual.ActionID)
		}
		if err := authorize(); err != nil {
			return err
		}
		if receiptedActionReservationEqual(actual, nextReservation) {
			return nil
		}
		return s.writeReceiptedActionBoundary("reservation_reauthorization", path, &nextReservation)
	})
}

// ReadReceiptedActionReservationInput returns the exact validated unpublished
// transaction input so an adapter can finish an interrupted authority journal
// before advancing to a newer current policy. Reservations written before the
// additive recovery body existed remain valid but are not recoverable here.
func (s *Store) ReadReceiptedActionReservationInput(workID, actionID string) (ReceiptedActionInput, time.Time, bool, error) {
	var zero ReceiptedActionInput
	reservation, present, _, err := s.preflightReceiptedActionReservation(workID)
	if err != nil || !present {
		return zero, time.Time{}, false, err
	}
	if reservation.ActionID != actionID {
		return zero, time.Time{}, false, fmt.Errorf("work %s receipted action reservation belongs to %s, not %s", workID, reservation.ActionID, actionID)
	}
	if reservation.Input == nil {
		return zero, time.Time{}, false, nil
	}
	authorizedAt, err := time.Parse(time.RFC3339Nano, reservation.AuthorizedAt)
	if err != nil {
		return zero, time.Time{}, false, err
	}
	return *reservation.Input, authorizedAt, true, nil
}

// RecordReservedReceiptedAction finalizes only the exact admitted transaction.
// The blocker is removed after the receipt, transition, work link, and event
// have converged durably.
func (s *Store) RecordReservedReceiptedAction(workID string, in ReceiptedActionInput, authorizedAt time.Time) (ReceiptedActionResult, error) {
	var out ReceiptedActionResult
	var recordErr error
	err := s.withLifecycleMutation(func() error {
		expected, err := newReceiptedActionReservation(workID, in, authorizedAt)
		if err != nil {
			return err
		}
		actual, present, path, err := s.preflightReceiptedActionReservation(workID)
		if err != nil {
			return err
		}
		if !present {
			completion, completed, _, err := s.preflightReceiptedActionCompletion(workID, in.Action.ID)
			if err != nil {
				return err
			}
			if !completed {
				return fmt.Errorf("work %s has no receipted action reservation for %s", workID, in.Action.ID)
			}
			if !receiptedActionReservationEqual(completion.reservation(), expected) {
				return fmt.Errorf("work %s completed receipted action %s conflicts with requested transaction", workID, completion.ActionID)
			}
			out, recordErr = s.replayCompletedReceiptedAction(workID, in, completion)
			return recordErr
		}
		if !receiptedActionReservationEqual(actual, expected) {
			return fmt.Errorf("work %s receipted action reservation %s conflicts with requested transaction", workID, actual.ActionID)
		}
		// Authorization identifies and admits the reservation. Completion is a
		// separate causal moment after the exact reservation match and immediately
		// before receipted-action publication begins.
		completedAt := time.Now()
		if completedAt.Before(authorizedAt) {
			completedAt = authorizedAt
		}
		out, recordErr = s.recordReceiptedActionLocked(workID, in, authorizedAt, completedAt, false)
		if recordErr != nil {
			return recordErr
		}
		return s.completeReceiptedActionReservation(path, expected, out)
	})
	return out, err
}

// AbortReceiptedActionReservation clears only an exact pre-publication
// reservation. Once receipted-action publication has started, recovery must
// finish that transaction instead of erasing its lifecycle blocker.
func (s *Store) AbortReceiptedActionReservation(workID, actionID string) error {
	return s.withLifecycleMutation(func() error {
		if filepath.Base(workID) != workID || !strings.HasPrefix(workID, "work_") {
			return fmt.Errorf("invalid receipted-action work id %q", workID)
		}
		if !strings.HasPrefix(actionID, "act_") {
			return fmt.Errorf("invalid receipted-action action id %q", actionID)
		}
		if pending, present, _, err := s.preflightReceiptedActionPending(workID); err != nil {
			return err
		} else if present && pending.ActionID == actionID {
			return fmt.Errorf("cannot abort receipted action reservation %s after receipt publication began", actionID)
		}
		if exists, err := s.events.Exists(actionID); err != nil {
			return err
		} else if exists {
			return fmt.Errorf("cannot abort receipted action reservation %s after receipt publication began", actionID)
		}
		actual, present, path, err := s.preflightReceiptedActionReservation(workID)
		if err != nil {
			return err
		}
		if !present {
			return nil
		}
		if actual.ActionID != actionID {
			return fmt.Errorf("work %s receipted action reservation belongs to %s, not %s", workID, actual.ActionID, actionID)
		}
		return s.clearReceiptedActionReservation(path, actual)
	})
}

func validateReceiptedActionReservationAdmission(s *Store, w envelope.WorkEnvelope, workID string, in ReceiptedActionInput, authorizedAt time.Time) error {
	if authorizedAt.IsZero() {
		return fmt.Errorf("receipted action reservation authorized_at is required")
	}
	if err := w.Validate(); err != nil {
		return fmt.Errorf("work envelope invalid: %w", err)
	}
	if w.State != "running" {
		return fmt.Errorf("work %s is %s; receipted action reservation requires running work", workID, w.State)
	}
	if expected := strings.TrimSpace(in.ExpectedWorkState); expected != "" && expected != w.State {
		return fmt.Errorf("work %s state %q no longer matches expected state %q", workID, w.State, expected)
	}
	if in.PreEvaluatedDecision == nil {
		return fmt.Errorf("receipted action reservation requires a pre-evaluated policy decision")
	}
	if in.Action.IDs.WorkID != workID {
		return fmt.Errorf("action work_id %q does not match work %q", in.Action.IDs.WorkID, workID)
	}
	if err := in.Action.Validate(); err != nil {
		return fmt.Errorf("action intent invalid: %w", err)
	}
	if err := validatePreEvaluatedReceiptedAction(s, w, in.Action, *in.PreEvaluatedDecision, authorizedAt); err != nil {
		return err
	}
	if in.Action.MutationClass != "read" && strings.TrimSpace(in.Action.RollbackPlan) == "" {
		return fmt.Errorf("receipted action %s requires rollback_plan", in.Action.ID)
	}
	if strings.TrimSpace(in.Summary) == "" {
		return fmt.Errorf("receipted action summary required")
	}
	moveType := strings.TrimSpace(in.MoveType)
	if moveType != "" && !oneOf(moveType, envelope.MoveTypes) {
		return fmt.Errorf("receipted action move_type must be one of %v, got %q", envelope.MoveTypes, moveType)
	}
	return nil
}

func newReceiptedActionReservation(workID string, in ReceiptedActionInput, authorizedAt time.Time) (receiptedActionReservation, error) {
	decisionID := ""
	if in.PreEvaluatedDecision != nil {
		decisionID = in.PreEvaluatedDecision.ID
	}
	payload := struct {
		WorkID       string               `json:"work_id"`
		AuthorizedAt string               `json:"authorized_at"`
		Input        ReceiptedActionInput `json:"input"`
	}{WorkID: workID, AuthorizedAt: authorizedAt.UTC().Format(time.RFC3339Nano), Input: in}
	body, err := json.Marshal(payload)
	if err != nil {
		return receiptedActionReservation{}, fmt.Errorf("marshal receipted action reservation digest: %w", err)
	}
	digest := sha256.Sum256(body)
	reauthorizationDigest, err := receiptedActionReauthorizationDigest(workID, in)
	if err != nil {
		return receiptedActionReservation{}, err
	}
	outcome := strings.TrimSpace(in.Outcome)
	if outcome == "" {
		outcome = "completed"
	}
	reservation := receiptedActionReservation{
		SchemaVersion: receiptedActionReservationSchemaVersion,
		WorkID:        workID, ActionID: in.Action.ID, PolicyDecisionID: decisionID,
		ReceiptID:    idFor("rec_", "receipted-action", workID, in.Action.ID, decisionID, outcome),
		TransitionID: idFor("tr_", "receipted-action", workID, in.Action.ID, decisionID, outcome),
		AuthorizedAt: payload.AuthorizedAt, TransactionDigest: hex.EncodeToString(digest[:]),
		ReauthorizationDigest: reauthorizationDigest,
		Input:                 &in,
	}
	if err := reservation.validate(); err != nil {
		return receiptedActionReservation{}, err
	}
	return reservation, nil
}

func receiptedActionReauthorizationDigest(workID string, in ReceiptedActionInput) (string, error) {
	stable := in
	stable.PreEvaluatedDecision = nil
	stable.ProofRefs = make([]string, 0, len(in.ProofRefs))
	policyRefs := 0
	for _, ref := range in.ProofRefs {
		if strings.HasPrefix(ref, "policy-authority:") {
			policyRefs++
			continue
		}
		stable.ProofRefs = append(stable.ProofRefs, ref)
	}
	if policyRefs > 1 {
		return "", fmt.Errorf("receipted action reauthorization accepts at most one policy-authority proof ref")
	}
	body, err := json.Marshal(struct {
		WorkID string               `json:"work_id"`
		Input  ReceiptedActionInput `json:"input"`
	}{WorkID: workID, Input: stable})
	if err != nil {
		return "", fmt.Errorf("marshal receipted action reauthorization digest: %w", err)
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

// receiptedActionReservationEqual keeps ReauthorizationDigest additive. The
// digest is a retry/convergence aid rather than transaction authority, so a
// reservation or completion written before the field existed remains exact
// when every original transaction field matches. When both sides carry the
// digest, it must also match.
func receiptedActionReservationEqual(left, right receiptedActionReservation) bool {
	if left.ReauthorizationDigest != "" && right.ReauthorizationDigest != "" && left.ReauthorizationDigest != right.ReauthorizationDigest {
		return false
	}
	left.ReauthorizationDigest = ""
	right.ReauthorizationDigest = ""
	left.Input = nil
	right.Input = nil
	left.SchemaVersion = receiptedActionReservationLegacySchemaVersion
	right.SchemaVersion = receiptedActionReservationLegacySchemaVersion
	return reflect.DeepEqual(left, right)
}

func (s *Store) preflightReceiptedActionReservation(workID string) (receiptedActionReservation, bool, string, error) {
	var reservation receiptedActionReservation
	if filepath.Base(workID) != workID || !strings.HasPrefix(workID, "work_") {
		return reservation, false, "", fmt.Errorf("invalid receipted-action work id %q", workID)
	}
	path := filepath.Join(s.home, "work", "receipted-action-reservations", workID+".json")
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return reservation, false, path, nil
	}
	if err != nil {
		return reservation, false, path, fmt.Errorf("work %s receipted action reservation unreadable: %w", workID, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reservation); err != nil {
		return reservation, false, path, fmt.Errorf("work %s receipted action reservation invalid: %w", workID, err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return reservation, false, path, fmt.Errorf("work %s receipted action reservation invalid: %w", workID, err)
	}
	if err := reservation.validate(); err != nil {
		return reservation, false, path, fmt.Errorf("work %s receipted action reservation invalid: %w", workID, err)
	}
	if reservation.WorkID != workID {
		return reservation, false, path, fmt.Errorf("work %s receipted action reservation belongs to %s", workID, reservation.WorkID)
	}
	return reservation, true, path, nil
}

func (r receiptedActionReservation) validate() error {
	switch r.SchemaVersion {
	case receiptedActionReservationSchemaVersion:
		if r.ReauthorizationDigest == "" || r.Input == nil {
			return fmt.Errorf("v0.2 reservation requires reauthorization_digest and input recovery evidence")
		}
	case receiptedActionReservationLegacySchemaVersion:
		if r.ReauthorizationDigest != "" || r.Input != nil {
			return fmt.Errorf("v0.1 reservation cannot carry v0.2-only recovery fields")
		}
	default:
		return fmt.Errorf("schema_version must be %q or %q", receiptedActionReservationSchemaVersion, receiptedActionReservationLegacySchemaVersion)
	}
	for _, item := range []struct {
		name, value, prefix string
	}{
		{name: "work_id", value: r.WorkID, prefix: "work_"},
		{name: "action_intent_id", value: r.ActionID, prefix: "act_"},
		{name: "policy_decision_id", value: r.PolicyDecisionID, prefix: "pol_"},
		{name: "receipt_id", value: r.ReceiptID, prefix: "rec_"},
		{name: "transition_id", value: r.TransitionID, prefix: "tr_"},
	} {
		if filepath.Base(item.value) != item.value || !strings.HasPrefix(item.value, item.prefix) {
			return fmt.Errorf("%s must be a local %q identifier", item.name, item.prefix)
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, r.AuthorizedAt); err != nil {
		return fmt.Errorf("authorized_at invalid: %w", err)
	}
	digest, err := hex.DecodeString(r.TransactionDigest)
	if err != nil || len(digest) != sha256.Size {
		return fmt.Errorf("transaction_digest must be a SHA-256 hex digest")
	}
	if r.ReauthorizationDigest != "" {
		digest, err = hex.DecodeString(r.ReauthorizationDigest)
		if err != nil || len(digest) != sha256.Size {
			return fmt.Errorf("reauthorization_digest must be a SHA-256 hex digest")
		}
	}
	if r.Input != nil {
		if r.Input.PreEvaluatedDecision == nil || r.Input.Action.ID != r.ActionID || r.Input.Action.IDs.WorkID != r.WorkID || r.Input.PreEvaluatedDecision.ID != r.PolicyDecisionID {
			return fmt.Errorf("input does not bind reservation work/action/policy")
		}
		payload := struct {
			WorkID       string               `json:"work_id"`
			AuthorizedAt string               `json:"authorized_at"`
			Input        ReceiptedActionInput `json:"input"`
		}{WorkID: r.WorkID, AuthorizedAt: r.AuthorizedAt, Input: *r.Input}
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		inputDigest := sha256.Sum256(body)
		if hex.EncodeToString(inputDigest[:]) != r.TransactionDigest {
			return fmt.Errorf("input transaction digest mismatch")
		}
		stableDigest, err := receiptedActionReauthorizationDigest(r.WorkID, *r.Input)
		if err != nil || stableDigest != r.ReauthorizationDigest {
			return errors.Join(fmt.Errorf("input reauthorization digest mismatch"), err)
		}
		outcome := strings.TrimSpace(r.Input.Outcome)
		if outcome == "" {
			outcome = "completed"
		}
		if r.ReceiptID != idFor("rec_", "receipted-action", r.WorkID, r.ActionID, r.PolicyDecisionID, outcome) || r.TransitionID != idFor("tr_", "receipted-action", r.WorkID, r.ActionID, r.PolicyDecisionID, outcome) {
			return fmt.Errorf("input does not bind reservation receipt/transition")
		}
	}
	return nil
}

func (s *Store) clearReceiptedActionReservation(path string, expected receiptedActionReservation) error {
	actual, present, actualPath, err := s.preflightReceiptedActionReservation(expected.WorkID)
	if err != nil {
		return err
	}
	if !present || actualPath != path || !receiptedActionReservationEqual(actual, expected) {
		return fmt.Errorf("work %s receipted action reservation %s changed before cleanup", expected.WorkID, expected.ActionID)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncReceiptedActionDir(filepath.Dir(path))
}

func (s *Store) completeReceiptedActionReservation(path string, expected receiptedActionReservation, result ReceiptedActionResult) error {
	events, err := s.admissionEventsFor(expected.WorkID)
	if err != nil {
		return err
	}
	event, err := completedReceiptedActionEvent(events, result.Action, result.Decision, result.Receipt, result.Transition)
	if err != nil {
		return err
	}
	expectedCompletion, err := newReceiptedActionCompletion(expected, result.Receipt, result.Transition, event)
	if err != nil {
		return err
	}
	completion, present, completionPath, err := s.preflightReceiptedActionCompletion(expected.WorkID, expected.ActionID)
	if err != nil {
		return err
	}
	if present && !reflect.DeepEqual(completion, expectedCompletion) {
		return fmt.Errorf("work %s completed receipted action %s conflicts with reservation", expected.WorkID, expected.ActionID)
	}
	if !present {
		if err := ensureReceiptedActionPendingDir(filepath.Dir(completionPath)); err != nil {
			return err
		}
		if err := s.writeReceiptedActionBoundary("reservation_completion", completionPath, &expectedCompletion); err != nil {
			return err
		}
	}
	return s.clearReceiptedActionReservation(path, expected)
}

func (s *Store) preflightReceiptedActionCompletion(workID, actionID string) (receiptedActionCompletion, bool, string, error) {
	var completion receiptedActionCompletion
	if filepath.Base(workID) != workID || !strings.HasPrefix(workID, "work_") {
		return completion, false, "", fmt.Errorf("invalid receipted-action work id %q", workID)
	}
	if filepath.Base(actionID) != actionID || !strings.HasPrefix(actionID, "act_") {
		return completion, false, "", fmt.Errorf("invalid receipted-action action id %q", actionID)
	}
	path := filepath.Join(s.home, "work", "receipted-action-completions", workID, actionID+".json")
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return completion, false, path, nil
	}
	if err != nil {
		return completion, false, path, fmt.Errorf("work %s completed receipted action %s unreadable: %w", workID, actionID, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&completion); err != nil {
		return completion, false, path, fmt.Errorf("work %s completed receipted action %s invalid: %w", workID, actionID, err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return completion, false, path, fmt.Errorf("work %s completed receipted action %s invalid: %w", workID, actionID, err)
	}
	if err := completion.validate(); err != nil {
		return completion, false, path, fmt.Errorf("work %s completed receipted action %s invalid: %w", workID, actionID, err)
	}
	if completion.WorkID != workID || completion.ActionID != actionID {
		return completion, false, path, fmt.Errorf("work %s completed receipted action path conflicts with transaction identity", workID)
	}
	return completion, true, path, nil
}

func newReceiptedActionCompletion(reservation receiptedActionReservation, receipt envelope.Receipt, transition envelope.Transition, event Event) (receiptedActionCompletion, error) {
	receiptDigest, err := exactReceiptedActionDigest(receipt)
	if err != nil {
		return receiptedActionCompletion{}, fmt.Errorf("digest completed receipted action receipt: %w", err)
	}
	transitionDigest, err := exactReceiptedActionDigest(transition)
	if err != nil {
		return receiptedActionCompletion{}, fmt.Errorf("digest completed receipted action transition: %w", err)
	}
	eventDigest, err := exactReceiptedActionDigest(event)
	if err != nil {
		return receiptedActionCompletion{}, fmt.Errorf("digest completed receipted action event: %w", err)
	}
	completion := receiptedActionCompletion{
		SchemaVersion: receiptedActionCompletionSchemaVersion,
		WorkID:        reservation.WorkID, ActionID: reservation.ActionID, PolicyDecisionID: reservation.PolicyDecisionID,
		ReceiptID: reservation.ReceiptID, TransitionID: reservation.TransitionID,
		AuthorizedAt: reservation.AuthorizedAt, TransactionDigest: reservation.TransactionDigest,
		ReceiptSHA256: receiptDigest, TransitionSHA256: transitionDigest, EventSHA256: eventDigest,
	}
	if err := completion.validate(); err != nil {
		return receiptedActionCompletion{}, err
	}
	return completion, nil
}

func (c receiptedActionCompletion) reservation() receiptedActionReservation {
	return receiptedActionReservation{
		SchemaVersion: receiptedActionReservationLegacySchemaVersion,
		WorkID:        c.WorkID, ActionID: c.ActionID, PolicyDecisionID: c.PolicyDecisionID,
		ReceiptID: c.ReceiptID, TransitionID: c.TransitionID,
		AuthorizedAt: c.AuthorizedAt, TransactionDigest: c.TransactionDigest,
	}
}

func (c receiptedActionCompletion) validate() error {
	if c.SchemaVersion != receiptedActionCompletionSchemaVersion {
		return fmt.Errorf("schema_version must be %q", receiptedActionCompletionSchemaVersion)
	}
	if err := c.reservation().validate(); err != nil {
		return err
	}
	for _, item := range []struct {
		name, value string
	}{
		{name: "receipt_sha256", value: c.ReceiptSHA256},
		{name: "transition_sha256", value: c.TransitionSHA256},
		{name: "event_sha256", value: c.EventSHA256},
	} {
		digest, err := hex.DecodeString(item.value)
		if err != nil || len(digest) != sha256.Size {
			return fmt.Errorf("%s must be a SHA-256 hex digest", item.name)
		}
	}
	return nil
}

func exactReceiptedActionDigest(value any) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Store) replayCompletedReceiptedAction(workID string, in ReceiptedActionInput, completion receiptedActionCompletion) (ReceiptedActionResult, error) {
	var out ReceiptedActionResult
	action, present, err := s.preflightReceiptedAction(in.Action)
	if err != nil {
		return out, err
	}
	if !present {
		return out, fmt.Errorf("completed receipted action %s is missing its action envelope", completion.ActionID)
	}
	if in.PreEvaluatedDecision == nil {
		return out, fmt.Errorf("completed receipted action %s requires its pre-evaluated decision", completion.ActionID)
	}
	decision := *in.PreEvaluatedDecision
	var storedDecision envelope.PolicyDecision
	if err := s.readEventJSON(completion.PolicyDecisionID, &storedDecision); err != nil {
		return out, fmt.Errorf("completed receipted action %s decision unreadable: %w", completion.ActionID, err)
	}
	if err := storedDecision.Validate(); err != nil || !reflect.DeepEqual(storedDecision, decision) {
		return out, errors.Join(fmt.Errorf("completed receipted action %s decision conflicts with requested transaction", completion.ActionID), err)
	}
	var receipt envelope.Receipt
	if err := s.readEventJSON(completion.ReceiptID, &receipt); err != nil {
		return out, fmt.Errorf("completed receipted action %s receipt unreadable: %w", completion.ActionID, err)
	}
	if err := receipt.Validate(); err != nil {
		return out, fmt.Errorf("completed receipted action %s receipt invalid: %w", completion.ActionID, err)
	}
	receiptDigest, err := exactReceiptedActionDigest(receipt)
	if err != nil {
		return out, fmt.Errorf("completed receipted action %s receipt digest failed: %w", completion.ActionID, err)
	}
	if receiptDigest != completion.ReceiptSHA256 {
		return out, fmt.Errorf("completed receipted action %s receipt digest conflicts with completion identity", completion.ActionID)
	}
	var transition envelope.Transition
	if err := s.readEventJSON(completion.TransitionID, &transition); err != nil {
		return out, fmt.Errorf("completed receipted action %s transition unreadable: %w", completion.ActionID, err)
	}
	if err := transition.Validate(); err != nil {
		return out, fmt.Errorf("completed receipted action %s transition invalid: %w", completion.ActionID, err)
	}
	transitionDigest, err := exactReceiptedActionDigest(transition)
	if err != nil {
		return out, fmt.Errorf("completed receipted action %s transition digest failed: %w", completion.ActionID, err)
	}
	if transitionDigest != completion.TransitionSHA256 {
		return out, fmt.Errorf("completed receipted action %s transition digest conflicts with completion identity", completion.ActionID)
	}
	w, err := s.recoverPendingClaimBeforeMutation(workID)
	if err != nil {
		return out, err
	}
	linked, _, err := s.preflightReceiptedActionWorkLink(w, action.ID, decision.ID)
	if err != nil {
		return out, err
	}
	if !linked {
		return out, fmt.Errorf("completed receipted action %s is not linked from work %s", action.ID, workID)
	}
	if err := validateCompletedReceiptedActionOutcome(w, in, action, decision, receipt, transition, completion); err != nil {
		return out, err
	}
	events, err := s.admissionEventsFor(workID)
	if err != nil {
		return out, err
	}
	if err := validateCompletedReceiptedActionEvent(events, action, decision, receipt, transition, completion); err != nil {
		return out, err
	}
	return ReceiptedActionResult{Work: w, Action: action, Decision: decision, Receipt: receipt, Transition: transition, Replayed: true}, nil
}

func validateCompletedReceiptedActionOutcome(w envelope.WorkEnvelope, in ReceiptedActionInput, action envelope.ActionIntent, decision envelope.PolicyDecision, receipt envelope.Receipt, transition envelope.Transition, completion receiptedActionCompletion) error {
	outcome := strings.TrimSpace(in.Outcome)
	if outcome == "" {
		outcome = "completed"
	}
	moveType := strings.TrimSpace(in.MoveType)
	if moveType == "" {
		moveType = "control_plane_repair"
	}
	proofRefs := mergeUnique(in.ProofRefs, action.EvidenceRefs, in.BeforeRefs, in.AfterRefs, []string{"work:" + w.ID})
	if receipt.ID != completion.ReceiptID || receipt.IDs.WorkID != w.ID || receipt.Outcome != outcome || receipt.MoveType != moveType ||
		receipt.StateDelta.Changed != in.Changed || receipt.StateDelta.Summary != strings.TrimSpace(in.Summary) ||
		!reflect.DeepEqual(receipt.StateDelta.FilesChanged, mergeUnique(in.FilesChanged)) ||
		!reflect.DeepEqual(receipt.ProofRefs, proofRefs) || !reflect.DeepEqual(receipt.EvidenceRefs, proofRefs) ||
		!reflect.DeepEqual(receipt.FailureCause, in.FailureCause) {
		return fmt.Errorf("completed receipted action %s receipt conflicts with reserved outcome", action.ID)
	}
	wantBefore := mergeUnique([]string{w.IDs.ContextPackID}, in.BeforeRefs)
	wantAfter := mergeUnique([]string{receipt.ID}, in.AfterRefs)
	if transition.ID != completion.TransitionID || transition.IDs.WorkID != w.ID ||
		!reflect.DeepEqual(transition.ActionRefs, []string{action.ID}) || !reflect.DeepEqual(transition.BeforeRefs, wantBefore) ||
		!reflect.DeepEqual(transition.AfterRefs, wantAfter) || transition.StateDelta != receipt.StateDelta.Summary ||
		!reflect.DeepEqual(transition.EvidenceRefs, proofRefs) || transition.SecondOrder.FutureBehaviorChanged != in.Changed ||
		decision.ID != completion.PolicyDecisionID {
		return fmt.Errorf("completed receipted action %s transition conflicts with reserved outcome", action.ID)
	}
	return nil
}

func validateCompletedReceiptedActionEvent(events []Event, action envelope.ActionIntent, decision envelope.PolicyDecision, receipt envelope.Receipt, transition envelope.Transition, completion receiptedActionCompletion) error {
	event, err := completedReceiptedActionEvent(events, action, decision, receipt, transition)
	if err != nil {
		return err
	}
	digest, err := exactReceiptedActionDigest(event)
	if err != nil {
		return fmt.Errorf("completed receipted action %s event digest failed: %w", action.ID, err)
	}
	if digest != completion.EventSHA256 {
		return fmt.Errorf("completed receipted action %s event digest conflicts with completion identity", action.ID)
	}
	return nil
}

func completedReceiptedActionEvent(events []Event, action envelope.ActionIntent, decision envelope.PolicyDecision, receipt envelope.Receipt, transition envelope.Transition) (Event, error) {
	var matched Event
	wantAttached := []string{action.ID, decision.ID, receipt.ID, transition.ID}
	matching := 0
	for _, event := range events {
		if !receiptedActionEventTouches(event, wantAttached) {
			continue
		}
		if event.WorkID != action.IDs.WorkID || event.From != event.To || event.By != action.Actor ||
			event.Note != "receipted action recorded: "+action.Action+" (policy "+decision.Outcome+")" ||
			!reflect.DeepEqual(event.Attached, wantAttached) {
			return Event{}, fmt.Errorf("completed receipted action %s event conflicts with reserved outcome", action.ID)
		}
		matched = event
		matching++
	}
	if matching != 1 {
		return Event{}, fmt.Errorf("completed receipted action %s has %d matching lifecycle events", action.ID, matching)
	}
	return matched, nil
}
