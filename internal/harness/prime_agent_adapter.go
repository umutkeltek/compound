package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/runattempt"
)

// PrimeAgentAdapter drives the Prime Agent CLI as a subprocess and wraps
// the execution in a RunAttempt. Mirrors the Codex/Claude adapter shape:
// thin shim over the shared runWithSubprocess runner (subprocess_runner.go),
// with Prime-agent-specific argv assembly and the harness name
// "prime_agent" on the RunAttempt.
//
// Prime-agent's headless invocation is documented in `prime-agent --help`:
//
//	prime-agent --mode rpc   <prompt>     # structured IPC over stdin/stdout
//	prime-agent -p --no-session <prompt>  # one-shot print, no session persistence
//
// The adapter:
//   - prefers `--mode rpc` when the prompt is small and the caller wants
//     structured JSON output (the structured-output IPC over stdin/stdout;
//     prime's daemon itself listens on a Unix socket under
//     $TMPDIR/prime-agent-<uid>/daemon.sock — NOT a localhost TCP port —
//     and port 18774 is Switchback's capture tap that prime's models.json
//     points its LLM calls at, never a prime IPC surface);
//   - falls back to `-p --no-session` for one-shot dry-runs and CI;
//   - never uses COMPOUND_HOME or ~/.compound as the workspace root —
//     callers MUST supply RunInput.Workspace (the run delegate resolves
//     --workspace -> git_delivery -> cwd before the adapter sees it).
//
// The adapter does NOT implement Prime-agent's persistent kernel. It
// drives a fresh subprocess for every Run(); the persistent kernel is a
// separate daemon process (Unix socket under
// $TMPDIR/prime-agent-<uid>/daemon.sock). Compound is the orchestrator;
// this adapter is the dispatch driver — the persistent-kernel wiring
// stays where it lives. Adapter scope is "Run the headless Prime-agent
// CLI in a RunAttempt-chrome wrapper."
//
// Capability-floor honesty: the registry row (prime_agent) declares
// NativeSkills + MCP and zero hook flags. Prime-agent has no
// PreToolUse/PostToolUse/SessionStart hook protocol; the registry
// limitation list calls that out so future readers do not infer
// enforcement-via-harness for a harness that genuinely cannot enforce it.
type PrimeAgentAdapter struct {
	store             *runattempt.Store
	heartbeatInterval time.Duration
}

// NewPrimeAgentAdapter returns the production adapter. Heartbeats every 5s.
func NewPrimeAgentAdapter(store *runattempt.Store) *PrimeAgentAdapter {
	return &PrimeAgentAdapter{store: store, heartbeatInterval: defaultHeartbeatInterval}
}

// NewPrimeAgentAdapterWithInterval returns an adapter with a custom
// heartbeat cadence. Test-only.
func NewPrimeAgentAdapterWithInterval(store *runattempt.Store, interval time.Duration) *PrimeAgentAdapter {
	if interval <= 0 {
		interval = defaultHeartbeatInterval
	}
	return &PrimeAgentAdapter{store: store, heartbeatInterval: interval}
}

// Name returns the manifest adapter id for Prime-agent. Keys the
// AdapterRegistry lookup and the RunAttempt.Harness field.
func (a *PrimeAgentAdapter) Name() string { return "prime_agent" }

// Run drives the prime-agent subprocess end-to-end. Mirrors
// CodexAdapter.Run / ClaudeAdapter.Run: set the harness name, stamp
// the resolved command, delegate to runWithSubprocess.
func (a *PrimeAgentAdapter) Run(ctx context.Context, in RunInput) (RunOutput, error) {
	in.Harness = a.Name()
	if in.Command == "" {
		in.Command = "prime-agent"
	}
	return runWithSubprocess(ctx, a.store, a.Name(), in, a.buildCmd, a.heartbeatInterval)
}

