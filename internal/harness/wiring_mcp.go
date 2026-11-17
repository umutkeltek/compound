package harness

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// MCPServers is what one MCP config file declares, by server NAME only. The
// readers never keep env, args, headers or URLs, so nothing here can carry a
// credential.
type MCPServers struct {
	// Names are the servers the file turns on, sorted.
	Names []string `json:"names"`
	// Disabled are the servers the file names but turns off, sorted.
	Disabled []string `json:"disabled,omitempty"`
	// Note names something the reader saw but did not list.
	Note string `json:"note,omitempty"`
}

var mcpServerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ReadMCPServers reads the server names a config file of the given shape
// declares. A missing file returns an error os.IsNotExist recognises, so a
// caller can tell "not installed" from "unreadable".
func ReadMCPServers(shape, path string) (MCPServers, error) {
	switch shape {
	case MCPShapeJSONServers, MCPShapeJSONMCP:
		data, err := os.ReadFile(path)
		if err != nil {
			return MCPServers{}, err
		}
		return readJSONMCP(shape, data)
	case MCPShapeTOMLServers:
		f, err := os.Open(path)
		if err != nil {
			return MCPServers{}, err
		}
		defer f.Close()
		return readTOMLMCP(f)
	case MCPShapeYAMLServers:
		data, err := os.ReadFile(path)
		if err != nil {
			return MCPServers{}, err
		}
		return readYAMLMCP(data)
	}
	return MCPServers{}, fmt.Errorf("unknown MCP config shape %q", shape)
}

func readJSONMCP(shape string, data []byte) (MCPServers, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return MCPServers{}, fmt.Errorf("parse json: %w", err)
	}
	key := "mcpServers"
	if shape == MCPShapeJSONMCP {
		key = "mcp"
	}
	var entries map[string]json.RawMessage
	if raw, ok := doc[key]; ok {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return MCPServers{}, fmt.Errorf("%s is not an object", key)
		}
	}
	set := mcpNameSet{}
	for name, raw := range entries {
		// OpenCode 2.x keeps mcp.servers.<name> beside the older mcp.<name>.
		// "servers" is the container only when it is not itself a server.
		if shape == MCPShapeJSONMCP && name == "servers" {
			if inner, ok := serverContainer(raw); ok {
				for innerName, innerRaw := range inner {
					set.add(innerName, jsonEntryEnabled(innerRaw))
				}
				continue
			}
		}
		set.add(name, jsonEntryEnabled(raw))
	}
	out := set.result()
	if raw, ok := doc["projects"]; ok && shape == MCPShapeJSONServers {
		var projects map[string]struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		}
		if json.Unmarshal(raw, &projects) == nil {
			n := 0
			for _, p := range projects {
				if len(p.MCPServers) > 0 {
					n++
				}
			}
			if n > 0 {
				out.Note = strconv.Itoa(n) + " project-scoped config(s) with servers not listed"
			}
		}
	}
	return out, nil
}

// serverContainer reports whether raw is a map of server objects rather than
// one server's own settings.
func serverContainer(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	var inner map[string]json.RawMessage
	if json.Unmarshal(raw, &inner) != nil {
		return nil, false
	}
	for _, field := range []string{"type", "command", "url", "enabled", "disabled"} {
		if _, ok := inner[field]; ok {
			return nil, false
		}
	}
	for _, v := range inner {
		var obj map[string]json.RawMessage
		if json.Unmarshal(v, &obj) != nil {
			return nil, false
		}
	}
	return inner, true
}

// jsonEntryEnabled is false only for an entry that turns itself off.
func jsonEntryEnabled(raw json.RawMessage) bool {
	var entry struct {
		Enabled  *bool `json:"enabled"`
		Disabled *bool `json:"disabled"`
	}
	if json.Unmarshal(raw, &entry) != nil {
		return true
	}
	if entry.Enabled != nil && !*entry.Enabled {
		return false
	}
	return entry.Disabled == nil || !*entry.Disabled
}

var (
	tomlTableHeader  = regexp.MustCompile(`^\[`)
	tomlServerHeader = regexp.MustCompile(`^\[mcp_servers\.(?:"([^"]+)"|([A-Za-z0-9_-]+))\]\s*(?:#.*)?$`)
	tomlEnabledLine  = regexp.MustCompile(`^enabled\s*=\s*(true|false)\s*(?:#.*)?$`)
)

// readTOMLMCP scans [mcp_servers.<name>] tables line by line. Only an
// `enabled` key inside the server's own table turns it off; one inside a
// sub-table such as [mcp_servers.<name>.env] is that table's value.
func readTOMLMCP(f *os.File) (MCPServers, error) {
	set := mcpNameSet{}
	current := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if tomlTableHeader.MatchString(line) {
			current = ""
			if m := tomlServerHeader.FindStringSubmatch(line); m != nil {
				current = m[1]
				if current == "" {
					current = m[2]
				}
				set.add(current, true)
			}
			continue
		}
		if current != "" {
			if m := tomlEnabledLine.FindStringSubmatch(line); m != nil && m[1] == "false" {
				set.add(current, false)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return MCPServers{}, err
	}
	return set.result(), nil
}

// readYAMLMCP walks mcp_servers as YAML nodes so no value is decoded into a
// Go string beyond each server's own `enabled` flag.
func readYAMLMCP(data []byte) (MCPServers, error) {
	var doc struct {
		MCPServers yaml.Node `yaml:"mcp_servers"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return MCPServers{}, fmt.Errorf("parse yaml: %w", err)
	}
	set := mcpNameSet{}
	node := doc.MCPServers
	if node.Kind != yaml.MappingNode {
		return set.result(), nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		name, value := node.Content[i].Value, node.Content[i+1]
		enabled := true
		if value.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(value.Content); j += 2 {
				if value.Content[j].Value == "enabled" && value.Content[j+1].Value == "false" {
					enabled = false
				}
			}
		}
		set.add(name, enabled)
	}
	return set.result(), nil
}

// mcpNameSet collects names; a server turned off anywhere stays off.
type mcpNameSet map[string]bool

func (s mcpNameSet) add(name string, enabled bool) {
	if !mcpServerName.MatchString(name) {
		return
	}
	if prev, ok := s[name]; ok {
		enabled = prev && enabled
	}
	s[name] = enabled
}

func (s mcpNameSet) result() MCPServers {
	out := MCPServers{Names: []string{}}
	for name, enabled := range s {
		if enabled {
			out.Names = append(out.Names, name)
		} else {
			out.Disabled = append(out.Disabled, name)
		}
	}
	sort.Strings(out.Names)
	sort.Strings(out.Disabled)
	return out
}
