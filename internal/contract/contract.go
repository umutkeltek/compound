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
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"compound/internal/capgraph"
	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/mem"
	"compound/internal/projection"
)

const (
	defaultRepoKind          = "product"
	memoryRelevanceSourceRef = "memory://relevance-gated"
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
			{Class: "memory-index", SourceClass: "generated", AmbientFor: all, Ref: memoryRelevanceSourceRef},
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

const installedHooksSchemaVersion = "compound.installed_hooks.v0.1"

const (
	InstalledHooksAuthorityRef          = "compound://config/installed-hooks"
	InstalledHooksSourceAuthoritySchema = "compound.installed_hooks_source_authority.v0.1"
	installedHooksSourceAuthorityFile   = "installed-hooks-source-authority.jsonl"
	installedHooksCodexImportedVia      = "contract import-hooks --codex-hooks-json"
	installedHooksRegistryImportedVia   = "contract import-hooks --hook-registration-ts"
)

type InstalledHooksManifest struct {
	SchemaVersion string          `yaml:"schema_version" json:"schema_version"`
	Hooks         []InstalledHook `yaml:"hooks" json:"hooks"`
}

type InstalledHook struct {
	Harness  string `yaml:"harness" json:"harness"`
	Name     string `yaml:"name" json:"name"`
	Event    string `yaml:"event,omitempty" json:"event,omitempty"`
	Matcher  string `yaml:"matcher,omitempty" json:"matcher,omitempty"`
	Ref      string `yaml:"ref" json:"ref"`
	Revision string `yaml:"revision" json:"revision"`
	Status   string `yaml:"status" json:"status"`
}

type InstalledHooksImportReport struct {
	AuthorityRef      string                        `json:"authority_ref"`
	AuthorityPath     string                        `json:"authority_path"`
	SourceAuthorityID string                        `json:"source_authority_id"`
	SourceAuthority   InstalledHooksSourceAuthority `json:"source_authority"`
	HookCount         int                           `json:"hook_count"`
	Harnesses         []string                      `json:"harnesses"`
	Manifest          InstalledHooksManifest        `json:"manifest"`
}

type HookGateReadiness struct {
	Harness         string `json:"harness"`
	Hook            string `json:"hook"`
	NativeBlock     bool   `json:"native_block"`
	Installed       bool   `json:"installed"`
	InstalledRef    string `json:"installed_ref,omitempty"`
	Status          string `json:"status"`
	AdapterRequired bool   `json:"adapter_required,omitempty"`
	AdapterCommand  string `json:"adapter_command,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

type InstalledHooksSourceAuthority struct {
	SchemaVersion       string   `json:"schema_version"`
	ID                  string   `json:"id"`
	CreatedAt           string   `json:"created_at"`
	SourcePath          string   `json:"source_path"`
	SourceRevision      string   `json:"source_revision"`
	ImportedVia         string   `json:"imported_via"`
	AuthorityRef        string   `json:"authority_ref"`
	AuthorityStatus     string   `json:"authority_status"`
	SourceRole          string   `json:"source_role"`
	Retires             string   `json:"retires"`
	RetirementCondition string   `json:"retirement_condition"`
	Rollback            string   `json:"rollback"`
	Harnesses           []string `json:"harnesses"`
	HookCount           int      `json:"hook_count"`
	EvidenceRefs        []string `json:"evidence_refs"`
}

// Compile produces the EffectiveContract: the instructions, hooks, memory/capability/
// mutation policy, and unknowns in force for this harness+repo+task.
func Compile(in Input, pol OrientationPolicy, now time.Time) envelope.EffectiveContract {
	incl, excl := partition(pol, in.RepoKind)
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
		Header:             header(envelope.SVEffectiveContract, contractIDForInput(in), now, in),
		Harness:            in.Harness,
		TaskMode:           in.Mode,
		ActiveInstructions: orEmpty(active),
		ActiveHooks:        []string{},
		ActiveDecisions:    []string{},
		Excluded:           excl,
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

// CompileWithRuntime compiles the static contract and enriches it with runtime
// state owned by COMPOUND_HOME: installed hook manifest, source file revisions,
// and accepted decision claims. The pure Compile function stays for fixtures and
// tests that do not want to touch runtime state.
func CompileWithRuntime(in Input, pol OrientationPolicy, homePath string, now time.Time) (envelope.EffectiveContract, error) {
	ec := Compile(in, pol, now)
	if strings.TrimSpace(homePath) == "" {
		return ec, nil
	}
	ec.SourceRevisions = resolveSourceRevisions(ec.SourceRevisions, in.Cwd)
	hooks, excludedHooks, hookRoster, hookRevs, err := loadInstalledHooksForInput(homePath, in)
	if err != nil {
		return ec, err
	}
	ec.ActiveHooks = hooks
	ec.HookRoster = hookRoster
	hookReadiness, err := hookReadinessForInput(homePath, in)
	if err != nil {
		return ec, err
	}
	ec.HookReadiness = hookReadiness
	ec.Excluded = append(ec.Excluded, excludedHooks...)
	ec.SourceRevisions = mergeSourceRevisions(ec.SourceRevisions, hookRevs)
	capabilityHits, surfacedTotal, err := surfaceCapabilityHits(homePath, in)
	if err != nil {
		return ec, err
	}
	for _, hit := range capabilityHits {
		ec.CapabilityOffers = append(ec.CapabilityOffers, capabilityOffer(hit))
	}
	if surfacedTotal > 0 {
		rev, err := capgraph.AuthorityRevision(homePath)
		if err != nil {
			return ec, err
		}
		ec.SourceRevisions = mergeSourceRevisions(ec.SourceRevisions, []envelope.SourceRev{{Source: capgraph.CapabilityGraphAuthorityRef, Rev: rev}})
	}
	decisions, decisionRevs, excludedDecisions, err := loadDecisionRefs(homePath, in)
	if err != nil {
		return ec, err
	}
	ec.ActiveDecisions = decisions
	ec.Excluded = append(ec.Excluded, excludedDecisions...)
	ec.SourceRevisions = mergeSourceRevisions(ec.SourceRevisions, decisionRevs)
	memoryRefs, err := loadMemoryRefs(homePath, in, pol.Memory[in.RepoKind])
	if err != nil {
		return ec, err
	}
	for _, ref := range memoryRefs {
		ec.ActiveMemory = append(ec.ActiveMemory, ref.Ref)
	}
	if len(memoryRefs) > 0 {
		rev, err := memoryLedgerRevision(homePath)
		if err != nil {
			return ec, err
		}
		ec.SourceRevisions = mergeSourceRevisions(ec.SourceRevisions, []envelope.SourceRev{{Source: memoryRelevanceSourceRef, Rev: rev}})
	}
	return ec, nil
}

type EffectiveContractArtifactPaths struct {
	ContractPath string `json:"contract_path"`
	LatestPath   string `json:"latest_path"`
}

type ContextPackArtifactPaths struct {
	PackPath string `json:"pack_path"`
}

func RecordEffectiveContract(homePath string, ec envelope.EffectiveContract) (EffectiveContractArtifactPaths, error) {
	if strings.TrimSpace(homePath) == "" {
		return EffectiveContractArtifactPaths{}, fmt.Errorf("compound home required")
	}
	if err := ec.Validate(); err != nil {
		return EffectiveContractArtifactPaths{}, fmt.Errorf("effective contract invalid: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339, ec.CreatedAt)
	if err != nil {
		return EffectiveContractArtifactPaths{}, fmt.Errorf("effective contract created_at invalid: %w", err)
	}
	date := createdAt.UTC().Format("2006-01-02")
	base := filepath.Join(homePath, "contracts", "effective")
	contractPath := filepath.Join(base, date, ec.ID+".json")
	latestPath := filepath.Join(base, "latest.json")
	if err := os.MkdirAll(filepath.Dir(contractPath), 0o755); err != nil {
		return EffectiveContractArtifactPaths{}, err
	}
	if err := os.MkdirAll(filepath.Dir(latestPath), 0o755); err != nil {
		return EffectiveContractArtifactPaths{}, err
	}
	if err := writeContractJSON(contractPath, ec); err != nil {
		return EffectiveContractArtifactPaths{}, err
	}
	if err := writeContractJSON(latestPath, ec); err != nil {
		return EffectiveContractArtifactPaths{}, err
	}
	return EffectiveContractArtifactPaths{ContractPath: contractPath, LatestPath: latestPath}, nil
}

func writeContractJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func RecordContextPack(homePath string, pack envelope.ContextPack) (ContextPackArtifactPaths, error) {
	if strings.TrimSpace(homePath) == "" {
		return ContextPackArtifactPaths{}, fmt.Errorf("compound home required")
	}
	if err := pack.Validate(); err != nil {
		return ContextPackArtifactPaths{}, fmt.Errorf("context pack invalid: %w", err)
	}
	packPath := filepath.Join(homePath, "context", "packs", pack.ID+".json")
	if err := os.MkdirAll(filepath.Dir(packPath), 0o755); err != nil {
		return ContextPackArtifactPaths{}, err
	}
	if err := writeContractJSON(packPath, pack); err != nil {
		return ContextPackArtifactPaths{}, err
	}
	return ContextPackArtifactPaths{PackPath: packPath}, nil
}

// Pack assembles the ContextPack an agent orients from. Its EXCLUDED list — what was
// NOT loaded and why — is the whole point: it is required and never silently dropped.
func Pack(in Input, pol OrientationPolicy, now time.Time) envelope.ContextPack {
	incl, excl := partition(pol, in.RepoKind)
	var active []string
	var revs []envelope.SourceRev
	for _, r := range incl {
		if r.SourceClass == "source" {
			active = append(active, r.Ref)
		}
		revs = append(revs, envelope.SourceRev{Source: r.Ref, Rev: "unknown"})
	}
	mem := pol.Memory[in.RepoKind]
	summary := fmt.Sprintf("Oriented as a **%s** repo for `%s`: %d sources included, %d excluded.",
		in.RepoKind, in.Harness, len(incl), len(excl))
	if len(excl) > 0 {
		summary += " Out-of-scope context recorded — see `excluded`."
	}
	hdr := header(envelope.SVContextPack, idFor("ctx_", in.Harness, in.RepoKind, in.Task), now, in)
	hdr.IDs.ContractID = contractIDForInput(in)
	return envelope.ContextPack{
		Header:             hdr,
		SummaryMD:          summary,
		Included:           incl,
		Excluded:           excl,
		ActiveInstructions: orEmpty(active),
		ActiveHooks:        []string{},
		ActiveDecisions:    []string{},
		ActiveMemory:       []string{},
		SourceRevisions:    resolveSourceRevisions(revs, in.Cwd),
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
	hooks, excludedHooks, hookRoster, hookRevs, err := loadInstalledHooksForInput(homePath, in)
	if err != nil {
		return pack, err
	}
	pack.ActiveHooks = hooks
	pack.HookRoster = hookRoster
	hookReadiness, err := hookReadinessForInput(homePath, in)
	if err != nil {
		return pack, err
	}
	pack.HookReadiness = hookReadiness
	pack.Excluded = append(pack.Excluded, excludedHooks...)
	pack.SourceRevisions = mergeSourceRevisions(pack.SourceRevisions, hookRevs)
	if len(hooks) > 0 {
		pack.SummaryMD += fmt.Sprintf(" %d active hook ref(s) included Compound runtime.", len(hooks))
	}
	decisions, decisionRevs, excludedDecisions, err := loadDecisionRefs(homePath, in)
	if err != nil {
		return pack, err
	}
	pack.Excluded = append(pack.Excluded, excludedDecisions...)
	for _, ref := range decisions {
		pack.Included = append(pack.Included, envelope.IncludedRef{
			Ref:         ref,
			SourceClass: "runtime",
			Reason:      "accepted decision from Compound memory ledger; old source is source-in-place evidence",
			Confidence:  1,
			Status:      "current",
		})
	}
	pack.ActiveDecisions = decisions
	pack.SourceRevisions = mergeSourceRevisions(pack.SourceRevisions, decisionRevs)
	if len(decisions) > 0 {
		pack.SummaryMD += fmt.Sprintf(" %d active decision ref(s) included from Compound runtime.", len(decisions))
	}
	memoryRefs, err := loadMemoryRefs(homePath, in, pol.Memory[in.RepoKind])
	if err != nil {
		return pack, err
	}
	for _, ref := range memoryRefs {
		pack.ActiveMemory = append(pack.ActiveMemory, ref.Ref)
		pack.Included = append(pack.Included, envelope.IncludedRef{
			Ref:         ref.Ref,
			SourceClass: "runtime",
			Reason:      ref.Reason,
			Confidence:  ref.Confidence,
			Status:      "current",
		})
	}
	if len(memoryRefs) > 0 {
		rev, err := memoryLedgerRevision(homePath)
		if err != nil {
			return pack, err
		}
		pack.SourceRevisions = mergeSourceRevisions(pack.SourceRevisions, []envelope.SourceRev{{Source: memoryRelevanceSourceRef, Rev: rev}})
		lines := make([]string, 0, len(memoryRefs))
		for _, ref := range memoryRefs {
			lines = append(lines, ref.Line)
		}
		pack.SummaryMD += " Compound memory (owned ledger): " + strings.Join(lines, "; ")
	}
	surfaced, surfacedTotal, err := surfaceCapabilityHits(homePath, in)
	if err != nil {
		return pack, err
	}
	if surfacedTotal > 0 {
		rev, err := capgraph.AuthorityRevision(homePath)
		if err != nil {
			return pack, err
		}
		pack.SourceRevisions = mergeSourceRevisions(pack.SourceRevisions, []envelope.SourceRev{{Source: capgraph.CapabilityGraphAuthorityRef, Rev: rev}})
		for _, hit := range surfaced {
			pack.CapabilityOffers = append(pack.CapabilityOffers, capabilityOffer(hit))
			pack.Included = append(pack.Included, envelope.IncludedRef{
				Ref:         "capability://" + hit.Node.ID,
				SourceClass: "generated",
				Reason:      capabilitySurfaceReason(hit),
				Confidence:  hit.Score,
				Status:      "current",
			})
		}
		if surfacedTotal > len(surfaced) {
			pack.SummaryMD += fmt.Sprintf(" %d of %d capability offer(s) surfaced; %d omitted from context budget; offers are not execution.", len(surfaced), surfacedTotal, surfacedTotal-len(surfaced))
		} else {
			pack.SummaryMD += fmt.Sprintf(" %d capability offer(s) surfaced; offers are not execution.", len(surfaced))
		}
	}
	externalIntake, err := capgraph.ExternalRepoIntakeReportForHome(homePath, in.Task)
	if err != nil {
		return pack, err
	}
	if len(externalIntake.Matches) > 0 {
		matches := externalIntake.Matches
		if len(matches) > maxContextExternalRepoIntakeRefs {
			matches = matches[:maxContextExternalRepoIntakeRefs]
		}
		for _, match := range matches {
			pack.Included = append(pack.Included, envelope.IncludedRef{
				Ref:         externalRepoIntakeRef(match.Repo),
				SourceClass: "generated",
				Reason:      externalRepoIntakeReason(match),
				Confidence:  externalRepoIntakeConfidence(match),
				Status:      "current",
			})
		}
		if len(externalIntake.Matches) > len(matches) {
			pack.SummaryMD += fmt.Sprintf(" %d of %d external repo intake match(es) included; intake is review evidence, not adoption.", len(matches), len(externalIntake.Matches))
		} else {
			pack.SummaryMD += fmt.Sprintf(" %d external repo intake match(es) included; intake is review evidence, not adoption.", len(matches))
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

func surfaceCapabilityHits(homePath string, in Input) ([]capgraph.Surfaced, int, error) {
	if !capgraph.Exists(homePath) {
		return nil, 0, nil
	}
	store, err := capgraph.Open(homePath)
	if err != nil {
		return nil, 0, err
	}
	surfaced, err := store.SurfaceForHarness(in.Task, in.Harness)
	if err != nil {
		return nil, 0, err
	}
	surfacedTotal := len(surfaced)
	if len(surfaced) > maxContextCapabilityOffers {
		surfaced = surfaced[:maxContextCapabilityOffers]
	}
	return surfaced, surfacedTotal, nil
}

func capabilitySurfaceReason(hit capgraph.Surfaced) string {
	reason := fmt.Sprintf("capability_graph surface score=%.1f kind=%s why=%s", hit.Score, hit.Node.Kind, hit.Why)
	if len(hit.MatchedTokens) > 0 {
		reason += " matched=" + strings.Join(hit.MatchedTokens, ",")
	}
	return reason
}

func capabilityOffer(hit capgraph.Surfaced) envelope.CapabilityOffer {
	return envelope.CapabilityOffer{
		Ref:                   "capability://" + hit.Node.ID,
		Kind:                  hit.Node.Kind,
		Name:                  hit.Node.Name,
		Score:                 hit.Score,
		Why:                   hit.Why,
		Required:              capabilityOfferConstraints(hit.Constraints.Required),
		Conflicts:             capabilityOfferConstraints(hit.Constraints.Conflicts),
		Harnesses:             capabilityOfferConstraints(hit.Constraints.Harnesses),
		KnownMissedWhen:       capabilityOfferConstraints(hit.Constraints.KnownMissedWhen),
		KnownFalseTriggeredBy: capabilityOfferConstraints(hit.Constraints.KnownFalseTriggeredBy),
		Evidence:              capabilityOfferConstraints(hit.Constraints.Evidence),
		CostRiskNote:          hit.Constraints.CostRiskNote,
	}
}

func capabilityOfferConstraints(values []capgraph.SurfacingConstraint) []envelope.CapabilityOfferConstraint {
	out := make([]envelope.CapabilityOfferConstraint, 0, len(values))
	for _, value := range values {
		out = append(out, envelope.CapabilityOfferConstraint{
			Kind:       value.Kind,
			Ref:        value.Ref,
			Detail:     value.Detail,
			Confidence: value.Confidence,
		})
	}
	return out
}

const (
	maxContextMemoryRefs             = 5
	maxContextCapabilityOffers       = 12
	maxContextExternalRepoIntakeRefs = 5
)

func externalRepoIntakeRef(repo string) string {
	slug := strings.ToLower(strings.TrimSpace(repo))
	slug = strings.TrimSuffix(slug, ".git")
	slug = strings.Trim(slug, "/")
	if slug == "" {
		slug = "unknown"
	}
	return capgraph.ExternalRepoIntakeAuthorityRef + "/" + slug
}

func externalRepoIntakeReason(match capgraph.ExternalRepoIntakeDecision) string {
	parts := []string{
		fmt.Sprintf("external_repo_intake repo=%s decision=%s", match.Repo, match.Decision),
	}
	if match.Lane != "" {
		parts = append(parts, "lane="+match.Lane)
	}
	if match.Owner != "" {
		parts = append(parts, "owner="+match.Owner)
	}
	if match.NextReviewStep != "" {
		parts = append(parts, "next_review_step="+match.NextReviewStep)
	}
	if match.NotToDo != "" {
		parts = append(parts, "boundary="+match.NotToDo)
	}
	return strings.Join(parts, "; ")
}

func externalRepoIntakeConfidence(match capgraph.ExternalRepoIntakeDecision) float64 {
	if match.EvidenceStrength > 0 {
		return match.EvidenceStrength
	}
	switch match.Decision {
	case "keep_needs_review":
		return 0.72
	case "merge_existing":
		return 0.66
	case "reference_only":
		return 0.58
	default:
		return 0.5
	}
}

type contextMemoryRef struct {
	Ref        string
	Reason     string
	Line       string
	Confidence float64
	Seq        int64
	Rank       float64
}

func loadMemoryRefs(homePath string, in Input, rule MemoryRule) ([]contextMemoryRef, error) {
	if len(rule.Ambient) == 0 {
		return nil, nil
	}
	entities := memoryEntityKeys(in)
	taskTokens := memoryTextTokens(in.Task)
	if len(entities) == 0 && len(taskTokens) == 0 {
		return nil, nil
	}
	store, err := mem.Open(homePath)
	if err != nil {
		return nil, err
	}
	claims, err := store.AllCurrent()
	if err != nil {
		return nil, err
	}
	out := make([]contextMemoryRef, 0, maxContextMemoryRefs)
	for _, claim := range claims {
		rank := ambientMemoryRank(claim, rule, entities, taskTokens)
		if rank <= 0 {
			continue
		}
		line := memoryLine(claim)
		out = append(out, contextMemoryRef{
			Ref:        "claim://" + claim.ID,
			Reason:     "Compound memory ledger claim; source stores and indexes are evidence, not authority: " + line,
			Line:       line,
			Confidence: claim.Confidence,
			Seq:        claim.Seq,
			Rank:       rank,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank > out[j].Rank
		}
		if out[i].Seq == out[j].Seq {
			return out[i].Ref < out[j].Ref
		}
		return out[i].Seq > out[j].Seq
	})
	if len(out) > maxContextMemoryRefs {
		out = out[:maxContextMemoryRefs]
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out, nil
}

func memoryLedgerRevision(homePath string) (string, error) {
	store, err := mem.Open(homePath)
	if err != nil {
		return "", err
	}
	return store.LedgerDigest()
}

func ambientMemoryRank(claim mem.Claim, rule MemoryRule, entities, taskTokens map[string]bool) float64 {
	if claim.ClaimType == "decision" {
		return 0
	}
	if volatileOperationalRepoStateClaim(claim) {
		return 0
	}
	if !memoryVisibilityAllowedForOrient(claim) {
		return 0
	}
	if (contains(rule.Forbidden, "personal-corpus") || contains(rule.ExplicitOnly, "personal-corpus")) && personalMemoryClaim(claim) {
		return 0
	}
	if (contains(rule.Forbidden, "self-model") || contains(rule.ExplicitOnly, "self-model")) && selfModelClaim(claim) {
		return 0
	}
	rank := 0.0
	if entities[memoryKey(claim.Entity)] {
		rank += 2
	}
	rank += memoryTaskOverlapScore(claim, taskTokens)
	return rank
}

func memoryTaskOverlapScore(claim mem.Claim, taskTokens map[string]bool) float64 {
	if len(taskTokens) == 0 {
		return 0
	}
	claimTokens := memoryTextTokens(claim.Entity + " " + claim.Predicate + " " + claim.Object)
	for k, v := range claim.Qualifiers {
		for token := range memoryTextTokens(k + " " + v) {
			claimTokens[token] = true
		}
	}
	score := 0.0
	for token := range taskTokens {
		if claimTokens[token] {
			score++
		}
	}
	return score
}

func volatileOperationalRepoStateClaim(claim mem.Claim) bool {
	if !strings.EqualFold(strings.TrimSpace(claim.Visibility), "operational") {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(claim.ClaimType), "status") {
		return false
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(claim.Entity)), "repo:") {
		return false
	}
	predicate := memoryKey(claim.Predicate)
	switch predicate {
	case "head", "aheadcount", "behindcount", "stagedcount", "unstagedcount", "untrackedcount", "changedpathcount", "sourcecontrolstatus":
		return true
	default:
		return false
	}
}

func memoryEntityKeys(in Input) map[string]bool {
	keys := map[string]bool{}
	add := func(value string) {
		key := memoryKey(value)
		if len(key) >= 3 {
			keys[key] = true
		}
	}
	add(filepath.Base(in.Cwd))
	for _, part := range strings.Fields(in.Task) {
		add(part)
	}
	return keys
}

// memoryStopwords are function words dropped from task/claim token overlap so
// ambient memory selection keys on content-bearing tokens only; short tokens
// (<3 chars) are already dropped by memoryTextTokens.
var memoryStopwords = map[string]bool{
	"about": true, "after": true, "against": true, "all": true, "also": true,
	"and": true, "any": true, "are": true, "because": true, "been": true,
	"before": true, "being": true, "between": true, "both": true, "but": true,
	"can": true, "could": true, "did": true, "does": true, "during": true,
	"each": true, "for": true, "from": true, "had": true, "has": true,
	"have": true, "her": true, "here": true, "him": true, "his": true,
	"how": true, "into": true, "its": true, "just": true, "may": true,
	"might": true, "more": true, "most": true, "must": true, "not": true,
	"off": true, "only": true, "onto": true, "other": true, "our": true,
	"out": true, "over": true, "per": true, "shall": true, "she": true,
	"should": true, "some": true, "such": true, "than": true, "that": true,
	"the": true, "their": true, "them": true, "then": true, "there": true,
	"these": true, "they": true, "this": true, "those": true, "through": true,
	"too": true, "under": true, "upon": true, "very": true, "via": true,
	"was": true, "were": true, "what": true, "when": true, "where": true,
	"which": true, "while": true, "who": true, "why": true, "will": true,
	"with": true, "within": true, "without": true, "would": true, "you": true,
	"your": true,
}

func memoryTextTokens(value string) map[string]bool {
	tokens := map[string]bool{}
	for _, part := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	}) {
		key := memoryKey(part)
		if len(key) >= 3 && !memoryStopwords[key] {
			tokens[key] = true
		}
	}
	return tokens
}

func memoryKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if i := strings.LastIndex(value, ":"); i >= 0 && i+1 < len(value) {
		value = value[i+1:]
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func memoryVisibilityAllowed(visibility string) bool {
	switch strings.ToLower(strings.TrimSpace(visibility)) {
	case "", "ambient", "contextual", "operational", "world", "public":
		return true
	default:
		return false
	}
}

func memoryVisibilityAllowedForOrient(claim mem.Claim) bool {
	return memoryVisibilityAllowed(claim.Visibility)
}

func selfModelClaim(claim mem.Claim) bool {
	if memoryKey(claim.Entity) == "umut" || memoryKey(claim.Entity) == "operator" {
		return true
	}
	return qualifierContains(claim, "domain", "self") || qualifierContains(claim, "domain", "personal")
}

func personalMemoryClaim(claim mem.Claim) bool {
	return strings.EqualFold(claim.Visibility, "private") ||
		strings.EqualFold(claim.ClaimType, "preference") ||
		selfModelClaim(claim) ||
		qualifierContains(claim, "source", "corpus")
}

func qualifierContains(claim mem.Claim, key, needle string) bool {
	if claim.Qualifiers == nil {
		return false
	}
	return strings.Contains(strings.ToLower(claim.Qualifiers[key]), needle)
}

func memoryLine(claim mem.Claim) string {
	text := fmt.Sprintf("%s.%s = %s", claim.Entity, claim.Predicate, claim.Object)
	const max = 180
	if len(text) <= max {
		return text
	}
	return strings.TrimSpace(text[:max-3]) + "..."
}

func loadDecisionRefs(homePath string, in Input) ([]string, []envelope.SourceRev, []envelope.ExcludedRef, error) {
	store, err := mem.Open(homePath)
	if err != nil {
		return nil, nil, nil, err
	}
	claims, err := store.AllCurrent()
	if err != nil {
		return nil, nil, nil, err
	}
	byRef := map[string]mem.Claim{}
	for _, c := range claims {
		if c.ClaimType != "decision" || c.Qualifiers == nil || canonicalDecisionStatus(c.Qualifiers["lifecycle_status"]) != "accepted" {
			continue
		}
		id := strings.TrimSpace(c.Qualifiers["decision_id"])
		if id == "" {
			id = c.ID
		}
		ref := "decision://" + id
		if prior, ok := byRef[ref]; !ok || c.Seq > prior.Seq || (c.Seq == prior.Seq && c.ID > prior.ID) {
			byRef[ref] = c
		}
	}
	refs := make([]string, 0, len(byRef))
	for ref := range byRef {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	active := refs
	var excluded []envelope.ExcludedRef
	if !decisionScopeIncludesAll(in.RepoKind) {
		active = make([]string, 0, len(refs))
		taskTokens := memoryTextTokens(in.Task + " " + filepath.Base(in.Cwd))
		for _, ref := range refs {
			if decisionRelevantToTask(byRef[ref], taskTokens) {
				active = append(active, ref)
				continue
			}
			excluded = append(excluded, envelope.ExcludedRef{
				Ref:    ref,
				Reason: "accepted decision from Compound ledger omitted: not relevant to repo/task scoped context",
			})
		}
	}
	revs := make([]envelope.SourceRev, 0, len(refs))
	for _, ref := range active {
		revs = append(revs, envelope.SourceRev{Source: ref, Rev: "claim:" + byRef[ref].ID})
	}
	return active, revs, excluded, nil
}

func decisionScopeIncludesAll(repoKind string) bool {
	return strings.TrimSpace(repoKind) == "control-plane"
}

func decisionRelevantToTask(claim mem.Claim, taskTokens map[string]bool) bool {
	if len(taskTokens) == 0 {
		return false
	}
	decisionTokens := memoryTextTokens(claim.Entity + " " + claim.Predicate + " " + claim.Object)
	for key, value := range claim.Qualifiers {
		for token := range memoryTextTokens(key + " " + value) {
			decisionTokens[token] = true
		}
	}
	for token := range taskTokens {
		if decisionTokens[token] {
			return true
		}
	}
	return false
}

func canonicalDecisionStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		return "accepted"
	}
	return status
}

type codexHooksFile struct {
	Hooks map[string][]codexHookGroup `json:"hooks"`
}

type codexHookGroup struct {
	Hooks []codexHookCommand `json:"hooks"`
}

type codexHookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

func InstalledHooksPath(homePath string) string {
	return filepath.Join(homePath, "config", "installed-hooks.yaml")
}

func InstalledHooksSourceAuthorityPath(homePath string) string {
	return filepath.Join(homePath, "registries", installedHooksSourceAuthorityFile)
}

func LoadInstalledHooksManifest(homePath string) (InstalledHooksManifest, error) {
	return loadInstalledHooksManifest(InstalledHooksPath(homePath))
}

func LoadInstalledHooksSourceAuthority(homePath string) ([]InstalledHooksSourceAuthority, error) {
	data, err := os.ReadFile(InstalledHooksSourceAuthorityPath(homePath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	byID := map[string]InstalledHooksSourceAuthority{}
	var order []string
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec InstalledHooksSourceAuthority
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("parse installed hooks source authority line %d: %w", i+1, err)
		}
		if strings.TrimSpace(rec.ID) == "" {
			return nil, fmt.Errorf("installed hooks source authority line %d missing id", i+1)
		}
		if _, seen := byID[rec.ID]; !seen {
			order = append(order, rec.ID)
		}
		byID[rec.ID] = rec
	}
	records := make([]InstalledHooksSourceAuthority, 0, len(order))
	for _, id := range order {
		records = append(records, byID[id])
	}
	return records, nil
}

type InstalledHooksSourceAuthoritySummary struct {
	SourceRoleCounts            map[string]int `json:"source_role_counts,omitempty"`
	RetiredSourceAuthorityCount int            `json:"retired_source_authority_count"`
	RetiredSourceAuthorityRefs  []string       `json:"retired_source_authority_refs,omitempty"`
}

func SummarizeInstalledHooksSourceAuthority(records []InstalledHooksSourceAuthority) InstalledHooksSourceAuthoritySummary {
	out := InstalledHooksSourceAuthoritySummary{SourceRoleCounts: map[string]int{}}
	retired := map[string]bool{}
	for _, rec := range records {
		role := strings.TrimSpace(rec.SourceRole)
		if role == "" {
			role = "unknown"
		}
		out.SourceRoleCounts[role]++
		if ref := strings.TrimSpace(rec.Retires); ref != "" && !retired[ref] {
			retired[ref] = true
			out.RetiredSourceAuthorityRefs = append(out.RetiredSourceAuthorityRefs, ref)
		}
	}
	sort.Strings(out.RetiredSourceAuthorityRefs)
	out.RetiredSourceAuthorityCount = len(out.RetiredSourceAuthorityRefs)
	return out
}

func ImportCodexHooksJSON(homePath, sourcePath string, now time.Time) (InstalledHooksImportReport, error) {
	if strings.TrimSpace(homePath) == "" {
		return InstalledHooksImportReport{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if strings.TrimSpace(sourcePath) == "" {
		return InstalledHooksImportReport{}, fmt.Errorf("source path required")
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return InstalledHooksImportReport{}, err
	}
	manifest, err := parseCodexHooksJSON(data)
	if err != nil {
		return InstalledHooksImportReport{}, err
	}
	return importInstalledHooks(homePath, sourcePath, data, manifest, installedHooksCodexImportedVia, now)
}

func ImportHookRegistrationTS(homePath, sourcePath string, now time.Time) (InstalledHooksImportReport, error) {
	if strings.TrimSpace(homePath) == "" {
		return InstalledHooksImportReport{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if strings.TrimSpace(sourcePath) == "" {
		return InstalledHooksImportReport{}, fmt.Errorf("source path required")
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return InstalledHooksImportReport{}, err
	}
	manifest, err := parseHookRegistrationTS(data)
	if err != nil {
		return InstalledHooksImportReport{}, err
	}
	return importInstalledHooks(homePath, sourcePath, data, manifest, installedHooksRegistryImportedVia, now)
}

func importInstalledHooks(homePath, sourcePath string, data []byte, manifest InstalledHooksManifest, importedVia string, now time.Time) (InstalledHooksImportReport, error) {
	if err := os.MkdirAll(filepath.Join(homePath, "config"), 0o755); err != nil {
		return InstalledHooksImportReport{}, err
	}
	authorityPath := InstalledHooksPath(homePath)
	manifestData, err := yaml.Marshal(manifest)
	if err != nil {
		return InstalledHooksImportReport{}, err
	}
	if err := os.WriteFile(authorityPath, manifestData, 0o644); err != nil {
		return InstalledHooksImportReport{}, err
	}
	sourceRevision := hashBytes(data)
	rec := InstalledHooksSourceAuthority{
		SchemaVersion:       InstalledHooksSourceAuthoritySchema,
		ID:                  installedHooksSourceAuthorityID(sourcePath, sourceRevision, importedVia),
		CreatedAt:           now.UTC().Format(time.RFC3339),
		SourcePath:          sourcePath,
		SourceRevision:      sourceRevision,
		ImportedVia:         importedVia,
		AuthorityRef:        InstalledHooksAuthorityRef,
		AuthorityStatus:     "compound_primary",
		SourceRole:          "evidence_only_input_after_import",
		Retires:             "source_view_authority:" + sourcePath,
		RetirementCondition: "EffectiveContract reads compound://config/installed-hooks from COMPOUND_HOME; source view remains rollback/evidence input",
		Rollback:            "re-import source hooks JSON or remove COMPOUND_HOME config/installed-hooks.yaml",
		Harnesses:           installedHookHarnesses(manifest),
		HookCount:           len(manifest.Hooks),
		EvidenceRefs:        []string{sourceRevision, InstalledHooksAuthorityRef},
	}
	if err := appendInstalledHooksSourceAuthority(homePath, rec); err != nil {
		return InstalledHooksImportReport{}, err
	}
	return InstalledHooksImportReport{
		AuthorityRef:      InstalledHooksAuthorityRef,
		AuthorityPath:     authorityPath,
		SourceAuthorityID: rec.ID,
		SourceAuthority:   rec,
		HookCount:         len(manifest.Hooks),
		Harnesses:         rec.Harnesses,
		Manifest:          manifest,
	}, nil
}

func parseHookRegistrationTS(data []byte) (InstalledHooksManifest, error) {
	body, err := hookRegistrationArrayBody(string(data))
	if err != nil {
		return InstalledHooksManifest{}, err
	}
	objects, err := splitTopLevelObjects(body)
	if err != nil {
		return InstalledHooksManifest{}, err
	}
	manifest := InstalledHooksManifest{SchemaVersion: installedHooksSchemaVersion}
	seen := map[string]bool{}
	for i, obj := range objects {
		rawHarness, ok := tsStringField(obj, "harness")
		if !ok {
			return InstalledHooksManifest{}, fmt.Errorf("hook registration %d missing harness", i+1)
		}
		harness := normalizeInstalledHookHarness(rawHarness)
		event, ok := tsStringField(obj, "event")
		if !ok {
			return InstalledHooksManifest{}, fmt.Errorf("hook registration %d missing event", i+1)
		}
		file, ok := tsStringField(obj, "file")
		if !ok {
			return InstalledHooksManifest{}, fmt.Errorf("hook registration %d missing file", i+1)
		}
		command, ok := tsStringField(obj, "command")
		if !ok {
			return InstalledHooksManifest{}, fmt.Errorf("hook registration %d missing command", i+1)
		}
		matcher, _ := tsStringField(obj, "matcher")
		ref := expandHomeRef(hookRefFromCommand(command))
		if ref == "" {
			ref = hookRefFromHarnessFile(rawHarness, file)
		}
		key := strings.Join([]string{harness, event, ref}, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		manifest.Hooks = append(manifest.Hooks, InstalledHook{
			Harness: harness,
			Name:    event,
			Event:   event,
			Matcher: matcher,
			Ref:     ref,
			Status:  "current",
		})
	}
	if len(manifest.Hooks) == 0 {
		return InstalledHooksManifest{}, fmt.Errorf("hook-registration ts has no hook registrations")
	}
	sort.Slice(manifest.Hooks, func(i, j int) bool {
		a, b := manifest.Hooks[i], manifest.Hooks[j]
		return strings.Join([]string{a.Harness, a.Name, a.Ref}, "\x00") < strings.Join([]string{b.Harness, b.Name, b.Ref}, "\x00")
	})
	return manifest, nil
}

func normalizeInstalledHookHarness(harness string) string {
	switch strings.TrimSpace(harness) {
	case "claude":
		return "claude_code"
	default:
		return strings.TrimSpace(harness)
	}
}

func hookRegistrationArrayBody(text string) (string, error) {
	idx := strings.Index(text, "HOOK_REGISTRATIONS")
	if idx == -1 {
		return "", fmt.Errorf("HOOK_REGISTRATIONS not found")
	}
	eq := strings.Index(text[idx:], "=")
	if eq == -1 {
		return "", fmt.Errorf("HOOK_REGISTRATIONS assignment not found")
	}
	eq += idx
	start := strings.Index(text[eq:], "[")
	if start == -1 {
		return "", fmt.Errorf("HOOK_REGISTRATIONS array not found")
	}
	start += eq
	end, err := findMatchingDelimited(text, start, '[', ']')
	if err != nil {
		return "", err
	}
	return text[start+1 : end], nil
}

func splitTopLevelObjects(body string) ([]string, error) {
	var objects []string
	for i := 0; i < len(body); i++ {
		if body[i] != '{' {
			continue
		}
		end, err := findMatchingDelimited(body, i, '{', '}')
		if err != nil {
			return nil, err
		}
		objects = append(objects, body[i+1:end])
		i = end
	}
	return objects, nil
}

func findMatchingDelimited(text string, start int, open, close byte) (int, error) {
	if start < 0 || start >= len(text) || text[start] != open {
		return -1, fmt.Errorf("delimiter %q not found", open)
	}
	depth := 0
	var quote byte
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return -1, fmt.Errorf("no matching %q found", close)
}

func tsStringField(obj, key string) (string, bool) {
	idx := strings.Index(obj, key+":")
	if idx == -1 {
		return "", false
	}
	rest := strings.TrimSpace(obj[idx+len(key)+1:])
	if rest == "" {
		return "", false
	}
	quote := rest[0]
	if quote != '\'' && quote != '"' && quote != '`' {
		return "", false
	}
	var b strings.Builder
	escaped := false
	for i := 1; i < len(rest); i++ {
		c := rest[i]
		if escaped {
			b.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == quote {
			return strings.TrimSpace(b.String()), true
		}
		b.WriteByte(c)
	}
	return "", false
}

func hookRefFromHarnessFile(harness, file string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch harness {
	case "claude":
		return filepath.Join(home, ".claude", "hooks", file)
	case "codex":
		return filepath.Join(home, ".codex", "hooks", file)
	default:
		return ""
	}
}

func parseCodexHooksJSON(data []byte) (InstalledHooksManifest, error) {
	var src codexHooksFile
	if err := json.Unmarshal(data, &src); err != nil {
		return InstalledHooksManifest{}, fmt.Errorf("parse codex hooks json: %w", err)
	}
	events := make([]string, 0, len(src.Hooks))
	for event := range src.Hooks {
		events = append(events, event)
	}
	sort.Strings(events)
	manifest := InstalledHooksManifest{SchemaVersion: installedHooksSchemaVersion}
	seen := map[string]bool{}
	for _, event := range events {
		for _, group := range src.Hooks[event] {
			for _, hook := range group.Hooks {
				ref := hookRefFromCommand(hook.Command)
				if ref == "" {
					continue
				}
				key := event + "\x00" + ref
				if seen[key] {
					continue
				}
				seen[key] = true
				manifest.Hooks = append(manifest.Hooks, InstalledHook{
					Harness: "codex",
					Name:    event,
					Event:   event,
					Ref:     ref,
					Status:  "current",
				})
			}
		}
	}
	if len(manifest.Hooks) == 0 {
		return InstalledHooksManifest{}, fmt.Errorf("codex hooks json has no command hooks")
	}
	return manifest, nil
}

func hookRefFromCommand(command string) string {
	for _, field := range strings.Fields(command) {
		field = strings.Trim(field, `"'`)
		if strings.Contains(field, ".hook.") || strings.HasSuffix(field, ".hook.ts") {
			return field
		}
	}
	return ""
}

func expandHomeRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ref
	}
	ref = strings.ReplaceAll(ref, "${HOME}", home)
	ref = strings.ReplaceAll(ref, "$HOME", home)
	if strings.HasPrefix(ref, "~/") {
		ref = filepath.Join(home, strings.TrimPrefix(ref, "~/"))
	}
	return ref
}

