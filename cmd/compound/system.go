package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"compound/internal/home"
	"compound/internal/mem"
	"compound/internal/product"
	"compound/internal/projection"
)

const systemUsage = "usage: compound system <status|map|stats|memory-graph|lineage|status-surface|inventory|graph|depends|impact> [--json]"

func cmdSystem(args []string) {
	if len(args) == 0 || hasFlag(args, "--help") || hasFlag(args, "-h") {
		fmt.Fprintln(os.Stderr, systemUsage)
		if len(args) == 0 {
			os.Exit(2)
		}
		return
	}
	switch args[0] {
	case "status":
		if len(args) > 2 || (len(args) == 2 && args[1] != "--json") {
			fmt.Fprintln(os.Stderr, "usage: compound system status [--json]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		report, err := projection.BuildSystemStatus(homePath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "system status:", err)
			os.Exit(1)
		}
		if len(args) == 2 && args[1] == "--json" {
			printJSON(report)
			return
		}
		fmt.Printf("# system status\n\n")
		fmt.Printf("- verdict: %s\n", report.Verdict)
		fmt.Printf("- workbench: open=%d needs_claim=%d needs_review=%d in_progress=%d (%s)\n",
			report.Sections.Workbench.Open, report.Sections.Workbench.NeedsClaim,
			report.Sections.Workbench.NeedsReview, report.Sections.Workbench.InProgress,
			report.Sections.Workbench.SourceRef)
		fmt.Printf("- job_roster: jobs=%d enabled=%d active_errors=%d (%s)\n",
			report.Sections.JobRoster.JobCount, report.Sections.JobRoster.EnabledCount,
			report.Sections.JobRoster.ActiveErrorCount, report.Sections.JobRoster.SourceRef)
		fmt.Printf("- route_evidence: rows=%d tokens=%d (%s)\n",
			report.Sections.RouteEvidence.RouteEvidenceCount, report.Sections.RouteEvidence.TotalTokens,
			report.Sections.RouteEvidence.SourceRef)
		fmt.Printf("- product_authority: products=%d signals=%d reviews=%d (%s)\n",
			report.Sections.Product.ProductCount, report.Sections.Product.SignalCount,
			report.Sections.Product.ReviewCount, report.Sections.Product.SourceRef)
		fmt.Printf("- capability_authority: nodes=%d edges=%d (%s)\n",
			report.Sections.Capability.NodeCount, report.Sections.Capability.EdgeCount,
			report.Sections.Capability.SourceRef)
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
	case "status-surface":
		cmdSystemStatusSurface(args[1:])
	case "inventory":
		cmdSystemInventory(args[1:])
	case "graph", "depends", "impact":
		cmdSystemGraph(args[0], args[1:])
	default:
		fmt.Fprintln(os.Stderr, systemUsage)
		os.Exit(2)
	}
}

const systemGraphUsage = "usage: compound system graph [--json] [--kind K] [--rel R]\n" +
	"       compound system depends <id | unique label | product id> [--depth N (default 6)] [--all] [--json]\n" +
	"       compound system impact <id | unique label | product id> [--all] [--json]"

// cmdSystemGraph is the thin CLI face of the system graph over the inventory
// snapshot (internal/projection/inventory_graph*.go). It makes no external
// call. An ambiguous target lists its candidates and exits 2; a target that
// names nothing, or a missing snapshot, exits 1.
func cmdSystemGraph(verb string, args []string) {
	var jsonOut bool
	var textOpts projection.InventoryTextOptions
	var target, kind, rel string
	depth := projection.DefaultInventoryDependsDepth
	for i := 0; i < len(args); i++ {
		flag := args[i]
		valued := (flag == "--kind" || flag == "--rel") && verb == "graph" || flag == "--depth" && verb == "depends"
		switch {
		case flag == "--json":
			jsonOut = true
		case flag == "--all" && verb != "graph":
			textOpts.All = true
		case valued && (i+1 >= len(args) || strings.TrimSpace(args[i+1]) == ""):
			fmt.Fprintln(os.Stderr, systemGraphUsage)
			os.Exit(2)
		case valued && flag == "--depth":
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				fmt.Fprintln(os.Stderr, "system depends: --depth must be a positive integer")
				os.Exit(2)
			}
			depth = n
		case valued && flag == "--kind":
			i++
			kind = args[i]
		case valued:
			i++
			rel = args[i]
		case verb != "graph" && target == "" && !strings.HasPrefix(flag, "--") && strings.TrimSpace(flag) != "":
			target = flag
		default:
			fmt.Fprintln(os.Stderr, systemGraphUsage)
			os.Exit(2)
		}
	}
	if verb != "graph" && target == "" {
		fmt.Fprintln(os.Stderr, systemGraphUsage)
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	products, now := systemProductResolver(homePath), time.Now()
	if verb == "graph" {
		report, err := projection.BuildInventoryGraph(homePath, projection.InventoryGraphQuery{Kind: kind, Rel: rel, Products: products}, now)
		if err != nil {
			fmt.Fprintln(os.Stderr, "system graph:", err)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(report)
		} else {
			fmt.Print(projection.FormatInventoryGraphText(report))
		}
		return
	}
	var report projection.InventoryWalkProjection
	if verb == "depends" {
		report, err = projection.BuildInventoryDepends(homePath, projection.InventoryDependsQuery{Target: target, Depth: depth, Products: products}, now)
	} else {
		report, err = projection.BuildInventoryImpact(homePath, target, products, now)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "system "+verb+":", err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(report)
	} else {
		fmt.Print(projection.FormatInventoryWalkTextWith(report, textOpts))
	}
	switch report.Status {
	case projection.InventoryWalkAmbiguous:
		os.Exit(2)
	case projection.InventoryWalkNotFound, projection.InventoryWalkMissing:
		os.Exit(1)
	}
}

// systemProductResolver resolves inventory product references through the
// owner of product identity (product.WhereAll / product.Where: separators,
// renames, aliases and app identifiers fold to one product), read without
// scaffolding the home. A home with no product ledger resolves nothing, and
// the graph then shows every product reference as an unknown product.
func systemProductResolver(homePath string) projection.InventoryProductResolver {
	var store *mem.Store
	var members map[string]string
	return func(ref string) (string, bool) {
		if members == nil {
			members = map[string]string{}
			if s, err := mem.OpenScan(homePath); err == nil {
				store = s
				all, _ := product.WhereAll(store)
				for _, r := range all {
					for _, m := range r.Members {
						members[m] = r.ProductID
					}
				}
			}
		}
		if id, ok := members[ref]; ok {
			return id, true
		}
		if store == nil {
			return "", false
		}
		r, err := product.Where(store, strings.TrimPrefix(ref, projection.InventoryProductPrefix))
		if err != nil || r.Status == product.WhereUnresolved || r.Status == product.WhereAmbiguous || r.ProductID == "" {
			return "", false
		}
		return r.ProductID, true
	}
}

const systemInventoryUsage = "usage: compound system inventory [--json] [--questions [--all]] [--kind K] [--provider P] [--stale-hours N]\n" +
	"       compound system inventory refresh --collector-json FILE [--allow-older] [--carry-days N (default 7)] [--json]"

// cmdSystemInventory is the thin CLI face of the read-only system inventory
// (internal/projection/inventory*.go). It makes no external call.
func cmdSystemInventory(args []string) {
	if len(args) > 0 && args[0] == "refresh" {
		cmdSystemInventoryRefresh(args[1:])
		return
	}
	query := projection.InventoryQuery{StaleHours: projection.DefaultInventoryStaleHours}
	var jsonOut, questionsOut bool
	var textOpts projection.InventoryTextOptions
	for i := 0; i < len(args); i++ {
		switch flag := args[i]; flag {
		case "--json":
			jsonOut = true
		case "--questions":
			questionsOut = true
		case "--all":
			textOpts.All = true
		case "--kind", "--provider", "--stale-hours":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				fmt.Fprintln(os.Stderr, systemInventoryUsage)
				os.Exit(2)
			}
			i++
			switch flag {
			case "--kind":
				query.Kind = args[i]
			case "--provider":
				query.Provider = args[i]
			default:
				hours, err := strconv.ParseFloat(args[i], 64)
				if err != nil || hours <= 0 {
					fmt.Fprintln(os.Stderr, "system inventory: --stale-hours must be a positive number")
					os.Exit(2)
				}
				query.StaleHours = hours
			}
		default:
			fmt.Fprintln(os.Stderr, systemInventoryUsage)
			os.Exit(2)
		}
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	query.Products = systemProductResolver(homePath)
	report, err := projection.BuildInventory(homePath, query, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "system inventory:", err)
		os.Exit(1)
	}
	switch {
	case jsonOut:
		printJSON(report)
	case questionsOut:
		fmt.Print(projection.FormatInventoryQuestionsTextWith(report, textOpts))
	default:
		fmt.Print(projection.FormatInventoryText(report))
	}
}

func cmdSystemInventoryRefresh(args []string) {
	var file string
	var jsonOut bool
	var opts projection.InventoryRefreshOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--allow-older":
			opts.AllowOlder = true
		case "--collector-json":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				fmt.Fprintln(os.Stderr, systemInventoryUsage)
				os.Exit(2)
			}
			file = args[i+1]
			i++
		case "--carry-days":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, systemInventoryUsage)
				os.Exit(2)
			}
			days, err := strconv.ParseFloat(args[i+1], 64)
			if err != nil || days <= 0 {
				fmt.Fprintln(os.Stderr, "system inventory refresh: --carry-days must be a positive number")
				os.Exit(2)
			}
			opts.CarryMaxAge = time.Duration(days * 24 * float64(time.Hour))
			i++
		default:
			fmt.Fprintln(os.Stderr, systemInventoryUsage)
			os.Exit(2)
		}
	}
	if file == "" {
		fmt.Fprintln(os.Stderr, systemInventoryUsage)
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	result, err := projection.RefreshInventoryFile(homePath, file, opts, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "system inventory refresh:", err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(result)
		return
	}
	fmt.Print(projection.FormatInventoryRefresh(result))
}
