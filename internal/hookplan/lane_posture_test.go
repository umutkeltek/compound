package hookplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The provider-lane profiles are the ones the lane launcher starts the lanes
// from. Install, the roster gate and the live posture reader must all use them;
// on the launcher's older config tree they reported a posture no lane ran with.
func TestProviderLaneHooksLiveInLauncherProviderProfiles(t *testing.T) {
	base := t.TempDir()
	want := map[string]string{
		claudeMinimaxHarness: "minimax-lane",
		claudeZaiHarness:     "zai-lane",
		claudeQwenHarness:    "qwen",
	}
	rep, err := InstallHooks(base, "/opt/compound/bin/compound", "global", []string{claudeMinimaxHarness, claudeZaiHarness, claudeQwenHarness}, false)
	if err != nil {
		t.Fatal(err)
	}
	for harnessName, label := range want {
		path := filepath.Join(base, ".switchback", "config", "claude", "_providers", label, "settings.json")
		entry := hookInstallEntryForHarness(t, rep, harnessName)
		if entry.ConfigPath != path {
			t.Fatalf("%s installed into %q, want the launcher provider profile %q", harnessName, entry.ConfigPath, path)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s settings not written at %s: %v", harnessName, path, err)
		}
		if got, ok := HookConfigPath(base, harnessName); !ok || got != path {
			t.Fatalf("HookConfigPath(%s) = %q, %v; want %q", harnessName, got, ok, path)
		}
		live, ok := ReadLiveHookPosture(base, harnessName)
		if !ok || live.ConfigPath != path || live.Posture != "enforce" {
			t.Fatalf("ReadLiveHookPosture(%s) = %+v, %v; want enforce from %s", harnessName, live, ok, path)
		}
	}
	if _, err := os.Stat(filepath.Join(base, ".config", "switchback")); !os.IsNotExist(err) {
		t.Fatalf("install must not write the retired launcher config tree, stat err=%v", err)
	}
}

// A reinstall keeps the PreToolUse posture an installed provider lane already
// has. A watch-only lane becomes blocking-in-governed-runs-only, so the
// operator's own sessions keep observing while governed runs block.
func TestLaneReinstallKeepsInstalledPreToolUsePosture(t *testing.T) {
	const binary = "/opt/compound/bin/compound"
	cases := []struct {
		name         string
		existing     bool
		existingFlag string
		wantFlag     string
		wantPosture  string
	}{
		{name: "fresh install blocks everywhere", wantFlag: "--enforce", wantPosture: HookPostureEnforced},
		{name: "watch-only lane blocks only in governed runs", existing: true, existingFlag: "", wantFlag: EnforceGovernedFlag, wantPosture: HookPostureGovernedOnly},
		{name: "enforced lane stays enforced", existing: true, existingFlag: "--enforce", wantFlag: "--enforce", wantPosture: HookPostureEnforced},
		{name: "governed-only lane stays governed-only", existing: true, existingFlag: EnforceGovernedFlag, wantFlag: EnforceGovernedFlag, wantPosture: HookPostureGovernedOnly},
	}
	for _, harnessName := range []string{claudeMinimaxHarness, claudeZaiHarness, claudeQwenHarness} {
		for _, tc := range cases {
			t.Run(harnessName+"/"+tc.name, func(t *testing.T) {
				base := t.TempDir()
				path, ok := LaneHookSettingsPath(base, harnessName)
				if !ok {
					t.Fatalf("%s is not a provider lane", harnessName)
				}
				if tc.existing {
					command := HookCommandForEvent(binary, harnessName, "PreToolUse")
					if tc.existingFlag != "" {
						command += " " + tc.existingFlag
					}
					writeLanePreToolUse(t, path, command)
				}
				rep, err := InstallHooks(base, binary, "global", []string{harnessName}, false)
				if err != nil {
					t.Fatal(err)
				}
				command := onDiskCompoundPreToolUse(t, path)
				if want := HookCommandForEvent(binary, harnessName, "PreToolUse") + " " + tc.wantFlag; command != want {
					t.Fatalf("installed PreToolUse = %q, want %q", command, want)
				}
				postures := InstalledHookPostures(path)
				if len(postures) != 1 || postures[0].Posture != tc.wantPosture {
					t.Fatalf("installed posture = %+v, want %s", postures, tc.wantPosture)
				}
				// The report (and so the published hook authority) describes
				// the written command: still blocking for governed execution.
				entry := hookInstallEntryForHarness(t, rep, harnessName)
				var reported *HookInstallBinding
				for i := range entry.Bindings {
					if entry.Bindings[i].Event == "PreToolUse" {
						reported = &entry.Bindings[i]
					}
				}
				if reported == nil || reported.Command != command || !reported.Blocking {
					t.Fatalf("reported PreToolUse = %+v, want blocking %q", reported, command)
				}
				if entry.Command != command {
					t.Fatalf("reported guard command = %q, want %q", entry.Command, command)
				}
				again, err := InstallHooks(base, binary, "global", []string{harnessName}, true)
				if err != nil {
					t.Fatal(err)
				}
				if action := hookInstallEntryForHarness(t, again, harnessName).Action; action != "ok" {
					t.Fatalf("a second install must keep the posture and change nothing, check action = %q", action)
				}
			})
		}
	}
}

