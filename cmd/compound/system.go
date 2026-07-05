package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"compound/internal/home"
	"compound/internal/projection"
)

func cmdSystem(args []string) {
	if len(args) == 0 || hasFlag(args, "--help") || hasFlag(args, "-h") {
		fmt.Fprintln(os.Stderr, "usage: compound system <map|stats|memory-graph|lineage> [--json]")
		if len(args) == 0 {
			os.Exit(2)
		}
		return
	}
	switch args[0] {
	case "map":
		if len(args) > 2 || (len(args) == 2 && args[1] != "--json") {
			fmt.Fprintln(os.Stderr, "usage: compound system map [--json]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		repoPath, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "system map:", err)
			os.Exit(1)
		}
		report, err := projection.BuildSystemMap(homePath, repoPath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "system map:", err)
			os.Exit(1)
		}
		if len(args) == 2 && args[1] == "--json" {
			printJSON(report)
			return
		}
		fmt.Printf("# system map\n\n")
		fmt.Printf("- nodes: %d\n", len(report.Nodes))
		fmt.Printf("- edges: %d\n", len(report.Edges))
		for _, node := range report.Nodes {
			fmt.Printf("- node: %s lane=%s status=%s projection=%s volume=%s\n", node.ID, node.Lane, node.Status, node.Projection, node.Volume)
		}
	case "stats":
		if len(args) > 2 || (len(args) == 2 && args[1] != "--json") {
			fmt.Fprintln(os.Stderr, "usage: compound system stats [--json]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		report := projection.BuildSystemStats(homePath, time.Now())
		if len(args) == 2 && args[1] == "--json" {
			printJSON(report)
			return
		}
		fmt.Printf("# system stats\n\n")
		fmt.Printf("- claims: %d\n", report.Claims)
		fmt.Printf("- entities: %d\n", report.Entities)
		fmt.Printf("- evidence: %d\n", report.Evidence)
		fmt.Printf("- route_evidence: %d\n", report.RouteEvidence)
		fmt.Printf("- knowledge_sources: %d\n", report.KnowledgeSources)
		fmt.Printf("- intake_repos: %d\n", report.IntakeRepos)
	case "memory-graph":
		if len(args) < 2 || len(args) > 5 {
			fmt.Fprintln(os.Stderr, "usage: compound system memory-graph <seed> [--max-nodes N] [--json]")
			os.Exit(2)
		}
		maxNodes := 60
		jsonOut := false
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--json":
				jsonOut = true
			case "--max-nodes":
				if i+1 >= len(args) {
					fmt.Fprintln(os.Stderr, "usage: compound system memory-graph <seed> [--max-nodes N] [--json]")
					os.Exit(2)
				}
				n, err := strconv.Atoi(args[i+1])
				if err != nil || n <= 0 {
					fmt.Fprintln(os.Stderr, "system memory-graph: --max-nodes must be a positive integer")
					os.Exit(2)
				}
				maxNodes = n
				i++
			default:
				fmt.Fprintln(os.Stderr, "usage: compound system memory-graph <seed> [--max-nodes N] [--json]")
				os.Exit(2)
			}
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		report, err := projection.BuildMemoryEgoGraph(homePath, args[1], maxNodes)
		if err != nil {
			fmt.Fprintln(os.Stderr, "system memory-graph:", err)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(report)
			return
		}
		fmt.Printf("# system memory-graph\n\n")
		fmt.Printf("- seed: %s\n", report.Seed)
		fmt.Printf("- resolved: %s\n", report.ResolvedAs)
		fmt.Printf("- nodes: %d\n", len(report.Nodes))
		fmt.Printf("- links: %d\n", len(report.Links))
	case "lineage":
		if len(args) < 2 || len(args) > 3 || (len(args) == 3 && args[2] != "--json") {
			fmt.Fprintln(os.Stderr, "usage: compound system lineage <id> [--json]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		report, err := projection.BuildLineage(homePath, args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "system lineage:", err)
			os.Exit(1)
		}
		if len(args) == 3 && args[2] == "--json" {
			printJSON(report)
			return
		}
		fmt.Printf("# system lineage\n\n")
		fmt.Printf("- query: %s\n", report.QueryID)
		fmt.Printf("- resolved: %s\n", report.ResolvedAs)
		fmt.Printf("- found: %t\n", report.Found)
		fmt.Printf("- steps: %d\n", len(report.Steps))
	default:
		fmt.Fprintln(os.Stderr, "usage: compound system <map|stats|memory-graph|lineage> [--json]")
		os.Exit(2)
	}
}
