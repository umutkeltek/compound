package work

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DispatchPlanSchemaVersion = "compound.work_dispatch_plan.v0.1"
const DispatchClaimSchemaVersion = "compound.work_dispatch_claim.v0.1"
const DispatchAdvanceSchemaVersion = "compound.work_dispatch_advance.v0.1"

type DispatchPlanOptions struct {
	Actor                 string
	Record                bool
	RecentLimit           int
	SourcePacketPath      string
	SourcePacketState     string
	SourcePacketOwner     string
	SourcePacketRisk      string
	SourcePacketPrivacy   string
	SourcePacketContextID string
}

type DispatchPlan struct {
	SchemaVersion string              `json:"schema_version"`
	ID            string              `json:"id"`
	GeneratedAt   string              `json:"generated_at"`
	Status        string              `json:"status"`
	Mode          string              `json:"mode"`
	Actor         string              `json:"actor"`
	PlanPath      string              `json:"plan_path,omitempty"`
	Source        *DispatchPlanSource `json:"source,omitempty"`
	Counts        WorkbenchCounts     `json:"counts"`
	Selected      *DispatchPlanItem   `json:"selected,omitempty"`
	Candidates    []DispatchPlanItem  `json:"candidates"`
	NoAction      string              `json:"no_action,omitempty"`
	Warnings      []string            `json:"warnings,omitempty"`
	Integrity     []string            `json:"integrity,omitempty"`
}

type DispatchPlanSource struct {
	Kind         string `json:"kind"`
	Path         string `json:"path"`
	Created      bool   `json:"created"`
	WorkID       string `json:"work_id"`
	State        string `json:"state"`
	PacketTitle  string `json:"packet_title,omitempty"`
	EvidenceRef  string `json:"evidence_ref,omitempty"`
	SourceDigest string `json:"source_digest,omitempty"`
}

type DispatchPlanItem struct {
	WorkID           string                `json:"work_id"`
	State            string                `json:"state"`
	Owner            string                `json:"owner,omitempty"`
	SuccessPredicate string                `json:"success_predicate,omitempty"`
	DesiredOutcome   string                `json:"desired_outcome,omitempty"`
	Action           string                `json:"action"`
	Command          []string              `json:"command,omitempty"`
	Reason           string                `json:"reason"`
	NextAction       string                `json:"next_action,omitempty"`
	EvidenceRefs     []string              `json:"evidence_refs,omitempty"`
	ClaimPolicy      *ClaimPolicySummary   `json:"claim_policy,omitempty"`
	AdvancePolicy    *AdvancePolicySummary `json:"advance_policy,omitempty"`
}

type DispatchClaimResult struct {
	SchemaVersion    string            `json:"schema_version"`
	ID               string            `json:"id"`
	GeneratedAt      string            `json:"generated_at"`
	Status           string            `json:"status"`
	Actor            string            `json:"actor"`
	ResultPath       string            `json:"result_path,omitempty"`
	DispatchPlanID   string            `json:"dispatch_plan_id"`
	DispatchPlanPath string            `json:"dispatch_plan_path,omitempty"`
	Selected         *DispatchPlanItem `json:"selected,omitempty"`
	ClaimedWorkID    string            `json:"claimed_work_id,omitempty"`
	ClaimedState     string            `json:"claimed_state,omitempty"`
	ClaimedBy        string            `json:"claimed_by,omitempty"`
	ActionIntentID   string            `json:"action_intent_id,omitempty"`
	PolicyDecisionID string            `json:"policy_decision_id,omitempty"`
	NoAction         string            `json:"no_action,omitempty"`
	Warnings         []string          `json:"warnings,omitempty"`
	Plan             DispatchPlan      `json:"dispatch_plan"`
}

type DispatchAdvanceResult struct {
	SchemaVersion    string             `json:"schema_version"`
	ID               string             `json:"id"`
	GeneratedAt      string             `json:"generated_at"`
	Status           string             `json:"status"`
	Actor            string             `json:"actor"`
	ResultPath       string             `json:"result_path,omitempty"`
	Counts           WorkbenchCounts    `json:"counts"`
	Selected         *DispatchPlanItem  `json:"selected,omitempty"`
	Candidates       []DispatchPlanItem `json:"candidates"`
	AdvancedWorkID   string             `json:"advanced_work_id,omitempty"`
	FromState        string             `json:"from_state,omitempty"`
	AdvancedState    string             `json:"advanced_state,omitempty"`
	ActionIntentID   string             `json:"action_intent_id,omitempty"`
	PolicyDecisionID string             `json:"policy_decision_id,omitempty"`
	AdvancePlan      *AdvancePlan       `json:"advance_plan,omitempty"`
	NoAction         string             `json:"no_action,omitempty"`
	Warnings         []string           `json:"warnings,omitempty"`
	Integrity        []string           `json:"integrity,omitempty"`
}

