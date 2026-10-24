package work

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/policy"
	"compound/internal/runattempt"
)

// RetainedFailedProofRecoveryHelp documents the recovery-only route that finishes an
// authentic retained failed proof after its source checkout moved or disappeared.
const RetainedFailedProofRecoveryHelp = "  --resume-retained PFR --work W --actor A --expected-pending-sha256 SHA --out FILE  finish an exact retained failed proof through its own reservation; never admits a battery"

// ProofRecoveryHelp documents every proof recovery-only route in one place.
const ProofRecoveryHelp = QuiescentProofRecoveryHelp + "\n" + RetainedFailedProofRecoveryHelp

// proofResourceAfterRetainedAdmission is a deterministic test boundary between the retained
// admission fence and the protected native mutation.
var proofResourceAfterRetainedAdmission = func(reservationID string) {}

// ProofResourceRetainedFailureInput identifies the exact retained failed proof completion a
// recovery-only route may finish. It carries no workspace, command or profile: those are read
// from the persisted reservation, never from the current checkout.
type ProofResourceRetainedFailureInput struct {
	Actor                 string
	ReservationID         string
	ExpectedPendingSHA256 string
	// PrepareOwnedReport is the existing projection-publication barrier. The retained report is
	// restored through it before the exclusive holder may be released.
	PrepareOwnedReport func(ProofResourceResult) error
}

// RetainedFailedProofOutcome is the truthful outcome of a retained failed proof recovery.
// It reports an audit completion, never a certification: VerificationRef stays empty and the
// original failed report, attempts and refusals are preserved.
type RetainedFailedProofOutcome struct {
	ReservationID     string                `json:"reservation_id"`
	WorkID            string                `json:"work_id"`
	Actor             string                `json:"actor"`
	ReportArtifactRef string                `json:"report_artifact_ref"`
	ReportStatus      string                `json:"report_status"`
	ProducerExitCode  int                   `json:"producer_exit_code"`
	VerificationRef   string                `json:"verification_ref,omitempty"`
	ResourceReleased  bool                  `json:"resource_released"`
	Work              envelope.WorkEnvelope `json:"work"`
}

