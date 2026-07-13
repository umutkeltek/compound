package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"compound/internal/executorlens"
	"compound/internal/home"
)

// cmdExecutorLens is the operator/orchestrator door onto the executor-receipt locator — the
// evidence-only collector that reads THIS repo's executor lanes' (Codex, Claude Code) receipts/events
// and turns them into behavioral-lens signals. It delegates to runExecutorLens, which returns an exit
// code (never calls os.Exit) so the visibility gate and fail-loud paths are exercised by tests.
func cmdExecutorLens(args []string) {
	if code := runExecutorLens(args, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

func executorLensUsage() string {
	return "usage:\n" +
		"  compound executor-lens subjects [--home P] [--json]\n" +
		"    list the executor lanes (Codex, Claude Code) found in this home's receipts/events and how\n" +
		"    many hedging (public_safe) and agreement (operator_private) observations they yield — no writes\n" +
		"  compound executor-lens run [--home P] [--record] [--json]\n" +
		"    collect executor observations, compose them through the home registry (operator_private lenses\n" +
		"    run only if this home enables them — default off, stranger clone fail-closed), and emit the\n" +
		"    OUTWARD-safe public projection; --record appends every produced signal under $COMPOUND_HOME\n" +
		"the collector reads recorded envelope files under $COMPOUND_HOME/events; it makes NO provider calls\n" +
		"and the outward projection is public_safe only — operator_private executor signals never leave the home."
}

func runExecutorLens(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || hasUsageFlag(args) {
		fmt.Fprintln(stderr, executorLensUsage())
		return 2
	}
	switch args[0] {
	case "subjects":
		return runExecutorLensSubjects(args[1:], stdout, stderr)
	case "run":
		return runExecutorLensRun(args[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, executorLensUsage())
		return 2
	}
}

func runExecutorLensSubjects(args []string, stdout, stderr io.Writer) int {
	homePath, err := home.Resolve(flagValue(args, "--home"))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	col, err := executorlens.CollectObservations(homePath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if hasFlag(args, "--json") {
		if err := encodeJSON(stdout, col); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "# executor-lens subjects (%d executor lanes seen)\n", len(col.Subjects))
	for _, s := range col.Subjects {
		fmt.Fprintf(stdout, "- %s\n", s)
	}
	fmt.Fprintf(stdout, "hedging observations (public_safe): %d\n", len(col.Hedging))
	fmt.Fprintf(stdout, "agreement observations (operator_private): %d\n", len(col.Agreement))
	fmt.Fprintln(stdout, "note: observations become EVIDENCE signals only when composed (see `run`); nothing is gated or promoted.")
	return 0
}

func runExecutorLensRun(args []string, stdout, stderr io.Writer) int {
	homePath, err := home.Resolve(flagValue(args, "--home"))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	report, err := executorlens.Run(homePath, hasFlag(args, "--record"), time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if hasFlag(args, "--json") {
		if err := encodeJSON(stdout, report); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "# executor-lens run (home=%s)\n", report.HomePath)
	fmt.Fprintf(stdout, "executor lanes: %d | hedging obs: %d | agreement obs: %d\n",
		len(report.Subjects), report.HedgingObs, report.AgreementObs)
	fmt.Fprintf(stdout, "agreement-asymmetry lens enabled this home: %v\n", report.AgreementEnabled)
	fmt.Fprintf(stdout, "composed signals: %d | recorded: %d\n", report.Composed, report.Recorded)
	fmt.Fprintln(stdout, "# public projection (public_safe only — operator_private stays under $COMPOUND_HOME)")
	for _, sig := range report.PublicSignals {
		fmt.Fprintf(stdout, "- lens=%s subject=%s value=%.3f confidence=%.2f visibility=%s\n",
			sig.LensID, sig.Subject, sig.Value, sig.Confidence, sig.Visibility)
	}
	return 0
}
