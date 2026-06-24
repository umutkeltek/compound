// Package engine is the RENTED layer: physical storage, vector/keyword indexes,
// and ranking sit behind MemoryEngine. The owned spine (object model, ledger,
// visibility, promotion, scope/query planning) never lives here, and visibility
// enforcement is NEVER delegated to an engine. Engines are disposable and
// rebuildable from the ledger. See docs/memory-architecture.md.
package engine

// Hit is a breadth-retrieval result.
type Hit struct {
	Score   float64
	Slug    string
	Snippet string
}

// Fact is an entity-anchored recall result.
type Fact struct {
	Claim  string
	Source string
}

// Capabilities lets the planner adapt to what an engine actually supports.
type Capabilities struct {
	Name    string
	Vector  bool
	Lexical bool
	Recall  bool
}

// MemoryEngine is the swappable storage/retrieval contract.
type MemoryEngine interface {
	Capabilities() Capabilities
	Query(q string, limit int) ([]Hit, error) // breadth: hybrid corpus
	Recall(entity string) ([]Fact, error)     // precision: entity-anchored
	Put(slug, markdown string) error          // ingest a page (system-of-record)
}
