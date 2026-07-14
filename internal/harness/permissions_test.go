package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"compound/internal/envelope"
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

func TestApplyPermissionsWritesManagedRulesAndArtifactIdempotently(t *testing.T) {
	homePath, targetPath := t.TempDir(), filepath.Join(t.TempDir(), "settings.json")
	writeTestJSON(t, targetPath, map[string]any{
		"theme":    "dark",
		"autoMode": map[string]any{"allow": []string{"$defaults", "HandOwned"}, "other": true},
		"hooks":    map[string]any{"PreToolUse": []any{map[string]any{"matcher": "Write"}}},
	})
	policy := permissionFixture([]string{"Read", "mcp__gbrain__*"})
	policy.AutoMode.Allow = []string{"$defaults", "Routine local shell is auto-approved."}
	writePermissionFixture(t, homePath, targetPath, policy)

	first, err := applyGovernedPermissions(t, homePath, "claude_code")
	if err != nil {
		t.Fatalf("apply permissions: %v", err)
	}
	if !first.Changed {
		t.Fatal("first apply changed = false, want true")
	}
	body, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeTestJSON(t, body)
	if got["theme"] != "dark" || got["autoMode"].(map[string]any)["other"] != true {
		t.Fatalf("hand-owned settings not preserved: %+v", got)
	}
	if _, ok := got["hooks"].(map[string]any)["PreToolUse"]; !ok {
		t.Fatalf("hooks not preserved: %+v", got["hooks"])
	}
	assertStrings(t, got["autoMode"].(map[string]any)["allow"], []string{"$defaults", "HandOwned", "Routine local shell is auto-approved."})
	artifact, ok, err := latestHarnessArtifact(homePath, "claude_code", targetPath)
	if err != nil || !ok {
		t.Fatalf("latest artifact ok=%t err=%v", ok, err)
	}
	if artifact.ContentSHA256 != first.Artifact.ContentSHA256 {
		t.Fatalf("artifact sha = %q, want %q", artifact.ContentSHA256, first.Artifact.ContentSHA256)
	}
	manifestBefore, err := os.ReadFile(HarnessArtifactManifestPath(homePath))
	if err != nil {
		t.Fatal(err)
	}

	second, err := applyGovernedPermissions(t, homePath, "claude_code")
	if err != nil {
		t.Fatalf("second apply permissions: %v", err)
	}
	if second.Changed {
		t.Fatal("second apply changed = true, want idempotent false")
	}
	manifestAfter, err := os.ReadFile(HarnessArtifactManifestPath(homePath))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manifestAfter, manifestBefore) {
		t.Fatalf("second apply rewrote identical manifest:\n%s\n---\n%s", manifestBefore, manifestAfter)
	}
}

func TestApplyPermissionsRollsBackSettingsWhenArtifactManifestFails(t *testing.T) {
	homePath, targetPath := t.TempDir(), filepath.Join(t.TempDir(), "settings.json")
	writeTestJSON(t, targetPath, map[string]any{"theme": "dark"})
	before, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	writePermissionFixture(t, homePath, targetPath, permissionFixture([]string{"Read"}))
	previousWriter := writeHarnessArtifactManifestForApply
	writeHarnessArtifactManifestForApply = func(string, []HarnessArtifact) error {
		return os.ErrPermission
	}
	t.Cleanup(func() { writeHarnessArtifactManifestForApply = previousWriter })

	if _, err := applyGovernedPermissions(t, homePath, "claude_code"); err == nil || !strings.Contains(err.Error(), "restored") {
		t.Fatalf("apply error = %v, want explicit restore evidence", err)
	}
	after, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("settings changed after manifest failure:\n%s\n---\n%s", before, after)
	}
}