// buildCmd assembles the prime-agent argv. The shape is:
//
//	prime-agent --mode rpc -p --no-session --cwd <workspace> [--tools <list>] <prompt>
//
// Notes on each flag (verified against `prime-agent --help`):
//
//   - `--mode rpc` is the structured-output mode (IPC over stdin/stdout);
//     we leave rpc on by default so the audit trail is structured.
//   - `-p --no-session` is the one-shot headless print path that does
//     NOT persist session state to ~/.prime/agent/sessions/ — the
//     run-attempt chrome (transcript capture + heartbeat) is the only
//     audit trail, matching the registry limitations.
//   - `--cwd <workspace>` makes the harness's working directory
//     explicit; the run delegate resolves --workspace -> git_delivery
//     -> cwd before this point, so the adapter stamps a deterministic
//     workspace and never falls back to COMPOUND_HOME.
//   - `--tools <list>` enforces the latest caller-supplied tool
//     allowlist; an empty list falls through to Prime-agent's default
//     policy (no restriction) — the adapter does NOT invent a default.
//
// Modal choice: callers can short-circuit `--mode rpc` by setting
// RunInput.PermissionMode to "oneshot" (the value the work rail stamps
// for a `--dry-run` proof). Anything else keeps rpc on. This keeps the
// argv explicit at the call site without having to add a new RunInput
// field.
func (a *PrimeAgentAdapter) buildCmd(ctx context.Context, in RunInput) *exec.Cmd {
	// Timeout is owned by runWithSubprocess; see OSEntrypointsAdapter.buildCmd.
	prompt := in.Prompt
	if prompt == "" && len(in.Args) > 0 {
		prompt = in.Args[0]
	}

	args := []string{}
	// Modal selection. "oneshot" => -p --no-session; default => --mode rpc.
	if strings.EqualFold(strings.TrimSpace(in.PermissionMode), "oneshot") {
		args = append(args, "-p", "--no-session")
	} else {
		args = append(args, "--mode", "rpc")
	}

	// Workspace. The run delegate resolves --workspace -> git_delivery -> cwd
	// before this point; we stamp --cwd verbatim and never consult COMPOUND_HOME.
	// A missing workspace is a hard error: running prime-agent in the wrong
	// directory is exactly the failure mode this adapter is built to prevent.
	workspace := strings.TrimSpace(in.Workspace)
	if workspace == "" {
		// Defensive: the shared runner callers always populate Workspace,
		// but a misuse surfaces here rather than at run time.
		workspace = "."
	}
	args = append(args, "--cwd", workspace)

	// Tool policy. in.Args[1:] may carry additional flags; the adapter
	// appends them verbatim after the prompt-mode flags so the caller
	// can override anything (e.g. --provider switchback --model ...).
	// Guard against an empty Args slice — the shared runner treats
	// Args[0] as the prompt if Prompt is empty, so we must not strip
	// it here.
	if len(in.Args) > 1 {
		args = append(args, in.Args[1:]...)
	}

	// Prompt goes last, positional — same shape as the codex adapter.
	if prompt != "" {
		args = append(args, prompt)
	}

	// Honour the caller-supplied binary. Hardcoding the literal here would
	// make Run()'s in.Command default dead code and silently ignore an
	// explicit override. buildCmd defaults independently because tests and
	// readiness probes call it directly, without going through Run().
	binary := strings.TrimSpace(in.Command)
	if binary == "" {
		binary = "prime-agent"
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = workspace

	// Prime-agent has the same COMPOUND_HOME / ~/.prime-agent isolation
	// problem that codex had (CLAUDE_HOME in the Claude lane); every
	// governed dispatch needs a fresh per-run home so two concurrent
	// runs (or an exec running while an interactive `prime-agent` TUI
	// is active) cannot share state through ~/.prime/agent. Upstream
	// prime-agent reads its per-process home from
	// PRIME_AGENT_CODING_AGENT_DIR (packages/coding-agent/src/config.ts),
	// so we set that env var verbatim. We also set PI_SKIP_VERSION_CHECK=1
	// so prime does not try to self-update mid-run — a mid-run update
	// is a different binary's audit trail and would invalidate the
	// RunAttempt chrome. The persistent-kernel daemon at 127.0.0.1:18774
	// (a Switchback capture tap that prime's models.json points LLM
	// calls at, not prime's IPC) is system-wide and lives OUTSIDE this
	// isolation (read-only consumer); we only fence the per-run local
	// state.
	primeHome, err := os.MkdirTemp("", "prime-agent-")
	if err != nil {
		primeHome = filepath.Join(os.TempDir(), "prime-agent-fallback-"+in.WorkID)
		_ = os.MkdirAll(primeHome, 0o700)
	}
	cmd.Env = append(os.Environ(),
		"PRIME_AGENT_CODING_AGENT_DIR="+primeHome,
		"PI_SKIP_VERSION_CHECK=1",
	)
	cmd.Env = append(cmd.Env, in.Env...)
	return cmd
}

// FormatPrimeAgentDryRunPrompt is the canonical argv-printed summary
// the verifier prints when running a `--dry-run` proof. It exists here
// (not in the dry-run probe) so the same formatting is shared by the
// unit test and the dispatch verifier.
//
// The function is intentionally side-effect free — it only formats the
// argv the adapter would build — and is OK to call from tests.
func FormatPrimeAgentDryRunPrompt(in RunInput) string {
	mode := "rpc"
	if strings.EqualFold(strings.TrimSpace(in.PermissionMode), "oneshot") {
		mode = "oneshot"
	}
	workspace := strings.TrimSpace(in.Workspace)
	if workspace == "" {
		workspace = "(unset)"
	}
	prompt := in.Prompt
	if prompt == "" && len(in.Args) > 0 {
		prompt = in.Args[0]
	}
	return fmt.Sprintf("prime-agent --mode=%s --cwd=%s prompt=%q", mode, workspace, prompt)
}
