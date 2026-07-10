package mem

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Engine errors are EXPLICIT and distinct so a caller can tell apart "this engine has no
// breadth" (degrade to ledger), "the engine process is down" (degrade to ledger, say so), and
// "the engine's embedder does not match what we indexed with" (HARD refuse — a silent model
// swap degrades recall invisibly). None of these is ever a silent nil.
var (
	ErrUnsupported       = errors.New("capability not supported by this engine")
	ErrEngineUnavailable = errors.New("breadth engine unavailable (transport down) — ledger answers exact/as-of")
	ErrEmbedderMismatch  = errors.New("engine embedder does not match the indexed embedder (refusing breadth, not degrading)")
	ErrProtocolVersion   = errors.New("engine protocol version skew (refusing breadth, not degrading)")
	ErrEngineAhead       = errors.New("breadth engine generation is ahead of owned ledger (refusing backward rebuild)")
)

// Gate decouples the visibility decision from the allowlist CONTENTS. GateList honors the
// allowlist and an EMPTY allowlist matches NOTHING (fail-closed); GateAll is admin/debug only
// and the core never emits it.
const (
	GateList = "list"
	GateAll  = "all"
)

// EngineCapabilities advertises what an engine can do so callers adapt instead of assuming.
type EngineCapabilities struct {
	Name      string `json:"name"`
	Breadth   bool   `json:"breadth"`    // semantic/lexical Query over many claims
	Embedder  string `json:"embedder"`   // "<provider:model:dim:bits>" — for the identity guard; "" = none
	CorpusGen int64  `json:"corpus_gen"` // the generation this engine is indexed at (ledger maxSeq)
}

// Hit is a breadth-query result: a claim id + score. The ledger resolves the id to the claim;
// the engine holds NO authority, only an index of claim ids.
type Hit struct {
	ClaimID string  `json:"claim_id"`
	Score   float64 `json:"score"`
}

// Engine is the RENTED breadth index. Recall (exact / as-of) stays the ledger's job; the engine
// only answers breadth Query, honoring the ledger-computed allowlist (the relevance gate). Index
// is idempotent on the corpus generation, so an out-of-process engine never re-embeds per query.
// This is the P6 seam the turbovec sidecar plugs into (behind ProtocolEngine); the legacy
// markdown engine (internal/engine) retires with the legacy memory object model.
type Engine interface {
	Capabilities() EngineCapabilities
	Index(claims []Claim, gen int64) error
	Query(text, gate string, allowlist []string, k int) ([]Hit, error)
}

type contextIndexEngine interface {
	IndexWithContext(claims []Claim, edges []WireEdge, gen int64) error
}

// NoEngine is the default: no breadth. Its ops return ErrUnsupported EXPLICITLY — never nil,nil —
// so a missing index cannot masquerade as an empty result.
type NoEngine struct{}

func (NoEngine) Capabilities() EngineCapabilities                   { return EngineCapabilities{Name: "none"} }
func (NoEngine) Index([]Claim, int64) error                         { return ErrUnsupported }
func (NoEngine) Query(string, string, []string, int) ([]Hit, error) { return nil, ErrUnsupported }

// LexicalEngine is a dependency-free, in-process breadth engine: case-insensitive token match
// over claim text, honoring the allowlist. It proves the seam end-to-end with real results and
// is the recall body the pure-Go mock sidecar reuses behind the wire. A semantic ANN engine
// (turbovec) is a later swap behind this same interface, via ProtocolEngine.
type LexicalEngine struct {
	claims []Claim
	gen    int64
}

func (e *LexicalEngine) Capabilities() EngineCapabilities {
	return EngineCapabilities{Name: "lexical", Breadth: true, CorpusGen: e.gen}
}

// Index is idempotent on gen: re-indexing the same generation is a no-op (no re-embed storm).
func (e *LexicalEngine) Index(claims []Claim, gen int64) error {
	if e.claims != nil && gen == e.gen {
		return nil
	}
	e.claims, e.gen = claims, gen
	return nil
}

