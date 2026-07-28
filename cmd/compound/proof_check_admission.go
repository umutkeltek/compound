package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"compound/internal/authority/gitrepository"
	"compound/internal/home"
	"compound/internal/landlease"
	"compound/internal/work"
)

const (
	defaultProofCheckReservationTTL = 45 * time.Minute
	minimumProofCheckReservationTTL = 3 * time.Second
)

type proofCheckAdmissionInput struct {
	Context                context.Context
	HomePath               string
	WorkID                 string
	Actor                  string
	Workspace              string
	Command                string
	Profile                work.ProofResourceProfile
	ReportPath             string
	TTL                    time.Duration
	Steal                  bool
	StealReason            string
	TerminatedRunAttemptID string
	TerminationEvidenceRef string
	Now                    func() time.Time
}

type proofCheckRunner func() (proofCheckReport, error)
type proofCheckContextRunner func(context.Context) (proofCheckReport, error)

var proofCheckLegacyRecoverer = func(
	homePath string,
	workID string,
	in work.ProofResourceLegacyRecoveryInput,
	now time.Time,
) (work.ProofResourceLegacyRecoveryResult, bool, error) {
	store, err := work.Open(homePath)
	if err != nil {
		return work.ProofResourceLegacyRecoveryResult{}, false, err
	}
	return store.RecoverLegacyProofResourceCompletion(workID, in, now)
}

// proofCheckAfterRunnerContextCancel is a deterministic test boundary before barrier release.
var proofCheckAfterRunnerContextCancel = func() {}

func proofCheckNeedsAdmission(dryRun, ciOnly, ciConsole bool) bool {
	return !dryRun && !ciOnly && !ciConsole
}

