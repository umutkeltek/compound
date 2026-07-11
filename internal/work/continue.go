package work

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const WorkContinueSchemaVersion = "compound.work_continue.v0.1"
const WorkContinueRunSchemaVersion = "compound.work_continue_run.v0.1"

const defaultWorkContinueMaxSteps = 10

type ContinueOptions struct {
	Actor        string
	RecentLimit  int
	ExpectWorkID string
	Outcome      string
	MoveType     string
	Summary      string
}

type ContinueResult struct {
	SchemaVersion       string                    `json:"schema_version"`
	ID                  string                    `json:"id"`
	GeneratedAt         string                    `json:"generated_at"`
	Status              string                    `json:"status"`
	Actor               string                    `json:"actor"`
	Action              string                    `json:"action"`
	WorkID              string                    `json:"work_id,omitempty"`
	ExpectWorkID        string                    `json:"expect_work_id,omitempty"`
	ResultPath          string                    `json:"result_path,omitempty"`
	Counts              WorkbenchCounts           `json:"counts"`
	PostCounts          *WorkbenchCounts          `json:"post_counts,omitempty"`
	Admission           *AdmitCreatePlanReport    `json:"admission,omitempty"`
	AdvancePlan         *AdvancePlan              `json:"advance_plan,omitempty"`
	DispatchStep        *DispatchStepResult       `json:"dispatch_step,omitempty"`
	ActionIntentID      string                    `json:"action_intent_id,omitempty"`
	PolicyDecisionID    string                    `json:"policy_decision_id,omitempty"`
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

type ContinueRunOptions struct {
	ContinueOptions
	MaxSteps int
}

type ContinueRunResult struct {
	SchemaVersion   string           `json:"schema_version"`
	ID              string           `json:"id"`
	GeneratedAt     string           `json:"generated_at"`
	Status          string           `json:"status"`
	Actor           string           `json:"actor"`
	ExpectWorkID    string           `json:"expect_work_id,omitempty"`
	ResultPath      string           `json:"result_path,omitempty"`
	MaxSteps        int              `json:"max_steps"`
	StepCount       int              `json:"step_count"`
	Steps           []ContinueResult `json:"steps"`
	CanContinue     bool             `json:"can_continue"`
	ContinueCommand []string         `json:"continue_command,omitempty"`
	WaitReason      string           `json:"wait_reason,omitempty"`
	BlockedOn       []string         `json:"blocked_on,omitempty"`
	NextAction      string           `json:"next_action,omitempty"`
	Warnings        []string         `json:"warnings,omitempty"`
	Integrity       []string         `json:"integrity,omitempty"`
}

func (s *Store) Continue(now time.Time, opts ContinueOptions) (ContinueResult, error) {
	actor := strings.TrimSpace(opts.Actor)
	if actor == "" {
		actor = "codex"
	}
	expectWorkID := strings.TrimSpace(opts.ExpectWorkID)
	report, err := s.Workbench(now, WorkbenchOptions{RecentLimit: opts.RecentLimit, Actor: actor})
	if err != nil {
		return ContinueResult{}, err
	}
	result := ContinueResult{
		SchemaVersion: WorkContinueSchemaVersion,
		ID:            idFor("wcon_", actor, ts(now)),
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Status:        "waiting",
		Actor:         actor,
		Action:        "none",
		ExpectWorkID:  expectWorkID,
		Counts:        report.Counts,
		Warnings: []string{
			"moves safe local lifecycle plumbing only",
			"does not execute task commands",
			"does not observe or synthesize traces",
			"does not promote memory",
		},
		Integrity: append([]string{}, report.Integrity...),
	}

	if report.DispatchNext.CanRun && continueMatchesExpected(expectWorkID, report.DispatchNext.WorkID) {
		step, err := s.DispatchStep(now, DispatchStepOptions{
			Actor:       actor,
			RecentLimit: opts.RecentLimit,
			Outcome:     opts.Outcome,
			MoveType:    opts.MoveType,
			Summary:     opts.Summary,
		})
		if err != nil {
			return ContinueResult{}, err
		}
		if expectWorkID != "" && step.WorkID != "" && step.WorkID != expectWorkID {
			result.Status = "target_changed"
			result.NoAction = "next safe lifecycle target changed; refresh before continuing"
			return s.recordContinue(now, result)
		}
		result.Action = step.Action
		result.Status = step.Status
		result.WorkID = step.WorkID
		result.DispatchStep = &step
		result.ContextPackID = step.ContextPackID
		result.CapabilitiesOffered = append([]DispatchCapabilityOffer{}, step.CapabilitiesOffered...)
		result.WorkerPromptMD = step.WorkerPromptMD
		result.NoAction = step.NoAction
		return s.recordContinue(now, result)
	}

	if item, plan, blocked := s.continuePrepareCandidate(now, report, actor, expectWorkID); item != nil {
		recordedPlan, err := s.AdvancePlan(item.WorkID, now, AdvancePlanOptions{Actor: actor, To: "ready", Record: true})
		if err != nil {
			return ContinueResult{}, err
		}
		advanced, err := s.Advance(item.WorkID, AdvanceInput{To: "ready", Actor: actor, Note: "continue prepare"}, now)
		if err != nil {
			return ContinueResult{}, err
		}
		result.Action = "prepare"
		result.Status = advanced.State
		result.WorkID = advanced.ID
		result.AdvancePlan = &recordedPlan
		result.PolicyDecisionID = advanced.PolicyDecisionID
		if len(advanced.RunIDs) > 0 {
			result.ActionIntentID = advanced.RunIDs[len(advanced.RunIDs)-1]
		}
		if plan != nil {
			result.Integrity = mergeUnique(result.Integrity, plan.Warnings)
		}
		result.BlockedOn = mergeUnique(result.BlockedOn, blocked)
		return s.recordContinue(now, result)
	}

	if preview := continueAdmissionPreview(report.AdmissionPreviews, expectWorkID); preview != nil {
		admission, err := s.AdmitCreatePlanPath(preview.PlanPath, now, AdmitCreatePlanOptions{
			Actor: actor,
			Note:  "continued_from_admission_preview",
		})
		if err != nil {
			return ContinueResult{}, err
		}
		result.Action = "admit"
		result.Status = admission.Status
		result.WorkID = admission.WorkID
		result.Admission = &admission
		result.ActionIntentID = admission.ActionIntentID
		result.PolicyDecisionID = admission.PolicyDecisionID
		result.NoAction = admission.NoAction
		return s.recordContinue(now, result)
	}

	result.NoAction = continueNoAction(report, expectWorkID)
	return s.recordContinue(now, result)
}

func (s *Store) ContinueRun(now time.Time, opts ContinueRunOptions) (ContinueRunResult, error) {
	actor := strings.TrimSpace(opts.Actor)
	if actor == "" {
		actor = "codex"
		opts.Actor = actor
	}
	maxSteps := opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultWorkContinueMaxSteps
	}
	result := ContinueRunResult{
		SchemaVersion: WorkContinueRunSchemaVersion,
		ID:            idFor("wconr_", actor, ts(now)),
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Status:        "waiting",
		Actor:         actor,
		ExpectWorkID:  strings.TrimSpace(opts.ExpectWorkID),
		MaxSteps:      maxSteps,
		Warnings: []string{
			"continues repeated safe local lifecycle steps until the next real wait",
			"does not execute task commands",
			"does not observe or synthesize traces",
			"does not promote memory",
		},
	}
	for i := 0; i < maxSteps; i++ {
		step, err := s.Continue(now.Add(time.Duration(i)*time.Nanosecond), opts.ContinueOptions)
		if err != nil {
			return ContinueRunResult{}, err
		}
		result.Steps = append(result.Steps, step)
		result.StepCount = len(result.Steps)
		result.NextAction = step.NextAction
		result.WaitReason = step.WaitReason
		result.BlockedOn = append([]string{}, step.BlockedOn...)
		result.Integrity = mergeUnique(result.Integrity, step.Integrity)
		if !step.CanContinue {
			result.Status = "waiting"
			result.CanContinue = false
			result.ContinueCommand = nil
			return s.recordContinueRun(result)
		}
	}
	result.Status = "max_steps_reached"
	result.CanContinue = true
	result.WaitReason = ""
	result.BlockedOn = nil
	result.ContinueCommand = continueCommand(actor, strings.TrimSpace(opts.ExpectWorkID), true, maxSteps)
	return s.recordContinueRun(result)
}

