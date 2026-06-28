package mem

// Task-shaped recall: instead of a flat top-k, seed-match entities, walk BOUNDED typed edges
// around the seeds, and return a budget-trimmed subgraph + a COVERAGE verdict — on a gap it names
// what is missing rather than fabricating (the pattern the graphify/codegraph audit converged on).
// Relate-edges are entity-level and confidence+provenance tagged; they coexist with the claim-level
// supersedes edges in the same append-only edges log (recall filters by kind).

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// RelationKinds are the entity-level edge kinds task-shaped recall traverses (claim-level
// "supersedes" is the version chain and is NOT a relation).
var RelationKinds = []string{"relates", "contradicts", "depends_on"}

// RelationHint is an extracted relation between two entities (the LLM proposes these; the
// reference extractor passes them through). Promoted into the ledger as a relate-edge.
type RelationHint struct {
	From       string  `json:"from"`
	To         string  `json:"to"`
	Kind       string  `json:"kind"`
	Confidence float64 `json:"confidence"`
}

// Extraction is what an Extractor turns a trace into: candidate facts + relation hints. The LLM
// extractor (free model via the gateway) produces this from raw text; StructuredExtractor parses
// an already-structured trace so the whole path is provable without a live model.
type Extraction struct {
	Candidates []Candidate    `json:"candidates"`
	Relations  []RelationHint `json:"relations"`
}

// Extractor turns a raw trace into an Extraction. Swappable: StructuredExtractor (deterministic,
// here) vs an LLM extractor (routed through the model gateway, free lane for extract — deferred).
type Extractor interface {
	Extract(trace []byte) (Extraction, error)
}

// StructuredExtractor parses a trace that is either a bare []Candidate (back-compat) or an
// {candidates, relations} object. It is the deterministic reference; the LLM extractor is the swap.
type StructuredExtractor struct{}

func (StructuredExtractor) Extract(trace []byte) (Extraction, error) {
	var ext Extraction
	if err := json.Unmarshal(trace, &ext); err == nil && (len(ext.Candidates) > 0 || len(ext.Relations) > 0) {
		return ext, nil
	}
	var cands []Candidate
	if err := json.Unmarshal(trace, &cands); err != nil {
		return Extraction{}, fmt.Errorf("trace is neither {candidates,relations} nor [candidates]: %w", err)
	}
	return Extraction{Candidates: cands}, nil
}

// Relate records an entity-level relation as a confidence+provenance-tagged edge. Relations are
// structural (not claims), so they are not candidate-adjudicated; recall confidence-gates them.
func (s *Store) Relate(from, kind, to string, conf float64, now time.Time) (Edge, error) {
	if !oneOf(kind, RelationKinds) {
		return Edge{}, fmt.Errorf("relation kind must be one of %v, got %q", RelationKinds, kind)
	}
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
		return Edge{}, fmt.Errorf("relate needs a from and a to entity")
	}
	var e Edge
	err := s.withLock(func() error {
		seq, err := s.nextSeq()
		if err != nil {
			return err
		}
		e = Edge{Seq: seq, Kind: kind, From: from, To: to, Confidence: conf, CreatedAt: rfc3339(now)}
		return appendJSONL(s.edgesLog(), &e)
	})
	return e, err
}

// loadRelations reads the entity-level relation edges (kind != supersedes), confidence-gated.
func (s *Store) loadRelations(minConf float64) ([]Edge, error) {
	var out []Edge
	err := readJSONL(s.edgesLog(), func(line []byte) error {
		var e Edge
		if err := json.Unmarshal(line, &e); err != nil {
			return err
		}
		if e.Kind != "supersedes" && e.Confidence >= minConf {
			out = append(out, e)
		}
		return nil
	})
	return out, err
}

