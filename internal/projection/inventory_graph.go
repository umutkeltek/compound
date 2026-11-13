package projection

// The system graph: the stored inventory snapshot read as nodes and typed
// links, and three questions any agent can ask of it: what does X depend on,
// what depends on X, and what is not known.
//
// Relations are data (the kinds file says what each one means). A walk for
// "depends" follows depends relations and parent (plus route relations when
// it starts from a hostname); "impact" follows the same links backwards, and
// route links too. A path is only as sure as its weakest link: an inferred
// link is never shown as observed. Product references resolve through
// Compound's product identity owner, which the caller passes in; an
// unresolved one stays in the graph as an unknown product. The graph reads
// only files under COMPOUND_HOME and makes no external call.

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// InventoryGraphSchemaVersion names `compound system graph --json`.
	InventoryGraphSchemaVersion = "compound.system_graph.v0.1"
	// InventoryDependsSchemaVersion names `compound system depends --json`.
	InventoryDependsSchemaVersion = "compound.system_depends.v0.1"
	// InventoryImpactSchemaVersion names `compound system impact --json`.
	InventoryImpactSchemaVersion = "compound.system_impact.v0.1"
	// DefaultInventoryDependsDepth is how many links deep `depends` walks.
	DefaultInventoryDependsDepth = 6

	inventoryUnknownProduct = "unknown_product"
	inventoryNotInInventory = "not_in_inventory"
	inventoryNotKnownValue  = "not_known"
	inventoryGraphBoundary  = "read-only walk over one observed inventory snapshot under COMPOUND_HOME/observed/inventory; " +
		"no external call, no credentials; links are what the collector observed, declared or inferred, not verified truth"
)

// Walk outcomes.
const (
	InventoryWalkOK        = "ok"
	InventoryWalkAmbiguous = "ambiguous"
	InventoryWalkNotFound  = "not_found"
	InventoryWalkMissing   = "missing"
)

// inventoryAccessKinds are the kinds a thing "rests on": the account, the
// sign-in identity and the store that keeps its secret.
var inventoryAccessKinds = map[string]bool{"account": true, "identity": true, "secret_store": true}

// InventoryProductResolver maps a product reference ("product:<id>" as a
// collector wrote it) to the canonical "product:<id>" of Compound's product
// identity owner, or false when no product matches. nil resolves nothing.
type InventoryProductResolver func(ref string) (string, bool)

type inventoryNode struct {
	ID, Kind, Provider, Label string
	// Unknown is inventoryUnknownProduct or inventoryNotInInventory for a node
	// that only a link names.
	Unknown        string
	Members        []string
	Carried        bool
	LastObservedAt string
	// ObservedAt is when a source last really saw it (v0.2, optional): the
	// oldest any of its resources gives, since a scout file carries older
	// evidence than the snapshot.
	ObservedAt string
	Facts      map[string]InventoryObservedFact
}

// inventoryRelOwnedBy is the ownership relation: a domain, service or host
// owned by an account. Its meaning is other, so a depends walk leaves it to
// "rests on"; an impact walk follows it backwards, since what an account owns
// stops when the account goes.
const inventoryRelOwnedBy = "owned_by"

type inventoryEdge struct {
	From, Rel, To string
	Meaning       string
	Confidence    string
	Evidence      string
	Carried       bool
}

type inventoryGraph struct {
	kinds     InventoryKindSet
	kindOrder map[string]int
	nodes     map[string]*inventoryNode
	ids       []string
	edges     []*inventoryEdge
	out, in   map[string][]*inventoryEdge
	// resourceNode maps every resource id (and product spelling) to its node.
	resourceNode     map[string]string
	unknownRelations int
	resolve          func(string) (string, bool)
	// impactCache holds impactCounts per node: dependents, products.
	impactCache map[string][2]int
}

