// memory_consumption.go — work_c13acdc090b5 (S3a): the `compound memory consumption` CLI door
// onto internal/mem's cause-consumption report (see internal/mem/cause_consumption.go for the
// full doctrine). Mirrors cmdMemoryObservation's shape (memory_concepts.go) exactly: build the
// report, optionally persist it with --record, print JSON or the human summary.
package main

import (
	"fmt"
	"os"
	"time"

	"compound/internal/mem"
)

func cmdMemoryConsumption(homePath string, args []string) {
	wantJSON := hasFlag(args, "--json")
	rep, err := mem.BuildCauseConsumptionReport(homePath, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if hasFlag(args, "--record") {
		path, err := mem.WriteCauseConsumptionRun(homePath, rep, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "recorded: %s\n", path)
	}
	if wantJSON {
		printJSON(rep)
		return
	}
	printCauseConsumptionSummary(rep)
}

// printCauseConsumptionSummary renders the human view: the D6 coverage honesty note, the
// zero-filled five-tier bucket counts, the thesis ratio, and the S3b dealt->caused meter.
func printCauseConsumptionSummary(rep mem.CauseConsumptionReport) {
	fmt.Println("# memory cause consumption")
	fmt.Printf("- generated_at: %s\n", rep.GeneratedAt)
	fmt.Printf("- coverage: %s\n", rep.CoverageNote)
	fmt.Printf("- transitions_scanned: %d  with_cause: %d\n", rep.TransitionsScanned, rep.TransitionsWithCause)
	fmt.Println("- ref_tier_counts:")
	for _, tier := range []string{
		mem.CauseConsumptionTierPromoted,
		mem.CauseConsumptionTierPreAdjudication,
		mem.CauseConsumptionTierClosed,
		mem.CauseConsumptionTierObservedRaw,
		mem.CauseConsumptionTierNonMemory,
	} {
		fmt.Printf("    %-16s %d\n", tier, rep.RefTierCounts[tier])
	}
	fmt.Printf("- motion_from_unpromoted_ratio: %.2f (%s)\n", rep.ThesisRatio, rep.ThesisNote)
	fmt.Printf("- stranger dealt->caused: %d/%d dealt (%s)\n", rep.DealtCausedCount, rep.DealtCount, rep.DealtCausedNote)
	if len(rep.Transitions) == 0 {
		fmt.Println("- transitions: (none observed)")
		return
	}
	fmt.Println("- transitions:")
	for _, tr := range rep.Transitions {
		fmt.Printf("    %-20s cause=%-10s refs=%d  work=%s\n", tr.TransitionID, tr.DeltaCause, len(tr.CausedByRefs), tr.WorkID)
	}
}
