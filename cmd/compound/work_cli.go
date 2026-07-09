package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/policy"
	"compound/internal/work"
)

func workUsage(sub string) string {
	switch sub {
	case "admit-plan":
		return "usage: compound work admit-plan --plan <create-plan.json> [--by A] [--json]"
	case "review-card-plan":
		return "usage: compound work review-card-plan --card <workbench_review_card_id> [--actor A] [--json]"
	case "create":
		return "usage: compound work create [--work-id ID] --owner O --predicate P [--outcome O] [--by A] [--state proposed|ready|running] [--context-pack ctx] [--canonical-surface S] [--route-decision-id R] [--risk tier1] [--privacy private]"
	case "import-packet":
		return "usage: compound work import-packet <packet.md> [--owner O] [--by A] [--state proposed|ready] [--context-pack ctx] [--risk tier1] [--privacy private]"
	case "leases":
		return "usage: compound work leases [--json]"
	default:
		return "usage: compound work <create-plan|admit-plan|review-card-plan|create|import-packet|show|list|leases|active-packet-proof|proof-chain-doctor|continue|dispatch-plan|dispatch-claim|dispatch-advance|dispatch-close-plan|dispatch-close|dispatch-step|claim-plan|claim|advance-plan|advance|observe-run|action-plan|act|close-plan|close> ..."
	}
}

