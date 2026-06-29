package distiller

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
