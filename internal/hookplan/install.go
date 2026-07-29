package hookplan

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"compound/internal/harness"
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
	Event    string
	Matcher  string
	Command  string
	Blocking bool
}

// HarnessMatcher deliberately covers every tool. Interactive postures need a
// complete action-evidence stream, while isolated guarded postures need the same
// coverage for deterministic authorization.
func HarnessMatcher(harness string) string {
	return "*"
}

// ConfigRel is the harness config path relative to the base directory. Claude
// Code uses a project-scoped settings overlay so Compound's lifecycle bindings
// can be scoped to the current repository.
func ConfigRel(harness, scope string) string {
	switch harness {
	case "codex":
		return filepath.Join(".codex", "hooks.json")
	case claudeZaiHarness:
		return filepath.Join(".config", "switchback", "claude", "zai-direct", "settings.json")
	default: // claude-code
		return filepath.Join(".claude", claudeSettingsFile(scope))
	}
}

func claudeSettingsFile(scope string) string {
	parts := []string{"settings"}
	if scope == "project" {
		parts = append(parts, "local")
	}
	return strings.Join(append(parts, "json"), ".")
}

// ClaudeZaiProfileDir is the default isolated Claude profile home under a user's base directory.
func ClaudeZaiProfileDir(baseDir string) string {
	return filepath.Join(baseDir, ".config", "switchback", "claude", "zai-direct")
}

// HookCommand is the exact observation-only command a harness runs for Compound PreToolUse.
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

// HookInstallBinding is one lifecycle event binding reported by an install/check pass.
type HookInstallBinding struct {
	Event    string `json:"event"`
	Matcher  string `json:"matcher,omitempty"`
	Command  string `json:"command"`
	Blocking bool   `json:"blocking"`
}

// HookInstallEntry is the per-harness outcome of an install/check pass.
type HookInstallEntry struct {
	Harness        string               `json:"harness"`
	ConfigPath     string               `json:"config_path"`
	ConfigRevision string               `json:"config_revision"` // sha256 of the exact desired config bytes
	Action         string               `json:"action"`          // installed | updated | ok | drift | absent
	Bindings       []HookInstallBinding `json:"bindings"`
	Matcher        string               `json:"matcher"` // blocking guard matcher; empty for observation-only posture
	Command        string               `json:"command"` // blocking guard command; empty for observation-only posture
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
	return InstallHooksWithConfigDirAndPublish(baseDir, binaryPath, scope, harnesses, configDir, check, nil)
}

// InstallHooksWithConfigDirAndPublish installs every requested native config as one transaction
// with its authority publisher. All configs are parsed and staged before the first write. If a
// native commit, byte verification, or publication fails, every config already written by this
// call is restored to its exact prior bytes and mode.
func InstallHooksWithConfigDirAndPublish(
	baseDir, binaryPath, scope string,
	harnesses []string,
	configDir string,
	check bool,
	publish func(HookInstallReport) error,
) (HookInstallReport, error) {
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
	prepared := make([]preparedHookConfig, 0, 3)
	for _, harness := range []string{"claude-code", claudeZaiHarness, "codex"} {
		if len(want) > 0 && !want[harness] {
			continue
		}
		configPath := filepath.Join(baseDir, ConfigRel(harness, scope))
		if harness == claudeZaiHarness && strings.TrimSpace(configDir) != "" {
			configPath = filepath.Join(configDir, "settings.json")
		}
		bindings, _, err := compiledManagedHookBindings(binaryPath, harness, scope)
		if err != nil {
			return report, fmt.Errorf("%s: %w", harness, err)
		}
		reportedBindings := make([]HookInstallBinding, 0, len(bindings))
		for _, binding := range bindings {
			reportedBindings = append(reportedBindings, HookInstallBinding{
				Event:    binding.Event,
				Matcher:  binding.Matcher,
				Command:  binding.Command,
				Blocking: binding.Blocking,
			})
		}
		matcher, command := installedGuardProjection(bindings)
		staged, err := prepareHookBindings(harness, configPath, bindings, check)
		if err != nil {
			return report, fmt.Errorf("%s: %w", harness, err)
		}
		prepared = append(prepared, staged)
		report.Entries = append(report.Entries, HookInstallEntry{
			Harness: harness, ConfigPath: configPath, ConfigRevision: hookConfigRevision(staged.after),
			Action: staged.action, Matcher: matcher, Command: command,
			Bindings: reportedBindings,
		})
	}
	if check {
		return report, nil
	}

	committed := make([]preparedHookConfig, 0, len(prepared))
	for _, staged := range prepared {
		if !staged.shouldWrite {
			continue
		}
		if err := writeHookConfigAtomically(staged.path, staged.after, staged.mode); err != nil {
			rollbackErr := rollbackHookConfigs(committed)
			return report, errors.Join(
				fmt.Errorf("%s: write hook config: %w", staged.harness, err),
				rollbackErr,
			)
		}
		committed = append(committed, staged)
	}
	if err := verifyHookConfigs(prepared); err != nil {
		return report, errors.Join(err, rollbackHookConfigs(committed))
	}
	if publish != nil {
		if err := publish(report); err != nil {
			return report, errors.Join(
				fmt.Errorf("publish installed-hook authority: %w", err),
				rollbackHookConfigs(committed),
			)
		}
	}
	return report, nil
}

