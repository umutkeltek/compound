package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/home"
	"compound/internal/runattempt"
	"compound/internal/work"
)

// cmdHarnessExecutors lists the registered adapter executors. Companion
// to `compound harness list` (which lists the capabilities manifest):
// the operator can see BOTH "what can this harness do" and "how do I
// drive it" without consulting two different surfaces.
func cmdHarnessExecutors(args []string) {
	homePath, err := home.Resolve("")
	die(err)
	store := runattempt.Open(homePath)
	r := harness.NewAdapterRegistry(store)
	names := r.Names()
	if hasFlag(args, "--json") {
		die(json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"executors": names,
		}))
		return
	}
	if len(names) == 0 {
		fmt.Println("(no adapter executors registered)")
		return
	}
	fmt.Printf("# %d adapter executor(s) registered\n", len(names))
	for _, n := range names {
		a, _ := r.AdapterByName(n)
		fmt.Printf("- %s\n", n)
		if a != nil {
			fmt.Printf("  adapter: %T\n", a)
		}
	}
}

// cmdHarnessRun drives an adapter to execute a command via a registered
// harness. This is the operator-facing entry point for the Adapter
// contract (C.1) + AdapterRegistry (C.3).
//
// Usage:
//
//	compound harness run --harness <name> --work <work_id> [--by <lane> | --actor <lane>] [-- <command> [args...]]
//	compound harness run --harness claude --work <work_id> --prompt "fix the bug"
//
// The work envelope must exist (read via work.Get). The adapter
// mints a RunAttempt, populates S2 fields as data emerges, terminalizes
// on subprocess exit, and returns the RunAttempt id. Exit code 0 on
// succeeded, non-zero on failed/timeout.
func cmdHarnessRun(args []string) {
	homePath, err := home.Resolve("")
	die(err)
	workID := valueFlag(args, "--work")
	harnessName := valueFlag(args, "--harness")
	prompt := valueFlag(args, "--prompt")
	jsonOut := hasFlag(args, "--json")
	// actor is the resolved lane that dispatched this run (--by, alias --actor).
	// Mirrors the work-advance --execute idiom. Optional and has no default/env
	// fallback: empty = unattributed run, RunInput.LaneID stays "" and the rail
	// stamps nothing (byte-identical to today's behavior). When set, it flows
	// into RunInput.LaneID and the rail stamps lane_id pre-terminalization.
	actor := firstNonEmpty(valueFlag(args, "--by"), valueFlag(args, "--actor"))

	if workID == "" {
		fmt.Fprintln(os.Stderr, "usage: compound harness run --harness <name> --work <work_id> [--by <lane> | --actor <lane>] [--prompt <text> | -- <command> [args...]] [--json]")
		os.Exit(2)
	}
	if harnessName == "" {
		fmt.Fprintln(os.Stderr, "usage: compound harness run --harness <name> --work <work_id> [--by <lane> | --actor <lane>] [--prompt <text> | -- <command> [args...]] [--json]")
		os.Exit(2)
	}

	// Validate work envelope exists. Read-only check -- the CLI does not
	// advance the work; that's the adapter's responsibility via the
	// action_intent_id the caller can pass. The returned envelope feeds
	// workspace resolution below.
	ws, err := work.Open(homePath)
	die(err)
	wenv, err := ws.Get(workID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "work envelope %q: %v\n", workID, err)
		os.Exit(1)
	}

	// Parse the rest-arg. Everything after `--` is the command + args.
	// If no `--`, the command is empty and the harness adapter must take
	// a prompt via --prompt.
	restArgs := args
	if idx := indexOf(args, "--"); idx >= 0 {
		restArgs = args[idx+1:]
	}
	var command string
	var commandArgs []string
	if len(restArgs) > 0 {
		command = restArgs[0]
		commandArgs = restArgs[1:]
	}

	// For the os-harness-entrypoints adapter, command is required
	// (it's the program path). For Claude/Codex, the adapter fills in
	// the binary path and --prompt is the prompt text. We can't tell
	// from here which adapter the user wants; require --prompt OR a
	// post-`--` command.
	if command == "" && prompt == "" {
		fmt.Fprintln(os.Stderr, "either --prompt or a post-`--` command is required")
		os.Exit(2)
	}

	store := runattempt.Open(homePath)
	r := harness.NewAdapterRegistry(store)
	a, ok := r.AdapterByName(harnessName)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown harness %q; available: %v\n", harnessName, r.Names())
		os.Exit(1)
	}

	// Resolve the execution workspace. Never default to COMPOUND_HOME --
	// running a real code-editing harness (codex/claude) inside the
	// state/config dir edits the wrong tree. See resolveExecuteWorkspace.
	workspace := resolveExecuteWorkspace(args, wenv.GitDelivery)
	if workspace == "" {
		fmt.Fprintln(os.Stderr, "error: cannot determine execution workspace; pass --workspace <path>")
		os.Exit(2)
	}

	in := harness.RunInput{
		WorkID:         workID,
		Harness:        harnessName,
		Workspace:      workspace,
		Command:        command,
		Args:           commandArgs,
		Prompt:         prompt,
		PermissionMode: valueFlag(args, "--permission-mode"),
		LaneID:         actor,
		Now:            time.Now().UTC(),
	}

	out, err := a.Run(context.Background(), in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "harness %s run: %v\n", harnessName, err)
		os.Exit(1)
	}

	if jsonOut {
		die(json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"run_attempt_id":  out.RunAttempt.ID,
			"status":          out.RunAttempt.Status,
			"harness":         out.RunAttempt.Harness,
			"command":         out.RunAttempt.Command,
			"workspace":       out.RunAttempt.Workspace,
			"transcript_path": out.TranscriptPath,
			"exit_code":       out.ExitCode,
			"timed_out":       out.TimedOut,
			"duration_ms":     out.Duration.Milliseconds(),
		}))
	} else {
		fmt.Printf("run_attempt_id: %s\n", out.RunAttempt.ID)
		fmt.Printf("status: %s\n", out.RunAttempt.Status)
		fmt.Printf("harness: %s\n", out.RunAttempt.Harness)
		fmt.Printf("command: %s\n", out.RunAttempt.Command)
		if out.TranscriptPath != "" {
			fmt.Printf("transcript: %s\n", out.TranscriptPath)
		}
		fmt.Printf("exit_code: %d\n", out.ExitCode)
		if out.TimedOut {
			fmt.Printf("timed_out: true\n")
		}
		fmt.Printf("duration_ms: %d\n", out.Duration.Milliseconds())
	}

	// Exit code matches the RunAttempt status: 0 on succeeded, 1 on
	// failed/timeout. The shell sees a non-zero exit when the harness
	// run failed, which is the natural operator contract.
	if out.RunAttempt.Status == runattempt.StatusSucceeded {
		return
	}
	os.Exit(1)
}

// indexOf returns the index of needle in haystack, or -1 if not present.
func indexOf(haystack []string, needle string) int {
	for i, s := range haystack {
		if s == needle {
			return i
		}
	}
	return -1
}

// resolveExecuteWorkspace picks the filesystem root a harness subprocess
// runs inside, for `harness run` and `work advance --execute`. Order:
//
//  1. explicit --workspace flag
//  2. the work envelope's git_delivery.target_worktree_root (where the
//     work's git delivery actually lands)
//  3. the current working directory
//
// It deliberately does NOT fall back to COMPOUND_HOME: running a real
// code-editing harness (codex/claude) inside the state/config dir edits
// the wrong tree and pollutes COMPOUND_HOME. Returns "" only when none of
// the three resolve (essentially: --workspace unset, no git delivery, and
// os.Getwd fails) -- the caller then refuses rather than defaulting home.
func resolveExecuteWorkspace(args []string, gd *envelope.GitDeliveryContract) string {
	if ws := strings.TrimSpace(valueFlag(args, "--workspace")); ws != "" {
		return ws
	}
	if gd != nil {
		if root := strings.TrimSpace(gd.TargetWorktreeRoot); root != "" {
			return root
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return ""
}