func (s *Store) recordContinue(now time.Time, result ContinueResult) (ContinueResult, error) {
	post, err := s.Workbench(now, WorkbenchOptions{RecentLimit: 0, Actor: result.Actor})
	if err != nil {
		return ContinueResult{}, err
	}
	result.PostCounts = &post.Counts
	result.NextAction = continueNextAction(result, post)
	result.CanContinue, result.ContinueCommand = continueCanContinue(post, result.Actor, result.ExpectWorkID)
	if !result.CanContinue {
		result.WaitReason, result.BlockedOn = continueWait(result, post)
	}
	result.Integrity = mergeUnique(result.Integrity, post.Integrity)
	path, err := s.RecordContinue(result)
	if err != nil {
		return ContinueResult{}, err
	}
	result.ResultPath = path
	return result, nil
}

func (s *Store) recordContinueRun(result ContinueRunResult) (ContinueRunResult, error) {
	path, err := s.RecordContinueRun(result)
	if err != nil {
		return ContinueRunResult{}, err
	}
	result.ResultPath = path
	return result, nil
}

func (s *Store) continuePrepareCandidate(now time.Time, report WorkbenchReport, actor, expectWorkID string) (*WorkbenchItem, *AdvancePlan, []string) {
	var blocked []string
	for i := range report.Open {
		item := report.Open[i]
		if !continueMatchesExpected(expectWorkID, item.WorkID) {
			continue
		}
		if item.State != "proposed" && item.State != "admitted" {
			continue
		}
		// Enforce the same admission gate as continueAdmissionPreview: a candidate
		// whose action context requires operator inputs must not be advanced to
		// ready by the autonomous loop, even when policy allows the local advance.
		// Otherwise proposed->ready would bypass the operator-owned admission gate.
		if workActionContextRequiresOperator(item.ActionContext) {
			blocked = append(blocked, "work.ready_state:"+item.WorkID)
			continue
		}
		plan, err := s.AdvancePlan(item.WorkID, now, AdvancePlanOptions{Actor: actor, To: "ready"})
		if err != nil {
			blocked = append(blocked, "work.ready_state:"+item.WorkID)
			continue
		}
		if plan.Policy.Allowed && strings.TrimSpace(plan.NoAction) == "" {
			return &item, &plan, blocked
		}
		blocked = append(blocked, "work.ready_state:"+item.WorkID)
	}
	return nil, nil, blocked
}

