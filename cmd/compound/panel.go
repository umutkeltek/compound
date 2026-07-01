package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"compound/internal/home"
	"compound/internal/panel"
)

func cmdPanel(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound panel <import-lenses <manifest>|lenses --target-kind K [--json]|plan --target-kind K [--target REF]... [--state S]... [--limit N] [--privacy-sensitive|--not-privacy-sensitive REASON] [--max-items N] [--max-tokens N] [--max-usd-micros N] [--json]|run --plan <run_id> --fixtures <dir> [--json]|show <run_id> [--json]>")
		os.Exit(2)
	}
	switch args[0] {
	case "import-lenses":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound panel import-lenses <manifest>")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		die(err)
		res, err := panel.ImportLensManifest(homePath, args[1], time.Now())
		die(err)
		fmt.Printf("# panel lens import %s\n", shortPanelHash(res.ManifestSHA256))
		fmt.Printf("- imported: %d\n", res.Imported)
		fmt.Printf("- nodes_changed: %d\n", res.NodesChanged)
		fmt.Printf("- prompts_changed: %d\n", res.PromptsChanged)
		fmt.Printf("- authority_appended: %t\n", res.AuthorityAppended)
	case "lenses":
		targetKind, jsonOut := "", false
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--target-kind":
				if i+1 >= len(args) {
					fmt.Fprintln(os.Stderr, "usage: compound panel lenses --target-kind K [--json]")
					os.Exit(2)
				}
				targetKind = args[i+1]
				i++
			case "--json":
				jsonOut = true
			default:
				fmt.Fprintf(os.Stderr, "unknown panel lenses arg %q\n", args[i])
				os.Exit(2)
			}
		}
		if strings.TrimSpace(targetKind) == "" {
			fmt.Fprintln(os.Stderr, "usage: compound panel lenses --target-kind K [--json]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		die(err)
		selection, err := panel.SelectLenses(homePath, targetKind)
		die(err)
		if jsonOut {
			printJSON(selection)
			return
		}
		fmt.Printf("# panel lenses %s\n", targetKind)
		fmt.Printf("- included: %d\n", len(selection.Included))
		for _, lens := range selection.Included {
			fmt.Printf("- lens: %s weight=%.2f prompt_sha256=%s\n", lens.NodeID, lens.Weight, shortPanelHash(lens.PromptSHA256))
		}
		fmt.Printf("- excluded: %d\n", len(selection.Excluded))
		for _, lens := range selection.Excluded {
			fmt.Printf("- excluded: %s reason=%s\n", lens.Ref, lens.Reason)
		}
	case "plan":
		in, jsonOut := parsePanelPlanArgs(args[1:])
		homePath, err := home.Resolve("")
		die(err)
		run, err := panel.BuildPlan(homePath, in)
		if err != nil {
			var blocked panel.ErrBlockedNoLane
			if errors.As(err, &blocked) && run.ID != "" {
				if jsonOut {
					printJSON(run)
				} else {
					printPanelRun(run)
				}
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			die(err)
		}
		if jsonOut {
			printJSON(run)
			return
		}
		printPanelRun(run)
	case "run":
		planID, fixtures, jsonOut := parsePanelRunArgs(args[1:])
		if strings.TrimSpace(fixtures) == "" {
			fmt.Fprintln(os.Stderr, "no caller configured (live gateway lands in a later slice)")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		die(err)
		store, err := panel.Open(homePath)
		die(err)
		run, err := store.LoadRun(planID)
		die(err)
		run, err = panel.ExecuteDryRun(context.Background(), homePath, run, panel.FixtureCaller{Dir: fixtures}, time.Now)
		if err != nil {
			var blocked panel.ErrBlockedNoLane
			if errors.As(err, &blocked) {
				if jsonOut {
					printJSON(run)
				} else {
					printPanelRun(run)
				}
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			die(err)
		}
		if jsonOut {
			printJSON(run)
			return
		}
		printPanelRun(run)
	case "show":
		if len(args) != 2 && !(len(args) == 3 && args[2] == "--json") {
			fmt.Fprintln(os.Stderr, "usage: compound panel show <run_id> [--json]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		die(err)
		store, err := panel.Open(homePath)
		die(err)
		run, err := store.LoadRun(args[1])
		die(err)
		if len(args) == 3 && args[2] == "--json" {
			printJSON(run)
			return
		}
		printPanelRun(run)
	default:
		fmt.Fprintf(os.Stderr, "unknown panel command %q\n", args[0])
		os.Exit(2)
	}
}

func parsePanelPlanArgs(args []string) (panel.PlanInput, bool) {
	var in panel.PlanInput
	jsonOut := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--target-kind":
			in.TargetKind = requirePanelValue(args, &i, "panel plan")
		case "--target":
			in.TargetRefs = append(in.TargetRefs, requirePanelValue(args, &i, "panel plan"))
		case "--state":
			in.States = append(in.States, requirePanelValue(args, &i, "panel plan"))
		case "--limit":
			in.Limit = parsePanelNonNegative(requirePanelValue(args, &i, "panel plan"), "--limit")
		case "--privacy-sensitive":
			in.PrivacySensitive = true
			in.PrivacyOverrideReason = ""
		case "--not-privacy-sensitive":
			in.PrivacySensitive = false
			in.PrivacyOverrideReason = requirePanelValue(args, &i, "panel plan")
		case "--max-items":
			in.Budget.MaxItems = parsePanelNonNegative(requirePanelValue(args, &i, "panel plan"), "--max-items")
		case "--max-tokens":
			in.Budget.MaxTotalTokens = parsePanelNonNegative(requirePanelValue(args, &i, "panel plan"), "--max-tokens")
		case "--max-usd-micros":
			in.Budget.MaxUSDMicros = int64(parsePanelNonNegative(requirePanelValue(args, &i, "panel plan"), "--max-usd-micros"))
		case "--json":
			jsonOut = true
		default:
			fmt.Fprintf(os.Stderr, "unknown panel plan arg %q\n", args[i])
			os.Exit(2)
		}
	}
	if strings.TrimSpace(in.TargetKind) == "" {
		fmt.Fprintln(os.Stderr, "usage: compound panel plan --target-kind K [--target REF]... [--state S]... [--limit N] [--privacy-sensitive|--not-privacy-sensitive REASON] [--max-items N] [--max-tokens N] [--max-usd-micros N] [--json]")
		os.Exit(2)
	}
	return in, jsonOut
}

func parsePanelRunArgs(args []string) (string, string, bool) {
	planID, fixtures, jsonOut := "", "", false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--plan":
			planID = requirePanelValue(args, &i, "panel run")
		case "--fixtures":
			fixtures = requirePanelValue(args, &i, "panel run")
		case "--json":
			jsonOut = true
		default:
			fmt.Fprintf(os.Stderr, "unknown panel run arg %q\n", args[i])
			os.Exit(2)
		}
	}
	if strings.TrimSpace(planID) == "" {
		fmt.Fprintln(os.Stderr, "usage: compound panel run --plan <run_id> --fixtures <dir> [--json]")
		os.Exit(2)
	}
	return planID, fixtures, jsonOut
}

func requirePanelValue(args []string, i *int, command string) string {
	if *i+1 >= len(args) {
		fmt.Fprintf(os.Stderr, "usage: compound %s: %s requires a value\n", command, args[*i])
		os.Exit(2)
	}
	*i = *i + 1
	return args[*i]
}

func parsePanelNonNegative(raw, name string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		fmt.Fprintf(os.Stderr, "panel plan: %s must be a non-negative integer\n", name)
		os.Exit(2)
	}
	return n
}

