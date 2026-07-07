package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"compound/internal/home"
	"compound/internal/mem"
	"compound/internal/observation"
	"compound/internal/sourceobs"
	"compound/internal/work"
)

// cmdSource imports external/source observations into Compound-owned intake
// state. It observes; it never promotes source truth.
func cmdSource(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, sourceUsage())
		os.Exit(2)
	}
	switch args[0] {
	case "observe":
		cmdSourceObserve(args[1:])
	case "observe-batch":
		cmdSourceObserveBatch(args[1:])
	case "observations":
		cmdSourceObservations(args[1:])
	case "intake":
		cmdSourceIntake(args[1:])
	case "connections":
		cmdSourceConnections(args[1:])
	case "intake-work-plan":
		cmdSourceIntakeWorkPlan(args[1:])
	case "manifest-create":
		cmdSourceManifestCreate(args[1:])
	case "manifest-show":
		cmdSourceManifestShow(args[1:])
	case "manifest-doctor":
		cmdSourceManifestDoctor(args[1:])
	case "manifest-drilldown":
		cmdSourceManifestDrilldown(args[1:])
	case "observation-verdict-plan":
		cmdSourceObservationVerdictPlan(args[1:])
	case "observation-verdict-review":
		cmdSourceObservationVerdictReview(args[1:])
	default:
		fmt.Fprintln(os.Stderr, sourceUsage())
		os.Exit(2)
	}
}

func sourceUsage() string {
	return "usage: compound source observe <source-observation.yaml> | observe-batch <batch.json> (--dry-run|--apply-product-signals|--apply-source-observations|--apply-knowledge|--apply-route-evidence|--apply-events|--review-candidates) [--json] | observations [--json] | intake [--limit N] [--json] | connections [--json] | intake-work-plan --card <card_id> [--actor A] [--json] | manifest-create --family <id> <source>... [--json] | manifest-show <family> [--json] | manifest-doctor <family> [--json] | manifest-drilldown <family> --item <id> [--json] | observation-verdict-plan --verdict <panel-verdict.json> [--target file] | observation-verdict-review <review.yaml>"
}

func cmdSourceObserve(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: compound source observe <source-observation.yaml>")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	obs, err := sourceobs.Load(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "source observe:", err)
		os.Exit(1)
	}
	report, err := sourceobs.Apply(homePath, obs, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "source observe:", err)
		for _, check := range report.Checks {
			fmt.Fprintf(os.Stderr, "- %s: %s", check.Name, check.Status)
			if check.Detail != "" {
				fmt.Fprintf(os.Stderr, " (%s)", check.Detail)
			}
			fmt.Fprintln(os.Stderr)
		}
		os.Exit(1)
	}
	fmt.Printf("# source observation %s\n\n", report.ObservationID)
	fmt.Printf("- status: %s\n", report.Status)
	for _, check := range report.Checks {
		fmt.Printf("- %s: %s", check.Name, check.Status)
		if check.Detail != "" {
			fmt.Printf(" (%s)", check.Detail)
		}
		fmt.Println()
	}
	for _, id := range report.CandidateIDs {
		fmt.Printf("- candidate: %s\n", id)
	}
	for _, id := range report.NoPromoteIDs {
		fmt.Printf("- no_promote: %s\n", id)
	}
}