func loadInstalledHooksManifest(path string) (InstalledHooksManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return InstalledHooksManifest{}, err
	}
	var manifest InstalledHooksManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return InstalledHooksManifest{}, fmt.Errorf("parse installed hooks %s: %w", path, err)
	}
	if manifest.SchemaVersion != installedHooksSchemaVersion {
		return InstalledHooksManifest{}, fmt.Errorf("installed hooks schema_version must be %s, got %q", installedHooksSchemaVersion, manifest.SchemaVersion)
	}
	return manifest, nil
}

func appendInstalledHooksSourceAuthority(homePath string, rec InstalledHooksSourceAuthority) error {
	records, err := LoadInstalledHooksSourceAuthority(homePath)
	if err != nil {
		return err
	}
	for _, existing := range records {
		if existing.ID == rec.ID {
			return nil
		}
	}
	path := InstalledHooksSourceAuthorityPath(homePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

func installedHookHarnesses(manifest InstalledHooksManifest) []string {
	seen := map[string]bool{}
	for _, hook := range manifest.Hooks {
		h := strings.TrimSpace(hook.Harness)
		if h != "" {
			seen[h] = true
		}
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

func installedHooksSourceAuthorityID(path, revision, importedVia string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{path, revision, importedVia}, "|")))
	return "hksrc_" + hex.EncodeToString(sum[:])[:12]
}

func loadInstalledHooks(homePath, harness string) ([]string, []envelope.SourceRev, error) {
	active, _, _, revs, err := loadInstalledHooksForInput(homePath, Input{Harness: harness})
	return active, revs, err
}

func hookReadinessForInput(homePath string, in Input) ([]envelope.HookReadiness, error) {
	if !writeMutationTask(in) && !hookReadinessTask(in) {
		return nil, nil
	}
	path := InstalledHooksPath(homePath)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		path = filepath.Join(homePath, "config", "installed-hooks.json")
	}
	manifest, err := loadInstalledHooksManifest(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows := InstalledHookGateReadiness(manifest, nativePreToolUseBlockMap(harness.DefaultRegistry()))
	out := make([]envelope.HookReadiness, 0, len(rows))
	requestedHarness := strings.TrimSpace(in.Harness)
	for _, row := range rows {
		if requestedHarness != "" && row.Harness != requestedHarness {
			continue
		}
		out = append(out, envelope.HookReadiness{
			Harness:         row.Harness,
			Hook:            row.Hook,
			NativeBlock:     row.NativeBlock,
			Installed:       row.Installed,
			InstalledRef:    row.InstalledRef,
			Status:          row.Status,
			AdapterRequired: row.AdapterRequired,
			AdapterCommand:  row.AdapterCommand,
			Reason:          row.Reason,
		})
	}
	return out, nil
}

func hookReadinessTask(in Input) bool {
	text := strings.ToLower(strings.Join([]string{in.Task, in.Mode}, " "))
	for _, sig := range []string{"hook", "pretooluse", "pre-tool-use", "write gate", "write-gate", "placementguard"} {
		if strings.Contains(text, sig) {
			return true
		}
	}
	return false
}

func nativePreToolUseBlockMap(reg harness.Registry) map[string]bool {
	out := map[string]bool{}
	for name, h := range reg.Harnesses {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out[name] = h.Capabilities.PreToolUseBlock
	}
	return out
}

func loadInstalledHooksForInput(homePath string, in Input) ([]string, []envelope.ExcludedRef, []envelope.HookRosterEntry, []envelope.SourceRev, error) {
	path := InstalledHooksPath(homePath)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		path = filepath.Join(homePath, "config", "installed-hooks.json")
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []string{}, []envelope.ExcludedRef{}, []envelope.HookRosterEntry{}, []envelope.SourceRev{}, nil
	}
	if err != nil {
		return nil, nil, nil, nil, err
	}
	manifest, err := loadInstalledHooksManifest(path)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	active := []string{}
	excluded := []envelope.ExcludedRef{}
	roster := []envelope.HookRosterEntry{}
	writeScopedHarnesses := writeScopedHookHarnesses(manifest)
	hasWriteScopedHook := false
	revs := []envelope.SourceRev{{Source: InstalledHooksAuthorityRef, Rev: hashBytes(data)}}
	for _, hook := range manifest.Hooks {
		if strings.TrimSpace(hook.Name) == "" {
			continue
		}
		if hook.Status != "" && hook.Status != "current" && hook.Status != "active" {
			continue
		}
		if hook.Harness != "" && in.Harness != "" && hook.Harness != in.Harness {
			continue
		}
		if writeScopedHook(hook) {
			hasWriteScopedHook = true
		}
		h := firstNonEmpty(hook.Harness, in.Harness, "unknown")
		ref := "hook://" + h + "/" + hook.Name
		if installedHookExcludedForTask(hook, in) {
			reason := "write-scoped hook excluded from read-only task context; installed hook remains Compound authority/fallback"
			excluded = append(excluded, envelope.ExcludedRef{
				Ref:    ref,
				Reason: reason,
			})
			roster = append(roster, hookRosterEntry(hook, h, ref, "excluded_read_only", reason))
			continue
		}
		active = append(active, ref)
		roster = append(roster, hookRosterEntry(hook, h, ref, "active", installedHookActiveReason(hook, in)))
		rev := strings.TrimSpace(hook.Revision)
		if rev == "" && strings.TrimSpace(hook.Ref) != "" {
			rev = revisionForPath(hook.Ref)
		}
		if rev == "" {
			rev = "unknown"
		}
		revs = append(revs, envelope.SourceRev{Source: ref, Rev: rev})
	}
	if writeMutationTask(in) && strings.TrimSpace(in.Harness) != "" && !hasWriteScopedHook {
		excluded = append(excluded, envelope.ExcludedRef{
			Ref:    "hook://" + in.Harness + "/PreToolUse",
			Reason: missingWriteGateReason(in.Harness, writeScopedHarnesses),
		})
	}
	sort.Strings(active)
	sort.Slice(excluded, func(i, j int) bool { return excluded[i].Ref < excluded[j].Ref })
	sort.Slice(roster, func(i, j int) bool {
		if roster[i].Ref == roster[j].Ref {
			return roster[i].Status < roster[j].Status
		}
		return roster[i].Ref < roster[j].Ref
	})
	sort.Slice(revs, func(i, j int) bool { return revs[i].Source < revs[j].Source })
	return active, excluded, roster, revs, nil
}

