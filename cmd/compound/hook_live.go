package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/hook"
	"compound/internal/policy"
)

const hookUsage = "usage: compound hook <session-start|user-prompt-submit|pre-tool-use|post-tool-use|session-end|stop> [--enforce] <fixture.json> OR compound hook <session-start|user-prompt-submit|pre-tool-use|post-tool-use|session-end|stop> --live --harness <claude-code>"

type hookCommandArgs struct {
	HookName    string
	FixturePath string
	Enforce     bool
	Live        bool
	Harness     string
}

type claudeLiveHookInput struct {
	HookEventName  string         `json:"hook_event_name"`
	SessionID      string         `json:"session_id"`
	Cwd            string         `json:"cwd"`
	Prompt         string         `json:"prompt"`
	ToolName       string         `json:"tool_name"`
	ToolInput      map[string]any `json:"tool_input"`
	Matcher        string         `json:"matcher"`
	WorkID         string         `json:"work_id"`
	CompoundWorkID string         `json:"compound_work_id"`
}

type claudeHookSpecificOutput struct {
	HookEventName            string `json:"hookEventName"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
}

type claudeLiveHookResponse struct {
	HookSpecificOutput claudeHookSpecificOutput `json:"hookSpecificOutput"`
}

func loadHookFixture(parsed hookCommandArgs, now time.Time) (hook.Fixture, error) {
	if parsed.Live {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return hook.Fixture{}, err
		}
		return liveHookFixture(parsed, data, now)
	}
	data, err := os.ReadFile(parsed.FixturePath)
	if err != nil {
		return hook.Fixture{}, err
	}
	var fx hook.Fixture
	if err := json.Unmarshal(data, &fx); err != nil {
		return hook.Fixture{}, fmt.Errorf("parse hook fixture: %w", err)
	}
	fx.Hook = parsed.HookName // the CLI verb is authoritative
	return fx, nil
}

func liveHookFixture(parsed hookCommandArgs, data []byte, now time.Time) (hook.Fixture, error) {
	var in claudeLiveHookInput
	if err := json.Unmarshal(data, &in); err != nil {
		return hook.Fixture{}, fmt.Errorf("parse live hook stdin: %w", err)
	}
	fx := hook.Fixture{
		Hook:    parsed.HookName,
		Harness: normalizeLiveHarness(parsed.Harness),
		Cwd:     in.Cwd,
		Prompt:  in.Prompt,
		Task:    liveTask(in),
		Tool:    in.ToolName,
	}
	if fx.Cwd == "" {
		fx.Cwd = os.Getenv("CLAUDE_PROJECT_DIR")
	}
	switch parsed.HookName {
	case "pre-tool-use":
		ai, err := liveActionIntent(fx.Harness, in, now)
		if err != nil {
			return hook.Fixture{}, err
		}
		fx.Action = &ai
	case "post-tool-use":
		if fx.Tool == "" {
			fx.Tool = "unknown"
		}
	case "session-end", "stop":
		fx.WorkID = firstNonEmpty(in.WorkID, in.CompoundWorkID)
		if fx.WorkID == "" {
			return hook.Fixture{}, fmt.Errorf("%s live stdin needs work_id or compound_work_id", parsed.HookName)
		}
		fx.Summary = "closed from live hook event " + firstNonEmpty(in.SessionID, "unknown-session")
	}
	return fx, nil
}

func liveTask(in claudeLiveHookInput) string {
	if strings.TrimSpace(in.Prompt) != "" {
		return strings.TrimSpace(in.Prompt)
	}
	if strings.TrimSpace(in.HookEventName) != "" {
		return strings.TrimSpace(in.HookEventName)
	}
	return strings.TrimSpace(in.SessionID)
}

func liveActionIntent(harnessName string, in claudeLiveHookInput, now time.Time) (envelope.ActionIntent, error) {
	toolName := strings.TrimSpace(in.ToolName)
	targetPath := liveToolPath(in.ToolInput)
	if toolName == "" {
		return envelope.ActionIntent{}, fmt.Errorf("pre-tool-use live stdin needs tool_name")
	}
	if targetPath == "" {
		return envelope.ActionIntent{}, fmt.Errorf("pre-tool-use live stdin needs tool_input.file_path or tool_input.path")
	}
	action := "use_tool"
	capability := "tool." + strings.ToLower(toolName)
	switch toolName {
	case "Write":
		action = "write_file"
		capability = "filesystem.write"
	case "Edit":
		action = "edit_file"
		capability = "filesystem.write"
	case "MultiEdit":
		action = "multi_edit_file"
		capability = "filesystem.write"
	}
	workID := firstNonEmpty(in.WorkID, in.CompoundWorkID)
	if workID == "" {
		workID = "work_live_" + shortHash(in.SessionID, in.Cwd)
	}
	id := "act_live_" + shortHash(toolName, targetPath, in.SessionID, now.UTC().Format(time.RFC3339Nano))
	return envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            id,
			CreatedAt:     now.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "hook", Name: "compound"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime"},
			Privacy:       "private",
			Risk:          "tier2",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         normalizeLiveHarness(harnessName),
		Capability:    capability,
		Action:        action,
		Target:        envelope.ActionTarget{Kind: "file", Path: targetPath},
		MutationClass: "local_reversible",
		Reversibility: "easy",
		BlastRadius:   "repo",
	}, nil
}

func liveToolPath(input map[string]any) string {
	for _, key := range []string{"file_path", "path"} {
		if raw, ok := input[key]; ok {
			if s := strings.TrimSpace(fmt.Sprint(raw)); s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func liveHookResponse(hookName string, res hook.Result) claudeLiveHookResponse {
	out := claudeHookSpecificOutput{HookEventName: hookEnumName(hookName)}
	switch hookName {
	case "session-start", "user-prompt-submit":
		out.AdditionalContext = res.Summary
	case "pre-tool-use":
		if decision, reason := livePermissionDecision(res); decision != "" {
			out.PermissionDecision = decision
			out.PermissionDecisionReason = reason
		}
	}
	return claudeLiveHookResponse{HookSpecificOutput: out}
}

func livePermissionDecision(res hook.Result) (string, string) {
	for _, produced := range res.Produced {
		pd, ok := produced.(*envelope.PolicyDecision)
		if !ok || policy.Allowed(*pd) {
			continue
		}
		reason := strings.TrimSpace(strings.Join(pd.Reasons, "; "))
		if reason == "" {
			reason = res.Summary
		}
		if pd.Outcome == "hold" {
			return "ask", reason
		}
		return "deny", reason
	}
	return "", ""
}

func hookEnumName(hookName string) string {
	if v, ok := map[string]string{
		"session-start":      "SessionStart",
		"user-prompt-submit": "UserPromptSubmit",
		"pre-tool-use":       "PreToolUse",
		"post-tool-use":      "PostToolUse",
		"session-end":        "SessionEnd",
		"stop":               "Stop",
	}[hookName]; ok {
		return v
	}
	return hookName
}

func normalizeLiveHarness(harnessName string) string {
	return strings.ReplaceAll(strings.TrimSpace(harnessName), "-", "_")
}

func shortHash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}
