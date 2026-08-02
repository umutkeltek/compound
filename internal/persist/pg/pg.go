// Package pg implements the persist interfaces backed by Postgres + pgvector.
// It is opt-in (COMPOUND_STORE=postgres) and requires a running Postgres instance
// with the vector extension enabled.
//
// The schema is auto-created on first connection via EnsureSchema().
// The file-based backend remains the default; this package is never imported
// unless the operator explicitly selects the postgres backend.
package pg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"compound/internal/envelope"
	"compound/internal/persist"
)

// Store is the Postgres-backed implementation of all persist interfaces.
type Store struct {
	pool *pgxpool.Pool
	cfg  persist.Config
}

// Open connects to Postgres and verifies the vector extension is available.
// It does NOT create tables — call EnsureSchema() for that.
func Open(ctx context.Context, cfg persist.Config) (*Store, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("persist/pg: COMPOUND_DSN required when COMPOUND_STORE=postgres")
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("persist/pg: parse DSN: %w", err)
	}

	// Conservative pool for a local-first tool
	poolCfg.MaxConns = 4
	poolCfg.MinConns = 1
	poolCfg.MaxConnLifetime = time.Hour

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("persist/pg: connect: %w", err)
	}

	// Verify connectivity
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("persist/pg: ping: %w", err)
	}

	return &Store{pool: pool, cfg: cfg}, nil
}

// Close releases the connection pool.
func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

// EnsureSchema creates the vector extension and all required tables if they
// do not exist. Idempotent — safe to call on every startup.
func (s *Store) EnsureSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE EXTENSION IF NOT EXISTS vector`,
		schemaDDL(s.cfg.VectorDimensions),
	}
	for _, stmt := range stmts {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("persist/pg: ensure schema: %w", err)
		}
	}
	return nil
}

func schemaDDL(vectorDims int) string {
	return fmt.Sprintf(`
