package work

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"compound/internal/authority/gitrepository"
	"compound/internal/landlease"
)

const presubmitNoFDEnv = "COMPOUND_SYNTHETIC_PRESUBMIT_NO_FDS"

func TestPresubmitDropsAllDescriptorsHelper(t *testing.T) {
	mode := os.Getenv(presubmitNoFDEnv)
	if mode == "" {
		return
	}
	if mode == "descendant" {
		if err := os.WriteFile(os.Getenv("COMPOUND_SYNTHETIC_PRESUBMIT_PID_FILE"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(33)
		}
		expected, err := strconv.Atoi(os.Getenv("COMPOUND_SYNTHETIC_PRESUBMIT_EXPECTED_PARENT"))
		if err != nil {
			os.Exit(34)
		}
		deadline := time.Now().Add(20 * time.Second)
		for os.Getppid() == expected {
			if time.Now().After(deadline) {
				os.Exit(35)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err = os.WriteFile(os.Getenv("COMPOUND_SYNTHETIC_PRESUBMIT_READY_FILE"), []byte("direct parent exited"), 0o600); err != nil {
			os.Exit(36)
		}
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestPresubmitDropsAllDescriptorsHelper$")
	child.Env = append(os.Environ(), presubmitNoFDEnv+"=descendant", "COMPOUND_SYNTHETIC_PRESUBMIT_EXPECTED_PARENT="+strconv.Itoa(os.Getpid()))
	child.Stdin = os.NewFile(3, "synthetic-fixture-control")
	// No ExtraFiles or Setpgid: ordinary Go os/exec drops every ownership FD.
	if err := child.Start(); err != nil {
		os.Exit(31)
	}
	os.Exit(0)
}

func TestPresubmitSameGroupWithoutDescriptorsCancelsBeforeLeaderReap(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	readyFile := pidFile + ".ready"
	controlRead, controlWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer controlRead.Close()
	defer controlWrite.Close()
	result, err := s.RunPresubmitProducer(context.Background(), request, func(ctx context.Context, _ PresubmitExecutionRequest) (ProofCheckReport, error) {
		outer, stop := context.WithTimeout(ctx, 30*time.Second)
		defer stop()
		childCtx, cancel := context.WithCancel(outer)
		defer cancel()
		watchDone := make(chan error, 1)
		go func() {
			for {
				if _, e := os.Stat(readyFile); e == nil {
					cancel()
					watchDone <- nil
					return
				}
				select {
				case <-outer.Done():
					watchDone <- fmt.Errorf("no-FD fixture never observed descendant and leader exit: %w", outer.Err())
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
		}()
		cmd := exec.Command(os.Args[0], "-test.run=^TestPresubmitDropsAllDescriptorsHelper$")
		cmd.ExtraFiles = []*os.File{controlRead}
		cmd.Env = append(os.Environ(), presubmitNoFDEnv+"=leader", "COMPOUND_SYNTHETIC_PRESUBMIT_PID_FILE="+pidFile, "COMPOUND_SYNTHETIC_PRESUBMIT_READY_FILE="+readyFile)
		runErr := gitrepository.RunCommand(childCtx, cmd)
		fixtureErr := <-watchDone
		if fixtureErr != nil {
			t.Error(fixtureErr)
		}
		return ProofCheckReport{}, errors.Join(runErr, fixtureErr)
	})
	if err == nil || !result.Quiescent || !result.Released || result.ContainmentUncertain {
		t.Fatalf("same-group no-FD descendant escaped synchronized cleanup: result=%+v err=%v", result, err)
	}
	body, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal("test never spawned the no-FD descendant:", err)
	}
	if _, err = os.Stat(readyFile); err != nil {
		t.Fatal("test never observed direct leader exit:", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	if err = syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("exact fixture descendant %d remains after observed group drain: %v", pid, err)
	}
}

func TestPresubmitUncertainGroupRefusesReapExpiryTakeoverAndBarrierRecovery(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	original := presubmitObserveGroup
	originalOpen := presubmitOpenGroup
	var cleanupGroup presubmitOwnedProcessGroup
	presubmitObserveGroup = func(presubmitOwnedProcessGroup) (bool, error) {
		return false, errors.New("synthetic exact-group query fault")
	}
	presubmitOpenGroup = func(pid int) (presubmitOwnedProcessGroup, error) {
		group, err := originalOpen(pid)
		if err != nil {
			return nil, err
		}
		fixture, err := originalOpen(pid)
		if err != nil {
			_ = group.close()
			return nil, err
		}
		cleanupGroup = fixture
		return group, nil
	}
	var child *exec.Cmd
	var owner *presubmitRuntime
	cleanup := func() {
		if cleanupGroup != nil {
			_ = cleanupGroup.kill()
			if cleanupGroup.reapEarly() {
				deadline := time.Now().Add(5 * time.Second)
				for {
					empty, e := cleanupGroup.drained()
					if e != nil || empty {
						break
					}
					if time.Now().After(deadline) {
						t.Error("fixture exact-handle cleanup did not drain")
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
			} else if child != nil {
				_ = child.Wait()
			}
			_ = cleanupGroup.close()
			cleanupGroup = nil
			child = nil
		}
		if owner != nil && owner.barrier != nil {
			_ = owner.barrier.Close()
			owner.barrier = nil
		}
	}
	defer func() { presubmitObserveGroup = original; presubmitOpenGroup = originalOpen; cleanup() }()
	result, err := s.RunPresubmitProducer(context.Background(), request, func(ctx context.Context, _ PresubmitExecutionRequest) (ProofCheckReport, error) {
		owner = ctx.Value(nativePresubmitRuntimeKey{}).(*presubmitRuntime)
		childCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		child = exec.Command("sh", "-c", "exec sleep 30")
		return ProofCheckReport{}, gitrepository.RunCommand(childCtx, child)
	})
	if err == nil || !result.ContainmentUncertain || result.Quiescent || result.Released {
		t.Fatalf("uncertain group claimed cleanup: %+v %v", result, err)
	}
	if cleanupGroup != nil && !cleanupGroup.reapEarly() && child.ProcessState != nil {
		t.Fatal("native owner reaped without group cleanup observation")
	}
	presubmitObserveGroup = original
	presubmitOpenGroup = originalOpen
	cleanup()
	resource := landlease.ProofResourceForIdentity(s.home, result.ProofCoordination)
	if _, err = resource.Refresh(result.Holder, time.Minute, time.Now().Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	before, err := resource.Status(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !before.IsExpired {
		t.Fatal("fixture did not expire the retained holder")
	}
	called := false
	_, err = s.RunPresubmitProducer(context.Background(), request, func(context.Context, PresubmitExecutionRequest) (ProofCheckReport, error) {
		called = true
		return ProofCheckReport{}, nil
	})
	after, readErr := resource.Status(time.Now())
	if err == nil || called || readErr != nil || after.Holder != result.Holder || after.AcquiredAt != before.AcquiredAt || after.ExpiresAt != before.ExpiresAt {
		t.Fatalf("ordinary admission bypassed exact expired-holder recovery: called=%v before=%+v after=%+v err=%v read=%v", called, before, after, err, readErr)
	}
	if _, err = s.RecoverPresubmitProducer(request.WorkID, request.Actor, result.RunAttemptID, "barrier alone is insufficient"); err == nil {
		t.Fatal("barrier-only recovery accepted observed group uncertainty")
	}
}

func TestPresubmitUnsupportedContainmentRefusesBeforePreparation(t *testing.T) {
	if presubmitGroupContainmentSupported() == nil {
		t.Skip("supported platform")
	}
	s := newStore(t)
	called := false
	result, err := s.RunPresubmitProducer(context.Background(), PresubmitExecutionRequest{WorkID: "work_synthetic_unsupported", Actor: "synthetic-owner", BaseRef: strings.Repeat("a", 40), OutPath: t.TempDir() + "/report.json", Budget: time.Minute}, func(context.Context, PresubmitExecutionRequest) (ProofCheckReport, error) {
		called = true
		return ProofCheckReport{}, nil
	})
	if err == nil || called || result.RunAttemptID != "" || result.Status != "refused" {
		t.Fatalf("unsupported platform admitted preparation: result=%+v called=%v err=%v", result, called, err)
	}
}
