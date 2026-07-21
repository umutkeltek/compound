package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/harness"
)

// captureStderr mirrors captureStdout for the stderr stream — needed by
// F3 because the usage-guard branch writes to stderr and exits non-zero.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TestHarnessInstructionPackCLI_F1_JSONShape exercises the new
// `compound harness instruction-pack --harness <name> --json` verb: it must
// write $COMPOUND_HOME/context/instruction-packs/<h>/latest.json AND emit
// the rendered pack as JSON. The fixture mirrors the harness pkg's own
// buildInstructionPackFullFixture recipe so the CLI exercise faithfully
// reflects what RenderInstructionPack does end-to-end.
func TestHarnessInstructionPackCLI_F1_JSONShape(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	targetPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(targetPath, []byte(`{"theme":"dark"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	policy := harness.PermissionAuthority{
		SchemaVersion: harness.PermissionAuthoritySchemaVersion,
		Harness:       "claude_code",
		AuthorityRef:  harness.PermissionAuthorityRef("claude_code"),
		TargetPath:    targetPath,
		Permissions: harness.PermissionRules{
			Allow:       []string{"Read", "mcp__gbrain__*"},
			Deny:        []string{"Bash(rm:*)"},
			Ask:         []string{"WebFetch"},
			DefaultMode: "default",
		},
		AutoMode: harness.AutoModePolicy{Allow: []string{"$defaults", "Read"}},
	}
	body, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	authPath := harness.PermissionAuthorityPath(homePath, "claude_code")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	render, err := harness.RenderPermissions(homePath, "claude_code")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, render.Content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := harness.WriteHarnessArtifactManifest(homePath, []harness.HarnessArtifact{render.Artifact}); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		cmdHarness([]string{"instruction-pack", "--harness", "claude_code", "--json"})
	})
	var got harness.InstructionPack
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse instruction-pack JSON: %v\n%s", err, out)
	}
	if got.Harness != "claude_code" {
		t.Fatalf("harness = %q, want claude_code", got.Harness)
	}
	if got.SchemaVersion != harness.InstructionPackSchemaVersion {
		t.Fatalf("schema_version = %q, want %q", got.SchemaVersion, harness.InstructionPackSchemaVersion)
	}
	if got.PolicyCore.Absent {
		t.Fatalf("PolicyCore must be populated when authority exists: %+v", got.PolicyCore)
	}
	if got.Posture.Absent {
		t.Fatalf("Posture must be populated when an artifact exists: %+v", got.Posture)
	}

	// File on disk under temp home?
	written, err := os.Stat(harness.InstructionPackPath(homePath, "claude_code"))
	if err != nil {
		t.Fatalf("pack latest.json not written: %v", err)
	}
	if written.Size() == 0 {
		t.Fatalf("pack latest.json was empty")
	}
}

// TestHarnessInstructionPackCLI_F2_TextShape exercises the text branch —
// no --json → human-readable key=value lines and a content_sha256 banner.
func TestHarnessInstructionPackCLI_F2_TextShape(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	// Empty home — degraded render still must print all expected keys.
	out := captureStdout(t, func() {
		cmdHarness([]string{"instruction-pack", "--harness", "claude_code"})
	})
	for _, want := range []string{
		"# harness instruction-pack",
		"- harness: claude_code",
		"- schema_version:",
		"- policy_core:",
		"- posture:",
		"- installed_hooks:",
		"- effective_contract:",
		"- registry_revision:",
		"- content_sha256:",
		"- written_to:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("text output missing %q:\n%s", want, out)
		}
	}
}

// TestHarnessInstructionPackCLI_F3_UsageFailsOnEmptyHarness verifies the
// usage guard rejects missing --harness by writing a usage line to stderr
// and exiting non-zero. We use a subprocess because the in-process exit
// would terminate the test harness.
func TestHarnessInstructionPackCLI_F3_UsageFailsOnEmptyHarness(t *testing.T) {
	if os.Getenv("COMPOUND_TEST_INSTRUCTION_PACK_USAGE_CHILD") == "1" {
		homePath := t.TempDir()
		t.Setenv("COMPOUND_HOME", homePath)
		cmdHarness([]string{"instruction-pack"})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHarnessInstructionPackCLI_F3_UsageFailsOnEmptyHarness$")
	cmd.Env = append(os.Environ(), "COMPOUND_TEST_INSTRUCTION_PACK_USAGE_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("missing --harness must exit non-zero; output=%s", out)
	}
	if !strings.Contains(string(out), "usage:") {
		t.Fatalf("missing usage stderr: %s", out)
	}
}
