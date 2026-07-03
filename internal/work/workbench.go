package work

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/envelope"
)

// WorkbenchOptions controls the read-only workbench projection.
type WorkbenchOptions struct {
	RecentLimit                     int
	Actor                           string
	ActivePacketSurface             string
	ActivePacketExpectedEvidenceRef string
	IncludeArchived                 bool
}

// WorkbenchReport is a read model over WorkEnvelope, Receipt, and Transition
// state. It does not dispatch, claim, close, or repair work.
type WorkbenchReport struct {
	GeneratedAt       string                   `json:"generated_at"`
	Counts            WorkbenchCounts          `json:"counts"`
	Open              []WorkbenchItem          `json:"open"`
	Recent            []WorkbenchItem          `json:"recent,omitempty"`
	ActivePacketProof *ActivePacketProofReport `json:"active_packet_proof,omitempty"`
	DispatchNext      WorkbenchDispatchNext    `json:"dispatch_next"`
	DispatchStepRuns  []DispatchStepRunSummary `json:"dispatch_step_runs,omitempty"`
	Integrity         []string                 `json:"integrity,omitempty"`
}

type WorkbenchCounts struct {
	Total             int            `json:"total"`
	Open              int            `json:"open"`
	Terminal          int            `json:"terminal"`
	NeedsClaim        int            `json:"needs_claim"`
	NeedsReview       int            `json:"needs_review"`
	InProgress        int            `json:"in_progress"`
	RunObservations   int            `json:"run_observations"`
	DispatchStepRuns  int            `json:"dispatch_step_runs"`
	MissingReceipt    int            `json:"missing_receipt"`
	MissingTransition int            `json:"missing_transition"`
	ByState           map[string]int `json:"by_state"`
}

type DispatchStepRunSummary struct {
	ID          string   `json:"id"`
	GeneratedAt string   `json:"generated_at"`
	Status      string   `json:"status"`
	Actor       string   `json:"actor"`
	StepCount   int      `json:"step_count"`
	CanContinue bool     `json:"can_continue"`
	WaitReason  string   `json:"wait_reason,omitempty"`
	BlockedOn   []string `json:"blocked_on,omitempty"`
	NextAction  string   `json:"next_action,omitempty"`
	ResultPath  string   `json:"result_path,omitempty"`
	StepActions []string `json:"step_actions,omitempty"`
	StepWorkIDs []string `json:"step_work_ids,omitempty"`
}

