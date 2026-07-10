package main

import (
	"fmt"
	"os"

	"compound/internal/home"
	"compound/internal/negatives"
)

func negativesUsage() string {
	return "usage: compound negatives <build [--json] [--home P]|list [--json] [--home P]>"
}

// cmdNegatives is the rebuild/inspect door onto the negatives projection
// (internal/negatives — inbox/failure-patterns + reviews/route-remorse joined into
// a scope-keyed "already tried and failed" view). It mirrors `compound memory
// temperature`'s build/read shape: `build` rebuilds from scratch and persists under
// projections/negatives.json (never the ledger); `list` reads the persisted
// projection without touching either producer. Orient (internal/contract) reads the
// persisted file directly — this command is the operator/cron door that keeps it
// fresh, not something orient itself calls inline.
func cmdNegatives(args []string) {
	if len(args) == 0 || hasUsageFlag(args) {
		fmt.Fprintln(os.Stderr, negativesUsage())
		os.Exit(2)
	}
	sub := args[0]
	jsonOut := false
	homeFlag := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--home":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "negatives: --home requires a path")
				os.Exit(2)
			}
			homeFlag = args[i+1]
			i++
		default:
			fmt.Fprintln(os.Stderr, negativesUsage())
			os.Exit(2)
		}
	}
	homePath, err := home.Resolve(homeFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "negatives:", err)
		os.Exit(1)
	}
	switch sub {
	case "build":
		proj, err := negatives.Build(homePath, negatives.BuildOptions{})
		if err != nil {
			fmt.Fprintln(os.Stderr, "negatives build:", err)
			os.Exit(1)
		}
		path, err := negatives.WriteProjection(homePath, proj)
		if err != nil {
			fmt.Fprintln(os.Stderr, "negatives build:", err)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(proj)
			return
		}
		fmt.Println("# negatives build")
		fmt.Printf("- wrote: %s\n", path)
		printNegativesSummary(proj)
	case "list":
		proj, ok, err := negatives.ReadProjection(homePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "negatives list:", err)
			os.Exit(1)
		}
		if !ok {
			if jsonOut {
				printJSON(negatives.Projection{
					SchemaVersion: negatives.SchemaVersion,
					ProjectionRef: negatives.ProjectionRef,
					Records:       []negatives.Record{},
				})
				return
			}
			fmt.Println("# negatives list")
			fmt.Println("- projection not built yet; run `compound negatives build`")
			return
		}
		if jsonOut {
			printJSON(proj)
			return
		}
		fmt.Println("# negatives list")
		printNegativesSummary(proj)
	default:
		fmt.Fprintln(os.Stderr, negativesUsage())
		os.Exit(2)
	}
}

func printNegativesSummary(proj negatives.Projection) {
	fmt.Printf("- computed_at: %s\n", proj.ComputedAt)
	fmt.Printf("- records: %d\n", len(proj.Records))
	for _, r := range proj.Records {
		fmt.Printf("  %s  %s  approach=%s ref=%s\n", r.Source, r.RefID, r.ApproachKey, r.Ref)
	}
}
