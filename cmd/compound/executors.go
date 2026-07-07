package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"compound/internal/home"
	"compound/internal/localmodel"
	"compound/internal/work"
)

const executorsUsage = "usage: compound executors [--json] [--probe-local] [--lm-studio-url URL]"

func cmdExecutors(args []string) {
	jsonOut := false
	probeLocal := false
	lmStudioURL := ""
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "--json":
			jsonOut = true
		case "--probe-local":
			probeLocal = true
		case "--lm-studio-url":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				fmt.Fprintln(os.Stderr, executorsUsage)
				os.Exit(2)
			}
			lmStudioURL = strings.TrimSpace(args[i])
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
	opts := work.ExecutorsProjectionOptions{ProbeLocal: probeLocal}
	if probeLocal {
		if lmStudioURL == "" {
			lmStudioURL = localRoutingBaseURL()
		}
		opts.LMStudioClient = localmodel.LMStudioClient{BaseURL: lmStudioURL}
	}
	projection, err := store.ExecutorsProjection(time.Now(), opts)
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
		if executor.WaitReason != "" {
			fmt.Printf("- wait_reason: %s\n", executor.WaitReason)
		}
		if executor.LastHookEvent != nil {
			fmt.Printf("- last_hook_event: %s hook=%s status=%s\n", executor.LastHookEvent.CreatedAt, executor.LastHookEvent.Hook, executor.LastHookEvent.Status)
		}
		if executor.RouteEvidencePresent || executor.RouteEvidenceCount > 0 {
			fmt.Printf("- route_evidence: present=%t count=%d\n", executor.RouteEvidencePresent, executor.RouteEvidenceCount)
		}
		if executor.OldestLeaseAgeSeconds != nil {
			fmt.Printf("- oldest_lease_age_seconds: %d\n", *executor.OldestLeaseAgeSeconds)
		}
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
		if len(executor.Leases) > 0 {
			fmt.Println("- leases:")
			for _, lease := range executor.Leases {
				fmt.Printf("  - %s by=%s age_seconds=%d\n", lease.WorkID, lease.By, lease.AgeSeconds)
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
