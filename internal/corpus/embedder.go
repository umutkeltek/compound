package corpus

import (
	"compound/internal/breadth"
	"context"
	"fmt"
	"strings"
)

type configuredEmbedder struct {
	adapter  breadth.EmbedderAdapter
	identity string
	dim      int
}

func NewConfiguredEmbedder(lane LaneConfig) (Embedder, error) {
	provider := lane.Embedder.Provider
	if strings.EqualFold(provider, "local") {
		provider = "ollama"
	}
	adapter, err := breadth.NewOpenAICompatEmbedder(breadth.OpenAICompatEmbedderConfig{
		Provider: provider,
		Model:    lane.Embedder.Model,
		BaseURL:  lane.Embedder.BaseURL,
	})
	if err != nil {
		return nil, err
	}
	return configuredEmbedder{adapter: adapter, identity: lane.Embedder.Identity(), dim: lane.Embedder.Dims}, nil
}

func (e configuredEmbedder) Identity() string { return e.identity }
func (e configuredEmbedder) Dim() int         { return e.dim }

func (e configuredEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	vectors, err := e.adapter.Embed(ctx, texts)
	if err != nil {
		return nil, err
	}
	for i, vec := range vectors {
		if len(vec) != e.dim {
			return nil, fmt.Errorf("embedding %d dim %d != configured dim %d", i, len(vec), e.dim)
		}
	}
	return vectors, nil
}