func cmdSourceObserveBatch(args []string) {
	jsonOut := false
	dryRun := false
	applyProductSignals := false
	applySourceObservations := false
	applyKnowledge := false
	applyRouteEvidence := false
	applyEvents := false
	reviewCandidates := false
	var paths []string
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		case "--dry-run":
			dryRun = true
		case "--apply-product-signals":
			applyProductSignals = true
		case "--apply-source-observations":
			applySourceObservations = true
		case "--apply-knowledge":
			applyKnowledge = true
		case "--apply-route-evidence":
			applyRouteEvidence = true
		case "--apply-events":
			applyEvents = true
		case "--review-candidates":
			reviewCandidates = true
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintln(os.Stderr, "source observe-batch: unknown arg", arg)
				os.Exit(2)
			}
			paths = append(paths, arg)
		}
	}
	modeCount := 0
	for _, enabled := range []bool{dryRun, applyProductSignals, applySourceObservations, applyKnowledge, applyRouteEvidence, applyEvents, reviewCandidates} {
		if enabled {
			modeCount++
		}
	}
	if len(paths) != 1 || modeCount != 1 {
		fmt.Fprintln(os.Stderr, "usage: compound source observe-batch <batch.json> (--dry-run|--apply-product-signals|--apply-source-observations|--apply-knowledge|--apply-route-evidence|--apply-events|--review-candidates) [--json]")
		os.Exit(2)
	}
	batch, err := observation.LoadBatch(paths[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "source observe-batch:", err)
		os.Exit(1)
	}
	var store *mem.Store
	homePath := ""
	if applyProductSignals || applyKnowledge {
		store, homePath = openMemStore()
	}
	if applySourceObservations || applyRouteEvidence || applyEvents || reviewCandidates {
		homePath, err = home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
	}
	report, err := observation.ObserveBatch(homePath, store, batch, paths[0], observation.Options{ApplyProductSignals: applyProductSignals, ReviewCandidates: reviewCandidates, ApplySourceObservations: applySourceObservations, ApplyKnowledge: applyKnowledge, ApplyRouteEvidence: applyRouteEvidence, ApplyEvents: applyEvents}, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "source observe-batch:", err)
		os.Exit(1)
	}
	if jsonOut {
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, "source observe-batch:", err)
			os.Exit(1)
		}
		return
	}
	fmt.Printf("# observation batch\n\n")
	fmt.Printf("- status: %s\n", report.Status)
	fmt.Printf("- source: %s %s\n", report.Source.Kind, report.Source.Ref)
	fmt.Printf("- subject: %s %s\n", report.Subject.Kind, report.Subject.ID)
	fmt.Printf("- dry_run: %t\n", report.DryRun)
	for _, decision := range report.Decisions {
		fmt.Printf("- route: %s -> %s owner=%s reason=%s\n", decision.ObservationID, decision.Route, decision.Owner, decision.Reason)
	}
	for _, result := range report.ProductApplyResults {
		fmt.Printf("- applied_product_signal: %s signals=%s transitions=%s\n", result.ObservationID, strings.Join(result.SignalIDs, ","), strings.Join(result.TransitionIDs, ","))
	}
	for _, result := range report.SourceObservationApplyResults {
		fmt.Printf("- applied_source_observation: %s source_observation=%s candidates=%s no_promote=%s next=%s\n", result.ObservationID, result.SourceObservationID, strings.Join(result.CandidateIDs, ","), strings.Join(result.NoPromoteIDs, ","), strings.Join(result.NextCommand, " "))
	}
	for _, result := range report.KnowledgeApplyResults {
		fmt.Printf("- applied_knowledge: %s evidence=%s candidates=%s extractions=%s next=%s\n", result.ObservationID, strings.Join(result.EvidenceIDs, ","), strings.Join(result.CandidateIDs, ","), strings.Join(result.ExtractionIDs, ","), strings.Join(result.NextCommand, " "))
	}
	for _, result := range report.RouteEvidenceApplyResults {
		fmt.Printf("- applied_route_evidence: %s route_evidence=%s transition=%s next=%s\n", result.ObservationID, result.RouteEvidenceID, result.TransitionID, strings.Join(result.NextCommand, " "))
	}
	for _, result := range report.EventApplyResults {
		fmt.Printf("- applied_events: observations=%s traces=%s source_authority=%s next=%s\n", strings.Join(result.ObservationIDs, ","), strings.Join(result.TraceIDs, ","), result.SourceAuthorityID, strings.Join(result.NextCommand, " "))
	}
	for _, result := range report.CandidateReviewResults {
		fmt.Printf("- observation_candidate_review: %s candidate=%s review=%s\n", result.ObservationID, result.CandidateID, result.ReviewYAMLPath)
	}
}

