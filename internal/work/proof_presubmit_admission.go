package work

import (
	"context"
	"errors"
	"time"

	"compound/internal/landlease"
)

// admit retains the existing FIFO ticket across ordinary contention. It does
// not recover predecessors, mint another attempt, or refresh foreign resources.
func (rt *presubmitRuntime) admit(ctx context.Context, admission *landlease.ProofAdmission, acquire func(*landlease.Resource) error) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	check := func(ctx context.Context) error {
		if err := rt.check(ctx); err != nil {
			return err
		}
		if rt.out.Request.Wait > 0 {
			_, err := rt.store.runAttempts().Heartbeat(rt.out.RunAttemptID, time.Now())
			return err
		}
		return nil
	}
	return waitPresubmitAdmission(ctx, admission, rt.out.Request.Wait > 0, ticker.C, check, acquire)
}

// Native admission locks keep their existing lock ordering. Cancellation is
// observed between calls; this loop never abandons a goroutine holding a lock.
func waitPresubmitAdmission(ctx context.Context, admission *landlease.ProofAdmission, wait bool, ticks <-chan time.Time, check func(context.Context) error, acquire func(*landlease.Resource) error) error {
	for {
		if err := check(ctx); err != nil {
			return err
		}
		entered := false
		err := admission.WithTurn(ctx, false, func(resource *landlease.Resource) error {
			entered = true
			return acquire(resource)
		})
		if cause := context.Cause(ctx); cause != nil {
			return errors.Join(err, cause)
		}
		if err == nil || !wait || entered || !presubmitContention(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-ticks:
		}
	}
}

func presubmitContention(err error) bool {
	var queue *landlease.ProofQueueBusyError
	var busy *landlease.ProofAdmissionBusyError
	return errors.As(err, &queue) || (errors.As(err, &busy) && busy.CanWait())
}

// Retain cleanup on panic as well as ordinary errors. Cleanup errors never turn
// a failed admission into success.
func withPresubmitAdmission(admission *landlease.ProofAdmission, run func() error) (err error) {
	defer func() { err = errors.Join(err, admission.Cancel()) }()
	return run()
}