func continueAdmissionPreview(previews []WorkbenchAdmissionPreview, expectWorkID string) *WorkbenchAdmissionPreview {
	for i := range previews {
		preview := previews[i]
		if !continueMatchesExpected(expectWorkID, preview.WorkID) {
			continue
		}
		if !preview.PolicyAllowed || preview.RequiresOperator || strings.TrimSpace(preview.AdmissionBlockedReason) != "" {
			continue
		}
		if preview.Staleness != "fresh" || strings.TrimSpace(preview.PlanPath) == "" {
			continue
		}
		return &preview
	}
	return nil
}

func continueCanContinue(report WorkbenchReport, actor, expectWorkID string) (bool, []string) {
	if report.DispatchNext.CanRun && continueMatchesExpected(expectWorkID, report.DispatchNext.WorkID) {
		return true, continueCommand(actor, expectWorkID, false, 0)
	}
	for _, item := range report.Open {
		if continueMatchesExpected(expectWorkID, item.WorkID) && (item.State == "proposed" || item.State == "admitted") {
			// Operator-required proposed/admitted work cannot be safely prepared
			// (see continuePrepareCandidate), so it is not a continuation target;
			// treating it as one would spin ContinueRun to max_steps. This mirrors
			// the operator-required filtering continueAdmissionPreview applies below.
			if workActionContextRequiresOperator(item.ActionContext) {
				continue
			}
			return true, continueCommand(actor, expectWorkID, false, 0)
		}
	}
	if continueAdmissionPreview(report.AdmissionPreviews, expectWorkID) != nil {
		return true, continueCommand(actor, expectWorkID, false, 0)
	}
	return false, nil
}