func TestApplyPermissionsInterruptedWriteIsVisibleAndRecovered(t *testing.T) {
	homePath, targetPath := t.TempDir(), filepath.Join(t.TempDir(), "settings.json")
	writeTestJSON(t, targetPath, map[string]any{"theme": "dark"})
	writePermissionFixture(t, homePath, targetPath, permissionFixture([]string{"Read"}))
	approved, action, decision := governedPermissionApproval(t, homePath, "claude_code")
	previousHook := applyPermissionsAfterTargetWrite
	applyPermissionsAfterTargetWrite = func() error { return os.ErrClosed }
	if _, err := ApplyPermissions(homePath, approved, action, decision); err == nil {
		t.Fatal("interrupted apply error = nil")
	}
	pendingInfo, err := os.Stat(PermissionApplyPendingPath(homePath, "claude_code"))
	if err != nil {
		t.Fatal(err)
	}
	if got := pendingInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("pending transaction mode = %o, want 600", got)
	}
	report, err := PermissionDrift(homePath, "claude_code")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Drifted || !strings.Contains(strings.Join(report.Details, " "), "incomplete permission apply transaction") {
		t.Fatalf("interrupted drift = %+v", report)
	}

	applyPermissionsAfterTargetWrite = previousHook
	t.Cleanup(func() { applyPermissionsAfterTargetWrite = previousHook })
	if _, err := ApplyPermissions(homePath, approved, action, decision); err != nil {
		t.Fatalf("recover apply: %v", err)
	}
	report, err = PermissionDrift(homePath, "claude_code")
	if err != nil || report.Drifted {
		t.Fatalf("drift after recovery = %+v err=%v", report, err)
	}
	if _, err := os.Stat(PermissionApplyPendingPath(homePath, "claude_code")); !os.IsNotExist(err) {
		t.Fatalf("pending transaction remains after recovery: %v", err)
	}
}

func TestApplyPermissionsRefusesConcurrentTargetChange(t *testing.T) {
	homePath, targetPath := t.TempDir(), filepath.Join(t.TempDir(), "settings.json")
	writeTestJSON(t, targetPath, map[string]any{"theme": "dark"})
	writePermissionFixture(t, homePath, targetPath, permissionFixture([]string{"Read"}))
	previousHook := applyPermissionsBeforeTargetCommit
	applyPermissionsBeforeTargetCommit = func() {
		writeTestJSON(t, targetPath, map[string]any{"theme": "manual-change"})
	}
	t.Cleanup(func() { applyPermissionsBeforeTargetCommit = previousHook })

	if _, err := applyGovernedPermissions(t, homePath, "claude_code"); err == nil || !strings.Contains(err.Error(), "preimage changed") {
		t.Fatalf("apply error = %v, want contention refusal", err)
	}
	body, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeTestJSON(t, body)["theme"]; got != "manual-change" {
		t.Fatalf("concurrent edit overwritten: theme=%v", got)
	}
	if _, err := os.Stat(PermissionApplyPendingPath(homePath, "claude_code")); !os.IsNotExist(err) {
		t.Fatalf("contention left pending transaction: %v", err)
	}
}

func TestApplyPermissionsRequiresExactGovernanceAndApprovedRender(t *testing.T) {
	homePath, targetPath := t.TempDir(), filepath.Join(t.TempDir(), "settings.json")
	writeTestJSON(t, targetPath, map[string]any{"theme": "dark"})
	writePermissionFixture(t, homePath, targetPath, permissionFixture([]string{"Read"}))
	approved, action, decision := governedPermissionApproval(t, homePath, "claude_code")
	before, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		mutate   func(*PermissionRender, *envelope.ActionIntent, *envelope.PolicyDecision)
		wantText string
	}{
		{"missing decision", func(_ *PermissionRender, _ *envelope.ActionIntent, d *envelope.PolicyDecision) {
			*d = envelope.PolicyDecision{}
		}, "policy decision"},
		{"wrong capability", func(_ *PermissionRender, a *envelope.ActionIntent, _ *envelope.PolicyDecision) {
			a.Capability = "other"
		}, "capability"},
		{"wrong action", func(_ *PermissionRender, a *envelope.ActionIntent, _ *envelope.PolicyDecision) { a.Action = "other" }, "action"},
		{"wrong target", func(_ *PermissionRender, a *envelope.ActionIntent, _ *envelope.PolicyDecision) {
			a.Target.Path += ".other"
		}, "target"},
		{"wrong class", func(_ *PermissionRender, a *envelope.ActionIntent, _ *envelope.PolicyDecision) {
			a.MutationClass = "local_reversible"
		}, "security"},
		{"held decision", func(_ *PermissionRender, _ *envelope.ActionIntent, d *envelope.PolicyDecision) { d.Outcome = "hold" }, "allow"},
		{"wrong binding", func(_ *PermissionRender, _ *envelope.ActionIntent, d *envelope.PolicyDecision) {
			d.ActionIntentID = "act_other"
		}, "bind"},
		{"tampered content", func(r *PermissionRender, _ *envelope.ActionIntent, _ *envelope.PolicyDecision) {
			r.Content = append(r.Content, ' ')
		}, "content"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, a, d := approved, action, decision
			tt.mutate(&r, &a, &d)
			if _, err := ApplyPermissions(homePath, r, a, d); err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.wantText)) {
				t.Fatalf("ApplyPermissions error = %v, want containing %q", err, tt.wantText)
			}
			after, err := os.ReadFile(targetPath)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatal("rejected governed apply mutated target")
			}
		})
	}
}

