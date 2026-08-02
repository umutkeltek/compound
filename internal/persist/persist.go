// Package persist is the storage abstraction layer — the logical seam between
// Compound's domain objects and their physical persistence. It defines interfaces
// that the existing file-based stores satisfy implicitly, and that the
// Postgres+pgvector backend (internal/persist/pg) implements explicitly.
//
// Design constraints (ADR-0005):
//   - The file backend remains the DEFAULT. Postgres is opt-in via COMPOUND_STORE=postgres.
//   - This package is ADDITIVE: no existing import path or function signature changes.
//   - The interfaces reference existing domain types (envelope, mem) to avoid mapping overhead.
//   - No import cycle: persist imports envelope/mem; they never import persist.
//
// Classification: leaf (shared infrastructure seam; owns no authority object).
package persist

import (
	"fmt"
	"os"
	"time"

	"compound/internal/envelope"
)

// ─── Ledger ───────────────────────────────────────────────────────────────────

// Claim is the durable, immutable assertion in the ledger (mirrors mem.Claim shape).
// The pg backend stores these in a claims table with MVCC-by-fold semantics.
type Claim struct {
	Seq             int64             `json:"seq"`
	ID              string            `json:"id"`
	Entity          string            `json:"entity"`
	Predicate       string            `json:"predicate"`
	Object          string            `json:"object"`
	ObservationDate string            `json:"observation_date"`
	Confidence      float64           `json:"confidence"`
	Derivation      string            `json:"derivation"`
	ClaimType       string            `json:"claim_type,omitempty"`
	CanonClass      string            `json:"canon_class,omitempty"`
	Qualifiers      map[string]string `json:"qualifiers,omitempty"`
	Visibility      string            `json:"visibility"`
	State           string            `json:"state"`
	SourceTraceRef  string            `json:"source_trace_ref"`
	EvidenceRefs    []string          `json:"evidence_refs,omitempty"`
	CreatedAt       string            `json:"created_at"`
}

// Edge is a version-chain relationship between claims (supersedes, reinforces, etc.).
type Edge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Kind  string `json:"kind"`
	Seq   int64  `json:"seq"`
	ClaimID string `json:"claim_id"`
}

// Candidate is an untrusted proposed fact awaiting adjudication (mirrors mem.Candidate).
type Candidate struct {
	ID             string  `json:"id"`
	Entity         string  `json:"entity"`
	Predicate      string  `json:"predicate"`
	Object         string  `json:"object"`
	Confidence     float64 `json:"confidence"`
	Derivation     string  `json:"derivation"`
	Visibility     string  `json:"visibility"`
	State          string  `json:"state"`
	SourceTraceRef string  `json:"source_trace_ref"`
	CreatedAt      string  `json:"created_at"`
}

// ClaimFilter narrows a fold query.
type ClaimFilter struct {
	Entity     string
	Topic      string
	Visibility []string
	State      string
	SinceSeq   int64
	Limit      int
}

// CandidateFilter narrows a candidate list query.
type CandidateFilter struct {
	State      string
	Entity     string
	Visibility string
	Limit      int
}

// Position is the current stance for an entity+topic, derived by folding claims.
type Position struct {
	Entity    string  `json:"entity"`
	Topic     string  `json:"topic"`
	Claim     Claim   `json:"claim"`
	Seq       int64   `json:"seq"`
	UpdatedAt string  `json:"updated_at"`
}

// LedgerStore is the claim/candidate persistence surface.
// The file-based mem.Store satisfies this through its existing methods;
// the pg backend implements it with SQL + MVCC.
type LedgerStore interface {
	// AppendClaim adds an immutable claim to the log and returns its sequence number.
	AppendClaim(claim Claim, now time.Time) (seq int64, err error)
	// AppendEdge records a version-chain edge (supersedes, reinforces, etc.).
	AppendEdge(edge Edge, now time.Time) error
	// FoldClaims returns claims matching the filter, folded to current state.
	FoldClaims(filter ClaimFilter) ([]Claim, error)
	// CurrentPosition derives the current stance for entity+topic by folding.
	CurrentPosition(entity, topic string) (*Position, error)
	// NextSeq returns the next monotonic sequence number.
	NextSeq() (int64, error)

	// AppendCandidate writes a new candidate to the inbox.
	AppendCandidate(cand Candidate, now time.Time) error
	// UpdateCandidate mutates a candidate's state (adjudication).
	UpdateCandidate(id string, mutate func(*Candidate) error) error
	// ListCandidates returns candidates matching the filter.
	ListCandidates(filter CandidateFilter) ([]Candidate, error)
	// GetCandidate returns one candidate by ID.
	GetCandidate(id string) (*Candidate, error)
}

