package work

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/landlease"
	"compound/internal/policy"
	"compound/internal/runattempt"
)

const (
	ProofResourceReservationSchemaVersion         = "compound.work_proof_reservation.v0.2"
	ProofResourceRefreshSchemaVersion             = "compound.work_proof_refresh.v0.1"
	ProofResourceResultSchemaVersion              = "compound.work_proof_result.v0.1"
	ProofResourceCompletionSchemaVersion          = "compound.work_proof_completion.v0.1"
	ProofResourceTerminationSchemaVersion         = "compound.work_proof_termination.v0.1"
	ProofResourceTerminationEvidenceSchemaVersion = "compound.work_proof_termination_evidence.v0.1"
	ProofResourceName                             = "compound-proof-battery"

	ProofResourceAcquired = "acquired"
	ProofResourceRefused  = "refused"
	ProofResourceVerified = "verified"
	ProofResourceFailed   = "failed"
	ProofResourceInvalid  = "invalid"

	ProofResourceProfileCanonicalFull = "canonical_full"

	proofResourceHarness = "proof-check"
)

// ProofResourceReserveInput requests the existing process-wide landlease mutex for a governed
// full proof run. Workspace and Command bind the real proof-owning RunAttempt before any gate
// subprocess can begin. Steal is exceptional and requires an explicit audited reason.
type ProofResourceReserveInput struct {
	Actor                  string
	TTL                    time.Duration
	Workspace              string
	Command                string
	Profile                ProofResourceProfile
	Steal                  bool
	StealReason            string
	TerminatedRunAttemptID string
	TerminationEvidenceRef string
}

// ProofResourceResumeInput identifies the canonical command boundary that may finish an existing
// retained completion. It never authorizes a new reservation or proof execution.
type ProofResourceResumeInput struct {
	Actor     string
	Workspace string
	Command   string
	Profile   ProofResourceProfile
	// PrepareOwnedReport is the projection-publication barrier for a retained report. Resume calls
	// it only after exact pending-input validation and before completion may release exclusivity.
	PrepareOwnedReport func(ProofResourceResult) error
}

// ProofResourceGateProfile is the immutable identity of one canonical full-proof gate.
// Runtime-only fields such as status, duration, exit code, and output are intentionally absent.
type ProofResourceGateProfile struct {
	Name          string   `json:"name"`
	Command       []string `json:"command"`
	Dir           string   `json:"dir,omitempty"`
	SkipIfMissing string   `json:"skip_if_missing,omitempty"`
	ProofRef      string   `json:"proof_ref"`
}

// ProofResourceProfile binds admission to a typed proof inventory. Only canonical_full may later
// become governed verification. Digest covers Kind, Repo, BaseRef, and the ordered gate identities.
type ProofResourceProfile struct {
	Kind    string                     `json:"kind"`
	Repo    string                     `json:"repo"`
	BaseRef string                     `json:"base_ref,omitempty"`
	Gates   []ProofResourceGateProfile `json:"gates"`
	Digest  string                     `json:"digest"`
}

// NewProofResourceProfile validates and seals one typed proof inventory.
func NewProofResourceProfile(kind, repo, baseRef string, gates []ProofResourceGateProfile) (ProofResourceProfile, error) {
	profile := ProofResourceProfile{
		Kind:    strings.TrimSpace(kind),
		Repo:    strings.TrimSpace(repo),
		BaseRef: strings.TrimSpace(baseRef),
		Gates:   append([]ProofResourceGateProfile(nil), gates...),
	}
	for i := range profile.Gates {
		profile.Gates[i].Name = strings.TrimSpace(profile.Gates[i].Name)
		profile.Gates[i].Command = append([]string(nil), profile.Gates[i].Command...)
		profile.Gates[i].Dir = strings.TrimSpace(profile.Gates[i].Dir)
		profile.Gates[i].SkipIfMissing = strings.TrimSpace(profile.Gates[i].SkipIfMissing)
		profile.Gates[i].ProofRef = strings.TrimSpace(profile.Gates[i].ProofRef)
	}
	if err := validateProofResourceProfileShape(profile); err != nil {
		return ProofResourceProfile{}, err
	}
	digest, err := proofResourceProfileDigest(profile)
	if err != nil {
		return ProofResourceProfile{}, err
	}
	profile.Digest = digest
	return profile, nil
}

// ProofResourceReservation is work-owned evidence around the existing landlease mutex. It creates
// no second scheduler, lock file, or authority: Holder is the exact landlease holder, RunAttemptID
// is the concrete proof execution, and work_id remains the multiplayer root.
type ProofResourceReservation struct {
	SchemaVersion             string               `json:"schema_version"`
	ID                        string               `json:"id"`
	Kind                      string               `json:"kind"`
	Status                    string               `json:"status"`
	CreatedAt                 string               `json:"created_at"`
	WorkID                    string               `json:"work_id"`
	Actor                     string               `json:"actor"`
	Resource                  string               `json:"resource"`
	Holder                    string               `json:"holder"`
	RunAttemptID              string               `json:"run_attempt_id,omitempty"`
	Workspace                 string               `json:"workspace"`
	Command                   string               `json:"command"`
	Profile                   ProofResourceProfile `json:"proof_profile"`
	AcquiredAt                string               `json:"acquired_at,omitempty"`
	ExpiresAt                 string               `json:"expires_at,omitempty"`
	TTLSeconds                int64                `json:"ttl_seconds,omitempty"`
	ConflictingHolder         string               `json:"conflicting_holder,omitempty"`
	RecoveredHolder           string               `json:"recovered_holder,omitempty"`
	RecoveredExpiresAt        string               `json:"recovered_expires_at,omitempty"`
	RecoveredRunAttemptID     string               `json:"recovered_run_attempt_id,omitempty"`
	PriorHolder               string               `json:"prior_holder,omitempty"`
	PriorRunAttemptID         string               `json:"prior_run_attempt_id,omitempty"`
	StealReason               string               `json:"steal_reason,omitempty"`
	StolenAt                  string               `json:"stolen_at,omitempty"`
	TerminatedRunAttemptID    string               `json:"terminated_run_attempt_id,omitempty"`
	TerminationEvidenceRef    string               `json:"termination_evidence_ref,omitempty"`
	TerminationConfirmedAt    string               `json:"termination_confirmed_at,omitempty"`
	TerminationConfirmationID string               `json:"termination_confirmation_id,omitempty"`
	FailureReason             string               `json:"failure_reason,omitempty"`
	ActionIntentID            string               `json:"action_intent_id"`
	PolicyDecisionID          string               `json:"policy_decision_id"`
}

type ProofResourceReserveResult struct {
	ExecutionBarrier *landlease.ExecutionBarrier `json:"-"`
	SchemaVersion    string                      `json:"schema_version"`
	Reservation      ProofResourceReservation    `json:"reservation"`
	Work             envelope.WorkEnvelope       `json:"work"`
}

// ProofResourceRefresh is append-only evidence that the original governed reservation remained
// continuously live and its exact RunAttempt heartbeat extended the exclusive window.
type ProofResourceRefresh struct {
	SchemaVersion    string `json:"schema_version"`
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	CreatedAt        string `json:"created_at"`
	WorkID           string `json:"work_id"`
	Actor            string `json:"actor"`
	ReservationID    string `json:"reservation_id"`
	RunAttemptID     string `json:"run_attempt_id"`
	Holder           string `json:"holder"`
	WindowOpenedAt   string `json:"window_opened_at"`
	RefreshedAt      string `json:"refreshed_at"`
	ExpiresAt        string `json:"expires_at"`
	TTLSeconds       int64  `json:"ttl_seconds"`
	ActionIntentID   string `json:"action_intent_id"`
	PolicyDecisionID string `json:"policy_decision_id"`
}

// ProofResourceTerminationConfirmation is the governed, immutable bridge between external
// process-supervisor evidence and a predecessor RunAttempt terminal state. It carries no PID:
// identity is the non-reusable ratt_* plus a content-addressed evidence ref.
type ProofResourceTerminationConfirmation struct {
	SchemaVersion          string `json:"schema_version"`
	ID                     string `json:"id"`
	Kind                   string `json:"kind"`
	CreatedAt              string `json:"created_at"`
	WorkID                 string `json:"work_id"`
	Actor                  string `json:"actor"`
	ReservationID          string `json:"reservation_id"`
	PriorHolder            string `json:"prior_holder"`
	TerminatedRunAttemptID string `json:"terminated_run_attempt_id"`
	TerminationEvidenceRef string `json:"termination_evidence_ref"`
	ActionIntentID         string `json:"action_intent_id"`
	PolicyDecisionID       string `json:"policy_decision_id"`
}

// ProofResourceTerminationEvidence is the immutable supervisor receipt required to recover a
// predecessor that could not terminalize itself. RunAttempt identity is stable and non-reusable;
// process IDs are deliberately absent.
type ProofResourceTerminationEvidence struct {
	Producer      string `json:"producer"`
	BarrierID     string `json:"barrier_id"`
	Holder        string `json:"holder"`
	SchemaVersion string `json:"schema_version"`
	RunAttemptID  string `json:"run_attempt_id"`
	Status        string `json:"status"`
	ObservedAt    string `json:"observed_at"`
	Observer      string `json:"observer"`
}

// ProofResourceHeldError is returned only after the refused attempt has been terminalized and
// attached to its work lifecycle. The caller can report ReservationID without inventing a second
// coordination message, and its runner callback must remain unentered.
type ProofResourceHeldError struct {
	WorkID        string
	ReservationID string
	Holder        string
	ExpiresAt     string
	Remaining     int64
}

func (e *ProofResourceHeldError) Error() string {
	return fmt.Sprintf(
		"proof resource %s held by %q until %s (%ds remaining); refusal recorded as %s on %s",
		ProofResourceName,
		e.Holder,
		e.ExpiresAt,
		e.Remaining,
		e.ReservationID,
		e.WorkID,
	)
}

// ProofResourceCompleteInput records an internally-observed proof producer outcome. The immutable
// report artifact is parsed by CompleteProofResource; caller-supplied status/owned booleans are not
// trusted. ProducerExitKnown prevents piped or otherwise exit-ambiguous output from becoming proof.
// FailureReason is the mutually-exclusive infrastructure-failure path when no report was produced.
type ProofResourceCompleteInput struct {
	Actor             string
	ReservationID     string
	ReportArtifactRef string
	ProducerExitKnown bool
	ProducerExitCode  int
	FailureReason     string
}

type ProofResourceResult struct {
	SchemaVersion     string `json:"schema_version"`
	ID                string `json:"id"`
	Kind              string `json:"kind"`
	Status            string `json:"status"`
	CreatedAt         string `json:"created_at"`
	WorkID            string `json:"work_id"`
	Actor             string `json:"actor"`
	Resource          string `json:"resource"`
	ReservationID     string `json:"reservation_id"`
	RunAttemptID      string `json:"run_attempt_id"`
	RunAttemptStatus  string `json:"run_attempt_status"`
	ReportArtifactRef string `json:"report_artifact_ref,omitempty"`
	ReportStatus      string `json:"report_status,omitempty"`
	OwnedReport       bool   `json:"owned_report"`
	ProducerExitKnown bool   `json:"producer_exit_known"`
	ProducerExitCode  int    `json:"producer_exit_code,omitempty"`
	FailureReason     string `json:"failure_reason,omitempty"`
	VerificationRef   string `json:"verification_ref,omitempty"`
	CompletionID      string `json:"completion_id"`
	ActionIntentID    string `json:"action_intent_id"`
	PolicyDecisionID  string `json:"policy_decision_id"`
}

// ProofResourceCompletion is the authoritative finalizer marker. A prepared result record is
// never valid proof on its own: the terminal RunAttempt, this marker, and the Work verification
// link must all converge before the scarce holder is released.
type ProofResourceCompletion struct {
	SchemaVersion    string `json:"schema_version"`
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	CreatedAt        string `json:"created_at"`
	WorkID           string `json:"work_id"`
	Actor            string `json:"actor"`
	ReservationID    string `json:"reservation_id"`
	ResultID         string `json:"result_id"`
	RunAttemptID     string `json:"run_attempt_id"`
	RunAttemptStatus string `json:"run_attempt_status"`
	VerificationRef  string `json:"verification_ref,omitempty"`
	ActionIntentID   string `json:"action_intent_id"`
	PolicyDecisionID string `json:"policy_decision_id"`
}

type ProofResourceCompleteResult struct {
	SchemaVersion           string                `json:"schema_version"`
	Result                  ProofResourceResult   `json:"result"`
	VerificationRef         string                `json:"verification_ref,omitempty"`
	ResourceReleased        bool                  `json:"resource_released"`
	RecoveryJournalRetained bool                  `json:"recovery_journal_retained,omitempty"`
	ReleaseWarning          string                `json:"release_warning,omitempty"`
	Work                    envelope.WorkEnvelope `json:"work"`
}

// ProofResourceReleasePendingError means the authoritative proof commit is visible, but the
// scarce holder and exact recovery journal are intentionally retained. Callers must not present
// this state as an ordinary green proof; retrying the exact completion releases idempotently.
type ProofResourceReleasePendingError struct {
	ReservationID string
	Holder        string
	Cause         error
}

func (e *ProofResourceReleasePendingError) Error() string {
	return fmt.Sprintf(
		"proof resource %s committed but release remains pending for holder %q: %v",
		e.ReservationID,
		e.Holder,
		e.Cause,
	)
}

func (e *ProofResourceReleasePendingError) Unwrap() error {
	return e.Cause
}

const proofResourceCompletionPendingSchemaVersion = "compound.work_proof_completion_pending.v0.1"

type proofResourceCompletionPending struct {
	SchemaVersion   string                  `json:"schema_version"`
	WorkID          string                  `json:"work_id"`
	ReservationID   string                  `json:"reservation_id"`
	InputDigest     string                  `json:"input_digest"`
	GovernanceSeal  string                  `json:"governance_seal,omitempty"`
	CreatedAt       string                  `json:"created_at"`
	Action          envelope.ActionIntent   `json:"action"`
	Decision        envelope.PolicyDecision `json:"decision"`
	Result          ProofResourceResult     `json:"result"`
	Completion      ProofResourceCompletion `json:"completion"`
	VerificationRef string                  `json:"verification_ref,omitempty"`
	TerminalStatus  string                  `json:"terminal_status"`
	TerminalReason  string                  `json:"terminal_reason"`
	ProofRefs       []string                `json:"proof_refs"`
	LifecycleEvent  Event                   `json:"lifecycle_event"`
}

type proofResourceCompletionExpectation struct {
	InputDigest     string
	ResultStatus    string
	ReportStatus    string
	OwnedReport     bool
	VerificationRef string
	TerminalStatus  string
	TerminalReason  string
}

var proofResourceWriteEventRecord = func(s *Store, stage, id string, value any) error {
	return s.writeEventJSON(id, value)
}

var proofResourceWriteWorkRecord = func(s *Store, stage string, w *envelope.WorkEnvelope) error {
	return s.writeJSON(s.workPath(w.ID), w)
}

var proofResourceAppendLifecycleRecord = func(s *Store, event Event) error {
	return s.appendEventControlled(event, nil, nil)
}

var proofResourceSetRunAttemptProofRefs = func(s *Store, id string, refs []string) error {
	_, err := s.runAttempts().SetProofRefs(id, refs)
	return err
}

var proofResourceTerminalizeRunAttempt = func(s *Store, id, status, reason string, now time.Time) (runattempt.RunAttempt, error) {
	return s.runAttempts().CompleteWithReason(id, status, reason, now)
}

var proofResourceAcquireWithExecutionBarrierPrepared = landlease.AcquireWithExecutionBarrierPrepared
var proofResourceAfterScarceMutation = func(phase string) {}

