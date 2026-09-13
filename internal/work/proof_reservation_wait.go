package work

import (
	"errors"
	"fmt"
	"io"
	"time"

	"compound/internal/landlease"
)

// proofResourceWaitPoll bounds each sleep of a queued reservation; the lease
// watcher wakes it sooner when the holder releases.
const proofResourceWaitPoll = 5 * time.Second

// ReserveProofResourceWhenFree queues for a busy proof resource instead of
// refusing. ReserveProofResource mints its RunAttempt before it tries the
// land-lease, so every refusal leaves an abandoned attempt that close must later
// acknowledge. This waits, writing nothing, until no live lease is held, then
// reserves; a lease taken in the gap refuses that one attempt as usual and the
// wait resumes until wait runs out. Each holder it waits on is named on progress.
func (s *Store) ReserveProofResourceWhenFree(
	workID string,
	in ProofResourceReserveInput,
	wait time.Duration,
	clk landlease.Clock,
	progress io.Writer,
) (ProofResourceReserveResult, error) {
	if in.Steal {
		return ProofResourceReserveResult{}, fmt.Errorf("proof resource wait cannot be combined with steal")
	}
	deadline := clk.Now().Add(wait)
	announced := ""
	for {
		status, err := landlease.StatusReadOnly(s.home, clk.Now())
		if err == nil && status.Held && status.Holder != announced && progress != nil {
			announced = status.Holder
			fmt.Fprintf(progress, "proof resource busy: waiting for %s (lease expires %s)\n", status.Holder, status.ExpiresAt)
		}
		if _, err := landlease.WaitUntilFree(s.home, clk, deadline.Sub(clk.Now()), proofResourceWaitPoll); err != nil {
			return ProofResourceReserveResult{}, fmt.Errorf("proof resource wait: %w", err)
		}
		result, err := s.ReserveProofResource(workID, in, clk.Now())
		var held *ProofResourceHeldError
		if errors.As(err, &held) && clk.Now().Before(deadline) {
			continue
		}
		return result, err
	}
}
