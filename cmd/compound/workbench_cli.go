package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"compound/internal/authority"
	"compound/internal/capgraph"
	"compound/internal/decisions"
	"compound/internal/distiller"
	"compound/internal/home"
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

const workbenchUsage = "usage: compound workbench [--json] [--recent N] [--actor A] [--active-packet-surface S] [--source-packet packet.md] [--packet-dir D] [--capability-task T] [--capability-harness H] [--capability-limit N] [--route-class C] [--route-privacy-sensitive] [--review-area A] [--include-archived]"

type workbenchCLIOutput struct {
	work.WorkbenchReport
	CapabilitySurface *capgraph.SurfaceReport            `json:"capability_surface,omitempty"`
	RoutePlan         *routeevidence.CapabilityRoutePlan `json:"route_plan,omitempty"`
	ReviewQueue       *workbenchReviewQueue              `json:"review_queue,omitempty"`
}

type workbenchReviewQueue struct {
	SchemaVersion string                     `json:"schema_version"`
	AuthorityRef  string                     `json:"authority_ref"`
	Area          string                     `json:"area,omitempty"`
	Counts        reviewartifact.QueueCounts `json:"counts"`
	Actionable    []workbenchReviewItem      `json:"actionable,omitempty"`
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
	output := workbenchCLIOutput{WorkbenchReport: report, CapabilitySurface: capabilitySurface, RoutePlan: routePlan, ReviewQueue: &reviewQueue}
	if jsonOut {
		printJSON(output)
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

func buildReviewQueueReport(homePath, area string, includeArchived bool) (reviewartifact.QueueReport, error) {
	report, err := reviewartifact.Queue(homePath, reviewartifact.QueueOptions{Area: area, IncludeArchived: includeArchived}, time.Now())
	if err != nil {
		return reviewartifact.QueueReport{}, err
	}
	decisions.AnnotateLifecycleRepairQueue(&report)
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
	distiller.AnnotateImprovementQueue(&report)
	distiller.AnnotateRequestQueue(&report)
	observation.AnnotateReviewQueue(&report)
	panel.AnnotateVerdictQueue(&report)
	panel.AnnotateCapabilityFeedbackQueue(&report)
	projection.AnnotateProjectionRegenerationQueue(&report)
	routeevidence.AnnotatePromotionReviewQueue(&report)
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

func workbenchReviewReadinessActionable(readiness string) bool {
	switch readiness {
	case "needs_review", "ready_to_record", "ready_to_apply", "blocked":
		return true
	default:
		return false
	}
}
