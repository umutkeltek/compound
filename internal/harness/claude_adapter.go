package harness

import (
	"context"
	"os"
	"os/exec"
	"time"

	"compound/internal/runattempt"
)

// ClaudeAdapter drives the Claude Code CLI as a subprocess and wraps the
// execution in a RunAttempt. It is a thin shim over the shared
// runWithSubprocess runner (subprocess_runner.go) -- the only Claude
// specifics are the argv assembly and the harness name on the
// RunAttempt.
//
// Today Claude's non-interactive invocation is:
//
//	claude --print --dangerously-skip-permissions <prompt>
//
// The flags are best-effort matches for the public Claude Code CLI; they
// may change as the CLI evolves. A future slice swaps the argv for the
// real, versioned invocation when a Claude adapter test is wired against
// the real binary.
//
// The adapter does NOT implement Claude's hook protocol (PreToolUseBlock,
// PostToolUseEvent, etc.). It is a subprocess wrapper, not a hook consumer.
// The HarnessRegistry continues to advertise Claude's capabilities from
// its manifest; the adapter is the executor.
type ClaudeAdapter struct {
	store             *runattempt.Store
	heartbeatInterval time.Duration
}

// NewClaudeAdapter returns the production adapter. Heartbeats every 5s.
func NewClaudeAdapter(store *runattempt.Store) *ClaudeAdapter {
	return &ClaudeAdapter{store: store, heartbeatInterval: defaultHeartbeatInterval}
}

// NewClaudeAdapterWithInterval returns an adapter with a custom heartbeat
// cadence. Test-only.
func NewClaudeAdapterWithInterval(store *runattempt.Store, interval time.Duration) *ClaudeAdapter {
	if interval <= 0 {
		interval = defaultHeartbeatInterval
	}
	return &ClaudeAdapter{store: store, heartbeatInterval: interval}
}

// Name returns the manifest adapter id for Claude. Future Registry
// integration (C.3) keys on this.
func (a *ClaudeAdapter) Name() string { return "claude" }

// Run drives the claude subprocess end-to-end. The RunInput.Command is
// overridden to "claude" so the caller doesn't have to know the binary
// path; Args[0] (or Prompt) is appended as the prompt text.
func (a *ClaudeAdapter) Run(ctx context.Context, in RunInput) (RunOutput, error) {
	// Override the harness name + command so the caller doesn't need to
	// know the binary path. The harnessName arg of runWithSubprocess
	// controls RunAttempt.Harness and the error-message prefix; the
	// RunInput.Command is what gets recorded on the RunAttempt as the
	// actual command (we set it to "claude" so the audit says "command:
	// claude" not "command: <whatever the caller passed>").
	in.Harness = a.Name()
	in.Command = a.Name()
	return runWithSubprocess(ctx, a.store, a.Name(), in, a.buildCmd, a.heartbeatInterval)
}

// buildCmd assembles `claude --print --dangerously-skip-permissions <prompt>`.
// Args[0] is used as the prompt if Prompt is empty (caller convenience);
// the explicit Prompt field wins.
func (a *ClaudeAdapter) buildCmd(ctx context.Context, in RunInput) *exec.Cmd {
	if in.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, in.Timeout)
		_ = cancel
	}
	prompt := in.Prompt
	if prompt == "" && len(in.Args) > 0 {
		prompt = in.Args[0]
	}
	args := []string{"--print", "--dangerously-skip-permissions"}
	if prompt != "" {
		args = append(args, prompt)
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = in.Workspace
	cmd.Env = append(os.Environ(), in.Env...)
	return cmd
}
