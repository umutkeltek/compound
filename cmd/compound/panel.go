package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"compound/internal/home"
	"compound/internal/panel"
)

func cmdPanel(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound panel <import-lenses <manifest>|lenses --target-kind K [--json]|plan --target-kind K [--target REF]... [--state S]... [--limit N] [--privacy-sensitive|--not-privacy-sensitive REASON] [--supervised|--unsupervised] [--max-items N] [--max-tokens N] [--max-usd-micros N] [--json]|run --plan <run_id> (--fixtures <dir>|--openai-compatible-url U|--apply) [--api-key-env ENV] [--model-override lane=model]... [--json]|verdict-record --review <panel_verdict.json> --rationale <text> --reviewed-by <actor> [--json]|show <run_id> [--json]>")
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
				i++
				targetKind = args[i]
			case "--json":
				jsonOut = true
			default:
				fmt.Fprintf(os.Stderr, "unknown panel lenses arg %q\n", args[i])
				os.Exit(2)
			}
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
		for _, lens := range selection.Included {
			fmt.Printf("- include: %s weight=%.2f prompt=%s\n", lens.NodeID, lens.Weight, lens.PromptSHA256)
		}
		for _, lens := range selection.Excluded {
			fmt.Printf("- exclude: %s reason=%s\n", lens.Ref, lens.Reason)
		}
	case "plan":
		in, jsonOut := parsePanelPlanArgs(args[1:])
		homePath, err := home.Resolve("")
		die(err)
		run, err := panel.BuildPlan(homePath, in, time.Now)
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
		parsed := parsePanelRunArgs(args[1:])
		homePath, err := home.Resolve("")
		die(err)
		store, err := panel.Open(homePath)
		die(err)
		run, err := store.LoadRun(parsed.planID)
		die(err)
		if parsed.apply {
			run, err = panel.Apply(homePath, run, time.Now)
			die(err)
		} else {
			caller, evidenceRefs := panelRunCaller(parsed)
			run.EvidenceRefs = appendPanelEvidenceRefs(run.EvidenceRefs, evidenceRefs...)
			run, err = panel.ExecuteDryRun(context.Background(), homePath, run, caller, time.Now)
			if err != nil {
				var blocked panel.ErrBlockedNoLane
				if errors.As(err, &blocked) {
					if parsed.jsonOut {
						printJSON(run)
					} else {
						printPanelRun(run)
					}
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
				die(err)
			}
		}
		if parsed.jsonOut {
			printJSON(run)
			return
		}
		printPanelRun(run)
	case "verdict-record":
		reviewPath, input, jsonOut := parsePanelVerdictRecordArgs(args[1:])
		homePath, err := home.Resolve("")
		die(err)
		report, err := panel.RecordVerdictHumanReview(homePath, reviewPath, input, time.Now())
		die(err)
		if jsonOut {
			printJSON(report)
			return
		}
		fmt.Printf("# panel verdict-record %s\n", report.ID)
		fmt.Printf("- verdict: %s\n", report.VerdictID)
		fmt.Printf("- decision: %s\n", report.Decision)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- reviewed_by: %s\n", report.ReviewedBy)
		fmt.Printf("- review: %s\n", report.ReviewPath)
		fmt.Printf("- next_action: %s\n", report.NextAction)
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

func parsePanelVerdictRecordArgs(args []string) (reviewPath string, input panel.VerdictHumanReviewInput, jsonOut bool) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--review":
			reviewPath = requirePanelValue(args, &i, "panel verdict-record")
		case "--rationale":
			input.Rationale = requirePanelValue(args, &i, "panel verdict-record")
		case "--reviewed-by", "--actor":
			input.ReviewedBy = requirePanelValue(args, &i, "panel verdict-record")
		case "--json":
			jsonOut = true
		default:
			fmt.Fprintln(os.Stderr, "usage: compound panel verdict-record --review <panel_verdict.json> --rationale <text> --reviewed-by <actor> [--json]")
			os.Exit(2)
		}
	}
	if strings.TrimSpace(reviewPath) == "" || strings.TrimSpace(input.Rationale) == "" || strings.TrimSpace(input.ReviewedBy) == "" {
		fmt.Fprintln(os.Stderr, "usage: compound panel verdict-record --review <panel_verdict.json> --rationale <text> --reviewed-by <actor> [--json]")
		os.Exit(2)
	}
	return reviewPath, input, jsonOut
}

