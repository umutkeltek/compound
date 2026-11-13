package projection

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// InventoryGraphQuery narrows `compound system graph`'s link list. Kind keeps
// links with an end of that kind; Rel keeps links of that relation.
type InventoryGraphQuery struct {
	Kind     string
	Rel      string
	Products InventoryProductResolver
}

// InventoryGraphProjection is compound.system_graph.v0.1: counts of things by
// kind and links by relation and by confidence, the references that did not
// resolve, and the link list when a filter is given.
type InventoryGraphProjection struct {
	SchemaVersion     string                  `json:"schema_version"`
	GeneratedAt       string                  `json:"generated_at"`
	Boundary          string                  `json:"boundary"`
	Snapshot          InventorySnapshotStatus `json:"snapshot"`
	OverlayError      string                  `json:"overlay_error"`
	Filter            InventoryGraphFilter    `json:"filter"`
	Nodes             int                     `json:"nodes"`
	Edges             int                     `json:"edges"`
	NodesByKind       map[string]int          `json:"nodes_by_kind"`
	EdgesByRelation   map[string]int          `json:"edges_by_relation"`
	EdgesByConfidence map[string]int          `json:"edges_by_confidence"`
	CarriedEdges      int                     `json:"carried_edges"`
	UnknownRelations  int                     `json:"unknown_relations"`
	Unresolved        []InventoryGraphRef     `json:"unresolved"`
	Relations         []InventoryRelation     `json:"relations"`
	EdgeList          []InventoryGraphEdge    `json:"edge_list"`
	EdgeListTotal     int                     `json:"edge_list_total"`
	EdgeListTruncated bool                    `json:"edge_list_truncated"`
}

// InventoryGraphFilter echoes the query.
type InventoryGraphFilter struct {
	Kind string `json:"kind"`
	Rel  string `json:"rel"`
}

// InventoryGraphRef is a link target that is not a known thing: an unknown
// product, or something no source lists. Links counts how many links name it.
type InventoryGraphRef struct {
	Ref   string `json:"ref"`
	Kind  string `json:"kind"` // unknown_product | not_in_inventory
	Label string `json:"label"`
	Links int    `json:"links"`
}

// InventoryGraphEdge is one link with both ends named.
type InventoryGraphEdge struct {
	From       string `json:"from"`
	FromKind   string `json:"from_kind"`
	FromLabel  string `json:"from_label"`
	Rel        string `json:"rel"`
	RelTitle   string `json:"rel_title"`
	Meaning    string `json:"meaning"`
	To         string `json:"to"`
	ToKind     string `json:"to_kind"`
	ToLabel    string `json:"to_label"`
	Confidence string `json:"confidence"`
	Evidence   string `json:"evidence"`
	Carried    bool   `json:"carried"`
}

// maxInventoryEdgeList bounds the JSON link list; the total is always given.
const maxInventoryEdgeList = 2000

