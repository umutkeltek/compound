package projection

// The read side of the system inventory: one projection over the stored
// snapshot (and the one before it, for changes). It reads only files under
// COMPOUND_HOME; it makes no external call and needs no credentials.
//
// Unknown is a first-class answer. Every declared fact of a resource's kind is
// listed; a fact the collector did not report is "not_reported", and a fact
// whose state is not "ok" never carries a value, so a missing number can never
// render as a number.

import (
	"encoding/json"
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
	// InventorySchemaVersion names the read projection.
	InventorySchemaVersion = "compound.system_inventory.v0.1"
	// DefaultInventoryStaleHours is how old a snapshot may be before it is stale.
	DefaultInventoryStaleHours = 36.0

	inventoryBoundary = "read-only projection over one observed snapshot under COMPOUND_HOME/observed/inventory; " +
		"no external call, no credentials; values are what the collector observed, not verified truth"
	inventoryNotReportedDetail = "not known: the collector did not report it"
	inventoryRefreshHint       = "run your collector to write an observation file, then `compound system inventory refresh --collector-json FILE`"
)

// InventoryQuery narrows the projection.
type InventoryQuery struct {
	Kind       string
	Provider   string
	StaleHours float64
	// Products resolves product references (the CLI passes Compound's product
	// identity owner); nil leaves every product reference unknown.
	Products InventoryProductResolver
}

// InventoryProjection is compound.system_inventory.v0.1.
type InventoryProjection struct {
	SchemaVersion string                  `json:"schema_version"`
	GeneratedAt   string                  `json:"generated_at"`
	Boundary      string                  `json:"boundary"`
	Snapshot      InventorySnapshotStatus `json:"snapshot"`
	OverlayError  string                  `json:"overlay_error"`
	Filter        InventoryFilter         `json:"filter"`
	Kinds         []InventoryKind         `json:"kinds"`
	Sources       []InventorySource       `json:"sources"`
	Summary       InventorySummary        `json:"summary"`
	Attention     []InventoryAttention    `json:"attention"`
	Changes       InventoryChanges        `json:"changes"`
	// Shared lists the pieces several things depend on (top 15, products
	// first); Questions lists what is not known and the declared line that
	// settles it. Both come from the graph over the whole snapshot, narrowed by
	// the kind and provider filter. Questions are ranked by how many products
	// and things depend on what they are about. CoverageGaps are facts no
	// reader reports for any thing of a kind: one line each, not one question
	// per thing.
	Shared       []InventoryShared       `json:"shared"`
	Questions    []InventoryQuestion     `json:"questions"`
	CoverageGaps []InventoryCoverageGap  `json:"coverage_gaps"`
	Resources    []InventoryResourceView `json:"resources"`
}

// InventorySnapshotStatus says how old the stored snapshot is.
type InventorySnapshotStatus struct {
	State      string   `json:"state"` // fresh | stale | missing
	ObservedAt string   `json:"observed_at"`
	ImportedAt string   `json:"imported_at"`
	AgeHours   *float64 `json:"age_hours"`
	Notice     string   `json:"notice"`
}

// InventoryFilter echoes the query.
type InventoryFilter struct {
	Kind       string  `json:"kind"`
	Provider   string  `json:"provider"`
	StaleHours float64 `json:"stale_hours"`
}

// InventorySummary counts what the projection shows.
type InventorySummary struct {
	Resources     int            `json:"resources"`
	ByKind        map[string]int `json:"by_kind"`
	ByProvider    map[string]int `json:"by_provider"`
	FactsKnown    int            `json:"facts_known"`
	FactsNotKnown int            `json:"facts_not_known"`
	Carried       int            `json:"carried"`
}

// InventoryAttention is one thing to look at: a stale or missing snapshot, a
// source that failed or needs sign-in, or a fact past its warning threshold.
type InventoryAttention struct {
	ResourceID string  `json:"resource_id"`
	Label      string  `json:"label"`
	Kind       string  `json:"kind"`
	Fact       string  `json:"fact"`
	Title      string  `json:"title"`
	Value      any     `json:"value"`
	Unit       string  `json:"unit"`
	Why        string  `json:"why"`
	rank       int     // 0 snapshot, 1 failed source, 2 sign-in source, 3 warning, 4 failed or sign-in fact
	past       float64 // how far past the threshold, relative
}

