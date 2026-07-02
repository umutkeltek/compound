package distiller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"compound/internal/routeevidence"
)

const LiveExtractionSchemaVersion = "compound.distiller.live_extraction.v0.1"

type OpenAICompatibleOptions struct {
	BaseURL       string
	APIKey        string
	Model         string
	EvalCorpusDir string
	EvalRoot      string
	HTTPClient    *http.Client
	Now           time.Time
}

type OpenAIUsage = routeevidence.Usage

type LiveExtractionReport struct {
	SchemaVersion  string                `json:"schema_version"`
	Provider       string                `json:"provider"`
	Model          string                `json:"model"`
	Called         bool                  `json:"called"`
	Eval           EvalCorpusReport      `json:"eval"`
	Usage          OpenAIUsage           `json:"usage,omitempty"`
	LatencyMS      int64                 `json:"latency_ms,omitempty"`
	Response       LLMExtractionResponse `json:"response,omitempty"`
	LiveModelCalls int                   `json:"live_model_calls"`
}

type openAIChatRequest struct {
	Model          string              `json:"model"`
	Messages       []openAIChatMessage `json:"messages"`
	Temperature    float64             `json:"temperature"`
	ResponseFormat map[string]string   `json:"response_format,omitempty"`
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message openAIChatMessage `json:"message"`
	} `json:"choices"`
	Usage OpenAIUsage `json:"usage,omitempty"`
}

func ExtractWithOpenAICompatible(ctx context.Context, req LLMExtractionRequest, opts OpenAICompatibleOptions) (LiveExtractionReport, error) {
	report := LiveExtractionReport{
		SchemaVersion: LiveExtractionSchemaVersion,
		Provider:      "openai-compatible",
		Model:         strings.TrimSpace(opts.Model),
	}
	if strings.TrimSpace(opts.EvalCorpusDir) == "" {
		return report, fmt.Errorf("eval corpus is required before provider extraction")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	eval, err := EvaluateCorpusDirInRoot(opts.EvalCorpusDir, opts.EvalRoot, now)
	report.Eval = eval
	if err != nil {
		return report, err
	}
	if eval.Status != "pass" {
		return report, fmt.Errorf("eval corpus status %s; provider call blocked", eval.Status)
	}
	if eval.LiveModelCalls != 0 {
		return report, fmt.Errorf("eval corpus reported %d live model call(s); provider call blocked", eval.LiveModelCalls)
	}
	if strings.TrimSpace(opts.BaseURL) == "" {
		return report, fmt.Errorf("openai-compatible base_url is required")
	}
	if report.Model == "" {
		return report, fmt.Errorf("model is required")
	}

	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	started := time.Now()
	response, usage, err := callOpenAICompatible(ctx, client, opts.BaseURL, opts.APIKey, report.Model, req)
	report.Called = true
	report.LiveModelCalls = 1
	report.Usage = usage
	report.LatencyMS = time.Since(started).Milliseconds()
	report.Response = response
	if err != nil {
		return report, err
	}
	return report, nil
}

func ChatCompletionJSON(ctx context.Context, client *http.Client, baseURL, apiKey, model, system, user string) (content []byte, usage OpenAIUsage, latencyMS int64, err error) {
	if client == nil {
		client = http.DefaultClient
	}
	jsonResponseFormat := true
	content, usage, latencyMS, err = chatCompletionOnce(ctx, client, baseURL, apiKey, model, system, user, jsonResponseFormat)
	// Some local gateways (e.g. LM Studio) reject response_format "json_object";
	// retry once without it — the prompts already demand strict JSON and callers
	// strict-decode the content.
	if err != nil && strings.Contains(err.Error(), "response_format") {
		var retryLatency int64
		jsonResponseFormat = false
		content, usage, retryLatency, err = chatCompletionOnce(ctx, client, baseURL, apiKey, model, system, user, jsonResponseFormat)
		latencyMS += retryLatency
	}
	if isTransientOpenAICompatibleUpstreamError(err) {
		var retryLatency int64
		content, usage, retryLatency, err = chatCompletionOnce(ctx, client, baseURL, apiKey, model, system, user, jsonResponseFormat)
		latencyMS += retryLatency
	}
	return content, usage, latencyMS, err
}

func isTransientOpenAICompatibleUpstreamError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if !strings.Contains(msg, "openai-compatible status 400") {
		return false
	}
	return strings.Contains(msg, "upstream_error") ||
		strings.Contains(msg, "upstream 400 error") ||
		strings.Contains(msg, "missing `choices[0]`")
}

