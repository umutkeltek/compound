// Package harness is the HarnessRegistry — the spine's record of what each agent
// harness ACTUALLY supports and where it is weak. Governance must be capability-aware,
// not fantasy-uniform: a harness that cannot block a tool call natively must be enforced
// via a wrapper/adapter, never trusted to self-gate. This is the public-core, Go-native
// generalization of a control-spine's per-harness capability views.
// See docs/architecture/runtime.md §3.
package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const RegistryManifestSchemaVersion = "compound.harness_registry.v0.1"

const OSEntrypointsAdapterID = "compound.os-harness-entrypoints"

// Capabilities is what a harness can verifiably do across the hook lifecycle.
//
// StopFeedback and SessionEndReceipt are deliberately distinct: some harnesses
// (e.g. codex) fire an end-of-turn Stop event that CAN inject/continue but have
// no SessionEnd event at all. Conflating them into one flag makes the registry
// lie for whichever event the flag doesn't actually describe — see
// docs_private/plan/workstreams/W2-hooks-context-injection.md "SMART HOOK DESIGN".
type Capabilities struct {
	SessionStartContext bool `json:"session_start_context"`
	UserPromptContext   bool `json:"user_prompt_context"`
	PreToolUseBlock     bool `json:"pre_tool_use_block"` // can it actually DENY a tool call?
	PostToolUseEvent    bool `json:"post_tool_use_event"`
	StopFeedback        bool `json:"stop_feedback"`       // can it inject/continue at end-of-turn (Stop)?
	SessionEndReceipt   bool `json:"session_end_receipt"` // does a SessionEnd event exist at all (telemetry-only)?
	NativeSkills        bool `json:"native_skills"`
	MCP                 bool `json:"mcp"`
}

// Harness is one registered executor.
type Harness struct {
	Name                 string       `json:"name"`
	InstructionMechanism string       `json:"instruction_mechanism"`
	Capabilities         Capabilities `json:"capabilities"`
	Limitations          []string     `json:"limitations"`
	InjectBudgetMS       int          `json:"inject_budget_ms,omitempty"`
	Fallback             bool         `json:"fallback,omitempty"` // true when this is the generic stand-in
}

// Registry maps harness name -> capabilities.
type Registry struct{ Harnesses map[string]Harness }

type ManifestSourceRef struct {
	Path        string `json:"path" yaml:"path"`
	Kind        string `json:"kind" yaml:"kind"`
	Owner       string `json:"owner" yaml:"owner"`
	SourceClass string `json:"source_class" yaml:"source_class"`
	Revision    string `json:"revision" yaml:"revision"`
}

type RegistryManifest struct {
	SchemaVersion string            `json:"schema_version" yaml:"schema_version"`
	ID            string            `json:"id" yaml:"id"`
	AdapterID     string            `json:"adapter_id,omitempty" yaml:"adapter_id,omitempty"`
	SourceSystem  string            `json:"source_system,omitempty" yaml:"source_system,omitempty"`
	ObservedAt    string            `json:"observed_at,omitempty" yaml:"observed_at,omitempty"`
	SourceRef     ManifestSourceRef `json:"source_ref" yaml:"source_ref"`
	Harnesses     []ManifestHarness `json:"harnesses" yaml:"harnesses"`
	EvidenceRefs  []string          `json:"evidence_refs,omitempty" yaml:"evidence_refs,omitempty"`
}

type OSEntrypointsFile struct {
	Version      int                        `yaml:"version"`
	Generated    bool                       `yaml:"generated"`
	Status       string                     `yaml:"status"`
	LastReviewed string                     `yaml:"last_reviewed"`
	ReviewAfter  string                     `yaml:"review_after"`
	ExpiresAt    string                     `yaml:"expires_at"`
	MustInclude  string                     `yaml:"must_include"`
	Harnesses    map[string]OSEntrypointRow `yaml:"harnesses"`
}

type OSEntrypointRow struct {
	Entrypoint string `yaml:"entrypoint"`
	Status     string `yaml:"status"`
	Generator  string `yaml:"generator"`
	Check      string `yaml:"check"`
	ExpiresAt  string `yaml:"expires_at"`
}

