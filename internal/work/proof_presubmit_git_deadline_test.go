package work

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"compound/internal/authority/gitrepository"
)

type deadlineObservedGroup struct {
	presubmitOwnedProcessGroup
	once   sync.Once
	killed chan struct{}
}

func (g *deadlineObservedGroup) kill() error {
	err := g.presubmitOwnedProcessGroup.kill()
	g.once.Do(func() { close(g.killed) })
	return err
}

func TestPresubmitGitLaunchDeadline(t *testing.T) {
	for _, mode := range []string{"slow-preparation-fast-git", "slow-git", "parent-deadline-during-preparation", "parent-cancel-during-preparation", "slow-running-checkpoint-fast-git", "slow-running-checkpoint", "group-open-error-keeps-committed-launch", "start-error-joins-capture"} {
		t.Run(mode, func(t *testing.T) {
			s, request := nativePresubmitFixture(t)
			request.Budget = time.Minute
			complete := errors.New("source-read diagnostic completed")
			cancelled := errors.New("parent cancelled during native preparation")
			groupFault := errors.New("synthetic group-open failure after successful start")
			originalOpen := presubmitOpenGroup
			defer func() { presubmitOpenGroup = originalOpen }()
			killed := make(chan struct{})
			var capturedGroup presubmitOwnedProcessGroup
			var owner *presubmitRuntime
			var commands []*exec.Cmd
			var gotSource bool
			var killedDuringCheckpoint bool
			pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
			if mode == "slow-running-checkpoint" {
				presubmitOpenGroup = func(pid int) (presubmitOwnedProcessGroup, error) {
					group, err := originalOpen(pid)
					if err != nil {
						return nil, err
					}
					return &deadlineObservedGroup{presubmitOwnedProcessGroup: group, killed: killed}, nil
				}
			}
			if mode == "group-open-error-keeps-committed-launch" {
				presubmitOpenGroup = func(pid int) (presubmitOwnedProcessGroup, error) {
					var err error
					capturedGroup, err = originalOpen(pid)
					if err != nil {
						return nil, err
					}
					return nil, groupFault
				}
			}
			// This fault deliberately retains uncertain native custody. Clean only the
			// exact fixture child/handle after checking its durable launch classification.
			defer func() {
				if capturedGroup != nil {
					_ = capturedGroup.kill()
					if len(commands) > 0 && commands[0].Process != nil && commands[0].ProcessState == nil {
						_ = commands[0].Wait()
					}
					_ = capturedGroup.close()
				}
				if owner != nil && owner.out.ContainmentUncertain && owner.barrier != nil {
					_ = owner.barrier.Close()
					owner.barrier = nil
				}
			}()
			result, runErr := s.RunPresubmitProducer(context.Background(), request, func(ctx context.Context, req PresubmitExecutionRequest) (ProofCheckReport, error) {
				owner = ctx.Value(nativePresubmitRuntimeKey{}).(*presubmitRuntime)
				readCtx := ctx
				if mode == "parent-deadline-during-preparation" {
					var cancel context.CancelFunc
					readCtx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
					defer cancel()
				}
				if mode == "parent-cancel-during-preparation" {
					var cancel context.CancelCauseFunc
					readCtx, cancel = context.WithCancelCause(ctx)
					defer cancel(nil)
					timer := time.AfterFunc(100*time.Millisecond, func() { cancel(cancelled) })
					defer timer.Stop()
				}
				record := owner.checkpointRecord
				owner.checkpointRecord = func(out *PresubmitExecutionResult) error {
					checkpoint := out.Checkpoints[len(out.Checkpoints)-1]
					if checkpoint.CommandSequence == 1 && checkpoint.Status == "started" {
						if mode == "slow-preparation-fast-git" {
							time.Sleep(6 * time.Second)
						}
						if strings.HasPrefix(mode, "parent-") {
							time.Sleep(350 * time.Millisecond)
						}
					}
					if checkpoint.CommandSequence == 1 && checkpoint.Status == "running" && mode == "slow-running-checkpoint-fast-git" {
						time.Sleep(6 * time.Second)
					}
					if checkpoint.CommandSequence == 1 && checkpoint.Status == "running" && mode == "slow-running-checkpoint" {
						// A durable write may outlast cancellation. The owned timer must kill
						// the real group while this checkpoint is still blocked, not after it.
						select {
						case <-killed:
							killedDuringCheckpoint = true
						case <-time.After(8 * time.Second):
						}
					}
					return record(out)
				}
				readCtx = gitrepository.ContextWithCommandFactory(readCtx, func(commandCtx context.Context, name string, args ...string) *exec.Cmd {
					var cmd *exec.Cmd
					if len(commands) == 0 && (mode == "slow-git" || mode == "slow-running-checkpoint") {
						cmd = exec.CommandContext(commandCtx, "sh", "-c", "sleep 30 & echo $! > \"$1\"; wait", "sh", pidFile)
					} else if len(commands) == 0 && mode == "start-error-joins-capture" {
						cmd = exec.CommandContext(commandCtx, filepath.Join(t.TempDir(), "missing-native-child"))
					} else if len(commands) == 0 && mode == "group-open-error-keeps-committed-launch" {
						cmd = exec.CommandContext(commandCtx, "sh", "-c", "exec sleep 30")
					} else {
						cmd = exec.CommandContext(commandCtx, name, args...)
					}
					commands = append(commands, cmd)
					return cmd
				})
				if mode == "start-error-joins-capture" {
					cmd := gitrepository.CommandContext(readCtx, "git")
					return ProofCheckReport{}, gitrepository.RunCommand(readCtx, cmd)
				}
				source, err := gitrepository.CaptureCleanSnapshotContext(readCtx, req.Repo, req.BaseRef)
				if err != nil {
					return ProofCheckReport{}, err
				}
				gotSource = source.Clean && source.BaseSHA == req.BaseRef
				return ProofCheckReport{}, complete
			})
			if mode == "slow-preparation-fast-git" || mode == "slow-running-checkpoint-fast-git" {
				if !errors.Is(runErr, complete) || !gotSource {
					t.Fatalf("native bookkeeping consumed Git execution allowance: source=%v err=%v", gotSource, runErr)
				}
				for _, cmd := range commands {
					if cmd.ProcessState == nil || !cmd.ProcessState.Success() {
						t.Fatal("real Git command did not complete successfully")
					}
				}
			} else if strings.HasPrefix(mode, "parent-") {
				expected := error(context.DeadlineExceeded)
				if mode == "parent-cancel-during-preparation" {
					expected = cancelled
				}
				if !errors.Is(runErr, expected) {
					t.Fatalf("lost parent boundary: %v, want %v", runErr, expected)
				}
				for _, cmd := range commands {
					if cmd.Process != nil {
						t.Fatal("launched Git after parent stopped during preparation")
					}
				}
				for _, c := range result.Checkpoints {
					if c.GroupStarted {
						t.Fatal("unstarted command was recorded as started")
					}
				}
			} else if mode == "start-error-joins-capture" {
				if !errors.Is(runErr, os.ErrNotExist) || len(commands) != 1 || commands[0].Process != nil {
					t.Fatalf("failed start was not joined as unstarted: commands=%d err=%v", len(commands), runErr)
				}
				attempt, err := s.runAttempts().Get(result.RunAttemptID)
				if err != nil || attempt.Launch == nil || attempt.Launch.StartErrorAt == nil || attempt.Launch.CommittedAt != nil {
					t.Fatalf("failed start misclassified: %+v %v", attempt.Launch, err)
				}
			} else if mode == "group-open-error-keeps-committed-launch" {
				if !errors.Is(runErr, groupFault) || !result.ContainmentUncertain || result.Quiescent || result.Released {
					t.Fatalf("group-open failure fabricated cleanup: %+v %v", result, runErr)
				}
				attempt, err := s.runAttempts().Get(result.RunAttemptID)
				if err != nil || attempt.Launch == nil || attempt.Launch.CommittedAt == nil || attempt.Launch.StartErrorAt != nil {
					t.Fatalf("successful start misclassified: %+v %v", attempt.Launch, err)
				}
				return
			} else {
				if !errors.Is(runErr, context.DeadlineExceeded) {
					t.Fatalf("slow Git lost five-second execution deadline: %v", runErr)
				}
				if len(commands) != 1 || commands[0].Process == nil || commands[0].ProcessState == nil || commands[0].ProcessState.Success() {
					t.Fatal("slow command was not started, killed and joined")
				}
				if mode == "slow-running-checkpoint" && !killedDuringCheckpoint {
					t.Fatal("execution deadline waited behind post-start durable checkpoint")
				}
				data, err := os.ReadFile(pidFile)
				if err != nil {
					t.Fatalf("slow fixture did not start its grandchild: %v", err)
				}
				pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
				if err != nil {
					t.Fatal(err)
				}
				if err = syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
					t.Fatalf("owned grandchild %d survived joined timeout: %v", pid, err)
				}
			}
			if !result.Quiescent || !result.Released || result.ContainmentUncertain {
				t.Fatalf("native cleanup not proven: %+v", result)
			}
			attempt, err := s.runAttempts().Get(result.RunAttemptID)
			if err != nil || attempt.Status != "failed" {
				t.Fatalf("diagnostic failed to terminalize attempt: %+v %v", attempt, err)
			}
		})
	}
}

