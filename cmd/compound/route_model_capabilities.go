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
	for _, arg := range args[1:] {
		if arg != "--json" {
			fmt.Fprintln(os.Stderr, "usage: compound route model-capabilities [--json]")
			os.Exit(2)
		}
	}
	report, err := routeevidence.ProjectModelCapabilities(homePath, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "route model-capabilities:", err)
		os.Exit(1)
	}
	if hasFlag(args[1:], "--json") {
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