func printPanelRun(run panel.Run) {
	fmt.Printf("# panel run %s\n", run.ID)
	fmt.Printf("- status: %s\n", run.Status)
	if run.StoppedReason != "" {
		fmt.Printf("- stopped_reason: %s\n", run.StoppedReason)
	}
	fmt.Printf("- target_kind: %s\n", run.TargetKind)
	fmt.Printf("- targets: %d\n", len(run.Targets))
	fmt.Printf("- findings: %d\n", len(run.Findings))
	printPanelLaneSummary(run.Route)
	for _, finding := range run.Findings {
		decision := finding.Synthesis.Decision
		if decision == "" {
			decision = "skipped"
		}
		fmt.Printf("- target: %s decision=%s confidence=%.2f\n", finding.TargetRef, decision, finding.Synthesis.Confidence)
		if len(finding.Synthesis.GuardrailsApplied) > 0 {
			fmt.Printf("  guardrails: %s\n", strings.Join(finding.Synthesis.GuardrailsApplied, ","))
		}
		if finding.VerdictArtifact != "" {
			fmt.Printf("  verdict: %s\n", finding.VerdictArtifact)
		}
	}
}

func printPanelLaneSummary(route panel.RoutePlan) {
	fmt.Printf("- route_authority: %s\n", route.AuthorityRef)
	if route.LensPlanID != "" {
		fmt.Printf("- lens_plan: %s lane=%s tier=%s role=%s\n", route.LensPlanID, route.LensLane.ModelID, route.LensLane.Tier, route.LensLane.GateRole)
	}
	if route.JudgePlanID != "" {
		fmt.Printf("- judge_plan: %s lane=%s tier=%s role=%s\n", route.JudgePlanID, route.JudgeLane.ModelID, route.JudgeLane.Tier, route.JudgeLane.GateRole)
	}
	if len(route.OmittedLanes) > 0 {
		fmt.Printf("- omitted_lanes: %d\n", len(route.OmittedLanes))
		for _, lane := range route.OmittedLanes {
			fmt.Printf("- omitted: %s reason=%s\n", lane.ModelID, lane.Reason)
		}
	}
}

func shortPanelHash(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}
