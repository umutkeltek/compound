package main

import (
	"fmt"
	"os"
	"time"

	"compound/internal/home"
	"compound/internal/sourceobs"
)

func cmdSourceObservations(args []string) {
	if len(args) > 1 || (len(args) == 1 && args[0] != "--json") {
		fmt.Fprintln(os.Stderr, "usage: compound source observations [--json]")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	report, err := sourceobs.ListSourceObservations(homePath, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "source observations:", err)
		os.Exit(1)
	}
	if len(args) == 1 && args[0] == "--json" {
		printJSON(report)
		return
	}
	fmt.Printf("# source observations\n\n")
	fmt.Printf("- candidates: %d\n", report.Counts.TotalCandidates)
	fmt.Printf("- source_observations: %d\n", report.Counts.SourceObservations)
	fmt.Printf("- no_promote: %d\n", report.Counts.NoPromoteDecisions)
	for _, item := range report.Items {
		fmt.Printf("- observation: %s candidate=%s verdict=%s promote_allowed=%t\n", item.SourceObservationID, item.CandidateID, item.Verdict, item.PromoteAllowed)
	}
}
