package panel

import (
	"context"
	"fmt"
	"net/http"
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
}

func (g GatewayCaller) RequiresMeteredBudget() bool { return true }

func (g GatewayCaller) Call(ctx context.Context, req CallRequest) (CallResult, error) {
	baseURL := strings.TrimSpace(g.BaseURL)
	if baseURL == "" {
		return CallResult{}, fmt.Errorf("openai-compatible base_url is required")
	}
	model := req.Lane.ModelID
	if g.ModelResolver != nil {
		model = g.ModelResolver(req.Lane)
	}
	model = strings.TrimSpace(model)
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
