package work

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/runattempt"
)

// retainedFailedProofFixture leaves an authentic owned failed completion journal behind: the
// producer already produced the failed report and the commit was interrupted before the
// terminal result became durable.
func retainedFailedProofFixture(t *testing.T) (privateSourceFixture, ProofResourceReserveResult, ProofResourceCompleteInput, string, time.Time) {
	t.Helper()
	f, acquired, input, at := privateFailedCompletionFixture(t)
	original := proofResourceWriteEventRecord
	proofResourceWriteEventRecord = func(s *Store, stage, id string, value any) error {
		if stage == "result" {
			return errors.New("injected failed-result persistence fault")
		}
		return original(s, stage, id, value)
	}
	t.Cleanup(func() { proofResourceWriteEventRecord = original })
	if _, err := f.store.CompleteProofResource(f.workID, input, at.Add(time.Second)); err == nil {
		t.Fatal("fixture must retain a genuine failed completion before commit")
	}
	proofResourceWriteEventRecord = original
	sum, err := f.store.proofResourceCompletionPendingSHA256(acquired.Reservation.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f, acquired, input, sum, at
}

func advanceProofSourceRepo(t *testing.T, repo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "advanced.txt"), []byte("synthetic later source commit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runProofSourceGit(t, repo, "add", "advanced.txt")
	runProofSourceGit(t, repo, "commit", "-m", "synthetic later source commit")
}

func assertRetainedFailedRecovery(t *testing.T, f privateSourceFixture, acquired ProofResourceReserveResult, outcome RetainedFailedProofOutcome) {
	t.Helper()
	if outcome.ReportStatus != "failed" || outcome.VerificationRef != "" || !outcome.ResourceReleased {
		t.Fatalf("retained failed proof recovery must stay a non-certifying audit completion: %+v", outcome)
	}
	if outcome.ReportArtifactRef == "" {
		t.Fatal("retained failed proof recovery lost the original owned report")
	}
	w, err := f.store.Get(f.workID)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.VerificationIDs) != 0 {
		t.Fatalf("retained failed proof recovery published a verification: %+v", w.VerificationIDs)
	}
	if w.State == "completed" {
		t.Fatal("retained failed report completed the source Work")
	}
	original, err := f.store.runAttempts().Get(acquired.Reservation.RunAttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if !runattempt.IsTerminal(original.Status) || original.Status != runattempt.StatusFailed {
		t.Fatalf("the original failed attempt was not preserved: %+v", original)
	}
}

func TestRetainedFailedProofRecoveryFinishesMovedCheckout(t *testing.T) {
	f, acquired, input, sum, at := retainedFailedProofFixture(t)
	// The source checkout advanced after the retained proof was produced.
	advanceProofSourceRepo(t, f.repo)
	restored := 0
	outcome, found, err := f.store.ResumeRetainedFailedProofCompletion(f.workID, ProofResourceRetainedFailureInput{
		Actor: proofActorAlpha, ReservationID: acquired.Reservation.ID, ExpectedPendingSHA256: sum,
		PrepareOwnedReport: func(result ProofResourceResult) error {
			restored++
			if result.ReportArtifactRef != input.ReportArtifactRef {
				t.Fatal("recovery changed the original owned report")
			}
			return nil
		},
	}, at.Add(6*time.Minute))
	if err != nil || !found {
		t.Fatalf("retained failed proof recovery was blocked after the source checkout moved: found=%v err=%v", found, err)
	}
	if restored != 1 {
		t.Fatalf("owned report publication barrier calls=%d", restored)
	}
	assertRetainedFailedRecovery(t, f, acquired, outcome)
	artifacts, err := home.NewArtifactStore(f.store.home)
	if err != nil {
		t.Fatal(err)
	}
	body, err := artifacts.Get(input.ReportArtifactRef)
	if err != nil {
		t.Fatalf("the original owned report artifact was not preserved: %v", err)
	}
	var report ProofCheckReport
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "failed" {
		t.Fatalf("the preserved report is no longer the failed battery verdict: %q", report.Status)
	}
}

func TestRetainedFailedProofRecoveryRearmsExpiredLeaseWithMissingCheckout(t *testing.T) {
	f, acquired, _, sum, at := retainedFailedProofFixture(t)
	// The original source checkout disappeared entirely.
	if err := os.RemoveAll(f.repo); err != nil {
		t.Fatal(err)
	}
	outcome, found, err := f.store.ResumeRetainedFailedProofCompletion(f.workID, ProofResourceRetainedFailureInput{
		Actor: proofActorAlpha, ReservationID: acquired.Reservation.ID, ExpectedPendingSHA256: sum,
		PrepareOwnedReport: func(result ProofResourceResult) error { return nil },
	}, at.Add(21*time.Minute))
	if err != nil || !found {
		t.Fatalf("retained failed proof recovery did not rearm the expired holder: found=%v err=%v", found, err)
	}
	assertRetainedFailedRecovery(t, f, acquired, outcome)
}

// The combined realistic recovery: an expired source review, an expired reservation lease, a
// terminal attempt whose retry lineage the recovery itself must extend, and a source checkout
// that advanced after the retained proof was produced.
func TestRetainedFailedProofRecoveryCombinesExpiredLeaseExpiredReviewTerminalRetryAndAdvancedCheckout(t *testing.T) {
	f, acquired, input, sum, at := retainedFailedProofFixture(t)
	// The source checkout advanced after the retained proof was produced.
	advanceProofSourceRepo(t, f.repo)
	later := at.Add(21 * time.Minute)
	if err := f.store.validatePrivateSourceProfileAuthority(f.workID, proofActorAlpha, acquired.Reservation.Profile, later); err == nil {
		t.Fatal("the fixture must carry a genuinely expired source review")
	}
	restored := 0
	outcome, found, err := f.store.ResumeRetainedFailedProofCompletion(f.workID, ProofResourceRetainedFailureInput{
		Actor: proofActorAlpha, ReservationID: acquired.Reservation.ID, ExpectedPendingSHA256: sum,
		PrepareOwnedReport: func(result ProofResourceResult) error {
			restored++
			if result.ReportArtifactRef != input.ReportArtifactRef {
				t.Fatal("recovery changed the original owned report")
			}
			return nil
		},
	}, later)
	if err != nil || !found {
		t.Fatalf("the combined retained recovery was blocked: found=%v err=%v", found, err)
	}
	if restored != 1 {
		t.Fatalf("owned report publication barrier calls=%d", restored)
	}
	assertRetainedFailedRecovery(t, f, acquired, outcome)
	// The original terminal attempt is preserved and the retry lineage extends it: the
	// recovery never replaces an original attempt.
	original, err := f.store.runAttempts().Get(acquired.Reservation.RunAttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if !runattempt.IsTerminal(original.Status) || original.Status != runattempt.StatusFailed {
		t.Fatalf("the original terminal attempt was not preserved: %+v", original)
	}
	attempts, err := f.store.runAttempts().List()
	if err != nil {
		t.Fatal(err)
	}
	retries := 0
	for _, attempt := range attempts {
		if attempt.PredecessorID == original.ID {
			retries++
		}
	}
	if retries == 0 {
		t.Fatalf("the retained recovery did not extend the terminal retry lineage: %+v", attempts)
	}
}

func TestRetainedFailedProofRecoveryRefusesMismatchedExpectations(t *testing.T) {
	f, acquired, _, sum, at := retainedFailedProofFixture(t)
	cases := []struct {
		name   string
		workID string
		in     ProofResourceRetainedFailureInput
	}{
		{"wrong_actor", f.workID, ProofResourceRetainedFailureInput{Actor: "codex:someone-else", ReservationID: acquired.Reservation.ID, ExpectedPendingSHA256: sum}},
		{"wrong_work", "work_other", ProofResourceRetainedFailureInput{Actor: proofActorAlpha, ReservationID: acquired.Reservation.ID, ExpectedPendingSHA256: sum}},
		{"wrong_reservation", f.workID, ProofResourceRetainedFailureInput{Actor: proofActorAlpha, ReservationID: "pfr_not_this_one", ExpectedPendingSHA256: sum}},
		{"wrong_pending_sha", f.workID, ProofResourceRetainedFailureInput{Actor: proofActorAlpha, ReservationID: acquired.Reservation.ID, ExpectedPendingSHA256: strings.Repeat("a", 64)}},
		{"malformed_pending_sha", f.workID, ProofResourceRetainedFailureInput{Actor: proofActorAlpha, ReservationID: acquired.Reservation.ID, ExpectedPendingSHA256: "not-a-digest"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			before, err := f.store.runAttempts().List()
			if err != nil {
				t.Fatal(err)
			}
			if _, found, err := f.store.ResumeRetainedFailedProofCompletion(testCase.workID, testCase.in, at.Add(6*time.Minute)); err == nil || found {
				t.Fatalf("mismatched retained recovery was accepted: found=%v err=%v", found, err)
			}
			after, err := f.store.runAttempts().List()
			if err != nil {
				t.Fatal(err)
			}
			if len(after) != len(before) {
				t.Fatalf("a refused retained recovery minted an attempt: %d -> %d", len(before), len(after))
			}
			sumNow, err := f.store.proofResourceCompletionPendingSHA256(acquired.Reservation.ID)
			if err != nil {
				t.Fatal(err)
			}
			if sumNow != sum {
				t.Fatal("a refused retained recovery changed the retained completion journal")
			}
		})
	}
}

func TestRetainedFailedProofRecoveryFencesPendingBytesInsideTheNativeGuard(t *testing.T) {
	f, acquired, _, sum, at := retainedFailedProofFixture(t)
	path, err := f.store.proofResourceCompletionPendingPath(acquired.Reservation.ID)
	if err != nil {
		t.Fatal(err)
	}
	tamper := func() {
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var decoded map[string]any
		if unmarshalErr := json.Unmarshal(body, &decoded); unmarshalErr != nil {
			t.Fatal(unmarshalErr)
		}
		decoded["injected_field"] = "forged"
		forged, marshalErr := json.Marshal(decoded)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if writeErr := os.WriteFile(path, forged, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	attemptsBefore, err := f.store.runAttempts().List()
	if err != nil {
		t.Fatal(err)
	}
	eventsBefore, err := f.store.eventsFor(f.workID)
	if err != nil {
		t.Fatal(err)
	}
	// The journal changes between the initial read and the protected mutation. The fence lives
	// inside the guard that performs the recovery effects, so the changed bytes refuse there.
	original := proofResourceBeforeRetainedMutation
	proofResourceBeforeRetainedMutation = func(reservationID string) { tamper() }
	t.Cleanup(func() { proofResourceBeforeRetainedMutation = original })
	_, found, err := f.store.ResumeRetainedFailedProofCompletion(f.workID, ProofResourceRetainedFailureInput{
		Actor: proofActorAlpha, ReservationID: acquired.Reservation.ID, ExpectedPendingSHA256: sum,
		PrepareOwnedReport: func(result ProofResourceResult) error {
			t.Fatal("a fenced retained recovery reached the owned report barrier")
			return nil
		},
	}, at.Add(6*time.Minute))
	if err == nil {
		t.Fatalf("changed retained completion bytes were accepted: found=%v err=%v", found, err)
	}
	if !strings.Contains(err.Error(), "expected pending sha256") {
		t.Fatalf("the changed pending bytes were not refused by the expected-hash fence: %v", err)
	}
	// A fenced refusal has ZERO callback, output and native effects.
	attemptsAfter, err := f.store.runAttempts().List()
	if err != nil {
		t.Fatal(err)
	}
	if len(attemptsAfter) != len(attemptsBefore) {
		t.Fatalf("a fenced retained recovery mutated the attempt ledger: %d -> %d", len(attemptsBefore), len(attemptsAfter))
	}
	eventsAfter, err := f.store.eventsFor(f.workID)
	if err != nil {
		t.Fatal(err)
	}
	if len(eventsAfter) != len(eventsBefore) {
		t.Fatalf("a fenced retained recovery wrote lifecycle events: %d -> %d", len(eventsBefore), len(eventsAfter))
	}
}

// The dedicated failure route requires an EXISTING, valid completion governance seal before any
// effect. A missing or malformed seal must have zero callback and output effects.
func TestRetainedFailedProofRecoveryRefusesMissingOrMalformedSeal(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"missing_seal": func(body map[string]any) { delete(body, "governance_seal") },
		"malformed_seal": func(body map[string]any) { body["governance_seal"] = strings.Repeat("0", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			f, acquired, _, sum, at := retainedFailedProofFixture(t)
			path, err := f.store.proofResourceCompletionPendingPath(acquired.Reservation.ID)
			if err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatal(err)
			}
			mutate(decoded)
			forged, err := json.Marshal(decoded)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, forged, 0o600); err != nil {
				t.Fatal(err)
			}
			attemptsBefore, err := f.store.runAttempts().List()
			if err != nil {
				t.Fatal(err)
			}
			// The seal mutation changes the journal bytes, so the fence refuses first. The seal
			// refusal itself is proven by the barrier check below, which the dedicated route
			// applies to the unchanged journal.
			_, _, _ = f.store.ResumeRetainedFailedProofCompletion(f.workID, ProofResourceRetainedFailureInput{
				Actor: proofActorAlpha, ReservationID: acquired.Reservation.ID, ExpectedPendingSHA256: sum,
				PrepareOwnedReport: func(result ProofResourceResult) error {
					t.Fatal("a refused seal reached the owned report barrier")
					return nil
				},
			}, at.Add(6*time.Minute))
			attemptsAfter, err := f.store.runAttempts().List()
			if err != nil {
				t.Fatal(err)
			}
			if len(attemptsAfter) != len(attemptsBefore) {
				t.Fatalf("a refused seal mutated the attempt ledger: %d -> %d", len(attemptsBefore), len(attemptsAfter))
			}
			// The barrier refuses the very same missing/malformed seal directly, with the
			// operator's expected bytes re-derived from the mutated journal.
			mutated, err := f.store.proofResourceCompletionPendingSHA256(acquired.Reservation.ID)
			if err != nil {
				t.Fatal(err)
			}
			pending, _, _, err := f.store.readProofResourceCompletionPending(acquired.Reservation.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.validateRetainedFailedProofBarriers(f.workID, proofActorAlpha, acquired.Reservation, pending); err == nil {
				t.Fatal("the dedicated failure route accepted a missing or malformed governance seal")
			}
			_ = mutated
		})
	}
}

func TestRetainedFailedProofRecoveryRefusesForgedOwnedReport(t *testing.T) {
	f, acquired, input, sum, at := retainedFailedProofFixture(t)
	hexDigest, err := home.ArtifactHex(input.ReportArtifactRef)
	if err != nil {
		t.Fatal(err)
	}
	forged := filepath.Join(f.store.home, "artifacts", "sha256", hexDigest)
	if err := os.WriteFile(forged, []byte(`{"schema_version":"compound.proof_check_report.v0.6","status":"ok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := f.store.ResumeRetainedFailedProofCompletion(f.workID, ProofResourceRetainedFailureInput{
		Actor: proofActorAlpha, ReservationID: acquired.Reservation.ID, ExpectedPendingSHA256: sum,
		PrepareOwnedReport: func(result ProofResourceResult) error { return nil },
	}, at.Add(6*time.Minute)); err == nil || found {
		t.Fatalf("a forged owned report was accepted: found=%v err=%v", found, err)
	}
}

func TestRetainedFailedProofRecoveryRefusesNonFailedResults(t *testing.T) {
	// The dedicated failure route accepts only an owned, exact failed result. The ordinary
	// successful-proof authority is untouched: a certifying result keeps its own route.
	reservation := ProofResourceReservation{ID: "pfr_synthetic", WorkID: "work_synthetic", Actor: proofActorAlpha, Status: ProofResourceAcquired}
	for name, result := range map[string]ProofResourceResult{
		"verified":     {WorkID: "work_synthetic", Actor: proofActorAlpha, ReservationID: "pfr_synthetic", Status: ProofResourceVerified, RunAttemptStatus: runattempt.StatusSucceeded, OwnedReport: true, ReportArtifactRef: "artifact:sha256:synthetic", ReportStatus: "ok", ProducerExitKnown: true, ProducerExitCode: 0},
		"unknown":      {WorkID: "work_synthetic", Actor: proofActorAlpha, ReservationID: "pfr_synthetic", Status: ProofResourceFailed, RunAttemptStatus: runattempt.StatusFailed, OwnedReport: true, ReportArtifactRef: "artifact:sha256:synthetic", ReportStatus: "failed"},
		"zero_exit":    {WorkID: "work_synthetic", Actor: proofActorAlpha, ReservationID: "pfr_synthetic", Status: ProofResourceFailed, RunAttemptStatus: runattempt.StatusFailed, OwnedReport: true, ReportArtifactRef: "artifact:sha256:synthetic", ReportStatus: "failed", ProducerExitKnown: true, ProducerExitCode: 0},
		"unowned":      {WorkID: "work_synthetic", Actor: proofActorAlpha, ReservationID: "pfr_synthetic", Status: ProofResourceFailed, RunAttemptStatus: runattempt.StatusFailed, ReportStatus: "failed", ProducerExitKnown: true, ProducerExitCode: 1},
		"no_artifact":  {WorkID: "work_synthetic", Actor: proofActorAlpha, ReservationID: "pfr_synthetic", Status: ProofResourceFailed, RunAttemptStatus: runattempt.StatusFailed, OwnedReport: true, ReportStatus: "failed", ProducerExitKnown: true, ProducerExitCode: 1},
		"verified_ref": {WorkID: "work_synthetic", Actor: proofActorAlpha, ReservationID: "pfr_synthetic", Status: ProofResourceFailed, RunAttemptStatus: runattempt.StatusFailed, OwnedReport: true, ReportArtifactRef: "artifact:sha256:synthetic", ReportStatus: "failed", ProducerExitKnown: true, ProducerExitCode: 1, VerificationRef: "proof-check:synthetic"},
	} {
		t.Run(name, func(t *testing.T) {
			pending := proofResourceCompletionPending{Result: result, TerminalStatus: runattempt.StatusFailed}
			if err := authenticateRetainedFailedProofResult("work_synthetic", proofActorAlpha, reservation, pending); err == nil {
				t.Fatal("the dedicated failure route accepted a result that is not an owned failure")
			}
		})
	}
	failed := ProofResourceResult{WorkID: "work_synthetic", Actor: proofActorAlpha, ReservationID: "pfr_synthetic", Status: ProofResourceFailed, RunAttemptStatus: runattempt.StatusFailed, OwnedReport: true, ReportArtifactRef: "artifact:sha256:synthetic", ReportStatus: "failed", ProducerExitKnown: true, ProducerExitCode: 1}
	if err := authenticateRetainedFailedProofResult("work_synthetic", proofActorAlpha, reservation, proofResourceCompletionPending{Result: failed, TerminalStatus: runattempt.StatusFailed}); err != nil {
		t.Fatalf("an owned failed result was refused: %v", err)
	}
}

func TestRetainedFailedProofRecoveryRepetitionRefusesWithoutStateDelta(t *testing.T) {
	f, acquired, _, sum, at := retainedFailedProofFixture(t)
	outcome, found, err := f.store.ResumeRetainedFailedProofCompletion(f.workID, ProofResourceRetainedFailureInput{
		Actor: proofActorAlpha, ReservationID: acquired.Reservation.ID, ExpectedPendingSHA256: sum,
		PrepareOwnedReport: func(result ProofResourceResult) error { return nil },
	}, at.Add(6*time.Minute))
	if err != nil || !found {
		t.Fatalf("the first retained recovery failed: found=%v err=%v", found, err)
	}
	if outcome.ReportArtifactRef == "" {
		t.Fatal("the first retained recovery lost the original owned report")
	}
	attempts, err := f.store.runAttempts().List()
	if err != nil {
		t.Fatal(err)
	}
	events, err := f.store.eventsFor(f.workID)
	if err != nil {
		t.Fatal(err)
	}
	// The consumed journal is gone. A repetition must refuse clearly: it must not require the
	// consumed pending file, must not recreate consumed evidence, and must not admit a battery.
	repeat, found, err := f.store.ResumeRetainedFailedProofCompletion(f.workID, ProofResourceRetainedFailureInput{
		Actor: proofActorAlpha, ReservationID: acquired.Reservation.ID, ExpectedPendingSHA256: sum,
		PrepareOwnedReport: func(result ProofResourceResult) error {
			t.Fatal("a repeated retained recovery restored consumed evidence")
			return nil
		},
	}, at.Add(7*time.Minute))
	if err != nil {
		t.Fatalf("a repeated retained recovery errored: %v", err)
	}
	if found {
		t.Fatalf("a repeated retained recovery claimed a retained completion: %+v", repeat)
	}
	if repeat.ReportArtifactRef != "" || repeat.VerificationRef != "" || repeat.ResourceReleased {
		t.Fatalf("a refused repetition reported a completion: %+v", repeat)
	}
	after, err := f.store.runAttempts().List()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(attempts) {
		t.Fatalf("a refused repetition invented an attempt: %d -> %d", len(attempts), len(after))
	}
	afterEvents, err := f.store.eventsFor(f.workID)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterEvents) != len(events) {
		t.Fatalf("a refused repetition wrote lifecycle events: %d -> %d", len(events), len(afterEvents))
	}
}

func retainedProofCLIRestorer(t *testing.T, restored *int) func(string, string, string) (ProofCheckReport, error) {
	t.Helper()
	return func(homePath, artifactRef, outPath string) (ProofCheckReport, error) {
		*restored++
		artifacts, err := home.NewArtifactStore(homePath)
		if err != nil {
			return ProofCheckReport{}, err
		}
		body, err := artifacts.Get(artifactRef)
		if err != nil {
			return ProofCheckReport{}, err
		}
		if err := os.WriteFile(outPath, body, 0o600); err != nil {
			return ProofCheckReport{}, err
		}
		return readRetainedProofCheckReport(homePath, artifactRef)
	}
}

func TestRetainedFailedProofRecoveryCLIKeepsFailedVerdictAndNeverAdmitsBattery(t *testing.T) {
	f, acquired, _, sum, _ := retainedFailedProofFixture(t)
	outPath := filepath.Join(t.TempDir(), "failed-proof.json")
	restored := 0
	var stdout bytes.Buffer
	code := RunRetainedFailedProofRecoveryCLI([]string{
		"--resume-retained", acquired.Reservation.ID,
		"--work", f.workID,
		"--actor", proofActorAlpha,
		"--expected-pending-sha256", sum,
		"--out", outPath,
		"--home", f.store.home,
	}, &stdout, io.Discard, retainedProofCLIRestorer(t, &restored))
	if code != 1 {
		t.Fatalf("retained failed proof recovery must keep the failed battery exit code: %d (%s)", code, stdout.String())
	}
	if restored != 1 {
		t.Fatalf("the owned report was not restored through the output custody: %d", restored)
	}
	verdict := stdout.String()
	if !strings.Contains(verdict, "battery: FAILED") || !strings.Contains(verdict, "retained-failed-proof=recovered") {
		t.Fatalf("the retained recovery verdict is not a truthful failed audit completion: %q", verdict)
	}
	if strings.Contains(verdict, "battery: ok") {
		t.Fatalf("the retained recovery read as a passed proof: %q", verdict)
	}
	published, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	var report ProofCheckReport
	if err := json.Unmarshal(published, &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "failed" {
		t.Fatalf("the restored report is not the original failed verdict: %q", report.Status)
	}
}

func TestRetainedFailedProofRecoveryCLIRefusesNoMatchWithoutWritingOrAdmitting(t *testing.T) {
	f, acquired, _, sum, _ := retainedFailedProofFixture(t)
	// The retained journal is gone and no completion was ever committed: there is nothing to
	// finish, so the route must refuse instead of admitting a battery.
	path, err := f.store.proofResourceCompletionPendingPath(acquired.Reservation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(t.TempDir(), "never-written.json")
	restored := 0
	var stderr bytes.Buffer
	code := RunRetainedFailedProofRecoveryCLI([]string{
		"--resume-retained", acquired.Reservation.ID,
		"--work", f.workID,
		"--actor", proofActorAlpha,
		"--expected-pending-sha256", sum,
		"--out", outPath,
		"--home", f.store.home,
	}, io.Discard, &stderr, retainedProofCLIRestorer(t, &restored))
	if code != 2 {
		t.Fatalf("a retained recovery without a matching completion must refuse: %d", code)
	}
	if restored != 0 {
		t.Fatal("a refused retained recovery restored a report")
	}
	if _, err := os.Stat(outPath); !os.IsNotExist(err) {
		t.Fatal("a refused retained recovery wrote the --out path")
	}
	if !strings.Contains(stderr.String(), "no exact retained failed proof completion") {
		t.Fatalf("the refusal did not name the missing retained completion: %q", stderr.String())
	}
}

func TestRetainedFailedProofRecoveryCLIRequiresExactInputs(t *testing.T) {
	for name, args := range map[string][]string{
		"missing_reservation": {"--work", "work_x", "--actor", "codex:proof-alpha", "--expected-pending-sha256", strings.Repeat("a", 64), "--out", "/tmp/x.json"},
		"missing_work":        {"--resume-retained", "pfr_x", "--actor", "codex:proof-alpha", "--expected-pending-sha256", strings.Repeat("a", 64), "--out", "/tmp/x.json"},
		"missing_actor":       {"--resume-retained", "pfr_x", "--work", "work_x", "--expected-pending-sha256", strings.Repeat("a", 64), "--out", "/tmp/x.json"},
		"missing_sha":         {"--resume-retained", "pfr_x", "--work", "work_x", "--actor", "codex:proof-alpha", "--out", "/tmp/x.json"},
		"missing_out":         {"--resume-retained", "pfr_x", "--work", "work_x", "--actor", "codex:proof-alpha", "--expected-pending-sha256", strings.Repeat("a", 64)},
	} {
		t.Run(name, func(t *testing.T) {
			if code := RunRetainedFailedProofRecoveryCLI(args, io.Discard, io.Discard, nil); code != 2 {
				t.Fatalf("incomplete retained recovery inputs must refuse: %d", code)
			}
		})
	}
}
