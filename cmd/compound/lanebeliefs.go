package main

import (
	"fmt"
	"os"

	"compound/internal/home"
	"compound/internal/lanebeliefs"
)

func laneBeliefsUsage() string {
	return "usage: compound lanebeliefs <fold [--dry-run] [--json] [--home P]|list [--json] [--home P]>"
}

func cmdLaneBeliefs(args []string) {
	if len(args) == 0 || hasUsageFlag(args) {
		fmt.Fprintln(os.Stderr, laneBeliefsUsage())
		os.Exit(2)
	}
	sub := args[0]
	jsonOut := false
	dryRun := false
	homeFlag := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--dry-run":
			dryRun = true
		case "--home":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "lanebeliefs: --home requires a path")
				os.Exit(2)
			}
			homeFlag = args[i+1]
			i++
		default:
			fmt.Fprintln(os.Stderr, laneBeliefsUsage())
			os.Exit(2)
		}
	}
	homePath, err := home.Resolve(homeFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lanebeliefs:", err)
		os.Exit(1)
	}
	switch sub {
	case "fold":
		report, err := lanebeliefs.Fold(homePath, lanebeliefs.FoldOptions{DryRun: dryRun})
		if err != nil {
			fmt.Fprintln(os.Stderr, "lanebeliefs fold:", err)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(report)
			return
		}
		fmt.Printf("# lanebeliefs fold\n")
		fmt.Printf("- raw_receipts: %d\n", report.RawReceiptCount)
		fmt.Printf("- observations: %d\n", report.NewObservationCount)
		fmt.Printf("- candidates: %d\n", report.CandidateCount)
		fmt.Printf("- unresolved_receipts: %d\n", report.UnresolvedReceiptCount)
		fmt.Printf("- cursor: %d -> %d\n", report.CursorBefore, report.CursorAfter)
	case "list":
		report, err := lanebeliefs.List(homePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "lanebeliefs list:", err)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(report)
			return
		}
		fmt.Printf("# lanebeliefs list\n")
		fmt.Printf("- candidates: %d\n", report.CandidateCount)
		for _, candidate := range report.Candidates {
			fmt.Printf("%s %s.%s=%s state=%s\n", candidate.ID, candidate.Entity, candidate.Predicate, candidate.Object, candidate.State)
		}
	default:
		fmt.Fprintln(os.Stderr, laneBeliefsUsage())
		os.Exit(2)
	}
}
