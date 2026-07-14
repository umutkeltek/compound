package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/policy"
	"compound/internal/work"
)

type workShowOutput struct {
	envelope.WorkEnvelope
	PacketPath   string       `json:"packet_path,omitempty"`
	PacketStatus string       `json:"packet_status,omitempty"`
	Lifecycle    []work.Event `json:"lifecycle"`
}

// packetEnrichmentStatus renders a packetPathForWork failure as an explicit,
// non-fatal enrichment note. Packet content is enrichment for `show`, not a
// load-bearing dependency: the envelope itself always renders, and a dangling
// packet pointer (or unconfigured workbench packet dir) becomes a visible
// packet_status note instead of a hard failure, so the operator still learns
// the pointer is dangling without losing the rest of the envelope.
func packetEnrichmentStatus(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("unavailable (%s)", err.Error())
}

func packetPathForWork(w envelope.WorkEnvelope) (string, error) {
	surface := strings.TrimSpace(w.CanonicalSurface)
	if !strings.HasPrefix(surface, "packet:") {
		return "", nil
	}
	packetDir := defaultWorkbenchPacketDir()
	if packetDir == "" {
		return "", fmt.Errorf("packet path unavailable for %s: workbench packet directory is not configured", w.ID)
	}
	offered, err := work.OfferedPackets(packetDir)
	if err != nil {
		return "", fmt.Errorf("resolve workbench packet directory: %w", err)
	}
	digest := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(w.Authority.SourceRevision), "sha256:"))
	matches := make([]work.OfferedPacketItem, 0, 1)
	for _, item := range offered.Items {
		if item.CanonicalSurface != surface {
			continue
		}
		if digest != "" && item.SourceDigest != digest {
			continue
		}
		matches = append(matches, item)
	}
	switch len(matches) {
	case 1:
		path, err := filepath.Abs(matches[0].Path)
		if err != nil {
			return "", fmt.Errorf("resolve current packet path for %s: %w", w.ID, err)
		}
		return path, nil
	case 0:
		return "", fmt.Errorf("packet path unavailable for %s: no current workbench packet matches %s", w.ID, surface)
	default:
		return "", fmt.Errorf("packet path ambiguous for %s: %d current workbench packets match %s", w.ID, len(matches), surface)
	}
}

type workVerbHelp struct {
	Usage   string
	Example string
}

const genericWorkUsage = "usage: compound work <create-plan|admit-plan|review-card-plan|create|import-packet|show|list|leases|active-packet-proof|proof-chain-doctor|continue|dispatch-plan|dispatch-claim|dispatch-advance|dispatch-close-plan|dispatch-close|dispatch-step|claim-plan|claim|advance-plan|advance|observe-run|land-check|action-plan|act|close-plan|close> ..."