func hookRosterEntry(hook InstalledHook, harness, ref, status, reason string) envelope.HookRosterEntry {
	return envelope.HookRosterEntry{
		Ref:     ref,
		Harness: harness,
		Hook:    strings.TrimSpace(hook.Name),
		Event:   firstNonEmpty(strings.TrimSpace(hook.Event), strings.TrimSpace(hook.Name)),
		Matcher: strings.TrimSpace(hook.Matcher),
		Status:  status,
		Reason:  reason,
	}
}

func installedHookActiveReason(hook InstalledHook, in Input) string {
	if writeScopedHook(hook) && writeMutationTask(in) {
		return "write-scoped hook selected for mutation task"
	}
	return "installed hook selected for harness and task"
}

func writeScopedHookHarnesses(manifest InstalledHooksManifest) []string {
	seen := map[string]bool{}
	for _, hook := range manifest.Hooks {
		if strings.TrimSpace(hook.Name) == "" {
			continue
		}
		if hook.Status != "" && hook.Status != "current" && hook.Status != "active" {
			continue
		}
		if !writeScopedHook(hook) {
			continue
		}
		seen[firstNonEmpty(hook.Harness, "unknown")] = true
	}
	out := make([]string, 0, len(seen))
	for harness := range seen {
		out = append(out, harness)
	}
	sort.Strings(out)
	return out
}

