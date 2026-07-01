package tooltopology

import (
	"strconv"
	"strings"

	"compound/internal/capgraph"
)

const BoundaryDoctorSchemaVersion = "compound.tool_boundary_doctor.v0.1"

type BoundaryDoctorReport struct {
	SchemaVersion         string   `json:"schema_version"`
	AuthorityRef          string   `json:"authority_ref"`
	Status                string   `json:"status"`
	NodeCount             int      `json:"node_count"`
	MCPCount              int      `json:"mcp_count"`
	ToolCount             int      `json:"tool_count"`
	PluginAdapterCount    int      `json:"plugin_adapter_count"`
	RetiredPluginAdapters int      `json:"retired_plugin_adapters"`
	PluginSurfaceCount    int      `json:"plugin_surface_count"`
	Checks                []Check  `json:"checks"`
	BoundaryNotes         []string `json:"boundary_notes"`
}

func DoctorBoundary(homePath string) (BoundaryDoctorReport, error) {
	report := BoundaryDoctorReport{
		SchemaVersion: BoundaryDoctorSchemaVersion,
		AuthorityRef:  "compound://registries/capability-graph",
		Status:        "ok",
		BoundaryNotes: []string{
			"mcporter and plugin inventories are observed evidence; they do not execute tools or mutate MCP/plugin configs",
			"adapter:plugin:* nodes must remain retired/non-surfacing until a separate reviewed tool contract promotes a concrete owner",
		},
	}
	store, err := capgraph.Open(homePath)
	if err != nil {
		return report, err
	}
	nodes, err := store.List()
	if err != nil {
		return report, err
	}
	report.NodeCount = len(nodes)
	for _, node := range nodes {
		switch node.Kind {
		case "mcp":
			report.MCPCount++
		case "tool":
			report.ToolCount++
		case "adapter":
			if strings.HasPrefix(node.ID, "adapter:plugin:") {
				report.PluginAdapterCount++
				if node.Retired || node.EvidenceOnly {
					report.RetiredPluginAdapters++
				}
			}
		}
	}
	surfaced, err := store.Surface("plugin marketplace mcp hook adapter")
	if err != nil {
		return report, err
	}
	for _, hit := range surfaced {
		if strings.HasPrefix(hit.Node.ID, "adapter:plugin:") {
			report.PluginSurfaceCount++
		}
	}
	report.addCheck("capability_graph_present", report.NodeCount > 0, intDetail(report.NodeCount))
	report.addCheck("mcp_nodes_observed", report.MCPCount > 0, intDetail(report.MCPCount))
	report.addCheck("tool_nodes_observed", report.ToolCount > 0, intDetail(report.ToolCount))
	report.addCheck("plugin_adapters_retired", report.PluginAdapterCount == report.RetiredPluginAdapters, intDetail(report.RetiredPluginAdapters)+"/"+intDetail(report.PluginAdapterCount))
	report.addCheck("plugin_adapters_not_surfacing", report.PluginSurfaceCount == 0, intDetail(report.PluginSurfaceCount))
	return report, nil
}

func (r *BoundaryDoctorReport) addCheck(name string, ok bool, detail string) {
	status := "ok"
	if !ok {
		status = "fail"
		r.Status = "fail"
	}
	r.Checks = append(r.Checks, Check{Name: name, Status: status, Detail: detail})
}

func intDetail(value int) string {
	return strconv.Itoa(value)
}
