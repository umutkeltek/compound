package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSupportsByHarness(t *testing.T) {
	r := DefaultRegistry()
	// claude_code can block a tool call; codex cannot (must be enforced via wrapper).
	if !r.Supports("claude_code", "PreToolUse") {
		t.Error("claude_code should support PreToolUse block")
	}
	if r.Supports("codex", "PreToolUse") {
		t.Error("codex should NOT be assumed to block a tool call natively")
	}
	// claude_code has a real SessionEnd event; codex has none (Stop is its only
	// end-of-turn hook) — conflating them would silently overclaim a receipt codex
	// never delivers.
	if !r.Supports("claude_code", "SessionEnd") {
		t.Error("claude_code should support session-end receipts")
	}
	if r.Supports("codex", "SessionEnd") {
		t.Error("codex should NOT claim session-end receipts — no such event exists")
	}
	// Both can inject/continue at end-of-turn via Stop (codex via JSON block/continuation).
	if !r.Supports("claude_code", "Stop") || !r.Supports("codex", "Stop") {
		t.Error("both should support stop feedback/continuation")
	}
}

// TestStopAndSessionEndCapabilitiesAreIndependent proves the registry no longer
// conflates the two events: mutating one capability must not move the other.
func TestStopAndSessionEndCapabilitiesAreIndependent(t *testing.T) {
	reg := Registry{Harnesses: map[string]Harness{
		"only_stop": {
			Name:                 "only_stop",
			InstructionMechanism: "test fixture",
			Capabilities:         Capabilities{StopFeedback: true, SessionEndReceipt: false},
		},
		"only_session_end": {
			Name:                 "only_session_end",
			InstructionMechanism: "test fixture",
			Capabilities:         Capabilities{StopFeedback: false, SessionEndReceipt: true},
		},
	}}
	if !reg.Supports("only_stop", "Stop") {
		t.Error("only_stop should support Stop")
	}
	if reg.Supports("only_stop", "SessionEnd") {
		t.Error("only_stop should NOT support SessionEnd — mutating StopFeedback leaked into SessionEndReceipt")
	}
	if !reg.Supports("only_session_end", "SessionEnd") {
		t.Error("only_session_end should support SessionEnd")
	}
	if reg.Supports("only_session_end", "Stop") {
		t.Error("only_session_end should NOT support Stop — mutating SessionEndReceipt leaked into StopFeedback")
	}
}

func TestGenericFallback(t *testing.T) {
	r := DefaultRegistry()
	h, ok := r.Get("some-unknown-harness")
	if ok {
		t.Error("unknown harness should not be found")
	}
	if !h.Fallback {
		t.Error("fallback flag should be set on the generic stand-in")
	}
	if r.Supports("some-unknown-harness", "PreToolUse") {
		t.Error("generic fallback must not claim to block tool calls (fail-open)")
	}
	if len(h.Limitations) == 0 {
		t.Error("generic harness should carry its fail-open limitation")
	}
}

func TestNamesSorted(t *testing.T) {
	got := DefaultRegistry().Names()
	if len(got) != 2 || got[0] != "claude_code" || got[1] != "codex" {
		t.Errorf("Names() = %v, want sorted [claude_code codex]", got)
	}
}

