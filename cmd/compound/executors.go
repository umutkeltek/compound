package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"compound/internal/home"
	"compound/internal/work"
)

const executorsUsage = "usage: compound executors [--json]"

func cmdExecutors(args []string) {
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		default:
			fmt.Fprintln(os.Stderr, executorsUsage)
			os.Exit(2)
		}
	}

	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	store, err := work.Open(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	projection, err := store.ExecutorsProjection(time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	if jsonOut {
		if err := json.NewEncoder(os.Stdout).Encode(projection); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	printExecutorsProjection(projection)
}

func printExecutorsProjection(projection work.ExecutorsProjection) {
	fmt.Println("# compound executors")
	fmt.Printf("- status: %s\n", projection.Status)
	fmt.Printf("- generated_at: %s\n", projection.GeneratedAt)
	fmt.Printf("- counts: total=%d open=%d running=%d ready=%d needs_me=%d leases=%d\n",
		projection.Counts.Total,
		projection.Counts.Open,
		projection.Counts.Running,
		projection.Counts.Ready,
		projection.Counts.NeedsMe,
		projection.Counts.Leases,
	)
	fmt.Printf("- source_refs: %s\n", strings.Join(projection.SourceRefs, ", "))
	for _, executor := range projection.Executors {
		fmt.Printf("\n## %s\n", executor.Label)
		fmt.Printf("- status: %s (%s)\n", executor.Status, executor.StatusSource)
		fmt.Printf("- counts: assigned=%d running=%d ready=%d needs_me=%d leases=%d\n",
			executor.AssignedCount,
			executor.RunningCount,
			executor.ReadyCount,
			executor.NeedsMeCount,
			executor.LeaseCount,
		)
		if len(executor.BestFor) > 0 {
			fmt.Printf("- best_for: %s\n", strings.Join(executor.BestFor, ", "))
		}
		if len(executor.CanUseTools) > 0 {
			fmt.Printf("- tools: %s\n", strings.Join(executor.CanUseTools, ", "))
		}
		if len(executor.CostLimits) > 0 {
			fmt.Printf("- cost_limits: %s\n", strings.Join(executor.CostLimits, ", "))
		}
		if executor.ProjectionNote != "" {
			fmt.Printf("- note: %s\n", executor.ProjectionNote)
		}
		if executor.NextAssigned != nil {
			fmt.Printf("- next_assigned: %s %s\n", executor.NextAssigned.WorkID, executor.NextAssigned.Title)
		}
		if len(executor.CurrentWork) > 0 {
			fmt.Println("- current_work:")
			for _, item := range executor.CurrentWork {
				fmt.Printf("  - %s state=%s title=%q\n", item.WorkID, item.State, item.Title)
			}
		}
		if executor.LastSuccess != nil {
			fmt.Printf("- last_success: %s %s\n", executor.LastSuccess.WorkID, executor.LastSuccess.Title)
		}
		if executor.LastFailure != nil {
			fmt.Printf("- last_failure: %s %s\n", executor.LastFailure.WorkID, executor.LastFailure.Title)
		}
	}
}
