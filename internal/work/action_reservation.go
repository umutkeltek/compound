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

const receiptedActionReservationSchemaVersion = "compound.receipted_action_reservation.v0.1"

// receiptedActionReservation closes the gap between governed admission and an
// adapter's external publication. While present, the work lifecycle cannot
// advance past the exact action that the adapter is authorized to finalize.
type receiptedActionReservation struct {
	SchemaVersion     string `json:"schema_version"`
	WorkID            string `json:"work_id"`
	ActionID          string `json:"action_intent_id"`
	PolicyDecisionID  string `json:"policy_decision_id"`
	AuthorizedAt      string `json:"authorized_at"`
	TransactionDigest string `json:"transaction_digest"`
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
			if !reflect.DeepEqual(actual, expected) {
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
			return fmt.Errorf("work %s has no receipted action reservation for %s", workID, in.Action.ID)
		}
		if !reflect.DeepEqual(actual, expected) {
			return fmt.Errorf("work %s receipted action reservation %s conflicts with requested transaction", workID, actual.ActionID)
		}
		out, recordErr = s.recordReceiptedActionLocked(workID, in, authorizedAt)
		if recordErr != nil {
			return recordErr
		}
		return s.clearReceiptedActionReservation(path, expected)
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
		if _, err := os.Stat(s.envPath(actionID)); err == nil {
			return fmt.Errorf("cannot abort receipted action reservation %s after receipt publication began", actionID)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
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
	reservation := receiptedActionReservation{
		SchemaVersion: receiptedActionReservationSchemaVersion,
		WorkID:        workID, ActionID: in.Action.ID, PolicyDecisionID: decisionID,
		AuthorizedAt: payload.AuthorizedAt, TransactionDigest: hex.EncodeToString(digest[:]),
	}
	if err := reservation.validate(); err != nil {
		return receiptedActionReservation{}, err
	}
	return reservation, nil
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
	if r.SchemaVersion != receiptedActionReservationSchemaVersion {
		return fmt.Errorf("schema_version must be %q", receiptedActionReservationSchemaVersion)
	}
	for _, item := range []struct {
		name, value, prefix string
	}{
		{name: "work_id", value: r.WorkID, prefix: "work_"},
		{name: "action_intent_id", value: r.ActionID, prefix: "act_"},
		{name: "policy_decision_id", value: r.PolicyDecisionID, prefix: "pol_"},
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
	return nil
}

func (s *Store) clearReceiptedActionReservation(path string, expected receiptedActionReservation) error {
	actual, present, actualPath, err := s.preflightReceiptedActionReservation(expected.WorkID)
	if err != nil {
		return err
	}
	if !present || actualPath != path || !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("work %s receipted action reservation %s changed before cleanup", expected.WorkID, expected.ActionID)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncReceiptedActionDir(filepath.Dir(path))
}