// buildInventoryGraph reads an observation as nodes and links. Product
// resources merge into one node per canonical product; a parent is a runs_on
// link; the same link twice keeps its strongest confidence.
func buildInventoryGraph(obs InventoryObservation, kinds InventoryKindSet, resolve InventoryProductResolver) *inventoryGraph {
	g := &inventoryGraph{
		kinds: kinds, kindOrder: map[string]int{}, nodes: map[string]*inventoryNode{},
		out: map[string][]*inventoryEdge{}, in: map[string][]*inventoryEdge{}, resourceNode: map[string]string{},
	}
	for i, k := range kinds.Kinds {
		g.kindOrder[k.ID] = i
	}
	cache := map[string]string{}
	g.resolve = func(ref string) (string, bool) {
		if canon, seen := cache[ref]; seen {
			return canon, canon != ""
		}
		canon := ""
		if resolve != nil {
			if c, ok := resolve(ref); ok && strings.HasPrefix(c, InventoryProductPrefix) && c != InventoryProductPrefix {
				canon = c
			}
		}
		cache[ref] = canon
		return canon, canon != ""
	}
	resources := append([]InventoryObservedResource(nil), obs.Resources...)
	sort.SliceStable(resources, func(i, j int) bool { return resources[i].ID < resources[j].ID })
	for _, r := range resources {
		id, unknown := r.ID, ""
		if r.Kind == "product" {
			if canon, ok := g.resolve(r.ID); ok {
				id = canon
			} else {
				unknown = inventoryUnknownProduct
			}
		}
		g.resourceNode[r.ID] = id
		n := g.nodes[id]
		if n == nil {
			n = &inventoryNode{ID: id, Kind: r.Kind, Provider: r.Provider, Label: r.Label, Unknown: unknown, Facts: map[string]InventoryObservedFact{}}
			g.nodes[id] = n
		}
		n.Members = append(n.Members, r.ID)
		if r.ObservedAt != "" && (n.ObservedAt == "" || r.ObservedAt < n.ObservedAt) {
			n.ObservedAt = r.ObservedAt
		}
		if r.Carried {
			n.Carried = true
			if n.LastObservedAt == "" || r.LastObservedAt < n.LastObservedAt {
				n.LastObservedAt = r.LastObservedAt
			}
		}
		for _, name := range sortedFactNames(r.Facts) {
			if _, have := n.Facts[name]; !have {
				n.Facts[name] = r.Facts[name]
			}
		}
	}
	byKey := map[string]*inventoryEdge{}
	for _, r := range resources {
		from := g.resourceNode[r.ID]
		links := r.Links
		if r.Parent != "" {
			// A parent is what a thing runs on, except an account: a domain or a
			// site whose parent is an account is owned by it, it does not run on it.
			rel := "runs_on"
			if p := g.nodes[g.resourceNode[r.Parent]]; p != nil && p.Kind == "account" {
				rel = inventoryRelOwnedBy
			}
			links = append([]InventoryLink{{Rel: rel, To: r.Parent, Evidence: InventoryRelParent}}, links...)
		}
		for _, l := range links {
			rel := l.Rel
			if rel == InventoryRelParent {
				rel = "runs_on"
			}
			meaning := InventoryMeaningOther
			if spec, known := kinds.Relation(rel); known {
				meaning = spec.Meaning
			} else {
				g.unknownRelations++
			}
			to := g.target(l.To)
			if to == from {
				continue
			}
			conf := InventoryLinkConfidence(l)
			if inventoryConfidenceRank(conf) == 0 {
				conf = InventoryConfidenceInferred
			}
			key := from + "\x00" + rel + "\x00" + to
			if e, seen := byKey[key]; seen {
				if inventoryConfidenceRank(conf) > inventoryConfidenceRank(e.Confidence) {
					e.Confidence, e.Evidence = conf, l.Evidence
				}
				e.Carried = e.Carried && r.Carried
				continue
			}
			byKey[key] = &inventoryEdge{From: from, Rel: rel, To: to, Meaning: meaning, Confidence: conf, Evidence: l.Evidence, Carried: r.Carried}
		}
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		e := byKey[k]
		g.edges = append(g.edges, e)
		g.out[e.From] = append(g.out[e.From], e)
		g.in[e.To] = append(g.in[e.To], e)
	}
	for id := range g.nodes {
		g.ids = append(g.ids, id)
	}
	sort.Strings(g.ids)
	return g
}

