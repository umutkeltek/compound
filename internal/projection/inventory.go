package projection

// System inventory: accounts, hosts, guests, services, domains, sites, apps
// and paid plans, as one collector last observed them.
//
// Compound only reads. A collector outside the core binary runs the user's
// own signed-in, read-only listing tools and writes one observation file
// (compound.system_inventory_observation.v0.2; v0.1 still imports). `compound system inventory
// refresh --collector-json FILE` validates it against the contract and the
// kinds, and stores it under COMPOUND_HOME/observed/inventory/ (snapshot.json,
// with the one before kept as previous.json). The same evidence rules as the
// door map apply: observed, never executed; names and numbers, never
// credential values; a stale or missing snapshot is a visible notice.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// InventoryObservationSchemaVersion names the collector's observation file.
	// v0.2 adds typed links (confidence, evidence, observed_at), resource
	// observed_at and product references; a v0.1 file still imports.
	InventoryObservationSchemaVersion = "compound.system_inventory_observation.v0.2"
	inventoryObservationSchemaV01     = "compound.system_inventory_observation.v0.1"
	// InventorySnapshotSchemaVersion names the stored snapshot (observation + imported_at).
	InventorySnapshotSchemaVersion = "compound.system_inventory_snapshot.v0.1"
	// InventoryRefreshSchemaVersion names the refresh command's JSON result.
	InventoryRefreshSchemaVersion = "compound.system_inventory_refresh.v0.1"

	inventoryDir             = "observed/inventory"
	inventorySnapshotFile    = "snapshot.json"
	inventoryPreviousFile    = "previous.json"
	maxInventoryBytes        = 32 << 20
	maxInventoryProblems     = 20
	inventoryFactNotReported = "not_reported"
	maxInventoryEvidence     = 200
)

// Link confidence: observed (a command or config file shows it now), declared
// (a person or a written record says it), inferred (concluded from a name or a
// match). A link without one is observed.
const (
	InventoryConfidenceObserved = "observed"
	InventoryConfidenceDeclared = "declared"
	InventoryConfidenceInferred = "inferred"
)

// InventoryProductPrefix starts a product reference: a link to a Compound
// product, or the id of a product resource that carries the product's links.
const InventoryProductPrefix = "product:"

// Source states a collector may report.
var inventorySourceStates = map[string]bool{
	"ok": true, "failed": true, "needs_sign_in": true, "needs_owner": true,
	"costs_money": true, "not_supported": true, "skipped": true,
}

// Fact states a collector may report. not_reported is reader-only.
var inventoryFactStates = map[string]bool{
	"ok": true, "needs_sign_in": true, "needs_owner": true,
	"costs_money": true, "not_supported": true, "failed": true,
}

// InventoryObservation is the collector's file.
type InventoryObservation struct {
	SchemaVersion string                      `json:"schema_version"`
	ObservedAt    string                      `json:"observed_at"`
	Collector     InventoryCollector          `json:"collector"`
	Sources       []InventorySource           `json:"sources"`
	Resources     []InventoryObservedResource `json:"resources"`
}

// InventoryCollector names the program that wrote the observation.
type InventoryCollector struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InventorySource is one thing the collector tried to read, and how it went.
type InventorySource struct {
	ID         string `json:"id"`
	Provider   string `json:"provider"`
	State      string `json:"state"`
	Detail     string `json:"detail"`
	ObservedAt string `json:"observed_at"`
	DurationMS int64  `json:"duration_ms"`
}

// InventoryObservedResource is one resource as observed.
type InventoryObservedResource struct {
	ID       string                           `json:"id"`
	Kind     string                           `json:"kind"`
	Provider string                           `json:"provider"`
	Label    string                           `json:"label"`
	Parent   string                           `json:"parent"`
	Source   string                           `json:"source"`
	Places   []string                         `json:"places"`
	Links    []InventoryLink                  `json:"links"`
	Facts    map[string]InventoryObservedFact `json:"facts"`
	// ObservedAt (v0.2, optional) is when the resource was last really seen,
	// for a source that carries older evidence.
	ObservedAt string `json:"observed_at,omitempty"`
	// Carried and LastObservedAt are set by refresh, never by a collector: a
	// carried resource is shown from the last good read of a source that did
	// not answer this time, with its facts unchanged.
	Carried        bool   `json:"carried,omitempty"`
	LastObservedAt string `json:"last_observed_at,omitempty"`
}

