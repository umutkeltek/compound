package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"compound/internal/routeevidence"
)

func tryRouteModelCapabilitiesCommand(args []string, homePath string) bool {
	if len(args) == 0 || args[0] != "model-capabilities" {
		return false
	}
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--json", "--yaml":
		case "--check-view":
			i++
			if i >= len(rest) || strings.TrimSpace(rest[i]) == "" {
				fmt.Fprintln(os.Stderr, routeModelCapabilitiesUsage)
				os.Exit(2)
			}
		default:
			fmt.Fprintln(os.Stderr, routeModelCapabilitiesUsage)
			os.Exit(2)
		}
	}

	// --check-view is a drift GATE, not a report: it exits nonzero on any model,
	// claim, default, class, or rule difference so a stale OS compatibility view
	// fails a battery instead of quietly disagreeing with Compound.
	if viewPath := strings.TrimSpace(flagValue(rest, "--check-view")); viewPath != "" {
		check, err := routeevidence.CheckModelCapabilitiesView(homePath, viewPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "route model-capabilities --check-view:", err)
			os.Exit(1)
		}
		if hasFlag(rest, "--json") {
			printJSON(check)
		} else {
			fmt.Println("# route model-capabilities --check-view")
			fmt.Printf("- authority: %s\n", check.AuthorityRef)
			fmt.Printf("- authority_revision: %s\n", check.AuthorityRevision)
			fmt.Printf("- view: %s\n", check.ViewPath)
			fmt.Printf("- view_revision: %s\n", check.ViewRevision)
			for _, drift := range check.Drifts {
				fmt.Printf("- drift: %s\n", drift)
			}
			if check.InSync {
				fmt.Println("- verdict: in_sync")
			} else {
				fmt.Printf("- verdict: drifted (%d)\n", len(check.Drifts))
			}
		}
		if !check.InSync {
			os.Exit(1)
		}
		return true
	}

	// --yaml is the deterministic projection the OS compatibility view is
	// generated from: no timestamps, no private paths, byte-stable per revision.
	if hasFlag(rest, "--yaml") {
		body, err := routeevidence.ProjectModelCapabilitiesYAML(homePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "route model-capabilities --yaml:", err)
			os.Exit(1)
		}
		os.Stdout.Write(body)
		return true
	}

	report, err := routeevidence.ProjectModelCapabilities(homePath, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "route model-capabilities:", err)
		os.Exit(1)
	}
	if hasFlag(rest, "--json") {
		printJSON(report)
		return true
	}

	fmt.Println("# route model-capabilities")
	fmt.Printf("- authority: %s\n", report.AuthorityRef)
	fmt.Printf("- authority_path: %s\n", report.AuthorityPath)
	if report.SourceAuthorityID != "" {
		fmt.Printf("- source_authority: %s\n", report.SourceAuthorityID)
	}
	fmt.Printf("- models: %d\n", report.ModelCount)
	fmt.Printf("- capability_classes: %s\n", strings.Join(report.CapabilityClasses, ","))
	fmt.Printf("- routing_defaults: %d\n", report.RoutingDefaultCount)
	for _, lane := range report.Lanes {
		fmt.Printf("- lane: %s tier=%s cost_class=%s harness=%s provider_route=%s capabilities=%d\n",
			lane.ModelID,
			emptyDash(lane.Tier),
			emptyDash(lane.CostClass),
			emptyDash(lane.Harness),
			emptyDash(lane.ProviderRoute),
			len(lane.Capabilities),
		)
	}
	fmt.Println("- verdict: read_only_projection")
	return true
}

const routeModelCapabilitiesUsage = "usage: compound route model-capabilities [--json] [--yaml] [--check-view <view.yaml>]"