// ResumeRetainedFailedProofCompletion finishes an authentic retained failed proof through the
// ordinary native completion machinery. It reads the persisted reservation for the original
// workspace, command and sealed profile, never the current checkout; it never admits a battery,
// never starts a gate subprocess, and never certifies the retained report.
//
// found=false means no retained completion matches this reservation: the caller must not fall
// back to fresh admission.
func (s *Store) ResumeRetainedFailedProofCompletion(
	workID string,
	in ProofResourceRetainedFailureInput,
	now time.Time,
) (RetainedFailedProofOutcome, bool, error) {
	workID = strings.TrimSpace(workID)
	in.Actor = strings.TrimSpace(in.Actor)
	in.ReservationID = strings.TrimSpace(in.ReservationID)
	in.ExpectedPendingSHA256 = strings.ToLower(strings.TrimSpace(in.ExpectedPendingSHA256))
	if workID == "" || in.Actor == "" || in.ReservationID == "" || in.ExpectedPendingSHA256 == "" {
		return RetainedFailedProofOutcome{}, false, fmt.Errorf(
			"retained failed proof recovery requires exact work, actor, reservation and expected pending sha256",
		)
	}
	if len(in.ExpectedPendingSHA256) != 64 || strings.Trim(in.ExpectedPendingSHA256, "0123456789abcdef") != "" {
		return RetainedFailedProofOutcome{}, false, fmt.Errorf("expected pending sha256 must be 64 lowercase hex characters")
	}
	var reservation ProofResourceReservation
	if err := s.readProofReservationInto(in.ReservationID, &reservation); err != nil {
		return RetainedFailedProofOutcome{}, false, fmt.Errorf("read retained proof reservation %s: %w", in.ReservationID, err)
	}
	if reservation.WorkID != workID || reservation.Actor != in.Actor {
		return RetainedFailedProofOutcome{}, false, fmt.Errorf(
			"retained proof reservation %s does not belong to work %q and actor %q",
			in.ReservationID,
			workID,
			in.Actor,
		)
	}
	if reservation.Status != ProofResourceAcquired {
		return RetainedFailedProofOutcome{}, false, fmt.Errorf(
			"retained proof reservation %s is not an acquired reservation",
			in.ReservationID,
		)
	}
	pending, present, _, err := s.readProofResourceCompletionPending(reservation.ID)
	if err != nil {
		return RetainedFailedProofOutcome{}, false, err
	}
	if !present {
		// The completion journal is gone. There is no retained completion to finish: the route
		// refuses safely. It never recreates consumed evidence, never writes an output, and
		// never admits a battery in its place.
		return RetainedFailedProofOutcome{}, false, nil
	}
	if err := s.verifyRetainedPendingSHA256(reservation.ID, in.ExpectedPendingSHA256); err != nil {
		return RetainedFailedProofOutcome{}, false, err
	}
	proofResourceAfterRetainedAdmission(reservation.ID)
	if err := authenticateRetainedFailedProofResult(workID, in.Actor, reservation, pending); err != nil {
		return RetainedFailedProofOutcome{}, false, err
	}
	completeInput := ProofResourceCompleteInput{
		Actor:             in.Actor,
		ReservationID:     reservation.ID,
		ReportArtifactRef: pending.Result.ReportArtifactRef,
		ProducerExitKnown: pending.Result.ProducerExitKnown,
		ProducerExitCode:  pending.Result.ProducerExitCode,
		FailureReason:     pending.Result.FailureReason,
	}
	if _, err := s.deriveProofResourceCompletionExpectation(workID, reservation, completeInput); err != nil {
		return RetainedFailedProofOutcome{}, false, fmt.Errorf(
			"retained failed proof report is not authentic for its reservation: %w",
			err,
		)
	}
	resumed, found, err := s.ResumeProofResourceCompletion(workID, ProofResourceResumeInput{
		Actor:                 in.Actor,
		Workspace:             reservation.Workspace,
		Command:               reservation.Command,
		Profile:               reservation.Profile,
		PrepareOwnedReport:    in.PrepareOwnedReport,
		ReservationID:         reservation.ID,
		ExpectedPendingSHA256: in.ExpectedPendingSHA256,
		RequireFailedResult:   true,
	}, now)
	if !found {
		if err != nil {
			return RetainedFailedProofOutcome{}, false, err
		}
		return RetainedFailedProofOutcome{}, false, nil
	}
	outcome := RetainedFailedProofOutcome{
		ReservationID:     reservation.ID,
		WorkID:            workID,
		Actor:             in.Actor,
		ReportArtifactRef: resumed.Result.ReportArtifactRef,
		ReportStatus:      resumed.Result.ReportStatus,
		ProducerExitCode:  resumed.Result.ProducerExitCode,
		VerificationRef:   resumed.VerificationRef,
		ResourceReleased:  resumed.ResourceReleased,
		Work:              resumed.Work,
	}
	if err != nil {
		return outcome, true, err
	}
	if resumed.Result.Status != ProofResourceFailed {
		return outcome, true, fmt.Errorf("retained failed proof recovery produced a %q result", resumed.Result.Status)
	}
	if resumed.VerificationRef != "" {
		return outcome, true, fmt.Errorf("retained failed proof recovery produced a verification reference")
	}
	return outcome, true, nil
}

// authenticateRetainedFailedProofResult accepts only an owned, exact failed result for this
// reservation. A successful, unknown, inconsistent, unowned or verification-bearing result can
// never use the dedicated failure route.
func authenticateRetainedFailedProofResult(
	workID, actor string,
	reservation ProofResourceReservation,
	pending proofResourceCompletionPending,
) error {
	result := pending.Result
	switch {
	case result.WorkID != workID || result.Actor != actor:
		return fmt.Errorf("retained proof result does not belong to the exact work and actor")
	case result.ReservationID != reservation.ID:
		return fmt.Errorf("retained proof result does not belong to reservation %s", reservation.ID)
	case result.Status != ProofResourceFailed:
		return fmt.Errorf("retained proof result status %q is not a failure", result.Status)
	case result.RunAttemptStatus != runattempt.StatusFailed:
		return fmt.Errorf("retained proof result attempt status %q is not a terminal failure", result.RunAttemptStatus)
	case !result.OwnedReport:
		return fmt.Errorf("retained proof result does not carry an owned report")
	case result.ReportArtifactRef == "":
		return fmt.Errorf("retained proof result has no owned report artifact")
	case result.ReportStatus != "failed":
		return fmt.Errorf("retained proof result report status %q is not a failed battery verdict", result.ReportStatus)
	case !result.ProducerExitKnown:
		return fmt.Errorf("retained proof result producer exit is unknown")
	case result.ProducerExitCode == 0:
		return fmt.Errorf("retained proof result producer exit was zero")
	case result.VerificationRef != "" || pending.VerificationRef != "":
		return fmt.Errorf("retained proof result carries a verification reference")
	case pending.TerminalStatus != runattempt.StatusFailed:
		return fmt.Errorf("retained proof completion terminal status %q is not a failure", pending.TerminalStatus)
	}
	return nil
}

