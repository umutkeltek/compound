package director

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/authority"
	"compound/internal/observation"
	"compound/internal/product"
	"compound/internal/reviewartifact"
	"compound/internal/routeevidence"
	"compound/internal/work"
)

const (
	ReportSchemaVersion = "compound.director_propose_report.v0.1"
	DirectorActor       = "director"
	maxProposalsPerRun  = 5
)

var proposalClassSeverityOrder = []string{
	"job_health",
	"source_connection_readiness",
	"product_loop",
	"synthesis_contradiction",
	"review_debt",
	// absorption_debt ranks last: os-absorption retirement debt is real but
	// never more urgent than live review debt (W5 J15). The operator can
	// reorder later; today it just must not starve behind the cap forever.
	"absorption_debt",
}

type Options struct {
	Apply bool
	Now   time.Time
}

type Report struct {
	SchemaVersion            string                `json:"schema_version"`
	ID                       string                `json:"id"`
	GeneratedAt              string                `json:"generated_at"`
	Status                   string                `json:"status"`
	Mode                     string                `json:"mode"`
	ProposedBy               string                `json:"proposed_by"`
	ProposalCount            int                   `json:"proposal_count"`
	SuppressedDuplicateCount int                   `json:"suppressed_duplicate_count"`
	SkippedCount             int                   `json:"skipped_count"`
	SkipHistogram            map[string]int        `json:"skip_histogram,omitempty"`
	RunWorkID                string                `json:"run_work_id,omitempty"`
	RunReceiptID             string                `json:"run_receipt_id,omitempty"`
	RunTransitionID          string                `json:"run_transition_id,omitempty"`
	RunMoveType              string                `json:"run_move_type,omitempty"`
	NoDelta                  bool                  `json:"no_delta,omitempty"`
	ResultPath               string                `json:"result_path,omitempty"`
	Proposals                []Proposal            `json:"proposals,omitempty"`
	SuppressedDuplicates     []SuppressedDuplicate `json:"suppressed_duplicates,omitempty"`
	Skipped                  []SkippedCandidate    `json:"skipped,omitempty"`
	Warnings                 []string              `json:"warnings,omitempty"`
}

type Proposal struct {
	ID              string                            `json:"id"`
	TriggerRef      string                            `json:"trigger_ref"`
	Kind            string                            `json:"kind"`
	Title           string                            `json:"title"`
	Consequence     string                            `json:"consequence"`
	CapabilityClass string                            `json:"capability_class"`
	EvidenceRefs    []string                          `json:"evidence_refs"`
	WorkID          string                            `json:"work_id"`
	WorkState       string                            `json:"work_state"`
	PlanPath        string                            `json:"plan_path,omitempty"`
	RoutePlan       routeevidence.CapabilityRoutePlan `json:"route_plan"`
	WorkPlan        work.CreatePlan                   `json:"work_plan,omitempty"`
}

type SuppressedDuplicate struct {
	TriggerRef      string   `json:"trigger_ref"`
	Kind            string   `json:"kind"`
	Title           string   `json:"title"`
	Reason          string   `json:"reason"`
	EvidenceRefs    []string `json:"evidence_refs"`
	ExistingWorkIDs []string `json:"existing_work_ids,omitempty"`
	ExistingPlanIDs []string `json:"existing_plan_ids,omitempty"`
}