// proofResourceAfterCompletionRetryMint is the deterministic hard-crash test boundary after a
// retry is fully bound but before its pending journal or Work reachability may advance.
var proofResourceAfterCompletionRetryMint = func(runattempt.RunAttempt) {}
var proofResourceWriteCompletionRetryJournal = func(
	s *Store,
	path string,
	pending proofResourceCompletionPending,
	_ runattempt.RunAttempt,
) error {
	return s.writeProofResourceCompletionPending(path, pending)
}
var proofResourceAfterCompletionRetryJournal = func(runattempt.RunAttempt, proofResourceCompletionPending) {}

type ownedProofReport struct {
	SchemaVersion string   `json:"schema_version"`
	ProfileKind   string   `json:"profile_kind"`
	Status        string   `json:"status"`
	GeneratedAt   string   `json:"generated_at"`
	Repo          string   `json:"repo"`
	BaseRef       string   `json:"base_ref,omitempty"`
	DryRun        bool     `json:"dry_run"`
	CIOnly        bool     `json:"ci_only,omitempty"`
	CIConsole     bool     `json:"ci_console,omitempty"`
	ProofRefs     []string `json:"proof_refs"`
	Steps         []struct {
		Name          string   `json:"name"`
		Command       []string `json:"command"`
		Dir           string   `json:"dir,omitempty"`
		SkipIfMissing string   `json:"skip_if_missing,omitempty"`
		ProofRef      string   `json:"proof_ref"`
		Status        string   `json:"status"`
	} `json:"steps"`
}

// ReserveProofResource evaluates and persists one ActionIntent/PolicyDecision around acquisition
// of the existing landlease mutex. A competing holder is refused before this function returns.
func (s *Store) ReserveProofResource(workID string, in ProofResourceReserveInput, now time.Time) (ProofResourceReserveResult, error) {
	var result ProofResourceReserveResult
	err := s.withLifecycleMutation(func() error {
		var err error
		result, err = s.reserveProofResourceLocked(strings.TrimSpace(workID), in, now)
		return err
	})
	if err != nil {
		return result, err
	}
	predecessorHolders, lineageErr := proofReservationPredecessorHolders(result.Reservation)
	if lineageErr != nil {
		barrierErr := s.retainProofTakeoverBarrier(result.Reservation, lineageErr, now)
		return result, errors.Join(lineageErr, barrierErr)
	}
	if len(predecessorHolders) == 0 {
		return result, nil
	}
	waitStarted := time.Now()
	grace := 3 * time.Second
	if ttlGrace := in.TTL / 3; ttlGrace > 0 && ttlGrace < grace {
		grace = ttlGrace
	}
	if grace < 100*time.Millisecond {
		grace = 100 * time.Millisecond
	}
	waitErr := s.awaitRecoveredProofAttempt(predecessorHolders[0], grace)
	checkAt := now.Add(time.Since(waitStarted))
	if waitErr == nil {
		status, statusErr := landlease.Status(s.home, checkAt)
		if statusErr != nil {
			waitErr = statusErr
		} else if !status.Held || status.Holder != result.Reservation.Holder {
			waitErr = fmt.Errorf(
				"proof resource holder %q changed while awaiting predecessor termination; current holder %q",
				result.Reservation.Holder,
				status.Holder,
			)
		}
	}
	if waitErr != nil {
		barrierErr := s.retainProofTakeoverBarrier(result.Reservation, waitErr, checkAt)
		return result, errors.Join(waitErr, barrierErr)
	}
	return result, err
}

func (s *Store) retainProofTakeoverBarrier(
	reservation ProofResourceReservation,
	cause error,
	now time.Time,
) error {
	return s.withLifecycleMutation(func() error {
		reason := "proof resource takeover retained as a non-executable barrier: " + cause.Error()
		if err := s.completeProofAttempt(
			reservation.RunAttemptID,
			runattempt.StatusAbandoned,
			reason,
			now,
		); err != nil {
			return err
		}
		state := ""
		if w, err := s.Get(reservation.WorkID); err == nil {
			state = w.State
		}
		attached := []string{reservation.ID, reservation.RunAttemptID}
		if reservation.RecoveredRunAttemptID != "" {
			attached = append(attached, reservation.RecoveredRunAttemptID)
		}
		if reservation.PriorRunAttemptID != "" {
			attached = append(attached, reservation.PriorRunAttemptID)
		}
		return s.appendEvent(Event{
			TS:       ts(now),
			WorkID:   reservation.WorkID,
			From:     state,
			To:       state,
			By:       reservation.Actor,
			Note:     reason,
			Attached: attached,
		})
	})
}

func (s *Store) awaitRecoveredProofAttempt(holder string, grace time.Duration) error {
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		blocker, err := s.proofRecoveryOldestNonterminal(holder)
		if err != nil {
			return err
		}
		if blocker.RunAttemptID == "" {
			return nil
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return fmt.Errorf(
				"proof resource predecessor %s did not confirm termination; expiry or a synthetic terminal barrier cannot make a successor executable",
				blocker.RunAttemptID,
			)
		}
	}
}

// ResumeProofResourceCompletion finishes an exact pending completion for the current live proof
// holder. The caller uses this before ReserveProofResource: a retained post-commit lease is released
// through its existing reservation, pending input, governance, and RunAttempt without entering the
// proof runner or minting a conflicting holder. found=false means normal admission should continue.
func (s *Store) ResumeProofResourceCompletion(
	workID string,
	in ProofResourceResumeInput,
	now time.Time,
) (result ProofResourceCompleteResult, found bool, err error) {
	workID = strings.TrimSpace(workID)
	in.Actor = strings.TrimSpace(in.Actor)
	in.Workspace = strings.TrimSpace(in.Workspace)
	in.Command = strings.TrimSpace(in.Command)
	status, err := landlease.Status(s.home, now)
	if err != nil {
		return ProofResourceCompleteResult{}, false, err
	}
	expiredExact := !status.Held && status.IsExpired
	if !status.Held && !expiredExact {
		return ProofResourceCompleteResult{}, false, nil
	}
	reservationID, runAttemptID, heldWorkID, proofHolder := parseProofResourceHolder(status.Holder)
	if !proofHolder || heldWorkID != workID {
		return ProofResourceCompleteResult{}, false, nil
	}
	var reservation ProofResourceReservation
	if err := s.readEventJSON(reservationID, &reservation); err != nil {
		return ProofResourceCompleteResult{}, true, fmt.Errorf("read retained proof reservation %s: %w", reservationID, err)
	}
	pending, present, _, err := s.readProofResourceCompletionPending(reservationID)
	if err != nil {
		return ProofResourceCompleteResult{}, true, err
	}
	if !present {
		return ProofResourceCompleteResult{}, false, nil
	}
	profile, profileErr := NewProofResourceProfile(in.Profile.Kind, in.Profile.Repo, in.Profile.BaseRef, in.Profile.Gates)
	if profileErr == nil && strings.TrimSpace(in.Profile.Digest) != "" && in.Profile.Digest != profile.Digest {
		profileErr = fmt.Errorf("proof resource profile digest %q does not match canonical %q", in.Profile.Digest, profile.Digest)
	}
	if profileErr != nil {
		return ProofResourceCompleteResult{}, true, fmt.Errorf("resume proof resource profile: %w", profileErr)
	}
	if workID == "" ||
		in.Actor == "" ||
		in.Workspace == "" ||
		in.Command == "" ||
		reservation.WorkID != workID ||
		reservation.Actor != in.Actor ||
		reservation.Workspace != in.Workspace ||
		reservation.Command != in.Command ||
		reservation.Status != ProofResourceAcquired ||
		reservation.Holder != status.Holder ||
		reservation.RunAttemptID != runAttemptID ||
		!reflect.DeepEqual(reservation.Profile, profile) ||
		pending.WorkID != workID ||
		pending.ReservationID != reservation.ID ||
		pending.Result.Actor != in.Actor {
		return ProofResourceCompleteResult{}, true, fmt.Errorf("retained proof completion does not match the exact work, actor, command, workspace, profile, and holder")
	}
	completeInput := ProofResourceCompleteInput{
		Actor:             in.Actor,
		ReservationID:     reservation.ID,
		ReportArtifactRef: pending.Result.ReportArtifactRef,
		ProducerExitKnown: pending.Result.ProducerExitKnown,
		ProducerExitCode:  pending.Result.ProducerExitCode,
		FailureReason:     pending.Result.FailureReason,
	}
	inputDigest, err := proofResourceCompletionInputDigest(workID, completeInput)
	if err != nil {
		return ProofResourceCompleteResult{}, true, err
	}
	if inputDigest != pending.InputDigest {
		return ProofResourceCompleteResult{}, true, fmt.Errorf("retained proof completion input digest changed")
	}
	if expiredExact {
		if err := s.rearmExpiredProofResourceCompletion(
			workID,
			in,
			reservation,
			pending,
			completeInput,
			status,
			now,
		); err != nil {
			return ProofResourceCompleteResult{}, true, err
		}
	}
	if pending.Result.ReportArtifactRef != "" {
		if in.PrepareOwnedReport == nil {
			return ProofResourceCompleteResult{}, true, fmt.Errorf("retained proof completion requires owned report publication before release")
		}
		if err := in.PrepareOwnedReport(pending.Result); err != nil {
			return ProofResourceCompleteResult{}, true, fmt.Errorf("prepare retained owned proof report: %w", err)
		}
	}
	result, err = s.CompleteProofResource(workID, completeInput, now)
	return result, true, err
}

func (s *Store) deriveProofResourceCompletionExpectation(
	workID string,
	reservation ProofResourceReservation,
	in ProofResourceCompleteInput,
) (proofResourceCompletionExpectation, error) {
	inputDigest, err := proofResourceCompletionInputDigest(workID, in)
	if err != nil {
		return proofResourceCompletionExpectation{}, err
	}
	reportStatus := ""
	ownedReport := false
	if in.ReportArtifactRef != "" {
		artifacts, err := home.NewArtifactStore(s.home)
		if err != nil {
			return proofResourceCompletionExpectation{}, err
		}
		body, err := artifacts.Get(in.ReportArtifactRef)
		if err != nil {
			return proofResourceCompletionExpectation{}, fmt.Errorf("proof report artifact: %w", err)
		}
		report, err := decodeOwnedProofReport(body)
		if err != nil {
			return proofResourceCompletionExpectation{}, fmt.Errorf("proof report decode: %w", err)
		}
		if err := validateOwnedProofReportAgainstReservation(report, reservation); err != nil {
			return proofResourceCompletionExpectation{}, fmt.Errorf("proof report profile: %w", err)
		}
		reportStatus = report.Status
		ownedReport = true
	}
	resultID := idFor("pfrs_", workID, reservation.ID, in.ReportArtifactRef, in.FailureReason)
	resultStatus := ProofResourceFailed
	verificationRef := ""
	if reportStatus == "ok" {
		if in.ProducerExitKnown && in.ProducerExitCode == 0 {
			resultStatus = ProofResourceVerified
			verificationRef = "proof-check:" + resultID
		} else {
			resultStatus = ProofResourceInvalid
		}
	}
	if reportStatus == "failed" && in.ProducerExitKnown && in.ProducerExitCode == 0 {
		return proofResourceCompletionExpectation{}, fmt.Errorf("proof report failed but producer exit code was zero")
	}
	terminalStatus := runattempt.StatusFailed
	if resultStatus == ProofResourceVerified {
		terminalStatus = runattempt.StatusSucceeded
	}
	return proofResourceCompletionExpectation{
		InputDigest:     inputDigest,
		ResultStatus:    resultStatus,
		ReportStatus:    reportStatus,
		OwnedReport:     ownedReport,
		VerificationRef: verificationRef,
		TerminalStatus:  terminalStatus,
		TerminalReason:  completionReason(resultStatus, in.FailureReason),
	}, nil
}

func (s *Store) rearmExpiredProofResourceCompletion(
	workID string,
	in ProofResourceResumeInput,
	reservation ProofResourceReservation,
	pending proofResourceCompletionPending,
	completeInput ProofResourceCompleteInput,
	expiredStatus landlease.StatusResult,
	now time.Time,
) error {
	return s.withLifecycleMutation(func() error {
		w, err := s.Get(workID)
		if err != nil {
			return err
		}
		claim, err := s.readLease(workID)
		if err != nil || claim.By != in.Actor {
			return fmt.Errorf("expired proof completion rearm requires work lease held by %q", in.Actor)
		}
		var currentReservation ProofResourceReservation
		if err := s.readEventJSON(reservation.ID, &currentReservation); err != nil {
			return err
		}
		currentPending, present, pendingPath, err := s.readProofResourceCompletionPending(reservation.ID)
		if err != nil {
			return err
		}
		if !present ||
			!reflect.DeepEqual(currentReservation, reservation) ||
			!reflect.DeepEqual(currentPending, pending) {
			return fmt.Errorf("expired proof completion identity changed before rearm")
		}
		expect, err := s.deriveProofResourceCompletionExpectation(workID, currentReservation, completeInput)
		if err != nil {
			return err
		}
		ra, err := s.runAttempts().Get(currentPending.Result.RunAttemptID)
		if err != nil {
			return err
		}
		normalized, migrated, err := s.validateAndNormalizeProofResourceCompletionPending(
			w,
			currentReservation,
			currentPending,
			ra,
			completeInput,
			expect,
		)
		if err != nil {
			return err
		}
		if migrated {
			if err := s.writeProofResourceCompletionPending(pendingPath, normalized); err != nil {
				return fmt.Errorf("seal expired proof completion recovery journal: %w", err)
			}
			currentPending = normalized
		}
		action := s.proofResourceAction(
			w,
			currentReservation.ID,
			in.Actor,
			"completion_rearm",
			"",
			"",
			"",
			"",
			now,
		)
		decision, err := s.evaluateProofResourceAction(action, now)
		if err != nil {
			return err
		}
		if !policy.Allowed(decision) {
			return fmt.Errorf("expired proof completion rearm policy %s", decision.Outcome)
		}
		if err := s.persistProofScarceMutationPreparation(
			&w,
			action,
			decision,
			currentReservation,
			currentPending.Result.RunAttemptID,
			"completion_rearm",
			now,
		); err != nil {
			return err
		}
		rearmed, err := landlease.RearmExpiredExact(s.home, expiredStatus, now)
		if err != nil {
			return fmt.Errorf("rearm exact expired proof completion holder: %w", err)
		}
		proofResourceAfterScarceMutation("completion_rearm")
		refresh := ProofResourceRefresh{
			SchemaVersion:    ProofResourceRefreshSchemaVersion,
			ID:               idFor("pfrh_", currentReservation.ID, currentReservation.RunAttemptID, rearmed.ExpiresAt),
			Kind:             "work.proof_resource.refresh",
			CreatedAt:        now.UTC().Format(time.RFC3339Nano),
			WorkID:           workID,
			Actor:            in.Actor,
			ReservationID:    currentReservation.ID,
			RunAttemptID:     currentReservation.RunAttemptID,
			Holder:           currentReservation.Holder,
			WindowOpenedAt:   currentReservation.AcquiredAt,
			RefreshedAt:      rearmed.AcquiredAt,
			ExpiresAt:        rearmed.ExpiresAt,
			TTLSeconds:       rearmed.TTLSeconds,
			ActionIntentID:   action.ID,
			PolicyDecisionID: decision.ID,
		}
		return s.persistProofRefresh(&w, action, decision, refresh, now)
	})
}

