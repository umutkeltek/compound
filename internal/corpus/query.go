package corpus

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"compound/internal/mem"
)

type QueryReport struct {
	SchemaVersion    string     `json:"schema_version"`
	Lane             string     `json:"lane"`
	Query            string     `json:"query"`
	EmbedderIdentity string     `json:"embedder_identity"`
	Hits             []QueryHit `json:"hits"`
	Cards            []string   `json:"cards"`
}

type QueryHit struct {
	ItemID     string  `json:"item_id"`
	ChunkID    string  `json:"chunk_id"`
	SourceKind string  `json:"source_kind,omitempty"`
	Title      string  `json:"title,omitempty"`
	Score      float64 `json:"score"`
	Text       string  `json:"text,omitempty"`
	TextStatus string  `json:"text_status"`
}

type QueryOptions struct {
	IncludeText bool
}

func Query(ctx context.Context, homePath string, lane LaneConfig, embedder Embedder, text string, k int) (QueryReport, error) {
	return QueryWithOptions(ctx, homePath, lane, embedder, text, k, QueryOptions{})
}

func QueryWithOptions(ctx context.Context, homePath string, lane LaneConfig, embedder Embedder, text string, k int, opts QueryOptions) (QueryReport, error) {
	lane, err := ValidateLaneConfig(lane)
	if err != nil {
		return QueryReport{}, err
	}
	if err := validateEmbedder(lane, embedder); err != nil {
		return QueryReport{}, err
	}
	transport := NewIndexTransport(homePath, lane, embedder)
	engine := mem.NewProtocolEngine(transport, lane.Embedder.Identity())
	defer engine.Close()
	hits, err := engine.Query(text, mem.GateAll, nil, k)
	if err != nil {
		return QueryReport{}, err
	}
	dir := IndexDir(homePath, lane)
	meta, err := loadChunkMeta(dir)
	if err != nil {
		return QueryReport{}, err
	}
	manifest, err := readManifest(dir)
	if err != nil {
		return QueryReport{}, err
	}
	report := QueryReport{
		SchemaVersion:    ManifestSchemaVersion,
		Lane:             lane.Lane,
		Query:            text,
		EmbedderIdentity: lane.Embedder.Identity(),
		Cards:            []string{},
	}
	for _, hit := range hits {
		info := meta[hit.ClaimID]
		resolved, err := ResolveChunkText(manifest, hit.ClaimID, opts.IncludeText)
		if err != nil {
			return QueryReport{}, fmt.Errorf("resolve chunk text for %s: %w", hit.ClaimID, err)
		}
		report.Hits = append(report.Hits, QueryHit{
			ItemID:     info.itemID,
			ChunkID:    hit.ClaimID,
			SourceKind: info.sourceKind,
			Title:      info.title,
			Score:      hit.Score,
			Text:       resolved.Text,
			TextStatus: string(resolved.Status),
		})
	}
	return report, nil
}

type IndexTransport struct {
	homePath string
	lane     LaneConfig
	embedder Embedder
	loaded   *loadedIndex
}

type loadedIndex struct {
	identity IdentityFile
	manifest Manifest
	vectors  VectorFile
}

func NewIndexTransport(homePath string, lane LaneConfig, embedder Embedder) *IndexTransport {
	return &IndexTransport{homePath: homePath, lane: lane, embedder: embedder}
}

func (t *IndexTransport) RoundTrip(req mem.Request) (mem.Response, error) {
	resp := mem.Response{SchemaVersion: mem.ProtocolVersion, RequestID: req.RequestID}
	switch req.Op {
	case mem.OpCapabilities:
		loaded, err := t.load()
		if err != nil {
			resp.OK = false
			resp.Error = &mem.WireError{Code: mem.CodeEngineUnavailable, Message: err.Error()}
			return resp, nil
		}
		resp.OK = true
		resp.Name = "corpus-index"
		resp.Breadth = true
		resp.Ready = true
		resp.Embedder = loaded.identity.EmbedderIdentity
		return resp, nil
	case mem.OpQuery:
		loaded, err := t.load()
		if err != nil {
			resp.OK = false
			resp.Error = &mem.WireError{Code: mem.CodeEngineUnavailable, Message: err.Error()}
			return resp, nil
		}
		hits, err := t.query(loaded, req)
		if err != nil {
			resp.OK = false
			resp.Error = &mem.WireError{Code: mem.CodeBadRequest, Message: err.Error()}
			return resp, nil
		}
		resp.OK = true
		resp.Hits = hits
		return resp, nil
	default:
		resp.OK = false
		resp.Error = &mem.WireError{Code: mem.CodeBadRequest, Message: "unsupported corpus op " + req.Op}
		return resp, nil
	}
}

func (t *IndexTransport) Close() error { return nil }

func (t *IndexTransport) load() (*loadedIndex, error) {
	if t.loaded != nil {
		return t.loaded, nil
	}
	dir := IndexDir(t.homePath, t.lane)
	identity, err := readIdentity(dir)
	if err != nil {
		return nil, err
	}
	manifest, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	vectors, err := readVectors(dir)
	if err != nil {
		return nil, err
	}
	t.loaded = &loadedIndex{identity: identity, manifest: manifest, vectors: vectors}
	return t.loaded, nil
}

func (t *IndexTransport) query(index *loadedIndex, req mem.Request) ([]mem.Hit, error) {
	queryVectors, err := t.embedder.Embed(context.Background(), []string{req.Text})
	if err != nil {
		return nil, err
	}
	if len(queryVectors) != 1 {
		return nil, fmt.Errorf("query embedder returned %d vectors", len(queryVectors))
	}
	query, err := normalizeVector(queryVectors[0])
	if err != nil {
		return nil, err
	}
	allow := map[string]bool{}
	for _, id := range req.Allowlist {
		allow[id] = true
	}
	current := manifestChunkHashes(index.manifest)
	var hits []mem.Hit
	for id, vec := range index.vectors.Vectors {
		if _, ok := current[id]; !ok {
			continue
		}
		if req.Gate != mem.GateAll && !allow[id] {
			continue
		}
		normalized, err := normalizeVector(vec)
		if err != nil {
			continue
		}
		if len(normalized) != len(query) {
			continue
		}
		hits = append(hits, mem.Hit{ClaimID: id, Score: cosine(query, normalized)})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].ClaimID < hits[j].ClaimID
		}
		return hits[i].Score > hits[j].Score
	})
	if req.K > 0 && len(hits) > req.K {
		hits = hits[:req.K]
	}
	return hits, nil
}

func cosine(a, b []float32) float64 {
	sum := 0.0
	for i := range a {
		sum += float64(a[i] * b[i])
	}
	return sum
}

type chunkInfo struct {
	itemID     string
	sourceKind string
	title      string
}

func loadChunkMeta(dir string) (map[string]chunkInfo, error) {
	manifest, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]chunkInfo{}
	for _, item := range manifest.Items {
		title := strings.TrimSpace(item.PageSlug)
		if title == "" {
			title = strings.TrimSpace(item.SourceRef)
		}
		for _, chunk := range item.Chunks {
			out[fmt.Sprintf("%s#%d", item.ID, chunk.Ordinal)] = chunkInfo{
				itemID:     item.ID,
				sourceKind: item.SourceKind,
				title:      title,
			}
		}
	}
	return out, nil
}
