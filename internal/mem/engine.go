package mem

import (
	"errors"
	"sort"
	"strings"
)

// ErrUnsupported is returned by an engine for a capability it does not implement. It is
// EXPLICIT on purpose: a silent no-op hides a missing index as "no results", which is the
// opposite of accurate. (The legacy markdown engine returned nil,nil; this seam does not.)
var ErrUnsupported = errors.New("capability not supported by this engine")

// EngineCapabilities advertises what an engine can do so callers adapt instead of assuming.
type EngineCapabilities struct {
	Name     string `json:"name"`
	Breadth  bool   `json:"breadth"`  // semantic/lexical Query over many claims
	Embedder string `json:"embedder"` // "<provider:model:dims>" — for the identity guard; "" = none
}

// Hit is a breadth-query result: a claim id + score. The ledger resolves the id to the claim;
// the engine holds NO authority, only an index of claim ids.
type Hit struct {
	ClaimID string  `json:"claim_id"`
	Score   float64 `json:"score"`
}

// Engine is the RENTED breadth index. Recall (exact / as-of) stays the ledger's job; the engine
// only answers breadth Query, and honors the ledger-computed allowlist (the relevance gate) so a
// hard filter stays cheap at the engine rather than a recall-losing post-filter (cf. turbovec's
// kernel allowlist). This is the P6 replacement for the legacy markdown engine (internal/engine),
// which retires with the legacy memory object model.
type Engine interface {
	Capabilities() EngineCapabilities
	Index(claims []Claim) error
	Query(text string, allowlist []string, k int) ([]Hit, error)
}

// NoEngine is the default: no breadth. Its ops return ErrUnsupported EXPLICITLY — never nil,nil —
// so a missing index cannot masquerade as an empty result.
type NoEngine struct{}

func (NoEngine) Capabilities() EngineCapabilities          { return EngineCapabilities{Name: "none"} }
func (NoEngine) Index([]Claim) error                       { return ErrUnsupported }
func (NoEngine) Query(string, []string, int) ([]Hit, error) { return nil, ErrUnsupported }

// LexicalEngine is a dependency-free, in-process breadth engine: case-insensitive token match
// over claim text, honoring the allowlist. It proves the rent-the-engine seam end-to-end with
// real results; a semantic ANN engine (turbovec) is a later swap behind this same interface.
type LexicalEngine struct{ claims []Claim }

func (e *LexicalEngine) Capabilities() EngineCapabilities {
	return EngineCapabilities{Name: "lexical", Breadth: true}
}
func (e *LexicalEngine) Index(claims []Claim) error {
	e.claims = claims
	return nil
}
func (e *LexicalEngine) Query(text string, allowlist []string, k int) ([]Hit, error) {
	allow := map[string]bool{}
	for _, id := range allowlist {
		allow[id] = true
	}
	terms := strings.Fields(strings.ToLower(text))
	var hits []Hit
	for _, c := range e.claims {
		if len(allowlist) > 0 && !allow[c.ID] {
			continue // honor the ledger's relevance gate
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
// computes the allowlist (current, non-retired claim ids) and passes it to the engine, so the
// gate is honored at the engine (cheap), not as a post-filter. Returns ErrUnsupported when the
// engine has no breadth capability — explicitly, so callers can say so plainly.
func (s *Store) QueryBreadth(text string, k int, eng Engine) ([]Claim, error) {
	if !eng.Capabilities().Breadth {
		return nil, ErrUnsupported
	}
	cur, err := s.currentVisible()
	if err != nil {
		return nil, err
	}
	if err := eng.Index(cur); err != nil {
		return nil, err
	}
	allow := make([]string, 0, len(cur))
	byID := make(map[string]Claim, len(cur))
	for _, c := range cur {
		allow = append(allow, c.ID)
		byID[c.ID] = c
	}
	hits, err := eng.Query(text, allow, k)
	if err != nil {
		return nil, err
	}
	out := make([]Claim, 0, len(hits))
	for _, h := range hits {
		if c, ok := byID[h.ClaimID]; ok {
			out = append(out, c)
		}
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
		if c.Visibility == "retired" || superseded[c.ID] {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}
