package interest

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"compound/internal/home"
)

// Usage is the usage line for `compound interest`. It lives here, not in
// cmd/compound, so the cmd layer stays one registration line under its file and
// LOC ceilings (internal/arch.checkCmdCompoundBudget).
const Usage = "usage: compound interest <build [--json] [--home P] [--since 720h]|list [--json] [--home P] [--state S] [--top N]>"

// RunCLI runs `compound interest` and exits non-zero on failure (2 usage, 1
// runtime). `build` rebuilds and persists projections/interest.json; `list`
// reads the persisted projection and touches nothing.
func RunCLI(args []string) {
	if code := run(args, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "build" && args[0] != "list") {
		fmt.Fprintln(stderr, Usage)
		return 2
	}
	// Each subcommand defines only the flags it documents, so any other flag
	// (or a stray positional) is a usage error rather than silently ignored.
	fs := flag.NewFlagSet("interest "+args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut, homeFlag := fs.Bool("json", false, ""), fs.String("home", "", "")
	since, state, top := new(time.Duration), new(string), new(int)
	if args[0] == "build" {
		fs.DurationVar(since, "since", DefaultWindow, "")
	} else {
		fs.StringVar(state, "state", "", "")
		fs.IntVar(top, "top", 20, "") // 0 lists every signature
	}
	if fs.Parse(args[1:]) != nil || fs.NArg() > 0 || *since < 0 || *top < 0 ||
		!slices.Contains([]string{"", StateAssimilated, StateLearnable, StateSingleton}, *state) {
		fmt.Fprintln(stderr, Usage)
		return 2
	}
	homePath, err := home.Resolve(*homeFlag)
	if err != nil {
		fmt.Fprintln(stderr, "interest:", err)
		return 1
	}
	if args[0] == "build" {
		proj, err := Build(homePath, BuildOptions{Since: *since})
		if err == nil {
			_, err = WriteProjection(homePath, proj)
		}
		if err != nil {
			fmt.Fprintln(stderr, "interest build:", err)
			return 1
		}
		if *jsonOut {
			return printJSON(stdout, stderr, proj)
		}
		fmt.Fprintln(stdout, Headline(proj))
		fmt.Fprintf(stdout, "wrote %s: %d signatures, %d warnings\n", RelPath, len(proj.Signatures), len(proj.Warnings))
		return 0
	}
	proj, found, err := ReadProjection(homePath)
	if err != nil {
		fmt.Fprintln(stderr, "interest list:", err)
		return 1
	} else if !found {
		fmt.Fprintln(stderr, "interest: no projection yet; run `compound interest build`")
		return 1
	}
	rows := []Signature{}
	for _, s := range proj.Signatures {
		if (*state == "" || s.State == *state) && (*top == 0 || len(rows) < *top) {
			rows = append(rows, s)
		}
	}
	if *jsonOut {
		proj.Signatures = rows
		return printJSON(stdout, stderr, proj)
	}
	fmt.Fprintln(stdout, Headline(proj))
	fmt.Fprintf(stdout, "%4s  %-11s  %4s  %-22s  %-72s  %s\n", "rank", "state", "occ", "step", "signature", "last_seen")
	for _, s := range rows {
		fmt.Fprintf(stdout, "%4d  %-11s  %4d  %-22s  %-72s  %s\n",
			s.AttentionRank, s.State, s.Occurrences, truncate(s.Step, 22), truncate(s.Signature, 72), s.LastSeen)
	}
	return 0
}

// Headline is the one-line summary `build` and `list` print first.
func Headline(p Projection) string {
	learnable, absorbed := 0, 0
	for _, s := range p.Signatures {
		if s.State == StateLearnable {
			learnable++
		}
	}
	for _, a := range p.Progress.NewlyAssimilated {
		absorbed += a.Occurrences
	}
	prev := p.Progress.PreviousComputedAt
	if prev == "" {
		prev = "none"
	}
	line := fmt.Sprintf("interest: %d failed results in %s; %.1f%% assimilated; %d learnable signatures; progress since %s: +%d assimilated (%d results absorbed)",
		p.Totals.FailedResults, windowLabel(p.Window), p.Totals.AssimilatedShare*100, learnable, prev, len(p.Progress.NewlyAssimilated), absorbed)
	// Orphans ride on the line every reader already sees: a rule that stops
	// counting an explanation must say so where the share drops.
	if o := p.OrphanedExplanations; o.Count > 0 {
		reasons := []string{}
		for _, r := range slices.Sorted(maps.Keys(o.ByReason)) {
			reasons = append(reasons, fmt.Sprintf("%d %s", o.ByReason[r], r))
		}
		line += fmt.Sprintf("; %d orphaned explanations (%s)", o.Count, strings.Join(reasons, ", "))
	}
	return line
}

func windowLabel(w Window) string {
	since, err1 := time.Parse(time.RFC3339, w.Since)
	until, err2 := time.Parse(time.RFC3339, w.Until)
	if errors.Join(err1, err2) != nil {
		return "an unknown window"
	}
	return strconv.FormatFloat(until.Sub(since).Hours()/24, 'f', -1, 64) + "d"
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-3]) + "..."
	}
	return s
}

func printJSON(stdout, stderr io.Writer, v any) int {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "interest:", err)
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}