// InventoryLink relates a resource to another resource (v0.2: a resource in
// the same file or a product:<id> reference; v0.1 also allowed a place).
// Confidence, Evidence and ObservedAt are v0.2; Evidence is a short pointer
// (a repo-relative file, a command name), never a value.
type InventoryLink struct {
	Rel        string `json:"rel"`
	To         string `json:"to"`
	Confidence string `json:"confidence,omitempty"`
	Evidence   string `json:"evidence,omitempty"`
	ObservedAt string `json:"observed_at,omitempty"`
}

// InventoryLinkConfidence is the link's confidence, observed when missing.
func InventoryLinkConfidence(l InventoryLink) string {
	if l.Confidence == "" {
		return InventoryConfidenceObserved
	}
	return l.Confidence
}

func inventoryConfidenceRank(c string) int {
	switch c {
	case InventoryConfidenceObserved, "":
		return 3
	case InventoryConfidenceDeclared:
		return 2
	case InventoryConfidenceInferred:
		return 1
	}
	return 0
}

// InventoryObservedFact is one fact with how and when it was read. Value is a
// json.Number, string, bool or nil; it is nil whenever State is not "ok".
type InventoryObservedFact struct {
	Value      any    `json:"value"`
	Unit       string `json:"unit"`
	State      string `json:"state"`
	Detail     string `json:"detail"`
	ObservedAt string `json:"observed_at"`
}

// InventorySnapshot is what refresh stores: the validated observation plus
// when it was imported.
type InventorySnapshot struct {
	SchemaVersion string               `json:"schema_version"`
	ImportedAt    string               `json:"imported_at"`
	Observation   InventoryObservation `json:"observation"`
	// Dropped lists carried resources this refresh stopped carrying, with why.
	Dropped []InventoryChangeRef `json:"dropped,omitempty"`
}

// DefaultInventoryCarryMaxAge is how long a resource of a source that does not
// answer is carried from its last good read before refresh drops it.
const DefaultInventoryCarryMaxAge = 7 * 24 * time.Hour

// InventoryRefreshOptions tune refresh.
type InventoryRefreshOptions struct {
	// AllowOlder imports an observation older than the stored snapshot.
	AllowOlder bool
	// CarryMaxAge bounds how old a carried resource's last good read may be;
	// zero means DefaultInventoryCarryMaxAge.
	CarryMaxAge time.Duration
}

// InventoryRefreshResult reports one import.
type InventoryRefreshResult struct {
	SchemaVersion      string         `json:"schema_version"`
	ImportedAt         string         `json:"imported_at"`
	ObservedAt         string         `json:"observed_at"`
	SnapshotPath       string         `json:"snapshot_path"`
	PreviousKept       bool           `json:"previous_kept"`
	PreviousObservedAt string         `json:"previous_observed_at,omitempty"`
	Resources          int            `json:"resources"`
	ByKind             map[string]int `json:"by_kind"`
	Sources            int            `json:"sources"`
	SourcesByState     map[string]int `json:"sources_by_state"`
	// Carried counts resources kept from the last good read of a source
	// that did not answer; Dropped lists the ones no longer carried.
	Carried      int                  `json:"carried"`
	Dropped      []InventoryChangeRef `json:"dropped"`
	OverlayError string               `json:"overlay_error,omitempty"`
	Notes        []string             `json:"notes,omitempty"`
}

// InventorySnapshotPath is the stored snapshot under a Compound home.
func InventorySnapshotPath(home string) string { return inventoryPath(home, inventorySnapshotFile) }

func inventoryPath(home, name string) string {
	return filepath.Join(home, filepath.FromSlash(inventoryDir), name)
}

// MaskInventoryID shows only the last four characters of an id, for any
// output a person reads.
func MaskInventoryID(id string) string {
	r := []rune(id)
	if len(r) > 4 {
		r = r[len(r)-4:]
	}
	return "…" + string(r)
}

// ParseInventoryObservation decodes an observation strictly (unknown fields
// are errors) and validates it against the contract and the served kinds.
func ParseInventoryObservation(data []byte, kinds InventoryKindSet) (InventoryObservation, error) {
	var obs InventoryObservation
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(&obs); err != nil {
		return InventoryObservation{}, fmt.Errorf("parse observation: %w", err)
	}
	if dec.More() {
		return InventoryObservation{}, fmt.Errorf("parse observation: trailing data after the JSON object")
	}
	if err := ValidateInventoryObservation(obs, kinds); err != nil {
		return InventoryObservation{}, err
	}
	return obs, nil
}