var workVerbHelpByName = map[string]workVerbHelp{
	"create-plan": {
		Usage:   "usage: compound work create-plan [--work-id ID] --owner O --predicate P [--outcome O] [--by A] [--state proposed|ready] [--context-pack ctx] [--canonical-surface S] [--route-decision-id R] [--risk tier1] [--privacy private] [--target-worktree-root /abs/repo --target-local-ref refs/heads/main --expected-base-sha SHA (--publication-ref refs/remotes/origin/main|--local-only)] [--json] [--dry-run]",
		Example: "compound work create-plan --owner codex --predicate \"tests pass\" --dry-run",
	},
	"admit-plan": {
		Usage:   "usage: compound work admit-plan --plan <create-plan.json> [--by A] [--json]",
		Example: "compound work admit-plan --plan create-plans/wcp_123.json --by codex",
	},
	"review-card-plan": {
		Usage:   "usage: compound work review-card-plan --card <workbench_review_card_id> [--actor A] [--json]",
		Example: "compound work review-card-plan --card wrc_abc123 --json",
	},
	"create": {
		Usage:   "usage: compound work create [--work-id ID] --owner O --predicate P [--outcome O] [--by A] [--state proposed|ready] [--context-pack ctx] [--canonical-surface S] [--route-decision-id R] [--risk tier1] [--privacy private] [--target-worktree-root /abs/repo --target-local-ref refs/heads/main --expected-base-sha SHA (--publication-ref refs/remotes/origin/main|--local-only)]",
		Example: "compound work create --owner codex --predicate \"tests pass\"",
	},
	"import-packet": {
		Usage:   "usage: compound work import-packet <packet.md> [--owner O] [--by A] [--state proposed|ready] [--context-pack ctx] [--risk tier1] [--privacy private]",
		Example: "compound work import-packet docs_private/plan/packets/example.md --owner codex",
	},
	"show": {
		Usage:   "usage: compound work show <work_id> [--json]",
		Example: "compound work show work_abc123 --json",
	},
	"list": {
		Usage:   "usage: compound work list",
		Example: "compound work list",
	},
	"leases": {
		Usage:   "usage: compound work leases [--json]",
		Example: "compound work leases --json",
	},
	"active-packet-proof": {
		Usage:   "usage: compound work active-packet-proof [--surface canonical_surface] [--source-packet packet.md] [--json]",
		Example: "compound work active-packet-proof --json",
	},
	"proof-chain-doctor": {
		Usage:   "usage: compound work proof-chain-doctor [--include-archived] [--json]",
		Example: "compound work proof-chain-doctor --json",
	},
	"continue": {
		Usage:   "usage: compound work continue [--actor A] [--expect-work work_id] [--outcome completed|failed|cancelled] [--move-type type] [--summary S] [--until-wait] [--max-steps N] [--json]",
		Example: "compound work continue --actor codex --until-wait --json",
	},
	"dispatch-plan": {
		Usage:   "usage: compound work dispatch-plan [--actor A] [--capability-class classify-extract|draft-prose] [--source-packet packet.md] [--source-state proposed|ready] [--source-owner O] [--source-context-pack ctx] [--source-risk tier1] [--source-privacy private] [--json] [--dry-run]",
		Example: "compound work dispatch-plan --actor codex --dry-run --json",
	},
	"dispatch-claim": {
		Usage:   "usage: compound work dispatch-claim [--next|<work_id>] [--confirm] [--actor A] [--source-packet packet.md] [--source-state proposed|ready] [--source-owner O] [--source-context-pack ctx] [--source-risk tier1] [--source-privacy private] [--json]\n  without --confirm previews; with --confirm claims",
		Example: "compound work dispatch-claim --next --actor codex --json",
	},
	"dispatch-advance": {
		Usage:   "usage: compound work dispatch-advance (--next|<work_id>) [--actor A] [--json]",
		Example: "compound work dispatch-advance --next --actor codex --json",
	},
	"dispatch-close-plan": {
		Usage:   "usage: compound work dispatch-close-plan (--next|<work_id>) [--actor A] [--outcome completed|failed|cancelled] [--move-type type] [--capability C] [--action A] [--mutation-class C] [--reversibility R] [--blast-radius B] [--target-kind K] [--target-id ID] [--target-path P] [--json]",
		Example: "compound work dispatch-close-plan --next --outcome completed --move-type outward_delta --json",
	},
	"dispatch-close": {
		Usage:   "usage: compound work dispatch-close (--next|<work_id>) [--actor A] [--outcome completed|failed|cancelled] [--move-type type] [--summary S] [--json]",
		Example: "compound work dispatch-close --next --outcome completed --move-type outward_delta",
	},
	"dispatch-step": {
		Usage:   "usage: compound work dispatch-step [--actor A] [--source-packet packet.md] [--source-state proposed|ready] [--source-owner O] [--source-context-pack ctx] [--source-risk tier1] [--source-privacy private] [--outcome completed|failed|cancelled] [--move-type type] [--summary S] [--until-wait] [--max-steps N] [--json]",
		Example: "compound work dispatch-step --actor codex --until-wait --json",
	},
	"claim-plan": {
		Usage:   "usage: compound work claim-plan <work_id> [--actor A] [--capability C] [--action A] [--mutation-class C] [--reversibility R] [--blast-radius B] [--target-kind K] [--target-id ID] [--target-path P] [--json] [--dry-run]",
		Example: "compound work claim-plan work_abc123 --actor codex --dry-run",
	},
	"claim": {
		Usage:   "usage: compound work claim <work_id> --by A",
		Example: "compound work claim work_abc123 --by codex",
	},
	"advance-plan": {
		Usage:   "usage: compound work advance-plan <work_id> --to <state> [--actor A] [--target-worktree-root /abs/repo --target-local-ref refs/heads/main --expected-base-sha SHA (--publication-ref refs/remotes/origin/main|--local-only)] [--capability C] [--action A] [--mutation-class C] [--reversibility R] [--blast-radius B] [--target-kind K] [--target-id ID] [--target-path P] [--json] [--dry-run]",
		Example: "compound work advance-plan work_abc123 --to running --actor codex --dry-run",
	},
	"advance": {
		Usage:   "usage: compound work advance <work_id> --to <state> [--by A|--actor A] [--run R] [--artifact A] [--verification V] [--context-pack ctx] [--note N] [--steal --reason R] [--target-worktree-root /abs/repo --target-local-ref refs/heads/main --expected-base-sha SHA (--publication-ref refs/remotes/origin/main|--local-only)]",
		Example: "compound work advance work_abc123 --to running --by codex",
	},
	"observe-run": {
		Usage:   "usage: compound work observe-run <work_id> --trace <trace_id> [--actor A] [--note N] [--json]",
		Example: "compound work observe-run work_abc123 --trace trace_xyz --json",
	},
	"land-check": {
		Usage:   "usage: compound work land-check <work_id> --tip <full-sha> [--repo /bound/repo] [--target-ref refs/heads/main] [--actor A] [--dry-run] [--json]",
		Example: "compound work land-check work_abc123 --tip 0123456789012345678901234567890123456789 --json",
	},
	"action-plan": {
		Usage:   "usage: compound work action-plan <work_id> [--actor A] [--move-type type] [--receipt-outcome completed|failed|blocked|no_op] [--capability C] [--action A] [--mutation-class C] [--reversibility R] [--blast-radius B] [--target-kind K] [--target-id ID] [--target-path P] [--json] [--dry-run]",
		Example: "compound work action-plan work_abc123 --dry-run --json",
	},
	"act": {
		Usage:   "usage: compound work act <work_id> (--intent <action-intent.json> | --plan <action-execution-plan.json>)",
		Example: "compound work act work_abc123 --plan action-plans/aep_123.json",
	},
	"close-plan": {
		Usage:   "usage: compound work close-plan <work_id> (--action-plan <action-execution-plan.json> | --outcome completed|failed|cancelled --move-type <type>) [--actor A] [--capability C] [--action A] [--mutation-class C] [--reversibility R] [--blast-radius B] [--target-kind K] [--target-id ID] [--target-path P] [--steal --reason R] [--json] [--dry-run]",
		Example: "compound work close-plan work_abc123 --outcome completed --move-type outward_delta --dry-run",
	},
	"close": {
		Usage:   "usage: compound work close <work_id> --outcome completed|failed|cancelled --move-type <type> [--summary S] [--changed] [--by A|--actor A] [--proof REF[,REF]...] [--proof-ref REF] [--used C] [--missed C] [--false-triggered C] [--failure-surface S] [--failure-mechanism M] [--memory-candidate C] [--memory-promoted C] [--memory-parked C] [--memory-rejected C] [--steal --reason R]",
		Example: "compound work close work_abc123 --outcome completed --move-type outward_delta --summary \"shipped\" --changed",
	},
}

