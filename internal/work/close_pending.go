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
	"reflect"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/policy"
	"compound/internal/runattempt"
)

const closePendingSchemaVersion = "compound.work_close_pending.v0.1"

type closePendingRunAttempt struct {
	Attempt    runattempt.RunAttempt            `json:"attempt"`
	Intent     runattempt.TerminalizationIntent `json:"intent"`
	CallerOwns bool                             `json:"caller_owns,omitempty"`
}

type closePendingLease struct {
	Present bool   `json:"present"`
	Body    []byte `json:"body,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
}

type closePendingGit struct {
	RepositoryPath string                    `json:"repository_path,omitempty"`
	Expectations   []gitReferenceExpectation `json:"expectations,omitempty"`
}

// closePending is the exact transaction identity for one terminal close. It
// exists before the first close authority mutation and survives until the
// lifecycle event, terminal WorkEnvelope, Git proof locks, RunAttempts, and
// captured lease have all converged.
type closePending struct {
	SchemaVersion    string                   `json:"schema_version"`
	WorkID           string                   `json:"work_id"`
	InputSHA256      string                   `json:"input_sha256"`
	CompletedAt      string                   `json:"completed_at"`
	SourceWork       envelope.WorkEnvelope    `json:"source_work"`
	SourceWorkSHA256 string                   `json:"source_work_sha256"`
	FinalWork        envelope.WorkEnvelope    `json:"final_work"`
	FinalWorkSHA256  string                   `json:"final_work_sha256"`
	Action           envelope.ActionIntent    `json:"action"`
	Decision         envelope.PolicyDecision  `json:"decision"`
	Receipt          envelope.Receipt         `json:"receipt"`
	Transition       envelope.Transition      `json:"transition"`
	Event            Event                    `json:"event"`
	Lease            closePendingLease        `json:"lease"`
	LeaseHolder      string                   `json:"lease_holder,omitempty"`
	LeaseStolen      bool                     `json:"lease_stolen,omitempty"`
	RunAttempts      []closePendingRunAttempt `json:"run_attempts,omitempty"`
	Git              closePendingGit          `json:"git,omitempty"`
}

func (s *Store) closePendingPath(workID string) string {
	return s.workPath(workID) + ".close-pending"
}

func closeRecoveryRequired(format string, args ...any) error {
	return fmt.Errorf("close recovery_required: "+format, args...)
}

func closePendingSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func canonicalCloseJSON(value any) ([]byte, error) {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func sameCanonicalCloseJSON(left, right any) bool {
	leftBody, leftErr := json.Marshal(left)
	rightBody, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBody, rightBody)
}

func decodeStrictCloseJSON(body []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func (s *Store) loadClosePending(workID string) (closePending, bool, error) {
	var pending closePending
	body, err := home.ReadRootedRegularFile(s.home, s.closePendingPath(workID), "work close pending")
	if errors.Is(err, os.ErrNotExist) {
		return pending, false, nil
	}
	if err != nil {
		return pending, false, closeRecoveryRequired("read work %s frame: %w", workID, err)
	}
	if err := decodeStrictCloseJSON(body, &pending); err != nil {
		return pending, false, closeRecoveryRequired("decode work %s frame: %w", workID, err)
	}
	if err := validateClosePending(pending); err != nil {
		return pending, false, closeRecoveryRequired("validate work %s frame: %w", workID, err)
	}
	if pending.WorkID != workID {
		return pending, false, closeRecoveryRequired("frame identity %s differs from requested work %s", pending.WorkID, workID)
	}
	return pending, true, nil
}

func validateClosePending(p closePending) error {
	if p.SchemaVersion != closePendingSchemaVersion {
		return fmt.Errorf("schema_version must be %q", closePendingSchemaVersion)
	}
	if strings.TrimSpace(p.WorkID) == "" || p.SourceWork.ID != p.WorkID || p.FinalWork.ID != p.WorkID {
		return errors.New("work identity mismatch")
	}
	if strings.TrimSpace(p.InputSHA256) == "" {
		return errors.New("input_sha256 required")
	}
	completedAt, err := time.Parse(time.RFC3339Nano, p.CompletedAt)
	if err != nil || completedAt.IsZero() {
		return errors.New("completed_at must be an RFC3339 timestamp")
	}
	if err := p.SourceWork.Validate(); err != nil {
		return fmt.Errorf("source WorkEnvelope invalid: %w", err)
	}
	if err := p.FinalWork.Validate(); err != nil {
		return fmt.Errorf("final WorkEnvelope invalid: %w", err)
	}
	sourceSHA, err := workEnvelopeSHA256(p.SourceWork)
	if err != nil || sourceSHA != p.SourceWorkSHA256 {
		return errors.New("source WorkEnvelope digest mismatch")
	}
	finalSHA, err := workEnvelopeSHA256(p.FinalWork)
	if err != nil || finalSHA != p.FinalWorkSHA256 {
		return errors.New("final WorkEnvelope digest mismatch")
	}
	if err := p.Action.Validate(); err != nil || p.Action.IDs.WorkID != p.WorkID {
		return errors.New("close ActionIntent invalid or work-mismatched")
	}
	if err := p.Decision.Validate(); err != nil || p.Decision.IDs.WorkID != p.WorkID || p.Decision.ActionIntentID != p.Action.ID || !policy.Allowed(p.Decision) {
		return errors.New("close PolicyDecision invalid, denied, or action-mismatched")
	}
	if err := p.Receipt.Validate(); err != nil || p.Receipt.IDs.WorkID != p.WorkID {
		return errors.New("close Receipt invalid or work-mismatched")
	}
	if err := p.Transition.Validate(); err != nil || p.Transition.IDs.WorkID != p.WorkID {
		return errors.New("close Transition invalid or work-mismatched")
	}
	if p.FinalWork.State != p.Event.To || p.SourceWork.State != p.Event.From ||
		p.Event.WorkID != p.WorkID || p.Event.PreviousWorkSHA256 != p.SourceWorkSHA256 ||
		p.Event.WorkSHA256 != p.FinalWorkSHA256 ||
		!reflect.DeepEqual(p.Event.Attached, []string{p.Action.ID, p.Decision.ID, p.Receipt.ID, p.Transition.ID}) {
		return errors.New("closing lifecycle event does not bind the frozen generation")
	}
	if latestActionIntentID(p.FinalWork) != p.Action.ID || p.FinalWork.PolicyDecisionID != p.Decision.ID || p.FinalWork.IDs.TransitionID != p.Transition.ID {
		return errors.New("final WorkEnvelope does not bind the frozen close generation")
	}
	receiptSHA, err := receiptEnvelopeSHA256(p.Receipt)
	if err != nil || receiptSHA != p.Event.ReceiptSHA256 {
		return errors.New("closing lifecycle event Receipt digest mismatch")
	}
	if p.Lease.Present {
		if len(p.Lease.Body) == 0 || p.Lease.SHA256 != closePendingSHA256(p.Lease.Body) {
			return errors.New("captured lease body or digest invalid")
		}
		var lease Lease
		if err := decodeStrictCloseJSON(p.Lease.Body, &lease); err != nil || lease.WorkID != p.WorkID || strings.TrimSpace(lease.By) == "" {
			return errors.New("captured lease invalid or work-mismatched")
		}
		if strings.TrimSpace(p.LeaseHolder) != lease.By {
			return errors.New("captured lease holder mismatch")
		}
	} else if len(p.Lease.Body) != 0 || p.Lease.SHA256 != "" || p.LeaseHolder != "" || p.LeaseStolen {
		return errors.New("absent lease carries authority data")
	}
	if (p.Git.RepositoryPath == "") != (len(p.Git.Expectations) == 0) {
		return errors.New("Git repository and expectations must be present together")
	}
	digests := map[string]string{}
	for _, binding := range p.Event.RunAttemptTerminalizationDigests {
		if digests[binding.RunAttemptID] != "" {
			return fmt.Errorf("duplicate RunAttempt terminalization digest for %s", binding.RunAttemptID)
		}
		digests[binding.RunAttemptID] = binding.IntentSHA256
	}
	for _, frozen := range p.RunAttempts {
		if err := frozen.Intent.Validate(); err != nil || frozen.Attempt.ID == "" || frozen.Attempt.WorkID != p.WorkID || frozen.Intent.WorkID != p.WorkID {
			return fmt.Errorf("frozen RunAttempt %s invalid or work-mismatched", frozen.Attempt.ID)
		}
		digest, err := terminalizationIntentSHA256(frozen.Intent)
		if err != nil || digest != digests[frozen.Attempt.ID] {
			return fmt.Errorf("frozen RunAttempt %s terminalization digest mismatch", frozen.Attempt.ID)
		}
		delete(digests, frozen.Attempt.ID)
	}
	if len(digests) != 0 {
		return errors.New("closing lifecycle event has an unowned RunAttempt terminalization digest")
	}
	return nil
}

func (s *Store) captureClosePendingLease(workID string) (closePendingLease, Lease, error) {
	body, err := home.ReadRootedRegularFile(s.home, s.leasePath(workID), "work close lease")
	if errors.Is(err, os.ErrNotExist) {
		return closePendingLease{}, Lease{}, nil
	}
	if err != nil {
		return closePendingLease{}, Lease{}, err
	}
	var lease Lease
	if err := decodeStrictCloseJSON(body, &lease); err != nil {
		return closePendingLease{}, Lease{}, fmt.Errorf("decode work %s lease: %w", workID, err)
	}
	if lease.WorkID != workID || strings.TrimSpace(lease.By) == "" {
		return closePendingLease{}, Lease{}, fmt.Errorf("work %s lease identity invalid", workID)
	}
	return closePendingLease{Present: true, Body: append([]byte(nil), body...), SHA256: closePendingSHA256(body)}, lease, nil
}

func newClosePending(
	sourceWork, finalWork envelope.WorkEnvelope,
	inputSHA string,
	action envelope.ActionIntent,
	decision envelope.PolicyDecision,
	receipt envelope.Receipt,
	transition envelope.Transition,
	event Event,
	lease closePendingLease,
	leaseHolder string,
	leaseStolen bool,
	plans []runAttemptTerminalizationPlan,
	repositoryPath string,
	expectations []gitReferenceExpectation,
) (closePending, error) {
	sourceSHA, err := workEnvelopeSHA256(sourceWork)
	if err != nil {
		return closePending{}, err
	}
	finalSHA, err := workEnvelopeSHA256(finalWork)
	if err != nil {
		return closePending{}, err
	}
	pending := closePending{
		SchemaVersion:    closePendingSchemaVersion,
		WorkID:           sourceWork.ID,
		InputSHA256:      inputSHA,
		CompletedAt:      receipt.CreatedAt,
		SourceWork:       sourceWork,
		SourceWorkSHA256: sourceSHA,
		FinalWork:        finalWork,
		FinalWorkSHA256:  finalSHA,
		Action:           action,
		Decision:         decision,
		Receipt:          receipt,
		Transition:       transition,
		Event:            event,
		Lease:            lease,
		LeaseHolder:      leaseHolder,
		LeaseStolen:      leaseStolen,
		Git:              closePendingGit{RepositoryPath: repositoryPath, Expectations: append([]gitReferenceExpectation(nil), expectations...)},
	}
	for _, plan := range plans {
		pending.RunAttempts = append(pending.RunAttempts, closePendingRunAttempt{Attempt: plan.Attempt, Intent: plan.Intent, CallerOwns: plan.CallerOwns})
	}
	if err := validateClosePending(pending); err != nil {
		return closePending{}, err
	}
	return pending, nil
}

func (s *Store) persistClosePending(pending closePending) error {
	path := s.closePendingPath(pending.WorkID)
	staged, err := s.stageJSON(path, &pending)
	if err != nil {
		return err
	}
	if err := commitStagedJSONExclusive(staged, path); err != nil {
		_ = os.Remove(staged)
		return err
	}
	_, present, err := s.loadClosePending(pending.WorkID)
	if err != nil {
		return err
	}
	expectedBody, err := canonicalCloseJSON(&pending)
	if err != nil {
		return err
	}
	actualBody, err := home.ReadRootedRegularFile(s.home, path, "work close pending")
	if err != nil {
		return err
	}
	if !present || !bytes.Equal(actualBody, expectedBody) {
		return closeRecoveryRequired("persisted frame for work %s differs from the frozen transaction", pending.WorkID)
	}
	return nil
}

func plansFromClosePending(pending closePending) []runAttemptTerminalizationPlan {
	plans := make([]runAttemptTerminalizationPlan, 0, len(pending.RunAttempts))
	for _, frozen := range pending.RunAttempts {
		plans = append(plans, runAttemptTerminalizationPlan{Attempt: frozen.Attempt, Intent: frozen.Intent, CallerOwns: frozen.CallerOwns})
	}
	return plans
}

func (s *Store) verifyClosePendingLease(pending closePending, allowReleased bool) error {
	body, err := home.ReadRootedRegularFile(s.home, s.leasePath(pending.WorkID), "work close lease")
	if errors.Is(err, os.ErrNotExist) {
		if !pending.Lease.Present || allowReleased {
			return nil
		}
		return closeRecoveryRequired("work %s captured lease disappeared before terminal publication", pending.WorkID)
	}
	if err != nil {
		return closeRecoveryRequired("read work %s captured lease: %w", pending.WorkID, err)
	}
	if !pending.Lease.Present || !bytes.Equal(body, pending.Lease.Body) || closePendingSHA256(body) != pending.Lease.SHA256 {
		return closeRecoveryRequired("work %s lease differs from the frozen whole-file snapshot", pending.WorkID)
	}
	return nil
}

func (s *Store) removeClosePendingLease(pending closePending) error {
	if !pending.Lease.Present {
		return s.verifyClosePendingLease(pending, false)
	}
	_, err := home.ReadRootedRegularFile(s.home, s.leasePath(pending.WorkID), "work close lease")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return home.RemoveRootedRegularFileDurableValidated(s.home, s.leasePath(pending.WorkID), "work close lease", func(body []byte) error {
		if !bytes.Equal(body, pending.Lease.Body) || closePendingSHA256(body) != pending.Lease.SHA256 {
			return closeRecoveryRequired("work %s lease changed before release", pending.WorkID)
		}
		return nil
	})
}

func (s *Store) persistClosePendingEnvelopes(pending closePending) error {
	for _, item := range []struct {
		id    string
		value any
	}{
		{pending.Action.ID, &pending.Action},
		{pending.Decision.ID, &pending.Decision},
		{pending.Receipt.ID, &pending.Receipt},
		{pending.Transition.ID, &pending.Transition},
	} {
		if err := s.writeEventJSON(item.id, item.value); err != nil {
			return err
		}
		if item.id == pending.Action.ID {
			closeAfterFirstEnvelopeWrite()
		}
	}
	return nil
}

func (s *Store) publishClosePendingEventAndWork(pending closePending) error {
	err := s.withWorkEventLock(func() error {
		current, err := s.Get(pending.WorkID)
		if err != nil {
			return err
		}
		currentSHA, err := workEnvelopeSHA256(current)
		if err != nil {
			return err
		}
		events, err := s.readEventsStrictUnlocked(pending.WorkID)
		if err != nil {
			return err
		}
		exact := 0
		for _, event := range events {
			if sameCanonicalCloseJSON(event, pending.Event) {
				exact++
				continue
			}
			if event.WorkSHA256 == pending.FinalWorkSHA256 || event.PreviousWorkSHA256 == pending.SourceWorkSHA256 {
				return closeRecoveryRequired("work %s has a competing lifecycle event for the frozen close generation", pending.WorkID)
			}
		}
		if exact > 1 {
			return closeRecoveryRequired("work %s has %d copies of the frozen close event", pending.WorkID, exact)
		}
		publishWork := func() error {
			latest, err := s.Get(pending.WorkID)
			if err != nil {
				return err
			}
			latestSHA, err := workEnvelopeSHA256(latest)
			if err != nil {
				return err
			}
			if latestSHA != pending.SourceWorkSHA256 && latestSHA != pending.FinalWorkSHA256 {
				return closeRecoveryRequired("work %s changed before terminal WorkEnvelope publication", pending.WorkID)
			}
			if latestSHA == pending.FinalWorkSHA256 {
				return nil
			}
			return s.publishClaimJSON(s.workPath(pending.WorkID), &pending.FinalWork)
		}
		switch currentSHA {
		case pending.SourceWorkSHA256:
			if exact == 0 {
				if err := s.appendEventControlledUnlocked(pending.Event, nil, publishWork); err != nil {
					return err
				}
			} else if err := publishWork(); err != nil {
				return err
			}
		case pending.FinalWorkSHA256:
			if exact != 1 {
				return closeRecoveryRequired("terminal WorkEnvelope for %s lacks its one exact lifecycle event", pending.WorkID)
			}
		default:
			return closeRecoveryRequired("work %s matches neither frozen source nor final WorkEnvelope", pending.WorkID)
		}
		return nil
	})
	if err != nil {
		return err
	}
	stored, err := s.Get(pending.WorkID)
	if err != nil {
		return closeRecoveryRequired("read work %s terminal WorkEnvelope: %v", pending.WorkID, err)
	}
	storedSHA, err := workEnvelopeSHA256(stored)
	if err != nil || storedSHA != pending.FinalWorkSHA256 {
		return closeRecoveryRequired("work %s terminal WorkEnvelope verification failed", pending.WorkID)
	}
	events, err := s.eventsFor(pending.WorkID)
	if err != nil {
		return err
	}
	exact := 0
	for _, event := range events {
		if sameCanonicalCloseJSON(event, pending.Event) {
			exact++
		}
	}
	if exact != 1 {
		return closeRecoveryRequired("work %s exact close event count is %d, want one", pending.WorkID, exact)
	}
	return nil
}

func (s *Store) clearClosePending(pending closePending) error {
	expectedBody, err := canonicalCloseJSON(&pending)
	if err != nil {
		return err
	}
	return home.RemoveRootedRegularFileDurableValidated(s.home, s.closePendingPath(pending.WorkID), "work close pending", func(body []byte) error {
		var actual closePending
		if err := decodeStrictCloseJSON(body, &actual); err != nil {
			return err
		}
		if !bytes.Equal(body, expectedBody) {
			return closeRecoveryRequired("work %s frame changed before cleanup", pending.WorkID)
		}
		return nil
	})
}

func (s *Store) completeClosePending(pending closePending, in CloseInput) (CloseResult, error) {
	res := CloseResult{Work: pending.SourceWork, Receipt: pending.Receipt, Transition: pending.Transition}
	inputSHA, err := closeInputSHA256(in)
	if err != nil {
		return res, err
	}
	if inputSHA != pending.InputSHA256 {
		return res, closeRecoveryRequired("work %s retry input %s differs from frozen input %s", pending.WorkID, inputSHA, pending.InputSHA256)
	}
	current, err := s.Get(pending.WorkID)
	if err != nil {
		return res, err
	}
	currentSHA, err := workEnvelopeSHA256(current)
	if err != nil {
		return res, err
	}
	if currentSHA != pending.SourceWorkSHA256 && currentSHA != pending.FinalWorkSHA256 {
		return res, closeRecoveryRequired("work %s current generation differs from frozen close", pending.WorkID)
	}
	if err := s.verifyClosePendingLease(pending, currentSHA == pending.FinalWorkSHA256); err != nil {
		return res, err
	}

	var referenceTransaction gitReferenceTransaction
	if pending.Git.RepositoryPath != "" {
		if err := s.validateBoundGitDelivery(pending.SourceWork); err != nil {
			return res, closeRecoveryRequired("work %s Git delivery identity changed: %w", pending.WorkID, err)
		}
		referenceTransaction, err = startGitReferenceTransaction(pending.Git.RepositoryPath, pending.Git.Expectations)
		if err != nil {
			return res, closeRecoveryRequired("prepare Git reference transaction: %w", err)
		}
	}
	referenceFinished := false
	defer func() {
		if referenceTransaction != nil && !referenceFinished {
			_ = referenceTransaction.Abort()
		}
	}()
	abortReference := func(cause error) error {
		if referenceTransaction == nil {
			return cause
		}
		abortErr := referenceTransaction.Abort()
		referenceFinished = true
		if abortErr != nil {
			return errors.Join(cause, fmt.Errorf("abort Git reference transaction: %w", abortErr))
		}
		return cause
	}

	plans := plansFromClosePending(pending)
	if err := s.prepareRunAttemptTerminalizations(plans); err != nil {
		return res, abortReference(err)
	}
	closeAfterTerminalizationPrepare()
	if pending.LeaseStolen {
		expected := leaseHolderStatus{Holder: pending.LeaseHolder, Stolen: true}
		if err := s.leaseHolderCloseGate(pending.SourceWork, pending.Action.Actor, in, expected); err != nil {
			return res, abortReference(err)
		}
		closeAfterLeaseStealDecision()
	}
	if err := s.persistClosePendingEnvelopes(pending); err != nil {
		return res, abortReference(err)
	}
	if err := s.publishClosePendingEventAndWork(pending); err != nil {
		return res, abortReference(err)
	}
	if referenceTransaction != nil {
		if err := referenceTransaction.Commit(); err != nil {
			referenceFinished = true
			return res, closeRecoveryRequired("commit Git reference transaction: %w", err)
		}
		referenceFinished = true
	}
	completedAt, _ := time.Parse(time.RFC3339Nano, pending.CompletedAt)
	unterminalized := s.terminalizePreparedRunAttempts(plans, completedAt)
	res = CloseResult{Work: pending.FinalWork, Receipt: pending.Receipt, Transition: pending.Transition, UnterminalizedRunAttemptIDs: unterminalized}
	if len(unterminalized) > 0 {
		return res, nil
	}
	closeBeforeLeaseRelease()
	if err := s.removeClosePendingLease(pending); err != nil {
		return res, closeRecoveryRequired("release work %s lease: %w", pending.WorkID, err)
	}
	if err := s.clearClosePending(pending); err != nil {
		return res, closeRecoveryRequired("clear work %s frame: %w", pending.WorkID, err)
	}
	return res, nil
}
