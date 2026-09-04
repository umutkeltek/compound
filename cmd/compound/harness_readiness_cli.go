package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"compound/internal/home"
	"compound/internal/readiness"
)

// `compound harness readiness --harness <h> [--workspace DIR] [--json]`
// is the read-only readiness face introduced by R3B 2026-09-04. It
// evaluates the same readiness the execute-time guard sees — without
// needing a work id — and prints Ready/details/evidence plus the per
// unmet-detail repair commands, so an operator can ask "can this lane
// run anything right now?" without claiming work first.
//
// Both `work delegate --dry-run` and this verb call readiness.EvaluateForHarness;
// the only logic in this file is arg parsing, stdout rendering, and exit code.
func runHarnessReadiness(args []string, stdout, stderr io.Writer) int {
	harnessName := strings.TrimSpace(valueFlag(args, "--harness"))
	if harnessName == "" {
		fmt.Fprintln(stderr, "usage: compound harness readiness --harness <name> [--workspace DIR] [--json]")
		return 2
	}
	workspace := strings.TrimSpace(valueFlag(args, "--workspace"))
	jsonOut := hasFlag(args, "--json")

	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(stderr, "error:", err, "(set COMPOUND_HOME)")
		return 1
	}
	result, err := readiness.EvaluateForHarness(homePath, harnessName, workspace, "", time.Now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if jsonOut {
		rep := struct {
			Harness        string   `json:"harness"`
			Workspace      string   `json:"workspace,omitempty"`
			Ready          bool     `json:"ready"`
			Details        []string `json:"details,omitempty"`
			RepairCommands []string `json:"repair_commands,omitempty"`
		}{
			Harness:        harnessName,
			Workspace:      workspace,
			Ready:          result.Ready,
			Details:        result.Details,
			RepairCommands: readiness.RepairCommandsForHarness(harnessName, workspace, result.Details),
		}
		if err := json.NewEncoder(stdout).Encode(rep); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		if !result.Ready {
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "# harness readiness\n")
	fmt.Fprintf(stdout, "- harness: %s\n", harnessName)
	if workspace != "" {
		fmt.Fprintf(stdout, "- workspace: %s\n", workspace)
	}
	fmt.Fprintf(stdout, "- status: %s\n", map[bool]string{true: "ready", false: "unready"}[result.Ready])
	for _, detail := range result.Details {
		fmt.Fprintf(stdout, "- detail: %s\n", detail)
	}
	if !result.Ready {
		fmt.Fprintln(stdout, "- repair:")
		for _, cmd := range readiness.RepairCommandsForHarness(harnessName, workspace, result.Details) {
			fmt.Fprintf(stdout, "    %s\n", cmd)
		}
	}
	if !result.Ready {
		return 1
	}
	return 0
}
