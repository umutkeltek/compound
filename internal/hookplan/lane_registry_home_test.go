package hookplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// adapters/_lib/lanes.json gives each shell lane a default profile folder; install-lane.sh
// installs hooks there and lane-launcher.sh starts Claude there. A lane whose own harness is a
// governed lane (lane zai with hook_harness claude-zai) must default to the folder the hook plan
// writes for that harness, or its installer and the hook plan maintain two different profiles.
func TestLaneRegistryDefaultsGovernedLanesToTheHookPlanHome(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "adapters", "_lib", "lanes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var registry struct {
		Lanes []struct {
			ID             string `json:"id"`
			ProfileDefault string `json:"profile_default"`
			HookHarness    string `json:"hook_harness"`
		} `json:"lanes"`
	}
	if err := json.Unmarshal(body, &registry); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, lane := range registry.Lanes {
		if lane.HookHarness == "" || lane.HookHarness != "claude-"+lane.ID {
			continue
		}
		label, governed := laneProfileLabel(lane.HookHarness)
		if !governed {
			continue
		}
		want := "${HOME}/" + filepath.ToSlash(launcherProviderProfileRel(label))
		if lane.ProfileDefault != want {
			t.Errorf("lane %s profile_default = %q, want the hook plan home %q", lane.ID, lane.ProfileDefault, want)
		}
		checked++
	}
	if checked < 3 {
		t.Fatalf("checked %d governed lanes in lanes.json, want at least zai, minimax and qwen", checked)
	}
}
