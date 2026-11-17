package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWiringFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// syntheticClaudeTree builds a user home with a default Claude home and a
// config tree: two signed-in seats, a lane the hook plan names, a lane only
// the tree holds, a lane only the lane registry names, a folder that is not a
// Claude home, and a hidden retired folder.
func syntheticClaudeTree(t *testing.T) (string, ClaudeHomeSources) {
	t.Helper()
	user := t.TempDir()
	tree := filepath.Join(user, "tool", "config", "claude")
	lanes := filepath.Join(tree, "lanes")
	writeWiringFile(t, filepath.Join(user, ".claude", "settings.json"), `{}`)
	writeWiringFile(t, filepath.Join(tree, "seat-a", ".claude.json"), `{"oauthAccount":{"accountUuid":"x"},"mcpServers":{}}`)
	writeWiringFile(t, filepath.Join(tree, "seat-a", "settings.json"), `{}`)
	writeWiringFile(t, filepath.Join(tree, "seat-b", ".claude.json"), `{"oauthAccount":{"accountUuid":"y"}}`)
	writeWiringFile(t, filepath.Join(tree, "tap-lane", "settings.json"), `{}`)
	writeWiringFile(t, filepath.Join(tree, "other-tool", "config.yml"), "x: 1\n")
	writeWiringFile(t, filepath.Join(tree, "signed-out", ".claude.json"), `{"oauthAccount":null}`)
	writeWiringFile(t, filepath.Join(lanes, "hooked-lane", "settings.json"), `{}`)
	writeWiringFile(t, filepath.Join(lanes, "free-lane", "settings.json"), `{}`)
	writeWiringFile(t, filepath.Join(lanes, ".retired", "old", "settings.json"), `{}`)
	if err := os.MkdirAll(filepath.Join(lanes, "empty-lane"), 0o755); err != nil {
		t.Fatal(err)
	}
	lanesFile := filepath.Join(user, "repo", "lanes.json")
	writeWiringFile(t, lanesFile, `{"schema_version":1,"lanes":[
	 {"id":"tap","family":"claude_code","profile_default":"${HOME}/tool/config/claude/tap-lane"},
	 {"id":"gone","family":"claude_code","profile_default":"${HOME}/tool/config/claude/gone-lane"},
	 {"id":"other","family":"codex","profile_default":"${HOME}/tool/config/codex/x"},
	 {"id":"native","family":"claude_code"}
	]}`)
	return user, ClaudeHomeSources{
		UserHome: user,
		LaneSettings: map[string]string{
			"claude_one":     filepath.Join(lanes, "hooked-lane", "settings.json"),
			"claude_one_alt": filepath.Join(lanes, "hooked-lane", "settings.json"),
		},
		LanesFile: lanesFile,
	}
}

func TestDiscoverClaudeHomesFindsSeatsAndLanesFromSeams(t *testing.T) {
	_, src := syntheticClaudeTree(t)
	homes := DiscoverClaudeHomes(src)
	got := map[string]ClaudeHome{}
	var order []string
	for _, h := range homes {
		got[h.Name] = h
		order = append(order, h.Kind+":"+h.Name)
	}
	want := "default:default,native:seat-a,native:seat-b,lane:free-lane,lane:gone-lane,lane:hooked-lane,lane:tap-lane"
	if strings.Join(order, ",") != want {
		t.Fatalf("homes = %s\nwant    %s", strings.Join(order, ","), want)
	}
	if h := got["hooked-lane"]; h.Source != "hook-plan" || h.HookHarness != "claude_one" {
		t.Fatalf("a lane the hook plan names must keep that seam and its first harness, got %+v", h)
	}
	if h := got["tap-lane"]; h.Source != "lane-registry" || h.Missing {
		t.Fatalf("tap-lane = %+v", h)
	}
	if h := got["gone-lane"]; !h.Missing {
		t.Fatalf("a lane registry home with no folder must be reported missing, got %+v", h)
	}
	for _, absent := range []string{"other-tool", "signed-out", "empty-lane", "old", ".retired", "x"} {
		if _, ok := got[absent]; ok {
			t.Fatalf("%s is not a Claude home and must not be listed: %v", absent, order)
		}
	}
}

func TestDiscoverClaudeHomesWithoutSeamsIsOnlyTheDefault(t *testing.T) {
	user := t.TempDir()
	homes := DiscoverClaudeHomes(ClaudeHomeSources{UserHome: user, LanesFile: filepath.Join(user, "absent.json")})
	if len(homes) != 1 || homes[0].Kind != ClaudeHomeDefault || homes[0].MCPFile(user) != filepath.Join(user, ".claude.json") {
		t.Fatalf("homes = %+v", homes)
	}
}

func TestDiscoverClaudeHomesReadsTheProcessConfigDir(t *testing.T) {
	user := t.TempDir()
	seat := filepath.Join(user, "elsewhere", "seat-z")
	writeWiringFile(t, filepath.Join(seat, ".claude.json"), `{"oauthAccount":{"accountUuid":"z"}}`)
	homes := DiscoverClaudeHomes(ClaudeHomeSources{UserHome: user, ConfigDir: seat})
	if len(homes) != 2 || homes[1].Name != "seat-z" || homes[1].Kind != ClaudeHomeNative || homes[1].Source != "env" {
		t.Fatalf("homes = %+v", homes)
	}
	if homes[1].MCPFile(user) != filepath.Join(seat, ".claude.json") {
		t.Fatalf("a non-default home reads its own .claude.json, got %s", homes[1].MCPFile(user))
	}
}

func TestDefaultClaudeHomeSourcesAsksTheHookPlanForEveryRegistryName(t *testing.T) {
	user := t.TempDir()
	t.Setenv("LANES_JSON", filepath.Join(user, "lanes.json"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	var asked []string
	src := DefaultClaudeHomeSources(user, DefaultRegistry(), func(base, name string) (string, bool) {
		asked = append(asked, name)
		if base != user {
			t.Fatalf("lane seam asked with base %q, want the user home", base)
		}
		if name == "claude_minimax" {
			return filepath.Join(base, "lanes", "m", "settings.json"), true
		}
		return "", false
	})
	if len(asked) != len(DefaultRegistry().Names()) {
		t.Fatalf("asked %d names, registry has %d", len(asked), len(DefaultRegistry().Names()))
	}
	if len(src.LaneSettings) != 1 || src.LanesFile != filepath.Join(user, "lanes.json") {
		t.Fatalf("sources = %+v", src)
	}
}