// InventoryChanges compares the snapshot with the one before it.
type InventoryChanges struct {
	PreviousObservedAt string                `json:"previous_observed_at"`
	Added              []InventoryChangeRef  `json:"added"`
	Removed            []InventoryChangeRef  `json:"removed"`
	FactChanges        []InventoryFactChange `json:"fact_changes"`
	LinksAdded         []InventoryLinkChange `json:"links_added"`
	LinksRemoved       []InventoryLinkChange `json:"links_removed"`
	Note               string                `json:"note,omitempty"`
}

// InventoryLinkChange is one link (resource, relation, target) that appeared
// or disappeared since the previous import. A parent counts as a runs_on link.
type InventoryLinkChange struct {
	ResourceID string `json:"resource_id"`
	Label      string `json:"label"`
	Rel        string `json:"rel"`
	To         string `json:"to"`
	Confidence string `json:"confidence"`
}

// InventoryChangeRef names an added or removed resource.
type InventoryChangeRef struct {
	ResourceID string `json:"resource_id"`
	Kind       string `json:"kind"`
	Label      string `json:"label"`
	// Reason is set when refresh dropped a carried resource.
	Reason string `json:"reason,omitempty"`
}

// InventoryFactChange is one fact whose value or state changed.
type InventoryFactChange struct {
	ResourceID string `json:"resource_id"`
	Label      string `json:"label"`
	Fact       string `json:"fact"`
	From       any    `json:"from"`
	To         any    `json:"to"`
	FromState  string `json:"from_state"`
	ToState    string `json:"to_state"`
}

// InventoryResourceView is one resource with every fact its kind declares.
type InventoryResourceView struct {
	ID       string          `json:"id"`
	Kind     string          `json:"kind"`
	Provider string          `json:"provider"`
	Label    string          `json:"label"`
	Parent   string          `json:"parent"`
	Children int             `json:"children"`
	Places   []string        `json:"places"`
	Links    []InventoryLink `json:"links"`
	// ObservedAt is when the resource was last really seen, when its source
	// says so (older evidence); "" means as of its source's read.
	ObservedAt string `json:"observed_at"`
	// Carried is true when the resource is shown from the last good read
	// (LastObservedAt) of a source that did not answer this time.
	Carried        bool                `json:"carried"`
	LastObservedAt string              `json:"last_observed_at"`
	Facts          []InventoryFactView `json:"facts"`
}

// InventoryFactView is one fact as shown. Value is nil unless State is "ok".
type InventoryFactView struct {
	Name       string `json:"name"`
	Title      string `json:"title"`
	Group      string `json:"group"`
	Value      any    `json:"value"`
	Unit       string `json:"unit"`
	State      string `json:"state"`
	Detail     string `json:"detail"`
	ObservedAt string `json:"observed_at"`
	Warn       bool   `json:"warn"`
}

