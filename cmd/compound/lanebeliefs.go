package main

import (
	"fmt"
	"os"
	"strings"

	"compound/internal/home"
	"compound/internal/lanebeliefs"
	"compound/internal/routeevidence"
)

func laneBeliefsUsage() string {
	return "usage: compound lanebeliefs <fold [--dry-run|--experimental] [--json] [--home P]|promote [--dry-run|--experimental] [--json] [--home P]|cool [--dry-run|--experimental] [--json] [--home P]|list [--json] [--home P]|posterior --lane L --class C [--json] [--home P]>"
}

func cmdLaneBeliefs(args []string) {
	if len(args) == 0 || hasUsageFlag(args) {
		fmt.Fprintln(os.Stderr, laneBeliefsUsage())
		os.Exit(2)
	}
	sub := args[0]
	jsonOut := false
	dryRun := false
	experimental := false
	homeFlag := ""
	laneFlag, classFlag := "", ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--lane", "--class":
			if sub != "posterior" || i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, laneBeliefsUsage())
				os.Exit(2)
			}
			if args[i] == "--lane" {
				laneFlag = args[i+1]
			} else {
				classFlag = args[i+1]
			}
			i++
		case "--json":
			jsonOut = true
		case "--dry-run":
			dryRun = true
		case "--experimental":
			experimental = true
		case "--home":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "lanebeliefs: --home requires a path")
				os.Exit(2)
			}
			homeFlag = args[i+1]
			i++
		default:
			fmt.Fprintln(os.Stderr, laneBeliefsUsage())
			os.Exit(2)
		}
	}
	if !dryRun && !experimental && (sub == "fold" || sub == "promote" || sub == "cool") {
		fmt.Fprintf(os.Stderr, "lanebeliefs %s: mutation is experimental; pass --experimental\n", sub)
		os.Exit(2)
	}
	homePath, err := home.Resolve(homeFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lanebeliefs:", err)
		os.Exit(1)
	}
	switch sub {
	case "fold":
		report, err := lanebeliefs.Fold(homePath, lanebeliefs.FoldOptions{DryRun: dryRun})
		if err != nil {
			fmt.Fprintln(os.Stderr, "lanebeliefs fold:", err)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(report)
			return
		}
		report.WriteText(os.Stdout)
	case "promote":
		report, err := lanebeliefs.Promote(homePath, lanebeliefs.PromoteOptions{DryRun: dryRun})
		if err != nil {
			fmt.Fprintln(os.Stderr, "lanebeliefs promote:", err)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(report)
			return
		}
		report.WriteText(os.Stdout)
	case "cool":
		report, err := lanebeliefs.Cool(homePath, lanebeliefs.CoolOptions{DryRun: dryRun})
		if err != nil {
			fmt.Fprintln(os.Stderr, "lanebeliefs cool:", err)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(report)
			return
		}
		report.WriteText(os.Stdout)
	case "list":
		report, err := lanebeliefs.List(homePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "lanebeliefs list:", err)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(report)
			return
		}
		fmt.Printf("# lanebeliefs list\n")
		fmt.Printf("- candidates: %d\n", report.CandidateCount)
		for _, candidate := range report.Candidates {
			fmt.Printf("%s %s.%s=%s state=%s\n", candidate.ID, candidate.Entity, candidate.Predicate, candidate.Object, candidate.State)
		}
	case "posterior":
		// Read-only: resolves the lane through the same capgraph door as `route ev` and prints the
		// fold's completion posterior for one (lane, class) cell. It mints and writes nothing.
		laneRef, class := strings.TrimSpace(laneFlag), strings.TrimSpace(classFlag)
		if laneRef == "" || class == "" {
			fmt.Fprintln(os.Stderr, "lanebeliefs posterior: --lane and --class are required")
			fmt.Fprintln(os.Stderr, laneBeliefsUsage())
			os.Exit(2)
		}
		// "unknown" is the fold's own bucket for receipts that declared no class, so it is readable
		// here even though no close may declare it.
		if class != "unknown" {
			if err := routeevidence.CheckKnownCapabilityClass(homePath, "--class", class); err != nil {
				fmt.Fprintln(os.Stderr, "lanebeliefs posterior:", err)
				os.Exit(2)
			}
		}
		nodeID, resolved, err := resolveEvLaneNodeID(homePath, laneRef)
		if err != nil {
			fmt.Fprintln(os.Stderr, "lanebeliefs posterior:", err)
			os.Exit(1)
		}
		if !resolved {
			fmt.Fprintf(os.Stderr, "lanebeliefs posterior: --lane %q does not resolve to an imported agent-lane node; pass an agent-lane node id or a short lane id (e.g. provider/model)\n", laneRef)
			os.Exit(2)
		}
		report, err := lanebeliefs.CompletionPosterior(homePath, nodeID, class)
		if err != nil {
			fmt.Fprintln(os.Stderr, "lanebeliefs posterior:", err)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(report)
			return
		}
		report.WriteText(os.Stdout)
	default:
		fmt.Fprintln(os.Stderr, laneBeliefsUsage())
		os.Exit(2)
	}
}
