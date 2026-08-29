package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/routeevidence"
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

// Preflight resolves a selected Compound LaneTuple to its owned executable
// before a governed caller claims or advances work. Legacy direct callers
// with no selected tuple retain the raw codex command.
func (a *CodexAdapter) Preflight(_ context.Context, in RunInput) (RunInput, error) {
	requestedModel := strings.TrimSpace(in.RequestedModelID)
	requestedRoute := strings.TrimSpace(in.RequestedProviderRoute)
	command := a.Name()
	if requestedModel != "" || requestedRoute != "" {
		if requestedModel == "" || requestedRoute == "" {
			return RunInput{}, fmt.Errorf("codex adapter: selected tuple requires both model and provider route")
		}
		manifest, _, currentRevision, err := routeevidence.LoadModelCapabilitiesSnapshot(a.store.Home())
		if err != nil {
			return RunInput{}, fmt.Errorf("codex adapter: load Compound model-router authority: %w", err)
		}
		if expectedRevision := strings.TrimSpace(in.ExpectedModelRouterRevision); expectedRevision != "" && currentRevision != expectedRevision {
			return RunInput{}, fmt.Errorf("codex adapter: model-router revision drifted: planned=%q current=%q", expectedRevision, currentRevision)
		}
		modelID, tuple, err := routeevidence.ResolveModelCapabilityTuple(manifest, requestedModel)
		if err != nil {
			return RunInput{}, fmt.Errorf("codex adapter: resolve selected tuple: %w", err)
		}
		if modelID != requestedModel || canonicalHarnessName(tuple.Harness) != a.Name() || strings.TrimSpace(tuple.ProviderRoute) != requestedRoute {
			return RunInput{}, fmt.Errorf(
				"codex adapter: selected route drifted: planned model=%q route=%q resolved model=%q route=%q harness=%q",
				requestedModel, requestedRoute, modelID, tuple.ProviderRoute, tuple.Harness,
			)
		}
		command, err = routeevidence.ResolveModelCapabilityExecutable(tuple.Aliases)
		if err != nil {
			return RunInput{}, fmt.Errorf("codex adapter: selected tuple %q: %w", modelID, err)
		}
	} else if _, err := exec.LookPath(command); err != nil {
		return RunInput{}, fmt.Errorf("codex adapter: resolve codex executable: %w", err)
	}
	in.Command = command
	in.preflighted = true
	return in, nil
}

// Run drives the codex subprocess end-to-end. Mirrors ClaudeAdapter.Run.
func (a *CodexAdapter) Run(ctx context.Context, in RunInput) (RunOutput, error) {
	if !in.preflighted {
		resolved, err := a.Preflight(ctx, in)
		if err != nil {
			return RunOutput{}, err
		}
		in = resolved
	}
	in.Harness = a.Name()
	return runWithSubprocess(ctx, a.store, a.Name(), in, a.buildCmd, a.heartbeatInterval)
}

// buildCmd assembles `codex exec [PROMPT]` (positional — see the type
// doc for the --prompt drift this replaces). Args[0] is used as the
// prompt if Prompt is empty (caller convenience); the explicit Prompt
// field wins.
//
// CODEX_HOME isolation: every compound-dispatched codex invocation gets a
// fresh CODEX_HOME under os.TempDir so two concurrent runs (or an exec
// running while an interactive `codex` TUI is active) cannot share state
// through ~/.codex. This is hardening, not a bug fix — the symptom
// ("scout launch leaks release-tooling content into an unrelated
// transcript") was not reproducible against codex-cli 0.145.0 in
// concurrent-exec probes, but the asymmetry between this adapter
// (no CODEX_HOME) and _launch_claude_lane (per-lane CLAUDE_CONFIG_DIR)
// was the latent risk. Caller-supplied in.Env still wins: we set the
// env line BEFORE in.Env so a caller override is honored.
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
	command := strings.TrimSpace(in.Command)
	if command == "" {
		command = a.Name()
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = in.Workspace
	codexHome, err := os.MkdirTemp("", "codex-")
	if err != nil {
		// Fall back to a path we are confident is writable; the OS temp
		// rotation will eventually reclaim it. Worst case: isolation is
		// weaker but the run still completes.
		codexHome = filepath.Join(os.TempDir(), "codex-fallback-"+in.WorkID)
		_ = os.MkdirAll(codexHome, 0o700)
	}
	cmd.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	cmd.Env = append(cmd.Env, in.Env...)
	return cmd
}
