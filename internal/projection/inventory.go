package projection

// System inventory: accounts, hosts, guests, services, domains, sites, apps
// and paid plans, as one collector last observed them.
//
// Compound only reads. A collector outside the core binary runs the user's
// own signed-in, read-only listing tools and writes one observation file
// (compound.system_inventory_observation.v0.1). `compound system inventory
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
	InventoryObservationSchemaVersion = "compound.system_inventory_observation.v0.1"
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
)

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
}

// InventoryLink relates a resource to another resource or place.
type InventoryLink struct {
	Rel string `json:"rel"`
	To  string `json:"to"`
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
}

// InventoryRefreshOptions tune refresh.
type InventoryRefreshOptions struct {
	// AllowOlder imports an observation older than the stored snapshot.
	AllowOlder bool
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
	OverlayError       string         `json:"overlay_error,omitempty"`
	Notes              []string       `json:"notes,omitempty"`
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
	if obs.SchemaVersion != InventoryObservationSchemaVersion {
		add("schema_version %q is not %q", obs.SchemaVersion, InventoryObservationSchemaVersion)
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
		if r.Parent != "" && (!ids[r.Parent] || r.Parent == r.ID) {
			add("%s: parent %s is not another resource in this file", where, MaskInventoryID(r.Parent))
		}
		for _, p := range r.Places {
			if strings.TrimSpace(p) == "" {
				add("%s: empty place", where)
			}
		}
		for _, l := range r.Links {
			if strings.TrimSpace(l.Rel) == "" || strings.TrimSpace(l.To) == "" {
				add("%s: a link needs rel and to", where)
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
		Resources:      len(obs.Resources),
		ByKind:         map[string]int{},
		Sources:        len(obs.Sources),
		SourcesByState: map[string]int{},
		OverlayError:   kinds.OverlayError,
	}
	for _, r := range obs.Resources {
		result.ByKind[r.Kind]++
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
	case errors.Is(err, os.ErrNotExist):
	default:
		result.PreviousKept = true
		result.Notes = append(result.Notes, "the stored snapshot could not be read and is kept as previous.json: "+err.Error())
	}
	snap := InventorySnapshot{
		SchemaVersion: InventorySnapshotSchemaVersion,
		ImportedAt:    result.ImportedAt,
		Observation:   obs,
	}
	if err := writeInventorySnapshot(home, snap); err != nil {
		return InventoryRefreshResult{}, err
	}
	return result, nil
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
