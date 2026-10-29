package gitrepository

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

// gitReadCommandTimeout bounds one Git subprocess. Native launch-aware owners
// keep preparation under the parent operation deadline and start this allowance
// at their checked launch. Ordinary and generic runners retain their context bound.
const gitReadCommandTimeout = 5 * time.Second

type commandRunnerKey struct{}
type gitReadTimeoutKey struct{}

// ContextWithCommandRunner binds an owner without changing its deadline contract.
func ContextWithCommandRunner(ctx context.Context, run func(context.Context, *exec.Cmd) error) context.Context {
	return context.WithValue(ctx, commandRunnerKey{}, run)
}

// ContextWithLaunchTimeoutCommandRunner opts an execution owner into enforcing
// the supplied subprocess allowance at launch, independently of its bookkeeping.
// A zero allowance leaves the original operation context in force.
func ContextWithLaunchTimeoutCommandRunner(ctx context.Context, run func(context.Context, *exec.Cmd, time.Duration) error) context.Context {
	return context.WithValue(ctx, commandRunnerKey{}, run)
}

func gitReadContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if run, ok := ctx.Value(commandRunnerKey{}).(func(context.Context, *exec.Cmd, time.Duration) error); ok && run != nil {
		return context.WithValue(ctx, gitReadTimeoutKey{}, gitReadCommandTimeout), func() {}
	}
	return context.WithTimeout(ctx, gitReadCommandTimeout)
}

func RunCommand(ctx context.Context, cmd *exec.Cmd) error {
	switch run := ctx.Value(commandRunnerKey{}).(type) {
	case func(context.Context, *exec.Cmd, time.Duration) error:
		if run != nil {
			timeout, _ := ctx.Value(gitReadTimeoutKey{}).(time.Duration)
			return run(ctx, cmd, timeout)
		}
	case func(context.Context, *exec.Cmd) error:
		if run != nil {
			return run(ctx, cmd)
		}
	}
	return cmd.Run()
}

// CommandOutput preserves buffers and the direct exec path outside owned runs.
func CommandOutput(ctx context.Context, cmd *exec.Cmd, combined bool) ([]byte, error) {
	switch ctx.Value(commandRunnerKey{}).(type) {
	case func(context.Context, *exec.Cmd) error, func(context.Context, *exec.Cmd, time.Duration) error:
	default:
		if combined {
			return cmd.CombinedOutput()
		}
		return cmd.Output()
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	if combined {
		cmd.Stderr = &output
	}
	err := RunCommand(ctx, cmd)
	return output.Bytes(), err
}
