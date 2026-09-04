package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"compound/internal/home"
	"compound/internal/readiness"
)

// `compound harness readiness --harness <h> [--workspace DIR] [--json]` — read-only readiness face (R3B 2026-09-04). Rendering lives in internal/readiness.Render.
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
	return readiness.Render(stdout, stderr, harnessName, workspace, result, jsonOut)
}
