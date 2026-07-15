package work

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/envelope"
)

const RunObservationSchemaVersion = "compound.work_run_observation.v0.1"

type ObserveRunInput struct {
	Actor   string
	TraceID string
	Note    string
}

type RunObservationResult struct {
	SchemaVersion    string                 `json:"schema_version"`
	ID               string                 `json:"id"`
	GeneratedAt      string                 `json:"generated_at"`
	Status           string                 `json:"status"`
	Actor            string                 `json:"actor"`
	Note             string                 `json:"note,omitempty"`
	ResultPath       string                 `json:"result_path,omitempty"`
	WorkID           string                 `json:"work_id"`
	FromState        string                 `json:"from_state"`
	State            string                 `json:"state"`
	TraceID          string                 `json:"trace_id"`
	ActionIntentID   string                 `json:"action_intent_id,omitempty"`
	PolicyDecisionID string                 `json:"policy_decision_id,omitempty"`
	Work             envelope.WorkEnvelope  `json:"work"`
	Trace            envelope.TraceEnvelope `json:"trace"`
	ActionContext    *WorkActionContext     `json:"action_context,omitempty"`
	NoAction         string                 `json:"no_action,omitempty"`
	Warnings         []string               `json:"warnings,omitempty"`
}

func (s *Store) ObserveRun(workID string, in ObserveRunInput, now time.Time) (RunObservationResult, error) {
	workID = strings.TrimSpace(workID)
	if workID == "" {
		return RunObservationResult{}, fmt.Errorf("observe-run requires <work_id>")
	}
	traceID := strings.TrimSpace(in.TraceID)
	if traceID == "" {
		return RunObservationResult{}, fmt.Errorf("observe-run requires --trace <trace_id>")
	}
	if !strings.HasPrefix(traceID, "trace_") {
		return RunObservationResult{}, fmt.Errorf("observe-run trace id must start with trace_: %s", traceID)
	}
	w, err := s.Get(workID)
	if err != nil {
		return RunObservationResult{}, err
	}
	trace, err := s.GetTrace(traceID)
	if err != nil {
		return RunObservationResult{}, err
	}
	if trace.IDs.WorkID != "" && trace.IDs.WorkID != workID {
		return RunObservationResult{}, fmt.Errorf("trace %s belongs to work %s, not %s", trace.ID, trace.IDs.WorkID, workID)
	}
	actor := strings.TrimSpace(in.Actor)
	if actor == "" {
		actor = "codex"
	}
	note := strings.TrimSpace(in.Note)
	if note == "" {
		note = "observed run trace " + trace.ID
	}
	from := w.State
	advanced, err := s.Advance(workID, AdvanceInput{
		To:             "verifying",
		Actor:          actor,
		VerificationID: trace.ID,
		Note:           note,
	}, now)
	if err != nil {
		return RunObservationResult{}, err
	}
	result := RunObservationResult{
		SchemaVersion:    RunObservationSchemaVersion,
		ID:               idFor("wro_", workID, trace.ID, actor, ts(now)),
		GeneratedAt:      now.UTC().Format(time.RFC3339),
		Status:           "observed",
		Actor:            actor,
		Note:             strings.TrimSpace(in.Note),
		WorkID:           workID,
		FromState:        from,
		State:            advanced.State,
		TraceID:          trace.ID,
		PolicyDecisionID: advanced.PolicyDecisionID,
		Work:             advanced,
		Trace:            trace,
		Warnings: []string{
			"attaches observed trace as verification evidence",
			"does not execute task commands",
			"does not close work",
			"does not emit receipt or transition proof",
		},
	}
	if actionContext, warnings := s.actionContextForWork(advanced); actionContext != nil || len(warnings) > 0 {
		result.ActionContext = actionContext
		result.Warnings = append(result.Warnings, warnings...)
	}
	if len(advanced.RunIDs) > 0 {
		result.ActionIntentID = advanced.RunIDs[len(advanced.RunIDs)-1]
	}
	path, err := s.RecordRunObservation(result)
	if err != nil {
		return RunObservationResult{}, err
	}
	result.ResultPath = path
	return result, nil
}

func (s *Store) GetTrace(traceID string) (envelope.TraceEnvelope, error) {
	var trace envelope.TraceEnvelope
	if err := s.readEventJSON(traceID, &trace); err != nil {
		return trace, err
	}
	if err := trace.Validate(); err != nil {
		return trace, err
	}
	return trace, nil
}

func (s *Store) RecordRunObservation(result RunObservationResult) (string, error) {
	if strings.TrimSpace(result.ID) == "" {
		return "", os.ErrInvalid
	}
	dir := filepath.Join(s.home, "run-observations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, result.ID+".json")
	if err := s.writeJSON(path, &result); err != nil {
		return "", err
	}
	return path, nil
}
