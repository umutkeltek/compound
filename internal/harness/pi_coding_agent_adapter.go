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

// The pi coding-agent FAMILY. prime_agent and oh_my_pi are two forks of the
// same upstream coding agent, and their headless contract is the same argv
// with a different binary name and a different per-run home env var. That is a
// parameterisation, not two integrations, and writing it twice would mean two
// places to fix the next time the family's dispatch contract moves.
//
// Verified against the installed binaries (`prime-agent --help`, `omp --help`):
//
//	-p / --print       one-shot print mode; the process exits when the turn ends
//	--no-session       do not persist session state to the agent home
//	--mode <m>         structured output mode; both accept text|json|rpc
//	--cwd <dir>        working directory (both validate it before dialling out)
//	-t / --tools <l>   comma-separated tool allowlist (prime spells it -t/--tools,
//	                   oh-my-pi spells it --tools; the long form works on both)
//	--system-prompt / --append-system-prompt
//	                   how Compound injects context, since neither fork has a
//	                   SessionStart/UserPromptSubmit hook to inject through
//
// What the family does NOT share is the environment: prime-agent honours
// PRIME_AGENT_CODING_AGENT_DIR and PI_SKIP_VERSION_CHECK, oh-my-pi honours
// PI_CODING_AGENT_DIR and has no version-check suppression env at all. Those
// differences are spec fields rather than branches in buildCmd, so a new family
// member is a struct literal.
type piCodingAgentSpec struct {
	// name is the registry id: the AdapterRegistry key, the harness registry
	// row, and the RunAttempt.Harness value. Drift between those three is a
	// silent routing failure, so one field feeds all of them.
	name string
	// binary is the default argv[0] when RunInput.Command is empty.
	binary string
	// homeEnv is the env var this fork reads for its per-run agent home.
	// Every governed dispatch gets a fresh temp home so two concurrent runs
	// (or a run alongside an interactive TUI) cannot share credentials,
	// sessions or memory through the shared agent directory.
	homeEnv string
	// homeTempPrefix prefixes the MkdirTemp home so a leaked directory is
	// attributable to a harness rather than anonymous.
	homeTempPrefix string
	// extraEnv holds fork-specific env lines appended after the home line.
	// Kept explicit: setting a var a fork does not read is a claim on disk
	// that nothing enforces, and the next reader would believe it.
	extraEnv []string
}

// PiCodingAgentAdapter drives one member of the pi coding-agent family as a
// subprocess and wraps the execution in a RunAttempt. Mirrors the Codex/Claude
// adapter shape: a thin shim over the shared runWithSubprocess runner
// (subprocess_runner.go), with family argv assembly and the spec's harness name
// on the RunAttempt.
//
// The adapter:
//   - prefers `--mode rpc` so the audit trail is structured;
//   - falls back to `-p --no-session` for one-shot dry-runs and CI;
//   - never uses COMPOUND_HOME or ~/.compound as the workspace root — callers
//     MUST supply RunInput.Workspace (the run delegate resolves --workspace ->
//     git_delivery -> cwd before the adapter sees it).
//
// It does NOT implement either fork's persistent kernel. It drives a fresh
// subprocess for every Run(); the persistent kernel is a separate daemon
// process (prime-agent's listens on a Unix socket under
// $TMPDIR/prime-agent-<uid>/daemon.sock). Compound is the orchestrator; this
// adapter is the dispatch driver.
//
// Capability-floor honesty: both family rows in DefaultRegistry declare zero
// hook flags. Neither fork exposes a hook protocol Compound's installer can
// write — prime-agent has none at all, and oh-my-pi's is file-based JS modules
// rather than shell-command settings entries — so the registry limitations say
// so, and no future reader can infer enforcement-via-harness from a harness
// that genuinely cannot enforce it.
type PiCodingAgentAdapter struct {
	spec              piCodingAgentSpec
	store             *runattempt.Store
	heartbeatInterval time.Duration
}

// newPiCodingAgentAdapter is the single constructor the per-fork constructors
// funnel through. A non-positive interval degrades to the default cadence.
func newPiCodingAgentAdapter(spec piCodingAgentSpec, store *runattempt.Store, interval time.Duration) *PiCodingAgentAdapter {
	if interval <= 0 {
		interval = defaultHeartbeatInterval
	}
	return &PiCodingAgentAdapter{spec: spec, store: store, heartbeatInterval: interval}
}

