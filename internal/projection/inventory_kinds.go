package projection

// System inventory kinds: the data that says which resource kinds exist
// (account, host, guest, ...) and which facts are worth knowing about each.
//
// Kinds are data, not code. The embedded file is the public default; a user
// may add kinds or facts, or change a warning threshold, in the optional
// overlay COMPOUND_HOME/config/inventory-kinds.yaml, merged by id. A broken
// overlay never breaks the read: the embedded kinds are served and the error
// is reported as OverlayError.

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// InventoryKindsSchemaVersion names the kinds file shape (embedded and overlay).
// v0.2 adds relations and the is_false warning rule; a v0.1 overlay still loads.
const InventoryKindsSchemaVersion = "compound.system_inventory_kinds.v0.2"

const inventoryKindsSchemaV01 = "compound.system_inventory_kinds.v0.1"

// Relation meanings. A depends relation means the source cannot work without
// the target; the graph walks depends relations (and parent) for "what does X
// depend on", route relations when it starts from a hostname, and identity
// relations for the sign-ins and secret stores a thing rests on.
const (
	InventoryMeaningDepends  = "depends"
	InventoryMeaningContains = "contains"
	InventoryMeaningRoute    = "route"
	InventoryMeaningIdentity = "identity"
	InventoryMeaningBackup   = "backup"
	InventoryMeaningOther    = "other"
)

// InventoryRelParent is the link name of a resource's parent. It is not a
// relation row: parent counts as runs_on.
const InventoryRelParent = "parent"

// inventoryWarnIsFalse is the boolean warning rule: warn when a yes/no fact is no.
const inventoryWarnIsFalse = "is_false"

// InventoryKindsOverlayRel is the overlay path relative to COMPOUND_HOME.
const InventoryKindsOverlayRel = "config/inventory-kinds.yaml"

const maxInventoryKindsBytes = 1 << 20

// Fact groups. Cost and credit facts that a collector does not report are
// shown as "not known", never as zero.
const (
	InventoryGroupIdentity = "identity"
	InventoryGroupUse      = "use"
	InventoryGroupCost     = "cost"
	InventoryGroupCredit   = "credit"
	InventoryGroupLimit    = "limit"
	// InventoryGroupOther is the reader-side group for facts a resource
	// carries but its kind does not declare. It is never valid in a kinds file.
	InventoryGroupOther = "other"
)

//go:embed inventory_kinds.yaml
var embeddedInventoryKinds []byte

var inventoryTokenRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// InventoryKindsFile is the on-disk shape of the embedded file and the overlay.
type InventoryKindsFile struct {
	SchemaVersion string              `yaml:"schema_version" json:"schema_version"`
	Relations     []InventoryRelation `yaml:"relations" json:"relations"`
	Kinds         []InventoryKind     `yaml:"kinds" json:"kinds"`
}

// InventoryRelation is one kind of link between resources: its id, how it
// reads forwards and backwards, and what it means for the graph walks.
type InventoryRelation struct {
	ID           string `yaml:"id" json:"id"`
	Title        string `yaml:"title" json:"title"`
	InverseTitle string `yaml:"inverse_title" json:"inverse_title"`
	Meaning      string `yaml:"meaning" json:"meaning"`
}

// InventoryKind is one resource kind and the facts worth knowing about it.
type InventoryKind struct {
	ID       string              `yaml:"id" json:"id"`
	Title    string              `yaml:"title" json:"title"`
	Question string              `yaml:"question" json:"question"`
	Facts    []InventoryFactSpec `yaml:"facts" json:"facts"`
}

// InventoryFactSpec declares one fact of a kind.
type InventoryFactSpec struct {
	Name     string   `yaml:"name" json:"name"`
	Title    string   `yaml:"title" json:"title"`
	Unit     string   `yaml:"unit" json:"unit"`
	Group    string   `yaml:"group" json:"group"`
	WarnAt   *float64 `yaml:"warn_at,omitempty" json:"warn_at,omitempty"`
	WarnWhen string   `yaml:"warn_when,omitempty" json:"warn_when,omitempty"`
}

