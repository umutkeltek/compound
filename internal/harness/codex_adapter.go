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
//	codex exec [PROMPT]
//
// The prompt is POSITIONAL: current Codex CLI rejects a --prompt flag
// with exit 2 ("unexpected argument '--prompt'"), which silently failed
// every governed dispatch until find_d435b414ef9b pinned it live
// (ratt_20260722T111145Z_f8859f4b). Flags remain best-effort matches
// for the public Codex CLI; a future slice wires an adapter test
// against the real binary so CLI drift fails in CI, not at dispatch.
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

// buildCmd assembles `codex exec [PROMPT]` (positional — see the type
// doc for the --prompt drift this replaces). Args[0] is used as the
// prompt if Prompt is empty (caller convenience); the explicit Prompt
// field wins.
func (a *CodexAdapter) buildCmd(ctx context.Context, in RunInput) *exec.Cmd {
	// Timeout is owned by runWithSubprocess; see OSEntrypointsAdapter.buildCmd.
	prompt := in.Prompt
	if prompt == "" && len(in.Args) > 0 {
		prompt = in.Args[0]
	}
	args := []string{"exec"}
	if prompt != "" {
		args = append(args, prompt)
	}
	cmd := exec.CommandContext(ctx, "codex", args...)
	cmd.Dir = in.Workspace
	cmd.Env = append(os.Environ(), in.Env...)
	return cmd
}
