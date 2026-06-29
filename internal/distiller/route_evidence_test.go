package distiller

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildExecutionRouteEvidenceMetadataOnly(t *testing.T) {
	req := LLMExtractionRequest{
		SchemaVersion: LLMExtractionSchemaVersion,
		TraceRef:      "trace_secret",
		RawTrace:      "SECRET_PROMPT_BODY",
		SourceRefs:    []string{"raw:turn_7", "raw:turn_7"},
		Work:          &LLMWork{ID: "work_123"},
	}
	report := LiveExtractionReport{
		SchemaVersion:  LiveExtractionSchemaVersion,
		Provider:       "openai-compatible",
		Model:          "fixture-model",
		Called:         true,
		Eval:           EvalCorpusReport{Dir: "fixtures/distiller", Status: "pass", CaseCount: 6},
		Usage:          OpenAIUsage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14},
		LatencyMS:      23,
		LiveModelCalls: 1,
		Response: LLMExtractionResponse{
			TraceRef: "trace_secret",
			Candidates: []LLMCandidate{{
				Kind:       KindMemory,
				Object:     "SECRET_RESPONSE_BODY",
				Confidence: ptrFloat(0.9),
			}},
		},
	}

	ev, err := BuildExecutionRouteEvidence(req, report, t0)
	if err != nil {
		t.Fatalf("route evidence: %v", err)
	}
	if ev.SchemaVersion != ExecutionRouteEvidenceSchemaVersion {
		t.Fatalf("schema_version = %q", ev.SchemaVersion)
	}
	if ev.RequestID != "llm_extraction:trace_secret" || ev.WorkID != "work_123" {
		t.Fatalf("ids = request:%q work:%q", ev.RequestID, ev.WorkID)
	}
	if ev.SelectedTarget.Provider != "openai-compatible" || ev.SelectedTarget.Model != "fixture-model" {
		t.Fatalf("target = %+v", ev.SelectedTarget)
	}
	if ev.Usage.TotalTokens != 14 || ev.LatencyMS != 23 || ev.Status != "completed" {
		t.Fatalf("usage/latency/status = %+v %d %q", ev.Usage, ev.LatencyMS, ev.Status)
	}
	if got := strings.Join(ev.EvidenceRefs, ","); got != "raw:turn_7,eval_corpus:fixtures/distiller" {
		t.Fatalf("evidence refs = %q", got)
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SECRET_PROMPT_BODY") || strings.Contains(string(raw), "SECRET_RESPONSE_BODY") {
		t.Fatalf("route evidence leaked prompt/response body: %s", raw)
	}
}

func TestRecordExecutionRouteEvidenceAppendsJSONL(t *testing.T) {
	home := t.TempDir()
	ev, err := BuildExecutionRouteEvidence(LLMExtractionRequest{
		SchemaVersion: LLMExtractionSchemaVersion,
		TraceRef:      "trace_route",
	}, LiveExtractionReport{
		SchemaVersion:  LiveExtractionSchemaVersion,
		Provider:       "openai-compatible",
		Model:          "fixture-model",
		Called:         true,
		Eval:           EvalCorpusReport{Dir: "fixtures/distiller", Status: "pass"},
		Usage:          OpenAIUsage{TotalTokens: 3},
		LiveModelCalls: 1,
	}, t0)
	if err != nil {
		t.Fatalf("route evidence: %v", err)
	}
	if err := RecordExecutionRouteEvidence(home, ev); err != nil {
		t.Fatalf("record: %v", err)
	}
	path := filepath.Join(home, "ledger", "execution-route-evidence.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	var got ExecutionRouteEvidence
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("decode ledger row: %v", err)
	}
	if got.ID != ev.ID || got.RequestID != "llm_extraction:trace_route" {
		t.Fatalf("row = %+v", got)
	}
}

func TestBuildExecutionRouteEvidenceRequiresRouteShape(t *testing.T) {
	_, err := BuildExecutionRouteEvidence(LLMExtractionRequest{}, LiveExtractionReport{}, t0)
	if err == nil || !strings.Contains(err.Error(), "trace_ref required") {
		t.Fatalf("err = %v, want trace_ref required", err)
	}
	_, err = BuildExecutionRouteEvidence(LLMExtractionRequest{TraceRef: "trace"}, LiveExtractionReport{Provider: "openai-compatible"}, t0)
	if err == nil || !strings.Contains(err.Error(), "model required") {
		t.Fatalf("err = %v, want model required", err)
	}
}

func ptrFloat(v float64) *float64 {
	return &v
}