func cmdSourceIntake(args []string) {
	jsonOut := false
	limit := 0
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--limit":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "usage: compound source intake [--limit N] [--json]")
				os.Exit(2)
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				fmt.Fprintln(os.Stderr, "source intake: --limit must be non-negative integer")
				os.Exit(2)
			}
			limit = n
		default:
			fmt.Fprintln(os.Stderr, "usage: compound source intake [--limit N] [--json]")
			os.Exit(2)
		}
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	projection, err := observation.LoadSourceIntakeProjection(homePath, time.Now(), limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "source intake:", err)
		os.Exit(1)
	}
	if jsonOut {
		if err := json.NewEncoder(os.Stdout).Encode(projection); err != nil {
			fmt.Fprintln(os.Stderr, "source intake:", err)
			os.Exit(1)
		}
		return
	}
	fmt.Printf("# source intake\n\n")
	fmt.Printf("- status: %s\n", projection.Status)
	fmt.Printf("- records: %d\n", projection.RecordCount)
	fmt.Printf("- rows: %d\n", projection.RowCount)
	fmt.Printf("- applied: %d\n", projection.Counts.Applied)
	fmt.Printf("- review_recorded: %d\n", projection.Counts.ReviewRecorded)
	fmt.Printf("- blocked_private: %d\n", projection.Counts.BlockedPrivate)
	fmt.Printf("- action_required: %d\n", projection.Counts.ActionRequired)
	fmt.Printf("- action_cards: %d\n", projection.ActionCardCount)
	for _, card := range projection.ActionCards {
		fmt.Printf("- action_card: %s row=%s route=%s next=%s proof=%s\n", card.Kind, card.RowID, card.Route, strings.Join(card.Command, " "), card.Proof)
	}
	for _, row := range projection.Rows {
		fmt.Printf("- %s %s owner=%s status=%s source=%s subject=%s next=%s\n", row.ObservationID, row.Route, row.Owner, row.Status, row.SourceKind, row.SubjectID, strings.Join(row.NextCommand, " "))
	}
}

func cmdSourceConnections(args []string) {
	if len(args) > 1 || (len(args) == 1 && args[0] != "--json") {
		fmt.Fprintln(os.Stderr, "usage: compound source connections [--json]")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	projection, err := observation.LoadSourceConnectionsProjection(homePath, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "source connections:", err)
		os.Exit(1)
	}
	if len(args) == 1 && args[0] == "--json" {
		if err := json.NewEncoder(os.Stdout).Encode(projection); err != nil {
			fmt.Fprintln(os.Stderr, "source connections:", err)
			os.Exit(1)
		}
		return
	}
	fmt.Printf("# source connections\n\n")
	fmt.Printf("- status: %s\n", projection.Status)
	fmt.Printf("- connections: %d\n", projection.Counts.Total)
	fmt.Printf("- availability: unknown=%d not_configured=%d needs_private_config=%d configured=%d healthy=%d failing=%d\n", projection.Counts.AvailabilityUnknown, projection.Counts.AvailabilityNotConfigured, projection.Counts.AvailabilityNeedsPrivateConfig, projection.Counts.AvailabilityConfigured, projection.Counts.AvailabilityHealthy, projection.Counts.AvailabilityFailing)
	fmt.Printf("- evidence_state: never_seen=%d observed=%d imported=%d stale=%d has_cards=%d\n", projection.Counts.EvidenceNeverSeen, projection.Counts.EvidenceObserved, projection.Counts.EvidenceImported, projection.Counts.EvidenceStale, projection.Counts.EvidenceHasCards)
	for _, conn := range projection.Connections {
		fmt.Printf("- %s availability=%s evidence_state=%s rows=%d latest_cards=%d next=%s\n", conn.ID, conn.Availability, conn.EvidenceState, conn.RowCount, conn.LatestCardCount, conn.NextSafeAction)
	}
}

