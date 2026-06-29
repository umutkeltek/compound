// Package contract compiles an EffectiveContract for a harness+repo+task and
// assembles the ContextPack an agent actually orients from. The point of the slice
// is path-independence of *meaning*: the same task in a product repo and in the
// control-plane repo must yield DIFFERENT packs, and the pack must record what was
// EXCLUDED and why — so OS-governance context stops being ambient noise in a product
// repo. See docs/architecture/contracts/README.md and docs/architecture/runtime.md.
package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"compound/internal/capgraph"
	"compound/internal/envelope"
	"compound/internal/projection"
)

const (
	defaultRepoKind = "product"
	// repoMarker is the file a repo commits to self-declare its kind. Generic and
	// leak-free: compound reads the marker, it does not heuristically sniff paths.
	repoMarker = ".compound/repo.yaml"

	repoKindRegistrySchema = "compound.repo_kind_registry.v0.1"
)

// ContextSource is one named class of orientation context and the repo kinds it is
// ambient for. A source ambient for "control-plane" only is excluded in a product repo.
type ContextSource struct {
	Class       string   `yaml:"class" json:"class"`
	SourceClass string   `yaml:"source_class" json:"source_class"`
	AmbientFor  []string `yaml:"ambient_for" json:"ambient_for"`
	Ref         string   `yaml:"ref" json:"ref"`
}

// MemoryRule is the recall gate per repo kind — encodes "personal corpus is not ambient".
type MemoryRule struct {
	Ambient      []string `yaml:"ambient" json:"ambient"`
	ExplicitOnly []string `yaml:"explicit_only" json:"explicit_only"`
	Forbidden    []string `yaml:"forbidden" json:"forbidden"`
}

// OrientationPolicy is the declarative orientation registry: what context is ambient
// for which repo kind, and what memory each kind may load. Shipped as a built-in
// default; overridable from COMPOUND_HOME/config/orientation.yaml.
type OrientationPolicy struct {
	RepoKinds []string              `yaml:"repo_kinds" json:"repo_kinds"`
	Sources   []ContextSource       `yaml:"sources" json:"sources"`
	Memory    map[string]MemoryRule `yaml:"memory" json:"memory"`
}

// RepoKindRegistry is a private/runtime overlay for repos that cannot or should
// not commit a repo-kind marker. It belongs under COMPOUND_HOME/config, not
// public docs; it is still an explicit declaration, not path-name guessing.
type RepoKindRegistry struct {
	SchemaVersion string          `yaml:"schema_version" json:"schema_version"`
	Repos         []RepoKindEntry `yaml:"repos" json:"repos"`
}

type RepoKindEntry struct {
	Kind  string   `yaml:"kind" json:"kind"`
	Roots []string `yaml:"roots" json:"roots"`
}

// DefaultPolicy is the built-in, leak-free orientation policy. Refs use generic
// schemes (no personal paths). It makes the orientation-scope fix concrete: governance
// context is ambient for control-plane only, so a product repo excludes it.
func DefaultPolicy() OrientationPolicy {
	all := []string{"control-plane", "product", "library"}
	return OrientationPolicy{
		RepoKinds: all,
		Sources: []ContextSource{
			{Class: "operating-contract", SourceClass: "source", AmbientFor: all, Ref: "AGENTS.md"},
			{Class: "governance-map", SourceClass: "source", AmbientFor: []string{"control-plane"}, Ref: "control-plane://orientation-map"},
			{Class: "governance-workflow-contract", SourceClass: "source", AmbientFor: []string{"control-plane"}, Ref: "control-plane://workflow-contract"},
			{Class: "governance-sop-packets", SourceClass: "source", AmbientFor: []string{"control-plane"}, Ref: "control-plane://sop-packets"},
			{Class: "product-docs", SourceClass: "source", AmbientFor: []string{"product"}, Ref: "repo://docs"},
			{Class: "memory-index", SourceClass: "generated", AmbientFor: all, Ref: "memory://relevance-gated"},
		},
		Memory: map[string]MemoryRule{
			"control-plane": {Ambient: []string{"project-facts", "decisions", "system-state"}, ExplicitOnly: []string{"self-model"}, Forbidden: []string{"personal-corpus"}},
			"product":       {Ambient: []string{"project-facts"}, ExplicitOnly: []string{"decisions"}, Forbidden: []string{"self-model", "personal-corpus"}},
			"library":       {Ambient: []string{"project-facts"}, ExplicitOnly: []string{"decisions"}, Forbidden: []string{"self-model", "personal-corpus"}},
		},
	}
}

