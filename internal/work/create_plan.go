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

const CreatePlanSchemaVersion = "compound.work_create_plan.v0.1"

type CreatePlanOptions struct {
	Actor         string
	Capability    string
	Action        string
	MutationClass string
	Reversibility string
	BlastRadius   string
	TargetKind    string
	TargetID      string
	TargetPath    string
	Record        bool
}

type CreatePolicySummary struct {
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

type CreatePlan struct {
	SchemaVersion string                  `json:"schema_version"`
	ID            string                  `json:"id"`
	GeneratedAt   string                  `json:"generated_at"`
	Status        string                  `json:"status"`
	Mode          string                  `json:"mode"`
	Actor         string                  `json:"actor"`
	WorkID        string                  `json:"work_id"`
	WorkState     string                  `json:"work_state"`
	WorkOwner     string                  `json:"work_owner,omitempty"`
	PlanPath      string                  `json:"plan_path,omitempty"`
	CreateCommand []string                `json:"create_command,omitempty"`
	Policy        CreatePolicySummary     `json:"policy"`
	ActionIntent  envelope.ActionIntent   `json:"action_intent"`
	Decision      envelope.PolicyDecision `json:"policy_decision"`
	Work          envelope.WorkEnvelope   `json:"work"`
	NoAction      string                  `json:"no_action,omitempty"`
	Warnings      []string                `json:"warnings,omitempty"`
}

func (s *Store) CreatePlan(in CreateInput, now time.Time, opts CreatePlanOptions) (CreatePlan, error) {
	w := BuildWork(in, now)
	if strings.TrimSpace(opts.Actor) == "" {
		opts.Actor = in.Actor
	}
	return s.createPlanForWork(w, now, opts)
}

func (s *Store) createPlanForWork(w envelope.WorkEnvelope, now time.Time, opts CreatePlanOptions) (CreatePlan, error) {
	if err := w.Validate(); err != nil {
		return CreatePlan{}, err
	}
	actor := strings.TrimSpace(opts.Actor)
	if actor == "" {
		actor = firstNonEmpty(w.Owner, "compound")
	}
	ai := s.createActionIntent(w, actor, now, opts)
	if err := ai.Validate(); err != nil {
		return CreatePlan{}, err
	}
	cfg, source, err := policy.LoadConfig(s.home)
	if err != nil {
		return CreatePlan{}, err
	}
	pd := policy.Evaluate(ai, cfg, now)
	if err := pd.Validate(); err != nil {
		return CreatePlan{}, err
	}
	plan := CreatePlan{
		SchemaVersion: CreatePlanSchemaVersion,
		ID:            idFor("wcrp_", w.ID, actor, ts(now)),
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Status:        "create_policy_blocked",
		Mode:          "proposal_only",
		Actor:         actor,
		WorkID:        w.ID,
		WorkState:     w.State,
		WorkOwner:     w.Owner,
		CreateCommand: createCommandForWork(w, actor),
		Policy:        summarizeCreatePolicy(pd, source),
		ActionIntent:  ai,
		Decision:      pd,
		Work:          w,
		NoAction:      "create lifecycle-ready but policy does not allow it",
		Warnings: []string{
			"does not create work",
			"does not write lifecycle event",
			"does not record action intent",
			"does not enforce policy; it reports create gate decision",
		},
	}
	if policy.Allowed(pd) {
		plan.Status = "create_policy_allowed"
		plan.NoAction = ""
	}
	if opts.Record {
		path, err := s.RecordCreatePlan(plan)
		if err != nil {
			return CreatePlan{}, err
		}
		plan.PlanPath = path
	}
	return plan, nil
}

func (s *Store) admitWork(plan CreatePlan, now time.Time, note string, extraAttached []string) (envelope.WorkEnvelope, error) {
	w := plan.Work
	if !plan.Policy.Allowed {
		return w, fmt.Errorf("cannot create work %s: policy %s (decision %s)", w.ID, plan.Policy.Outcome, plan.Policy.PolicyDecisionID)
	}
	if err := s.validateContextPackRef(w.IDs.ContextPackID); err != nil {
		return w, err
	}
	state := w.State
	w.RunIDs = append(w.RunIDs, plan.ActionIntent.ID)
	w.PolicyDecisionID = plan.Decision.ID
	if err := s.writeJSON(s.envPath(plan.ActionIntent.ID), &plan.ActionIntent); err != nil {
		return w, err
	}
	if err := s.writeJSON(s.envPath(plan.Decision.ID), &plan.Decision); err != nil {
		return w, err
	}
	if err := s.writeJSON(s.workPath(w.ID), &w); err != nil {
		return w, err
	}
	attached := []string{plan.ActionIntent.ID, plan.Decision.ID}
	attached = append(attached, extraAttached...)
	if strings.TrimSpace(note) == "" {
		note = "created"
	}
	if err := s.appendEvent(Event{TS: ts(now), WorkID: w.ID, From: "", To: state, Note: note, Attached: attached}); err != nil {
		return w, err
	}
	return w, nil
}

func createCommandForWork(w envelope.WorkEnvelope, actor string) []string {
	cmd := []string{"compound", "work", "create", "--work-id", w.ID, "--owner", w.Owner, "--predicate", w.SuccessPredicate, "--by", actor}
	if strings.TrimSpace(w.DesiredOutcome) != "" {
		cmd = append(cmd, "--outcome", w.DesiredOutcome)
	}
	for _, nonGoal := range w.NonGoals {
		if strings.TrimSpace(nonGoal) != "" {
			cmd = append(cmd, "--non-goal", nonGoal)
		}
	}
	for _, evidenceRef := range w.EvidenceRefs {
		if strings.TrimSpace(evidenceRef) != "" {
			cmd = append(cmd, "--evidence-ref", evidenceRef)
		}
	}
	if strings.TrimSpace(w.IDs.ContextPackID) != "" {
		cmd = append(cmd, "--context-pack", w.IDs.ContextPackID)
	}
	if strings.TrimSpace(w.State) != "" {
		cmd = append(cmd, "--state", w.State)
	}
	if strings.TrimSpace(w.Risk) != "" {
		cmd = append(cmd, "--risk", w.Risk)
	}
	if strings.TrimSpace(w.Privacy) != "" {
		cmd = append(cmd, "--privacy", w.Privacy)
	}
	return cmd
}

func (s *Store) RecordCreatePlan(plan CreatePlan) (string, error) {
	if strings.TrimSpace(plan.ID) == "" {
		return "", os.ErrInvalid
	}
	dir := filepath.Join(s.home, "create-plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, plan.ID+".json")
	if err := s.writeJSON(path, &plan); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) createActionIntent(w envelope.WorkEnvelope, actor string, now time.Time, opts CreatePlanOptions) envelope.ActionIntent {
	targetID := strings.TrimSpace(opts.TargetID)
	if targetID == "" {
		targetID = w.ID
	}
	targetPath := strings.TrimSpace(opts.TargetPath)
	if targetPath == "" {
		targetPath = s.workPath(w.ID)
	}
	capability := orDefault(opts.Capability, "work.create")
	action := orDefault(opts.Action, "create_work")
	targetKind := orDefault(opts.TargetKind, "work")
	mutationClass := orDefault(opts.MutationClass, "local_reversible")
	reversibility := orDefault(opts.Reversibility, "easy")
	blastRadius := orDefault(opts.BlastRadius, "local")
	return envelope.ActionIntent{
		Header: newHeader(
			envelope.SVActionIntent,
			idFor("act_", capability, action, targetKind, targetID, targetPath, mutationClass, reversibility, blastRadius, actor, ts(now)),
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
		Preview:       fmt.Sprintf("create work %s state %s", w.ID, w.State),
		RollbackPlan:  "delete created WorkEnvelope and matching lifecycle event before any claim/action starts",
	}
}

func summarizeCreatePolicy(pd envelope.PolicyDecision, source string) CreatePolicySummary {
	if strings.TrimSpace(source) == "" {
		source = "builtin:default"
	}
	return CreatePolicySummary{
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
