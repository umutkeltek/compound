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
	Resources     []InventoryResourceView `json:"resources"`
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
	rank       int     // 0 snapshot, 1 failed source, 2 sign-in source, 3 warning
	past       float64 // how far past the threshold, relative
}

// InventoryChanges compares the snapshot with the one before it.
type InventoryChanges struct {
	PreviousObservedAt string                `json:"previous_observed_at"`
	Added              []InventoryChangeRef  `json:"added"`
	Removed            []InventoryChangeRef  `json:"removed"`
	FactChanges        []InventoryFactChange `json:"fact_changes"`
	Note               string                `json:"note,omitempty"`
}

// InventoryChangeRef names an added or removed resource.
type InventoryChangeRef struct {
	ResourceID string `json:"resource_id"`
	Kind       string `json:"kind"`
	Label      string `json:"label"`
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
	ID       string              `json:"id"`
	Kind     string              `json:"kind"`
	Provider string              `json:"provider"`
	Label    string              `json:"label"`
	Parent   string              `json:"parent"`
	Children int                 `json:"children"`
	Places   []string            `json:"places"`
	Links    []InventoryLink     `json:"links"`
	Facts    []InventoryFactView `json:"facts"`
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
		Changes:       InventoryChanges{Added: []InventoryChangeRef{}, Removed: []InventoryChangeRef{}, FactChanges: []InventoryFactChange{}},
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
	observedAt, _ := time.Parse(time.RFC3339, obs.ObservedAt)
	age := math.Max(0, now.Sub(observedAt).Hours())
	ageRounded := math.Round(age*10) / 10
	p.Snapshot = InventorySnapshotStatus{State: "fresh", ObservedAt: obs.ObservedAt, ImportedAt: snap.ImportedAt, AgeHours: &ageRounded}
	if age > q.StaleHours {
		p.Snapshot.State = "stale"
		p.Snapshot.Notice = fmt.Sprintf("The inventory was observed %s ago, more than %s hours: values may be out of date; %s.",
			humanInventoryAge(age), strconv.FormatFloat(q.StaleHours, 'f', -1, 64), inventoryRefreshHint)
		p.Attention = append(p.Attention, InventoryAttention{Kind: "snapshot", Title: "Inventory snapshot", Why: p.Snapshot.Notice})
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
				Why: "source failed" + inventoryDetailSuffix(s.Detail), rank: 1})
		case "needs_sign_in":
			p.Attention = append(p.Attention, InventoryAttention{Label: s.ID, Kind: "source", Title: "Source " + s.ID,
				Why: "source needs sign-in" + inventoryDetailSuffix(s.Detail), rank: 2})
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
		fallbackAt := sourceAt[r.Source]
		if fallbackAt == "" {
			fallbackAt = obs.ObservedAt
		}
		view := InventoryResourceView{
			ID: r.ID, Kind: r.Kind, Provider: r.Provider, Label: r.Label, Parent: r.Parent,
			Children: children[r.ID], Places: nonNilStrings(r.Places), Links: r.Links,
			Facts: inventoryFactViews(kind, r.Facts, fallbackAt),
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
				p.Attention = append(p.Attention, InventoryAttention{
					ResourceID: r.ID, Label: r.Label, Kind: r.Kind, Fact: f.Name, Title: f.Title,
					Value: f.Value, Unit: f.Unit,
					Why: fmt.Sprintf("%s is %s, %s %s", f.Title, formatInventoryValue(f.Value, f.Unit),
						spec.WarnWhen, formatInventoryValue(*spec.WarnAt, f.Unit)),
					rank: 3, past: inventoryPastThreshold(f.Value, spec),
				})
			}
		}
		p.Summary.Resources++
		p.Summary.ByKind[r.Kind]++
		p.Summary.ByProvider[r.Provider]++
		p.Resources = append(p.Resources, view)
	}
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
		p.Changes = diffInventory(kinds, inventoryFilter(prev.Observation.Resources, q), current)
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
// past its kind's threshold, or 0 when it is not past it.
func inventoryPastThreshold(value any, spec InventoryFactSpec) float64 {
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
func diffInventory(kinds InventoryKindSet, before, after []InventoryObservedResource) InventoryChanges {
	ch := InventoryChanges{Added: []InventoryChangeRef{}, Removed: []InventoryChangeRef{}, FactChanges: []InventoryFactChange{}}
	prev := map[string]InventoryObservedResource{}
	for _, r := range before {
		prev[r.ID] = r
	}
	next := map[string]bool{}
	for _, r := range after {
		next[r.ID] = true
		old, existed := prev[r.ID]
		if !existed {
			ch.Added = append(ch.Added, InventoryChangeRef{ResourceID: r.ID, Kind: r.Kind, Label: r.Label})
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
			ch.Removed = append(ch.Removed, InventoryChangeRef{ResourceID: r.ID, Kind: r.Kind, Label: r.Label})
		}
	}
	return ch
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
	switch {
	case hours < 1:
		return fmt.Sprintf("%d minutes", int(math.Round(hours*60)))
	case hours < 48:
		return strconv.FormatFloat(math.Round(hours*10)/10, 'f', -1, 64) + " hours"
	default:
		return strconv.FormatFloat(math.Round(hours/24*10)/10, 'f', -1, 64) + " days"
	}
}

func formatInventoryNumber(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(math.Round(f*100)/100, 'f', -1, 64)
}
