package product

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"compound/internal/mem"
)

// Where statuses. Known and unknown are both successful answers; only an
// unresolved query means the reader could not find the product at all.
const (
	WhereKnown      = "known"      // the query selects exactly one recorded place
	WhereMultiple   = "multiple"   // the product has several deploy targets (app, web, API); name one
	WhereUnknown    = "unknown"    // the product exists; nothing records where it runs
	WhereConflict   = "conflict"   // two records of the same target disagree on its place
	WhereAmbiguous  = "ambiguous"  // the query names more than one product
	WhereUnresolved = "unresolved" // the query names no product
)

// legacyDeploymentPredicates are the runtime facts internal/authority wrote on
// bare-slug entities before deployment moved onto product entities. They are
// shown beside the answer, never promoted into it: that registry is frozen.
var legacyDeploymentPredicates = []string{"deployed_on", "source_host", "database", "domain", "cloud_profile"}

// WhereTarget is one recorded deployment: the product entity that carries it
// and where that surface runs. An app and its web/API side are separate
// targets of one product, each with its own place and command.
type WhereTarget struct {
	ProductID    string     `json:"product_id"`
	Deployment   Deployment `json:"deployment"`
	EvidenceRefs []string   `json:"evidence_refs,omitempty"`
}

// WhereReport answers "where does this product run, and how does it get there".
// Deployment is filled only when the query selects exactly one target; every
// recorded target is listed in Targets either way.
type WhereReport struct {
	Query     string `json:"query,omitempty"`
	Status    string `json:"status"`
	ProductID string `json:"product_id,omitempty"`
	// GroupID is the one name the product goes by everywhere: app pages,
	// results, the inventory, the dependency graph, and the app a piece of
	// work records. ProductID stays the deploy target the answer selected;
	// a product with an app and a web side has one GroupID and two targets.
	GroupID    string        `json:"group_id,omitempty"`
	Members    []string      `json:"members,omitempty"`
	Deployment Deployment    `json:"deployment"`
	Targets    []WhereTarget `json:"targets,omitempty"`
	Conflicts  []string      `json:"conflicts,omitempty"`
	// Legacy holds the frozen bare-slug authority rows for the same product,
	// keyed by predicate. Context for a human, not a deploy instruction.
	Legacy       map[string]string `json:"legacy_authority,omitempty"`
	Surfaces     []string          `json:"surfaces,omitempty"`
	EvidenceRefs []string          `json:"evidence_refs,omitempty"`
	Next         string            `json:"next"`
}

// Where resolves query — a product id, slug, display name, alias, stable
// identifier, or a path inside a product checkout — to one product and reports
// its recorded deployment.
//
// One product is often several ledger entities: writers disagree on separators
// (product:a-b vs product:a_b), a rename leaves the old slug behind, and an app
// and its registry page are imported separately. Where folds those together
// before answering, so a fact recorded under one spelling answers a question
// asked under another. It folds only on evidence of identity — the same slug
// once separators are ignored, a shared App Store bundle or app id, or an
// alias the product itself declares — never on a shared URL, because one
// service can front several products.
func Where(store *mem.Store, query string) (WhereReport, error) {
	idx, err := loadWhereIndex(store)
	if err != nil {
		return WhereReport{}, err
	}
	query = strings.TrimSpace(query)
	matched := idx.match(query)
	var groups []int
	for root := range matched {
		groups = append(groups, root)
	}
	sort.Ints(groups)
	switch len(groups) {
	case 0:
		return WhereReport{
			Query:  query,
			Status: WhereUnresolved,
			Next:   fmt.Sprintf("no product matches %q; `compound product where --all` lists every product the ledger knows", query),
		}, nil
	case 1:
		report := idx.report(groups[0], matched[groups[0]])
		report.Query = query
		return report, nil
	}
	var names []string
	for _, g := range groups {
		names = append(names, strings.Join(idx.members(g), "|"))
	}
	return WhereReport{
		Query:     query,
		Status:    WhereAmbiguous,
		Conflicts: names,
		Next:      "the query names more than one product; ask again with a product id",
	}, nil
}

// WhereAll reports every product once, however many entities spell it.
func WhereAll(store *mem.Store) ([]WhereReport, error) {
	idx, err := loadWhereIndex(store)
	if err != nil {
		return nil, err
	}
	var out []WhereReport
	for _, g := range idx.groups() {
		out = append(out, idx.report(g, nil))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProductID < out[j].ProductID })
	return out, nil
}

