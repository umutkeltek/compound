package breadth

import (
	"context"

	"compound/internal/mem"
)

// NewMemoryReader uses the owned ranker and stamped vectors without writing
// or embedding missing documents during recall.
func NewMemoryReader(ctx context.Context, homePath string) (mem.Engine, error) {
	embedder, _ := ResolveEmbedder(homePath)
	engine, err := NewEngine(EngineConfig{Context: ctx, CompoundHome: homePath, Embedder: embedder, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	return &memoryReader{engine: engine}, nil
}

type memoryReader struct{ engine *Engine }

func (r *memoryReader) Capabilities() mem.EngineCapabilities {
	identity := DefaultEmbedder
	if r.engine.embedder != nil {
		identity = r.engine.embedder.Identity()
	}
	gen := r.engine.CorpusGen()
	if !r.engine.Ready() {
		gen = -1
	}
	return mem.EngineCapabilities{Name: Name + "-readonly", Breadth: true, Embedder: identity, CorpusGen: gen}
}
func (r *memoryReader) Index(claims []mem.Claim, gen int64) error {
	docs := make([]mem.ClaimText, 0, len(claims))
	for _, c := range claims {
		docs = append(docs, mem.ClaimText{ClaimID: c.ID, Text: mem.ClaimIndexText(c), Entity: c.Entity, ClaimType: c.ClaimType})
	}
	_, err := r.engine.IndexWithError(docs, gen)
	return err
}
func (r *memoryReader) Query(text, gate string, allowlist []string, k int) ([]mem.Hit, error) {
	hits, _, err := r.QueryWithDiagnostics(text, gate, allowlist, k)
	return hits, err
}
func (r *memoryReader) QueryWithDiagnostics(text, gate string, allowlist []string, k int) ([]mem.Hit, []string, error) {
	result, err := r.engine.QueryWithLanes(text, gate, allowlist, k)
	return result.Hits, result.DegradedLanes, err
}