// Subgraph is the result of task-shaped recall: the seed-matched entities, the bounded set of
// entities reached, their current claims, the relations among them, and a coverage verdict.
type Subgraph struct {
	Seed         string  `json:"seed"`
	SeedEntities []string `json:"seed_entities"`
	Entities     []string `json:"entities"`
	Claims       []Claim  `json:"claims"`
	Relations    []Edge   `json:"relations"`
	DepthReached int      `json:"depth_reached"`
	BudgetHit    bool     `json:"budget_hit"`
	Coverage     string   `json:"coverage"`
}

// RecallGraph does task-shaped recall: seed-match -> bounded typed-edge BFS (depth + entity
// budget) -> the connected subgraph of current claims + relations, plus a coverage verdict. It
// never fabricates: a seed that matches nothing says so.
func (s *Store) RecallGraph(seed string, depth, budget int, minConf float64) (Subgraph, error) {
	res := Subgraph{Seed: seed}
	cur, err := s.currentVisible()
	if err != nil {
		return res, err
	}
	res.SeedEntities = matchSeed(cur, seed)
	if len(res.SeedEntities) == 0 {
		res.Coverage = fmt.Sprintf("seed %q matched no entity — not fabricating", seed)
		return res, nil
	}
	rels, err := s.loadRelations(minConf)
	if err != nil {
		return res, err
	}

	// BFS over (undirected) relations from the seed entities, bounded by depth and entity budget.
	adj := map[string][]Edge{}
	for _, e := range rels {
		adj[e.From] = append(adj[e.From], e)
		adj[e.To] = append(adj[e.To], e)
	}
	seen := map[string]bool{}
	usedRel := map[string]Edge{}
	frontier := append([]string{}, res.SeedEntities...)
	for _, e := range frontier {
		seen[e] = true
	}
	for d := 0; d < depth && len(frontier) > 0; d++ {
		var next []string
		for _, ent := range frontier {
			for _, e := range adj[ent] {
				other := e.To
				if other == ent {
					other = e.From
				}
				usedRel[relKey(e)] = e
				if seen[other] {
					continue
				}
				if budget > 0 && len(seen) >= budget {
					res.BudgetHit = true
					continue
				}
				seen[other] = true
				next = append(next, other)
			}
		}
		if len(next) > 0 {
			res.DepthReached = d + 1
		}
		frontier = next
	}

	for e := range seen {
		res.Entities = append(res.Entities, e)
	}
	sort.Strings(res.Entities)
	entSet := seen
	for _, c := range cur {
		if entSet[c.Entity] {
			res.Claims = append(res.Claims, c)
		}
	}
	sort.Slice(res.Claims, func(i, j int) bool { return res.Claims[i].Seq < res.Claims[j].Seq })
	for _, e := range usedRel {
		// only relations fully inside the reached set
		if entSet[e.From] && entSet[e.To] {
			res.Relations = append(res.Relations, e)
		}
	}
	sort.Slice(res.Relations, func(i, j int) bool { return res.Relations[i].Seq < res.Relations[j].Seq })

	budgetNote := ""
	if res.BudgetHit {
		budgetNote = fmt.Sprintf(" (budget %d hit — subgraph truncated)", budget)
	}
	res.Coverage = fmt.Sprintf("subgraph from %d seed(s): %d entities, %d claims, %d relations, depth %d%s",
		len(res.SeedEntities), len(res.Entities), len(res.Claims), len(res.Relations), res.DepthReached, budgetNote)
	return res, nil
}

// matchSeed returns entities whose name or any current-claim text contains a seed token.
func matchSeed(cur []Claim, seed string) []string {
	terms := strings.Fields(strings.ToLower(seed))
	if len(terms) == 0 {
		return nil
	}
	hit := map[string]bool{}
	for _, c := range cur {
		hay := strings.ToLower(c.Entity + " " + c.Predicate + " " + c.Object)
		for _, t := range terms {
			if strings.Contains(hay, t) {
				hit[c.Entity] = true
				break
			}
		}
	}
	out := make([]string, 0, len(hit))
	for e := range hit {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

func relKey(e Edge) string { return e.From + "|" + e.Kind + "|" + e.To }
