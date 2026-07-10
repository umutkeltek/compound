package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"compound/internal/home"
	"compound/internal/reviewartifact"
)

const reviewsResolveUsage = "usage: compound reviews resolve --item ID --decision reviewed|parked|dismissed --reason TEXT [--json]"
const reviewsResolveUndoUsage = "usage: compound reviews resolve --item ID --undo --reason TEXT [--json]"

func cmdReviewsResolve(args []string) {
	itemID := strings.TrimSpace(valueFlag(args, "--item"))
	decision := strings.TrimSpace(valueFlag(args, "--decision"))
	reason := strings.TrimSpace(valueFlag(args, "--reason"))
	undo := hasFlag(args, "--undo")
	jsonOut := hasFlag(args, "--json")
	usage := reviewsResolveUsage
	if undo {
		usage = reviewsResolveUndoUsage
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json", "--undo":
		case "--item", "--decision", "--reason":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
			i++
		default:
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
	}
	if itemID == "" || reason == "" || (!undo && decision == "") || (undo && decision != "") {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if !undo && decision != "reviewed" && decision != "parked" && decision != "dismissed" {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "reviews resolve:", err)
		os.Exit(1)
	}
	report, err := buildReviewQueueReport(homePath, "", false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "reviews resolve:", err)
		os.Exit(1)
	}
	items := make([]reviewartifact.QueueItem, 0, 1)
	for _, item := range report.Items {
		if item.ID == itemID {
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		fmt.Fprintf(os.Stderr, "reviews resolve: unknown review item %q\n", itemID)
		os.Exit(1)
	}
	if len(items) > 1 {
		areas := make([]string, 0, len(items))
		for _, item := range items {
			areas = append(areas, item.Area)
		}
		fmt.Fprintf(os.Stderr, "reviews resolve: review item %q is ambiguous across areas %s\n", itemID, strings.Join(areas, ", "))
		os.Exit(1)
	}
	actor := firstNonEmpty(
		os.Getenv("COMPOUND_OPERATOR_ID"),
		os.Getenv("COMPOUND_REVIEWED_BY"),
		"operator",
	)
	var result reviewartifact.ReviewResolutionResult
	if undo {
		result, err = reviewartifact.UndoQueueItem(homePath, items[0], reviewartifact.UndoResolutionInput{
			Reason: reason,
			Actor:  actor,
		}, time.Now())
	} else {
		result, err = reviewartifact.ResolveQueueItem(homePath, items[0], reviewartifact.ResolutionInput{
			Decision: decision,
			Reason:   reason,
			Actor:    actor,
		}, time.Now())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "reviews resolve:", err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(result)
		return
	}
	fmt.Println("# reviews resolve")
	fmt.Printf("- operation: %s\n", result.Operation)
	fmt.Printf("- item: %s\n", result.ItemID)
	fmt.Printf("- area: %s\n", result.Area)
	if result.Decision != "" {
		fmt.Printf("- decision: %s\n", result.Decision)
	}
	fmt.Printf("- status: %s\n", result.Status)
	if result.Readiness != "" {
		fmt.Printf("- readiness: %s\n", result.Readiness)
	}
	fmt.Printf("- receipt: %s\n", result.ReceiptID)
	if result.UndoHint != "" {
		fmt.Printf("- undo: %s\n", result.UndoHint)
	}
}