// validateRetainedFailedProofBarriers authenticates the native pending, attempt, lineage and
// holder barriers of the dedicated failure route BEFORE any recovery effect, so a missing or
// malformed seal and an invalid or still-active attempt can never reach the owned report
// publication or any native mutation.
//
// The dedicated route requires an EXISTING, valid completion governance seal. The ordinary
// legacy normalization of a missing seal stays untouched for every other path.
func (s *Store) validateRetainedFailedProofBarriers(
	workID, actor string,
	reservation ProofResourceReservation,
	pending proofResourceCompletionPending,
) error {
	if pending.GovernanceSeal == "" {
		return fmt.Errorf("retained failed proof recovery requires an existing completion governance seal")
	}
	seal, err := proofResourceCompletionGovernanceSeal(pending.Action, pending.Decision)
	if err != nil {
		return fmt.Errorf("retained failed proof completion governance seal cannot be computed: %w", err)
	}
	if seal != pending.GovernanceSeal {
		return fmt.Errorf("retained failed proof completion governance seal does not match its action and decision")
	}
	if err := pending.Action.Validate(); err != nil {
		return fmt.Errorf("retained failed proof completion action is invalid: %w", err)
	}
	if err := pending.Decision.Validate(); err != nil {
		return fmt.Errorf("retained failed proof completion decision is invalid: %w", err)
	}
	if !policy.Allowed(pending.Decision) {
		return fmt.Errorf("retained failed proof completion decision is not allowing")
	}
	ra, err := s.runAttempts().Get(pending.Result.RunAttemptID)
	if err != nil {
		return fmt.Errorf("retained failed proof attempt cannot be read: %w", err)
	}
	if !runattempt.IsTerminal(ra.Status) {
		return fmt.Errorf("retained failed proof attempt %s is still active", ra.ID)
	}
	if ra.Status != runattempt.StatusFailed {
		return fmt.Errorf("retained failed proof attempt %s status %q is not a terminal failure", ra.ID, ra.Status)
	}
	if ra.WorkID != workID ||
		ra.Actor != actor ||
		ra.Harness != proofResourceHarness ||
		ra.Origin != runattempt.OriginHarnessExecution {
		return fmt.Errorf("retained failed proof attempt %s is not the reserved same-work execution", ra.ID)
	}
	if ra.Workspace != reservation.Workspace || ra.Command != reservation.Command {
		return fmt.Errorf("retained failed proof attempt %s does not carry the reserved workspace and command", ra.ID)
	}
	if ra.ActionIntentID != reservation.ActionIntentID && ra.ActionIntentID != pending.Action.ID {
		return fmt.Errorf("retained failed proof attempt %s is not bound to the reserved action intent", ra.ID)
	}
	if ra.ID != reservation.RunAttemptID && ra.PredecessorID == "" {
		return fmt.Errorf("retained failed proof retry attempt %s has no predecessor", ra.ID)
	}
	return nil
}

// verifyRetainedPendingSHA256 fences the retained completion journal by its exact bytes. It runs
// at admission and again inside the guard that performs the recovery effects, before any
// report, custody, rearm or attempt mutation.
func (s *Store) verifyRetainedPendingSHA256(reservationID, expected string) error {
	expected = strings.ToLower(strings.TrimSpace(expected))
	if expected == "" {
		return nil
	}
	sum, err := s.proofResourceCompletionPendingSHA256(reservationID)
	if err != nil {
		return err
	}
	if sum != expected {
		return fmt.Errorf("retained proof completion journal %s does not match the expected pending sha256", reservationID)
	}
	return nil
}