type whereIndex struct {
	entities []mem.Entity
	parent   []int
	// claims holds the latest current claim per entity and predicate.
	claims map[string]map[string]mem.Claim
	// signalRows counts product signal rows per lowercased product id; the
	// main spelling of a group is the registered member signals name most.
	signalRows map[string]int
	// systems are the registered system: slugs, and systemPaths the recorded
	// folders of entities whose parent is a system. They are the second kind
	// of home a piece of work can have; `where` itself still answers products.
	systems     map[string]bool
	systemPaths []systemPath
}

// systemPath is one recorded folder of an entity that belongs to a system.
type systemPath struct {
	root, system string
}

// registeredEntity reports whether a product entity was registered with
// identity facts, as opposed to a slug some writer only put a claim on.
func registeredEntity(e mem.Entity) bool {
	return e.CreatedAt != "" || len(e.Aliases) > 0 || len(e.Identifiers) > 0
}

// mainSpelling is the one rule that picks the name a product group goes by.
// Among registered spellings, the one the product signal ledger names most
// wins; ties go to the first in sort order. A group with no registered
// spelling chooses among all of them the same way. Signal writers are the
// longest-running writers of product ids, so this is the spelling the app
// pages and results already show; a separator-variant registered later does
// not take the name over. ids must be sorted.
func mainSpelling(ids []string, registered func(string) bool, rows func(string) int) string {
	best := ""
	pick := func(candidate func(string) bool) {
		for _, id := range ids {
			if candidate(id) && (best == "" || rows(id) > rows(best)) {
				best = id
			}
		}
	}
	pick(registered)
	if best == "" {
		pick(func(string) bool { return true })
	}
	return best
}

// signalSpellingRows counts the signal rows that name each product spelling,
// with the same filter the growth projection applies to its signal rows.
func signalSpellingRows(signals []Signal) map[string]int {
	rows := map[string]int{}
	for _, s := range signals {
		metric := strings.TrimSpace(s.MetricName)
		if strings.TrimSpace(s.ProductID) == "" || metric == "" || strings.HasPrefix(metric, "ads_") {
			continue
		}
		rows[strings.ToLower(s.ProductID)]++
	}
	return rows
}

// groupID is the main spelling of the group rooted at root.
func (idx *whereIndex) groupID(root int) string {
	members := idx.memberIndexes(root)
	ids := make([]string, 0, len(members))
	registered := map[string]bool{}
	for _, i := range members {
		ids = append(ids, idx.entities[i].Slug)
		registered[idx.entities[i].Slug] = registeredEntity(idx.entities[i])
	}
	sort.Strings(ids)
	return mainSpelling(ids, func(id string) bool { return registered[id] }, func(id string) int { return idx.signalRows[strings.ToLower(id)] })
}

func loadWhereIndex(store *mem.Store) (*whereIndex, error) {
	registered, err := store.Entities()
	if err != nil {
		return nil, err
	}
	view, err := store.CurrentView()
	if err != nil {
		return nil, err
	}
	idx := &whereIndex{claims: map[string]map[string]mem.Claim{}}
	for _, c := range view.Claims() {
		byPredicate := idx.claims[c.Entity]
		if byPredicate == nil {
			byPredicate = map[string]mem.Claim{}
			idx.claims[c.Entity] = byPredicate
		}
		if prior, ok := byPredicate[c.Predicate]; !ok || c.Seq > prior.Seq {
			byPredicate[c.Predicate] = c
		}
	}
	seen := map[string]bool{}
	idx.systems = map[string]bool{}
	for _, e := range registered {
		if strings.HasPrefix(e.Slug, "system:") {
			idx.systems[e.Slug] = true
		}
		if parent := strings.TrimSpace(e.Parent); strings.HasPrefix(parent, "system:") && !strings.HasPrefix(e.Slug, "product:") {
			idx.systems[parent] = true
			for _, id := range e.Identifiers {
				if filepath.IsAbs(id) {
					idx.systemPaths = append(idx.systemPaths, systemPath{root: filepath.Clean(id), system: parent})
				}
			}
		}
		if e.Kind == "product" && strings.HasPrefix(e.Slug, "product:") && !seen[e.Slug] {
			seen[e.Slug] = true
			idx.entities = append(idx.entities, e)
		}
	}
	// Some writers record claims on a product id without registering it. They
	// are still spellings of a product, so they join by slug alone.
	for entity := range idx.claims {
		if strings.HasPrefix(entity, "product:") && !seen[entity] {
			seen[entity] = true
			idx.entities = append(idx.entities, mem.Entity{Slug: entity, Kind: "product"})
		}
	}
	sort.Slice(idx.entities, func(i, j int) bool { return idx.entities[i].Slug < idx.entities[j].Slug })
	idx.fold()
	idx.signalRows = map[string]int{}
	if home := store.HomePath(); home != "" {
		signals, err := loadSignals(home)
		if err != nil {
			return nil, err
		}
		idx.signalRows = signalSpellingRows(signals)
	}
	return idx, nil
}

