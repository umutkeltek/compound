package work

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/policy"
)

const AdvancePlanSchemaVersion = "compound.work_advance_plan.v0.1"

type AdvancePlanOptions struct {
	Actor         string
	To            string
	Capability    string
	Action        string
	MutationClass string
	Reversibility string
	BlastRadius   string
	TargetKind    string
	TargetID      string
	TargetPath    string
	Record        bool
	// Steal and StealReason preview the advance-gate lease escape hatch (see AdvanceInput in
	// work.go): when set with a non-empty reason, the plan reports the advance as
	// steal-ready (and its AdvanceCommand carries --steal --reason) instead of
	// held-by-other. AdvancePlan never mutates or records anything for this preview — only
	// Advance (work.go) does.
	Steal       bool
	StealReason string
}

type AdvancePolicySummary struct {
	ActionIntentID   string   `json:"action_intent_id"`
	PolicyDecisionID string   `json:"policy_decision_id"`
	PolicySource     string   `json:"policy_source,omitempty"`
	Outcome          string   `json:"outcome"`
	Allowed          bool     `json:"allowed"`
	StandingGrantID  string   `json:"standing_grant_id,omitempty"`
	ExpiresAt        string   `json:"expires_at,omitempty"`
	Reasons          []string `json:"reasons,omitempty"`
	Obligations      []string `json:"obligations,omitempty"`
	EvidenceRefs     []string `json:"evidence_refs,omitempty"`
}

type AdvancePlan struct {
	SchemaVersion  string                  `json:"schema_version"`
	ID             string                  `json:"id"`
	GeneratedAt    string                  `json:"generated_at"`
	Status         string                  `json:"status"`
	Mode           string                  `json:"mode"`
	Actor          string                  `json:"actor"`
	WorkID         string                  `json:"work_id"`
	WorkState      string                  `json:"work_state"`
	ToState        string                  `json:"to_state"`
	WorkOwner      string                  `json:"work_owner,omitempty"`
	PlanPath       string                  `json:"plan_path,omitempty"`
	AdvanceCommand []string                `json:"advance_command,omitempty"`
	Policy         AdvancePolicySummary    `json:"policy"`
	ActionIntent   envelope.ActionIntent   `json:"action_intent"`
	Decision       envelope.PolicyDecision `json:"policy_decision"`
	ActionContext  *WorkActionContext      `json:"action_context,omitempty"`
	NoAction       string                  `json:"no_action,omitempty"`
	Warnings       []string                `json:"warnings,omitempty"`
}

func (s *Store) AdvancePlan(workID string, now time.Time, opts AdvancePlanOptions) (AdvancePlan, error) {
	workID = strings.TrimSpace(workID)
	if workID == "" {
		return AdvancePlan{}, fmt.Errorf("advance-plan requires <work_id>")
	}
	to := strings.TrimSpace(opts.To)
	if to == "" {
		return AdvancePlan{}, fmt.Errorf("advance-plan requires --to <state>")
	}
	w, err := s.Get(workID)
	if err != nil {
		return AdvancePlan{}, err
	}
	actor := strings.TrimSpace(opts.Actor)
	if actor == "" {
		actor = "codex"
	}
	ai := s.advanceActionIntent(w, actor, to, now, opts)
	if err := ai.Validate(); err != nil {
		return AdvancePlan{}, err
	}
	cfg, source, err := policy.LoadConfig(s.home)
	if err != nil {
		return AdvancePlan{}, err
	}
	pd := policy.Evaluate(ai, cfg, now)
	if err := pd.Validate(); err != nil {
		return AdvancePlan{}, err
	}
	plan := AdvancePlan{
		SchemaVersion: AdvancePlanSchemaVersion,
		ID:            idFor("wap_", workID, actor, to, ts(now)),
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Status:        "not_advanceable",
		Mode:          "proposal_only",
		Actor:         actor,
		WorkID:        w.ID,
		WorkState:     w.State,
		ToState:       to,
		WorkOwner:     w.Owner,
		Policy:        summarizeAdvancePolicy(pd, source),
		ActionIntent:  ai,
		Decision:      pd,
		Warnings: []string{
			"does not advance work",
			"does not attach run, artifact, or verification ids",
			"does not enforce policy; it reports advance gate decision",
		},
	}
	heldReleaseErr := heldReleaseOperatorError(w.State, to, actor)
	directorReleaseErr := directorProposedReleaseOperatorError(w, to, actor)
	leaseStatus := s.leaseHolderStatusFor(w.ID, actor, opts.Steal, opts.StealReason, "advance")
	switch {
	case heldReleaseErr != nil:
		plan.NoAction = heldReleaseErr.Error()
	case directorReleaseErr != nil:
		plan.NoAction = directorReleaseErr.Error()
	case leaseStatus.Blocked:
		plan.NoAction = leaseStatus.Message
	case terminalStates[w.State]:
		plan.NoAction = fmt.Sprintf("work is terminal in state %q", w.State)
	case to == w.State || canTransition(w.State, to):
		plan.AdvanceCommand = []string{"compound", "work", "advance", w.ID, "--to", to, "--by", actor}
		if leaseStatus.Stolen {
			plan.AdvanceCommand = append(plan.AdvanceCommand, "--steal", "--reason", opts.StealReason)
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("lease held by %q; advance command carries --steal --reason", leaseStatus.Holder))
		}
		if policy.Allowed(pd) {
			plan.Status = "advance_policy_allowed"
		} else {
			plan.Status = "advance_policy_blocked"
			plan.NoAction = "advance lifecycle-ready but policy does not allow it"
		}
	default:
		plan.NoAction = fmt.Sprintf("illegal transition %s -> %s", w.State, to)
	}
	if actionContext, warnings := s.actionContextForWork(w); actionContext != nil || len(warnings) > 0 {
		plan.ActionContext = actionContext
		plan.Warnings = append(plan.Warnings, warnings...)
	}
	if opts.Record {
		path, err := s.RecordAdvancePlan(plan)
		if err != nil {
			return AdvancePlan{}, err
		}
		plan.PlanPath = path
	}
	return plan, nil
}