func (s *Store) reserveProofResourceLocked(workID string, in ProofResourceReserveInput, now time.Time) (ProofResourceReserveResult, error) {
	in.Actor = strings.TrimSpace(in.Actor)
	in.Workspace = strings.TrimSpace(in.Workspace)
	in.Command = strings.TrimSpace(in.Command)
	in.StealReason = strings.TrimSpace(in.StealReason)
	in.TerminatedRunAttemptID = strings.TrimSpace(in.TerminatedRunAttemptID)
	in.TerminationEvidenceRef = strings.TrimSpace(in.TerminationEvidenceRef)
	profile, profileErr := NewProofResourceProfile(in.Profile.Kind, in.Profile.Repo, in.Profile.BaseRef, in.Profile.Gates)
	if profileErr == nil && strings.TrimSpace(in.Profile.Digest) != "" && in.Profile.Digest != profile.Digest {
		profileErr = fmt.Errorf("proof resource profile digest %q does not match canonical %q", in.Profile.Digest, profile.Digest)
	}
	in.Profile = profile
	switch {
	case workID == "":
		return ProofResourceReserveResult{}, fmt.Errorf("proof resource work_id is required")
	case in.Actor == "":
		return ProofResourceReserveResult{}, fmt.Errorf("proof resource actor is required")
	case in.Workspace == "":
		return ProofResourceReserveResult{}, fmt.Errorf("proof resource workspace is required")
	case in.Command == "":
		return ProofResourceReserveResult{}, fmt.Errorf("proof resource command is required")
	case profileErr != nil:
		return ProofResourceReserveResult{}, fmt.Errorf("proof resource profile: %w", profileErr)
	case in.Profile.Repo != in.Workspace:
		return ProofResourceReserveResult{}, fmt.Errorf("proof resource profile repo %q does not match workspace %q", in.Profile.Repo, in.Workspace)
	case in.TTL < time.Second:
		return ProofResourceReserveResult{}, fmt.Errorf("proof resource ttl must be at least one second")
	case in.Steal && in.StealReason == "":
		return ProofResourceReserveResult{}, fmt.Errorf("proof resource --steal requires a non-empty reason")
	case !in.Steal && in.StealReason != "":
		return ProofResourceReserveResult{}, fmt.Errorf("proof resource steal reason requires --steal")
	case in.TerminationEvidenceRef != "" && in.TerminatedRunAttemptID == "":
		return ProofResourceReserveResult{}, fmt.Errorf("proof resource termination evidence requires its terminated RunAttempt")
	case in.TerminationEvidenceRef != "" && !in.Steal:
		return ProofResourceReserveResult{}, fmt.Errorf("proof resource termination evidence requires exceptional --steal governance")
	case in.TerminatedRunAttemptID != "" && !runattempt.Valid(in.TerminatedRunAttemptID):
		return ProofResourceReserveResult{}, fmt.Errorf("proof resource terminated RunAttempt %q is invalid", in.TerminatedRunAttemptID)
	}

	w, err := s.recoverPendingClaimBeforeMutation(workID)
	if err != nil {
		return ProofResourceReserveResult{}, err
	}
	if terminalStates[w.State] {
		return ProofResourceReserveResult{}, fmt.Errorf("work %s %s (terminal); cannot reserve proof resource", workID, w.State)
	}
	claim, err := s.readLease(workID)
	if err != nil {
		return ProofResourceReserveResult{}, fmt.Errorf("proof resource requires a claimed work lease: %w", err)
	}
	if claim.By != in.Actor {
		return ProofResourceReserveResult{}, fmt.Errorf("proof resource work %s lease held by %q, actor is %q", workID, claim.By, in.Actor)
	}

	reservationID := idFor("pfr_", workID, in.Actor, ts(now))
	priorHolder := ""
	if in.Steal {
		status, statusErr := landlease.Status(s.home, now)
		if statusErr != nil {
			return ProofResourceReserveResult{}, statusErr
		}
		priorHolder = strings.TrimSpace(status.Holder)
	}
	var preparedTerminationEvidence []byte
	if in.TerminatedRunAttemptID != "" {
		if in.TerminationEvidenceRef == "" {
			var evidenceErr error
			in.TerminationEvidenceRef, preparedTerminationEvidence, evidenceErr = s.prepareTrustedProofTerminationEvidence(
				priorHolder,
				in.TerminatedRunAttemptID,
				in.Actor,
				now,
			)
			if evidenceErr != nil {
				return ProofResourceReserveResult{}, evidenceErr
			}
		}
		if len(preparedTerminationEvidence) == 0 {
			if err := s.validateProofTerminationEvidence(in.TerminationEvidenceRef, in.TerminatedRunAttemptID, priorHolder, now); err != nil {
				return ProofResourceReserveResult{}, err
			}
		}
		blocker, err := s.proofRecoveryOldestNonterminal(priorHolder)
		if err != nil {
			return ProofResourceReserveResult{}, err
		}
		if blocker.RunAttemptID == "" {
			return ProofResourceReserveResult{}, fmt.Errorf(
				"proof resource termination evidence targets %s, but predecessor lineage is already terminal",
				in.TerminatedRunAttemptID,
			)
		}
		if blocker.RunAttemptID != in.TerminatedRunAttemptID {
			return ProofResourceReserveResult{}, fmt.Errorf(
				"proof resource termination evidence targets %s, but oldest nonterminal predecessor is %s",
				in.TerminatedRunAttemptID,
				blocker.RunAttemptID,
			)
		}
	}
	action := s.proofResourceAction(
		w,
		reservationID,
		in.Actor,
		"reserve",
		priorHolder,
		in.StealReason,
		in.TerminatedRunAttemptID,
		in.TerminationEvidenceRef,
		now,
	)
	decision, err := s.evaluateProofResourceAction(action, now)
	if err != nil {
		return ProofResourceReserveResult{}, err
	}
	reservation := ProofResourceReservation{
		SchemaVersion:          ProofResourceReservationSchemaVersion,
		ID:                     reservationID,
		Kind:                   "work.proof_resource.reservation",
		Status:                 ProofResourceAcquired,
		CreatedAt:              now.UTC().Format(time.RFC3339Nano),
		WorkID:                 workID,
		Actor:                  in.Actor,
		Resource:               ProofResourceName,
		Workspace:              in.Workspace,
		Command:                in.Command,
		Profile:                in.Profile,
		PriorHolder:            priorHolder,
		StealReason:            in.StealReason,
		TerminatedRunAttemptID: in.TerminatedRunAttemptID,
		TerminationEvidenceRef: in.TerminationEvidenceRef,
		ActionIntentID:         action.ID,
		PolicyDecisionID:       decision.ID,
	}
	existingRunIDs := append([]string(nil), w.RunAttemptIDs...)
	proofRun, err := s.prepareProofRunAttempt(w, action.ID, in, now)
	if proofRun.ID != "" {
		reservation.RunAttemptID = proofRun.ID
		reservation.Holder = proofResourceHolder(reservation.ID, proofRun.ID, workID, in.Actor)
	}
	if err != nil {
		return s.failProofResourceAcquisition(&w, action, decision, reservation, proofRun, err, now)
	}
	if !policy.Allowed(decision) {
		reservation.Status = ProofResourceRefused
		terminalErr := s.completeProofAttempt(proofRun.ID, runattempt.StatusAbandoned, "proof resource policy refused before execution", now)
		persistErr := s.persistProofReservation(&w, action, decision, reservation, "proof resource policy refused before execution", now)
		result := ProofResourceReserveResult{SchemaVersion: ProofResourceReservationSchemaVersion, Reservation: reservation, Work: w}
		if terminalErr != nil || persistErr != nil {
			return result, errors.Join(fmt.Errorf("proof resource policy %s", decision.Outcome), terminalErr, persistErr)
		}
		return result, fmt.Errorf("proof resource policy %s", decision.Outcome)
	}
	if err := s.persistProofScarceMutationPreparation(
		&w,
		action,
		decision,
		reservation,
		proofRun.ID,
		"reserve",
		now,
	); err != nil {
		return s.failProofResourceAcquisition(&w, action, decision, reservation, proofRun, err, now)
	}
	if len(preparedTerminationEvidence) != 0 {
		if err := s.persistTrustedProofTerminationEvidence(
			in.TerminationEvidenceRef,
			preparedTerminationEvidence,
		); err != nil {
			return s.failProofResourceAcquisition(&w, action, decision, reservation, proofRun, err, now)
		}
	}
	if in.TerminationEvidenceRef != "" {
		reason := fmt.Sprintf(
			"governed termination evidence %s confirms predecessor %s terminated before exceptional proof takeover",
			in.TerminationEvidenceRef,
			in.TerminatedRunAttemptID,
		)
		if err := s.terminalizeGovernedProofPredecessor(in.TerminatedRunAttemptID, reason, now); err != nil {
			return s.failProofResourceAcquisition(&w, action, decision, reservation, proofRun, err, now)
		}
		reservation.TerminationConfirmedAt = now.UTC().Format(time.RFC3339Nano)
		reservation.TerminationConfirmationID = idFor(
			"pfrt_",
			reservation.ID,
			in.TerminatedRunAttemptID,
			in.TerminationEvidenceRef,
			ts(now),
		)
		if err := s.persistProofTerminationConfirmation(&w, reservation, now); err != nil {
			return s.failProofResourceAcquisition(&w, action, decision, reservation, proofRun, err, now)
		}
	}

	var lease landlease.Lease
	var recovered *landlease.Lease
	var executionBarrier *landlease.ExecutionBarrier
	barrierTransferred := false
	defer func() {
		if executionBarrier != nil && !barrierTransferred {
			_ = executionBarrier.Close()
		}
	}()
	prepareExactReservation := func(preparedLease landlease.Lease, preparedRecovered *landlease.Lease) error {
		applyProofResourceLease(&reservation, preparedLease, preparedRecovered)
		return nil
	}
	if in.Steal && priorHolder != "" {
		if in.TerminatedRunAttemptID == "" {
			if _, requestErr := landlease.RequestExecutionTermination(
				s.home,
				priorHolder,
				reservation.Holder,
				in.StealReason,
				now,
			); requestErr != nil {
				return s.failProofResourceAcquisition(&w, action, decision, reservation, proofRun, requestErr, now)
			}
		}
		lease, executionBarrier, err = landlease.StealExpectedWithExecutionBarrierPrepared(
			s.home,
			reservation.Holder,
			priorHolder,
			in.StealReason,
			now,
			func(prepared landlease.Lease) error {
				return prepareExactReservation(prepared, nil)
			},
		)
	} else {
		var outcome landlease.AcquireOutcome
		outcome, executionBarrier, err = proofResourceAcquireWithExecutionBarrierPrepared(
			s.home,
			reservation.Holder,
			in.TTL,
			now,
			func(prepared landlease.AcquireOutcome) error {
				return prepareExactReservation(prepared.Lease, prepared.ExpiredPredecessor)
			},
		)
		lease = outcome.Lease
		recovered = outcome.ExpiredPredecessor
	}
	if err != nil {
		var held *landlease.HeldError
		var executionPending *landlease.ExecutionPendingError
		if errors.As(err, &executionPending) {
			status, statusErr := landlease.Status(s.home, now)
			if statusErr != nil {
				return s.failProofResourceAcquisition(&w, action, decision, reservation, proofRun, errors.Join(err, statusErr), now)
			}
			held = &landlease.HeldError{
				Holder:    status.Holder,
				ExpiresAt: status.ExpiresAt,
				Remaining: status.RemainingSeconds,
			}
		}
		if held == nil && !errors.As(err, &held) {
			return s.failProofResourceAcquisition(&w, action, decision, reservation, proofRun, err, now)
		}
		reservation.Status = ProofResourceRefused
		reservation.ConflictingHolder = held.Holder
		reservation.ExpiresAt = held.ExpiresAt
		if termErr := s.completeProofAttempt(proofRun.ID, runattempt.StatusAbandoned, "proof resource refused before execution", now); termErr != nil {
			return ProofResourceReserveResult{}, termErr
		}
		if persistErr := s.persistProofReservation(&w, action, decision, reservation, "proof resource refused before execution", now); persistErr != nil {
			return ProofResourceReserveResult{}, persistErr
		}
		return ProofResourceReserveResult{SchemaVersion: ProofResourceReservationSchemaVersion, Reservation: reservation, Work: w},
			&ProofResourceHeldError{
				WorkID:        workID,
				ReservationID: reservation.ID,
				Holder:        held.Holder,
				ExpiresAt:     held.ExpiresAt,
				Remaining:     held.Remaining,
			}
	}
	proofResourceAfterScarceMutation("reserve")

	applyProofResourceLease(&reservation, lease, recovered)
	if _, err := s.runAttempts().MarkRunning(proofRun.ID, now); err != nil {
		if executionBarrier != nil {
			_ = executionBarrier.Close()
		}
		_, _ = landlease.Release(s.home, reservation.Holder, now)
		return s.failProofResourceAcquisition(&w, action, decision, reservation, proofRun, err, now)
	}
	if err := s.persistProofReservation(&w, action, decision, reservation, "proof resource acquired before execution", now); err != nil {
		if executionBarrier != nil {
			_ = executionBarrier.Close()
		}
		_, _ = landlease.Release(s.home, reservation.Holder, now)
		return s.failProofResourceAcquisition(&w, action, decision, reservation, proofRun, err, now)
	}
	s.supersedeClaimReservations(existingRunIDs, []string{proofRun.ID}, now)
	barrierTransferred = true
	return ProofResourceReserveResult{
		ExecutionBarrier: executionBarrier,
		SchemaVersion:    ProofResourceReservationSchemaVersion,
		Reservation:      reservation,
		Work:             w,
	}, nil
}

func applyProofResourceLease(
	reservation *ProofResourceReservation,
	lease landlease.Lease,
	recovered *landlease.Lease,
) {
	reservation.Holder = lease.Holder
	reservation.AcquiredAt = lease.AcquiredAt
	reservation.ExpiresAt = lease.ExpiresAt
	reservation.TTLSeconds = lease.TTLSeconds
	reservation.PriorHolder = lease.PriorHolder
	reservation.StealReason = lease.StealReason
	reservation.StolenAt = lease.StolenAt
	if recovered != nil {
		reservation.RecoveredHolder = recovered.Holder
		reservation.RecoveredExpiresAt = recovered.ExpiresAt
		_, recoveredRun, _, ok := parseProofResourceHolder(recovered.Holder)
		if ok {
			reservation.RecoveredRunAttemptID = recoveredRun
		}
	}
	if lease.PriorHolder != "" {
		_, priorRun, _, ok := parseProofResourceHolder(lease.PriorHolder)
		if ok {
			reservation.PriorRunAttemptID = priorRun
		}
	}
}

func (s *Store) prepareProofRunAttempt(w envelope.WorkEnvelope, actionID string, in ProofResourceReserveInput, now time.Time) (runattempt.RunAttempt, error) {
	store := s.runAttempts()
	ra, err := store.Mint(w.ID, in.Actor, proofResourceHarness, now)
	if err != nil {
		return runattempt.RunAttempt{}, fmt.Errorf("proof resource mint RunAttempt: %w", err)
	}
	mintedID := ra.ID
	fail := func(step string, cause error) (runattempt.RunAttempt, error) {
		terminalErr := s.completeProofAttempt(mintedID, runattempt.StatusFailed, "proof resource "+step+" failed: "+cause.Error(), now)
		failedRun, getErr := store.Get(mintedID)
		return failedRun, errors.Join(fmt.Errorf("proof resource %s: %w", step, cause), terminalErr, getErr)
	}
	if ra, err = store.SetOrigin(ra.ID, runattempt.OriginHarnessExecution); err != nil {
		return fail("stamp RunAttempt origin", err)
	}
	if w.IDs.ContextPackID != "" {
		if ra, err = store.SetContextPack(ra.ID, w.IDs.ContextPackID); err != nil {
			return fail("bind RunAttempt context", err)
		}
	}
	if ra, err = store.SetWorkspace(ra.ID, in.Workspace); err != nil {
		return fail("bind RunAttempt workspace", err)
	}
	if ra, err = store.SetCommand(ra.ID, in.Command); err != nil {
		return fail("bind RunAttempt command", err)
	}
	if err = store.AttachActionIntent(ra.ID, actionID); err != nil {
		return fail("bind RunAttempt action", err)
	}
	if len(w.RunAttemptIDs) > 0 {
		if ra, err = store.SetPredecessor(ra.ID, w.RunAttemptIDs[len(w.RunAttemptIDs)-1]); err != nil {
			return fail("bind RunAttempt predecessor", err)
		}
	}
	return ra, nil
}