func (s *Store) DispatchPlan(now time.Time, opts DispatchPlanOptions) (DispatchPlan, error) {
	actor := strings.TrimSpace(opts.Actor)
	if actor == "" {
		actor = "codex"
	}
	var source *DispatchPlanSource
	if strings.TrimSpace(opts.SourcePacketPath) != "" {
		state := strings.TrimSpace(opts.SourcePacketState)
		if state == "" {
			state = "ready"
		}
		imported, err := s.ImportPacket(opts.SourcePacketPath, PacketImportOptions{
			Owner:         opts.SourcePacketOwner,
			Actor:         actor,
			State:         state,
			Risk:          opts.SourcePacketRisk,
			Privacy:       opts.SourcePacketPrivacy,
			ContextPackID: opts.SourcePacketContextID,
		}, now)
		if err != nil {
			return DispatchPlan{}, err
		}
		source = &DispatchPlanSource{
			Kind:         "packet",
			Path:         opts.SourcePacketPath,
			Created:      imported.Created,
			WorkID:       imported.Work.ID,
			State:        imported.Work.State,
			PacketTitle:  imported.PacketTitle,
			EvidenceRef:  imported.EvidenceRef,
			SourceDigest: imported.SourceDigest,
		}
	}
	report, err := s.Workbench(now, WorkbenchOptions{RecentLimit: opts.RecentLimit})
	if err != nil {
		return DispatchPlan{}, err
	}
	plan := DispatchPlan{
		SchemaVersion: DispatchPlanSchemaVersion,
		ID:            idFor("wdp_", actor, ts(now)),
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Status:        "no_open_work",
		Mode:          "proposal_only",
		Actor:         actor,
		Source:        source,
		Counts:        report.Counts,
		Candidates:    []DispatchPlanItem{},
		Warnings: []string{
			"does not claim work",
			"does not execute work",
			"does not close work",
			"source packet import creates only source-backed WorkEnvelope state",
		},
		Integrity: append([]string{}, report.Integrity...),
	}
	policyBlocked := false
	for _, item := range report.Open {
		candidate := dispatchCandidate(item, actor)
		if candidate.Action == "claim" {
			claimPlan, err := s.ClaimPlan(item.WorkID, now, ClaimPlanOptions{Actor: actor})
			if err != nil {
				candidate.Reason = strings.TrimSpace(candidate.Reason + "; claim policy plan failed: " + err.Error())
				policyBlocked = true
			} else {
				policySummary := claimPlan.Policy
				candidate.ClaimPolicy = &policySummary
				if !policySummary.Allowed {
					candidate.Reason = strings.TrimSpace(candidate.Reason + "; claim policy " + policySummary.Outcome)
					policyBlocked = true
				}
			}
		}
		plan.Candidates = append(plan.Candidates, candidate)
		if plan.Selected == nil && candidate.Action == "claim" && candidate.ClaimPolicy != nil && candidate.ClaimPolicy.Allowed {
			selected := candidate
			plan.Selected = &selected
			plan.Status = "ready_to_claim"
		}
	}
	if plan.Selected == nil {
		switch {
		case len(plan.Candidates) == 0:
			plan.NoAction = "workbench has no open work"
		case policyBlocked:
			plan.Status = "claim_policy_blocked"
			plan.NoAction = "ready work exists but claim policy does not allow dispatch"
		case report.Counts.NeedsReview > 0:
			plan.Status = "needs_review"
			plan.NoAction = "open work exists but requires review before dispatch"
		case report.Counts.InProgress > 0:
			plan.Status = "in_progress_only"
			plan.NoAction = "open work is already claimed or running"
		default:
			plan.Status = "no_claimable_work"
			plan.NoAction = "open work is not ready to claim"
		}
	}
	if opts.Record {
		path, err := s.RecordDispatchPlan(plan)
		if err != nil {
			return DispatchPlan{}, err
		}
		plan.PlanPath = path
	}
	return plan, nil
}

