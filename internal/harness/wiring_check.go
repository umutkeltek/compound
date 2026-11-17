package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// `compound harness check` proves the wiring facts against the machine. A
// file that merely exists never passes: the file must hold Compound's binding
// and, where the harness can report it, the harness must say it loaded it.
// Every row names the file to fix.

// Row statuses.
const (
	WiringPass        = "PASS"
	WiringFail        = "FAIL"
	WiringInfo        = "INFO"
	WiringUnknownRow  = "UNKNOWN"
	WiringCheckSchema = "compound.harness_wiring_check.v0.1"
)

// Row facts.
const (
	FactContext      = "context"
	FactSessionEnd   = "session end"
	FactMCP          = "mcp"
	FactMCPParity    = "mcp parity"
	FactPermissions  = "permissions"
	FactHome         = "home"
	FactStatus       = "status"
	FactSkillAdapter = "skill adapter"
)

// compoundServer is the MCP server every active MCP harness is expected to run.
const compoundServer = "compound"

// renderedAdapterSkill is the one skill the adapter door renders per harness.
const renderedAdapterSkill = "Browser"

// WiringRow is one live finding.
type WiringRow struct {
	Harness string `json:"harness"`
	Home    string `json:"home,omitempty"`
	Fact    string `json:"fact"`
	Status  string `json:"status"`
	Found   string `json:"found"`
	Path    string `json:"path,omitempty"`
	Fix     string `json:"fix,omitempty"`
}

// WiringProbe runs a harness's own report command and returns its stdout.
type WiringProbe func(binary string, args ...string) ([]byte, error)