// proofCheckGoCache gives each worktree a stable cache outside the repository. Sibling
// certifying lanes otherwise share the host Go cache and can evict entries while another
// battery is reading them. Per-run isolation would fix the race but make every battery cold;
// the worktree is the stable execution-lane identity that needs isolation.
func proofCheckGoCache(repo string) (string, bool) {
	if strings.TrimSpace(os.Getenv("COMPOUND_PROOF_SHARED_GOCACHE")) != "" {
		return "", false
	}
	if strings.TrimSpace(repo) == "" {
		return "", false
	}
	resolved, err := filepath.Abs(repo)
	if err != nil {
		return "", false
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256([]byte(resolved))
	dir := filepath.Join(base, "compound-proof-gocache", hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false
	}
	return dir, true
}

// runProofCheckWithAdmission is the canonical full-battery process boundary. ReserveProofResource
// must return before runner is called, so a conflicting request cannot enter runProofCheck or start
// even its first gate subprocess. Before reserving, an exact retained completion is resumed through
// its existing governed reservation and owned report without re-entering runner. A newly-produced
// report is placed in immutable Compound custody and finalized while the exact lease remains held.
func runProofCheckWithAdmission(
	in proofCheckAdmissionInput,
	runner proofCheckRunner,
) (proofCheckReport, work.ProofResourceCompleteResult, error) {
	if runner == nil {
		return proofCheckReport{}, work.ProofResourceCompleteResult{}, fmt.Errorf("proof-check runner required")
	}
	return runProofCheckWithAdmissionContext(in, func(context.Context) (proofCheckReport, error) {
		return runner()
	})
}

func runProofCheckWithAdmissionContext(
	in proofCheckAdmissionInput,
	runner proofCheckContextRunner,
) (proofCheckReport, work.ProofResourceCompleteResult, error) {
	if runner == nil {
		return proofCheckReport{}, work.ProofResourceCompleteResult{}, fmt.Errorf("proof-check runner required")
	}
	in.HomePath = strings.TrimSpace(in.HomePath)
	in.WorkID = strings.TrimSpace(in.WorkID)
	in.Actor = strings.TrimSpace(in.Actor)
	in.Workspace = strings.TrimSpace(in.Workspace)
	in.Command = strings.TrimSpace(in.Command)
	in.ReportPath = strings.TrimSpace(in.ReportPath)
	if in.Now == nil {
		in.Now = time.Now
	}
	if in.TTL <= 0 {
		in.TTL = defaultProofCheckReservationTTL
	}
	if in.TTL < minimumProofCheckReservationTTL {
		return proofCheckReport{}, work.ProofResourceCompleteResult{}, fmt.Errorf(
			"proof-check reservation TTL %s is unsafe; minimum is %s",
			in.TTL,
			minimumProofCheckReservationTTL,
		)
	}
	if in.Profile.SourceSnapshot != nil {
		if err := gitrepository.RevalidateSnapshot(in.Workspace, *in.Profile.SourceSnapshot); err != nil {
			return proofCheckReport{}, work.ProofResourceCompleteResult{}, err
		}
	}
	store, err := work.Open(in.HomePath)
	if err != nil {
		return proofCheckReport{}, work.ProofResourceCompleteResult{}, err
	}
	var preparedReport proofCheckReport
	resumed, found, resumeErr := store.ResumeProofResourceCompletion(in.WorkID, work.ProofResourceResumeInput{
		Actor:     in.Actor,
		Workspace: in.Workspace,
		Command:   in.Command,
		Profile:   in.Profile,
		PrepareOwnedReport: func(result work.ProofResourceResult) error {
			report, prepareErr := restoreOwnedProofCheckReport(
				in.HomePath,
				result.ReportArtifactRef,
				in.ReportPath,
			)
			preparedReport = report
			return prepareErr
		},
	}, in.Now())
	if found {
		priorReport := preparedReport
		if resumed.Result.ReportArtifactRef != "" && priorReport.SchemaVersion == "" {
			priorReport, err = readOwnedProofCheckReport(in.HomePath, resumed.Result.ReportArtifactRef)
			if err != nil {
				return proofCheckReport{}, resumed, errors.Join(resumeErr, err)
			}
		}
		if resumed.Result.ReportArtifactRef != "" {
			if priorReport.Status != resumed.Result.ReportStatus {
				return proofCheckReport{}, resumed, errors.Join(
					resumeErr,
					fmt.Errorf(
						"resumed owned report status %q does not match governed result %q",
						priorReport.Status,
						resumed.Result.ReportStatus,
					),
				)
			}
		}
		if resumeErr != nil {
			return priorReport, resumed, resumeErr
		}
		if resumed.Result.ReportArtifactRef == "" {
			return proofCheckReport{}, resumed, fmt.Errorf(
				"resumed prior proof failure without an owned report: %s",
				resumed.Result.FailureReason,
			)
		}
		return priorReport, resumed, nil
	}
	if resumeErr != nil {
		return proofCheckReport{}, work.ProofResourceCompleteResult{}, resumeErr
	}
	reserved, err := store.ReserveProofResource(in.WorkID, work.ProofResourceReserveInput{
		Actor:                  in.Actor,
		TTL:                    in.TTL,
		Workspace:              in.Workspace,
		Command:                in.Command,
		Profile:                in.Profile,
		Steal:                  in.Steal,
		StealReason:            in.StealReason,
		TerminatedRunAttemptID: in.TerminatedRunAttemptID,
		TerminationEvidenceRef: in.TerminationEvidenceRef,
	}, in.Now())
	if err != nil {
		return proofCheckReport{}, work.ProofResourceCompleteResult{}, err
	}

	if reserved.ExecutionBarrier == nil {
		return proofCheckReport{}, work.ProofResourceCompleteResult{}, fmt.Errorf("proof reservation did not return execution barrier")
	}
	defer reserved.ExecutionBarrier.Close()
	parentContext := in.Context
	if parentContext == nil {
		parentContext = context.Background()
	}
	runnerContext, cancelRunner := context.WithCancel(
		proofCheckContextWithExecutionBarrier(parentContext, reserved.ExecutionBarrier),
	)
	defer cancelRunner()
	stopHeartbeat, heartbeatErr := startProofCheckReservationHeartbeat(
		store,
		in,
		reserved.Reservation,
		cancelRunner,
	)
	report, runErr := runner(runnerContext)
	stopHeartbeat()
	if hbErr := heartbeatErr(); hbErr != nil {
		if runErr == nil {
			runErr = fmt.Errorf("proof resource heartbeat: %w", hbErr)
		} else {
			runErr = errors.Join(runErr, fmt.Errorf("proof resource heartbeat: %w", hbErr))
		}
	}
	// Stop every context-bound gate start before dropping the supervisor's execution-barrier
	// descriptor. A runner may have returned while one of its goroutines is still queued to spawn;
	// cancellation makes exec.CommandContext refuse that late start instead of letting it escape
	// after completion has attested the process tree clear.
	cancelRunner()
	proofCheckAfterRunnerContextCancel()
	// The supervisor's descriptor must close before governed completion starts.
	// Any background descendant that escaped its gate process still owns an
	// inherited copy, so the Work finalizer can attest the whole process tree
	// terminated while the exact lease holder remains authoritative.
	if barrierErr := reserved.ExecutionBarrier.Close(); barrierErr != nil {
		if runErr == nil {
			runErr = fmt.Errorf("close proof execution barrier: %w", barrierErr)
		} else {
			runErr = errors.Join(runErr, fmt.Errorf("close proof execution barrier: %w", barrierErr))
		}
	}
	if in.Profile.SourceSnapshot != nil {
		if sourceErr := gitrepository.RevalidateSnapshot(in.Workspace, *in.Profile.SourceSnapshot); sourceErr != nil {
			if runErr == nil {
				runErr = sourceErr
			} else {
				runErr = errors.Join(runErr, sourceErr)
			}
		}
	}
	if runErr != nil {
		completed, completeErr := store.CompleteProofResource(in.WorkID, work.ProofResourceCompleteInput{
			Actor:         in.Actor,
			ReservationID: reserved.Reservation.ID,
			FailureReason: runErr.Error(),
		}, in.Now())
		if completeErr != nil {
			return report, completed, errors.Join(runErr, completeErr)
		}
		return report, completed, runErr
	}

	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		failed, failErr := failProofCheckAdmission(store, in, reserved.Reservation.ID, err)
		return report, failed, failErr
	}
	body = append(body, '\n')
	if in.ReportPath != "" {
		ownedBytes, readErr := os.ReadFile(in.ReportPath)
		if readErr != nil {
			failed, failErr := failProofCheckAdmission(store, in, reserved.Reservation.ID, readErr)
			return report, failed, failErr
		}
		if !bytes.Equal(ownedBytes, body) {
			cause := fmt.Errorf("owned --out report differs from internally produced report")
			failed, failErr := failProofCheckAdmission(store, in, reserved.Reservation.ID, cause)
			return report, failed, failErr
		}
		body = ownedBytes
	}
	artifacts, err := home.NewArtifactStore(in.HomePath)
	if err != nil {
		failed, failErr := failProofCheckAdmission(store, in, reserved.Reservation.ID, err)
		return report, failed, failErr
	}
	reportRef, err := artifacts.Put(body)
	if err != nil {
		failed, failErr := failProofCheckAdmission(store, in, reserved.Reservation.ID, err)
		return report, failed, failErr
	}
	exitCode := 0
	if report.Status != "ok" {
		exitCode = 1
	}
	completed, err := store.CompleteProofResource(in.WorkID, work.ProofResourceCompleteInput{
		Actor:             in.Actor,
		ReservationID:     reserved.Reservation.ID,
		ReportArtifactRef: reportRef,
		ProducerExitKnown: true,
		ProducerExitCode:  exitCode,
	}, in.Now())
	return report, completed, err
}

