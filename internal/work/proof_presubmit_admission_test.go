package work

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"compound/internal/landlease"
	"compound/internal/runattempt"
)

type presubmitTestHolder struct {
	resource *landlease.Resource
	barrier  *landlease.ExecutionBarrier
	holder   string
}

func (h presubmitTestHolder) release(t *testing.T) {
	t.Helper()
	if err := h.barrier.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.resource.Release(h.holder, time.Now()); err != nil {
		t.Fatal(err)
	}
}
func presubmitTestTicket(t *testing.T, s *Store, repo string) *landlease.ProofAdmission {
	t.Helper()
	identity, err := landlease.ProofCoordinationFor(s.home, landlease.ProofCapacitySystemFull, strings.Repeat(repo, 64))
	if err != nil || identity == nil {
		t.Fatalf("identity: %v", err)
	}
	ticket, err := landlease.BeginProofAdmission(s.home, *identity, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ticket.Cancel(); err != nil {
			t.Error(err)
		}
	})
	return ticket
}
func presubmitTestAcquire(holder string, result chan<- presubmitTestHolder) func(*landlease.Resource) error {
	return func(r *landlease.Resource) error {
		_, b, err := r.AcquireWithExecutionBarrierPrepared(holder, time.Minute, time.Now(), func(landlease.AcquireOutcome) error { return nil })
		if err == nil {
			result <- presubmitTestHolder{r, b, holder}
		}
		return err
	}
}
func presubmitTestOccupy(t *testing.T, s *Store, repo string) presubmitTestHolder {
	t.Helper()
	ticket := presubmitTestTicket(t, s, repo)
	result := make(chan presubmitTestHolder, 1)
	if err := ticket.WithTurn(context.Background(), false, presubmitTestAcquire("holder-"+repo, result)); err != nil {
		t.Fatal(err)
	}
	if err := ticket.Cancel(); err != nil {
		t.Fatal(err)
	}
	h := <-result
	t.Cleanup(func() { _ = h.barrier.Close(); _, _ = h.resource.Release(h.holder, time.Now()) })
	return h
}
func presubmitTestTick(t *testing.T, ticks chan<- time.Time) {
	t.Helper()
	select {
	case ticks <- time.Now():
	case <-time.After(5 * time.Second):
		t.Fatal("wait loop did not reach bounded tick")
	}
}

// presubmitTestTickUntilDone ticks until the loop returns. A release can race the
// iteration the previous tick already started. That iteration may admit without
// another tick, so an unconditional tick here would wait on a finished loop.
func presubmitTestTickUntilDone(t *testing.T, ticks chan<- time.Time, done <-chan error) error {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case err := <-done:
			return err
		case ticks <- time.Now():
		case <-deadline:
			t.Fatal("wait loop did not return")
			return nil
		}
	}
}

func presubmitTestResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("wait loop did not return")
		return nil
	}
}

