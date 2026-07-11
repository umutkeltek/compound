package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPermissionAuthorityValidation(t *testing.T) {
	tests := []struct {
		name, wantErr string
		policy        PermissionAuthority
	}{
		{"rejects global MCP wildcard", "mcp__*", permissionFixture([]string{"mcp__*"})},
		{"accepts literal server wildcard", "", permissionFixture([]string{"mcp__gbrain__*"})},
		{"rejects duplicates", "duplicate", permissionFixture([]string{"Read", "Read"})},
	}
	badDefaults := permissionFixture([]string{"Read"})
	badDefaults.AutoMode.Allow = []string{"Read", "$defaults"}
	tests = append(tests, struct {
		name, wantErr string
		policy        PermissionAuthority
	}{"requires defaults first", "$defaults", badDefaults})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.Validate()
			if tt.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadPermissionAuthorityRejectsWrongHarnessFile(t *testing.T) {
	homePath := t.TempDir()
	policy := permissionFixture([]string{"Read"})
	policy.Harness = "codex"
	policy.AuthorityRef = PermissionAuthorityRef("codex")
	writeTestJSON(t, PermissionAuthorityPath(homePath, "claude_code"), policy)
	_, _, err := LoadPermissionAuthority(homePath, "claude_code")
	if err == nil || !strings.Contains(err.Error(), "claude_code") {
		t.Fatalf("LoadPermissionAuthority error = %v, want harness mismatch", err)
	}
}

func TestRenderPermissionsReconcilesManagedSliceAndIsIdempotent(t *testing.T) {
	homePath, targetPath := t.TempDir(), filepath.Join(t.TempDir(), "settings.json")
	writeTestJSON(t, targetPath, map[string]any{
		"theme":       "dark",
		"permissions": map[string]any{"allow": []string{"mcp__*", "HandOwned", "mcp__gbrain__*"}, "deny": []string{"HandDeny"}, "ask": []string{"HandAsk"}, "defaultMode": "legacy"},
		"autoMode":    map[string]any{"allow": []string{"$defaults", "LegacyManaged", "HandAuto"}, "other": true},
	})
	writePermissionFixture(t, homePath, targetPath, permissionFixture([]string{"Read", "mcp__gbrain__*"}))
	err := WriteHarnessArtifactManifest(homePath, []HarnessArtifact{{
		Path: targetPath, Harness: "claude_code", SourceAuthorityRef: PermissionAuthorityRef("claude_code"),
		ManagedEntries: map[string][]string{"permissions.allow": {"mcp__*", "mcp__gbrain__*"}, "autoMode.allow": {"LegacyManaged"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	render, err := RenderPermissions(homePath, "claude_code")
	if err != nil {
		t.Fatal(err)
	}
	got := decodeTestJSON(t, render.Content)
	if got["theme"] != "dark" || got["autoMode"].(map[string]any)["other"] != true {
		t.Fatalf("unrelated keys changed: %#v", got)
	}
	permissions := got["permissions"].(map[string]any)
	assertStrings(t, permissions["allow"], []string{"HandOwned", "Read", "mcp__gbrain__*"})
	assertStrings(t, permissions["deny"], []string{"HandDeny", "Bash(rm:*)"})
	assertStrings(t, permissions["ask"], []string{"HandAsk", "WebFetch"})
	if permissions["defaultMode"] != "default" || strings.Contains(string(render.Content), "\"mcp__*\"") {
		t.Fatalf("managed reconciliation failed:\n%s", render.Content)
	}
	assertStrings(t, got["autoMode"].(map[string]any)["allow"], []string{"$defaults", "HandAuto", "Read"})

	if err := os.WriteFile(targetPath, render.Content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteHarnessArtifactManifest(homePath, []HarnessArtifact{render.Artifact}); err != nil {
		t.Fatal(err)
	}
	second, err := RenderPermissions(homePath, "claude_code")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second.Content, render.Content) {
		t.Fatalf("second render differs:\n%s\n---\n%s", render.Content, second.Content)
	}
}

func TestPermissionDriftRedGreenAndIgnoresUnrelatedKeys(t *testing.T) {
	homePath, targetPath := t.TempDir(), filepath.Join(t.TempDir(), "settings.json")
	writeTestJSON(t, targetPath, map[string]any{"theme": "dark"})
	writePermissionFixture(t, homePath, targetPath, permissionFixture([]string{"Read", "mcp__gbrain__*"}))
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
	assertPermissionDrift(t, homePath, false, "")

	settings := decodeTestJSON(t, render.Content)
	settings["theme"] = "light"
	writeTestJSON(t, targetPath, settings)
	assertPermissionDrift(t, homePath, false, "")

	permissions := settings["permissions"].(map[string]any)
	permissions["allow"] = []string{"Read", "mcp__gbrain__*", "mcp__*"}
	writeTestJSON(t, targetPath, settings)
	assertPermissionDrift(t, homePath, true, "mcp__*")

	permissions["allow"] = []string{"mcp__gbrain__*"}
	writeTestJSON(t, targetPath, settings)
	assertPermissionDrift(t, homePath, true, "Read")

	settings = decodeTestJSON(t, render.Content)
	settings["autoMode"].(map[string]any)["allow"] = []string{"Read", "$defaults"}
	writeTestJSON(t, targetPath, settings)
	assertPermissionDrift(t, homePath, true, "$defaults")
}

func permissionFixture(allow []string) PermissionAuthority {
	return PermissionAuthority{
		SchemaVersion: PermissionAuthoritySchemaVersion, Harness: "claude_code", AuthorityRef: PermissionAuthorityRef("claude_code"),
		TargetPath:  "/synthetic/settings.json",
		Permissions: PermissionRules{Allow: allow, Deny: []string{"Bash(rm:*)"}, Ask: []string{"WebFetch"}, DefaultMode: "default"},
		AutoMode:    AutoModePolicy{Allow: []string{"$defaults", "Read"}},
	}
}

func writePermissionFixture(t *testing.T, homePath, targetPath string, policy PermissionAuthority) {
	t.Helper()
	policy.TargetPath = targetPath
	writeTestJSON(t, PermissionAuthorityPath(homePath, policy.Harness), policy)
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func decodeTestJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func assertStrings(t *testing.T, value any, want []string) {
	t.Helper()
	raw := value.([]any)
	got := make([]string, len(raw))
	for i := range raw {
		got[i] = raw[i].(string)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func assertPermissionDrift(t *testing.T, homePath string, want bool, detail string) {
	t.Helper()
	report, err := PermissionDrift(homePath, "claude_code")
	if err != nil {
		t.Fatal(err)
	}
	if report.Drifted != want {
		t.Fatalf("drifted=%v want=%v details=%v", report.Drifted, want, report.Details)
	}
	if detail != "" && !strings.Contains(strings.Join(report.Details, " "), detail) {
		t.Fatalf("details=%v want %q", report.Details, detail)
	}
}