func InstalledHookGateReadiness(manifest InstalledHooksManifest, nativePreToolUseBlock map[string]bool) []HookGateReadiness {
	harnesses := map[string]bool{}
	for _, hook := range manifest.Hooks {
		harness := strings.TrimSpace(hook.Harness)
		if harness != "" {
			harnesses[harness] = true
		}
	}
	for harness := range nativePreToolUseBlock {
		harness = strings.TrimSpace(harness)
		if harness != "" {
			harnesses[harness] = true
		}
	}
	names := make([]string, 0, len(harnesses))
	for harness := range harnesses {
		names = append(names, harness)
	}
	sort.Strings(names)

	out := make([]HookGateReadiness, 0, len(names))
	for _, harness := range names {
		installed, ref := installedWriteGateForHarness(manifest, harness)
		nativeBlock := nativePreToolUseBlock[harness]
		row := HookGateReadiness{
			Harness:     harness,
			Hook:        "PreToolUse",
			NativeBlock: nativeBlock,
			Installed:   installed,
		}
		if installed {
			row.InstalledRef = ref
		}
		switch {
		case installed && nativeBlock:
			row.Status = "native_blocking"
			row.Reason = "installed write gate can block in the harness"
		case installed:
			row.Status = "wrapper_required"
			row.AdapterRequired = true
			row.AdapterCommand = "compound hook pre-tool-use --enforce FIXTURE_JSON"
			row.Reason = "installed write gate is fail-open in this harness; wrapper adapter must enforce the CLI exit code"
		case nativeBlock:
			row.Status = "not_installed"
			row.Reason = "harness can block, but no installed write-scoped PreToolUse hook is registered"
		default:
			row.Status = "missing_gate"
			row.AdapterRequired = true
			row.AdapterCommand = "compound hook pre-tool-use --enforce FIXTURE_JSON"
			row.Reason = "no installed write-scoped PreToolUse hook and harness cannot block natively"
		}
		out = append(out, row)
	}
	return out
}