func TestPresubmitGitLargeOutputDuringRunningCheckpoint(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	request.Budget = time.Minute
	complete := errors.New("large-output read diagnostic completed")
	expected := bytes.Repeat([]byte("native-git-output\n"), 65536)
	var child *exec.Cmd
	var captured []byte
	var rawPath string
	result, runErr := s.RunPresubmitProducer(context.Background(), request, func(ctx context.Context, req PresubmitExecutionRequest) (ProofCheckReport, error) {
		owner := ctx.Value(nativePresubmitRuntimeKey{}).(*presubmitRuntime)
		record := owner.checkpointRecord
		owner.checkpointRecord = func(out *PresubmitExecutionResult) error {
			checkpoint := out.Checkpoints[len(out.Checkpoints)-1]
			if checkpoint.CommandSequence == 1 && checkpoint.Status == "running" {
				time.Sleep(6 * time.Second)
			}
			return record(out)
		}
		ctx = gitrepository.ContextWithCommandFactory(ctx, func(commandCtx context.Context, _ string, _ ...string) *exec.Cmd {
			// This real child produces more than a pipe can hold and exits. It
			// probes capture backpressure without fabricating source identity.
			child = exec.CommandContext(commandCtx, os.Args[0], "-test.run=^TestPresubmitGitLargeOutputHelper$", "--", "native-large-output")
			return child
		})
		var err error
		captured, err = gitrepository.CommittedFileBody(ctx, req.Repo, req.BaseRef, "large-output.txt")
		rawPath = owner.outputPaths[0]
		if err != nil {
			return ProofCheckReport{}, err
		}
		return ProofCheckReport{}, complete
	})
	raw, rawErr := os.ReadFile(rawPath)
	if !errors.Is(runErr, complete) || !bytes.Equal(captured, expected) || rawErr != nil || !bytes.Equal(raw, expected) {
		t.Fatalf("bookkeeping blocked owned capture: captured=%d raw=%d want=%d rawErr=%v runErr=%v", len(captured), len(raw), len(expected), rawErr, runErr)
	}
	if child == nil || child.ProcessState == nil || !child.ProcessState.Success() {
		t.Fatal("large-output child did not exit successfully")
	}
	if !result.Quiescent || !result.Released || result.ContainmentUncertain {
		t.Fatalf("large-output cleanup not proven: %+v", result)
	}
}

func TestPresubmitGitLargeOutputHelper(t *testing.T) {
	if len(os.Args) == 0 || os.Args[len(os.Args)-1] != "native-large-output" {
		return
	}
	body := bytes.Repeat([]byte("native-git-output\n"), 65536)
	if n, err := os.Stdout.Write(body); err != nil || n != len(body) {
		os.Exit(2)
	}
	os.Exit(0)
}