type SkippedCandidate struct {
	Class        string   `json:"class,omitempty"`
	Kind         string   `json:"kind"`
	Title        string   `json:"title,omitempty"`
	Reason       string   `json:"reason"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

type candidate struct {
	Kind               string
	SourceID           string
	Title              string
	Consequence        string
	CapabilityClass    string
	Owner              string
	CanonicalSurface   string
	SuccessPredicate   string
	DesiredOutcome     string
	Command            []string
	RequiredInputs     []string
	ExpectedValidation []string
	StopConditions     []string
	Boundary           string
	Proof              string
	EvidenceRefs       []string
	CreatedAt          string
}

type duplicateIndex struct {
	openWorkByTrigger map[string][]string
	plansByTrigger    map[string][]string
}

func Propose(homePath string, opts Options) (Report, error) {
	homePath = strings.TrimSpace(homePath)
	if homePath == "" {
		return Report{}, fmt.Errorf("COMPOUND_HOME required")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	mode := "dry_run"
	if opts.Apply {
		mode = "apply"
	}
	report := Report{
		SchemaVersion: ReportSchemaVersion,
		ID:            idFor("dirrun_", mode, timestamp(now)),
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Status:        "ok",
		Mode:          mode,
		ProposedBy:    DirectorActor,
	}
	store, err := work.Open(homePath)
	if err != nil {
		return report, err
	}
	dupes, warnings := loadDuplicateIndex(homePath, store)
	report.Warnings = append(report.Warnings, warnings...)
	candidates, warnings := scanCandidates(homePath, now)
	report.Warnings = append(report.Warnings, warnings...)
	for _, cand := range fairCandidateOrder(candidates) {
		if len(report.Proposals) >= maxProposalsPerRun {
			addSkipped(&report, SkippedCandidate{
				Class:        candidateClass(cand),
				Kind:         cand.Kind,
				Title:        cand.Title,
				Reason:       "cap_reached",
				EvidenceRefs: compactRefs(cand.EvidenceRefs),
			})
			continue
		}
		proposal, skipped := buildProposal(homePath, store, cand, now)
		if skipped != nil {
			if skipped.Class == "" {
				skipped.Class = candidateClass(cand)
			}
			addSkipped(&report, *skipped)
			continue
		}
		if dup := dupes.find(proposal.TriggerRef); dup != nil {
			dup.Kind = proposal.Kind
			dup.Title = proposal.Title
			dup.EvidenceRefs = append([]string{}, proposal.EvidenceRefs...)
			report.SuppressedDuplicates = append(report.SuppressedDuplicates, *dup)
			continue
		}
		if opts.Apply {
			plan, err := store.CreatePlan(work.CreateInput{
				WorkID:           proposal.WorkID,
				Owner:            proposalOwner(cand),
				Actor:            DirectorActor,
				CanonicalSurface: cand.CanonicalSurface,
				SuccessPredicate: cand.SuccessPredicate,
				DesiredOutcome:   cand.DesiredOutcome,
				NonGoals:         nonGoalsFor(cand),
				EvidenceRefs:     proposal.EvidenceRefs,
				RouteDecisionID:  proposal.RoutePlan.ID,
				State:            "proposed",
				Risk:             "tier1",
				Privacy:          "private",
			}, now, work.CreatePlanOptions{
				Actor:         DirectorActor,
				Capability:    "director.propose",
				Action:        "propose_work",
				TargetKind:    "director_proposal",
				TargetID:      proposal.TriggerRef,
				MutationClass: "local_reversible",
				Reversibility: "easy",
				BlastRadius:   "local",
				ActionContext: actionContextFor(cand, proposal),
				Record:        true,
			})
			if err != nil {
				return report, err
			}
			proposal.PlanPath = plan.PlanPath
			proposal.WorkPlan = plan
		}
		report.Proposals = append(report.Proposals, proposal)
		dupes.addPlan(proposal.TriggerRef, proposal.ID)
	}
	report.ProposalCount = len(report.Proposals)
	report.SuppressedDuplicateCount = len(report.SuppressedDuplicates)
	report.SkippedCount = len(report.Skipped)
	if opts.Apply {
		path, err := recordReport(homePath, report)
		if err != nil {
			return report, err
		}
		report.ResultPath = path
		if err := recordApplyRunEnvelope(store, &report, now); err != nil {
			return report, err
		}
		if err := rewriteReport(path, report); err != nil {
			return report, err
		}
	}
	return report, nil
}

func scanCandidates(homePath string, now time.Time) ([]candidate, []string) {
	var out []candidate
	var warnings []string
	if items, err := productLoopCandidates(homePath, now); err != nil {
		warnings = append(warnings, "product loop scan skipped: "+err.Error())
	} else {
		out = append(out, items...)
	}
	if items, err := synthesisContradictionCandidates(homePath, now); err != nil {
		warnings = append(warnings, "synthesis contradiction scan skipped: "+err.Error())
	} else {
		out = append(out, items...)
	}
	if items, err := reviewDebtCandidates(homePath, now); err != nil {
		warnings = append(warnings, "review debt scan skipped: "+err.Error())
	} else {
		out = append(out, items...)
	}
	if items, err := sourceConnectionCandidates(homePath, now); err != nil {
		warnings = append(warnings, "source connection scan skipped: "+err.Error())
	} else {
		out = append(out, items...)
	}
	if items, err := jobHealthCandidates(homePath, now); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			warnings = append(warnings, "job health scan skipped: "+err.Error())
		}
	} else {
		out = append(out, items...)
	}
	if items, err := absorptionDebtCandidates(homePath, now); err != nil {
		warnings = append(warnings, "absorption debt scan skipped: "+err.Error())
	} else {
		out = append(out, items...)
	}
	return out, warnings
}

func fairCandidateOrder(candidates []candidate) []candidate {
	buckets := map[string][]candidate{}
	unknownClasses := map[string]bool{}
	for _, cand := range candidates {
		class := candidateClass(cand)
		buckets[class] = append(buckets[class], cand)
		if !knownProposalClass(class) {
			unknownClasses[class] = true
		}
	}
	for class := range buckets {
		sort.SliceStable(buckets[class], func(i, j int) bool {
			return candidateLessWithinClass(buckets[class][i], buckets[class][j])
		})
	}
	classes := append([]string{}, proposalClassSeverityOrder...)
	for class := range unknownClasses {
		classes = append(classes, class)
	}
	sort.SliceStable(classes[len(proposalClassSeverityOrder):], func(i, j int) bool {
		return classes[len(proposalClassSeverityOrder)+i] < classes[len(proposalClassSeverityOrder)+j]
	})

	out := make([]candidate, 0, len(candidates))
	for len(out) < len(candidates) {
		added := false
		for _, class := range classes {
			if len(buckets[class]) == 0 {
				continue
			}
			out = append(out, buckets[class][0])
			buckets[class] = buckets[class][1:]
			added = true
		}
		if !added {
			break
		}
	}
	return out
}

func candidateClass(cand candidate) string {
	switch cand.Kind {
	case "product_signal_without_hypothesis", "product_action_card":
		return "product_loop"
	default:
		return cand.Kind
	}
}

func knownProposalClass(class string) bool {
	for _, known := range proposalClassSeverityOrder {
		if class == known {
			return true
		}
	}
	return false
}

func candidateLessWithinClass(a, b candidate) bool {
	aCreated := strings.TrimSpace(a.CreatedAt)
	bCreated := strings.TrimSpace(b.CreatedAt)
	if aCreated != bCreated {
		if aCreated == "" {
			return false
		}
		if bCreated == "" {
			return true
		}
		return aCreated < bCreated
	}
	if a.SourceID != b.SourceID {
		return a.SourceID < b.SourceID
	}
	if a.Title != b.Title {
		return a.Title < b.Title
	}
	return strings.Join(compactRefs(a.EvidenceRefs), "|") < strings.Join(compactRefs(b.EvidenceRefs), "|")
}

func addSkipped(report *Report, skipped SkippedCandidate) {
	report.Skipped = append(report.Skipped, skipped)
	class := strings.TrimSpace(skipped.Class)
	if class == "" {
		class = skipped.Kind
	}
	if report.SkipHistogram == nil {
		report.SkipHistogram = map[string]int{}
	}
	report.SkipHistogram[class]++
}

func buildProposal(homePath string, store *work.Store, cand candidate, now time.Time) (Proposal, *SkippedCandidate) {
	refs := compactRefs(cand.EvidenceRefs)
	if len(refs) == 0 {
		return Proposal{}, &SkippedCandidate{Kind: cand.Kind, Title: cand.Title, Reason: "missing_evidence_refs"}
	}
	if strings.TrimSpace(cand.CapabilityClass) == "" {
		return Proposal{}, &SkippedCandidate{Kind: cand.Kind, Title: cand.Title, Reason: "missing_capability_class", EvidenceRefs: refs}
	}
	routePlan, err := routeevidence.PlanCapabilityRoute(homePath, cand.CapabilityClass, routeevidence.CapabilityRoutePlanOptions{}, now)
	if err != nil {
		return Proposal{}, &SkippedCandidate{Kind: cand.Kind, Title: cand.Title, Reason: "route_plan_unavailable: " + err.Error(), EvidenceRefs: refs}
	}
	if len(routePlan.Chain) == 0 {
		return Proposal{}, &SkippedCandidate{Kind: cand.Kind, Title: cand.Title, Reason: "route_plan_blocked", EvidenceRefs: refs}
	}
	triggerRef := triggerRefFor(cand)
	refs = reviewartifact.UniqueSorted(append(refs, "proposed_by:director", triggerRef, "route_plan:"+routePlan.ID))
	workID := idFor("work_", triggerRef)
	workEnvelope := work.BuildWork(work.CreateInput{
		WorkID:           workID,
		Owner:            proposalOwner(cand),
		Actor:            DirectorActor,
		CanonicalSurface: cand.CanonicalSurface,
		SuccessPredicate: cand.SuccessPredicate,
		DesiredOutcome:   cand.DesiredOutcome,
		NonGoals:         nonGoalsFor(cand),
		EvidenceRefs:     refs,
		RouteDecisionID:  routePlan.ID,
		State:            "proposed",
		Risk:             "tier1",
		Privacy:          "private",
	}, now)
	if err := workEnvelope.Validate(); err != nil {
		return Proposal{}, &SkippedCandidate{Kind: cand.Kind, Title: cand.Title, Reason: "work_invalid: " + err.Error(), EvidenceRefs: refs}
	}
	if store == nil {
		return Proposal{}, &SkippedCandidate{Kind: cand.Kind, Title: cand.Title, Reason: "work_store_unavailable", EvidenceRefs: refs}
	}
	return Proposal{
		ID:              idFor("dirprop_", triggerRef),
		TriggerRef:      triggerRef,
		Kind:            cand.Kind,
		Title:           cand.Title,
		Consequence:     cand.Consequence,
		CapabilityClass: cand.CapabilityClass,
		EvidenceRefs:    refs,
		WorkID:          workID,
		WorkState:       "proposed",
		RoutePlan:       routePlan,
	}, nil
}

func productLoopCandidates(homePath string, now time.Time) ([]candidate, error) {
	model, err := product.BuildLoopModel(homePath, product.LoopOptions{Now: now})
	if err != nil {
		return nil, err
	}
	var out []candidate
	for _, loop := range model.Products {
		seen := map[string]bool{}
		for _, group := range loop.SignalsWithoutHypothesis {
			if group.Consumed {
				continue
			}
			for _, card := range group.ActionCards {
				seen[card.ID] = true
				out = append(out, productCardCandidate(loop, card, "product_signal_without_hypothesis", productSignalGapEvidence(loop, card, group)))
			}
		}
		for _, card := range loop.ActionCards {
			if seen[card.ID] {
				continue
			}
			out = append(out, productCardCandidate(loop, card, "product_action_card", productActionCardEvidence(loop, card)))
		}
	}
	return out, nil
}

func productCardCandidate(loop product.ProductLoop, card product.LoopActionCard, kind string, refs []string) candidate {
	summary := firstNonEmpty(card.ActionSummary, card.Title)
	consequence := "Adding this creates bounded product-loop work for " + summary
	return candidate{
		Kind:               kind,
		SourceID:           card.ID,
		Title:              card.Title,
		Consequence:        consequence,
		CapabilityClass:    productCardCapabilityClass(card),
		Owner:              loop.ProductID,
		CanonicalSurface:   "director:product-loop:" + loop.ProductID + ":" + card.ID,
		SuccessPredicate:   fmt.Sprintf("Resolve product loop action card %s for %s; proof: %s", card.ID, loop.ProductID, card.Proof),
		DesiredOutcome:     consequence,
		Command:            append([]string{}, card.Command...),
		RequiredInputs:     append([]string{}, card.RequiredInputs...),
		ExpectedValidation: append([]string{}, card.ExpectedValidation...),
		StopConditions:     append([]string{}, card.StopConditions...),
		Boundary:           card.Boundary,
		Proof:              card.Proof,
		EvidenceRefs:       refs,
	}
}

func productCardCapabilityClass(card product.LoopActionCard) string {
	kind := strings.ToLower(card.Kind)
	if strings.Contains(kind, "review") || strings.Contains(kind, "hypothesis") {
		return "judge-critique"
	}
	return "classify-extract"
}

func productActionCardEvidence(loop product.ProductLoop, card product.LoopActionCard) []string {
	refs := []string{"product_loop:" + loop.ProductID, "product_action_card:" + card.ID}
	for _, group := range loop.OpenHypothesesByMissingSignalSource {
		if !cardGroupHasID(group.ActionCards, card.ID) {
			continue
		}
		for _, id := range group.HypothesisIDs {
			refs = append(refs, "product_hypothesis:"+id)
		}
		if group.Source != "" {
			refs = append(refs, "product_signal_source:"+group.Source)
		}
	}
	for _, group := range loop.ClosedReviewsByNextAction {
		if !cardGroupHasID(group.ActionCards, card.ID) {
			continue
		}
		for _, id := range group.ReviewIDs {
			refs = append(refs, "product_review:"+id)
		}
		for _, id := range group.HypothesisIDs {
			refs = append(refs, "product_hypothesis:"+id)
		}
		for _, id := range group.SignalIDs {
			refs = append(refs, "product_signal:"+id)
		}
	}
	for _, group := range loop.SignalsWithoutHypothesis {
		refs = append(refs, productSignalGapEvidence(loop, card, group)...)
	}
	return reviewartifact.UniqueSorted(refs)
}

func productSignalGapEvidence(loop product.ProductLoop, card product.LoopActionCard, group product.LoopSignalHypothesisGap) []string {
	if !cardGroupHasID(group.ActionCards, card.ID) {
		return nil
	}
	refs := []string{"product_loop:" + loop.ProductID, "product_action_card:" + card.ID}
	for _, id := range group.SignalIDs {
		refs = append(refs, "product_signal:"+id)
	}
	if group.ChangeEvent != "" {
		refs = append(refs, "product_signal_change_event:"+slug(group.ChangeEvent))
	}
	return reviewartifact.UniqueSorted(refs)
}

func cardGroupHasID(cards []product.LoopActionCard, id string) bool {
	for _, card := range cards {
		if card.ID == id {
			return true
		}
	}
	return false
}

func synthesisContradictionCandidates(homePath string, now time.Time) ([]candidate, error) {
	queue, err := reviewartifact.Queue(homePath, reviewartifact.QueueOptions{Area: "synthesis-contradictions"}, now)
	if err != nil {
		return nil, err
	}
	var out []candidate
	for _, item := range queue.Items {
		out = append(out, candidate{
			Kind:             "synthesis_contradiction",
			SourceID:         reviewSourceID(item),
			Title:            "Review synthesis contradiction " + item.ID,
			Consequence:      "Adding this creates focused synthesis contradiction review work for " + item.ID,
			CapabilityClass:  "judge-critique",
			Owner:            "memory-synthesis",
			CanonicalSurface: "director:synthesis-contradiction:" + item.ID,
			SuccessPredicate: "Review synthesis contradiction " + item.ID + " without bulk applying or superseding memory",
			DesiredOutcome:   "Adding this creates focused synthesis contradiction review work for " + item.ID,
			Command:          []string{"compound", "reviews", "--area", "synthesis-contradictions", "--json"},
			RequiredInputs:   []string{"current synthesis contradiction artifact", "claim/candidate evidence named in evidence_refs"},
			ExpectedValidation: []string{
				"review outcome cites the contradiction evidence refs",
				"no memory claim is promoted, rejected, or superseded without a later reviewed work receipt",
			},
			StopConditions: []string{"contradiction artifact is stale, superseded, or lacks claim/candidate evidence"},
			Boundary:       "review-only synthesis handling; no bulk apply, auto-adjudication, memory promotion, rejection, or supersession",
			Proof:          "review artifact or follow-up WorkEnvelope cites the contradiction evidence refs",
			EvidenceRefs:   reviewEvidenceRefs(item),
			CreatedAt:      item.CreatedAt,
		})
	}
	return out, nil
}

func reviewDebtCandidates(homePath string, now time.Time) ([]candidate, error) {
	queue, err := reviewartifact.Queue(homePath, reviewartifact.QueueOptions{}, now)
	if err != nil {
		return nil, err
	}
	var out []candidate
	for _, item := range queue.Items {
		if item.Area == "synthesis-contradictions" || item.Area == "job-health" || reviewStatusTerminal(item.Status) {
			continue
		}
		area := firstNonEmpty(item.Area, "reviews")
		title := "Resolve review debt " + area + "/" + item.ID
		out = append(out, candidate{
			Kind:             "review_debt",
			SourceID:         reviewSourceID(item),
			Title:            title,
			Consequence:      "Adding this creates review-debt resolution work for " + area + "/" + item.ID,
			CapabilityClass:  "judge-critique",
			Owner:            "compound-reviews",
			CanonicalSurface: "director:review-debt:" + area + ":" + item.ID,
			SuccessPredicate: "Resolve review artifact " + area + "/" + item.ID + " through its owner command or an explicit parked decision",
			DesiredOutcome:   "Adding this creates review-debt resolution work for " + area + "/" + item.ID,
			Command:          []string{"compound", "reviews", "--area", area, "--json"},
			RequiredInputs:   []string{"current review artifact " + area + "/" + item.ID, "reviewer decision or owner command named by the review surface"},
			ExpectedValidation: []string{
				"`compound reviews --area " + area + " --json` shows the item recorded, resolved, parked, or still explicitly blocked",
			},
			StopConditions: []string{"review artifact lacks enough evidence for a bounded decision", "owner command would mutate public, financial, credential, destructive, or security-sensitive state without policy"},
			Boundary:       "review-debt work only; no owner mutation except through the review artifact's explicit command and policy path",
			Proof:          "review queue no longer reports the same item as unresolved, or a follow-up receipt explains why it remains blocked",
			EvidenceRefs:   reviewEvidenceRefs(item),
			CreatedAt:      item.CreatedAt,
		})
	}
	return out, nil
}

func sourceConnectionCandidates(homePath string, now time.Time) ([]candidate, error) {
	projection, err := observation.LoadSourceConnectionsProjection(homePath, now)
	if err != nil {
		return nil, err
	}
	var out []candidate
	for _, conn := range projection.Connections {
		for _, card := range conn.LatestCards {
			refs := []string{
				"source_connection:" + conn.ID,
				"source_intake_card:" + card.ID,
				"source_intake_row:" + card.RowID,
				"source_observation:" + card.ObservationID,
			}
			if conn.SourceRef != "" {
				refs = append(refs, conn.SourceRef)
			}
			out = append(out, candidate{
				Kind:               "source_connection_readiness",
				SourceID:           card.ID,
				Title:              card.Title,
				Consequence:        "Adding this creates source-connection readiness work for " + conn.Label + ": " + card.Title,
				CapabilityClass:    "classify-extract",
				Owner:              "compound-sources",
				CanonicalSurface:   "director:source-connection:" + conn.ID + ":" + card.ID,
				SuccessPredicate:   "Resolve source connection " + conn.ID + " card " + card.ID + "; proof: " + card.Proof,
				DesiredOutcome:     "Adding this creates source-connection readiness work for " + conn.Label + ": " + card.Title,
				Command:            append([]string{}, card.Command...),
				RequiredInputs:     append([]string{}, card.RequiredInputs...),
				ExpectedValidation: append([]string{}, card.ExpectedValidation...),
				StopConditions:     append([]string{}, card.StopConditions...),
				Boundary:           card.Boundary,
				Proof:              card.Proof,
				EvidenceRefs:       refs,
			})
		}
	}
	return out, nil
}

func jobHealthCandidates(homePath string, now time.Time) ([]candidate, error) {
	roster, err := authority.LoadJobRosterAuthority(homePath)
	if err != nil {
		return nil, err
	}
	plans, err := authority.BuildJobHealthPlans(homePath, roster, now)
	if err != nil {
		return nil, err
	}
	var out []candidate
	for _, plan := range plans {
		if reviewStatusTerminal(plan.Status) {
			continue
		}
		refs := append([]string{}, plan.EvidenceRefs...)
		refs = append(refs, "job_health_plan:"+plan.ID)
		if plan.JobID != "" {
			refs = append(refs, "job:"+plan.JobID)
		}
		out = append(out, candidate{
			Kind:             "job_health",
			SourceID:         plan.ID,
			Title:            "Review job health for " + plan.JobName,
			Consequence:      "Adding this creates job-health review work for " + plan.JobName,
			CapabilityClass:  "judge-critique",
			Owner:            "compound-authority",
			CanonicalSurface: "director:job-health:" + plan.ID,
			SuccessPredicate: "Review failing scheduled job " + plan.JobName + " without executing or mutating scheduler state",
			DesiredOutcome:   "Adding this creates job-health review work for " + plan.JobName,
			Command:          []string{"compound", "authority", "job-health-plan"},
			RequiredInputs:   plan.ReviewDraft.RequiredEvidence,
			ExpectedValidation: []string{
				"`compound authority job-health-plan` records or updates the governed review item",
				"follow-up job roster import proves resolved, parked, disabled, no-action, or continuing debt",
			},
			StopConditions: []string{"repair requires scheduler mutation without a separate reviewed owner action"},
			Boundary:       plan.ReviewDraft.MutationGuardrail,
			Proof:          plan.ReviewDraft.Verification,
			EvidenceRefs:   refs,
			CreatedAt:      firstNonEmpty(plan.LastRunAt, plan.CreatedAt),
		})
	}
	return out, nil
}

// absorptionDebtCandidates turns os-absorption-doctor debt into director
// proposals (W5 J15 → W9 seam): the doctor already distinguishes machine
// proven (proof_status=ok) rows from asserted-but-unproven (manual) and
// broken (failed) rows; this scanner reads that existing doctor report plus
// the underlying matrix (both existing authority readers — no new state, no
// matrix mutation) and proposes the next retirement move so an orchestrator
// does not have to notice by hand.
//
// Every manual/failed row becomes a candidate (bounded further by the
// per-run cap and class-fair round-robin). Only ONE pending row is proposed
// per run, chosen by ascending row ID — the matrix's IDs are stable and
// lexicographic order is simplest to reason about and reproduce without
// encoding a second wave-order table that could drift from the workstream
// doc. If that row's retirement gate names an operator-only next step (the
// gate/consumer/proof text mentions "operator"), the proposal names the gate
// instead of instructing the gated step.
func absorptionDebtCandidates(homePath string, now time.Time) ([]candidate, error) {
	doctor, err := authority.OSAbsorptionDoctor(homePath)
	if err != nil {
		return nil, err
	}
	if !doctor.Installed || len(doctor.Rows) == 0 {
		return nil, nil
	}
	matrix, err := authority.LoadOSAbsorptionMatrix(homePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	rowsByID := make(map[string]authority.OSAbsorptionMatrixRow, len(matrix.Rows))
	for _, row := range matrix.Rows {
		rowsByID[row.ID] = row
	}

	var out []candidate
	var pending []authority.OSAbsorptionDoctorRow
	for _, dr := range doctor.Rows {
		switch dr.ProofStatus {
		case "manual":
			out = append(out, absorptionManualCandidate(dr, rowsByID[dr.ID]))
		case "failed":
			out = append(out, absorptionFailedCandidate(dr, rowsByID[dr.ID]))
		case "pending":
			pending = append(pending, dr)
		}
	}
	if len(pending) > 0 {
		sort.Slice(pending, func(i, j int) bool { return pending[i].ID < pending[j].ID })
		out = append(out, absorptionPendingCandidate(pending[0], rowsByID[pending[0].ID]))
	}
	return out, nil
}

func absorptionManualCandidate(dr authority.OSAbsorptionDoctorRow, row authority.OSAbsorptionMatrixRow) candidate {
	legacy := absorptionLegacySurface(dr, row)
	consequence := "Proving this row retires " + legacy + " as authority"
	return candidate{
		Kind:             "absorption_debt",
		SourceID:         dr.ID,
		Title:            "Wire the machine proof for " + dr.ID,
		Consequence:      consequence,
		CapabilityClass:  "judge-critique",
		Owner:            "compound-authority",
		CanonicalSurface: "director:absorption-debt:" + dr.ID,
		SuccessPredicate: "Wire an auto-proof for os-absorption row " + dr.ID + " so proof_status moves off manual (self-asserted) to ok (machine-checked) or failed",
		DesiredOutcome:   consequence,
		Command:          []string{"compound", "authority", "os-absorption-doctor", "--json"},
		RequiredInputs:   []string{"os-absorption doctor report for " + dr.ID, "the row's real consumer named in compound_owner/consumer"},
		ExpectedValidation: []string{
			"`compound authority os-absorption-doctor --json` reports proof_status=ok or proof_status=failed (never manual) for " + dr.ID,
		},
		StopConditions: []string{"the row has no real consumer yet to prove against"},
		Boundary:       "read-only auto-proof wiring in the absorption doctor; no matrix status-string change, no new registry",
		Proof:          "os-absorption doctor row " + dr.ID + " carries proof_status=ok with a proof_ref, or proof_status=failed with a proof_detail",
		EvidenceRefs:   absorptionEvidenceRefs(dr, row),
	}
}

func absorptionFailedCandidate(dr authority.OSAbsorptionDoctorRow, row authority.OSAbsorptionMatrixRow) candidate {
	legacy := absorptionLegacySurface(dr, row)
	consequence := "Proving this row retires " + legacy + " as authority"
	return candidate{
		Kind:             "absorption_debt",
		SourceID:         dr.ID,
		Title:            "Repair failed absorption proof for " + dr.ID,
		Consequence:      consequence,
		CapabilityClass:  "judge-critique",
		Owner:            "compound-authority",
		CanonicalSurface: "director:absorption-debt:" + dr.ID,
		SuccessPredicate: "Repair the failing auto-proof for os-absorption row " + dr.ID + " (" + firstNonEmpty(dr.ProofDetail, "no proof_detail recorded") + ") so proof_status returns to ok",
		DesiredOutcome:   consequence,
		Command:          []string{"compound", "authority", "os-absorption-doctor", "--json"},
		RequiredInputs:   []string{"os-absorption doctor proof_detail for " + dr.ID, "the row's real consumer named in compound_owner/consumer"},
		ExpectedValidation: []string{
			"`compound authority os-absorption-doctor --json` reports proof_status=ok with a proof_ref for " + dr.ID,
		},
		StopConditions: []string{"the underlying consumer/ledger this row proves against is itself gone"},
		Boundary:       "read-only auto-proof repair in the absorption doctor; no matrix status-string change, no new registry",
		Proof:          "os-absorption doctor row " + dr.ID + " carries proof_status=ok with a proof_ref",
		EvidenceRefs:   absorptionEvidenceRefs(dr, row),
	}
}

func absorptionPendingCandidate(dr authority.OSAbsorptionDoctorRow, row authority.OSAbsorptionMatrixRow) candidate {
	legacy := absorptionLegacySurface(dr, row)
	gate := absorptionOperatorGateText(row)
	if gate != "" {
		consequence := "Proving this row (" + dr.ID + ") retires " + legacy + " as authority, once the operator runs: " + gate
		return candidate{
			Kind:             "absorption_debt",
			SourceID:         dr.ID,
			Title:            "Blocked on operator: " + gate,
			Consequence:      consequence,
			CapabilityClass:  "judge-critique",
			Owner:            "compound-authority",
			CanonicalSurface: "director:absorption-debt:" + dr.ID,
			SuccessPredicate: "Confirm the operator-gated step for " + dr.ID + " ran; the director never runs it itself. Gate: " + gate,
			DesiredOutcome:   consequence,
			Command:          []string{"compound", "authority", "os-absorption-doctor", "--json"},
			RequiredInputs:   []string{"the row's retirement gate text", "operator confirmation the gated step ran"},
			ExpectedValidation: []string{
				"os-absorption doctor row " + dr.ID + " moves off pending only after the operator confirms the gated step ran",
			},
			StopConditions: []string{"blocked on operator: " + gate, "do not run ssh, DB export/delete, or any other operator-only command on this row's behalf"},
			Boundary:       "propose-only; never execute the operator-gated step named in the retirement gate",
			Proof:          "the operator confirms the gated step ran; a later os-absorption doctor run shows " + dr.ID + " progressed",
			EvidenceRefs:   absorptionEvidenceRefs(dr, row),
		}
	}
	consequence := "Proving this row retires " + legacy + " as authority"
	return candidate{
		Kind:             "absorption_debt",
		SourceID:         dr.ID,
		Title:            "Advance pending absorption row " + dr.ID,
		Consequence:      consequence,
		CapabilityClass:  "judge-critique",
		Owner:            "compound-authority",
		CanonicalSurface: "director:absorption-debt:" + dr.ID,
		SuccessPredicate: "Make bounded progress absorbing " + dr.ID + " (" + firstNonEmpty(row.CompoundOwner, dr.CompoundOwner) + ") toward its retirement gate",
		DesiredOutcome:   consequence,
		Command:          []string{"compound", "authority", "os-absorption-doctor", "--json"},
		RequiredInputs:   []string{"row retirement_gate/consumer text for " + dr.ID},
		ExpectedValidation: []string{
			"a later os-absorption doctor run shows " + dr.ID + " proof_status moved off pending (ok, failed, or an explicit operator gate)",
		},
		StopConditions: []string{"the next step requires operator-only action (ssh, DB export, credential use)"},
		Boundary:       "propose-only; no matrix mutation, no new registry",
		Proof:          "a follow-up os-absorption doctor run shows " + dr.ID + " no longer pending, or names why it remains blocked",
		EvidenceRefs:   absorptionEvidenceRefs(dr, row),
	}
}

// absorptionOperatorGateText reports the row's operator-only gate text, if
// any, by checking the same free-text fields the matrix already carries
// (retirement_gate, proof, consumer) for the word "operator" — the matrix
// schema is not mutated to add a new boolean field; this reads what is
// already there. Empty means the row's next step is not operator-gated.
func absorptionOperatorGateText(row authority.OSAbsorptionMatrixRow) string {
	for _, field := range []string{row.RetirementGate, row.Proof, row.Consumer} {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if strings.Contains(strings.ToLower(field), "operator") {
			return field
		}
	}
	return ""
}

func absorptionLegacySurface(dr authority.OSAbsorptionDoctorRow, row authority.OSAbsorptionMatrixRow) string {
	return firstNonEmpty(row.SourceRef, dr.SourceRef, dr.ID)
}

func absorptionEvidenceRefs(dr authority.OSAbsorptionDoctorRow, row authority.OSAbsorptionMatrixRow) []string {
	refs := []string{
		authority.OSAbsorptionAuthorityRef,
		"os_absorption_row:" + dr.ID,
	}
	if dr.ProofRef != "" {
		refs = append(refs, "os_absorption_proof_ref:"+dr.ProofRef)
	}
	if row.SourceRef != "" {
		refs = append(refs, "source_ref:"+row.SourceRef)
	}
	return reviewartifact.UniqueSorted(refs)
}

func actionContextFor(cand candidate, proposal Proposal) *work.WorkActionContext {
	expected := append([]string{}, cand.ExpectedValidation...)
	if len(proposal.RoutePlan.LaneJustification) > 0 {
		expected = append(expected, "suggested lane: "+strings.Join(proposal.RoutePlan.LaneJustification, " | "))
	}
	return &work.WorkActionContext{
		Kind:               "director_proposal",
		SourceRef:          proposal.TriggerRef,
		SourceID:           proposal.TriggerRef,
		Title:              cand.Title,
		ActionSummary:      cand.Consequence,
		Command:            append([]string{}, cand.Command...),
		RequiredInputs:     append([]string{}, cand.RequiredInputs...),
		ExpectedValidation: expected,
		StopConditions:     append([]string{}, cand.StopConditions...),
		Boundary:           cand.Boundary,
		Proof:              cand.Proof,
		EvidenceRefs:       append([]string{}, proposal.EvidenceRefs...),
	}
}

func nonGoalsFor(cand candidate) []string {
	out := []string{
		"do not execute this proposal while generating director output",
		"do not admit, claim, dispatch, advance, close, or mutate owner state without an operator act",
		"do not exceed director evidence boundary: " + firstNonEmpty(cand.Boundary, "use cited evidence refs only"),
	}
	for _, stop := range cand.StopConditions {
		if strings.TrimSpace(stop) != "" {
			out = append(out, "stop if: "+strings.TrimSpace(stop))
		}
	}
	return reviewartifact.Unique(out)
}

func proposalOwner(cand candidate) string {
	return firstNonEmpty(cand.Owner, "compound-director")
}

func loadDuplicateIndex(homePath string, store *work.Store) (duplicateIndex, []string) {
	idx := duplicateIndex{openWorkByTrigger: map[string][]string{}, plansByTrigger: map[string][]string{}}
	var warnings []string
	if store != nil {
		works, err := store.List()
		if err != nil {
			warnings = append(warnings, "open work duplicate scan skipped: "+err.Error())
		} else {
			for _, item := range works {
				if workStateTerminal(item.State) {
					continue
				}
				for _, ref := range item.EvidenceRefs {
					if strings.HasPrefix(ref, "director_trigger:") {
						idx.openWorkByTrigger[ref] = append(idx.openWorkByTrigger[ref], item.ID)
					}
				}
			}
		}
	}
	dir := filepath.Join(homePath, "create-plans")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			warnings = append(warnings, "prior director proposal scan skipped: "+err.Error())
		}
		return idx, warnings
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		plan, err := work.LoadCreatePlan(filepath.Join(dir, entry.Name()))
		if err != nil {
			warnings = append(warnings, "prior director proposal "+entry.Name()+" unreadable: "+err.Error())
			continue
		}
		if plan.Actor != DirectorActor && !containsRef(plan.Work.EvidenceRefs, "proposed_by:director") {
			continue
		}
		for _, ref := range plan.Work.EvidenceRefs {
			if strings.HasPrefix(ref, "director_trigger:") {
				idx.plansByTrigger[ref] = append(idx.plansByTrigger[ref], plan.ID)
			}
		}
	}
	return idx, warnings
}

func (idx duplicateIndex) find(triggerRef string) *SuppressedDuplicate {
	if workIDs := idx.openWorkByTrigger[triggerRef]; len(workIDs) > 0 {
		return &SuppressedDuplicate{TriggerRef: triggerRef, Reason: "open_work", ExistingWorkIDs: reviewartifact.UniqueSorted(workIDs)}
	}
	if planIDs := idx.plansByTrigger[triggerRef]; len(planIDs) > 0 {
		return &SuppressedDuplicate{TriggerRef: triggerRef, Reason: "prior_director_proposal", ExistingPlanIDs: reviewartifact.UniqueSorted(planIDs)}
	}
	return nil
}

func (idx duplicateIndex) addPlan(triggerRef, planID string) {
	idx.plansByTrigger[triggerRef] = append(idx.plansByTrigger[triggerRef], planID)
}

func recordReport(homePath string, report Report) (string, error) {
	dir := filepath.Join(homePath, "projections", "director", "runs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, report.ID+".json")
	return path, rewriteReport(path, report)
}

func recordApplyRunEnvelope(store *work.Store, report *Report, now time.Time) error {
	if store == nil {
		return fmt.Errorf("work_store_unavailable")
	}
	report.RunWorkID = idFor("work_", "director_run", report.ID)
	report.RunMoveType = "control_plane_repair"
	report.NoDelta = false
	if report.ProposalCount == 0 {
		report.RunMoveType = "audit_only"
		report.NoDelta = true
	}
	created, err := store.Create(work.CreateInput{
		WorkID:           report.RunWorkID,
		Owner:            "compound-director",
		Actor:            DirectorActor,
		CanonicalSurface: "director:run:" + report.ID,
		SuccessPredicate: "Record director propose --apply run receipt and closing transition",
		DesiredOutcome:   "Director apply run is enveloped with proposal, duplicate, and skip counts",
		NonGoals: []string{
			"do not admit, claim, advance, dispatch, or close proposed work generated by the director",
			"do not mutate scheduler, source, product, review, or memory owner state",
		},
		EvidenceRefs: directorRunEvidenceRefs(*report),
		State:        "running",
		Risk:         "tier1",
		Privacy:      "private",
	}, now)
	if err != nil {
		return err
	}
	closed, err := store.Close(created.ID, work.CloseInput{
		Outcome:   "completed",
		MoveType:  report.RunMoveType,
		Actor:     DirectorActor,
		Summary:   directorRunSummary(*report),
		Changed:   report.ProposalCount > 0,
		ProofRefs: directorRunEvidenceRefs(*report),
	}, now)
	if err != nil {
		return err
	}
	report.RunWorkID = closed.Work.ID
	report.RunReceiptID = closed.Receipt.ID
	report.RunTransitionID = closed.Transition.ID
	return nil
}

func directorRunSummary(report Report) string {
	parts := []string{
		"director propose --apply",
		fmt.Sprintf("proposal_count=%d", report.ProposalCount),
		fmt.Sprintf("suppressed_duplicate_count=%d", report.SuppressedDuplicateCount),
		fmt.Sprintf("skipped_count=%d", report.SkippedCount),
	}
	if len(report.SkipHistogram) > 0 {
		parts = append(parts, "skip_histogram="+histogramString(report.SkipHistogram))
	}
	if report.NoDelta {
		parts = append(parts, "no_delta=true")
	}
	return strings.Join(parts, "; ")
}

func directorRunEvidenceRefs(report Report) []string {
	refs := []string{
		"director_run:" + report.ID,
		fmt.Sprintf("director_counts:proposal_count=%d,suppressed_duplicate_count=%d,skipped_count=%d", report.ProposalCount, report.SuppressedDuplicateCount, report.SkippedCount),
	}
	if report.ResultPath != "" {
		refs = append(refs, "director_run_report:"+filepath.ToSlash(report.ResultPath))
	}
	if report.NoDelta {
		refs = append(refs, "no_delta:director_apply_empty")
	}
	for _, class := range sortedHistogramKeys(report.SkipHistogram) {
		refs = append(refs, fmt.Sprintf("director_skip_histogram:%s=%d", class, report.SkipHistogram[class]))
	}
	return reviewartifact.UniqueSorted(refs)
}

func histogramString(histogram map[string]int) string {
	var parts []string
	for _, class := range sortedHistogramKeys(histogram) {
		parts = append(parts, fmt.Sprintf("%s:%d", class, histogram[class]))
	}
	return strings.Join(parts, ",")
}

func sortedHistogramKeys(histogram map[string]int) []string {
	var keys []string
	for key := range histogram {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func rewriteReport(path string, report Report) error {
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o644)
}

func reviewEvidenceRefs(item reviewartifact.QueueItem) []string {
	ref := "review:" + reviewSourceID(item)
	return reviewartifact.UniqueSorted(append(append([]string{}, item.EvidenceRefs...), ref))
}

func reviewSourceID(item reviewartifact.QueueItem) string {
	if item.Area == "" {
		return item.ID
	}
	return item.Area + "/" + item.ID
}

func reviewStatusTerminal(status string) bool {
	switch strings.TrimSpace(status) {
	case "ok", "applied", "resolved", "accepted", "recorded", "candidate_recorded":
		return true
	default:
		return false
	}
}

func workStateTerminal(state string) bool {
	switch state {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func triggerRefFor(cand candidate) string {
	key := strings.Join([]string{cand.Kind, cand.SourceID, strings.Join(compactRefs(cand.EvidenceRefs), "|")}, "|")
	return "director_trigger:" + cand.Kind + ":" + digest12(key)
}

func compactRefs(refs []string) []string {
	var out []string
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref != "" {
			out = append(out, ref)
		}
	}
	return reviewartifact.UniqueSorted(out)
}

func containsRef(refs []string, want string) bool {
	for _, ref := range refs {
		if strings.TrimSpace(ref) == want {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func idFor(prefix string, parts ...string) string {
	return prefix + digest12(strings.Join(parts, "|"))
}

func digest12(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func timestamp(now time.Time) string {
	return fmt.Sprintf("%d", now.UTC().UnixNano())
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "unknown"
	}
	return out
}
