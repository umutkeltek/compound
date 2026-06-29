package tooltopology

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// LoadMCPorterCatalog adapts `mcporter list --json` output into the generic
// ToolTopology contract. It observes the catalog only; it never invokes MCP
// tools or mutates mcporter config.
func LoadMCPorterCatalog(path string, now time.Time) (Topology, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Topology{}, err
	}
	var src mcporterList
	if err := json.Unmarshal(data, &src); err != nil {
		return Topology{}, fmt.Errorf("parse mcporter catalog %s: %w", path, err)
	}
	if len(src.Servers) == 0 {
		return Topology{}, fmt.Errorf("mcporter catalog %s has no servers", path)
	}
	if now.IsZero() {
		now = time.Now()
	}
	sum := sha256.Sum256(data)
	revision := "sha256:" + hex.EncodeToString(sum[:])
	evidenceRef := "mcporter_json:" + revision[:19]
	topo := Topology{
		SchemaVersion: SchemaVersion,
		ID:            "topo_mcporter_" + revision[7:19],
		AdapterID:     "mcporter.list-json",
		SourceSystem:  "mcporter",
		ObservedAt:    now.UTC().Format(time.RFC3339),
		SourceRef: SourceRef{
			Path:        path,
			Kind:        "catalog",
			Owner:       "tool-topology",
			SourceClass: "mcporter-json",
			Revision:    revision,
		},
		EvidenceRefs: []string{evidenceRef},
	}
	sort.Slice(src.Servers, func(i, j int) bool { return src.Servers[i].Name < src.Servers[j].Name })
	for _, server := range src.Servers {
		serverName := strings.TrimSpace(server.Name)
		if serverName == "" {
			continue
		}
		serverID := "mcp:" + slug(serverName)
		status := mcporterStatus(server.Status)
		topo.Entries = append(topo.Entries, Entry{
			ID:           serverID,
			Kind:         "mcp",
			Name:         serverName,
			Interface:    "mcp",
			Status:       status,
			Triggers:     limitTokens(normalizedTokens(serverName, server.Transport), 24),
			Capabilities: limitTokens(mcporterToolNames(server.Tools), 40),
			Constraints:  mcporterConstraints(server),
			Weight:       mcporterWeight(status, false),
			Retired:      status != "on_demand",
			EvidenceRefs: []string{evidenceRef},
		})
		sort.Slice(server.Tools, func(i, j int) bool { return server.Tools[i].Name < server.Tools[j].Name })
		for _, tool := range server.Tools {
			toolName := strings.TrimSpace(tool.Name)
			if toolName == "" {
				continue
			}
			topo.Entries = append(topo.Entries, Entry{
				ID:           "tool:" + slug(serverName) + "." + slug(toolName),
				Kind:         "tool",
				Name:         serverName + "." + toolName,
				Interface:    "mcp_tool",
				Status:       status,
				Triggers:     limitTokens(normalizedTokens(serverName, toolName, tool.Description), 32),
				Capabilities: limitTokens(normalizedTokens(toolName, tool.Description), 32),
				Constraints:  append(mcporterConstraints(server), "mcp_server:"+slug(serverName)),
				Weight:       mcporterWeight(status, true),
				Retired:      status != "on_demand",
				EvidenceRefs: []string{evidenceRef},
			})
		}
	}
	if len(topo.Entries) == 0 {
		return Topology{}, fmt.Errorf("mcporter catalog %s produced no topology entries", path)
	}
	return topo, nil
}

type mcporterList struct {
	Mode    string           `json:"mode"`
	Counts  map[string]int   `json:"counts"`
	Servers []mcporterServer `json:"servers"`
}

type mcporterServer struct {
	Name        string           `json:"name"`
	Status      string           `json:"status"`
	Transport   string           `json:"transport"`
	Source      mcporterSource   `json:"source"`
	Sources     []mcporterSource `json:"sources"`
	Tools       []mcporterTool   `json:"tools"`
	Issue       map[string]any   `json:"issue"`
	AuthCommand string           `json:"authCommand"`
	Error       string           `json:"error"`
}

type mcporterSource struct {
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	ImportKind string `json:"importKind"`
}

type mcporterTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func mcporterStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok":
		return "on_demand"
	case "auth":
		return "auth_required"
	case "offline":
		return "offline"
	case "error":
		return "error"
	default:
		return firstNonEmpty(strings.ToLower(strings.TrimSpace(status)), "unknown")
	}
}

func mcporterWeight(status string, tool bool) float64 {
	if status != "on_demand" {
		return 0.1
	}
	if tool {
		return 1.2
	}
	return 1.0
}

func mcporterToolNames(tools []mcporterTool) []string {
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return limitTokens(normalizedTokens(names...), 40)
}

func mcporterConstraints(server mcporterServer) []string {
	values := []string{"status:" + mcporterStatus(server.Status)}
	if fields := strings.Fields(server.Transport); len(fields) > 0 {
		values = append(values, "transport:"+strings.ToLower(fields[0]))
	}
	if server.Source.Kind != "" {
		values = append(values, "source:"+server.Source.Kind)
	}
	for _, source := range server.Sources {
		if source.ImportKind != "" {
			values = append(values, "import:"+source.ImportKind)
		}
	}
	if server.AuthCommand != "" {
		values = append(values, "auth_required")
	}
	return uniqueStrings(values)
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func limitTokens(values []string, max int) []string {
	if max > 0 && len(values) > max {
		return values[:max]
	}
	return values
}
