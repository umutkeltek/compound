package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/landlease"
	"compound/internal/runattempt"
	"compound/internal/work"
)

const proofCheckTransportCLISubEnv = "COMPOUND_PROOF_TRANSPORT_CLI_SUB"
const proofCheckReleasePendingCLISubEnv = "COMPOUND_PROOF_RELEASE_PENDING_CLI_SUB"
const proofCheckFailedCanonicalCLISubEnv = "COMPOUND_PROOF_FAILED_CANONICAL_CLI_SUB"
const proofCheckFailedReleaseCLISubEnv = "COMPOUND_PROOF_FAILED_RELEASE_CLI_SUB"
const proofCheckPostReportErrorCLISubEnv = "COMPOUND_PROOF_POST_REPORT_ERROR_CLI_SUB"
const proofCheckUnsafeTTLCLISubEnv = "COMPOUND_PROOF_UNSAFE_TTL_CLI_SUB"
const proofCheckMinimumTTLCLISubEnv = "COMPOUND_PROOF_MINIMUM_TTL_CLI_SUB"
const proofCheckBarrierChildSubEnv = "COMPOUND_PROOF_BARRIER_CHILD_SUB"
const proofCheckArtifactPublishFailureSubEnv = "COMPOUND_PROOF_ARTIFACT_PUBLISH_FAILURE_SUB"
const proofCheckSignalForwardSubEnv = "COMPOUND_PROOF_SIGNAL_FORWARD_SUB"

