package hookplan

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// install.go is the WRITER half of the hook lifecycle. hookplan already models desired vs observed
// bindings and reconciles them; InstallHooks materializes canonical Compound bindings into the
// harness-owned configs the reconciler reads — same shape, same paths. It is NO-CLOBBER: it
// preserves every other key and every non-Compound hook, replacing only entries whose command is
// Compound's own for the same event.

// hookCommandMarker identifies compound's own PreToolUse binding so an update never clobbers a user's
// other hooks (the JSON analog of profiles' gen-marker).
const hookCommandMarker = "hook pre-tool-use --live"

const claudeZaiHarness = "claude-zai"

type desiredHookBinding struct {
	Event   string
	Matcher string
	Command string
}

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
	case claudeZaiHarness:
		return filepath.Join(".config", "switchback", "claude", "zai-direct", "settings.json")
	default: // claude-code
		if scope == "project" {
			return filepath.Join(".claude", "settings.local.json")
		}
		return filepath.Join(".claude", "settings.json")
	}
}

// ClaudeZaiProfileDir is the default isolated Claude profile home under a user's base directory.
func ClaudeZaiProfileDir(baseDir string) string {
	return filepath.Join(baseDir, ".config", "switchback", "claude", "zai-direct")
}

// HookCommand is the exact command a harness runs for the compound PreToolUse gate.
func HookCommand(binaryPath, harness string) string {
	return HookCommandForEvent(binaryPath, harness, "PreToolUse")
}

// HookCommandForEvent is the exact command a harness runs for one Compound live-hook event.
func HookCommandForEvent(binaryPath, harness, event string) string {
	verb := map[string]string{
		"SessionStart":     "session-start",
		"UserPromptSubmit": "user-prompt-submit",
		"PreToolUse":       "pre-tool-use",
		"PostToolUse":      "post-tool-use",
	}[event]
	return fmt.Sprintf("%s hook %s --live --harness %s", shellQuote(binaryPath), verb, harness)
}

// shellQuote wraps s in POSIX single quotes so a shell (or any shell-word splitter, including the
// harness that execs this command) treats it as exactly one token even when it contains spaces or
// other shell metacharacters, e.g. an app-bundle install path like `/tmp/Compound Apps/compound`.
// Without this, a spaced binaryPath silently truncates at the first space and the harness execs a
// nonexistent binary, which fails the mutation-policy guard open. An embedded single quote is
// escaped with the standard POSIX idiom: close the quote, insert a backslash-escaped quote
// character, then reopen the quote (see the ReplaceAll call below for the exact byte sequence).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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

// InstallHooks writes (or, with check=true, only reports) Compound bindings into each requested
// harness config under baseDir. An empty harness list means every managed harness.
func InstallHooks(baseDir, binaryPath, scope string, harnesses []string, check bool) (HookInstallReport, error) {
	return InstallHooksWithConfigDir(baseDir, binaryPath, scope, harnesses, "", check)
}

// InstallHooksWithConfigDir supports an explicit isolated profile directory for claude-zai. The
// override is intentionally narrow: mixing it with another harness would make one path flag carry
// two incompatible meanings.
func InstallHooksWithConfigDir(baseDir, binaryPath, scope string, harnesses []string, configDir string, check bool) (HookInstallReport, error) {
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
	if strings.TrimSpace(configDir) != "" && (len(want) != 1 || !want[claudeZaiHarness]) {
		return report, fmt.Errorf("config dir is supported only with harness %s", claudeZaiHarness)
	}
	for _, harness := range []string{"claude-code", claudeZaiHarness, "codex"} {
		if len(want) > 0 && !want[harness] {
			continue
		}
		configPath := filepath.Join(baseDir, ConfigRel(harness, scope))
		if harness == claudeZaiHarness && strings.TrimSpace(configDir) != "" {
			configPath = filepath.Join(configDir, "settings.json")
		}
		matcher := HarnessMatcher(harness)
		command := HookCommand(binaryPath, harness)
		bindings := []desiredHookBinding{{Event: "PreToolUse", Matcher: matcher, Command: command}}
		if harness == claudeZaiHarness {
			bindings = claudeZaiHookBindings(binaryPath)
		}
		action, err := mergeHookBindings(configPath, bindings, check)
		if err != nil {
			return report, fmt.Errorf("%s: %w", harness, err)
		}
		report.Entries = append(report.Entries, HookInstallEntry{
			Harness: harness, ConfigPath: configPath, Action: action, Matcher: matcher, Command: command,
		})
	}
	return report, nil
}