func (s *Store) failProofResourceAcquisition(
	w *envelope.WorkEnvelope,
	action envelope.ActionIntent,
	decision envelope.PolicyDecision,
	reservation ProofResourceReservation,
	proofRun runattempt.RunAttempt,
	cause error,
	now time.Time,
) (ProofResourceReserveResult, error) {
	if proofRun.ID == "" {
		return ProofResourceReserveResult{}, cause
	}
	reservation.Status = ProofResourceFailed
	reservation.RunAttemptID = proofRun.ID
	if reservation.Holder == "" {
		reservation.Holder = proofResourceHolder(reservation.ID, proofRun.ID, reservation.WorkID, reservation.Actor)
	}
	reservation.FailureReason = strings.TrimSpace(cause.Error())
	terminalErr := s.completeProofAttempt(
		proofRun.ID,
		runattempt.StatusFailed,
		"proof resource acquisition failed before execution: "+reservation.FailureReason,
		now,
	)
	reservationExists, existenceErr := s.events.Exists(reservation.ID)
	var persistErr error
	if existenceErr == nil && reservationExists {
		var stored ProofResourceReservation
		if err := s.readEventJSON(reservation.ID, &stored); err != nil {
			persistErr = err
		} else if stored.WorkID != reservation.WorkID ||
			stored.Actor != reservation.Actor ||
			stored.RunAttemptID != reservation.RunAttemptID {
			persistErr = fmt.Errorf("persisted proof reservation %s conflicts with failed acquisition", reservation.ID)
		} else {
			persistErr = s.persistProofAcquisitionFailureReachability(w, action, decision, stored, now)
		}
	} else if existenceErr == nil {
		persistErr = s.persistProofReservation(
			w,
			action,
			decision,
			reservation,
			"proof resource acquisition failed before execution",
			now,
		)
	}
	result := ProofResourceReserveResult{
		SchemaVersion: ProofResourceReservationSchemaVersion,
		Reservation:   reservation,
		Work:          *w,
	}
	if terminalErr != nil || existenceErr != nil || persistErr != nil {
		return result, errors.Join(cause, terminalErr, existenceErr, persistErr)
	}
	return result, cause
}

// RefreshProofResource extends an already-governed reservation only while its work lease and exact
// running RunAttempt remain live. The landlease refresh itself refuses resurrection after expiry.
func (s *Store) RefreshProofResource(workID, reservationID, actor string, ttl time.Duration, now time.Time) (ProofResourceReservation, error) {
	var reservation ProofResourceReservation
	err := s.withLifecycleMutation(func() error {
		workID = strings.TrimSpace(workID)
		reservationID = strings.TrimSpace(reservationID)
		actor = strings.TrimSpace(actor)
		if ttl < time.Second {
			return fmt.Errorf("proof resource ttl must be at least one second")
		}
		w, err := s.recoverPendingClaimBeforeMutation(workID)
		if err != nil {
			return err
		}
		if terminalStates[w.State] {
			return fmt.Errorf("work %s is terminal", workID)
		}
		claim, err := s.readLease(workID)
		if err != nil || claim.By != actor {
			return fmt.Errorf("proof refresh requires work lease held by %q", actor)
		}
		if err := s.readEventJSON(reservationID, &reservation); err != nil {
			return err
		}
		ra, err := s.activeProofRunAttempt(reservation, workID, actor)
		if err != nil {
			return err
		}
		action := s.proofResourceAction(w, reservation.ID, actor, "refresh", "", "", "", "", now)
		decision, err := s.evaluateProofResourceAction(action, now)
		if err != nil {
			return err
		}
		if !policy.Allowed(decision) {
			return fmt.Errorf("proof refresh policy %s", decision.Outcome)
		}
		if err := s.persistProofScarceMutationPreparation(
			&w,
			action,
			decision,
			reservation,
			ra.ID,
			"refresh",
			now,
		); err != nil {
			return err
		}
		windowOpenedAt := reservation.AcquiredAt
		lease, err := landlease.Refresh(s.home, reservation.Holder, ttl, now)
		if err != nil {
			return err
		}
		proofResourceAfterScarceMutation("refresh")
		if _, err := s.runAttempts().Heartbeat(ra.ID, now); err != nil {
			return err
		}
		reservation.ExpiresAt = lease.ExpiresAt
		reservation.TTLSeconds = lease.TTLSeconds
		refresh := ProofResourceRefresh{
			SchemaVersion:    ProofResourceRefreshSchemaVersion,
			ID:               idFor("pfrh_", reservation.ID, ra.ID, lease.ExpiresAt),
			Kind:             "work.proof_resource.refresh",
			CreatedAt:        now.UTC().Format(time.RFC3339Nano),
			WorkID:           workID,
			Actor:            actor,
			ReservationID:    reservation.ID,
			RunAttemptID:     ra.ID,
			Holder:           reservation.Holder,
			WindowOpenedAt:   windowOpenedAt,
			RefreshedAt:      lease.AcquiredAt,
			ExpiresAt:        lease.ExpiresAt,
			TTLSeconds:       lease.TTLSeconds,
			ActionIntentID:   action.ID,
			PolicyDecisionID: decision.ID,
		}
		return s.persistProofRefresh(&w, action, decision, refresh, now)
	})
	return reservation, err
}

// CompleteProofResource persists the governed result/work/event while the exact LIVE holder is
// still exclusive, terminalizes the proof RunAttempt, and only then releases. An expired, stolen,
// malformed, fabricated, piped, or exit-ambiguous result cannot enter VerificationIDs.
func (s *Store) CompleteProofResource(workID string, in ProofResourceCompleteInput, now time.Time) (ProofResourceCompleteResult, error) {
	var result ProofResourceCompleteResult
	err := s.withLifecycleMutation(func() error {
		var err error
		workID = strings.TrimSpace(workID)
		result, err = s.completeProofResourceLocked(workID, in, now)
		if err == nil {
			return nil
		}
		return errors.Join(err, s.terminalizeProofCompletionErrorLocked(workID, in, err, now))
	})
	return result, err
}

func (s *Store) terminalizeProofCompletionErrorLocked(
	workID string,
	in ProofResourceCompleteInput,
	cause error,
	now time.Time,
) error {
	if workID == "" || strings.TrimSpace(in.Actor) == "" || strings.TrimSpace(in.ReservationID) == "" {
		return nil
	}
	var reservation ProofResourceReservation
	if err := s.readEventJSON(strings.TrimSpace(in.ReservationID), &reservation); err != nil {
		return nil
	}
	if reservation.WorkID != workID || reservation.Actor != strings.TrimSpace(in.Actor) {
		return nil
	}
	runID := reservation.RunAttemptID
	if pending, present, _, err := s.readProofResourceCompletionPending(reservation.ID); err == nil && present && pending.Result.RunAttemptID != "" {
		runID = pending.Result.RunAttemptID
	}
	if runID == "" {
		return nil
	}
	ra, err := s.runAttempts().Get(runID)
	if err != nil || runattempt.IsTerminal(ra.Status) {
		return err
	}
	reason := "proof completion returned before authoritative commit: " + strings.TrimSpace(cause.Error())
	if err := s.completeProofAttempt(runID, runattempt.StatusFailed, reason, now); err != nil {
		return err
	}
	state := ""
	if w, err := s.Get(workID); err == nil {
		state = w.State
	}
	return s.appendEvent(Event{
		TS:       ts(now),
		WorkID:   workID,
		From:     state,
		To:       state,
		By:       reservation.Actor,
		Note:     reason,
		Attached: []string{reservation.ID, runID},
	})
}

func (s *Store) completeProofResourceLocked(workID string, in ProofResourceCompleteInput, now time.Time) (ProofResourceCompleteResult, error) {
	in.Actor = strings.TrimSpace(in.Actor)
	in.ReservationID = strings.TrimSpace(in.ReservationID)
	in.ReportArtifactRef = strings.TrimSpace(in.ReportArtifactRef)
	in.FailureReason = strings.TrimSpace(in.FailureReason)
	switch {
	case workID == "" || in.Actor == "" || in.ReservationID == "":
		return ProofResourceCompleteResult{}, fmt.Errorf("proof completion requires work_id, actor, and reservation_id")
	case in.ReportArtifactRef == "" && in.FailureReason == "":
		return ProofResourceCompleteResult{}, fmt.Errorf("proof completion requires report artifact or failure reason")
	case in.ReportArtifactRef != "" && in.FailureReason != "":
		return ProofResourceCompleteResult{}, fmt.Errorf("proof completion report artifact and failure reason are mutually exclusive")
	}
	w, err := s.recoverPendingClaimBeforeMutation(workID)
	if err != nil {
		return ProofResourceCompleteResult{}, err
	}
	claim, err := s.readLease(workID)
	if err != nil || claim.By != in.Actor {
		return ProofResourceCompleteResult{}, fmt.Errorf("proof completion requires work lease held by %q", in.Actor)
	}
	var reservation ProofResourceReservation
	if err := s.readEventJSON(in.ReservationID, &reservation); err != nil {
		return ProofResourceCompleteResult{}, err
	}
	reservation, err = s.proofReservationWithLatestRefresh(reservation)
	if err != nil {
		return ProofResourceCompleteResult{}, err
	}
	leaseStatus, err := landlease.Status(s.home, now)
	if err != nil {
		return ProofResourceCompleteResult{}, err
	}
	if !leaseStatus.Held || leaseStatus.Holder != reservation.Holder {
		if leaseStatus.Holder == reservation.Holder && leaseStatus.IsExpired {
			return ProofResourceCompleteResult{}, fmt.Errorf("proof reservation %s expired at %s", reservation.ID, leaseStatus.ExpiresAt)
		}
		return ProofResourceCompleteResult{}, fmt.Errorf("proof reservation %s no longer holds exclusive resource; current holder %q", reservation.ID, leaseStatus.Holder)
	}

	expect, err := s.deriveProofResourceCompletionExpectation(workID, reservation, in)
	if err != nil {
		return ProofResourceCompleteResult{}, err
	}
	resultID := idFor("pfrs_", workID, reservation.ID, in.ReportArtifactRef, in.FailureReason)
	status := expect.ResultStatus
	reportStatus := expect.ReportStatus
	ownedReport := expect.OwnedReport
	verificationRef := expect.VerificationRef
	terminalStatus := expect.TerminalStatus
	inputDigest := expect.InputDigest
	pending, pendingPresent, pendingPath, err := s.readProofResourceCompletionPending(reservation.ID)
	if err != nil {
		return ProofResourceCompleteResult{}, err
	}
	var ra runattempt.RunAttempt
	if pendingPresent {
		if pending.WorkID != workID || pending.ReservationID != reservation.ID || pending.InputDigest != inputDigest {
			return ProofResourceCompleteResult{}, fmt.Errorf("proof completion pending transaction conflicts with requested input")
		}
		ra, pending, err = s.proofRunAttemptForCompletionRetry(
			&w,
			reservation,
			pending,
			pendingPath,
			in,
			expect,
			now,
		)
		if err != nil {
			return ProofResourceCompleteResult{}, err
		}
	} else {
		ra, err = s.activeProofRunAttempt(reservation, workID, in.Actor)
		if err != nil {
			return ProofResourceCompleteResult{}, err
		}
		action, decision, governanceSeal, err := s.proofResourceCompletionGovernance(
			w,
			reservation,
			in.Actor,
			now,
		)
		if err != nil {
			return ProofResourceCompleteResult{}, err
		}
		if !policy.Allowed(decision) {
			return ProofResourceCompleteResult{}, fmt.Errorf("proof completion policy %s", decision.Outcome)
		}
		completionID := idFor("pfrc_", workID, reservation.ID, resultID)
		proofResult := ProofResourceResult{
			SchemaVersion:     ProofResourceResultSchemaVersion,
			ID:                resultID,
			Kind:              "work.proof_resource.result",
			Status:            status,
			CreatedAt:         now.UTC().Format(time.RFC3339Nano),
			WorkID:            workID,
			Actor:             in.Actor,
			Resource:          ProofResourceName,
			ReservationID:     reservation.ID,
			RunAttemptID:      ra.ID,
			RunAttemptStatus:  terminalStatus,
			ReportArtifactRef: in.ReportArtifactRef,
			ReportStatus:      reportStatus,
			OwnedReport:       ownedReport,
			ProducerExitKnown: in.ProducerExitKnown,
			ProducerExitCode:  in.ProducerExitCode,
			FailureReason:     in.FailureReason,
			VerificationRef:   verificationRef,
			CompletionID:      completionID,
			ActionIntentID:    action.ID,
			PolicyDecisionID:  decision.ID,
		}
		completion := ProofResourceCompletion{
			SchemaVersion:    ProofResourceCompletionSchemaVersion,
			ID:               completionID,
			Kind:             "work.proof_resource.completion",
			CreatedAt:        now.UTC().Format(time.RFC3339Nano),
			WorkID:           workID,
			Actor:            in.Actor,
			ReservationID:    reservation.ID,
			ResultID:         proofResult.ID,
			RunAttemptID:     ra.ID,
			RunAttemptStatus: terminalStatus,
			VerificationRef:  verificationRef,
			ActionIntentID:   action.ID,
			PolicyDecisionID: decision.ID,
		}
		proofRefs := []string{reservation.ID, proofResult.ID, completion.ID}
		if in.ReportArtifactRef != "" {
			proofRefs = append(proofRefs, in.ReportArtifactRef)
		}
		if verificationRef != "" {
			proofRefs = append(proofRefs, verificationRef)
		}
		pending = proofResourceCompletionPending{
			SchemaVersion:   proofResourceCompletionPendingSchemaVersion,
			WorkID:          workID,
			ReservationID:   reservation.ID,
			InputDigest:     inputDigest,
			GovernanceSeal:  governanceSeal,
			CreatedAt:       now.UTC().Format(time.RFC3339Nano),
			Action:          action,
			Decision:        decision,
			Result:          proofResult,
			Completion:      completion,
			VerificationRef: verificationRef,
			TerminalStatus:  terminalStatus,
			TerminalReason:  completionReason(status, in.FailureReason),
			ProofRefs:       proofRefs,
			LifecycleEvent: Event{
				TS:       ts(now),
				WorkID:   workID,
				From:     w.State,
				To:       w.State,
				By:       in.Actor,
				Note:     "proof resource finalizer committed; verification publishes after terminal RunAttempt",
				Attached: append([]string{action.ID, decision.ID, reservation.ID, proofResult.ID, completion.ID, ra.ID}, proofRefs[3:]...),
			},
		}
		if err := s.writeProofResourceCompletionPending(pendingPath, pending); err != nil {
			return ProofResourceCompleteResult{}, err
		}
	}

	finalWork := w
	var terminal runattempt.RunAttempt
	released, releaseErr := landlease.ReleaseAfter(s.home, reservation.Holder, now, func() error {
		if _, err := landlease.AttestExecutionTerminated(s.home, reservation.Holder); err != nil {
			return fmt.Errorf("attest proof execution terminated: %w", err)
		}
		if err := s.persistPreparedProofCompletion(&finalWork, pending); err != nil {
			return err
		}
		var err error
		terminal, err = s.terminalizePreparedProofCompletion(ra, pending, now)
		if err != nil {
			return err
		}
		if err := proofResourceWriteEventRecord(s, "completion", pending.Completion.ID, &pending.Completion); err != nil {
			return err
		}
		if pending.VerificationRef != "" && !oneOf(pending.VerificationRef, finalWork.VerificationIDs) {
			finalWork.VerificationIDs = append(finalWork.VerificationIDs, pending.VerificationRef)
		}
		if err := proofResourceWriteWorkRecord(s, "work_final", &finalWork); err != nil {
			return err
		}
		return nil
	})
	if releaseErr != nil {
		committedWork, committed := s.visibleProofCompletionCommit(workID, pending, terminal)
		if !committed {
			return ProofResourceCompleteResult{}, fmt.Errorf("proof completion retained exclusive holder: %w", releaseErr)
		}
		finalWork = committedWork
		finalResult := pending.Result
		finalResult.RunAttemptStatus = terminal.Status
		finalResult.VerificationRef = pending.VerificationRef
		return ProofResourceCompleteResult{
				SchemaVersion:           ProofResourceResultSchemaVersion,
				Result:                  finalResult,
				VerificationRef:         pending.VerificationRef,
				ResourceReleased:        false,
				RecoveryJournalRetained: true,
				ReleaseWarning:          releaseErr.Error(),
				Work:                    finalWork,
			}, &ProofResourceReleasePendingError{
				ReservationID: reservation.ID,
				Holder:        reservation.Holder,
				Cause:         releaseErr,
			}
	}
	cleanupErr := s.clearProofResourceCompletionPending(pendingPath, pending)
	finalResult := pending.Result
	finalResult.RunAttemptStatus = terminal.Status
	finalResult.VerificationRef = pending.VerificationRef
	result := ProofResourceCompleteResult{
		SchemaVersion:    ProofResourceResultSchemaVersion,
		Result:           finalResult,
		VerificationRef:  pending.VerificationRef,
		ResourceReleased: released.Freed,
		Work:             finalWork,
	}
	if cleanupErr != nil {
		result.RecoveryJournalRetained = true
		result.ReleaseWarning = "proof completion committed; recovery journal cleanup deferred: " + cleanupErr.Error()
	}
	return result, nil
}

