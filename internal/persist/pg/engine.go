package pg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pgvector/pgvector-go"

	"compound/internal/mem"
)

// PgEngine implements mem.Engine backed by Postgres + pgvector. It embeds query
// text via an OpenAI-compatible /v1/embeddings endpoint (e.g. Ollama) and
// searches the vectors table using the HNSW index. Vectors are populated
// out-of-band by "compound store embed"; Index() is a generation-tracking no-op
// (the corpus lives in Postgres, not in the engine process).
type PgEngine struct {
	store      *Store
	embedURL   string // base URL, e.g. "http://localhost:11434/v1"
	embedModel string // e.g. "nomic-embed-text"
	dims       int
	corpusGen  int64
}

// NewPgEngine returns a mem.Engine that queries pgvector for semantic recall.
// embedURL is the OpenAI-compatible base (scheme://host:port/v1); the engine
// appends "/embeddings". dims must match the stored vector dimension.
func NewPgEngine(store *Store, embedURL, embedModel string, dims int) *PgEngine {
	return &PgEngine{
		store:      store,
		embedURL:   strings.TrimRight(embedURL, "/"),
		embedModel: embedModel,
		dims:       dims,
	}
}

func (e *PgEngine) Capabilities() mem.EngineCapabilities {
	return mem.EngineCapabilities{
		Name:      "pgvector",
		Breadth:   true,
		Embedder:  fmt.Sprintf("ollama:%s:%d", e.embedModel, e.dims),
		CorpusGen: e.corpusGen,
	}
}

// Index is a no-op: vectors are populated out-of-band by "compound store embed".
// It records the generation so Capabilities() reports it and the breadth query
// path does not re-invoke Index on every call.
func (e *PgEngine) Index(_ []mem.Claim, gen int64) error {
	e.corpusGen = gen
	return nil
}

// Query embeds the text via the configured endpoint, then searches pgvector
// with an allowlist gate (GateList → WHERE ref = ANY; GateAll → unfiltered).
// An empty allowlist under GateList matches nothing (fail-closed, matching
// LexicalEngine semantics).
func (e *PgEngine) Query(text, gate string, allowlist []string, k int) ([]mem.Hit, error) {
	if gate != mem.GateAll && len(allowlist) == 0 {
		return nil, nil // fail-closed: empty allowlist matches nothing
	}
	vec, err := e.embed(text)
	if err != nil {
		return nil, fmt.Errorf("pgvector engine: embed: %w", err)
	}
	hits, err := e.searchAllowlisted(vec, gate, allowlist, k)
	if err != nil {
		return nil, fmt.Errorf("pgvector engine: search: %w", err)
	}
	return hits, nil
}

// --- embedding ---

type pgEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type pgEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

func (e *PgEngine) embed(text string) ([]float32, error) {
	reqBody, _ := json.Marshal(pgEmbedRequest{Model: e.embedModel, Input: []string{text}})
	url := e.embedURL + "/embeddings"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, fmt.Errorf("embed API %d: %s", resp.StatusCode, string(body))
	}
	var result pgEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("embed decode: %w", err)
	}
	if len(result.Data) == 0 || len(result.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embed returned empty vector")
	}
	return result.Data[0].Embedding, nil
}

// --- search ---

func (e *PgEngine) searchAllowlisted(queryVec []float32, gate string, allowlist []string, k int) ([]mem.Hit, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	vec := pgvector.NewVector(queryVec)

	var (
		rows interface{ Next() bool; Scan(...any) error; Close(); Err() error }
		err  error
	)

	if gate == mem.GateAll {
		rows, err = e.store.pool.Query(ctx,
			`SELECT ref, 1 - (embedding <=> $1) AS score FROM vectors ORDER BY embedding <=> $1 LIMIT $2`,
			vec, k,
		)
	} else {
		rows, err = e.store.pool.Query(ctx,
			`SELECT ref, 1 - (embedding <=> $1) AS score FROM vectors WHERE ref = ANY($2) ORDER BY embedding <=> $1 LIMIT $3`,
			vec, allowlist, k,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("persist/pg: allowlisted search: %w", err)
	}
	defer rows.Close()

	var hits []mem.Hit
	for rows.Next() {
		var h mem.Hit
		if err := rows.Scan(&h.ClaimID, &h.Score); err != nil {
			return nil, fmt.Errorf("persist/pg: scan hit: %w", err)
		}
		h.Kind = mem.KindIndexHit
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// Close releases the underlying connection pool. Callers that create a PgEngine
// for a single query batch should defer Close.
func (e *PgEngine) Close() {
	if e.store != nil && e.store.pool != nil {
		e.store.pool.Close()
	}
}
