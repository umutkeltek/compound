package authority

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/capgraph"
	"compound/internal/home"
	"compound/internal/routeevidence"
)

type osCapabilityProofFixture struct {
	disabledExitZero      bool
	disabledAuthorityRefs []string
	projectionPoison      string
	entrypointPoison      string
	wrapperOfferID        string
	legacyDispatch        bool
	resolutionMutator     func(map[string]any)
}

type installedOSCapabilityProofFixture struct {
	compoundHome string
}

func installOSCapabilityProofFixture(t *testing.T, opts osCapabilityProofFixture) installedOSCapabilityProofFixture {
	t.Helper()
	root := t.TempDir()
	userHome := filepath.Join(root, "home")
	compoundHome := filepath.Join(root, "compound-home")
	binDir := filepath.Join(root, "bin")
	osRoot := filepath.Join(userHome, "Projects", "operating-system", "umut-os")
	execLane := filepath.Join(osRoot, "exec-lane")
	for _, dir := range []string{filepath.Join(userHome, ".local", "bin"), filepath.Join(userHome, ".bun", "bin"), binDir, execLane} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := home.Init(compoundHome, "test", time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	dispatch := `else if (cmd === "capability") exit = await runCapabilityProxy(rest);`
	if opts.legacyDispatch {
		dispatch = `else if (cmd === "capability") exit = await runCapability(rest);`
	}
	entrypoint := `#!/usr/bin/env bun
import { runCapabilityProxy } from "./os-capability-proxy";
const [cmd, ...rest] = process.argv.slice(2);
let exit = 0;
` + dispatch + `
process.exit(exit);
` + opts.entrypointPoison + "\n"
	writeExecutableFixture(t, filepath.Join(execLane, "os.ts"), entrypoint)
	projection := `import { spawnSync } from "node:child_process";
const compoundArgs = ["capability", "resolve", "--json"];
function validateResolution() { return true; }
export async function runCapabilityProxy() {
  const result = spawnSync("compound", compoundArgs);
  if (result.status !== 0) return 2;
  return validateResolution() ? 0 : 2;
}
` + opts.projectionPoison + "\n"
	writeExecutableFixture(t, filepath.Join(execLane, "os-capability-proxy.ts"), projection)

	shim := `#!/bin/sh
exec "${HOME}/.bun/bin/bun" "${HOME}/Projects/operating-system/umut-os/exec-lane/os.ts" "$@"
`
	writeExecutableFixture(t, filepath.Join(userHome, ".local", "bin", "os"), shim)

	direct := osCapabilityResolutionFixture("tool:compound.proof-check")
	wrapperOfferID := opts.wrapperOfferID
	if wrapperOfferID == "" {
		wrapperOfferID = "tool:compound.proof-check"
	}
	wrapper := osCapabilityResolutionFixture(wrapperOfferID)
	if opts.resolutionMutator != nil {
		opts.resolutionMutator(direct)
		opts.resolutionMutator(wrapper)
	}
	directBody, err := json.Marshal(direct)
	if err != nil {
		t.Fatal(err)
	}
	wrapperBody, err := json.Marshal(wrapper)
	if err != nil {
		t.Fatal(err)
	}

	compoundScript := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "version" ]; then
  echo "compound 0.1.0 (build abc1234)"
  exit 0
fi
if [ "$1" = "capability" ] && [ "$2" = "resolve" ]; then
  cat <<'JSON'
%s
JSON
  exit 0
fi
exit 2
`, directBody)
	writeExecutableFixture(t, filepath.Join(binDir, "compound"), compoundScript)

	disabledExit := 2
	disabledAuthorityRefs := opts.disabledAuthorityRefs
	if disabledAuthorityRefs == nil {
		disabledAuthorityRefs = []string{
			routeevidence.CapabilityTaskClassifierAuthorityRef,
			capgraph.CapabilityGraphAuthorityRef,
			routeevidence.ModelCapabilitiesAuthorityRef,
		}
	}
	disabledReport := map[string]any{
		"schema_version": "os.capability_proxy.v0.1",
		"status":         "unavailable",
		"task":           osCapabilityProbeTask,
		"authority_refs": disabledAuthorityRefs,
		"error":          "disabled",
	}
	disabledBytes, err := json.Marshal(disabledReport)
	if err != nil {
		t.Fatal(err)
	}
	disabledBody := string(disabledBytes)
	if opts.disabledExitZero {
		disabledExit = 0
		disabledBody = string(wrapperBody)
	}
	bunScript := fmt.Sprintf(`#!/bin/sh
if [ "${OS_COMPOUND_CLI:-}" = "off" ]; then
  cat <<'JSON'
%s
JSON
  exit %d
fi
cat <<'JSON'
%s
JSON
exit 0
`, disabledBody, disabledExit, wrapperBody)
	writeExecutableFixture(t, filepath.Join(userHome, ".bun", "bin", "bun"), bunScript)

	t.Setenv("HOME", userHome)
	t.Setenv("PATH", filepath.Join(userHome, ".local", "bin")+string(os.PathListSeparator)+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return installedOSCapabilityProofFixture{compoundHome: compoundHome}
}

func doctorOSCapabilityRow(t *testing.T, homePath string) OSAbsorptionDoctorRow {
	t.Helper()
	matrixRow := row("os_source_os_capability_cli", "cli:os capability", "wrapper", capgraph.CapabilityGraphAuthorityRef, "os capability wrapper and compound capability resolve", "")
	matrixRow.SourceKind = "compatibility_cli"
	matrixRow.CurrentRole = "compatibility_wrapper_over_compound_capability_and_router"
	matrixRow.ProofMode = osAbsorptionProofModeOSCapability
	matrixRow.EvidenceRefs = []string{
		routeevidence.CapabilityTaskClassifierAuthorityRef,
		capgraph.CapabilityGraphAuthorityRef,
		routeevidence.ModelCapabilitiesAuthorityRef,
	}
	matrixRow.RetirementGate = "os capability performs no independent authority read"
	matrixPath := filepath.Join(t.TempDir(), "matrix.json")
	writeJSONFixture(t, matrixPath, OSAbsorptionSourceMatrix{SchemaVersion: OSAbsorptionMatrixSchema, Rows: []OSAbsorptionMatrixRow{matrixRow}})
	if _, err := ImportOSAbsorptionMatrix(homePath, matrixPath, time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	report, err := OSAbsorptionDoctor(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("doctor rows = %+v", report.Rows)
	}
	return report.Rows[0]
}

func osCapabilityResolutionFixture(offerID string) map[string]any {
	const task = "implement a bounded Go authority proof"
	return map[string]any{
		"schema_version":    "compound.capability_resolution.v0.1",
		"status":            "ok",
		"task":              task,
		"privacy_sensitive": false,
		"classification": map[string]any{
			"schema_version":      routeevidence.CapabilityTaskClassifierSchemaVersion,
			"authority_ref":       routeevidence.CapabilityTaskClassifierAuthorityRef,
			"authority_revision":  "sha256:" + repeatHex("b", 64),
			"rule_id":             "build-implement",
			"capability_class":    "build-implement",
			"deterministic_first": "reproduce/run tests before LLM code work",
			"evaluation_method":   "tests + diff + runtime verification",
		},
		"capability_surface": map[string]any{
			"schema_version":      capgraph.SurfaceSchemaVersion,
			"authority_ref":       capgraph.CapabilityGraphAuthorityRef,
			"authority_revision":  "sha256:" + repeatHex("c", 12),
			"status":              "ok",
			"task":                task,
			"node_count":          1,
			"edge_count":          0,
			"source_record_count": 1,
			"offer_count":         1,
			"offer_limit":         12,
			"offers": []any{map[string]any{
				"node":  map[string]any{"id": offerID, "kind": "tool", "name": "compound proof-check"},
				"score": 4.2,
				"why":   "fixture",
			}},
			"offer_summaries": []any{},
		},
		"route_plan": map[string]any{
			"schema_version":      routeevidence.CapabilityRoutePlanSchemaVersion,
			"id":                  "crp_fixture",
			"created_at":          "2026-07-11T00:00:00Z",
			"status":              "planned",
			"authority_ref":       routeevidence.ModelCapabilitiesAuthorityRef,
			"authority_path":      "/synthetic/model-capabilities.yaml",
			"source_authority_id": "mrcsrc_fixture",
			"source_revision":     "sha256:" + repeatHex("a", 64),
			"capability_class":    "build-implement",
			"privacy_sensitive":   false,
			"routing_rule":        "routing_defaults[build-implement]",
			"lane_justification":  []string{"fixture"},
			"chain": []any{map[string]any{
				"position": 0, "model_id": "codex", "lane": "codex", "tier": "premium", "claim_level": "capable", "gate_role": "eligible", "justification": "fixture",
			}},
			"route_lane_limit":          16,
			"policy_omitted_lane_limit": 16,
			"warning_limit":             16,
		},
		"authority_refs": []string{
			routeevidence.CapabilityTaskClassifierAuthorityRef,
			capgraph.CapabilityGraphAuthorityRef,
			routeevidence.ModelCapabilitiesAuthorityRef,
		},
	}
}

func TestParseOSCapabilityResolutionAcceptsAdditiveReorderedAuthorityEvidence(t *testing.T) {
	report := osCapabilityResolutionFixture("tool:compound.proof-check")
	report["authority_refs"] = []string{
		"future://additive-proof",
		routeevidence.ModelCapabilitiesAuthorityRef,
		capgraph.CapabilityGraphAuthorityRef,
		routeevidence.CapabilityTaskClassifierAuthorityRef,
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseOSCapabilityResolution(body); err != nil {
		t.Fatalf("additive reordered authority evidence should parse: %v", err)
	}
}

func TestParseOSCapabilityResolutionRejectsUnboundNestedAuthorities(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing classifier top ref", mutate: func(report map[string]any) {
			report["authority_refs"] = []string{capgraph.CapabilityGraphAuthorityRef, routeevidence.ModelCapabilitiesAuthorityRef}
		}},
		{name: "classifier schema", mutate: func(report map[string]any) { report["classification"].(map[string]any)["schema_version"] = "forged" }},
		{name: "classifier ref", mutate: func(report map[string]any) { report["classification"].(map[string]any)["authority_ref"] = "forged" }},
		{name: "classifier revision", mutate: func(report map[string]any) {
			report["classification"].(map[string]any)["authority_revision"] = "sha256:short"
		}},
		{name: "classifier class", mutate: func(report map[string]any) { report["classification"].(map[string]any)["capability_class"] = "" }},
		{name: "classifier rule", mutate: func(report map[string]any) { report["classification"].(map[string]any)["rule_id"] = "" }},
		{name: "graph schema", mutate: func(report map[string]any) {
			report["capability_surface"].(map[string]any)["schema_version"] = "forged"
		}},
		{name: "graph ref", mutate: func(report map[string]any) { report["capability_surface"].(map[string]any)["authority_ref"] = "forged" }},
		{name: "graph revision", mutate: func(report map[string]any) {
			report["capability_surface"].(map[string]any)["authority_revision"] = "sha256:short"
		}},
		{name: "graph status", mutate: func(report map[string]any) { report["capability_surface"].(map[string]any)["status"] = "draft" }},
		{name: "router schema", mutate: func(report map[string]any) { report["route_plan"].(map[string]any)["schema_version"] = "forged" }},
		{name: "router ref", mutate: func(report map[string]any) { report["route_plan"].(map[string]any)["authority_ref"] = "forged" }},
		{name: "router source id", mutate: func(report map[string]any) { report["route_plan"].(map[string]any)["source_authority_id"] = "" }},
		{name: "router revision", mutate: func(report map[string]any) { report["route_plan"].(map[string]any)["source_revision"] = "sha256:short" }},
		{name: "router status", mutate: func(report map[string]any) { report["route_plan"].(map[string]any)["status"] = "draft" }},
		{name: "router class mismatch", mutate: func(report map[string]any) {
			report["route_plan"].(map[string]any)["capability_class"] = "judge-critique"
		}},
		{name: "empty route", mutate: func(report map[string]any) { report["route_plan"].(map[string]any)["chain"] = []any{} }},
		{name: "oversized route", mutate: func(report map[string]any) {
			lane := report["route_plan"].(map[string]any)["chain"].([]any)[0]
			report["route_plan"].(map[string]any)["chain"] = make([]any, 17)
			for index := range report["route_plan"].(map[string]any)["chain"].([]any) {
				report["route_plan"].(map[string]any)["chain"].([]any)[index] = lane
			}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := osCapabilityResolutionFixture("tool:compound.proof-check")
			tc.mutate(report)
			body, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parseOSCapabilityResolution(body); err == nil {
				t.Fatal("malformed authority binding should fail")
			}
		})
	}
}

func writeExecutableFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func repeatHex(value string, count int) string {
	out := ""
	for len(out) < count {
		out += value
	}
	return out[:count]
}