func installedWriteGateForHarness(manifest InstalledHooksManifest, harness string) (bool, string) {
	for _, hook := range manifest.Hooks {
		if strings.TrimSpace(hook.Name) == "" {
			continue
		}
		if hook.Status != "" && hook.Status != "current" && hook.Status != "active" {
			continue
		}
		if strings.TrimSpace(hook.Harness) != strings.TrimSpace(harness) {
			continue
		}
		if !writeScopedHook(hook) {
			continue
		}
		return true, "hook://" + strings.TrimSpace(harness) + "/" + strings.TrimSpace(hook.Name)
	}
	return false, ""
}

func missingWriteGateReason(harness string, availableHarnesses []string) string {
	base := "write-scoped hook missing for mutation task; no installed PreToolUse write gate registered for harness"
	if len(availableHarnesses) == 0 {
		return base
	}
	return base + "; write gate registered for harness(es) " + strings.Join(availableHarnesses, ", ") + " but not requested harness " + harness
}

func installedHookExcludedForTask(hook InstalledHook, in Input) bool {
	return readOnlyTask(in) && writeScopedHook(hook)
}

func writeScopedHook(hook InstalledHook) bool {
	event := strings.ToLower(strings.TrimSpace(firstNonEmpty(hook.Event, hook.Name)))
	matcher := strings.ToLower(strings.TrimSpace(hook.Matcher))
	ref := strings.ToLower(strings.TrimSpace(hook.Ref))
	if event != "pretooluse" {
		return false
	}
	if matcher == "" {
		return true
	}
	return strings.Contains(matcher, "write") ||
		strings.Contains(matcher, "edit") ||
		strings.Contains(matcher, "multiedit") ||
		strings.Contains(ref, "placementguard")
}