func parsePanelPlanArgs(args []string) (panel.PlanInput, bool) {
	in := panel.PlanInput{Supervised: true}
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
		case "--supervised":
			in.Supervised = true
			in.Unsupervised = false
		case "--unsupervised":
			in.Supervised = false
			in.Unsupervised = true
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
		fmt.Fprintln(os.Stderr, "usage: compound panel plan --target-kind K [--target REF]... [--state S]... [--limit N] [--privacy-sensitive|--not-privacy-sensitive REASON] [--supervised|--unsupervised] [--max-items N] [--max-tokens N] [--max-usd-micros N] [--json]")
		os.Exit(2)
	}
	return in, jsonOut
}

type panelRunArgs struct {
	planID              string
	fixtures            string
	apply               bool
	jsonOut             bool
	openAICompatibleURL string
	apiKeyEnv           string
	modelOverrides      map[string]string
	gatewayFlags        bool
}

func parsePanelRunArgs(args []string) panelRunArgs {
	var parsed panelRunArgs
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--plan":
			parsed.planID = requirePanelValue(args, &i, "panel run")
		case "--fixtures":
			parsed.fixtures = requirePanelValue(args, &i, "panel run")
		case "--openai-compatible-url":
			parsed.openAICompatibleURL = requirePanelValue(args, &i, "panel run")
			parsed.gatewayFlags = true
		case "--api-key-env":
			parsed.apiKeyEnv = requirePanelValue(args, &i, "panel run")
			parsed.gatewayFlags = true
		case "--model-override":
			lane, model := parsePanelModelOverride(requirePanelValue(args, &i, "panel run"))
			if parsed.modelOverrides == nil {
				parsed.modelOverrides = map[string]string{}
			}
			parsed.modelOverrides[lane] = model
			parsed.gatewayFlags = true
		case "--apply":
			parsed.apply = true
		case "--json":
			parsed.jsonOut = true
		default:
			fmt.Fprintf(os.Stderr, "unknown panel run arg %q", args[i])
			fmt.Fprintln(os.Stderr)
			os.Exit(2)
		}
	}
	fixtureSet := strings.TrimSpace(parsed.fixtures) != ""
	effectiveBaseURL := firstNonEmptyString(parsed.openAICompatibleURL, os.Getenv("COMPOUND_PANEL_BASE_URL"))
	if strings.TrimSpace(parsed.planID) == "" || (!parsed.apply && !fixtureSet && strings.TrimSpace(effectiveBaseURL) == "") {
		fmt.Fprintln(os.Stderr, panelRunUsage())
		os.Exit(2)
	}
	if parsed.apply && (fixtureSet || parsed.gatewayFlags) {
		fmt.Fprintln(os.Stderr, panelRunUsage())
		os.Exit(2)
	}
	if fixtureSet && parsed.gatewayFlags {
		fmt.Fprintln(os.Stderr, panelRunUsage())
		os.Exit(2)
	}
	return parsed
}

func panelRunUsage() string {
	return "usage: compound panel run --plan <run_id> (--fixtures <dir>|--openai-compatible-url U|--apply) [--api-key-env ENV] [--model-override lane=model]... [--json]"
}