func (e *LexicalEngine) Query(text, gate string, allowlist []string, k int) ([]Hit, error) {
	allow := map[string]bool{}
	for _, id := range allowlist {
		allow[id] = true
	}
	terms := strings.Fields(strings.ToLower(text))
	var hits []Hit
	for _, c := range e.claims {
		// GateList: an empty allowlist matches NOTHING (fail-closed). GateAll skips the gate.
		if gate != GateAll && !allow[c.ID] {
			continue
		}
		hay := strings.ToLower(c.Entity + " " + c.Predicate + " " + c.Object)
		score := 0.0
		for _, t := range terms {
			if strings.Contains(hay, t) {
				score++
			}
		}
		if score > 0 {
			hits = append(hits, Hit{ClaimID: c.ID, Score: score})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if k > 0 && len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

// QueryBreadth answers a breadth query via the RENTED engine, relevance-gated: the LEDGER
// computes the allowlist (current, non-retired claim ids) and passes it under GateList, so the
// gate is honored at the engine (cheap), not as a post-filter. It re-Indexes ONLY when the
// engine's generation lags the ledger's (no re-embed per query). Returns ErrUnsupported /
// ErrEngineUnavailable / ErrEmbedderMismatch explicitly — callers degrade or refuse, never
// silently empty.
func (s *Store) QueryBreadth(text string, k int, eng Engine) ([]Claim, error) {
	if !eng.Capabilities().Breadth {
		return nil, ErrUnsupported
	}
	cur, err := s.currentVisible()
	if err != nil {
		return nil, err
	}
	gen := s.currentGen()
	engineGen := eng.Capabilities().CorpusGen
	if engineGen > gen {
		return nil, fmt.Errorf("%w: engine_gen=%d ledger_gen=%d", ErrEngineAhead, engineGen, gen)
	}
	if engineGen < gen {
		if ceng, ok := eng.(contextIndexEngine); ok {
			edges, err := s.visibleWireEdges(cur, 0.5)
			if err != nil {
				return nil, err
			}
			if err := ceng.IndexWithContext(cur, edges, gen); err != nil {
				return nil, err
			}
		} else if err := eng.Index(cur, gen); err != nil {
			return nil, err
		}
	}
	allow := make([]string, 0, len(cur))
	byID := make(map[string]Claim, len(cur))
	for _, c := range cur {
		allow = append(allow, c.ID)
		byID[c.ID] = c
	}
	// E1 deterministic query expansion: widen the QUERY only (never the gate).
	// Absent/empty registry → original text only (fail-open, byte-identical path).
	reg, _ := LoadRecallAliases(s.home)
	expanded := ExpandQuery(text, reg)
	fetchK := k
	if fetchK > 0 && expanded.Active {
		// Pull a wider pool so discounted expansion hits can still fill top-k.
		fetchK = k * 2
	}
	hits, err := eng.Query(text, GateList, allow, fetchK)
	if err != nil {
		return nil, err
	}
	if expanded.Active && len(expanded.Added) > 0 {
		expHits, expErr := eng.Query(strings.Join(expanded.Added, " "), GateList, allow, fetchK)
		if expErr != nil {
			return nil, expErr
		}
		hits = mergeHitsWithExpansionDiscount(hits, expHits, k)
	} else if k > 0 && len(hits) > k {
		hits = hits[:k]
	}
	out := make([]Claim, 0, len(hits))
	for _, h := range hits {
		if c, ok := byID[h.ClaimID]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// currentGen is ledger generation = max seq across claims and relation edges.
func (s *Store) currentGen() int64 {
	gen, err := s.maxLedgerSeq()
	if err != nil {
		return 0
	}
	return gen
}

func (s *Store) visibleWireEdges(cur []Claim, minConf float64) ([]WireEdge, error) {
	visibleEntities := make(map[string]bool, len(cur))
	for _, c := range cur {
		visibleEntities[c.Entity] = true
	}
	rels, err := s.loadRelations(minConf)
	if err != nil {
		return nil, err
	}
	out := make([]WireEdge, 0, len(rels))
	for _, e := range rels {
		if !visibleEntities[e.From] || !visibleEntities[e.To] {
			continue
		}
		out = append(out, WireEdge{From: e.From, Kind: e.Kind, To: e.To, Confidence: e.Confidence})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].To < out[j].To
	})
	return out, nil
}

// AllCurrent returns the current (non-superseded, non-retired) claims — the canon a distiller
// compares new extractions against (RetrievedPriorState). Public accessor over currentVisible.
func (s *Store) AllCurrent() ([]Claim, error) { return s.currentVisible() }

// LedgerClaims returns every immutable claim row in the append-only ledger,
// including superseded history. Authority proofs use it when the existence of
// an earned promotion matters independently of current recall visibility.
func (s *Store) LedgerClaims() ([]Claim, error) { return s.loadClaimsRaw() }

// AllCurrentOperationalDecisions returns the runtime decision-control subset
// with its own protected supersession fold. Only another stamped/legacy
// operator decision may hide one of these claims; an ordinary memory edge,
// review mark, or same-slot shadow cannot erase grant/freeze/revocation
// authority before its consumer validates it.
func (s *Store) AllCurrentOperationalDecisions() ([]Claim, error) {
	claims, err := s.loadClaimsRaw()
	if err != nil {
		return nil, err
	}
	protected := map[string]bool{}
	for _, claim := range claims {
		if protectedOperationalDecisionClaim(claim) {
			protected[claim.ID] = true
		}
	}
	superseded := map[string]bool{}
	err = readJSONL(s.edgesLog(), func(line []byte) error {
		var edge Edge
		if err := json.Unmarshal(line, &edge); err != nil {
			return err
		}
		if edge.Kind == EdgeKindSupersedes && protected[edge.From] && protected[edge.To] {
			superseded[edge.To] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, claim := range claims {
		if protected[claim.ID] && protected[claim.Supersedes] {
			superseded[claim.Supersedes] = true
		}
	}
	var out []Claim
	for _, claim := range claims {
		if !protected[claim.ID] || claim.Visibility == "retired" || superseded[claim.ID] {
			continue
		}
		claim.State = ClaimStatePromoted
		claim.ReviewRefs = nil
		claim.ConfidenceDelta = 0
		out = append(out, claim)
	}
	return out, nil
}

// currentVisible returns the current (non-superseded as of now), non-retired claims — the set
// an engine is allowed to surface (the relevance gate, computed by the ledger).
func (s *Store) currentVisible() ([]Claim, error) {
	claims, err := s.loadClaims()
	if err != nil {
		return nil, err
	}
	superseded, err := s.supersededAsOf(s.maxSeq(claims))
	if err != nil {
		return nil, err
	}
	var out []Claim
	for _, c := range claims {
		if c.Visibility == "retired" || c.State == ClaimStateNeedsReview || superseded[c.ID] {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}
