package harness

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWiringMapPrintsEveryFactWithItsOwnerPath(t *testing.T) {
	user, src := syntheticClaudeTree(t)
	homes := DiscoverClaudeHomes(src)
	out, err := WiringMap(DefaultRegistry(), "", homes, user, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"claude_code (active)",
		"~/.claude.json mcpServers - user scope; the owner list for the whole Claude family",
		"<home>/skills (one level)",
		"native seats: seat-a, seat-b",
		"model lanes: free-lane, gone-lane (missing), hooked-lane, tap-lane",
		"codex (active)",
		"~/.codex/config.toml [mcp_servers.<name>]",
		"~/.agents/skills (recursive)",
		"opencode (active)",
		"mcp.<name> and mcp.servers.<name>",
		"~/.config/opencode/plugins/compound.ts (plugin; proof: // compound-owned: opencode-plugin",
		"oh_my_pi (active)",
		"$CLAUDE_CONFIG_DIR/skills (one level)",
		"hermes (active)",
		"session-end-plugin, no session-start context",
		"deepseek_harness (dormant, last session 2026-08-23)",
		"Prove it on this machine: compound harness check",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("map is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, user) {
		t.Fatalf("the map must show home-relative paths, not the user's home:\n%s", out)
	}
}

func TestWiringMapFiltersAndRefusesUnknownHarness(t *testing.T) {
	out, err := WiringMap(DefaultRegistry(), "oh-my-pi", nil, "/Users/example", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "oh_my_pi (active)") || strings.Contains(out, "codex (") {
		t.Fatalf("--harness oh-my-pi must print only oh_my_pi:\n%s", out)
	}
	for _, bad := range []string{"cursor", "claude_minimax"} {
		if _, err := WiringMap(DefaultRegistry(), bad, nil, "/Users/example", false); err == nil || !strings.Contains(err.Error(), "claude_code, codex") {
			t.Fatalf("%s has no wiring facts; want an error naming the ones that do, got %v", bad, err)
		}
	}
}

func TestWiringMapJSONCarriesFactsAndHomes(t *testing.T) {
	user, src := syntheticClaudeTree(t)
	out, err := WiringMap(DefaultRegistry(), "claude", DiscoverClaudeHomes(src), user, true)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Schema    string `json:"schema_version"`
		Harnesses []struct {
			Name   string       `json:"name"`
			Wiring Wiring       `json:"wiring"`
			Homes  []ClaudeHome `json:"homes"`
		} `json:"harnesses"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("map --json is not JSON: %v\n%s", err, out)
	}
	if doc.Schema != WiringMapSchema || len(doc.Harnesses) != 1 || doc.Harnesses[0].Name != "claude_code" {
		t.Fatalf("doc = %+v", doc)
	}
	if len(doc.Harnesses[0].Homes) != 7 || doc.Harnesses[0].Wiring.Context.Marker != SessionStartHookMarker {
		t.Fatalf("claude_code entry = %+v", doc.Harnesses[0])
	}
}

func TestRunWiringCommandRefusesBadArguments(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, args := range [][]string{{"--harnes", "codex"}, {"--harness"}, {"stray"}} {
		var out, errOut bytes.Buffer
		if code := RunWiringCommand("map", args, nil, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage: compound harness map|check") {
			t.Fatalf("args %v: exit %d, stderr %q", args, code, errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	if code := RunWiringCommand("map", []string{"--harness", "codex"}, nil, &out, &errOut); code != 0 || !strings.HasPrefix(out.String(), "codex (active)") {
		t.Fatalf("map --harness codex: exit %d\n%s%s", code, out.String(), errOut.String())
	}
}
