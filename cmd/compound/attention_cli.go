package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"compound/internal/attention"
	"compound/internal/home"
	"compound/internal/work"
)

const attentionUsage = "usage: compound attention [--json] [--home P]"

func cmdAttention(args []string) {
	if hasUsageFlag(args) {
		fmt.Println(attentionUsage)
		return
	}
	jsonOut := false
	homeFlag := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--home":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				fmt.Fprintln(os.Stderr, attentionUsage)
				os.Exit(2)
			}
			i++
			homeFlag = strings.TrimSpace(args[i])
		default:
			fmt.Fprintln(os.Stderr, attentionUsage)
			os.Exit(2)
		}
	}

	homePath, err := home.Resolve(homeFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	store, err := work.Open(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	now := time.Now()
	reviews, err := buildReviewQueueReport(homePath, "", false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	// One Workbench build serves both consumers below. attention.Build never
	// reads Recent, so ExecutorsProjection's RecentLimit 8 is the binding
	// requirement; building twice (5 here, 8 inside the projection) walked
	// the whole event store twice per invocation.
	workbench, err := store.Workbench(now, work.WorkbenchOptions{RecentLimit: 8})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	executors, err := store.ExecutorsProjection(now, work.ExecutorsProjectionOptions{Workbench: &workbench})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	report := attention.Build(attention.Inputs{
		Reviews:   &reviews,
		Workbench: &workbench,
		Executors: &executors,
	}, now)
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", " ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	printAttentionText(report)
}

func printAttentionText(report attention.Report) {
	fmt.Printf("attention needs_you=%d groups=%d actionable_now=%d watching=%d blocked_on_you=%d fallback_groups=%d\n",
		report.Counts.NeedsYou,
		report.Counts.NeedsYouGroups,
		report.Counts.ActionableNow,
		report.Counts.Watching,
		report.Counts.BlockedOnYou,
		report.Counts.FallbackGroups,
	)
	limit := len(report.Groups)
	if limit > 10 {
		limit = 10
	}
	for i := 0; i < limit; i++ {
		group := report.Groups[i]
		fmt.Printf("- %s %.2f %s/%s count=%d %s",
			attention.Class(group),
			group.Leverage,
			group.Source,
			group.Area,
			group.Count,
			group.OperatorSummary,
		)
		if group.OperatorUnlock != "" {
			fmt.Printf(" unlock=%q", group.OperatorUnlock)
		}
		if group.Action != nil {
			fmt.Printf(" action=%s/%s", group.Action.Kind, group.Action.ID)
		}
		if group.TerminalReason != "" {
			fmt.Printf(" reason=%q", group.TerminalReason)
		}
		fmt.Println()
	}
}