type preparedHookConfig struct {
	harness     string
	path        string
	existed     bool
	before      []byte
	after       []byte
	mode        os.FileMode
	action      string
	shouldWrite bool
}

func compiledManagedHookBindings(
	binaryPath string,
	harnessName string,
	configScope string,
) ([]desiredHookBinding, CompiledPosture, error) {
	registry := harness.DefaultRegistry()
	if harnessName == claudeZaiHarness {
		configScope = "profile"
	}
	compiled, err := CompileDefaultPosture(DefaultPostureRequest{
		Harness:         harnessName,
		Registry:        registry,
		HandlerRef:      "compound",
		HandlerRevision: "binary:" + binaryPath,
		ConfigScope:     configScope,
	})
	if err != nil {
		return nil, CompiledPosture{}, err
	}
	if err := validateInstallableDefaultPosture(harnessName, compiled); err != nil {
		return nil, compiled, err
	}
	bindings := make([]desiredHookBinding, 0, len(compiled.HookPlan.Bindings))
	for _, binding := range compiled.HookPlan.Bindings {
		command := HookCommandForEvent(binaryPath, harnessName, binding.Event)
		if binding.Blocking {
			command += " --enforce"
		}
		bindings = append(bindings, desiredHookBinding{
			Event:    binding.Event,
			Matcher:  binding.Matcher,
			Command:  command,
			Blocking: binding.Blocking,
		})
	}
	return bindings, compiled, nil
}

func validateInstallableDefaultPosture(harnessName string, compiled CompiledPosture) error {
	if compiled.MutationReady {
		return nil
	}
	registryName := strings.ReplaceAll(strings.TrimSpace(harnessName), "-", "_")
	if (registryName == "codex" || registryName == "claude_code") &&
		compiled.CompiledFrom.PostureID == "hpost_default_"+registryName &&
		isInteractiveObservationalDefault(compiled.Intentions) {
		return nil
	}
	return fmt.Errorf("compiled posture is not mutation-ready")
}

func isInteractiveObservationalDefault(intentions []CompiledIntention) bool {
	if len(intentions) != 3 {
		return false
	}
	seen := map[HarnessIntention]bool{}
	for _, intention := range intentions {
		if intention.Intention == IntentionGuardMutation ||
			intention.Disposition != DispositionNative {
			return false
		}
		switch intention.Intention {
		case IntentionPrepareContext:
			if intention.EnforcementStrength != "context" {
				return false
			}
		case IntentionObserveAction, IntentionObserveOutcome:
			if intention.EnforcementStrength != "observe" {
				return false
			}
		default:
			return false
		}
		seen[intention.Intention] = true
	}
	return seen[IntentionPrepareContext] &&
		seen[IntentionObserveAction] &&
		seen[IntentionObserveOutcome]
}

func installedGuardProjection(bindings []desiredHookBinding) (string, string) {
	for _, binding := range bindings {
		if binding.Event == "PreToolUse" && binding.Blocking {
			return binding.Matcher, binding.Command
		}
	}
	return "", ""
}

// mergePreToolUseBinding merges compound's PreToolUse binding into an existing harness config without
// disturbing any other content. It identifies compound's own entry by command marker so re-installs
// update in place and a user's other PreToolUse hooks are never touched.
func mergePreToolUseBinding(configPath, matcher, command string, check bool) (string, error) {
	return mergeHookBindings(configPath, []desiredHookBinding{{Event: "PreToolUse", Matcher: matcher, Command: command}}, check)
}

// mergeHookBindings retains the narrow single-config seam used by package tests. Multi-harness
// installs use InstallHooksWithConfigDirAndPublish so every selected config is prepared first.
func mergeHookBindings(configPath string, bindings []desiredHookBinding, check bool) (string, error) {
	prepared, err := prepareHookBindings("", configPath, bindings, check)
	if err != nil {
		return "", err
	}
	if !prepared.shouldWrite {
		return prepared.action, nil
	}
	if err := writeHookConfigAtomically(prepared.path, prepared.after, prepared.mode); err != nil {
		return "", err
	}
	return prepared.action, nil
}