func TestProofCheckCommandSignalCancelsAndWaitsForWholeGateGroup(t *testing.T) {
	if os.Getenv(proofCheckSignalForwardSubEnv) == "1" {
		proofCheckCanonicalRunner = func(
			ctx context.Context,
			repoPath string,
			dryRun bool,
			baseRef string,
			ciOnly bool,
			ciConsole bool,
			now time.Time,
		) (proofCheckReport, error) {
			cmd := proofCheckCommandContext(
				ctx,
				"sh",
				"-c",
				`sleep 30 & child=$!; printf '%s\n' "$child" > "$COMPOUND_PROOF_SIGNAL_PID"; wait "$child"`,
			)
			cmd.Env = os.Environ()
			return proofCheckReport{}, cmd.Run()
		}
		cmdProofCheck([]string{
			"--repo", os.Getenv("COMPOUND_PROOF_SIGNAL_REPO"),
			"--work", os.Getenv("COMPOUND_PROOF_SIGNAL_WORK"),
			"--actor", "codex:signal-forward",
			"--out", os.Getenv("COMPOUND_PROOF_SIGNAL_OUT"),
		})
		os.Exit(0)
	}

	homePath := t.TempDir()
	now := time.Date(2026, 7, 24, 12, 45, 0, 0, time.UTC)
	if err := home.Init(homePath, "proof-signal-forward-test", now); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w := createClaimedProofWork(t, store, "signal cancels proof group", "codex:signal-forward", now)
	repoPath := t.TempDir()
	outPath := filepath.Join(t.TempDir(), "proof.json")
	pidPath := filepath.Join(t.TempDir(), "gate-child.pid")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProofCheckCommandSignalCancelsAndWaitsForWholeGateGroup$")
	cmd.Env = append(
		os.Environ(),
		proofCheckSignalForwardSubEnv+"=1",
		"COMPOUND_HOME="+homePath,
		"COMPOUND_PROOF_SIGNAL_REPO="+repoPath,
		"COMPOUND_PROOF_SIGNAL_WORK="+w.ID,
		"COMPOUND_PROOF_SIGNAL_OUT="+outPath,
		"COMPOUND_PROOF_SIGNAL_PID="+pidPath,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	pidDeadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(pidPath); err == nil {
			break
		}
		if time.Now().After(pidDeadline) {
			t.Fatal("timed out waiting for gate child pid")
		}
		time.Sleep(20 * time.Millisecond)
	}
	rawPID, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	var gatePID int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(rawPID)), "%d", &gatePID); err != nil {
		t.Fatalf("parse gate child pid %q: %v", rawPID, err)
	}
	if err := syscall.Kill(cmd.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("signal-interrupted proof-check exited green")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Kill(gatePID, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gate descendant pid %d remained after proof-check signal handling: %v", gatePID, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestProofCheckCommandInstalledRuntimePublicationFailureIsNonGreen(t *testing.T) {
	if os.Getenv(proofCheckArtifactPublishFailureSubEnv) == "1" {
		proofCheckCanonicalRunner = func(
			ctx context.Context,
			repoPath string,
			dryRun bool,
			baseRef string,
			ciOnly bool,
			ciConsole bool,
			now time.Time,
		) (proofCheckReport, error) {
			return injectedCanonicalProofCheckReport(repoPath, baseRef, now)
		}
		proofCheckRuntimeArtifactRecorder = func(string, proofCheckReport) (proofCheckReport, error) {
			return proofCheckReport{}, errors.New("synthetic installed-runtime publication fault")
		}
		cmdProofCheck([]string{
			"--repo", os.Getenv("COMPOUND_PROOF_ARTIFACT_FAILURE_REPO"),
			"--work", os.Getenv("COMPOUND_PROOF_ARTIFACT_FAILURE_WORK"),
			"--actor", "codex:artifact-publication",
			"--out", os.Getenv("COMPOUND_PROOF_ARTIFACT_FAILURE_OUT"),
		})
		os.Exit(0)
	}

	homePath := t.TempDir()
	now := time.Date(2026, 7, 24, 12, 30, 0, 0, time.UTC)
	if err := home.Init(homePath, "proof-artifact-publication-test", now); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w := createClaimedProofWork(t, store, "runtime artifact publication failure", "codex:artifact-publication", now)
	repoPath := t.TempDir()
	outPath := filepath.Join(t.TempDir(), "proof.json")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProofCheckCommandInstalledRuntimePublicationFailureIsNonGreen$")
	cmd.Env = append(
		os.Environ(),
		proofCheckArtifactPublishFailureSubEnv+"=1",
		"COMPOUND_HOME="+homePath,
		"COMPOUND_PROOF_ARTIFACT_FAILURE_REPO="+repoPath,
		"COMPOUND_PROOF_ARTIFACT_FAILURE_WORK="+w.ID,
		"COMPOUND_PROOF_ARTIFACT_FAILURE_OUT="+outPath,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		t.Fatalf("publication failure exit = %v, want 2; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "battery: FAILED post_report_error=true ") {
		t.Fatalf("publication failure last stdout = %q, want non-green post-report verdict; stdout=%q", last, stdout.String())
	}
	if got := strings.Count(stdout.String(), "battery:"); got != 1 {
		t.Fatalf("publication failure verdict count = %d, want one; stdout=%q", got, stdout.String())
	}
}

func TestProofCheckAdmissionSweepAndExpiryCannotBypassLiveProcessTree(t *testing.T) {
	if os.Getenv(proofCheckBarrierChildSubEnv) == "1" {
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}

	homePath := t.TempDir()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "proof-sweep-barrier-test", now); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	first := createClaimedProofWork(t, store, "swept proof predecessor", "codex:sweep-alpha", now)
	second := createClaimedProofWork(t, store, "sweep proof successor", "codex:sweep-beta", now.Add(time.Second))

	childStarted := make(chan *exec.Cmd, 1)
	firstDone := make(chan error, 1)
	go func() {
		_, _, runErr := runProofCheckWithAdmissionContext(proofCheckAdmissionInput{
			HomePath:  homePath,
			WorkID:    first.ID,
			Actor:     "codex:sweep-alpha",
			Workspace: "/synthetic/repo-alpha",
			Command:   "compound proof-check --out /synthetic/sweep-alpha.json",
			Profile:   syntheticAdmissionProfile(t, "/synthetic/repo-alpha"),
			TTL:       minimumProofCheckReservationTTL,
			Now:       func() time.Time { return now.Add(2 * time.Second) },
		}, func(ctx context.Context) (proofCheckReport, error) {
			cmd := proofCheckCommandContext(ctx, os.Args[0], "-test.run=^TestProofCheckAdmissionSweepAndExpiryCannotBypassLiveProcessTree$")
			cmd.Env = append(os.Environ(), proofCheckBarrierChildSubEnv+"=1")
			if err := cmd.Start(); err != nil {
				return proofCheckReport{}, err
			}
			childStarted <- cmd
			return proofCheckReport{}, cmd.Wait()
		})
		firstDone <- runErr
	}()
	child := <-childStarted
	t.Cleanup(func() {
		if child.Process != nil {
			_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
		}
		select {
		case <-firstDone:
		case <-time.After(5 * time.Second):
		}
	})

	firstWork, err := store.Get(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	proofRunID := firstWork.RunAttemptIDs[len(firstWork.RunAttemptIDs)-1]
	attempts := runattempt.Open(homePath)
	report, err := attempts.SweepStaleWithProbe(
		time.Second,
		now.Add(time.Minute),
		buildDelegateLivenessProbe(homePath),
	)
	if err != nil {
		t.Fatal(err)
	}
	swept, err := attempts.Get(proofRunID)
	if err != nil {
		t.Fatal(err)
	}
	if swept.Status != runattempt.StatusRunning || report.Protected != 1 {
		t.Fatalf("live proof sweep = status %q report %+v, want running and protected", swept.Status, report)
	}

	var successorStarts atomic.Int32
	_, _, err = runProofCheckWithAdmissionContext(proofCheckAdmissionInput{
		HomePath:  homePath,
		WorkID:    second.ID,
		Actor:     "codex:sweep-beta",
		Workspace: "/synthetic/repo-beta",
		Command:   "compound proof-check --out /synthetic/sweep-beta.json",
		Profile:   syntheticAdmissionProfile(t, "/synthetic/repo-beta"),
		TTL:       minimumProofCheckReservationTTL,
		Now:       func() time.Time { return now.Add(6 * time.Second) },
	}, func(context.Context) (proofCheckReport, error) {
		successorStarts.Add(1)
		return successfulProofCheckReport(), nil
	})
	if err == nil {
		t.Fatal("sweep and TTL expiry admitted successor while predecessor process tree was alive")
	}
	if successorStarts.Load() != 0 {
		t.Fatalf("successor runner starts = %d, want zero before predecessor group death", successorStarts.Load())
	}
}

func TestProofCheckGateProcessInheritsExecutionBarrier(t *testing.T) {
	if os.Getenv(proofCheckBarrierChildSubEnv) == "1" {
		time.Sleep(250 * time.Millisecond)
		os.Exit(0)
	}
	homePath := t.TempDir()
	now := time.Date(2026, 7, 24, 13, 0, 0, 0, time.UTC)
	_, barrier, err := landlease.AcquireWithExecutionBarrierPrepared(
		homePath,
		"proof:pfr_gate:ratt_20260724T130000Z_aaaaaaaa:work_gate:codex-gate",
		time.Second,
		now,
		func(landlease.AcquireOutcome) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := proofCheckContextWithExecutionBarrier(context.Background(), barrier)
	cmd := proofCheckCommandContext(ctx, os.Args[0], "-test.run=^TestProofCheckGateProcessInheritsExecutionBarrier$")
	cmd.Env = append(os.Environ(), proofCheckBarrierChildSubEnv+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := barrier.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := landlease.Acquire(homePath, "generic-landing", time.Minute, now.Add(2*time.Second)); err == nil {
		t.Fatal("gate subprocess did not inherit execution barrier after proof supervisor exited")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := landlease.Acquire(homePath, "generic-landing", time.Minute, now.Add(2*time.Second)); err != nil {
		t.Fatalf("barrier remained after full gate process exit: %v", err)
	}
}

func TestProofCheckAdmissionCancelsLateAsyncCommandBeforeBarrierRelease(t *testing.T) {
	homePath := t.TempDir()
	outPath := filepath.Join(t.TempDir(), "proof-report.json")
	markerPath := filepath.Join(t.TempDir(), "late-command-started")
	now := time.Date(2026, 7, 24, 13, 30, 0, 0, time.UTC)
	if err := home.Init(homePath, "proof-late-command-test", now); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	item := createClaimedProofWork(t, store, "cancel late proof command before attestation", "codex:late-command", now)
	input := proofCheckAdmissionInput{
		HomePath:   homePath,
		WorkID:     item.ID,
		Actor:      "codex:late-command",
		Workspace:  "/synthetic/repo-alpha",
		Command:    "compound proof-check --out " + outPath,
		Profile:    syntheticAdmissionProfile(t, "/synthetic/repo-alpha"),
		ReportPath: outPath,
		TTL:        time.Minute,
		Now:        func() time.Time { return now.Add(time.Second) },
	}

	lateDone := make(chan error, 1)
	var hookErr error
	var heldBeforeBarrierClose bool
	originalAfterCancel := proofCheckAfterRunnerContextCancel
	t.Cleanup(func() { proofCheckAfterRunnerContextCancel = originalAfterCancel })
	proofCheckAfterRunnerContextCancel = func() {
		lateErr := <-lateDone
		if !errors.Is(lateErr, context.Canceled) {
			hookErr = fmt.Errorf("late command error = %v, want context cancellation", lateErr)
			return
		}
		status, err := landlease.Status(homePath, input.Now())
		if err != nil {
			hookErr = err
			return
		}
		heldBeforeBarrierClose = status.Held && status.Holder != ""
	}

	report, completed, err := runProofCheckWithAdmissionContext(input, func(ctx context.Context) (proofCheckReport, error) {
		go func() {
			<-ctx.Done()
			cmd := proofCheckCommandContext(
				ctx,
				"sh",
				"-c",
				`printf started > "$1"`,
				"proof-late-command",
				markerPath,
			)
			lateDone <- cmd.Run()
		}()
		report := successfulProofCheckReport()
		report.GeneratedAt = now.Add(2 * time.Second).Format(time.RFC3339)
		if err := proofCheckWriteReportFile(outPath, report); err != nil {
			return proofCheckReport{}, err
		}
		return report, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	if !heldBeforeBarrierClose {
		t.Fatal("completion released or stopped holding before the cancelled async command attempt completed")
	}
	if report.Status != "ok" || !completed.ResourceReleased || completed.VerificationRef == "" {
		t.Fatalf("governed completion = report:%+v result:%+v", report, completed)
	}
	if _, err := os.Stat(markerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("late cancelled command created marker: %v", err)
	}
}

func TestProofCheckAdmissionRejectsSecondBeforeRunnerStarts(t *testing.T) {
	homePath := t.TempDir()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "proof-admission-test", now); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	first := createClaimedProofWork(t, store, "first governed proof", "codex:alpha", now)
	second := createClaimedProofWork(t, store, "second governed proof", "codex:beta", now.Add(time.Second))

	started := make(chan struct{})
	finish := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, _, runErr := runProofCheckWithAdmission(proofCheckAdmissionInput{
			HomePath:  homePath,
			WorkID:    first.ID,
			Actor:     "codex:alpha",
			Workspace: "/synthetic/repo-alpha",
			Command:   "compound proof-check --out /synthetic/report-alpha.json",
			Profile:   syntheticAdmissionProfile(t, "/synthetic/repo-alpha"),
			TTL:       time.Minute,
			Now:       func() time.Time { return now.Add(2 * time.Second) },
		}, func() (proofCheckReport, error) {
			close(started)
			<-finish
			return successfulProofCheckReport(), nil
		})
		firstDone <- runErr
	}()
	<-started

	var secondRunnerStarts atomic.Int32
	_, _, err = runProofCheckWithAdmission(proofCheckAdmissionInput{
		HomePath:  homePath,
		WorkID:    second.ID,
		Actor:     "codex:beta",
		Workspace: "/synthetic/repo-beta",
		Command:   "compound proof-check --out /synthetic/report-beta.json",
		Profile:   syntheticAdmissionProfile(t, "/synthetic/repo-beta"),
		TTL:       time.Minute,
		Now:       func() time.Time { return now.Add(3 * time.Second) },
	}, func() (proofCheckReport, error) {
		secondRunnerStarts.Add(1)
		return successfulProofCheckReport(), nil
	})
	var held *work.ProofResourceHeldError
	if !errors.As(err, &held) {
		t.Fatalf("second run error = %v, want proof resource held", err)
	}
	if secondRunnerStarts.Load() != 0 {
		t.Fatalf("conflicting runner started %d times before admission", secondRunnerStarts.Load())
	}

	close(finish)
	if err := <-firstDone; err != nil {
		t.Fatalf("first governed proof: %v", err)
	}
}

func TestProofCheckAdmissionStealCancelsPredecessorProcessBeforeSuccessorStarts(t *testing.T) {
	homePath := t.TempDir()
	now := time.Now().UTC()
	if err := home.Init(homePath, "proof-steal-cancellation-test", now); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homePath, "config", "policy.yaml"), []byte(`standing_grants:
  - id: operator_proof_resource_steal
    capability: proof.resource
    mutation_class: security
    reversibility: manual
    blast_radius: repo
    expires_at: "2027-01-01T00:00:00Z"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	first := createClaimedProofWork(t, store, "cancellable predecessor proof", "codex:cancel-first", now)
	second := createClaimedProofWork(t, store, "successor waits for process death", "codex:cancel-second", now.Add(time.Millisecond))
	firstProfile := syntheticAdmissionProfile(t, "/synthetic/repo-alpha")
	secondProfile := syntheticAdmissionProfile(t, "/synthetic/repo-beta")
	startedPath := filepath.Join(t.TempDir(), "predecessor-started")
	terminatedPath := filepath.Join(t.TempDir(), "predecessor-terminated")
	firstDone := make(chan error, 1)
	go func() {
		_, _, runErr := runProofCheckWithAdmissionContext(proofCheckAdmissionInput{
			HomePath:  homePath,
			WorkID:    first.ID,
			Actor:     "codex:cancel-first",
			Workspace: "/synthetic/repo-alpha",
			Command:   "compound proof-check --out /synthetic/report-alpha.json",
			Profile:   firstProfile,
			TTL:       30 * time.Second,
			Now:       time.Now,
		}, func(ctx context.Context) (proofCheckReport, error) {
			cmd := proofCheckCommandContext(ctx, "sh", "-c", "printf started > \"$1\"; while :; do sleep 1; done", "proof-predecessor", startedPath)
			runErr := cmd.Run()
			if err := os.WriteFile(terminatedPath, []byte("terminated\n"), 0o644); err != nil {
				return proofCheckReport{}, errors.Join(runErr, err)
			}
			return proofCheckReport{}, runErr
		})
		firstDone <- runErr
	}()
	waitForTestPath(t, startedPath, 3*time.Second)

	var successorStarts atomic.Int32
	_, _, secondErr := runProofCheckWithAdmissionContext(proofCheckAdmissionInput{
		HomePath:    homePath,
		WorkID:      second.ID,
		Actor:       "codex:cancel-second",
		Workspace:   "/synthetic/repo-beta",
		Command:     "compound proof-check --out /synthetic/report-beta.json --steal",
		Profile:     secondProfile,
		TTL:         30 * time.Second,
		Steal:       true,
		StealReason: "operator requested takeover after observing the predecessor process",
		Now:         time.Now,
	}, func(context.Context) (proofCheckReport, error) {
		successorStarts.Add(1)
		if _, err := os.Stat(terminatedPath); err != nil {
			return proofCheckReport{}, fmt.Errorf("successor overlapped predecessor process: %w", err)
		}
		return proofCheckReport{}, errors.New("synthetic successor stops after admission-order assertion")
	})
	if secondErr == nil {
		t.Fatal("synthetic successor unexpectedly completed")
	}
	if successorStarts.Load() != 0 {
		status, _ := landlease.Status(homePath, time.Now())
		t.Fatalf("successor runner starts = %d, want zero until predecessor termination; admission error=%v status=%+v", successorStarts.Load(), secondErr, status)
	}
	select {
	case firstErr := <-firstDone:
		if firstErr == nil || !strings.Contains(firstErr.Error(), "heartbeat") {
			t.Fatalf("predecessor completion error = %v, want audited holder-loss heartbeat failure", firstErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("predecessor runner did not terminate after lease loss")
	}
	if _, err := os.Stat(terminatedPath); err != nil {
		t.Fatalf("predecessor process termination sentinel missing: %v", err)
	}
	_, _, retryErr := runProofCheckWithAdmissionContext(proofCheckAdmissionInput{
		HomePath:    homePath,
		WorkID:      second.ID,
		Actor:       "codex:cancel-second",
		Workspace:   "/synthetic/repo-beta",
		Command:     "compound proof-check --out /synthetic/report-beta.json --steal",
		Profile:     secondProfile,
		TTL:         30 * time.Second,
		Steal:       true,
		StealReason: "operator requested takeover after observing the predecessor process",
		Now:         time.Now,
	}, func(context.Context) (proofCheckReport, error) {
		successorStarts.Add(1)
		if _, err := os.Stat(terminatedPath); err != nil {
			return proofCheckReport{}, fmt.Errorf("successor overlapped predecessor process: %w", err)
		}
		return proofCheckReport{}, errors.New("synthetic successor stops after admission-order assertion")
	})
	if retryErr == nil {
		t.Fatal("synthetic successor retry unexpectedly completed")
	}
	if successorStarts.Load() != 1 {
		t.Fatalf("successor retry runner starts = %d, want one after predecessor group termination; err=%v", successorStarts.Load(), retryErr)
	}
}

func TestProofCheckAdmissionExpiryRequiresGovernedTerminationEvidenceBeforeRunner(t *testing.T) {
	homePath := t.TempDir()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "proof-expiry-termination-test", now); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homePath, "config", "policy.yaml"), []byte(`standing_grants:
  - id: operator_proof_resource_steal
    capability: proof.resource
    mutation_class: security
    reversibility: manual
    blast_radius: repo
    expires_at: "2027-01-01T00:00:00Z"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	first := createClaimedProofWork(t, store, "expired stopped proof", "codex:expiry-first", now)
	second := createClaimedProofWork(t, store, "governed stale recovery", "codex:expiry-second", now.Add(time.Millisecond))
	firstProfile := syntheticAdmissionProfile(t, "/synthetic/repo-alpha")
	secondProfile := syntheticAdmissionProfile(t, "/synthetic/repo-beta")
	acquired, err := store.ReserveProofResource(first.ID, work.ProofResourceReserveInput{
		Actor:     "codex:expiry-first",
		TTL:       3 * time.Second,
		Workspace: "/synthetic/repo-alpha",
		Command:   "compound proof-check --out /synthetic/report-alpha.json",
		Profile:   firstProfile,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	var runnerStarts atomic.Int32
	input := proofCheckAdmissionInput{
		HomePath:  homePath,
		WorkID:    second.ID,
		Actor:     "codex:expiry-second",
		Workspace: "/synthetic/repo-beta",
		Command:   "compound proof-check --out /synthetic/report-beta.json",
		Profile:   secondProfile,
		TTL:       3 * time.Second,
		Now:       func() time.Time { return now.Add(5 * time.Second) },
	}
	_, _, err = runProofCheckWithAdmissionContext(input, func(context.Context) (proofCheckReport, error) {
		runnerStarts.Add(1)
		return proofCheckReport{}, errors.New("synthetic post-admission stop")
	})
	if err == nil {
		t.Fatalf("expired predecessor admission error = %v, want execution barrier", err)
	}
	if runnerStarts.Load() != 0 {
		t.Fatalf("expired predecessor started successor runner %d times", runnerStarts.Load())
	}

	if err := acquired.ExecutionBarrier.Close(); err != nil {
		t.Fatal(err)
	}
	input.Command += " --steal --terminated-run " + acquired.Reservation.RunAttemptID
	input.Steal = true
	input.StealReason = "predecessor process group was terminated and waited"
	input.TerminatedRunAttemptID = acquired.Reservation.RunAttemptID
	input.Now = func() time.Time { return now.Add(7 * time.Second) }
	_, _, err = runProofCheckWithAdmissionContext(input, func(context.Context) (proofCheckReport, error) {
		runnerStarts.Add(1)
		return proofCheckReport{}, errors.New("synthetic post-admission stop")
	})
	if err == nil || !strings.Contains(err.Error(), "synthetic post-admission stop") {
		t.Fatalf("governed termination recovery error = %v, want post-admission sentinel", err)
	}
	if runnerStarts.Load() != 1 {
		t.Fatalf("successor runner starts after governed termination = %d, want one", runnerStarts.Load())
	}
}

func TestProofCheckConflictPreflightPreservesOutTarget(t *testing.T) {
	homePath := t.TempDir()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "proof-conflict-out-test", now); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	first := createClaimedProofWork(t, store, "proof conflict holder", "codex:out-alpha", now)
	second := createClaimedProofWork(t, store, "proof conflict waiter", "codex:out-beta", now.Add(time.Second))
	if _, err := store.ReserveProofResource(first.ID, work.ProofResourceReserveInput{
		Actor:     "codex:out-alpha",
		TTL:       time.Minute,
		Workspace: "/synthetic/repo-alpha",
		Command:   "compound proof-check --out /synthetic/report-alpha.json",
		Profile:   syntheticAdmissionProfile(t, "/synthetic/repo-alpha"),
	}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}

	for i, tc := range []struct {
		name     string
		existing []byte
	}{
		{name: "nonexistent"},
		{name: "existing", existing: []byte("previous proof report remains authoritative\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outPath := filepath.Join(t.TempDir(), "proof.json")
			if tc.existing != nil {
				if err := os.WriteFile(outPath, tc.existing, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := proofCheckValidateOutPath(outPath); err != nil {
				t.Fatalf("preflight --out: %v", err)
			}
			var runnerStarts atomic.Int32
			_, _, err := runProofCheckWithAdmission(proofCheckAdmissionInput{
				HomePath:   homePath,
				WorkID:     second.ID,
				Actor:      "codex:out-beta",
				Workspace:  "/synthetic/repo-beta",
				Command:    "compound proof-check --out " + outPath,
				Profile:    syntheticAdmissionProfile(t, "/synthetic/repo-beta"),
				ReportPath: outPath,
				TTL:        time.Minute,
				Now:        func() time.Time { return now.Add(time.Duration(3+i) * time.Second) },
			}, func() (proofCheckReport, error) {
				runnerStarts.Add(1)
				return successfulProofCheckReport(), nil
			})
			var held *work.ProofResourceHeldError
			if !errors.As(err, &held) {
				t.Fatalf("conflicting proof = %v, want held refusal", err)
			}
			if runnerStarts.Load() != 0 {
				t.Fatalf("conflicting runner started %d times", runnerStarts.Load())
			}
			got, readErr := os.ReadFile(outPath)
			if tc.existing == nil {
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatalf("conflict created nonexistent --out: err=%v bytes=%q", readErr, got)
				}
			} else if readErr != nil || !bytes.Equal(got, tc.existing) {
				t.Fatalf("conflict changed existing --out: err=%v got=%q want=%q", readErr, got, tc.existing)
			}
		})
	}
}

func TestProofCheckAdmissionCompletionErrorLeavesNoRunningAttempt(t *testing.T) {
	homePath := t.TempDir()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "proof-terminal-return-test", now); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w := createClaimedProofWork(t, store, "completion error terminalizes run", "codex:terminal-return", now)
	workDir := filepath.Join(homePath, "work")
	_, _, runErr := runProofCheckWithAdmission(proofCheckAdmissionInput{
		HomePath:  homePath,
		WorkID:    w.ID,
		Actor:     "codex:terminal-return",
		Workspace: "/synthetic/repo-alpha",
		Command:   "compound proof-check --out /synthetic/report-alpha.json",
		Profile:   syntheticAdmissionProfile(t, "/synthetic/repo-alpha"),
		TTL:       time.Minute,
		Now:       func() time.Time { return now.Add(2 * time.Second) },
	}, func() (proofCheckReport, error) {
		if err := os.Chmod(workDir, 0o555); err != nil {
			return proofCheckReport{}, err
		}
		return successfulProofCheckReport(), nil
	})
	if err := os.Chmod(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if runErr == nil {
		t.Fatal("completion persistence fault unexpectedly succeeded")
	}
	after, err := store.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.RunAttemptIDs) == 0 {
		t.Fatal("completion fault omitted attached ratt_*")
	}
	ra, err := runattempt.Open(homePath).Get(after.RunAttemptIDs[len(after.RunAttemptIDs)-1])
	if err != nil {
		t.Fatal(err)
	}
	if !runattempt.IsTerminal(ra.Status) {
		t.Fatalf("runProofCheckWithAdmission returned error with %s still %q", ra.ID, ra.Status)
	}
}

func TestProofCheckAdmissionSurfacesCommittedButRetainedLease(t *testing.T) {
	homePath := t.TempDir()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "proof-retained-release-test", now); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w := createClaimedProofWork(t, store, "retained release is not plain green", "codex:retained-release", now)
	successorWork := createClaimedProofWork(t, store, "successor after public recovery", "codex:successor", now.Add(time.Second))
	stateDir := filepath.Join(homePath, "state")
	outPath := filepath.Join(t.TempDir(), "proof.json")
	currentNow := now.Add(2 * time.Second)
	runInput := proofCheckAdmissionInput{
		HomePath:   homePath,
		WorkID:     w.ID,
		Actor:      "codex:retained-release",
		Workspace:  "/synthetic/repo-alpha",
		Command:    "compound proof-check --out " + outPath,
		Profile:    syntheticAdmissionProfile(t, "/synthetic/repo-alpha"),
		ReportPath: outPath,
		TTL:        time.Minute,
		Now:        func() time.Time { return currentNow },
	}
	var runnerCalls atomic.Int32

	report, completed, runErr := runProofCheckWithAdmission(runInput, func() (proofCheckReport, error) {
		runnerCalls.Add(1)
		produced := successfulProofCheckReport()
		if err := proofCheckWriteReportFile(outPath, produced); err != nil {
			return proofCheckReport{}, err
		}
		if err := os.Chmod(stateDir, 0o555); err != nil {
			return proofCheckReport{}, err
		}
		return produced, nil
	})
	if err := os.Chmod(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if runErr == nil {
		t.Fatalf("retained lease returned plain success with report status %q", report.Status)
	}
	var releasePending *work.ProofResourceReleasePendingError
	if !errors.As(runErr, &releasePending) {
		t.Fatalf("admission error = %v, want typed release-pending signal", runErr)
	}
	if completed.VerificationRef == "" ||
		completed.ResourceReleased ||
		!completed.RecoveryJournalRetained {
		t.Fatalf("admission result did not expose retained release: %+v", completed)
	}
	status, err := landlease.Status(homePath, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !status.Held {
		t.Fatal("admission boundary reported retained release but lease was not observable")
	}

	beforeResumeOut, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeResumeWork, err := store.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	currentNow = now.Add(4 * time.Second)
	resumedReport, resumed, resumeErr := runProofCheckWithAdmission(runInput, func() (proofCheckReport, error) {
		runnerCalls.Add(1)
		return proofCheckReport{}, errors.New("proof runner must not restart during retained-release recovery")
	})
	if resumeErr != nil {
		t.Fatalf("same public command did not recover retained release: %v", resumeErr)
	}
	if runnerCalls.Load() != 1 {
		t.Fatalf("retained-release recovery restarted proof runner; calls=%d", runnerCalls.Load())
	}
	if !reflect.DeepEqual(resumedReport, report) {
		t.Fatalf("resumed report differs from prior owned report:\n got=%+v\nwant=%+v", resumedReport, report)
	}
	if resumed.Result.ID != completed.Result.ID ||
		resumed.Result.RunAttemptID != completed.Result.RunAttemptID ||
		resumed.VerificationRef != completed.VerificationRef ||
		!resumed.ResourceReleased ||
		resumed.RecoveryJournalRetained {
		t.Fatalf("resumed result did not truthfully finalize prior completion: first=%+v resumed=%+v", completed, resumed)
	}
	afterResumeOut, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterResumeOut, beforeResumeOut) {
		t.Fatal("retained-release recovery rewrote the prior owned --out")
	}
	status, err = landlease.Status(homePath, currentNow)
	if err != nil {
		t.Fatal(err)
	}
	if status.Held {
		t.Fatalf("public recovery retained scarce holder: %+v", status)
	}
	pendingEntries, err := os.ReadDir(filepath.Join(homePath, "work", "proof-resource-completions"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingEntries) != 0 {
		t.Fatalf("public recovery retained completion journal entries: %v", pendingEntries)
	}
	finalWork, err := store.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(finalWork.RunAttemptIDs) != len(beforeResumeWork.RunAttemptIDs) {
		t.Fatalf("public recovery minted a new RunAttempt: before=%v after=%v", beforeResumeWork.RunAttemptIDs, finalWork.RunAttemptIDs)
	}
	attempts, err := runattempt.Open(homePath).List()
	if err != nil {
		t.Fatal(err)
	}
	for _, attempt := range attempts {
		if attempt.WorkID != w.ID {
			continue
		}
		if !containsString(finalWork.RunAttemptIDs, attempt.ID) {
			t.Fatalf("recovery attempt %s is not reachable from Work: %v", attempt.ID, finalWork.RunAttemptIDs)
		}
		if !runattempt.IsTerminal(attempt.Status) {
			t.Fatalf("recovery attempt %s remained %q", attempt.ID, attempt.Status)
		}
	}
	successor, err := store.ReserveProofResource(successorWork.ID, work.ProofResourceReserveInput{
		Actor:     "codex:successor",
		TTL:       time.Minute,
		Workspace: "/synthetic/repo-beta",
		Command:   "compound proof-check --out /synthetic/report-beta.json",
		Profile:   syntheticAdmissionProfile(t, "/synthetic/repo-beta"),
	}, currentNow.Add(time.Second))
	if err != nil {
		t.Fatalf("successor remained wedged after public recovery: %v", err)
	}
	if successor.Reservation.Status != work.ProofResourceAcquired {
		t.Fatalf("successor reservation = %+v, want acquired", successor.Reservation)
	}
}

func TestProofCheckCommandReleasePendingVerdictAndExactRecovery(t *testing.T) {
	if os.Getenv(proofCheckReleasePendingCLISubEnv) == "1" {
		proofCheckCanonicalRunner = func(_ context.Context, repoPath string, _ bool, baseRef string, _ bool, _ bool, now time.Time) (proofCheckReport, error) {
			marker, err := os.OpenFile(os.Getenv("COMPOUND_PROOF_RELEASE_PENDING_RUNS"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return proofCheckReport{}, err
			}
			if _, err := marker.WriteString("runner-started\n"); err != nil {
				_ = marker.Close()
				return proofCheckReport{}, err
			}
			if err := marker.Close(); err != nil {
				return proofCheckReport{}, err
			}
			report, err := injectedCanonicalProofCheckReport(repoPath, baseRef, now)
			if err != nil {
				return proofCheckReport{}, err
			}
			if err := os.Chmod(filepath.Join(os.Getenv("COMPOUND_HOME"), "state"), 0o555); err != nil {
				return proofCheckReport{}, err
			}
			return report, nil
		}
		cmdProofCheck([]string{
			"--repo", os.Getenv("COMPOUND_PROOF_RELEASE_PENDING_REPO"),
			"--work", os.Getenv("COMPOUND_PROOF_RELEASE_PENDING_WORK"),
			"--actor", "codex:release-pending-cli",
			"--out", os.Getenv("COMPOUND_PROOF_RELEASE_PENDING_OUT"),
		})
		os.Exit(0)
	}

	homePath := t.TempDir()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "proof-release-pending-cli-test", now); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w := createClaimedProofWork(t, store, "real CLI release-pending recovery", "codex:release-pending-cli", now)
	repo := t.TempDir()
	outPath := filepath.Join(t.TempDir(), "proof.json")
	runnerMarker := filepath.Join(t.TempDir(), "runner.log")
	stateDir := filepath.Join(homePath, "state")
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o755) })

	runCommand := func() (stdout, stderr string, err error) {
		command := exec.Command(os.Args[0], "-test.run=^TestProofCheckCommandReleasePendingVerdictAndExactRecovery$")
		command.Env = append(os.Environ(),
			proofCheckReleasePendingCLISubEnv+"=1",
			"COMPOUND_HOME="+homePath,
			"COMPOUND_PROOF_RELEASE_PENDING_REPO="+repo,
			"COMPOUND_PROOF_RELEASE_PENDING_WORK="+w.ID,
			"COMPOUND_PROOF_RELEASE_PENDING_OUT="+outPath,
			"COMPOUND_PROOF_RELEASE_PENDING_RUNS="+runnerMarker,
		)
		var stdoutBuf bytes.Buffer
		var stderrBuf bytes.Buffer
		command.Stdout = &stdoutBuf
		command.Stderr = &stderrBuf
		err = command.Run()
		return stdoutBuf.String(), stderrBuf.String(), err
	}

	firstStdout, firstStderr, firstErr := runCommand()
	var firstExit *exec.ExitError
	if !errors.As(firstErr, &firstExit) || firstExit.ExitCode() != 2 {
		t.Fatalf("release-pending command exit = %v, want 2; stdout=%q stderr=%q", firstErr, firstStdout, firstStderr)
	}
	firstLines := strings.Split(strings.TrimSpace(firstStdout), "\n")
	firstLast := firstLines[len(firstLines)-1]
	if !strings.HasPrefix(firstLast, "battery: FAILED ") ||
		!strings.Contains(firstLast, "release_pending=true") ||
		!strings.HasSuffix(firstLast, "out="+outPath) {
		t.Fatalf("release-pending last stdout line = %q, want explicit FAILED verdict; stdout:\n%s", firstLast, firstStdout)
	}
	if strings.Contains(firstStdout, "battery: ok") {
		t.Fatalf("release-pending command printed a green verdict:\n%s", firstStdout)
	}
	if got := strings.Count(firstStdout, "battery:"); got != 1 {
		t.Fatalf("release-pending stdout verdict count = %d, want exactly one:\n%s", got, firstStdout)
	}
	if !strings.Contains(firstStderr, "release remains pending") {
		t.Fatalf("release-pending stderr omitted recovery reason:\n%s", firstStderr)
	}
	firstOut, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("release-pending command did not preserve --out: %v", err)
	}
	var firstReport proofCheckReport
	if err := json.Unmarshal(firstOut, &firstReport); err != nil {
		t.Fatalf("release-pending --out is not a report: %v", err)
	}
	if firstReport.Status != "ok" {
		t.Fatalf("authoritative report status = %q, want prior gate result ok", firstReport.Status)
	}
	runtimeArtifacts, err := filepath.Glob(filepath.Join(homePath, "artifacts", "refs", "proof-check-installed-runtime-*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimeArtifacts) != 0 {
		t.Fatalf("release-pending proof published installed-runtime artifact before governed release: %v", runtimeArtifacts)
	}
	if err := os.Chmod(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	secondStdout, secondStderr, secondErr := runCommand()
	if secondErr != nil {
		t.Fatalf("exact CLI recovery failed: %v; stdout=%q stderr=%q", secondErr, secondStdout, secondStderr)
	}
	secondLines := strings.Split(strings.TrimSpace(secondStdout), "\n")
	secondLast := secondLines[len(secondLines)-1]
	if !strings.HasPrefix(secondLast, "battery: ok ") || !strings.HasSuffix(secondLast, "out="+outPath) {
		t.Fatalf("recovered last stdout line = %q, want truthful prior ok verdict; stdout:\n%s", secondLast, secondStdout)
	}
	if got := strings.Count(secondStdout, "battery:"); got != 1 {
		t.Fatalf("recovered stdout verdict count = %d, want exactly one:\n%s", got, secondStdout)
	}
	if secondStderr != "" {
		t.Fatalf("exact recovery wrote unexpected stderr:\n%s", secondStderr)
	}
	runnerRuns, err := os.ReadFile(runnerMarker)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(runnerRuns), "runner-started"); got != 1 {
		t.Fatalf("exact CLI recovery runner starts = %d, want one total; marker=%q", got, runnerRuns)
	}
	secondOut, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(secondOut, firstOut) {
		t.Fatal("exact CLI recovery rewrote the prior owned report")
	}
	runtimeArtifacts, err = filepath.Glob(filepath.Join(homePath, "artifacts", "refs", "proof-check-installed-runtime-*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimeArtifacts) != 1 {
		t.Fatalf("successful governed recovery published %d installed-runtime artifacts, want exactly one: %v", len(runtimeArtifacts), runtimeArtifacts)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{
		To:    "running",
		Actor: "codex:release-pending-cli",
	}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Close(w.ID, work.CloseInput{
		Outcome:  "failed",
		MoveType: "audit_only",
		Actor:    "codex:release-pending-cli",
		Summary:  "test projection after governed proof release",
	}, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(work.CreateInput{
		Owner:            "compound-core",
		SuccessPredicate: "synthetic held valve keeps adjacent proof projection visible",
		State:            "held",
	}, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	afterRecoveryWorkbench, err := store.Workbench(now.Add(5*time.Second), work.WorkbenchOptions{RecentLimit: 10, Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	adjacent := afterRecoveryWorkbench.DispatchNext.AdjacentProgress
	if adjacent == nil {
		t.Fatalf("successful governed recovery omitted adjacent Workbench projection: %+v", afterRecoveryWorkbench.DispatchNext)
	}
	if _, ok := workbenchAdjacentActionByID(adjacent.ConsumedNonPacketActions, "proof-check-installed-runtime"); !ok {
		t.Fatalf("successful governed recovery did not consume installed-runtime action: %+v", adjacent)
	}
}

func TestProofCheckCommandReleasePendingRecoveryRestoresOwnedOut(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, path string)
	}{
		{
			name: "missing",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "tampered",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("tampered proof output\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homePath := t.TempDir()
			now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
			if err := home.Init(homePath, "proof-release-pending-restore-test", now); err != nil {
				t.Fatal(err)
			}
			store, err := work.Open(homePath)
			if err != nil {
				t.Fatal(err)
			}
			w := createClaimedProofWork(t, store, "restore retained proof output", "codex:release-pending-cli", now)
			repo := t.TempDir()
			outPath := filepath.Join(t.TempDir(), "proof.json")
			runnerMarker := filepath.Join(t.TempDir(), "runner.log")
			stateDir := filepath.Join(homePath, "state")
			t.Cleanup(func() { _ = os.Chmod(stateDir, 0o755) })

			runCommand := func() (stdout, stderr string, err error) {
				command := exec.Command(os.Args[0], "-test.run=^TestProofCheckCommandReleasePendingVerdictAndExactRecovery$")
				command.Env = append(
					os.Environ(),
					proofCheckReleasePendingCLISubEnv+"=1",
					"COMPOUND_HOME="+homePath,
					"COMPOUND_PROOF_RELEASE_PENDING_REPO="+repo,
					"COMPOUND_PROOF_RELEASE_PENDING_WORK="+w.ID,
					"COMPOUND_PROOF_RELEASE_PENDING_OUT="+outPath,
					"COMPOUND_PROOF_RELEASE_PENDING_RUNS="+runnerMarker,
				)
				var stdoutBuf bytes.Buffer
				var stderrBuf bytes.Buffer
				command.Stdout = &stdoutBuf
				command.Stderr = &stderrBuf
				err = command.Run()
				return stdoutBuf.String(), stderrBuf.String(), err
			}

			firstStdout, firstStderr, firstErr := runCommand()
			var firstExit *exec.ExitError
			if !errors.As(firstErr, &firstExit) || firstExit.ExitCode() != 2 {
				t.Fatalf("release-pending command exit = %v, want 2; stdout=%q stderr=%q", firstErr, firstStdout, firstStderr)
			}
			firstOut, err := os.ReadFile(outPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(stateDir, 0o755); err != nil {
				t.Fatal(err)
			}
			tc.mutate(t, outPath)

			secondStdout, secondStderr, secondErr := runCommand()
			if secondErr != nil {
				t.Fatalf("exact restore recovery failed: %v; stdout=%q stderr=%q", secondErr, secondStdout, secondStderr)
			}
			secondLines := strings.Split(strings.TrimSpace(secondStdout), "\n")
			secondLast := secondLines[len(secondLines)-1]
			if !strings.HasPrefix(secondLast, "battery: ok ") ||
				!strings.HasSuffix(secondLast, "out="+outPath) ||
				strings.Count(secondStdout, "battery:") != 1 {
				t.Fatalf("restored recovery verdict = %q; stdout:\n%s", secondLast, secondStdout)
			}
			if secondStderr != "" {
				t.Fatalf("restored recovery wrote unexpected stderr:\n%s", secondStderr)
			}
			restoredOut, err := os.ReadFile(outPath)
			if err != nil {
				t.Fatalf("recovery returned green with missing --out: %v", err)
			}
			if !bytes.Equal(restoredOut, firstOut) {
				t.Fatalf("recovery returned green with non-owned --out: got=%q want immutable report", restoredOut)
			}
			runnerRuns, err := os.ReadFile(runnerMarker)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(string(runnerRuns), "runner-started"); got != 1 {
				t.Fatalf("restore recovery runner starts = %d, want one total", got)
			}
			status, err := landlease.Status(homePath, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if status.Held {
				t.Fatalf("restore recovery retained lease: %+v", status)
			}
		})
	}
}

func TestProofCheckCommandCanonicalFailedBatteryReleasesAdmission(t *testing.T) {
	if os.Getenv(proofCheckFailedCanonicalCLISubEnv) == "1" {
		proofCheckCanonicalRunner = func(_ context.Context, repoPath string, _ bool, baseRef string, _ bool, _ bool, now time.Time) (proofCheckReport, error) {
			report, err := injectedCanonicalProofCheckReport(repoPath, baseRef, now)
			if err != nil {
				return proofCheckReport{}, err
			}
			report.Status = "failed"
			report.Steps[0].Status = "failed"
			report.Steps[0].ExitCode = 1
			report.Steps[0].OutputTail = "injected canonical gate failed"
			report.ProofRefs = append([]string(nil), report.ProofRefs[1:]...)
			return report, nil
		}
		cmdProofCheck([]string{
			"--repo", os.Getenv("COMPOUND_PROOF_FAILED_CANONICAL_REPO"),
			"--work", os.Getenv("COMPOUND_PROOF_FAILED_CANONICAL_WORK"),
			"--actor", "codex:failed-canonical-cli",
			"--out", os.Getenv("COMPOUND_PROOF_FAILED_CANONICAL_OUT"),
		})
		os.Exit(0)
	}

	homePath := t.TempDir()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "proof-failed-canonical-cli-test", now); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w := createClaimedProofWork(t, store, "real CLI complete failed proof", "codex:failed-canonical-cli", now)
	successorWork := createClaimedProofWork(t, store, "proof after real CLI failure", "codex:successor", now.Add(time.Second))
	repo := t.TempDir()
	outPath := filepath.Join(t.TempDir(), "proof.json")

	command := exec.Command(os.Args[0], "-test.run=^TestProofCheckCommandCanonicalFailedBatteryReleasesAdmission$")
	command.Env = append(
		os.Environ(),
		proofCheckFailedCanonicalCLISubEnv+"=1",
		"COMPOUND_HOME="+homePath,
		"COMPOUND_PROOF_FAILED_CANONICAL_REPO="+repo,
		"COMPOUND_PROOF_FAILED_CANONICAL_WORK="+w.ID,
		"COMPOUND_PROOF_FAILED_CANONICAL_OUT="+outPath,
	)
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	command.Stdout = &stdoutBuf
	command.Stderr = &stderrBuf
	runErr := command.Run()
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("failed canonical command exit = %v, want 1; stdout=%q stderr=%q", runErr, stdoutBuf.String(), stderrBuf.String())
	}
	if stderrBuf.Len() != 0 {
		t.Fatalf("complete failed battery wrote unexpected stderr:\n%s", stderrBuf.String())
	}
	stdout := stdoutBuf.String()
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "battery: FAILED ") ||
		!strings.Contains(last, "failed=1") ||
		!strings.HasSuffix(last, "out="+outPath) {
		t.Fatalf("failed canonical last stdout line = %q, want truthful FAILED verdict; stdout:\n%s", last, stdout)
	}
	if got := strings.Count(stdout, "battery:"); got != 1 {
		t.Fatalf("failed canonical stdout verdict count = %d, want exactly one:\n%s", got, stdout)
	}

	reportBody, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("failed canonical command did not preserve --out: %v", err)
	}
	var report proofCheckReport
	if err := json.Unmarshal(reportBody, &report); err != nil {
		t.Fatalf("failed canonical --out is not a full report: %v", err)
	}
	expectedSteps, err := proofCheckSteps("")
	if err != nil {
		t.Fatal(err)
	}
	failedGates := 0
	skippedGates := 0
	for _, step := range report.Steps {
		switch step.Status {
		case "failed":
			failedGates++
		case "skipped":
			skippedGates++
		}
	}
	if report.Status != "failed" ||
		len(report.Steps) != len(expectedSteps) ||
		failedGates != 1 ||
		skippedGates != 0 ||
		len(report.ProofRefs) != len(expectedSteps)-1 {
		t.Fatalf("failed canonical --out = %+v, want complete red report", report)
	}

	finalWork, err := store.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(finalWork.VerificationIDs) != 0 {
		t.Fatalf("failed canonical report entered verification_ids: %v", finalWork.VerificationIDs)
	}
	for _, attemptID := range finalWork.RunAttemptIDs {
		attempt, err := runattempt.Open(homePath).Get(attemptID)
		if err != nil {
			t.Fatal(err)
		}
		if !runattempt.IsTerminal(attempt.Status) {
			t.Fatalf("failed canonical attempt %s remained %q", attempt.ID, attempt.Status)
		}
	}
	pending, err := os.ReadDir(filepath.Join(homePath, "work", "proof-resource-completions"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("failed canonical command retained completion journal: %v", pending)
	}
	lease, err := landlease.Status(homePath, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if lease.Held {
		t.Fatalf("failed canonical command retained scarce lease: %+v", lease)
	}

	successor, err := store.ReserveProofResource(successorWork.ID, work.ProofResourceReserveInput{
		Actor:     "codex:successor",
		TTL:       time.Minute,
		Workspace: "/synthetic/repo-beta",
		Command:   "compound proof-check --out /synthetic/report-beta.json",
		Profile:   syntheticAdmissionProfile(t, "/synthetic/repo-beta"),
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("successor remained blocked after complete failed battery: %v", err)
	}
	if successor.Reservation.Status != work.ProofResourceAcquired {
		t.Fatalf("successor reservation = %+v, want acquired", successor.Reservation)
	}
}

func TestProofCheckCommandFailedBatteryReleasePendingAndExactRecovery(t *testing.T) {
	if os.Getenv(proofCheckFailedReleaseCLISubEnv) == "1" {
		proofCheckCanonicalRunner = func(_ context.Context, repoPath string, _ bool, baseRef string, _ bool, _ bool, now time.Time) (proofCheckReport, error) {
			marker, err := os.OpenFile(os.Getenv("COMPOUND_PROOF_FAILED_RELEASE_RUNS"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return proofCheckReport{}, err
			}
			if _, err := marker.WriteString("runner-started\n"); err != nil {
				_ = marker.Close()
				return proofCheckReport{}, err
			}
			if err := marker.Close(); err != nil {
				return proofCheckReport{}, err
			}
			report, err := injectedCanonicalProofCheckReport(repoPath, baseRef, now)
			if err != nil {
				return proofCheckReport{}, err
			}
			report.Status = "failed"
			report.Steps[0].Status = "failed"
			report.Steps[0].ExitCode = 1
			report.Steps[0].OutputTail = "injected canonical gate failed"
			report.ProofRefs = append([]string(nil), report.ProofRefs[1:]...)
			if err := os.Chmod(filepath.Join(os.Getenv("COMPOUND_HOME"), "state"), 0o555); err != nil {
				return proofCheckReport{}, err
			}
			return report, nil
		}
		cmdProofCheck([]string{
			"--repo", os.Getenv("COMPOUND_PROOF_FAILED_RELEASE_REPO"),
			"--work", os.Getenv("COMPOUND_PROOF_FAILED_RELEASE_WORK"),
			"--actor", "codex:failed-release-cli",
			"--out", os.Getenv("COMPOUND_PROOF_FAILED_RELEASE_OUT"),
		})
		os.Exit(0)
	}

	homePath := t.TempDir()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "proof-failed-release-cli-test", now); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w := createClaimedProofWork(t, store, "failed battery retained release", "codex:failed-release-cli", now)
	repo := t.TempDir()
	outPath := filepath.Join(t.TempDir(), "proof.json")
	runnerMarker := filepath.Join(t.TempDir(), "runner.log")
	stateDir := filepath.Join(homePath, "state")
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o755) })

	runCommand := func() (stdout, stderr string, err error) {
		command := exec.Command(os.Args[0], "-test.run=^TestProofCheckCommandFailedBatteryReleasePendingAndExactRecovery$")
		command.Env = append(os.Environ(),
			proofCheckFailedReleaseCLISubEnv+"=1",
			"COMPOUND_HOME="+homePath,
			"COMPOUND_PROOF_FAILED_RELEASE_REPO="+repo,
			"COMPOUND_PROOF_FAILED_RELEASE_WORK="+w.ID,
			"COMPOUND_PROOF_FAILED_RELEASE_OUT="+outPath,
			"COMPOUND_PROOF_FAILED_RELEASE_RUNS="+runnerMarker,
		)
		var stdoutBuf bytes.Buffer
		var stderrBuf bytes.Buffer
		command.Stdout = &stdoutBuf
		command.Stderr = &stderrBuf
		err = command.Run()
		return stdoutBuf.String(), stderrBuf.String(), err
	}

	firstStdout, firstStderr, firstErr := runCommand()
	var firstExit *exec.ExitError
	if !errors.As(firstErr, &firstExit) || firstExit.ExitCode() != 2 {
		t.Fatalf("failed release-pending exit = %v, want 2; stdout=%q stderr=%q", firstErr, firstStdout, firstStderr)
	}
	firstLines := strings.Split(strings.TrimSpace(firstStdout), "\n")
	firstLast := firstLines[len(firstLines)-1]
	if !strings.HasPrefix(firstLast, "battery: FAILED ") ||
		!strings.Contains(firstLast, "release_pending=true") ||
		!strings.Contains(firstLast, "failed=1") ||
		!strings.HasSuffix(firstLast, "out="+outPath) {
		t.Fatalf("failed release-pending last stdout line = %q; stdout:\n%s", firstLast, firstStdout)
	}
	if got := strings.Count(firstStdout, "battery:"); got != 1 {
		t.Fatalf("failed release-pending verdict count = %d, want one:\n%s", got, firstStdout)
	}
	if !strings.Contains(firstStderr, "release remains pending") {
		t.Fatalf("failed release-pending stderr omitted recovery reason:\n%s", firstStderr)
	}
	firstOut, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	var firstReport proofCheckReport
	if err := json.Unmarshal(firstOut, &firstReport); err != nil {
		t.Fatal(err)
	}
	if firstReport.Status != "failed" {
		t.Fatalf("failed release-pending report status = %q", firstReport.Status)
	}
	firstWork, err := store.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstWork.VerificationIDs) != 0 {
		t.Fatalf("red release-pending battery certified verification: %v", firstWork.VerificationIDs)
	}
	if err := os.Chmod(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	secondStdout, secondStderr, secondErr := runCommand()
	var secondExit *exec.ExitError
	if !errors.As(secondErr, &secondExit) || secondExit.ExitCode() != 1 {
		t.Fatalf("failed exact recovery exit = %v, want 1; stdout=%q stderr=%q", secondErr, secondStdout, secondStderr)
	}
	secondLines := strings.Split(strings.TrimSpace(secondStdout), "\n")
	secondLast := secondLines[len(secondLines)-1]
	if !strings.HasPrefix(secondLast, "battery: FAILED ") ||
		strings.Contains(secondLast, "release_pending=true") ||
		!strings.Contains(secondLast, "failed=1") ||
		!strings.HasSuffix(secondLast, "out="+outPath) {
		t.Fatalf("failed exact recovery last stdout line = %q; stdout:\n%s", secondLast, secondStdout)
	}
	if secondStderr != "" {
		t.Fatalf("failed exact recovery wrote stderr:\n%s", secondStderr)
	}
	runnerRuns, err := os.ReadFile(runnerMarker)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(runnerRuns), "runner-started"); got != 1 {
		t.Fatalf("failed exact recovery runner starts = %d, want one total", got)
	}
	secondOut, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(secondOut, firstOut) {
		t.Fatal("failed exact recovery changed immutable report")
	}
	status, err := landlease.Status(homePath, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if status.Held {
		t.Fatalf("failed exact recovery retained scarce lease: %+v", status)
	}
	finalWork, err := store.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(finalWork.VerificationIDs) != 0 {
		t.Fatalf("recovered red battery certified verification: %v", finalWork.VerificationIDs)
	}
}

func TestProofCheckCommandPostReportFailuresAlwaysPrintFailedVerdict(t *testing.T) {
	if os.Getenv(proofCheckPostReportErrorCLISubEnv) == "1" {
		proofCheckCanonicalRunner = func(ctx context.Context, repoPath string, _ bool, baseRef string, _ bool, _ bool, now time.Time) (proofCheckReport, error) {
			report, err := injectedCanonicalProofCheckReport(repoPath, baseRef, now)
			if err != nil {
				return proofCheckReport{}, err
			}
			switch os.Getenv("COMPOUND_PROOF_POST_REPORT_ERROR_CASE") {
			case "validation":
				report.Steps[0].Command = []string{"go", "test", "./synthetic-tampered-gate"}
			case "heartbeat":
				if err := os.Chmod(filepath.Join(os.Getenv("COMPOUND_HOME"), "state"), 0o555); err != nil {
					return proofCheckReport{}, err
				}
				select {
				case <-ctx.Done():
				case <-time.After(3 * time.Second):
					return proofCheckReport{}, errors.New("heartbeat cancellation did not reach runner")
				}
			default:
				return proofCheckReport{}, errors.New("unknown injected post-report error case")
			}
			return report, nil
		}
		cmdProofCheck([]string{
			"--repo", os.Getenv("COMPOUND_PROOF_POST_REPORT_ERROR_REPO"),
			"--work", os.Getenv("COMPOUND_PROOF_POST_REPORT_ERROR_WORK"),
			"--actor", "codex:post-report-error-cli",
			"--out", os.Getenv("COMPOUND_PROOF_POST_REPORT_ERROR_OUT"),
			"--ttl", "3s",
		})
		os.Exit(0)
	}

	for _, testCase := range []string{"validation", "heartbeat"} {
		t.Run(testCase, func(t *testing.T) {
			homePath := t.TempDir()
			now := time.Now().UTC()
			if err := home.Init(homePath, "proof-post-report-error-test", now); err != nil {
				t.Fatal(err)
			}
			store, err := work.Open(homePath)
			if err != nil {
				t.Fatal(err)
			}
			w := createClaimedProofWork(t, store, "post-report error verdict", "codex:post-report-error-cli", now)
			repo := t.TempDir()
			outPath := filepath.Join(t.TempDir(), "proof.json")
			stateDir := filepath.Join(homePath, "state")
			t.Cleanup(func() { _ = os.Chmod(stateDir, 0o755) })
			command := exec.Command(os.Args[0], "-test.run=^TestProofCheckCommandPostReportFailuresAlwaysPrintFailedVerdict$")
			command.Env = append(os.Environ(),
				proofCheckPostReportErrorCLISubEnv+"=1",
				"COMPOUND_HOME="+homePath,
				"COMPOUND_PROOF_POST_REPORT_ERROR_CASE="+testCase,
				"COMPOUND_PROOF_POST_REPORT_ERROR_REPO="+repo,
				"COMPOUND_PROOF_POST_REPORT_ERROR_WORK="+w.ID,
				"COMPOUND_PROOF_POST_REPORT_ERROR_OUT="+outPath,
			)
			var stdoutBuf bytes.Buffer
			var stderrBuf bytes.Buffer
			command.Stdout = &stdoutBuf
			command.Stderr = &stderrBuf
			err = command.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
				t.Fatalf("%s post-report exit = %v, want 2; stdout=%q stderr=%q", testCase, err, stdoutBuf.String(), stderrBuf.String())
			}
			stdout := stdoutBuf.String()
			lines := strings.Split(strings.TrimSpace(stdout), "\n")
			last := lines[len(lines)-1]
			if !strings.HasPrefix(last, "battery: FAILED ") ||
				!strings.HasSuffix(last, "out="+outPath) ||
				strings.Contains(stdout, "battery: ok") {
				t.Fatalf("%s post-report last stdout line = %q; stdout:\n%s", testCase, last, stdout)
			}
			if got := strings.Count(stdout, "battery:"); got != 1 {
				t.Fatalf("%s post-report verdict count = %d, want one:\n%s", testCase, got, stdout)
			}
			body, readErr := os.ReadFile(outPath)
			if readErr != nil {
				t.Fatalf("%s post-report failure omitted durable --out: %v", testCase, readErr)
			}
			var report proofCheckReport
			if err := json.Unmarshal(body, &report); err != nil || report.SchemaVersion == "" {
				t.Fatalf("%s post-report --out is not a complete report: err=%v report=%+v", testCase, err, report)
			}
		})
	}
}

func TestProofCheckCommandRejectsUnsafeTTLBeforeAdmission(t *testing.T) {
	if os.Getenv(proofCheckUnsafeTTLCLISubEnv) == "1" {
		proofCheckCanonicalRunner = func(_ context.Context, repoPath string, _ bool, baseRef string, _ bool, _ bool, now time.Time) (proofCheckReport, error) {
			if err := os.WriteFile(os.Getenv("COMPOUND_PROOF_UNSAFE_TTL_RUNNER"), []byte("runner-started\n"), 0o644); err != nil {
				return proofCheckReport{}, err
			}
			return injectedCanonicalProofCheckReport(repoPath, baseRef, now)
		}
		cmdProofCheck([]string{
			"--repo", os.Getenv("COMPOUND_PROOF_UNSAFE_TTL_REPO"),
			"--work", os.Getenv("COMPOUND_PROOF_UNSAFE_TTL_WORK"),
			"--actor", "codex:unsafe-ttl-cli",
			"--out", os.Getenv("COMPOUND_PROOF_UNSAFE_TTL_OUT"),
			"--ttl", "1s",
		})
		os.Exit(0)
	}

	homePath := t.TempDir()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "proof-unsafe-ttl-cli-test", now); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w := createClaimedProofWork(t, store, "unsafe proof ttl refused", "codex:unsafe-ttl-cli", now)
	before, err := store.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	outPath := filepath.Join(t.TempDir(), "proof.json")
	runnerMarker := filepath.Join(t.TempDir(), "runner.log")
	command := exec.Command(os.Args[0], "-test.run=^TestProofCheckCommandRejectsUnsafeTTLBeforeAdmission$")
	command.Env = append(
		os.Environ(),
		proofCheckUnsafeTTLCLISubEnv+"=1",
		"COMPOUND_HOME="+homePath,
		"COMPOUND_PROOF_UNSAFE_TTL_REPO="+repo,
		"COMPOUND_PROOF_UNSAFE_TTL_WORK="+w.ID,
		"COMPOUND_PROOF_UNSAFE_TTL_OUT="+outPath,
		"COMPOUND_PROOF_UNSAFE_TTL_RUNNER="+runnerMarker,
	)
	output, runErr := command.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 2 {
		t.Fatalf("unsafe TTL command exit = %v, want usage 2; output:\n%s", runErr, output)
	}
	if !strings.Contains(string(output), "--ttl") || strings.Contains(string(output), "battery:") {
		t.Fatalf("unsafe TTL refusal = %q, want usage reason without fabricated battery", output)
	}
	if _, err := os.Stat(runnerMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe TTL started runner: %v", err)
	}
	if _, err := os.Stat(outPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe TTL created --out: %v", err)
	}
	after, err := store.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after.RunAttemptIDs, before.RunAttemptIDs) {
		t.Fatalf("unsafe TTL minted RunAttempt: before=%v after=%v", before.RunAttemptIDs, after.RunAttemptIDs)
	}
}

func TestProofCheckCommandMinimumTTLHeartbeatPreventsConcurrentRunner(t *testing.T) {
	if os.Getenv(proofCheckMinimumTTLCLISubEnv) == "1" {
		role := os.Getenv("COMPOUND_PROOF_MINIMUM_TTL_ROLE")
		proofCheckCanonicalRunner = func(_ context.Context, repoPath string, _ bool, baseRef string, _ bool, _ bool, now time.Time) (proofCheckReport, error) {
			if role == "first" {
				if err := os.WriteFile(os.Getenv("COMPOUND_PROOF_MINIMUM_TTL_FIRST_RUNNER"), []byte("runner-started\n"), 0o644); err != nil {
					return proofCheckReport{}, err
				}
				releasePath := os.Getenv("COMPOUND_PROOF_MINIMUM_TTL_RELEASE")
				for {
					if _, err := os.Stat(releasePath); err == nil {
						break
					} else if !errors.Is(err, os.ErrNotExist) {
						return proofCheckReport{}, err
					}
					time.Sleep(20 * time.Millisecond)
				}
			} else {
				if err := os.WriteFile(os.Getenv("COMPOUND_PROOF_MINIMUM_TTL_SECOND_RUNNER"), []byte("runner-started\n"), 0o644); err != nil {
					return proofCheckReport{}, err
				}
			}
			return injectedCanonicalProofCheckReport(repoPath, baseRef, now)
		}
		cmdProofCheck([]string{
			"--repo", os.Getenv("COMPOUND_PROOF_MINIMUM_TTL_REPO"),
			"--work", os.Getenv("COMPOUND_PROOF_MINIMUM_TTL_WORK"),
			"--actor", os.Getenv("COMPOUND_PROOF_MINIMUM_TTL_ACTOR"),
			"--out", os.Getenv("COMPOUND_PROOF_MINIMUM_TTL_OUT"),
			"--ttl", "3s",
		})
		os.Exit(0)
	}

	homePath := t.TempDir()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "proof-minimum-ttl-cli-test", now); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	firstWork := createClaimedProofWork(t, store, "minimum TTL slow proof", "codex:min-ttl-first", now)
	secondWork := createClaimedProofWork(t, store, "minimum TTL competitor", "codex:min-ttl-second", now.Add(time.Second))
	repo := t.TempDir()
	firstOut := filepath.Join(t.TempDir(), "first-proof.json")
	secondOut := filepath.Join(t.TempDir(), "second-proof.json")
	firstMarker := filepath.Join(t.TempDir(), "first-runner.log")
	secondMarker := filepath.Join(t.TempDir(), "second-runner.log")
	releasePath := filepath.Join(t.TempDir(), "release")

	newCommand := func(role, workID, actor, outPath string) *exec.Cmd {
		command := exec.Command(os.Args[0], "-test.run=^TestProofCheckCommandMinimumTTLHeartbeatPreventsConcurrentRunner$")
		command.Env = append(
			os.Environ(),
			proofCheckMinimumTTLCLISubEnv+"=1",
			"COMPOUND_HOME="+homePath,
			"COMPOUND_PROOF_MINIMUM_TTL_ROLE="+role,
			"COMPOUND_PROOF_MINIMUM_TTL_REPO="+repo,
			"COMPOUND_PROOF_MINIMUM_TTL_WORK="+workID,
			"COMPOUND_PROOF_MINIMUM_TTL_ACTOR="+actor,
			"COMPOUND_PROOF_MINIMUM_TTL_OUT="+outPath,
			"COMPOUND_PROOF_MINIMUM_TTL_FIRST_RUNNER="+firstMarker,
			"COMPOUND_PROOF_MINIMUM_TTL_SECOND_RUNNER="+secondMarker,
			"COMPOUND_PROOF_MINIMUM_TTL_RELEASE="+releasePath,
		)
		return command
	}

	firstCommand := newCommand("first", firstWork.ID, "codex:min-ttl-first", firstOut)
	var firstOutput bytes.Buffer
	firstCommand.Stdout = &firstOutput
	firstCommand.Stderr = &firstOutput
	if err := firstCommand.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(releasePath, []byte("release\n"), 0o644)
		if firstCommand.Process != nil {
			_ = firstCommand.Process.Kill()
			_, _ = firstCommand.Process.Wait()
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(firstMarker); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("minimum TTL first runner did not start; output:\n%s", firstOutput.String())
		}
		time.Sleep(20 * time.Millisecond)
	}

	time.Sleep(3500 * time.Millisecond)
	secondOutput, secondErr := newCommand("second", secondWork.ID, "codex:min-ttl-second", secondOut).CombinedOutput()
	var secondExit *exec.ExitError
	if !errors.As(secondErr, &secondExit) || secondExit.ExitCode() != 2 {
		t.Fatalf("minimum TTL competitor exit = %v, want admission refusal; output:\n%s", secondErr, secondOutput)
	}
	if _, err := os.Stat(secondMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("minimum TTL competitor runner started: %v", err)
	}
	if err := os.WriteFile(releasePath, []byte("release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := firstCommand.Wait(); err != nil {
		t.Fatalf("minimum TTL first proof failed: %v; output:\n%s", err, firstOutput.String())
	}
}

func TestProofCheckAdmissionLeavesLightweightModesUnreserved(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dryRun    bool
		ciOnly    bool
		ciConsole bool
		want      bool
	}{
		{name: "full", want: true},
		{name: "dry-run", dryRun: true},
		{name: "ci", ciOnly: true},
		{name: "ci-console", ciConsole: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := proofCheckNeedsAdmission(tc.dryRun, tc.ciOnly, tc.ciConsole); got != tc.want {
				t.Fatalf("proofCheckNeedsAdmission = %t, want %t", got, tc.want)
			}
		})
	}
}

