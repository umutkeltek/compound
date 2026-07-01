package distiller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAICompatibleExtractionRequiresPassingCorpusBeforeCall(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		t.Fatal("provider should not be called when eval corpus is invalid")
	}))
	defer server.Close()

	_, err := ExtractWithOpenAICompatible(context.Background(), LLMExtractionRequest{TraceRef: "trace"}, OpenAICompatibleOptions{
		BaseURL:       server.URL,
		Model:         "fixture-model",
		EvalCorpusDir: t.TempDir(),
		HTTPClient:    server.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "contains no eval case") {
		t.Fatalf("err = %v, want invalid eval corpus", err)
	}
	if calls != 0 {
		t.Fatalf("provider calls = %d, want 0", calls)
	}
}

func TestOpenAICompatibleExtractionCallsProviderAfterCorpusPasses(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}
		var body openAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Model != "fixture-model" {
			t.Fatalf("model = %q", body.Model)
		}
		if len(body.Messages) != 2 || !strings.Contains(body.Messages[1].Content, "trace_live") {
			t.Fatalf("unexpected messages: %+v", body.Messages)
		}
		extraction := `{"schema_version":"compound.distiller.llm_extraction.v0.1","trace_ref":"trace_live","extractor":"httptest","model":"fixture-model","source_refs":["raw:one"],"candidates":[{"kind":"memory","entity":"svc","predicate":"lang","object":"go","confidence":0.91,"evidence_refs":["server:test"]}]}`
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"role": "assistant", "content": extraction},
			}},
			"usage": map[string]int{"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18},
		})
	}))
	defer server.Close()

	report, err := ExtractWithOpenAICompatible(context.Background(), LLMExtractionRequest{
		SchemaVersion: LLMExtractionSchemaVersion,
		TraceRef:      "trace_live",
		Instructions:  []string{"extract candidates only"},
	}, OpenAICompatibleOptions{
		BaseURL:       server.URL,
		APIKey:        "test-key",
		Model:         "fixture-model",
		EvalCorpusDir: filepath.Join("..", "..", "fixtures", "distiller"),
		HTTPClient:    server.Client(),
		Now:           t0,
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
	if report.Eval.Status != "pass" || report.Eval.CaseCount != 9 || report.Eval.LiveModelCalls != 0 {
		t.Fatalf("eval report = %+v", report.Eval)
	}
	if !report.Called || report.LiveModelCalls != 1 {
		t.Fatalf("provider call accounting = called:%v live:%d", report.Called, report.LiveModelCalls)
	}
	if report.Response.TraceRef != "trace_live" || len(report.Response.Candidates) != 1 {
		t.Fatalf("response = %+v", report.Response)
	}
	if report.Usage.TotalTokens != 18 {
		t.Fatalf("usage = %+v", report.Usage)
	}
}

func TestOpenAICompatibleExtractionRejectsBadModelContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		extraction := `{"schema_version":"compound.distiller.llm_extraction.v0.1","trace_ref":"trace_bad","source_refs":["raw:one"],"candidates":[{"kind":"memory","entity":"svc","predicate":"lang","object":"go","confidence":0.91,"promotion_basis":"model says so"}]}`
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"role": "assistant", "content": extraction},
			}},
		})
	}))
	defer server.Close()

	report, err := ExtractWithOpenAICompatible(context.Background(), LLMExtractionRequest{
		SchemaVersion: LLMExtractionSchemaVersion,
		TraceRef:      "trace_bad",
	}, OpenAICompatibleOptions{
		BaseURL:       server.URL,
		Model:         "fixture-model",
		EvalCorpusDir: filepath.Join("..", "..", "fixtures", "distiller"),
		HTTPClient:    server.Client(),
		Now:           t0,
	})
	if err == nil || !strings.Contains(err.Error(), "attempted promotion_basis") {
		t.Fatalf("err = %v, want promotion_basis rejection", err)
	}
	if !report.Called || report.LiveModelCalls != 1 {
		t.Fatalf("provider call accounting = called:%v live:%d", report.Called, report.LiveModelCalls)
	}
}

