package hookplan

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"compound/internal/harness"
)

// provider_lanes.go covers the launcher's other Claude profiles: every profile folder under
// launcherProviderProfileRel("") that is not one of the four hook-governed lanes. Those lanes
// run Claude Code through some other model provider and had no Compound hooks, or only a
// session-start under a harness name the registry does not list, so their sessions left no
// record. They get a record-only set: the session briefing, tool calls written to the spool
// before they run and recorded after, and a session-end receipt. Nothing here can block a
// tool call; a lane that should gate mutations is enrolled as a named isolated lane instead.
//
// Lanes are read from the machine, not from a list in code: the profile folders that exist,
// and the launcher's launch profiles file for the harness name each folder runs as. A lane
// with no registry row of its own reports as claude_code, the canonical generic Claude
// posture docs/harness-wiring.md prescribes for launch profiles that are model selections
// rather than distinct harness types.

// ProviderLanesHarness is the `compound hook install --harness` selector for every record-only
// provider lane found under the launcher's profile root.
const ProviderLanesHarness = "claude-provider-lanes"

// providerLaneSettingsFile is the Claude settings file inside one lane profile folder.
const providerLaneSettingsFile = "settings.json"

// launcherLaunchProfilesRel is the launcher's launch profiles file, relative to the user's base
// directory. Each launch profile names the profile folder (profile_label) it starts Claude in.
func launcherLaunchProfilesRel() string {
	return filepath.Join(".switchback", "config", "launch-profiles.json")
}

// ProviderLane is one launcher profile folder that takes the record-only hook set.
type ProviderLane struct {
	// Label is the profile folder name under the launcher's profile root.
	Label string `json:"label"`
	// Harness is the registered harness name the hooks report as.
	Harness string `json:"harness"`
	// ConfigPath is the lane's Claude settings file.
	ConfigPath string `json:"config_path"`
}

// governedLaneLabels are the profile folders the four hook-governed lanes own. They keep their
// own compiled posture (including blocking) and are never treated as record-only.
func governedLaneLabels() map[string]bool {
	out := map[string]bool{}
	for _, installer := range []string{claudeZaiHarness, claudeMinimaxHarness, claudeQwenHarness, claudeUnattendedHarness} {
		if label, ok := laneProfileLabel(installer); ok {
			out[label] = true
		}
	}
	return out
}

// DiscoverProviderLanes lists the record-only provider lanes under baseDir, sorted by label,
// with each lane's registered harness name resolved. A missing profile root is not an error:
// there are no lanes.
func DiscoverProviderLanes(baseDir string, registry harness.Registry) ([]ProviderLane, error) {
	root := filepath.Join(baseDir, launcherProviderProfileRel(""))
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read provider lane profiles: %w", err)
	}
	launchNames, err := launchProfileNamesByLabel(filepath.Join(baseDir, launcherLaunchProfilesRel()))
	if err != nil {
		return nil, err
	}
	governed := governedLaneLabels()
	var lanes []ProviderLane
	for _, entry := range entries {
		label := entry.Name()
		if strings.HasPrefix(label, ".") || governed[label] || !entry.IsDir() {
			continue
		}
		settings := filepath.Join(root, label, providerLaneSettingsFile)
		if info, err := os.Stat(settings); err != nil || !info.Mode().IsRegular() {
			continue
		}
		lanes = append(lanes, ProviderLane{
			Label:      label,
			Harness:    resolveProviderLaneHarness(label, launchNames[label], registry),
			ConfigPath: settings,
		})
	}
	sort.Slice(lanes, func(i, j int) bool { return lanes[i].Label < lanes[j].Label })
	return lanes, nil
}

// launchProfileNamesByLabel maps each profile folder to the launch profiles that start Claude
// in it, sorted. A missing file means no launch profiles; an unreadable one is an error.
func launchProfileNamesByLabel(path string) (map[string][]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string][]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read launch profiles: %w", err)
	}
	var doc struct {
		LaunchProfiles map[string]struct {
			ProfileLabel string `json:"profile_label"`
		} `json:"launch_profiles"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse launch profiles %s: %w", path, err)
	}
	out := map[string][]string{}
	for name, profile := range doc.LaunchProfiles {
		if label := strings.TrimSpace(profile.ProfileLabel); label != "" {
			out[label] = append(out[label], name)
		}
	}
	for label := range out {
		sort.Strings(out[label])
	}
	return out, nil
}

// resolveProviderLaneHarness returns the registered harness name a lane's hooks report as. The
// registry names Claude lanes after their launch profile in underscore form (launch profile
// claude-gpt runs in folder gpt56-sol-codex and registers as claude_gpt), so a launch profile
// that starts Claude in this folder wins. Without one, the folder name itself is tried as
// claude_<label>, and for a gateway-<lane> folder (the launcher's name for a lane reached
// through its gateway) as claude_<lane>. A lane with no row of its own is claude_code.
func resolveProviderLaneHarness(label string, launchProfiles []string, registry harness.Registry) string {
	candidates := make([]string, 0, len(launchProfiles)+2)
	for _, name := range launchProfiles {
		candidates = append(candidates, underscoreName(name))
	}
	candidates = append(candidates, "claude_"+underscoreName(label))
	if rest, ok := strings.CutPrefix(label, "gateway-"); ok && rest != "" {
		candidates = append(candidates, "claude_"+underscoreName(rest))
	}
	for _, candidate := range candidates {
		if !strings.HasPrefix(candidate, "claude_") || candidate == "claude_code" {
			continue
		}
		if _, ok := registry.Harnesses[candidate]; ok {
			return candidate
		}
	}
	return genericProviderLaneHarness
}

// genericProviderLaneHarness is the registered identity of a lane with no registry row: the
// generic Claude posture, which records and never blocks.
const genericProviderLaneHarness = "claude_code"

func underscoreName(name string) string {
	return strings.ReplaceAll(strings.TrimSpace(name), "-", "_")
}

// providerLaneBindings is the record-only hook set for one provider lane. PreToolUse appends the
// call to the spool and answers at once (--spool), so it can never block or slow a tool call;
// SessionEnd uses the bounded session-end adapter for the Claude-family names it serves and the
// generic session-end verb for every other registered lane (HookCommandForEvent decides).
func providerLaneBindings(binaryPath, harnessName string) []desiredHookBinding {
	return []desiredHookBinding{
		{Event: "SessionStart", Command: HookCommandForEvent(binaryPath, harnessName, "SessionStart")},
		{Event: "PreToolUse", Matcher: HarnessMatcher(harnessName), Command: HookCommandForEvent(binaryPath, harnessName, "PreToolUse") + " --spool", Timeout: WatchOnlySpoolTimeoutSeconds},
		{Event: "PostToolUse", Matcher: HarnessMatcher(harnessName), Command: HookCommandForEvent(binaryPath, harnessName, "PostToolUse")},
		{Event: "SessionEnd", Command: HookCommandForEvent(binaryPath, harnessName, "SessionEnd")},
	}
}
