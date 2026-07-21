package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"compound/internal/mem"
	"compound/internal/product"
)

// tryProductPostHogSignal handles `product signal --posthog-report <json>`. It is
// the distribution loop's measurement ingest: a raw PostHog report export becomes
// ProductSignals (with a real before -> after delta) that join the actuator-minted
// distribution hypotheses and close the loop. Mirrors the Google report path.
func tryProductPostHogSignal(args []string, homePath string, store *mem.Store) bool {
	if !hasPostHogReportFlag(args) {
		return false
	}
	docs, sourcePath, err := loadPostHogSignalArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "product signal:", err)
		os.Exit(1)
	}
	now := time.Now()
	var reports []product.Report
	for _, doc := range docs {
		report, err := product.ApplySignal(homePath, store, doc, sourcePath, now)
		if err != nil {
			fmt.Fprintln(os.Stderr, "product signal:", err)
			os.Exit(1)
		}
		reports = append(reports, report)
	}
	productID := ""
	if len(docs) > 0 {
		productID = docs[0].Signal.ProductID
	}
	fmt.Printf("# product signals posthog\n\n")
	fmt.Printf("- status: ok\n")
	fmt.Printf("- product: %s\n", productID)
	fmt.Printf("- source: %s\n", sourcePath)
	fmt.Printf("- signals: %d\n", len(reports))
	for i, report := range reports {
		fmt.Printf("- signal: %s", report.SignalIDs[0])
		if i < len(docs) {
			fmt.Printf(" %s before=%.4g after=%.4g", docs[i].Signal.MetricName, docs[i].Signal.Before, docs[i].Signal.After)
		}
		if len(report.TransitionIDs) > 0 {
			fmt.Printf(" transition:%s", report.TransitionIDs[0])
		}
		fmt.Println()
	}
	return true
}

func hasPostHogReportFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--posthog-report" {
			return true
		}
	}
	return false
}

func loadPostHogSignalArgs(args []string) ([]product.SignalDocument, string, error) {
	var report string
	opts := product.PostHogReportSignalOptions{}
	for i := 0; i < len(args); i++ {
		if i+1 >= len(args) {
			return nil, "", fmt.Errorf("%s", productUsage())
		}
		value := args[i+1]
		switch args[i] {
		case "--posthog-report":
			report = value
		case "--product":
			opts.ProductID = value
		case "--project":
			opts.Project = value
		case "--metric":
			opts.Metric = value
		default:
			return nil, "", fmt.Errorf("%s", productUsage())
		}
		i++
	}
	if strings.TrimSpace(report) == "" {
		return nil, "", fmt.Errorf("%s", productUsage())
	}
	if strings.TrimSpace(opts.ProductID) == "" || strings.TrimSpace(opts.Project) == "" {
		return nil, "", fmt.Errorf("%s", productUsage())
	}
	docs, err := product.LoadPostHogReportSignals(report, opts)
	return docs, report, err
}
