package distiller

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/envelope"
)

const ExecutionRouteEvidenceSchemaVersion = "compound.execution_route_evidence.v0.1"

type RouteTarget struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Protocol string `json:"protocol"`
}

type RejectedRouteTarget struct {
	Target RouteTarget `json:"target"`
	Reason string      `json:"reason"`
}

type ExecutionRouteEvidence struct {
	SchemaVersion  string                `json:"schema_version"`
	ID             string                `json:"id"`
	CreatedAt      string                `json:"created_at"`
	RequestID      string                `json:"request_id"`
	WorkID         string                `json:"work_id,omitempty"`
	TransitionID   string                `json:"transition_id,omitempty"`
	ClientProfile  string                `json:"client_profile"`
	Protocol       string                `json:"protocol"`
	SelectedTarget RouteTarget           `json:"selected_target"`
	Fallbacks      []RouteTarget         `json:"fallbacks,omitempty"`
	Rejected       []RejectedRouteTarget `json:"rejected,omitempty"`
	Usage          OpenAIUsage           `json:"usage,omitempty"`
	CostMicros     int64                 `json:"cost_micros,omitempty"`
	LatencyMS      int64                 `json:"latency_ms,omitempty"`
	Status         string                `json:"status"`
	EvidenceRefs   []string              `json:"evidence_refs,omitempty"`
}

func BuildExecutionRouteEvidence(req LLMExtractionRequest, report LiveExtractionReport, now time.Time) (ExecutionRouteEvidence, error) {
	if strings.TrimSpace(req.TraceRef) == "" {
		return ExecutionRouteEvidence{}, fmt.Errorf("trace_ref required for execution route evidence")
	}
	if strings.TrimSpace(report.Provider) == "" {
		return ExecutionRouteEvidence{}, fmt.Errorf("provider required for execution route evidence")
	}
	if strings.TrimSpace(report.Model) == "" {
		return ExecutionRouteEvidence{}, fmt.Errorf("model required for execution route evidence")
	}
	createdAt := rfc3339(now)
	workID := ""
	if req.Work != nil {
		workID = req.Work.ID
	}
	status := "completed"
	if !report.Called {
		status = "not_called"
	}
	protocol := "openai-chat-completions"
	ev := ExecutionRouteEvidence{
		SchemaVersion: ExecutionRouteEvidenceSchemaVersion,
		CreatedAt:     createdAt,
		RequestID:     "llm_extraction:" + req.TraceRef,
		WorkID:        workID,
		ClientProfile: "compound.distiller.extract",
		Protocol:      protocol,
		SelectedTarget: RouteTarget{
			Provider: report.Provider,
			Model:    report.Model,
			Protocol: protocol,
		},
		Usage:        report.Usage,
		LatencyMS:    report.LatencyMS,
		Status:       status,
		EvidenceRefs: routeEvidenceRefs(req, report),
	}
	ev.ID = idFor("route_", ev.RequestID, ev.CreatedAt, ev.SelectedTarget.Provider, ev.SelectedTarget.Model, ev.Status)
	return ev, nil
}