func TestPresubmitAdmissionRetainsFIFOAgainstNewerWaiter(t *testing.T) {
	s := coordinatedStoreFixture(t)
	first := presubmitTestOccupy(t, s, "a")
	_ = presubmitTestOccupy(t, s, "b")
	a := presubmitTestTicket(t, s, "c")
	b := presubmitTestTicket(t, s, "d")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ticksA, ticksB := make(chan time.Time), make(chan time.Time)
	doneA, doneB := make(chan error, 1), make(chan error, 1)
	enteredA, enteredB := make(chan presubmitTestHolder, 1), make(chan presubmitTestHolder, 1)
	check := func(c context.Context) error { return context.Cause(c) }
	// A parks in its second check, before its next turn. No iteration of A can then
	// be in flight across the release, so the free slot is still free when B tries
	// it, and only FIFO order can stop B.
	var callsA atomic.Int32
	parkedA, resumeA := make(chan struct{}), make(chan struct{})
	checkA := func(c context.Context) error {
		if callsA.Add(1) == 2 {
			close(parkedA)
			select {
			case <-resumeA:
			case <-c.Done():
			}
		}
		return context.Cause(c)
	}
	go func() {
		doneA <- withPresubmitAdmission(a, func() error {
			return waitPresubmitAdmission(ctx, a, true, ticksA, checkA, presubmitTestAcquire("waiter-a", enteredA))
		})
	}()
	go func() {
		doneB <- withPresubmitAdmission(b, func() error {
			return waitPresubmitAdmission(ctx, b, true, ticksB, check, presubmitTestAcquire("waiter-b", enteredB))
		})
	}()
	// Sending ticks proves each loop already encountered contention; no sleep or
	// assumption about which goroutine was scheduled first establishes ordering.
	presubmitTestTick(t, ticksA)
	select {
	case <-parkedA:
	case <-time.After(5 * time.Second):
		t.Fatal("head waiter did not reach its next check")
	}
	presubmitTestTick(t, ticksB)
	presubmitTestTick(t, ticksB)
	if len(enteredA)+len(enteredB) != 0 {
		t.Fatal("producer entered while both slots held")
	}
	first.release(t)
	// The newer contender must still refuse even though a slot just became free.
	presubmitTestTick(t, ticksB)
	presubmitTestTick(t, ticksB)
	if len(enteredA)+len(enteredB) != 0 {
		t.Fatal("new waiter overtook retained FIFO position")
	}
	close(resumeA)
	if err := presubmitTestTickUntilDone(t, ticksA, doneA); err != nil {
		t.Fatal(err)
	}
	admittedA := <-enteredA
	// The second send proves the iteration the first one started already refused
	// while A held the capacity. Only then does the check below prove anything.
	presubmitTestTick(t, ticksB)
	presubmitTestTick(t, ticksB)
	if len(enteredB) != 0 {
		t.Fatal("second waiter double-booked capacity")
	}
	admittedA.release(t)
	if err := presubmitTestTickUntilDone(t, ticksB, doneB); err != nil {
		t.Fatal(err)
	}
	(<-enteredB).release(t)
	if len(enteredA)+len(enteredB) != 0 {
		t.Fatal("admission callback ran more than once")
	}
}

func TestPresubmitAdmissionCancellationAndAuthorityFailurePreserveForeignCapacity(t *testing.T) {
	for _, kind := range []string{"cancel", "deadline", "authority", "heartbeat"} {
		t.Run(kind, func(t *testing.T) {
			s := coordinatedStoreFixture(t)
			first := presubmitTestOccupy(t, s, "a")
			_ = presubmitTestOccupy(t, s, "b")
			ticket := presubmitTestTicket(t, s, "c")
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(context.Canceled)
			ticks := make(chan time.Time)
			done := make(chan error, 1)
			sentinel := errors.New(kind + " stopped")
			if kind == "deadline" {
				sentinel = context.DeadlineExceeded
			}
			checks := 0
			go func() {
				done <- withPresubmitAdmission(ticket, func() error {
					return waitPresubmitAdmission(ctx, ticket, true, ticks, func(c context.Context) error {
						checks++
						if checks == 2 && (kind == "authority" || kind == "heartbeat") {
							return sentinel
						}
						return context.Cause(c)
					}, func(*landlease.Resource) error { return errors.New("unexpected admission") })
				})
			}()
			if kind == "authority" || kind == "heartbeat" {
				presubmitTestTick(t, ticks)
			} else {
				// This send only succeeds after the first real contention refusal.
				presubmitTestTick(t, ticks)
				cancel(sentinel)
			}
			if err := presubmitTestResult(t, done); !errors.Is(err, sentinel) {
				t.Fatalf("cause lost: %v", err)
			}
			status, err := first.resource.Status(time.Now())
			if err != nil || status.Holder != first.holder || !status.Held {
				t.Fatalf("foreign holder changed: %+v %v", status, err)
			}
			// Canceled ticket must not block a new eligible ticket after release.
			first.release(t)
			next := presubmitTestTicket(t, s, "d")
			result := make(chan presubmitTestHolder, 1)
			if err := next.WithTurn(context.Background(), false, presubmitTestAcquire("after-cancel", result)); err != nil {
				t.Fatal(err)
			}
			(<-result).release(t)
		})
	}
}

