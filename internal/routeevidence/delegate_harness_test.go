package routeevidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/work"
)

func writeDelegateTupleManifest(t *testing.T, home, providerRoute string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "registries"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `version: 1
purpose: fixture
capability_classes: [build-implement]
models:
  fixture-qwen:
    lane: fixture qwen
    harness: claude_qwen
    provider_route: ` + providerRoute + `
    tier: cheap
    packet_ceiling: multi-file
    aliases: [definitely-not-installed-qwen-wrapper]
    capabilities: {build-implement: {level: capable, evidence: fixture}}
routing_defaults:
  build-implement: [fixture-qwen]
`
	if err := os.WriteFile(ModelCapabilitiesPath(home), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveDelegateHarnessRawCommandOverridesExecutableOnly(t *testing.T) {
	home := t.TempDir()
	writeDelegateTupleManifest(t, home, "qwen")
	resolution, err := resolveDelegateHarness(work.DelegateHarnessResolveRequest{
		HomePath: home, Harness: "claude_qwen", CommandOverride: "raw-worker-tool", Now: t0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Command != "raw-worker-tool" || resolution.RequestedModelID != "fixture-qwen" || resolution.RequestedProviderRoute != "qwen" || !resolution.TupleBacked {
		t.Fatalf("raw override lost tuple identity: %+v", resolution)
	}
}

func TestResolveDelegateHarnessRawCommandCannotBypassTupleAuthority(t *testing.T) {
	home := t.TempDir()
	writeDelegateTupleManifest(t, home, "qwen")
	if _, err := resolveDelegateHarness(work.DelegateHarnessResolveRequest{
		HomePath: home, Harness: "missing-harness", CommandOverride: "raw-worker-tool", Now: t0,
	}); err == nil || !strings.Contains(err.Error(), "no Compound lane tuple") {
		t.Fatalf("raw command bypassed missing tuple: %v", err)
	}

	writeDelegateTupleManifest(t, home, "''")
	if _, err := resolveDelegateHarness(work.DelegateHarnessResolveRequest{
		HomePath: home, Harness: "claude_qwen", CommandOverride: "raw-worker-tool", Now: t0,
	}); err == nil || !strings.Contains(err.Error(), "provider_route") {
		t.Fatalf("raw command bypassed missing provider route: %v", err)
	}
}
