package tooltopology

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/capgraph"
)

var tTopo = time.Date(2026, 6, 29, 13, 0, 0, 0, time.UTC)

func TestApplyImportsToolAndMCPNodesWithFallbackRecommendation(t *testing.T) {
	home := t.TempDir()
	topo := validTopology()

	report, err := Apply(home, topo, tTopo)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || len(report.NodeIDs) != 2 || len(report.EdgeIDs) != 1 {
		t.Fatalf("report = %+v", report)
	}

	store, err := capgraph.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]capgraph.Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	if byID["tool:browser-cli"].Kind != "tool" || byID["mcp:browser"].Kind != "mcp" {
		t.Fatalf("nodes = %+v", byID)
	}

	hits, err := store.Surface("browser screenshots")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Node.ID != "tool:browser-cli" {
		t.Fatalf("surface hits = %+v", hits)
	}

	edges := readEdges(t, home)
	if len(edges) != 1 || edges[0].Kind != capgraph.EdFallbackFor || edges[0].From != "mcp:browser" || edges[0].To != "tool:browser-cli" {
		t.Fatalf("edges = %+v", edges)
	}
}

func TestValidateRejectsUnknownNodeKind(t *testing.T) {
	topo := validTopology()
	topo.Entries[0].Kind = "plugin"
	report := Validate(topo)
	if report.Status != "fail" {
		t.Fatalf("status = %s, want fail", report.Status)
	}
}

func TestLoadOSResolverAdaptsGeneratedToolResolver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool-resolver.json")
	writeFile(t, path, `{
  "marker": "# @generated",
  "generated_from": ["registry/tool-topology.json"],
  "tools": [
    {
      "id": "mcporter",
      "title": "mcporter gateway/catalog",
      "topology": "gateway_core",
      "functions": ["tool.catalog", "tool.invoke"],
      "effect_class": ["read", "execute"],
      "preferred_model_tier": "deterministic",
      "host_availability": {"codex": "cli", "bridge": false},
      "actual_surfaces": ["tool-doctor"]
    },
    {
      "id": "context7",
      "title": "current library/framework/API documentation",
      "topology": "mcporter_catalog_on_demand",
      "mcporter_server": "context7",
      "functions": ["docs.lookup", "library.reference"],
      "effect_class": ["read"],
      "preferred_model_tier": "cheap_ok",
      "host_availability": {"codex": "on_demand", "bridge": false}
    },
    {
      "id": "ios_simulator",
      "title": "iOS simulator MCP",
      "topology": "mcporter_catalog_on_demand",
      "functions": ["ios.run", "ios.ui_automate", "screenshot.capture"],
      "effect_class": ["read", "execute"],
      "host_availability": {"codex": "on_demand"}
    },
    {
      "id": "xcodebuildmcp",
      "title": "xcodebuildmcp fallback",
      "topology": "legacy_or_last_resort",
      "functions": ["ios.build", "ios.debug"],
      "replacement": "ios_simulator",
      "host_availability": {"codex": "none"}
    }
  ]
}`)
	topo, err := LoadOSResolver(path, tTopo)
	if err != nil {
		t.Fatal(err)
	}
	if topo.SchemaVersion != SchemaVersion || topo.AdapterID != "os.tool-resolver-json" || len(topo.EvidenceRefs) != 1 {
		t.Fatalf("topology = %+v", topo)
	}
	if len(topo.Entries) != 4 || len(topo.Recommendations) != 1 {
		t.Fatalf("entries=%d recommendations=%d", len(topo.Entries), len(topo.Recommendations))
	}
	byID := map[string]Entry{}
	for _, entry := range topo.Entries {
		byID[entry.ID] = entry
	}
	if byID["adapter:mcporter"].Kind != "adapter" ||
		byID["mcp:context7"].Kind != "mcp" ||
		byID["tool:xcodebuildmcp"].Status != "fallback" {
		t.Fatalf("entries = %+v", byID)
	}
	rec := topo.Recommendations[0]
	if rec.Primary != "mcp:ios-simulator" || rec.Fallback != "tool:xcodebuildmcp" {
		t.Fatalf("recommendation = %+v", rec)
	}
	home := t.TempDir()
	report, err := Apply(home, topo, tTopo)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || len(report.NodeIDs) != 4 || len(report.EdgeIDs) != 1 {
		t.Fatalf("report = %+v", report)
	}
	store, err := capgraph.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := store.Surface("docs lookup")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Node.ID != "mcp:context7" {
		t.Fatalf("docs lookup hits = %+v", hits)
	}
}

func validTopology() Topology {
	return Topology{
		SchemaVersion: SchemaVersion,
		ID:            "topo_fixture_browser",
		AdapterID:     "fixture.tool-topology",
		SourceSystem:  "fixture-os",
		ObservedAt:    "2026-06-29T00:00:00Z",
		SourceRef: SourceRef{
			Path:        "fixtures/generated/tool-resolver.md",
			Kind:        "generated_view",
			Owner:       "fixture-spine",
			SourceClass: "generated",
			Revision:    "sha256:def456",
		},
		Entries: []Entry{
			{
				ID:           "tool:browser-cli",
				Kind:         "tool",
				Name:         "Browser CLI",
				Interface:    "cli",
				Status:       "active",
				Triggers:     []string{"browser", "screenshots"},
				Capabilities: []string{"web automation"},
				EvidenceRefs: []string{"artifact_sha256:def456"},
				Weight:       2,
			},
			{
				ID:           "mcp:browser",
				Kind:         "mcp",
				Name:         "Browser MCP",
				Interface:    "mcp",
				Status:       "fallback",
				Triggers:     []string{"browser"},
				Capabilities: []string{"web automation"},
				EvidenceRefs: []string{"artifact_sha256:def456"},
			},
		},
		Recommendations: []Recommendation{
			{Primary: "tool:browser-cli", Fallback: "mcp:browser", Reason: "prefer CLI for low-context browser work", Confidence: 0.9},
		},
		EvidenceRefs: []string{"artifact_sha256:def456"},
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readEdges(t *testing.T, home string) []capgraph.Edge {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "registries", "capability-edges.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var edges []capgraph.Edge
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var edge capgraph.Edge
		if err := json.Unmarshal([]byte(line), &edge); err != nil {
			t.Fatalf("parse edge %q: %v", line, err)
		}
		edges = append(edges, edge)
	}
	return edges
}