// ─── Events ───────────────────────────────────────────────────────────────────

// EventRecord is one event in the append-only log.
type EventRecord struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Bytes     []byte          `json:"-"`
	CreatedAt time.Time       `json:"created_at"`
}

// EventScanOptions controls a scan over the event log.
type EventScanOptions struct {
	Prefixes    []string
	RegularOnly bool
	Limit       int
}

// EventStore is the append-only event persistence surface.
// The file-based eventstore.Store satisfies this through its existing methods;
// the pg backend implements it with an events table + append-only inserts.
type EventStore interface {
	// PutJSON writes a JSON record, creating or overwriting.
	PutJSON(id string, value any) error
	// PutJSONIfAbsent writes only if the ID does not exist.
	PutJSONIfAbsent(id string, value any) (created bool, err error)
	// Read returns one event by reference.
	Read(ref string) (*EventRecord, error)
	// Scan returns events matching the options.
	Scan(opts EventScanOptions) ([]EventRecord, error)
	// AppendWorkLog appends a line to the work event stream.
	AppendWorkLog(data []byte) error
	// ReadWorkLog returns the full work event stream.
	ReadWorkLog() ([]byte, error)
}

// ─── Work ─────────────────────────────────────────────────────────────────────

// WorkStore is the work envelope persistence surface.
// The file-based work.Store satisfies this through its existing methods;
// the pg backend implements it with a work_envelopes table.
type WorkStore interface {
	// PutWork writes or overwrites a work envelope.
	PutWork(env envelope.WorkEnvelope) error
	// GetWork returns one envelope by work_id.
	GetWork(workID string) (*envelope.WorkEnvelope, error)
	// ListWork returns all non-terminal work envelopes.
	ListWork() ([]envelope.WorkEnvelope, error)
	// ListAllWork returns all envelopes including terminal/archived.
	ListAllWork() ([]envelope.WorkEnvelope, error)
}

// ─── Vectors (pgvector) ───────────────────────────────────────────────────────

// VectorHit is one result from a similarity search.
type VectorHit struct {
	Ref      string            `json:"ref"`
	Score    float64           `json:"score"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// VectorStore is the semantic recall surface backed by pgvector.
// This has NO file-based equivalent — it is new capability that replaces
// the O(n) directory scan in memory recall with indexed similarity search.
type VectorStore interface {
	// Upsert inserts or updates an embedding for a reference.
	Upsert(ref string, embedding []float32, metadata map[string]string) error
	// Search returns the k nearest neighbors to the query vector.
	Search(queryVec []float32, k int, filter map[string]string) ([]VectorHit, error)
	// Delete removes an embedding by reference.
	Delete(ref string) error
	// Count returns the number of stored embeddings.
	Count() (int64, error)
}

// ─── Backend selection ────────────────────────────────────────────────────────

// Backend names the active storage engine.
type Backend string

const (
	// BackendFile is the default JSON-file-on-disk storage (current behavior).
	BackendFile Backend = "file"
	// BackendPostgres is the Postgres+pgvector storage (opt-in via COMPOUND_STORE).
	BackendPostgres Backend = "postgres"
)

// Config holds the storage backend selection and connection parameters.
type Config struct {
	// Backend selects the storage engine. Default: BackendFile.
	Backend Backend
	// DSN is the Postgres connection string (required when Backend=postgres).
	// Example: "postgres://compound:secret@192.168.1.10:5432/compound?sslmode=disable"
	DSN string
	// VectorDimensions is the embedding dimension for pgvector columns.
	// Must match the embedding model output. Default: 1536 (OpenAI ada-002).
	VectorDimensions int
}

// DefaultConfig returns the file backend (zero-change default).
func DefaultConfig() Config {
	return Config{
		Backend:          BackendFile,
		VectorDimensions: 1536,
	}
}

// ConfigFromEnv reads COMPOUND_STORE and COMPOUND_DSN from the environment.
// Returns DefaultConfig() when COMPOUND_STORE is unset or "file".
func ConfigFromEnv() Config {
	cfg := DefaultConfig()

	store := envOr("COMPOUND_STORE", "file")
	switch Backend(store) {
	case BackendPostgres:
		cfg.Backend = BackendPostgres
		cfg.DSN = envOr("COMPOUND_DSN", "")
	case BackendFile:
		// default, no change
	}

	if dims := envOr("COMPOUND_VECTOR_DIMS", ""); dims != "" {
		var d int
		if _, err := fmt.Sscanf(dims, "%d", &d); err == nil && d > 0 {
			cfg.VectorDimensions = d
		}
	}

	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
