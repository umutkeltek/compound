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

// claudeMinimaxHarness is the sibling isolated-profile harness to claudeZaiHarness: same NO-CLOBBER
// installer, same profile-scoped config, different provider lane.
const claudeMinimaxHarness = "claude-minimax"

// claudeQwenHarness is the third enrolled isolated-profile harness: same NO-CLOBBER installer and
// profile-scoped config as claudeZaiHarness and claudeMinimaxHarness, backing the qwen token-plan
// lane. Enrollment raises the claude_qwen registry floor to the four genuinely installed bindings.
const claudeQwenHarness = "claude-qwen"

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
	case claudeMinimaxHarness:
		return filepath.Join(".config", "switchback", "claude", "_providers", "minimax-lane", "settings.json")
	case claudeQwenHarness:
		return filepath.Join(".config", "switchback", "claude", "_providers", "qwen", "settings.json")
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

// ClaudeMinimaxProfileDir is the default isolated Claude profile home for the minimax lane, under
// a user's base directory.
func ClaudeMinimaxProfileDir(baseDir string) string {
	return filepath.Join(baseDir, ".config", "switchback", "claude", "_providers", "minimax-lane")
}

// ClaudeQwenProfileDir is the default isolated Claude profile home for the qwen lane, under a
// user's base directory. It matches the profile_default recorded for the qwen lane in
// adapters/_lib/lanes.json.
func ClaudeQwenProfileDir(baseDir string) string {
	return filepath.Join(baseDir, ".config", "switchback", "claude", "_providers", "qwen")
}

// HookCommand is the exact observation-only command a harness runs for Compound PreToolUse.
func HookCommand(binaryPath, harness string) string {
	return HookCommandForEvent(binaryPath, harness, "PreToolUse")
}

// HookCommandForEvent is the exact command a harness runs for one Compound live-hook event.
func HookCommandForEvent(binaryPath, harness, event string) string {
	if event == "SessionEnd" {
		return fmt.Sprintf("%s hook session-end-adapter --live --harness %s", shellQuote(binaryPath), runtimeHarnessName(harness))
	}
	verb := map[string]string{
		"SessionStart":     "session-start",
		"UserPromptSubmit": "user-prompt-submit",
		"PreToolUse":       "pre-tool-use",
		"PostToolUse":      "post-tool-use",
	}[event]
	return fmt.Sprintf("%s hook %s --live --harness %s", shellQuote(binaryPath), verb, runtimeHarnessName(harness))
}

