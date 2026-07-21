package harness

import (
	"context"
	"fmt"
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
	// Validate the permission mode BEFORE runWithSubprocess mints the
	// RunAttempt (mint-before-side-effect is the S1+S2 contract, but an
	// invalid posture must not even reach that point). A typo must never
	// silently downgrade to a safe-looking default and still run -- it
	// must fail closed with zero ratt_ files. claudePermissionArgs can
	// then assume mode is one of the recognized values.
	if err := validateClaudePermissionMode(in.PermissionMode); err != nil {
		return RunOutput{}, err
	}
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

// validateClaudePermissionMode rejects any RunInput.PermissionMode that is
// not one of the modes the Claude CLI actually understands. Called at the
// top of Run, before runWithSubprocess mints a RunAttempt -- an unknown
// mode (e.g. a typo) must fail closed with zero ratt_ files, not silently
// fall back to a safe-looking default and still run unguarded config.
func validateClaudePermissionMode(mode string) error {
	switch mode {
	case "", "default", "plan", "acceptEdits", "bypass":
		return nil
	default:
		return fmt.Errorf("claude adapter: unknown --permission-mode %q; allowed: \"\" (default), \"default\", \"plan\", \"acceptEdits\", \"bypass\"", mode)
	}
}

// claudeDefaultPermissionMode is the posture used when RunInput.PermissionMode
// is empty. It is "plan" on purpose: this adapter drives Claude as a
// NON-INTERACTIVE subprocess (`claude --print`), where a permission prompt
// cannot be answered and would hang the run until the kill timer fires.
// "plan" is the only governed mode that does not hang a headless run --
// planning mode researches and proposes but takes no state-changing action,
// so it never prompts. To let Claude actually edit/execute autonomously,
// pass --permission-mode explicitly: "acceptEdits" (auto-accept file edits,
// still prompts on bash) or "bypass" (--dangerously-skip-permissions, no
// prompts at all). Pinned by TestClaudeAdapterHappyPath (default is plan,
// not bypass) and TestClaudeAdapterPermissionMode.
const claudeDefaultPermissionMode = "plan"

// claudePermissionArgs translates a PermissionMode into the argv fragment
// the Claude CLI understands. "" -> the safe plan default; "bypass" ->
// --dangerously-skip-permissions (explicit opt-in for fully-autonomous
// runs); "default" / "plan" / "acceptEdits" pass through verbatim. Callers
// go through Run(), which calls validateClaudePermissionMode first, so
// mode is always one of the recognized values here; the default case is a
// defensive fallback for any future direct caller, never for a typo (a
// typo is now rejected before this function runs).
func claudePermissionArgs(mode string) []string {
	if mode == "" {
		mode = claudeDefaultPermissionMode
	}
	switch mode {
	case "bypass":
		return []string{"--dangerously-skip-permissions"}
	case "default", "plan", "acceptEdits":
		return []string{"--permission-mode", mode}
	default:
		return []string{"--permission-mode", claudeDefaultPermissionMode}
	}
}

// buildCmd assembles the claude argv from the RunInput. The permission
// posture comes from RunInput.PermissionMode (default plan -- see
// claudeDefaultPermissionMode). Args[0] is used as the prompt if Prompt is
// empty (caller convenience); the explicit Prompt field wins.
func (a *ClaudeAdapter) buildCmd(ctx context.Context, in RunInput) *exec.Cmd {
	// Timeout is owned by runWithSubprocess; see OSEntrypointsAdapter.buildCmd.
	prompt := in.Prompt
	if prompt == "" && len(in.Args) > 0 {
		prompt = in.Args[0]
	}
	args := append([]string{"--print"}, claudePermissionArgs(in.PermissionMode)...)
	if prompt != "" {
		args = append(args, prompt)
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = in.Workspace
	cmd.Env = append(os.Environ(), in.Env...)
	return cmd
}
