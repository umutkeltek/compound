package landlease

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestExecutionBarrierBlocksExpiredAcquireAndStealUntilAllHoldersExit(t *testing.T) {
	homePath := t.TempDir()
	now := baseNow

	_, barrier, err := AcquireWithExecutionBarrierPrepared(
		homePath,
		"proof:reservation-alpha:ratt_20260102T030405Z_aaaaaaaa:work_alpha:codex-alpha",
		time.Second,
		now,
		func(AcquireOutcome) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = barrier.Close() })
	if barrier.ID() == "" {
		t.Fatal("proof lease did not persist execution barrier identity")
	}

	inherited, err := barrier.DuplicateForChild()
	if err != nil {
		t.Fatal(err)
	}
	defer inherited.Close()
	if err := barrier.Close(); err != nil {
		t.Fatal(err)
	}

	expiredAt := now.Add(2 * time.Second)
	if _, err := Acquire(homePath, laneBeta, time.Minute, expiredAt); err == nil {
		t.Fatal("ordinary expired acquire bypassed live proof execution barrier")
	} else {
		var pending *ExecutionPendingError
		if !errors.As(err, &pending) {
			t.Fatalf("expired acquire error = %v, want ExecutionPendingError", err)
		}
	}
	if _, err := Steal(homePath, laneGamma, "operator exceptional takeover", expiredAt); err == nil {
		t.Fatal("generic steal bypassed live proof execution barrier")
	} else {
		var pending *ExecutionPendingError
		if !errors.As(err, &pending) {
			t.Fatalf("steal error = %v, want ExecutionPendingError", err)
		}
	}

	if err := inherited.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Acquire(homePath, laneBeta, time.Minute, expiredAt)
	if err != nil {
		t.Fatalf("stale already-dead proof predecessor did not recover: %v", err)
	}
	if next.Holder != laneBeta {
		t.Fatalf("successor holder = %q, want %q", next.Holder, laneBeta)
	}
}

func TestReleaseAfterRefusesActiveInheritedExecutionBarrierBeforeFinalizer(t *testing.T) {
	homePath := t.TempDir()
	holder := "proof:pfr_release_after:ratt_20260102T030405Z_abababab:work_release_after:codex-release-after"
	_, barrier, err := AcquireWithExecutionBarrierPrepared(
		homePath,
		holder,
		time.Minute,
		baseNow,
		func(AcquireOutcome) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = barrier.Close() })

	inherited, err := barrier.DuplicateForChild()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inherited.Close() })
	if err := barrier.Close(); err != nil {
		t.Fatal(err)
	}

	finalizerCalled := false
	released, err := ReleaseAfter(homePath, holder, baseNow.Add(time.Second), func() error {
		finalizerCalled = true
		return nil
	})
	if err == nil {
		t.Fatalf("ReleaseAfter bypassed active inherited execution barrier: %+v", released)
	}
	var pending *ExecutionPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("ReleaseAfter error = %v, want ExecutionPendingError", err)
	}
	if finalizerCalled {
		t.Fatal("ReleaseAfter invoked finalizer while inherited execution barrier was pending")
	}
	if released.Freed {
		t.Fatalf("refused ReleaseAfter reported Freed: %+v", released)
	}
	status, err := Status(homePath, baseNow.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !status.Held || status.Holder != holder {
		t.Fatalf("refused ReleaseAfter released or transferred lease: %+v", status)
	}
}