func createClaimedProofWork(t *testing.T, store *work.Store, predicate, actor string, now time.Time) workEnvelope {
	t.Helper()
	w, err := store.Create(work.CreateInput{
		Owner:            "proof-admission",
		Actor:            actor,
		SuccessPredicate: predicate,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	w, err = store.Claim(w.ID, actor, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return workEnvelope{ID: w.ID}
}

type workEnvelope struct {
	ID string
}

func successfulProofCheckReport() proofCheckReport {
	return proofCheckReport{
		SchemaVersion: proofCheckSchemaVersion,
		ProfileKind:   work.ProofResourceProfileCanonicalFull,
		Status:        "ok",
		GeneratedAt:   "2026-07-24T12:00:03Z",
		Repo:          "/synthetic/repo-alpha",
		ProofRefs:     []string{"synthetic-proof-a", "synthetic-proof-b"},
		Steps: []proofCheckStep{
			{
				Name:     "synthetic-gate-a",
				Command:  []string{"go", "test", "./internal/synthetic-a"},
				ProofRef: "synthetic-proof-a",
				Status:   "ok",
			},
			{
				Name:     "synthetic-gate-b",
				Command:  []string{"go", "test", "./internal/synthetic-b"},
				ProofRef: "synthetic-proof-b",
				Status:   "ok",
			},
		},
	}
}

func injectedCanonicalProofCheckReport(repo, baseRef string, now time.Time) (proofCheckReport, error) {
	steps, err := proofCheckSteps(baseRef)
	if err != nil {
		return proofCheckReport{}, err
	}
	report := proofCheckReport{
		SchemaVersion: proofCheckSchemaVersion,
		ProfileKind:   work.ProofResourceProfileCanonicalFull,
		Status:        "ok",
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Repo:          repo,
		BaseRef:       baseRef,
		ProofRefs:     make([]string, 0, len(steps)),
		Steps:         make([]proofCheckStep, 0, len(steps)),
	}
	for _, step := range steps {
		step.Status = "ok"
		step.DurationMS = 1
		step.OutputTail = "injected canonical gate passed"
		report.ProofRefs = append(report.ProofRefs, step.ProofRef)
		report.Steps = append(report.Steps, step)
	}
	return report, nil
}

func syntheticAdmissionProfile(t *testing.T, repo string) work.ProofResourceProfile {
	t.Helper()
	profile, err := work.NewProofResourceProfile(work.ProofResourceProfileCanonicalFull, repo, "", []work.ProofResourceGateProfile{
		{
			Name:     "synthetic-gate-a",
			Command:  []string{"go", "test", "./internal/synthetic-a"},
			ProofRef: "synthetic-proof-a",
		},
		{
			Name:     "synthetic-gate-b",
			Command:  []string{"go", "test", "./internal/synthetic-b"},
			ProofRef: "synthetic-proof-b",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

// A certifying battery must stay runnable by any harness or model invoked as a capability.
// Agents run proof-check with stdout redirected to a log, and a scheduled merge queue has no
// controlling terminal at all, so requiring an interactive stdout would make the certifying
// path human-only and leave every automated lane on the non-certifying --dev profile. Custody
// of the --out report under the still-held lease is the proof; the stdout verdict line is a
// convenience for whoever is watching. This test fails if a stdout-is-a-terminal admission
// gate is reintroduced: it pins stdout to a pipe (never a char device) and requires the
// governed run to be admitted, execute its runner exactly once, and record its verification.
func TestProofCheckAdmissionAdmitsNonTerminalStdoutForAgentInvocation(t *testing.T) {
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEnd.Close()
	realStdout := os.Stdout
	os.Stdout = writeEnd
	t.Cleanup(func() {
		os.Stdout = realStdout
		writeEnd.Close()
	})
	if info, statErr := os.Stdout.Stat(); statErr != nil {
		t.Fatal(statErr)
	} else if info.Mode()&os.ModeCharDevice != 0 {
		t.Fatal("stdout must not be a character device for this test to falsify a terminal gate")
	}

	homePath := t.TempDir()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "proof-admission-test", now); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	item := createClaimedProofWork(t, store, "agent-invoked governed proof", "agent:claude-code", now)

	var runnerStarts atomic.Int32
	report, completed, err := runProofCheckWithAdmission(proofCheckAdmissionInput{
		HomePath:  homePath,
		WorkID:    item.ID,
		Actor:     "agent:claude-code",
		Workspace: "/synthetic/repo-alpha",
		Command:   "compound proof-check --out /synthetic/report-agent.json",
		Profile:   syntheticAdmissionProfile(t, "/synthetic/repo-alpha"),
		TTL:       time.Minute,
		Now:       func() time.Time { return now.Add(time.Second) },
	}, func() (proofCheckReport, error) {
		runnerStarts.Add(1)
		return successfulProofCheckReport(), nil
	})
	if err != nil {
		t.Fatalf("governed proof with non-terminal stdout = %v, want admitted", err)
	}
	if runnerStarts.Load() != 1 {
		t.Fatalf("runner starts = %d, want exactly one", runnerStarts.Load())
	}
	if report.Status != "ok" {
		t.Fatalf("report status = %q, want ok", report.Status)
	}
	if completed.VerificationRef == "" {
		t.Fatal("completed verification ref is empty; custody is the proof and must be recorded")
	}
}

func TestProofCheckCompletionRetainsHolderUntilBackgroundDescendantExits(t *testing.T) {
	homePath := t.TempDir()
	outPath := filepath.Join(t.TempDir(), "proof-report.json")
	now := time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "proof-orphan-descendant-test", now); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	item := createClaimedProofWork(t, store, "background descendants must exit before proof completion", "codex:orphan-descendant", now)
	input := proofCheckAdmissionInput{
		HomePath:   homePath,
		WorkID:     item.ID,
		Actor:      "codex:orphan-descendant",
		Workspace:  "/synthetic/repo-alpha",
		Command:    "compound proof-check --out " + outPath,
		Profile:    syntheticAdmissionProfile(t, "/synthetic/repo-alpha"),
		ReportPath: outPath,
		TTL:        time.Minute,
		Now:        func() time.Time { return now.Add(time.Second) },
	}

	var runnerStarts atomic.Int32
	var gate *exec.Cmd
	report, completed, firstErr := runProofCheckWithAdmissionContext(input, func(ctx context.Context) (proofCheckReport, error) {
		runnerStarts.Add(1)
		// The gate process exits cleanly after spawning a background descendant.
		// exec.Cmd has finished waiting, so later context cancellation cannot kill
		// that descendant; only the inherited barrier can prove it is still alive.
		gate = proofCheckCommandContext(ctx, "sh", "-c", "sleep 0.75 &")
		if err := gate.Run(); err != nil {
			return proofCheckReport{}, err
		}
		report := successfulProofCheckReport()
		report.GeneratedAt = now.Add(2 * time.Second).Format(time.RFC3339)
		if err := proofCheckWriteReportFile(outPath, report); err != nil {
			return proofCheckReport{}, err
		}
		return report, nil
	})
	if firstErr == nil {
		t.Fatalf("proof completion released while descendant still held the execution barrier: completed=%+v report=%+v", completed, report)
	}
	var executionPending *landlease.ExecutionPendingError
	if !errors.As(firstErr, &executionPending) {
		t.Fatalf("first completion error = %v, want ExecutionPendingError", firstErr)
	}
	t.Logf("first completion retained holder: %v", firstErr)
	status, err := landlease.Status(homePath, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !status.Held {
		t.Fatalf("failed-closed completion did not retain the proof holder: %+v", status)
	}
	if gate == nil || gate.Process == nil {
		t.Fatal("runner did not start the gate process")
	}
	t.Cleanup(func() { _ = syscall.Kill(-gate.Process.Pid, syscall.SIGKILL) })
	deadline := time.Now().Add(3 * time.Second)
	for {
		pending, err := landlease.ExecutionPending(homePath, status.Holder)
		if err != nil {
			t.Fatal(err)
		}
		if !pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background descendant did not release execution barrier before deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}

	resumedReport, resumed, retryErr := runProofCheckWithAdmissionContext(input, func(context.Context) (proofCheckReport, error) {
		runnerStarts.Add(1)
		return proofCheckReport{}, errors.New("exact completion retry reran proof gates")
	})
	if retryErr != nil {
		t.Fatalf("exact retry after descendant exit did not complete: %v", retryErr)
	}
	if runnerStarts.Load() != 1 {
		t.Fatalf("runner starts = %d, want one across retained-result recovery", runnerStarts.Load())
	}
	if resumedReport.Status != "ok" || resumed.VerificationRef == "" || !resumed.ResourceReleased {
		t.Fatalf("resumed completion = report:%+v result:%+v", resumedReport, resumed)
	}
}

func TestProofCheckGoCacheIsPerWorktree(t *testing.T) {
	first, firstOK := proofCheckGoCache(t.TempDir())
	second, secondOK := proofCheckGoCache(t.TempDir())
	t.Cleanup(func() {
		_ = os.RemoveAll(first)
		_ = os.RemoveAll(second)
	})
	if !firstOK || !secondOK {
		t.Fatal("no cache chosen for a real worktree")
	}
	if first == second {
		t.Fatalf("two worktrees share one cache (%s); sibling lanes can still evict each other", first)
	}
}

func TestProofCheckGoCacheIsStableForOneWorktree(t *testing.T) {
	worktree := t.TempDir()
	first, firstOK := proofCheckGoCache(worktree)
	second, secondOK := proofCheckGoCache(worktree)
	t.Cleanup(func() { _ = os.RemoveAll(first) })
	if !firstOK || !secondOK {
		t.Fatal("no cache chosen for a real worktree")
	}
	if first != second {
		t.Fatalf("same worktree got two caches (%s vs %s); repeated batteries would start cold", first, second)
	}
}

func TestProofCheckGoCacheLivesOutsideWorktree(t *testing.T) {
	worktree := t.TempDir()
	cache, ok := proofCheckGoCache(worktree)
	t.Cleanup(func() { _ = os.RemoveAll(cache) })
	if !ok {
		t.Fatal("no cache chosen for a real worktree")
	}
	relative, err := filepath.Rel(worktree, cache)
	if err == nil && !strings.HasPrefix(relative, "..") {
		t.Fatalf("cache %s is inside worktree %s; the public redact gate will scan it", cache, worktree)
	}
}

func TestProofCheckGoCacheHonorsSharedOptOut(t *testing.T) {
	t.Setenv("COMPOUND_PROOF_SHARED_GOCACHE", "1")
	if _, ok := proofCheckGoCache(t.TempDir()); ok {
		t.Fatal("shared-cache opt-out ignored")
	}
}

// Path selection is insufficient proof: this fails if runProofCheckStep stops injecting the
// per-worktree cache even while every proofCheckGoCache unit test remains green.
func TestRunProofCheckStepInjectsWorktreeGoCache(t *testing.T) {
	repo := t.TempDir()
	want, ok := proofCheckGoCache(repo)
	if !ok {
		t.Fatal("no cache chosen for a real worktree")
	}
	t.Cleanup(func() { _ = os.RemoveAll(want) })

	binDir := t.TempDir()
	fakeGo := filepath.Join(binDir, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/bin/sh\nprintf '%s\\n' \"$GOCACHE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOCACHE", "shared-parent-cache")

	got := runProofCheckStep(repo, proofCheckStep{
		Name:     "go-cache-wiring",
		Command:  []string{"go"},
		ProofRef: "go-cache-wiring",
	}, "")
	if got.Status != "ok" {
		t.Fatalf("fake Go gate failed: %+v", got)
	}
	if strings.TrimSpace(got.OutputTail) != want {
		t.Fatalf("Go gate saw GOCACHE %q, want per-worktree cache %q", got.OutputTail, want)
	}
}