// ValidateInventoryObservation checks the observation contract: schema,
// timestamps, known source and fact states, unique ids, known kinds, declared
// sources and parents, and the value/state rule (ok needs a value; any other
// state has value null). Problems name resources by masked id only.
func ValidateInventoryObservation(obs InventoryObservation, kinds InventoryKindSet) error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	v01 := obs.SchemaVersion == inventoryObservationSchemaV01
	if obs.SchemaVersion != InventoryObservationSchemaVersion && !v01 {
		add("schema_version %q is not %q (or %q)", obs.SchemaVersion, InventoryObservationSchemaVersion, inventoryObservationSchemaV01)
	}
	if _, err := time.Parse(time.RFC3339, obs.ObservedAt); err != nil {
		add("observed_at %q is not an RFC 3339 time", obs.ObservedAt)
	}
	if strings.TrimSpace(obs.Collector.Name) == "" {
		add("collector.name is empty")
	}
	sources := map[string]bool{}
	for i, s := range obs.Sources {
		where := fmt.Sprintf("source %d (%s)", i, s.ID)
		switch {
		case strings.TrimSpace(s.ID) == "":
			add("source %d has no id", i)
		case sources[s.ID]:
			add("%s: id used twice", where)
		}
		sources[s.ID] = true
		if strings.TrimSpace(s.Provider) == "" {
			add("%s: provider is empty", where)
		}
		if !inventorySourceStates[s.State] {
			add("%s: unknown state %q", where, s.State)
		}
		if s.ObservedAt != "" {
			if _, err := time.Parse(time.RFC3339, s.ObservedAt); err != nil {
				add("%s: observed_at %q is not an RFC 3339 time", where, s.ObservedAt)
			}
		}
		if s.DurationMS < 0 {
			add("%s: duration_ms is negative", where)
		}
	}
	ids := map[string]bool{}
	for _, r := range obs.Resources {
		ids[r.ID] = true
	}
	seen := map[string]bool{}
	for i, r := range obs.Resources {
		where := fmt.Sprintf("resource %d (%s)", i, MaskInventoryID(r.ID))
		if strings.TrimSpace(r.ID) == "" {
			add("resource %d has no id", i)
		} else if seen[r.ID] {
			add("%s: id used twice", where)
		}
		seen[r.ID] = true
		if _, known := kinds.Kind(r.Kind); !known {
			add("%s: unknown kind %q (known kinds: %s)", where, r.Kind, strings.Join(kinds.KindIDs(), ", "))
		}
		if strings.TrimSpace(r.Provider) == "" {
			add("%s: provider is empty", where)
		}
		if !sources[r.Source] {
			add("%s: source %q is not listed in sources", where, r.Source)
		}
		if r.Carried || r.LastObservedAt != "" {
			add("%s: carried and last_observed_at are set by refresh, never by a collector", where)
		}
		if r.Parent != "" && (!ids[r.Parent] || r.Parent == r.ID) {
			add("%s: parent %s is not another resource in this file", where, MaskInventoryID(r.Parent))
		}
		for _, p := range r.Places {
			if strings.TrimSpace(p) == "" {
				add("%s: empty place", where)
			}
		}
		isProduct := strings.HasPrefix(r.ID, InventoryProductPrefix)
		if isProduct != (r.Kind == "product") {
			add("%s: a product resource has kind product and an id that starts with %q", where, InventoryProductPrefix)
		}
		if r.ObservedAt != "" {
			if v01 {
				add("%s: observed_at needs schema_version %q", where, InventoryObservationSchemaVersion)
			} else if _, err := time.Parse(time.RFC3339, r.ObservedAt); err != nil {
				add("%s: observed_at %q is not an RFC 3339 time", where, r.ObservedAt)
			}
		}
		for _, l := range r.Links {
			if strings.TrimSpace(l.Rel) == "" || strings.TrimSpace(l.To) == "" {
				add("%s: a link needs rel and to", where)
				continue
			}
			if v01 {
				// v0.1 links stay loose (a place, any rel), so an old file still imports.
				if l.Confidence != "" || l.Evidence != "" || l.ObservedAt != "" {
					add("%s: link confidence, evidence and observed_at need schema_version %q", where, InventoryObservationSchemaVersion)
				}
				continue
			}
			lw := fmt.Sprintf("%s link %s", where, l.Rel)
			if _, known := kinds.Relation(l.Rel); !known {
				add("%s: unknown relation %q (nearest: %s)", lw, l.Rel, kinds.NearestRelation(l.Rel))
			}
			// A target not in this file is allowed: a source that did not answer
			// gets its resources back only by carry-forward. Refresh counts such
			// links and the graph shows them as not in the inventory.
			switch {
			case l.To == r.ID:
				add("%s: a link points at its own resource", lw)
			case l.To == InventoryProductPrefix:
				add("%s: product reference has no id", lw)
			}
			if inventoryConfidenceRank(l.Confidence) == 0 {
				add("%s: unknown confidence %q (want observed, declared or inferred)", lw, l.Confidence)
			}
			if len([]rune(l.Evidence)) > maxInventoryEvidence {
				add("%s: evidence is longer than %d characters (a pointer, never a value)", lw, maxInventoryEvidence)
			}
			if l.ObservedAt != "" {
				if _, err := time.Parse(time.RFC3339, l.ObservedAt); err != nil {
					add("%s: observed_at %q is not an RFC 3339 time", lw, l.ObservedAt)
				}
			}
		}
		for _, name := range sortedFactNames(r.Facts) {
			f := r.Facts[name]
			fw := fmt.Sprintf("%s fact %q", where, name)
			if !inventoryTokenRe.MatchString(name) {
				add("%s: name must be lowercase letters, digits and _", fw)
			}
			if !inventoryFactStates[f.State] {
				add("%s: unknown state %q", fw, f.State)
			}
			switch v := f.Value.(type) {
			case nil:
				if f.State == "ok" {
					add("%s: state ok needs a value (null is only valid with a state that says why)", fw)
				}
			case json.Number, string, bool:
				if f.State != "ok" && inventoryFactStates[f.State] {
					add("%s: state %s must have value null", fw, f.State)
				}
			default:
				add("%s: value must be a number, string, bool or null, got %T", fw, v)
			}
			if f.ObservedAt != "" {
				if _, err := time.Parse(time.RFC3339, f.ObservedAt); err != nil {
					add("%s: observed_at %q is not an RFC 3339 time", fw, f.ObservedAt)
				}
			}
		}
		// Facts the kind does not declare are allowed; the reader shows them as "other".
	}
	if len(problems) == 0 {
		return nil
	}
	total := len(problems)
	if total > maxInventoryProblems {
		problems = append(problems[:maxInventoryProblems], fmt.Sprintf("... and %d more", total-maxInventoryProblems))
	}
	return fmt.Errorf("observation rejected (%d problem(s)):\n  %s", total, strings.Join(problems, "\n  "))
}