// BuildInventoryGraph projects the stored snapshot as a graph. It fails only
// on an invalid embedded kinds file or an unknown --kind or --rel; a missing
// snapshot is a "missing" state with a notice.
func BuildInventoryGraph(home string, q InventoryGraphQuery, now time.Time) (InventoryGraphProjection, error) {
	src, err := loadInventoryGraph(home, q.Products, now)
	if err != nil {
		return InventoryGraphProjection{}, err
	}
	kinds, _ := LoadInventoryKinds(home)
	if q.Kind != "" {
		if _, ok := kinds.Kind(q.Kind); !ok {
			return InventoryGraphProjection{}, fmt.Errorf("unknown kind %q (known kinds: %s)", q.Kind, strings.Join(kinds.KindIDs(), ", "))
		}
	}
	if q.Rel != "" {
		if _, ok := kinds.Relation(q.Rel); !ok {
			return InventoryGraphProjection{}, fmt.Errorf("unknown relation %q (nearest: %s)", q.Rel, kinds.NearestRelation(q.Rel))
		}
	}
	p := InventoryGraphProjection{
		SchemaVersion: InventoryGraphSchemaVersion, GeneratedAt: now.UTC().Format(time.RFC3339), Boundary: inventoryGraphBoundary,
		Snapshot: src.snapshot, OverlayError: src.overlay, Filter: InventoryGraphFilter{Kind: q.Kind, Rel: q.Rel},
		NodesByKind: map[string]int{}, EdgesByRelation: map[string]int{}, EdgesByConfidence: map[string]int{},
		Unresolved: []InventoryGraphRef{}, Relations: kinds.Relations, EdgeList: []InventoryGraphEdge{},
	}
	g := src.graph
	if g == nil {
		return p, nil
	}
	for _, id := range g.ids {
		n := g.nodes[id]
		if n.Unknown != "" {
			label := n.Label
			if n.Unknown == inventoryNotInInventory {
				label = ""
			}
			p.Unresolved = append(p.Unresolved, InventoryGraphRef{Ref: id, Kind: n.Unknown, Label: label, Links: len(g.in[id]) + len(g.out[id])})
			continue
		}
		p.Nodes++
		p.NodesByKind[n.Kind]++
	}
	sort.SliceStable(p.Unresolved, func(i, j int) bool {
		a, b := p.Unresolved[i], p.Unresolved[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Ref < b.Ref
	})
	p.UnknownRelations = g.unknownRelations
	for _, e := range g.edges {
		p.Edges++
		p.EdgesByRelation[e.Rel]++
		p.EdgesByConfidence[e.Confidence]++
		if e.Carried {
			p.CarriedEdges++
		}
		if q.Kind == "" && q.Rel == "" {
			continue
		}
		from, to := g.nodes[e.From], g.nodes[e.To]
		if q.Rel != "" && e.Rel != q.Rel {
			continue
		}
		if q.Kind != "" && from.Kind != q.Kind && to.Kind != q.Kind {
			continue
		}
		p.EdgeListTotal++
		if len(p.EdgeList) >= maxInventoryEdgeList {
			p.EdgeListTruncated = true
			continue
		}
		title := e.Rel
		if spec, ok := kinds.Relation(e.Rel); ok {
			title = spec.Title
		}
		p.EdgeList = append(p.EdgeList, InventoryGraphEdge{
			From: e.From, FromKind: from.Kind, FromLabel: from.Label, Rel: e.Rel, RelTitle: title, Meaning: e.Meaning,
			To: e.To, ToKind: to.Kind, ToLabel: to.Label, Confidence: e.Confidence, Evidence: e.Evidence, Carried: e.Carried,
		})
	}
	return p, nil
}

// InventoryDependsQuery asks what one thing cannot work without.
type InventoryDependsQuery struct {
	Target   string
	Depth    int
	Products InventoryProductResolver
}

// InventoryWalkProjection is the answer of `compound system depends`
// (compound.system_depends.v0.1) and `compound system impact`
// (compound.system_impact.v0.1).
type InventoryWalkProjection struct {
	SchemaVersion string                  `json:"schema_version"`
	GeneratedAt   string                  `json:"generated_at"`
	Boundary      string                  `json:"boundary"`
	Snapshot      InventorySnapshotStatus `json:"snapshot"`
	OverlayError  string                  `json:"overlay_error"`
	Query         string                  `json:"query"`
	// Status is ok, ambiguous (Candidates lists the matches), not_found or
	// missing (no snapshot); Notice says it in words.
	Status     string               `json:"status"`
	Notice     string               `json:"notice"`
	Start      *InventoryGraphStep  `json:"start"`
	Candidates []InventoryGraphStep `json:"candidates"`
	// Depends only: the depth limit, whether route links were followed (a
	// hostname start), everything the start cannot work without, and the
	// accounts, identities and secret stores those rest on.
	DepthLimit     int                  `json:"depth_limit,omitempty"`
	RoutesFollowed bool                 `json:"routes_followed,omitempty"`
	Steps          []InventoryGraphStep `json:"steps"`
	// Code is where its code is (code_in links from the start and from what
	// the walk reached). Addresses are the hostnames that serve it (serves
	// links, and route links from a hostname into what the walk reached), each
	// followed by the first thing its route leads to. Both are depends only.
	Code      []InventoryGraphStep `json:"code,omitempty"`
	Addresses []InventoryGraphStep `json:"addresses,omitempty"`
	RestsOn   []InventoryGraphStep `json:"rests_on"`
	// Impact only: how many of the dependents are products.
	Products int `json:"products"`
	// NotKnown lists where the answer stops at "not known".
	NotKnown []InventoryGraphNotKnown `json:"not_known"`
	// impact marks an impact answer for the text view.
	impact bool
}

func newInventoryWalk(schema, query string, now time.Time, src inventoryGraphSource) InventoryWalkProjection {
	return InventoryWalkProjection{
		SchemaVersion: schema, GeneratedAt: now.UTC().Format(time.RFC3339), Boundary: inventoryGraphBoundary,
		Snapshot: src.snapshot, OverlayError: src.overlay, Query: query, Status: InventoryWalkOK,
		Candidates: []InventoryGraphStep{}, Steps: []InventoryGraphStep{}, RestsOn: []InventoryGraphStep{},
		NotKnown: []InventoryGraphNotKnown{},
	}
}

// find resolves a query to one node: an exact id or a resource id merged into
// a node; otherwise the union of a product the identity owner resolves, a
// case-insensitive label, and a masked id tail ("…ab12"). Several matches are
// ambiguous.
func (g *inventoryGraph) find(query string) (string, []string, string) {
	q := strings.TrimSpace(query)
	if q == "" {
		return "", nil, "an empty query names nothing"
	}
	if id, ok := g.resourceNode[q]; ok {
		return id, nil, ""
	}
	if _, ok := g.nodes[q]; ok {
		return q, nil, ""
	}
	found := map[string]bool{}
	ref := q
	if !strings.HasPrefix(ref, InventoryProductPrefix) {
		ref = InventoryProductPrefix + q
	}
	productKnown := false
	if canon, ok := g.resolve(ref); ok {
		productKnown = true
		if _, inGraph := g.nodes[canon]; inGraph {
			found[canon] = true
		}
	}
	tail := strings.TrimPrefix(q, "…")
	for _, id := range g.ids {
		n := g.nodes[id]
		if n.Unknown == inventoryNotInInventory {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(n.Label), q) || (tail != q && tail != "" && strings.HasSuffix(id, tail)) {
			found[id] = true
		}
	}
	var ids []string
	for id := range found {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	switch len(ids) {
	case 0:
		if productKnown {
			return "", nil, fmt.Sprintf("%q is a Compound product, but nothing in the inventory links to it", q)
		}
		return "", nil, fmt.Sprintf("nothing in the inventory has the id or label %q", q)
	case 1:
		return ids[0], nil, ""
	}
	return "", ids, fmt.Sprintf("%d things match %q; ask again with one of their ids", len(ids), q)
}

// startWalk resolves the query or fills the answer's status.
func startWalk(p *InventoryWalkProjection, g *inventoryGraph, query string) (string, bool) {
	if g == nil {
		p.Status, p.Notice = InventoryWalkMissing, p.Snapshot.Notice
		return "", false
	}
	id, candidates, why := g.find(query)
	if id == "" {
		p.Notice = why
		p.Status = InventoryWalkNotFound
		if len(candidates) > 0 {
			p.Status = InventoryWalkAmbiguous
			for _, c := range candidates {
				p.Candidates = append(p.Candidates, g.step(c, walkHit{conf: 3}))
			}
		}
		return "", false
	}
	start := g.step(id, walkHit{conf: 3})
	p.Start = &start
	return id, true
}

// BuildInventoryDepends answers "what does X depend on": everything X cannot
// work without, walking depends relations and parent (and route relations
// when X is a hostname) up to q.Depth links; then the accounts, identities
// and secret stores those rest on; then where the walk stopped at not known.
func BuildInventoryDepends(home string, q InventoryDependsQuery, now time.Time) (InventoryWalkProjection, error) {
	src, err := loadInventoryGraph(home, q.Products, now)
	if err != nil {
		return InventoryWalkProjection{}, err
	}
	if q.Depth <= 0 {
		q.Depth = DefaultInventoryDependsDepth
	}
	p := newInventoryWalk(InventoryDependsSchemaVersion, q.Target, now, src)
	p.DepthLimit = q.Depth
	g := src.graph
	id, ok := startWalk(&p, g, q.Target)
	if !ok {
		return p, nil
	}
	p.RoutesFollowed = g.nodes[id].Kind == "hostname"
	follow := g.dependsFollow(p.RoutesFollowed)
	hits := g.walk(map[string]int{id: 3}, follow, false, q.Depth)
	starts := map[string]int{}
	for nid, h := range hits {
		starts[nid] = h.conf
		if nid != id {
			p.Steps = append(p.Steps, g.step(nid, h))
		}
	}
	sort.SliceStable(p.Steps, func(i, j int) bool { return g.less(p.Steps[i], p.Steps[j], true, false) })

	// Where its code is and which addresses serve it. Both, and the domain each
	// address belongs to, also seed what it rests on: a domain or a service an
	// address leads to is owned by an account.
	origin := map[string]walkHit{}
	for nid, h := range hits {
		origin[nid] = h
	}
	code, addresses, domains := g.codeAndAddresses(hits)
	for _, extra := range []map[string]walkHit{code, addresses, domains} {
		for nid, h := range extra {
			if _, seen := origin[nid]; !seen {
				origin[nid], starts[nid] = h, h.conf
			}
		}
	}
	for nid, h := range code {
		p.Code = append(p.Code, g.step(nid, h))
	}
	for nid, h := range addresses {
		p.Addresses = append(p.Addresses, g.step(nid, h))
	}
	sort.SliceStable(p.Code, func(i, j int) bool { return g.less(p.Code[i], p.Code[j], false, false) })
	sort.SliceStable(p.Addresses, func(i, j int) bool { return g.less(p.Addresses[i], p.Addresses[j], true, false) })

	// What it rests on: identity links, and any link to an account, identity
	// or secret store, from everything reached, as far as they go.
	access := g.walk(starts, func(e *inventoryEdge) bool {
		return e.Meaning == InventoryMeaningIdentity || inventoryAccessKinds[g.nodes[e.To].Kind]
	}, false, len(g.ids)+1)
	for nid, h := range access {
		if nid == id || (h.depth == 0 && !inventoryAccessKinds[g.nodes[nid].Kind]) {
			continue
		}
		if h.depth == 0 {
			h = origin[nid] // an account the walk itself reached: show the link it came by
		}
		p.RestsOn = append(p.RestsOn, g.step(nid, h))
	}
	sort.SliceStable(p.RestsOn, func(i, j int) bool { return g.less(p.RestsOn[i], p.RestsOn[j], false, false) })

	// Where it stops at not known: unknown things, facts whose value is
	// not_known, and things at the depth limit that have more below them.
	reached := []string{id}
	for _, s := range p.Steps {
		reached = append(reached, s.Resource)
	}
	for _, s := range p.Addresses {
		reached = append(reached, s.Resource)
	}
	for _, nid := range reached {
		n := g.nodes[nid]
		if why := n.unknownWhy(); why != "" {
			p.NotKnown = append(p.NotKnown, InventoryGraphNotKnown{Resource: nid, Kind: n.Kind, Label: n.Label, Why: why})
			continue
		}
		for _, why := range g.notKnownFacts(n) {
			p.NotKnown = append(p.NotKnown, InventoryGraphNotKnown{Resource: nid, Kind: n.Kind, Label: n.Label, Why: why})
		}
		if h, walked := hits[nid]; walked && h.depth == q.Depth {
			for _, e := range g.out[nid] {
				if _, seen := hits[e.To]; follow(e) && !seen {
					p.NotKnown = append(p.NotKnown, InventoryGraphNotKnown{Resource: nid, Kind: n.Kind, Label: n.Label,
						Why: fmt.Sprintf("depth limit %d reached: more lies below (--depth N walks further)", q.Depth)})
					break
				}
			}
		}
	}
	return p, nil
}

// BuildInventoryImpact answers "what depends on X": everything that cannot
// work without X, through any number of depends, parent or route links, each
// counted once, products first, then by kind.
func BuildInventoryImpact(home, target string, products InventoryProductResolver, now time.Time) (InventoryWalkProjection, error) {
	src, err := loadInventoryGraph(home, products, now)
	if err != nil {
		return InventoryWalkProjection{}, err
	}
	p := newInventoryWalk(InventoryImpactSchemaVersion, target, now, src)
	p.impact = true
	g := src.graph
	id, ok := startWalk(&p, g, target)
	if !ok {
		return p, nil
	}
	for nid, h := range g.impact(id) {
		s := g.step(nid, h)
		p.Steps = append(p.Steps, s)
		if s.Kind == "product" {
			p.Products++
		}
		if why := g.nodes[nid].unknownWhy(); why != "" {
			p.NotKnown = append(p.NotKnown, InventoryGraphNotKnown{Resource: nid, Kind: s.Kind, Label: s.Label, Why: why})
		}
	}
	sort.SliceStable(p.Steps, func(i, j int) bool { return g.less(p.Steps[i], p.Steps[j], false, true) })
	sort.SliceStable(p.NotKnown, func(i, j int) bool { return p.NotKnown[i].Resource < p.NotKnown[j].Resource })
	return p, nil
}