// ExecWiringProbe runs the command found on PATH, in dir, bounded by timeout.
// It never reads stdin.
func ExecWiringProbe(timeout time.Duration, dir string) WiringProbe {
	return func(binary string, args ...string) ([]byte, error) {
		path, err := exec.LookPath(binary)
		if err != nil {
			return nil, fmt.Errorf("%s is not on PATH", binary)
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, path, args...)
		if dir != "" {
			cmd.Dir = dir
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if ctx.Err() != nil {
			return out, fmt.Errorf("%s %s did not answer within %s", binary, strings.Join(args, " "), timeout)
		}
		if err != nil {
			return out, fmt.Errorf("%s %s: %v %s", binary, strings.Join(args, " "), err, firstLine(stderr.String()))
		}
		return out, nil
	}
}

// WiringCheck is the input of one check run.
type WiringCheck struct {
	Registry Registry
	UserHome string
	// Homes are the Claude homes found at run time; the default home is checked
	// even when it is not in the list.
	Homes []ClaudeHome
	// Only limits the run to one harness (any spelling Registry.Get accepts).
	Only string
	// Probe asks a harness what it loaded; nil never asks, which leaves those
	// rows UNKNOWN rather than green.
	Probe WiringProbe
	// SkillAdapters maps a harness to its rendered skill adapter file; nil
	// skips the adapter rows.
	SkillAdapters map[string]string
}

// RenderedSkillAdapterPaths maps each rendered skill adapter to its file.
func RenderedSkillAdapterPaths() map[string]string {
	out := map[string]string{}
	for _, a := range DefaultSkillAdapters() {
		out[a.ID] = a.SkillPath(renderedAdapterSkill)
	}
	return out
}

// CheckWiring runs every check and returns the rows, grouped by harness in
// registry order.
func CheckWiring(c WiringCheck) ([]WiringRow, error) {
	names := c.Registry.WiringNames()
	if only := strings.TrimSpace(c.Only); only != "" {
		h, ok := c.Registry.Get(only)
		if !ok || h.Wiring == nil {
			return nil, fmt.Errorf("no wiring facts for harness %q (have: %s)", only, strings.Join(names, ", "))
		}
		names = []string{h.Name}
	}
	var rows []WiringRow
	families := map[string]familyServers{}
	for _, name := range names {
		h := c.Registry.Harnesses[name]
		w := h.Wiring
		if w.Status != WiringActive {
			rows = append(rows, WiringRow{Harness: name, Fact: FactStatus, Status: WiringInfo,
				Found: strings.TrimSpace(w.Status + "; " + w.StatusNote + "; not checked")})
			continue
		}
		switch w.Context.Kind {
		case ContextSettingsHooks:
			if name == "claude_code" {
				rows = append(rows, c.claudeFamilyRows(w, families)...)
				continue
			}
			rows = append(rows, c.hookFileContextRow(name, w))
		case ContextPlugin, ContextHookShim:
			rows = append(rows, c.ownedFileContextRow(name, w))
		case ContextSessionEndPlugin:
			rows = append(rows, WiringRow{Harness: name, Fact: FactContext, Status: WiringInfo,
				Found: "no session-start context route: the Compound plugin is session-end only",
				Path:  ExpandWiringPath(w.Context.Path, c.UserHome, "")})
			rows = append(rows, c.sessionEndPluginRow(name, w))
		}
		if name != "claude_code" {
			rows = append(rows, c.mcpRows(name, h, families)...)
		}
		if adapter, ok := c.SkillAdapters[name]; ok {
			rows = append(rows, skillAdapterRow(name, adapter))
		}
	}
	if c.Only == "" {
		rows = append(rows, parityRows(families, c.UserHome)...)
	}
	return rows, nil
}

// HasFailure reports whether any row failed.
func HasFailure(rows []WiringRow) bool {
	for _, r := range rows {
		if r.Status == WiringFail {
			return true
		}
	}
	return false
}

type familyServers struct {
	path  string
	names map[string]bool
}

// claudeFamilyRows checks the default home, every native seat and every lane
// against the family owner list.
func (c WiringCheck) claudeFamilyRows(w *Wiring, families map[string]familyServers) []WiringRow {
	const name = "claude_code"
	homes := c.Homes
	hasDefault := false
	for _, h := range homes {
		hasDefault = hasDefault || h.Kind == ClaudeHomeDefault
	}
	if !hasDefault {
		homes = append([]ClaudeHome{{Name: ClaudeHomeDefault, Dir: filepath.Join(c.UserHome, ".claude"), Kind: ClaudeHomeDefault, Source: "default"}}, homes...)
	}
	ownerPath := ExpandWiringPath(w.MCP[0].Path, c.UserHome, "")
	owner, ownerErr := ReadMCPServers(w.MCP[0].Shape, ownerPath)
	ownerSet := map[string]bool{}
	for _, n := range owner.Names {
		ownerSet[n] = true
	}
	if ownerErr == nil {
		families[name] = familyServers{path: ownerPath, names: ownerSet}
	}
	var rows []WiringRow
	for _, home := range homes {
		if home.Missing {
			rows = append(rows, WiringRow{Harness: name, Home: home.Name, Fact: FactHome, Status: WiringInfo,
				Found: "named by the " + home.Source + " but no folder exists", Path: home.Dir})
			continue
		}
		rows = append(rows, c.claudeContextRow(w, home))
		if home.Kind == ClaudeHomeDefault {
			rows = append(rows, ownerListRow(ownerPath, owner, ownerErr))
		} else {
			rows = append(rows, claudeHomeMCPRow(home, c.UserHome, w.MCP[0].Shape, ownerPath, owner, ownerErr))
		}
		rows = append(rows, claudePermissionsRow(home, ExpandWiringPath(w.Context.Path, c.UserHome, home.Dir)))
	}
	if adapter, ok := c.SkillAdapters[name]; ok {
		rows = append(rows, skillAdapterRow(name, adapter))
	}
	return rows
}

func (c WiringCheck) claudeContextRow(w *Wiring, home ClaudeHome) WiringRow {
	path := ExpandWiringPath(w.Context.Path, c.UserHome, home.Dir)
	row := WiringRow{Harness: "claude_code", Home: home.Name, Fact: FactContext, Path: path}
	commands, err := settingsHookCommands(path, "SessionStart")
	wired := err == nil && containsMarker(commands, w.Context.Marker)
	switch {
	case wired:
		row.Status, row.Found = WiringPass, "SessionStart runs the Compound hook"
		return row
	case err != nil && os.IsNotExist(err):
		row.Found = "no settings.json"
	case err != nil:
		row.Found = "settings.json unreadable: " + err.Error()
	default:
		row.Found = "SessionStart does not run the Compound hook"
	}
	switch {
	case home.Kind == ClaudeHomeDefault:
		row.Status, row.Fix = WiringFail, "compound init --harness claude-code"
	case home.Kind == ClaudeHomeNative:
		row.Status, row.Fix = WiringFail, "the seat's settings converge at its next mirrored launch; if they do not, check the default home first"
	case home.HookHarness != "":
		row.Status, row.Fix = WiringFail, "compound hook install --harness "+strings.ReplaceAll(home.HookHarness, "_", "-")
	default:
		row.Status = WiringInfo
		row.Found += " (lane not enrolled in the hook plan)"
	}
	return row
}

func ownerListRow(path string, owner MCPServers, err error) WiringRow {
	row := WiringRow{Harness: "claude_code", Home: ClaudeHomeDefault, Fact: FactMCP, Path: path}
	switch {
	case err != nil:
		row.Status, row.Found = WiringFail, "family owner list unreadable: "+err.Error()
	case !hasName(owner.Names, compoundServer):
		row.Status, row.Found = WiringFail, "family owner list lacks the compound server: "+listOrNone(owner.Names)
		row.Fix = "add a compound server (command: compound mcp-transport) to mcpServers in this file"
	default:
		row.Status, row.Found = WiringPass, "family owner list: "+listOrNone(owner.Names)
	}
	return row
}

func claudeHomeMCPRow(home ClaudeHome, userHome, shape, ownerPath string, owner MCPServers, ownerErr error) WiringRow {
	path := home.MCPFile(userHome)
	row := WiringRow{Harness: "claude_code", Home: home.Name, Fact: FactMCP, Path: path}
	if ownerErr != nil {
		row.Status, row.Found = WiringUnknownRow, "cannot compare: the family owner list is unreadable"
		return row
	}
	got, err := ReadMCPServers(shape, path)
	if err != nil {
		got = MCPServers{}
	}
	missing, extra := diffNames(owner.Names, got.Names)
	if err == nil && len(missing) == 0 && len(extra) == 0 {
		row.Status, row.Found = WiringPass, "matches the family owner list"
		return row
	}
	var parts []string
	if err != nil {
		parts = append(parts, "no readable .claude.json")
	}
	if len(missing) > 0 {
		parts = append(parts, "missing "+strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		parts = append(parts, "extra "+strings.Join(extra, ", "))
	}
	row.Found = strings.Join(parts, "; ") + " (owner " + displayHomePath(ownerPath, userHome) + ")"
	if home.Kind == ClaudeHomeNative {
		row.Status = WiringFail
		row.Fix = "converges at this seat's next mirrored launch, which copies the owner's mcpServers"
		return row
	}
	row.Status = WiringInfo
	row.Found += "; lanes keep their own pinned settings"
	return row
}

// claudePermissionsRow fails a home that runs in bypass mode with no deny
// list, and says what its pre-tool hook does.
func claudePermissionsRow(home ClaudeHome, settingsPath string) WiringRow {
	row := WiringRow{Harness: "claude_code", Home: home.Name, Fact: FactPermissions, Path: settingsPath}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		row.Status, row.Found = WiringInfo, "no settings.json; Claude defaults apply"
		return row
	}
	var settings struct {
		Permissions struct {
			DefaultMode string   `json:"defaultMode"`
			Deny        []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		row.Status, row.Found = WiringFail, "settings.json unreadable: "+err.Error()
		return row
	}
	commands, _ := settingsHookCommands(settingsPath, "PreToolUse")
	hook := preToolHookPosture(commands)
	mode := firstNonEmptyString(settings.Permissions.DefaultMode, "default")
	deny := "deny list present"
	if len(settings.Permissions.Deny) == 0 {
		deny = "no deny list"
	}
	row.Found = "mode " + mode + ", " + deny + ", pre-tool hook " + hook
	if mode == "bypassPermissions" && len(settings.Permissions.Deny) == 0 {
		row.Status = WiringFail
		row.Fix = "add permissions.deny to this settings.json, or stop launching this home in bypass mode"
		return row
	}
	row.Status = WiringPass
	return row
}

// preToolHookPosture reads what the Compound PreToolUse command does.
func preToolHookPosture(commands []string) string {
	posture := "absent"
	for _, cmd := range commands {
		if !strings.Contains(cmd, " hook pre-tool-use ") && !strings.HasSuffix(cmd, " hook pre-tool-use") {
			continue
		}
		switch {
		case strings.Contains(cmd, "--enforce-governed"):
			return "blocking in governed runs"
		case strings.Contains(cmd, "--enforce"):
			return "blocking"
		default:
			posture = "record-only"
		}
	}
	return posture
}

// hookFileContextRow checks a harness whose hooks live in a settings-style
// JSON file and asks the harness whether its hook support is on.
func (c WiringCheck) hookFileContextRow(name string, w *Wiring) WiringRow {
	path := ExpandWiringPath(w.Context.Path, c.UserHome, "")
	row := WiringRow{Harness: name, Fact: FactContext, Path: path}
	commands, err := settingsHookCommands(path, "SessionStart")
	switch {
	case err != nil && os.IsNotExist(err):
		row.Status, row.Found = WiringFail, "no "+filepath.Base(path)
		row.Fix = "compound init --harness " + name
		return row
	case err != nil:
		row.Status, row.Found = WiringFail, filepath.Base(path)+" unreadable: "+err.Error()
		return row
	case !containsMarker(commands, w.Context.Marker):
		row.Status, row.Found = WiringFail, "SessionStart does not run the Compound hook"
		row.Fix = "compound init --harness " + name
		return row
	}
	if name != "codex" {
		row.Status, row.Found = WiringPass, "SessionStart runs the Compound hook"
		return row
	}
	if c.Probe == nil {
		row.Status, row.Found = WiringUnknownRow, "SessionStart runs the Compound hook; Codex was not asked whether hooks are on"
		return row
	}
	out, err := c.Probe("codex", "features", "list")
	state := codexFeature(out, "hooks")
	switch {
	case state == "true":
		row.Status, row.Found = WiringPass, "SessionStart runs the Compound hook; codex features: hooks true"
	case state != "":
		row.Status, row.Found = WiringFail, "SessionStart runs the Compound hook, but codex features: hooks "+state
		row.Fix = "set hooks = true under [features] in ~/.codex/config.toml"
	default:
		reason := "it did not list the hooks feature"
		if err != nil {
			reason = err.Error()
		}
		row.Status, row.Found = WiringUnknownRow, "SessionStart runs the Compound hook; Codex did not confirm hooks are on: "+reason
	}
	return row
}

// codexFeature reads one feature's state from `codex features list`.
func codexFeature(out []byte, feature string) string {
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == feature {
			return fields[len(fields)-1]
		}
	}
	return ""
}

// ownedFileContextRow checks a plugin or shim file Compound owns and, for
// OpenCode, asks the harness whether it loaded it.
func (c WiringCheck) ownedFileContextRow(name string, w *Wiring) WiringRow {
	path := ExpandWiringPath(w.Context.Path, c.UserHome, "")
	row := WiringRow{Harness: name, Fact: FactContext, Path: path}
	data, err := os.ReadFile(path)
	installLabel := strings.ReplaceAll(name, "_", "-")
	switch {
	case err != nil && os.IsNotExist(err):
		row.Status, row.Found, row.Fix = WiringFail, "no "+w.Context.Kind+" file", "compound init --harness "+installLabel
		return row
	case err != nil:
		row.Status, row.Found = WiringFail, w.Context.Kind+" unreadable: "+err.Error()
		return row
	case !strings.Contains(string(data), w.Context.Marker):
		row.Status, row.Found = WiringFail, "the "+w.Context.Kind+" file is not Compound's"
		row.Fix = "move the file aside, then compound init --harness " + installLabel
		return row
	}
	if name != "opencode" {
		row.Status, row.Found = WiringPass, "Compound's "+w.Context.Kind+" is installed; the harness cannot report whether it loaded it"
		return row
	}
	if c.Probe == nil {
		row.Status, row.Found = WiringUnknownRow, "Compound's plugin is installed; OpenCode was not asked whether it loaded it"
		return row
	}
	out, err := c.Probe("opencode", "api", "GET", "/api/plugin")
	status, detail, known := openCodePluginState(out, path)
	switch {
	case err != nil && !known:
		row.Status, row.Found = WiringUnknownRow, "Compound's plugin is installed; OpenCode did not answer: "+err.Error()
	case !known:
		row.Status, row.Found = WiringFail, "Compound's plugin is installed, but OpenCode does not list it"
		row.Fix = "opencode service restart, then compound harness check --harness opencode"
	case status == "active":
		row.Status, row.Found = WiringPass, "OpenCode reports the Compound plugin active"
	default:
		row.Status = WiringFail
		row.Found = strings.TrimSpace("OpenCode reports the Compound plugin " + status + " " + detail)
		row.Fix = "read the plugin error above, fix " + filepath.Base(path) + " (or reinstall: compound init --harness opencode), then opencode service restart"
	}
	return row
}

// openCodePluginState finds the Compound plugin in `opencode api GET
// /api/plugin` output, by id or by its source path.
func openCodePluginState(out []byte, pluginPath string) (status, detail string, known bool) {
	var doc struct {
		Data []struct {
			ID     string `json:"id"`
			Source struct {
				Path string `json:"path"`
			} `json:"source"`
			State map[string]any `json:"state"`
		} `json:"data"`
	}
	if json.Unmarshal(out, &doc) != nil {
		return "", "", false
	}
	for _, p := range doc.Data {
		if p.ID != compoundServer && filepath.Clean(p.Source.Path) != filepath.Clean(pluginPath) {
			continue
		}
		status, _ = p.State["status"].(string)
		for _, key := range []string{"error", "message", "reason"} {
			if v, ok := p.State[key]; ok {
				detail = firstLine(fmt.Sprint(v))
				break
			}
		}
		return firstNonEmptyString(status, Unknown), detail, true
	}
	return "", "", false
}

// sessionEndPluginRow checks Hermes's session-end plugin: the file holds the
// on_session_end registration and the config turns the plugin on.
func (c WiringCheck) sessionEndPluginRow(name string, w *Wiring) WiringRow {
	path := ExpandWiringPath(w.Context.Path, c.UserHome, "")
	row := WiringRow{Harness: name, Fact: FactSessionEnd, Path: path}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), w.Context.Marker) {
		row.Status, row.Found = WiringFail, "the Compound session-end plugin is not installed"
		row.Fix = "copy adapters/hermes/" + filepath.Base(filepath.Dir(path)) + " into " + filepath.Dir(filepath.Dir(path))
		return row
	}
	plugin := filepath.Base(filepath.Dir(path))
	configPath := ExpandWiringPath(w.Context.EnabledBy, c.UserHome, "")
	enabled, err := yamlPluginEnabled(configPath, plugin)
	switch {
	case err != nil:
		row.Status, row.Found = WiringUnknownRow, "plugin installed; cannot read plugins.enabled: "+err.Error()
	case enabled:
		row.Status, row.Found = WiringPass, "plugin "+plugin+" installed and listed in plugins.enabled"
	default:
		row.Status, row.Found = WiringFail, "plugin "+plugin+" installed but not in plugins.enabled"
		row.Path, row.Fix = configPath, "add "+plugin+" to plugins.enabled in this file"
	}
	return row
}