type ManifestHarness struct {
	Name                 string               `json:"name" yaml:"name"`
	InstructionMechanism string               `json:"instruction_mechanism" yaml:"instruction_mechanism"`
	Capabilities         ManifestCapabilities `json:"capabilities" yaml:"capabilities"`
	Limitations          []string             `json:"limitations" yaml:"limitations"`
	InjectBudgetMS       int                  `json:"inject_budget_ms,omitempty" yaml:"inject_budget_ms,omitempty"`
	Fallback             bool                 `json:"fallback,omitempty" yaml:"fallback,omitempty"`
}

type ManifestCapabilities struct {
	SessionStartContext bool `json:"session_start_context" yaml:"session_start_context"`
	UserPromptContext   bool `json:"user_prompt_context" yaml:"user_prompt_context"`
	PreToolUseBlock     bool `json:"pre_tool_use_block" yaml:"pre_tool_use_block"`
	PostToolUseEvent    bool `json:"post_tool_use_event" yaml:"post_tool_use_event"`
	StopFeedback        bool `json:"stop_feedback" yaml:"stop_feedback"`
	SessionEndReceipt   bool `json:"session_end_receipt" yaml:"session_end_receipt"`
	NativeSkills        bool `json:"native_skills" yaml:"native_skills"`
	MCP                 bool `json:"mcp" yaml:"mcp"`
}

// DefaultRegistry records what the common harnesses verifiably support. Conservative on
// purpose: PreToolUseBlock=false unless the harness is known to enforce it, because
// assuming a block that does not exist is a silent fail-open.
func DefaultRegistry() Registry {
	return Registry{Harnesses: map[string]Harness{
		"claude_code": {
			Name:                 "claude_code",
			InstructionMechanism: "settings + hooks + CLAUDE.md",
			Capabilities: Capabilities{
				SessionStartContext: true, UserPromptContext: true, PreToolUseBlock: true,
				PostToolUseEvent: true, StopFeedback: true, SessionEndReceipt: true,
				NativeSkills: true, MCP: true,
			},
			Limitations:    []string{"long-session instruction drift", "instruction/context budget cap"},
			InjectBudgetMS: 1500,
		},
		"codex": {
			Name:                 "codex",
			InstructionMechanism: "AGENTS.md + skills (progressive disclosure)",
			Capabilities: Capabilities{
				SessionStartContext: true, UserPromptContext: true, PreToolUseBlock: false,
				PostToolUseEvent: true, StopFeedback: true, SessionEndReceipt: false,
				NativeSkills: true, MCP: true,
			},
			Limitations: []string{
				"no verified native pre-tool-use block — enforce via wrapper/adapter",
				"no SessionEnd event — Stop (JSON block/continuation) is the only end-of-turn hook",
				"initial skill list capped (progressive disclosure)",
				"cwd-dependent guidance",
			},
			InjectBudgetMS: 1500,
		},
	}}
}

func LoadRegistryManifest(path string) (RegistryManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RegistryManifest{}, fmt.Errorf("read harness registry %s: %w", path, err)
	}
	var manifest RegistryManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return RegistryManifest{}, fmt.Errorf("parse harness registry %s: %w", path, err)
	}
	if strings.TrimSpace(manifest.SchemaVersion) == "" {
		manifest.SchemaVersion = RegistryManifestSchemaVersion
	}
	if strings.TrimSpace(manifest.ID) == "" {
		manifest.ID = "harness-registry"
	}
	if strings.TrimSpace(manifest.AdapterID) == "" {
		manifest.AdapterID = "compound.harness-registry"
	}
	if strings.TrimSpace(manifest.SourceSystem) == "" {
		manifest.SourceSystem = "compound"
	}
	if strings.TrimSpace(manifest.SourceRef.Path) == "" {
		manifest.SourceRef.Path = path
	}
	if strings.TrimSpace(manifest.SourceRef.Kind) == "" {
		manifest.SourceRef.Kind = "registry"
	}
	if strings.TrimSpace(manifest.SourceRef.Owner) == "" {
		manifest.SourceRef.Owner = manifest.ID
	}
	if strings.TrimSpace(manifest.SourceRef.SourceClass) == "" {
		manifest.SourceRef.SourceClass = "generated"
	}
	if strings.TrimSpace(manifest.SourceRef.Revision) == "" {
		sum := sha256.Sum256(data)
		manifest.SourceRef.Revision = "sha256:" + hex.EncodeToString(sum[:])
	}
	if len(manifest.EvidenceRefs) == 0 {
		manifest.EvidenceRefs = []string{"harness_registry:" + manifest.SourceRef.Revision}
	}
	if _, err := manifest.Registry(); err != nil {
		return RegistryManifest{}, err
	}
	return manifest, nil
}