func writeMutationTask(in Input) bool {
	text := strings.ToLower(strings.Join([]string{in.Task, in.Mode}, " "))
	if strings.TrimSpace(text) == "" {
		return false
	}
	writeSignals := []string{
		"write", "edit", "modify", "change", "implement", "fix", "patch", "commit",
		"apply", "create", "add", "delete", "remove", "move", "rename",
	}
	for _, sig := range writeSignals {
		if strings.Contains(text, sig) {
			return true
		}
	}
	return false
}

func readOnlyTask(in Input) bool {
	text := strings.ToLower(strings.Join([]string{in.Task, in.Mode}, " "))
	if strings.TrimSpace(text) == "" {
		return false
	}
	mutationSignals := []string{
		"write", "edit", "modify", "change", "implement", "fix", "patch", "commit",
		"apply", "create", "add", "delete", "remove", "move", "rename", "run", "execute",
	}
	for _, sig := range mutationSignals {
		if strings.Contains(text, sig) {
			return false
		}
	}
	readOnlySignals := []string{
		"audit", "inspect", "explain", "review", "status", "list", "show", "read",
		"understand", "analyze", "analyse", "summarize", "summarise", "diagnose",
	}
	for _, sig := range readOnlySignals {
		if strings.Contains(text, sig) {
			return true
		}
	}
	return false
}

