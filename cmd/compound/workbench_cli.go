package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"compound/internal/attention"
	"compound/internal/authority"
	"compound/internal/capgraph"
	"compound/internal/decisions"
	"compound/internal/distiller"
	"compound/internal/home"
	"compound/internal/mem"
	"compound/internal/observation"
	"compound/internal/panel"
	"compound/internal/product"
	"compound/internal/projection"
	"compound/internal/reviewartifact"
	"compound/internal/routeevidence"
	"compound/internal/runtimebinding"
	"compound/internal/sourceobs"
	"compound/internal/work"
)

const workbenchCapabilityOfferLimit = 12

const workbenchUsage = "usage: compound workbench [--json] [--operator] [--recent N] [--actor A] [--active-packet-surface S] [--source-packet packet.md] [--packet-dir D] [--capability-task T] [--capability-harness H] [--capability-limit N] [--route-class C] [--route-privacy-sensitive] [--review-area A] [--include-archived]"

type workbenchCLIOutput struct {
	work.WorkbenchReport
	CapabilitySurface *capgraph.SurfaceReport            `json:"capability_surface,omitempty"`
	RoutePlan         *routeevidence.CapabilityRoutePlan `json:"route_plan,omitempty"`
	ReviewQueue       *workbenchReviewQueue              `json:"review_queue,omitempty"`
}

// workbenchReviewQueue is the workbench's operator-load headline. It is the
// ONE meter (packet W1e): Counts.ReviewRequired is not computed independently
// from review-artifact status anymore — it is overwritten with the attention
// projection's needs_you count (internal/attention) so this surface never
// disagrees with `compound attention`. The JSON field name stays stable for
// existing consumers (e.g. the umut-os session-start digest hook reads
// workbench.review_queue.counts.review_required); only its source changed.
// BlockedOnYou is additive detail from the same projection, never a
// different total. If the attention projection's inputs cannot be read,
// AttentionUnavailable is set and Counts.ReviewRequired keeps the last-known
// review-artifact-derived value instead of silently recomputing anything.
type workbenchReviewQueue struct {
	SchemaVersion        string                     `json:"schema_version"`
	AuthorityRef         string                     `json:"authority_ref"`
	Area                 string                     `json:"area,omitempty"`
	Counts               reviewartifact.QueueCounts `json:"counts"`
	BlockedOnYou         int                        `json:"blocked_on_you,omitempty"`
	AttentionUnavailable bool                       `json:"attention_unavailable,omitempty"`
	Actionable           []workbenchReviewItem      `json:"actionable,omitempty"`
}

type workbenchReviewItem struct {
	Area       string                  `json:"area"`
	ID         string                  `json:"id"`
	Status     string                  `json:"status"`
	Readiness  string                  `json:"readiness,omitempty"`
	NextAction string                  `json:"next_action,omitempty"`
	Actions    []reviewartifact.Action `json:"actions,omitempty"`
}

func defaultWorkbenchPacketDir() string {
	if dir := strings.TrimSpace(os.Getenv("COMPOUND_PACKET_DIR")); dir != "" {
		return dir
	}
	for _, dir := range []string{
		filepath.Join("docs_private", "plan", "packets"),
		filepath.Join("..", "docs_private", "plan", "packets"),
	} {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return dir
		}
	}
	return ""
}