func chatCompletionOnce(ctx context.Context, client *http.Client, baseURL, apiKey, model, system, user string, jsonResponseFormat bool) (content []byte, usage OpenAIUsage, latencyMS int64, err error) {
	body := openAIChatRequest{
		Model:       model,
		Temperature: 0,
		Messages: []openAIChatMessage{
			{
				Role:    "system",
				Content: system,
			},
			{
				Role:    "user",
				Content: user,
			},
		},
	}
	if jsonResponseFormat {
		body.ResponseFormat = map[string]string{
			"type": "json_object",
		}
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, OpenAIUsage{}, 0, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, chatCompletionsURL(baseURL), bytes.NewReader(rawBody))
	if err != nil {
		return nil, OpenAIUsage{}, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(apiKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}
	started := time.Now()
	resp, err := client.Do(httpReq)
	latencyMS = time.Since(started).Milliseconds()
	if err != nil {
		return nil, OpenAIUsage{}, latencyMS, err
	}
	defer resp.Body.Close()
	rawResp, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, OpenAIUsage{}, latencyMS, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, OpenAIUsage{}, latencyMS, fmt.Errorf("openai-compatible status %d: %s", resp.StatusCode, strings.TrimSpace(string(rawResp)))
	}
	var chat openAIChatResponse
	if err := json.Unmarshal(rawResp, &chat); err != nil {
		return nil, OpenAIUsage{}, latencyMS, fmt.Errorf("parse openai-compatible response: %w", err)
	}
	if len(chat.Choices) == 0 || strings.TrimSpace(chat.Choices[0].Message.Content) == "" {
		return nil, chat.Usage, latencyMS, fmt.Errorf("openai-compatible response missing choice content")
	}
	return []byte(chat.Choices[0].Message.Content), chat.Usage, latencyMS, nil
}

func callOpenAICompatible(ctx context.Context, client *http.Client, baseURL, apiKey, model string, req LLMExtractionRequest) (LLMExtractionResponse, OpenAIUsage, error) {
	packet, err := json.MarshalIndent(req, "", " ")
	if err != nil {
		return LLMExtractionResponse{}, OpenAIUsage{}, err
	}
	content, usage, _, err := ChatCompletionJSON(ctx, client, baseURL, apiKey, model, strings.Join([]string{
		"Return only JSON matching compound.distiller.llm_extraction.v0.1.",
		"Do not include promotion_basis or any authority-bearing field.",
		"You extract candidates only; Compound owns adjudication, promotion, writeback.",
	}, "\n"), string(packet))
	if err != nil {
		return LLMExtractionResponse{}, usage, err
	}
	extraction, err := decodeOpenAICompatibleExtraction(content, req.TraceRef)
	if err != nil {
		return LLMExtractionResponse{}, usage, err
	}
	normalized, err := json.Marshal(extraction)
	if err != nil {
		return LLMExtractionResponse{}, usage, err
	}
	if _, err := NewGroundedLLMExtractor(req).Extract(normalized, DefaultPolicy()); err != nil {
		return LLMExtractionResponse{}, usage, err
	}
	return extraction, usage, nil
}

func decodeOpenAICompatibleExtraction(content []byte, requestTraceRef string) (LLMExtractionResponse, error) {
	content = stripJSONFence(content)
	var extraction LLMExtractionResponse
	if err := json.Unmarshal(content, &extraction); err != nil {
		return LLMExtractionResponse{}, fmt.Errorf("parse llm extraction content: %w", err)
	}
	var repairs []string
	if strings.TrimSpace(extraction.SchemaVersion) == "" {
		extraction.SchemaVersion = LLMExtractionSchemaVersion
		repairs = append(repairs, "repaired_missing_schema_version")
	}
	if extraction.SchemaVersion != LLMExtractionSchemaVersion {
		return LLMExtractionResponse{}, fmt.Errorf("llm extraction schema_version=%q, want %q", extraction.SchemaVersion, LLMExtractionSchemaVersion)
	}
	if strings.TrimSpace(extraction.TraceRef) == "" && strings.TrimSpace(requestTraceRef) != "" {
		extraction.TraceRef = strings.TrimSpace(requestTraceRef)
		repairs = append(repairs, "repaired_missing_trace_ref")
	}
	for i := range extraction.Candidates {
		if strings.TrimSpace(extraction.Candidates[i].Kind) == "memory_candidate" {
			extraction.Candidates[i].Kind = KindMemory
			repairs = append(repairs, "normalized_kind_memory_candidate")
		}
	}
	if len(repairs) > 0 {
		extraction.Warnings = append(extraction.Warnings, repairs...)
	}
	return extraction, nil
}

func stripJSONFence(data []byte) []byte {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return data
	}
	if strings.HasPrefix(trimmed, "```") {
		lines := strings.Split(trimmed, "\n")
		if len(lines) >= 3 && isJSONFenceOpener(lines[0]) && strings.TrimSpace(lines[len(lines)-1]) == "```" {
			return []byte(strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n")))
		}
	}
	if strings.HasSuffix(trimmed, "```") {
		withoutFence := strings.TrimSpace(strings.TrimSuffix(trimmed, "```"))
		if strings.HasPrefix(withoutFence, "{") {
			return []byte(withoutFence)
		}
	}
	return data
}

func isJSONFenceOpener(line string) bool {
	opener := strings.TrimSpace(line)
	if opener == "```" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(opener, "```")), "json")
}

func chatCompletionsURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	return base + "/chat/completions"
}