func restoreOwnedProofCheckReport(homePath, artifactRef, reportPath string) (proofCheckReport, error) {
	artifactRef = strings.TrimSpace(artifactRef)
	reportPath = strings.TrimSpace(reportPath)
	if artifactRef == "" || reportPath == "" {
		return proofCheckReport{}, fmt.Errorf("owned proof artifact and --out path required")
	}
	artifacts, err := home.NewArtifactStore(homePath)
	if err != nil {
		return proofCheckReport{}, err
	}
	body, err := artifacts.Get(artifactRef)
	if err != nil {
		return proofCheckReport{}, fmt.Errorf("read retained proof report: %w", err)
	}
	report, err := decodeOwnedProofCheckReport(body)
	if err != nil {
		return proofCheckReport{}, err
	}
	published, readErr := os.ReadFile(reportPath)
	if readErr == nil && bytes.Equal(published, body) {
		return report, nil
	}
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return report, readErr
	}
	if err := proofCheckWriteReportBytes(reportPath, body); err != nil {
		return report, fmt.Errorf("restore retained --out: %w", err)
	}
	published, err = os.ReadFile(reportPath)
	if err != nil {
		return report, fmt.Errorf("verify restored --out: %w", err)
	}
	if !bytes.Equal(published, body) {
		return report, fmt.Errorf("verify restored --out: published bytes differ from immutable artifact")
	}
	return report, nil
}