// LoadPolicy returns the policy from COMPOUND_HOME/config/orientation.yaml if present,
// else the built-in default. Returns the source ("built-in" or the file path) for
// observability. An empty homePath always falls back to the default.
func LoadPolicy(homePath string) (OrientationPolicy, string, error) {
	if homePath != "" {
		p := filepath.Join(homePath, "config", "orientation.yaml")
		if data, err := os.ReadFile(p); err == nil {
			var pol OrientationPolicy
			if err := yaml.Unmarshal(data, &pol); err != nil {
				return OrientationPolicy{}, p, fmt.Errorf("parse %s: %w", p, err)
			}
			return pol, p, nil
		}
	}
	return DefaultPolicy(), "built-in", nil
}

// LoadRepoKindRegistry reads COMPOUND_HOME/config/repo-kinds.yaml when present.
// Missing registry is not an error; malformed registry is, because silent
// fallback would recreate ambient-context drift.
func LoadRepoKindRegistry(homePath string) (RepoKindRegistry, string, error) {
	if strings.TrimSpace(homePath) == "" {
		return RepoKindRegistry{}, "", nil
	}
	p := filepath.Join(homePath, "config", "repo-kinds.yaml")
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return RepoKindRegistry{}, "", nil
	}
	if err != nil {
		return RepoKindRegistry{}, p, fmt.Errorf("read %s: %w", p, err)
	}
	var reg RepoKindRegistry
	if err := yaml.Unmarshal(data, &reg); err != nil {
		return RepoKindRegistry{}, p, fmt.Errorf("parse %s: %w", p, err)
	}
	if reg.SchemaVersion != repoKindRegistrySchema {
		return RepoKindRegistry{}, p, fmt.Errorf("repo kind registry schema_version must be %s, got %q", repoKindRegistrySchema, reg.SchemaVersion)
	}
	for i, repo := range reg.Repos {
		if strings.TrimSpace(repo.Kind) == "" {
			return RepoKindRegistry{}, p, fmt.Errorf("repo kind registry repos[%d] missing kind", i)
		}
		if len(repo.Roots) == 0 {
			return RepoKindRegistry{}, p, fmt.Errorf("repo kind registry repos[%d] missing roots", i)
		}
	}
	return reg, p, nil
}