// BuildInventory projects the stored snapshot. It fails only on an invalid
// embedded kinds file or an unknown --kind; a missing or unreadable snapshot
// is a "missing" state with a notice, never an error.
func BuildInventory(home string, q InventoryQuery, now time.Time) (InventoryProjection, error) {
	if q.StaleHours <= 0 {
		q.StaleHours = DefaultInventoryStaleHours
	}
	kinds, err := LoadInventoryKinds(home)
	if err != nil {
		return InventoryProjection{}, err
	}
	if q.Kind != "" {
		if _, ok := kinds.Kind(q.Kind); !ok {
			return InventoryProjection{}, fmt.Errorf("unknown kind %q (known kinds: %s)", q.Kind, strings.Join(kinds.KindIDs(), ", "))
		}
	}
	p := InventoryProjection{
		SchemaVersion: InventorySchemaVersion,
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Boundary:      inventoryBoundary,
		OverlayError:  kinds.OverlayError,
		Filter:        InventoryFilter{Kind: q.Kind, Provider: q.Provider, StaleHours: q.StaleHours},
		Kinds:         kinds.Kinds,
		Sources:       []InventorySource{},
		Summary:       InventorySummary{ByKind: map[string]int{}, ByProvider: map[string]int{}},
		Attention:     []InventoryAttention{},
		Changes:       emptyInventoryChanges(),
		Shared:        []InventoryShared{},
		Questions:     []InventoryQuestion{},
		CoverageGaps:  []InventoryCoverageGap{},
		Resources:     []InventoryResourceView{},
	}
	snap, err := LoadInventorySnapshot(home)
	if err != nil {
		p.Snapshot.State = "missing"
		if errors.Is(err, os.ErrNotExist) {
			p.Snapshot.Notice = "No inventory snapshot yet: " + inventoryRefreshHint + "."
		} else {
			p.Snapshot.Notice = "The inventory snapshot could not be read (" + err.Error() + "): " + inventoryRefreshHint + "."
		}
		p.Attention = append(p.Attention, InventoryAttention{Kind: "snapshot", Title: "Inventory snapshot", Why: p.Snapshot.Notice})
		return p, nil
	}

	obs := snap.Observation
	p.Snapshot = inventorySnapshotState(snap, now, q.StaleHours)
	if p.Snapshot.State == "stale" {
		p.Attention = append(p.Attention, InventoryAttention{Kind: "snapshot", Title: "Inventory snapshot", Why: p.Snapshot.Notice})
	}

	// Carried resources per source, and the oldest last good read among them.
	carriedBySource := map[string]int{}
	carriedSince := map[string]time.Time{}
	for _, r := range obs.Resources {
		if !r.Carried {
			continue
		}
		carriedBySource[r.Source]++
		if at, err := time.Parse(time.RFC3339, r.LastObservedAt); err == nil {
			if old, ok := carriedSince[r.Source]; !ok || at.Before(old) {
				carriedSince[r.Source] = at
			}
		}
	}
	carriedNote := func(source string) string {
		n := carriedBySource[source]
		if n == 0 {
			return ""
		}
		note := fmt.Sprintf("; %d resources shown from the last good read", n)
		if at, ok := carriedSince[source]; ok {
			note += ", " + humanInventoryAge(math.Max(0, now.Sub(at).Hours())) + " ago"
		}
		return note
	}

	sourceAt := map[string]string{}
	for _, s := range obs.Sources {
		sourceAt[s.ID] = s.ObservedAt
		if q.Provider != "" && s.Provider != q.Provider {
			continue
		}
		p.Sources = append(p.Sources, s)
		switch s.State {
		case "failed":
			p.Attention = append(p.Attention, InventoryAttention{Label: s.ID, Kind: "source", Title: "Source " + s.ID,
				Why: "source failed" + inventoryDetailSuffix(s.Detail) + carriedNote(s.ID), rank: 1})
		case "needs_sign_in":
			p.Attention = append(p.Attention, InventoryAttention{Label: s.ID, Kind: "source", Title: "Source " + s.ID,
				Why: "source needs sign-in" + inventoryDetailSuffix(s.Detail) + carriedNote(s.ID), rank: 2})
		}
	}

	children := map[string]int{}
	for _, r := range obs.Resources {
		if r.Parent != "" {
			children[r.Parent]++
		}
	}
	kindOrder := map[string]int{}
	for i, k := range kinds.Kinds {
		kindOrder[k.ID] = i
	}
	current := inventoryFilter(obs.Resources, q)
	for _, r := range current {
		kind, _ := kinds.Kind(r.Kind)
		fallbackAt := r.ObservedAt
		if fallbackAt == "" {
			fallbackAt = sourceAt[r.Source]
		}
		if fallbackAt == "" {
			fallbackAt = obs.ObservedAt
		}
		view := InventoryResourceView{
			ID: r.ID, Kind: r.Kind, Provider: r.Provider, Label: r.Label, Parent: r.Parent,
			Children: children[r.ID], Places: nonNilStrings(r.Places), Links: r.Links,
			ObservedAt: r.ObservedAt, Carried: r.Carried, LastObservedAt: r.LastObservedAt,
			Facts: inventoryFactViews(kind, r.Facts, fallbackAt),
		}
		if r.Carried {
			p.Summary.Carried++
		}
		if view.Links == nil {
			view.Links = []InventoryLink{}
		}
		for _, f := range view.Facts {
			if f.State == "ok" {
				p.Summary.FactsKnown++
			} else {
				p.Summary.FactsNotKnown++
			}
			if f.Warn {
				spec, _ := kind.Fact(f.Name)
				why := f.Title + " is " + formatInventoryValue(f.Value, f.Unit)
				if spec.WarnAt != nil {
					why += fmt.Sprintf(", %s %s", spec.WarnWhen, formatInventoryValue(*spec.WarnAt, f.Unit))
				}
				p.Attention = append(p.Attention, InventoryAttention{
					ResourceID: r.ID, Label: r.Label, Kind: r.Kind, Fact: f.Name, Title: f.Title,
					Value: f.Value, Unit: f.Unit, Why: why,
					rank: 3, past: inventoryPastThreshold(f.Value, spec),
				})
			}
			// A fact that failed or needs sign-in is something to fix. A fact
			// that needs the owner, costs money, is not supported or was not
			// reported is a standing unknown and is not an attention row.
			if f.State == "failed" || f.State == "needs_sign_in" {
				why := f.Title + " " + strings.ReplaceAll(strings.Replace(f.State, "needs_sign_in", "needs sign-in", 1), "_", " ")
				why += inventoryDetailSuffix(f.Detail)
				if at, err := time.Parse(time.RFC3339, f.ObservedAt); err == nil {
					why += fmt.Sprintf(" (read %s ago)", humanInventoryAge(math.Max(0, now.Sub(at).Hours())))
				}
				p.Attention = append(p.Attention, InventoryAttention{
					ResourceID: r.ID, Label: r.Label, Kind: r.Kind, Fact: f.Name, Title: f.Title,
					Unit: f.Unit, Why: why, rank: 4,
				})
			}
		}
		p.Summary.Resources++
		p.Summary.ByKind[r.Kind]++
		p.Summary.ByProvider[r.Provider]++
		p.Resources = append(p.Resources, view)
	}
	p.addGraphFindings(buildInventoryGraph(obs, kinds, q.Products), q)
	sort.SliceStable(p.Resources, func(i, j int) bool {
		a, b := p.Resources[i], p.Resources[j]
		if kindOrder[a.Kind] != kindOrder[b.Kind] {
			return kindOrder[a.Kind] < kindOrder[b.Kind]
		}
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		return a.ID < b.ID
	})
	sort.SliceStable(p.Attention, func(i, j int) bool {
		a, b := p.Attention[i], p.Attention[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.past != b.past {
			return a.past > b.past
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		return a.Fact < b.Fact
	})

	prev, err := LoadPreviousInventorySnapshot(home)
	switch {
	case err == nil:
		p.Changes = diffInventory(inventoryFilter(prev.Observation.Resources, q), current, snap.Dropped)
		p.Changes.PreviousObservedAt = prev.Observation.ObservedAt
	case errors.Is(err, os.ErrNotExist):
		p.Changes.Note = "no previous import to compare with"
	default:
		p.Changes.Note = "the previous snapshot could not be read: " + err.Error()
	}
	return p, nil
}

func inventoryFilter(resources []InventoryObservedResource, q InventoryQuery) []InventoryObservedResource {
	out := make([]InventoryObservedResource, 0, len(resources))
	for _, r := range resources {
		if q.Kind != "" && r.Kind != q.Kind {
			continue
		}
		if q.Provider != "" && r.Provider != q.Provider {
			continue
		}
		out = append(out, r)
	}
	return out
}

// inventoryFactViews lists every declared fact in kind order, then the
// undeclared ("other") facts by name. A non-ok fact never carries a value.
func inventoryFactViews(kind InventoryKind, facts map[string]InventoryObservedFact, fallbackAt string) []InventoryFactView {
	out := make([]InventoryFactView, 0, len(kind.Facts)+len(facts))
	for _, spec := range kind.Facts {
		f, reported := facts[spec.Name]
		if !reported {
			out = append(out, InventoryFactView{
				Name: spec.Name, Title: spec.Title, Group: spec.Group, Unit: spec.Unit,
				State: inventoryFactNotReported, Detail: inventoryNotReportedDetail,
			})
			continue
		}
		v := inventoryFactView(spec.Name, spec.Title, spec.Group, spec.Unit, f, fallbackAt)
		v.Warn = inventoryPastThreshold(v.Value, spec) > 0
		out = append(out, v)
	}
	for _, name := range sortedFactNames(facts) {
		if _, declared := kind.Fact(name); declared {
			continue
		}
		out = append(out, inventoryFactView(name, name, InventoryGroupOther, "", facts[name], fallbackAt))
	}
	return out
}

func inventoryFactView(name, title, group, unit string, f InventoryObservedFact, fallbackAt string) InventoryFactView {
	v := InventoryFactView{Name: name, Title: title, Group: group, Unit: unit, State: f.State, Detail: f.Detail, ObservedAt: f.ObservedAt}
	if f.Unit != "" {
		v.Unit = f.Unit
	}
	if v.ObservedAt == "" {
		v.ObservedAt = fallbackAt
	}
	if f.State == "ok" {
		v.Value = f.Value
	} else if v.Detail == "" {
		v.Detail = "not known: " + strings.ReplaceAll(f.State, "_", " ")
	}
	return v
}

// inventoryPastThreshold returns how far (relative) an ok numeric value is
// past its kind's threshold, or 0 when it is not past it. Under the boolean
// rule is_false, a value of no is 1 past (and yes, or not a bool, is 0).
func inventoryPastThreshold(value any, spec InventoryFactSpec) float64 {
	if spec.WarnWhen == inventoryWarnIsFalse {
		if b, ok := value.(bool); ok && !b {
			return 1
		}
		return 0
	}
	if spec.WarnAt == nil {
		return 0
	}
	v, ok := inventoryNumber(value)
	if !ok {
		return 0
	}
	w := *spec.WarnAt
	var past float64
	switch spec.WarnWhen {
	case "above":
		past = v - w
	case "below":
		past = w - v
	default:
		return 0
	}
	if past <= 0 {
		return 0
	}
	if w != 0 {
		past /= math.Abs(w)
	}
	return past
}

func inventoryNumber(value any) (float64, bool) {
	switch n := value.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

// diffInventory reports added and removed resources and, for resources in
// both, facts whose state or value changed (observed_at alone is not a change).
// A carried resource is never added or removed; a removed resource that refresh
// dropped carries the drop reason.
func diffInventory(before, after []InventoryObservedResource, dropped []InventoryChangeRef) InventoryChanges {
	ch := emptyInventoryChanges()
	ch.LinksAdded, ch.LinksRemoved = diffInventoryLinks(before, after)
	reasons := map[string]string{}
	for _, d := range dropped {
		reasons[d.ResourceID] = d.Reason
	}
	prev := map[string]InventoryObservedResource{}
	for _, r := range before {
		prev[r.ID] = r
	}
	next := map[string]bool{}
	for _, r := range after {
		next[r.ID] = true
		old, existed := prev[r.ID]
		if !existed {
			if !r.Carried {
				ch.Added = append(ch.Added, InventoryChangeRef{ResourceID: r.ID, Kind: r.Kind, Label: r.Label})
			}
			continue
		}
		names := map[string]bool{}
		for n := range old.Facts {
			names[n] = true
		}
		for n := range r.Facts {
			names[n] = true
		}
		sorted := make([]string, 0, len(names))
		for n := range names {
			sorted = append(sorted, n)
		}
		sort.Strings(sorted)
		for _, n := range sorted {
			fromV, fromS := inventoryDiffSide(old.Facts, n)
			toV, toS := inventoryDiffSide(r.Facts, n)
			if fromS == toS && inventoryValueEqual(fromV, toV) {
				continue
			}
			ch.FactChanges = append(ch.FactChanges, InventoryFactChange{
				ResourceID: r.ID, Label: r.Label, Fact: n, From: fromV, To: toV, FromState: fromS, ToState: toS,
			})
		}
	}
	for _, r := range before {
		if !next[r.ID] {
			ch.Removed = append(ch.Removed, InventoryChangeRef{ResourceID: r.ID, Kind: r.Kind, Label: r.Label, Reason: reasons[r.ID]})
		}
	}
	return ch
}

func emptyInventoryChanges() InventoryChanges {
	return InventoryChanges{Added: []InventoryChangeRef{}, Removed: []InventoryChangeRef{}, FactChanges: []InventoryFactChange{},
		LinksAdded: []InventoryLinkChange{}, LinksRemoved: []InventoryLinkChange{}}
}

// diffInventoryLinks compares the (resource, relation, target) links of two
// snapshots; a parent counts as a runs_on link and the same link twice counts
// once. A carried resource keeps its links, so carrying changes none.
func diffInventoryLinks(before, after []InventoryObservedResource) (added, removed []InventoryLinkChange) {
	keyed := func(resources []InventoryObservedResource) (map[string]InventoryLinkChange, []string) {
		out := map[string]InventoryLinkChange{}
		var keys []string
		for _, r := range resources {
			links := r.Links
			if r.Parent != "" {
				links = append([]InventoryLink{{Rel: "runs_on", To: r.Parent}}, links...)
			}
			for _, l := range links {
				rel := l.Rel
				if rel == InventoryRelParent {
					rel = "runs_on"
				}
				key := r.ID + "\x00" + rel + "\x00" + l.To
				if old, seen := out[key]; seen {
					if inventoryConfidenceRank(l.Confidence) > inventoryConfidenceRank(old.Confidence) {
						old.Confidence = InventoryLinkConfidence(l)
						out[key] = old
					}
					continue
				}
				out[key] = InventoryLinkChange{ResourceID: r.ID, Label: r.Label, Rel: rel, To: l.To, Confidence: InventoryLinkConfidence(l)}
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		return out, keys
	}
	prev, prevKeys := keyed(before)
	next, nextKeys := keyed(after)
	added, removed = []InventoryLinkChange{}, []InventoryLinkChange{}
	for _, k := range nextKeys {
		if _, ok := prev[k]; !ok {
			added = append(added, next[k])
		}
	}
	for _, k := range prevKeys {
		if _, ok := next[k]; !ok {
			removed = append(removed, prev[k])
		}
	}
	return added, removed
}

func inventoryDiffSide(facts map[string]InventoryObservedFact, name string) (any, string) {
	f, ok := facts[name]
	if !ok {
		return nil, inventoryFactNotReported
	}
	if f.State != "ok" {
		return nil, f.State
	}
	return f.Value, f.State
}

func inventoryValueEqual(a, b any) bool {
	if fa, ok := inventoryNumber(a); ok {
		fb, ok := inventoryNumber(b)
		return ok && fa == fb
	}
	return a == b
}

func inventoryDetailSuffix(detail string) string {
	if strings.TrimSpace(detail) == "" {
		return ""
	}
	return ": " + detail
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func humanInventoryAge(hours float64) string {
	unit := func(v float64, one, many string) string {
		if v == 1 {
			return "1 " + one
		}
		return strconv.FormatFloat(v, 'f', -1, 64) + " " + many
	}
	switch {
	case hours < 1:
		return unit(math.Round(hours*60), "minute", "minutes")
	case hours < 48:
		return unit(math.Round(hours*10)/10, "hour", "hours")
	default:
		return unit(math.Round(hours/24*10)/10, "day", "days")
	}
}

func formatInventoryNumber(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(math.Round(f*100)/100, 'f', -1, 64)
}