func claudeZaiHookBindings(binaryPath string) []desiredHookBinding {
	return []desiredHookBinding{
		{Event: "SessionStart", Command: HookCommandForEvent(binaryPath, claudeZaiHarness, "SessionStart")},
		{Event: "UserPromptSubmit", Command: HookCommandForEvent(binaryPath, claudeZaiHarness, "UserPromptSubmit")},
		{Event: "PreToolUse", Matcher: HarnessMatcher(claudeZaiHarness), Command: HookCommandForEvent(binaryPath, claudeZaiHarness, "PreToolUse")},
		{Event: "PostToolUse", Matcher: HarnessMatcher(claudeZaiHarness), Command: HookCommandForEvent(binaryPath, claudeZaiHarness, "PostToolUse")},
	}
}

// mergePreToolUseBinding merges compound's PreToolUse binding into an existing harness config without
// disturbing any other content. It identifies compound's own entry by command marker so re-installs
// update in place and a user's other PreToolUse hooks are never touched.
func mergePreToolUseBinding(configPath, matcher, command string, check bool) (string, error) {
	return mergeHookBindings(configPath, []desiredHookBinding{{Event: "PreToolUse", Matcher: matcher, Command: command}}, check)
}

// mergeHookBindings performs one read and at most one write for a complete desired roster. This
// keeps the isolated profile from observing a partially-installed four-event set.
func mergeHookBindings(configPath string, bindings []desiredHookBinding, check bool) (string, error) {
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

	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	action := "ok"
	for _, binding := range bindings {
		if strings.TrimSpace(binding.Event) == "" || strings.TrimSpace(binding.Command) == "" {
			return "", fmt.Errorf("hook binding needs event and command")
		}
		ourEntry := map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": binding.Command}},
		}
		if strings.TrimSpace(binding.Matcher) != "" {
			ourEntry["matcher"] = binding.Matcher
		}
		groups, _ := hooks[binding.Event].([]any)
		idx := -1
		for i, entry := range groups {
			if em, ok := entry.(map[string]any); ok && bindingIsCompoundEvent(em, binding.Event) {
				idx = i
				break
			}
		}
		switch {
		case idx >= 0 && sameEntry(groups[idx], ourEntry):
		case idx >= 0:
			if check {
				action = combineHookInstallAction(action, "drift", true)
				continue
			}
			groups[idx] = ourEntry
			action = combineHookInstallAction(action, "updated", false)
		default:
			if check {
				action = combineHookInstallAction(action, "absent", true)
				continue
			}
			groups = append(groups, ourEntry)
			action = combineHookInstallAction(action, "installed", false)
		}
		hooks[binding.Event] = groups
	}
	if check || action == "ok" {
		return action, nil
	}
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

func combineHookInstallAction(current, next string, check bool) string {
	if check {
		if current == "drift" || next == "drift" {
			return "drift"
		}
		if current == "absent" || next == "absent" {
			return "absent"
		}
		return "ok"
	}
	if current == "updated" || next == "updated" {
		return "updated"
	}
	if current == "installed" || next == "installed" {
		return "installed"
	}
	return "ok"
}

// bindingIsCompound reports whether a PreToolUse entry is compound's own (matched by command marker).
func bindingIsCompound(entry map[string]any) bool {
	return bindingIsCompoundEvent(entry, "PreToolUse")
}

func bindingIsCompoundEvent(entry map[string]any, event string) bool {
	verb := map[string]string{
		"SessionStart":     "session-start",
		"UserPromptSubmit": "user-prompt-submit",
		"PreToolUse":       "pre-tool-use",
		"PostToolUse":      "post-tool-use",
	}[event]
	marker := "hook " + verb + " --live"
	inner, _ := entry["hooks"].([]any)
	for _, h := range inner {
		if hm, ok := h.(map[string]any); ok {
			if cmd, _ := hm["command"].(string); strings.Contains(cmd, marker) {
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