func workUsage(sub string) string {
	if help, ok := workVerbHelpByName[sub]; ok {
		return help.Usage
	}
	return genericWorkUsage
}

func workHelpText(sub string) string {
	help, ok := workVerbHelpByName[sub]
	if !ok {
		return genericWorkUsage
	}
	return help.Usage + "\nexample: " + help.Example
}

func previewDispatchClaim(store *work.Store, now time.Time, opts work.DispatchPlanOptions) (work.DispatchPlan, error) {
	opts.Record = false
	opts.Confirm = false
	opts.ReadOnly = true
	return store.DispatchPlan(now, opts)
}

func cmdWork(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, workUsage(""))
		os.Exit(2)
	}
	if hasUsageFlag(args) {
		fmt.Println(workHelpText(args[0]))
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
	parseExplicitDispatchTarget := func(usage string, valueFlags, boolFlags map[string]bool) string {
		targetWorkID := ""
		next := false
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "--next":
				next = true
			case boolFlags[arg]:
			case valueFlags[arg]:
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				if strings.HasPrefix(arg, "-") {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				if targetWorkID != "" {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				targetWorkID = arg
			}
		}
		if targetWorkID == "" && !next {
			fmt.Fprintln(os.Stderr, "error: mutating dispatch requires --next or <work_id>")
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		if targetWorkID != "" && next {
			fmt.Fprintln(os.Stderr, "error: pass either --next or <work_id>, not both")
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		return targetWorkID
	}
	parseDispatchClaimTarget := func(usage string) (targetWorkID string, confirmed bool) {
		valueFlags := map[string]bool{
			"--actor": true, "--source-packet": true, "--source-state": true,
			"--source-owner": true, "--source-context-pack": true,
			"--source-risk": true, "--source-privacy": true,
		}
		next := false
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "--next":
				next = true
			case arg == "--confirm":
				confirmed = true
			case arg == "--json":
			case valueFlags[arg]:
				if i+1 >= len(rest) || strings.HasPrefix(rest[i+1], "-") {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				if strings.HasPrefix(arg, "-") || targetWorkID != "" {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				targetWorkID = arg
			}
		}
		if targetWorkID != "" && next {
			fmt.Fprintln(os.Stderr, "error: pass either --next or <work_id>, not both")
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		return targetWorkID, confirmed
	}
	validateCreateState := func(usage string, allowTestRunning bool) {
		state := strings.TrimSpace(flag("--state"))
		if state == "" || state == "proposed" || state == "ready" {
			return
		}
		if state == "running" && allowTestRunning {
			manifest, manifestErr := home.LoadManifest(homePath)
			if manifestErr == nil && manifest.HomeKind == "test" {
				return
			}
			fmt.Fprintln(os.Stderr, "error: create at ready -> claim -> advance")
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "error: initial work state must be proposed or ready; use lifecycle commands for later states")
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	die := func(e error) {
		if e != nil {
			fmt.Fprintln(os.Stderr, "error:", e)
			os.Exit(1)
		}
	}
	parseGitDelivery := func(usage string) *envelope.GitDeliveryContract {
		root := strings.TrimSpace(flag("--target-worktree-root"))
		localRef := strings.TrimSpace(flag("--target-local-ref"))
		base := strings.TrimSpace(flag("--expected-base-sha"))
		publicationRef := strings.TrimSpace(flag("--publication-ref"))
		localOnly := has("--local-only")
		declared := root != "" || localRef != "" || base != "" || publicationRef != "" || localOnly
		if !declared {
			return nil
		}
		if root == "" || localRef == "" || base == "" || (publicationRef == "" && !localOnly) || (publicationRef != "" && localOnly) {
			fmt.Fprintln(os.Stderr, "error: Git delivery requires --target-worktree-root, --target-local-ref, --expected-base-sha, and exactly one of --publication-ref or --local-only")
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		mode := "required"
		if localOnly {
			mode = "local_only"
		}
		return &envelope.GitDeliveryContract{
			TargetWorktreeRoot: root,
			TargetLocalRef:     localRef,
			ExpectedBaseSHA:    base,
			PublicationMode:    mode,
			PublicationRef:     publicationRef,
		}
	}

	switch sub {
	case "create-plan":
		usage := workUsage("create-plan")
		if err := validateWorkArguments(rest, map[string]bool{
			"--work-id": true, "--owner": true, "--predicate": true, "--outcome": true, "--by": true, "--actor": true,
			"--state": true, "--context-pack": true, "--canonical-surface": true, "--route-decision-id": true, "--risk": true, "--privacy": true,
			"--non-goal": true, "--non-goals": true, "--evidence-ref": true, "--evidence": true,
			"--capability": true, "--action": true, "--mutation-class": true, "--reversibility": true, "--blast-radius": true,
			"--target-kind": true, "--target-id": true, "--target-path": true,
			"--target-worktree-root": true, "--target-local-ref": true, "--expected-base-sha": true, "--publication-ref": true,
		}, map[string]bool{"--json": true, "--dry-run": true, "--local-only": true}, 0); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		validateCreateState(usage, false)
		gitDelivery := parseGitDelivery(usage)
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
			GitDelivery:      gitDelivery,
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
		usage := workUsage("create")
		if err := validateWorkArguments(rest, map[string]bool{
			"--work-id": true, "--owner": true, "--predicate": true, "--outcome": true, "--by": true, "--actor": true,
			"--state": true, "--context-pack": true, "--canonical-surface": true, "--route-decision-id": true, "--risk": true, "--privacy": true,
			"--non-goal": true, "--non-goals": true, "--evidence-ref": true, "--evidence": true,
			"--target-worktree-root": true, "--target-local-ref": true, "--expected-base-sha": true, "--publication-ref": true,
		}, map[string]bool{"--local-only": true}, 0); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		validateCreateState(usage, true)
		gitDelivery := parseGitDelivery(usage)
		requestedState := flag("--state")
		createState := requestedState
		actor := firstNonEmpty(flag("--by"), flag("--actor"))
		if requestedState == "running" {
			createState = "ready"
			actor = firstNonEmpty(actor, "test-fixture")
		}
		now := time.Now()
		w, err := store.Create(work.CreateInput{
			WorkID:           flag("--work-id"),
			Owner:            flag("--owner"),
			Actor:            actor,
			CanonicalSurface: flag("--canonical-surface"),
			SuccessPredicate: flag("--predicate"),
			DesiredOutcome:   flag("--outcome"),
			NonGoals:         flagCSVValues(rest, "--non-goal", "--non-goals"),
			EvidenceRefs:     flagCSVValues(rest, "--evidence-ref", "--evidence"),
			ContextPackID:    flag("--context-pack"),
			RouteDecisionID:  flag("--route-decision-id"),
			GitDelivery:      gitDelivery,
			State:            createState,
			Risk:             flag("--risk"),
			Privacy:          flag("--privacy"),
		}, now)
		die(err)
		if requestedState == "running" {
			w, err = store.Claim(w.ID, actor, now)
			die(err)
			w, err = store.Advance(w.ID, work.AdvanceInput{To: "running", Actor: actor}, now)
			die(err)
		}
		fmt.Printf("created %s (%s)\n", w.ID, w.State)
		printJSON(w)
	case "import-packet":
		validateCreateState("usage: compound work import-packet <packet.md> [--owner O] [--by A] [--state proposed|ready] [--context-pack ctx] [--risk tier1] [--privacy private]", false)
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
		packetPath, packetErr := packetPathForWork(w)
		packetStatus := packetEnrichmentStatus(packetErr)
		if has("--json") {
			printJSON(workShowOutput{WorkEnvelope: w, PacketPath: packetPath, PacketStatus: packetStatus, Lifecycle: evs})
			return
		}
		printJSON(w)
		if packetPath != "" {
			fmt.Printf("packet_path: %s\n", packetPath)
		}
		if packetStatus != "" {
			fmt.Printf("packet_status: %s\n", packetStatus)
		}
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
		usage := "usage: compound work dispatch-plan [--actor A] [--capability-class classify-extract|draft-prose] [--source-packet packet.md preview-only] [--source-state proposed|ready] [--source-owner O] [--source-context-pack ctx] [--source-risk tier1] [--source-privacy private] [--json] [--dry-run]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json", "--dry-run":
			case "--actor", "--capability-class", "--source-packet", "--source-state", "--source-owner", "--source-context-pack", "--source-risk", "--source-privacy":
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
			CapabilityClass:       flag("--capability-class"),
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
		if plan.LaneSelection != nil {
			sel := plan.LaneSelection
			if sel.Winner != nil {
				fmt.Printf("lane_selection: class=%s winner=%s floor=%.1ft/s status=%s (%s)\n", sel.CapabilityClass, sel.Winner.Lane, sel.FloorTokensPerSecond, sel.Status, sel.Winner.Rationale)
			} else {
				fmt.Printf("lane_selection: class=%s status=%s (no eligible lane)\n", sel.CapabilityClass, sel.Status)
			}
		}
		if plan.PlanPath != "" {
			fmt.Printf("plan: %s\n", plan.PlanPath)
		}
	case "dispatch-claim":
		usage := workUsage("dispatch-claim") + "\n  (omit --confirm to preview only; --next and <work_id> are mutually exclusive)"
		targetWorkID, confirmed := parseDispatchClaimTarget(usage)
		dispatchOpts := work.DispatchPlanOptions{
			Actor:                 flag("--actor"),
			Confirm:               confirmed,
			WorkID:                targetWorkID,
			SourcePacketPath:      flag("--source-packet"),
			SourcePacketState:     flag("--source-state"),
			SourcePacketOwner:     flag("--source-owner"),
			SourcePacketRisk:      flag("--source-risk"),
			SourcePacketPrivacy:   flag("--source-privacy"),
			SourcePacketContextID: flag("--source-context-pack"),
		}
		if !confirmed {
			preview, err := previewDispatchClaim(store, time.Now(), dispatchOpts)
			die(err)
			if has("--json") {
				printJSON(preview)
			} else {
				fmt.Printf("dispatch-claim PREVIEW %s status=%s\n", preview.ID, preview.Status)
				if preview.Selected != nil {
					fmt.Printf("would_claim: %s by=%s\n", preview.Selected.WorkID, preview.Actor)
				}
				if preview.NoAction != "" {
					fmt.Printf("no_action: %s\n", preview.NoAction)
				}
				fmt.Println("nothing claimed; re-run with --confirm to perform the claim")
			}
			os.Exit(1)
		}
		result, err := store.DispatchClaim(time.Now(), dispatchOpts)
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
		usage := "usage: compound work dispatch-advance (--next|<work_id>) [--actor A] [--json]"
		targetWorkID := parseExplicitDispatchTarget(
			usage,
			map[string]bool{"--actor": true},
			map[string]bool{"--json": true},
		)
		result, err := store.DispatchAdvance(time.Now(), work.DispatchPlanOptions{Actor: flag("--actor"), WorkID: targetWorkID})
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
		usage := "usage: compound work dispatch-close-plan (--next|<work_id>) [--actor A] [--outcome completed|failed|cancelled] [--move-type type] [--capability C] [--action A] [--mutation-class C] [--reversibility R] [--blast-radius B] [--target-kind K] [--target-id ID] [--target-path P] [--json]"
		targetWorkID := parseExplicitDispatchTarget(
			usage,
			map[string]bool{"--actor": true, "--outcome": true, "--move-type": true, "--capability": true, "--action": true, "--mutation-class": true, "--reversibility": true, "--blast-radius": true, "--target-kind": true, "--target-id": true, "--target-path": true},
			map[string]bool{"--json": true},
		)
		result, err := store.DispatchClosePlan(time.Now(), work.DispatchClosePlanOptions{
			Actor:         flag("--actor"),
			WorkID:        targetWorkID,
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
		usage := "usage: compound work dispatch-close (--next|<work_id>) [--actor A] [--outcome completed|failed|cancelled] [--move-type type] [--summary S] [--json]"
		targetWorkID := parseExplicitDispatchTarget(
			usage,
			map[string]bool{"--actor": true, "--outcome": true, "--move-type": true, "--summary": true},
			map[string]bool{"--json": true},
		)
		result, err := store.DispatchClose(time.Now(), work.DispatchCloseOptions{
			Actor:    flag("--actor"),
			WorkID:   targetWorkID,
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
		usage := workUsage("advance-plan")
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json", "--dry-run", "--local-only":
			case "--to", "--actor", "--capability", "--action", "--mutation-class", "--reversibility", "--blast-radius", "--target-kind", "--target-id", "--target-path", "--target-worktree-root", "--target-local-ref", "--expected-base-sha", "--publication-ref":
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
		gitDelivery := parseGitDelivery(usage)
		plan, err := store.AdvancePlan(pos(), time.Now(), work.AdvancePlanOptions{
			Actor:         flag("--actor"),
			To:            flag("--to"),
			GitDelivery:   gitDelivery,
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
		usage := workUsage("advance")
		if err := validateWorkArguments(rest, map[string]bool{
			"--to": true, "--by": true, "--actor": true, "--run": true, "--artifact": true, "--verification": true,
			"--context-pack": true, "--note": true, "--reason": true,
			"--target-worktree-root": true, "--target-local-ref": true, "--expected-base-sha": true, "--publication-ref": true,
		}, map[string]bool{"--steal": true, "--local-only": true}, 1); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		if pos() == "" || flag("--to") == "" {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		gitDelivery := parseGitDelivery(usage)
		w, err := store.Advance(pos(), work.AdvanceInput{
			To:             flag("--to"),
			Actor:          firstNonEmpty(flag("--by"), flag("--actor")),
			GitDelivery:    gitDelivery,
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
	case "land-check":
		usage := workUsage("land-check")
		if err := validateWorkArguments(rest,
			map[string]bool{"--tip": true, "--repo": true, "--target-ref": true, "--actor": true},
			map[string]bool{"--dry-run": true, "--json": true}, 1); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		if strings.TrimSpace(flag("--tip")) == "" {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		report, err := store.LandCheck(pos(), work.LandCheckOptions{
			RepoPath:        flag("--repo"),
			CandidateTipSHA: flag("--tip"),
			TargetRef:       flag("--target-ref"),
			Actor:           flag("--actor"),
			Record:          !has("--dry-run"),
		}, time.Now())
		die(err)
		if has("--json") {
			printJSON(report)
			return
		}
		fmt.Printf("land-check %s status=%s target=%s candidate=%s recorded=%v\n", report.WorkID, report.Status, report.TargetBranchRef, report.CandidateTipSHA, report.Recorded)
		if report.EvidenceRef != "" {
			fmt.Printf("evidence: %s\n", report.EvidenceRef)
		}
		if report.NextAction != "" {
			fmt.Printf("next_action: %s\n", report.NextAction)
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
		fmt.Fprintln(os.Stderr, workUsage(""))
		os.Exit(2)
	}
}

func validateWorkArguments(args []string, valueFlags, boolFlags map[string]bool, positionalCount int) error {
	positions := 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case boolFlags[arg]:
		case valueFlags[arg]:
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				return fmt.Errorf("%s requires a value", arg)
			}
			i++
		case strings.HasPrefix(arg, "-"):
			return fmt.Errorf("unknown flag %s", arg)
		default:
			positions++
		}
	}
	if positions != positionalCount {
		return fmt.Errorf("expected %d positional argument(s), got %d", positionalCount, positions)
	}
	return nil
}
