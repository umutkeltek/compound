package work

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/authority/gitrepository"
	"compound/internal/runattempt"
)

func nativePresubmitFixture(t *testing.T) (*Store, PresubmitExecutionRequest) {
	t.Helper()
	if err := presubmitGroupContainmentSupported(); err != nil {
		t.Skip(err)
	}
	nativeHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(nativeHome, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := Open(nativeHome)
	if err != nil {
		t.Fatal(err)
	}
	s.advanceClock = func(logical time.Time) time.Time { return logical }
	repo := initProofSourceRepo(t, "https://example.invalid/native-presubmit.git")
	source, err := gitrepository.CaptureCleanSnapshot(repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	actor := "native-presubmit-owner"
	w := createDifferentialProofWork(t, s, repo, source, source.BaseSHA, actor, time.Now().Add(-time.Minute))
	if _, err = s.Advance(w.ID, AdvanceInput{To: "running", Actor: actor}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Advance(w.ID, AdvanceInput{To: "verifying", Actor: actor, ArtifactID: "commit:" + source.HeadSHA}, time.Now()); err != nil {
		t.Fatal(err)
	}
	return s, PresubmitExecutionRequest{WorkID: w.ID, Actor: actor, Repo: repo, BaseRef: source.BaseSHA, OutPath: t.TempDir() + "/report.json", Budget: time.Minute}
}

func TestPresubmitProducerAdmitsBeforePreparationAndTerminalizesFailure(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	called := false
	result, err := s.RunPresubmitProducer(context.Background(), request, func(ctx context.Context, _ PresubmitExecutionRequest) (ProofCheckReport, error) {
		called = true
		execution, ok := PresubmitExecutionFromContext(ctx)
		if !ok {
			t.Fatal("preparation lacks native execution authority")
		}
		attempt, err := s.runAttempts().Get(execution.RunAttemptID)
		if err != nil || attempt.ActionIntentID != execution.ActionIntentID || attempt.WorkID != request.WorkID {
			t.Fatalf("attempt not atomically bound before preparation: %+v %v", attempt, err)
		}
		w, err := s.Get(request.WorkID)
		if err != nil || w.PolicyDecisionID != execution.PolicyDecisionID || selectedLastAction(w) != execution.ActionIntentID {
			t.Fatalf("policy/intent not current before preparation: %+v %v", w, err)
		}
		return ProofCheckReport{}, errors.New("synthetic preparation failure")
	})
	if !called || err == nil || !strings.Contains(err.Error(), "synthetic preparation failure") {
		t.Fatalf("preparation outcome: called=%v result=%+v err=%v", called, result, err)
	}
	attempt, getErr := s.runAttempts().Get(result.RunAttemptID)
	if getErr != nil || attempt.Status != runattempt.StatusFailed {
		t.Fatalf("post-mint failure left nonterminal attempt: %+v %v", attempt, getErr)
	}
	if result.Kind != "presubmit-execution" || result.Status != "failed" || result.ArtifactRef == "" {
		t.Fatalf("missing distinct durable execution result: %+v", result)
	}
	w, getErr := s.Get(request.WorkID)
	if getErr != nil || w.State != "verifying" || len(w.VerificationIDs) != 0 {
		t.Fatalf("presubmit changed certifying lifecycle: %+v %v", w, getErr)
	}
	found := false
	for _, ref := range w.ArtifactIDs {
		found = found || ref == result.ArtifactRef
	}
	if !found {
		t.Fatalf("execution-only result not reachable from Work artifacts: %+v", w.ArtifactIDs)
	}
}

func TestPresubmitProducerRejectsForeignActorBeforePreparation(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	request.Actor = "foreign-owner"
	called := false
	_, err := s.RunPresubmitProducer(context.Background(), request, func(context.Context, PresubmitExecutionRequest) (ProofCheckReport, error) {
		called = true
		return ProofCheckReport{}, nil
	})
	if err == nil || called {
		t.Fatalf("foreign actor admitted preparation: called=%v err=%v", called, err)
	}
}

func TestPresubmitPreparationChildCommitsLaunchAndCancellationJoins(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	request.Budget = time.Minute
	result, err := s.RunPresubmitProducer(context.Background(), request, func(ctx context.Context, _ PresubmitExecutionRequest) (ProofCheckReport, error) {
		childCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		capture := &nativePresubmitCancelWriter{cancel: cancel}
		cmd := exec.Command("sh", "-c", "printf preparation-live; sleep 30")
		cmd.Stdout = capture
		childErr := gitrepository.RunCommand(childCtx, cmd)
		if !strings.Contains(capture.body.String(), "preparation-live") {
			t.Fatalf("current raw output lost: %q", capture.body.String())
		}
		return ProofCheckReport{Status: "ok"}, childErr
	})
	if err == nil {
		t.Fatal("deadline reported success")
	}
	attempt, getErr := s.runAttempts().Get(result.RunAttemptID)
	if getErr != nil || attempt.Status != runattempt.StatusFailed || attempt.Launch == nil || attempt.Launch.CommittedAt == nil {
		t.Fatalf("real preparation launch or terminal outcome missing: %+v %v", attempt, getErr)
	}
	if !result.Quiescent || !result.Released {
		t.Fatalf("owned preparation not joined/released: %+v", result)
	}
}

func TestPresubmitCancelledCallbackCannotSucceed(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := false
	result, err := s.RunPresubmitProducer(ctx, request, func(ctx context.Context, _ PresubmitExecutionRequest) (ProofCheckReport, error) {
		entered = true
		cancel()
		return ProofCheckReport{Status: "ok"}, nil
	})
	if !entered || err == nil || result.Status != "failed" {
		t.Fatalf("cancelled callback escaped native failure: entered=%v result=%+v err=%v", entered, result, err)
	}
}

func TestPresubmitPreservesNewerWorkGeneration(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	var newer []byte
	result, err := s.RunPresubmitProducer(context.Background(), request, func(ctx context.Context, _ PresubmitExecutionRequest) (ProofCheckReport, error) {
		if _, err := s.Advance(request.WorkID, AdvanceInput{To: "verifying", Actor: request.Actor, ArtifactID: "newer-generation"}, time.Now()); err != nil {
			t.Fatal(err)
		}
		var readErr error
		newer, readErr = os.ReadFile(s.workPath(request.WorkID))
		if readErr != nil {
			t.Fatal(readErr)
		}
		childErr := gitrepository.RunCommand(ctx, exec.Command("sh", "-c", "exit 0"))
		if childErr == nil {
			t.Fatal("child started after authority drift")
		}
		return ProofCheckReport{}, childErr
	})
	actual, readErr := os.ReadFile(s.workPath(request.WorkID))
	if err == nil || readErr != nil || string(actual) != string(newer) {
		t.Fatalf("newer generation changed: result=%+v err=%v read=%v", result, err, readErr)
	}
	attempt, getErr := s.runAttempts().Get(result.RunAttemptID)
	if getErr != nil || attempt.Status != runattempt.StatusFailed {
		t.Fatalf("drift left nonterminal attempt: %+v %v", attempt, getErr)
	}
}

type nativePresubmitCancelWriter struct {
	body   bytes.Buffer
	cancel context.CancelFunc
}

func (w *nativePresubmitCancelWriter) Write(p []byte) (int, error) {
	n, err := w.body.Write(p)
	w.cancel()
	return n, err
}

func nativePresubmitSuccessfulReport(ctx context.Context, request PresubmitExecutionRequest) (ProofCheckReport, error) {
	source, err := gitrepository.CaptureCleanSnapshotContext(ctx, request.Repo, request.BaseRef)
	if err != nil {
		return ProofCheckReport{}, err
	}
	if err = BindPresubmitSource(ctx, source); err != nil {
		return ProofCheckReport{}, err
	}
	step := ProofCheckStep{Name: "synthetic-native-gate", Command: []string{"sh", "-c", "exit 0"}, ProofRef: "presubmit:synthetic"}
	plan := &ProofCheckPresubmit{Head: source.HeadSHA}
	if err = SealPresubmitInventory(ctx, []ProofCheckStep{step}, plan); err != nil {
		return ProofCheckReport{}, err
	}
	report := ProofCheckReport{SchemaVersion: ProofCheckPresubmitSchemaVersion, ProfileKind: ProofCheckProfilePresubmit, Status: "ok", SourceSnapshot: &source, Presubmit: plan}
	return RunNonCertifyingProofGates(ctx, report, []ProofCheckStep{step}, func(ctx context.Context, step ProofCheckStep) ProofCheckStep {
		step.Status = "ok"
		if err := gitrepository.RunCommand(ctx, exec.Command(step.Command[0], step.Command[1:]...)); err != nil {
			step.Status = "failed"
			step.OutputTail = err.Error()
		}
		return step
	})
}

func TestPresubmitRetryLineageRecoversFailedNativeExecution(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	first, err := s.RunPresubmitProducer(context.Background(), request, func(ctx context.Context, _ PresubmitExecutionRequest) (ProofCheckReport, error) {
		return ProofCheckReport{}, gitrepository.RunCommand(ctx, exec.Command("sh", "-c", "exit 9"))
	})
	if err == nil {
		t.Fatal("first native execution did not fail")
	}
	second, err := s.RunPresubmitProducer(context.Background(), request, nativePresubmitSuccessfulReport)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := s.runAttempts().Get(second.RunAttemptID)
	if err != nil || attempt.PredecessorID != first.RunAttemptID || !runAttemptHasExecuted(attempt) || attempt.Status != runattempt.StatusSucceeded {
		t.Fatalf("actual native retry lineage missing: %+v %v", attempt, err)
	}
	w, err := s.Get(request.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	attempts := map[string]runattempt.RunAttempt{}
	for _, id := range w.RunAttemptIDs {
		attempts[id], err = s.runAttempts().Get(id)
		if err != nil {
			t.Fatal(err)
		}
	}
	history, err := buildAttachedRunAttemptHistory(w.RunAttemptIDs, attempts)
	if err != nil || !history.SuccessfulRetryPredecessors[first.RunAttemptID] {
		t.Fatalf("native retry still wedges unchanged close validator: %+v %v", history, err)
	}
}

func TestPresubmitRefusesOverlappingNativeBeforeReplacingAuthority(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	result, err := s.RunPresubmitProducer(context.Background(), request, func(ctx context.Context, _ PresubmitExecutionRequest) (ProofCheckReport, error) {
		before, err := os.ReadFile(s.workPath(request.WorkID))
		if err != nil {
			t.Fatal(err)
		}
		called := false
		_, overlapErr := s.RunPresubmitProducer(context.Background(), request, func(context.Context, PresubmitExecutionRequest) (ProofCheckReport, error) {
			called = true
			return ProofCheckReport{}, nil
		})
		after, readErr := os.ReadFile(s.workPath(request.WorkID))
		if overlapErr == nil || called || readErr != nil || string(before) != string(after) {
			t.Fatalf("overlap replaced live native authority: called=%v overlap=%v read=%v", called, overlapErr, readErr)
		}
		return ProofCheckReport{}, errors.New("synthetic first owner failure")
	})
	if err == nil || result.Status != "failed" {
		t.Fatalf("native first owner outcome: %+v %v", result, err)
	}
}

func TestPresubmitPanicCannotPersistSuccessfulExecution(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	attemptID := ""
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("native panic was swallowed")
			}
		}()
		_, _ = s.RunPresubmitProducer(context.Background(), request, func(ctx context.Context, _ PresubmitExecutionRequest) (ProofCheckReport, error) {
			execution, _ := PresubmitExecutionFromContext(ctx)
			attemptID = execution.RunAttemptID
			panic("synthetic native panic")
		})
	}()
	attempt, err := s.runAttempts().Get(attemptID)
	if err != nil || attempt.Status != runattempt.StatusFailed {
		t.Fatalf("panic persisted successful/nonterminal attempt: %+v %v", attempt, err)
	}
	result, err := s.readPresubmitExecution(attemptID)
	if err != nil || result.Status != "failed" || !strings.Contains(result.FailureReason, "synthetic native panic") {
		t.Fatalf("panic sealed false success artifact: %+v %v", result, err)
	}
}

func TestPresubmitFirstMetadataFailureTerminalizesAtomicallyBoundAttempt(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	prior := presubmitStampOrigin
	presubmitStampOrigin = func(*runattempt.Store, string) (runattempt.RunAttempt, error) {
		return runattempt.RunAttempt{}, errors.New("synthetic first metadata fault")
	}
	defer func() { presubmitStampOrigin = prior }()
	called := false
	result, err := s.RunPresubmitProducer(context.Background(), request, func(context.Context, PresubmitExecutionRequest) (ProofCheckReport, error) {
		called = true
		return ProofCheckReport{}, nil
	})
	if err == nil || called {
		t.Fatalf("first metadata fault entered preparation: called=%v err=%v", called, err)
	}
	attempt, err := s.runAttempts().Get(result.RunAttemptID)
	if err != nil || attempt.Status != runattempt.StatusFailed || attempt.ActionIntentID != result.ActionIntentID {
		t.Fatalf("first metadata failure broke atomic audit/terminalization: %+v %v", attempt, err)
	}
	artifact, err := s.readPresubmitExecution(attempt.ID)
	if err != nil || artifact.Status != "failed" {
		t.Fatalf("metadata failure lacks truthful durable result: %+v %v", artifact, err)
	}
}

func TestPresubmitFailedExecutionCannotRenderGreenVerdict(t *testing.T) {
	report := ProofCheckReport{SchemaVersion: ProofCheckPresubmitSchemaVersion, ProfileKind: ProofCheckProfilePresubmit, Status: "ok", Steps: []ProofCheckStep{{Name: "synthetic", Status: "ok"}}}
	result := PresubmitExecutionResult{Kind: presubmitExecutionKind, Status: "failed", Report: &report}
	var stdout, stderr bytes.Buffer
	code := renderNativePresubmitExecution(result, errors.New("synthetic finalization failure"), nil, &stdout, &stderr)
	if code == 0 || !strings.Contains(stdout.String(), "FAILED") || strings.Contains(stdout.String(), "presubmit: ok") {
		t.Fatalf("failed native operation rendered green: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestPresubmitClosedPipesDescendantCancelledBeforeLeaderReap(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	result, err := s.RunPresubmitProducer(context.Background(), request, func(ctx context.Context, _ PresubmitExecutionRequest) (ProofCheckReport, error) {
		childCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		cmd := exec.Command("sh", "-c", "(exec 1>&- 2>&-; sleep 30) & exit 0")
		return ProofCheckReport{}, gitrepository.RunCommand(childCtx, cmd)
	})
	if err == nil || !result.Quiescent || !result.Released {
		t.Fatalf("same-group closed-pipe descendant survived ordinary deadline: result=%+v err=%v", result, err)
	}
	attempt, err := s.runAttempts().Get(result.RunAttemptID)
	if err != nil || attempt.Launch == nil || attempt.Launch.CommittedAt == nil {
		t.Fatalf("closed-pipes test never exercised actual child: %+v %v", attempt, err)
	}
}