func (s *Store) visibleProofCompletionCommit(
	workID string,
	pending proofResourceCompletionPending,
	terminal runattempt.RunAttempt,
) (envelope.WorkEnvelope, bool) {
	if terminal.ID == "" || terminal.Status != pending.TerminalStatus {
		return envelope.WorkEnvelope{}, false
	}
	w, err := s.Get(workID)
	if err != nil ||
		!oneOf(pending.Action.ID, w.ActionIntentIDs) ||
		w.PolicyDecisionID != pending.Decision.ID {
		return envelope.WorkEnvelope{}, false
	}
	var result ProofResourceResult
	if err := s.readEventJSON(pending.Result.ID, &result); err != nil ||
		result.WorkID != workID ||
		result.ReservationID != pending.ReservationID ||
		result.RunAttemptID != terminal.ID ||
		result.RunAttemptStatus != pending.TerminalStatus ||
		result.VerificationRef != pending.VerificationRef ||
		result.CompletionID != pending.Completion.ID ||
		result.ActionIntentID != pending.Action.ID ||
		result.PolicyDecisionID != pending.Decision.ID ||
		(result.Status == ProofResourceVerified) != (pending.VerificationRef != "") {
		return envelope.WorkEnvelope{}, false
	}
	var completion ProofResourceCompletion
	if err := s.readEventJSON(pending.Completion.ID, &completion); err != nil ||
		completion.WorkID != workID ||
		completion.ReservationID != pending.ReservationID ||
		completion.ResultID != pending.Result.ID ||
		completion.VerificationRef != pending.VerificationRef ||
		completion.RunAttemptID != terminal.ID ||
		completion.RunAttemptStatus != pending.TerminalStatus ||
		completion.ActionIntentID != pending.Action.ID ||
		completion.PolicyDecisionID != pending.Decision.ID {
		return envelope.WorkEnvelope{}, false
	}
	if pending.VerificationRef != "" && !oneOf(pending.VerificationRef, w.VerificationIDs) {
		return envelope.WorkEnvelope{}, false
	}
	return w, true
}

