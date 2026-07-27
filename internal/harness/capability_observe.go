package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// The registry DECLARES what a harness can do. For a lane-backed harness that
// declaration was never checked against the lane, and it was wrong in the most
// expensive direction: over-claiming. A lane profile whose settings file carried
// no `hooks` key at all, launched by a wrapper passing `--no-mcp --no-skills`,
// was registered with every hook event true plus skills and MCP.
//
// Routing then rested on that fiction. Work was dispatched to an executor
// believed to be hooked, oriented and MCP-connected; it had none of those, so a
// whole wave of delegated audits had no way to record a finding and printed to
// stdout instead — where the reports were later reported as nonexistent.
//
// Over-claiming and under-claiming are NOT symmetric. Under-claiming wastes a
// capability. Over-claiming routes work to an executor that cannot do the job
// and cannot say so, and the failure surfaces as silence. Both are reported;
// only over-claim is described as unsafe.

// hookEventCapability maps a Claude-Code settings hook event name onto the
// registry field it justifies. A declared hook capability is only real if the
// harness's settings actually register that event.
var hookEventCapability = map[string]string{
	"SessionStart":     "session_start_context",
	"UserPromptSubmit": "user_prompt_context",
	"PreToolUse":       "pre_tool_use_block",
	"PostToolUse":      "post_tool_use_event",
	"Stop":             "stop_feedback",
	"SessionEnd":       "session_end_receipt",
}

// ObservedCapabilities is what the lane's own files prove, plus how it was
// determined. Evidence is carried so a drift report can cite rather than assert.
type ObservedCapabilities struct {
	Capabilities Capabilities `json:"capabilities"`
	Evidence     []string     `json:"evidence,omitempty"`
}

// ObserveLaneCapabilities derives capabilities from the two files the lane owner
// publishes for a harness: its settings file and its launch wrapper.
//
// A missing settings file is NOT treated as "no hooks" — that would silently
// convert an unreadable path into a confident negative, and an unreadable path
// is exactly the bug we are hunting (the lane doctor reports a settings path
// nothing writes to). It is an error, so the caller reports "unknown" instead of
// manufacturing a fact.
func ObserveLaneCapabilities(settingsPath, wrapperPath string) (ObservedCapabilities, error) {
	var out ObservedCapabilities
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return out, fmt.Errorf("observe capabilities: read harness settings: %w", err)
	}
	var settings struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return out, fmt.Errorf("observe capabilities: parse harness settings: %w", err)
	}
	if len(settings.Hooks) == 0 {
		out.Evidence = append(out.Evidence, "settings declares no hooks key: no hook capability is observable")
	}
	registered := make([]string, 0, len(settings.Hooks))
	for event := range settings.Hooks {
		if _, known := hookEventCapability[event]; known {
			registered = append(registered, event)
		}
	}
	sort.Strings(registered)
	for _, event := range registered {
		switch event {
		case "SessionStart":
			out.Capabilities.SessionStartContext = true
		case "UserPromptSubmit":
			out.Capabilities.UserPromptContext = true
		case "PreToolUse":
			out.Capabilities.PreToolUseBlock = true
		case "PostToolUse":
			out.Capabilities.PostToolUseEvent = true
		case "Stop":
			out.Capabilities.StopFeedback = true
		case "SessionEnd":
			out.Capabilities.SessionEndReceipt = true
		}
	}
	if len(registered) > 0 {
		out.Evidence = append(out.Evidence, "settings registers hooks: "+strings.Join(registered, ","))
	}

	// Skills and MCP are decided by the launcher, not the settings file: the
	// wrapper's flags win regardless of what settings would allow.
	wrapper, err := os.ReadFile(wrapperPath)
	if err != nil {
		return out, fmt.Errorf("observe capabilities: read launch wrapper: %w", err)
	}
	text := string(wrapper)
	noSkills := strings.Contains(text, "--no-skills")
	noMCP := strings.Contains(text, "--no-mcp")
	out.Capabilities.NativeSkills = !noSkills
	out.Capabilities.MCP = !noMCP
	if noSkills {
		out.Evidence = append(out.Evidence, "launch wrapper passes --no-skills")
	}
	if noMCP {
		out.Evidence = append(out.Evidence, "launch wrapper passes --no-mcp")
	}
	return out, nil
}

// CapabilityMismatch is one declared capability the observation contradicts.
type CapabilityMismatch struct {
	Capability string `json:"capability"`
	Declared   bool   `json:"declared"`
	Observed   bool   `json:"observed"`
	// Overclaimed marks the unsafe direction: the registry promises something
	// the executor cannot do, so work routes to it and fails silently.
	Overclaimed bool `json:"overclaimed"`
}

// CapabilityDriftReport mirrors InstructionPackDriftReport's shape so the
// observation loop can switch on Drifted without caring which seam produced it.
type CapabilityDriftReport struct {
	Harness    string               `json:"harness"`
	Drifted    bool                 `json:"drifted"`
	Overclaims int                  `json:"overclaims"`
	Mismatches []CapabilityMismatch `json:"mismatches,omitempty"`
	Evidence   []string             `json:"evidence,omitempty"`
}

// CapabilityDrift compares a declared registry entry against an observation.
func CapabilityDrift(name string, declared Capabilities, observed ObservedCapabilities) CapabilityDriftReport {
	report := CapabilityDriftReport{Harness: name, Evidence: observed.Evidence}
	for _, f := range []struct {
		name     string
		declared bool
		observed bool
	}{
		{"session_start_context", declared.SessionStartContext, observed.Capabilities.SessionStartContext},
		{"user_prompt_context", declared.UserPromptContext, observed.Capabilities.UserPromptContext},
		{"pre_tool_use_block", declared.PreToolUseBlock, observed.Capabilities.PreToolUseBlock},
		{"post_tool_use_event", declared.PostToolUseEvent, observed.Capabilities.PostToolUseEvent},
		{"stop_feedback", declared.StopFeedback, observed.Capabilities.StopFeedback},
		{"session_end_receipt", declared.SessionEndReceipt, observed.Capabilities.SessionEndReceipt},
		{"native_skills", declared.NativeSkills, observed.Capabilities.NativeSkills},
		{"mcp", declared.MCP, observed.Capabilities.MCP},
	} {
		if f.declared == f.observed {
			continue
		}
		over := f.declared && !f.observed
		if over {
			report.Overclaims++
		}
		report.Mismatches = append(report.Mismatches, CapabilityMismatch{
			Capability: f.name, Declared: f.declared, Observed: f.observed, Overclaimed: over,
		})
	}
	report.Drifted = len(report.Mismatches) > 0
	return report
}
