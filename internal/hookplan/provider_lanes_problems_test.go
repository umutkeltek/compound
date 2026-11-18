package hookplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/harness"
)

func writeLaneFile(t *testing.T, base, label, body string) string {
	t.Helper()
	path := filepath.Join(base, launcherProviderProfileRel(label), providerLaneSettingsFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func laneProblemFor(report HookInstallReport, label string) (HookLaneProblem, bool) {
	for _, p := range report.LaneProblems {
		if p.Label == label {
			return p, true
		}
	}
	return HookLaneProblem{}, false
}

// Every lane home records under a name that is its own: a launch profile name two homes
// share goes to neither, a home with no launch profile uses its own folder name, a gateway
// folder yields to the folder that owns the name, and nothing falls back to claude_code.
func TestProviderLaneNamesAreUniqueToTheirHome(t *testing.T) {
	base := t.TempDir()
	for _, label := range []string{"gpt-a", "gpt-b", "kimi", "deepseek-flash-nvidia", "gateway-deepseek-flash-nvidia", "gateway-unlisted-local"} {
		writeLaneFile(t, base, label, "{}")
	}
	writeJSONFile(t, filepath.Join(base, launcherLaunchProfilesRel()), map[string]any{
		"launch_profiles": map[string]any{
			"claude-gpt": map[string]any{"profile_label": "gpt-a"},
			"claude_gpt": map[string]any{"profile_label": "gpt-b"},
		},
	})
	lanes, err := DiscoverProviderLanes(base, harness.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	got := providerLaneByLabel(t, lanes)
	want := map[string]string{
		"gpt-a":                         "",
		"gpt-b":                         "",
		"kimi":                          "claude_kimi",
		"deepseek-flash-nvidia":         "claude_deepseek_flash_nvidia",
		"gateway-deepseek-flash-nvidia": "",
		"gateway-unlisted-local":        "",
	}
	owners := map[string]string{}
	for label, name := range want {
		lane := got[label]
		if lane.Harness != name {
			t.Errorf("%s = %q, want %q", label, lane.Harness, name)
		}
		if (name == "") != (lane.NotWired != "") {
			t.Errorf("%s: name %q with not-wired reason %q", label, lane.Harness, lane.NotWired)
		}
		if lane.Harness == "" {
			continue
		}
		if other, taken := owners[lane.Harness]; taken {
			t.Errorf("%s and %s both record as %s", other, label, lane.Harness)
		}
		owners[lane.Harness] = label
	}

	report, err := InstallHooks(base, providerLaneTestBinary, "global", []string{ProviderLanesHarness}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"gpt-a", "gpt-b", "gateway-deepseek-flash-nvidia", "gateway-unlisted-local"} {
		if p, ok := laneProblemFor(report, label); !ok || p.State != LaneStateNotWired {
			t.Errorf("%s must be reported not wired: %+v", label, report.LaneProblems)
		}
	}
	if len(report.Entries) != 2 || report.HasLaneErrors() {
		t.Fatalf("entries = %+v, problems = %+v, want kimi and deepseek-flash-nvidia only", report.Entries, report.LaneProblems)
	}
}

// One unreadable lane file is that lane's error row; every other harness and lane is still
// checked and installed, and the file is left as it was.
func TestABrokenLaneFileDoesNotStopTheOtherHarnesses(t *testing.T) {
	base := providerLaneFixture(t, true)
	broken := writeLaneFile(t, base, "kimi", "{not json")

	check, err := InstallHooks(base, providerLaneTestBinary, "global", nil, true)
	if err != nil {
		t.Fatalf("one broken lane failed the whole check: %v", err)
	}
	if p, ok := laneProblemFor(check, "kimi"); !ok || p.State != LaneStateError || p.ConfigPath != broken || !check.HasLaneErrors() {
		t.Fatalf("kimi problem = %+v, want an error row naming its file", check.LaneProblems)
	}
	hookInstallEntryForHarness(t, check, "claude-code")
	hookInstallEntryForHarness(t, check, "codex")
	hookInstallEntryForHarness(t, check, "claude_gpt")

	installed, err := InstallHooksWithRoster(base, providerLaneTestBinary, "global", []string{ProviderLanesHarness}, "", nil, false, nil)
	if err != nil {
		t.Fatalf("install stopped on the broken lane: %v", err)
	}
	if entry := hookInstallEntryForHarness(t, installed, "claude_gpt"); entry.Action != "installed" && entry.Action != "updated" {
		t.Fatalf("the other lanes must still be wired: %+v", entry)
	}
	if data, _ := os.ReadFile(broken); string(data) != "{not json" {
		t.Fatalf("the broken lane file was rewritten: %q", data)
	}

	removed, err := UninstallHooks(base, providerLaneTestBinary, "global", nil, "", true, nil)
	if err != nil {
		t.Fatalf("one broken lane failed uninstall: %v", err)
	}
	if p, ok := laneProblemFor(removed, "kimi"); !ok || p.State != LaneStateError {
		t.Fatalf("uninstall kimi problem = %+v", removed.LaneProblems)
	}
	if entry := hookInstallEntryForHarness(t, removed, "claude_gpt"); entry.Action != "would remove" {
		t.Fatalf("uninstall must still reach the other lanes: %+v", entry)
	}
}

// An unreadable launch profiles file is an error row, and the lanes are resolved by folder
// name alone instead of the run failing.
func TestUnreadableLaunchProfilesFallBackToFolderNames(t *testing.T) {
	base := providerLaneFixture(t, false)
	if err := os.WriteFile(filepath.Join(base, launcherLaunchProfilesRel()), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	lanes, err := DiscoverProviderLanes(base, harness.DefaultRegistry())
	if err == nil || !strings.Contains(err.Error(), "launch profiles") {
		t.Fatalf("err = %v, want the launch profiles named", err)
	}
	if got := providerLaneByLabel(t, lanes)["kimi"].Harness; got != "claude_kimi" {
		t.Fatalf("kimi by folder name = %q, want claude_kimi", got)
	}
	report, err := InstallHooks(base, providerLaneTestBinary, "global", nil, true)
	if err != nil {
		t.Fatalf("unreadable launch profiles failed the whole check: %v", err)
	}
	if !report.HasLaneErrors() || !strings.Contains(report.LaneProblems[0].Detail, "folder name") {
		t.Fatalf("problems = %+v, want an error row that says lanes were resolved by folder name", report.LaneProblems)
	}
	hookInstallEntryForHarness(t, report, "claude-code")
	hookInstallEntryForHarness(t, report, "claude_kimi")
}