func TestPresubmitAdmissionRefusalAndCallbackErrorsDoNotRetry(t *testing.T) {
	s := coordinatedStoreFixture(t)
	first := presubmitTestOccupy(t, s, "a")
	_ = presubmitTestOccupy(t, s, "b")
	a := presubmitTestTicket(t, s, "c")
	err := withPresubmitAdmission(a, func() error {
		return waitPresubmitAdmission(context.Background(), a, false, nil, func(context.Context) error { return nil }, func(*landlease.Resource) error { t.Fatal("busy callback"); return nil })
	})
	if !presubmitContention(err) {
		t.Fatalf("default did not refuse capacity: %v", err)
	}
	first.release(t)
	b := presubmitTestTicket(t, s, "d")
	sentinel := &landlease.ProofQueueBusyError{EarlierTicket: "callback-shaped-error"}
	calls := 0
	err = withPresubmitAdmission(b, func() error {
		return waitPresubmitAdmission(context.Background(), b, true, nil, func(context.Context) error { return nil }, func(*landlease.Resource) error { calls++; return sentinel })
	})
	if !errors.Is(err, sentinel) || calls != 1 {
		t.Fatalf("callback retried: %d %v", calls, err)
	}
	for _, err := range []error{errors.New("integrity"), &landlease.ProofAdmissionBusyError{Holder: "same-holder", Reason: "repository descendants remain live"}, &landlease.ProofAdmissionBusyError{Holder: "same-holder", Reason: "same repository requires exact predecessor recovery"}} {
		if presubmitContention(err) {
			t.Fatalf("recovery/integrity classified as contention: %v", err)
		}
	}
}

// A neighbour's short snapshot can hold the slot's class lock at the moment the
// head waiter acquires it. That must not end the waiter and hand its place away.
func TestPresubmitAdmissionSurvivesClassLockBusyAfterEntry(t *testing.T) {
	s := coordinatedStoreFixture(t)
	ticket := presubmitTestTicket(t, s, "a")
	entered := make(chan presubmitTestHolder, 1)
	acquire := presubmitTestAcquire("waiter", entered)
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	calls := 0
	err := withPresubmitAdmission(ticket, func() error {
		return waitPresubmitAdmission(context.Background(), ticket, true, ticks, func(context.Context) error { return nil }, func(r *landlease.Resource) error {
			calls++
			if calls == 1 {
				return landlease.NewProofClassLockBusyError()
			}
			return acquire(r)
		})
	})
	if err != nil || calls != 2 {
		t.Fatalf("a busy class lock must retry: calls=%d err=%v", calls, err)
	}
	(<-entered).release(t)

	// Fail-fast callers still see the refusal at once.
	next := presubmitTestTicket(t, s, "b")
	calls = 0
	err = withPresubmitAdmission(next, func() error {
		return waitPresubmitAdmission(context.Background(), next, false, nil, func(context.Context) error { return nil }, func(*landlease.Resource) error {
			calls++
			return landlease.NewProofClassLockBusyError()
		})
	})
	var busy *landlease.ProofAdmissionBusyError
	if !errors.As(err, &busy) || calls != 1 {
		t.Fatalf("no-wait must not retry: calls=%d err=%v", calls, err)
	}
}

func TestPresubmitAdmissionPanicCancelsOnlyItsTicket(t *testing.T) {
	s := coordinatedStoreFixture(t)
	a := presubmitTestTicket(t, s, "a")
	b := presubmitTestTicket(t, s, "b")
	func() {
		defer func() {
			if recover() != "synthetic panic" {
				t.Error("panic not preserved")
			}
		}()
		_ = withPresubmitAdmission(a, func() error { panic("synthetic panic") })
	}()
	result := make(chan presubmitTestHolder, 1)
	if err := b.WithTurn(context.Background(), false, presubmitTestAcquire("after-panic", result)); err != nil {
		t.Fatal(err)
	}
	(<-result).release(t)
}

