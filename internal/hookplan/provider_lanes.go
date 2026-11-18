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
// and the launcher's launch profiles file for the harness name each folder runs as. Every
// lane home records under a registered name that is its own (resolveProviderLaneNames);
// a home with no such name is reported "not wired" and gets no hooks, because recording
// under another home's name, or under claude_code (the operator's own seats), would mix
// two sources in one record.

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
	// Harness is the registered harness name the hooks report as; empty when NotWired.
	Harness string `json:"harness"`
	// ConfigPath is the lane's Claude settings file.
	ConfigPath string `json:"config_path"`
	// NotWired says why the home gets no hooks: no registered name is unique to it.
	NotWired string `json:"not_wired,omitempty"`
}

// RefreshProviderLane applies the group writer to exactly one discovered home.
// Its checked and writing paths both require an unambiguous registered target.
func RefreshProviderLane(baseDir, binaryPath, name string, check bool, publish func(HookInstallReport) error) (HookInstallReport, error) {
	if !harness.DefaultRegistry().ProviderLaneName(name) {
		return HookInstallReport{}, fmt.Errorf("unregistered provider lane %q", name)
	}
	return installHooksImpl(baseDir, binaryPath, "global", []string{ProviderLanesHarness}, "", nil, check, publish, name)
}

// HookLaneProblem is a lane home the plan did not wire. State is LaneStateError (a file
// could not be read) or LaneStateNotWired (no name unique to the home). Either way the
// run goes on for every other harness and lane.
type HookLaneProblem struct {
	Label      string `json:"label,omitempty"`
	ConfigPath string `json:"config_path,omitempty"`
	State      string `json:"state"`
	Detail     string `json:"detail"`
}

const (
	LaneStateError    = "error"
	LaneStateNotWired = "not wired"
)

// HasLaneErrors reports whether a lane home could not be read. The CLI fails on it
// only when the record-only lane set was named on its own.
func (r HookInstallReport) HasLaneErrors() bool {
	for _, p := range r.LaneProblems {
		if p.State == LaneStateError {
			return true
		}
	}
	return false
}

// wirableProviderLanes is the lane set an install or uninstall works on: discovery
// errors and homes without a name of their own become problem rows, never a failed run.
func wirableProviderLanes(baseDir string, report *HookInstallReport) []ProviderLane {
	lanes, err := DiscoverProviderLanes(baseDir, harness.DefaultRegistry())
	if err != nil {
		report.LaneProblems = append(report.LaneProblems, HookLaneProblem{State: LaneStateError, Detail: err.Error()})
	}
	var out []ProviderLane
	for _, lane := range lanes {
		if lane.NotWired != "" {
			report.LaneProblems = append(report.LaneProblems, HookLaneProblem{Label: lane.Label, ConfigPath: lane.ConfigPath, State: LaneStateNotWired, Detail: lane.NotWired})
			continue
		}
		out = append(out, lane)
	}
	return out
}

// laneProblem records one lane whose settings file could not be prepared.
func laneProblem(report *HookInstallReport, lane ProviderLane, err error) {
	report.LaneProblems = append(report.LaneProblems, HookLaneProblem{Label: lane.Label, ConfigPath: lane.ConfigPath, State: LaneStateError, Detail: err.Error()})
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
// each with its own registered harness name, or NotWired when it has none. A missing
// profile root is not an error: there are no lanes. An unreadable launch profiles file
// is returned as the error together with the lanes, resolved by folder name alone.
func DiscoverProviderLanes(baseDir string, registry harness.Registry) ([]ProviderLane, error) {
	root := filepath.Join(baseDir, launcherProviderProfileRel(""))
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read provider lane profiles: %w", err)
	}
	launchNames, profilesErr := launchProfileNamesByLabel(filepath.Join(baseDir, launcherLaunchProfilesRel()))
	if profilesErr != nil {
		profilesErr = fmt.Errorf("%w; lanes resolved by folder name", profilesErr)
		launchNames = map[string][]string{}
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
		lanes = append(lanes, ProviderLane{Label: label, ConfigPath: settings})
	}
	sort.Slice(lanes, func(i, j int) bool { return lanes[i].Label < lanes[j].Label })
	resolveProviderLaneNames(lanes, launchNames, registry)
	return lanes, profilesErr
}

// WiringHookPlan hands `compound harness check` the hook plan's own readers, so
// the check names the same lane homes, install commands and hook states as
// `compound hook install --check`.
func WiringHookPlan() harness.HookPlan {
	return harness.HookPlan{
		LaneSettings: LaneHookSettingsPath,
		RecordOnlyLanes: func(baseDir string) (map[string]string, error) {
			lanes, err := DiscoverProviderLanes(baseDir, harness.DefaultRegistry())
			out := map[string]string{}
			for _, lane := range lanes {
				out[lane.ConfigPath] = lane.Harness
			}
			return out, err
		},
		RecordOnlyInstall: ProviderLanesHarness,
		PreToolState:      PreToolHookState,
	}
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

// resolveProviderLaneNames gives each lane home a registered name that belongs to it alone.
// A name is claimed strongly by the launch profiles that start Claude in the folder (the
// registry names Claude lanes after the launch profile: claude-gpt runs in gpt56-sol-codex
// and registers as claude_gpt) and by claude_<label>; weakly by claude_<lane> for a
// gateway-<lane> folder (the launcher's name for a lane reached through its gateway). A
// name goes to a home only when exactly one home claims it at the strongest level anyone
// claims it, and only a name the registry gives the record-only floor counts
// (Registry.ProviderLaneName, which never includes claude_code). A home left without a
// name is NotWired.
func resolveProviderLaneNames(lanes []ProviderLane, launchNames map[string][]string, registry harness.Registry) {
	const strong, weak = 2, 1
	type claim struct {
		name  string
		level int
	}
	claims := make([][]claim, len(lanes)) // per home, in priority order
	best := map[string]int{}              // the strongest claim level on each name
	count := map[string]int{}             // homes claiming each name at that level
	for i, lane := range lanes {
		seen := map[string]bool{}
		add := func(name string, level int) {
			if seen[name] || !registry.ProviderLaneName(name) {
				return
			}
			seen[name] = true
			claims[i] = append(claims[i], claim{name, level})
			switch {
			case level > best[name]:
				best[name], count[name] = level, 1
			case level == best[name]:
				count[name]++
			}
		}
		for _, profile := range launchNames[lane.Label] {
			add(underscoreName(profile), strong)
		}
		add("claude_"+underscoreName(lane.Label), strong)
		if rest, ok := strings.CutPrefix(lane.Label, "gateway-"); ok && rest != "" {
			add("claude_"+underscoreName(rest), weak)
		}
	}
	for i := range lanes {
		for _, c := range claims[i] {
			if c.level == best[c.name] && count[c.name] == 1 {
				lanes[i].Harness = c.name
				break
			}
		}
		if lanes[i].Harness == "" {
			lanes[i].NotWired = "no registered harness name belongs to this home alone"
		}
	}
}

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