// InventoryKindSet is the served kinds: embedded, merged with the overlay
// when the overlay is present and valid.
type InventoryKindSet struct {
	Kinds     []InventoryKind
	Relations []InventoryRelation
	// OverlayPath is set when an overlay file exists (merged or not).
	OverlayPath string
	// OverlayError is the reason a present overlay was ignored; "" when the
	// overlay was merged or absent.
	OverlayError string
}

// Kind returns the served kind with the given id.
func (s InventoryKindSet) Kind(id string) (InventoryKind, bool) {
	for _, k := range s.Kinds {
		if k.ID == id {
			return k, true
		}
	}
	return InventoryKind{}, false
}

// Fact returns the declared fact of a kind.
func (k InventoryKind) Fact(name string) (InventoryFactSpec, bool) {
	for _, f := range k.Facts {
		if f.Name == name {
			return f, true
		}
	}
	return InventoryFactSpec{}, false
}

// KindIDs lists the served kind ids in file order.
func (s InventoryKindSet) KindIDs() []string {
	out := make([]string, 0, len(s.Kinds))
	for _, k := range s.Kinds {
		out = append(out, k.ID)
	}
	return out
}

// Relation returns the served relation with the given id. parent is served
// as runs_on.
func (s InventoryKindSet) Relation(id string) (InventoryRelation, bool) {
	if id == InventoryRelParent {
		id = "runs_on"
	}
	for _, r := range s.Relations {
		if r.ID == id {
			return r, true
		}
	}
	return InventoryRelation{}, false
}

// NearestRelation is the served relation id closest to id by edit distance,
// for an error that says what was probably meant.
func (s InventoryKindSet) NearestRelation(id string) string {
	best, bestD := "", -1
	for _, r := range s.Relations {
		if d := inventoryEditDistance(id, r.ID); bestD < 0 || d < bestD {
			best, bestD = r.ID, d
		}
	}
	return best
}

func inventoryEditDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// LoadInventoryKinds returns the embedded kinds merged with the overlay under
// homePath (when homePath is non-empty and the overlay exists). It fails only
// when the embedded file itself is invalid; any overlay problem is reported in
// OverlayError while the embedded kinds are served.
func LoadInventoryKinds(homePath string) (InventoryKindSet, error) {
	base, err := ParseInventoryKinds(embeddedInventoryKinds)
	if err != nil {
		return InventoryKindSet{}, fmt.Errorf("embedded inventory kinds: %w", err)
	}
	if err := ValidateInventoryKinds(base.Kinds); err != nil {
		return InventoryKindSet{}, fmt.Errorf("embedded inventory kinds: %w", err)
	}
	if err := ValidateInventoryRelations(base.Relations); err != nil {
		return InventoryKindSet{}, fmt.Errorf("embedded inventory kinds: %w", err)
	}
	set := InventoryKindSet{Kinds: base.Kinds, Relations: base.Relations}
	if strings.TrimSpace(homePath) == "" {
		return set, nil
	}
	path := filepath.Join(homePath, filepath.FromSlash(InventoryKindsOverlayRel))
	data, err := readInventoryKindsOverlay(path)
	if errors.Is(err, os.ErrNotExist) {
		return set, nil
	}
	set.OverlayPath = path
	if err != nil {
		set.OverlayError = err.Error()
		return set, nil
	}
	overlay, err := ParseInventoryKinds(data)
	if err != nil {
		set.OverlayError = "overlay " + InventoryKindsOverlayRel + ": " + err.Error()
		return set, nil
	}
	merged := MergeInventoryKinds(base.Kinds, overlay.Kinds)
	if err := ValidateInventoryKinds(merged); err != nil {
		set.OverlayError = "overlay " + InventoryKindsOverlayRel + ": " + err.Error()
		return set, nil
	}
	mergedRels := MergeInventoryRelations(base.Relations, overlay.Relations)
	if err := ValidateInventoryRelations(mergedRels); err != nil {
		set.OverlayError = "overlay " + InventoryKindsOverlayRel + ": " + err.Error()
		return set, nil
	}
	set.Kinds = merged
	set.Relations = mergedRels
	return set, nil
}

