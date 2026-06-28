// Package harness is the HarnessRegistry — the spine's record of what each agent
// harness ACTUALLY supports and where it is weak. Governance must be capability-aware,
// not fantasy-uniform: a harness that cannot block a tool call natively must be enforced
// via a wrapper/adapter, never trusted to self-gate. This is the public-core, Go-native
// generalization of a control-spine's per-harness capability views.
// See docs/architecture/runtime.md §3.
package harness

import "sort"

// Capabilities is what a harness can verifiably do across the hook lifecycle.
type Capabilities struct {
	SessionStartContext bool `json:"session_start_context"`
	UserPromptContext   bool `json:"user_prompt_context"`
	PreToolUseBlock     bool `json:"pre_tool_use_block"` // can it actually DENY a tool call?
	PostToolUseEvent    bool `json:"post_tool_use_event"`
	SessionEndReceipt   bool `json:"session_end_receipt"`
	NativeSkills        bool `json:"native_skills"`
	MCP                 bool `json:"mcp"`
}

// Harness is one registered executor.
type Harness struct {
	Name                 string       `json:"name"`
	InstructionMechanism string       `json:"instruction_mechanism"`
	Capabilities         Capabilities `json:"capabilities"`
	Limitations          []string     `json:"limitations"`
	Fallback             bool         `json:"fallback,omitempty"` // true when this is the generic stand-in
}

// Registry maps harness name -> capabilities.
type Registry struct{ Harnesses map[string]Harness }

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
				PostToolUseEvent: true, SessionEndReceipt: true, NativeSkills: true, MCP: true,
			},
			Limitations: []string{"long-session instruction drift", "instruction/context budget cap"},
		},
		"codex": {
			Name:                 "codex",
			InstructionMechanism: "AGENTS.md + skills (progressive disclosure)",
			Capabilities: Capabilities{
				SessionStartContext: true, UserPromptContext: true, PreToolUseBlock: false,
				PostToolUseEvent: true, SessionEndReceipt: true, NativeSkills: true, MCP: true,
			},
			Limitations: []string{
				"no verified native pre-tool-use block — enforce via wrapper/adapter",
				"initial skill list capped (progressive disclosure)",
				"cwd-dependent guidance",
			},
		},
	}}
}

// Generic is the fallback for an unknown harness: assume fail-open, enforce everything in
// the adapter, not the harness.
func Generic() Harness {
	return Harness{
		Name:                 "generic",
		InstructionMechanism: "static instructions file",
		Capabilities:         Capabilities{PostToolUseEvent: true, SessionEndReceipt: true},
		Limitations:          []string{"assume fail-open; enforce all gates in the adapter, not the harness"},
		Fallback:             true,
	}
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
	case "SessionEnd", "Stop":
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
