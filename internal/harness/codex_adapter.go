package harness

import (
	"context"
	"os"
	"os/exec"
	"time"

	"compound/internal/runattempt"
)

// CodexAdapter drives the Codex CLI as a subprocess and wraps the
// execution in a RunAttempt. Mirrors ClaudeAdapter: thin shim over
// runWithSubprocess, harness-specific argv, harness name "codex" on
// the RunAttempt.
//
// Today Codex's non-interactive invocation is:
//
//	codex exec --prompt <prompt>
//
// Flags are best-effort matches for the public Codex CLI; they may
// change as the CLI evolves. A future slice swaps the argv for the
// real, versioned invocation when a Codex adapter test is wired against
// the real binary.
//
// The adapter does NOT implement Codex's hook protocol. It is a
// subprocess wrapper, not a hook consumer. The HarnessRegistry continues
// to advertise Codex's capabilities from its manifest.
type CodexAdapter struct {
	store             *runattempt.Store
	heartbeatInterval time.Duration
}

// NewCodexAdapter returns the production adapter. Heartbeats every 5s.
func NewCodexAdapter(store *runattempt.Store) *CodexAdapter {
	return &CodexAdapter{store: store, heartbeatInterval: defaultHeartbeatInterval}
}

// NewCodexAdapterWithInterval returns an adapter with a custom heartbeat
// cadence. Test-only.
func NewCodexAdapterWithInterval(store *runattempt.Store, interval time.Duration) *CodexAdapter {
	if interval <= 0 {
		interval = defaultHeartbeatInterval
	}
	return &CodexAdapter{store: store, heartbeatInterval: interval}
}

// Name returns the manifest adapter id for Codex. Future Registry
// integration (C.3) keys on this.
func (a *CodexAdapter) Name() string { return "codex" }

// Run drives the codex subprocess end-to-end. Mirrors ClaudeAdapter.Run.
func (a *CodexAdapter) Run(ctx context.Context, in RunInput) (RunOutput, error) {
	in.Harness = a.Name()
	in.Command = a.Name()
	return runWithSubprocess(ctx, a.store, a.Name(), in, a.buildCmd, a.heartbeatInterval)
}

// buildCmd assembles `codex exec --prompt <prompt>`. Args[0] is used as
// the prompt if Prompt is empty (caller convenience); the explicit
// Prompt field wins.
func (a *CodexAdapter) buildCmd(ctx context.Context, in RunInput) *exec.Cmd {
	// Timeout is owned by runWithSubprocess; see OSEntrypointsAdapter.buildCmd.
	prompt := in.Prompt
	if prompt == "" && len(in.Args) > 0 {
		prompt = in.Args[0]
	}
	args := []string{"exec", "--prompt"}
	if prompt != "" {
		args = append(args, prompt)
	}
	cmd := exec.CommandContext(ctx, "codex", args...)
	cmd.Dir = in.Workspace
	cmd.Env = append(os.Environ(), in.Env...)
	return cmd
}