-- Claims: append-only, MVCC-by-fold. Never updated, never deleted.
CREATE TABLE IF NOT EXISTS claims (
    seq             BIGSERIAL PRIMARY KEY,
    id              TEXT NOT NULL UNIQUE,
    entity          TEXT NOT NULL,
    predicate       TEXT NOT NULL,
    object          TEXT NOT NULL,
    observation_date TEXT,
    confidence      DOUBLE PRECISION DEFAULT 0,
    derivation      TEXT DEFAULT 'deterministic',
    claim_type      TEXT DEFAULT '',
    canon_class     TEXT DEFAULT '',
    qualifiers      JSONB DEFAULT '{}',
    visibility      TEXT DEFAULT 'candidate',
    state           TEXT DEFAULT 'promoted',
    source_trace_ref TEXT DEFAULT '',
    evidence_refs   JSONB DEFAULT '[]',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_claims_entity ON claims(entity);
CREATE INDEX IF NOT EXISTS idx_claims_visibility ON claims(visibility);
CREATE INDEX IF NOT EXISTS idx_claims_state ON claims(state);

-- Edges: version-chain relationships (supersedes, reinforces, etc.)
CREATE TABLE IF NOT EXISTS edges (
    id          BIGSERIAL PRIMARY KEY,
    from_id     TEXT NOT NULL,
    to_id       TEXT NOT NULL,
    kind        TEXT NOT NULL,
    claim_id    TEXT NOT NULL,
    seq         BIGINT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_edges_from ON edges(from_id);
CREATE INDEX IF NOT EXISTS idx_edges_claim ON edges(claim_id);
DO $$ BEGIN
    ALTER TABLE edges ADD CONSTRAINT edges_natural_key UNIQUE (from_id, to_id, kind, claim_id, seq);
EXCEPTION WHEN duplicate_table THEN NULL;
END $$;

-- Candidates: untrusted proposed facts awaiting adjudication.
CREATE TABLE IF NOT EXISTS candidates (
    id              TEXT PRIMARY KEY,
    entity          TEXT NOT NULL,
    predicate       TEXT NOT NULL,
    object          TEXT NOT NULL,
    confidence      DOUBLE PRECISION DEFAULT 0,
    derivation      TEXT DEFAULT 'extracted',
    claim_type      TEXT DEFAULT '',
    canon_class     TEXT DEFAULT '',
    qualifiers      JSONB DEFAULT '{}',
    visibility      TEXT DEFAULT 'candidate',
    state           TEXT DEFAULT 'proposed',
    source_trace_ref TEXT DEFAULT '',
    evidence_refs   JSONB DEFAULT '[]',
    reason          TEXT DEFAULT '',
    adjudicated_by  TEXT DEFAULT '',
    adjudicated_at  TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_candidates_state ON candidates(state);
CREATE INDEX IF NOT EXISTS idx_candidates_entity ON candidates(entity);

-- Events: append-only event log (replaces JSONL segments).
CREATE TABLE IF NOT EXISTS events (
    id          TEXT PRIMARY KEY,
    kind        TEXT NOT NULL DEFAULT '',
    payload     JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_events_kind ON events(kind);

-- Work log: the work event stream (replaces work.jsonl).
CREATE TABLE IF NOT EXISTS work_log (
    seq         BIGSERIAL PRIMARY KEY,
    data        JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Work envelopes: current state of each work item.
CREATE TABLE IF NOT EXISTS work_envelopes (
    work_id     TEXT PRIMARY KEY,
    state       TEXT NOT NULL,
    owner       TEXT DEFAULT '',
    payload     JSONB NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_work_state ON work_envelopes(state);

-- Vectors: pgvector embeddings for semantic recall.
CREATE TABLE IF NOT EXISTS vectors (
    ref         TEXT PRIMARY KEY,
    embedding   vector(%d) NOT NULL,
    metadata    JSONB DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_vectors_embedding ON vectors
    USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);
`, vectorDims)
}

// ─── LedgerStore implementation ───────────────────────────────────────────────

var _ persist.LedgerStore = (*Store)(nil)

func (s *Store) AppendClaim(claim persist.Claim, now time.Time) (int64, error) {
	ctx := context.Background()
	qualifiers, _ := json.Marshal(claim.Qualifiers)
	evidenceRefs, _ := json.Marshal(claim.EvidenceRefs)

	var seq int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO claims (id, entity, predicate, object, observation_date, confidence,
			derivation, claim_type, canon_class, qualifiers, visibility, state,
			source_trace_ref, evidence_refs, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (id) DO UPDATE SET entity = claims.entity
		RETURNING seq`,
		claim.ID, claim.Entity, claim.Predicate, claim.Object, claim.ObservationDate,
		claim.Confidence, claim.Derivation, claim.ClaimType, claim.CanonClass,
		qualifiers, claim.Visibility, claim.State, claim.SourceTraceRef,
		evidenceRefs, now,
	).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("persist/pg: append claim: %w", err)
	}
	return seq, nil
}

func (s *Store) AppendEdge(edge persist.Edge, now time.Time) error {
	ctx := context.Background()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO edges (from_id, to_id, kind, claim_id, seq, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT ON CONSTRAINT edges_natural_key DO NOTHING`,
		edge.From, edge.To, edge.Kind, edge.ClaimID, edge.Seq, now,
	)
	if err != nil {
		return fmt.Errorf("persist/pg: append edge: %w", err)
	}
	return nil
}

func (s *Store) FoldClaims(filter persist.ClaimFilter) ([]persist.Claim, error) {
	ctx := context.Background()
	query := `SELECT seq, id, entity, predicate, object, observation_date, confidence,
		derivation, claim_type, canon_class, qualifiers, visibility, state,
		source_trace_ref, evidence_refs, created_at FROM claims WHERE 1=1`
	args := []any{}
	argN := 1

	if filter.Entity != "" {
		query += fmt.Sprintf(" AND entity = $%d", argN)
		args = append(args, filter.Entity)
		argN++
	}
	if filter.State != "" {
		query += fmt.Sprintf(" AND state = $%d", argN)
		args = append(args, filter.State)
		argN++
	}
	if len(filter.Visibility) > 0 {
		query += fmt.Sprintf(" AND visibility = ANY($%d)", argN)
		args = append(args, filter.Visibility)
		argN++
	}
	if filter.SinceSeq > 0 {
		query += fmt.Sprintf(" AND seq > $%d", argN)
		args = append(args, filter.SinceSeq)
		argN++
	}

	query += " ORDER BY seq ASC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argN)
		args = append(args, filter.Limit)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("persist/pg: fold claims: %w", err)
	}
	defer rows.Close()

	var claims []persist.Claim
	for rows.Next() {
		var c persist.Claim
		var qualifiers, evidenceRefs []byte
		var createdAt time.Time
		if err := rows.Scan(&c.Seq, &c.ID, &c.Entity, &c.Predicate, &c.Object,
			&c.ObservationDate, &c.Confidence, &c.Derivation, &c.ClaimType,
			&c.CanonClass, &qualifiers, &c.Visibility, &c.State,
			&c.SourceTraceRef, &evidenceRefs, &createdAt); err != nil {
			return nil, fmt.Errorf("persist/pg: scan claim: %w", err)
		}
		json.Unmarshal(qualifiers, &c.Qualifiers)
		json.Unmarshal(evidenceRefs, &c.EvidenceRefs)
		c.CreatedAt = createdAt.Format(time.RFC3339)
		claims = append(claims, c)
	}
	return claims, rows.Err()
}

func (s *Store) CurrentPosition(entity, topic string) (*persist.Position, error) {
	claims, err := s.FoldClaims(persist.ClaimFilter{
		Entity:     entity,
		Visibility: []string{"ambient", "contextual", "operational"},
		State:      "promoted",
	})
	if err != nil {
		return nil, err
	}
	// Fold: last claim per predicate wins (MVCC-by-fold)
	byPredicate := map[string]persist.Claim{}
	for _, c := range claims {
		byPredicate[c.Predicate] = c // last wins (ascending seq order)
	}
	if topic != "" {
		if c, ok := byPredicate[topic]; ok {
			return &persist.Position{
				Entity:    entity,
				Topic:     topic,
				Claim:     c,
				Seq:       c.Seq,
				UpdatedAt: c.CreatedAt,
			}, nil
		}
		return nil, nil
	}
	// Return the highest-seq position
	var best *persist.Position
	for pred, c := range byPredicate {
		if best == nil || c.Seq > best.Seq {
			best = &persist.Position{
				Entity:    entity,
				Topic:     pred,
				Claim:     c,
				Seq:       c.Seq,
				UpdatedAt: c.CreatedAt,
			}
		}
	}
	return best, nil
}

func (s *Store) NextSeq() (int64, error) {
	ctx := context.Background()
	var seq int64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM claims`).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("persist/pg: next seq: %w", err)
	}
	return seq, nil
}

func (s *Store) AppendCandidate(cand persist.Candidate, now time.Time) error {
	ctx := context.Background()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO candidates (id, entity, predicate, object, confidence,
			derivation, visibility, state, source_trace_ref, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (id) DO NOTHING`,
		cand.ID, cand.Entity, cand.Predicate, cand.Object, cand.Confidence,
		cand.Derivation, cand.Visibility, cand.State, cand.SourceTraceRef, now,
	)
	if err != nil {
		return fmt.Errorf("persist/pg: append candidate: %w", err)
	}
	return nil
}

func (s *Store) UpdateCandidate(id string, mutate func(*persist.Candidate) error) error {
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("persist/pg: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var cand persist.Candidate
	var createdAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT id, entity, predicate, object, confidence, derivation,
			visibility, state, source_trace_ref, created_at
		FROM candidates WHERE id = $1 FOR UPDATE`, id,
	).Scan(&cand.ID, &cand.Entity, &cand.Predicate, &cand.Object,
		&cand.Confidence, &cand.Derivation, &cand.Visibility, &cand.State,
		&cand.SourceTraceRef, &createdAt)
	if err != nil {
		return fmt.Errorf("persist/pg: get candidate for update: %w", err)
	}

	if err := mutate(&cand); err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		UPDATE candidates SET visibility=$1, state=$2 WHERE id=$3`,
		cand.Visibility, cand.State, cand.ID,
	)
	if err != nil {
		return fmt.Errorf("persist/pg: update candidate: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *Store) ListCandidates(filter persist.CandidateFilter) ([]persist.Candidate, error) {
	ctx := context.Background()
	query := `SELECT id, entity, predicate, object, confidence, derivation,
		visibility, state, source_trace_ref, created_at FROM candidates WHERE 1=1`
	args := []any{}
	argN := 1

	if filter.State != "" {
		query += fmt.Sprintf(" AND state = $%d", argN)
		args = append(args, filter.State)
		argN++
	}
	if filter.Entity != "" {
		query += fmt.Sprintf(" AND entity = $%d", argN)
		args = append(args, filter.Entity)
		argN++
	}
	query += " ORDER BY created_at DESC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argN)
		args = append(args, filter.Limit)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("persist/pg: list candidates: %w", err)
	}
	defer rows.Close()

	var cands []persist.Candidate
	for rows.Next() {
		var c persist.Candidate
		var createdAt time.Time
		if err := rows.Scan(&c.ID, &c.Entity, &c.Predicate, &c.Object,
			&c.Confidence, &c.Derivation, &c.Visibility, &c.State,
			&c.SourceTraceRef, &createdAt); err != nil {
			return nil, fmt.Errorf("persist/pg: scan candidate: %w", err)
		}
		c.CreatedAt = createdAt.Format(time.RFC3339)
		cands = append(cands, c)
	}
	return cands, rows.Err()
}

func (s *Store) GetCandidate(id string) (*persist.Candidate, error) {
	ctx := context.Background()
	var c persist.Candidate
	var createdAt time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT id, entity, predicate, object, confidence, derivation,
			visibility, state, source_trace_ref, created_at
		FROM candidates WHERE id = $1`, id,
	).Scan(&c.ID, &c.Entity, &c.Predicate, &c.Object, &c.Confidence,
		&c.Derivation, &c.Visibility, &c.State, &c.SourceTraceRef, &createdAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("persist/pg: get candidate: %w", err)
	}
	c.CreatedAt = createdAt.Format(time.RFC3339)
	return &c, nil
}

// ─── EventStore implementation ────────────────────────────────────────────────

var _ persist.EventStore = (*Store)(nil)

func (s *Store) PutJSON(id string, value any) error {
	ctx := context.Background()
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("persist/pg: marshal event: %w", err)
	}
	// Idempotent on same bytes; conflict on same-ID-different-bytes.
	// An event store must never silently overwrite history.
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO events (id, payload, created_at) VALUES ($1, $2, now())
		ON CONFLICT (id) DO NOTHING`, id, payload)
	if err != nil {
		return fmt.Errorf("persist/pg: put event: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil // inserted
	}
	var existing []byte
	if err := s.pool.QueryRow(ctx, `SELECT payload FROM events WHERE id = $1`, id).Scan(&existing); err != nil {
		return fmt.Errorf("persist/pg: conflict check: %w", err)
	}
	if !bytes.Equal(existing, payload) {
		return fmt.Errorf("persist/pg: event conflict: id %s exists with different payload", id)
	}
	return nil // idempotent: same id, same bytes
}

func (s *Store) PutJSONIfAbsent(id string, value any) (bool, error) {
	ctx := context.Background()
	payload, err := json.Marshal(value)
	if err != nil {
		return false, fmt.Errorf("persist/pg: marshal event: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO events (id, payload, created_at) VALUES ($1, $2, now())
		ON CONFLICT (id) DO NOTHING`, id, payload)
	if err != nil {
		return false, fmt.Errorf("persist/pg: put if absent: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return true, nil // created
	}
	// Already exists — detect same-ID-different-bytes corruption.
	var existing []byte
	if err := s.pool.QueryRow(ctx, `SELECT payload FROM events WHERE id = $1`, id).Scan(&existing); err != nil {
		return false, fmt.Errorf("persist/pg: conflict check: %w", err)
	}
	if !bytes.Equal(existing, payload) {
		return false, fmt.Errorf("persist/pg: event conflict: id %s exists with different payload", id)
	}
	return false, nil // already present, same bytes
}

