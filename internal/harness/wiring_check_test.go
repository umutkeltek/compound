package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sessionStartHook = `{"hooks":{"SessionStart":[{"matcher":"*","hooks":[{"type":"command","command":"/usr/local/bin/compound hook session-start --live --harness claude_code"}]}]%s}}`

func claudeSettings(mode string, deny []string, preToolCommand string) string {
	pre := ""
	if preToolCommand != "" {
		pre = `,"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"` + preToolCommand + `"}]}]`
	}
	perm := `"permissions":{"defaultMode":"` + mode + `"`
	if deny != nil {
		b, _ := json.Marshal(deny)
		perm += `,"deny":` + string(b)
	}
	perm += `}`
	return strings.Replace(strings.Replace(sessionStartHook, "%s", pre, 1), `{"hooks"`, `{`+perm+`,"hooks"`, 1)
}

// wiringMachine is a synthetic user home with every harness wired, so each
// test breaks one thing and expects exactly that row to change.
type wiringMachine struct {
	user, tree string
	homes      []ClaudeHome
	probe      map[string]probeAnswer
}

type probeAnswer struct {
	out string
	err error
}

func newWiringMachine(t *testing.T) *wiringMachine {
	t.Helper()
	user := t.TempDir()
	m := &wiringMachine{user: user, tree: filepath.Join(user, "cfg", "claude"), probe: map[string]probeAnswer{}}
	w := func(rel, body string) { writeWiringFile(t, filepath.Join(user, filepath.FromSlash(rel)), body) }
	w(".claude.json", `{"mcpServers":{"compound":{"command":"compound"},"alpha":{"env":{"TOKEN":"sekret-1"}}}}`)
	w(".claude/settings.json", claudeSettings("bypassPermissions", []string{"Bash(rm -rf /)"}, "/usr/local/bin/compound hook pre-tool-use --live --harness claude_code --spool"))
	w(".codex/hooks.json", `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/usr/local/bin/compound hook session-start --live --harness codex"}]}]}}`)
	w(".codex/config.toml", "[mcp_servers.compound]\ncommand = \"compound\"\nenabled_tools = [\"x\"]\n[mcp_servers.alpha]\ncommand = \"a\"\n[mcp_servers.alpha.env]\nenabled = false\n[mcp_servers.off]\ncommand = \"o\"\nenabled = false\n")
	w(".config/opencode/plugins/compound.ts", OpenCodePluginMarker+"@1\nexport default {}\n")
	w(".config/opencode/opencode.json", `{"mcp":{"servers":{"compound":{"type":"local"},"beta":{"type":"local","enabled":false}},"alpha":{"type":"remote","url":"https://h.example/mcp?key=sekret-2"}}}`)
	w(".omp/agent/hooks/pre/compound.ts", OmpShimMarker+"@2\n")
	w(".hermes/plugins/compound_memory/__init__.py", "def register(ctx):\n    ctx.register_hook(\"on_session_end\", f)\n")
	w(".hermes/config.yaml", "plugins:\n  enabled:\n    - compound_memory\nmcp_servers:\n  compound:\n    command: compound\n  gamma:\n    command: g\n    enabled: false\n")
	w("skills/claude/SKILL.md", "---\nname: Browser\ndescription: \"One door: the CLI\"\n---\nbody\n")
	m.probe["codex features list"] = probeAnswer{out: "goals   stable  true\nhooks   stable  true\n"}
	m.probe["opencode api GET /api/plugin"] = probeAnswer{out: `{"data":[{"id":"opencode.agent","state":{"status":"active"}},{"id":"compound","source":{"type":"local"},"state":{"status":"active"}}]}`}
	m.homes = []ClaudeHome{{Name: ClaudeHomeDefault, Dir: filepath.Join(user, ".claude"), Kind: ClaudeHomeDefault, Source: "default"}}
	return m
}