func cmdSourceIntakeWorkPlan(args []string) {
	cardID := ""
	actor := "codex"
	jsonOut := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--card":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "usage: compound source intake-work-plan --card <card_id> [--actor A] [--json]")
				os.Exit(2)
			}
			cardID = args[i+1]
			i++
		case "--actor":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "usage: compound source intake-work-plan --card <card_id> [--actor A] [--json]")
				os.Exit(2)
			}
			actor = args[i+1]
			i++
		default:
			fmt.Fprintln(os.Stderr, "usage: compound source intake-work-plan --card <card_id> [--actor A] [--json]")
			os.Exit(2)
		}
	}
	if strings.TrimSpace(cardID) == "" {
		fmt.Fprintln(os.Stderr, "usage: compound source intake-work-plan --card <card_id> [--actor A] [--json]")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	workStore, err := work.Open(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "source intake-work-plan:", err)
		os.Exit(1)
	}
	report, err := observation.BuildSourceIntakeWorkPlan(homePath, workStore, cardID, time.Now(), observation.SourceIntakeWorkPlanOptions{Actor: actor})
	if err != nil {
		fmt.Fprintln(os.Stderr, "source intake-work-plan:", err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(report)
		return
	}
	fmt.Printf("# source intake-work-plan %s\n\n", report.SourceIntake.CardID)
	fmt.Printf("- status: %s\n", report.Status)
	fmt.Printf("- card_kind: %s\n", report.SourceIntake.CardKind)
	fmt.Printf("- row: %s\n", report.SourceIntake.RowID)
	fmt.Printf("- route: %s\n", report.SourceIntake.Route)
	fmt.Printf("- work: %s\n", report.WorkPlan.WorkID)
	fmt.Printf("- work_state: %s\n", report.WorkPlan.WorkState)
	fmt.Printf("- policy: %s allowed=%t\n", report.WorkPlan.Policy.Outcome, report.WorkPlan.Policy.Allowed)
	fmt.Printf("- plan: %s\n", report.WorkPlan.PlanPath)
	if len(report.SourceIntake.ActionCommand) > 0 {
		fmt.Printf("- action_command: %s\n", strings.Join(report.SourceIntake.ActionCommand, " "))
	}
	fmt.Printf("- proof: %s\n", report.SourceIntake.Proof)
}

func cmdSourceManifestCreate(args []string) {
	family := ""
	jsonOut := false
	var paths []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--family":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "usage: compound source manifest-create --family <id> <source>... [--json]")
				os.Exit(2)
			}
			family = args[i+1]
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintln(os.Stderr, "usage: compound source manifest-create --family <id> <source>... [--json]")
				os.Exit(2)
			}
			paths = append(paths, args[i])
		}
	}
	if strings.TrimSpace(family) == "" || len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound source manifest-create --family <id> <source>... [--json]")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	manifest, err := sourceobs.BuildSourceManifest(family, paths, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "source manifest-create:", err)
		os.Exit(1)
	}
	report, err := sourceobs.RecordSourceManifest(homePath, manifest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "source manifest-create:", err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(report)
		return
	}
	fmt.Printf("# source manifest %s\n\n", report.ManifestID)
	fmt.Printf("- authority: %s\n", report.AuthorityRef)
	fmt.Printf("- items: %d\n", report.ItemCount)
	fmt.Printf("- path: %s\n", report.ManifestPath)
}

func cmdSourceManifestShow(args []string) {
	jsonOut := false
	var family string
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		default:
			if strings.HasPrefix(arg, "-") || family != "" {
				fmt.Fprintln(os.Stderr, "usage: compound source manifest-show <family> [--json]")
				os.Exit(2)
			}
			family = arg
		}
	}
	if strings.TrimSpace(family) == "" {
		fmt.Fprintln(os.Stderr, "usage: compound source manifest-show <family> [--json]")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	manifest, err := sourceobs.LoadSourceManifest(homePath, family)
	if err != nil {
		fmt.Fprintln(os.Stderr, "source manifest-show:", err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(manifest)
		return
	}
	fmt.Printf("source manifest: %s\n", manifest.AuthorityRef)
	fmt.Printf("items: %d\n", len(manifest.Items))
	for _, item := range manifest.Items {
		fmt.Printf("- %s %s %s\n", item.ID, item.SourceKind, item.FileHash)
	}
}

func cmdSourceManifestDoctor(args []string) {
	jsonOut := false
	var family string
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		default:
			if strings.HasPrefix(arg, "-") || family != "" {
				fmt.Fprintln(os.Stderr, "usage: compound source manifest-doctor <family> [--json]")
				os.Exit(2)
			}
			family = arg
		}
	}
	if strings.TrimSpace(family) == "" {
		fmt.Fprintln(os.Stderr, "usage: compound source manifest-doctor <family> [--json]")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	report, err := sourceobs.DoctorSourceManifest(homePath, family)
	if err != nil {
		fmt.Fprintln(os.Stderr, "source manifest-doctor:", err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(report)
		if report.Status != "ok" {
			os.Exit(1)
		}
		return
	}
	fmt.Printf("source manifest doctor: %s\n", report.Status)
	fmt.Printf("items: %d missing=%d hash_mismatch=%d\n", report.ItemCount, report.MissingFieldCount, report.HashMismatchCount)
	for _, check := range report.Checks {
		fmt.Printf("- %s: %s", check.Name, check.Status)
		if check.Detail != "" {
			fmt.Printf(" (%s)", check.Detail)
		}
		fmt.Println()
	}
	if report.Status != "ok" {
		os.Exit(1)
	}
}

func cmdSourceManifestDrilldown(args []string) {
	jsonOut := false
	itemID := ""
	var family string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--item":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "usage: compound source manifest-drilldown <family> --item <id> [--json]")
				os.Exit(2)
			}
			itemID = args[i+1]
			i++
		default:
			if strings.HasPrefix(args[i], "-") || family != "" {
				fmt.Fprintln(os.Stderr, "usage: compound source manifest-drilldown <family> --item <id> [--json]")
				os.Exit(2)
			}
			family = args[i]
		}
	}
	if strings.TrimSpace(family) == "" || strings.TrimSpace(itemID) == "" {
		fmt.Fprintln(os.Stderr, "usage: compound source manifest-drilldown <family> --item <id> [--json]")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	drilldown, err := sourceobs.DrilldownSourceManifest(homePath, family, itemID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "source manifest-drilldown:", err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(drilldown)
		return
	}
	fmt.Printf("compact: %s\n", drilldown.CompactRowRef)
	fmt.Printf("manifest: %s\n", drilldown.ManifestRef)
	fmt.Printf("source: %s\n", drilldown.SourceRef)
	fmt.Printf("hash: %s matches=%t\n", drilldown.FileHash, drilldown.HashMatches)
}