func BuildExecutionRouteTransition(ev ExecutionRouteEvidence) (envelope.Transition, error) {
	if err := validateExecutionRouteEvidence(ev); err != nil {
		return envelope.Transition{}, err
	}
	workID := ev.WorkID
	if workID == "" {
		workID = idFor("work_route_", ev.ID)
	}
	evidenceRefs := dedupeRefs(append([]string{ev.ID}, ev.EvidenceRefs...))
	costRefs := []string{}
	if ev.CostMicros > 0 || ev.Usage.TotalTokens > 0 {
		costRefs = []string{ev.ID}
	}
	tr := envelope.Transition{
		Header: envelope.Header{
			SchemaVersion: envelope.SVTransition,
			ID:            idFor("tr_", ev.ID, ev.RequestID, ev.CreatedAt),
			CreatedAt:     ev.CreatedAt,
			Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  evidenceRefs,
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		SourceTraceID: ev.RequestID,
		BeforeRefs:    append([]string{}, ev.EvidenceRefs...),
		ActionRefs:    []string{ev.RequestID},
		AfterRefs:     []string{ev.ID},
		StateDelta:    routeTransitionDelta(ev),
		Projections: envelope.Projections{
			Memory:         []string{},
			Skill:          []string{},
			Product:        []string{},
			Cost:           costRefs,
			Risk:           []string{},
			Taste:          []string{},
			Routing:        []string{ev.ID},
			HumanAttention: []string{},
		},
		SecondOrder: envelope.SecondOrder{FutureBehaviorChanged: false},
	}
	if err := tr.Validate(); err != nil {
		return envelope.Transition{}, err
	}
	return tr, nil
}

func RecordExecutionRouteEvidence(homePath string, ev ExecutionRouteEvidence) error {
	if err := validateExecutionRouteEvidence(ev); err != nil {
		return err
	}
	path := filepath.Join(homePath, "ledger", "execution-route-evidence.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(&ev)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func RecordExecutionRouteTransition(homePath string, tr envelope.Transition) error {
	if err := tr.Validate(); err != nil {
		return err
	}
	path := filepath.Join(homePath, "events", tr.ID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(&tr, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func validateExecutionRouteEvidence(ev ExecutionRouteEvidence) error {
	var errs []string
	req := func(name, value string) {
		if strings.TrimSpace(value) == "" {
			errs = append(errs, name+" required")
		}
	}
	req("schema_version", ev.SchemaVersion)
	req("id", ev.ID)
	req("created_at", ev.CreatedAt)
	req("request_id", ev.RequestID)
	req("client_profile", ev.ClientProfile)
	req("protocol", ev.Protocol)
	req("selected_target.provider", ev.SelectedTarget.Provider)
	req("selected_target.model", ev.SelectedTarget.Model)
	req("selected_target.protocol", ev.SelectedTarget.Protocol)
	req("status", ev.Status)
	if ev.SchemaVersion != ExecutionRouteEvidenceSchemaVersion {
		errs = append(errs, fmt.Sprintf("schema_version=%q, want %q", ev.SchemaVersion, ExecutionRouteEvidenceSchemaVersion))
	}
	if ev.CreatedAt != "" {
		if _, err := time.Parse(time.RFC3339, ev.CreatedAt); err != nil {
			errs = append(errs, fmt.Sprintf("created_at invalid: %v", err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("execution route evidence invalid: %s", strings.Join(errs, "; "))
	}
	return nil
}

func routeTransitionDelta(ev ExecutionRouteEvidence) string {
	parts := []string{
		"route evidence recorded",
		ev.SelectedTarget.Provider + "/" + ev.SelectedTarget.Model,
		"via " + ev.Protocol,
		"status " + ev.Status,
	}
	if ev.Usage.TotalTokens > 0 {
		parts = append(parts, fmt.Sprintf("%d tokens", ev.Usage.TotalTokens))
	}
	if ev.LatencyMS > 0 {
		parts = append(parts, fmt.Sprintf("%dms", ev.LatencyMS))
	}
	parts = append(parts, "not task success")
	return strings.Join(parts, "; ")
}

func dedupeRefs(refs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	return out
}

func routeEvidenceRefs(req LLMExtractionRequest, report LiveExtractionReport) []string {
	seen := map[string]bool{}
	var refs []string
	add := func(ref string) {
		ref = strings.TrimSpace(ref)
		if ref == "" || seen[ref] {
			return
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	for _, ref := range req.SourceRefs {
		add(ref)
	}
	if report.Eval.Dir != "" {
		add("eval_corpus:" + report.Eval.Dir)
	}
	if len(refs) == 0 {
		add("trace_ref:" + req.TraceRef)
	}
	return refs
}
