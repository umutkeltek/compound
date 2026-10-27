package gitrepository

import (
	"bytes"
	"context"
	"os/exec"
)

type commandRunnerKey struct{}

// ContextWithCommandRunner binds a native execution owner to existing source
// reads. It changes no command selection or behavior for ordinary callers.
func ContextWithCommandRunner(ctx context.Context, run func(context.Context, *exec.Cmd) error) context.Context {
	return context.WithValue(ctx, commandRunnerKey{}, run)
}

func RunCommand(ctx context.Context, cmd *exec.Cmd) error {
	if run, ok := ctx.Value(commandRunnerKey{}).(func(context.Context, *exec.Cmd) error); ok && run != nil {
		return run(ctx, cmd)
	}
	return cmd.Run()
}

// CommandOutput preserves the standard exec path outside native execution.
func CommandOutput(ctx context.Context, cmd *exec.Cmd, combined bool) ([]byte, error) {
	if _, ok := ctx.Value(commandRunnerKey{}).(func(context.Context, *exec.Cmd) error); !ok {
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