func readInventoryKindsOverlay(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxInventoryKindsBytes+1))
	if err != nil {
		return nil, fmt.Errorf("overlay %s: %w", InventoryKindsOverlayRel, err)
	}
	if len(data) > maxInventoryKindsBytes {
		return nil, fmt.Errorf("overlay %s exceeds %d bytes", InventoryKindsOverlayRel, maxInventoryKindsBytes)
	}
	return data, nil
}

// ParseInventoryKinds decodes a kinds file strictly: unknown fields and a
// wrong schema_version are errors. An overlay may omit schema_version.
func ParseInventoryKinds(data []byte) (InventoryKindsFile, error) {
	var file InventoryKindsFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil {
		if errors.Is(err, io.EOF) {
			return InventoryKindsFile{}, fmt.Errorf("empty kinds file")
		}
		return InventoryKindsFile{}, fmt.Errorf("parse kinds: %w", err)
	}
	switch file.SchemaVersion {
	case "", InventoryKindsSchemaVersion:
	case inventoryKindsSchemaV01:
		if len(file.Relations) > 0 {
			return InventoryKindsFile{}, fmt.Errorf("relations need kinds schema %q", InventoryKindsSchemaVersion)
		}
	default:
		return InventoryKindsFile{}, fmt.Errorf("unsupported kinds schema %q (want %q)", file.SchemaVersion, InventoryKindsSchemaVersion)
	}
	return file, nil
}

// ValidateInventoryRelations checks unique token-shaped relation ids, titles
// both ways and a known meaning. parent is reserved: it is served as runs_on.
func ValidateInventoryRelations(rels []InventoryRelation) error {
	seen := map[string]bool{}
	for i, r := range rels {
		if !inventoryTokenRe.MatchString(r.ID) {
			return fmt.Errorf("relation %d: id %q must be lowercase letters, digits and _", i, r.ID)
		}
		if r.ID == InventoryRelParent {
			return fmt.Errorf("relation %q is reserved: parent counts as runs_on", r.ID)
		}
		if seen[r.ID] {
			return fmt.Errorf("relation %q is listed twice", r.ID)
		}
		seen[r.ID] = true
		if strings.TrimSpace(r.Title) == "" || strings.TrimSpace(r.InverseTitle) == "" {
			return fmt.Errorf("relation %q needs title and inverse_title", r.ID)
		}
		switch r.Meaning {
		case InventoryMeaningDepends, InventoryMeaningContains, InventoryMeaningRoute,
			InventoryMeaningIdentity, InventoryMeaningBackup, InventoryMeaningOther:
		default:
			return fmt.Errorf("relation %q: unknown meaning %q (want depends, contains, route, identity, backup or other)", r.ID, r.Meaning)
		}
	}
	if !seen["runs_on"] {
		return fmt.Errorf("relation runs_on is missing (parent counts as runs_on)")
	}
	return nil
}

// MergeInventoryRelations merges overlay relations into base by id: a known
// relation takes the overlay's non-empty fields, a new one is appended. The
// result is not validated here.
func MergeInventoryRelations(base, overlay []InventoryRelation) []InventoryRelation {
	out := append([]InventoryRelation(nil), base...)
	index := map[string]int{}
	for i, r := range out {
		index[r.ID] = i
	}
	for _, o := range overlay {
		i, exists := index[o.ID]
		if !exists {
			index[o.ID] = len(out)
			out = append(out, o)
			continue
		}
		r := out[i]
		if strings.TrimSpace(o.Title) != "" {
			r.Title = o.Title
		}
		if strings.TrimSpace(o.InverseTitle) != "" {
			r.InverseTitle = o.InverseTitle
		}
		if o.Meaning != "" {
			r.Meaning = o.Meaning
		}
		out[i] = r
	}
	return out
}

