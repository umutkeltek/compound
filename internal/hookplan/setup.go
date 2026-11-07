package hookplan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// setup.go holds what `compound init`, `compound hook uninstall` and the doctor's hook section
// need from the hook plan: which supported harnesses this machine has, and the exact reverse
// of an install. The install writer stays in install.go; uninstall reuses its staging,
// verification and rollback so both directions share one no-clobber path.

// SetupHarness is one harness `compound init` looks for on this machine.
type SetupHarness struct {
	Name     string `json:"name"`     // installer label: claude-code, codex, opencode
	Label    string `json:"label"`    // the name a user knows it by
	Present  bool   `json:"present"`  // its config folder exists or its command is on PATH
	Wireable bool   `json:"wireable"` // false: Compound observes it, init installs no hooks
	Evidence string `json:"evidence"` // the folder or command that proved it is here
}

type setupHarnessSpec struct {
	name, label, dir, command string
	wireable                  bool
}

// setupHarnessSpecs is the public setup roster. Isolated provider profiles are
// deliberately absent: they are opt-in lanes wired by name with `hook install`.
var setupHarnessSpecs = []setupHarnessSpec{
	{name: "claude-code", label: "Claude Code", dir: ".claude", command: "claude", wireable: true},
	{name: "codex", label: "Codex", dir: ".codex", command: "codex", wireable: true},
	{name: "opencode", label: "OpenCode", dir: filepath.Join(".config", "opencode"), command: "opencode"},
}

// DetectSetupHarnesses reports each harness in the setup roster: present when its config
// folder exists under base or its command resolves through lookPath (nil lookPath skips
// the command check).
func DetectSetupHarnesses(base string, lookPath func(string) (string, error)) []SetupHarness {
	out := make([]SetupHarness, 0, len(setupHarnessSpecs))
	for _, spec := range setupHarnessSpecs {
		h := SetupHarness{Name: spec.name, Label: spec.label, Wireable: spec.wireable}
		if dir := filepath.Join(base, spec.dir); isDir(dir) {
			h.Present, h.Evidence = true, dir
		} else if lookPath != nil {
			if path, err := lookPath(spec.command); err == nil {
				h.Present, h.Evidence = true, path
			}
		}
		out = append(out, h)
	}
	return out
}

// SetupHarnessLabel is the user-facing name of an installer label.
func SetupHarnessLabel(name string) string {
	for _, spec := range setupHarnessSpecs {
		if spec.name == name {
			return spec.label
		}
	}
	return name
}

// HarnessConfigPresent reports whether a harness's own config folder exists under base. A
// read-only report uses it to leave out harnesses this machine does not have, so a fresh
// machine never lists another user's provider profiles.
func HarnessConfigPresent(base, harnessName, scope string) bool {
	return isDir(filepath.Dir(filepath.Join(base, ConfigRel(harnessName, scope))))
}

// DefaultSetupBase is where harness configs live when no --base is given: the current
// project for scope "project" (Claude Code reads that project's local settings file),
// the user's home folder otherwise.
func DefaultSetupBase(scope string) (string, error) {
	if scope == "project" {
		return os.Getwd()
	}
	return os.UserHomeDir()
}

// UninstallHooks removes Compound's own entries from each requested harness config and keeps
// every other key and hook. check=true only reports. Entry actions: "removed" (Compound
// entries taken out; "would remove" under check) or "absent" (nothing of Compound's there;
// the file is not touched or created). Each entry carries the bindings install would write,
// so the installed-hook authority can record them as removed. publish runs only when a
// config changed, inside the same rollback as install.
func UninstallHooks(
	baseDir, binaryPath, scope string,
	harnesses []string,
	configDir string,
	check bool,
	publish func(HookInstallReport) error,
) (HookInstallReport, error) {
	if strings.TrimSpace(baseDir) == "" || strings.TrimSpace(binaryPath) == "" {
		return HookInstallReport{}, fmt.Errorf("base dir and binary path required")
	}
	report := HookInstallReport{Check: check, Scope: scope}
	want := map[string]bool{}
	for _, h := range harnesses {
		want[strings.TrimSpace(h)] = true
	}
	if strings.TrimSpace(configDir) != "" && (len(want) != 1 || want["codex"]) {
		return report, fmt.Errorf("config dir supported only for a single Claude profile harness")
	}
	var prepared []preparedHookConfig
	changed := HookInstallReport{Check: check, Scope: scope}
	for _, harnessName := range []string{"claude-code", claudeZaiHarness, claudeMinimaxHarness, claudeQwenHarness, "codex"} {
		if len(want) > 0 && !want[harnessName] {
			continue
		}
		configPath := filepath.Join(baseDir, ConfigRel(harnessName, scope))
		if strings.TrimSpace(configDir) != "" {
			configPath = filepath.Join(configDir, "settings.json")
		}
		bindings, _, err := compiledManagedHookBindings(binaryPath, harnessName, scope, nil)
		if err != nil {
			return report, fmt.Errorf("%s: %w", harnessName, err)
		}
		staged, _, err := prepareHookBindings(harnessName, configPath, nil, check)
		if err != nil {
			return report, fmt.Errorf("%s: %w", harnessName, err)
		}
		entry := HookInstallEntry{Harness: harnessName, ConfigPath: configPath, ConfigRevision: hookConfigRevision(staged.after), Action: "absent"}
		if staged.action != "ok" {
			entry.Action = "removed"
			if check {
				entry.Action = "would remove"
			}
			for _, binding := range bindings {
				entry.Bindings = append(entry.Bindings, HookInstallBinding{Event: binding.Event, Matcher: binding.Matcher, Command: binding.Command, Blocking: binding.Blocking})
			}
			prepared = append(prepared, staged)
			changed.Entries = append(changed.Entries, entry)
		}
		report.Entries = append(report.Entries, entry)
	}
	if check || len(prepared) == 0 {
		return report, nil
	}
	if publish != nil {
		inner := publish
		publish = func(HookInstallReport) error { return inner(changed) }
	}
	return report, commitHookInstallTransaction(prepared, report, publish, nil)
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