func TestReleaseRefusesProofHolderUntilExecutionTerminationIsAttested(t *testing.T) {
	t.Run("active execution barrier keeps lease held", func(t *testing.T) {
		homePath := t.TempDir()
		holder := "proof:pfr_release:ratt_20260102T030405Z_eeeeeeee:work_release:codex-release"
		_, barrier, err := AcquireWithExecutionBarrierPrepared(
			homePath,
			holder,
			time.Second,
			baseNow,
			func(AcquireOutcome) error { return nil },
		)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = barrier.Close() })

		expiredAt := baseNow.Add(2 * time.Second)
		released, err := Release(homePath, holder, expiredAt)
		if err == nil {
			t.Fatalf("plain release bypassed active proof execution barrier: %+v", released)
		}
		var pending *ExecutionPendingError
		if !errors.As(err, &pending) {
			t.Fatalf("release error = %v, want ExecutionPendingError", err)
		}
		if released.Freed {
			t.Fatalf("refused release reported Freed: %+v", released)
		}
		if _, err := Acquire(homePath, laneBeta, time.Minute, expiredAt); err == nil {
			t.Fatal("successor acquired after refused proof release")
		} else if !errors.As(err, &pending) {
			t.Fatalf("successor acquire error = %v, want ExecutionPendingError", err)
		}

		if err := barrier.Close(); err != nil {
			t.Fatal(err)
		}
		released, err = Release(homePath, holder, expiredAt)
		if err != nil {
			t.Fatalf("plain release after execution termination: %v", err)
		}
		if !released.Freed {
			t.Fatalf("plain release after execution termination = %+v, want Freed", released)
		}
		if _, err := Acquire(homePath, laneBeta, time.Minute, expiredAt); err != nil {
			t.Fatalf("successor after attested release: %v", err)
		}
	})

	t.Run("legacy proof lease without barrier fails closed", func(t *testing.T) {
		homePath := t.TempDir()
		holder := "proof:pfr_release_legacy:ratt_20260102T030405Z_ffffffff:work_release_legacy:codex-release-legacy"
		if _, err := Acquire(homePath, holder, time.Second, baseNow); err != nil {
			t.Fatal(err)
		}

		released, err := Release(homePath, holder, baseNow.Add(2*time.Second))
		if err == nil {
			t.Fatalf("plain release bypassed unattested proof holder: %+v", released)
		}
		var unattested *ExecutionUnattestedError
		if !errors.As(err, &unattested) {
			t.Fatalf("release error = %v, want ExecutionUnattestedError", err)
		}
		if released.Freed {
			t.Fatalf("refused unattested release reported Freed: %+v", released)
		}
		if _, err := Acquire(homePath, laneBeta, time.Minute, baseNow.Add(2*time.Second)); err == nil {
			t.Fatal("successor acquired after refused unattested proof release")
		} else if !errors.As(err, &unattested) {
			t.Fatalf("successor acquire error = %v, want ExecutionUnattestedError", err)
		}
	})
}

