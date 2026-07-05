package main

import (
	"fmt"
	"os"
	"time"

	"compound/internal/home"
	"compound/internal/projection"
)

func cmdSystem(args []string) {
	if len(args) == 0 || hasFlag(args, "--help") || hasFlag(args, "-h") {
		fmt.Fprintln(os.Stderr, "usage: compound system map [--json]")
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
	default:
		fmt.Fprintln(os.Stderr, "usage: compound system map [--json]")
		os.Exit(2)
	}
}
