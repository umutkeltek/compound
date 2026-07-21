package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"compound/internal/work"
)

type delegateCLIOptions struct {
	workID, harness, actor, session, prompt, workspace string
	command                                            string
	args                                               []string
	expectedMinutes                                    int
	confirm, dryRun, jsonOut                           bool
}

type steerCLIOptions struct {
	workID, actor, text string
	enter, jsonOut      bool
}

// runWorkDelegateCLI is the testable command edge for work
// delegate|steer|attach. Parsing and rendering live here; lifecycle meaning
// remains in internal/work and process supervision remains behind runner.
func runWorkDelegateCLI(sub string, args []string, store *work.Store, runner work.SessionRunner, attach func([]string) error, now time.Time, stdout, stderr io.Writer) int {
	if store == nil || runner == nil {
		fmt.Fprintln(stderr, "error: delegate runtime is unavailable")
		return 1
	}
	switch sub {
	case "delegate":
		opts, err := parseDelegateCLI(args)
		if err != nil {
			return delegateCLIUsageError(stderr, "delegate", err)
		}
		result, err := store.Delegate(work.DelegateOptions{
			WorkID:          opts.workID,
			Harness:         opts.harness,
			Session:         opts.session,
			Actor:           opts.actor,
			Command:         opts.command,
			Args:            opts.args,
			Workspace:       opts.workspace,
			ExpectedMinutes: opts.expectedMinutes,
			Confirm:         opts.confirm,
			DryRun:          opts.dryRun,
			Now:             now,
		}, runner)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		if opts.jsonOut {
			if err := encodeDelegateCLIJSON(stdout, result); err != nil {
				fmt.Fprintln(stderr, "error: encode delegate result:", err)
				return 1
			}
		} else {
			renderDelegateResult(stdout, result)
		}
		if result.Status == "refused" {
			return 1
		}
		return 0

	case "steer":
		opts, err := parseSteerCLI(args)
		if err != nil {
			return delegateCLIUsageError(stderr, "steer", err)
		}
		result, err := store.Steer(work.SteerOptions{
			WorkID: opts.workID,
			Actor:  opts.actor,
			Text:   opts.text,
			Enter:  opts.enter,
			Now:    now,
		}, runner)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		if opts.jsonOut {
			if err := encodeDelegateCLIJSON(stdout, result); err != nil {
				fmt.Fprintln(stderr, "error: encode steer result:", err)
				return 1
			}
		} else {
			fmt.Fprintf(stdout, "delegate steered: work=%s session=%s event=%s\n", result.WorkID, result.Session, result.EventID)
		}
		return 0

	case "attach":
		workID, err := parseAttachCLI(args)
		if err != nil {
			return delegateCLIUsageError(stderr, "attach", err)
		}
		result, err := store.Attach(work.AttachOptions{WorkID: workID}, runner)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		if attach == nil {
			fmt.Fprintln(stderr, "error: attach runtime is unavailable")
			return 1
		}
		if err := attach(result.AttachArgv); err != nil {
			fmt.Fprintln(stderr, "error: attach session:", err)
			return 1
		}
		return 0
	default:
		return delegateCLIUsageError(stderr, sub, fmt.Errorf("unknown governed execution verb %q", sub))
	}
}