func parsePanelModelOverride(value string) (string, string) {
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		fmt.Fprintf(os.Stderr, "invalid --model-override %q, want lane=model", value)
		fmt.Fprintln(os.Stderr)
		os.Exit(2)
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func panelRunCaller(parsed panelRunArgs) (panel.LensCaller, []string) {
	if strings.TrimSpace(parsed.fixtures) != "" {
		return panel.FixtureCaller{Dir: parsed.fixtures}, nil
	}
	apiKeyEnv := firstNonEmptyString(parsed.apiKeyEnv, os.Getenv("COMPOUND_PANEL_API_KEY_ENV"))
	apiKey := ""
	if apiKeyEnv != "" {
		apiKey = os.Getenv(apiKeyEnv)
	}
	overrides := parsed.modelOverrides
	refs := panelModelOverrideEvidence(overrides)
	return panel.GatewayCaller{
		BaseURL: firstNonEmptyString(parsed.openAICompatibleURL, os.Getenv("COMPOUND_PANEL_BASE_URL")),
		APIKey:  apiKey,
		ModelResolver: func(lane panel.LaneRef) string {
			if overrides != nil {
				if model := strings.TrimSpace(overrides[lane.ModelID]); model != "" {
					return model
				}
			}
			return lane.ModelID
		},
	}, refs
}

func panelModelOverrideEvidence(overrides map[string]string) []string {
	if len(overrides) == 0 {
		return nil
	}
	lanes := make([]string, 0, len(overrides))
	for lane := range overrides {
		lanes = append(lanes, lane)
	}
	sort.Strings(lanes)
	refs := make([]string, 0, len(lanes))
	for _, lane := range lanes {
		refs = append(refs, "override:model:"+lane+"="+overrides[lane])
	}
	return refs
}

func appendPanelEvidenceRefs(values []string, refs ...string) []string {
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		found := false
		for _, existing := range values {
			if existing == ref {
				found = true
				break
			}
		}
		if !found {
			values = append(values, ref)
		}
	}
	return values
}

func requirePanelValue(args []string, i *int, command string) string {
	if *i+1 >= len(args) {
		fmt.Fprintf(os.Stderr, "usage: compound %s: %s requires value\n", command, args[*i])
		os.Exit(2)
	}
	*i = *i + 1
	return args[*i]
}

func parsePanelNonNegative(raw, name string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		fmt.Fprintf(os.Stderr, "panel plan: %s must be non-negative integer\n", name)
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
	if run.WorkID != "" {
		fmt.Printf("- work: %s\n", run.WorkID)
	}
	if run.ReceiptID != "" {
		fmt.Printf("- receipt: %s\n", run.ReceiptID)
	}
	if run.TransitionID != "" {
		fmt.Printf("- transition: %s\n", run.TransitionID)
	}
	if run.Supervised {
		fmt.Printf("- supervision: %s\n", firstNonEmptyString(run.Supervision, "operator"))
	} else {
		fmt.Println("- supervision: unsupervised")
	}
	fmt.Printf("- target_kind: %s\n", run.TargetKind)
	fmt.Printf("- targets: %d\n", len(run.Targets))
	fmt.Printf("- findings: %d\n", len(run.Findings))
	if len(run.CountsBefore) > 0 {
		fmt.Printf("- counts_before: %s\n", formatPanelCounts(run.CountsBefore))
	}
	if len(run.CountsAfter) > 0 {
		fmt.Printf("- counts_after: %s\n", formatPanelCounts(run.CountsAfter))
	}
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
		app := finding.Application
		if app.MappedVerdict != "" || app.ActionIntentRef != "" || app.PolicyDecisionRef != "" || app.Note != "" {
			status := "skipped"
			if app.Applied {
				status = "applied"
			}
			fmt.Printf("  application: mapped=%s status=%s\n", app.MappedVerdict, status)
			if app.CandidateRef != "" {
				fmt.Printf("  candidate: %s\n", app.CandidateRef)
			}
			if app.ClaimRef != "" {
				fmt.Printf("  claim: %s\n", app.ClaimRef)
			}
			if app.NoPromoteRef != "" {
				fmt.Printf("  no_promote: %s\n", app.NoPromoteRef)
			}
			if app.TransitionRef != "" {
				fmt.Printf("  memory_transition: %s\n", app.TransitionRef)
			}
			if app.ActionIntentRef != "" {
				fmt.Printf("  action_intent: %s\n", app.ActionIntentRef)
			}
			if app.PolicyDecisionRef != "" {
				fmt.Printf("  policy_decision: %s\n", app.PolicyDecisionRef)
			}
			if app.Note != "" {
				fmt.Printf("  note: %s\n", app.Note)
			}
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

func formatPanelCounts(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, ",")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func shortPanelHash(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}