func cmdWorkbench(args []string) {
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	store, err := work.Open(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	jsonOut := false
	operatorOut := false
	recent := 5
	actor := "codex"
	activePacketSurface := ""
	activePacketExpectedRef := ""
	capabilityTask := ""
	capabilityHarness := ""
	capabilityLimit := workbenchCapabilityOfferLimit
	routeClass := ""
	routePrivacySensitive := false
	reviewArea := ""
	includeArchived := false
	packetDir := defaultWorkbenchPacketDir()
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--operator":
			operatorOut = true
		case "--include-archived":
			includeArchived = true
		case "--active-packet-surface":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, workbenchUsage)
				os.Exit(2)
			}
			i++
			activePacketSurface = strings.TrimSpace(args[i])
			if activePacketSurface == "" {
				fmt.Fprintln(os.Stderr, "error: --active-packet-surface must be non-empty")
				os.Exit(2)
			}
		case "--source-packet":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, workbenchUsage)
				os.Exit(2)
			}
			i++
			proof, err := work.PacketSourceProofForPath(args[i])
			if err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
			activePacketExpectedRef = proof.EvidenceRef
			if activePacketSurface == "" {
				activePacketSurface = proof.CanonicalSurface
			}
		case "--packet-dir":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, workbenchUsage)
				os.Exit(2)
			}
			i++
			packetDir = strings.TrimSpace(args[i])
			if packetDir == "" {
				fmt.Fprintln(os.Stderr, "error: --packet-dir must be non-empty")
				os.Exit(2)
			}
		case "--capability-task":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, workbenchUsage)
				os.Exit(2)
			}
			i++
			capabilityTask = strings.TrimSpace(args[i])
			if capabilityTask == "" {
				fmt.Fprintln(os.Stderr, "error: --capability-task must be non-empty")
				os.Exit(2)
			}
		case "--capability-harness":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, workbenchUsage)
				os.Exit(2)
			}
			i++
			capabilityHarness = strings.TrimSpace(args[i])
			if capabilityHarness == "" {
				fmt.Fprintln(os.Stderr, "error: --capability-harness must be non-empty")
				os.Exit(2)
			}
		case "--capability-limit":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, workbenchUsage)
				os.Exit(2)
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				fmt.Fprintln(os.Stderr, "error: --capability-limit must be a non-negative integer")
				os.Exit(2)
			}
			capabilityLimit = n
		case "--route-class":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, workbenchUsage)
				os.Exit(2)
			}
			i++
			routeClass = strings.TrimSpace(args[i])
			if routeClass == "" {
				fmt.Fprintln(os.Stderr, "error: --route-class must be non-empty")
				os.Exit(2)
			}
		case "--route-privacy-sensitive":
			routePrivacySensitive = true
		case "--review-area":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, workbenchUsage)
				os.Exit(2)
			}
			i++
			reviewArea = strings.TrimSpace(args[i])
			if reviewArea == "" {
				fmt.Fprintln(os.Stderr, "error: --review-area must be non-empty")
				os.Exit(2)
			}
		case "--actor":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, workbenchUsage)
				os.Exit(2)
			}
			i++
			actor = strings.TrimSpace(args[i])
			if actor == "" {
				fmt.Fprintln(os.Stderr, "error: --actor must be non-empty")
				os.Exit(2)
			}
		case "--recent":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, workbenchUsage)
				os.Exit(2)
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				fmt.Fprintln(os.Stderr, "error: --recent must be a non-negative integer")
				os.Exit(2)
			}
			recent = n
		default:
			fmt.Fprintln(os.Stderr, workbenchUsage)
			os.Exit(2)
		}
	}
	report, err := store.Workbench(time.Now(), work.WorkbenchOptions{
		RecentLimit:                     recent,
		Actor:                           actor,
		ActivePacketSurface:             activePacketSurface,
		ActivePacketExpectedEvidenceRef: activePacketExpectedRef,
		IncludeArchived:                 includeArchived,
		PacketDir:                       packetDir,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	var capabilitySurface *capgraph.SurfaceReport
	if capabilityTask != "" {
		if capabilityHarness == "" {
			capabilityHarness = actor
		}
		capStore, err := capgraph.Open(homePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		surface, err := buildCapabilitySurfaceReport(capStore, homePath, capabilityTask, capabilityHarness)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		surface = surface.LimitOffers(capabilityLimit)
		capabilitySurface = &surface
	}
	var routePlan *routeevidence.CapabilityRoutePlan
	if routeClass != "" {
		plan, err := routeevidence.PlanCapabilityRoute(homePath, routeClass, routeevidence.CapabilityRoutePlanOptions{
			PrivacySensitive: routePrivacySensitive,
		}, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		routePlan = &plan
	}
	reviewReport, err := buildReviewQueueReport(homePath, reviewArea, includeArchived)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	reviewQueue := buildWorkbenchReviewQueue(reviewReport)
	applyAttentionMeter(&reviewQueue, store, reviewReport, report, time.Now())
	output := workbenchCLIOutput{WorkbenchReport: report, CapabilitySurface: capabilitySurface, RoutePlan: routePlan, ReviewQueue: &reviewQueue}
	if jsonOut {
		printJSON(output)
		return
	}
	if operatorOut {
		printWorkbenchOperator(output)
		return
	}
	fmt.Printf("workbench open=%d needs_claim=%d needs_review=%d in_progress=%d terminal=%d\n",
		report.Counts.Open, report.Counts.NeedsClaim, report.Counts.NeedsReview, report.Counts.InProgress, report.Counts.Terminal)
	if report.Counts.RunObservations > 0 {
		fmt.Printf("run_observations=%d\n", report.Counts.RunObservations)
	}
	if report.Counts.RunArtifacts > 0 {
		fmt.Printf("run_artifacts=%d\n", report.Counts.RunArtifacts)
	}
	if report.Counts.DispatchStepRuns > 0 {
		fmt.Printf("dispatch_step_runs=%d\n", report.Counts.DispatchStepRuns)
	}
	if report.Counts.ContinueRuns > 0 {
		fmt.Printf("continue_runs=%d\n", report.Counts.ContinueRuns)
	}
	if report.Counts.AdmissionPreviews > 0 {
		fmt.Printf("admission_previews=%d\n", report.Counts.AdmissionPreviews)
		for i, preview := range report.AdmissionPreviews {
			if i >= 3 {
				break
			}
			fmt.Printf("admission_preview: %s owner=%s state=%s staleness=%s age=%s plan=%s\n", preview.WorkID, preview.WorkOwner, preview.WorkState, emptyDash(preview.Staleness), emptyDash(preview.Age), preview.PlanPath)
			if preview.NextAction != "" {
				fmt.Printf("  next_action: %s\n", preview.NextAction)
			}
			if preview.RequiresOperator {
				fmt.Printf("  operator_required: true\n")
			}
			if preview.AdmissionBlockedReason != "" {
				fmt.Printf("  blocked_reason: %s\n", preview.AdmissionBlockedReason)
			}
			if preview.ActionSummary != "" {
				fmt.Printf(" action_summary: %s\n", preview.ActionSummary)
			}
			if len(preview.AdmitCommand) > 0 {
				fmt.Printf("  admit: %s\n", strings.Join(preview.AdmitCommand, " "))
			}
			if len(preview.RetireDryRun) > 0 {
				fmt.Printf("  retire_dry_run: %s\n", strings.Join(preview.RetireDryRun, " "))
			}
			if len(preview.RetireCommand) > 0 {
				fmt.Printf("  retire: %s\n", strings.Join(preview.RetireCommand, " "))
			}
			if preview.ActionContext != nil && len(preview.ActionContext.Command) > 0 {
				fmt.Printf("  action_context: %s proof=%s\n", strings.Join(preview.ActionContext.Command, " "), emptyDash(preview.ActionContext.Proof))
			}
		}
	}
	if capabilitySurface != nil {
		fmt.Printf("capability_surface status=%s offers=%d showing=%d omitted=%d task=%q harness=%q authority=%s\n",
			capabilitySurface.Status,
			capabilitySurface.OfferCount,
			len(capabilitySurface.OfferSummaries),
			capabilitySurface.OmittedOfferCount,
			capabilitySurface.Task,
			capabilitySurface.Harness,
			capabilitySurface.AuthorityRef,
		)
		for i, offer := range capabilitySurface.OfferSummaries {
			if i >= 3 {
				break
			}
			fmt.Printf("capability_offer: %s %-28s score %.1f [%s]\n", offer.Kind, offer.ID, offer.Score, offer.Why)
		}
	}
	if routePlan != nil {
		fmt.Printf("route_plan status=%s class=%q privacy_sensitive=%t lanes=%d omitted=%d authority=%s\n",
			routePlan.Status,
			routePlan.CapabilityClass,
			routePlan.PrivacySensitive,
			len(routePlan.Chain),
			len(routePlan.Omitted),
			routePlan.AuthorityRef,
		)
		for i, lane := range routePlan.Chain {
			if i >= 3 {
				break
			}
			fmt.Printf("route_lane: %d %s tier=%s cost_class=%s role=%s harness=%s\n",
				lane.Position,
				lane.ModelID,
				lane.Tier,
				lane.CostClass,
				lane.GateRole,
				lane.Harness,
			)
		}
	}
	fmt.Printf("review_queue total=%d review_required=%d ready_to_apply=%d ok=%d authority=%s\n",
		reviewQueue.Counts.Total,
		reviewQueue.Counts.ReviewRequired,
		reviewQueue.Counts.ReadyToApply,
		reviewQueue.Counts.OK,
		reviewQueue.AuthorityRef,
	)
	for i, item := range reviewQueue.Actionable {
		if i >= 3 {
			break
		}
		fmt.Printf("review_action: %s %-28s %s %s\n", item.Area, item.ID, item.Readiness, item.NextAction)
	}
	if report.ActivePacketProof != nil {
		fmt.Printf("active_packet_proof status=%s work=%s context=%s receipt=%s transition=%s\n",
			report.ActivePacketProof.Status,
			report.ActivePacketProof.WorkID,
			report.ActivePacketProof.ContextPackID,
			report.ActivePacketProof.ReceiptID,
			report.ActivePacketProof.TransitionID)
	}
	if report.DispatchNext.CanRun {
		fmt.Printf("dispatch_next: %s\n", strings.Join(report.DispatchNext.Command, " "))
	} else if report.DispatchNext.WaitReason != "" {
		fmt.Printf("dispatch_wait: %s", report.DispatchNext.WaitReason)
		if report.DispatchNext.WorkID != "" {
			fmt.Printf(" work=%s", report.DispatchNext.WorkID)
		}
		fmt.Println()
		if adjacent := report.DispatchNext.AdjacentProgress; adjacent != nil {
			fmt.Printf("adjacent_progress: available=%t packet_candidates=%d consumed_non_packet_actions=%d reason=%s\n", adjacent.Available, adjacent.PacketCandidateCount, len(adjacent.ConsumedNonPacketActions), adjacent.Reason)
			if adjacent.NextAction != "" {
				fmt.Printf("adjacent_next: %s\n", adjacent.NextAction)
			}
			if len(adjacent.Command) > 0 {
				fmt.Printf("adjacent_command: %s\n", strings.Join(adjacent.Command, " "))
			}
			for i, action := range adjacent.ConsumedNonPacketActions {
				if i >= 3 {
					break
				}
				fmt.Printf("consumed_adjacent_action: %s kind=%s evidence=%s\n", action.ID, action.Kind, strings.Join(action.ConsumedEvidenceRefs, ","))
			}
			for i, action := range adjacent.NonPacketActions {
				if i >= 3 {
					break
				}
				fmt.Printf("adjacent_non_packet_action: %s kind=%s operator=%t proof=%s\n", action.ID, action.Kind, action.RequiresOperator, action.Proof)
				if action.NextAction != "" {
					fmt.Printf(" action_next: %s\n", action.NextAction)
				}
				if action.ReviewCard != nil && action.ReviewCard.ConditionStatus != "" {
					fmt.Printf(" action_condition_status: %s evidence=%s\n", action.ReviewCard.ConditionStatus, action.ReviewCard.TriggeringEvidenceRef)
				}
				if len(action.Command) > 0 {
					fmt.Printf(" action_command: %s\n", strings.Join(action.Command, " "))
				}
				if len(action.RequiredInputs) > 0 {
					fmt.Printf(" action_required_inputs: %s\n", strings.Join(action.RequiredInputs, ", "))
				}
				if len(action.ExpectedValidation) > 0 {
					fmt.Printf(" action_expected_validation: %s\n", strings.Join(action.ExpectedValidation, ", "))
				}
				if len(action.StopConditions) > 0 {
					fmt.Printf(" action_stop_conditions: %s\n", strings.Join(action.StopConditions, ", "))
				}
			}
		}
	}
	if len(report.Integrity) > 0 {
		fmt.Println("integrity:")
		for _, msg := range report.Integrity {
			fmt.Println("- " + msg)
		}
	}
	if len(report.Open) == 0 {
		fmt.Println("open: none")
	} else {
		fmt.Println("open:")
		for _, item := range report.Open {
			fmt.Printf("- %s %-10s %-14s %s\n", item.WorkID, item.State, item.OpenReason, item.SuccessPredicate)
			fmt.Printf("  next: %s\n", item.NextAction)
			if item.ActionContext != nil && len(item.ActionContext.Command) > 0 {
				fmt.Printf("  action_context: %s proof=%s\n", strings.Join(item.ActionContext.Command, " "), emptyDash(item.ActionContext.Proof))
			}
			if item.ClaimedBy != "" {
				fmt.Printf("  claimed_by: %s\n", item.ClaimedBy)
			}
			if len(item.RunObservations) > 0 {
				latest := item.RunObservations[0]
				fmt.Printf("  observed_run: %s trace=%s policy=%s\n", latest.ID, latest.TraceID, latest.PolicyDecisionID)
			}
			if len(item.RunArtifacts) > 0 {
				artifact := item.RunArtifacts[0]
				count := "-"
				if artifact.Count != nil {
					count = strconv.Itoa(*artifact.Count)
				}
				target := "-"
				if len(artifact.TargetRefs) > 0 {
					target = artifact.TargetRefs[0]
				} else if len(artifact.CandidateIDs) > 0 {
					target = artifact.CandidateIDs[0]
				}
				fmt.Printf("  run_artifact: %s kind=%s status=%s target=%s count=%s\n", artifact.Ref, artifact.Kind, emptyDash(artifact.Status), target, count)
			}
		}
	}
	if len(report.Recent) > 0 {
		fmt.Println("recent:")
		for _, item := range report.Recent {
			fmt.Printf("- %s %-10s receipt=%s transition=%s move=%s changed=%v\n",
				item.WorkID, item.State, emptyDash(item.ReceiptID), emptyDash(item.TransitionID), emptyDash(item.MoveType), item.Changed)
		}
	}
	if len(report.DispatchStepRuns) > 0 {
		fmt.Println("dispatch_step_runs:")
		for _, run := range report.DispatchStepRuns {
			fmt.Printf("- %s status=%s steps=%d wait=%s next=%s\n",
				run.ID, run.Status, run.StepCount, emptyDash(run.WaitReason), emptyDash(run.NextAction))
		}
	}
	if len(report.ContinueRuns) > 0 {
		fmt.Println("continue_runs:")
		for _, run := range report.ContinueRuns {
			fmt.Printf("- %s status=%s steps=%d wait=%s next=%s\n",
				run.ID, run.Status, run.StepCount, emptyDash(run.WaitReason), emptyDash(run.NextAction))
		}
	}
}

type operatorWorkbenchRow struct {
	Item        work.WorkbenchItem
	Status      string
	Authority   string
	Opportunity string
	Mission     string
	Assignee    string
	Proof       string
	Decision    string
	Learning    string
}

func printWorkbenchOperator(output workbenchCLIOutput) {
	rows := operatorWorkbenchRows(output.WorkbenchReport)
	fmt.Println("Operator workbench")
	fmt.Println("Flow stages: Explore → Build → Prove → Integrate")
	fmt.Println("Narrative: Opportunity → Mission → Run → Proof → Decision → Learning")
	fmt.Println("Summary: " + operatorFlowSummary(output, rows))
	fmt.Println()
	if len(rows) == 0 {
		fmt.Println("Explore")
		fmt.Println("Opportunity")
		fmt.Println("- [Parked] no visible work; Authority: Free to act")
		fmt.Println()
		fmt.Println("Build")
		fmt.Println("Mission")
		fmt.Println("- unknown/not yet proven")
		fmt.Println()
		fmt.Println("Prove")
		fmt.Println("Proof")
		fmt.Println("- unknown/not yet proven")
		fmt.Println()
		fmt.Println("Integrate")
		fmt.Println("Decision")
		fmt.Println("- no action available from workbench")
		fmt.Println("Learning")
		fmt.Println("- unknown/not yet proven")
		fmt.Println()
		fmt.Println("Advanced")
		fmt.Printf("- generated_at=%s counts_open=%d counts_terminal=%d dispatch_wait=%s\n",
			operatorValue(output.GeneratedAt),
			output.Counts.Open,
			output.Counts.Terminal,
			operatorValue(output.DispatchNext.WaitReason),
		)
		return
	}

	fmt.Println("Explore")
	fmt.Println("Opportunity")
	for _, row := range rows {
		fmt.Printf("- [%s] %s | Authority: %s\n", row.Status, row.Opportunity, row.Authority)
	}
	fmt.Println()
	fmt.Println("Build")
	fmt.Println("Mission")
	for _, row := range rows {
		fmt.Printf("- %s: %s\n", row.Item.WorkID, row.Mission)
	}
	fmt.Println("Run")
	for _, row := range rows {
		fmt.Printf("- %s: assigned %s; state=%s; next=%s\n",
			row.Item.WorkID,
			row.Assignee,
			operatorValue(row.Item.State),
			operatorValue(row.Item.NextAction),
		)
	}
	fmt.Println()
	fmt.Println("Prove")
	fmt.Println("Proof")
	for _, row := range rows {
		fmt.Printf("- %s: %s\n", row.Item.WorkID, row.Proof)
	}
	fmt.Println()
	fmt.Println("Integrate")
	fmt.Println("Decision")
	for _, row := range rows {
		fmt.Printf("- %s: Authority: %s; %s\n", row.Item.WorkID, row.Authority, row.Decision)
	}
	fmt.Println("Learning")
	for _, row := range rows {
		fmt.Printf("- %s: %s\n", row.Item.WorkID, row.Learning)
	}
	fmt.Println()
	fmt.Println("Advanced")
	for _, row := range rows {
		fmt.Printf("- work_id=%s state=%s status=%s authority=%s owner=%s claimed_by=%s policy=%s receipt=%s transition=%s move=%s changed=%t evidence=%s artifacts=%s verifications=%s\n",
			row.Item.WorkID,
			operatorValue(row.Item.State),
			row.Status,
			row.Authority,
			operatorValue(row.Item.Owner),
			operatorValue(row.Item.ClaimedBy),
			operatorValue(row.Item.PolicyDecisionID),
			operatorValue(row.Item.ReceiptID),
			operatorValue(row.Item.TransitionID),
			operatorValue(row.Item.MoveType),
			row.Item.Changed,
			operatorList(row.Item.EvidenceRefs),
			operatorList(row.Item.ArtifactIDs),
			operatorList(row.Item.VerificationIDs),
		)
	}
}

func operatorWorkbenchRows(report work.WorkbenchReport) []operatorWorkbenchRow {
	items := append([]work.WorkbenchItem{}, report.Open...)
	items = append(items, report.Recent...)
	rows := make([]operatorWorkbenchRow, 0, len(items))
	for _, item := range items {
		status := operatorStatusForItem(item)
		authority := operatorAuthorityForItem(item)
		rows = append(rows, operatorWorkbenchRow{
			Item:        item,
			Status:      status,
			Authority:   authority,
			Opportunity: operatorOpportunityForItem(item),
			Mission:     firstNonEmpty(item.DesiredOutcome, item.SuccessPredicate, "unknown/not yet proven"),
			Assignee:    operatorAssigneeForItem(item),
			Proof:       operatorProofForItem(item),
			Decision:    operatorDecisionForItem(item),
			Learning:    operatorLearningForItem(item),
		})
	}
	return rows
}

// operatorStatusForItem is a deterministic translation of existing
// WorkEnvelope/proof/review state. It does not add a second lifecycle.
func operatorStatusForItem(item work.WorkbenchItem) string {
	switch strings.TrimSpace(item.State) {
	case "proposed", "admitted", "ready":
		return "Ready"
	case "claimed", "running":
		return "Working"
	case "verifying":
		return "Proving"
	case "held":
		return "Needs you"
	case "blocked":
		return "Blocked"
	case "completed":
		if item.ReceiptID == "" || item.TransitionID == "" {
			return "Blocked"
		}
		if item.Changed {
			return "Integrated"
		}
		return "Verified"
	case "cancelled":
		return "Parked"
	case "failed":
		return "Discarded"
	default:
		return "Parked"
	}
}

func operatorAuthorityForItem(item work.WorkbenchItem) string {
	if operatorItemNeedsUmut(item) {
		return "Needs Umut"
	}
	if strings.TrimSpace(item.State) == "verifying" ||
		strings.TrimSpace(item.ReviewTrigger) != "" ||
		(item.ActionContext != nil && len(item.ActionContext.ExpectedValidation) > 0) {
		return "Review before landing"
	}
	return "Free to act"
}

func operatorItemNeedsUmut(item work.WorkbenchItem) bool {
	if item.State == "held" || item.State == "blocked" {
		return true
	}
	if item.ActionContext != nil && item.ActionContext.RequiresOperator {
		return true
	}
	for _, card := range item.ReviewCards {
		if card.RequiresOperator {
			return true
		}
	}
	return false
}

func operatorOpportunityForItem(item work.WorkbenchItem) string {
	return firstNonEmpty(item.SuccessPredicate, item.CanonicalSurface, item.WorkID, "unknown/not yet proven")
}

func operatorAssigneeForItem(item work.WorkbenchItem) string {
	return firstNonEmpty(item.ClaimedBy, item.Owner, "unknown")
}

func operatorProofForItem(item work.WorkbenchItem) string {
	if proof := firstNonEmpty(item.EvidenceRefs...); proof != "" {
		return proof
	}
	if proof := firstNonEmpty(item.VerificationIDs...); proof != "" {
		return proof
	}
	if proof := firstNonEmpty(item.ArtifactIDs...); proof != "" {
		return proof
	}
	for _, obs := range item.RunObservations {
		if obs.TraceID != "" {
			return obs.TraceID
		}
	}
	if item.ReceiptID != "" && item.TransitionID != "" {
		return item.ReceiptID + " + " + item.TransitionID
	}
	return "unknown/not yet proven"
}

func operatorDecisionForItem(item work.WorkbenchItem) string {
	if item.NextAction != "" {
		return "next: " + item.NextAction
	}
	if item.ReceiptID != "" && item.TransitionID != "" {
		return "receipt and transition recorded"
	}
	return "unknown/not yet proven"
}

func operatorLearningForItem(item work.WorkbenchItem) string {
	if item.TransitionID != "" {
		return "transition " + item.TransitionID + "; move=" + operatorValue(item.MoveType)
	}
	if len(item.MemoryCandidateIDs) > 0 {
		return "memory candidates " + operatorList(item.MemoryCandidateIDs)
	}
	return "unknown/not yet proven"
}

func operatorFlowSummary(output workbenchCLIOutput, rows []operatorWorkbenchRow) string {
	return fmt.Sprintf("We noticed %s, chose %s, assigned %s, proved it with %s, and now need %s.",
		operatorSummaryOpportunity(output),
		operatorSummaryChoice(output),
		operatorSummaryAssignee(output, rows),
		operatorSummaryProof(rows),
		operatorSummaryNeed(output),
	)
}

func operatorSummaryOpportunity(output workbenchCLIOutput) string {
	if output.Counts.Open > 0 {
		return operatorCount(output.Counts.Open, "open work item", "open work items")
	}
	if output.Counts.Terminal > 0 {
		return operatorCount(output.Counts.Terminal, "terminal work item", "terminal work items")
	}
	return "no active work"
}

func operatorSummaryChoice(output workbenchCLIOutput) string {
	if output.DispatchNext.CanRun {
		return "the next governed dispatch step"
	}
	if output.DispatchNext.WaitReason != "" {
		return "to wait on " + operatorWords(output.DispatchNext.WaitReason)
	}
	return "to inspect current work"
}

func operatorSummaryAssignee(output workbenchCLIOutput, rows []operatorWorkbenchRow) string {
	if output.DispatchNext.Actor != "" {
		return output.DispatchNext.Actor
	}
	for _, row := range rows {
		if row.Assignee != "" && row.Assignee != "unknown" {
			return row.Assignee
		}
	}
	return "unknown"
}

func operatorSummaryProof(rows []operatorWorkbenchRow) string {
	for _, row := range rows {
		if (row.Status == "Integrated" || row.Status == "Verified") && row.Proof != "unknown/not yet proven" {
			return row.Proof
		}
	}
	for _, row := range rows {
		if row.Proof != "unknown/not yet proven" {
			return row.Proof
		}
	}
	return "unknown/not yet proven"
}

func operatorSummaryNeed(output workbenchCLIOutput) string {
	if output.ReviewQueue != nil {
		if output.ReviewQueue.BlockedOnYou > 0 {
			return "Umut to clear " + operatorCount(output.ReviewQueue.BlockedOnYou, "blocked item", "blocked items")
		}
		if output.ReviewQueue.Counts.ReviewRequired > 0 {
			return "review for " + operatorCount(output.ReviewQueue.Counts.ReviewRequired, "item", "items")
		}
	}
	if output.Counts.NeedsReview > 0 {
		return "review for " + operatorCount(output.Counts.NeedsReview, "work item", "work items")
	}
	if output.Counts.NeedsClaim > 0 {
		return "a claim or dispatch for " + operatorCount(output.Counts.NeedsClaim, "work item", "work items")
	}
	if output.Counts.InProgress > 0 {
		return "proof for " + operatorCount(output.Counts.InProgress, "work item", "work items")
	}
	if output.Counts.MissingReceipt > 0 || output.Counts.MissingTransition > 0 {
		return "receipt/transition proof"
	}
	if output.DispatchNext.WaitReason != "" {
		return operatorWords(output.DispatchNext.WaitReason)
	}
	return "nothing; visible work is integrated"
}

func operatorCount(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(n) + " " + plural
}

func operatorValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func operatorWords(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, "-", " ")
	return value
}

func operatorList(values []string) string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	if len(cleaned) == 0 {
		return "unknown"
	}
	return strings.Join(cleaned, ",")
}

func buildReviewQueueReport(homePath, area string, includeArchived bool) (reviewartifact.QueueReport, error) {
	report, err := reviewartifact.Queue(homePath, reviewartifact.QueueOptions{Area: area, IncludeArchived: includeArchived}, time.Now())
	if err != nil {
		return reviewartifact.QueueReport{}, err
	}
	decisions.AnnotateLifecycleRepairQueue(&report)
	decisions.AnnotateAutonomyGrantMintQueue(&report, homePath)
	product.AnnotateReviewQueue(&report)
	product.AnnotateOutboundDryRunEvidenceQueue(&report)
	authority.AnnotateJobHealthQueue(&report)
	authority.AnnotateSourceControlCleanupQueue(&report)
	runtimebinding.AnnotateRepairQueue(&report)
	capgraph.AnnotateCapabilityEvaluationQueue(&report)
	capgraph.AnnotateExternalRepoIntakeQueue(&report)
	capgraph.AnnotateOutboundDistributionVerdictQueue(&report)
	capgraph.AnnotateOutboundDistributionDryRunQueue(&report)
	capgraph.AnnotateSkillCandidateQueue(&report)
	capgraph.AnnotateCapabilityLifecycleProposalQueue(&report)
	distiller.AnnotateImprovementQueue(&report)
	distiller.AnnotateRequestQueue(&report)
	mem.AnnotateBakeoffQueue(&report)
	observation.AnnotateReviewQueue(&report)
	panel.AnnotateVerdictQueue(&report)
	panel.AnnotateCapabilityFeedbackQueue(&report)
	projection.AnnotateProjectionRegenerationQueue(&report)
	routeevidence.AnnotatePromotionReviewQueue(&report)
	routeevidence.AnnotateRouteOutcomeQueue(&report)
	sourceobs.AnnotateObservationVerdictQueue(&report)
	annotateSynthesisReviewQueue(&report)
	reviewartifact.Recount(&report)
	return report, nil
}

func buildWorkbenchReviewQueue(report reviewartifact.QueueReport) workbenchReviewQueue {
	out := workbenchReviewQueue{
		SchemaVersion: reviewartifact.QueueSchemaVersion,
		AuthorityRef:  "compound://reviews",
		Area:          report.Area,
		Counts:        report.Counts,
	}
	for _, item := range report.Items {
		if len(out.Actionable) >= 5 {
			break
		}
		if len(item.Actions) == 0 && !workbenchReviewReadinessActionable(item.Readiness) {
			continue
		}
		out.Actionable = append(out.Actionable, workbenchReviewItem{
			Area:       item.Area,
			ID:         item.ID,
			Status:     item.Status,
			Readiness:  item.Readiness,
			NextAction: item.NextAction,
			Actions:    item.Actions,
		})
	}
	return out
}

// applyAttentionMeter is the ONE meter seam (packet W1e, work_246942fca52c):
// it overwrites reviewQueue's independently-computed review_required with
// the attention projection's needs_you count, and adds blocked_on_you as
// additive detail, so workbench never reports a different "what needs the
// operator" total than `compound attention`. attention.Build itself never
// errors; the only failure mode is an unreadable executor source feeding its
// Inputs (e.g. a corrupt hook-events or route-evidence file), surfaced here
// as a *work.Store.ExecutorsProjection error. On that path this function
// sets AttentionUnavailable and returns without touching reviewQueue.Counts
// again — the review-artifact-derived value already in Counts.ReviewRequired
// stands as the last-known count. It never falls back to recomputing an
// independent total silently; the flag makes the degradation visible.
func applyAttentionMeter(reviewQueue *workbenchReviewQueue, store *work.Store, reviews reviewartifact.QueueReport, bench work.WorkbenchReport, now time.Time) {
	executors, err := store.ExecutorsProjection(now)
	if err != nil {
		reviewQueue.AttentionUnavailable = true
		return
	}
	report := attention.Build(attention.Inputs{
		Reviews:   &reviews,
		Workbench: &bench,
		Executors: &executors,
	}, now)
	reviewQueue.Counts.ReviewRequired = report.Counts.NeedsYou
	reviewQueue.BlockedOnYou = report.Counts.BlockedOnYou
}

func workbenchReviewReadinessActionable(readiness string) bool {
	switch readiness {
	case "needs_review", "ready_to_record", "ready_to_apply", "blocked":
		return true
	default:
		return false
	}
}