// Posture keeping is only for provider lanes: Claude Code keeps its compiled
// watch-only spool binding even when an enforced binding was installed by hand.
func TestPostureKeepingDoesNotTouchInteractiveHarnesses(t *testing.T) {
	const binary = "/opt/compound/bin/compound"
	base := t.TempDir()
	path := filepath.Join(base, ConfigRel("claude-code", "global"))
	writeLanePreToolUse(t, path, HookCommandForEvent(binary, "claude-code", "PreToolUse"))
	if _, err := InstallHooks(base, binary, "global", []string{"claude-code"}, false); err != nil {
		t.Fatal(err)
	}
	command := onDiskCompoundPreToolUse(t, path)
	if strings.Contains(command, EnforceGovernedFlag) || !strings.HasSuffix(command, " --spool") {
		t.Fatalf("claude-code PreToolUse = %q, want the compiled --spool watch-only binding", command)
	}
}

func TestLiveHookPostureReadsGovernedOnlyAsEnforceAtLaunch(t *testing.T) {
	base := t.TempDir()
	path, _ := LaneHookSettingsPath(base, "claude_minimax")
	writeLanePreToolUse(t, path, GovernedOnlyHookCommandForEvent("/opt/compound/bin/compound", claudeMinimaxHarness, "PreToolUse"))
	live, ok := ReadLiveHookPosture(base, "claude_minimax")
	if !ok || live.Posture != "enforce" {
		t.Fatalf("a runner launch always carries a RunAttempt id, so governed-only reads as enforce; got %+v, %v", live, ok)
	}
	if got := InstalledHookPostures(path); len(got) != 1 || got[0].Posture != HookPostureGovernedOnly {
		t.Fatalf("doctor reader must keep the governed-only posture distinct, got %+v", got)
	}
}

func writeLanePreToolUse(t *testing.T, path, command string) {
	t.Helper()
	config := map[string]any{
		"model": "kept-unrelated-setting",
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "*",
					"hooks":   []any{map[string]any{"type": "command", "command": command}},
				},
			},
		},
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func onDiskCompoundPreToolUse(t *testing.T, path string) string {
	t.Helper()
	root := readJSON(t, path)
	hooks, _ := root["hooks"].(map[string]any)
	groups, _ := hooks["PreToolUse"].([]any)
	var found []string
	for _, group := range groups {
		inner, _ := group.(map[string]any)["hooks"].([]any)
		for _, hook := range inner {
			if hookIsCompoundEvent(hook, "PreToolUse") {
				command, _ := hook.(map[string]any)["command"].(string)
				found = append(found, command)
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one Compound PreToolUse in %s, got %q", path, found)
	}
	return found[0]
}
