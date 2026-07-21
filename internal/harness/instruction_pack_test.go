package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// instructionPackTestNow is a fixed clock so GeneratedAt stays stable
// across re-runs within the same test (ContentSHA256 is the deterministic
// identity — GeneratedAt is rendered but excluded from the hash by design).
var instructionPackTestNow = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

// installedHookFixture is the minimal installed-hooks manifest shape the
// pack layer needs (we mirror the contract schema inline because the harness
// package can't import internal/contract — that direction would cycle).
type installedHookFixture struct {
	Harness            string `yaml:"harness"`
	Name               string `yaml:"name"`
	Event              string `yaml:"event,omitempty"`
	Matcher            string `yaml:"matcher,omitempty"`
	Ref                string `yaml:"ref"`
	InvocationRevision string `yaml:"invocation_revision,omitempty"`
	Revision           string `yaml:"revision"`
	Status             string `yaml:"status"`
}

// writeInstalledHooksManifest writes a minimal installed-hooks.yaml at the
// path the contract layer uses so the renderer sees a real manifest.
func writeInstalledHooksManifest(t *testing.T, homePath string, hooks []installedHookFixture) {
	t.Helper()
	path := filepath.Join(homePath, "config", "installed-hooks.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := yaml.Marshal(map[string]any{
		"schema_version": "compound.installed_hooks.v0.1",
		"hooks":          hooks,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeContractLatest writes contracts/effective/latest.json so the
// EffectiveContractRef layer has a real source to hash (we don't need the
// full schema validation here — the layer just needs a body to sha).
func writeContractLatest(t *testing.T, homePath string, body []byte) {
	t.Helper()
	path := filepath.Join(homePath, "contracts", "effective", "latest.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestInstructionPackSchemaContract pins the schema constants so a future
// bump is conscious.
func TestInstructionPackSchemaContract(t *testing.T) {
	if InstructionPackSchemaVersion == "" {
		t.Fatal("InstructionPackSchemaVersion must be set")
	}
	if !strings.HasPrefix(InstructionPackSchemaVersion, "compound.instruction_pack.") {
		t.Fatalf("InstructionPackSchemaVersion = %q, want compound.instruction_pack.*", InstructionPackSchemaVersion)
	}
}

// F1 — full render: every layer populated, file lands at the harness-scoped
// latest.json under context/instruction-packs/<h>/latest.json.
func TestRenderInstructionPack_F1_AllLayersPopulated(t *testing.T) {
	homePath, _, _ := buildInstructionPackFullFixture(t)

	writeInstalledHooksManifest(t, homePath, []installedHookFixture{{
		Harness: "claude_code", Name: "session-start",
		Ref:      "compound://hooks/session-start",
		Revision: "rev_alpha", Status: "active",
	}})
	contractBody := []byte(`{"schema_version":"compound.effective_contract.v0.1","header":{"id":"contract_seed"},"harness":"claude_code","active_instructions":[],"active_decisions":[]}` + "\n")
	writeContractLatest(t, homePath, contractBody)

	pack, err := RenderInstructionPack(homePath, "claude_code", instructionPackTestNow)
	if err != nil {
		t.Fatalf("RenderInstructionPack: %v", err)
	}
	if pack.Harness != "claude_code" {
		t.Fatalf("harness = %q, want claude_code", pack.Harness)
	}
	if pack.SchemaVersion != InstructionPackSchemaVersion {
		t.Fatalf("schema_version = %q, want %q", pack.SchemaVersion, InstructionPackSchemaVersion)
	}
	if pack.PolicyCore.Absent {
		t.Fatalf("PolicyCore must be populated when authority exists: %+v", pack.PolicyCore)
	}
	if pack.PolicyCore.AuthorityRef == "" || pack.PolicyCore.SourceRevision == "" || pack.PolicyCore.ContentSHA256 == "" {
		t.Fatalf("PolicyCore must carry ref+revision+sha: %+v", pack.PolicyCore)
	}
	if pack.Posture.Absent {
		t.Fatalf("Posture must be populated when an artifact exists: %+v", pack.Posture)
	}
	if pack.Posture.AuthorityRef == "" || pack.Posture.SourceRevision == "" || pack.Posture.ContentSHA256 == "" {
		t.Fatalf("Posture must carry ref+revision+sha: %+v", pack.Posture)
	}
	if pack.Roster.RegistryRevision == "" {
		t.Fatalf("RegistryRevision must be populated: %+v", pack.Roster)
	}
	if pack.Roster.InstalledHooks.Absent {
		t.Fatalf("InstalledHooks layer must populate when manifest exists: %+v", pack.Roster.InstalledHooks)
	}
	if pack.Roster.InstalledHooks.Ref == "" || pack.Roster.InstalledHooks.Revision == "" {
		t.Fatalf("InstalledHooks must carry ref+revision: %+v", pack.Roster.InstalledHooks)
	}
	if pack.Roster.EffectiveContract.Absent {
		t.Fatalf("EffectiveContract layer must populate when latest.json exists: %+v", pack.Roster.EffectiveContract)
	}
	if pack.Roster.EffectiveContract.Ref == "" || pack.Roster.EffectiveContract.Revision == "" {
		t.Fatalf("EffectiveContract must carry ref+revision: %+v", pack.Roster.EffectiveContract)
	}
	if pack.ContentSHA256 == "" {
		t.Fatalf("ContentSHA256 must be set on the pack: %+v", pack)
	}
	if pack.GeneratedAt.IsZero() {
		t.Fatalf("GeneratedAt must reflect the supplied now: %+v", pack)
	}

	if _, err := os.Stat(InstructionPackPath(homePath, "claude_code")); err != nil {
		t.Fatalf("pack latest.json must exist on disk: %v", err)
	}
}

// F2 — degraded render: empty home → no inputs → pack still renders; every
// layer carries the absent marker; no error returned (fail-open falsifier).
func TestRenderInstructionPack_F2_DegradedRender(t *testing.T) {
	homePath := t.TempDir()
	pack, err := RenderInstructionPack(homePath, "claude_code", instructionPackTestNow)
	if err != nil {
		t.Fatalf("RenderInstructionPack must not error on empty home: %v", err)
	}
	if !pack.PolicyCore.Absent {
		t.Fatalf("PolicyCore must be marked absent on empty home: %+v", pack.PolicyCore)
	}
	if !pack.Posture.Absent {
		t.Fatalf("Posture must be marked absent on empty home: %+v", pack.Posture)
	}
	if pack.Roster.RegistryRevision == "" {
		t.Fatalf("RegistryRevision must still resolve (default registry, no inputs needed): %+v", pack.Roster)
	}
	if !pack.Roster.InstalledHooks.Absent {
		t.Fatalf("InstalledHooks layer must mark absent: %+v", pack.Roster.InstalledHooks)
	}
	if !pack.Roster.EffectiveContract.Absent {
		t.Fatalf("EffectiveContract layer must mark absent: %+v", pack.Roster.EffectiveContract)
	}
	if pack.ContentSHA256 == "" {
		t.Fatalf("ContentSHA256 must be set even on degraded render: %+v", pack)
	}

	if _, err := os.Stat(InstructionPackPath(homePath, "claude_code")); err != nil {
		t.Fatalf("pack latest.json must still be written on degraded render: %v", err)
	}
}

// F3 — identity stability: two renders over identical inputs must yield
// identical ContentSHA256 regardless of GeneratedAt. Touching an UNRELATED
// file in the home must not perturb the hash. Bumping the permission
// authority yaml (new source_revision) MUST change the hash.
func TestRenderInstructionPack_F3_StableIdentity(t *testing.T) {
	homePath := t.TempDir()
	targetPath := filepath.Join(t.TempDir(), "settings.json")
	policy := permissionFixture([]string{"Read", "mcp__gbrain__*"})
	writePermissionFixture(t, homePath, targetPath, policy)
	if err := os.WriteFile(targetPath, []byte(`{"theme":"dark"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Make sure the harness-artifact manifest is populated so a re-render
	// has a posture-layer source to hash.
	render, err := RenderPermissions(homePath, "claude_code")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, render.Content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteHarnessArtifactManifest(homePath, []HarnessArtifact{render.Artifact}); err != nil {
		t.Fatal(err)
	}

	first, err := RenderInstructionPack(homePath, "claude_code", instructionPackTestNow)
	if err != nil {
		t.Fatal(err)
	}
	other := instructionPackTestNow.Add(7 * time.Hour)
	second, err := RenderInstructionPack(homePath, "claude_code", other)
	if err != nil {
		t.Fatal(err)
	}
	if first.ContentSHA256 != second.ContentSHA256 {
		t.Fatalf("identical inputs must yield identical ContentSHA256: %q vs %q", first.ContentSHA256, second.ContentSHA256)
	}
	if first.GeneratedAt.Equal(second.GeneratedAt) {
		t.Fatalf("GeneratedAt must reflect distinct clock values: %v vs %v", first.GeneratedAt, second.GeneratedAt)
	}

	// Touch an unrelated file in the home — must not change the hash.
	unrelated := filepath.Join(homePath, "config", "unrelated.txt")
	if err := os.MkdirAll(filepath.Dir(unrelated), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelated, []byte("harmless"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := RenderInstructionPack(homePath, "claude_code", instructionPackTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if third.ContentSHA256 != first.ContentSHA256 {
		t.Fatalf("unrelated file change must not perturb hash: %q vs %q", third.ContentSHA256, first.ContentSHA256)
	}

	// Bump authority yaml (new source_revision) — must change hash.
	policyBumped := permissionFixture([]string{"Read", "mcp__gbrain__*", "WebFetch"})
	writePermissionFixture(t, homePath, targetPath, policyBumped)
	fourth, err := RenderInstructionPack(homePath, "claude_code", instructionPackTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if fourth.ContentSHA256 == first.ContentSHA256 {
		t.Fatalf("authority bump must change ContentSHA256, got identical: %q", first.ContentSHA256)
	}
}

// F4 — write-then-roundtrip: the JSON written to disk parses back to a
// pack with identical fields (including ContentSHA256).
func TestRenderInstructionPack_F4_RoundtripJSON(t *testing.T) {
	homePath, _, _ := buildInstructionPackFullFixture(t)

	written, err := RenderInstructionPack(homePath, "claude_code", instructionPackTestNow)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(InstructionPackPath(homePath, "claude_code"))
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip InstructionPack
	if err := json.Unmarshal(body, &roundtrip); err != nil {
		t.Fatalf("unmarshal written pack: %v\n%s", err, body)
	}
	if roundtrip.Harness != written.Harness {
		t.Fatalf("roundtrip harness = %q, want %q", roundtrip.Harness, written.Harness)
	}
	if roundtrip.PolicyCore.AuthorityRef != written.PolicyCore.AuthorityRef {
		t.Fatalf("roundtrip PolicyCore.AuthorityRef = %q, want %q", roundtrip.PolicyCore.AuthorityRef, written.PolicyCore.AuthorityRef)
	}
	if roundtrip.ContentSHA256 != written.ContentSHA256 {
		t.Fatalf("roundtrip ContentSHA256 = %q, want %q", roundtrip.ContentSHA256, written.ContentSHA256)
	}
}

// F5 — InstalledHooksRevision tracks the actual installed-hooks content
// digest (the same way PermissionDrift's posture hash tracks content).
func TestRenderInstructionPack_F5_InstalledHooksLayer(t *testing.T) {
	homePath, _, _ := buildInstructionPackFullFixture(t)

	writeInstalledHooksManifest(t, homePath, []installedHookFixture{{
		Harness: "claude_code", Name: "session-start",
		Ref:      "compound://hooks/session-start",
		Revision: "rev_one", Status: "active",
	}})
	first, err := RenderInstructionPack(homePath, "claude_code", instructionPackTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if first.Roster.InstalledHooks.Absent {
		t.Fatalf("InstalledHooks must populate when manifest exists: %+v", first.Roster.InstalledHooks)
	}
	if first.Roster.InstalledHooks.Ref != filepath.Join("config", "installed-hooks.yaml") {
		t.Fatalf("InstalledHooks.Ref = %q, want config/installed-hooks.yaml", first.Roster.InstalledHooks.Ref)
	}

	// Re-render with a manifest that has DIFFERENT revisions — revision sha must change.
	writeInstalledHooksManifest(t, homePath, []installedHookFixture{{
		Harness: "claude_code", Name: "session-start",
		Ref:      "compound://hooks/session-start",
		Revision: "rev_two", Status: "active",
	}})
	second, err := RenderInstructionPack(homePath, "claude_code", instructionPackTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if second.Roster.InstalledHooks.Revision == first.Roster.InstalledHooks.Revision {
		t.Fatalf("InstalledHooks.Revision must change when manifest content changes: %q vs %q", first.Roster.InstalledHooks.Revision, second.Roster.InstalledHooks.Revision)
	}
}

// F6 — EffectiveContractRef resolves from contracts/effective/latest.json
// (the canonical write point) and the revision sha tracks its content.
func TestRenderInstructionPack_F6_EffectiveContractLayer(t *testing.T) {
	homePath, _, _ := buildInstructionPackFullFixture(t)

	body := []byte(`{"schema_version":"compound.effective_contract.v0.1","header":{"id":"contract_seed"},"harness":"claude_code","active_instructions":[],"active_decisions":[]}` + "\n")
	writeContractLatest(t, homePath, body)
	first, err := RenderInstructionPack(homePath, "claude_code", instructionPackTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if first.Roster.EffectiveContract.Absent {
		t.Fatalf("EffectiveContract must populate when latest.json exists: %+v", first.Roster.EffectiveContract)
	}
	if first.Roster.EffectiveContract.Ref != filepath.Join("contracts", "effective", "latest.json") {
		t.Fatalf("EffectiveContract.Ref = %q, want contracts/effective/latest.json", first.Roster.EffectiveContract.Ref)
	}

	bodyB := []byte(`{"schema_version":"compound.effective_contract.v0.1","header":{"id":"contract_seed"},"harness":"claude_code","active_instructions":["Bump"],"active_decisions":[]}` + "\n")
	writeContractLatest(t, homePath, bodyB)
	second, err := RenderInstructionPack(homePath, "claude_code", instructionPackTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if second.Roster.EffectiveContract.Revision == first.Roster.EffectiveContract.Revision {
		t.Fatalf("EffectiveContract.Revision must change when latest.json content changes: %q vs %q", first.Roster.EffectiveContract.Revision, second.Roster.EffectiveContract.Revision)
	}
	if second.ContentSHA256 == first.ContentSHA256 {
		t.Fatalf("ContentSHA256 must change when latest.json content changes")
	}
}

// F7 — byte-identical re-render: identical inputs across two renders →
// identical written files; the body does NOT include GeneratedAt variance
// since the test holds the clock constant.
func TestRenderInstructionPack_F7_IdempotentWrite(t *testing.T) {
	homePath, _, _ := buildInstructionPackFullFixture(t)

	if _, err := RenderInstructionPack(homePath, "claude_code", instructionPackTestNow); err != nil {
		t.Fatal(err)
	}
	bodyA, err := os.ReadFile(InstructionPackPath(homePath, "claude_code"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RenderInstructionPack(homePath, "claude_code", instructionPackTestNow); err != nil {
		t.Fatal(err)
	}
	bodyB, err := os.ReadFile(InstructionPackPath(homePath, "claude_code"))
	if err != nil {
		t.Fatal(err)
	}
	if string(bodyA) != string(bodyB) {
		t.Fatalf("identical inputs must write byte-identical files:\n%s\n%s", bodyA, bodyB)
	}
}

// buildInstructionPackFullFixture writes an authority + target file +
// HarnessArtifact manifest, returning the (homePath, targetPath, render) so
// per-test extensions (installed-hooks, contract-latest) can layer on top.
// Mirrors buildDriftFixture's discipline so F1/F4/F5/F6/F7 all exercise the
// SAME red/green states the prior posture-packet already proved.
func buildInstructionPackFullFixture(t *testing.T) (homePath, targetPath string, render PermissionRender) {
	t.Helper()
	homePath, targetPath = t.TempDir(), filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(targetPath, []byte(`{"theme":"dark"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	policy := permissionFixture([]string{"Read", "mcp__gbrain__*"})
	writePermissionFixture(t, homePath, targetPath, policy)
	render, err := RenderPermissions(homePath, "claude_code")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, render.Content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteHarnessArtifactManifest(homePath, []HarnessArtifact{render.Artifact}); err != nil {
		t.Fatal(err)
	}
	return homePath, targetPath, render
}