// target is the node a link points at: a resource, a resolved product, an
// unknown product, or a thing that is not in the inventory.
func (g *inventoryGraph) target(to string) string {
	if id, ok := g.resourceNode[to]; ok {
		return id
	}
	add := func(id, kind, label, unknown string) string {
		n := g.nodes[id]
		if n == nil {
			n = &inventoryNode{ID: id, Kind: kind, Label: label, Unknown: unknown, Facts: map[string]InventoryObservedFact{}}
			g.nodes[id] = n
		}
		if !inventoryContains(n.Members, to) {
			n.Members = append(n.Members, to)
		}
		g.resourceNode[to] = id
		return id
	}
	if strings.HasPrefix(to, InventoryProductPrefix) {
		if canon, ok := g.resolve(to); ok {
			return add(canon, "product", strings.TrimPrefix(canon, InventoryProductPrefix), "")
		}
		return add(to, "product", strings.TrimPrefix(to, InventoryProductPrefix), inventoryUnknownProduct)
	}
	return add(to, "", "", inventoryNotInInventory)
}

func inventoryContains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// unknownWhy says why a node is not known, "" for a known one.
func (n *inventoryNode) unknownWhy() string {
	switch n.Unknown {
	case inventoryUnknownProduct:
		return "unknown product: Compound has no product by this id"
	case inventoryNotInInventory:
		return "not in the inventory: a link names it, no source lists it"
	}
	return ""
}

// walkHit is how a walk reached a node: how deep, how sure the surest path
// is (a confidence rank), and the link it came by.
type walkHit struct {
	depth int
	conf  int
	via   *inventoryEdge
}

// walk is a bounded breadth-first walk from starts (node -> confidence rank)
// over the links follow accepts (backwards when reverse). It runs once per
// confidence level, surest first, so each node gets the confidence of its
// surest path: the weakest link on that path. Every node is counted once and
// a cycle ends because a reached node is never entered again.
func (g *inventoryGraph) walk(starts map[string]int, follow func(*inventoryEdge) bool, reverse bool, maxDepth int) map[string]walkHit {
	hits := map[string]walkHit{}
	startIDs := make([]string, 0, len(starts))
	for id := range starts {
		startIDs = append(startIDs, id)
	}
	sort.Strings(startIDs)
	for level := 3; level >= 1; level-- {
		seen := map[string]bool{}
		var frontier []string
		for _, id := range startIDs {
			if starts[id] >= level {
				seen[id] = true
				frontier = append(frontier, id)
				if _, done := hits[id]; !done {
					hits[id] = walkHit{depth: 0, conf: starts[id]}
				}
			}
		}
		for depth := 1; depth <= maxDepth && len(frontier) > 0; depth++ {
			var next []string
			for _, id := range frontier {
				edges := g.out[id]
				if reverse {
					edges = g.in[id]
				}
				for _, e := range edges {
					if !follow(e) || inventoryConfidenceRank(e.Confidence) < level {
						continue
					}
					other := e.To
					if reverse {
						other = e.From
					}
					if seen[other] {
						continue
					}
					seen[other] = true
					next = append(next, other)
					if _, done := hits[other]; !done {
						hits[other] = walkHit{depth: depth, conf: level, via: e}
					}
				}
			}
			frontier = next
		}
	}
	return hits
}

func inventoryConfidenceName(rank int) string {
	switch rank {
	case 3:
		return InventoryConfidenceObserved
	case 2:
		return InventoryConfidenceDeclared
	}
	return InventoryConfidenceInferred
}

func (g *inventoryGraph) dependsFollow(routes bool) func(*inventoryEdge) bool {
	return func(e *inventoryEdge) bool {
		return e.Meaning == InventoryMeaningDepends || (routes && e.Meaning == InventoryMeaningRoute)
	}
}

func (g *inventoryGraph) impactFollow(e *inventoryEdge) bool {
	return e.Meaning == InventoryMeaningDepends || e.Meaning == InventoryMeaningRoute || e.Rel == inventoryRelOwnedBy
}

