package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/harness"
	"compound/internal/home"
	"compound/internal/work"
)

func TestHarnessGenerateApplyRequiresWorkAndRecordsGovernedAction(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(t.TempDir(), "settings.json")
	writeHarnessApplyJSON(t, targetPath, map[string]any{
		"theme": "dark",
		"autoMode": map[string]any{
			"allow": []string{"$defaults", "HandOwned"},
		},
		"hooks": map[string]any{"PreToolUse": []any{map[string]any{"matcher": "Write"}}},
	})
	writeHarnessApplyJSON(t, harness.PermissionAuthorityPath(homePath, "claude_code"), harness.PermissionAuthority{
		SchemaVersion: harness.PermissionAuthoritySchemaVersion,
		Harness:       "claude_code",
		AuthorityRef:  harness.PermissionAuthorityRef("claude_code"),
		TargetPath:    targetPath,
		Permissions: harness.PermissionRules{
			Allow: []string{"Read"}, Deny: []string{}, Ask: []string{}, DefaultMode: "auto",
		},
		AutoMode: harness.AutoModePolicy{Allow: []string{"$defaults", "Routine local shell is auto-approved."}},
	})
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(work.CreateInput{
		Owner: "harness-settings", Actor: "operator:test", SuccessPredicate: "permissions applied", State: "ready",
	}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(w.ID, "operator:test", cliTestNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "running", Actor: "operator:test"}, cliTestNow.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runHarnessGenerateApply([]string{
		"--harness", "claude_code", "--apply", "--work", w.ID, "--actor", "operator:test", "--json",
	}, &stdout, &stderr, cliTestNow.Add(3*time.Minute))
	if code != 0 {
		t.Fatalf("apply code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var report harnessPermissionApplyReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if !report.Changed || report.WorkID != w.ID || report.ActionIntentID == "" || report.PolicyDecisionID == "" {
		t.Fatalf("apply report = %+v", report)
	}
	body, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatal(err)
	}
	if settings["theme"] != "dark" || settings["hooks"] == nil {
		t.Fatalf("hand-owned settings lost: %+v", settings)
	}
	auto := settings["autoMode"].(map[string]any)["allow"].([]any)
	if strings.Join([]string{auto[0].(string), auto[1].(string), auto[2].(string)}, "|") != "$defaults|HandOwned|Routine local shell is auto-approved." {
		t.Fatalf("autoMode.allow = %+v", auto)
	}
	drift, err := harness.PermissionDrift(homePath, "claude_code")
	if err != nil || drift.Drifted {
		t.Fatalf("drift after apply = %+v err=%v", drift, err)
	}
	recorded, err := store.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(recorded.RunIDs, report.ActionIntentID) || recorded.PolicyDecisionID != report.PolicyDecisionID {
		t.Fatalf("work action binding = runs=%v policy=%s", recorded.RunIDs, recorded.PolicyDecisionID)
	}

	stdout.Reset()
	stderr.Reset()
	code = runHarnessGenerateApply([]string{
		"--harness", "claude_code", "--apply", "--work", w.ID, "--actor", "operator:test", "--json",
	}, &stdout, &stderr, cliTestNow.Add(4*time.Minute))
	if code != 0 {
		t.Fatalf("second apply code=%d stderr=%s", code, stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Changed {
		t.Fatal("second apply changed = true, want idempotent false")
	}
}

func TestHarnessGenerateApplyWithoutWorkIsNonMutating(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(t.TempDir(), "settings.json")
	writeHarnessApplyJSON(t, targetPath, map[string]any{"theme": "dark"})
	writeHarnessApplyJSON(t, harness.PermissionAuthorityPath(homePath, "claude_code"), harness.PermissionAuthority{
		SchemaVersion: harness.PermissionAuthoritySchemaVersion,
		Harness:       "claude_code", AuthorityRef: harness.PermissionAuthorityRef("claude_code"), TargetPath: targetPath,
		Permissions: harness.PermissionRules{Allow: []string{"Read"}, Deny: []string{}, Ask: []string{}, DefaultMode: "auto"},
		AutoMode:    harness.AutoModePolicy{Allow: []string{"$defaults", "Routine local shell is auto-approved."}},
	})
	before, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runHarnessGenerateApply([]string{"--harness", "claude_code", "--apply"}, &stdout, &stderr, cliTestNow); code != 2 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	after, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("apply without work mutated settings")
	}
}

func writeHarnessApplyJSON(t *testing.T, path string, value any) {
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