func readOwnedProofCheckReport(homePath, artifactRef string) (proofCheckReport, error) {
	artifacts, err := home.NewArtifactStore(homePath)
	if err != nil {
		return proofCheckReport{}, err
	}
	body, err := artifacts.Get(strings.TrimSpace(artifactRef))
	if err != nil {
		return proofCheckReport{}, fmt.Errorf("read retained proof report: %w", err)
	}
	return decodeOwnedProofCheckReport(body)
}

func decodeOwnedProofCheckReport(body []byte) (proofCheckReport, error) {
	var report proofCheckReport
	if err := json.Unmarshal(body, &report); err != nil {
		return proofCheckReport{}, fmt.Errorf("decode retained proof report: %w", err)
	}
	if report.SchemaVersion != "compound.proof_check_report.v0.1" &&
		report.SchemaVersion != "compound.proof_check_report.v0.2" &&
		report.SchemaVersion != proofCheckSchemaVersion {
		return proofCheckReport{}, fmt.Errorf(
			"retained proof report schema_version %q is unsupported (current %q)",
			report.SchemaVersion,
			proofCheckSchemaVersion,
		)
	}
	return report, nil
}

func failProofCheckAdmission(
	store *work.Store,
	in proofCheckAdmissionInput,
	reservationID string,
	cause error,
) (work.ProofResourceCompleteResult, error) {
	completed, completeErr := store.CompleteProofResource(in.WorkID, work.ProofResourceCompleteInput{
		Actor:         in.Actor,
		ReservationID: reservationID,
		FailureReason: cause.Error(),
	}, in.Now())
	if completeErr != nil {
		return completed, errors.Join(cause, completeErr)
	}
	return completed, cause
}

func startProofCheckReservationHeartbeat(
	store *work.Store,
	in proofCheckAdmissionInput,
	reservation work.ProofResourceReservation,
	cancelRunner context.CancelFunc,
) (stop func(), result func() error) {
	interval := in.TTL / 3
	if interval < time.Second {
		interval = time.Second
	}
	if interval > 5*time.Second {
		interval = 5 * time.Second
	}
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	var once sync.Once
	var heartbeatErr error
	go func() {
		defer close(doneCh)
		refreshTicker := time.NewTicker(interval)
		defer refreshTicker.Stop()
		holderTicker := time.NewTicker(250 * time.Millisecond)
		defer holderTicker.Stop()
		fail := func(err error) {
			heartbeatErr = err
			if cancelRunner != nil {
				cancelRunner()
			}
		}
		for {
			select {
			case <-refreshTicker.C:
				if _, err := store.RefreshProofResource(in.WorkID, reservation.ID, in.Actor, in.TTL, in.Now()); err != nil {
					fail(err)
					return
				}
			case <-holderTicker.C:
				status, err := landlease.Status(in.HomePath, in.Now())
				if err != nil {
					fail(err)
					return
				}
				if status.Holder == reservation.Holder && status.TerminationRequestedBy != "" {
					fail(fmt.Errorf(
						"proof reservation %s received governed termination request from %q: %s",
						reservation.ID,
						status.TerminationRequestedBy,
						status.TerminationRequestReason,
					))
					return
				}
				if !status.Held || status.Holder != reservation.Holder {
					fail(fmt.Errorf(
						"proof reservation %s lost exclusive holder %q; current holder %q",
						reservation.ID,
						reservation.Holder,
						status.Holder,
					))
					return
				}
			case <-stopCh:
				return
			}
		}
	}()
	stop = func() {
		once.Do(func() {
			close(stopCh)
			<-doneCh
		})
	}
	result = func() error {
		stop()
		return heartbeatErr
	}
	return stop, result
}