// codeAndAddresses looks around the things a depends walk reached: where the
// code is (code_in links), which hostnames serve them (serves links, and
// route links from a hostname into them) with the first thing each address
// leads to, and the domain each address belongs to. Each is reached once, by
// its surest and then shortest link; a path is as sure as its weakest link.
func (g *inventoryGraph) codeAndAddresses(hits map[string]walkHit) (code, addresses, domains map[string]walkHit) {
	code, addresses, domains = map[string]walkHit{}, map[string]walkHit{}, map[string]walkHit{}
	offer := func(into map[string]walkHit, id string, from walkHit, e *inventoryEdge) {
		if _, inWalk := hits[id]; inWalk {
			return
		}
		h := walkHit{depth: from.depth + 1, conf: min(from.conf, inventoryConfidenceRank(e.Confidence)), via: e}
		if prev, ok := into[id]; ok && (prev.conf > h.conf || prev.conf == h.conf && prev.depth <= h.depth) {
			return
		}
		into[id] = h
	}
	isAddress := func(id string) bool { k := g.nodes[id].Kind; return k == "hostname" || k == "domain" }
	reached := make([]string, 0, len(hits))
	for id := range hits {
		reached = append(reached, id)
	}
	sort.Strings(reached)
	for _, id := range reached {
		h := hits[id]
		for _, e := range g.out[id] {
			switch {
			case e.Rel == "code_in":
				offer(code, e.To, h, e)
			case e.Rel == "serves" && isAddress(e.To):
				offer(addresses, e.To, h, e)
			}
		}
		for _, e := range g.in[id] {
			if e.Meaning == InventoryMeaningRoute && isAddress(e.From) {
				offer(addresses, e.From, h, e)
			}
		}
	}
	names := make([]string, 0, len(addresses))
	for id := range addresses {
		names = append(names, id)
	}
	sort.Strings(names)
	for _, a := range names {
		h := addresses[a]
		for _, e := range g.out[a] {
			switch {
			case e.Meaning == InventoryMeaningRoute:
				if _, already := addresses[e.To]; !already {
					offer(addresses, e.To, h, e)
				}
			case e.Rel == inventoryRelOwnedBy && g.nodes[e.To].Kind == "domain":
				offer(domains, e.To, h, e)
			}
		}
	}
	return code, addresses, domains
}

// impact is everything that depends on id, each counted once.
func (g *inventoryGraph) impact(id string) map[string]walkHit {
	hits := g.walk(map[string]int{id: 3}, g.impactFollow, true, len(g.ids)+1)
	delete(hits, id)
	return hits
}

// InventoryGraphStep is one thing a walk reached and how.
type InventoryGraphStep struct {
	Resource string `json:"resource"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	// Unknown is unknown_product or not_in_inventory for a thing that only a
	// link names.
	Unknown string `json:"unknown,omitempty"`
	Depth   int    `json:"depth"`
	// Via is the thing this step was reached from; Rel is the link between
	// them, read from the dependent side ("depends on" for depends, "is
	// depended on" for impact).
	Via            string `json:"via"`
	ViaLabel       string `json:"via_label"`
	Rel            string `json:"rel"`
	RelTitle       string `json:"rel_title"`
	LinkConfidence string `json:"link_confidence"`
	PathConfidence string `json:"path_confidence"`
	Evidence       string `json:"evidence"`
	Carried        bool   `json:"carried"`
	LastObservedAt string `json:"last_observed_at,omitempty"`
	// ObservedAt is when a source last really saw the thing, when it says so
	// (older than the snapshot for second-hand evidence).
	ObservedAt string `json:"observed_at,omitempty"`
	// StoreKind is a secret store's kind (vault, keychain, key_file, ...),
	// never what it holds.
	StoreKind string   `json:"store_kind,omitempty"`
	Warnings  []string `json:"warnings"`
	// viaIsTarget: the link runs from this step to Via (an address reached
	// by its route into the walk, or any impact step), not from Via to it.
	viaIsTarget bool
}

// InventoryGraphNotKnown is a place where an answer stops at "not known".
type InventoryGraphNotKnown struct {
	Resource string `json:"resource"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Why      string `json:"why"`
}

func (g *inventoryGraph) step(id string, h walkHit) InventoryGraphStep {
	n := g.nodes[id]
	s := InventoryGraphStep{
		Resource: id, Kind: n.Kind, Label: n.Label, Unknown: n.Unknown, Depth: h.depth,
		PathConfidence: inventoryConfidenceName(h.conf), Carried: n.Carried, LastObservedAt: n.LastObservedAt,
		ObservedAt: n.ObservedAt, Warnings: g.warnings(n),
	}
	if v, known := n.factIs("store_kind"); known && n.Kind == "secret_store" {
		s.StoreKind = inventoryClean(fmt.Sprint(v), 40)
	}
	if h.via != nil {
		s.Rel, s.LinkConfidence, s.Evidence = h.via.Rel, h.via.Confidence, h.via.Evidence
		s.RelTitle = h.via.Rel
		if spec, ok := g.kinds.Relation(h.via.Rel); ok {
			s.RelTitle = spec.Title
		}
		s.Via = h.via.From
		if s.Via == id {
			s.Via, s.viaIsTarget = h.via.To, true
		}
		s.ViaLabel = g.nodes[s.Via].Label
	}
	return s
}

