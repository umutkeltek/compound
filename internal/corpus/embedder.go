package corpus

import (
	"compound/internal/breadth"
	"context"
	"fmt"
	"strings"
	"time"
)

const configuredEmbedBatch = 32

// Ingestion includes cold model load and long document batches. The caller's
// earlier deadline still bounds every request; interactive breadth keeps its own default.
const configuredEmbedTimeout = 60 * time.Second

type configuredEmbedder struct {
	config   EmbedderConfig
	adapter  breadth.EmbedderAdapter
	identity string
	dim      int
}

func NewConfiguredEmbedder(lane LaneConfig) (Embedder, error) {
	digest, err := normalizeModelDigest(lane.Embedder.ModelDigest)
	if err != nil {
		return nil, err
	}
	lane.Embedder.ModelDigest = digest
	if digest != "" {
		if _, err := modelInventoryURL(lane.Embedder); err != nil {
			return nil, err
		}
	}
	provider := lane.Embedder.Provider
	if strings.EqualFold(provider, "local") {
		provider = "ollama"
	}
	adapter, err := breadth.NewOpenAICompatEmbedder(breadth.OpenAICompatEmbedderConfig{
		Provider: provider,
		Batch:    configuredEmbedBatch,
		Timeout:  configuredEmbedTimeout,
		Model:    lane.Embedder.Model,
		BaseURL:  lane.Embedder.BaseURL,
	})
	if err != nil {
		return nil, err
	}
	return configuredEmbedder{config: lane.Embedder, adapter: adapter, identity: lane.Embedder.Identity(), dim: lane.Embedder.Dims}, nil
}

func (e configuredEmbedder) Identity() string     { return e.identity }
func (e configuredEmbedder) Dim() int             { return e.dim }
func (e configuredEmbedder) indexBatchLimit() int { return configuredEmbedBatch }

func (e configuredEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	var vectors [][]float32
	var err error
	if e.config.ModelDigest == "" {
		vectors, err = e.adapter.Embed(ctx, texts)
	} else {
		for start := 0; start < len(texts); start += configuredEmbedBatch {
			end := start + configuredEmbedBatch
			if end > len(texts) {
				end = len(texts)
			}
			if err = verifyModelDigest(ctx, e.config); err != nil {
				return nil, err
			}
			batch, eErr := e.adapter.Embed(ctx, texts[start:end])
			if err = verifyModelDigest(ctx, e.config); err != nil {
				return nil, err
			}
			if eErr != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, fmt.Errorf("configured embedding request unavailable")
			}

			vectors = append(vectors, batch...)
		}
	}
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