func (m *wiringMachine) addHome(t *testing.T, name, kind, hookHarness, claudeJSON, settings string) string {
	t.Helper()
	dir := filepath.Join(m.tree, name)
	if claudeJSON != "" {
		writeWiringFile(t, filepath.Join(dir, ".claude.json"), claudeJSON)
	}
	if settings != "" {
		writeWiringFile(t, filepath.Join(dir, "settings.json"), settings)
	}
	m.homes = append(m.homes, ClaudeHome{Name: name, Dir: dir, Kind: kind, HookHarness: hookHarness, Source: "config-tree"})
	return dir
}

func (m *wiringMachine) check(t *testing.T, only string) []WiringRow {
	t.Helper()
	rows, err := CheckWiring(WiringCheck{
		Registry: DefaultRegistry(), UserHome: m.user, Homes: m.homes, Only: only,
		Probe: func(binary string, args ...string) ([]byte, error) {
			a, ok := m.probe[binary+" "+strings.Join(args, " ")]
			if !ok {
				return nil, errors.New("not on PATH")
			}
			return []byte(a.out), a.err
		},
		SkillAdapters: map[string]string{"claude_code": filepath.Join(m.user, "skills", "claude", "SKILL.md")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func findRow(t *testing.T, rows []WiringRow, harness, home, fact string) WiringRow {
	t.Helper()
	for _, r := range rows {
		if r.Harness == harness && r.Home == home && r.Fact == fact {
			return r
		}
	}
	t.Fatalf("no row %s/%s/%s in:\n%s", harness, home, fact, FormatWiringCheck(rows, ""))
	return WiringRow{}
}

func TestWiringCheckPassesAFullyWiredMachine(t *testing.T) {
	m := newWiringMachine(t)
	rows := m.check(t, "")
	if HasFailure(rows) {
		t.Fatalf("a fully wired machine must not fail:\n%s", FormatWiringCheck(rows, m.user))
	}
	for _, want := range []struct{ harness, home, fact string }{
		{"claude_code", ClaudeHomeDefault, FactContext}, {"claude_code", ClaudeHomeDefault, FactMCP},
		{"claude_code", ClaudeHomeDefault, FactPermissions}, {"claude_code", "", FactSkillAdapter},
		{"codex", "", FactContext}, {"codex", "", FactMCP},
		{"opencode", "", FactContext}, {"opencode", "", FactMCP},
		{"oh_my_pi", "", FactContext}, {"hermes", "", FactSessionEnd}, {"hermes", "", FactMCP},
	} {
		if r := findRow(t, rows, want.harness, want.home, want.fact); r.Status != WiringPass {
			t.Errorf("%+v = %s: %s", want, r.Status, r.Found)
		}
	}
	if r := findRow(t, rows, "deepseek_harness", "", FactStatus); r.Status != WiringInfo || !strings.Contains(r.Found, "dormant") {
		t.Fatalf("a dormant harness is reported, not checked: %+v", r)
	}
	if r := findRow(t, rows, "hermes", "", FactContext); r.Status != WiringInfo || !strings.Contains(r.Found, "session-end only") {
		t.Fatalf("hermes has no session-start route and must say so: %+v", r)
	}
	if r := findRow(t, rows, "claude_code", ClaudeHomeDefault, FactPermissions); !strings.Contains(r.Found, "pre-tool hook record-only") {
		t.Fatalf("permissions row must name the pre-tool hook posture: %+v", r)
	}
	if r := findRow(t, rows, "codex", "", FactMCP); r.Found != "alpha, compound (off: off)" {
		t.Fatalf("codex servers: enabled = false turns a server off, an env value named enabled does not: %q", r.Found)
	}
	if r := findRow(t, rows, "opencode", "", FactMCP); r.Found != "alpha, compound (off: beta)" {
		t.Fatalf("opencode must read mcp.<name> and mcp.servers.<name>, never report 'servers': %q", r.Found)
	}
	blob, _ := json.Marshal(rows)
	if strings.Contains(string(blob), "sekret") || strings.Contains(string(blob), "TOKEN") {
		t.Fatalf("a check row leaked a config value: %s", blob)
	}
}

func TestWiringCheckFailsSeatDriftButOnlyInformsOnLaneDrift(t *testing.T) {
	m := newWiringMachine(t)
	hooked := claudeSettings("bypassPermissions", []string{"x"}, "")
	seat := m.addHome(t, "seat-a", ClaudeHomeNative, "", `{"oauthAccount":{},"mcpServers":{"compound":{}}}`, hooked)
	m.addHome(t, "seat-b", ClaudeHomeNative, "", `{"oauthAccount":{},"mcpServers":{"compound":{},"alpha":{}}}`, hooked)
	m.addHome(t, "lane-a", ClaudeHomeLane, "", `{"mcpServers":{}}`, hooked)
	rows := m.check(t, "")
	r := findRow(t, rows, "claude_code", "seat-a", FactMCP)
	if r.Status != WiringFail || r.Path != filepath.Join(seat, ".claude.json") || !strings.Contains(r.Found, "missing alpha") || !strings.Contains(r.Fix, "next mirrored launch") {
		t.Fatalf("a seat that differs from the owner list must fail and name the seat file: %+v", r)
	}
	if r := findRow(t, rows, "claude_code", "seat-b", FactMCP); r.Status != WiringPass {
		t.Fatalf("a seat that matches passes: %+v", r)
	}
	if r := findRow(t, rows, "claude_code", "lane-a", FactMCP); r.Status != WiringInfo || !strings.Contains(r.Found, "missing alpha, compound") {
		t.Fatalf("a lane that differs is information, not failure: %+v", r)
	}
	text := FormatWiringCheck(rows, m.user)
	if !strings.Contains(text, "FAIL    claude_code [seat-a] mcp: missing alpha (owner ~/.claude.json)") || !strings.Contains(text, "file: ~/cfg/claude/seat-a/.claude.json") {
		t.Fatalf("text output must name the failing seat and its file:\n%s", text)
	}
}

func TestWiringCheckOwnerListMustCarryTheCompoundServer(t *testing.T) {
	m := newWiringMachine(t)
	writeWiringFile(t, filepath.Join(m.user, ".claude.json"), `{"mcpServers":{"alpha":{}}}`)
	writeWiringFile(t, filepath.Join(m.user, ".codex", "config.toml"), "[mcp_servers.compound]\ncommand = \"c\"\nenabled = false\n")
	rows := m.check(t, "")
	if r := findRow(t, rows, "claude_code", ClaudeHomeDefault, FactMCP); r.Status != WiringFail || !strings.Contains(r.Fix, "compound mcp-transport") {
		t.Fatalf("the family owner list without compound must fail: %+v", r)
	}
	if r := findRow(t, rows, "codex", "", FactMCP); r.Status != WiringFail || !strings.Contains(r.Found, "off: compound") {
		t.Fatalf("a compound server turned off is not on: %+v", r)
	}
}

func TestWiringCheckFailsBypassWithoutDenyAndReadsHookPosture(t *testing.T) {
	m := newWiringMachine(t)
	m.addHome(t, "lane-open", ClaudeHomeLane, "", `{}`, claudeSettings("bypassPermissions", nil, ""))
	m.addHome(t, "lane-empty", ClaudeHomeLane, "", `{}`, claudeSettings("bypassPermissions", []string{}, "c hook pre-tool-use --live --harness x --enforce"))
	m.addHome(t, "lane-asks", ClaudeHomeLane, "", `{}`, claudeSettings("default", nil, "c hook pre-tool-use --live --harness x --enforce-governed"))
	rows := m.check(t, "claude")
	for _, home := range []string{"lane-open", "lane-empty"} {
		if r := findRow(t, rows, "claude_code", home, FactPermissions); r.Status != WiringFail || !strings.Contains(r.Found, "no deny list") {
			t.Fatalf("%s runs in bypass mode with no deny list and must fail: %+v", home, r)
		}
	}
	if r := findRow(t, rows, "claude_code", "lane-open", FactPermissions); !strings.Contains(r.Found, "pre-tool hook absent") {
		t.Fatalf("hook posture absent: %+v", r)
	}
	if r := findRow(t, rows, "claude_code", "lane-empty", FactPermissions); !strings.HasSuffix(r.Found, "pre-tool hook blocking") {
		t.Fatalf("hook posture blocking: %+v", r)
	}
	if r := findRow(t, rows, "claude_code", "lane-asks", FactPermissions); r.Status != WiringPass || !strings.Contains(r.Found, "blocking in governed runs") {
		t.Fatalf("a home that is not in bypass mode passes without a deny list: %+v", r)
	}
}

func TestWiringCheckContextNeedsTheBindingNotJustTheFile(t *testing.T) {
	m := newWiringMachine(t)
	plain := `{"permissions":{"defaultMode":"default"},"hooks":{"SessionStart":[{"hooks":[{"command":"other-tool start"}]}]}}`
	writeWiringFile(t, filepath.Join(m.user, ".claude", "settings.json"), plain)
	m.addHome(t, "seat-a", ClaudeHomeNative, "", `{"oauthAccount":{}}`, plain)
	m.addHome(t, "lane-hooked", ClaudeHomeLane, "claude_one", `{}`, plain)
	m.addHome(t, "lane-free", ClaudeHomeLane, "", `{}`, plain)
	writeWiringFile(t, filepath.Join(m.user, ".codex", "hooks.json"), `{"hooks":{"SessionStart":[]}}`)
	writeWiringFile(t, filepath.Join(m.user, ".omp", "agent", "hooks", "pre", "compound.ts"), "export default {}\n")
	rows := m.check(t, "")
	cases := []struct {
		harness, home, status, fix string
	}{
		{"claude_code", ClaudeHomeDefault, WiringFail, "compound init --harness claude-code"},
		{"claude_code", "seat-a", WiringFail, "next mirrored launch"},
		{"claude_code", "lane-hooked", WiringFail, "compound hook install --harness claude-one"},
		{"claude_code", "lane-free", WiringInfo, ""},
		{"codex", "", WiringFail, "compound init --harness codex"},
		{"oh_my_pi", "", WiringFail, "compound init --harness oh-my-pi"},
	}
	for _, c := range cases {
		r := findRow(t, rows, c.harness, c.home, FactContext)
		if r.Status != c.status || !strings.Contains(r.Fix, c.fix) {
			t.Errorf("%s/%s context = %s %q fix %q, want %s fix containing %q", c.harness, c.home, r.Status, r.Found, r.Fix, c.status, c.fix)
		}
	}
}

func TestWiringCheckAsksTheHarnessWhetherItLoadedTheRoute(t *testing.T) {
	cases := []struct {
		name, harness, key string
		answer             probeAnswer
		status, found      string
	}{
		{"codex hooks off", "codex", "codex features list", probeAnswer{out: "hooks stable false\n"}, WiringFail, "hooks false"},
		{"codex silent", "codex", "codex features list", probeAnswer{out: ""}, WiringUnknownRow, "did not confirm"},
		{"codex absent", "codex", "", probeAnswer{}, WiringUnknownRow, "not on PATH"},
		{"opencode plugin failed", "opencode", "opencode api GET /api/plugin", probeAnswer{out: `{"data":[{"id":"compound","state":{"status":"failed","error":"Plugin must export default definition"}}]}`}, WiringFail, "failed Plugin must export default definition"},
		{"opencode plugin missing", "opencode", "opencode api GET /api/plugin", probeAnswer{out: `{"data":[{"id":"opencode.agent","state":{"status":"active"}}]}`}, WiringFail, "does not list it"},
		{"opencode not answering", "opencode", "opencode api GET /api/plugin", probeAnswer{out: "<!doctype html>", err: errors.New("timeout")}, WiringUnknownRow, "did not answer"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newWiringMachine(t)
			for key := range m.probe {
				if strings.HasPrefix(key, c.harness) {
					delete(m.probe, key)
				}
			}
			if c.key != "" {
				m.probe[c.key] = c.answer
			}
			r := findRow(t, m.check(t, c.harness), c.harness, "", FactContext)
			if r.Status != c.status || !strings.Contains(r.Found, c.found) {
				t.Fatalf("context = %s %q, want %s containing %q", r.Status, r.Found, c.status, c.found)
			}
		})
	}
	// Without a probe the route is unproven: never PASS on the file alone.
	m := newWiringMachine(t)
	rows, err := CheckWiring(WiringCheck{Registry: DefaultRegistry(), UserHome: m.user, Only: "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	if r := findRow(t, rows, "opencode", "", FactContext); r.Status != WiringUnknownRow {
		t.Fatalf("an unasked plugin route must stay UNKNOWN, got %+v", r)
	}
}

func TestWiringCheckHermesSessionEndPluginMustBeEnabled(t *testing.T) {
	m := newWiringMachine(t)
	writeWiringFile(t, filepath.Join(m.user, ".hermes", "config.yaml"), "plugins:\n  enabled: [other]\nmcp_servers:\n  gamma: {command: g}\n")
	rows := m.check(t, "hermes")
	if r := findRow(t, rows, "hermes", "", FactSessionEnd); r.Status != WiringFail || !strings.Contains(r.Fix, "plugins.enabled") {
		t.Fatalf("an installed but disabled plugin fails: %+v", r)
	}
	if r := findRow(t, rows, "hermes", "", FactMCP); r.Status != WiringFail {
		t.Fatalf("hermes without the compound server fails: %+v", r)
	}
	if err := os.Remove(filepath.Join(m.user, ".hermes", "plugins", "compound_memory", "__init__.py")); err != nil {
		t.Fatal(err)
	}
	if r := findRow(t, m.check(t, "hermes"), "hermes", "", FactSessionEnd); r.Status != WiringFail || !strings.Contains(r.Found, "not installed") {
		t.Fatalf("a missing plugin fails: %+v", r)
	}
}

func TestWiringCheckSkillAdapterFrontmatterMustBeStrictYAML(t *testing.T) {
	m := newWiringMachine(t)
	writeWiringFile(t, filepath.Join(m.user, "skills", "claude", "SKILL.md"), "---\nname: Browser\ndescription: One door: the CLI\n---\nbody\n")
	r := findRow(t, m.check(t, "claude_code"), "claude_code", "", FactSkillAdapter)
	if r.Status != WiringFail || !strings.Contains(r.Found, "mapping values are not allowed") || !strings.Contains(r.Fix, "skill-adapter --skill Browser") {
		t.Fatalf("an unquoted colon in a plain scalar is invalid YAML and must fail: %+v", r)
	}
	if err := os.Remove(filepath.Join(m.user, "skills", "claude", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if r := findRow(t, m.check(t, "claude_code"), "claude_code", "", FactSkillAdapter); r.Status != WiringInfo {
		t.Fatalf("an adapter not rendered here is information: %+v", r)
	}
}

func TestValidateSkillFrontmatter(t *testing.T) {
	good := []string{
		"---\nname: a\ndescription: b\n---\nbody",
		"---\nname: a\ndescription: \"x: y\"\n---\n",
		"---\nname: a\ndescription: >-\n  x: y\n---",
	}
	for _, s := range good {
		if err := ValidateSkillFrontmatter([]byte(s)); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	bad := map[string]string{
		"name: a\n":                                  "does not open",
		"---\nname: a\ndescription: b\n":             "no closing",
		"---\nname: a\ndescription: x: y\n---\n":     "mapping values",
		"---\nname: a\n---\n":                        "description",
		"---\nname: [a]\ndescription: b\n---\n":      "name",
		"---\n- a\n---\n":                            "cannot unmarshal",
		"---\nname: a\ndescription: \"\"\n---\nbody": "description",
	}
	for s, want := range bad {
		if err := ValidateSkillFrontmatter([]byte(s)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want one containing %q", s, err, want)
		}
	}
}

func TestWiringCheckParityNamesServersOneFamilyLacks(t *testing.T) {
	m := newWiringMachine(t)
	rows := m.check(t, "")
	var parity []WiringRow
	for _, r := range rows {
		if r.Fact == FactMCPParity {
			parity = append(parity, r)
		}
	}
	if len(parity) != 1 || parity[0].Status != WiringInfo || parity[0].Harness != "claude_code+codex+opencode" ||
		!strings.Contains(parity[0].Found, "alpha is absent from hermes (~/.hermes/config.yaml)") {
		t.Fatalf("parity rows = %+v", parity)
	}
	if only := m.check(t, "codex"); len(only) == 0 || strings.Contains(FormatWiringCheck(only, m.user), FactMCPParity) {
		t.Fatal("a one-harness check compares no families")
	}
}

func TestWiringCheckRefusesAHarnessWithoutFacts(t *testing.T) {
	_, err := CheckWiring(WiringCheck{Registry: DefaultRegistry(), UserHome: "/Users/example", Only: "claude_minimax"})
	if err == nil || !strings.Contains(err.Error(), "no wiring facts") {
		t.Fatalf("err = %v", err)
	}
}

// The CLI exits 1 on any FAIL and 0 otherwise; nothing on PATH answers here,
// so the harness-reported routes stay UNKNOWN rather than PASS.
func TestRunWiringCheckExitCodes(t *testing.T) {
	m := newWiringMachine(t)
	t.Setenv("HOME", m.user)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("LANES_JSON", filepath.Join(m.user, "absent.json"))
	var out, errOut bytes.Buffer
	code := RunWiringCommand("check", []string{"--harness", "opencode"}, nil, &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "UNKNOWN opencode context") {
		t.Fatalf("exit %d\n%s%s", code, out.String(), errOut.String())
	}
	writeWiringFile(t, filepath.Join(m.user, ".config", "opencode", "opencode.json"), `{"mcp":{}}`)
	out.Reset()
	code = RunWiringCommand("check", []string{"--harness", "opencode", "--json"}, nil, &out, &errOut)
	var doc struct {
		Schema string      `json:"schema_version"`
		Failed bool        `json:"failed"`
		Rows   []WiringRow `json:"rows"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || code != 1 || !doc.Failed || doc.Schema != WiringCheckSchema {
		t.Fatalf("a FAIL must exit 1 with failed=true (exit %d, err %v):\n%s", code, err, out.String())
	}
}

func TestReadMCPServersShapes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	cases := []struct {
		shape, body, names, disabled, note string
	}{
		{MCPShapeJSONServers, `{"mcpServers":{"b":{},"a":{},"../x":{}},"projects":{"/p":{"mcpServers":{"c":{}}}}}`, "a,b", "", "1 project-scoped"},
		{MCPShapeJSONMCP, `{"mcp":{"servers":{"a":{"type":"local"},"b":{"disabled":true}},"c":{"type":"local","enabled":false},"d":{}}}`, "a,d", "b,c", ""},
		// A server literally named "servers" in the older layout is still a server.
		{MCPShapeJSONMCP, `{"mcp":{"servers":{"type":"local","command":["x"]}}}`, "servers", "", ""},
		{MCPShapeTOMLServers, "[mcp_servers.a]\nenabled = true\n[mcp_servers.\"b-1\"]\nenabled = false # off\n[other]\nenabled = false\n", "a", "b-1", ""},
		{MCPShapeYAMLServers, "mcp_servers:\n  a: {command: x}\n  b:\n    enabled: false\n  c: null\n", "a,c", "b", ""},
		{MCPShapeYAMLServers, "other: 1\n", "", "", ""},
	}
	for i, c := range cases {
		writeWiringFile(t, path, c.body)
		got, err := ReadMCPServers(c.shape, path)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if strings.Join(got.Names, ",") != c.names || strings.Join(got.Disabled, ",") != c.disabled || !strings.Contains(got.Note, c.note) {
			t.Errorf("case %d (%s): got %+v, want names %q disabled %q", i, c.shape, got, c.names, c.disabled)
		}
	}
	if _, err := ReadMCPServers(MCPShapeJSONServers, filepath.Join(dir, "absent")); !os.IsNotExist(err) {
		t.Fatalf("a missing file must read as not-exist, got %v", err)
	}
	writeWiringFile(t, path, `{"mcpServers": [`)
	if _, err := ReadMCPServers(MCPShapeJSONServers, path); err == nil {
		t.Fatal("a malformed file is an error, not an empty list")
	}
	if _, err := ReadMCPServers("xml", path); err == nil {
		t.Fatal("an unknown shape is an error")
	}
}