// DetectRepoKind walks up from cwd for a committed repo-kind marker. Returns ("","")
// when none is found; the caller defaults to product (the lean pack).
func DetectRepoKind(cwd string) (kind, via string) {
	dir := cwd
	for {
		p := filepath.Join(dir, repoMarker)
		if data, err := os.ReadFile(p); err == nil {
			var m struct {
				Kind string `yaml:"kind"`
			}
			if yaml.Unmarshal(data, &m) == nil && m.Kind != "" {
				return m.Kind, p
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ""
		}
		dir = parent
	}
}

// ResolveRepoKind picks the repo kind: explicit flag wins, else a committed marker,
// else the product default. `via` explains the source for the contract's record.
func DetectRepoKindFromRegistry(reg RepoKindRegistry, registryPath, cwd string) (kind, via string) {
	cwdAbs, err := filepath.Abs(expandPath(cwd))
	if err != nil {
		return "", ""
	}
	bestLen := -1
	for _, repo := range reg.Repos {
		for _, rawRoot := range repo.Roots {
			root := expandPath(rawRoot)
			if !filepath.IsAbs(root) && registryPath != "" {
				root = filepath.Join(filepath.Dir(registryPath), root)
			}
			rootAbs, err := filepath.Abs(root)
			if err != nil || !isWithin(rootAbs, cwdAbs) {
				continue
			}
			if len(rootAbs) > bestLen {
				bestLen = len(rootAbs)
				kind = repo.Kind
				via = "repo-kind-registry:" + registryPath
			}
		}
	}
	return kind, via
}

func ResolveRepoKindWithHome(homePath, flag, cwd string) (kind, via string, err error) {
	if flag != "" {
		return flag, "flag", nil
	}
	if k, p := DetectRepoKind(cwd); k != "" {
		return k, p, nil
	}
	reg, registryPath, err := LoadRepoKindRegistry(homePath)
	if err != nil {
		return "", "", err
	}
	if k, p := DetectRepoKindFromRegistry(reg, registryPath, cwd); k != "" {
		return k, p, nil
	}
	return defaultRepoKind, "default", nil
}

func ResolveRepoKind(flag, cwd string) (kind, via string) {
	kind, via, _ = ResolveRepoKindWithHome("", flag, cwd)
	return kind, via
}

// Input is a resolved orientation request (repo kind already resolved by the caller).
type Input struct {
	Harness     string
	Cwd         string
	Task        string
	RepoKind    string
	RepoKindVia string // "flag" | "<marker path>" | "default"
	Mode        string // observe | plan | execute | review
}

// Compile produces the EffectiveContract: the instructions, hooks, memory/capability/
// mutation policy, and unknowns in force for this harness+repo+task.
func Compile(in Input, pol OrientationPolicy, now time.Time) envelope.EffectiveContract {
	incl, _ := partition(pol, in.RepoKind)
	var active []string
	var revs []envelope.SourceRev
	for _, r := range incl {
		if r.SourceClass == "source" {
			active = append(active, r.Ref)
		}
		revs = append(revs, envelope.SourceRev{Source: r.Ref, Rev: "unknown"})
	}
	mem := pol.Memory[in.RepoKind]
	unknowns := []string{}
	if in.RepoKindVia == "default" {
		unknowns = append(unknowns, fmt.Sprintf("repo_kind defaulted to %s (no %s marker found)", in.RepoKind, repoMarker))
	}
	return envelope.EffectiveContract{
		Header:             header(envelope.SVEffectiveContract, idFor("contract_", in.Harness, in.RepoKind, in.Task), now, in),
		Harness:            in.Harness,
		TaskMode:           in.Mode,
		ActiveInstructions: orEmpty(active),
		ActiveHooks:        []string{},
		ActiveDecisions:    []string{},
		MemoryPolicy: envelope.MemoryPolicy{
			Ambient:      orEmpty(mem.Ambient),
			ExplicitOnly: orEmpty(mem.ExplicitOnly),
			Forbidden:    orEmpty(mem.Forbidden),
		},
		CapabilityPolicy: envelope.CapabilityPolicy{
			ToolsAllowed:     []string{},
			ToolsOnDemand:    []string{},
			MCPsNotByDefault: []string{},
		},
		MutationPolicy:  envelope.MutationPolicy{RequiresActionIntent: true, IrreversibleRequiresHuman: true},
		SourceRevisions: revs,
		Unknowns:        unknowns,
		Conflicts:       []string{},
	}
}

// Pack assembles the ContextPack an agent orients from. Its EXCLUDED list — what was
// NOT loaded and why — is the whole point: it is required and never silently dropped.
func Pack(in Input, pol OrientationPolicy, now time.Time) envelope.ContextPack {
	incl, excl := partition(pol, in.RepoKind)
	var active []string
	for _, r := range incl {
		if r.SourceClass == "source" {
			active = append(active, r.Ref)
		}
	}
	mem := pol.Memory[in.RepoKind]
	summary := fmt.Sprintf("Oriented as a **%s** repo for `%s`: %d sources included, %d excluded.",
		in.RepoKind, in.Harness, len(incl), len(excl))
	if len(excl) > 0 {
		summary += " Out-of-scope context recorded — see `excluded`."
	}
	return envelope.ContextPack{
		Header:             header(envelope.SVContextPack, idFor("ctx_", in.Harness, in.RepoKind, in.Task), now, in),
		SummaryMD:          summary,
		Included:           incl,
		Excluded:           excl,
		ActiveInstructions: orEmpty(active),
		ActiveDecisions:    []string{},
		RequiredChecks:     []string{},
		Avoid:              orEmpty(mem.Forbidden),
	}
}

// PackWithCapabilities appends CapabilityGraph offers to the ContextPack. It is
// still orientation-only: surfaced capabilities are included refs, not execution.
func PackWithCapabilities(in Input, pol OrientationPolicy, homePath string, now time.Time) (envelope.ContextPack, error) {
	pack := Pack(in, pol, now)
	if strings.TrimSpace(homePath) == "" {
		return pack, nil
	}
	if capgraph.Exists(homePath) {
		store, err := capgraph.Open(homePath)
		if err != nil {
			return pack, err
		}
		surfaced, err := store.SurfaceForHarness(in.Task, in.Harness)
		if err != nil {
			return pack, err
		}
		for _, hit := range surfaced {
			pack.Included = append(pack.Included, envelope.IncludedRef{
				Ref:         "capability://" + hit.Node.ID,
				SourceClass: "generated",
				Reason:      fmt.Sprintf("capability_graph surface score=%.1f kind=%s why=%s", hit.Score, hit.Node.Kind, hit.Why),
				Confidence:  hit.Score,
				Status:      "current",
			})
		}
		if len(surfaced) > 0 {
			pack.SummaryMD += fmt.Sprintf(" %d capability offer(s) surfaced; offers are not execution.", len(surfaced))
		}
	}
	records, err := projection.LoadContext(homePath, in.RepoKind)
	if err != nil {
		return pack, err
	}
	for _, record := range records {
		freshness := record.Freshness
		pack.Included = append(pack.Included, envelope.IncludedRef{
			Ref:         projection.ContextRef(freshness),
			SourceClass: "generated",
			Reason:      fmt.Sprintf("projection_freshness status=%s view=%s; generated-view context evidence, not authority or execution", freshness.Status, freshness.ViewRef),
			Confidence:  projectionContextConfidence(freshness.Status),
			Status:      projection.ContextStatus(freshness.Status),
		})
	}
	if len(records) > 0 {
		pack.SummaryMD += fmt.Sprintf(" %d projection freshness ref(s) included; generated views are context evidence, not truth.", len(records))
	}
	return pack, nil
}

func projectionContextConfidence(status string) float64 {
	switch status {
	case projection.StatusFresh:
		return 1
	case projection.StatusStale:
		return 0.6
	case projection.StatusDrifted:
		return 0.3
	default:
		return 0.1
	}
}

// partition splits the policy's sources into included (ambient for this repo kind) and
// excluded (ambient elsewhere), each excluded ref carrying a why.
func partition(pol OrientationPolicy, repoKind string) ([]envelope.IncludedRef, []envelope.ExcludedRef) {
	incl := []envelope.IncludedRef{}
	excl := []envelope.ExcludedRef{}
	for _, s := range pol.Sources {
		if contains(s.AmbientFor, repoKind) {
			incl = append(incl, envelope.IncludedRef{
				Ref:         s.Ref,
				SourceClass: s.SourceClass,
				Reason:      "ambient for " + repoKind + " repos",
				Confidence:  1,
				Status:      "current",
			})
			continue
		}
		excl = append(excl, envelope.ExcludedRef{
			Ref:    s.Ref,
			Reason: fmt.Sprintf("%s context (ambient for %s) is out of scope in a %s repo", s.Class, strings.Join(s.AmbientFor, "+"), repoKind),
		})
	}
	return incl, excl
}

// header builds a runtime, private, tier0 (read-only orientation) envelope header.
// Uncertainty is "inferred" when the repo kind was defaulted rather than declared.
func header(sv, id string, now time.Time, in Input) envelope.Header {
	level := "known"
	if in.RepoKindVia == "default" {
		level = "inferred"
	}
	return envelope.Header{
		SchemaVersion: sv,
		ID:            id,
		CreatedAt:     now.UTC().Format(time.RFC3339),
		Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
		Scope:         envelope.Scope{Surface: in.Cwd},
		Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
		Privacy:       "private",
		Risk:          "tier0",
		EvidenceRefs:  []string{},
		Uncertainty:   envelope.Uncertainty{Level: level},
	}
}

// idFor derives a deterministic id from inputs so the same request yields the same id
// (testable; no clock/random in the id).
func idFor(prefix string, parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return prefix + hex.EncodeToString(h[:])[:12]
}

func expandPath(path string) string {
	path = os.ExpandEnv(strings.TrimSpace(path))
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func isWithin(root, cwd string) bool {
	rel, err := filepath.Rel(root, cwd)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