// ValidateInventoryKinds checks unique kind ids, unique fact names per kind,
// token-shaped ids and names, required titles, known groups, and complete
// warning thresholds.
func ValidateInventoryKinds(kinds []InventoryKind) error {
	if len(kinds) == 0 {
		return fmt.Errorf("no kinds")
	}
	seen := map[string]bool{}
	for i, k := range kinds {
		if !inventoryTokenRe.MatchString(k.ID) {
			return fmt.Errorf("kind %d: id %q must be lowercase letters, digits and _", i, k.ID)
		}
		if seen[k.ID] {
			return fmt.Errorf("kind %q is listed twice", k.ID)
		}
		seen[k.ID] = true
		if strings.TrimSpace(k.Title) == "" {
			return fmt.Errorf("kind %q has no title", k.ID)
		}
		names := map[string]bool{}
		for j, f := range k.Facts {
			if !inventoryTokenRe.MatchString(f.Name) {
				return fmt.Errorf("kind %q fact %d: name %q must be lowercase letters, digits and _", k.ID, j, f.Name)
			}
			if names[f.Name] {
				return fmt.Errorf("kind %q lists fact %q twice", k.ID, f.Name)
			}
			names[f.Name] = true
			if strings.TrimSpace(f.Title) == "" {
				return fmt.Errorf("kind %q fact %q has no title", k.ID, f.Name)
			}
			switch f.Group {
			case InventoryGroupIdentity, InventoryGroupUse, InventoryGroupCost, InventoryGroupCredit, InventoryGroupLimit:
			default:
				return fmt.Errorf("kind %q fact %q: unknown group %q (want identity, use, cost, credit or limit)", k.ID, f.Name, f.Group)
			}
			switch {
			case f.WarnAt == nil && f.WarnWhen == "":
			case f.WarnWhen == inventoryWarnIsFalse:
				if f.WarnAt != nil {
					return fmt.Errorf("kind %q fact %q: warn_when is_false takes no warn_at", k.ID, f.Name)
				}
			case f.WarnAt == nil:
				return fmt.Errorf("kind %q fact %q: warn_when needs warn_at", k.ID, f.Name)
			case f.WarnWhen != "above" && f.WarnWhen != "below":
				return fmt.Errorf("kind %q fact %q: warn_when must be above or below, got %q", k.ID, f.Name, f.WarnWhen)
			}
		}
	}
	return nil
}

// MergeInventoryKinds merges overlay kinds into base by id. A known kind takes
// the overlay's non-empty title and question; its facts merge by name, field
// by field (an overlay fact may change only warn_at). A new kind or fact is
// appended. The result is not validated here.
func MergeInventoryKinds(base, overlay []InventoryKind) []InventoryKind {
	out := make([]InventoryKind, 0, len(base)+len(overlay))
	index := map[string]int{}
	for _, k := range base {
		k.Facts = append([]InventoryFactSpec(nil), k.Facts...)
		index[k.ID] = len(out)
		out = append(out, k)
	}
	for _, ok := range overlay {
		i, exists := index[ok.ID]
		if !exists {
			ok.Facts = append([]InventoryFactSpec(nil), ok.Facts...)
			index[ok.ID] = len(out)
			out = append(out, ok)
			continue
		}
		k := out[i]
		if strings.TrimSpace(ok.Title) != "" {
			k.Title = ok.Title
		}
		if strings.TrimSpace(ok.Question) != "" {
			k.Question = ok.Question
		}
		for _, of := range ok.Facts {
			k.Facts = mergeInventoryFact(k.Facts, of)
		}
		out[i] = k
	}
	return out
}

func mergeInventoryFact(facts []InventoryFactSpec, of InventoryFactSpec) []InventoryFactSpec {
	for i, f := range facts {
		if f.Name != of.Name {
			continue
		}
		if of.Title != "" {
			f.Title = of.Title
		}
		if of.Unit != "" {
			f.Unit = of.Unit
		}
		if of.Group != "" {
			f.Group = of.Group
		}
		if of.WarnAt != nil {
			v := *of.WarnAt
			f.WarnAt = &v
		}
		if of.WarnWhen != "" {
			f.WarnWhen = of.WarnWhen
		}
		facts[i] = f
		return facts
	}
	return append(facts, of)
}
