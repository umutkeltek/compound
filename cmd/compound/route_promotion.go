package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"compound/internal/routeevidence"
)

func tryRoutePromotionCommand(args []string, homePath string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "import-promotion-criteria":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound route import-promotion-criteria <promotion-criteria.yaml>")
			os.Exit(2)
		}
		report, err := routeevidence.ImportPromotionCriteria(homePath, args[1], time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "route import-promotion-criteria:", err)
			os.Exit(1)
		}
		fmt.Println("# route import-promotion-criteria")
		fmt.Printf("- authority: %s\n", report.AuthorityRef)
		fmt.Printf("- authority_path: %s\n", report.AuthorityPath)
		fmt.Printf("- source_authority: %s\n", report.SourceAuthorityID)
		fmt.Printf("- entries: %d\n", report.EntryCount)
		fmt.Printf("- capability_classes: %s\n", strings.Join(report.CapabilityClasses, ","))
		fmt.Println("- verdict: compound_primary")
		return true
	case "promotion-review":
		laneID, jsonOut := parseRoutePromotionReviewArgs(args[1:])
		reviews, err := routeevidence.EvaluateLanePromotions(homePath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "route promotion-review:", err)
			os.Exit(1)
		}
		reviews = filterPromotionReviews(reviews, laneID)
		if jsonOut {
			printJSON(reviews)
			return true
		}
		printRoutePromotionReviews(reviews)
		return true
	default:
		return false
	}
}

func parseRoutePromotionReviewArgs(args []string) (laneID string, jsonOut bool) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--lane":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				fmt.Fprintln(os.Stderr, "usage: compound route promotion-review [--lane <id>] [--json]")
				os.Exit(2)
			}
			laneID = args[i]
		default:
			fmt.Fprintln(os.Stderr, "usage: compound route promotion-review [--lane <id>] [--json]")
			os.Exit(2)
		}
	}
	return laneID, jsonOut
}

func filterPromotionReviews(reviews []routeevidence.PromotionReview, laneID string) []routeevidence.PromotionReview {
	laneID = strings.TrimSpace(laneID)
	if laneID == "" {
		return reviews
	}
	filtered := make([]routeevidence.PromotionReview, 0, len(reviews))
	for _, review := range reviews {
		if review.LaneID == laneID {
			filtered = append(filtered, review)
		}
	}
	return filtered
}

func printRoutePromotionReviews(reviews []routeevidence.PromotionReview) {
	fmt.Println("# route promotion-review")
	fmt.Printf("- reviews: %d\n", len(reviews))
	for _, review := range reviews {
		fmt.Printf("- lane: %s capability_class=%s claim=%s samples=%d failed_status_rate=%.3f judge_json_malformed_rate=%.3f window_days=%.2f recommendation=%s\n",
			review.LaneID,
			review.CapabilityClass,
			review.CurrentClaimLevel,
			review.Tallies.Samples,
			review.Tallies.FailedStatusRate,
			review.Tallies.JudgeJSONMalformedRate,
			review.Tallies.WindowDays,
			review.Recommendation,
		)
		fmt.Printf("  reason: %s\n", review.Reason)
		if review.ProposedChange != "" {
			fmt.Printf("  proposed_change: %s\n", review.ProposedChange)
		}
	}
	fmt.Println("- verdict: review_artifacts_only_no_authority_mutation")
}