func resolveSourceRevisions(revs []envelope.SourceRev, cwd string) []envelope.SourceRev {
	out := append([]envelope.SourceRev(nil), revs...)
	for i := range out {
		if rev := revisionForSource(out[i].Source, cwd); rev != "" {
			out[i].Rev = rev
		}
	}
	return out
}

func revisionForSource(source, cwd string) string {
	switch {
	case source == "AGENTS.md" || source == "CLAUDE.md" || source == "CONTEXT.md":
		if p := findUp(cwd, source); p != "" {
			return revisionForPath(p)
		}
	case strings.HasPrefix(source, "control-plane://"):
		return revisionForControlPlaneSource(source, cwd)
	case source == "repo://docs":
		if p := findUp(cwd, "docs"); p != "" {
			return revisionForPath(p)
		}
	case filepath.IsAbs(source) || strings.HasPrefix(source, "~/"):
		return revisionForPath(source)
	}
	return ""
}

func revisionForControlPlaneSource(source, cwd string) string {
	docsDir := findUp(cwd, "docs")
	if docsDir == "" {
		return ""
	}
	root := filepath.Dir(docsDir)
	var rels []string
	switch source {
	case "control-plane://orientation-map":
		rels = []string{"docs/ARCHITECTURE.md", "docs/LAYERS.md"}
	case "control-plane://workflow-contract":
		rels = []string{"docs/architecture/contracts/README.md"}
	case "control-plane://sop-packets":
		rels = []string{"docs/architecture/runtime.md", "docs/roadmap/phases.md"}
	default:
		return ""
	}
	return revisionForRelativePaths(root, rels)
}

