package hookplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/harness"
)

const providerLaneTestBinary = "/usr/local/bin/compound"

// providerLaneFixture lays out a launcher profile root the way the launcher writes it: one
// folder per Claude profile with its settings.json, plus the launch profiles file that names
// the folder each launch profile starts Claude in.
func providerLaneFixture(t *testing.T, withLaunchProfiles bool) string {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, launcherProviderProfileRel(""))
	doorsServe := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "'/usr/local/bin/compound' doors serve"}}}},
	}}
	settings := map[string]map[string]any{
		"kimi":                          {"model": "fixture-model", "permissions": map[string]any{"deny": []any{"Bash(rm -rf:*)"}}, "hooks": doorsServe["hooks"]},
		"gpt56-sol-codex":               {"hooks": doorsServe["hooks"]},
		"gateway-neuralwatt":            {"hooks": doorsServe["hooks"]},
		"gateway-deepseek-flash-nvidia": {"hooks": doorsServe["hooks"]},
		"deepseek-flash-api": {"hooks": map[string]any{
			"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "'/usr/local/bin/compound' hook session-start --live --harness claude-deepseek-flash-api"}}}},
		}},
		"gateway-unlisted-local": {"hooks": doorsServe["hooks"]},
		"zai-lane": {"hooks": map[string]any{
			"PreToolUse": []any{map[string]any{"matcher": "*", "hooks": []any{map[string]any{"type": "command", "command": "'/usr/local/bin/compound' hook pre-tool-use --live --harness claude_zai --enforce-governed"}}}},
		}},
	}
	for label, body := range settings {
		writeJSONFile(t, filepath.Join(root, label, "settings.json"), body)
	}
	// Not lanes: a retired folder, a folder without settings, and a stray file.
	writeJSONFile(t, filepath.Join(root, ".retired-2026-01-01", "kimi", "settings.json"), map[string]any{})
	if err := os.MkdirAll(filepath.Join(root, "no-settings"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".DS_Store"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if withLaunchProfiles {
		writeJSONFile(t, filepath.Join(base, launcherLaunchProfilesRel()), map[string]any{
			"launch_profiles": map[string]any{
				"claude-gpt":        map[string]any{"profile_label": "gpt56-sol-codex"},
				"claude-neuralwatt": map[string]any{"profile_label": "gateway-neuralwatt"},
				"claude-zai-full":   map[string]any{"profile_label": "zai-lane"},
				"claude-accounts":   map[string]any{},
			},
		})
	}
	return base
}

func writeJSONFile(t *testing.T, path string, body any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func providerLaneByLabel(t *testing.T, lanes []ProviderLane) map[string]ProviderLane {
	t.Helper()
	out := map[string]ProviderLane{}
	for _, lane := range lanes {
		out[lane.Label] = lane
	}
	return out
}

func TestDiscoverProviderLanesResolvesRegisteredNamesFromTheMachine(t *testing.T) {
	base := providerLaneFixture(t, true)
	lanes, err := DiscoverProviderLanes(base, harness.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	got := providerLaneByLabel(t, lanes)
	want := map[string]string{
		"deepseek-flash-api":            "claude_deepseek_flash_api",
		"gateway-deepseek-flash-nvidia": "claude_deepseek_flash_nvidia",
		"gateway-neuralwatt":            "claude_neuralwatt",
		"gateway-unlisted-local":        "claude_code",
		"gpt56-sol-codex":               "claude_gpt",
		"kimi":                          "claude_kimi",
	}
	if len(got) != len(want) {
		t.Fatalf("lanes = %+v, want exactly %v", lanes, want)
	}
	for label, name := range want {
		lane, ok := got[label]
		if !ok {
			t.Fatalf("lane %s not found in %+v", label, lanes)
		}
		if lane.Harness != name {
			t.Fatalf("lane %s resolved to %q, want %q", label, lane.Harness, name)
		}
		if _, registered := harness.DefaultRegistry().Harnesses[name]; !registered {
			t.Fatalf("lane %s resolved to unregistered name %q", label, name)
		}
	}
	// The governed lane keeps its compiled posture, and non-lane folders are never lanes.
	for _, excluded := range []string{"zai-lane", ".retired-2026-01-01", "no-settings", ".DS_Store"} {
		if _, ok := got[excluded]; ok {
			t.Fatalf("%s must not be a record-only provider lane", excluded)
		}
	}
}

// Without the launcher's launch profiles file, a folder whose own name is not registered takes
// the generic Claude posture instead of borrowing another lane's registered name.
func TestDiscoverProviderLanesWithoutLaunchProfilesDoesNotGuess(t *testing.T) {
	base := providerLaneFixture(t, false)
	lanes, err := DiscoverProviderLanes(base, harness.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	got := providerLaneByLabel(t, lanes)
	if name := got["gpt56-sol-codex"].Harness; name != "claude_code" {
		t.Fatalf("gpt56-sol-codex resolved to %q without a launch profile naming it, want claude_code", name)
	}
	if name := got["gateway-neuralwatt"].Harness; name != "claude_neuralwatt" {
		t.Fatalf("gateway-neuralwatt = %q, want claude_neuralwatt from the gateway-<lane> folder name", name)
	}
	if lanes, err := DiscoverProviderLanes(t.TempDir(), harness.DefaultRegistry()); err != nil || len(lanes) != 0 {
		t.Fatalf("a machine without the launcher has no provider lanes: %+v %v", lanes, err)
	}
}

func TestInstallProviderLanesIsRecordOnlyAndKeepsOtherSettings(t *testing.T) {
	base := providerLaneFixture(t, true)
	root := filepath.Join(base, launcherProviderProfileRel(""))
	before := map[string][]byte{}
	for _, label := range []string{"kimi", "zai-lane", "gateway-unlisted-local", "deepseek-flash-api"} {
		data, err := os.ReadFile(filepath.Join(root, label, "settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		before[label] = data
	}

	check, err := InstallHooksWithRoster(base, providerLaneTestBinary, "global", []string{ProviderLanesHarness}, "", nil, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(check.Entries) != 6 {
		t.Fatalf("check entries = %+v, want all six lanes", check.Entries)
	}
	for _, entry := range check.Entries {
		if entry.Action != "absent" && entry.Action != "drift" {
			t.Fatalf("check on an unwired lane = %s for %s", entry.Action, entry.Harness)
		}
	}
	for label, data := range before {
		if after, _ := os.ReadFile(filepath.Join(root, label, "settings.json")); string(after) != string(data) {
			t.Fatalf("--check changed %s", label)
		}
	}

	report, err := InstallHooksWithRoster(base, providerLaneTestBinary, "global", []string{ProviderLanesHarness}, "", nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	kimi := hookInstallEntryForHarness(t, report, "claude_kimi")
	if kimi.Matcher != "" || kimi.Command != "" {
		t.Fatalf("record-only lane reported a blocking guard: %+v", kimi)
	}
	events := map[string]HookInstallBinding{}
	for _, binding := range kimi.Bindings {
		events[binding.Event] = binding
		if binding.Blocking || strings.Contains(binding.Command, "--enforce") {
			t.Fatalf("record-only lane got a blocking binding: %+v", binding)
		}
		if !strings.HasSuffix(strings.Fields(binding.Command)[len(strings.Fields(binding.Command))-1], "claude_kimi") && binding.Event != "PreToolUse" {
			t.Fatalf("binding does not report as the registered harness: %+v", binding)
		}
	}
	if len(events) != 4 || events["UserPromptSubmit"].Event != "" {
		t.Fatalf("bindings = %+v, want SessionStart, PreToolUse, PostToolUse and SessionEnd", kimi.Bindings)
	}
	if got := events["PreToolUse"]; got.Command != "'/usr/local/bin/compound' hook pre-tool-use --live --harness claude_kimi --spool" || got.Timeout != WatchOnlySpoolTimeoutSeconds || got.Matcher != "*" {
		t.Fatalf("PreToolUse = %+v, want the spool form with its bound", got)
	}
	if got := events["SessionEnd"].Command; got != "'/usr/local/bin/compound' hook session-end --live --harness claude_kimi" {
		t.Fatalf("SessionEnd = %q, want the generic session-end verb", got)
	}

	settings := readJSONMap(t, filepath.Join(root, "kimi", "settings.json"))
	if settings["model"] != "fixture-model" || settings["permissions"] == nil {
		t.Fatalf("install dropped unrelated settings: %v", settings)
	}
	sessionStart := commandsFor(t, settings, "SessionStart")
	if len(sessionStart) != 2 || sessionStart[0] != "'/usr/local/bin/compound' doors serve" || sessionStart[1] != "'/usr/local/bin/compound' hook session-start --live --harness claude_kimi" {
		t.Fatalf("SessionStart = %v, want the existing doors entry kept and the session-start hook added", sessionStart)
	}

	deepseek := commandsFor(t, readJSONMap(t, filepath.Join(root, "deepseek-flash-api", "settings.json")), "SessionStart")
	if len(deepseek) != 1 || deepseek[0] != "'/usr/local/bin/compound' hook session-start --live --harness claude_deepseek_flash_api" {
		t.Fatalf("deepseek SessionStart = %v, want the dashed name replaced by the registered one", deepseek)
	}
	if after, _ := os.ReadFile(filepath.Join(root, "zai-lane", "settings.json")); string(after) != string(before["zai-lane"]) {
		t.Fatal("provider-lane install touched the governed zai lane")
	}
	// A lane with no registry row of its own records as the generic Claude posture.
	generic := hookInstallEntryForHarness(t, report, "claude_code")
	if !strings.HasSuffix(generic.ConfigPath, filepath.Join("gateway-unlisted-local", "settings.json")) {
		t.Fatalf("generic lane entry = %+v", generic)
	}
	for _, binding := range generic.Bindings {
		if binding.Blocking || strings.Contains(binding.Command, "--enforce") {
			t.Fatalf("generic lane got a blocking binding: %+v", binding)
		}
	}
	genericCommands := readJSONMap(t, filepath.Join(root, "gateway-unlisted-local", "settings.json"))
	if got := commandsFor(t, genericCommands, "SessionEnd"); len(got) != 1 || got[0] != "'/usr/local/bin/compound' hook session-end-adapter --live --harness claude_code" {
		t.Fatalf("generic lane SessionEnd = %v", got)
	}
	if got := commandsFor(t, genericCommands, "PreToolUse"); len(got) != 1 || got[0] != "'/usr/local/bin/compound' hook pre-tool-use --live --harness claude_code --spool" {
		t.Fatalf("generic lane PreToolUse = %v", got)
	}

	again, err := InstallHooksWithRoster(base, providerLaneTestBinary, "global", []string{ProviderLanesHarness}, "", nil, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range again.Entries {
		if entry.Action != "ok" {
			t.Fatalf("check after install = %s for %s, want ok", entry.Action, entry.Harness)
		}
	}
}

// `--harness all` (an empty list) covers provider lanes once each, next to the governed lane
// that owns its own folder; a named harness never reaches them.
func TestInstallAllCoversProviderLanesButANamedHarnessDoesNot(t *testing.T) {
	base := providerLaneFixture(t, true)
	all, err := InstallHooks(base, providerLaneTestBinary, "global", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, entry := range all.Entries {
		seen[entry.Harness]++
	}
	for _, name := range []string{"claude_kimi", "claude_gpt", "claude_neuralwatt", "claude_deepseek_flash_api", "claude_deepseek_flash_nvidia", claudeZaiHarness} {
		if seen[name] != 1 {
			t.Fatalf("install --check all saw %s %d times: %v", name, seen[name], seen)
		}
	}
	zai := hookInstallEntryForHarness(t, all, claudeZaiHarness)
	if !strings.HasSuffix(zai.ConfigPath, filepath.Join("_providers", "zai-lane", "settings.json")) {
		t.Fatalf("zai entry = %+v", zai)
	}

	named, err := InstallHooks(base, providerLaneTestBinary, "global", []string{"claude-code"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(named.Entries) != 1 || named.Entries[0].Harness != "claude-code" {
		t.Fatalf("a named harness reached provider lanes: %+v", named)
	}
}

func TestInstallProviderLanesHonoursTheRoster(t *testing.T) {
	base := providerLaneFixture(t, true)
	report, err := InstallHooksWithRoster(base, providerLaneTestBinary, "global", []string{ProviderLanesHarness}, "", map[string]bool{"SessionStart": true}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range hookInstallEntryForHarness(t, report, "claude_kimi").Bindings {
		if binding.Event != "SessionStart" {
			t.Fatalf("roster narrowed to SessionStart installed %s", binding.Event)
		}
	}
}

func TestUninstallProviderLanesRemovesOnlyCompoundEntries(t *testing.T) {
	base := providerLaneFixture(t, true)
	if _, err := InstallHooksWithRoster(base, providerLaneTestBinary, "global", []string{ProviderLanesHarness}, "", nil, false, nil); err != nil {
		t.Fatal(err)
	}
	report, err := UninstallHooks(base, providerLaneTestBinary, "global", []string{ProviderLanesHarness}, "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if entry := hookInstallEntryForHarness(t, report, "claude_kimi"); entry.Action != "removed" {
		t.Fatalf("uninstall kimi = %+v, want removed", entry)
	}
	settings := readJSONMap(t, filepath.Join(base, launcherProviderProfileRel("kimi"), "settings.json"))
	if got := commandsFor(t, settings, "SessionStart"); len(got) != 1 || got[0] != "'/usr/local/bin/compound' doors serve" {
		t.Fatalf("SessionStart after uninstall = %v, want only the doors entry", got)
	}
	for _, event := range []string{"PreToolUse", "PostToolUse", "SessionEnd"} {
		if got := commandsFor(t, settings, event); len(got) != 0 {
			t.Fatalf("%s left after uninstall: %v", event, got)
		}
	}
}

func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func commandsFor(t *testing.T, settings map[string]any, event string) []string {
	t.Helper()
	hooks, _ := settings["hooks"].(map[string]any)
	groups, _ := hooks[event].([]any)
	var out []string
	for _, group := range groups {
		entry, _ := group.(map[string]any)
		inner, _ := entry["hooks"].([]any)
		for _, hook := range inner {
			command, _ := hook.(map[string]any)["command"].(string)
			out = append(out, command)
		}
	}
	return out
}

// Every name the hook plan derives for a lane home must carry the record-only
// floor in the registry the hook runtime reads, or the lane's events are written
// as fail_open (capability absent) although its hooks ran.
func TestEveryDerivedProviderLaneNameHasTheRecordOnlyFloor(t *testing.T) {
	reg := harness.DefaultRegistry()
	for _, withProfiles := range []bool{true, false} {
		lanes, err := DiscoverProviderLanes(providerLaneFixture(t, withProfiles), reg)
		if err != nil {
			t.Fatal(err)
		}
		for _, lane := range lanes {
			for _, event := range []string{"SessionStart", "PostToolUse", "SessionEnd"} {
				if !reg.Supports(lane.Harness, event) {
					t.Errorf("lane %s reports as %s, which does not support %s", lane.Label, lane.Harness, event)
				}
			}
			if lane.Harness != "claude_code" && reg.Supports(lane.Harness, "PreToolUse") {
				t.Errorf("record-only lane %s (%s) must not claim a blocking pre-tool hook", lane.Label, lane.Harness)
			}
		}
	}
}