func LoadOSEntrypointsManifest(path string) (RegistryManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RegistryManifest{}, fmt.Errorf("read OS harness entrypoints %s: %w", path, err)
	}
	var source OSEntrypointsFile
	if err := yaml.Unmarshal(data, &source); err != nil {
		return RegistryManifest{}, fmt.Errorf("parse OS harness entrypoints %s: %w", path, err)
	}
	if len(source.Harnesses) == 0 {
		return RegistryManifest{}, fmt.Errorf("OS harness entrypoints needs at least one harness")
	}
	sum := sha256.Sum256(data)
	revision := "sha256:" + hex.EncodeToString(sum[:])
	manifest := RegistryManifest{
		SchemaVersion: RegistryManifestSchemaVersion,
		ID:            "os-harness-entrypoints",
		AdapterID:     OSEntrypointsAdapterID,
		SourceSystem:  "external-os",
		SourceRef: ManifestSourceRef{
			Path:        path,
			Kind:        "registry",
			Owner:       "harness-entrypoints",
			SourceClass: "os-harness-entrypoints",
			Revision:    revision,
		},
		EvidenceRefs: []string{"os_harness_entrypoints:" + revision},
	}
	names := make([]string, 0, len(source.Harnesses))
	for name := range source.Harnesses {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, sourceID := range names {
		row := source.Harnesses[sourceID]
		entrypoint := strings.TrimSpace(row.Entrypoint)
		if entrypoint == "" {
			return RegistryManifest{}, fmt.Errorf("OS harness entrypoint %q needs entrypoint", sourceID)
		}
		manifest.Harnesses = append(manifest.Harnesses, ManifestHarness{
			Name:                 canonicalHarnessName(sourceID),
			InstructionMechanism: osEntrypointInstruction(row),
			Capabilities: ManifestCapabilities{
				SessionStartContext: true,
			},
			Limitations: osEntrypointLimitations(sourceID, source, row),
		})
	}
	if _, err := manifest.Registry(); err != nil {
		return RegistryManifest{}, err
	}
	return manifest, nil
}

func (m RegistryManifest) Registry() (Registry, error) {
	if strings.TrimSpace(m.SchemaVersion) != RegistryManifestSchemaVersion {
		return Registry{}, fmt.Errorf("harness registry schema_version = %q, want %q", m.SchemaVersion, RegistryManifestSchemaVersion)
	}
	if len(m.Harnesses) == 0 {
		return Registry{}, fmt.Errorf("harness registry needs at least one harness")
	}
	byName := map[string]Harness{}
	for _, in := range m.Harnesses {
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return Registry{}, fmt.Errorf("harness registry contains harness without name")
		}
		if _, exists := byName[name]; exists {
			return Registry{}, fmt.Errorf("harness registry duplicate harness %q", name)
		}
		if strings.TrimSpace(in.InstructionMechanism) == "" {
			return Registry{}, fmt.Errorf("harness %q needs instruction_mechanism", name)
		}
		byName[name] = Harness{
			Name:                 name,
			InstructionMechanism: strings.TrimSpace(in.InstructionMechanism),
			Capabilities: Capabilities{
				SessionStartContext: in.Capabilities.SessionStartContext,
				UserPromptContext:   in.Capabilities.UserPromptContext,
				PreToolUseBlock:     in.Capabilities.PreToolUseBlock,
				PostToolUseEvent:    in.Capabilities.PostToolUseEvent,
				StopFeedback:        in.Capabilities.StopFeedback,
				SessionEndReceipt:   in.Capabilities.SessionEndReceipt,
				NativeSkills:        in.Capabilities.NativeSkills,
				MCP:                 in.Capabilities.MCP,
			},
			Limitations:    append([]string(nil), in.Limitations...),
			InjectBudgetMS: firstPositive(in.InjectBudgetMS, defaultInjectBudgetMS(name)),
			Fallback:       in.Fallback,
		}
	}
	return Registry{Harnesses: byName}, nil
}