func (s *Store) Read(ref string) (*persist.EventRecord, error) {
	ctx := context.Background()
	var rec persist.EventRecord
	var payload []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, kind, payload, created_at FROM events WHERE id = $1`, ref,
	).Scan(&rec.ID, &rec.Kind, &payload, &rec.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("persist/pg: read event: %w", err)
	}
	rec.Bytes = payload
	return &rec, nil
}

func (s *Store) Scan(opts persist.EventScanOptions) ([]persist.EventRecord, error) {
	ctx := context.Background()
	query := `SELECT id, kind, payload, created_at FROM events WHERE 1=1`
	args := []any{}
	argN := 1

	if len(opts.Prefixes) > 0 {
		conditions := ""
		for i, p := range opts.Prefixes {
			if i > 0 {
				conditions += " OR"
			}
			conditions += fmt.Sprintf(" id LIKE $%d", argN)
			args = append(args, p+"%")
			argN++
		}
		query += " AND (" + conditions + ")"
	}
	query += " ORDER BY created_at ASC"
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argN)
		args = append(args, opts.Limit)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("persist/pg: scan events: %w", err)
	}
	defer rows.Close()

	var recs []persist.EventRecord
	for rows.Next() {
		var rec persist.EventRecord
		var payload []byte
		if err := rows.Scan(&rec.ID, &rec.Kind, &payload, &rec.CreatedAt); err != nil {
			return nil, fmt.Errorf("persist/pg: scan event: %w", err)
		}
		rec.Bytes = payload
		recs = append(recs, rec)
	}
	return recs, rows.Err()
}

func (s *Store) AppendWorkLog(data []byte) error {
	ctx := context.Background()
	_, err := s.pool.Exec(ctx, `INSERT INTO work_log (data) VALUES ($1)`, data)
	if err != nil {
		return fmt.Errorf("persist/pg: append work log: %w", err)
	}
	return nil
}

func (s *Store) ReadWorkLog() ([]byte, error) {
	ctx := context.Background()
	rows, err := s.pool.Query(ctx, `SELECT data FROM work_log ORDER BY seq ASC`)
	if err != nil {
		return nil, fmt.Errorf("persist/pg: read work log: %w", err)
	}
	defer rows.Close()

	var buf []byte
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		buf = append(buf, data...)
		buf = append(buf, '\n')
	}
	return buf, rows.Err()
}

// ─── WorkStore implementation ─────────────────────────────────────────────────

var _ persist.WorkStore = (*Store)(nil)

func (s *Store) PutWork(env envelope.WorkEnvelope) error {
	ctx := context.Background()
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("persist/pg: marshal work: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO work_envelopes (work_id, state, owner, payload, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (work_id) DO UPDATE SET state=$2, owner=$3, payload=$4, updated_at=now()`,
		env.IDs.WorkID, env.State, env.Owner, payload)
	if err != nil {
		return fmt.Errorf("persist/pg: put work: %w", err)
	}
	return nil
}