func continueWait(result ContinueResult, report WorkbenchReport) (string, []string) {
	if result.Status == "target_changed" {
		blocked := []string{"work.target_changed"}
		if result.ExpectWorkID != "" {
			blocked = append(blocked, "work.target_changed:"+result.ExpectWorkID)
		}
		return "target_changed", blocked
	}
	reason, blockedOn := dispatchStepWait(DispatchStepResult{Actor: result.Actor, Status: result.Status, WorkID: result.WorkID}, report)
	for _, preview := range report.AdmissionPreviews {
		if !continueMatchesExpected(result.ExpectWorkID, preview.WorkID) {
			continue
		}
		if preview.RequiresOperator || strings.TrimSpace(preview.AdmissionBlockedReason) != "" || preview.Staleness != "fresh" || !preview.PolicyAllowed {
			blockedOn = append(blockedOn, "work.admission_preview:"+preview.WorkID)
		}
	}
	if len(blockedOn) == 0 && result.ExpectWorkID != "" {
		blockedOn = append(blockedOn, "work.expected:"+result.ExpectWorkID)
	}
	if len(blockedOn) > 0 && reason == "no_open_work" {
		reason = "needs_review"
	}
	return reason, mergeUnique(blockedOn)
}

func continueNextAction(result ContinueResult, post WorkbenchReport) string {
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
	if post.DispatchNext.CanRun {
		return "continue the next safe lifecycle step"
	}
	if continueAdmissionPreview(post.AdmissionPreviews, result.ExpectWorkID) != nil {
		return "add the suggested task to work"
	}
	return "waiting for work that can safely continue"
}

func continueNoAction(report WorkbenchReport, expectWorkID string) string {
	if expectWorkID != "" {
		return "expected work is not ready for a safe continue step"
	}
	if report.Counts.Open == 0 && len(report.AdmissionPreviews) == 0 {
		return "workbench has no open or suggested work"
	}
	return "no safe lifecycle step is ready"
}

func continueMatchesExpected(expectWorkID, workID string) bool {
	expectWorkID = strings.TrimSpace(expectWorkID)
	if expectWorkID == "" {
		return true
	}
	return strings.TrimSpace(workID) == expectWorkID
}

func continueCommand(actor, expectWorkID string, untilWait bool, maxSteps int) []string {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "codex"
	}
	out := []string{"compound", "work", "continue", "--actor", actor}
	if strings.TrimSpace(expectWorkID) != "" {
		out = append(out, "--expect-work", strings.TrimSpace(expectWorkID))
	}
	if untilWait {
		out = append(out, "--until-wait")
		if maxSteps > 0 {
			out = append(out, "--max-steps", strconv.Itoa(maxSteps))
		}
	}
	return out
}

func (s *Store) RecordContinue(result ContinueResult) (string, error) {
	if strings.TrimSpace(result.ID) == "" {
		return "", os.ErrInvalid
	}
	dir := filepath.Join(s.home, "continues")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, result.ID+".json")
	if err := s.writeJSON(path, &result); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) RecordContinueRun(result ContinueRunResult) (string, error) {
	if strings.TrimSpace(result.ID) == "" {
		return "", os.ErrInvalid
	}
	dir := filepath.Join(s.home, "continue-runs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, result.ID+".json")
	if err := s.writeJSON(path, &result); err != nil {
		return "", err
	}
	return path, nil
}
