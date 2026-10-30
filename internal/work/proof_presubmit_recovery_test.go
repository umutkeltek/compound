package work

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"compound/internal/authority/gitrepository"
	"compound/internal/landlease"
	"compound/internal/runattempt"
)

const presubmitHelperEnv = "COMPOUND_SYNTHETIC_PRESUBMIT_HELPER"

type presubmitHelperInput struct {
	Home    string
	Request PresubmitExecutionRequest
	Mode    string
}

func TestPresubmitKilledProducerHelper(t *testing.T) {
	raw := os.Getenv(presubmitHelperEnv)
	if raw == "" {
		return
	}
	var input presubmitHelperInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	s, err := Open(input.Home)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.RunPresubmitProducer(context.Background(), input.Request, func(ctx context.Context, _ PresubmitExecutionRequest) (ProofCheckReport, error) {
		if input.Mode == "pre-child" {
			os.Exit(23)
		}
		if input.Mode == "closed-group" {
			if err := gitrepository.RunCommand(ctx, exec.Command("sh", "-c", "exit 0")); err != nil {
				os.Exit(37)
			}
			os.Exit(23)
		}
		started := make(chan struct{})
		runningRecorded := make(chan struct{})
		var runningOnce sync.Once
		rt := ctx.Value(nativePresubmitRuntimeKey{}).(*presubmitRuntime)
		record := rt.checkpointRecord
		rt.checkpointRecord = func(out *PresubmitExecutionResult) error {
			if err := record(out); err != nil {
				return err
			}
			checkpoint := out.Checkpoints[len(out.Checkpoints)-1]
			if checkpoint.CommandSequence > 0 && checkpoint.Status == "running" &&
				checkpoint.GroupStarted && !checkpoint.GroupClosed && checkpoint.ProcessGroupID > 0 {
				runningOnce.Do(func() { close(runningRecorded) })
			}
			return nil
		}
		writer := &presubmitStartWriter{started: started}
		go func() {
			cmd := exec.Command("sh", "-c", "printf child-live; read fixture_line || true")
			cmd.Stdout = writer
			cmd.Stdin = os.NewFile(3, "synthetic-fixture-control")
			_ = gitrepository.RunCommand(ctx, cmd)
		}()
		<-started
		<-runningRecorded
		os.Exit(23)
		return ProofCheckReport{}, nil
	})
	os.Exit(24)
}

type presubmitStartWriter struct {
	once    sync.Once
	started chan struct{}
}

func (w *presubmitStartWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	return len(p), nil
}

func lostPresubmitFixture(t *testing.T, s *Store, request PresubmitExecutionRequest, mode string, control *os.File) string {
	t.Helper()
	body, err := json.Marshal(presubmitHelperInput{Home: s.home, Request: request, Mode: mode})
	if err != nil {
		t.Fatal(err)
	}
	helper := exec.Command(os.Args[0], "-test.run=^TestPresubmitKilledProducerHelper$", "-test.timeout=45s")
	helper.Env = append(os.Environ(), presubmitHelperEnv+"="+string(body))
	if control != nil {
		helper.ExtraFiles = []*os.File{control}
	}
	output, err := helper.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("abrupt native helper: %v %s", err, output)
	}
	history, err := s.runAttempts().ForWork(request.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	for _, attempt := range history {
		if attempt.Harness == "proof-check-presubmit" {
			return attempt.ID
		}
	}
	t.Fatal("lost native attempt")
	return ""
}

