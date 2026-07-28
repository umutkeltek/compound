package routeevidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeCF(t *testing.T, root, id, route string, aliases map[string]string) {
	t.Helper()
	cf := conformanceFile{Schema: "switchback/profile-conformance@1", ConformanceRevision: "sha256:" + id, Profile: conformanceProfile{ID: id, Harness: "claude-code", Route: route, RequestedModel: route, RequestedEffort: "xhigh", ModelAliases: aliases}}
	b, _ := json.Marshal(cf)
	if err := os.WriteFile(filepath.Join(root, id+".json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestPlanRouteExperimentSeparatesRoleAliasFromObservation(t *testing.T) {
	root := t.TempDir()
	writeCF(t, root, "claude-gpt", "codex/gpt-5.6-sol", map[string]string{"opus": "codex/gpt-5.6-sol"})
	writeCF(t, root, "claude-minimax", "minimax/MiniMax-M3", map[string]string{"opus": "MiniMax-M3[1m]"})
	p, err := PlanRouteExperiment(root, "opus", []string{"claude-minimax", "claude-gpt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Lanes) != 2 || p.Lanes[0].ProfileID != "claude-gpt" {
		t.Fatalf("lanes not deterministic: %#v", p.Lanes)
	}
	if p.Lanes[1].RequestedAlias != "MiniMax-M3[1m]" || p.Lanes[1].ObservedModel != "" || p.Lanes[1].EvidenceStatus != "unknown_until_route_evidence" {
		t.Fatalf("role/observation conflated: %#v", p.Lanes[1])
	}
}
func TestPlanRouteExperimentFailsMissingAlias(t *testing.T) {
	root := t.TempDir()
	writeCF(t, root, "a", "x/a", map[string]string{})
	writeCF(t, root, "b", "x/b", map[string]string{"opus": "b"})
	if _, err := PlanRouteExperiment(root, "opus", []string{"a", "b"}); err == nil {
		t.Fatal("expected missing alias refusal")
	}
}