// identityKey folds a product reference to what every writer agrees on: the
// slug's letters and digits. product:a-b, product:a_b, and "A B" share a key.
func identityKey(ref string) string {
	ref = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ref)), "product:")
	var b strings.Builder
	for _, r := range ref {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// appIdentity returns a store identity that belongs to exactly one app: its
// bundle id or its App Store id, in whichever surface form a writer used.
func appIdentity(identifier string) string {
	low := strings.ToLower(strings.TrimSpace(identifier))
	switch {
	case strings.HasPrefix(low, "bundle:"):
		return "bundle:" + strings.TrimPrefix(low, "bundle:")
	case strings.HasPrefix(low, "appstore:"):
		value := strings.TrimPrefix(low, "appstore:")
		if isDigits(value) {
			return "app:" + value
		}
		return "bundle:" + value
	case strings.Contains(low, "apps.apple.com/"):
		if i := strings.LastIndex(low, "/id"); i >= 0 {
			value := strings.TrimRight(low[i+3:], "/")
			if cut := strings.IndexAny(value, "?#"); cut >= 0 {
				value = value[:cut]
			}
			if isDigits(value) {
				return "app:" + value
			}
		}
	}
	return ""
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (idx *whereIndex) find(i int) int {
	for idx.parent[i] != i {
		idx.parent[i] = idx.parent[idx.parent[i]]
		i = idx.parent[i]
	}
	return i
}

func (idx *whereIndex) union(a, b int) {
	if ra, rb := idx.find(a), idx.find(b); ra != rb {
		idx.parent[rb] = ra
	}
}

func (idx *whereIndex) fold() {
	idx.parent = make([]int, len(idx.entities))
	for i := range idx.parent {
		idx.parent[i] = i
	}
	bySlugKey := map[string]int{}
	byApp := map[string]int{}
	for i, e := range idx.entities {
		if key := identityKey(e.Slug); key != "" {
			if j, ok := bySlugKey[key]; ok {
				idx.union(j, i)
			} else {
				bySlugKey[key] = i
			}
		}
		for _, id := range e.Identifiers {
			if app := appIdentity(id); app != "" {
				if j, ok := byApp[app]; ok {
					idx.union(j, i)
				} else {
					byApp[app] = i
				}
			}
		}
	}
	// A declared alias joins only when it spells another product's slug; an
	// alias that names no product ("feed API for X") joins nothing.
	for i, e := range idx.entities {
		for _, alias := range e.Aliases {
			if j, ok := bySlugKey[identityKey(alias)]; ok {
				idx.union(i, j)
			}
		}
	}
}

func (idx *whereIndex) groups() []int {
	var roots []int
	seen := map[int]bool{}
	for i := range idx.entities {
		if r := idx.find(i); !seen[r] {
			seen[r] = true
			roots = append(roots, r)
		}
	}
	return roots
}

func (idx *whereIndex) memberIndexes(root int) []int {
	var out []int
	for i := range idx.entities {
		if idx.find(i) == root {
			out = append(out, i)
		}
	}
	return out
}

func (idx *whereIndex) members(root int) []string {
	var out []string
	for _, i := range idx.memberIndexes(root) {
		out = append(out, idx.entities[i].Slug)
	}
	return out
}

// match returns the groups the query names, using the strongest tier any
// entity matches at: exact id, then folded slug, then stable identifier, then
// alias, then the longest recorded checkout path containing the query path.
// Only an exact id or a path inside a target's own checkout also selects that
// entity's target; a name names the product, and a product with several
// targets stays open until one is chosen.
func (idx *whereIndex) match(query string) map[int]map[string]bool {
	if query == "" {
		return nil
	}
	key := identityKey(query)
	low := strings.ToLower(query)
	path := ""
	if filepath.IsAbs(query) {
		path = filepath.Clean(query)
	}
	tiers := map[int]map[int]map[string]bool{}
	add := func(tier, i int) {
		if tiers[tier] == nil {
			tiers[tier] = map[int]map[string]bool{}
		}
		root := idx.find(i)
		if tiers[tier][root] == nil {
			tiers[tier][root] = map[string]bool{}
		}
		tiers[tier][root][idx.entities[i].Slug] = true
	}
	addGroup := func(tier, i int) {
		if tiers[tier] == nil {
			tiers[tier] = map[int]map[string]bool{}
		}
		if root := idx.find(i); tiers[tier][root] == nil {
			tiers[tier][root] = map[string]bool{}
		}
	}
	type pathHit struct{ entity, depth int }
	var pathHits []pathHit
	longest := 0
	for i, e := range idx.entities {
		switch {
		case e.Slug == query || e.Slug == "product:"+query:
			add(4, i)
		case key != "" && identityKey(e.Slug) == key:
			addGroup(3, i)
		}
		for _, id := range e.Identifiers {
			if id == query {
				addGroup(2, i)
			}
			if path == "" || !filepath.IsAbs(id) {
				continue
			}
			root := filepath.Clean(id)
			if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
				pathHits = append(pathHits, pathHit{i, len(root)})
				if len(root) > longest {
					longest = len(root)
				}
			}
		}
		for _, alias := range e.Aliases {
			if strings.ToLower(strings.TrimSpace(alias)) == low {
				addGroup(1, i)
			}
		}
	}
	for _, h := range pathHits {
		if h.depth != longest {
			continue
		}
		if idx.containsOtherTarget(h.entity) {
			// A checkout that holds another target's checkout is a workspace:
			// the path names the product, not this target. Selecting the root
			// here would hand a sibling app's directory the root's command.
			addGroup(0, h.entity)
			continue
		}
		add(0, h.entity)
	}
	for tier := 4; tier >= 0; tier-- {
		if len(tiers[tier]) > 0 {
			return tiers[tier]
		}
	}
	return nil
}

// containsOtherTarget reports whether entity i's checkout path is a strict
// ancestor of the checkout of another member of its product that records a
// deployment.
func (idx *whereIndex) containsOtherTarget(i int) bool {
	root := idx.find(i)
	for _, own := range idx.entities[i].Identifiers {
		if !filepath.IsAbs(own) {
			continue
		}
		prefix := filepath.Clean(own) + string(filepath.Separator)
		for j, other := range idx.entities {
			if j == i || idx.find(j) != root {
				continue
			}
			if _, ok := idx.claim(other.Slug, "deploys_to"); !ok {
				continue
			}
			for _, id := range other.Identifiers {
				if filepath.IsAbs(id) && strings.HasPrefix(filepath.Clean(id), prefix) {
					return true
				}
			}
		}
	}
	return false
}

func (idx *whereIndex) claim(entity, predicate string) (mem.Claim, bool) {
	c, ok := idx.claims[entity][predicate]
	return c, ok && strings.TrimSpace(c.Object) != ""
}

func (idx *whereIndex) report(root int, selected map[string]bool) WhereReport {
	members := idx.memberIndexes(root)
	report := WhereReport{Status: WhereUnknown}
	for _, i := range members {
		report.Members = append(report.Members, idx.entities[i].Slug)
	}
	report.ProductID = report.Members[0]
	report.GroupID = idx.groupID(root)

	var surfaces []string
	for _, slug := range report.Members {
		if c, ok := idx.claim(slug, "surfaces"); ok {
			surfaces = append(surfaces, strings.Split(c.Object, ",")...)
		}
	}
	report.Surfaces = compact(surfaces)
	report.Legacy = idx.legacy(members)

	targets, conflicts := idx.targets(members)
	switch {
	case len(conflicts) > 0:
		report.Status = WhereConflict
		report.Conflicts = conflicts
		report.Next = fmt.Sprintf("two records of the same target disagree on where it runs (%s); reconcile that project contract before deploying — do not pick one", strings.Join(conflicts, ", "))
		return report
	case len(targets) == 0:
		report.Next = "no project contract records where this product runs; ask the operator or add runtime.authority and runtime.deployment to the product's project.yaml — do not infer a deploy command from the repository"
		if len(report.Legacy) > 0 {
			report.Next += "; the frozen legacy authority rows below are a lead to confirm, not an answer"
		}
		return report
	}
	report.Targets = targets
	chosen := -1
	if len(targets) == 1 {
		chosen = 0
	} else {
		for i, target := range targets {
			if selected[target.ProductID] {
				if chosen >= 0 {
					chosen = -1
					break
				}
				chosen = i
			}
		}
	}
	if chosen < 0 {
		report.Status = WhereMultiple
		var names []string
		for _, target := range targets {
			names = append(names, target.ProductID+" → "+target.Deployment.Place)
		}
		report.Next = fmt.Sprintf("this product has %d deploy targets (%s); ask again with the target's product id or from inside its checkout", len(targets), strings.Join(names, "; "))
		return report
	}
	target := targets[chosen]
	report.Status = WhereKnown
	report.ProductID = target.ProductID
	report.Deployment = target.Deployment
	report.EvidenceRefs = target.EvidenceRefs
	if target.Deployment.Command == "" {
		report.Next = fmt.Sprintf("it runs on %s, but no deploy command is recorded; ask the operator — do not infer one from the repository", target.Deployment.Place)
	} else {
		report.Next = fmt.Sprintf("run the recorded command only against %s, then check the healthchecks", target.Deployment.Place)
	}
	return report
}

// targets collects each member's recorded deployment. Two members whose
// records come from the same source — the same checkout or repository, as
// when a contract's slug is renamed in place — describe one target: if they
// agree the newer stands, and if they disagree on the place that is a
// conflict. Members from different sources are different targets.
func (idx *whereIndex) targets(members []int) ([]WhereTarget, []string) {
	type carrier struct {
		target  WhereTarget
		seq     int64
		sources map[string]bool
	}
	var carriers []carrier
	for _, i := range members {
		slug := idx.entities[i].Slug
		place, ok := idx.claim(slug, "deploys_to")
		if !ok {
			continue
		}
		d := Deployment{Place: place.Object}
		for predicate, field := range map[string]*string{
			"deploy_method":   &d.Method,
			"deploy_command":  &d.Command,
			"deploy_rollback": &d.Rollback,
		} {
			if c, ok := idx.claim(slug, predicate); ok {
				*field = c.Object
			}
		}
		if c, ok := idx.claim(slug, "deploy_services"); ok {
			d.Services = compact(strings.Split(c.Object, ","))
		}
		if c, ok := idx.claim(slug, "deploy_healthchecks"); ok {
			d.Healthchecks = compact(strings.Split(c.Object, ","))
		}
		// A checkout path pins the exact contract directory; a repository ref
		// is shared by every contract in that repository. Use repository refs
		// only when a record carries no checkout path.
		paths, repos := map[string]bool{}, map[string]bool{}
		for _, id := range idx.entities[i].Identifiers {
			if source := sourceIdentity(id); strings.HasPrefix(source, "path:") {
				paths[source] = true
			} else if source != "" {
				repos[source] = true
			}
		}
		sources := paths
		if len(sources) == 0 {
			sources = repos
		}
		carriers = append(carriers, carrier{
			target:  WhereTarget{ProductID: slug, Deployment: d, EvidenceRefs: compact(place.EvidenceRefs)},
			seq:     place.Seq,
			sources: sources,
		})
	}
	sort.Slice(carriers, func(i, j int) bool { return carriers[i].seq > carriers[j].seq })
	var kept []carrier
	conflicts := map[string]bool{}
	for _, c := range carriers {
		same := -1
		for k, prior := range kept {
			for source := range c.sources {
				if prior.sources[source] {
					same = k
				}
			}
		}
		if same < 0 {
			kept = append(kept, c)
			continue
		}
		if kept[same].target.Deployment.Place != c.target.Deployment.Place {
			conflicts[kept[same].target.Deployment.Place] = true
			conflicts[c.target.Deployment.Place] = true
		}
	}
	var out []WhereTarget
	for _, c := range kept {
		out = append(out, c.target)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProductID < out[j].ProductID })
	var conflictList []string
	for place := range conflicts {
		conflictList = append(conflictList, place)
	}
	sort.Strings(conflictList)
	return out, conflictList
}

