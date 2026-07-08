package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/bench"
	"compound/internal/home"
)

// cmdBench is the evaluator organ front door. It delegates to runBench, which returns an exit
// code (never calls os.Exit) so the fail-loud gates — absent must-hit ids on `run`, a split
// regression on `compare` — are exercised directly by tests without killing the test process.
func cmdBench(args []string) {
	if code := runBench(args, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

func runBench(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, benchUsage())
		return 2
	}
	switch args[0] {
	case "run":
		return runBenchRun(args[1:], stdout, stderr)
	case "compare":
		return runBenchCompare(args[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, benchUsage())
		return 2
	}
}

func benchUsage() string {
	return "usage:\n" +
		"  compound bench run --suite <file> [--home <dir>] [--json] [--allow-absent]\n" +
		"  compound bench compare --before <report.json> --after <report.json> [--json]"
}

func runBenchRun(args []string, stdout, stderr io.Writer) int {
	suitePath := flagValue(args, "--suite")
	if strings.TrimSpace(suitePath) == "" {
		fmt.Fprintln(stderr, "usage: compound bench run --suite <file> [--home <dir>] [--json] [--allow-absent]")
		return 2
	}
	homePath, err := home.Resolve(flagValue(args, "--home"))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	suite, err := bench.LoadSuite(suitePath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	now := time.Now()
	report, err := bench.Run(bench.Options{
		Home:     homePath,
		Suite:    suite,
		BuildSHA: buildSHAOrDev(),
		Now:      now,
	})
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	artifactPath, err := writeBenchReport(homePath, report, now)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	jsonOut := hasFlag(args, "--json")
	if jsonOut {
		if err := encodeJSON(stdout, report); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
	} else {
		printBenchSummary(stdout, report, artifactPath)
	}

	// Fail-loud gate: a must-hit expected id that is absent from the graph/ledger entirely is a
	// parity gap, not a tuning miss. Exit nonzero unless the operator opts into --allow-absent.
	if report.HasAbsent() && !hasFlag(args, "--allow-absent") {
		fmt.Fprintf(stderr, "bench: %d expected id(s) absent from graph/ledger (parity gap): %s\n",
			len(report.AbsentFromGraph), strings.Join(report.AbsentFromGraph, ", "))
		fmt.Fprintln(stderr, "pass --allow-absent to record the gap without failing the run")
		return 1
	}
	return 0
}

func runBenchCompare(args []string, stdout, stderr io.Writer) int {
	beforePath := flagValue(args, "--before")
	afterPath := flagValue(args, "--after")
	if strings.TrimSpace(beforePath) == "" || strings.TrimSpace(afterPath) == "" {
		fmt.Fprintln(stderr, "usage: compound bench compare --before <report.json> --after <report.json> [--json]")
		return 2
	}
	before, err := loadBenchReport(beforePath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	after, err := loadBenchReport(afterPath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	cmp := bench.Compare(before, after)
	if hasFlag(args, "--json") {
		if err := encodeJSON(stdout, cmp); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
	} else {
		printBenchCompareSummary(stdout, cmp)
	}
	if cmp.Regressed {
		fmt.Fprintf(stderr, "bench compare: regression on %s (hit@5 or MRR lower); accept rule requires no regression on either split\n",
			strings.Join(cmp.RegressedSplit, ", "))
		return 1
	}
	return 0
}

// writeBenchReport persists the report to $COMPOUND_HOME/reviews/bench/<ts>-<suite>.json —
// generated evidence under COMPOUND_HOME, never the repo tree.
func writeBenchReport(homePath string, report bench.Report, now time.Time) (string, error) {
	dir := filepath.Join(homePath, "reviews", "bench")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := now.UTC().Format("20060102T150405Z") + "-" + slugifyBench(report.Suite) + ".json"
	path := filepath.Join(dir, name)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func loadBenchReport(path string) (bench.Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return bench.Report{}, err
	}
	var r bench.Report
	if err := json.Unmarshal(data, &r); err != nil {
		return bench.Report{}, fmt.Errorf("parse report %s: %w", path, err)
	}
	return r, nil
}

func encodeJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

func printBenchSummary(w io.Writer, report bench.Report, artifactPath string) {
	fmt.Fprintf(w, "# compound bench %s (build %s, graph %s)\n", report.Suite, report.BuildSHA, report.GraphHash)
	fmt.Fprintf(w, "cases: %d scored, %d skipped\n", report.ScoredCount, report.SkippedCount)
	for _, split := range []string{bench.SplitHeldIn, bench.SplitHeldOut} {
		m := report.Splits[split]
		fmt.Fprintf(w, "  %-9s n=%-3d hit@1=%.3f hit@5=%.3f mrr=%.3f\n", split, m.Cases, m.Hit1, m.Hit5, m.MRR)
	}
	fmt.Fprintf(w, "  %-9s n=%-3d hit@1=%.3f hit@5=%.3f mrr=%.3f\n", "overall", report.Overall.Cases, report.Overall.Hit1, report.Overall.Hit5, report.Overall.MRR)
	if len(report.AbsentFromGraph) > 0 {
		fmt.Fprintf(w, "absent_from_graph (%d): %s\n", len(report.AbsentFromGraph), strings.Join(report.AbsentFromGraph, ", "))
	}
	fmt.Fprintf(w, "report: %s\n", artifactPath)
}

func printBenchCompareSummary(w io.Writer, cmp bench.CompareReport) {
	verdict := "ACCEPT"
	if cmp.Regressed {
		verdict = "REJECT"
	}
	fmt.Fprintf(w, "# compound bench compare: %s\n", verdict)
	if cmp.SuiteMismatch {
		fmt.Fprintf(w, "warning: before suite %q != after suite %q (comparison is apples-to-oranges)\n", cmp.BeforeSuite, cmp.AfterSuite)
	}
	for _, d := range cmp.Splits {
		flag := ""
		if d.Regressed {
			flag = "  <- REGRESSED"
		}
		fmt.Fprintf(w, "  %-9s hit@1 %+.3f  hit@5 %+.3f  mrr %+.3f%s\n", d.Split, d.Hit1Delta, d.Hit5Delta, d.MRRDelta, flag)
	}
	fmt.Fprintf(w, "  %-9s hit@1 %+.3f  hit@5 %+.3f  mrr %+.3f\n", "overall", cmp.Overall.Hit1Delta, cmp.Overall.Hit5Delta, cmp.Overall.MRRDelta)
}

func slugifyBench(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "suite"
	}
	return out
}
