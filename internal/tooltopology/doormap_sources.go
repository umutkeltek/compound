package tooltopology

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"compound/internal/harness"
)

// MCP source kinds. A kind names a config SHAPE, not a product; the harness
// registry owns the shapes and the one reader of them. It extracts server
// names only and never touches env, args, headers or URLs, so no credential
// can reach the snapshot.
const (
	MCPKindJSONServers = harness.MCPShapeJSONServers // top-level "mcpServers" object
	MCPKindJSONMCP     = harness.MCPShapeJSONMCP     // "mcp" object: mcp.<name> and mcp.servers.<name>
	MCPKindTOMLServers = harness.MCPShapeTOMLServers // [mcp_servers.<name>] tables; enabled = false is off
	MCPKindYAMLServers = harness.MCPShapeYAMLServers // mcp_servers.<name> map; enabled: false is off
)

// MCPSource is one place a harness config declares MCP servers.
type MCPSource struct {
	Harness string
	Kind    string
	Path    string
	// Display is the home-relative label shown to readers ("~/..."), so the
	// snapshot says where to look without embedding the absolute home path.
	Display string
}

// seatLabelSeparator joins a family and one of its homes in an MCP set label
// ("claude_code@seat-a"). The served block summarises those sets.
const seatLabelSeparator = "@"

// DefaultMCPSources lists the MCP files the harness registry's wiring facts
// name for every active harness, each signed-in Claude seat's own list, and
// the bridge config. Absent files are skipped by ScanMCP.
func DefaultMCPSources(homeDir string, claudeHomes []harness.ClaudeHome) []MCPSource {
	reg := harness.DefaultRegistry()
	var out []MCPSource
	for _, name := range reg.WiringNames() {
		w := reg.Harnesses[name].Wiring
		if w.Status != harness.WiringActive {
			continue
		}
		for _, src := range w.MCP {
			// "<home>" files belong to one Claude home each; seats are added below.
			if !strings.HasPrefix(src.Path, "~/") {
				continue
			}
			out = append(out, MCPSource{Harness: name, Kind: src.Shape, Path: harness.ExpandWiringPath(src.Path, homeDir, ""), Display: src.Path})
		}
	}
	for _, h := range claudeHomes {
		if h.Kind != harness.ClaudeHomeNative || h.Missing {
			continue
		}
		path := h.MCPFile(homeDir)
		display := path
		if rel, err := filepath.Rel(homeDir, path); err == nil && !strings.HasPrefix(rel, "..") {
			display = "~/" + filepath.ToSlash(rel)
		}
		out = append(out, MCPSource{Harness: "claude_code" + seatLabelSeparator + h.Name, Kind: MCPKindJSONServers, Path: path, Display: display})
	}
	out = append(out, MCPSource{Harness: "mcporter", Kind: MCPKindJSONServers,
		Path: filepath.Join(homeDir, ".mcporter", "mcporter.json"), Display: "~/.mcporter/mcporter.json"})
	return out
}

// ScanMCP reads server names from each source. A missing file is quiet (the
// harness may not be installed); an unreadable or unparseable one is a gap.
func ScanMCP(sources []MCPSource) ([]MCPSet, []string) {
	var sets []MCPSet
	var gaps []string
	for _, src := range sources {
		label := src.Display
		if label == "" {
			label = filepath.Base(src.Path)
		}
		names, extra, err := readMCPNames(src)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			gaps = append(gaps, fmt.Sprintf("mcp %s: unreadable (%s)", src.Harness, truncBytes(cleanField(err.Error(), 80), 80)))
			continue
		}
		if extra != "" {
			gaps = append(gaps, fmt.Sprintf("mcp %s: %s", src.Harness, extra))
		}
		sets = append(sets, MCPSet{Harness: src.Harness, Source: label, Servers: names})
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].Harness < sets[j].Harness })
	return sets, gaps
}