func yamlPluginEnabled(path, plugin string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var doc struct {
		Plugins struct {
			Enabled []string `yaml:"enabled"`
		} `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false, err
	}
	return hasName(doc.Plugins.Enabled, plugin), nil
}

// mcpRows checks a harness's own MCP files: the compound server must be on.
func (c WiringCheck) mcpRows(name string, h Harness, families map[string]familyServers) []WiringRow {
	w := h.Wiring
	if len(w.MCP) == 0 {
		if !h.Capabilities.MCP && len(w.Notes) > 0 {
			return []WiringRow{{Harness: name, Fact: FactMCP, Status: WiringInfo, Found: w.Notes[0]}}
		}
		return nil
	}
	var rows []WiringRow
	for _, src := range w.MCP {
		path := ExpandWiringPath(src.Path, c.UserHome, "")
		if path == "" {
			continue
		}
		row := WiringRow{Harness: name, Fact: FactMCP, Path: path}
		got, err := ReadMCPServers(src.Shape, path)
		switch {
		case err != nil && os.IsNotExist(err):
			row.Status, row.Found = WiringFail, "no MCP config file"
			row.Fix = addCompoundServerFix(src)
		case err != nil:
			row.Status, row.Found = WiringFail, "MCP config unreadable: "+err.Error()
		case !hasName(got.Names, compoundServer):
			row.Status, row.Found = WiringFail, "the compound server is not on: "+listOrNone(got.Names)+disabledSuffix(got)
			row.Fix = addCompoundServerFix(src)
		default:
			row.Status, row.Found = WiringPass, listOrNone(got.Names)+disabledSuffix(got)
		}
		if err == nil {
			set := map[string]bool{}
			for _, n := range got.Names {
				set[n] = true
			}
			families[name] = familyServers{path: path, names: set}
		}
		rows = append(rows, row)
	}
	return rows
}

// parityRows names each server one family runs and another does not.
func parityRows(families map[string]familyServers, userHome string) []WiringRow {
	if len(families) < 2 {
		return nil
	}
	fams := make([]string, 0, len(families))
	all := map[string]bool{}
	for f, s := range families {
		fams = append(fams, f)
		for n := range s.names {
			all[n] = true
		}
	}
	sort.Strings(fams)
	servers := make([]string, 0, len(all))
	for n := range all {
		servers = append(servers, n)
	}
	sort.Strings(servers)
	var rows []WiringRow
	for _, server := range servers {
		var in, out, inPaths []string
		for _, f := range fams {
			label := f + " (" + displayHomePath(families[f].path, userHome) + ")"
			if families[f].names[server] {
				in = append(in, f)
				inPaths = append(inPaths, families[f].path)
			} else {
				out = append(out, label)
			}
		}
		if len(out) == 0 {
			continue
		}
		rows = append(rows, WiringRow{Harness: strings.Join(in, "+"), Fact: FactMCPParity, Status: WiringInfo,
			Found: server + " is absent from " + strings.Join(out, ", "), Path: strings.Join(inPaths, ", ")})
	}
	return rows
}

// skillAdapterRow requires strictly valid YAML frontmatter with a name and a
// description; a harness may drop a skill whose frontmatter does not parse.
func skillAdapterRow(name, path string) WiringRow {
	row := WiringRow{Harness: name, Fact: FactSkillAdapter, Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		row.Status, row.Found = WiringInfo, "not rendered here"
		return row
	}
	if err := ValidateSkillFrontmatter(data); err != nil {
		row.Status, row.Found = WiringFail, "frontmatter is not valid YAML: "+err.Error()
		row.Fix = "fix the adapter template, then compound harness skill-adapter --skill " + renderedAdapterSkill + " --harness " + name + " --apply"
		return row
	}
	row.Status, row.Found = WiringPass, "frontmatter is valid YAML"
	return row
}

// ValidateSkillFrontmatter checks that a SKILL.md opens with a "---" block
// that parses as a YAML mapping with non-empty string name and description.
func ValidateSkillFrontmatter(data []byte) error {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return errors.New(`the file does not open with "---"`)
	}
	rest := text[len("---\n"):]
	var block string
	switch i := strings.Index(rest, "\n---\n"); {
	case strings.HasPrefix(rest, "---\n") || rest == "---":
		block = ""
	case i >= 0:
		block = rest[:i+1]
	case strings.HasSuffix(rest, "\n---"):
		block = strings.TrimSuffix(rest, "---")
	default:
		return errors.New(`no closing "---" line`)
	}
	var fm map[string]any
	if err := yaml.Unmarshal([]byte(block), &fm); err != nil {
		return errors.New(strings.Join(strings.Fields(err.Error()), " "))
	}
	for _, key := range []string{"name", "description"} {
		v, ok := fm[key].(string)
		if !ok || strings.TrimSpace(v) == "" {
			return fmt.Errorf("%s is missing or not a string", key)
		}
	}
	return nil
}

// settingsHookCommands returns the commands a settings-style JSON file binds
// to one hook event. Both the Claude layout ({"hooks": {...}}) and a bare
// event map are read.
func settingsHookCommands(path, event string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var out []string
	for _, group := range doc.Hooks[event] {
		for _, hook := range group.Hooks {
			out = append(out, hook.Command)
		}
	}
	return out, nil
}

func containsMarker(commands []string, marker string) bool {
	for _, cmd := range commands {
		if strings.Contains(cmd, marker) {
			return true
		}
	}
	return false
}

func diffNames(want, got []string) (missing, extra []string) {
	g := map[string]bool{}
	for _, n := range got {
		g[n] = true
	}
	w := map[string]bool{}
	for _, n := range want {
		w[n] = true
		if !g[n] {
			missing = append(missing, n)
		}
	}
	for _, n := range got {
		if !w[n] {
			extra = append(extra, n)
		}
	}
	return missing, extra
}

func hasName(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func listOrNone(names []string) string {
	if len(names) == 0 {
		return "no servers"
	}
	return strings.Join(names, ", ")
}

func disabledSuffix(s MCPServers) string {
	if len(s.Disabled) == 0 {
		return ""
	}
	return " (off: " + strings.Join(s.Disabled, ", ") + ")"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// runWiringCheck is the `compound harness check` verb behind RunWiringCommand.
func runWiringCheck(reg Registry, only string, homes []ClaudeHome, userHome string, asJSON bool, stdout, stderr io.Writer) int {
	rows, err := CheckWiring(WiringCheck{
		Registry: reg, UserHome: userHome, Homes: homes, Only: only,
		Probe:         ExecWiringProbe(WiringProbeTimeout, userHome),
		SkillAdapters: RenderedSkillAdapterPaths(),
	})
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	if asJSON {
		data, _ := json.MarshalIndent(map[string]any{
			"schema_version": WiringCheckSchema,
			"failed":         HasFailure(rows),
			"rows":           rows,
		}, "", "  ")
		fmt.Fprintln(stdout, string(data))
	} else {
		fmt.Fprint(stdout, FormatWiringCheck(rows, userHome))
	}
	if HasFailure(rows) {
		return 1
	}
	return 0
}

// WiringProbeTimeout bounds each harness report command the check runs.
const WiringProbeTimeout = 20 * time.Second

// maxListedHomes caps the homes one PASS or INFO line names.
const maxListedHomes = 6

// FormatWiringCheck prints rows grouped: rows that say the same thing about
// several homes become one line naming the homes. Failures come first, and a
// failing or unknown row names its file.
func FormatWiringCheck(rows []WiringRow, userHome string) string {
	type group struct {
		row   WiringRow
		homes []string
		paths []string
	}
	var order []string
	groups := map[string]*group{}
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.Status]++
		key := strings.Join([]string{r.Status, r.Harness, r.Fact, r.Found, r.Fix}, "\x00")
		g, ok := groups[key]
		if !ok {
			g = &group{row: r}
			groups[key] = g
			order = append(order, key)
		}
		if r.Home != "" {
			g.homes = append(g.homes, r.Home)
		}
		if r.Path != "" {
			g.paths = append(g.paths, displayHomePath(r.Path, userHome))
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# harness check: %d FAIL, %d UNKNOWN, %d INFO, %d PASS\n",
		counts[WiringFail], counts[WiringUnknownRow], counts[WiringInfo], counts[WiringPass])
	for _, status := range []string{WiringFail, WiringUnknownRow, WiringInfo, WiringPass} {
		for _, key := range order {
			g := groups[key]
			if g.row.Status != status {
				continue
			}
			who := g.row.Harness
			if len(g.homes) > 0 {
				homes := g.homes
				// A failing row names every home; a passing or informational one
				// stays one short line (--json has them all).
				if len(homes) > maxListedHomes && status != WiringFail && status != WiringUnknownRow {
					homes = append(append([]string(nil), homes[:maxListedHomes]...), fmt.Sprintf("+%d more", len(g.homes)-maxListedHomes))
				}
				who += " [" + strings.Join(homes, ", ") + "]"
			}
			fmt.Fprintf(&b, "%-7s %s %s: %s\n", status, who, g.row.Fact, g.row.Found)
			if (status == WiringFail || status == WiringUnknownRow) && len(g.paths) > 0 {
				fmt.Fprintf(&b, "        file: %s\n", strings.Join(g.paths, ", "))
			}
			if g.row.Fix != "" {
				fmt.Fprintf(&b, "        fix: %s\n", g.row.Fix)
			}
		}
	}
	return b.String()
}

// addCompoundServerFix says where the compound server goes in one MCP file.
// A file that holds two layouts gets the newer one.
func addCompoundServerFix(src WiringMCP) string {
	key := src.Key
	if src.Shape == MCPShapeJSONMCP {
		key = "mcp.servers.<name>"
	}
	return "add a compound server (command: compound mcp-transport) under " + key + " in this file"
}