// sourceIdentity reduces a checkout path or repository reference to a form two
// writers agree on, so the same source is recognised however it was spelled.
// Anything else (URLs, bundle ids) is not a source and yields "".
func sourceIdentity(identifier string) string {
	id := strings.TrimSpace(identifier)
	if filepath.IsAbs(id) {
		return "path:" + filepath.Clean(id)
	}
	low := strings.ToLower(id)
	if !strings.HasPrefix(low, "git@") && !strings.HasSuffix(low, ".git") && !strings.Contains(low, "github.com/") {
		return ""
	}
	low = strings.TrimSuffix(low, ".git")
	for _, prefix := range []string{"https://", "http://", "ssh://", "git@"} {
		low = strings.TrimPrefix(low, prefix)
	}
	return "repo:" + strings.Replace(low, ":", "/", 1)
}

// legacy collects frozen bare-slug authority rows whose slug folds to one of
// the group's slugs or declared aliases.
func (idx *whereIndex) legacy(members []int) map[string]string {
	keys := map[string]bool{}
	for _, i := range members {
		keys[identityKey(idx.entities[i].Slug)] = true
		for _, alias := range idx.entities[i].Aliases {
			keys[identityKey(alias)] = true
		}
	}
	out := map[string]string{}
	var bare []string
	for entity := range idx.claims {
		if !strings.Contains(entity, ":") && keys[identityKey(entity)] {
			bare = append(bare, entity)
		}
	}
	sort.Strings(bare)
	for _, entity := range bare {
		for _, predicate := range legacyDeploymentPredicates {
			if c, ok := idx.claim(entity, predicate); ok {
				out[predicate] = c.Object
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// App binding statuses: the answer to "which app is this work for".
const (
	AppBound     = "bound"     // exactly one app matches by a recorded key
	AppNone      = "none"      // no recorded key names an app
	AppAmbiguous = "ambiguous" // recorded keys name more than one app; nothing is set
)

// App binding keys, in the order they are consulted. Each is a value the work
// itself records; none is a guess from a name.
const (
	AppKeySurfaceProduct = "surface_product" // canonical surface is product:<id>[:...]
	AppKeySurfacePath    = "surface_path"    // canonical surface is a folder
	AppKeyWorktree       = "worktree"        // the git worktree the work runs in
	AppKeyContextFolder  = "context_folder"  // the folder its context pack was built for
)

// AppKeySurfaceSystem is a canonical surface that names a registered system.
const AppKeySurfaceSystem = "surface_system"

// AppKeys are the recorded values of one piece of work that can name its app.
type AppKeys struct {
	Surface       string
	Worktree      string
	ContextFolder string
}

// AppBinding is the owner's answer for one piece of work. ProductID is the
// app's group id, set only when Status is bound; Key names the recorded key
// that decided it. Reason says why in plain words, also when nothing is set.
type AppBinding struct {
	Status    string `json:"status"`
	ProductID string `json:"product_id,omitempty"`
	// SystemID is set instead of ProductID when the one home that matches is
	// a system (a registered system: entity) rather than an app.
	SystemID   string   `json:"system_id,omitempty"`
	Key        string   `json:"key,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	Reason     string   `json:"reason"`
}

// AppResolver answers AppBinding questions against one read of the identity
// owner, so a caller binding many items reads the ledger once.
type AppResolver struct{ idx *whereIndex }

// NewAppResolver reads the identity owner once.
func NewAppResolver(store *mem.Store) (*AppResolver, error) {
	idx, err := loadWhereIndex(store)
	if err != nil {
		return nil, err
	}
	return &AppResolver{idx: idx}, nil
}

// ResolveApp is NewAppResolver(store).Resolve(keys).
func ResolveApp(store *mem.Store, keys AppKeys) (AppBinding, error) {
	r, err := NewAppResolver(store)
	if err != nil {
		return AppBinding{}, err
	}
	return r.Resolve(keys), nil
}

// Resolve sets an app only when every recorded key that names an app names
// the same one. A key that names two apps, or two keys that name different
// apps, leave it empty: a wrong label is worse than none. A product reference
// must resolve through the owner (slug, separator fold, identifier or declared
// alias); a folder must lie inside a checkout a product records, by longest
// path. A folder whose name merely looks like an app names nothing.
func (r *AppResolver) Resolve(keys AppKeys) AppBinding {
	type hit struct {
		key    string
		groups []string
	}
	var hits []hit
	if ref, ok := productSurfaceRef(keys.Surface); ok {
		hits = append(hits, hit{AppKeySurfaceProduct, r.idx.groupIDs(r.idx.match(ref))})
	} else if system := strings.TrimSpace(keys.Surface); r.idx.systems[system] {
		hits = append(hits, hit{AppKeySurfaceSystem, []string{system}})
	} else if path := absPath(keys.Surface); path != "" {
		hits = append(hits, hit{AppKeySurfacePath, r.idx.pathGroupIDs(path)})
	}
	if path := absPath(keys.Worktree); path != "" {
		hits = append(hits, hit{AppKeyWorktree, r.idx.pathGroupIDs(path)})
	}
	if path := absPath(keys.ContextFolder); path != "" {
		hits = append(hits, hit{AppKeyContextFolder, r.idx.pathGroupIDs(path)})
	}
	named := map[string]bool{}
	var said []string
	key := ""
	for _, h := range hits {
		if len(h.groups) == 0 {
			continue
		}
		if key == "" {
			key = h.key
		}
		said = append(said, h.key+" names "+strings.Join(h.groups, " and "))
		for _, g := range h.groups {
			named[g] = true
		}
	}
	var candidates []string
	for g := range named {
		candidates = append(candidates, g)
	}
	sort.Strings(candidates)
	switch len(candidates) {
	case 0:
		return AppBinding{Status: AppNone, Reason: "no recorded key names an app"}
	case 1:
		bound := AppBinding{Status: AppBound, Key: key, Reason: strings.Join(said, "; ")}
		if r.idx.systems[candidates[0]] {
			bound.SystemID = candidates[0]
		} else {
			bound.ProductID = candidates[0]
		}
		return bound
	}
	return AppBinding{Status: AppAmbiguous, Candidates: candidates, Reason: "more than one app matches (" + strings.Join(said, "; ") + "); left empty"}
}

// productSurfaceRef returns the product reference a canonical surface names:
// product:<id>, optionally followed by :<detail>. Anything else names none.
func productSurfaceRef(surface string) (string, bool) {
	surface = strings.TrimSpace(surface)
	if !strings.HasPrefix(strings.ToLower(surface), "product:") {
		return "", false
	}
	id := surface[len("product:"):]
	if cut := strings.Index(id, ":"); cut >= 0 {
		id = id[:cut]
	}
	if id == "" || strings.ContainsAny(id, "/\\ \t") {
		return "", false
	}
	return "product:" + id, true
}

func absPath(value string) string {
	value = strings.TrimSpace(value)
	if !filepath.IsAbs(value) {
		return ""
	}
	return filepath.Clean(value)
}

// groupIDs names the groups a match selected.
func (idx *whereIndex) groupIDs(matched map[int]map[string]bool) []string {
	var out []string
	for root := range matched {
		out = append(out, idx.groupID(root))
	}
	sort.Strings(out)
	return out
}

// pathGroupIDs names the homes whose recorded folder holds path, by the
// longest recorded folder: a product group, or a system whose member entity
// records the folder. Two homes at that same depth both answer.
func (idx *whereIndex) pathGroupIDs(path string) []string {
	longest := -1
	roots := map[int]bool{}
	systems := map[string]bool{}
	for _, sp := range idx.systemPaths {
		if path != sp.root && !strings.HasPrefix(path, sp.root+string(filepath.Separator)) {
			continue
		}
		switch {
		case len(sp.root) > longest:
			longest = len(sp.root)
			systems = map[string]bool{sp.system: true}
		case len(sp.root) == longest:
			systems[sp.system] = true
		}
	}
	for i, e := range idx.entities {
		for _, id := range e.Identifiers {
			if !filepath.IsAbs(id) {
				continue
			}
			root := filepath.Clean(id)
			if path != root && !strings.HasPrefix(path, root+string(filepath.Separator)) {
				continue
			}
			switch {
			case len(root) > longest:
				longest = len(root)
				roots = map[int]bool{idx.find(i): true}
				systems = map[string]bool{}
			case len(root) == longest:
				roots[idx.find(i)] = true
			}
		}
	}
	matched := map[int]map[string]bool{}
	for root := range roots {
		matched[root] = nil
	}
	out := idx.groupIDs(matched)
	for system := range systems {
		out = append(out, system)
	}
	sort.Strings(out)
	return out
}

// RunWhereCLI is `compound product where`. It lives here rather than in
// cmd/compound, which sits on its file-count ratchet; cmd/compound only
// dispatches. An unresolved or ambiguous query exits 1 so a script cannot
// mistake "no such product" for "no deployment recorded".
//
//	compound product where <product|path> [--json]
//	compound product where --all [--json]
func RunWhereCLI(args []string, store *mem.Store, stdout, stderr io.Writer) int {
	asJSON, all := false, false
	var query []string
	for _, arg := range args {
		switch arg {
		case "--json":
			asJSON = true
		case "--all":
			all = true
		default:
			query = append(query, arg)
		}
	}
	if all == (len(query) == 1) || len(query) > 1 {
		fmt.Fprintln(stderr, "usage: compound product "+WhereUsage())
		return 2
	}
	if all {
		reports, err := WhereAll(store)
		if err != nil {
			fmt.Fprintln(stderr, "product where:", err)
			return 1
		}
		if asJSON {
			return writeWhereJSON(stdout, stderr, reports)
		}
		counts := map[string]int{}
		for _, r := range reports {
			counts[r.Status]++
		}
		fmt.Fprintf(stdout, "# product where --all: %d products, %d known, %d multiple, %d unknown, %d conflict\n", len(reports), counts[WhereKnown], counts[WhereMultiple], counts[WhereUnknown], counts[WhereConflict])
		for _, r := range reports {
			line := fmt.Sprintf("- %s [%s]", r.ProductID, r.Status)
			if r.Deployment.Place != "" {
				line += " " + r.Deployment.Place
			} else if len(r.Targets) > 1 {
				var places []string
				for _, target := range r.Targets {
					places = append(places, target.ProductID+"→"+target.Deployment.Place)
				}
				line += " " + strings.Join(places, "; ")
			}
			if len(r.Conflicts) > 0 {
				line += " " + strings.Join(r.Conflicts, " vs ")
			}
			var also []string
			for _, m := range r.Members {
				if m != r.ProductID {
					also = append(also, m)
				}
			}
			if len(also) > 0 {
				line += " (also " + strings.Join(also, ", ") + ")"
			}
			fmt.Fprintln(stdout, line)
		}
		return 0
	}
	ref := query[0]
	if ref == "." || strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../") {
		if abs, err := filepath.Abs(ref); err == nil {
			ref = abs
		}
	}
	if strings.HasPrefix(ref, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			ref = filepath.Join(home, ref[2:])
		}
	}
	report, err := Where(store, ref)
	if err != nil {
		fmt.Fprintln(stderr, "product where:", err)
		return 1
	}
	code := 0
	if report.Status == WhereUnresolved || report.Status == WhereAmbiguous {
		code = 1
	}
	if asJSON {
		if rc := writeWhereJSON(stdout, stderr, report); rc != 0 {
			return rc
		}
		return code
	}
	fmt.Fprintf(stdout, "# product where %s\n\n- status: %s\n", report.Query, report.Status)
	field := func(name, value string) {
		if strings.TrimSpace(value) != "" {
			fmt.Fprintf(stdout, "- %s: %s\n", name, value)
		}
	}
	field("product", report.ProductID)
	if len(report.Members) > 1 {
		field("members", strings.Join(report.Members, ", "))
	}
	field("place", report.Deployment.Place)
	field("method", report.Deployment.Method)
	field("command", report.Deployment.Command)
	field("rollback", report.Deployment.Rollback)
	field("services", strings.Join(report.Deployment.Services, ", "))
	field("healthchecks", strings.Join(report.Deployment.Healthchecks, ", "))
	if report.Status == WhereMultiple {
		for _, target := range report.Targets {
			line := target.ProductID + " → " + target.Deployment.Place
			if target.Deployment.Command != "" {
				line += " (" + target.Deployment.Command + ")"
			}
			field("target", line)
		}
	}
	field("conflicts", strings.Join(report.Conflicts, ", "))
	var legacy []string
	for _, predicate := range legacyDeploymentPredicates {
		if value, ok := report.Legacy[predicate]; ok {
			legacy = append(legacy, predicate+"="+value)
		}
	}
	field("legacy_authority", strings.Join(legacy, "; "))
	field("surfaces", strings.Join(report.Surfaces, ", "))
	field("evidence", strings.Join(report.EvidenceRefs, ", "))
	field("next", report.Next)
	return code
}

// WhereUsage is the verb's usage line, appended to productUsage().
func WhereUsage() string {
	return "where <product|path> [--json] | where --all [--json]"
}

func writeWhereJSON(stdout, stderr io.Writer, value any) int {
	blob, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "product where:", err)
		return 1
	}
	fmt.Fprintln(stdout, string(blob))
	return 0
}
