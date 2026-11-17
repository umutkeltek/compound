package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// wiring_cli.go is the organ CLI face of `compound harness map` and
// `compound harness check`. cmd/compound only dispatches here and hands in
// the hook plan's lane seam, which this package cannot import.

// WiringMapSchema names the `harness map --json` shape.
const WiringMapSchema = "compound.harness_wiring_map.v0.1"

// WiringUsage is the usage text for both verbs.
const WiringUsage = "usage: compound harness map|check [--harness H] [--json]\n" +
	"  map    where each harness gets its instructions, skills, MCP servers, permissions and session context, and its own listing command\n" +
	"  check  proves the map against this machine: one row per fact, with the file to fix; exits 1 on any FAIL"

// RunWiringCommand runs `compound harness map|check`.
func RunWiringCommand(sub string, args []string, laneSettings LaneSettingsFunc, stdout, stderr io.Writer) int {
	only, asJSON := "", false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--harness":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				fmt.Fprintln(stderr, WiringUsage)
				return 2
			}
			i++
			only = strings.TrimSpace(args[i])
		case "-h", "--help":
			fmt.Fprintln(stdout, WiringUsage)
			return 0
		default:
			fmt.Fprintf(stderr, "error: unknown argument %q\n%s\n", args[i], WiringUsage)
			return 2
		}
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	reg := DefaultRegistry()
	homes := DiscoverClaudeHomes(DefaultClaudeHomeSources(userHome, reg, laneSettings))
	switch sub {
	case "map":
		out, err := WiringMap(reg, only, homes, userHome, asJSON)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
		fmt.Fprint(stdout, out)
		return 0
	case "check":
		return runWiringCheck(reg, only, homes, userHome, asJSON, stdout, stderr)
	}
	fmt.Fprintln(stderr, WiringUsage)
	return 2
}

// WiringMap renders the facts of every harness (or one), plus the Claude
// homes found on this machine.
func WiringMap(reg Registry, only string, homes []ClaudeHome, userHome string, asJSON bool) (string, error) {
	names := reg.WiringNames()
	if strings.TrimSpace(only) != "" {
		h, ok := reg.Get(only)
		if !ok || h.Wiring == nil {
			return "", fmt.Errorf("no wiring facts for harness %q (have: %s)", only, strings.Join(names, ", "))
		}
		names = []string{h.Name}
	}
	if asJSON {
		type entry struct {
			Name   string       `json:"name"`
			Wiring *Wiring      `json:"wiring"`
			Homes  []ClaudeHome `json:"homes,omitempty"`
		}
		var entries []entry
		for _, name := range names {
			e := entry{Name: name, Wiring: reg.Harnesses[name].Wiring}
			if name == "claude_code" {
				e.Homes = homes
			}
			entries = append(entries, e)
		}
		data, err := json.MarshalIndent(map[string]any{"schema_version": WiringMapSchema, "harnesses": entries}, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data) + "\n", nil
	}
	var b strings.Builder
	for i, name := range names {
		if i > 0 {
			b.WriteString("\n")
		}
		w := reg.Harnesses[name].Wiring
		status := w.Status
		if w.StatusNote != "" {
			status += ", " + w.StatusNote
		}
		fmt.Fprintf(&b, "%s (%s)\n", name, status)
		line := func(label string, values ...string) {
			if len(values) == 0 {
				return
			}
			fmt.Fprintf(&b, "  %-13s %s\n", label+":", values[0])
			for _, v := range values[1:] {
				fmt.Fprintf(&b, "  %-13s %s\n", "", v)
			}
		}
		line("instructions", w.Instructions...)
		var skills []string
		for _, root := range w.SkillRoots {
			skills = append(skills, root.Path+" ("+recursionLabel(root.Recursive)+")")
		}
		line("skills", skills...)
		var mcp []string
		for _, src := range w.MCP {
			text := src.Path + " " + src.Key
			if src.Scope != "" {
				text += " - " + src.Scope
			}
			mcp = append(mcp, text)
		}
		line("mcp", mcp...)
		line("permissions", w.Permissions)
		line("context", contextLabel(w.Context))
		line("listing", strings.Join(w.Listing, "; "))
		if w.Homes != "" {
			line("homes", w.Homes)
		}
		if name == "claude_code" && len(homes) > 0 {
			line("found homes", foundHomesLabel(homes, userHome)...)
		}
		line("note", w.Notes...)
	}
	b.WriteString("\nProve it on this machine: compound harness check\n")
	return b.String(), nil
}

func recursionLabel(r string) string {
	switch r {
	case "yes":
		return "recursive"
	case "no":
		return "one level"
	}
	return "recursion " + Unknown
}

func contextLabel(c WiringContext) string {
	if c.Kind == ContextNone {
		return "none"
	}
	text := c.Path + " (" + c.Kind
	if !c.SessionStart {
		text += ", no session-start context"
	}
	return text + "; proof: " + strings.TrimSpace(c.Marker) + "; loaded: " + c.LoadReport + ")"
}

// foundHomesLabel lists the Claude homes by kind, and the config tree they
// share.
func foundHomesLabel(homes []ClaudeHome, userHome string) []string {
	byKind := map[string][]string{}
	trees := map[string]bool{}
	var treeOrder []string
	for _, h := range homes {
		label := h.Name
		if h.Missing {
			label += " (missing)"
		}
		byKind[h.Kind] = append(byKind[h.Kind], label)
		if h.Kind != ClaudeHomeDefault && h.Source == "config-tree" {
			tree := displayHomePath(filepath.Dir(h.Dir), userHome)
			if !trees[tree] {
				trees[tree] = true
				treeOrder = append(treeOrder, tree)
			}
		}
	}
	var out []string
	for _, kind := range []string{ClaudeHomeDefault, ClaudeHomeNative, ClaudeHomeLane} {
		if len(byKind[kind]) == 0 {
			continue
		}
		label := map[string]string{ClaudeHomeDefault: "default", ClaudeHomeNative: "native seats", ClaudeHomeLane: "model lanes"}[kind]
		out = append(out, label+": "+strings.Join(byKind[kind], ", "))
	}
	var roots []string
	for _, tree := range treeOrder {
		nested := false
		for _, other := range treeOrder {
			nested = nested || (other != tree && strings.HasPrefix(tree, other+"/"))
		}
		if !nested {
			roots = append(roots, tree)
		}
	}
	if len(roots) > 0 {
		out = append(out, "config tree: "+strings.Join(roots, ", "))
	}
	return out
}
