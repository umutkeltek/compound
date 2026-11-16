package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"compound/internal/activity"
	"compound/internal/attention"
	"compound/internal/candidates"
	"compound/internal/home"
	"compound/internal/hookplan"
	"compound/internal/mem"
	"compound/internal/observation"
	"compound/internal/policy"
	"compound/internal/product"
	"compound/internal/work"
)

const attentionUsage = "usage: compound attention [--json] [--receipt-only-grade tidy] [--home P]"

func cmdAttention(args []string) {
	if hasUsageFlag(args) {
		fmt.Println(attentionUsage)
		return
	}
	jsonOut := false
	homeFlag := ""
	receiptOnlyGrade := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--receipt-only-grade":
			if i+1 >= len(args) || args[i+1] != attention.RiskGradeTidy {
				fmt.Fprintln(os.Stderr, attentionUsage)
				os.Exit(2)
			}
			i++
			receiptOnlyGrade = attention.RiskGradeTidy
		case "--home":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				fmt.Fprintln(os.Stderr, attentionUsage)
				os.Exit(2)
			}
			i++
			homeFlag = strings.TrimSpace(args[i])
		default:
			fmt.Fprintln(os.Stderr, attentionUsage)
			os.Exit(2)
		}
	}

	homePath, err := home.Resolve(homeFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	// Read-only by registry contract: attention inspects the ledger and must
	// not materialize work/ or events/ just by looking (see work.OpenReadOnly).
	store, err := work.OpenReadOnly(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	now := time.Now()
	reviews, err := buildReviewQueueReport(homePath, "", false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	// One Workbench build serves both consumers below. attention.Build never
	// reads Recent, so ExecutorsProjection's RecentLimit 8 is the binding
	// requirement; building twice (5 here, 8 inside the projection) walked
	// the whole event store twice per invocation.
	workbench, err := store.Workbench(now, work.WorkbenchOptions{RecentLimit: 8})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	executors, err := store.ExecutorsProjection(now, work.ExecutorsProjectionOptions{Workbench: &workbench})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	report := attention.Build(attentionSources(attention.Inputs{
		Reviews:   &reviews,
		Workbench: &workbench,
		Executors: &executors,
		Activity:  activityAttentionInput(homePath, &workbench, now),
	}, homePath, now), now)
	if jsonOut {
		operatorAttention, err := hookplan.ReadOperatorAttention(hookplan.OperatorAttentionRequest{
			HomePath: homePath,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		payload := struct {
			attention.Report
			OperatorAttention hookplan.OperatorAttentionProjection `json:"operator_attention"`
		}{
			Report:            report,
			OperatorAttention: operatorAttention,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", " ")
		if err := enc.Encode(payload); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if receiptOnlyGrade == "" {
		printAttentionText(report)
		return
	}
	printAttentionTextWithReceiptOnlyGrade(report, receiptOnlyGrade)
}

// attentionRules reads his standing grants the way the policy owner loads
// them (policy.yaml plus the installed registry) for the covered_by_rule
// band. A read failure warns and returns nil: nothing then leaves his list.
func attentionRules(homePath string, now time.Time) *attention.RulesInput {
	cfg, _, err := policy.LoadConfig(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: standing rules not read:", err)
		return nil
	}
	return &attention.RulesInput{StandingGrants: cfg.StandingGrants, Presence: cfg.OperatorPresence.Effective(now)}
}

// attentionSources adds his standing rules and the other places things wait
// for him (memory proposals, product results ready to judge, hook approval
// requests, data sources needing setup), each read through its owner's
// reader. A failed read warns and leaves that input out, never fakes it.
func attentionSources(in attention.Inputs, homePath string, now time.Time) attention.Inputs {
	warn := func(what string, err error) { fmt.Fprintln(os.Stderr, "warning: "+what+" not read:", err) }
	stamp := now.UTC().Format(time.RFC3339)
	in.Rules = attentionRules(homePath, now)
	in.DraftPlaceholders = []string{product.HypothesisDraftExpectedEffectPlaceholderPrefix}
	if store, err := mem.OpenScan(homePath); err != nil {
		warn("memory proposals", err)
	} else if list, err := store.ListCandidates(now, mem.CandidateListOptions{State: "proposed"}); err != nil {
		warn("memory proposals", err)
	} else {
		in.MemoryProposals = &attention.MemoryProposalsInput{GeneratedAt: list.GeneratedAt}
		for _, c := range list.Items {
			claim := strings.Join([]string{c.Entity, c.Predicate, c.Object}, " ")
			in.MemoryProposals.Items = append(in.MemoryProposals.Items, attention.MemoryProposal{
				ID: c.ID, Claim: strings.TrimSpace(claim), ClaimKey: c.Entity + "|" + c.Predicate + "|" + c.Object, Context: c.Entity,
				CreatedAt: c.CreatedAt, AdjudicatedBy: c.AdjudicatedBy,
				Path: filepath.Join(homePath, candidates.InboxDir(candidates.Memory), c.ID+".json"),
			})
		}
	}
	// No memory store: folding product spellings takes the memory lock,
	// which would create a lock file in a home that has none.
	if due, err := product.ResultsDue(homePath, nil, now); err != nil {
		warn("product results", err)
	} else {
		in.ProductResults = &attention.ProductResultsInput{GeneratedAt: stamp}
		for _, d := range due {
			in.ProductResults.Items = append(in.ProductResults.Items, attention.ProductResult{ProductID: d.ProductID, HypothesisID: d.HypothesisID, ChangeEvent: d.ChangeEvent, ExpectedEffect: d.ExpectedEffect, CreatedAt: d.CreatedAt, SignalIDs: d.SignalIDs})
		}
	}
	if frontier, err := policy.LoadOperatorApprovalFrontierFromHome(homePath); err != nil && !os.IsNotExist(err) {
		warn("hook approval requests", err)
	} else {
		in.HookRequests = &attention.HookRequestsInput{GeneratedAt: stamp, Path: policy.OperatorApprovalFrontierPath(homePath)}
		for _, r := range frontier.ActionApprovals {
			in.HookRequests.Items = append(in.HookRequests.Items, attention.HookRequest{ID: r.ID, ActionIntentID: r.ActionIntentID, Action: r.Action, Capability: r.Capability, Harness: r.Harness, MintedAt: r.MintedAt, Status: r.Status})
		}
	}
	if sources, err := observation.LoadSourceConnectionsProjection(homePath, now); err != nil {
		warn("data sources", err)
	} else {
		in.DataSources = &attention.DataSourcesInput{GeneratedAt: sources.GeneratedAt}
		for _, c := range sources.Connections {
			// A source that never produced evidence is setup he may choose,
			// not something in use that waits for him.
			if c.EvidenceState == "never_seen" || strings.TrimSpace(c.LastSeen) == "" {
				continue
			}
			switch c.Status {
			case "stale", "blocked_private_config", "needs_private_input", "ready_preflight", "needs_review", "needs_mapping":
				in.DataSources.Items = append(in.DataSources.Items, attention.DataSource{ID: c.ID, Label: c.Label, Status: c.Status, LastSeen: c.LastSeen, PrivateSetup: c.PrivateSetupNeeded, NextStep: c.NextSafeAction})
			}
		}
	}
	return in
}

// activityAttentionMaxAgeDays bounds how old the latest span day may be before the activity read
// yields nothing: a sensor that stopped draining weeks ago must not squat a stale block on the
// attention board. Staleness-gating over memory is the attention norm; freshness metadata alone
// would still rank a dead sensor's last day every morning.
const activityAttentionMaxAgeDays = 3

// activityAttentionInput is the decision-13 additive activity read: latest persisted span day ->
// activity.InferDay against the workbench's visible envelope/receipt timestamps ->
// attention.ActivityDayInput. Nil in every quiet case (no spans, stale spans, zero unaccounted
// blocks). A read error warns on stderr but never blocks the board — this input is enrichment; the
// attention projection is the product.
func activityAttentionInput(homePath string, workbench *work.WorkbenchReport, now time.Time) *attention.ActivityDayInput {
	day, ok, err := activity.LatestSpanDay(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: activity read skipped:", err)
		return nil
	}
	if !ok {
		return nil
	}
	dayStart, err := time.Parse("2006-01-02", day)
	if err != nil || now.UTC().Sub(dayStart) > time.Duration(activityAttentionMaxAgeDays)*24*time.Hour {
		return nil
	}
	spanFile, ok, err := activity.ReadSpanFile(homePath, day)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: activity read skipped:", err)
		return nil
	}
	if !ok {
		return nil
	}
	inference := activity.InferDay(day, spanFile.Spans, workbenchWorkActivities(workbench))
	if len(inference.UnaccountedBlocks) == 0 {
		return nil
	}
	input := &attention.ActivityDayInput{
		Date:                inference.Date,
		GeneratedAt:         spanFile.GeneratedAt,
		TotalUnaccountedMin: inference.TotalUnaccountedMin,
	}
	for _, block := range inference.UnaccountedBlocks {
		input.UnaccountedBlocks = append(input.UnaccountedBlocks, attention.ActivityBlock{
			Start:       block.Start,
			End:         block.End,
			ActiveMin:   block.ActiveMin,
			DurationMin: block.DurationMin,
			CategoryMix: block.CategoryMix,
		})
	}
	return input
}

// workbenchWorkActivities extracts every envelope/receipt timestamp the workbench already renders —
// claim times plus run-observation and route-outcome times, across open AND recent items — as the
// reconciliation anchors an activity block is "accounted" against (decision 11: work activity for
// slice 1 = timestamps visible in work.WorkbenchReport).
func workbenchWorkActivities(report *work.WorkbenchReport) []activity.WorkActivity {
	if report == nil {
		return nil
	}
	var out []activity.WorkActivity
	add := func(ref, ts string) {
		at, ok := workTimestamp(ts)
		if !ok {
			return
		}
		out = append(out, activity.WorkActivity{Ref: ref, At: at})
	}
	items := make([]work.WorkbenchItem, 0, len(report.Open)+len(report.Recent))
	items = append(items, report.Open...)
	items = append(items, report.Recent...)
	for _, item := range items {
		add(item.WorkID, item.ClaimedAt)
		for _, obs := range item.RunObservations {
			add(item.WorkID+":"+obs.ID, obs.GeneratedAt)
		}
		for _, route := range item.RouteOutcomes {
			add(item.WorkID+":"+route.ID, route.CreatedAt)
		}
	}
	return out
}

// workTimestamp accepts both timestamp encodings the workbench renders: RFC3339 (run observations,
// route outcomes) and integer epoch values (lease claimed_at is epoch NANOSECONDS, not RFC3339 —
// grep-verified premise correction). Mirrors internal/work's unexported executorClaimedAtTime
// (executors.go:783), which is outside this slice's touch boundary.
func workTimestamp(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, true
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	switch {
	case n > 1e16:
		return time.Unix(0, n), true
	case n > 1e13:
		return time.Unix(0, n*int64(time.Microsecond)), true
	case n > 1e11:
		return time.Unix(0, n*int64(time.Millisecond)), true
	default:
		return time.Unix(n, 0), true
	}
}

func printAttentionText(report attention.Report) {
	printAttentionTextWithReceiptOnlyGrade(report, "")
}

func printAttentionTextWithReceiptOnlyGrade(report attention.Report, receiptOnlyGrade string) {
	fmt.Printf("attention needs_you=%d groups=%d actionable_now=%d watching=%d blocked_on_you=%d fallback_groups=%d\n",
		report.Counts.NeedsYou,
		report.Counts.NeedsYouGroups,
		report.Counts.ActionableNow,
		report.Counts.Watching,
		report.Counts.BlockedOnYou,
		report.Counts.FallbackGroups,
	)
	if report.Totals.SetAside > 0 {
		fmt.Printf("set aside as not yours: %d (closed without you: %d, waiting on others: %d) — view with --json\n",
			report.Totals.SetAside,
			report.Totals.ClosedWithoutYou,
			report.Totals.SetAside-report.Totals.ClosedWithoutYou,
		)
	}
	if report.Totals.CoveredByRule > 0 {
		fmt.Printf("covered by your standing rules, not counted: %d (nothing works them yet) — view with --json\n", report.Totals.CoveredByRule)
	}
	groups := report.Groups
	receiptOnlyCount := 0
	if receiptOnlyGrade != "" {
		groups = make([]attention.Group, 0, len(report.Groups))
		for _, group := range report.Groups {
			if group.Band == attention.BandNeedsYou && group.RiskGrade == receiptOnlyGrade {
				receiptOnlyCount += group.Count
				continue
			}
			groups = append(groups, group)
		}
	}
	if receiptOnlyCount > 0 {
		fmt.Printf("%d receipt-only (risk_grade %s) — still in the ledger; view with --json\n", receiptOnlyCount, receiptOnlyGrade)
	}
	limit := len(groups)
	if limit > 10 {
		limit = 10
	}
	for i := 0; i < limit; i++ {
		group := groups[i]
		fmt.Printf("- %s %.2f %s/%s count=%d %s",
			attention.Class(group),
			group.Leverage,
			group.Source,
			group.Area,
			group.Count,
			group.OperatorSummary,
		)
		if group.OperatorUnlock != "" {
			fmt.Printf(" unlock=%q", group.OperatorUnlock)
		}
		if group.Action != nil {
			fmt.Printf(" action=%s/%s", group.Action.Kind, group.Action.ID)
		}
		if group.TerminalReason != "" {
			fmt.Printf(" reason=%q", group.TerminalReason)
		}
		fmt.Println()
	}
}
