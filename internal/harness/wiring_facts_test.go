package harness

import (
	"path/filepath"
	"strings"
	"testing"
)

// Every harness an operator runs carries wiring facts, and each fact is either
// a path the check can open or an explicit Unknown, never an empty guess.
func TestWiringFactsCoverEveryRunHarness(t *testing.T) {
	reg := DefaultRegistry()
	want := []string{"claude_code", "codex", "deepseek_harness", "hermes", "oh_my_pi", "opencode"}
	if got := reg.WiringNames(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("WiringNames() = %v, want %v", got, want)
	}
	for _, name := range want {
		w := reg.Harnesses[name].Wiring
		if w.Status != WiringActive && w.Status != WiringDormant {
			t.Errorf("%s status = %q", name, w.Status)
		}
		if len(w.Instructions) == 0 || strings.TrimSpace(w.Permissions) == "" || len(w.Listing) == 0 {
			t.Errorf("%s wiring leaves a fact empty instead of saying %q: %+v", name, Unknown, w)
		}
		if w.Context.Kind == "" || w.Context.LoadReport == "" {
			t.Errorf("%s context route is not described: %+v", name, w.Context)
		}
		if w.Context.Kind != ContextNone && (w.Context.Path == "" || w.Context.Marker == "") {
			t.Errorf("%s context route needs a file and the text that proves Compound is in it: %+v", name, w.Context)
		}
		for _, root := range w.SkillRoots {
			if root.Recursive != "yes" && root.Recursive != "no" && root.Recursive != Unknown {
				t.Errorf("%s skill root %s recursive = %q", name, root.Path, root.Recursive)
			}
		}
		for _, src := range w.MCP {
			switch src.Shape {
			case MCPShapeJSONServers, MCPShapeJSONMCP, MCPShapeTOMLServers, MCPShapeYAMLServers:
			default:
				t.Errorf("%s MCP source %s has unknown shape %q", name, src.Path, src.Shape)
			}
		}
		// An active harness that supports MCP must name the file it reads servers
		// from, unless it says it has none of its own.
		if w.Status == WiringActive && reg.Harnesses[name].Capabilities.MCP && len(w.MCP) == 0 {
			t.Errorf("%s supports MCP but names no MCP file", name)
		}
	}
	if reg.Harnesses["deepseek_harness"].Wiring.Status != WiringDormant {
		t.Fatal("the DeepSeek harness is dormant; the live check must not expect it wired")
	}
	if reg.Harnesses["opencode"].Wiring.MCP[0].Shape != MCPShapeJSONMCP {
		t.Fatal("OpenCode keeps two MCP layouts in one file; its shape must say so")
	}
	if got := reg.Harnesses["claude_code"].Wiring.MCP[0]; got.Path != "~/.claude.json" || !strings.Contains(got.Scope, "owner list") {
		t.Fatalf("the Claude family owner list must be ~/.claude.json, got %+v", got)
	}
}

func TestExpandWiringPathResolvesOnlyMachinePaths(t *testing.T) {
	user := "/Users/example"
	seat := filepath.Join(user, "seats", "one")
	cases := map[string]string{
		"~/.codex/config.toml":      filepath.Join(user, ".codex", "config.toml"),
		"<home>/settings.json":      filepath.Join(user, ".claude", "settings.json"),
		"$CLAUDE_CONFIG_DIR/skills": filepath.Join(user, ".claude", "skills"),
		"<repo>/AGENTS.md":          "",
		Unknown:                     "",
		"prompt-only: the task":     "",
	}
	for in, want := range cases {
		if got := ExpandWiringPath(in, user, ""); got != want {
			t.Errorf("ExpandWiringPath(%q) = %q, want %q", in, got, want)
		}
	}
	if got := ExpandWiringPath("<home>/.claude.json", user, seat); got != filepath.Join(seat, ".claude.json") {
		t.Fatalf("a seat home must replace <home>, got %q", got)
	}
}