func TestLoadRegistryManifest(t *testing.T) {
	manifest, err := LoadRegistryManifest(filepath.Join("..", "..", "fixtures", "harness", "harness-registry.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != RegistryManifestSchemaVersion || manifest.ID != "fixture-harness-registry" {
		t.Fatalf("manifest identity = %q/%q", manifest.SchemaVersion, manifest.ID)
	}
	reg, err := manifest.Registry()
	if err != nil {
		t.Fatal(err)
	}
	if !reg.Supports("claude_code", "PreToolUse") {
		t.Fatal("claude_code should support pre-tool block from manifest")
	}
	if reg.Supports("codex", "PreToolUse") {
		t.Fatal("codex fixture should not claim pre-tool block")
	}
	if got := RegistryRevision(reg); !strings.HasPrefix(got, "sha256:") {
		t.Fatalf("registry revision = %q", got)
	}
}

func TestRegistryManifestRejectsDuplicateHarness(t *testing.T) {
	manifest := RegistryManifest{
		SchemaVersion: RegistryManifestSchemaVersion,
		Harnesses: []ManifestHarness{
			{Name: "codex", InstructionMechanism: "AGENTS.md"},
			{Name: "codex", InstructionMechanism: "AGENTS.md"},
		},
	}
	if _, err := manifest.Registry(); err == nil || !strings.Contains(err.Error(), "duplicate harness") {
		t.Fatalf("duplicate harness err = %v", err)
	}
}

func TestRegistryManifestRejectsEmptyHarnessName(t *testing.T) {
	manifest := RegistryManifest{
		SchemaVersion: RegistryManifestSchemaVersion,
		Harnesses:     []ManifestHarness{{InstructionMechanism: "AGENTS.md"}},
	}
	if _, err := manifest.Registry(); err == nil || !strings.Contains(err.Error(), "without name") {
		t.Fatalf("empty harness err = %v", err)
	}
}

func TestRegistryManifestRejectsWrongSchema(t *testing.T) {
	manifest := RegistryManifest{
		SchemaVersion: "compound.harness_registry.v9",
		Harnesses:     []ManifestHarness{{Name: "codex", InstructionMechanism: "AGENTS.md"}},
	}
	if _, err := manifest.Registry(); err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("wrong schema err = %v", err)
	}
}

func TestLoadRegistryManifestDefaultsSourceRef(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.yaml")
	data := []byte(`schema_version: compound.harness_registry.v0.1
harnesses:
  - name: codex
    instruction_mechanism: AGENTS.md
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadRegistryManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SourceRef.Path != path || !strings.HasPrefix(manifest.SourceRef.Revision, "sha256:") || len(manifest.EvidenceRefs) != 1 {
		t.Fatalf("defaulted source ref/evidence = %+v refs=%v", manifest.SourceRef, manifest.EvidenceRefs)
	}
}

func TestLoadOSEntrypointsManifest(t *testing.T) {
	manifest, err := LoadOSEntrypointsManifest(filepath.Join("..", "..", "fixtures", "harness", "os-harness-entrypoints.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != RegistryManifestSchemaVersion || manifest.ID != "os-harness-entrypoints" || manifest.AdapterID != OSEntrypointsAdapterID {
		t.Fatalf("manifest identity = %q/%q/%q", manifest.SchemaVersion, manifest.ID, manifest.AdapterID)
	}
	if manifest.SourceRef.SourceClass != "os-harness-entrypoints" || !strings.HasPrefix(manifest.SourceRef.Revision, "sha256:") {
		t.Fatalf("source ref = %+v", manifest.SourceRef)
	}
	reg, err := manifest.Registry()
	if err != nil {
		t.Fatal(err)
	}
	if !reg.Supports("claude_code", "SessionStart") || !reg.Supports("codex", "SessionStart") {
		t.Fatalf("entrypoint adapter should mark session-start context available")
	}
	if reg.Supports("claude_code", "PreToolUse") || reg.Supports("codex", "PostToolUse") || reg.Supports("hermes", "SessionEnd") {
		t.Fatalf("OS entrypoint adapter should not infer hook/MCP/receipt capabilities")
	}
	claude, ok := reg.Get("claude_code")
	if !ok {
		t.Fatalf("claude source id should canonicalize to claude_code")
	}
	if !strings.Contains(claude.InstructionMechanism, "entrypoint:") || !hasString(claude.Limitations, "source_id=claude") {
		t.Fatalf("claude manifest row = %+v", claude)
	}
}

func TestLoadOSEntrypointsManifestRejectsEmptyEntrypoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness-entrypoints.yaml")
	data := []byte(`version: 2
harnesses:
  codex:
    status: generated_applied
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOSEntrypointsManifest(path); err == nil || !strings.Contains(err.Error(), "needs entrypoint") {
		t.Fatalf("empty entrypoint err = %v", err)
	}
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
