package work

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
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
		writer := &presubmitStartWriter{started: started}
		go func() {
			cmd := exec.Command("sh", "-c", "printf child-live; read fixture_line || true")
			cmd.Stdout = writer
			cmd.Stdin = os.NewFile(3, "synthetic-fixture-control")
			_ = gitrepository.RunCommand(ctx, cmd)
		}()
		<-started
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
	result, err := s.RecoverPresubmitProducer(request.WorkID, request.Actor, attemptID, "clear barrier cannot replace missing native group evidence")
	if err == nil || result.Released {
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