func proofResourceCompletionInputDigest(workID string, in ProofResourceCompleteInput) (string, error) {
	body, err := json.Marshal(struct {
		WorkID string                     `json:"work_id"`
		Input  ProofResourceCompleteInput `json:"input"`
	}{WorkID: workID, Input: in})
	if err != nil {
		return "", fmt.Errorf("marshal proof completion input: %w", err)
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func proofResourceCompletionGovernanceSeal(
	action envelope.ActionIntent,
	decision envelope.PolicyDecision,
) (string, error) {
	body, err := json.Marshal(struct {
		Action   envelope.ActionIntent   `json:"action"`
		Decision envelope.PolicyDecision `json:"decision"`
	}{Action: action, Decision: decision})
	if err != nil {
		return "", fmt.Errorf("marshal proof completion governance: %w", err)
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Store) proofResourceCompletionGovernance(
	w envelope.WorkEnvelope,
	reservation ProofResourceReservation,
	actor string,
	now time.Time,
) (envelope.ActionIntent, envelope.PolicyDecision, string, error) {
	action := s.proofResourceAction(w, reservation.ID, actor, "complete", "", "", "", "", now)
	decision, err := s.evaluateProofResourceAction(action, now)
	if err != nil {
		return envelope.ActionIntent{}, envelope.PolicyDecision{}, "", err
	}
	seal, err := proofResourceCompletionGovernanceSeal(action, decision)
	if err != nil {
		return envelope.ActionIntent{}, envelope.PolicyDecision{}, "", err
	}
	return action, decision, seal, nil
}

func (s *Store) proofResourceCompletionPendingPath(reservationID string) (string, error) {
	if filepath.Base(reservationID) != reservationID || !strings.HasPrefix(reservationID, "pfr_") {
		return "", fmt.Errorf("invalid proof reservation id %q", reservationID)
	}
	return filepath.Join(s.home, "work", "proof-resource-completions", reservationID+".json"), nil
}

func (s *Store) readProofResourceCompletionPending(reservationID string) (proofResourceCompletionPending, bool, string, error) {
	var pending proofResourceCompletionPending
	path, err := s.proofResourceCompletionPendingPath(reservationID)
	if err != nil {
		return pending, false, "", err
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return pending, false, path, nil
	}
	if err != nil {
		return pending, false, path, err
	}
	if err := json.Unmarshal(body, &pending); err != nil {
		return pending, false, path, fmt.Errorf("proof completion pending decode: %w", err)
	}
	if pending.SchemaVersion != proofResourceCompletionPendingSchemaVersion ||
		pending.ReservationID != reservationID ||
		pending.WorkID == "" ||
		pending.InputDigest == "" ||
		pending.Action.ID == "" ||
		pending.Decision.ID == "" ||
		pending.Result.ID == "" ||
		pending.Completion.ID == "" ||
		pending.TerminalStatus == "" ||
		pending.LifecycleEvent.WorkID != pending.WorkID {
		return pending, false, path, fmt.Errorf("proof completion pending transaction is incomplete")
	}
	return pending, true, path, nil
}

func (s *Store) writeProofResourceCompletionPending(path string, pending proofResourceCompletionPending) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return s.writeJSON(path, &pending)
}

func (s *Store) clearProofResourceCompletionPending(path string, expected proofResourceCompletionPending) error {
	actual, present, actualPath, err := s.readProofResourceCompletionPending(expected.ReservationID)
	if err != nil {
		return err
	}
	if !present || actualPath != path || !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("proof completion pending transaction changed before commit")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncReceiptedActionDir(filepath.Dir(path))
}

func (s *Store) validateAndNormalizeProofResourceCompletionPending(
	w envelope.WorkEnvelope,
	reservation ProofResourceReservation,
	pending proofResourceCompletionPending,
	ra runattempt.RunAttempt,
	in ProofResourceCompleteInput,
	expect proofResourceCompletionExpectation,
) (proofResourceCompletionPending, bool, error) {
	fail := func(reason string) (proofResourceCompletionPending, bool, error) {
		return pending, false, fmt.Errorf("proof completion pending identity is corrupt: %s", reason)
	}
	workID := w.ID
	if pending.SchemaVersion != proofResourceCompletionPendingSchemaVersion ||
		pending.WorkID != workID ||
		pending.ReservationID != reservation.ID ||
		pending.InputDigest != expect.InputDigest {
		return fail("work, reservation, input, or schema binding changed")
	}
	if reservation.WorkID != workID ||
		reservation.Actor != in.Actor ||
		reservation.Status != ProofResourceAcquired {
		return fail("current reservation is not the acquired same-work actor binding")
	}
	if err := pending.Action.Validate(); err != nil {
		return fail("completion ActionIntent is invalid: " + err.Error())
	}
	if err := pending.Decision.Validate(); err != nil {
		return fail("completion PolicyDecision is invalid: " + err.Error())
	}
	seal, err := proofResourceCompletionGovernanceSeal(pending.Action, pending.Decision)
	if err != nil {
		return fail("completion governance seal cannot be computed: " + err.Error())
	}
	normalized := pending
	governanceMigrated := false
	governanceCreatedAt := pending.CreatedAt
	if ra.ID != reservation.RunAttemptID {
		originalAttempt, err := s.runAttempts().Get(reservation.RunAttemptID)
		if err != nil {
			return fail("completion governance original RunAttempt cannot be read: " + err.Error())
		}
		if originalAttempt.CompletedAt.IsZero() {
			return fail("completion governance original RunAttempt has no completion time")
		}
		governanceCreatedAt = originalAttempt.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, governanceCreatedAt)
	if err != nil {
		return fail("completion governance creation time is invalid: " + err.Error())
	}
	expectedAction := s.proofResourceAction(w, reservation.ID, in.Actor, "complete", "", "", "", "", createdAt)
	if !reflect.DeepEqual(pending.Action, expectedAction) {
		return fail("completion ActionIntent differs from canonical construction")
	}
	if pending.GovernanceSeal == "" {
		expectedAction, expectedDecision, expectedSeal, err := s.proofResourceCompletionGovernance(
			w,
			reservation,
			in.Actor,
			createdAt,
		)
		if err != nil {
			return fail("legacy completion governance cannot be reconstructed: " + err.Error())
		}
		if !reflect.DeepEqual(pending.Action, expectedAction) ||
			!reflect.DeepEqual(pending.Decision, expectedDecision) ||
			seal != expectedSeal {
			return fail("legacy completion governance differs from canonical reconstruction")
		}
		normalized.GovernanceSeal = expectedSeal
		governanceMigrated = true
	} else if pending.GovernanceSeal != seal {
		return fail("completion governance seal does not match the complete ActionIntent and PolicyDecision")
	}
	if !policy.Allowed(pending.Decision) {
		return fail("completion PolicyDecision is not allowing")
	}
	if ra.WorkID != workID ||
		ra.Actor != in.Actor ||
		ra.Harness != proofResourceHarness ||
		ra.Origin != runattempt.OriginHarnessExecution ||
		(ra.ActionIntentID != reservation.ActionIntentID && ra.ActionIntentID != pending.Action.ID) ||
		ra.Workspace != reservation.Workspace ||
		ra.Command != reservation.Command {
		return fail("pending RunAttempt is not the reserved same-work execution")
	}

	expectedResultID := idFor(
		"pfrs_",
		workID,
		reservation.ID,
		in.ReportArtifactRef,
		in.FailureReason,
	)
	retry := ra.ID != reservation.RunAttemptID
	if retry {
		expectedResultID = idFor(
			"pfrs_",
			workID,
			reservation.ID,
			in.ReportArtifactRef,
			in.FailureReason,
			ra.ID,
		)
		if ra.PredecessorID == "" {
			return fail("retry RunAttempt has no predecessor")
		}
	}
	expectedVerificationRef := expect.VerificationRef
	if retry && expectedVerificationRef != "" {
		expectedVerificationRef = "proof-check:" + expectedResultID
	}
	expectedCompletionID := idFor("pfrc_", workID, reservation.ID, expectedResultID)
	if pending.Result.SchemaVersion != ProofResourceResultSchemaVersion ||
		pending.Result.ID != expectedResultID ||
		pending.Result.Kind != "work.proof_resource.result" ||
		pending.Result.WorkID != workID ||
		pending.Result.Actor != in.Actor ||
		pending.Result.Resource != ProofResourceName ||
		pending.Result.ReservationID != reservation.ID ||
		pending.Result.RunAttemptID != ra.ID ||
		pending.Result.ReportArtifactRef != in.ReportArtifactRef ||
		pending.Result.ReportStatus != expect.ReportStatus ||
		pending.Result.OwnedReport != expect.OwnedReport ||
		pending.Result.ProducerExitKnown != in.ProducerExitKnown ||
		pending.Result.ProducerExitCode != in.ProducerExitCode ||
		pending.Result.FailureReason != in.FailureReason ||
		pending.Result.Status != expect.ResultStatus ||
		pending.Result.CompletionID != expectedCompletionID ||
		pending.Result.ActionIntentID != pending.Action.ID ||
		pending.Result.PolicyDecisionID != pending.Decision.ID {
		return fail("ProofResourceResult immutable fields or cross-object references changed")
	}
	if pending.TerminalStatus != expect.TerminalStatus ||
		pending.TerminalReason != expect.TerminalReason ||
		pending.VerificationRef != expectedVerificationRef ||
		(pending.Result.Status == ProofResourceVerified) != (pending.VerificationRef != "") ||
		(pending.TerminalStatus == runattempt.StatusSucceeded) != (pending.VerificationRef != "") {
		return fail("terminal status, reason, result status, or verification reference is inconsistent")
	}
	if pending.Completion.SchemaVersion != ProofResourceCompletionSchemaVersion ||
		pending.Completion.ID != expectedCompletionID ||
		pending.Completion.Kind != "work.proof_resource.completion" ||
		pending.Completion.WorkID != workID ||
		pending.Completion.Actor != in.Actor ||
		pending.Completion.ReservationID != reservation.ID ||
		pending.Completion.ResultID != pending.Result.ID ||
		pending.Completion.RunAttemptID != ra.ID ||
		pending.Completion.RunAttemptStatus != pending.TerminalStatus ||
		pending.Completion.VerificationRef != pending.VerificationRef ||
		pending.Completion.ActionIntentID != pending.Action.ID ||
		pending.Completion.PolicyDecisionID != pending.Decision.ID {
		return fail("ProofResourceCompletion immutable fields or cross-object references changed")
	}
	if pending.CreatedAt == "" ||
		pending.Result.CreatedAt != pending.CreatedAt ||
		pending.Completion.CreatedAt != pending.CreatedAt {
		return fail("pending, result, and completion creation identity changed")
	}

	expectedProofRefs := []string{reservation.ID, pending.Result.ID, pending.Completion.ID}
	if in.ReportArtifactRef != "" {
		expectedProofRefs = append(expectedProofRefs, in.ReportArtifactRef)
	}
	if pending.VerificationRef != "" {
		expectedProofRefs = append(expectedProofRefs, pending.VerificationRef)
	}
	if !reflect.DeepEqual(pending.ProofRefs, expectedProofRefs) {
		return fail("proof references changed")
	}
	expectedNote := "proof resource finalizer committed; verification publishes after terminal RunAttempt"
	expectedAttached := append(
		[]string{
			pending.Action.ID,
			pending.Decision.ID,
			reservation.ID,
			pending.Result.ID,
			pending.Completion.ID,
			ra.ID,
		},
		expectedProofRefs[3:]...,
	)
	if retry {
		expectedNote = "proof resource finalizer retry committed; verification publishes after terminal RunAttempt"
		expectedAttached = append(
			[]string{
				pending.Action.ID,
				pending.Decision.ID,
				reservation.ID,
				pending.Result.ID,
				pending.Completion.ID,
				ra.PredecessorID,
				ra.ID,
			},
			expectedProofRefs[3:]...,
		)
	}
	if pending.LifecycleEvent.TS == "" ||
		pending.LifecycleEvent.WorkID != workID ||
		pending.LifecycleEvent.From != w.State ||
		pending.LifecycleEvent.To != w.State ||
		pending.LifecycleEvent.By != in.Actor ||
		pending.LifecycleEvent.Note != expectedNote ||
		!reflect.DeepEqual(pending.LifecycleEvent.Attached, expectedAttached) {
		return fail("lifecycle event identity or attachments changed")
	}

	exactResultProjection := pending.Result.RunAttemptStatus == pending.TerminalStatus &&
		pending.Result.VerificationRef == pending.VerificationRef
	legacyResultProjection := (pending.Result.RunAttemptStatus == runattempt.StatusPending ||
		pending.Result.RunAttemptStatus == runattempt.StatusRunning) &&
		pending.Result.VerificationRef == ""
	switch {
	case exactResultProjection:
		return normalized, governanceMigrated, nil
	case legacyResultProjection:
		normalized.Result.RunAttemptStatus = normalized.TerminalStatus
		normalized.Result.VerificationRef = normalized.VerificationRef
		return normalized, true, nil
	default:
		return fail("ProofResourceResult terminal projection is neither exact nor a known v0.1 pending-or-running/empty shape")
	}
}

func (s *Store) proofRunAttemptForCompletionRetry(
	w *envelope.WorkEnvelope,
	reservation ProofResourceReservation,
	pending proofResourceCompletionPending,
	pendingPath string,
	in ProofResourceCompleteInput,
	expect proofResourceCompletionExpectation,
	now time.Time,
) (runattempt.RunAttempt, proofResourceCompletionPending, error) {
	workID := w.ID
	actor := in.Actor
	ra, err := s.runAttempts().Get(pending.Result.RunAttemptID)
	if err != nil {
		return runattempt.RunAttempt{}, pending, err
	}
	normalizedPending, migrated, err := s.validateAndNormalizeProofResourceCompletionPending(
		*w,
		reservation,
		pending,
		ra,
		in,
		expect,
	)
	if err != nil {
		return runattempt.RunAttempt{}, pending, err
	}
	if migrated {
		if err := s.writeProofResourceCompletionPending(pendingPath, normalizedPending); err != nil {
			return runattempt.RunAttempt{}, pending, fmt.Errorf("normalize legacy proof completion retry journal: %w", err)
		}
		pending = normalizedPending
	}
	pending, err = s.adoptProofCompletionRetryDescendant(
		w,
		reservation,
		pending,
		pendingPath,
		workID,
		actor,
		now,
	)
	if err != nil {
		return runattempt.RunAttempt{}, pending, err
	}
	ra, err = s.runAttempts().Get(pending.Result.RunAttemptID)
	if err != nil {
		return runattempt.RunAttempt{}, pending, err
	}
	if reservation.WorkID != workID ||
		reservation.Actor != actor ||
		reservation.Status != ProofResourceAcquired ||
		ra.WorkID != workID ||
		ra.Actor != actor ||
		ra.Harness != proofResourceHarness ||
		ra.Origin != runattempt.OriginHarnessExecution ||
		(ra.ActionIntentID != reservation.ActionIntentID && ra.ActionIntentID != pending.Action.ID) ||
		ra.Workspace != reservation.Workspace ||
		ra.Command != reservation.Command ||
		pending.Action.IDs.WorkID != workID ||
		pending.Decision.ActionIntentID != pending.Action.ID ||
		!policy.Allowed(pending.Decision) {
		return runattempt.RunAttempt{}, pending, fmt.Errorf("proof completion retry is not bound to the reserved same-work RunAttempt and allowing decision")
	}
	if ra.ID != reservation.RunAttemptID {
		if err := s.persistProofCompletionRetryReachability(w, pending, ra, now); err != nil {
			return runattempt.RunAttempt{}, pending, err
		}
		if ra.Status == runattempt.StatusPending {
			ra, err = s.runAttempts().MarkRunning(ra.ID, now)
			if err != nil {
				return runattempt.RunAttempt{}, pending, err
			}
		}
	}
	if proofRunAttemptMatchesPreparedCompletion(ra, pending) {
		return ra, pending, nil
	}
	if ra.Status == runattempt.StatusRunning {
		return ra, pending, nil
	}
	if !runattempt.IsTerminal(ra.Status) {
		return runattempt.RunAttempt{}, pending, fmt.Errorf("proof completion retry RunAttempt status %q is not recoverable", ra.Status)
	}

	retry, retryErr := s.prepareProofRunAttempt(*w, pending.Action.ID, ProofResourceReserveInput{
		Actor:     actor,
		Workspace: reservation.Workspace,
		Command:   reservation.Command,
	}, now)
	failRetry := func(cause error) (runattempt.RunAttempt, proofResourceCompletionPending, error) {
		if retry.ID != "" {
			cause = errors.Join(cause, s.completeProofAttempt(
				retry.ID,
				runattempt.StatusFailed,
				"proof completion retry preparation failed: "+cause.Error(),
				now,
			))
		}
		return runattempt.RunAttempt{}, pending, cause
	}
	if retryErr != nil {
		return failRetry(retryErr)
	}
	if retry.PredecessorID != ra.ID {
		return failRetry(fmt.Errorf("proof completion retry predecessor %q does not match terminal attempt %q", retry.PredecessorID, ra.ID))
	}
	proofResourceAfterCompletionRetryMint(retry)
	nextPending := reseedProofResourceCompletionPending(pending, retry, now)
	if err := proofResourceWriteCompletionRetryJournal(s, pendingPath, nextPending, retry); err != nil {
		reachabilityErr := s.persistProofCompletionRetryReachability(w, nextPending, retry, now)
		return failRetry(errors.Join(
			fmt.Errorf("persist proof completion retry journal: %w", err),
			reachabilityErr,
		))
	}
	proofResourceAfterCompletionRetryJournal(retry, nextPending)
	pending = nextPending
	if err := s.persistProofCompletionRetryReachability(w, pending, retry, now); err != nil {
		return failRetry(err)
	}
	retry, err = s.runAttempts().MarkRunning(retry.ID, now)
	if err != nil {
		return failRetry(err)
	}
	return retry, pending, nil
}

func (s *Store) adoptProofCompletionRetryDescendant(
	w *envelope.WorkEnvelope,
	reservation ProofResourceReservation,
	pending proofResourceCompletionPending,
	pendingPath, workID, actor string,
	now time.Time,
) (proofResourceCompletionPending, error) {
	currentID := pending.Result.RunAttemptID
	adopted := false
	seen := map[string]bool{currentID: true}
	candidateIDs := append([]string(nil), w.RunAttemptIDs...)
	allAttempts, err := s.runAttempts().List()
	if err != nil {
		return pending, fmt.Errorf("list proof completion retry descendants: %w", err)
	}
	for _, candidate := range allAttempts {
		if candidate.WorkID == workID && !oneOf(candidate.ID, candidateIDs) {
			candidateIDs = append(candidateIDs, candidate.ID)
		}
	}
	for {
		var descendants []runattempt.RunAttempt
		for _, candidateID := range candidateIDs {
			if seen[candidateID] {
				continue
			}
			candidate, err := s.runAttempts().Get(candidateID)
			if err != nil {
				return pending, fmt.Errorf("read proof completion retry descendant %s: %w", candidateID, err)
			}
			if candidate.PredecessorID != currentID ||
				candidate.WorkID != workID ||
				candidate.Actor != actor ||
				candidate.Harness != proofResourceHarness ||
				candidate.Origin != runattempt.OriginHarnessExecution ||
				candidate.ActionIntentID != pending.Action.ID ||
				candidate.Workspace != reservation.Workspace ||
				candidate.Command != reservation.Command {
				continue
			}
			descendants = append(descendants, candidate)
		}
		if len(descendants) == 0 {
			break
		}
		if len(descendants) > 1 {
			var ambiguityErrs []error
			ids := make([]string, 0, len(descendants))
			for _, descendant := range descendants {
				ids = append(ids, descendant.ID)
				if err := s.persistProofCompletionRetryReachability(w, pending, descendant, now); err != nil {
					ambiguityErrs = append(ambiguityErrs, fmt.Errorf("persist ambiguous retry %s reachability: %w", descendant.ID, err))
				}
				if !runattempt.IsTerminal(descendant.Status) {
					if err := s.completeProofAttempt(
						descendant.ID,
						runattempt.StatusAbandoned,
						"ambiguous proof completion retry lineage",
						now,
					); err != nil {
						ambiguityErrs = append(ambiguityErrs, fmt.Errorf("terminalize ambiguous retry %s: %w", descendant.ID, err))
					}
				}
			}
			return pending, errors.Join(
				fmt.Errorf(
					"ambiguous proof completion retry descendants of %s: %s",
					currentID,
					strings.Join(ids, ","),
				),
				errors.Join(ambiguityErrs...),
			)
		}
		descendant := descendants[0]
		pending = reseedProofResourceCompletionPending(pending, descendant, now)
		currentID = descendant.ID
		seen[currentID] = true
		adopted = true
	}
	if !adopted {
		return pending, nil
	}
	if err := s.writeProofResourceCompletionPending(pendingPath, pending); err != nil {
		return pending, fmt.Errorf("adopt reachable proof completion retry %s: %w", currentID, err)
	}
	return pending, nil
}

func (s *Store) persistProofCompletionRetryReachability(
	w *envelope.WorkEnvelope,
	pending proofResourceCompletionPending,
	retry runattempt.RunAttempt,
	now time.Time,
) error {
	if err := proofResourceWriteEventRecord(s, "completion_retry_action", pending.Action.ID, &pending.Action); err != nil {
		return err
	}
	if err := proofResourceWriteEventRecord(s, "completion_retry_decision", pending.Decision.ID, &pending.Decision); err != nil {
		return err
	}
	if !oneOf(pending.Action.ID, w.ActionIntentIDs) {
		w.ActionIntentIDs = append(w.ActionIntentIDs, pending.Action.ID)
	}
	if !oneOf(retry.ID, w.RunAttemptIDs) {
		w.RunAttemptIDs = append(w.RunAttemptIDs, retry.ID)
	}
	w.PolicyDecisionID = pending.Decision.ID
	if err := proofResourceWriteWorkRecord(s, "completion_retry_work", w); err != nil {
		return err
	}
	retryEvent := Event{
		TS:       ts(now),
		WorkID:   w.ID,
		From:     w.State,
		To:       w.State,
		By:       pending.Result.Actor,
		Note:     "proof completion retry RunAttempt prepared from terminal predecessor",
		Attached: []string{pending.Action.ID, pending.Decision.ID, pending.ReservationID, retry.PredecessorID, retry.ID},
	}
	events, err := s.eventsFor(w.ID)
	if err != nil {
		return err
	}
	for _, event := range events {
		if event.WorkID == retryEvent.WorkID &&
			event.By == retryEvent.By &&
			event.Note == retryEvent.Note &&
			reflect.DeepEqual(event.Attached, retryEvent.Attached) {
			return nil
		}
	}
	return proofResourceAppendLifecycleRecord(s, retryEvent)
}

func reseedProofResourceCompletionPending(
	pending proofResourceCompletionPending,
	retry runattempt.RunAttempt,
	now time.Time,
) proofResourceCompletionPending {
	resultID := idFor("pfrs_", pending.Result.WorkID, pending.ReservationID, pending.Result.ReportArtifactRef, pending.Result.FailureReason, retry.ID)
	completionID := idFor("pfrc_", pending.Result.WorkID, pending.ReservationID, resultID)
	verificationRef := ""
	if pending.VerificationRef != "" {
		verificationRef = "proof-check:" + resultID
	}
	pending.CreatedAt = now.UTC().Format(time.RFC3339Nano)
	pending.Result.ID = resultID
	pending.Result.CreatedAt = pending.CreatedAt
	pending.Result.RunAttemptID = retry.ID
	pending.Result.RunAttemptStatus = pending.TerminalStatus
	pending.Result.VerificationRef = verificationRef
	pending.Result.CompletionID = completionID
	pending.Completion.ID = completionID
	pending.Completion.CreatedAt = pending.CreatedAt
	pending.Completion.ResultID = resultID
	pending.Completion.RunAttemptID = retry.ID
	pending.Completion.VerificationRef = verificationRef
	pending.VerificationRef = verificationRef
	pending.ProofRefs = []string{pending.ReservationID, resultID, completionID}
	if pending.Result.ReportArtifactRef != "" {
		pending.ProofRefs = append(pending.ProofRefs, pending.Result.ReportArtifactRef)
	}
	if verificationRef != "" {
		pending.ProofRefs = append(pending.ProofRefs, verificationRef)
	}
	pending.LifecycleEvent = Event{
		TS:       ts(now),
		WorkID:   pending.WorkID,
		From:     pending.LifecycleEvent.From,
		To:       pending.LifecycleEvent.To,
		By:       pending.Result.Actor,
		Note:     "proof resource finalizer retry committed; verification publishes after terminal RunAttempt",
		Attached: append([]string{pending.Action.ID, pending.Decision.ID, pending.ReservationID, resultID, completionID, retry.PredecessorID, retry.ID}, pending.ProofRefs[3:]...),
	}
	return pending
}

func (s *Store) persistPreparedProofCompletion(w *envelope.WorkEnvelope, pending proofResourceCompletionPending) error {
	for _, item := range []struct {
		stage string
		id    string
		value any
	}{
		{stage: "action", id: pending.Action.ID, value: &pending.Action},
		{stage: "decision", id: pending.Decision.ID, value: &pending.Decision},
		{stage: "result", id: pending.Result.ID, value: &pending.Result},
	} {
		if err := proofResourceWriteEventRecord(s, item.stage, item.id, item.value); err != nil {
			return fmt.Errorf("persist %s: %w", item.stage, err)
		}
	}
	if !oneOf(pending.Action.ID, w.ActionIntentIDs) {
		w.ActionIntentIDs = append(w.ActionIntentIDs, pending.Action.ID)
	}
	w.PolicyDecisionID = pending.Decision.ID
	if err := proofResourceWriteWorkRecord(s, "work", w); err != nil {
		return fmt.Errorf("persist work: %w", err)
	}
	events, err := s.eventsFor(w.ID)
	if err != nil {
		return err
	}
	for _, event := range events {
		if reflect.DeepEqual(event, pending.LifecycleEvent) {
			return nil
		}
	}
	if err := proofResourceAppendLifecycleRecord(s, pending.LifecycleEvent); err != nil {
		return fmt.Errorf("persist lifecycle: %w", err)
	}
	return nil
}

func (s *Store) terminalizePreparedProofCompletion(
	ra runattempt.RunAttempt,
	pending proofResourceCompletionPending,
	now time.Time,
) (runattempt.RunAttempt, error) {
	if runattempt.IsTerminal(ra.Status) {
		if !proofRunAttemptMatchesPreparedCompletion(ra, pending) {
			return runattempt.RunAttempt{}, fmt.Errorf("terminal proof RunAttempt conflicts with pending completion")
		}
		return ra, nil
	}
	if err := proofResourceSetRunAttemptProofRefs(s, ra.ID, pending.ProofRefs); err != nil {
		return runattempt.RunAttempt{}, fmt.Errorf("persist RunAttempt proof refs: %w", err)
	}
	terminal, err := proofResourceTerminalizeRunAttempt(s, ra.ID, pending.TerminalStatus, pending.TerminalReason, now)
	if err != nil {
		return runattempt.RunAttempt{}, fmt.Errorf("persist terminal RunAttempt: %w", err)
	}
	return terminal, nil
}

func proofRunAttemptMatchesPreparedCompletion(
	ra runattempt.RunAttempt,
	pending proofResourceCompletionPending,
) bool {
	return runattempt.IsTerminal(ra.Status) &&
		ra.Status == pending.TerminalStatus &&
		ra.SweepReason == pending.TerminalReason &&
		reflect.DeepEqual(ra.ProofRefs, pending.ProofRefs)
}

func (s *Store) activeProofRunAttempt(reservation ProofResourceReservation, workID, actor string) (runattempt.RunAttempt, error) {
	if reservation.WorkID != workID ||
		reservation.Actor != actor ||
		reservation.Status != ProofResourceAcquired ||
		reservation.RunAttemptID == "" {
		return runattempt.RunAttempt{}, fmt.Errorf("proof reservation %s does not authorize actor %q on work %s", reservation.ID, actor, workID)
	}
	ra, err := s.runAttempts().Get(reservation.RunAttemptID)
	if err != nil {
		return runattempt.RunAttempt{}, fmt.Errorf("proof reservation %s active RunAttempt: %w", reservation.ID, err)
	}
	if ra.WorkID != workID ||
		ra.Actor != actor ||
		ra.Harness != proofResourceHarness ||
		ra.Origin != runattempt.OriginHarnessExecution ||
		ra.ActionIntentID != reservation.ActionIntentID ||
		ra.Workspace != reservation.Workspace ||
		ra.Command != reservation.Command ||
		ra.Status != runattempt.StatusRunning {
		return runattempt.RunAttempt{}, fmt.Errorf("proof reservation %s requires active RunAttempt bound to same work/actor/action, got %+v", reservation.ID, ra)
	}
	return ra, nil
}

func decodeOwnedProofReport(body []byte) (ownedProofReport, error) {
	var report ownedProofReport
	if err := json.Unmarshal(body, &report); err != nil {
		return ownedProofReport{}, err
	}
	if report.SchemaVersion != "compound.proof_check_report.v0.1" &&
		report.SchemaVersion != "compound.proof_check_report.v0.2" {
		return ownedProofReport{}, fmt.Errorf("unsupported schema_version %q", report.SchemaVersion)
	}
	if report.Status != "ok" && report.Status != "failed" {
		return ownedProofReport{}, fmt.Errorf("status must be ok or failed")
	}
	if report.DryRun || report.CIOnly || report.CIConsole {
		return ownedProofReport{}, fmt.Errorf("lightweight/dry-run report is not a full proof battery")
	}
	if strings.TrimSpace(report.GeneratedAt) == "" {
		return ownedProofReport{}, fmt.Errorf("generated_at required")
	}
	if _, err := time.Parse(time.RFC3339, report.GeneratedAt); err != nil {
		return ownedProofReport{}, fmt.Errorf("generated_at: %w", err)
	}
	if strings.TrimSpace(report.Repo) == "" {
		return ownedProofReport{}, fmt.Errorf("repo required")
	}
	if len(report.Steps) == 0 {
		return ownedProofReport{}, fmt.Errorf("steps required")
	}
	failed := false
	passed := false
	for i, step := range report.Steps {
		if strings.TrimSpace(step.Name) == "" || strings.TrimSpace(step.ProofRef) == "" {
			return ownedProofReport{}, fmt.Errorf("step %d name and proof_ref required", i)
		}
		switch step.Status {
		case "ok":
			passed = true
		case "skipped":
		case "failed":
			failed = true
		default:
			return ownedProofReport{}, fmt.Errorf("step %d invalid status %q", i, step.Status)
		}
	}
	if report.Status == "ok" && (failed || !passed) {
		return ownedProofReport{}, fmt.Errorf("ok report must contain a passed gate and no failed gate")
	}
	if report.Status == "failed" && !failed {
		return ownedProofReport{}, fmt.Errorf("failed report must contain a failed gate")
	}
	return report, nil
}

func validateProofResourceProfileShape(profile ProofResourceProfile) error {
	if profile.Kind == "" {
		return fmt.Errorf("kind required")
	}
	if profile.Repo == "" {
		return fmt.Errorf("repo required")
	}
	if len(profile.Gates) == 0 {
		return fmt.Errorf("full gate inventory required")
	}
	names := make(map[string]bool, len(profile.Gates))
	refs := make(map[string]bool, len(profile.Gates))
	for i, gate := range profile.Gates {
		if gate.Name == "" {
			return fmt.Errorf("gate %d name required", i)
		}
		if len(gate.Command) == 0 {
			return fmt.Errorf("gate %d command required", i)
		}
		for j, part := range gate.Command {
			if strings.TrimSpace(part) == "" {
				return fmt.Errorf("gate %d command part %d required", i, j)
			}
		}
		if gate.ProofRef == "" {
			return fmt.Errorf("gate %d proof_ref required", i)
		}
		if names[gate.Name] {
			return fmt.Errorf("gate inventory repeats name %q", gate.Name)
		}
		if refs[gate.ProofRef] {
			return fmt.Errorf("gate inventory repeats proof_ref %q", gate.ProofRef)
		}
		names[gate.Name] = true
		refs[gate.ProofRef] = true
	}
	return nil
}

func proofResourceProfileDigest(profile ProofResourceProfile) (string, error) {
	profile.Digest = ""
	body, err := json.Marshal(profile)
	if err != nil {
		return "", fmt.Errorf("marshal proof resource profile: %w", err)
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func validateOwnedProofReportAgainstReservation(report ownedProofReport, reservation ProofResourceReservation) error {
	profile, err := NewProofResourceProfile(reservation.Profile.Kind, reservation.Profile.Repo, reservation.Profile.BaseRef, reservation.Profile.Gates)
	if err != nil {
		return fmt.Errorf("reservation profile invalid: %w", err)
	}
	if profile.Digest != reservation.Profile.Digest {
		return fmt.Errorf("reservation profile digest mismatch: stored=%q canonical=%q", reservation.Profile.Digest, profile.Digest)
	}
	if profile.Kind != ProofResourceProfileCanonicalFull {
		return fmt.Errorf("reservation profile_kind %q is not certifying; want %q", profile.Kind, ProofResourceProfileCanonicalFull)
	}
	if strings.TrimSpace(report.ProfileKind) != ProofResourceProfileCanonicalFull {
		return fmt.Errorf("report profile_kind %q is not certifying; want %q", report.ProfileKind, ProofResourceProfileCanonicalFull)
	}
	if reservation.Workspace != profile.Repo || report.Repo != profile.Repo {
		return fmt.Errorf("repo mismatch: report=%q profile=%q workspace=%q", report.Repo, profile.Repo, reservation.Workspace)
	}
	if strings.TrimSpace(report.BaseRef) != profile.BaseRef {
		return fmt.Errorf("base_ref mismatch: report=%q profile=%q", report.BaseRef, profile.BaseRef)
	}
	if len(report.Steps) != len(profile.Gates) {
		return fmt.Errorf("gate inventory mismatch: report=%d profile=%d", len(report.Steps), len(profile.Gates))
	}
	reportProofRefs := make([]string, 0, len(report.Steps))
	failedGates := 0
	for i, gate := range profile.Gates {
		step := report.Steps[i]
		switch {
		case step.Name != gate.Name:
			return fmt.Errorf("gate inventory mismatch at %d: name=%q want=%q", i, step.Name, gate.Name)
		case !reflect.DeepEqual(step.Command, gate.Command):
			return fmt.Errorf("gate %q command mismatch: report=%v profile=%v", gate.Name, step.Command, gate.Command)
		case strings.TrimSpace(step.Dir) != gate.Dir:
			return fmt.Errorf("gate %q dir mismatch: report=%q profile=%q", gate.Name, step.Dir, gate.Dir)
		case strings.TrimSpace(step.SkipIfMissing) != gate.SkipIfMissing:
			return fmt.Errorf("gate %q skip_if_missing mismatch: report=%q profile=%q", gate.Name, step.SkipIfMissing, gate.SkipIfMissing)
		case step.ProofRef != gate.ProofRef:
			return fmt.Errorf("gate %q proof_ref mismatch: report=%q profile=%q", gate.Name, step.ProofRef, gate.ProofRef)
		}
		switch step.Status {
		case "ok":
			reportProofRefs = append(reportProofRefs, step.ProofRef)
		case "failed":
			if report.Status != "failed" {
				return fmt.Errorf("canonical_full ok report contains failed gate %q", gate.Name)
			}
			failedGates++
		default:
			return fmt.Errorf(
				"canonical_full gate %q status %q is incomplete; every reserved gate must finish ok or failed",
				gate.Name,
				step.Status,
			)
		}
	}
	if report.Status == "ok" && failedGates != 0 {
		return fmt.Errorf("canonical_full ok report contains %d failed gates", failedGates)
	}
	if report.Status == "failed" && failedGates == 0 {
		return fmt.Errorf("canonical_full failed report contains no failed gate")
	}
	if !reflect.DeepEqual(report.ProofRefs, reportProofRefs) {
		return fmt.Errorf("proof_ref inventory mismatch: report=%v successful_gates=%v", report.ProofRefs, reportProofRefs)
	}
	generatedAt, err := time.Parse(time.RFC3339, report.GeneratedAt)
	if err != nil {
		return fmt.Errorf("generated_at: %w", err)
	}
	acquiredAt, err := time.Parse(time.RFC3339, reservation.AcquiredAt)
	if err != nil {
		return fmt.Errorf("reservation acquired_at: %w", err)
	}
	expiresAt, err := time.Parse(time.RFC3339, reservation.ExpiresAt)
	if err != nil {
		return fmt.Errorf("reservation expires_at: %w", err)
	}
	if generatedAt.Before(acquiredAt) || !generatedAt.Before(expiresAt) {
		return fmt.Errorf("generated_at %s outside reservation window [%s,%s)", report.GeneratedAt, reservation.AcquiredAt, reservation.ExpiresAt)
	}
	return nil
}

func (s *Store) proofReservationWithLatestRefresh(reservation ProofResourceReservation) (ProofResourceReservation, error) {
	events, err := s.eventsFor(reservation.WorkID)
	if err != nil {
		return ProofResourceReservation{}, err
	}
	latest := ProofResourceRefresh{}
	seen := map[string]bool{}
	for _, event := range events {
		for _, ref := range event.Attached {
			if !strings.HasPrefix(ref, "pfrh_") || seen[ref] {
				continue
			}
			seen[ref] = true
			var refresh ProofResourceRefresh
			if err := s.readEventJSON(ref, &refresh); err != nil {
				return ProofResourceReservation{}, fmt.Errorf("proof refresh evidence %s: %w", ref, err)
			}
			if refresh.SchemaVersion != ProofResourceRefreshSchemaVersion ||
				refresh.ID != ref ||
				refresh.WorkID != reservation.WorkID ||
				refresh.Actor != reservation.Actor ||
				refresh.ReservationID != reservation.ID ||
				refresh.RunAttemptID != reservation.RunAttemptID ||
				refresh.Holder != reservation.Holder ||
				refresh.WindowOpenedAt != reservation.AcquiredAt ||
				refresh.ActionIntentID == "" ||
				refresh.PolicyDecisionID == "" {
				return ProofResourceReservation{}, fmt.Errorf("proof refresh evidence %s is not bound to the reserved work/actor/RunAttempt/window", ref)
			}
			var action envelope.ActionIntent
			if err := s.readEventJSON(refresh.ActionIntentID, &action); err != nil {
				return ProofResourceReservation{}, fmt.Errorf("proof refresh %s ActionIntent: %w", ref, err)
			}
			var decision envelope.PolicyDecision
			if err := s.readEventJSON(refresh.PolicyDecisionID, &decision); err != nil {
				return ProofResourceReservation{}, fmt.Errorf("proof refresh %s PolicyDecision: %w", ref, err)
			}
			if action.IDs.WorkID != reservation.WorkID ||
				action.Actor != reservation.Actor ||
				(action.Action != "refresh_full_proof_resource" &&
					action.Action != "rearm_expired_proof_completion") ||
				decision.ActionIntentID != action.ID ||
				!policy.Allowed(decision) {
				return ProofResourceReservation{}, fmt.Errorf("proof refresh evidence %s lacks its exact allowing governance chain", ref)
			}
			refreshedAt, err := time.Parse(time.RFC3339, refresh.RefreshedAt)
			if err != nil {
				return ProofResourceReservation{}, fmt.Errorf("proof refresh %s refreshed_at: %w", ref, err)
			}
			expiresAt, err := time.Parse(time.RFC3339, refresh.ExpiresAt)
			if err != nil || !expiresAt.After(refreshedAt) {
				return ProofResourceReservation{}, fmt.Errorf("proof refresh %s has invalid expires_at %q", ref, refresh.ExpiresAt)
			}
			if latest.ID == "" || refresh.RefreshedAt > latest.RefreshedAt {
				latest = refresh
			}
		}
	}
	if latest.ID != "" {
		reservation.ExpiresAt = latest.ExpiresAt
		reservation.TTLSeconds = latest.TTLSeconds
	}
	return reservation, nil
}

func (s *Store) persistProofReservation(
	w *envelope.WorkEnvelope,
	action envelope.ActionIntent,
	decision envelope.PolicyDecision,
	reservation ProofResourceReservation,
	note string,
	now time.Time,
) error {
	if err := s.writeEventJSON(action.ID, &action); err != nil {
		return err
	}
	if err := s.writeEventJSON(decision.ID, &decision); err != nil {
		return err
	}
	if err := s.writeEventJSON(reservation.ID, &reservation); err != nil {
		return err
	}
	if !oneOf(action.ID, w.ActionIntentIDs) {
		w.ActionIntentIDs = append(w.ActionIntentIDs, action.ID)
	}
	if reservation.RunAttemptID != "" && !oneOf(reservation.RunAttemptID, w.RunAttemptIDs) {
		w.RunAttemptIDs = append(w.RunAttemptIDs, reservation.RunAttemptID)
	}
	w.PolicyDecisionID = decision.ID
	if err := s.writeJSON(s.workPath(w.ID), w); err != nil {
		return err
	}
	attached := []string{action.ID, decision.ID, reservation.ID}
	if reservation.RunAttemptID != "" {
		attached = append(attached, reservation.RunAttemptID)
	}
	return s.appendEvent(Event{
		TS:       ts(now),
		WorkID:   w.ID,
		From:     w.State,
		To:       w.State,
		By:       reservation.Actor,
		Note:     note,
		Attached: attached,
	})
}

func (s *Store) persistProofScarceMutationPreparation(
	w *envelope.WorkEnvelope,
	action envelope.ActionIntent,
	decision envelope.PolicyDecision,
	reservation ProofResourceReservation,
	runAttemptID, phase string,
	now time.Time,
) error {
	if err := proofResourceWriteEventRecord(s, phase+"_prepare_action", action.ID, &action); err != nil {
		return fmt.Errorf("persist proof %s preparation action: %w", phase, err)
	}
	if err := proofResourceWriteEventRecord(s, phase+"_prepare_decision", decision.ID, &decision); err != nil {
		return fmt.Errorf("persist proof %s preparation decision: %w", phase, err)
	}
	if !oneOf(action.ID, w.ActionIntentIDs) {
		w.ActionIntentIDs = append(w.ActionIntentIDs, action.ID)
	}
	if runAttemptID != "" && !oneOf(runAttemptID, w.RunAttemptIDs) {
		w.RunAttemptIDs = append(w.RunAttemptIDs, runAttemptID)
	}
	w.PolicyDecisionID = decision.ID
	if err := proofResourceWriteWorkRecord(s, phase+"_prepare_work", w); err != nil {
		return fmt.Errorf("persist proof %s preparation work: %w", phase, err)
	}
	attached := []string{action.ID, decision.ID}
	if phase == "refresh" || phase == "completion_rearm" {
		attached = append(attached, reservation.ID)
	}
	if runAttemptID != "" {
		attached = append(attached, runAttemptID)
	}
	return proofResourceAppendLifecycleRecord(s, Event{
		TS:       ts(now),
		WorkID:   w.ID,
		From:     w.State,
		To:       w.State,
		By:       reservation.Actor,
		Note:     "proof resource " + phase + " governance persisted before scarce mutation",
		Attached: attached,
	})
}

func (s *Store) persistProofRefresh(
	w *envelope.WorkEnvelope,
	action envelope.ActionIntent,
	decision envelope.PolicyDecision,
	refresh ProofResourceRefresh,
	now time.Time,
) error {
	note := "proof resource heartbeat refreshed the exclusive expiry"
	if action.Action == "rearm_expired_proof_completion" {
		note = "exact expired proof completion holder rearmed after journal validation and execution attestation"
	}
	if err := s.writeEventJSON(action.ID, &action); err != nil {
		return err
	}
	if err := s.writeEventJSON(decision.ID, &decision); err != nil {
		return err
	}
	if err := s.writeEventJSON(refresh.ID, &refresh); err != nil {
		return err
	}
	if !oneOf(action.ID, w.ActionIntentIDs) {
		w.ActionIntentIDs = append(w.ActionIntentIDs, action.ID)
	}
	w.PolicyDecisionID = decision.ID
	if err := s.writeJSON(s.workPath(w.ID), w); err != nil {
		return err
	}
	return s.appendEvent(Event{
		TS:       ts(now),
		WorkID:   w.ID,
		From:     w.State,
		To:       w.State,
		By:       refresh.Actor,
		Note:     note,
		Attached: []string{action.ID, decision.ID, refresh.ReservationID, refresh.ID, refresh.RunAttemptID},
	})
}

func (s *Store) prepareTrustedProofTerminationEvidence(
	holder, runAttemptID, actor string,
	now time.Time,
) (string, []byte, error) {
	attestation, err := landlease.AttestExecutionTerminated(s.home, holder)
	if err != nil {
		return "", nil, fmt.Errorf("proof resource trusted termination producer: %w", err)
	}
	body, err := json.Marshal(ProofResourceTerminationEvidence{
		SchemaVersion: ProofResourceTerminationEvidenceSchemaVersion,
		Producer:      attestation.Producer,
		BarrierID:     attestation.BarrierID,
		Holder:        attestation.Holder,
		RunAttemptID:  runAttemptID,
		Status:        "terminated_and_waited",
		ObservedAt:    now.UTC().Format(time.RFC3339Nano),
		Observer:      strings.TrimSpace(actor),
	})
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(body)
	ref := home.ArtifactRefPrefix + hex.EncodeToString(sum[:])
	if err := s.validateProofTerminationEvidenceBody(ref, body, runAttemptID, holder, now); err != nil {
		return "", nil, err
	}
	return ref, body, nil
}

func (s *Store) persistTrustedProofTerminationEvidence(expectedRef string, body []byte) error {
	artifacts, err := home.NewArtifactStore(s.home)
	if err != nil {
		return err
	}
	ref, err := artifacts.Put(body)
	if err != nil {
		return err
	}
	if ref != expectedRef {
		return fmt.Errorf("proof resource trusted termination evidence ref %q, want %q", ref, expectedRef)
	}
	return nil
}

func (s *Store) validateProofTerminationEvidence(ref, runAttemptID, holder string, now time.Time) error {
	artifacts, err := home.NewArtifactStore(s.home)
	if err != nil {
		return err
	}
	body, err := artifacts.Get(ref)
	if err != nil {
		return fmt.Errorf("proof resource termination evidence %q is not in immutable Compound custody: %w", ref, err)
	}
	return s.validateProofTerminationEvidenceBody(ref, body, runAttemptID, holder, now)
}

func (s *Store) validateProofTerminationEvidenceBody(
	ref string,
	body []byte,
	runAttemptID, holder string,
	now time.Time,
) error {
	var evidence ProofResourceTerminationEvidence
	if err := json.Unmarshal(body, &evidence); err != nil {
		return fmt.Errorf("proof resource termination evidence %q is malformed: %w", ref, err)
	}
	attestation, err := landlease.AttestExecutionTerminated(s.home, holder)
	if err != nil {
		return fmt.Errorf("proof resource termination evidence %q cannot be re-attested: %w", ref, err)
	}
	if evidence.Producer != attestation.Producer ||
		evidence.BarrierID != attestation.BarrierID ||
		evidence.Holder != attestation.Holder {
		return fmt.Errorf("proof resource termination evidence %q is not from the trusted execution-barrier producer", ref)
	}
	if evidence.SchemaVersion != ProofResourceTerminationEvidenceSchemaVersion ||
		evidence.RunAttemptID != runAttemptID ||
		evidence.Status != "terminated_and_waited" ||
		strings.TrimSpace(evidence.Observer) == "" {
		return fmt.Errorf(
			"proof resource termination evidence %q is not a terminated-and-waited receipt for %s",
			ref,
			runAttemptID,
		)
	}
	observedAt, err := time.Parse(time.RFC3339Nano, evidence.ObservedAt)
	if err != nil {
		return fmt.Errorf("proof resource termination evidence %q observed_at: %w", ref, err)
	}
	ra, err := s.runAttempts().Get(runAttemptID)
	if err != nil {
		return fmt.Errorf("proof resource termination evidence %q RunAttempt: %w", ref, err)
	}
	if observedAt.Before(ra.CreatedAt) || observedAt.After(now) {
		return fmt.Errorf(
			"proof resource termination evidence %q observed_at %s is outside RunAttempt interval [%s,%s]",
			ref,
			evidence.ObservedAt,
			ra.CreatedAt.UTC().Format(time.RFC3339Nano),
			now.UTC().Format(time.RFC3339Nano),
		)
	}
	return nil
}

func (s *Store) persistProofTerminationConfirmation(
	w *envelope.WorkEnvelope,
	reservation ProofResourceReservation,
	now time.Time,
) error {
	confirmation := ProofResourceTerminationConfirmation{
		SchemaVersion:          ProofResourceTerminationSchemaVersion,
		ID:                     reservation.TerminationConfirmationID,
		Kind:                   "work.proof_resource.termination",
		CreatedAt:              reservation.TerminationConfirmedAt,
		WorkID:                 reservation.WorkID,
		Actor:                  reservation.Actor,
		ReservationID:          reservation.ID,
		PriorHolder:            reservation.PriorHolder,
		TerminatedRunAttemptID: reservation.TerminatedRunAttemptID,
		TerminationEvidenceRef: reservation.TerminationEvidenceRef,
		ActionIntentID:         reservation.ActionIntentID,
		PolicyDecisionID:       reservation.PolicyDecisionID,
	}
	if err := s.writeEventJSON(confirmation.ID, &confirmation); err != nil {
		return fmt.Errorf("persist proof termination confirmation: %w", err)
	}
	if !oneOf(reservation.TerminationEvidenceRef, w.ArtifactIDs) {
		w.ArtifactIDs = append(w.ArtifactIDs, reservation.TerminationEvidenceRef)
		if err := s.writeJSON(s.workPath(w.ID), w); err != nil {
			return fmt.Errorf("persist proof termination evidence reachability: %w", err)
		}
	}
	return s.appendEvent(Event{
		TS:     ts(now),
		WorkID: w.ID,
		From:   w.State,
		To:     w.State,
		By:     reservation.Actor,
		Note: fmt.Sprintf(
			"proof resource predecessor %s termination confirmed by governed immutable evidence before exceptional takeover",
			reservation.TerminatedRunAttemptID,
		),
		Attached: []string{
			reservation.ActionIntentID,
			reservation.PolicyDecisionID,
			reservation.ID,
			confirmation.ID,
			reservation.TerminatedRunAttemptID,
			reservation.TerminationEvidenceRef,
		},
	})
}

func (s *Store) persistProofAcquisitionFailureReachability(
	w *envelope.WorkEnvelope,
	action envelope.ActionIntent,
	decision envelope.PolicyDecision,
	reservation ProofResourceReservation,
	now time.Time,
) error {
	if !oneOf(action.ID, w.ActionIntentIDs) {
		w.ActionIntentIDs = append(w.ActionIntentIDs, action.ID)
	}
	if !oneOf(reservation.RunAttemptID, w.RunAttemptIDs) {
		w.RunAttemptIDs = append(w.RunAttemptIDs, reservation.RunAttemptID)
	}
	w.PolicyDecisionID = decision.ID
	if err := s.writeJSON(s.workPath(w.ID), w); err != nil {
		return err
	}
	return s.appendEvent(Event{
		TS:       ts(now),
		WorkID:   w.ID,
		From:     w.State,
		To:       w.State,
		By:       reservation.Actor,
		Note:     "proof resource acquisition failed after reservation persistence; lease released before execution",
		Attached: []string{action.ID, decision.ID, reservation.ID, reservation.RunAttemptID},
	})
}

func (s *Store) proofResourceAction(
	w envelope.WorkEnvelope,
	reservationID, actor, phase, priorHolder, reason, terminatedRunAttemptID, terminationEvidenceRef string,
	now time.Time,
) envelope.ActionIntent {
	action := "reserve_full_proof_resource"
	preview := "reserve full proof resource before any gate subprocess"
	rollback := "release the exclusive proof resource or let its heartbeat-backed TTL expire"
	mutationClass := "local_reversible"
	reversibility := "easy"
	blastRadius := "local"
	switch {
	case phase == "complete":
		action = "complete_full_proof_resource"
		preview = "persist governed full proof result while exclusive, then release"
		rollback = "result remains evidence; no successor holder is released by a failed finalizer"
	case phase == "refresh":
		action = "refresh_full_proof_resource"
		preview = "heartbeat the active proof RunAttempt and extend its exact exclusive reservation"
		rollback = "stop refreshing; the last evidenced TTL expires without reopening a stale holder"
	case phase == "completion_rearm":
		action = "rearm_expired_proof_completion"
		preview = "rearm the exact expired proof holder after complete journal validation and execution termination"
		rollback = "the exact holder remains auditable and its short rearmed TTL expires without successor release"
	case strings.TrimSpace(terminationEvidenceRef) != "":
		action = "steal_full_proof_resource_after_termination_evidence"
		preview = fmt.Sprintf(
			"exceptionally steal full proof resource from %q after terminated predecessor %s; reason: %s; immutable termination evidence: %s",
			priorHolder,
			terminatedRunAttemptID,
			reason,
			terminationEvidenceRef,
		)
		rollback = "termination evidence and the predecessor terminal state remain auditable; the scarce holder stays closed on transfer failure"
		mutationClass = "security"
		reversibility = "manual"
		blastRadius = "repo"
	case strings.TrimSpace(reason) != "":
		action = "steal_full_proof_resource"
		preview = fmt.Sprintf("exceptionally steal full proof resource from %q; reason: %s", priorHolder, reason)
		rollback = "stolen predecessor remains auditable through reservation and RunAttempt evidence"
		mutationClass = "security"
		reversibility = "manual"
		blastRadius = "repo"
	}
	return envelope.ActionIntent{
		Header: newHeader(
			envelope.SVActionIntent,
			idFor(
				"act_",
				"proof.resource",
				action,
				w.ID,
				reservationID,
				actor,
				priorHolder,
				reason,
				terminatedRunAttemptID,
				terminationEvidenceRef,
				ts(now),
			),
			now,
			w.ID,
			w.IDs.ContextPackID,
			w.Privacy,
			w.Risk,
		),
		Actor:         actor,
		Capability:    "proof.resource",
		Action:        action,
		Target:        envelope.ActionTarget{Kind: "work", ID: w.ID, Path: s.workPath(w.ID)},
		MutationClass: mutationClass,
		Reversibility: reversibility,
		BlastRadius:   blastRadius,
		Preview:       preview,
		RollbackPlan:  rollback,
	}
}

func (s *Store) evaluateProofResourceAction(action envelope.ActionIntent, now time.Time) (envelope.PolicyDecision, error) {
	if err := action.Validate(); err != nil {
		return envelope.PolicyDecision{}, err
	}
	cfg, _, err := policy.LoadConfig(s.home)
	if err != nil {
		return envelope.PolicyDecision{}, err
	}
	decision := policy.Evaluate(action, cfg, now)
	if err := decision.Validate(); err != nil {
		return envelope.PolicyDecision{}, err
	}
	return decision, nil
}

func (s *Store) completeProofAttempt(id, status, reason string, now time.Time) error {
	ra, err := s.runAttempts().Get(id)
	if err != nil {
		return err
	}
	if runattempt.IsTerminal(ra.Status) {
		if ra.Status == status {
			return nil
		}
		return fmt.Errorf("proof RunAttempt %s already terminal as %s, want %s", id, ra.Status, status)
	}
	_, err = s.runAttempts().CompleteWithReason(id, status, reason, now)
	return err
}

func (s *Store) terminalizeGovernedProofPredecessor(id, reason string, now time.Time) error {
	ra, err := s.runAttempts().Get(id)
	if err != nil {
		return fmt.Errorf("recover proof RunAttempt %s: %w", id, err)
	}
	if runattempt.IsTerminal(ra.Status) {
		return nil
	}
	_, err = s.runAttempts().CompleteWithReason(id, runattempt.StatusAbandoned, reason, now)
	return err
}

type proofRecoveryBlocker struct {
	ReservationID string
	RunAttemptID  string
}

func proofReservationPredecessorHolders(reservation ProofResourceReservation) ([]string, error) {
	holders := make([]string, 0, 2)
	for _, holder := range []string{reservation.RecoveredHolder, reservation.PriorHolder} {
		holder = strings.TrimSpace(holder)
		if holder == "" || oneOf(holder, holders) {
			continue
		}
		holders = append(holders, holder)
	}
	if len(holders) > 1 {
		return nil, fmt.Errorf(
			"proof resource reservation %s has ambiguous predecessor holders %q and %q",
			reservation.ID,
			holders[0],
			holders[1],
		)
	}
	return holders, nil
}

func (s *Store) proofRecoveryOldestNonterminal(holder string) (proofRecoveryBlocker, error) {
	return s.proofRecoveryOldestNonterminalFrom(holder, map[string]bool{})
}

func (s *Store) proofRecoveryOldestNonterminalFrom(
	holder string,
	path map[string]bool,
) (proofRecoveryBlocker, error) {
	reservationID, runID, _, ok := parseProofResourceHolder(holder)
	if !ok {
		return proofRecoveryBlocker{}, fmt.Errorf("proof resource predecessor holder %q is invalid", holder)
	}
	node := reservationID + ":" + runID
	if path[node] {
		return proofRecoveryBlocker{}, fmt.Errorf("proof resource predecessor lineage cycle at %s", node)
	}
	path[node] = true
	defer delete(path, node)

	var reservation ProofResourceReservation
	if err := s.readEventJSON(reservationID, &reservation); err != nil {
		exists, existsErr := s.events.Exists(reservationID)
		if existsErr != nil {
			return proofRecoveryBlocker{}, errors.Join(err, existsErr)
		}
		if !exists {
			// A crash after the lease rename but before reservation promotion
			// leaves the exact holder-bound RunAttempt reachable from Work. No
			// runner was returned to the caller, so the missing node has no
			// executable descendants; recover that exact run fail-closed.
			ra, runErr := s.runAttempts().Get(runID)
			if runErr != nil {
				return proofRecoveryBlocker{}, errors.Join(err, runErr)
			}
			if !runattempt.IsTerminal(ra.Status) {
				return proofRecoveryBlocker{ReservationID: reservationID, RunAttemptID: runID}, nil
			}
			return proofRecoveryBlocker{}, nil
		}
		return proofRecoveryBlocker{}, fmt.Errorf("read proof resource predecessor reservation %s: %w", reservationID, err)
	}
	if reservation.ID != reservationID || reservation.RunAttemptID != runID {
		return proofRecoveryBlocker{}, fmt.Errorf(
			"proof resource predecessor holder %q does not match reservation %s run %s",
			holder,
			reservation.ID,
			reservation.RunAttemptID,
		)
	}
	predecessorHolders, err := proofReservationPredecessorHolders(reservation)
	if err != nil {
		return proofRecoveryBlocker{}, err
	}
	if len(predecessorHolders) == 1 {
		blocker, err := s.proofRecoveryOldestNonterminalFrom(predecessorHolders[0], path)
		if err != nil {
			return proofRecoveryBlocker{}, err
		}
		if blocker.RunAttemptID != "" {
			return blocker, nil
		}
	}
	ra, err := s.runAttempts().Get(runID)
	if err != nil {
		return proofRecoveryBlocker{}, fmt.Errorf("inspect recovered proof RunAttempt %s: %w", runID, err)
	}
	if !runattempt.IsTerminal(ra.Status) {
		return proofRecoveryBlocker{ReservationID: reservationID, RunAttemptID: runID}, nil
	}
	return proofRecoveryBlocker{}, nil
}

func proofResourceHolder(reservationID, runAttemptID, workID, actor string) string {
	return strings.Join([]string{"proof", reservationID, runAttemptID, workID, actor}, ":")
}

func parseProofResourceHolder(holder string) (reservationID, runAttemptID, workID string, ok bool) {
	parts := strings.SplitN(holder, ":", 5)
	if len(parts) != 5 ||
		parts[0] != "proof" ||
		!strings.HasPrefix(parts[1], "pfr_") ||
		!runattempt.Valid(parts[2]) ||
		!strings.HasPrefix(parts[3], "work_") {
		return "", "", "", false
	}
	return parts[1], parts[2], parts[3], true
}

func completionReason(status, failureReason string) string {
	if strings.TrimSpace(failureReason) != "" {
		return "proof resource failed: " + strings.TrimSpace(failureReason)
	}
	return "proof resource result " + status
}
