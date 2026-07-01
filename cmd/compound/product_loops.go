package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"compound/internal/product"
)

func cmdProductLoops(args []string, homePath string) {
	opts, asJSON, err := parseProductLoopArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "product loops:", err)
		os.Exit(2)
	}
	model, err := product.BuildLoopModel(homePath, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "product loops:", err)
		os.Exit(1)
	}
	if asJSON {
		b, err := json.MarshalIndent(model, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "product loops:", err)
			os.Exit(1)
		}
		fmt.Println(string(b))
		return
	}
	renderProductLoops(model)
}

func parseProductLoopArgs(args []string) (product.LoopOptions, bool, error) {
	var opts product.LoopOptions
	asJSON := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--product":
			if i+1 >= len(args) {
				return opts, false, fmt.Errorf("usage: compound product loops [--product <id>] [--json] [--recent N] [--stale-days D]")
			}
			opts.ProductID = args[i+1]
			i++
		case "--recent":
			value, err := parsePositiveIntFlag(args, &i, "--recent")
			if err != nil {
				return opts, false, err
			}
			opts.Recent = value
		case "--stale-days":
			value, err := parsePositiveIntFlag(args, &i, "--stale-days")
			if err != nil {
				return opts, false, err
			}
			opts.StaleDays = value
		default:
			return opts, false, fmt.Errorf("usage: compound product loops [--product <id>] [--json] [--recent N] [--stale-days D]")
		}
	}
	return opts, asJSON, nil
}

func parsePositiveIntFlag(args []string, index *int, flag string) (int, error) {
	if *index+1 >= len(args) {
		return 0, fmt.Errorf("%s requires a positive integer", flag)
	}
	value, err := strconv.Atoi(args[*index+1])
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s requires a positive integer", flag)
	}
	*index = *index + 1
	return value, nil
}

func renderProductLoops(model product.LoopModel) {
	fmt.Printf("# product loops\n\n")
	fmt.Printf("- generated_at: %s\n", model.GeneratedAt)
	fmt.Printf("- products: %d\n", len(model.Products))
	for _, loop := range model.Products {
		fmt.Printf("\n## %s\n", loop.ProductID)
		fmt.Printf("- name: %s\n", loop.Name)
		fmt.Printf("- freshness: latest_signal=%s latest_review=%s stale=%t\n", none(loop.Freshness.LatestSignalAt), none(loop.Freshness.LatestReviewAt), loop.Freshness.Stale)
		fmt.Printf("- open_source_issues: %d", loop.OpenSourceIssues.Count)
		if len(loop.OpenSourceIssues.Refs) > 0 {
			fmt.Printf(" %s", strings.Join(loop.OpenSourceIssues.Refs, ", "))
		}
		fmt.Println()
		renderLoopMetrics(loop.SignalsByMetric)
		renderLoopHypotheses(loop.OpenHypotheses)
		renderLoopReviews(loop.Reviews)
		if len(loop.LoopGaps) > 0 {
			fmt.Printf("- gaps: %s\n", strings.Join(loop.LoopGaps, ", "))
		} else {
			fmt.Printf("- gaps: none\n")
		}
	}
}

func renderLoopMetrics(signalsByMetric map[string][]product.LoopSignal) {
	keys := make([]string, 0, len(signalsByMetric))
	for key := range signalsByMetric {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		fmt.Printf("- metrics: none\n")
		return
	}
	fmt.Printf("- metrics:\n")
	for _, key := range keys {
		signals := signalsByMetric[key]
		if len(signals) == 0 {
			continue
		}
		latest := signals[0]
		fmt.Printf("  - %s: %.4g -> %.4g (%s, %s, %s)\n", key, latest.Before, latest.After, latest.Provider, latest.SignalID, none(latest.ObservedAt))
	}
}

func renderLoopHypotheses(hypotheses []product.LoopHypothesis) {
	if len(hypotheses) == 0 {
		fmt.Printf("- open_hypotheses: none\n")
		return
	}
	fmt.Printf("- open_hypotheses:\n")
	for _, hypothesis := range hypotheses {
		fmt.Printf("  - %s: %s -> %s\n", hypothesis.ID, hypothesis.Statement, hypothesis.ExpectedEffect)
	}
}

func renderLoopReviews(reviews []product.LoopReview) {
	if len(reviews) == 0 {
		fmt.Printf("- reviews: none\n")
		return
	}
	fmt.Printf("- reviews:\n")
	for _, review := range reviews {
		parts := []string{review.ID, review.Verdict, "hypothesis=" + review.HypothesisID}
		if review.SignalID != "" {
			parts = append(parts, "signal="+review.SignalID)
		}
		if review.ReviewedAt != "" {
			parts = append(parts, "reviewed="+review.ReviewedAt)
		}
		fmt.Printf("  - %s\n", strings.Join(parts, " "))
	}
}

func none(value string) string {
	if strings.TrimSpace(value) == "" {
		return "none"
	}
	return value
}
