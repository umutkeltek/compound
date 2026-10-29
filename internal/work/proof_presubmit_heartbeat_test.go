package work

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPresubmitHeartbeatShutdownPreservesExecutionCause(t *testing.T) {
	authorityLost := errors.New("synthetic authority loss")
	parentCancelled := errors.New("synthetic execution cancellation")
	for _, tc := range []struct {
		name       string
		refreshErr error
		parentErr  error
		want       error
	}{
		{name: "normal shutdown"},
		{name: "authority failure during shutdown", refreshErr: authorityLost, want: authorityLost},
		{name: "execution cancellation during shutdown", parentErr: parentCancelled, want: parentCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			stopCtx, stop := context.WithCancel(ctx)
			defer stop()
			ticks := make(chan time.Time, 1)
			entered, done := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(done)
				runPresubmitHeartbeat(ctx, stopCtx, ticks, func(checkCtx context.Context) error {
					close(entered)
					// Hold an in-flight check until shutdown, as when the
					// lifecycle lock delays the production authority check.
					<-stopCtx.Done()
					if checkCtx.Err() != nil {
						return context.Cause(checkCtx)
					}
					return tc.refreshErr
				}, cancel)
			}()
			ticks <- time.Now()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("heartbeat did not enter its check")
			}
			if tc.parentErr != nil {
				cancel(tc.parentErr)
			}
			stop()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("shutdown did not drain heartbeat")
			}
			if got := context.Cause(ctx); !errors.Is(got, tc.want) {
				t.Fatalf("execution cause = %v; want %v", got, tc.want)
			}
		})
	}
}