// runtimeHarnessName translates the operator-facing installer/profile label to
// the HarnessRegistry identity consumed by live hooks and ContextPack compilation.
// Keeping the translation at the emitted-command boundary preserves readable
// launch labels without letting a dashed label fall through to Generic().
func runtimeHarnessName(installerHarness string) string {
	switch strings.TrimSpace(installerHarness) {
	case "claude-code":
		return "claude_code"
	case claudeZaiHarness:
		return "claude_zai"
	case claudeMinimaxHarness:
		return "claude_minimax"
	case claudeQwenHarness:
		return "claude_qwen"
	default:
		return strings.TrimSpace(installerHarness)
	}
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

// InstallHooksWithConfigDir supports an explicit isolated profile directory for a single
// isolated-profile harness (claude-zai, claude-minimax, or claude-qwen). The override is
// intentionally narrow: mixing it with another harness would make one path flag carry two
// incompatible meanings.
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
	if strings.TrimSpace(configDir) != "" {
		if len(want) != 1 || (!want["claude-code"] && !want[claudeZaiHarness] && !want[claudeMinimaxHarness] && !want[claudeQwenHarness]) {
			return report, fmt.Errorf(
				"config dir is supported only with a single Claude profile harness (claude-code, %s, %s, or %s)",
				claudeZaiHarness, claudeMinimaxHarness, claudeQwenHarness,
			)
		}
	}
	prepared := make([]preparedHookConfig, 0, 5)
	for _, harness := range []string{"claude-code", claudeZaiHarness, claudeMinimaxHarness, claudeQwenHarness, "codex"} {
		if len(want) > 0 && !want[harness] {
			continue
		}
		configPath := filepath.Join(baseDir, ConfigRel(harness, scope))
		if strings.TrimSpace(configDir) != "" && (harness == "claude-code" || harness == claudeZaiHarness || harness == claudeMinimaxHarness || harness == claudeQwenHarness) {
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
	if err := commitHookInstallTransaction(prepared, report, publish, nil); err != nil {
		return report, err
	}
	return report, nil
}

func commitHookInstallTransaction(
	prepared []preparedHookConfig,
	report HookInstallReport,
	publish func(HookInstallReport) error,
	beforeCommit func(preparedHookConfig) error,
) error {
	committed := make([]preparedHookConfig, 0, len(prepared))
	for _, staged := range prepared {
		if !staged.shouldWrite {
			continue
		}
		if beforeCommit != nil {
			if err := beforeCommit(staged); err != nil {
				return errors.Join(
					fmt.Errorf("%s: before hook config commit: %w", staged.harness, err),
					rollbackHookConfigs(committed),
				)
			}
		}
		if err := commitPreparedHookConfig(staged); err != nil {
			rollbackErr := rollbackHookConfigs(committed)
			return errors.Join(
				fmt.Errorf("%s: write hook config: %w", staged.harness, err),
				rollbackErr,
			)
		}
		committed = append(committed, staged)
	}
	if err := verifyHookConfigs(prepared); err != nil {
		return errors.Join(err, rollbackHookConfigs(committed))
	}
	if publish != nil {
		if err := publish(report); err != nil {
			return errors.Join(
				fmt.Errorf("publish installed-hook authority: %w", err),
				rollbackHookConfigs(committed),
			)
		}
	}
	return nil
}

type preparedHookConfig struct {
	harness     string
	path        string
	existed     bool
	before      []byte
	after       []byte
	mode        os.FileMode
	preimage    os.FileInfo
	action      string
	shouldWrite bool
}

func compiledManagedHookBindings(
	binaryPath string,
	harnessName string,
	configScope string,
) ([]desiredHookBinding, CompiledPosture, error) {
	registry := harness.DefaultRegistry()
	if harnessName == claudeZaiHarness || harnessName == claudeMinimaxHarness || harnessName == claudeQwenHarness {
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
	if len(intentions) < 3 || len(intentions) > 4 {
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
		case IntentionRecordReceipt:
			if intention.EnforcementStrength != "receipt" {
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
	if err := commitPreparedHookConfig(prepared); err != nil {
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
	info, err := os.Lstat(configPath)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			return preparedHookConfig{}, fmt.Errorf("refuse symlinked hook config %s", configPath)
		}
		if !info.Mode().IsRegular() {
			return preparedHookConfig{}, fmt.Errorf("hook config %s must be a regular file", configPath)
		}
		data, readErr := os.ReadFile(configPath)
		if readErr != nil {
			return preparedHookConfig{}, readErr
		}
		afterReadInfo, statErr := os.Lstat(configPath)
		if statErr != nil {
			return preparedHookConfig{}, fmt.Errorf("hook config %s changed during preflight: %w", configPath, statErr)
		}
		if afterReadInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, afterReadInfo) {
			return preparedHookConfig{}, fmt.Errorf("hook config %s changed during preflight", configPath)
		}
		prepared.existed = true
		prepared.before = append([]byte(nil), data...)
		prepared.mode = afterReadInfo.Mode()
		prepared.preimage = afterReadInfo
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &root); err != nil {
				return preparedHookConfig{}, fmt.Errorf("parse %s: %w", configPath, err)
			}
			if root == nil {
				root = map[string]any{}
			}
		}
	case os.IsNotExist(err):
	case err != nil:
		return preparedHookConfig{}, err
	}

	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	action := "ok"
	desiredByEvent := make(map[string]desiredHookBinding, len(bindings))
	for _, binding := range bindings {
		desiredByEvent[binding.Event] = binding
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "SessionEnd"} {
		groups, _ := hooks[event].([]any)
		binding, desired := desiredByEvent[event]
		var desiredBinding *desiredHookBinding
		if desired {
			desiredBinding = &binding
		}
		reconciled, removed := reconcileHookEventGroups(groups, event, desiredBinding)
		if sameHookGroups(groups, reconciled) {
			continue
		}
		if check {
			next := "drift"
			if desired && removed == 0 {
				next = "absent"
			}
			action = combineHookInstallAction(action, next, true)
		} else {
			next := "updated"
			if desired && removed == 0 {
				next = "installed"
			}
			action = combineHookInstallAction(action, next, false)
		}
		if len(reconciled) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = reconciled
		}
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
		info, err := os.Lstat(config.path)
		if err != nil {
			return fmt.Errorf("verify installed %s hook config: %w", config.harness, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("verify installed %s hook config: target is not a regular file", config.harness)
		}
		actual, err := os.ReadFile(config.path)
		if err != nil {
			return fmt.Errorf("verify installed %s hook config: %w", config.harness, err)
		}
		afterReadInfo, err := os.Lstat(config.path)
		if err != nil {
			return fmt.Errorf("verify installed %s hook config: %w", config.harness, err)
		}
		if !afterReadInfo.Mode().IsRegular() || !os.SameFile(info, afterReadInfo) {
			return fmt.Errorf("verify installed %s hook config: target changed while reading", config.harness)
		}
		if !bytes.Equal(actual, config.after) {
			return fmt.Errorf(
				"verify installed %s hook config: revision mismatch: got %s want %s",
				config.harness,
				hookConfigRevision(actual),
				hookConfigRevision(config.after),
			)
		}
		if afterReadInfo.Mode() != config.mode {
			return fmt.Errorf(
				"verify installed %s hook config: mode mismatch: got %v want %v",
				config.harness,
				afterReadInfo.Mode(),
				config.mode,
			)
		}
	}
	return nil
}

func rollbackHookConfigs(configs []preparedHookConfig) error {
	var rollbackErr error
	for i := len(configs) - 1; i >= 0; i-- {
		config := configs[i]
		info, err := os.Lstat(config.path)
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
		if !info.Mode().IsRegular() {
			rollbackErr = errors.Join(
				rollbackErr,
				fmt.Errorf(
					"rollback %s hook config %s refused; target changed after commit: not a regular file",
					config.harness,
					config.path,
				),
			)
			continue
		}
		current, err := os.ReadFile(config.path)
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
		afterReadInfo, err := os.Lstat(config.path)
		if err != nil ||
			!afterReadInfo.Mode().IsRegular() ||
			!os.SameFile(info, afterReadInfo) ||
			!bytes.Equal(current, config.after) ||
			afterReadInfo.Mode() != config.mode {
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

func commitPreparedHookConfig(config preparedHookConfig) error {
	if err := verifyHookConfigPreimage(config); err != nil {
		return err
	}
	return writeHookConfigAtomically(config.path, config.after, config.mode)
}

func verifyHookConfigPreimage(config preparedHookConfig) error {
	info, err := os.Lstat(config.path)
	if !config.existed {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("hook config %s changed since preflight: %w", config.path, err)
		}
		return fmt.Errorf("hook config %s changed since preflight: target appeared", config.path)
	}
	if err != nil {
		return fmt.Errorf("hook config %s changed since preflight: %w", config.path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("hook config %s changed since preflight: target became a symlink", config.path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("hook config %s changed since preflight: target is no longer a regular file", config.path)
	}
	if config.preimage == nil || !os.SameFile(config.preimage, info) {
		return fmt.Errorf("hook config %s changed since preflight: file identity changed", config.path)
	}
	current, err := os.ReadFile(config.path)
	if err != nil {
		return fmt.Errorf("hook config %s changed since preflight: %w", config.path, err)
	}
	afterReadInfo, err := os.Lstat(config.path)
	if err != nil {
		return fmt.Errorf("hook config %s changed since preflight: %w", config.path, err)
	}
	if afterReadInfo.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(info, afterReadInfo) ||
		!os.SameFile(config.preimage, afterReadInfo) {
		return fmt.Errorf("hook config %s changed since preflight: file identity changed", config.path)
	}
	if !bytes.Equal(current, config.before) {
		return fmt.Errorf("hook config %s changed since preflight: bytes changed", config.path)
	}
	if afterReadInfo.Mode() != config.mode {
		return fmt.Errorf("hook config %s changed since preflight: mode changed", config.path)
	}
	return nil
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
	if err := temp.Chmod(mode); err != nil {
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

func hookIsCompoundEvent(hook any, event string) bool {
	hookMap, ok := hook.(map[string]any)
	if !ok {
		return false
	}
	command, _ := hookMap["command"].(string)
	if event == "SessionEnd" {
		trimmed := strings.TrimSpace(command)
		return trimmed == `"$HOME/.claude/hooks/SessionReceipt.hook.ts"` ||
			isCompoundSessionEndCommand(trimmed)
	}
	verb := map[string]string{
		"SessionStart":     "session-start",
		"UserPromptSubmit": "user-prompt-submit",
		"PreToolUse":       "pre-tool-use",
		"PostToolUse":      "post-tool-use",
	}[event]
	marker := "hook " + verb + " --live"
	return strings.Contains(command, marker)
}

func isCompoundSessionEndCommand(command string) bool {
	trimmed := strings.TrimSpace(command)
	for _, verb := range []string{"session-end-adapter", "session-end"} {
		marker := " hook " + verb + " --live --harness "
		markerAt := strings.LastIndex(trimmed, marker)
		if markerAt < 0 {
			continue
		}
		executable := strings.TrimSpace(trimmed[:markerAt])
		harnessName := strings.TrimSpace(trimmed[markerAt+len(marker):])
		if !isClaudeFamilyHarnessName(harnessName) || !isCompoundExecutableToken(executable) {
			continue
		}
		return true
	}
	return false
}

func isClaudeFamilyHarnessName(name string) bool {
	switch strings.TrimSpace(name) {
	case "claude-code", "claude_code",
		"claude-zai", "claude_zai",
		"claude-minimax", "claude_minimax",
		"claude-qwen", "claude_qwen":
		return true
	default:
		return false
	}
}

func isCompoundExecutableToken(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	if strings.HasPrefix(token, "'") {
		if !strings.HasSuffix(token, "'") || len(token) < 2 {
			return false
		}
		decoded := strings.ReplaceAll(token[1:len(token)-1], `'\''`, `'`)
		if shellQuote(decoded) != token {
			return false
		}
		token = decoded
	} else if strings.ContainsAny(token, " \t\r\n'\"`$;&|<>") {
		return false
	}
	return filepath.Base(filepath.Clean(token)) == "compound"
}

func reconcileHookEventGroups(
	groups []any,
	event string,
	desired *desiredHookBinding,
) ([]any, int) {
	reconciled := make([]any, 0, len(groups)+1)
	insertAt := -1
	removed := 0
	for _, group := range groups {
		entry, ok := group.(map[string]any)
		if !ok {
			reconciled = append(reconciled, group)
			continue
		}
		inner, ok := entry["hooks"].([]any)
		if !ok {
			reconciled = append(reconciled, group)
			continue
		}
		kept := make([]any, 0, len(inner))
		removedFromGroup := 0
		for _, hook := range inner {
			if hookIsCompoundEvent(hook, event) {
				removed++
				removedFromGroup++
				continue
			}
			kept = append(kept, hook)
		}
		if removedFromGroup == 0 {
			reconciled = append(reconciled, group)
			continue
		}
		if len(kept) > 0 {
			preserved := make(map[string]any, len(entry))
			for key, value := range entry {
				preserved[key] = value
			}
			preserved["hooks"] = kept
			reconciled = append(reconciled, preserved)
		}
		if desired != nil && insertAt < 0 {
			insertAt = len(reconciled)
		}
	}
	if desired == nil {
		if removed == 0 {
			return groups, 0
		}
		return reconciled, removed
	}
	desiredEntry := map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": desired.Command}},
	}
	if strings.TrimSpace(desired.Matcher) != "" {
		desiredEntry["matcher"] = desired.Matcher
	}
	if insertAt < 0 {
		insertAt = len(reconciled)
	}
	reconciled = append(reconciled, nil)
	copy(reconciled[insertAt+1:], reconciled[insertAt:])
	reconciled[insertAt] = desiredEntry
	return reconciled, removed
}

func sameHookGroups(a, b []any) bool {
	ad, _ := json.Marshal(a)
	bd, _ := json.Marshal(b)
	return bytes.Equal(ad, bd)
}
