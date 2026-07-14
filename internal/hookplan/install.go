package hookplan

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// install.go is the missing WRITER half of the hook lifecycle. hookplan already models desired vs
// observed bindings and reconciles them; InstallHooks materializes the canonical compound PreToolUse
// binding into the harness-owned config the reconciler reads — same shape, same paths. It is
// dual-harness by construction (Claude Code AND Codex) so Codex gets parity, not a Claude-first
// afterthought, and it is NO-CLOBBER: it preserves every other key and every non-compound hook,
// replacing only the one PreToolUse entry whose command is compound's own.

// hookCommandMarker identifies compound's own PreToolUse binding so an update never clobbers a user's
// other hooks (the JSON analog of profiles' gen-marker).
const hookCommandMarker = "hook pre-tool-use --live"

// HarnessMatcher is the tool matcher for each harness — Claude has MultiEdit; Codex does not.
func HarnessMatcher(harness string) string {
	if harness == "codex" {
		return "Write|Edit"
	}
	return "Write|Edit|MultiEdit"
}

// ConfigRel is the harness config path (relative to the base dir) the reconciler reads. Claude Code
// has a project-scoped overlay (settings.local.json) so the compound-specific guard can be wired to
// fire only inside a compound repo rather than on every project's writes globally.
func ConfigRel(harness, scope string) string {
	switch harness {
	case "codex":
		return filepath.Join(".codex", "hooks.json")
	default: // claude-code
		if scope == "project" {
			return filepath.Join(".claude", "settings.local.json")
		}
		return filepath.Join(".claude", "settings.json")
	}
}

// HookCommand is the exact command a harness runs for the compound PreToolUse gate.
func HookCommand(binaryPath, harness string) string {
	return fmt.Sprintf("%s hook pre-tool-use --live --harness %s", binaryPath, harness)
}

// HookInstallEntry is the per-harness outcome of an install/check pass.
type HookInstallEntry struct {
	Harness    string `json:"harness"`
	ConfigPath string `json:"config_path"`
	Action     string `json:"action"` // installed | updated | ok | drift | absent
	Matcher    string `json:"matcher"`
	Command    string `json:"command"`
}

// HookInstallReport is the full install/check result.
type HookInstallReport struct {
	Check   bool               `json:"check"`
	Scope   string             `json:"scope"`
	Entries []HookInstallEntry `json:"entries"`
}

// InstallHooks writes (or, with check=true, only reports) the compound PreToolUse binding into each
// requested harness config under baseDir. binaryPath is the absolute path to the compound binary the
// hook invokes; harnesses filters which targets to touch (empty = both). scope selects the Claude
// config file (global settings.json vs project settings.local.json).
func InstallHooks(baseDir, binaryPath, scope string, harnesses []string, check bool) (HookInstallReport, error) {
	if strings.TrimSpace(baseDir) == "" {
		return HookInstallReport{}, fmt.Errorf("base dir required")
	}
	if strings.TrimSpace(binaryPath) == "" {
		return HookInstallReport{}, fmt.Errorf("binary path required")
	}
	report := HookInstallReport{Check: check, Scope: scope}
	want := map[string]bool{}
	for _, h := range harnesses {
		want[strings.TrimSpace(h)] = true
	}
	for _, harness := range []string{"claude-code", "codex"} {
		if len(want) > 0 && !want[harness] {
			continue
		}
		configPath := filepath.Join(baseDir, ConfigRel(harness, scope))
		matcher := HarnessMatcher(harness)
		command := HookCommand(binaryPath, harness)
		action, err := mergePreToolUseBinding(configPath, matcher, command, check)
		if err != nil {
			return report, fmt.Errorf("%s: %w", harness, err)
		}
		report.Entries = append(report.Entries, HookInstallEntry{
			Harness: harness, ConfigPath: configPath, Action: action, Matcher: matcher, Command: command,
		})
	}
	return report, nil
}

// mergePreToolUseBinding merges compound's PreToolUse binding into an existing harness config without
// disturbing any other content. It identifies compound's own entry by command marker so re-installs
// update in place and a user's other PreToolUse hooks are never touched.
func mergePreToolUseBinding(configPath, matcher, command string, check bool) (string, error) {
	root := map[string]any{}
	if data, err := os.ReadFile(configPath); err == nil {
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &root); err != nil {
				return "", fmt.Errorf("parse %s: %w", configPath, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}

	ourEntry := map[string]any{
		"matcher": matcher,
		"hooks":   []any{map[string]any{"type": "command", "command": command}},
	}

	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	pre, _ := hooks["PreToolUse"].([]any)

	idx := -1
	for i, e := range pre {
		if em, ok := e.(map[string]any); ok && bindingIsCompound(em) {
			idx = i
			break
		}
	}

	action := "installed"
	switch {
	case idx >= 0 && sameEntry(pre[idx], ourEntry):
		return "ok", nil
	case idx >= 0:
		if check {
			return "drift", nil
		}
		pre[idx] = ourEntry
		action = "updated"
	default:
		if check {
			return "absent", nil
		}
		pre = append(pre, ourEntry)
	}

	hooks["PreToolUse"] = pre
	root["hooks"] = hooks
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(configPath, append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	return action, nil
}

// bindingIsCompound reports whether a PreToolUse entry is compound's own (matched by command marker).
func bindingIsCompound(entry map[string]any) bool {
	inner, _ := entry["hooks"].([]any)
	for _, h := range inner {
		if hm, ok := h.(map[string]any); ok {
			if cmd, _ := hm["command"].(string); strings.Contains(cmd, hookCommandMarker) {
				return true
			}
		}
	}
	return false
}

func sameEntry(a any, b map[string]any) bool {
	am, ok := a.(map[string]any)
	if !ok {
		return false
	}
	ad, _ := json.Marshal(am)
	bd, _ := json.Marshal(b)
	return string(ad) == string(bd)
}
