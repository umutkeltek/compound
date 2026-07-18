package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSupportsByHarness(t *testing.T) {
	r := DefaultRegistry()
	// Current Claude Code and Codex releases both expose blocking PreToolUse hooks.
	if !r.Supports("claude_code", "PreToolUse") {
		t.Error("claude_code should support PreToolUse block")
	}
	if !r.Supports("codex", "PreToolUse") {
		t.Error("codex should support the stable native PreToolUse block")
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
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop", "SessionEnd"} {
		if r.Supports("some-unknown-harness", event) {
			t.Errorf("generic fallback must not claim native %s support", event)
		}
	}
	if len(h.Limitations) == 0 {
		t.Error("generic harness should carry its fail-open limitation")
	}
}

func TestNamesSorted(t *testing.T) {
	got := DefaultRegistry().Names()
	want := []string{"claude_code", "codex", "hermes", "opencode"}
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Names() = %v, want %v", got, want)
		}
	}
}

func TestHermesCapabilitiesAreExplicitAndConservative(t *testing.T) {
	r := DefaultRegistry()
	hermes, found := r.Get("hermes")
	if !found || hermes.Fallback {
		t.Fatalf("hermes registry row = %+v found=%v, want explicit non-fallback", hermes, found)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "SessionEnd"} {
		if !r.Supports("hermes", event) {
			t.Errorf("hermes should support %s through its evidenced plugin lifecycle", event)
		}
	}
	if r.Supports("hermes", "Stop") {
		t.Error("hermes must not claim a native Stop feedback mapping")
	}
	if !hermes.Capabilities.NativeSkills || !hermes.Capabilities.MCP {
		t.Fatalf("hermes native capabilities = %+v, want skills + MCP", hermes.Capabilities)
	}
	for _, limitation := range []string{
		"dynamic context uses pre_llm_call; on_session_start fires after the system prompt is built",
		"plugin callback exceptions fail open",
		"native blocking requires an installed and content-attested Compound plugin",
		"on_session_end is turn/run scoped, not final chat lifetime",
		"no-agent cron and script execution requires a wrapper or action contract",
	} {
		if !hasString(hermes.Limitations, limitation) {
			t.Errorf("hermes limitations = %v, missing %q", hermes.Limitations, limitation)
		}
	}
}

func TestOpenCodeCapabilitiesDoNotOverclaimMutationOrEndEvents(t *testing.T) {
	r := DefaultRegistry()
	opencode, found := r.Get("opencode")
	if !found || opencode.Fallback {
		t.Fatalf("opencode registry row = %+v found=%v, want explicit non-fallback", opencode, found)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse"} {
		if !r.Supports("opencode", event) {
			t.Errorf("opencode should support %s", event)
		}
	}
	for _, event := range []string{"PreToolUse", "Stop", "SessionEnd"} {
		if r.Supports("opencode", event) {
			t.Errorf("opencode must not claim %s before complete permission/end-event proof", event)
		}
	}
	if !opencode.Capabilities.NativeSkills || !opencode.Capabilities.MCP {
		t.Fatalf("opencode native capabilities = %+v, want skills + MCP", opencode.Capabilities)
	}
	for _, limitation := range []string{
		"Compound context injection uses stable chat.message message.system; the system context transform remains experimental and unused",
		"tool.execute.before cannot deny; the Compound integration does not register it",
		"injection and observation callbacks fail open",
		"no proven SessionEnd or Stop mapping",
		"installed state and content revision come from installed-hooks authority, not the static harness registry",
		"not a mutation-control lane in imported capability authority",
	} {
		if !hasString(opencode.Limitations, limitation) {
			t.Errorf("opencode limitations = %v, missing %q", opencode.Limitations, limitation)
		}
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
	if !reg.Supports("codex", "PreToolUse") {
		t.Fatal("codex fixture should expose the stable native pre-tool block")
	}
	if reg.Supports("codex", "SessionEnd") || !reg.Supports("codex", "Stop") {
		t.Fatalf("codex fixture must model Stop=true and SessionEnd=false")
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