func (s *Store) RecordAdvancePlan(plan AdvancePlan) (string, error) {
	if strings.TrimSpace(plan.ID) == "" {
		return "", os.ErrInvalid
	}
	dir := filepath.Join(s.home, "advance-plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, plan.ID+".json")
	if err := s.writeJSON(path, &plan); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) advanceActionIntent(w envelope.WorkEnvelope, actor, to string, now time.Time, opts AdvancePlanOptions) envelope.ActionIntent {
	targetID := strings.TrimSpace(opts.TargetID)
	if targetID == "" {
		targetID = w.ID
	}
	targetPath := strings.TrimSpace(opts.TargetPath)
	if targetPath == "" {
		targetPath = s.workPath(w.ID)
	}
	capability := orDefault(opts.Capability, "work.advance")
	action := orDefault(opts.Action, "advance_work_to_"+to)
	targetKind := orDefault(opts.TargetKind, "work")
	mutationClass := orDefault(opts.MutationClass, "local_reversible")
	reversibility := orDefault(opts.Reversibility, "easy")
	blastRadius := orDefault(opts.BlastRadius, "local")
	return envelope.ActionIntent{
		Header: newHeader(
			envelope.SVActionIntent,
			idFor("act_", capability, action, targetKind, targetID, targetPath, mutationClass, reversibility, blastRadius, actor, to, ts(now)),
			now,
			w.ID,
			w.IDs.ContextPackID,
			w.Privacy,
			w.Risk,
		),
		Actor:         actor,
		Capability:    capability,
		Action:        action,
		Target:        envelope.ActionTarget{Kind: targetKind, ID: targetID, Path: targetPath},
		MutationClass: mutationClass,
		Reversibility: reversibility,
		BlastRadius:   blastRadius,
		Preview:       fmt.Sprintf("advance work %s from %s to %s actor %s", w.ID, w.State, to, actor),
		RollbackPlan:  fmt.Sprintf("advance work back to %s if no downstream action started", w.State),
	}
}

func summarizeAdvancePolicy(pd envelope.PolicyDecision, source string) AdvancePolicySummary {
	if strings.TrimSpace(source) == "" {
		source = "builtin:default"
	}
	return AdvancePolicySummary{
		ActionIntentID:   pd.ActionIntentID,
		PolicyDecisionID: pd.ID,
		PolicySource:     source,
		Outcome:          pd.Outcome,
		Allowed:          policy.Allowed(pd),
		StandingGrantID:  pd.StandingGrantID,
		ExpiresAt:        pd.ExpiresAt,
		Reasons:          append([]string{}, pd.Reasons...),
		Obligations:      append([]string{}, pd.Obligations...),
		EvidenceRefs:     append([]string{}, pd.EvidenceRefs...),
	}
}