func (s *Store) GetWork(workID string) (*envelope.WorkEnvelope, error) {
	ctx := context.Background()
	var payload []byte
	err := s.pool.QueryRow(ctx, `
		SELECT payload FROM work_envelopes WHERE work_id = $1`, workID,
	).Scan(&payload)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("persist/pg: get work: %w", err)
	}
	var env envelope.WorkEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, fmt.Errorf("persist/pg: unmarshal work: %w", err)
	}
	return &env, nil
}

func (s *Store) ListWork() ([]envelope.WorkEnvelope, error) {
	return s.listWork(`WHERE state NOT IN ('completed','failed','cancelled')`)
}

func (s *Store) ListAllWork() ([]envelope.WorkEnvelope, error) {
	return s.listWork("")
}

func (s *Store) listWork(where string) ([]envelope.WorkEnvelope, error) {
	ctx := context.Background()
	rows, err := s.pool.Query(ctx,
		`SELECT payload FROM work_envelopes `+where+` ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("persist/pg: list work: %w", err)
	}
	defer rows.Close()

	var envs []envelope.WorkEnvelope
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var env envelope.WorkEnvelope
		if err := json.Unmarshal(payload, &env); err != nil {
			return nil, err
		}
		envs = append(envs, env)
	}
	return envs, rows.Err()
}

// ─── VectorStore implementation (pgvector) ────────────────────────────────────

var _ persist.VectorStore = (*Store)(nil)

func (s *Store) Upsert(ref string, embedding []float32, metadata map[string]string) error {
	ctx := context.Background()
	meta, _ := json.Marshal(metadata)
	vec := pgvector.NewVector(embedding)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO vectors (ref, embedding, metadata, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (ref) DO UPDATE SET embedding=$2, metadata=$3, updated_at=now()`,
		ref, vec, meta)
	if err != nil {
		return fmt.Errorf("persist/pg: upsert vector: %w", err)
	}
	return nil
}