func TestPresubmitExactRecoveryOnlyPreChildOrClosedGroups(t *testing.T) {
	for _, mode := range []string{"pre-child", "closed-group"} {
		t.Run(mode, func(t *testing.T) {
			s, request := nativePresubmitFixture(t)
			attemptID := lostPresubmitFixture(t, s, request, mode, nil)
			journal, err := s.readPresubmitExecution(attemptID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "closed-group" {
				saw := false
				for _, c := range journal.Checkpoints {
					if c.GroupStarted && c.GroupClosed && c.CommandSequence > 0 {
						saw = true
					}
				}
				if !saw {
					t.Fatal("fixture lacked durable native group closure")
				}
			}
			result, err := s.RecoverPresubmitProducer(request.WorkID, request.Actor, attemptID, "synthetic lost owner with no unproved started group")
			if err != nil {
				t.Fatal(err)
			}
			if result.Kind != "presubmit-execution" || result.Status != "failed" || !result.Quiescent || !result.Released {
				t.Fatalf("recovery is not failed execution-only evidence: %+v", result)
			}
			attempt, err := s.runAttempts().Get(attemptID)
			if err != nil || attempt.Status != runattempt.StatusAbandoned {
				t.Fatalf("lost attempt not terminal: %+v %v", attempt, err)
			}
			w, err := s.Get(request.WorkID)
			if err != nil || w.State != "verifying" || len(w.VerificationIDs) != 0 {
				t.Fatalf("recovery changed certification authority: %+v %v", w, err)
			}
			if !strings.HasPrefix(result.ArtifactRef, presubmitExecutionRefPrefix) {
				t.Fatalf("missing distinct recovery ref: %q", result.ArtifactRef)
			}
		})
	}
}

func TestPresubmitUnprovedGroupCrashRefusesEvenAfterBarrierClears(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	attemptID := lostPresubmitFixture(t, s, request, "unclosed-group", read)
	journal, err := s.readPresubmitExecution(attemptID)
	if err != nil {
		t.Fatal(err)
	}
	sawUnclosed := false
	for _, c := range journal.Checkpoints {
		if c.GroupStarted && !c.GroupClosed && c.ProcessGroupID > 0 {
			sawUnclosed = true
		}
	}
	if !sawUnclosed {
		t.Fatal("fixture lacked durable unclosed started group")
	}
	resource := landlease.ProofResourceForIdentity(s.home, journal.ProofCoordination)
	before, err := os.ReadFile(s.workPath(request.WorkID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecoverPresubmitProducer(request.WorkID, request.Actor, attemptID, "live started group"); err == nil {
		t.Fatal("recovered an unproved live group")
	}
	// The fixture owns this stdin writer. EOF ends its child without any late
	// numeric group signal after abrupt producer loss.
	_ = write.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		pending, e := resource.ExecutionPending(journal.Holder)
		if e != nil {
			t.Fatal(e)
		}
		if !pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture child failed to release its inherited barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The group is gone, but the exact holder lease is still live: a clear
	// barrier and an empty group never outrun lease expiry.
	result, err := s.RecoverPresubmitProducer(request.WorkID, request.Actor, attemptID, "clear barrier cannot replace missing native group evidence")
	if err == nil || result.Released || !strings.Contains(err.Error(), "lease has not expired") {
		t.Fatalf("barrier-only recovery overrode missing started-group proof: result=%+v err=%v", result, err)
	}
	after, readErr := os.ReadFile(s.workPath(request.WorkID))
	if readErr != nil || string(before) != string(after) {
		t.Fatalf("unsupported crash recovery mutated Work authority: %v", readErr)
	}
	status, err := resource.Status(time.Now())
	if err != nil || status.Holder != journal.Holder {
		t.Fatalf("unsupported recovery released exact holder: %+v %v", status, err)
	}
}

// killedGroupPresubmitFixture loses a native producer mid-gate: its journal
// holds a started command group with no durable closure. The caller owns the
// child's stdin writer; closing it ends the child without any signal.
func killedGroupPresubmitFixture(t *testing.T) (*Store, PresubmitExecutionRequest, string, PresubmitExecutionResult, int, *os.File) {
	t.Helper()
	s, request := nativePresubmitFixture(t)
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = read.Close(); _ = write.Close() })
	attemptID := lostPresubmitFixture(t, s, request, "unclosed-group", read)
	journal, err := s.readPresubmitExecution(attemptID)
	if err != nil {
		t.Fatal(err)
	}
	latest := map[int]PresubmitCheckpoint{}
	for _, c := range journal.Checkpoints {
		if c.CommandSequence > 0 {
			latest[c.CommandSequence] = c
		}
	}
	pgid := 0
	for _, c := range latest {
		if c.GroupStarted && !c.GroupClosed && c.ProcessGroupID > 0 {
			pgid = c.ProcessGroupID
		}
	}
	if pgid == 0 {
		t.Fatal("fixture lacked durable unclosed started group")
	}
	return s, request, attemptID, journal, pgid, write
}

func awaitPresubmitRecordedGroupGone(t *testing.T, s *Store, journal PresubmitExecutionResult, pgid int) {
	t.Helper()
	resource := landlease.ProofResourceForIdentity(s.home, journal.ProofCoordination)
	deadline := time.Now().Add(10 * time.Second)
	for {
		pending, err := resource.ExecutionPending(journal.Holder)
		if err != nil {
			t.Fatal(err)
		}
		members, err := presubmitRecordedGroupMembers(pgid)
		if err != nil {
			t.Fatal(err)
		}
		if !pending && members == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture group %d kept %d members (barrier pending=%v)", pgid, members, pending)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A producer killed mid-gate leaves a started group without closure. After its
// exact holder lease expired with no renewal and the kernel reports no member
// of that group, the door releases only that holder as failed evidence.
func TestPresubmitRecoveryReleasesKilledGroupWithNoMembersAfterLeaseExpiry(t *testing.T) {
	s, request, attemptID, journal, pgid, write := killedGroupPresubmitFixture(t)
	_ = write.Close()
	awaitPresubmitRecordedGroupGone(t, s, journal, pgid)
	s.presubmitRecoveryClock = func() time.Time { return time.Now().Add(presubmitLeaseTTL + time.Minute) }
	result, err := s.RecoverPresubmitProducer(request.WorkID, request.Actor, attemptID, "synthetic producer killed mid-gate; recorded group is empty")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" || !result.Quiescent || !result.Released || !slices.Equal(result.RecoveredEmptyGroups, []int{pgid}) {
		t.Fatalf("killed-group recovery is not failed execution-only evidence: %+v", result)
	}
	attempt, err := s.runAttempts().Get(attemptID)
	if err != nil || attempt.Status != runattempt.StatusAbandoned {
		t.Fatalf("killed attempt not terminal: %+v %v", attempt, err)
	}
	w, err := s.Get(request.WorkID)
	if err != nil || w.State != "verifying" || len(w.VerificationIDs) != 0 || !slices.Contains(w.ArtifactIDs, result.ArtifactRef) {
		t.Fatalf("recovery changed certification authority or lost its evidence: %+v %v", w, err)
	}
	status, err := landlease.ProofResourceForIdentity(s.home, journal.ProofCoordination).Status(s.presubmitRecoveryNow())
	if err != nil || status.Holder == journal.Holder {
		t.Fatalf("killed holder was not released: %+v %v", status, err)
	}
}

// A recorded group that still has a member refuses even with a stale lease and
// heartbeat. The member may be the run's own child or an unrelated group that
// reused the number; the door cannot tell them apart, so it never releases.
func TestPresubmitRecoveryRefusesRecordedGroupWithLiveMember(t *testing.T) {
	s, request, attemptID, journal, pgid, write := killedGroupPresubmitFixture(t)
	if members, err := presubmitRecordedGroupMembers(pgid); err != nil || members == 0 {
		t.Fatalf("fixture group %d is not live: members=%d err=%v", pgid, members, err)
	}
	before, err := os.ReadFile(s.workPath(request.WorkID))
	if err != nil {
		t.Fatal(err)
	}
	s.presubmitRecoveryClock = func() time.Time { return time.Now().Add(presubmitLeaseTTL + time.Minute) }
	result, err := s.RecoverPresubmitProducer(request.WorkID, request.Actor, attemptID, "stale lease cannot outweigh a live group member")
	if err == nil || result.Released || !strings.Contains(err.Error(), "live member") {
		t.Fatalf("live recorded group did not refuse on its members: result=%+v err=%v", result, err)
	}
	after, err := os.ReadFile(s.workPath(request.WorkID))
	if err != nil || string(before) != string(after) {
		t.Fatalf("refused recovery mutated Work authority: %v", err)
	}
	status, err := landlease.ProofResourceForIdentity(s.home, journal.ProofCoordination).Status(s.presubmitRecoveryNow())
	if err != nil || status.Holder != journal.Holder {
		t.Fatalf("refused recovery released the holder: %+v %v", status, err)
	}
	// EOF ends the fixture child; nothing signals the recorded group.
	_ = write.Close()
}

func TestPresubmitRecordedGroupMembersCountsWithoutSignalling(t *testing.T) {
	if err := presubmitGroupContainmentSupported(); err != nil {
		t.Skip(err)
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	cmd := exec.Command("sh", "-c", "read fixture_line || true")
	cmd.Stdin = read
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pgid := cmd.Process.Pid
	if members, err := presubmitRecordedGroupMembers(pgid); err != nil || members != 1 {
		_ = write.Close()
		_ = cmd.Wait()
		t.Fatalf("live group members=%d err=%v", members, err)
	}
	_ = write.Close()
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if members, err := presubmitRecordedGroupMembers(pgid); err != nil || members != 0 {
		t.Fatalf("reaped group members=%d err=%v", members, err)
	}
	if _, err := presubmitRecordedGroupMembers(1); err == nil {
		t.Fatal("init's group accepted as a recorded presubmit group")
	}
}