func TestExecutionBarrierCanBeInheritedByRealProcess(t *testing.T) {
	if os.Getenv("COMPOUND_LANDLEASE_BARRIER_CHILD") == "1" {
		file := os.NewFile(3, "compound-landlease-execution-barrier")
		if file == nil {
			os.Exit(2)
		}
		_, _ = file.Write([]byte{})
		time.Sleep(250 * time.Millisecond)
		_ = file.Close()
		os.Exit(0)
	}

	homePath := t.TempDir()
	now := baseNow
	outcome, barrier, err := AcquireWithExecutionBarrierPrepared(
		homePath,
		"proof:reservation-real:ratt_20260102T030405Z_bbbbbbbb:work_real:codex-real",
		time.Second,
		now,
		func(AcquireOutcome) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	defer barrier.Close()

	cmd := testBarrierChildCommand(t, barrier)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.ExtraFiles[0].Close(); err != nil {
		t.Fatal(err)
	}
	if err := barrier.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Acquire(homePath, laneBeta, time.Minute, now.Add(2*time.Second)); err == nil {
		t.Fatal("real child holding inherited barrier did not block expired successor")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(homePath, laneBeta, time.Minute, now.Add(2*time.Second)); err != nil {
		t.Fatalf("successor remained blocked after real child exited: %v; lease=%+v", err, outcome.Lease)
	}
}

func TestProofLeaseWithoutBarrierFailsClosedAndBarrierReleaseIsAttested(t *testing.T) {
	t.Run("legacy proof lease cannot be taken from state alone", func(t *testing.T) {
		homePath := t.TempDir()
		holder := "proof:pfr_legacy:ratt_20260102T030405Z_cccccccc:work_legacy:codex-legacy"
		if _, err := Acquire(homePath, holder, time.Second, baseNow); err != nil {
			t.Fatal(err)
		}
		if _, err := Acquire(homePath, laneBeta, time.Minute, baseNow.Add(2*time.Second)); err == nil {
			t.Fatal("legacy proof holder without execution barrier was treated as safely terminated")
		} else {
			var unattested *ExecutionUnattestedError
			if !errors.As(err, &unattested) {
				t.Fatalf("legacy proof takeover error = %v, want ExecutionUnattestedError", err)
			}
		}
	})

	t.Run("trusted producer attests only after final barrier descriptor closes", func(t *testing.T) {
		homePath := t.TempDir()
		holder := "proof:pfr_attested:ratt_20260102T030405Z_dddddddd:work_attested:codex-attested"
		_, barrier, err := AcquireWithExecutionBarrierPrepared(
			homePath,
			holder,
			time.Minute,
			baseNow,
			func(AcquireOutcome) error { return nil },
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := AttestExecutionTerminated(homePath, holder); err == nil {
			t.Fatal("trusted producer attested a still-held execution barrier")
		}
		if err := barrier.Close(); err != nil {
			t.Fatal(err)
		}
		attestation, err := AttestExecutionTerminated(homePath, holder)
		if err != nil {
			t.Fatal(err)
		}
		if attestation.BarrierID != barrier.ID() ||
			attestation.Producer != "compound.landlease.execution-barrier" {
			t.Fatalf("termination attestation = %+v", attestation)
		}
	})
}

func testBarrierChildCommand(t *testing.T, barrier *ExecutionBarrier) *exec.Cmd {
	t.Helper()
	childFile, err := barrier.DuplicateForChild()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = childFile.Close() })
	cmd := exec.Command(os.Args[0], "-test.run=^TestExecutionBarrierCanBeInheritedByRealProcess$")
	cmd.Env = append(os.Environ(), "COMPOUND_LANDLEASE_BARRIER_CHILD=1")
	cmd.ExtraFiles = []*os.File{childFile}
	return cmd
}

func TestCompositeExecutionBarrierSurvivesInRealChild(t *testing.T) {
	if os.Getenv("COMPOUND_COMPOSITE_BARRIER_CHILD") == "1" {
		for _, fd := range []uintptr{3, 4} {
			file := os.NewFile(fd, "barrier")
			if file == nil {
				os.Exit(2)
			}
			if _, err := file.Stat(); err != nil {
				os.Exit(3)
			}
		}
		_, _ = os.Stdout.Write([]byte("ready"))
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	capacityRoot, repositoryRoot := t.TempDir(), t.TempDir()
	capacity, err := lockExecutionBarrier(capacityRoot, "capacity")
	if err != nil {
		t.Fatal(err)
	}
	defer capacity.Close()
	repository, err := lockExecutionBarrier(repositoryRoot, "repository")
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err := capacity.include(repository); err != nil {
		t.Fatal(err)
	}
	if _, err := capacity.DuplicateForChild(); err == nil {
		t.Fatal("singular API silently dropped repository barrier")
	}
	files, err := capacity.DuplicateFilesForChild()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, file := range files {
			_ = file.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCompositeExecutionBarrierSurvivesInRealChild$")
	cmd.Env = append(os.Environ(), "COMPOUND_COMPOSITE_BARRIER_CHILD=1")
	cmd.ExtraFiles = files
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	defer func() {
		if !joined {
			cancel()
			_ = cmd.Wait()
		}
	}()
	ready := make([]byte, 5)
	if _, err := io.ReadFull(output, ready); err != nil {
		t.Fatal(err)
	}
	if string(ready) != "ready" {
		t.Fatalf("child readiness = %q", ready)
	}
	for _, file := range files {
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := capacity.Close(); err != nil {
		t.Fatal(err)
	}
	for root, holder := range map[string]string{capacityRoot: "capacity", repositoryRoot: "repository"} {
		pending, err := executionBarrierPending(root, holder)
		if err != nil || !pending {
			t.Fatalf("descendant protection lost for %s: %v", holder, err)
		}
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	joined = true
	for root, holder := range map[string]string{capacityRoot: "capacity", repositoryRoot: "repository"} {
		pending, err := executionBarrierPending(root, holder)
		if err != nil || pending {
			t.Fatalf("barrier retained after child exit for %s: %v", holder, err)
		}
	}
}