func TestApplyPermissionsRejectsApprovedRenderWhenAuthorityOrTargetChanges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, homePath, targetPath string)
	}{
		{"authority revision", func(t *testing.T, homePath, targetPath string) {
			writePermissionFixture(t, homePath, targetPath, permissionFixture([]string{"Read", "Write"}))
		}},
		{"target path", func(t *testing.T, homePath, targetPath string) {
			writePermissionFixture(t, homePath, targetPath+".other", permissionFixture([]string{"Read"}))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homePath, targetPath := t.TempDir(), filepath.Join(t.TempDir(), "settings.json")
			writeTestJSON(t, targetPath, map[string]any{"theme": "dark"})
			writePermissionFixture(t, homePath, targetPath, permissionFixture([]string{"Read"}))
			approved, action, decision := governedPermissionApproval(t, homePath, "claude_code")
			before, _ := os.ReadFile(targetPath)
			tc.mutate(t, homePath, targetPath)
			if _, err := ApplyPermissions(homePath, approved, action, decision); err == nil || !strings.Contains(err.Error(), "approved render") {
				t.Fatalf("ApplyPermissions error = %v, want stale approved render refusal", err)
			}
			after, _ := os.ReadFile(targetPath)
			if !bytesEqual(before, after) {
				t.Fatal("stale approval mutated original target")
			}
			if _, err := os.Stat(targetPath + ".other"); !os.IsNotExist(err) {
				t.Fatalf("stale approval created changed target: %v", err)
			}
		})
	}
}

func TestPermissionRenderFormattingDifferencePublishesCanonicalBytes(t *testing.T) {
	homePath, targetPath := t.TempDir(), filepath.Join(t.TempDir(), "settings.json")
	compact := []byte(`{"permissions":{"allow":["Read"],"deny":["Bash(rm:*)"],"ask":["WebFetch"],"defaultMode":"default"},"autoMode":{"allow":["$defaults","Read"]}}`)
	if err := os.WriteFile(targetPath, compact, 0o644); err != nil {
		t.Fatal(err)
	}
	writePermissionFixture(t, homePath, targetPath, permissionFixture([]string{"Read"}))
	approved, action, decision := governedPermissionApproval(t, homePath, "claude_code")
	if !approved.Changed {
		t.Fatal("byte-different semantic JSON reported Changed=false")
	}
	applied, err := ApplyPermissions(homePath, approved, action, decision)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(targetPath)
	if !bytesEqual(actual, approved.Content) || applied.Artifact.ContentSHA256 != "sha256:"+digest(actual) {
		t.Fatalf("target/artifact do not attest canonical bytes:\n%s", actual)
	}
}

func TestApplyPermissionsManifestFailurePreservesConcurrentManualEditAndPending(t *testing.T) {
	homePath, targetPath := t.TempDir(), filepath.Join(t.TempDir(), "settings.json")
	writeTestJSON(t, targetPath, map[string]any{"theme": "dark"})
	writePermissionFixture(t, homePath, targetPath, permissionFixture([]string{"Read"}))
	previousWriter := writeHarnessArtifactManifestForApply
	writeHarnessArtifactManifestForApply = func(string, []HarnessArtifact) error {
		writeTestJSON(t, targetPath, map[string]any{"theme": "manual-after-publication"})
		return os.ErrPermission
	}
	t.Cleanup(func() { writeHarnessArtifactManifestForApply = previousWriter })
	if _, err := applyGovernedPermissions(t, homePath, "claude_code"); err == nil || !strings.Contains(err.Error(), "manual") {
		t.Fatalf("ApplyPermissions error = %v, want ambiguous rollback evidence", err)
	}
	body, _ := os.ReadFile(targetPath)
	if got := decodeTestJSON(t, body)["theme"]; got != "manual-after-publication" {
		t.Fatalf("manual edit overwritten: %v", got)
	}
	if _, err := os.Stat(PermissionApplyPendingPath(homePath, "claude_code")); err != nil {
		t.Fatalf("pending recovery evidence missing: %v", err)
	}
	report, err := PermissionDrift(homePath, "claude_code")
	if err != nil || !report.Drifted || !strings.Contains(strings.Join(report.Details, " "), "incomplete") {
		t.Fatalf("drift=%+v err=%v", report, err)
	}
}