func parseDelegateCLI(args []string) (delegateCLIOptions, error) {
	var out delegateCLIOptions
	flagArgs, rawCommand := splitDelegateRawCommand(args)
	seenPrompt := false
	positionals := make([]string, 0, 1)
	for i := 0; i < len(flagArgs); i++ {
		arg := flagArgs[i]
		readValue := func() (string, error) {
			if i+1 >= len(flagArgs) {
				return "", fmt.Errorf("%s requires a value", arg)
			}
			i++
			return flagArgs[i], nil
		}
		switch arg {
		case "--harness", "--actor", "--session", "--prompt", "--workspace", "--expected-minutes":
			value, err := readValue()
			if err != nil {
				return out, err
			}
			switch arg {
			case "--harness":
				out.harness = value
			case "--actor":
				out.actor = value
			case "--session":
				out.session = value
			case "--prompt":
				out.prompt, seenPrompt = value, true
			case "--workspace":
				out.workspace = value
			case "--expected-minutes":
				minutes, parseErr := strconv.Atoi(value)
				if parseErr != nil || minutes <= 0 {
					return out, errors.New("--expected-minutes must be a positive integer")
				}
				out.expectedMinutes = minutes
			}
		case "--confirm":
			out.confirm = true
		case "--dry-run":
			out.dryRun = true
		case "--json":
			out.jsonOut = true
		default:
			if strings.HasPrefix(arg, "-") {
				return out, fmt.Errorf("unknown flag %s", arg)
			}
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) != 1 || strings.TrimSpace(positionals[0]) == "" {
		return out, errors.New("delegate requires exactly one work_id")
	}
	out.workID = positionals[0]
	if strings.TrimSpace(out.harness) == "" {
		return out, errors.New("delegate requires --harness")
	}
	if seenPrompt && strings.TrimSpace(out.prompt) == "" {
		return out, errors.New("--prompt requires non-empty text")
	}
	if seenPrompt && len(rawCommand) > 0 {
		return out, errors.New("pass either --prompt or a raw command after --, not both")
	}
	wrapper, err := work.ResolveDelegateHarness(out.harness)
	if err != nil {
		return out, err
	}
	out.command = wrapper
	if seenPrompt {
		out.args = []string{out.prompt}
	}
	if rawCommand != nil {
		if len(rawCommand) == 0 || strings.TrimSpace(rawCommand[0]) == "" {
			return out, errors.New("-- must be followed by a command")
		}
		out.command = rawCommand[0]
		out.args = append([]string(nil), rawCommand[1:]...)
	}
	return out, nil
}

func splitDelegateRawCommand(args []string) ([]string, []string) {
	for i, arg := range args {
		if arg == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

func parseSteerCLI(args []string) (steerCLIOptions, error) {
	var out steerCLIOptions
	positionals := make([]string, 0, 1)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--text", "--actor":
			if i+1 >= len(args) {
				return out, fmt.Errorf("%s requires a value", arg)
			}
			i++
			if arg == "--text" {
				out.text = args[i]
			} else {
				out.actor = args[i]
			}
		case "--enter":
			out.enter = true
		case "--json":
			out.jsonOut = true
		default:
			if strings.HasPrefix(arg, "-") {
				return out, fmt.Errorf("unknown flag %s", arg)
			}
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) != 1 || strings.TrimSpace(positionals[0]) == "" {
		return out, errors.New("steer requires exactly one work_id")
	}
	if strings.TrimSpace(out.text) == "" {
		return out, errors.New("steer requires --text")
	}
	out.workID = positionals[0]
	return out, nil
}

func parseAttachCLI(args []string) (string, error) {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" || strings.HasPrefix(args[0], "-") {
		return "", errors.New("attach requires exactly one work_id and no flags")
	}
	return args[0], nil
}

func delegateCLIUsageError(stderr io.Writer, sub string, err error) int {
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
	}
	fmt.Fprintln(stderr, workUsage(sub))
	return 2
}

func encodeDelegateCLIJSON(stdout io.Writer, value any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func renderDelegateResult(stdout io.Writer, result work.DelegateResult) {
	switch result.Status {
	case "preview":
		fmt.Fprintf(stdout, "delegate: status=preview work=%s lane=%s", result.WorkID, result.Lane)
		if result.Decision != nil {
			fmt.Fprintf(stdout, " capacity=%s refused=%t confirmation_required=%t", result.Decision.Capacity.State, result.Decision.Refused, result.Decision.ConfirmationRequired)
		}
		fmt.Fprintln(stdout)
		if result.Decision != nil && strings.TrimSpace(result.Decision.Message) != "" {
			fmt.Fprintln(stdout, "decision:", result.Decision.Message)
		}
	case "spawned":
		fmt.Fprintf(stdout, "delegate spawned: work=%s session=%s harness=%s lane=%s event=%s\n", result.WorkID, result.Binding.Session, result.Binding.Harness, result.Binding.Lane, result.EventID)
		fmt.Fprintf(stdout, "attach: compound work attach %s\n", result.WorkID)
	case "refused":
		fmt.Fprintf(stdout, "delegate refused: work=%s", result.WorkID)
		if result.Refusal != nil {
			fmt.Fprintf(stdout, " reason=%s", result.Refusal.Reason)
			if result.Refusal.Session != "" {
				fmt.Fprintf(stdout, " session=%s", result.Refusal.Session)
			}
		}
		fmt.Fprintln(stdout)
		if result.Refusal != nil && strings.TrimSpace(result.Refusal.Message) != "" {
			fmt.Fprintln(stdout, "decision:", result.Refusal.Message)
		}
	}
}

func runDelegateAttachProcess(argv []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(argv) == 0 {
		return errors.New("empty attach command")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func productionDelegateAttach(argv []string) error {
	return runDelegateAttachProcess(argv, os.Stdin, os.Stdout, os.Stderr)
}
