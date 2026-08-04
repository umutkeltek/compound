package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"compound/internal/routeevidence"
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
	name              string
	command           string
	tupleBacked       bool
}

// NewClaudeAdapter returns the production adapter. Heartbeats every 5s.
func NewClaudeAdapter(store *runattempt.Store) *ClaudeAdapter {
	return newClaudeAdapter(store, "claude", "claude", defaultHeartbeatInterval, false)
}

func newClaudeLaneAdapter(store *runattempt.Store, name string, interval time.Duration) *ClaudeAdapter {
	return newClaudeAdapter(store, name, "", interval, true)
}

func newClaudeAdapter(store *runattempt.Store, name, command string, interval time.Duration, tupleBacked bool) *ClaudeAdapter {
	if interval <= 0 {
		interval = defaultHeartbeatInterval
	}
	return &ClaudeAdapter{store: store, heartbeatInterval: interval, name: name, command: command, tupleBacked: tupleBacked}
}

// NewClaudeAdapterWithInterval returns an adapter with a custom heartbeat
// cadence. Test-only.
func NewClaudeAdapterWithInterval(store *runattempt.Store, interval time.Duration) *ClaudeAdapter {
	return newClaudeAdapter(store, "claude", "claude", interval, false)
}

// Name returns the manifest adapter id for Claude. Future Registry
// integration (C.3) keys on this.
func (a *ClaudeAdapter) Name() string { return a.name }

// GovernedPermissionPosture marks every direct and tuple-backed Claude-family
// adapter as carrying the governed PermissionMode boundary.
func (a *ClaudeAdapter) GovernedPermissionPosture() {}

// RevalidateGovernedRun reloads the Claude-family lane tuple, executable, and
// declared capacity without recording refusal evidence. The governed start
// guard calls it while holding the autonomy-admission lock, so a tuple import
// cannot cross the comparison-to-cmd.Start boundary. Suppressing refusal
// evidence is required here because evidence publication is itself a governed
// mutation and would recursively acquire that lock.
func (a *ClaudeAdapter) RevalidateGovernedRun(_ context.Context, in RunInput) (RunInput, error) {
	if err := validateClaudePermissionMode(in.PermissionMode); err != nil {
		return RunInput{}, err
	}
	return a.resolveRunInput(in, true)
}

// Run drives the claude subprocess end-to-end. The RunInput.Command is
// overridden to "claude" so the caller doesn't have to know the binary
// path; Args[0] (or Prompt) is appended as the prompt text.
func (a *ClaudeAdapter) Run(ctx context.Context, in RunInput) (RunOutput, error) {
	if !in.preflighted {
		resolved, err := a.Preflight(ctx, in)
		if err != nil {
			return RunOutput{}, err
		}
		in = resolved
	}
	// Override the harness name + command so the caller doesn't need to
	// know the binary path. The harnessName arg of runWithSubprocess
	// controls RunAttempt.Harness and the error-message prefix; the
	// RunInput.Command is what gets recorded on the RunAttempt as the
	// actual command (we set it to "claude" so the audit says "command:
	// claude" not "command: <whatever the caller passed>").
	in.Harness = a.Name()
	if in.Command == "" {
		in.Command = a.command
	}
	return runWithSubprocess(ctx, a.store, a.Name(), in, a.buildCmd, a.heartbeatInterval)
}

// Preflight resolves the lane without minting a RunAttempt or starting a
// subprocess. Governed callers invoke this before advancing work to running;
// Run invokes it again so the manual harness command has the same fail-closed
// boundary.
func (a *ClaudeAdapter) Preflight(_ context.Context, in RunInput) (RunInput, error) {
	if err := validateClaudePermissionMode(in.PermissionMode); err != nil {
		return RunInput{}, err
	}
	resolved, err := a.resolveRunInput(in, false)
	if err != nil {
		return RunInput{}, err
	}
	resolved.preflighted = true
	return resolved, nil
}

func (a *ClaudeAdapter) resolveRunInput(in RunInput, suppressRefusalEvidence bool) (RunInput, error) {
	if !a.tupleBacked {
		in.Command = a.command
		return in, nil
	}
	manifest, _, err := routeevidence.LoadModelCapabilitiesFromHome(a.store.Home())
	if err != nil {
		return RunInput{}, fmt.Errorf("%s adapter: load Compound model-router authority: %w", a.Name(), err)
	}
	modelID, tuple, err := routeevidence.ResolveModelCapabilityTuple(manifest, a.Name())
	if err != nil {
		return RunInput{}, fmt.Errorf("%s adapter: %w", a.Name(), err)
	}
	command, err := routeevidence.ResolveModelCapabilityExecutable(tuple.Aliases)
	if err != nil {
		return RunInput{}, fmt.Errorf("%s adapter: %w", a.Name(), err)
	}
	if tuple.CapacitySource != nil {
		decision, err := routeevidence.GuardDeclaredLaneCapacity(
			a.store.Home(),
			tuple.ProviderRoute,
			"build-implement",
			*tuple.CapacitySource,
			in.Now,
			60,
			false,
			nil,
			suppressRefusalEvidence,
		)
		if err != nil {
			return RunInput{}, fmt.Errorf("%s adapter: capacity preflight: %w", a.Name(), err)
		}
		if decision.Capacity.State != routeevidence.CapacityAlive {
			message := strings.TrimSpace(decision.Message)
			if message == "" {
				message = decision.Capacity.DataRef
			}
			return RunInput{}, fmt.Errorf("%s adapter: capacity %s: %s", a.Name(), decision.Capacity.State, message)
		}
	}
	in.Command = command
	in.RequestedModelID = modelID
	in.RequestedProviderRoute = tuple.ProviderRoute
	if strings.TrimSpace(in.LaneID) == "" {
		in.LaneID = a.Name()
	}
	return in, nil
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
// a direct caller passes --permission-mode explicitly: "acceptEdits"
// (auto-accept file edits, still prompts on bash) or "bypass"
// (--dangerously-skip-permissions, no prompts at all). Governed work execution
// may resolve an omitted mode to bypass before calling the adapter. Pinned by
// TestClaudeAdapterHappyPath (the direct default is plan, not bypass) and
// TestClaudeAdapterPermissionMode.
const claudeDefaultPermissionMode = "plan"

// claudePermissionArgs translates a PermissionMode into the argv fragment
// the Claude CLI understands. "" -> the safe plan default; "bypass" ->
// --dangerously-skip-permissions; "default" / "plan" / "acceptEdits" pass
// through verbatim. Callers
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
	cmd := exec.CommandContext(ctx, in.Command, args...)
	cmd.Dir = in.Workspace
	cmd.Env = append(os.Environ(), in.Env...)
	return cmd
}