func sortedFactNames(facts map[string]InventoryObservedFact) []string {
	names := make([]string, 0, len(facts))
	for name := range facts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// LoadInventorySnapshot reads the stored snapshot. A missing snapshot returns
// an error wrapping os.ErrNotExist.
func LoadInventorySnapshot(home string) (InventorySnapshot, error) {
	return readInventorySnapshot(inventoryPath(home, inventorySnapshotFile))
}

// LoadPreviousInventorySnapshot reads the snapshot kept by the last refresh.
func LoadPreviousInventorySnapshot(home string) (InventorySnapshot, error) {
	return readInventorySnapshot(inventoryPath(home, inventoryPreviousFile))
}

func readInventorySnapshot(path string) (InventorySnapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return InventorySnapshot{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxInventoryBytes+1))
	if err != nil {
		return InventorySnapshot{}, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if len(data) > maxInventoryBytes {
		return InventorySnapshot{}, fmt.Errorf("%s exceeds %d bytes", filepath.Base(path), maxInventoryBytes)
	}
	var snap InventorySnapshot
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&snap); err != nil {
		return InventorySnapshot{}, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	if snap.SchemaVersion != InventorySnapshotSchemaVersion {
		return InventorySnapshot{}, fmt.Errorf("%s: unsupported schema %q", filepath.Base(path), snap.SchemaVersion)
	}
	if _, err := time.Parse(time.RFC3339, snap.Observation.ObservedAt); err != nil {
		return InventorySnapshot{}, fmt.Errorf("%s: observed_at %q is not an RFC 3339 time", filepath.Base(path), snap.Observation.ObservedAt)
	}
	return snap, nil
}

// RefreshInventory validates one collector observation and stores it as the
// current snapshot, keeping the one before as previous.json. It refuses an
// observation older than the stored snapshot unless opts.AllowOlder. It makes
// no external call: the only input is the file the collector already wrote.
func RefreshInventory(home string, data []byte, opts InventoryRefreshOptions, now time.Time) (InventoryRefreshResult, error) {
	if strings.TrimSpace(home) == "" {
		return InventoryRefreshResult{}, fmt.Errorf("no Compound home")
	}
	if len(data) > maxInventoryBytes {
		return InventoryRefreshResult{}, fmt.Errorf("observation exceeds %d bytes", maxInventoryBytes)
	}
	kinds, err := LoadInventoryKinds(home)
	if err != nil {
		return InventoryRefreshResult{}, err
	}
	obs, err := ParseInventoryObservation(data, kinds)
	if err != nil {
		return InventoryRefreshResult{}, err
	}
	newAt, _ := time.Parse(time.RFC3339, obs.ObservedAt)
	result := InventoryRefreshResult{
		SchemaVersion:  InventoryRefreshSchemaVersion,
		ImportedAt:     now.UTC().Format(time.RFC3339),
		ObservedAt:     obs.ObservedAt,
		SnapshotPath:   InventorySnapshotPath(home),
		ByKind:         map[string]int{},
		Sources:        len(obs.Sources),
		SourcesByState: map[string]int{},
		Dropped:        []InventoryChangeRef{},
		OverlayError:   kinds.OverlayError,
	}
	for _, s := range obs.Sources {
		result.SourcesByState[s.State]++
	}
	stored, err := LoadInventorySnapshot(home)
	switch {
	case err == nil:
		storedAt, _ := time.Parse(time.RFC3339, stored.Observation.ObservedAt)
		if newAt.Before(storedAt) && !opts.AllowOlder {
			return InventoryRefreshResult{}, fmt.Errorf("observation observed_at %s is older than the stored snapshot (%s); pass --allow-older to import it anyway", obs.ObservedAt, stored.Observation.ObservedAt)
		}
		result.PreviousKept = true
		result.PreviousObservedAt = stored.Observation.ObservedAt
		maxAge := opts.CarryMaxAge
		if maxAge <= 0 {
			maxAge = DefaultInventoryCarryMaxAge
		}
		carried, dropped := carryInventoryResources(stored.Observation, obs, maxAge, now)
		obs.Resources = append(obs.Resources, carried...)
		result.Carried = len(carried)
		result.Dropped = append(result.Dropped, dropped...)
	case errors.Is(err, os.ErrNotExist):
	default:
		result.PreviousKept = true
		result.Notes = append(result.Notes, "the stored snapshot could not be read and is kept as previous.json; nothing was carried from it: "+err.Error())
	}
	result.Resources = len(obs.Resources)
	for _, r := range obs.Resources {
		result.ByKind[r.Kind]++
	}
	if n := inventoryLinksOutside(obs.Resources); n > 0 {
		result.Notes = append(result.Notes, fmt.Sprintf("%d links point at a resource that is not in the inventory; they are kept and shown as not known", n))
	}
	snap := InventorySnapshot{
		SchemaVersion: InventorySnapshotSchemaVersion,
		ImportedAt:    result.ImportedAt,
		Observation:   obs,
		Dropped:       result.Dropped,
	}
	if err := writeInventorySnapshot(home, snap); err != nil {
		return InventoryRefreshResult{}, err
	}
	return result, nil
}

// carryInventoryResources keeps the resources of a source that did not answer
// this time. A source whose new row has a state other than ok or skipped gets
// its resources from the stored snapshot carried forward unchanged (facts and
// their observed_at as they were), marked carried, with last_observed_at set
// to the last good read. A resource the new observation already lists (by id)
// is never carried, so a recovered source replaces its carried rows. A skipped
// source (turned off by the user) carries nothing. A carried resource whose
// last good read is older than maxAge is dropped and reported with a reason.
func carryInventoryResources(stored, next InventoryObservation, maxAge time.Duration, now time.Time) ([]InventoryObservedResource, []InventoryChangeRef) {
	carrySources := map[string]bool{}
	for _, s := range next.Sources {
		if s.State != "ok" && s.State != "skipped" {
			carrySources[s.ID] = true
		}
	}
	if len(carrySources) == 0 {
		return nil, nil
	}
	present := map[string]bool{}
	for _, r := range next.Resources {
		present[r.ID] = true
	}
	storedSourceAt := map[string]string{}
	for _, s := range stored.Sources {
		storedSourceAt[s.ID] = s.ObservedAt
	}
	var carried []InventoryObservedResource
	var dropped []InventoryChangeRef
	for _, r := range stored.Resources {
		if !carrySources[r.Source] || present[r.ID] {
			continue
		}
		last := r.LastObservedAt
		if !r.Carried || last == "" {
			last = storedSourceAt[r.Source]
			if last == "" {
				last = stored.ObservedAt
			}
		}
		if lastAt, err := time.Parse(time.RFC3339, last); err == nil && now.Sub(lastAt) > maxAge {
			dropped = append(dropped, InventoryChangeRef{ResourceID: r.ID, Kind: r.Kind, Label: r.Label,
				Reason: "not seen for " + inventoryDays(maxAge)})
			continue
		}
		r.Carried = true
		r.LastObservedAt = last
		present[r.ID] = true
		carried = append(carried, r)
	}
	return carried, dropped
}

// inventoryLinksOutside counts links whose target is neither a resource in
// the list nor a product reference.
func inventoryLinksOutside(resources []InventoryObservedResource) int {
	ids := map[string]bool{}
	for _, r := range resources {
		ids[r.ID] = true
	}
	n := 0
	for _, r := range resources {
		for _, l := range r.Links {
			if !ids[l.To] && !strings.HasPrefix(l.To, InventoryProductPrefix) {
				n++
			}
		}
	}
	return n
}

func inventoryDays(d time.Duration) string {
	days := d.Hours() / 24
	if days == 1 {
		return "1 day"
	}
	return formatInventoryNumber(days) + " days"
}

// RefreshInventoryFile reads the collector's observation file (size-capped)
// and refreshes the stored snapshot from it.
func RefreshInventoryFile(home, path string, opts InventoryRefreshOptions, now time.Time) (InventoryRefreshResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return InventoryRefreshResult{}, fmt.Errorf("read observation: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxInventoryBytes+1))
	if err != nil {
		return InventoryRefreshResult{}, fmt.Errorf("read observation: %w", err)
	}
	return RefreshInventory(home, data, opts, now)
}

// FormatInventoryRefresh is the plain-words result of one import.
func FormatInventoryRefresh(r InventoryRefreshResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "inventory imported: %d resources (%s), %d sources (%s)\n",
		r.Resources, formatInventoryCounts(r.ByKind), r.Sources, formatInventoryCounts(r.SourcesByState))
	fmt.Fprintf(&b, "  observed %s, imported %s\n", r.ObservedAt, r.ImportedAt)
	fmt.Fprintf(&b, "  snapshot: %s\n", r.SnapshotPath)
	if r.PreviousObservedAt != "" {
		fmt.Fprintf(&b, "  previous snapshot kept (observed %s)\n", r.PreviousObservedAt)
	}
	if r.Carried > 0 {
		fmt.Fprintf(&b, "  %d resources kept from the last good read of a source that did not answer\n", r.Carried)
	}
	if len(r.Dropped) > 0 {
		fmt.Fprintf(&b, "  %d carried resources dropped: %s\n", len(r.Dropped), r.Dropped[0].Reason)
	}
	if r.OverlayError != "" {
		fmt.Fprintf(&b, "  kinds overlay ignored: %s\n", r.OverlayError)
	}
	for _, n := range r.Notes {
		fmt.Fprintf(&b, "  note: %s\n", n)
	}
	return b.String()
}

// formatInventoryCounts renders "a 2, b 1" in key order.
func formatInventoryCounts(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, counts[k]))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// writeInventorySnapshot keeps the current snapshot as previous.json and
// replaces it atomically (temp file + rename), as the door map does, so a
// concurrent reader never sees a torn file.
func writeInventorySnapshot(home string, snap InventorySnapshot) error {
	dir := filepath.Join(home, filepath.FromSlash(inventoryDir))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return err
	}
	cur := inventoryPath(home, inventorySnapshotFile)
	if _, err := os.Stat(cur); err == nil {
		if err := os.Rename(cur, inventoryPath(home, inventoryPreviousFile)); err != nil {
			os.Remove(tmpName)
			return fmt.Errorf("keep previous snapshot: %w", err)
		}
	}
	if err := os.Rename(tmpName, cur); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