func TestApplyPermissionsSerializesSharedManifestAcrossHarnesses(t *testing.T) {
	homePath := t.TempDir()
	var approvals []struct {
		r PermissionRender
		a envelope.ActionIntent
		d envelope.PolicyDecision
	}
	for _, name := range []string{"claude_code", "codex"} {
		targetPath := filepath.Join(t.TempDir(), name+".json")
		writeTestJSON(t, targetPath, map[string]any{"theme": name})
		policy := permissionFixture([]string{"Read"})
		policy.Harness, policy.AuthorityRef = name, PermissionAuthorityRef(name)
		writePermissionFixture(t, homePath, targetPath, policy)
		r, a, d := governedPermissionApproval(t, homePath, name)
		approvals = append(approvals, struct {
			r PermissionRender
			a envelope.ActionIntent
			d envelope.PolicyDecision
		}{r, a, d})
	}
	previousWriter := writeHarnessArtifactManifestForApply
	var active, maxActive atomic.Int32
	writeHarnessArtifactManifestForApply = func(home string, artifacts []HarnessArtifact) error {
		n := active.Add(1)
		defer active.Add(-1)
		if n > maxActive.Load() {
			maxActive.Store(n)
		}
		time.Sleep(20 * time.Millisecond)
		return WriteHarnessArtifactManifest(home, artifacts)
	}
	t.Cleanup(func() { writeHarnessArtifactManifestForApply = previousWriter })
	var wg sync.WaitGroup
	errs := make(chan error, len(approvals))
	for _, approval := range approvals {
		approval := approval
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := ApplyPermissions(homePath, approval.r, approval.a, approval.d)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if maxActive.Load() != 1 {
		t.Fatalf("shared manifest writer concurrency = %d, want 1", maxActive.Load())
	}
	artifacts, err := loadHarnessArtifacts(homePath)
	if err != nil || len(artifacts) != 2 {
		t.Fatalf("manifest artifacts=%+v err=%v", artifacts, err)
	}
}

func TestApplyPermissionsSyncsEveryPublicationBoundary(t *testing.T) {
	homePath, targetPath := t.TempDir(), filepath.Join(t.TempDir(), "settings.json")
	writeTestJSON(t, targetPath, map[string]any{"theme": "dark"})
	writePermissionFixture(t, homePath, targetPath, permissionFixture([]string{"Read"}))
	previous := permissionApplySyncBoundary
	var got []string
	permissionApplySyncBoundary = func(boundary, _ string) error {
		got = append(got, boundary)
		return nil
	}
	t.Cleanup(func() { permissionApplySyncBoundary = previous })
	if _, err := applyGovernedPermissions(t, homePath, "claude_code"); err != nil {
		t.Fatal(err)
	}
	want := []string{"pending_write", "target_publish", "manifest_write", "pending_remove"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sync boundaries = %v, want %v", got, want)
	}
}

func TestApplyPermissionsPendingSyncFailureDoesNotPublishTarget(t *testing.T) {
	homePath, targetPath := t.TempDir(), filepath.Join(t.TempDir(), "settings.json")
	writeTestJSON(t, targetPath, map[string]any{"theme": "dark"})
	writePermissionFixture(t, homePath, targetPath, permissionFixture([]string{"Read"}))
	before, _ := os.ReadFile(targetPath)
	previous := permissionApplySyncBoundary
	permissionApplySyncBoundary = func(boundary, _ string) error {
		if boundary == "pending_write" {
			return os.ErrInvalid
		}
		return nil
	}
	t.Cleanup(func() { permissionApplySyncBoundary = previous })
	if _, err := applyGovernedPermissions(t, homePath, "claude_code"); err == nil {
		t.Fatal("pending sync failure error = nil")
	}
	after, _ := os.ReadFile(targetPath)
	if !bytesEqual(before, after) {
		t.Fatal("pending sync failure published target")
	}
}

func TestPendingRecoveryRejectsChangedAuthorityRevision(t *testing.T) {
	homePath, targetPath := t.TempDir(), filepath.Join(t.TempDir(), "settings.json")
	writeTestJSON(t, targetPath, map[string]any{"theme": "dark"})
	writePermissionFixture(t, homePath, targetPath, permissionFixture([]string{"Read"}))
	approved, action, decision := governedPermissionApproval(t, homePath, "claude_code")
	previousHook := applyPermissionsAfterTargetWrite
	applyPermissionsAfterTargetWrite = func() error { return os.ErrClosed }
	if _, err := ApplyPermissions(homePath, approved, action, decision); err == nil {
		t.Fatal("interrupted apply error = nil")
	}
	applyPermissionsAfterTargetWrite = previousHook
	t.Cleanup(func() { applyPermissionsAfterTargetWrite = previousHook })
	writePermissionFixture(t, homePath, targetPath+".changed", permissionFixture([]string{"Write"}))
	before, _ := os.ReadFile(targetPath)
	if _, err := ApplyPermissions(homePath, approved, action, decision); err == nil || !strings.Contains(err.Error(), "source revision") {
		t.Fatalf("recovery error = %v, want source revision refusal", err)
	}
	after, _ := os.ReadFile(targetPath)
	if !bytesEqual(before, after) {
		t.Fatal("changed-authority recovery mutated prior target")
	}
}

func TestPendingRecoveryRejectsNonLeafRecoveryName(t *testing.T) {
	base := t.TempDir()
	homePath := filepath.Join(base, "home")
	targetPath := filepath.Join(base, "target", "settings.json")
	writeTestJSON(t, targetPath, map[string]any{"theme": "dark"})
	writePermissionFixture(t, homePath, targetPath, permissionFixture([]string{"Read"}))
	approved, action, decision := governedPermissionApproval(t, homePath, "claude_code")
	before, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	sentinelPath := filepath.Join(base, "sentinel")
	if err := os.WriteFile(sentinelPath, []byte("must survive"), 0o600); err != nil {
		t.Fatal(err)
	}
	pending := permissionApplyPending{
		SchemaVersion: permissionApplyPendingSchemaVersion, Harness: "claude_code", TargetPath: targetPath,
		RecoveryName: "../sentinel", BeforeExisted: true, BeforeContent: before, BeforeSHA256: "sha256:" + digest(before),
		IntendedSHA256: approved.Artifact.ContentSHA256, SourceAuthorityRef: approved.Artifact.SourceAuthorityRef, SourceRevision: approved.Artifact.SourceRevision,
	}
	writeTestJSON(t, PermissionApplyPendingPath(homePath, "claude_code"), pending)
	if _, err := ApplyPermissions(homePath, approved, action, decision); err == nil || !strings.Contains(err.Error(), "recovery name") {
		t.Fatalf("ApplyPermissions error = %v, want invalid recovery name", err)
	}
	if body, err := os.ReadFile(sentinelPath); err != nil || string(body) != "must survive" {
		t.Fatalf("non-leaf recovery name touched sentinel: body=%q err=%v", body, err)
	}
}

func governedPermissionApproval(t *testing.T, homePath, harnessName string) (PermissionRender, envelope.ActionIntent, envelope.PolicyDecision) {
	t.Helper()
	render, err := RenderPermissions(homePath, harnessName)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	action := envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent, ID: "act_permission_test", CreatedAt: now.Format(time.RFC3339),
			Producer: envelope.Producer{Kind: "cli", Name: "compound"}, IDs: envelope.IDs{WorkID: "work_permission_test"},
			Authority: envelope.Authority{SourceClass: "runtime", Owner: "compound", Status: "current"}, Privacy: "private", Risk: "tier1",
			EvidenceRefs: []string{render.Artifact.SourceAuthorityRef, render.Artifact.SourceRevision}, Uncertainty: envelope.Uncertainty{Level: "known"},
		},
		Actor: "operator:test", Capability: "harness.permissions.apply", Action: "reconcile_harness_permissions",
		Target:        envelope.ActionTarget{Kind: "harness_settings", ID: harnessName, Path: render.TargetPath},
		MutationClass: "security", Reversibility: "easy", BlastRadius: "local", RollbackPlan: "restore exact pre-image",
	}
	decision := envelope.PolicyDecision{
		Header: envelope.Header{
			SchemaVersion: envelope.SVPolicyDecision, ID: "pol_permission_test", CreatedAt: now.Format(time.RFC3339),
			Producer: envelope.Producer{Kind: "cli", Name: "compound"}, IDs: envelope.IDs{WorkID: "work_permission_test"},
			Authority: envelope.Authority{SourceClass: "runtime", Status: "current"}, Privacy: "private", Risk: "tier1",
			EvidenceRefs: []string{}, Uncertainty: envelope.Uncertainty{Level: "known"},
		},
		ActionIntentID: action.ID, Outcome: "allow_with_obligations", Reasons: []string{"test grant"}, Obligations: []string{"emit receipt"}, StandingGrantID: "grant_test",
	}
	return render, action, decision
}

func applyGovernedPermissions(t *testing.T, homePath, harnessName string) (PermissionRender, error) {
	t.Helper()
	render, action, decision := governedPermissionApproval(t, homePath, harnessName)
	return ApplyPermissions(homePath, render, action, decision)
}

func bytesEqual(a, b []byte) bool { return reflect.DeepEqual(a, b) }

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