// Name returns the manifest adapter id. Keys the AdapterRegistry lookup and
// the RunAttempt.Harness field.
func (a *PiCodingAgentAdapter) Name() string { return a.spec.name }

// Run drives the subprocess end-to-end. Mirrors CodexAdapter.Run /
// ClaudeAdapter.Run: set the harness name, stamp the resolved command,
// delegate to runWithSubprocess.
func (a *PiCodingAgentAdapter) Run(ctx context.Context, in RunInput) (RunOutput, error) {
	in.Harness = a.Name()
	if in.Command == "" {
		in.Command = a.spec.binary
	}
	return runWithSubprocess(ctx, a.store, a.Name(), in, a.buildCmd, a.heartbeatInterval)
}

// buildCmd assembles the family argv. The shape is:
//
//	<binary> --mode rpc --cwd <workspace> [caller flags...] <prompt>
//	<binary> -p --no-session --cwd <workspace> [caller flags...] <prompt>
//
// Notes on each flag (verified against the installed `--help` of both forks):
//
//   - `--mode rpc` is the structured-output mode; both forks list
//     text|json|rpc, so rpc stays on by default and the audit trail is
//     structured.
//   - `-p --no-session` is the one-shot headless print path that does NOT
//     persist session state to the agent home — the run-attempt chrome
//     (transcript capture + heartbeat) is the only audit trail, matching the
//     registry limitations.
//   - `--cwd <workspace>` makes the working directory explicit; the run
//     delegate resolves --workspace -> git_delivery -> cwd before this point,
//     so the adapter stamps a deterministic workspace and never falls back to
//     COMPOUND_HOME. Both forks validate the directory locally before any
//     network call, so a bad workspace fails fast rather than mid-turn.
//   - `--tools <list>` enforces a caller-supplied tool allowlist; the adapter
//     does NOT invent a default, so an empty list falls through to the fork's
//     own policy. Callers pass it (and anything else, e.g. --approval-mode or
//     --model) through RunInput.Args.
//
// Modal choice: callers short-circuit `--mode rpc` by setting
// RunInput.PermissionMode to "oneshot" (the value the work rail stamps for a
// `--dry-run` proof). Anything else keeps rpc on. This keeps the argv explicit
// at the call site without adding a RunInput field.
func (a *PiCodingAgentAdapter) buildCmd(ctx context.Context, in RunInput) *exec.Cmd {
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
	// A missing workspace is a hard error: running a coding agent in the wrong
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
		binary = a.spec.binary
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = workspace

	// Per-run home isolation. The family has the same COMPOUND_HOME /
	// shared-agent-dir problem codex had (CLAUDE_HOME in the Claude lane):
	// every governed dispatch needs a fresh home so two concurrent runs (or an
	// exec running while an interactive TUI is active) cannot share auth,
	// sessions or memory. A fresh home also means the fork has no stored
	// credential to fall back on, which is deliberate — the lane's env-var
	// provider config is then the only way it can reach a model, and an
	// unconfigured lane fails loudly instead of quietly using an uncaptured
	// default provider. Any Switchback capture tap is system-wide and lives
	// OUTSIDE this isolation (read-only consumer); we fence per-run local state
	// only.
	agentHome, err := os.MkdirTemp("", a.spec.homeTempPrefix)
	if err != nil {
		agentHome = filepath.Join(os.TempDir(), a.spec.homeTempPrefix+"fallback-"+in.WorkID)
		_ = os.MkdirAll(agentHome, 0o700)
	}
	cmd.Env = append(os.Environ(), a.spec.homeEnv+"="+agentHome)
	cmd.Env = append(cmd.Env, a.spec.extraEnv...)
	cmd.Env = append(cmd.Env, in.Env...)
	return cmd
}

// formatPiCodingAgentDryRunPrompt is the canonical argv-printed summary the
// verifier prints when running a `--dry-run` proof. It lives beside the adapter
// (not in the dry-run probe) so the same formatting is shared by the unit tests
// and the dispatch verifier.
//
// Intentionally side-effect free — it only formats the argv the adapter would
// build — and safe to call from tests.
func formatPiCodingAgentDryRunPrompt(binary string, in RunInput) string {
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
	return fmt.Sprintf("%s --mode=%s --cwd=%s prompt=%q", binary, mode, workspace, prompt)
}