func prepareHookBindings(harnessName, configPath string, bindings []desiredHookBinding, check bool) (preparedHookConfig, error) {
	prepared := preparedHookConfig{
		harness: harnessName,
		path:    configPath,
		mode:    0o644,
	}
	for _, binding := range bindings {
		if strings.TrimSpace(binding.Event) == "" || strings.TrimSpace(binding.Command) == "" {
			return preparedHookConfig{}, fmt.Errorf("hook binding needs event and command")
		}
	}

	root := map[string]any{}
	if data, err := os.ReadFile(configPath); err == nil {
		prepared.existed = true
		prepared.before = append([]byte(nil), data...)
		info, statErr := os.Stat(configPath)
		if statErr != nil {
			return preparedHookConfig{}, statErr
		}
		prepared.mode = info.Mode().Perm()
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &root); err != nil {
				return preparedHookConfig{}, fmt.Errorf("parse %s: %w", configPath, err)
			}
			if root == nil {
				root = map[string]any{}
			}
		}
	} else if !os.IsNotExist(err) {
		return preparedHookConfig{}, err
	}

	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	action := "ok"
	desiredEvents := make(map[string]bool, len(bindings))
	for _, binding := range bindings {
		desiredEvents[binding.Event] = true
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse"} {
		if desiredEvents[event] {
			continue
		}
		groups, _ := hooks[event].([]any)
		kept := make([]any, 0, len(groups))
		removed := false
		for _, entry := range groups {
			entryMap, ok := entry.(map[string]any)
			if ok && bindingIsCompoundEvent(entryMap, event) {
				removed = true
				continue
			}
			kept = append(kept, entry)
		}
		if !removed {
			continue
		}
		if check {
			action = combineHookInstallAction(action, "drift", true)
		} else {
			action = combineHookInstallAction(action, "updated", false)
		}
		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}
	for _, binding := range bindings {
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
			} else {
				action = combineHookInstallAction(action, "updated", false)
			}
			groups[idx] = ourEntry
		default:
			if check {
				action = combineHookInstallAction(action, "absent", true)
			} else {
				action = combineHookInstallAction(action, "installed", false)
			}
			groups = append(groups, ourEntry)
		}
		hooks[binding.Event] = groups
	}
	prepared.action = action
	if action == "ok" {
		prepared.after = append([]byte(nil), prepared.before...)
		return prepared, nil
	}
	root["hooks"] = hooks
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return preparedHookConfig{}, err
	}
	prepared.after = append(data, '\n')
	prepared.shouldWrite = !check
	return prepared, nil
}

func hookConfigRevision(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum)
}

func verifyHookConfigs(configs []preparedHookConfig) error {
	for _, config := range configs {
		actual, err := os.ReadFile(config.path)
		if err != nil {
			return fmt.Errorf("verify installed %s hook config: %w", config.harness, err)
		}
		if !bytes.Equal(actual, config.after) {
			return fmt.Errorf(
				"verify installed %s hook config: revision mismatch: got %s want %s",
				config.harness,
				hookConfigRevision(actual),
				hookConfigRevision(config.after),
			)
		}
	}
	return nil
}

func rollbackHookConfigs(configs []preparedHookConfig) error {
	var rollbackErr error
	for i := len(configs) - 1; i >= 0; i-- {
		config := configs[i]
		current, err := os.ReadFile(config.path)
		if os.IsNotExist(err) && !config.existed {
			continue
		}
		if err != nil {
			rollbackErr = errors.Join(
				rollbackErr,
				fmt.Errorf(
					"rollback %s hook config %s refused; target changed after commit: %w",
					config.harness,
					config.path,
					err,
				),
			)
			continue
		}
		info, err := os.Stat(config.path)
		if err != nil {
			rollbackErr = errors.Join(
				rollbackErr,
				fmt.Errorf(
					"rollback %s hook config %s refused; target changed after commit: %w",
					config.harness,
					config.path,
					err,
				),
			)
			continue
		}
		if !bytes.Equal(current, config.after) || info.Mode().Perm() != config.mode.Perm() {
			rollbackErr = errors.Join(
				rollbackErr,
				fmt.Errorf(
					"rollback %s hook config %s refused; target changed after commit",
					config.harness,
					config.path,
				),
			)
			continue
		}
		if config.existed {
			if err := writeHookConfigAtomically(config.path, config.before, config.mode); err != nil {
				rollbackErr = errors.Join(
					rollbackErr,
					fmt.Errorf("rollback %s hook config %s: %w", config.harness, config.path, err),
				)
			}
			continue
		}
		if err := os.Remove(config.path); err != nil && !os.IsNotExist(err) {
			rollbackErr = errors.Join(
				rollbackErr,
				fmt.Errorf("rollback %s hook config %s: %w", config.harness, config.path, err),
			)
		}
	}
	return rollbackErr
}

func writeHookConfigAtomically(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".compound-hook-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(mode.Perm()); err != nil {
		return errors.Join(err, temp.Close())
	}
	if _, err := temp.Write(data); err != nil {
		return errors.Join(err, temp.Close())
	}
	if err := temp.Sync(); err != nil {
		return errors.Join(err, temp.Close())
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	keepTemp = false
	return nil
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

// bindingIsCompoundEvent reports whether an event entry is Compound's own.
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
