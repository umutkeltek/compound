package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Claude home kinds. The default home is the owner of the family's settings
// and MCP list; a native seat is another signed-in account that gets both by a
// mirror step at launch; a model lane keeps its own pinned settings.
const (
	ClaudeHomeDefault = "default"
	ClaudeHomeNative  = "native"
	ClaudeHomeLane    = "lane"
)

// ClaudeHome is one Claude config home found on this machine.
type ClaudeHome struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`
	Kind string `json:"kind"`
	// HookHarness is the registry name whose hooks the hook plan installs into
	// this home; empty when the hook plan does not name it.
	HookHarness string `json:"hook_harness,omitempty"`
	// HookInstall is the `hook install --harness` selector for this home when
	// it is not HookHarness itself (the record-only lane set).
	HookInstall string `json:"hook_install,omitempty"`
	// Source names the seam that found it: default, hook-plan, lane-registry,
	// config-tree or env.
	Source string `json:"source"`
	// Missing is true when a seam names the home but no folder exists.
	Missing bool `json:"missing,omitempty"`
}

// MCPFile is the file this home reads its user-scope MCP servers from: the
// default home reads ~/.claude.json, every other home <home>/.claude.json.
func (h ClaudeHome) MCPFile(userHome string) string {
	if h.Kind == ClaudeHomeDefault {
		return ExpandWiringPath("~/.claude.json", userHome, h.Dir)
	}
	return ExpandWiringPath(WiringClaudeHome+"/.claude.json", userHome, h.Dir)
}

// ClaudeHomeSources are the seams that locate Claude homes at run time. None
// of them is a list kept here: the hook plan names the lane homes it installs
// into, the lane registry file names lane profiles, and the folder that holds
// the hook plan's lane homes sits inside the config tree every other home
// shares.
type ClaudeHomeSources struct {
	UserHome string `json:"user_home"`
	// LaneSettings maps a registry name to the settings file the hook plan
	// installs that lane's hooks into.
	LaneSettings map[string]string `json:"lane_settings,omitempty"`
	// RecordOnlyLanes maps the settings file of every other lane home the hook
	// plan hooks with its record-only set to the harness name its hooks report
	// as; RecordOnlyInstall is the selector that writes them.
	RecordOnlyLanes   map[string]string `json:"record_only_lanes,omitempty"`
	RecordOnlyInstall string            `json:"record_only_install,omitempty"`
	// LanesFile is the lane registry file; "" when none was found.
	LanesFile string `json:"lanes_file,omitempty"`
	// ConfigDir is CLAUDE_CONFIG_DIR of this process, when set.
	ConfigDir string `json:"config_dir,omitempty"`
}

// LaneSettingsFunc is the hook plan's seam: the settings file it installs a
// lane's hooks into, or ok=false when the name is not a hook-governed lane.
type LaneSettingsFunc func(baseDir, harnessName string) (string, bool)

// DefaultClaudeHomeSources fills the sources from this process: every
// registry name is offered to the hook plan's seam, the lane registry file
// comes from LANES_JSON (the variable the lane scripts read) or the working
// repository, and CLAUDE_CONFIG_DIR from the environment.
func DefaultClaudeHomeSources(userHome string, reg Registry, laneSettings LaneSettingsFunc) ClaudeHomeSources {
	src := ClaudeHomeSources{UserHome: userHome, ConfigDir: strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))}
	if laneSettings != nil {
		for _, name := range reg.Names() {
			if path, ok := laneSettings(userHome, name); ok {
				if src.LaneSettings == nil {
					src.LaneSettings = map[string]string{}
				}
				src.LaneSettings[name] = path
			}
		}
	}
	if f := strings.TrimSpace(os.Getenv("LANES_JSON")); f != "" {
		src.LanesFile = f
	} else if wd, err := os.Getwd(); err == nil {
		if candidate := filepath.Join(wd, "adapters", "_lib", "lanes.json"); isRegularFile(candidate) {
			src.LanesFile = candidate
		}
	}
	return src
}

// DiscoverClaudeHomes finds the default home, every home a seam names, and
// every runnable home in the config tree. A folder in the tree is a native
// seat when its .claude.json holds a signed-in account, a lane when it has
// its own settings.json, and is skipped otherwise (it is another tool's
// folder, not a Claude home).
func DiscoverClaudeHomes(src ClaudeHomeSources) []ClaudeHome {
	var homes []ClaudeHome
	seen := map[string]int{}
	add := func(h ClaudeHome) {
		key := filepath.Clean(h.Dir)
		if i, ok := seen[key]; ok {
			if homes[i].HookHarness == "" {
				homes[i].HookHarness = h.HookHarness
			}
			return
		}
		seen[key] = len(homes)
		homes = append(homes, h)
	}
	add(ClaudeHome{Name: ClaudeHomeDefault, Dir: filepath.Join(src.UserHome, ".claude"), Kind: ClaudeHomeDefault, Source: "default"})

	laneNames := make([]string, 0, len(src.LaneSettings))
	for name := range src.LaneSettings {
		laneNames = append(laneNames, name)
	}
	sort.Strings(laneNames)
	laneParents := map[string]bool{}
	for _, name := range laneNames {
		dir := filepath.Dir(src.LaneSettings[name])
		add(ClaudeHome{Name: filepath.Base(dir), Dir: dir, Kind: ClaudeHomeLane, HookHarness: name, Source: "hook-plan", Missing: !isDirectory(dir)})
		laneParents[filepath.Dir(dir)] = true
	}
	recordOnly := make([]string, 0, len(src.RecordOnlyLanes))
	for path := range src.RecordOnlyLanes {
		recordOnly = append(recordOnly, path)
	}
	sort.Strings(recordOnly)
	for _, path := range recordOnly {
		dir := filepath.Dir(path)
		add(ClaudeHome{Name: filepath.Base(dir), Dir: dir, Kind: ClaudeHomeLane, HookHarness: src.RecordOnlyLanes[path], HookInstall: src.RecordOnlyInstall, Source: "hook-plan"})
		laneParents[filepath.Dir(dir)] = true
	}

	for _, dir := range laneRegistryHomes(src.LanesFile, src.UserHome) {
		add(ClaudeHome{Name: filepath.Base(dir), Dir: dir, Kind: ClaudeHomeLane, Source: "lane-registry", Missing: !isDirectory(dir)})
	}

	parents := make([]string, 0, len(laneParents))
	for p := range laneParents {
		parents = append(parents, p)
	}
	sort.Strings(parents)
	for _, lanesDir := range parents {
		for _, dir := range childDirs(lanesDir) {
			if isRegularFile(filepath.Join(dir, "settings.json")) {
				add(ClaudeHome{Name: filepath.Base(dir), Dir: dir, Kind: ClaudeHomeLane, Source: "config-tree"})
			}
		}
		for _, dir := range childDirs(filepath.Dir(lanesDir)) {
			if filepath.Clean(dir) == filepath.Clean(lanesDir) {
				continue
			}
			if kind := classifyClaudeHome(dir); kind != "" {
				add(ClaudeHome{Name: filepath.Base(dir), Dir: dir, Kind: kind, Source: "config-tree"})
			}
		}
	}
	if dir := strings.TrimSpace(src.ConfigDir); dir != "" {
		if kind := classifyClaudeHome(dir); kind != "" {
			add(ClaudeHome{Name: filepath.Base(dir), Dir: dir, Kind: kind, Source: "env"})
		}
	}

	// Two folders can share a base name (a lane at the tree root and one in the
	// lanes folder); a row must name exactly one home, so both take their parent.
	byName := map[string]int{}
	for _, h := range homes {
		byName[h.Name]++
	}
	for i, h := range homes {
		if byName[h.Name] > 1 && h.Kind != ClaudeHomeDefault {
			homes[i].Name = filepath.Base(filepath.Dir(h.Dir)) + "/" + h.Name
		}
	}

	rank := map[string]int{ClaudeHomeDefault: 0, ClaudeHomeNative: 1, ClaudeHomeLane: 2}
	sort.SliceStable(homes, func(i, j int) bool {
		if rank[homes[i].Kind] != rank[homes[j].Kind] {
			return rank[homes[i].Kind] < rank[homes[j].Kind]
		}
		return homes[i].Name < homes[j].Name
	})
	return homes
}

// classifyClaudeHome answers native, lane, or "" for a folder that is not a
// runnable Claude home.
func classifyClaudeHome(dir string) string {
	if hasSignedInAccount(filepath.Join(dir, ".claude.json")) {
		return ClaudeHomeNative
	}
	if isRegularFile(filepath.Join(dir, "settings.json")) {
		return ClaudeHomeLane
	}
	return ""
}

// hasSignedInAccount reports whether a .claude.json records a signed-in
// account. Only the presence of the key is read.
func hasSignedInAccount(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var doc struct {
		OAuthAccount json.RawMessage `json:"oauthAccount"`
	}
	if json.NewDecoder(f).Decode(&doc) != nil {
		return false
	}
	trimmed := strings.TrimSpace(string(doc.OAuthAccount))
	return trimmed != "" && trimmed != "null"
}

// laneRegistryHomes reads the Claude-family lane homes a lane registry file
// names. A missing or unreadable file names none.
func laneRegistryHomes(path, userHome string) []string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var reg struct {
		Lanes []struct {
			Family         string `json:"family"`
			ProfileDefault string `json:"profile_default"`
		} `json:"lanes"`
	}
	if json.Unmarshal(data, &reg) != nil {
		return nil
	}
	var out []string
	for _, lane := range reg.Lanes {
		dir := strings.TrimSpace(lane.ProfileDefault)
		if lane.Family != "claude_code" || dir == "" {
			continue
		}
		for _, prefix := range []string{"${HOME}/", "$HOME/", "~/"} {
			if strings.HasPrefix(dir, prefix) {
				dir = filepath.Join(userHome, filepath.FromSlash(strings.TrimPrefix(dir, prefix)))
				break
			}
		}
		if filepath.IsAbs(dir) {
			out = append(out, filepath.Clean(dir))
		}
	}
	return out
}

func childDirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if isDirectory(path) {
			out = append(out, path)
		}
	}
	return out
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// displayHomePath shortens a path under the user's home to "~/...".
func displayHomePath(path, userHome string) string {
	if userHome != "" {
		if rel, err := filepath.Rel(userHome, path); err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return path
}