func (s *Store) RecordDispatchPlan(plan DispatchPlan) (string, error) {
	if strings.TrimSpace(plan.ID) == "" {
		return "", os.ErrInvalid
	}
	dir := filepath.Join(s.home, "dispatch-plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, plan.ID+".json")
	if err := s.writeJSON(path, &plan); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) DispatchClaim(now time.Time, opts DispatchPlanOptions) (DispatchClaimResult, error) {
	opts.Record = true
	plan, err := s.DispatchPlan(now, opts)
	if err != nil {
		return DispatchClaimResult{}, err
	}
	result := DispatchClaimResult{
		SchemaVersion:    DispatchClaimSchemaVersion,
		ID:               idFor("wdc_", plan.ID, ts(now)),
		GeneratedAt:      now.UTC().Format(time.RFC3339),
		Status:           plan.Status,
		Actor:            plan.Actor,
		DispatchPlanID:   plan.ID,
		DispatchPlanPath: plan.PlanPath,
		Selected:         plan.Selected,
		NoAction:         plan.NoAction,
		Warnings: []string{
			"claims at most one selected ready work item",
			"does not execute work",
			"does not close work",
		},
		Plan: plan,
	}
	if plan.Selected == nil {
		path, err := s.RecordDispatchClaim(result)
		if err != nil {
			return DispatchClaimResult{}, err
		}
		result.ResultPath = path
		return result, nil
	}
	if plan.Selected.Action != "claim" {
		result.NoAction = "selected work is not claimable"
		path, err := s.RecordDispatchClaim(result)
		if err != nil {
			return DispatchClaimResult{}, err
		}
		result.ResultPath = path
		return result, nil
	}
	if plan.Selected.ClaimPolicy == nil || !plan.Selected.ClaimPolicy.Allowed {
		result.Status = "claim_policy_blocked"
		result.NoAction = "selected work claim policy does not allow dispatch"
		path, err := s.RecordDispatchClaim(result)
		if err != nil {
			return DispatchClaimResult{}, err
		}
		result.ResultPath = path
		return result, nil
	}
	claimed, err := s.Claim(plan.Selected.WorkID, plan.Actor, now)
	if err != nil {
		return DispatchClaimResult{}, err
	}
	result.Status = "claimed"
	result.NoAction = ""
	result.ClaimedWorkID = claimed.ID
	result.ClaimedState = claimed.State
	result.ClaimedBy = claimed.Authority.Owner
	result.PolicyDecisionID = claimed.PolicyDecisionID
	if len(claimed.RunIDs) > 0 {
		result.ActionIntentID = claimed.RunIDs[len(claimed.RunIDs)-1]
	}
	path, err := s.RecordDispatchClaim(result)
	if err != nil {
		return DispatchClaimResult{}, err
	}
	result.ResultPath = path
	return result, nil
}

func (s *Store) RecordDispatchClaim(result DispatchClaimResult) (string, error) {
	if strings.TrimSpace(result.ID) == "" {
		return "", os.ErrInvalid
	}
	dir := filepath.Join(s.home, "dispatch-claims")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, result.ID+".json")
	if err := s.writeJSON(path, &result); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) DispatchAdvance(now time.Time, opts DispatchPlanOptions) (DispatchAdvanceResult, error) {
	actor := strings.TrimSpace(opts.Actor)
	if actor == "" {
		actor = "codex"
	}
	report, err := s.Workbench(now, WorkbenchOptions{RecentLimit: opts.RecentLimit})
	if err != nil {
		return DispatchAdvanceResult{}, err
	}
	result := DispatchAdvanceResult{
		SchemaVersion: DispatchAdvanceSchemaVersion,
		ID:            idFor("wda_", actor, ts(now)),
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Status:        "no_open_work",
		Actor:         actor,
		Counts:        report.Counts,
		Candidates:    []DispatchPlanItem{},
		Warnings: []string{
			"advances at most one actor-claimed work item to running",
			"does not execute task commands",
			"does not close work",
		},
		Integrity: append([]string{}, report.Integrity...),
	}
	policyBlocked := false
	claimedByOther := false
	for _, item := range report.Open {
		candidate := dispatchAdvanceCandidate(item, actor)
		if candidate.Action == "advance_running" {
			advancePlan, err := s.AdvancePlan(item.WorkID, now, AdvancePlanOptions{Actor: actor, To: "running"})
			if err != nil {
				candidate.Reason = strings.TrimSpace(candidate.Reason + "; advance policy plan failed: " + err.Error())
				policyBlocked = true
			} else {
				policySummary := advancePlan.Policy
				candidate.AdvancePolicy = &policySummary
				if !policySummary.Allowed {
					candidate.Reason = strings.TrimSpace(candidate.Reason + "; advance policy " + policySummary.Outcome)
					policyBlocked = true
				}
			}
		}
		if item.State == "claimed" && item.ClaimedBy != "" && item.ClaimedBy != actor {
			claimedByOther = true
		}
		result.Candidates = append(result.Candidates, candidate)
		if result.Selected == nil && candidate.Action == "advance_running" && candidate.AdvancePolicy != nil && candidate.AdvancePolicy.Allowed {
			selected := candidate
			result.Selected = &selected
			result.Status = "ready_to_advance"
		}
	}
	if result.Selected == nil {
		switch {
		case len(result.Candidates) == 0:
			result.NoAction = "workbench has no open work"
		case policyBlocked:
			result.Status = "advance_policy_blocked"
			result.NoAction = "claimed work exists but advance policy does not allow dispatch"
		case claimedByOther:
			result.Status = "claimed_by_other"
			result.NoAction = "claimed work is owned by another actor"
		case report.Counts.InProgress > 0:
			result.Status = "in_progress_only"
			result.NoAction = "open work is running or verifying, not claimed-ready"
		default:
			result.Status = "no_advanceable_work"
			result.NoAction = "open work is not claimed by actor"
		}
		path, err := s.RecordDispatchAdvance(result)
		if err != nil {
			return DispatchAdvanceResult{}, err
		}
		result.ResultPath = path
		return result, nil
	}
	advancePlan, err := s.AdvancePlan(result.Selected.WorkID, now, AdvancePlanOptions{Actor: actor, To: "running"})
	if err != nil {
		return DispatchAdvanceResult{}, err
	}
	result.AdvancePlan = &advancePlan
	if !advancePlan.Policy.Allowed {
		result.Status = "advance_policy_blocked"
		result.NoAction = "selected work advance policy does not allow dispatch"
		path, err := s.RecordDispatchAdvance(result)
		if err != nil {
			return DispatchAdvanceResult{}, err
		}
		result.ResultPath = path
		return result, nil
	}
	advanced, err := s.Advance(result.Selected.WorkID, AdvanceInput{To: "running", Actor: actor, Note: "dispatch advance"}, now)
	if err != nil {
		return DispatchAdvanceResult{}, err
	}
	result.Status = "running"
	result.NoAction = ""
	result.AdvancedWorkID = advanced.ID
	result.FromState = "claimed"
	result.AdvancedState = advanced.State
	result.PolicyDecisionID = advanced.PolicyDecisionID
	if len(advanced.RunIDs) > 0 {
		result.ActionIntentID = advanced.RunIDs[len(advanced.RunIDs)-1]
	}
	path, err := s.RecordDispatchAdvance(result)
	if err != nil {
		return DispatchAdvanceResult{}, err
	}
	result.ResultPath = path
	return result, nil
}