func TestOpenAICompatibleExtractionRequiresEvalCorpus(t *testing.T) {
	_, err := ExtractWithOpenAICompatible(context.Background(), LLMExtractionRequest{TraceRef: "trace"}, OpenAICompatibleOptions{
		BaseURL: "http://127.0.0.1:1/v1",
		Model:   "fixture-model",
	})
	if err == nil || !strings.Contains(err.Error(), "eval corpus is required") {
		t.Fatalf("err = %v, want eval corpus required", err)
	}
}

func TestChatCompletionsURL(t *testing.T) {
	if got := chatCompletionsURL("https://gateway.example/v1/"); got != "https://gateway.example/v1/chat/completions" {
		t.Fatalf("url = %q", got)
	}
	if got := chatCompletionsURL("https://gateway.example/v1/chat/completions"); got != "https://gateway.example/v1/chat/completions" {
		t.Fatalf("url = %q", got)
	}
}

func TestOpenAICompatibleExtractionBlocksFailingCorpus(t *testing.T) {
	dir := t.TempDir()
	response := `{"schema_version":"compound.distiller.llm_extraction.v0.1","trace_ref":"trace_fail","source_refs":["raw:one"],"candidates":[{"kind":"memory","entity":"svc","predicate":"lang","object":"go","confidence":0.91,"evidence_refs":["manual:test"]}]}`
	if err := os.WriteFile(filepath.Join(dir, "response.json"), []byte(response), 0o644); err != nil {
		t.Fatal(err)
	}
	evalCase := `{
  "schema_version": "compound.distiller.eval_case.v0.1",
  "id": "eval_expected_failure",
  "request": {"trace_ref": "trace_fail"},
  "response": "response.json",
  "expect": {
    "decisions": [{"kind":"memory","verdict":"promote"}],
    "current_claims": 0,
    "live_model_calls": 0
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "case-eval.json"), []byte(evalCase), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
	}))
	defer server.Close()

	report, err := ExtractWithOpenAICompatible(context.Background(), LLMExtractionRequest{TraceRef: "trace"}, OpenAICompatibleOptions{
		BaseURL:       server.URL,
		Model:         "fixture-model",
		EvalCorpusDir: dir,
		HTTPClient:    server.Client(),
		Now:           t0,
	})
	if err == nil || !strings.Contains(err.Error(), "provider call blocked") {
		t.Fatalf("err = %v, want provider call blocked", err)
	}
	if report.Eval.Status != "fail" || report.Eval.FailCount != 1 {
		t.Fatalf("eval report = %+v", report.Eval)
	}
	if calls != 0 {
		t.Fatalf("provider calls = %d, want 0", calls)
	}
}

func TestChatCompletionJSONReturnsRawContentUsageAndLatency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}
		var body openAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Model != "served-model" || body.ResponseFormat["type"] != "json_object" {
			t.Fatalf("request body = %+v", body)
		}
		if len(body.Messages) != 2 || body.Messages[0].Content != "system-json" || body.Messages[1].Content != "user-json" {
			t.Fatalf("messages = %+v", body.Messages)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"role": "assistant", "content": `{"ok":true}`},
			}},
			"usage": map[string]int{"total_tokens": 42},
		})
	}))
	defer server.Close()

	content, usage, latencyMS, err := ChatCompletionJSON(context.Background(), server.Client(), server.URL, "test-key", "served-model", "system-json", "user-json")
	if err != nil {
		t.Fatalf("chat completion: %v", err)
	}
	if string(content) != `{"ok":true}` || usage.TotalTokens != 42 || latencyMS < 0 {
		t.Fatalf("content=%s usage=%+v latency=%d", content, usage, latencyMS)
	}
}
