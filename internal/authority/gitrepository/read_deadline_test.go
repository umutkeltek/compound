package gitrepository

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

const expectedGitReadBudget = 5 * time.Second

// Model the durable controller's admission/checkpoint cost while running real
// Git. No source identity or status output is fabricated by this runner.
func delayedGitReadContext(t *testing.T, parent context.Context, delay time.Duration) (context.Context, *int) {
	t.Helper()
	calls := 0
	ctx := ContextWithCommandRunner(parent, func(ctx context.Context, cmd *exec.Cmd) error {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > expectedGitReadBudget {
			t.Errorf("Git read has no independent five-second bound: deadline=%v present=%v", deadline, ok)
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-timer.C:
		}
		return cmd.Run()
	})
	return ctx, &calls
}

func TestSnapshotReadBudgetIsPerCommand(t *testing.T) {
	for _, operation := range []string{"capture", "revalidate"} {
		t.Run(operation, func(t *testing.T) {
			repo := initSnapshotRepo(t)
			sealed, err := CaptureCleanSnapshot(repo, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			parent, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			// Seven real reads plus controller overhead exceed the former shared five
			// seconds, although each individual command remains comfortably bounded.
			ctx, calls := delayedGitReadContext(t, parent, 900*time.Millisecond)
			if operation == "capture" {
				var got Snapshot
				got, err = CaptureCleanSnapshotContext(ctx, repo, "HEAD")
				if err == nil && got != sealed {
					t.Fatalf("source seal changed: got %+v want %+v", got, sealed)
				}
			} else {
				err = RevalidateSnapshotContext(ctx, repo, sealed)
			}
			if err != nil {
				t.Fatalf("%s accumulated unrelated command overhead: %v", operation, err)
			}
			if *calls != 7 {
				t.Fatalf("%s ran %d commands, want all seven fresh reads", operation, *calls)
			}
		})
	}
}

func TestIdentityReadBudgetIsPerCommand(t *testing.T) {
	repo := initSnapshotRepo(t)
	parent, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx, calls := delayedGitReadContext(t, parent, 1800*time.Millisecond)
	got, err := ReadIdentityContext(ctx, repo, "HEAD")
	if err != nil {
		t.Fatalf("identity accumulated unrelated command overhead: %v", err)
	}
	if len(got.Fingerprint) != 64 || *calls != 3 {
		t.Fatalf("identity=%+v calls=%d", got, *calls)
	}
}

func TestCommittedContentReadCommandsHaveDeadline(t *testing.T) {
	repo := initSnapshotRepo(t)
	head := snapshotGitOutput(t, repo, "rev-parse", "HEAD")
	operations := map[string]func(context.Context) error{
		"paths":      func(ctx context.Context) error { _, err := CommittedContentPaths(ctx, repo, head, head); return err },
		"ancestor":   func(ctx context.Context) error { _, err := CommittedAncestor(ctx, repo, head, head); return err },
		"body":       func(ctx context.Context) error { _, err := CommittedFileBody(ctx, repo, head, "seed.txt"); return err },
		"diff-check": func(ctx context.Context) error { return CommittedDiffCheck(ctx, repo, head, head) },
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			parent, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			ctx, calls := delayedGitReadContext(t, parent, 0)
			if err := operation(ctx); err != nil {
				t.Fatal(err)
			}
			if *calls == 0 {
				t.Fatal("committed read bypassed the execution owner")
			}
		})
	}
}

func TestGitReadDeadlineStillBoundsSlowProcess(t *testing.T) {
	t.Setenv("COMPOUND_TEST_GIT_READ_SLOW_HELPER", "1")
	repo := initSnapshotRepo(t)
	reads := map[string]func(context.Context) error{
		"identity": func(ctx context.Context) error { _, err := gitOutput(ctx, repo, "status", "--porcelain"); return err },
		"committed-content": func(ctx context.Context) error {
			_, err := committedGitOutput(ctx, repo, "status", "--porcelain")
			return err
		},
	}
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			parent, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var child *exec.Cmd
			ctx := ContextWithCommandFactory(parent, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
				child = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGitReadDeadlineHelperProcess$")
				return child
			})
			ownerCalls := 0
			ctx = ContextWithCommandRunner(ctx, func(_ context.Context, cmd *exec.Cmd) error { ownerCalls++; return cmd.Run() })
			if err := read(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("slow read error=%v, want per-command deadline", err)
			}
			if parent.Err() != nil {
				t.Fatalf("parent deadline replaced the local bound: %v", parent.Err())
			}
			if ownerCalls != 1 || child == nil || child.Process == nil || child.ProcessState == nil || child.ProcessState.Success() {
				t.Fatalf("slow process was not launched and joined through owner: calls=%d child=%v", ownerCalls, child)
			}
		})
	}
}

func TestGitReadDeadlinePreservesParentBoundary(t *testing.T) {
	readers := map[string]func(context.Context, string, string) error{
		"snapshot": func(ctx context.Context, repo string, head string) error {
			_, err := CaptureCleanSnapshotContext(ctx, repo, "HEAD")
			return err
		},
		"identity": func(ctx context.Context, repo string, head string) error {
			_, err := ReadIdentityContext(ctx, repo, "HEAD")
			return err
		},
		"committed-content": func(ctx context.Context, repo string, head string) error {
			return CommittedDiffCheck(ctx, repo, head, head)
		},
	}
	for name, read := range readers {
		t.Run(name, func(t *testing.T) {
			repo := initSnapshotRepo(t)
			head := snapshotGitOutput(t, repo, "rev-parse", "HEAD")
			t.Run("deadline-before-launch", func(t *testing.T) {
				parent, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
				defer cancel()
				launched := 0
				ctx := ContextWithCommandRunner(parent, func(ctx context.Context, cmd *exec.Cmd) error {
					timer := time.NewTimer(time.Second)
					defer timer.Stop()
					select {
					case <-ctx.Done():
						return context.Cause(ctx)
					case <-timer.C:
					}
					if ctx.Err() != nil {
						return context.Cause(ctx)
					}
					err := cmd.Run()
					if cmd.Process != nil {
						launched++
					}
					return err
				})
				if err := read(ctx, repo, head); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("parent deadline error=%v", err)
				}
				if launched != 0 {
					t.Fatalf("launched %d commands after parent expired", launched)
				}
			})
			t.Run("cancel-after-first-command", func(t *testing.T) {
				parent, cancel := context.WithCancelCause(context.Background())
				defer cancel(nil)
				cause := errors.New("source-read parent cancelled")
				launched := 0
				ctx := ContextWithCommandRunner(parent, func(ctx context.Context, cmd *exec.Cmd) error {
					if ctx.Err() != nil {
						return context.Cause(ctx)
					}
					err := cmd.Run()
					if cmd.Process != nil {
						launched++
					}
					cancel(cause)
					return err
				})
				if err := read(ctx, repo, head); !errors.Is(err, cause) {
					t.Fatalf("parent cancellation cause error=%v", err)
				}
				if launched != 1 {
					t.Fatalf("launched %d commands, want only the first", launched)
				}
			})
		})
	}
}

func TestGitReadDeadlineHelperProcess(t *testing.T) {
	if os.Getenv("COMPOUND_TEST_GIT_READ_SLOW_HELPER") != "1" {
		return
	}
	// A real child process exceeds the unchanged local allowance. CommandContext
	// must kill and join it; there are no grandchildren or instant-success stubs.
	time.Sleep(2 * expectedGitReadBudget)
}