// readMCPNames delegates to the harness registry's reader, so the door map and
// `compound harness check` can never disagree about what a file declares.
func readMCPNames(src MCPSource) (names []string, note string, err error) {
	got, err := harness.ReadMCPServers(src.Kind, src.Path)
	if err != nil {
		return nil, "", err
	}
	return got.Names, got.Note, nil
}

// BinarySpec maps a harness name to the executable that launches it.
type BinarySpec struct {
	Harness string
	Binary  string
}

// DefaultBinaries are the harness executables Compound serves.
var DefaultBinaries = []BinarySpec{
	{"claude_code", "claude"},
	{"codex", "codex"},
	{"opencode", "opencode"},
	{"oh_my_pi", "omp"},
}

// vendorVersionDirs are conventional version-pin directories, relative to a
// user home, used when the executable on PATH is a wrapper rather than a
// versioned install.
var vendorVersionDirs = map[string]string{
	"claude": ".local/share/claude/versions",
}

// ResolveVersions infers each harness version WITHOUT executing the binary:
// a version in the resolved install path, a nearby package.json, a vendor
// version-pin directory, else an "unversioned@<mtime>:<size>" fingerprint that
// still changes when the binary is replaced.
func ResolveVersions(specs []BinarySpec, pathDirs []string, homeDir string) []HarnessVersion {
	out := make([]HarnessVersion, 0, len(specs))
	for _, s := range specs {
		out = append(out, HarnessVersion{Harness: s.Harness, Binary: s.Binary, Version: resolveVersion(s.Binary, pathDirs, homeDir)})
	}
	return out
}

func resolveVersion(bin string, pathDirs []string, homeDir string) string {
	var found string
	for _, d := range pathDirs {
		p := filepath.Join(d, bin)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			found = p
			break
		}
	}
	if found == "" {
		return "not-found"
	}
	real, err := filepath.EvalSymlinks(found)
	if err != nil {
		real = found
	}
	if v := semverInPath(real); v != "" {
		return v
	}
	if v := packageJSONVersion(real); v != "" {
		return v
	}
	if rel, ok := vendorVersionDirs[bin]; ok && homeDir != "" {
		if v := highestSemverEntry(filepath.Join(homeDir, filepath.FromSlash(rel))); v != "" {
			return v
		}
	}
	if fi, err := os.Stat(real); err == nil {
		return fmt.Sprintf("unversioned@%s:%d", fi.ModTime().UTC().Format("2006-01-02"), fi.Size())
	}
	return "unknown"
}

func semverInPath(p string) string {
	parts := strings.Split(filepath.ToSlash(p), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if m := semverRe.FindString(parts[i]); m != "" {
			return m
		}
	}
	return ""
}

func packageJSONVersion(real string) string {
	dir := filepath.Dir(real)
	for i := 0; i < 4 && dir != "/" && dir != "."; i++ {
		data, err := os.ReadFile(filepath.Join(dir, "package.json"))
		if err == nil && len(data) < 1<<20 {
			var pkg struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(data, &pkg) == nil && semverRe.MatchString(pkg.Version) {
				return pkg.Version
			}
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

func highestSemverEntry(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best, bestKey := "", [3]int{-1, -1, -1}
	for _, e := range entries {
		v := semverRe.FindString(e.Name())
		if v == "" {
			continue
		}
		if k := semverKey(v); k[0] > bestKey[0] || (k[0] == bestKey[0] && (k[1] > bestKey[1] || (k[1] == bestKey[1] && k[2] > bestKey[2]))) {
			best, bestKey = v, k
		}
	}
	return best
}

func semverKey(v string) [3]int {
	var k [3]int
	core := strings.SplitN(strings.SplitN(v, "-", 2)[0], "+", 2)[0]
	for i, p := range strings.SplitN(core, ".", 3) {
		k[i], _ = strconv.Atoi(p)
	}
	return k
}