func (s *Store) RecordDispatchAdvance(result DispatchAdvanceResult) (string, error) {
	if strings.TrimSpace(result.ID) == "" {
		return "", os.ErrInvalid
	}
	dir := filepath.Join(s.home, "dispatch-advances")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, result.ID+".json")
	if err := s.writeJSON(path, &result); err != nil {
		return "", err
	}
	return path, nil
}

func dispatchCandidate(item WorkbenchItem, actor string) DispatchPlanItem {
	out := DispatchPlanItem{
		WorkID:           item.WorkID,
		State:            item.State,
		Owner:            item.Owner,
		SuccessPredicate: item.SuccessPredicate,
		DesiredOutcome:   item.DesiredOutcome,
		Reason:           item.OpenReason,
		NextAction:       item.NextAction,
		EvidenceRefs:     append([]string{}, item.EvidenceRefs...),
	}
	switch item.State {
	case "ready":
		out.Action = "claim"
		out.Command = []string{"compound", "work", "claim", item.WorkID, "--by", actor}
	case "proposed", "admitted":
		out.Action = "prepare"
	case "held", "blocked":
		out.Action = "review"
	case "claimed", "running", "verifying":
		out.Action = "observe"
	default:
		out.Action = "inspect"
	}
	return out
}

func dispatchAdvanceCandidate(item WorkbenchItem, actor string) DispatchPlanItem {
	out := dispatchCandidate(item, actor)
	switch item.State {
	case "claimed":
		if item.ClaimedBy == actor {
			out.Action = "advance_running"
			out.Command = []string{"compound", "work", "advance", item.WorkID, "--to", "running", "--by", actor}
			out.Reason = "claimed by actor"
		} else if item.ClaimedBy != "" {
			out.Action = "observe"
			out.Reason = "claimed by " + item.ClaimedBy
		}
	case "ready":
		out.Action = "claim_first"
		out.Command = nil
	case "running", "verifying":
		out.Action = "observe"
	}
	return out
}