// warnings are the node's facts past their kind's warning rule.
func (g *inventoryGraph) warnings(n *inventoryNode) []string {
	out := []string{}
	kind, ok := g.kinds.Kind(n.Kind)
	if !ok {
		return out
	}
	for _, v := range inventoryFactViews(kind, n.Facts, "") {
		if !v.Warn {
			continue
		}
		spec, _ := kind.Fact(v.Name)
		w := v.Title + " is " + formatInventoryValue(v.Value, v.Unit)
		if spec.WarnAt != nil {
			w += fmt.Sprintf(", %s %s", spec.WarnWhen, formatInventoryValue(*spec.WarnAt, v.Unit))
		}
		out = append(out, w)
	}
	return out
}

// notKnownFacts are the declared facts a node holds with the value not_known.
func (g *inventoryGraph) notKnownFacts(n *inventoryNode) []string {
	var out []string
	kind, _ := g.kinds.Kind(n.Kind)
	for _, name := range sortedFactNames(n.Facts) {
		f := n.Facts[name]
		if f.State == "ok" && f.Value == inventoryNotKnownValue {
			title := name
			if spec, ok := kind.Fact(name); ok {
				title = spec.Title
			}
			out = append(out, title+" not known")
		}
	}
	return out
}

// less orders steps: products first when asked, then by depth when asked,
// then by kind (in kinds-file order), label and id.
func (g *inventoryGraph) less(a, b InventoryGraphStep, depthFirst, productsFirst bool) bool {
	if productsFirst && (a.Kind == "product") != (b.Kind == "product") {
		return a.Kind == "product"
	}
	if depthFirst && a.Depth != b.Depth {
		return a.Depth < b.Depth
	}
	ka, kb := g.kindRank(a.Kind), g.kindRank(b.Kind)
	if ka != kb {
		return ka < kb
	}
	if a.Label != b.Label {
		return a.Label < b.Label
	}
	return a.Resource < b.Resource
}

func (g *inventoryGraph) kindRank(kind string) int {
	if i, ok := g.kindOrder[kind]; ok {
		return i
	}
	return len(g.kindOrder)
}

// inventoryGraphSource is the loaded snapshot a graph answer is built from.
type inventoryGraphSource struct {
	graph    *inventoryGraph
	snapshot InventorySnapshotStatus
	overlay  string
}

func loadInventoryGraph(home string, resolve InventoryProductResolver, now time.Time) (inventoryGraphSource, error) {
	kinds, err := LoadInventoryKinds(home)
	if err != nil {
		return inventoryGraphSource{}, err
	}
	src := inventoryGraphSource{overlay: kinds.OverlayError}
	snap, err := LoadInventorySnapshot(home)
	if err != nil {
		src.snapshot = InventorySnapshotStatus{State: "missing"}
		if errors.Is(err, os.ErrNotExist) {
			src.snapshot.Notice = "No inventory snapshot yet: " + inventoryRefreshHint + "."
		} else {
			src.snapshot.Notice = "The inventory snapshot could not be read (" + err.Error() + "): " + inventoryRefreshHint + "."
		}
		return src, nil
	}
	src.snapshot = inventorySnapshotState(snap, now, DefaultInventoryStaleHours)
	src.graph = buildInventoryGraph(snap.Observation, kinds, resolve)
	return src, nil
}

// inventorySnapshotState is how old a stored snapshot is: fresh or stale.
func inventorySnapshotState(snap InventorySnapshot, now time.Time, staleHours float64) InventorySnapshotStatus {
	observedAt, _ := time.Parse(time.RFC3339, snap.Observation.ObservedAt)
	age := math.Max(0, now.Sub(observedAt).Hours())
	ageRounded := math.Round(age*10) / 10
	st := InventorySnapshotStatus{State: "fresh", ObservedAt: snap.Observation.ObservedAt, ImportedAt: snap.ImportedAt, AgeHours: &ageRounded}
	if age > staleHours {
		st.State = "stale"
		st.Notice = fmt.Sprintf("The inventory was observed %s ago, more than %s hours: values may be out of date; %s.",
			humanInventoryAge(age), strconv.FormatFloat(staleHours, 'f', -1, 64), inventoryRefreshHint)
	}
	return st
}