type WorkbenchDispatchNext struct {
	Actor      string   `json:"actor"`
	CanRun     bool     `json:"can_run"`
	Command    []string `json:"command,omitempty"`
	Action     string   `json:"action,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	WorkID     string   `json:"work_id,omitempty"`
	WaitReason string   `json:"wait_reason,omitempty"`
	BlockedOn  []string `json:"blocked_on,omitempty"`
}

type WorkbenchItem struct {
	WorkID             string                  `json:"work_id"`
	State              string                  `json:"state"`
	Owner              string                  `json:"owner,omitempty"`
	ClaimedBy          string                  `json:"claimed_by,omitempty"`
	ClaimedAt          string                  `json:"claimed_at,omitempty"`
	SuccessPredicate   string                  `json:"success_predicate"`
	DesiredOutcome     string                  `json:"desired_outcome,omitempty"`
	CanonicalSurface   string                  `json:"canonical_surface,omitempty"`
	ContextPackID      string                  `json:"context_pack_id,omitempty"`
	PolicyDecisionID   string                  `json:"policy_decision_id,omitempty"`
	ReceiptID          string                  `json:"receipt_id,omitempty"`
	TransitionID       string                  `json:"transition_id,omitempty"`
	MoveType           string                  `json:"move_type,omitempty"`
	Changed            bool                    `json:"changed,omitempty"`
	ReviewTrigger      string                  `json:"review_trigger,omitempty"`
	ObservatoryTags    []string                `json:"observatory_tags,omitempty"`
	NextAction         string                  `json:"next_action"`
	OpenReason         string                  `json:"open_reason,omitempty"`
	EvidenceRefs       []string                `json:"evidence_refs,omitempty"`
	ArtifactIDs        []string                `json:"artifact_ids,omitempty"`
	VerificationIDs    []string                `json:"verification_ids,omitempty"`
	MemoryCandidateIDs []string                `json:"memory_candidate_ids,omitempty"`
	RunObservations    []RunObservationSummary `json:"run_observations,omitempty"`
	recentAt           string
}

type RunObservationSummary struct {
	ID               string `json:"id"`
	GeneratedAt      string `json:"generated_at,omitempty"`
	Actor            string `json:"actor,omitempty"`
	Note             string `json:"note,omitempty"`
	TraceID          string `json:"trace_id"`
	State            string `json:"state"`
	ActionIntentID   string `json:"action_intent_id,omitempty"`
	PolicyDecisionID string `json:"policy_decision_id,omitempty"`
	ResultPath       string `json:"result_path,omitempty"`
}

func (s *Store) Workbench(now time.Time, opts WorkbenchOptions) (WorkbenchReport, error) {
	if opts.RecentLimit <= 0 {
		opts.RecentLimit = 5
	}
	report := WorkbenchReport{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Counts:      WorkbenchCounts{ByState: map[string]int{}},
		Open:        []WorkbenchItem{},
	}
	var works []envelope.WorkEnvelope
	var err error
	if opts.IncludeArchived {
		works, err = s.ListWithArchived()
	} else {
		works, err = s.List()
	}
	if err != nil {
		return report, err
	}
	report.Counts.Total = len(works)
	for _, w := range works {
		events, err := s.eventsFor(w.ID)
		if err != nil {
			return report, err
		}
		item := s.workbenchItem(w, events, &report)
		report.Counts.RunObservations += len(item.RunObservations)
		report.Counts.ByState[w.State]++
		if terminalStates[w.State] {
			report.Counts.Terminal++
			if terminalProofRequired(w.State) && item.ReceiptID == "" {
				report.Counts.MissingReceipt++
			}
			if terminalProofRequired(w.State) && item.TransitionID == "" {
				report.Counts.MissingTransition++
			}
			report.Recent = append(report.Recent, item)
			continue
		}
		report.Counts.Open++
		switch w.State {
		case "ready", "admitted", "proposed":
			report.Counts.NeedsClaim++
		case "held", "blocked":
			report.Counts.NeedsReview++
		case "claimed", "running", "verifying":
			report.Counts.InProgress++
		}
		report.Open = append(report.Open, item)
	}
	runs, warnings := s.workbenchDispatchStepRuns(opts.RecentLimit)
	report.DispatchStepRuns = runs
	report.Counts.DispatchStepRuns = len(runs)
	for _, warning := range warnings {
		report.Integrity = append(report.Integrity, warning)
	}
	activePacket, activeWarnings := s.workbenchActivePacketProof(now, opts.ActivePacketSurface, opts.ActivePacketExpectedEvidenceRef)
	report.ActivePacketProof = activePacket
	for _, warning := range activeWarnings {
		report.Integrity = append(report.Integrity, warning)
	}
	sort.Slice(report.Open, func(i, j int) bool {
		pi, pj := statePriority(report.Open[i].State), statePriority(report.Open[j].State)
		if pi != pj {
			return pi < pj
		}
		return report.Open[i].WorkID < report.Open[j].WorkID
	})
	report.DispatchNext = workbenchDispatchNext(report, opts.Actor)
	sort.SliceStable(report.Recent, func(i, j int) bool {
		if report.Recent[i].recentAt != report.Recent[j].recentAt {
			return workbenchTimestampAfter(report.Recent[i].recentAt, report.Recent[j].recentAt)
		}
		return report.Recent[i].WorkID > report.Recent[j].WorkID
	})
	if len(report.Recent) > opts.RecentLimit {
		report.Recent = report.Recent[:opts.RecentLimit]
	}
	return report, nil
}

func (s *Store) workbenchActivePacketProof(now time.Time, surface, expectedPacketEvidenceRef string) (*ActivePacketProofReport, []string) {
	proof, err := s.ActivePacketProof(now, surface, expectedPacketEvidenceRef)
	if err != nil {
		return nil, []string{"active-packet-proof unreadable: " + err.Error()}
	}
	if strings.TrimSpace(proof.WorkID) == "" {
		return nil, nil
	}
	return &proof, nil
}

func (s *Store) workbenchItem(w envelope.WorkEnvelope, events []Event, report *WorkbenchReport) WorkbenchItem {
	item := WorkbenchItem{
		WorkID:             w.ID,
		State:              w.State,
		Owner:              w.Owner,
		SuccessPredicate:   w.SuccessPredicate,
		DesiredOutcome:     w.DesiredOutcome,
		CanonicalSurface:   w.CanonicalSurface,
		ContextPackID:      w.IDs.ContextPackID,
		PolicyDecisionID:   w.PolicyDecisionID,
		TransitionID:       w.IDs.TransitionID,
		NextAction:         nextWorkAction(w.State),
		OpenReason:         openReason(w.State),
		EvidenceRefs:       append([]string{}, w.EvidenceRefs...),
		ArtifactIDs:        append([]string{}, w.ArtifactIDs...),
		VerificationIDs:    append([]string{}, w.VerificationIDs...),
		MemoryCandidateIDs: append([]string{}, w.MemoryCandidateIDs...),
		recentAt:           w.CreatedAt,
	}
	if observations, warnings := s.workbenchRunObservations(w.ID); len(observations) > 0 || len(warnings) > 0 {
		item.RunObservations = observations
		for _, warning := range warnings {
			report.Integrity = append(report.Integrity, w.ID+": "+warning)
		}
	}
	if lease, err := s.readLease(w.ID); err == nil {
		item.ClaimedBy = lease.By
		item.ClaimedAt = lease.ClaimAt
	}
	item.ReceiptID = lastAttachedWithPrefix(events, "rec_")
	if item.ReceiptID != "" {
		var rec envelope.Receipt
		if err := s.readJSON(s.envPath(item.ReceiptID), &rec); err != nil {
			report.Integrity = append(report.Integrity, w.ID+": receipt "+item.ReceiptID+" unreadable: "+err.Error())
		} else if err := rec.Validate(); err != nil {
			report.Integrity = append(report.Integrity, w.ID+": receipt "+item.ReceiptID+" invalid: "+err.Error())
		} else {
			item.MoveType = rec.MoveType
			item.Changed = rec.EffectiveChanged()
			item.EvidenceRefs = mergeUnique(item.EvidenceRefs, rec.ProofRefs)
			item.ReviewTrigger = rec.Next.ReviewTrigger
			item.recentAt = workbenchLatestTimestamp(item.recentAt, rec.CreatedAt)
		}
	}
	if item.TransitionID == "" {
		item.TransitionID = lastAttachedWithPrefix(events, "tr_")
	}
	if terminalStates[w.State] && item.TransitionID != "" {
		var tr envelope.Transition
		if err := s.readJSON(s.envPath(item.TransitionID), &tr); err != nil {
			report.Integrity = append(report.Integrity, w.ID+": transition "+item.TransitionID+" unreadable: "+err.Error())
		} else if err := tr.Validate(); err != nil {
			report.Integrity = append(report.Integrity, w.ID+": transition "+item.TransitionID+" invalid: "+err.Error())
		} else {
			item.recentAt = workbenchLatestTimestamp(item.recentAt, tr.CreatedAt)
		}
	}
	item.ObservatoryTags = workbenchObservatoryTags(item)
	return item
}

func (s *Store) workbenchRunObservations(workID string) ([]RunObservationSummary, []string) {
	dir := filepath.Join(s.home, "run-observations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{"run observations unreadable: " + err.Error()}
	}
	out := []RunObservationSummary{}
	warnings := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		var result RunObservationResult
		if err := s.readJSON(path, &result); err != nil {
			warnings = append(warnings, "run observation "+entry.Name()+" unreadable: "+err.Error())
			continue
		}
		if result.WorkID != workID {
			continue
		}
		out = append(out, RunObservationSummary{
			ID:               result.ID,
			GeneratedAt:      result.GeneratedAt,
			Actor:            result.Actor,
			Note:             result.Note,
			TraceID:          result.TraceID,
			State:            result.State,
			ActionIntentID:   result.ActionIntentID,
			PolicyDecisionID: result.PolicyDecisionID,
			ResultPath:       path,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GeneratedAt != out[j].GeneratedAt {
			return out[i].GeneratedAt > out[j].GeneratedAt
		}
		return out[i].ID > out[j].ID
	})
	return out, warnings
}

func (s *Store) workbenchDispatchStepRuns(limit int) ([]DispatchStepRunSummary, []string) {
	dir := filepath.Join(s.home, "dispatch-step-runs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{"dispatch-step-runs unreadable: " + err.Error()}
	}
	var out []DispatchStepRunSummary
	var warnings []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		var result DispatchStepRunResult
		if err := s.readJSON(path, &result); err != nil {
			warnings = append(warnings, "dispatch step run "+entry.Name()+" unreadable: "+err.Error())
			continue
		}
		summary := DispatchStepRunSummary{
			ID:          result.ID,
			GeneratedAt: result.GeneratedAt,
			Status:      result.Status,
			Actor:       result.Actor,
			StepCount:   result.StepCount,
			CanContinue: result.CanContinue,
			WaitReason:  result.WaitReason,
			BlockedOn:   append([]string{}, result.BlockedOn...),
			NextAction:  result.NextAction,
			ResultPath:  path,
		}
		for _, step := range result.Steps {
			summary.StepActions = append(summary.StepActions, step.Action)
			if step.WorkID != "" {
				summary.StepWorkIDs = append(summary.StepWorkIDs, step.WorkID)
			}
		}
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GeneratedAt != out[j].GeneratedAt {
			return out[i].GeneratedAt > out[j].GeneratedAt
		}
		return out[i].ID > out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, warnings
}

func workbenchDispatchNext(report WorkbenchReport, actor string) WorkbenchDispatchNext {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "codex"
	}
	out := WorkbenchDispatchNext{Actor: actor}
	for _, item := range report.Open {
		if item.State == "verifying" && hasCloseProofEvidence(item) {
			out.CanRun = true
			out.Action = "close"
			out.Reason = "proof_ready_close"
			out.WorkID = item.WorkID
			break
		}
	}
	if !out.CanRun {
		for _, item := range report.Open {
			if item.State == "claimed" && item.ClaimedBy == actor {
				out.CanRun = true
				out.Action = "advance"
				out.Reason = "actor_claimed_work"
				out.WorkID = item.WorkID
				break
			}
		}
	}
	if !out.CanRun {
		for _, item := range report.Open {
			if item.State == "ready" {
				out.CanRun = true
				out.Action = "claim"
				out.Reason = "ready_work"
				out.WorkID = item.WorkID
				break
			}
		}
	}
	if out.CanRun {
		out.Command = []string{"compound", "work", "dispatch-step", "--actor", actor, "--until-wait"}
		return out
	}
	if report.Counts.Open == 0 && report.ActivePacketProof != nil {
		out.Action = "none"
		out.WorkID = report.ActivePacketProof.WorkID
		switch report.ActivePacketProof.Status {
		case "ok":
			out.Reason = "active_packet_terminal"
			out.WaitReason = "source_packet_terminal"
			out.BlockedOn = []string{"work.terminal", "work.terminal:" + report.ActivePacketProof.WorkID}
			return out
		case "risk":
			out.Reason = "active_packet_proof_risk"
			out.WaitReason = "active_packet_proof_risk"
			out.BlockedOn = []string{"active_packet_proof:" + report.ActivePacketProof.WorkID}
			return out
		}
	}
	out.WaitReason, out.BlockedOn = dispatchStepWait(DispatchStepResult{Actor: actor}, report)
	return out
}

func lastAttachedWithPrefix(events []Event, prefix string) string {
	for i := len(events) - 1; i >= 0; i-- {
		for j := len(events[i].Attached) - 1; j >= 0; j-- {
			if strings.HasPrefix(events[i].Attached[j], prefix) {
				return events[i].Attached[j]
			}
		}
	}
	return ""
}

func nextWorkAction(state string) string {
	switch state {
	case "proposed":
		return "admit or cancel after owner review"
	case "admitted":
		return "mark ready when context and policy are available"
	case "ready":
		return "claim before touching"
	case "claimed":
		return "advance running or release to ready"
	case "running":
		return "advance verifying, completed, blocked, held, or failed"
	case "verifying":
		return "close completed or failed, or return running"
	case "held":
		return "resolve policy/source-control hold, then ready or cancel"
	case "blocked":
		return "resolve blocker, then running, failed, or cancel"
	case "completed", "failed", "cancelled":
		return "terminal; mine receipt and transition if useful"
	default:
		return "inspect work state"
	}
}

func openReason(state string) string {
	switch state {
	case "proposed", "admitted", "ready":
		return "needs_claim"
	case "held", "blocked":
		return "needs_review"
	case "claimed", "running", "verifying":
		return "in_progress"
	default:
		return ""
	}
}

func workbenchObservatoryTags(item WorkbenchItem) []string {
	var tags []string
	seen := map[string]bool{}
	add := func(tag string) {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			return
		}
		seen[tag] = true
		tags = append(tags, tag)
	}
	addFacet := func(facet, value string) {
		if value = workbenchTagValue(value); value != "" {
			add(facet + ":" + value)
		}
	}

	addFacet("state", item.State)
	if item.OpenReason != "" {
		addFacet("attention", item.OpenReason)
	} else if terminalStates[item.State] {
		add("attention:terminal")
	}
	if item.Owner != "" {
		addFacet("owner", item.Owner)
	}
	if item.ClaimedBy != "" {
		addFacet("actor", item.ClaimedBy)
	}
	if item.CanonicalSurface != "" {
		addFacet("surface", item.CanonicalSurface)
	}
	if item.ContextPackID != "" {
		add("context:compiled")
	}
	if item.PolicyDecisionID != "" {
		add("policy:decided")
	}
	if item.MoveType != "" {
		addFacet("move", item.MoveType)
		switch item.MoveType {
		case "memory_delta":
			add("recipient:memory")
		case "rule_delta":
			add("recipient:rule")
		case "outward_delta":
			add("recipient:outward")
		case "control_plane_repair":
			add("recipient:control-plane")
		case "audit_only":
			add("recipient:audit")
		}
		if item.Changed {
			add("change:changed")
		} else {
			add("change:unchanged")
		}
	}
	if item.ReviewTrigger != "" {
		addFacet("review", item.ReviewTrigger)
	}
	if len(item.RunObservations) > 0 {
		add("evidence:run-observed")
	}
	if len(item.EvidenceRefs) > 0 {
		add("evidence:source")
	}
	if len(item.ArtifactIDs) > 0 {
		add("evidence:artifact")
	}
	if len(item.VerificationIDs) > 0 {
		add("evidence:verification")
	}
	if len(item.MemoryCandidateIDs) > 0 {
		add("memory:candidates")
	}
	if terminalProofRequired(item.State) {
		if item.ReceiptID == "" {
			add("proof:missing-receipt")
		} else {
			add("proof:receipt")
		}
		if item.TransitionID == "" {
			add("proof:missing-transition")
		} else {
			add("proof:transition")
		}
		if item.ReceiptID != "" && item.TransitionID != "" {
			add("proof:complete")
		}
	}
	return tags
}

func workbenchTagValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		keep := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if keep {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if b.Len() > 0 && !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func terminalProofRequired(state string) bool {
	return state == "completed" || state == "failed"
}

func workbenchLatestTimestamp(a, b string) string {
	if strings.TrimSpace(a) == "" {
		return b
	}
	if workbenchTimestampAfter(b, a) {
		return b
	}
	return a
}

func workbenchTimestampAfter(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" {
		return false
	}
	if b == "" {
		return true
	}
	at, aerr := time.Parse(time.RFC3339Nano, a)
	bt, berr := time.Parse(time.RFC3339Nano, b)
	if aerr == nil && berr == nil {
		return at.After(bt)
	}
	return a > b
}

func statePriority(state string) int {
	switch state {
	case "held":
		return 0
	case "blocked":
		return 1
	case "running", "verifying":
		return 2
	case "claimed":
		return 3
	case "ready":
		return 4
	case "admitted":
		return 5
	case "proposed":
		return 6
	default:
		return 9
	}
}