func TestPresubmitWaitNativeRefusalsAndPhaseCleanup(t *testing.T) {
	for _, kind := range []string{"negative", "uncoordinated", "admitted"} {
		t.Run(kind, func(t *testing.T) {
			s, request := nativePresubmitFixture(t)
			request.Wait = time.Minute
			if kind == "negative" {
				request.Wait = -time.Second
			}
			if kind == "admitted" {
				backing, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				if err = landlease.MigrateLegacyLayout(s.home, backing, func() error { return nil }); err != nil {
					t.Fatal(err)
				}
				canonical, err := filepath.EvalSymlinks(s.home)
				if err != nil {
					t.Fatal(err)
				}
				if err = landlease.MigrateProofDomain(backing, []string{canonical}, func() error { return nil }, nil, landlease.ProofDomainFencedV3); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			result, err := s.RunPresubmitProducer(context.Background(), request, func(ctx context.Context, r PresubmitExecutionRequest) (ProofCheckReport, error) {
				called = true
				if context.Cause(ctx) != nil {
					t.Fatalf("finished phase poisoned producer: %v", context.Cause(ctx))
				}
				return nativePresubmitSuccessfulReport(ctx, r)
			})
			if kind == "admitted" {
				if err != nil || !called || result.Status != "succeeded" || !result.Quiescent || !result.Released {
					t.Fatalf("admitted cleanup: %+v %v", result, err)
				}
			} else if err == nil || called {
				t.Fatalf("invalid wait entered producer: %+v %v", result, err)
			}
			if kind == "uncoordinated" {
				if _, err := os.Stat(request.OutPath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("refusal created output: %v", err)
				}
			}
		})
	}
}

func TestPresubmitNativeBudgetExpiryPreservesDeadlineIdentity(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	request.Budget = time.Nanosecond
	called := false
	_, err := s.RunPresubmitProducer(context.Background(), request, func(context.Context, PresubmitExecutionRequest) (ProofCheckReport, error) {
		called = true
		return ProofCheckReport{}, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || called {
		t.Fatalf("deadline compatibility: called=%v err=%v", called, err)
	}
}

func TestPresubmitNativeWaitExcludesOnlyQueueFromActiveBudget(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	backing, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = landlease.MigrateLegacyLayout(s.home, backing, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(s.home)
	if err != nil {
		t.Fatal(err)
	}
	if err = landlease.MigrateProofDomain(backing, []string{canonical}, func() error { return nil }, nil, landlease.ProofDomainFencedV3); err != nil {
		t.Fatal(err)
	}
	first := presubmitTestOccupy(t, s, "a")
	_ = presubmitTestOccupy(t, s, "b")
	generation, err := landlease.ProofCoordinationGeneration(s.home)
	if err != nil {
		t.Fatal(err)
	}
	queuePath := filepath.Join(backing, "landlease", "proof", generation, "admission", "state", "proof-queue.json")
	request.Budget = 4 * time.Second
	request.Wait = 20 * time.Second
	// Use the existing metadata fault-injection seam to consume measurable real
	// preparation time. This distinguishes conservation from resetting a budget.
	stamp := presubmitStampOrigin
	var preparationCharge time.Duration
	presubmitStampOrigin = func(store *runattempt.Store, id string) (runattempt.RunAttempt, error) {
		started := time.Now()
		timer := time.NewTimer(250 * time.Millisecond)
		<-timer.C
		result, err := stamp(store, id)
		preparationCharge = time.Since(started)
		return result, err
	}
	defer func() { presubmitStampOrigin = stamp }()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	type outcome struct {
		result    PresubmitExecutionResult
		err       error
		remaining time.Duration
		called    bool
	}
	done := make(chan outcome, 1)
	sentinel := errors.New("observed admitted active budget")
	go func() {
		var result outcome
		result.result, result.err = s.RunPresubmitProducer(ctx, request, func(c context.Context, _ PresubmitExecutionRequest) (ProofCheckReport, error) {
			result.called = true
			deadline, _ := c.Deadline()
			result.remaining = time.Until(deadline)
			return ProofCheckReport{}, sentinel
		})
		done <- result
	}()
	// Observe the actual durable ticket before starting the elapsed-budget
	// control. The timer deliberately exceeds the active budget, not a scheduling
	// guess about when the waiter entered the queue.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	ticketID := ""
	for ticketID == "" {
		select {
		case <-ctx.Done():
			t.Fatal("native waiter never enqueued")
		case result := <-done:
			t.Fatalf("native waiter exited before enqueue: %+v", result)
		case <-ticker.C:
			body, e := os.ReadFile(queuePath)
			if e != nil {
				continue
			}
			var q struct{ Records []struct{ ID string } }
			if e = json.Unmarshal(body, &q); e != nil {
				t.Fatal(e)
			}
			if len(q.Records) == 1 {
				ticketID = q.Records[0].ID
			}
		}
	}
	select {
	case result := <-done:
		t.Fatalf("queue consumed active allowance: %+v", result)
	case <-time.After(5 * time.Second):
	}
	body, err := os.ReadFile(queuePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), ticketID) {
		t.Fatal("waiter replaced original FIFO ticket")
	}
	first.release(t)
	var result outcome
	select {
	case result = <-done:
	case <-ctx.Done():
		t.Fatal("native waiter did not admit after release")
	}
	if !result.called || !errors.Is(result.err, sentinel) || result.remaining <= 0 || result.remaining > request.Budget-preparationCharge {
		t.Fatalf("active budget was reset/consumed by queue or producer failed: %+v", result)
	}
	if !result.result.Quiescent || !result.result.Released {
		t.Fatalf("admitted failure not finalized: %+v", result.result)
	}
	body, err = os.ReadFile(queuePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), ticketID) {
		t.Fatal("terminal waiter left FIFO ticket")
	}
}

func TestPresubmitNativeWaitingAuthorityDrift(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	backing, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = landlease.MigrateLegacyLayout(s.home, backing, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(s.home)
	if err != nil {
		t.Fatal(err)
	}
	if err = landlease.MigrateProofDomain(backing, []string{canonical}, func() error { return nil }, nil, landlease.ProofDomainFencedV3); err != nil {
		t.Fatal(err)
	}
	first := presubmitTestOccupy(t, s, "a")
	_ = presubmitTestOccupy(t, s, "b")
	generation, err := landlease.ProofCoordinationGeneration(s.home)
	if err != nil {
		t.Fatal(err)
	}
	queuePath := filepath.Join(backing, "landlease", "proof", generation, "admission", "state", "proof-queue.json")
	request.Wait = time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	type outcome struct {
		result PresubmitExecutionResult
		err    error
		called bool
	}
	done := make(chan outcome, 1)
	go func() {
		var o outcome
		o.result, o.err = s.RunPresubmitProducer(ctx, request, func(context.Context, PresubmitExecutionRequest) (ProofCheckReport, error) {
			o.called = true
			return ProofCheckReport{}, nil
		})
		done <- o
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	ticketID := ""
	for ticketID == "" {
		select {
		case <-ctx.Done():
			t.Fatal("waiter never enqueued")
		case o := <-done:
			t.Fatalf("waiter exited before drift: %+v", o)
		case <-ticker.C:
			body, e := os.ReadFile(queuePath)
			if e != nil {
				continue
			}
			var q struct{ Records []struct{ ID string } }
			if e = json.Unmarshal(body, &q); e != nil {
				t.Fatal(e)
			}
			if len(q.Records) == 1 {
				ticketID = q.Records[0].ID
			}
		}
	}
	if err := s.withLifecycleMutation(func() error {
		w, e := s.Get(request.WorkID)
		if e != nil {
			return e
		}
		w.PolicyDecisionID = "pol_newer_synthetic_authority"
		return s.writeJSON(s.workPath(w.ID), &w)
	}); err != nil {
		t.Fatal(err)
	}
	var o outcome
	select {
	case o = <-done:
	case <-ctx.Done():
		t.Fatal("authority drift not observed")
	}
	if o.called || o.err == nil || !strings.Contains(o.err.Error(), "authority") {
		t.Fatalf("drift admitted producer: %+v", o)
	}
	w, err := s.Get(request.WorkID)
	if err != nil || w.PolicyDecisionID != "pol_newer_synthetic_authority" {
		t.Fatalf("newer authority overwritten: %+v %v", w, err)
	}
	status, err := first.resource.Status(time.Now())
	if err != nil || status.Holder != first.holder || !status.Held {
		t.Fatalf("foreign holder changed: %+v %v", status, err)
	}
	body, err := os.ReadFile(queuePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), ticketID) {
		t.Fatal("authority-refused waiter retained FIFO position")
	}
}

func TestPresubmitNativeWaitDeadlineIsTerminalWithoutAcquisition(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	backing, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = landlease.MigrateLegacyLayout(s.home, backing, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(s.home)
	if err != nil {
		t.Fatal(err)
	}
	if err = landlease.MigrateProofDomain(backing, []string{canonical}, func() error { return nil }, nil, landlease.ProofDomainFencedV3); err != nil {
		t.Fatal(err)
	}
	first := presubmitTestOccupy(t, s, "a")
	_ = presubmitTestOccupy(t, s, "b")
	request.Wait = 100 * time.Millisecond
	called := false
	result, err := s.RunPresubmitProducer(context.Background(), request, func(context.Context, PresubmitExecutionRequest) (ProofCheckReport, error) {
		called = true
		return ProofCheckReport{}, nil
	})
	if called || !errors.Is(err, context.DeadlineExceeded) || result.Status != "failed" || result.LeasePrepared {
		t.Fatalf("wait expiry: called=%v result=%+v err=%v", called, result, err)
	}
	attempt, err := s.runAttempts().Get(result.RunAttemptID)
	if err != nil || attempt.Status != runattempt.StatusFailed {
		t.Fatalf("wait left live attempt: %+v %v", attempt, err)
	}
	status, err := first.resource.Status(time.Now())
	if err != nil || status.Holder != first.holder || !status.Held {
		t.Fatalf("foreign holder changed: %+v %v", status, err)
	}
	generation, err := landlease.ProofCoordinationGeneration(s.home)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(backing, "landlease", "proof", generation, "admission", "state", "proof-queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	var queue struct{ Records []json.RawMessage }
	if err = json.Unmarshal(body, &queue); err != nil || len(queue.Records) != 0 {
		t.Fatalf("expired ticket retained: %s %v", body, err)
	}
}

func TestPresubmitNativeParentDeadlineWinsOverWait(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	request.Wait = time.Hour
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	called := false
	_, err := s.RunPresubmitProducer(ctx, request, func(context.Context, PresubmitExecutionRequest) (ProofCheckReport, error) {
		called = true
		return ProofCheckReport{}, nil
	})
	if called || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired parent was renewed: %v", err)
	}
}

func TestPresubmitNativeDeadlineDuringCleanup(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	request.Budget = 3 * time.Second
	cleanupStartedBeforeExpiry := false
	sentinel := errors.New("synthetic producer refused before cleanup")
	result, err := s.RunPresubmitProducer(context.Background(), request, func(ctx context.Context, r PresubmitExecutionRequest) (ProofCheckReport, error) {
		rt := ctx.Value(nativePresubmitRuntimeKey{}).(*presubmitRuntime)
		rt.stopHeartbeat()
		<-rt.heartbeatDone
		// The producer refuses before any gate; cleanup still has an obligation that
		// completes at the operation deadline. No subprocess or lock is abandoned.
		rt.heartbeatDone = make(chan struct{})
		rt.stopHeartbeat = func() { cleanupStartedBeforeExpiry = ctx.Err() == nil; <-ctx.Done(); close(rt.heartbeatDone) }
		return ProofCheckReport{}, sentinel
	})
	if !cleanupStartedBeforeExpiry || !errors.Is(err, sentinel) || !errors.Is(err, context.DeadlineExceeded) || result.Status != "failed" || !result.Quiescent || !result.Released {
		t.Fatalf("late cleanup: before_expiry=%v status=%s quiescent=%v released=%v err=%v", cleanupStartedBeforeExpiry, result.Status, result.Quiescent, result.Released, err)
	}
}