func revisionForRelativePaths(root string, rels []string) string {
	rows := make([]string, 0, len(rels))
	for _, rel := range rels {
		rev := revisionForPath(filepath.Join(root, filepath.FromSlash(rel)))
		if rev == "" {
			return ""
		}
		rows = append(rows, rel+":"+rev)
	}
	sort.Strings(rows)
	return "sha256:" + hashBytes([]byte(strings.Join(rows, "\n")))
}

func revisionForPath(path string) string {
	path = expandPath(path)
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return "sha256:" + hashDir(path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return "sha256:" + hashBytes(data)
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12]
}

func hashDir(root string) string {
	var rows []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rows = append(rows, filepath.ToSlash(rel)+":"+hashBytes(data))
		return nil
	})
	sort.Strings(rows)
	return hashBytes([]byte(strings.Join(rows, "\n")))
}

func findUp(cwd, name string) string {
	dir := expandPath(cwd)
	if dir == "" {
		return ""
	}
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		dir = filepath.Dir(dir)
	}
	for {
		cand := filepath.Join(dir, name)
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func mergeSourceRevisions(groups ...[]envelope.SourceRev) []envelope.SourceRev {
	bySource := map[string]envelope.SourceRev{}
	for _, group := range groups {
		for _, rev := range group {
			if strings.TrimSpace(rev.Source) == "" {
				continue
			}
			bySource[rev.Source] = rev
		}
	}
	keys := make([]string, 0, len(bySource))
	for key := range bySource {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]envelope.SourceRev, 0, len(keys))
	for _, key := range keys {
		out = append(out, bySource[key])
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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

func contractIDForInput(in Input) string {
	return idFor("contract_", in.Harness, in.RepoKind, in.Task)
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