func RegistryRevision(reg Registry) string {
	type harnessSnapshot struct {
		Name                 string       `json:"name"`
		InstructionMechanism string       `json:"instruction_mechanism"`
		Capabilities         Capabilities `json:"capabilities"`
		Limitations          []string     `json:"limitations"`
		InjectBudgetMS       int          `json:"inject_budget_ms,omitempty"`
		Fallback             bool         `json:"fallback,omitempty"`
	}
	names := reg.Names()
	snapshot := make([]harnessSnapshot, 0, len(names))
	for _, name := range names {
		h, _ := reg.Get(name)
		limitations := append([]string(nil), h.Limitations...)
		sort.Strings(limitations)
		snapshot = append(snapshot, harnessSnapshot{
			Name:                 h.Name,
			InstructionMechanism: h.InstructionMechanism,
			Capabilities:         h.Capabilities,
			Limitations:          limitations,
			InjectBudgetMS:       h.InjectBudgetMS,
			Fallback:             h.Fallback,
		})
	}
	data, _ := json.Marshal(snapshot)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func canonicalHarnessName(name string) string {
	switch strings.TrimSpace(name) {
	case "claude":
		return "claude_code"
	default:
		return strings.TrimSpace(name)
	}
}

func osEntrypointInstruction(row OSEntrypointRow) string {
	parts := []string{"entrypoint:" + strings.TrimSpace(row.Entrypoint)}
	if strings.TrimSpace(row.Generator) != "" {
		parts = append(parts, "generator:"+strings.TrimSpace(row.Generator))
	}
	return strings.Join(parts, " ")
}

func osEntrypointLimitations(sourceID string, source OSEntrypointsFile, row OSEntrypointRow) []string {
	var out []string
	add := func(key, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, key+"="+value)
		}
	}
	add("source_id", sourceID)
	add("registry_status", source.Status)
	add("entrypoint_status", row.Status)
	add("must_include", source.MustInclude)
	add("check", row.Check)
	add("generator", row.Generator)
	add("last_reviewed", source.LastReviewed)
	add("review_after", source.ReviewAfter)
	add("registry_expires_at", source.ExpiresAt)
	add("entrypoint_expires_at", row.ExpiresAt)
	return out
}

// Generic is the fallback for an unknown harness: assume fail-open, enforce everything in
// the adapter, not the harness.
func Generic() Harness {
	return Harness{
		Name:                 "generic",
		InstructionMechanism: "static instructions file",
		Capabilities:         Capabilities{PostToolUseEvent: true, SessionEndReceipt: true},
		Limitations:          []string{"assume fail-open; enforce all gates in the adapter, not the harness"},
		InjectBudgetMS:       1000,
		Fallback:             true,
	}
}

func defaultInjectBudgetMS(name string) int {
	switch canonicalHarnessName(name) {
	case "claude_code", "codex":
		return 1500
	default:
		return 1000
	}
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

// InjectionBudgetMS returns the fail-open context injection budget for a harness.
func (r Registry) InjectionBudgetMS(name string) int {
	h, _ := r.Get(name)
	return firstPositive(h.InjectBudgetMS, defaultInjectBudgetMS(name))
}

// Get returns the harness by name, or the generic fallback with found=false.
func (r Registry) Get(name string) (Harness, bool) {
	if h, ok := r.Harnesses[name]; ok {
		return h, true
	}
	return Generic(), false
}

// Supports reports whether a harness can verifiably honor a hook (by enum name:
// SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, SessionEnd, Stop).
func (r Registry) Supports(name, hookName string) bool {
	h, _ := r.Get(name)
	switch hookName {
	case "SessionStart":
		return h.Capabilities.SessionStartContext
	case "UserPromptSubmit":
		return h.Capabilities.UserPromptContext
	case "PreToolUse":
		return h.Capabilities.PreToolUseBlock
	case "PostToolUse":
		return h.Capabilities.PostToolUseEvent
	case "Stop":
		return h.Capabilities.StopFeedback
	case "SessionEnd":
		return h.Capabilities.SessionEndReceipt
	}
	return false
}

// Names lists registered harnesses, sorted.
func (r Registry) Names() []string {
	out := make([]string, 0, len(r.Harnesses))
	for n := range r.Harnesses {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