func cmdWork(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, workUsage(""))
		os.Exit(2)
	}
	if hasUsageFlag(args) {
		fmt.Println(workUsage(args[0]))
		return
	}
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
	sub, rest := args[0], args[1:]
	flag := func(name string) string {
		for i := 0; i < len(rest); i++ {
			if rest[i] == name && i+1 < len(rest) {
				return rest[i+1]
			}
		}
		return ""
	}
	has := func(name string) bool {
		for _, a := range rest {
			if a == name {
				return true
			}
		}
		return false
	}
	pos := func() string {
		for _, a := range rest {
			if !strings.HasPrefix(a, "-") {
				return a
			}
		}
		return ""
	}
	die := func(e error) {
		if e != nil {
			fmt.Fprintln(os.Stderr, "error:", e)
			os.Exit(1)
		}
	}

	switch sub {
	case "create-plan":
		plan, err := store.CreatePlan(work.CreateInput{
			WorkID:           flag("--work-id"),
			Owner:            flag("--owner"),
			Actor:            firstNonEmpty(flag("--by"), flag("--actor")),
			CanonicalSurface: flag("--canonical-surface"),
			SuccessPredicate: flag("--predicate"),
			DesiredOutcome:   flag("--outcome"),
			NonGoals:         flagCSVValues(rest, "--non-goal", "--non-goals"),
			EvidenceRefs:     flagCSVValues(rest, "--evidence-ref", "--evidence"),
			ContextPackID:    flag("--context-pack"),
			RouteDecisionID:  flag("--route-decision-id"),
			State:            flag("--state"),
			Risk:             flag("--risk"),
			Privacy:          flag("--privacy"),
		}, time.Now(), work.CreatePlanOptions{
			Actor:         firstNonEmpty(flag("--by"), flag("--actor")),
			Capability:    flag("--capability"),
			Action:        flag("--action"),
			MutationClass: flag("--mutation-class"),
			Reversibility: flag("--reversibility"),
			BlastRadius:   flag("--blast-radius"),
			TargetKind:    flag("--target-kind"),
			TargetID:      flag("--target-id"),
			TargetPath:    flag("--target-path"),
			Record:        !has("--dry-run"),
		})
		die(err)
		if has("--json") {
			printJSON(plan)
			return
		}
		fmt.Printf("create-plan %s status=%s work=%s policy=%s allowed=%v\n", plan.ID, plan.Status, plan.WorkID, plan.Policy.Outcome, plan.Policy.Allowed)
		if plan.Policy.StandingGrantID != "" {
			fmt.Printf("standing_grant: %s\n", plan.Policy.StandingGrantID)
		}
		if len(plan.CreateCommand) > 0 {
			fmt.Printf("command: %s\n", strings.Join(plan.CreateCommand, " "))
		}
		if plan.NoAction != "" {
			fmt.Printf("no_action: %s\n", plan.NoAction)
		}
		if plan.PlanPath != "" {
			fmt.Printf("plan: %s\n", plan.PlanPath)
		}
	case "admit-plan":
		planPath := firstNonEmpty(flag("--plan"), pos())
		if planPath == "" {
			fmt.Fprintln(os.Stderr, workUsage("admit-plan"))
			os.Exit(2)
		}
		report, err := store.AdmitCreatePlanPath(planPath, time.Now(), work.AdmitCreatePlanOptions{
			Actor: firstNonEmpty(flag("--by"), flag("--actor")),
		})
		die(err)
		if has("--json") {
			printJSON(report)
			return
		}
		fmt.Printf("admit-plan %s status=%s work=%s state=%s policy=%s allowed=%v\n", report.PlanID, report.Status, report.WorkID, report.WorkState, report.PolicyOutcome, report.PolicyAllowed)
		if report.NoAction != "" {
			fmt.Printf("no_action: %s\n", report.NoAction)
		}
		if report.PlanPath != "" {
			fmt.Printf("plan: %s\n", report.PlanPath)
		}
	case "review-card-plan":
		cardID := firstNonEmpty(flag("--card"), pos())
		if cardID == "" {
			fmt.Fprintln(os.Stderr, workUsage("review-card-plan"))
			os.Exit(2)
		}
		report, err := store.BuildReviewCardWorkPlan(cardID, time.Now(), work.ReviewCardWorkPlanOptions{
			Actor: firstNonEmpty(flag("--actor"), flag("--by")),
		})
		die(err)
		if has("--json") {
			printJSON(report)
			return
		}
		fmt.Printf("review-card-plan %s status=%s work=%s card=%s kind=%s\n", report.WorkPlan.ID, report.Status, report.WorkPlan.WorkID, report.ReviewCard.CardID, report.ReviewCard.CardKind)
		if report.ReviewCard.ConditionStatus != "" {
			fmt.Printf("condition_status: %s evidence=%s\n", report.ReviewCard.ConditionStatus, report.ReviewCard.TriggeringEvidenceRef)
		}
		if report.ReviewCard.NextAction != "" {
			fmt.Printf("next_action: %s\n", report.ReviewCard.NextAction)
		}
		fmt.Printf("action_context: %s\n", strings.Join(report.ReviewCard.ActionCommand, " "))
		if len(report.ReviewCard.RequiredInputs) > 0 {
			fmt.Printf("required_inputs: %s\n", strings.Join(report.ReviewCard.RequiredInputs, " | "))
		}
		if report.WorkPlan.PlanPath != "" {
			fmt.Printf("plan: %s\n", report.WorkPlan.PlanPath)
		}
	case "create":
		w, err := store.Create(work.CreateInput{
			WorkID:           flag("--work-id"),
			Owner:            flag("--owner"),
			Actor:            firstNonEmpty(flag("--by"), flag("--actor")),
			CanonicalSurface: flag("--canonical-surface"),
			SuccessPredicate: flag("--predicate"),
			DesiredOutcome:   flag("--outcome"),
			NonGoals:         flagCSVValues(rest, "--non-goal", "--non-goals"),
			EvidenceRefs:     flagCSVValues(rest, "--evidence-ref", "--evidence"),
			ContextPackID:    flag("--context-pack"),
			RouteDecisionID:  flag("--route-decision-id"),
			State:            flag("--state"),
			Risk:             flag("--risk"),
			Privacy:          flag("--privacy"),
		}, time.Now())
		die(err)
		fmt.Printf("created %s (%s)\n", w.ID, w.State)
		printJSON(w)
	case "import-packet":
		packetPath := pos()
		if packetPath == "" {
			fmt.Fprintln(os.Stderr, "usage: compound work import-packet <packet.md> [--owner O] [--by A] [--state proposed|ready] [--context-pack ctx] [--risk tier1] [--privacy private]")
			os.Exit(2)
		}
		report, err := store.ImportPacket(packetPath, work.PacketImportOptions{
			Owner:         flag("--owner"),
			Actor:         firstNonEmpty(flag("--by"), flag("--actor")),
			State:         flag("--state"),
			Risk:          flag("--risk"),
			Privacy:       flag("--privacy"),
			ContextPackID: flag("--context-pack"),
		}, time.Now())
		die(err)
		fmt.Printf("imported packet %q -> %s created=%v state=%s\n", report.PacketTitle, report.Work.ID, report.Created, report.Work.State)
		printJSON(report)
	case "show":
		w, evs, err := store.Show(pos())
		die(err)
		printJSON(w)
		fmt.Println("--- lifecycle ---")
		for _, e := range evs {
			fmt.Printf("%s -> %s  %s %s\n", emptyDash(e.From), e.To, e.By, e.Note)
		}
	case "list":
		ws, err := store.List()
		die(err)
		for _, w := range ws {
			fmt.Printf("%s  %-10s %s\n", w.ID, w.State, w.SuccessPredicate)
		}
	case "leases":
		usage := "usage: compound work leases [--json]"
		for _, arg := range rest {
			if arg != "--json" {
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
		}
		leases, err := store.ListLeases()
		die(err)
		if has("--json") {
			printJSON(struct {
				SchemaVersion string       `json:"schema_version"`
				AuthorityRef  string       `json:"authority_ref"`
				Leases        []work.Lease `json:"leases"`
			}{
				SchemaVersion: "compound.work_leases_projection.v0.1",
				AuthorityRef:  "compound://work/leases",
				Leases:        leases,
			})
			return
		}
		fmt.Printf("# work leases\n\n")
		for _, lease := range leases {
			fmt.Printf("- lease: %s by=%s claimed_at=%s\n", lease.WorkID, lease.By, lease.ClaimAt)
		}
	case "active-packet-proof":
		usage := "usage: compound work active-packet-proof [--surface canonical_surface] [--source-packet packet.md] [--json]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json":
			case "--surface":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			case "--source-packet":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
		}
		activePacketSurface := flag("--surface")
		activePacketExpectedRef := ""
		if sourcePacket := flag("--source-packet"); sourcePacket != "" {
			proof, err := work.PacketSourceProofForPath(sourcePacket)
			die(err)
			activePacketExpectedRef = proof.EvidenceRef
			if strings.TrimSpace(activePacketSurface) == "" {
				activePacketSurface = proof.CanonicalSurface
			}
		}
		report, err := store.ActivePacketProof(time.Now(), activePacketSurface, activePacketExpectedRef)
		die(err)
		if has("--json") {
			printJSON(report)
			return
		}
		fmt.Printf("# work active-packet-proof %s\n", report.Status)
		fmt.Printf("- authority: %s\n", report.AuthorityRef)
		fmt.Printf("- surface: %s\n", report.CanonicalSurface)
		if report.WorkID != "" {
			fmt.Printf("- work: %s state=%s\n", report.WorkID, report.State)
		}
		if report.ContextPackID != "" {
			fmt.Printf("- context_pack: %s\n", report.ContextPackID)
		}
		if report.ReceiptID != "" {
			fmt.Printf("- receipt: %s\n", report.ReceiptID)
		}
		if report.TransitionID != "" {
			fmt.Printf("- transition: %s\n", report.TransitionID)
		}
		for _, ref := range report.PacketEvidenceRefs {
			fmt.Printf("- packet_evidence: %s\n", ref)
		}
		for name, ok := range report.Checks {
			fmt.Printf("- check.%s: %v\n", name, ok)
		}
		for _, item := range report.Integrity {
			fmt.Printf("- integrity: %s\n", item)
		}
		fmt.Printf("- next: %s\n", report.NextAction)
	case "proof-chain-doctor":
		usage := "usage: compound work proof-chain-doctor [--include-archived] [--json]"
		for _, arg := range rest {
			switch arg {
			case "--include-archived", "--json":
			default:
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
		}
		report, err := store.ProofChainDoctor(time.Now(), work.ProofChainDoctorOptions{IncludeArchived: has("--include-archived")})
		die(err)
		if has("--json") {
			printJSON(report)
		} else {
			fmt.Printf("# work proof-chain-doctor %s\n\n", report.Status)
			fmt.Printf("- works_checked: %d\n", report.Counts.WorksChecked)
			fmt.Printf("- terminal_checked: %d\n", report.Counts.TerminalChecked)
			fmt.Printf("- complete: %d\n", report.Counts.Complete)
			if len(report.Findings) > 0 {
				fmt.Printf("- findings: %d\n", len(report.Findings))
				for _, finding := range report.Findings {
					fmt.Printf("- %s %s: %s\n", finding.WorkID, finding.Code, finding.Message)
				}
			}
		}
		if report.Status != "ok" {
			os.Exit(1)
		}
	case "continue":
		usage := "usage: compound work continue [--actor A] [--expect-work work_id] [--outcome completed|failed|cancelled] [--move-type type] [--summary S] [--until-wait] [--max-steps N] [--json]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json", "--until-wait":
			case "--actor", "--expect-work", "--outcome", "--move-type", "--summary", "--max-steps":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
		}
		continueOpts := work.ContinueOptions{
			Actor:        flag("--actor"),
			ExpectWorkID: flag("--expect-work"),
			Outcome:      flag("--outcome"),
			MoveType:     flag("--move-type"),
			Summary:      flag("--summary"),
		}
		maxSteps := 0
		if raw := strings.TrimSpace(flag("--max-steps")); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
			maxSteps = n
		}
		if maxSteps > 0 && !has("--until-wait") {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		if has("--until-wait") {
			result, err := store.ContinueRun(time.Now(), work.ContinueRunOptions{ContinueOptions: continueOpts, MaxSteps: maxSteps})
			die(err)
			if has("--json") {
				printJSON(result)
				return
			}
			fmt.Printf("continue-run %s status=%s steps=%d", result.ID, result.Status, result.StepCount)
			if result.WaitReason != "" {
				fmt.Printf(" wait=%s", result.WaitReason)
			}
			fmt.Println()
			if result.ResultPath != "" {
				fmt.Printf("result: %s\n", result.ResultPath)
			}
			return
		}
		result, err := store.Continue(time.Now(), continueOpts)
		die(err)
		if has("--json") {
			printJSON(result)
			return
		}
		if result.WorkID != "" {
			fmt.Printf("continue %s status=%s action=%s work=%s\n", result.ID, result.Status, result.Action, result.WorkID)
		} else {
			fmt.Printf("continue %s status=%s no_action=%s\n", result.ID, result.Status, result.NoAction)
		}
		if result.ResultPath != "" {
			fmt.Printf("result: %s\n", result.ResultPath)
		}
	case "dispatch-plan":
		usage := "usage: compound work dispatch-plan [--actor A] [--source-packet packet.md preview-only] [--source-state proposed|ready] [--source-owner O] [--source-context-pack ctx] [--source-risk tier1] [--source-privacy private] [--json] [--dry-run]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json", "--dry-run":
			case "--actor", "--source-packet", "--source-state", "--source-owner", "--source-context-pack", "--source-risk", "--source-privacy":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
		}
		plan, err := store.DispatchPlan(time.Now(), work.DispatchPlanOptions{
			Actor:                 flag("--actor"),
			Record:                !has("--dry-run"),
			SourcePacketPath:      flag("--source-packet"),
			SourcePacketState:     flag("--source-state"),
			SourcePacketOwner:     flag("--source-owner"),
			SourcePacketRisk:      flag("--source-risk"),
			SourcePacketPrivacy:   flag("--source-privacy"),
			SourcePacketContextID: flag("--source-context-pack"),
		})
		die(err)
		if has("--json") {
			printJSON(plan)
			return
		}
		if plan.Selected != nil {
			fmt.Printf("dispatch-plan %s status=%s selected=%s action=%s\n", plan.ID, plan.Status, plan.Selected.WorkID, plan.Selected.Action)
			if len(plan.Selected.Command) > 0 {
				fmt.Printf("command: %s\n", strings.Join(plan.Selected.Command, " "))
			}
			if plan.Selected.ActionContext != nil && len(plan.Selected.ActionContext.Command) > 0 {
				fmt.Printf("action_context: %s proof=%s\n", strings.Join(plan.Selected.ActionContext.Command, " "), emptyDash(plan.Selected.ActionContext.Proof))
			}
		} else {
			fmt.Printf("dispatch-plan %s status=%s no_action=%s\n", plan.ID, plan.Status, plan.NoAction)
		}
		if plan.PlanPath != "" {
			fmt.Printf("plan: %s\n", plan.PlanPath)
		}
	case "dispatch-claim":
		usage := "usage: compound work dispatch-claim [--actor A] [--source-packet packet.md] [--source-state proposed|ready] [--source-owner O] [--source-context-pack ctx] [--source-risk tier1] [--source-privacy private] [--json]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json":
			case "--actor", "--source-packet", "--source-state", "--source-owner", "--source-context-pack", "--source-risk", "--source-privacy":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
		}
		result, err := store.DispatchClaim(time.Now(), work.DispatchPlanOptions{
			Actor:                 flag("--actor"),
			SourcePacketPath:      flag("--source-packet"),
			SourcePacketState:     flag("--source-state"),
			SourcePacketOwner:     flag("--source-owner"),
			SourcePacketRisk:      flag("--source-risk"),
			SourcePacketPrivacy:   flag("--source-privacy"),
			SourcePacketContextID: flag("--source-context-pack"),
		})
		die(err)
		if has("--json") {
			printJSON(result)
			return
		}
		if result.ClaimedWorkID != "" {
			fmt.Printf("dispatch-claim %s status=%s claimed=%s policy=%s\n", result.ID, result.Status, result.ClaimedWorkID, result.PolicyDecisionID)
		} else {
			fmt.Printf("dispatch-claim %s status=%s no_action=%s\n", result.ID, result.Status, result.NoAction)
		}
		if result.DispatchPlanPath != "" {
			fmt.Printf("plan: %s\n", result.DispatchPlanPath)
		}
		if result.ResultPath != "" {
			fmt.Printf("result: %s\n", result.ResultPath)
		}
	case "dispatch-advance":
		usage := "usage: compound work dispatch-advance [--actor A] [--json]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json":
			case "--actor":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
		}
		result, err := store.DispatchAdvance(time.Now(), work.DispatchPlanOptions{Actor: flag("--actor")})
		die(err)
		if has("--json") {
			printJSON(result)
			return
		}
		if result.AdvancedWorkID != "" {
			fmt.Printf("dispatch-advance %s status=%s advanced=%s policy=%s\n", result.ID, result.Status, result.AdvancedWorkID, result.PolicyDecisionID)
		} else {
			fmt.Printf("dispatch-advance %s status=%s no_action=%s\n", result.ID, result.Status, result.NoAction)
		}
		if result.ResultPath != "" {
			fmt.Printf("result: %s\n", result.ResultPath)
		}
	case "dispatch-close-plan":
		usage := "usage: compound work dispatch-close-plan [--actor A] [--outcome completed|failed|cancelled] [--move-type type] [--capability C] [--action A] [--mutation-class C] [--reversibility R] [--blast-radius B] [--target-kind K] [--target-id ID] [--target-path P] [--json]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json":
			case "--actor", "--outcome", "--move-type", "--capability", "--action", "--mutation-class", "--reversibility", "--blast-radius", "--target-kind", "--target-id", "--target-path":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
		}
		result, err := store.DispatchClosePlan(time.Now(), work.DispatchClosePlanOptions{
			Actor:         flag("--actor"),
			Outcome:       flag("--outcome"),
			MoveType:      flag("--move-type"),
			Capability:    flag("--capability"),
			Action:        flag("--action"),
			MutationClass: flag("--mutation-class"),
			Reversibility: flag("--reversibility"),
			BlastRadius:   flag("--blast-radius"),
			TargetKind:    flag("--target-kind"),
			TargetID:      flag("--target-id"),
			TargetPath:    flag("--target-path"),
		})
		die(err)
		if has("--json") {
			printJSON(result)
			return
		}
		if result.ClosePlanID != "" && result.Selected != nil {
			fmt.Printf("dispatch-close-plan %s status=%s selected=%s close_plan=%s\n", result.ID, result.Status, result.Selected.WorkID, result.ClosePlanID)
		} else {
			fmt.Printf("dispatch-close-plan %s status=%s no_action=%s\n", result.ID, result.Status, result.NoAction)
		}
		if result.ClosePlanPath != "" {
			fmt.Printf("close_plan: %s\n", result.ClosePlanPath)
		}
		if result.ResultPath != "" {
			fmt.Printf("result: %s\n", result.ResultPath)
		}
	case "dispatch-close":
		usage := "usage: compound work dispatch-close [--actor A] [--outcome completed|failed|cancelled] [--move-type type] [--summary S] [--json]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json":
			case "--actor", "--outcome", "--move-type", "--summary":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
		}
		result, err := store.DispatchClose(time.Now(), work.DispatchCloseOptions{
			Actor:    flag("--actor"),
			Outcome:  flag("--outcome"),
			MoveType: flag("--move-type"),
			Summary:  flag("--summary"),
		})
		die(err)
		if has("--json") {
			printJSON(result)
			return
		}
		if result.ClosedWorkID != "" {
			fmt.Printf("dispatch-close %s status=%s closed=%s receipt=%s transition=%s\n", result.ID, result.Status, result.ClosedWorkID, result.ReceiptID, result.TransitionID)
		} else {
			fmt.Printf("dispatch-close %s status=%s no_action=%s\n", result.ID, result.Status, result.NoAction)
		}
		if result.DispatchClosePlanPath != "" {
			fmt.Printf("dispatch_close_plan: %s\n", result.DispatchClosePlanPath)
		}
		if result.ResultPath != "" {
			fmt.Printf("result: %s\n", result.ResultPath)
		}
	case "dispatch-step":
		usage := "usage: compound work dispatch-step [--actor A] [--source-packet packet.md] [--source-state proposed|ready] [--source-owner O] [--source-context-pack ctx] [--source-risk tier1] [--source-privacy private] [--outcome completed|failed|cancelled] [--move-type type] [--summary S] [--until-wait] [--max-steps N] [--json]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json", "--until-wait":
			case "--actor", "--source-packet", "--source-state", "--source-owner", "--source-context-pack", "--source-risk", "--source-privacy", "--outcome", "--move-type", "--summary", "--max-steps":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
		}
		stepOpts := work.DispatchStepOptions{
			Actor:                 flag("--actor"),
			Outcome:               flag("--outcome"),
			MoveType:              flag("--move-type"),
			Summary:               flag("--summary"),
			SourcePacketPath:      flag("--source-packet"),
			SourcePacketState:     flag("--source-state"),
			SourcePacketOwner:     flag("--source-owner"),
			SourcePacketRisk:      flag("--source-risk"),
			SourcePacketPrivacy:   flag("--source-privacy"),
			SourcePacketContextID: flag("--source-context-pack"),
		}
		maxSteps := 0
		if raw := strings.TrimSpace(flag("--max-steps")); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
			maxSteps = n
		}
		if maxSteps > 0 && !has("--until-wait") {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		if has("--until-wait") {
			result, err := store.DispatchStepRun(time.Now(), work.DispatchStepRunOptions{DispatchStepOptions: stepOpts, MaxSteps: maxSteps})
			die(err)
			if has("--json") {
				printJSON(result)
				return
			}
			fmt.Printf("dispatch-step-run %s status=%s steps=%d", result.ID, result.Status, result.StepCount)
			if result.WaitReason != "" {
				fmt.Printf(" wait=%s", result.WaitReason)
			}
			fmt.Println()
			if result.ResultPath != "" {
				fmt.Printf("result: %s\n", result.ResultPath)
			}
			return
		}
		result, err := store.DispatchStep(time.Now(), stepOpts)
		die(err)
		if has("--json") {
			printJSON(result)
			return
		}
		if result.WorkID != "" {
			fmt.Printf("dispatch-step %s status=%s action=%s work=%s\n", result.ID, result.Status, result.Action, result.WorkID)
		} else {
			fmt.Printf("dispatch-step %s status=%s no_action=%s\n", result.ID, result.Status, result.NoAction)
		}
		if result.ResultPath != "" {
			fmt.Printf("result: %s\n", result.ResultPath)
		}
	case "claim-plan":
		usage := "usage: compound work claim-plan <work_id> [--actor A] [--capability C] [--action A] [--mutation-class C] [--reversibility R] [--blast-radius B] [--target-kind K] [--target-id ID] [--target-path P] [--json] [--dry-run]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json", "--dry-run":
			case "--actor", "--capability", "--action", "--mutation-class", "--reversibility", "--blast-radius", "--target-kind", "--target-id", "--target-path":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				if strings.HasPrefix(rest[i], "-") {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
			}
		}
		if pos() == "" {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		plan, err := store.ClaimPlan(pos(), time.Now(), work.ClaimPlanOptions{
			Actor:         flag("--actor"),
			Capability:    flag("--capability"),
			Action:        flag("--action"),
			MutationClass: flag("--mutation-class"),
			Reversibility: flag("--reversibility"),
			BlastRadius:   flag("--blast-radius"),
			TargetKind:    flag("--target-kind"),
			TargetID:      flag("--target-id"),
			TargetPath:    flag("--target-path"),
			Record:        !has("--dry-run"),
		})
		die(err)
		if has("--json") {
			printJSON(plan)
			return
		}
		fmt.Printf("claim-plan %s status=%s work=%s policy=%s allowed=%v\n", plan.ID, plan.Status, plan.WorkID, plan.Policy.Outcome, plan.Policy.Allowed)
		if plan.Policy.StandingGrantID != "" {
			fmt.Printf("standing_grant: %s\n", plan.Policy.StandingGrantID)
		}
		if len(plan.ClaimCommand) > 0 {
			fmt.Printf("command: %s\n", strings.Join(plan.ClaimCommand, " "))
		}
		if plan.ActionContext != nil && len(plan.ActionContext.Command) > 0 {
			fmt.Printf("action_context: %s proof=%s\n", strings.Join(plan.ActionContext.Command, " "), emptyDash(plan.ActionContext.Proof))
		}
		if plan.NoAction != "" {
			fmt.Printf("no_action: %s\n", plan.NoAction)
		}
		if plan.PlanPath != "" {
			fmt.Printf("plan: %s\n", plan.PlanPath)
		}
	case "claim":
		w, err := store.Claim(pos(), flag("--by"), time.Now())
		die(err)
		fmt.Printf("%s claimed by %s (%s) policy=%s\n", w.ID, w.Authority.Owner, w.State, w.PolicyDecisionID)
	case "advance-plan":
		usage := "usage: compound work advance-plan <work_id> --to <state> [--actor A] [--capability C] [--action A] [--mutation-class C] [--reversibility R] [--blast-radius B] [--target-kind K] [--target-id ID] [--target-path P] [--json] [--dry-run]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json", "--dry-run":
			case "--to", "--actor", "--capability", "--action", "--mutation-class", "--reversibility", "--blast-radius", "--target-kind", "--target-id", "--target-path":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				if strings.HasPrefix(rest[i], "-") {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
			}
		}
		if pos() == "" || flag("--to") == "" {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		plan, err := store.AdvancePlan(pos(), time.Now(), work.AdvancePlanOptions{
			Actor:         flag("--actor"),
			To:            flag("--to"),
			Capability:    flag("--capability"),
			Action:        flag("--action"),
			MutationClass: flag("--mutation-class"),
			Reversibility: flag("--reversibility"),
			BlastRadius:   flag("--blast-radius"),
			TargetKind:    flag("--target-kind"),
			TargetID:      flag("--target-id"),
			TargetPath:    flag("--target-path"),
			Record:        !has("--dry-run"),
		})
		die(err)
		if has("--json") {
			printJSON(plan)
			return
		}
		fmt.Printf("advance-plan %s status=%s work=%s to=%s policy=%s allowed=%v\n", plan.ID, plan.Status, plan.WorkID, plan.ToState, plan.Policy.Outcome, plan.Policy.Allowed)
		if plan.Policy.StandingGrantID != "" {
			fmt.Printf("standing_grant: %s\n", plan.Policy.StandingGrantID)
		}
		if len(plan.AdvanceCommand) > 0 {
			fmt.Printf("command: %s\n", strings.Join(plan.AdvanceCommand, " "))
		}
		if plan.ActionContext != nil && len(plan.ActionContext.Command) > 0 {
			fmt.Printf("action_context: %s proof=%s\n", strings.Join(plan.ActionContext.Command, " "), emptyDash(plan.ActionContext.Proof))
		}
		if plan.NoAction != "" {
			fmt.Printf("no_action: %s\n", plan.NoAction)
		}
		if plan.PlanPath != "" {
			fmt.Printf("plan: %s\n", plan.PlanPath)
		}
	case "advance":
		w, err := store.Advance(pos(), work.AdvanceInput{
			To:             flag("--to"),
			Actor:          firstNonEmpty(flag("--by"), flag("--actor")),
			RunID:          flag("--run"),
			ArtifactID:     flag("--artifact"),
			VerificationID: flag("--verification"),
			ContextPackID:  flag("--context-pack"),
			Note:           flag("--note"),
			Steal:          has("--steal"),
			StealReason:    flag("--reason"),
		}, time.Now())
		die(err)
		fmt.Printf("%s -> %s policy=%s\n", w.ID, w.State, w.PolicyDecisionID)
	case "observe-run":
		workID := pos()
		if workID == "" || flag("--trace") == "" {
			fmt.Fprintln(os.Stderr, "usage: compound work observe-run <work_id> --trace <trace_id> [--actor A] [--note N] [--json]")
			os.Exit(2)
		}
		result, err := store.ObserveRun(workID, work.ObserveRunInput{
			Actor:   firstNonEmpty(flag("--by"), flag("--actor")),
			TraceID: flag("--trace"),
			Note:    flag("--note"),
		}, time.Now())
		die(err)
		if has("--json") {
			printJSON(result)
			return
		}
		fmt.Printf("%s observed trace=%s state=%s policy=%s\n", result.WorkID, result.TraceID, result.State, result.PolicyDecisionID)
		if result.ActionContext != nil && len(result.ActionContext.Command) > 0 {
			fmt.Printf("action_context: %s proof=%s\n", strings.Join(result.ActionContext.Command, " "), emptyDash(result.ActionContext.Proof))
		}
		if result.ResultPath != "" {
			fmt.Printf("result: %s\n", result.ResultPath)
		}
	case "action-plan":
		usage := "usage: compound work action-plan <work_id> [--actor A] [--move-type type] [--receipt-outcome completed|failed|blocked|no_op] [--capability C] [--action A] [--mutation-class C] [--reversibility R] [--blast-radius B] [--target-kind K] [--target-id ID] [--target-path P] [--json] [--dry-run]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json", "--dry-run":
			case "--actor", "--move-type", "--receipt-outcome", "--capability", "--action", "--mutation-class", "--reversibility", "--blast-radius", "--target-kind", "--target-id", "--target-path":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				if strings.HasPrefix(rest[i], "-") {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
			}
		}
		if pos() == "" {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		plan, err := store.ActionExecutionPlan(pos(), time.Now(), work.ActionExecutionPlanOptions{
			Actor:          flag("--actor"),
			Capability:     flag("--capability"),
			Action:         flag("--action"),
			MutationClass:  flag("--mutation-class"),
			Reversibility:  flag("--reversibility"),
			BlastRadius:    flag("--blast-radius"),
			TargetKind:     flag("--target-kind"),
			TargetID:       flag("--target-id"),
			TargetPath:     flag("--target-path"),
			MoveType:       flag("--move-type"),
			ReceiptOutcome: flag("--receipt-outcome"),
			Record:         !has("--dry-run"),
		})
		die(err)
		if has("--json") {
			printJSON(plan)
			return
		}
		fmt.Printf("action-plan %s status=%s work=%s state=%s policy=%s allowed=%v\n", plan.ID, plan.Status, plan.WorkID, plan.WorkState, plan.Policy.Outcome, plan.Policy.Allowed)
		if plan.ActionSummary != "" {
			fmt.Printf("action_summary: %s\n", plan.ActionSummary)
		}
		if len(plan.ActionCommand) > 0 {
			fmt.Printf("action_command: %s\n", strings.Join(plan.ActionCommand, " "))
		}
		if len(plan.RequiredInputs) > 0 {
			fmt.Printf("required_inputs: %s\n", strings.Join(plan.RequiredInputs, ", "))
		}
		if len(plan.ExpectedValidation) > 0 {
			fmt.Printf("expected_validation: %s\n", strings.Join(plan.ExpectedValidation, ", "))
		}
		if len(plan.StopConditions) > 0 {
			fmt.Printf("stop_conditions: %s\n", strings.Join(plan.StopConditions, ", "))
		}
		if plan.Boundary != "" {
			fmt.Printf("boundary: %s\n", plan.Boundary)
		}
		if plan.Proof != "" {
			fmt.Printf("proof: %s\n", plan.Proof)
		}
		if len(plan.RecordCommand) > 0 {
			fmt.Printf("record_after_execution: %s\n", strings.Join(plan.RecordCommand, " "))
		}
		if len(plan.CloseCommand) > 0 {
			fmt.Printf("close_after_validation: %s\n", strings.Join(plan.CloseCommand, " "))
		}
		if plan.NoAction != "" {
			fmt.Printf("no_action: %s\n", plan.NoAction)
		}
		if plan.PlanPath != "" {
			fmt.Printf("plan: %s\n", plan.PlanPath)
		}
	case "act":
		workID := pos()
		intentFile := flag("--intent")
		planFile := flag("--plan")
		if workID == "" || (intentFile == "" && planFile == "") || (intentFile != "" && planFile != "") {
			fmt.Fprintln(os.Stderr, "usage: compound work act <work_id> (--intent <action-intent.json> | --plan <action-execution-plan.json>)")
			os.Exit(2)
		}
		var ai envelope.ActionIntent
		var pd envelope.PolicyDecision
		if planFile != "" {
			plan, err := work.LoadActionExecutionPlan(planFile)
			die(err)
			if plan.WorkID != workID {
				die(fmt.Errorf("action plan belongs to work %s, not %s", plan.WorkID, workID))
			}
			ai = plan.ActionIntent
			pd = plan.Decision
		} else {
			ai = loadActionIntent(intentFile)
			ai.IDs.WorkID = workID // bind the action to this work
			pd = policy.Evaluate(ai, policyConfig(), time.Now())
		}
		printJSON(pd)
		if !policy.Allowed(pd) {
			fmt.Fprintf(os.Stderr, "BLOCKED: policy %s — action NOT recorded\n", pd.Outcome)
			os.Exit(gateExit(pd))
		}
		w, err := store.RecordAction(workID, ai, pd, time.Now())
		die(err)
		fmt.Printf("%s: action %s recorded (policy %s, decision %s)\n", w.ID, ai.ID, pd.Outcome, pd.ID)
	case "close-plan":
		usage := "usage: compound work close-plan <work_id> (--action-plan <action-execution-plan.json> | --outcome completed|failed|cancelled --move-type <type>) [--actor A] [--capability C] [--action A] [--mutation-class C] [--reversibility R] [--blast-radius B] [--target-kind K] [--target-id ID] [--target-path P] [--steal --reason R] [--json] [--dry-run]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json", "--dry-run", "--steal":
			case "--outcome", "--move-type", "--actor", "--action-plan", "--capability", "--action", "--mutation-class", "--reversibility", "--blast-radius", "--target-kind", "--target-id", "--target-path", "--reason":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				if strings.HasPrefix(rest[i], "-") {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
			}
		}
		if pos() == "" || (flag("--action-plan") == "" && (flag("--outcome") == "" || flag("--move-type") == "")) {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		plan, err := store.ClosePlan(pos(), time.Now(), work.ClosePlanOptions{
			Actor:          flag("--actor"),
			Outcome:        flag("--outcome"),
			MoveType:       flag("--move-type"),
			ActionPlanPath: flag("--action-plan"),
			Capability:     flag("--capability"),
			Action:         flag("--action"),
			MutationClass:  flag("--mutation-class"),
			Reversibility:  flag("--reversibility"),
			BlastRadius:    flag("--blast-radius"),
			TargetKind:     flag("--target-kind"),
			TargetID:       flag("--target-id"),
			TargetPath:     flag("--target-path"),
			Record:         !has("--dry-run"),
			Steal:          has("--steal"),
			StealReason:    flag("--reason"),
		})
		die(err)
		if has("--json") {
			printJSON(plan)
			return
		}
		fmt.Printf("close-plan %s status=%s work=%s outcome=%s move_type=%s policy=%s allowed=%v\n", plan.ID, plan.Status, plan.WorkID, plan.Outcome, plan.MoveType, plan.Policy.Outcome, plan.Policy.Allowed)
		if plan.Policy.StandingGrantID != "" {
			fmt.Printf("standing_grant: %s\n", plan.Policy.StandingGrantID)
		}
		if len(plan.CloseCommand) > 0 {
			fmt.Printf("command: %s\n", strings.Join(plan.CloseCommand, " "))
		}
		if len(plan.ProofRefs) > 0 {
			fmt.Printf("proof_refs: %s\n", strings.Join(plan.ProofRefs, ","))
		}
		if len(plan.RunObservations) > 0 {
			fmt.Printf("run_observations: %d\n", len(plan.RunObservations))
		}
		if plan.ActionExecutionPlanID != "" {
			fmt.Printf("action_execution_plan: %s\n", plan.ActionExecutionPlanID)
		}
		if len(plan.ExpectedValidation) > 0 {
			fmt.Printf("expected_validation: %s\n", strings.Join(plan.ExpectedValidation, ", "))
		}
		if plan.NoAction != "" {
			fmt.Printf("no_action: %s\n", plan.NoAction)
		}
		if plan.PlanPath != "" {
			fmt.Printf("plan: %s\n", plan.PlanPath)
		}
	case "close":
		closeUsage := "usage: compound work close <work_id> --outcome completed|failed|cancelled --move-type <type> [--summary S] [--changed] [--by A|--actor A] [--proof REF[,REF]...] [--proof-ref REF] [--used C] [--missed C] [--false-triggered C] [--failure-surface S] [--failure-mechanism M] [--memory-candidate C] [--memory-promoted C] [--memory-parked C] [--memory-rejected C] [--steal --reason R]\n  (passing --used at all — even --used \"\" for declared-none — stamps skills.used_declared=true, so injected-but-unused refs count as ignored; omitting --used leaves them unknown)\n  (a work held by another actor's lease refuses close unless --by/--actor matches the holder, or --steal --reason \"...\" is given)"
		if err := validateKnownFlags(rest, map[string]bool{
			"--outcome":           true,
			"--move-type":         true,
			"--summary":           true,
			"--by":                true,
			"--actor":             true,
			"--used":              true,
			"--missed":            true,
			"--false-triggered":   true,
			"--failure-surface":   true,
			"--failure-mechanism": true,
			"--proof":             true,
			"--proof-ref":         true,
			"--memory-candidate":  true,
			"--memory-promoted":   true,
			"--memory-parked":     true,
			"--memory-rejected":   true,
			"--reason":            true,
		}, map[string]bool{
			"--changed": true,
			"--steal":   true,
		}); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			fmt.Fprintln(os.Stderr, closeUsage)
			os.Exit(2)
		}
		res, err := store.Close(pos(), work.CloseInput{
			Outcome:          flag("--outcome"),
			MoveType:         flag("--move-type"),
			Actor:            firstNonEmpty(flag("--by"), flag("--actor")),
			Summary:          flag("--summary"),
			Changed:          has("--changed"),
			UsedCapabilities: splitCSV(flag("--used")),
			// Declared iff --used was EXPLICITLY passed (even --used "" = declared-none). Omitting
			// the flag leaves used_declared=false, so injected refs stay unknown, not ignored.
			UsedDeclared:               has("--used"),
			MissedCapabilities:         splitCSV(flag("--missed")),
			FalseTriggeredCapabilities: splitCSV(flag("--false-triggered")),
			ProofRefs:                  flagCSVValues(rest, "--proof", "--proof-ref"),
			MemoryCandidates:           splitCSV(flag("--memory-candidate")),
			MemoryPromoted:             splitCSV(flag("--memory-promoted")),
			MemoryParked:               splitCSV(flag("--memory-parked")),
			MemoryRejected:             splitCSV(flag("--memory-rejected")),
			FailureSurfaceOutcome:      flag("--failure-surface"),
			FailureMechanism:           flag("--failure-mechanism"),
			Steal:                      has("--steal"),
			StealReason:                flag("--reason"),
		}, time.Now())
		die(err)
		fmt.Printf("%s closed: %s  move_type=%s  policy=%s  receipt=%s  transition=%s\n",
			res.Work.ID, res.Work.State, res.Receipt.MoveType, res.Work.PolicyDecisionID, res.Receipt.ID, res.Transition.ID)
		printJSON(res)
		autoConsumeCapabilityFeedback(homePath)
	default:
		fmt.Fprintln(os.Stderr, "usage: compound work <create-plan|admit-plan|review-card-plan|create|import-packet|show|list|leases|active-packet-proof|proof-chain-doctor|continue|dispatch-plan|dispatch-claim|dispatch-advance|dispatch-close-plan|dispatch-close|dispatch-step|claim-plan|claim|advance-plan|advance|observe-run|action-plan|act|close-plan|close> ...")
		os.Exit(2)
	}
}