func cmdSourceObservationVerdictPlan(args []string) {
	verdictPath := flagValue(args, "--verdict")
	targetPath := flagValue(args, "--target")
	if verdictPath == "" {
		fmt.Fprintln(os.Stderr, "usage: compound source observation-verdict-plan --verdict <panel-verdict.json> [--target file]")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	plan, err := sourceobs.BuildSourceObservationVerdictPlan(homePath, verdictPath, targetPath, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "source observation verdict plan:", err)
		os.Exit(1)
	}
	paths, err := sourceobs.RecordSourceObservationVerdictPlan(homePath, plan)
	if err != nil {
		fmt.Fprintln(os.Stderr, "source observation verdict plan:", err)
		os.Exit(1)
	}
	fmt.Printf("# source observation-verdict-plan %s\n\n", plan.ID)
	fmt.Printf("- status: %s\n", plan.Status)
	fmt.Printf("- owner_status: %s\n", plan.OwnerStatus)
	fmt.Printf("- owner: %s %s\n", plan.OwnerKind, plan.OwnerRef)
	fmt.Printf("- verdict: %s\n", plan.VerdictID)
	fmt.Printf("- candidate: %s\n", plan.TargetRef)
	fmt.Printf("- panel_decision: %s\n", plan.PanelDecision)
	fmt.Printf("- review_id: %s\n", plan.ReviewDraft.ReviewID)
	fmt.Printf("- required_evidence: %d\n", len(plan.ReviewDraft.RequiredEvidence))
	fmt.Printf("- evidence_refs: %d\n", len(plan.EvidenceRefs))
	fmt.Printf("- plan: %s\n", paths.PlanPath)
	fmt.Printf("- review: %s\n", paths.ReviewPath)
	fmt.Printf("- review_yaml: %s\n", paths.ReviewYAMLPath)
}

func cmdSourceObservationVerdictReview(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: compound source observation-verdict-review <review.yaml>")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	review, err := sourceobs.LoadSourceObservationVerdictReview(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "source observation verdict review:", err)
		os.Exit(1)
	}
	report, err := sourceobs.ApplySourceObservationVerdictReview(homePath, review, args[0], time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "source observation verdict review:", err)
		os.Exit(1)
	}
	fmt.Printf("# source observation-verdict-review %s\n\n", report.ReviewID)
	fmt.Printf("- status: %s\n", report.Status)
	fmt.Printf("- verdict: %s\n", report.VerdictID)
	fmt.Printf("- decision: %s\n", report.Decision)
	fmt.Printf("- owner: %s %s\n", report.OwnerKind, report.OwnerRef)
	fmt.Printf("- no_promote: %s\n", report.NoPromoteID)
	fmt.Printf("- evidence_refs: %d\n", len(report.EvidenceRefs))
}
