package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"compound/internal/distiller"
)

const defaultGatewayCallTimeout = 120 * time.Second

// GatewayCaller calls an OpenAI-compatible chat-completions endpoint for panel lenses.
type GatewayCaller struct {
	BaseURL       string
	APIKey        string
	HTTPClient    *http.Client
	ModelResolver func(lane LaneRef) string
	// RequireLoadedContextMax checks LM Studio's live model metadata before a
	// panel run so local long-context review does not silently run at 8k.
	RequireLoadedContextMax bool
}

func (g GatewayCaller) RequiresMeteredBudget() bool { return true }

func (g GatewayCaller) Preflight(ctx context.Context, run Run) error {
	if !g.RequireLoadedContextMax {
		return nil
	}
	baseURL := strings.TrimSpace(g.BaseURL)
	if baseURL == "" {
		return fmt.Errorf("openai-compatible base_url is required")
	}
	models, err := g.lmStudioModels(ctx, baseURL)
	if err != nil {
		return fmt.Errorf("lm studio context preflight: %w", err)
	}
	seen := map[string]bool{}
	for _, lane := range []LaneRef{run.Route.LensLane, run.Route.JudgeLane} {
		model := g.resolveModel(lane)
		if model == "" || seen[model] {
			continue
		}
		seen[model] = true
		info, ok := models[model]
		if !ok {
			return fmt.Errorf("lm studio context preflight: model %q not listed by /api/v0/models", model)
		}
		if strings.TrimSpace(info.State) != "loaded" {
			return fmt.Errorf("lm studio context preflight: model %q state=%q; load it with max context before panel run", model, info.State)
		}
		if info.LoadedContextLength == nil {
			return fmt.Errorf("lm studio context preflight: model %q loaded_context_length missing", model)
		}
		if info.MaxContextLength <= 0 {
			return fmt.Errorf("lm studio context preflight: model %q max_context_length missing", model)
		}
		if *info.LoadedContextLength < info.MaxContextLength {
			return fmt.Errorf("lm studio context preflight: model %q loaded_context_length=%d below max_context_length=%d; reload with `lms unload %s && lms load %s -c %d --parallel 1`", model, *info.LoadedContextLength, info.MaxContextLength, model, model, info.MaxContextLength)
		}
	}
	return nil
}

func (g GatewayCaller) Call(ctx context.Context, req CallRequest) (CallResult, error) {
	baseURL := strings.TrimSpace(g.BaseURL)
	if baseURL == "" {
		return CallResult{}, fmt.Errorf("openai-compatible base_url is required")
	}
	model := g.resolveModel(req.Lane)
	if model == "" {
		return CallResult{}, fmt.Errorf("model is required")
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultGatewayCallTimeout)
	defer cancel()
	content, usage, latencyMS, err := distiller.ChatCompletionJSON(callCtx, g.HTTPClient, baseURL, g.APIKey, model, req.System, req.User)
	if err != nil {
		return CallResult{TotalTokens: usage.TotalTokens, LatencyMS: latencyMS}, err
	}
	return CallResult{Content: content, TotalTokens: usage.TotalTokens, LatencyMS: latencyMS}, nil
}

func (g GatewayCaller) resolveModel(lane LaneRef) string {
	model := lane.ModelID
	if g.ModelResolver != nil {
		model = g.ModelResolver(lane)
	}
	return strings.TrimSpace(model)
}

type lmStudioModelsPayload struct {
	Data []lmStudioModel `json:"data"`
}

type lmStudioModel struct {
	ID                  string `json:"id"`
	State               string `json:"state"`
	MaxContextLength    int    `json:"max_context_length"`
	LoadedContextLength *int   `json:"loaded_context_length"`
}

func (g GatewayCaller) lmStudioModels(ctx context.Context, baseURL string) (map[string]lmStudioModel, error) {
	modelsURL, err := lmStudioModelsURL(baseURL)
	if err != nil {
		return nil, err
	}
	client := g.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("models status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var payload lmStudioModelsPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("parse models response: %w", err)
	}
	out := make(map[string]lmStudioModel, len(payload.Data))
	for _, model := range payload.Data {
		if strings.TrimSpace(model.ID) != "" {
			out[strings.TrimSpace(model.ID)] = model
		}
	}
	return out, nil
}

func lmStudioModelsURL(baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("openai-compatible base_url must be absolute")
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(path, "/v1") {
		path = strings.TrimSuffix(path, "/v1")
	}
	parsed.Path = strings.TrimRight(path, "/") + "/api/v0/models"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
