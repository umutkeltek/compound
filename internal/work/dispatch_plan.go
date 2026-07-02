package work

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const DispatchPlanSchemaVersion = "compound.work_dispatch_plan.v0.1"
const DispatchClaimSchemaVersion = "compound.work_dispatch_claim.v0.1"
const DispatchAdvanceSchemaVersion = "compound.work_dispatch_advance.v0.1"
const DispatchClosePlanSchemaVersion = "compound.work_dispatch_close_plan.v0.1"
const DispatchCloseSchemaVersion = "compound.work_dispatch_close.v0.1"
const DispatchStepSchemaVersion = "compound.work_dispatch_step.v0.1"
const DispatchStepRunSchemaVersion = "compound.work_dispatch_step_run.v0.1"

const defaultDispatchStepRunMaxSteps = 10

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
	ClosePolicy      *ClosePolicySummary   `json:"close_policy,omitempty"`
	ClosePlanReady   bool                  `json:"close_plan_ready,omitempty"`
}

type DispatchCapabilityOffer struct {
	Ref   string  `json:"ref"`
	Kind  string  `json:"kind"`
	Name  string  `json:"name"`
	Score float64 `json:"score"`
	Why   string  `json:"why"`
}

type DispatchClaimResult struct {
	SchemaVersion       string                    `json:"schema_version"`
	ID                  string                    `json:"id"`
	GeneratedAt         string                    `json:"generated_at"`
	Status              string                    `json:"status"`
	Actor               string                    `json:"actor"`
	ResultPath          string                    `json:"result_path,omitempty"`
	DispatchPlanID      string                    `json:"dispatch_plan_id"`
	DispatchPlanPath    string                    `json:"dispatch_plan_path,omitempty"`
	Selected            *DispatchPlanItem         `json:"selected,omitempty"`
	ClaimedWorkID       string                    `json:"claimed_work_id,omitempty"`
	ClaimedState        string                    `json:"claimed_state,omitempty"`
	ClaimedBy           string                    `json:"claimed_by,omitempty"`
	ActionIntentID      string                    `json:"action_intent_id,omitempty"`
	PolicyDecisionID    string                    `json:"policy_decision_id,omitempty"`
	ContextPackID       string                    `json:"context_pack_id,omitempty"`
	CapabilitiesOffered []DispatchCapabilityOffer `json:"capabilities_offered,omitempty"`
	WorkerPromptMD      string                    `json:"worker_prompt_md,omitempty"`
	NoAction            string                    `json:"no_action,omitempty"`
	Warnings            []string                  `json:"warnings,omitempty"`
	Plan                DispatchPlan              `json:"dispatch_plan"`
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

type DispatchClosePlanOptions struct {
	Actor         string
	RecentLimit   int
	Outcome       string
	MoveType      string
	Capability    string
	Action        string
	MutationClass string
	Reversibility string
	BlastRadius   string
	TargetKind    string
	TargetID      string
	TargetPath    string
}

type DispatchClosePlanResult struct {
	SchemaVersion string             `json:"schema_version"`
	ID            string             `json:"id"`
	GeneratedAt   string             `json:"generated_at"`
	Status        string             `json:"status"`
	Mode          string             `json:"mode"`
	Actor         string             `json:"actor"`
	Outcome       string             `json:"outcome"`
	MoveType      string             `json:"move_type"`
	ResultPath    string             `json:"result_path,omitempty"`
	Counts        WorkbenchCounts    `json:"counts"`
	Selected      *DispatchPlanItem  `json:"selected,omitempty"`
	Candidates    []DispatchPlanItem `json:"candidates"`
	ClosePlanID   string             `json:"close_plan_id,omitempty"`
	ClosePlanPath string             `json:"close_plan_path,omitempty"`
	ClosePlan     *ClosePlan         `json:"close_plan,omitempty"`
	NoAction      string             `json:"no_action,omitempty"`
	Warnings      []string           `json:"warnings,omitempty"`
	Integrity     []string           `json:"integrity,omitempty"`
}

type DispatchCloseOptions struct {
	Actor       string
	RecentLimit int
	Outcome     string
	MoveType    string
	Summary     string
}

type DispatchCloseResult struct {
	SchemaVersion         string                   `json:"schema_version"`
	ID                    string                   `json:"id"`
	GeneratedAt           string                   `json:"generated_at"`
	Status                string                   `json:"status"`
	Actor                 string                   `json:"actor"`
	Outcome               string                   `json:"outcome"`
	MoveType              string                   `json:"move_type"`
	ResultPath            string                   `json:"result_path,omitempty"`
	DispatchClosePlanID   string                   `json:"dispatch_close_plan_id,omitempty"`
	DispatchClosePlanPath string                   `json:"dispatch_close_plan_path,omitempty"`
	DispatchClosePlan     *DispatchClosePlanResult `json:"dispatch_close_plan,omitempty"`
	Selected              *DispatchPlanItem        `json:"selected,omitempty"`
	ClosePlanID           string                   `json:"close_plan_id,omitempty"`
	ClosePlanPath         string                   `json:"close_plan_path,omitempty"`
	ClosedWorkID          string                   `json:"closed_work_id,omitempty"`
	FinalState            string                   `json:"final_state,omitempty"`
	ReceiptID             string                   `json:"receipt_id,omitempty"`
	TransitionID          string                   `json:"transition_id,omitempty"`
	CloseResult           *CloseResult             `json:"close_result,omitempty"`
	NoAction              string                   `json:"no_action,omitempty"`
	Warnings              []string                 `json:"warnings,omitempty"`
	Integrity             []string                 `json:"integrity,omitempty"`
}

type DispatchStepOptions struct {
	Actor                 string
	RecentLimit           int
	Outcome               string
	MoveType              string
	Summary               string
	SourcePacketPath      string
	SourcePacketState     string
	SourcePacketOwner     string
	SourcePacketRisk      string
	SourcePacketPrivacy   string
	SourcePacketContextID string
}

type DispatchStepResult struct {
	SchemaVersion       string                    `json:"schema_version"`
	ID                  string                    `json:"id"`
	GeneratedAt         string                    `json:"generated_at"`
	Status              string                    `json:"status"`
	Actor               string                    `json:"actor"`
	Action              string                    `json:"action"`
	WorkID              string                    `json:"work_id,omitempty"`
	ResultPath          string                    `json:"result_path,omitempty"`
	Source              *DispatchPlanSource       `json:"source,omitempty"`
	Counts              WorkbenchCounts           `json:"counts"`
	PostCounts          *WorkbenchCounts          `json:"post_counts,omitempty"`
	DispatchClose       *DispatchCloseResult      `json:"dispatch_close,omitempty"`
	DispatchAdvance     *DispatchAdvanceResult    `json:"dispatch_advance,omitempty"`
	DispatchClaim       *DispatchClaimResult      `json:"dispatch_claim,omitempty"`
	ContextPackID       string                    `json:"context_pack_id,omitempty"`
	CapabilitiesOffered []DispatchCapabilityOffer `json:"capabilities_offered,omitempty"`
	WorkerPromptMD      string                    `json:"worker_prompt_md,omitempty"`
	NextAction          string                    `json:"next_action,omitempty"`
	CanContinue         bool                      `json:"can_continue"`
	ContinueCommand     []string                  `json:"continue_command,omitempty"`
	WaitReason          string                    `json:"wait_reason,omitempty"`
	BlockedOn           []string                  `json:"blocked_on,omitempty"`
	NoAction            string                    `json:"no_action,omitempty"`
	Warnings            []string                  `json:"warnings,omitempty"`
	Integrity           []string                  `json:"integrity,omitempty"`
}

type DispatchStepRunOptions struct {
	DispatchStepOptions
	MaxSteps int
}

type DispatchStepRunResult struct {
	SchemaVersion   string               `json:"schema_version"`
	ID              string               `json:"id"`
	GeneratedAt     string               `json:"generated_at"`
	Status          string               `json:"status"`
	Actor           string               `json:"actor"`
	ResultPath      string               `json:"result_path,omitempty"`
	MaxSteps        int                  `json:"max_steps"`
	StepCount       int                  `json:"step_count"`
	Steps           []DispatchStepResult `json:"steps"`
	CanContinue     bool                 `json:"can_continue"`
	ContinueCommand []string             `json:"continue_command,omitempty"`
	WaitReason      string               `json:"wait_reason,omitempty"`
	BlockedOn       []string             `json:"blocked_on,omitempty"`
	NextAction      string               `json:"next_action,omitempty"`
	Warnings        []string             `json:"warnings,omitempty"`
	Integrity       []string             `json:"integrity,omitempty"`
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
	prompt, err := s.attachDispatchPrompt(claimed.ID, plan.Actor, now)
	if err != nil {
		return DispatchClaimResult{}, err
	}
	result.ContextPackID = prompt.ContextPackID
	result.CapabilitiesOffered = prompt.CapabilitiesOffered
	result.WorkerPromptMD = prompt.Markdown
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

func (s *Store) DispatchClosePlan(now time.Time, opts DispatchClosePlanOptions) (DispatchClosePlanResult, error) {
	actor := strings.TrimSpace(opts.Actor)
	if actor == "" {
		actor = "codex"
	}
	outcome := strings.TrimSpace(opts.Outcome)
	if outcome == "" {
		outcome = "completed"
	}
	moveType := strings.TrimSpace(opts.MoveType)
	if moveType == "" {
		moveType = "control_plane_repair"
	}
	report, err := s.Workbench(now, WorkbenchOptions{RecentLimit: opts.RecentLimit})
	if err != nil {
		return DispatchClosePlanResult{}, err
	}
	result := DispatchClosePlanResult{
		SchemaVersion: DispatchClosePlanSchemaVersion,
		ID:            idFor("wdcp_", actor, outcome, moveType, ts(now)),
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Status:        "no_open_work",
		Mode:          "proposal_only",
		Actor:         actor,
		Outcome:       outcome,
		MoveType:      moveType,
		Counts:        report.Counts,
		Candidates:    []DispatchPlanItem{},
		Warnings: []string{
			"records close-plan readiness only",
			"does not close work",
			"does not emit receipt or transition",
			"does not promote memory",
		},
		Integrity: append([]string{}, report.Integrity...),
	}
	policyBlocked := false
	awaitingProof := false
	var blocked *DispatchPlanItem
	for _, item := range report.Open {
		candidate := dispatchClosePlanCandidate(item, actor, outcome, moveType)
		if candidate.Action == "plan_close" {
			closePlan, err := s.dispatchClosePlan(item.WorkID, now, opts, actor, outcome, moveType, false)
			if err != nil {
				candidate.Reason = strings.TrimSpace(candidate.Reason + "; close policy plan failed: " + err.Error())
				policyBlocked = true
			} else {
				policySummary := closePlan.Policy
				candidate.ClosePolicy = &policySummary
				candidate.Command = append([]string{}, closePlan.CloseCommand...)
				candidate.EvidenceRefs = mergeUnique(candidate.EvidenceRefs, closePlan.ProofRefs)
				if !policySummary.Allowed || closePlan.NoAction != "" {
					policyBlocked = true
					candidate.Reason = strings.TrimSpace(candidate.Reason + "; close policy " + policySummary.Outcome)
					if blocked == nil {
						copyCandidate := candidate
						blocked = &copyCandidate
					}
				} else {
					candidate.ClosePlanReady = true
				}
			}
		}
		if candidate.Action == "await_proof" {
			awaitingProof = true
		}
		result.Candidates = append(result.Candidates, candidate)
		if result.Selected == nil && candidate.Action == "plan_close" && candidate.ClosePlanReady {
			selected := candidate
			result.Selected = &selected
			result.Status = "close_policy_allowed"
		}
	}
	if result.Selected == nil && blocked != nil {
		result.Selected = blocked
	}
	if result.Selected != nil {
		closePlan, err := s.dispatchClosePlan(result.Selected.WorkID, now, opts, actor, outcome, moveType, true)
		if err != nil {
			return DispatchClosePlanResult{}, err
		}
		result.ClosePlan = &closePlan
		result.ClosePlanID = closePlan.ID
		result.ClosePlanPath = closePlan.PlanPath
		result.Status = closePlan.Status
		if !closePlan.Policy.Allowed || closePlan.NoAction != "" {
			result.NoAction = "selected work close policy does not allow dispatch"
		}
		path, err := s.RecordDispatchClosePlan(result)
		if err != nil {
			return DispatchClosePlanResult{}, err
		}
		result.ResultPath = path
		return result, nil
	}
	switch {
	case len(result.Candidates) == 0:
		result.NoAction = "workbench has no open work"
	case policyBlocked:
		result.Status = "close_policy_blocked"
		result.NoAction = "verifying work exists but close policy does not allow dispatch"
	case awaitingProof:
		result.Status = "awaiting_proof"
		result.NoAction = "verifying work exists without artifact, verification, or observed-run evidence"
	case report.Counts.InProgress > 0:
		result.Status = "in_progress_only"
		result.NoAction = "open work is not verifying with proof evidence"
	default:
		result.Status = "no_closeable_work"
		result.NoAction = "open work is not ready for close planning"
	}
	path, err := s.RecordDispatchClosePlan(result)
	if err != nil {
		return DispatchClosePlanResult{}, err
	}
	result.ResultPath = path
	return result, nil
}

func (s *Store) dispatchClosePlan(workID string, now time.Time, opts DispatchClosePlanOptions, actor, outcome, moveType string, record bool) (ClosePlan, error) {
	return s.ClosePlan(workID, now, ClosePlanOptions{
		Actor:         actor,
		Outcome:       outcome,
		MoveType:      moveType,
		Capability:    opts.Capability,
		Action:        opts.Action,
		MutationClass: opts.MutationClass,
		Reversibility: opts.Reversibility,
		BlastRadius:   opts.BlastRadius,
		TargetKind:    opts.TargetKind,
		TargetID:      opts.TargetID,
		TargetPath:    opts.TargetPath,
		Record:        record,
	})
}

func (s *Store) RecordDispatchClosePlan(result DispatchClosePlanResult) (string, error) {
	if strings.TrimSpace(result.ID) == "" {
		return "", os.ErrInvalid
	}
	dir := filepath.Join(s.home, "dispatch-close-plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, result.ID+".json")
	if err := s.writeJSON(path, &result); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) DispatchClose(now time.Time, opts DispatchCloseOptions) (DispatchCloseResult, error) {
	actor := strings.TrimSpace(opts.Actor)
	if actor == "" {
		actor = "codex"
	}
	outcome := strings.TrimSpace(opts.Outcome)
	if outcome == "" {
		outcome = "completed"
	}
	moveType := strings.TrimSpace(opts.MoveType)
	if moveType == "" {
		moveType = "control_plane_repair"
	}
	plan, err := s.DispatchClosePlan(now, DispatchClosePlanOptions{
		Actor:       actor,
		RecentLimit: opts.RecentLimit,
		Outcome:     outcome,
		MoveType:    moveType,
	})
	if err != nil {
		return DispatchCloseResult{}, err
	}
	result := DispatchCloseResult{
		SchemaVersion:         DispatchCloseSchemaVersion,
		ID:                    idFor("wdcl_", actor, outcome, moveType, ts(now)),
		GeneratedAt:           now.UTC().Format(time.RFC3339),
		Status:                plan.Status,
		Actor:                 actor,
		Outcome:               outcome,
		MoveType:              moveType,
		DispatchClosePlanID:   plan.ID,
		DispatchClosePlanPath: plan.ResultPath,
		DispatchClosePlan:     &plan,
		Selected:              plan.Selected,
		ClosePlanID:           plan.ClosePlanID,
		ClosePlanPath:         plan.ClosePlanPath,
		NoAction:              plan.NoAction,
		Warnings: []string{
			"closes at most one proof-ready verifying work item",
			"does not execute task commands",
			"does not promote memory",
		},
		Integrity: append([]string{}, plan.Integrity...),
	}
	if plan.Selected == nil || plan.ClosePlan == nil || plan.Status != "close_policy_allowed" || plan.ClosePlan.NoAction != "" || !plan.ClosePlan.Policy.Allowed {
		if result.NoAction == "" {
			result.NoAction = "no proof-ready close plan selected"
		}
		path, err := s.RecordDispatchClose(result)
		if err != nil {
			return DispatchCloseResult{}, err
		}
		result.ResultPath = path
		return result, nil
	}
	summary := strings.TrimSpace(opts.Summary)
	if summary == "" {
		summary = "dispatch close"
	}
	closed, err := s.Close(plan.Selected.WorkID, CloseInput{
		Outcome:   outcome,
		MoveType:  moveType,
		Actor:     actor,
		Summary:   summary,
		Changed:   true,
		ProofRefs: append([]string{}, plan.ClosePlan.ProofRefs...),
	}, now)
	if err != nil {
		return DispatchCloseResult{}, err
	}
	result.Status = "closed"
	result.NoAction = ""
	result.ClosedWorkID = closed.Work.ID
	result.FinalState = closed.Work.State
	result.ReceiptID = closed.Receipt.ID
	result.TransitionID = closed.Transition.ID
	result.CloseResult = &closed
	path, err := s.RecordDispatchClose(result)
	if err != nil {
		return DispatchCloseResult{}, err
	}
	result.ResultPath = path
	return result, nil
}

func (s *Store) RecordDispatchClose(result DispatchCloseResult) (string, error) {
	if strings.TrimSpace(result.ID) == "" {
		return "", os.ErrInvalid
	}
	dir := filepath.Join(s.home, "dispatch-closes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, result.ID+".json")
	if err := s.writeJSON(path, &result); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) DispatchStep(now time.Time, opts DispatchStepOptions) (DispatchStepResult, error) {
	actor := strings.TrimSpace(opts.Actor)
	if actor == "" {
		actor = "codex"
	}
	report, err := s.Workbench(now, WorkbenchOptions{RecentLimit: opts.RecentLimit})
	if err != nil {
		return DispatchStepResult{}, err
	}
	result := DispatchStepResult{
		SchemaVersion: DispatchStepSchemaVersion,
		ID:            idFor("wds_", actor, ts(now)),
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Status:        "no_open_work",
		Actor:         actor,
		Action:        "none",
		Counts:        report.Counts,
		Warnings: []string{
			"dispatches at most one lifecycle step",
			"does not execute task commands",
			"does not observe or synthesize traces",
			"does not promote memory",
		},
		Integrity: append([]string{}, report.Integrity...),
	}
	for _, item := range report.Open {
		if item.State == "verifying" && hasCloseProofEvidence(item) {
			closed, err := s.DispatchClose(now, DispatchCloseOptions{
				Actor:       actor,
				RecentLimit: opts.RecentLimit,
				Outcome:     opts.Outcome,
				MoveType:    opts.MoveType,
				Summary:     opts.Summary,
			})
			if err != nil {
				return DispatchStepResult{}, err
			}
			result.Action = "close"
			result.Status = closed.Status
			result.WorkID = closed.ClosedWorkID
			if result.WorkID == "" && closed.Selected != nil {
				result.WorkID = closed.Selected.WorkID
			}
			result.DispatchClose = &closed
			result.NoAction = closed.NoAction
			return s.recordDispatchStep(now, result)
		}
	}
	for _, item := range report.Open {
		if item.State == "claimed" && item.ClaimedBy == actor {
			advanced, err := s.DispatchAdvance(now, DispatchPlanOptions{Actor: actor, RecentLimit: opts.RecentLimit})
			if err != nil {
				return DispatchStepResult{}, err
			}
			result.Action = "advance"
			result.Status = advanced.Status
			result.WorkID = advanced.AdvancedWorkID
			if result.WorkID == "" && advanced.Selected != nil {
				result.WorkID = advanced.Selected.WorkID
			}
			result.DispatchAdvance = &advanced
			result.NoAction = advanced.NoAction
			return s.recordDispatchStep(now, result)
		}
	}
	readyToClaim := false
	for _, item := range report.Open {
		if item.State == "ready" {
			readyToClaim = true
			break
		}
	}
	if readyToClaim || strings.TrimSpace(opts.SourcePacketPath) != "" {
		claimed, err := s.DispatchClaim(now, DispatchPlanOptions{
			Actor:                 actor,
			RecentLimit:           opts.RecentLimit,
			SourcePacketPath:      opts.SourcePacketPath,
			SourcePacketState:     opts.SourcePacketState,
			SourcePacketOwner:     opts.SourcePacketOwner,
			SourcePacketRisk:      opts.SourcePacketRisk,
			SourcePacketPrivacy:   opts.SourcePacketPrivacy,
			SourcePacketContextID: opts.SourcePacketContextID,
		})
		if err != nil {
			return DispatchStepResult{}, err
		}
		result.Action = "claim"
		result.Status = claimed.Status
		result.Counts = claimed.Plan.Counts
		result.Integrity = append([]string{}, claimed.Plan.Integrity...)
		result.Source = claimed.Plan.Source
		result.WorkID = claimed.ClaimedWorkID
		result.ContextPackID = claimed.ContextPackID
		result.CapabilitiesOffered = claimed.CapabilitiesOffered
		result.WorkerPromptMD = claimed.WorkerPromptMD
		if result.WorkID == "" && claimed.Selected != nil {
			result.WorkID = claimed.Selected.WorkID
		}
		result.DispatchClaim = &claimed
		result.NoAction = claimed.NoAction
		if sourcePacketAlreadyTerminal(claimed) {
			result.Action = "none"
			result.Status = "source_packet_terminal"
			result.WorkID = claimed.Plan.Source.WorkID
			result.NoAction = "source packet already has terminal Compound work proof; no dispatch step needed"
		}
		return s.recordDispatchStep(now, result)
	}
	switch {
	case report.Counts.Open == 0:
		result.NoAction = "workbench has no open work"
	case report.Counts.NeedsReview > 0:
		result.Status = "needs_review"
		result.NoAction = "open work requires review before dispatch"
	case report.Counts.InProgress > 0:
		result.Status = "in_progress_only"
		result.NoAction = "open work is not ready for a safe dispatch step"
	default:
		result.Status = "no_dispatchable_work"
		result.NoAction = "open work has no dispatchable lifecycle step"
	}
	return s.recordDispatchStep(now, result)
}

func (s *Store) DispatchStepRun(now time.Time, opts DispatchStepRunOptions) (DispatchStepRunResult, error) {
	stepOpts := opts.DispatchStepOptions
	actor := strings.TrimSpace(stepOpts.Actor)
	if actor == "" {
		actor = "codex"
		stepOpts.Actor = actor
	}
	maxSteps := opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultDispatchStepRunMaxSteps
	}
	result := DispatchStepRunResult{
		SchemaVersion: DispatchStepRunSchemaVersion,
		ID:            idFor("wdsr_", actor, ts(now)),
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Status:        "waiting",
		Actor:         actor,
		MaxSteps:      maxSteps,
		Warnings: []string{
			"dispatches repeated safe lifecycle steps only while dispatch-step can continue",
			"does not execute task commands",
			"does not observe or synthesize traces",
			"does not promote memory",
		},
	}
	for i := 0; i < maxSteps; i++ {
		step, err := s.DispatchStep(now.Add(time.Duration(i)*time.Nanosecond), stepOpts)
		if err != nil {
			return DispatchStepRunResult{}, err
		}
		result.Steps = append(result.Steps, step)
		result.StepCount = len(result.Steps)
		result.NextAction = step.NextAction
		result.WaitReason = step.WaitReason
		result.BlockedOn = append([]string{}, step.BlockedOn...)
		result.Integrity = mergeUnique(result.Integrity, step.Integrity)
		stepOpts.SourcePacketPath = ""
		stepOpts.SourcePacketState = ""
		stepOpts.SourcePacketOwner = ""
		stepOpts.SourcePacketRisk = ""
		stepOpts.SourcePacketPrivacy = ""
		stepOpts.SourcePacketContextID = ""
		if !step.CanContinue {
			result.Status = "waiting"
			result.CanContinue = false
			result.ContinueCommand = nil
			return s.recordDispatchStepRun(result)
		}
	}
	result.Status = "max_steps_reached"
	result.CanContinue = true
	result.WaitReason = ""
	result.BlockedOn = nil
	result.ContinueCommand = dispatchStepRunContinuation(actor, maxSteps)
	return s.recordDispatchStepRun(result)
}

func (s *Store) recordDispatchStep(now time.Time, result DispatchStepResult) (DispatchStepResult, error) {
	post, err := s.Workbench(now, WorkbenchOptions{RecentLimit: 0})
	if err != nil {
		return DispatchStepResult{}, err
	}
	result.PostCounts = &post.Counts
	result.NextAction = dispatchStepNextAction(result, post)
	result.CanContinue, result.ContinueCommand = dispatchStepContinuation(result, post)
	if !result.CanContinue {
		result.WaitReason, result.BlockedOn = dispatchStepWait(result, post)
	}
	result.Integrity = mergeUnique(result.Integrity, post.Integrity)
	path, err := s.RecordDispatchStep(result)
	if err != nil {
		return DispatchStepResult{}, err
	}
	result.ResultPath = path
	return result, nil
}

func (s *Store) recordDispatchStepRun(result DispatchStepRunResult) (DispatchStepRunResult, error) {
	path, err := s.RecordDispatchStepRun(result)
	if err != nil {
		return DispatchStepRunResult{}, err
	}
	result.ResultPath = path
	return result, nil
}

func dispatchStepNextAction(result DispatchStepResult, post WorkbenchReport) string {
	if result.Status == "source_packet_terminal" && strings.TrimSpace(result.NoAction) != "" {
		return result.NoAction
	}
	if strings.TrimSpace(result.WorkID) != "" {
		for _, item := range post.Open {
			if item.WorkID == result.WorkID {
				return item.NextAction
			}
		}
		for _, item := range post.Recent {
			if item.WorkID == result.WorkID {
				return item.NextAction
			}
		}
	}
	if strings.TrimSpace(result.NoAction) != "" {
		return result.NoAction
	}
	if post.Counts.Open == 0 {
		return "workbench has no open work"
	}
	return "run dispatch-step again for next open work"
}

func dispatchStepContinuation(result DispatchStepResult, post WorkbenchReport) (bool, []string) {
	actor := strings.TrimSpace(result.Actor)
	if actor == "" {
		actor = "codex"
	}
	for _, item := range post.Open {
		switch {
		case item.State == "verifying" && hasCloseProofEvidence(item):
			return true, []string{"compound", "work", "dispatch-step", "--actor", actor}
		case item.State == "claimed" && item.ClaimedBy == actor:
			return true, []string{"compound", "work", "dispatch-step", "--actor", actor}
		case item.State == "ready":
			return true, []string{"compound", "work", "dispatch-step", "--actor", actor}
		}
	}
	return false, nil
}

func dispatchStepWait(result DispatchStepResult, post WorkbenchReport) (string, []string) {
	if result.Status == "source_packet_terminal" {
		blockedOn := []string{"work.terminal"}
		if strings.TrimSpace(result.WorkID) != "" {
			blockedOn = append(blockedOn, "work.terminal:"+result.WorkID)
		}
		return "source_packet_terminal", blockedOn
	}
	if post.Counts.Open == 0 {
		return "no_open_work", []string{"work.ready_or_source_packet"}
	}
	actor := strings.TrimSpace(result.Actor)
	if actor == "" {
		actor = "codex"
	}
	var review []string
	var runEvidence []string
	var closeProof []string
	var claimedOther []string
	var readyState []string
	var unknown []string
	for _, item := range post.Open {
		switch item.State {
		case "held", "blocked":
			review = append(review, "work.review:"+item.WorkID)
		case "running":
			runEvidence = append(runEvidence, "work.run_evidence:"+item.WorkID)
		case "verifying":
			if !hasCloseProofEvidence(item) {
				closeProof = append(closeProof, "work.close_proof:"+item.WorkID)
			}
		case "claimed":
			if item.ClaimedBy != actor {
				claimedOther = append(claimedOther, "work.claimed_by_other_actor:"+item.WorkID)
			}
		case "proposed", "admitted":
			readyState = append(readyState, "work.ready_state:"+item.WorkID)
		case "ready":
			// dispatchStepContinuation would have returned true for ready work.
		default:
			unknown = append(unknown, "work.state:"+item.WorkID)
		}
	}
	switch {
	case len(review) > 0 || result.Status == "needs_review":
		return "needs_review", mergeUnique(review, runEvidence, closeProof, claimedOther, readyState, unknown)
	case len(runEvidence) > 0:
		return "run_evidence", mergeUnique(runEvidence, closeProof, claimedOther, readyState, unknown)
	case len(closeProof) > 0:
		return "close_proof", mergeUnique(closeProof, claimedOther, readyState, unknown)
	case len(claimedOther) > 0:
		return "claimed_by_other_actor", mergeUnique(claimedOther, readyState, unknown)
	case len(readyState) > 0:
		return "not_ready", mergeUnique(readyState, unknown)
	default:
		return "no_dispatchable_work", mergeUnique(unknown)
	}
}

func sourcePacketAlreadyTerminal(result DispatchClaimResult) bool {
	source := result.Plan.Source
	if source == nil || source.Created || strings.TrimSpace(source.WorkID) == "" {
		return false
	}
	if strings.TrimSpace(result.ClaimedWorkID) != "" || result.Selected != nil {
		return false
	}
	return terminalStates[source.State]
}

func dispatchStepRunContinuation(actor string, maxSteps int) []string {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "codex"
	}
	if maxSteps <= 0 {
		maxSteps = defaultDispatchStepRunMaxSteps
	}
	return []string{"compound", "work", "dispatch-step", "--actor", actor, "--until-wait", "--max-steps", strconv.Itoa(maxSteps)}
}

func (s *Store) RecordDispatchStep(result DispatchStepResult) (string, error) {
	if strings.TrimSpace(result.ID) == "" {
		return "", os.ErrInvalid
	}
	dir := filepath.Join(s.home, "dispatch-steps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, result.ID+".json")
	if err := s.writeJSON(path, &result); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) RecordDispatchStepRun(result DispatchStepRunResult) (string, error) {
	if strings.TrimSpace(result.ID) == "" {
		return "", os.ErrInvalid
	}
	dir := filepath.Join(s.home, "dispatch-step-runs")
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

func dispatchClosePlanCandidate(item WorkbenchItem, actor, outcome, moveType string) DispatchPlanItem {
	out := dispatchCandidate(item, actor)
	out.Command = nil
	switch item.State {
	case "verifying":
		if hasCloseProofEvidence(item) {
			out.Action = "plan_close"
			out.Command = []string{"compound", "work", "close-plan", item.WorkID, "--outcome", outcome, "--move-type", moveType, "--actor", actor}
			out.Reason = "verifying with proof evidence"
			out.EvidenceRefs = mergeUnique(out.EvidenceRefs, item.ArtifactIDs, item.VerificationIDs)
		} else {
			out.Action = "await_proof"
			out.Reason = "verifying without artifact, verification, or observed-run evidence"
		}
	case "running":
		out.Action = "observe"
		out.Reason = "running work needs observed-run proof before close planning"
	case "claimed":
		out.Action = "advance_running"
	case "ready":
		out.Action = "claim_first"
	case "proposed", "admitted":
		out.Action = "prepare"
	case "held", "blocked":
		out.Action = "review"
	}
	return out
}

func hasCloseProofEvidence(item WorkbenchItem) bool {
	return len(item.ArtifactIDs) > 0 || len(item.VerificationIDs) > 0 || len(item.RunObservations) > 0
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