// proofResourceCompletionPendingSHA256 hashes the exact retained completion journal bytes.
func (s *Store) proofResourceCompletionPendingSHA256(reservationID string) (string, error) {
	path, err := s.proofResourceCompletionPendingPath(reservationID)
	if err != nil {
		return "", err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read retained proof completion journal: %w", err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// RunProofRecoveryCLI dispatches the proof recovery-only routes. It never admits a battery.
func RunProofRecoveryCLI(
	args []string,
	stdout, stderr io.Writer,
	restore func(homePath, artifactRef, outPath string) (ProofCheckReport, error),
) int {
	if HasProofRecoveryFlag(args, "--resume-retained") {
		if HasProofRecoveryFlag(args, "--reconcile-quiescent") {
			fmt.Fprintln(stderr, "proof-check: --resume-retained and --reconcile-quiescent are mutually exclusive")
			return 2
		}
		return RunRetainedFailedProofRecoveryCLI(args, stdout, stderr, restore)
	}
	return RunQuiescentProofRecoveryCLI(args, stdout, stderr)
}

// HasProofRecoveryFlag detects the EXACT recovery-only tokens. Both "--resume-retained PFR" and
// "--resume-retained=PFR" select the retained route, and both enter it BEFORE any source capture,
// plan rebuilding or current-snapshot validation. A longer token that merely starts with the same
// characters is not a recovery flag.
func HasProofRecoveryFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}

// RunRetainedFailedProofRecoveryCLI finishes an exact retained failed proof without a battery.
// The retained report is restored through the caller's owned output custody before the
// exclusive holder is released; the verdict stays a failed battery.
func RunRetainedFailedProofRecoveryCLI(
	args []string,
	stdout, stderr io.Writer,
	restore func(homePath, artifactRef, outPath string) (ProofCheckReport, error),
) int {
	flags := flag.NewFlagSet("proof-check --resume-retained", flag.ContinueOnError)
	flags.SetOutput(stderr)
	reservation := flags.String("resume-retained", "", "exact retained proof reservation")
	workID := flags.String("work", "", "owned work")
	actor := flags.String("actor", "", "current actor")
	expected := flags.String("expected-pending-sha256", "", "expected sha256 of the retained completion journal bytes")
	outPath := flags.String("out", "", "path that receives the restored original failed report")
	homeArg := flags.String("home", "", "owning home")
	jsonOut := flags.Bool("json", false, "emit JSON")
	seen := map[string]bool{}
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			key := strings.SplitN(strings.TrimLeft(arg, "-"), "=", 2)[0]
			if seen[key] {
				fmt.Fprintln(stderr, "duplicate recovery option:", key)
				return 2
			}
			seen[key] = true
		}
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 ||
		strings.TrimSpace(*reservation) == "" ||
		strings.TrimSpace(*workID) == "" ||
		strings.TrimSpace(*actor) == "" ||
		strings.TrimSpace(*expected) == "" ||
		strings.TrimSpace(*outPath) == "" {
		fmt.Fprintln(stderr, "exact --resume-retained, --work, --actor, --expected-pending-sha256 and --out required")
		return 2
	}
	if restore == nil {
		fmt.Fprintln(stderr, "retained failed proof recovery requires an owned report restorer")
		return 2
	}
	resolvedOut, err := filepath.Abs(*outPath)
	if err != nil {
		fmt.Fprintln(stderr, "proof-check: resolve --out path:", err)
		return 2
	}
	root, err := home.Resolve(*homeArg)
	if err != nil {
		fmt.Fprintln(stderr, "proof-check:", err)
		return 1
	}
	store, err := Open(root)
	if err != nil {
		fmt.Fprintln(stderr, "proof-check:", err)
		return 1
	}
	var report ProofCheckReport
	outcome, found, err := store.ResumeRetainedFailedProofCompletion(*workID, ProofResourceRetainedFailureInput{
		Actor:                 *actor,
		ReservationID:         *reservation,
		ExpectedPendingSHA256: *expected,
		PrepareOwnedReport: func(result ProofResourceResult) error {
			restored, restoreErr := restore(root, result.ReportArtifactRef, resolvedOut)
			if restoreErr != nil {
				return restoreErr
			}
			report = restored
			return nil
		},
	}, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "proof-check:", err)
		return 2
	}
	if !found {
		fmt.Fprintln(stderr, "proof-check: no exact retained failed proof completion matches the requested reservation")
		return 2
	}
	if outcome.ReportArtifactRef != "" && report.SchemaVersion == "" {
		report, err = readRetainedProofCheckReport(root, outcome.ReportArtifactRef)
		if err != nil {
			fmt.Fprintln(stderr, "proof-check:", err)
			return 2
		}
	}
	state := "recovered"
	if *jsonOut {
		body, marshalErr := json.Marshal(report)
		if marshalErr != nil {
			fmt.Fprintln(stderr, "proof-check:", marshalErr)
			return 2
		}
		fmt.Fprintln(stdout, string(body))
	}
	fmt.Fprintln(stdout, ProofCheckVerdictLine(report, resolvedOut)+" retained-failed-proof="+state)
	return 1
}

// readRetainedProofCheckReport decodes the immutable owned report artifact of a retained proof.
func readRetainedProofCheckReport(homePath, artifactRef string) (ProofCheckReport, error) {
	artifacts, err := home.NewArtifactStore(homePath)
	if err != nil {
		return ProofCheckReport{}, err
	}
	body, err := artifacts.Get(strings.TrimSpace(artifactRef))
	if err != nil {
		return ProofCheckReport{}, fmt.Errorf("read retained proof report: %w", err)
	}
	var report ProofCheckReport
	if err := json.Unmarshal(body, &report); err != nil {
		return ProofCheckReport{}, fmt.Errorf("decode retained proof report: %w", err)
	}
	if !IsAcceptedProofCheckSchemaVersion(report.SchemaVersion, ProofCheckSchemaVersion) {
		return ProofCheckReport{}, fmt.Errorf("retained proof report schema_version %q is unsupported", report.SchemaVersion)
	}
	return report, nil
}