func (s *Store) Search(queryVec []float32, k int, filter map[string]string) ([]persist.VectorHit, error) {
	ctx := context.Background()
	vec := pgvector.NewVector(queryVec)

	query := `SELECT ref, 1 - (embedding <=> $1) AS score, metadata
		FROM vectors ORDER BY embedding <=> $1 LIMIT $2`
	args := []any{vec, k}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("persist/pg: vector search: %w", err)
	}
	defer rows.Close()

	var hits []persist.VectorHit
	for rows.Next() {
		var h persist.VectorHit
		var meta []byte
		if err := rows.Scan(&h.Ref, &h.Score, &meta); err != nil {
			return nil, fmt.Errorf("persist/pg: scan vector hit: %w", err)
		}
		json.Unmarshal(meta, &h.Metadata)
		// Apply metadata filter client-side (simple equality)
		if len(filter) > 0 {
			match := true
			for k, v := range filter {
				if h.Metadata[k] != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

func (s *Store) Delete(ref string) error {
	ctx := context.Background()
	_, err := s.pool.Exec(ctx, `DELETE FROM vectors WHERE ref = $1`, ref)
	if err != nil {
		return fmt.Errorf("persist/pg: delete vector: %w", err)
	}
	return nil
}

func (s *Store) Count() (int64, error) {
	ctx := context.Background()
	var count int64
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM vectors`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("persist/pg: count vectors: %w", err)
	}
	return count, nil
}

// VectorRefs returns the set of all refs that already have vectors stored.
// Used by incremental embed to skip claims that are already embedded.
func (s *Store) VectorRefs() (map[string]bool, error) {
	ctx := context.Background()
	rows, err := s.pool.Query(ctx, `SELECT ref FROM vectors`)
	if err != nil {
		return nil, fmt.Errorf("persist/pg: list vector refs: %w", err)
	}
	defer rows.Close()
	refs := make(map[string]bool)
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, fmt.Errorf("persist/pg: scan vector ref: %w", err)
		}
		refs[ref] = true
	}
	return refs, rows.Err()
}

// ─── Diagnostics ─────────────────────────────────────────────────────────────────

// TableCounts returns row counts for all store tables. Used by `compound store status`.
func (s *Store) TableCounts(ctx context.Context) map[string]int64 {
	tables := []string{"claims", "edges", "candidates", "events", "work_log", "work_envelopes", "vectors"}
	counts := make(map[string]int64, len(tables))
	for _, t := range tables {
		var n int64
		err := s.pool.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", t)).Scan(&n)
		if err == nil {
			counts[t] = n
		}
	}
	return counts
}

// PoolStats exposes connection pool diagnostics for operator surfaces.
func (s *Store) PoolStats() pgxpoolStats {
	stat := s.pool.Stat()
	return pgxpoolStats{
		AcquireCount:      stat.AcquireCount(),
		TotalConns:        stat.TotalConns(),
		IdleConns:         stat.IdleConns(),
		MaxConns:          stat.MaxConns(),
		ConstructingConns: stat.ConstructingConns(),
	}
}

type pgxpoolStats struct {
	AcquireCount      int64 `json:"acquire_count"`
	TotalConns        int32 `json:"total_conns"`
	IdleConns         int32 `json:"idle_conns"`
	MaxConns          int32 `json:"max_conns"`
	ConstructingConns int32 `json:"constructing_conns"`
}
