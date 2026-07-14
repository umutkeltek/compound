package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/home"
	"compound/internal/policy"
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
	installHarnessPermissionGrant(t, homePath, targetPath)

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
	if !report.Changed || report.WorkID != w.ID || report.ActionIntentID == "" || report.PolicyDecisionID == "" ||
		report.ReceiptID == "" || report.TransitionID == "" || report.PreImageRef == "" || report.PostImageRef == "" {
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
	var action envelope.ActionIntent
	readHarnessApplyJSON(t, filepath.Join(homePath, "events", report.ActionIntentID+".json"), &action)
	if err := action.Validate(); err != nil {
		t.Fatalf("persisted action invalid: %v", err)
	}
	if action.IDs.WorkID != w.ID || action.Capability != "harness.permissions.apply" ||
		action.Action != "reconcile_harness_permissions" || action.Target.Kind != "harness_settings" ||
		action.Target.ID != "claude_code" || action.Target.Path != targetPath || action.MutationClass != "security" ||
		action.Reversibility != "easy" || action.BlastRadius != "local" {
		t.Fatalf("persisted action identity = %+v", action)
	}
	var decision envelope.PolicyDecision
	readHarnessApplyJSON(t, filepath.Join(homePath, "events", report.PolicyDecisionID+".json"), &decision)
	if err := decision.Validate(); err != nil {
		t.Fatalf("persisted decision invalid: %v", err)
	}
	if decision.ActionIntentID != action.ID || !policy.Allowed(decision) || decision.StandingGrantID != "grant_harness_permission_apply" {
		t.Fatalf("persisted policy binding = %+v", decision)
	}
	var receipt envelope.Receipt
	readHarnessApplyJSON(t, filepath.Join(homePath, "events", report.ReceiptID+".json"), &receipt)
	if err := receipt.Validate(); err != nil || receipt.Outcome != "completed" || !receipt.StateDelta.Changed ||
		!containsString(receipt.ProofRefs, report.PreImageRef) || !containsString(receipt.ProofRefs, report.PostImageRef) {
		t.Fatalf("persisted receipt = %+v err=%v", receipt, err)
	}
	var transition envelope.Transition
	readHarnessApplyJSON(t, filepath.Join(homePath, "events", report.TransitionID+".json"), &transition)
	if err := transition.Validate(); err != nil || transition.IDs.WorkID != w.ID ||
		!containsString(transition.BeforeRefs, report.PreImageRef) || !containsString(transition.AfterRefs, report.PostImageRef) ||
		!containsString(transition.ActionRefs, action.ID) {
		t.Fatalf("persisted transition = %+v err=%v", transition, err)
	}
	_, events, err := store.Show(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	for _, ref := range []string{report.ActionIntentID, report.PolicyDecisionID, report.ReceiptID, report.TransitionID} {
		if !containsString(last.Attached, ref) {
			t.Fatalf("work event missing %s: %+v", ref, last)
		}
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

func TestHarnessGenerateApplyHeldPolicyIsNonMutating(t *testing.T) {
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
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(work.CreateInput{Owner: "harness-settings", Actor: "operator:test", SuccessPredicate: "held apply", State: "ready"}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(w.ID, "operator:test", cliTestNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "running", Actor: "operator:test"}, cliTestNow.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	cfg := policy.DefaultConfig()
	cfg.ClassOutcome["security"] = policy.ClassRule{Outcome: "hold"}
	writeHarnessApplyJSON(t, filepath.Join(homePath, "config", "policy.yaml"), cfg)
	workBefore, err := store.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runHarnessGenerateApply([]string{
		"--harness", "claude_code", "--apply", "--work", w.ID, "--actor", "operator:test", "--json",
	}, &stdout, &stderr, cliTestNow.Add(3*time.Minute))
	if code != 1 || !strings.Contains(stderr.String(), "policy hold") {
		t.Fatalf("held apply code=%d stderr=%s", code, stderr.String())
	}
	after, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("held policy mutated settings")
	}
	if _, err := os.Stat(harness.HarnessArtifactManifestPath(homePath)); !os.IsNotExist(err) {
		t.Fatalf("held policy wrote artifact manifest: %v", err)
	}
	recorded, err := store.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recorded.RunIDs, workBefore.RunIDs) || recorded.PolicyDecisionID != workBefore.PolicyDecisionID {
		t.Fatalf("held policy recorded performed action: %+v", recorded)
	}
}

func TestHarnessGenerateApplyRequiresExplicitActor(t *testing.T) {
	homePath, targetPath, w := setupHarnessPermissionApplyWork(t, "operator:test", "running")
	installHarnessPermissionGrant(t, homePath, targetPath)
	before, _ := os.ReadFile(targetPath)
	var stdout, stderr bytes.Buffer
	code := runHarnessGenerateApply([]string{"--harness", "claude_code", "--apply", "--work", w.ID, "--json"}, &stdout, &stderr, cliTestNow.Add(3*time.Minute))
	if code != 2 || !strings.Contains(stderr.String(), "--actor") {
		t.Fatalf("missing actor code=%d stderr=%s", code, stderr.String())
	}
	after, _ := os.ReadFile(targetPath)
	if !bytes.Equal(before, after) {
		t.Fatal("missing actor mutated settings")
	}
}

func TestHarnessGenerateApplyRequiresRunningWorkHeldByActor(t *testing.T) {
	for _, tc := range []struct {
		name, leaseHolder, state, actor, want string
	}{
		{"ready", "", "ready", "operator:test", "running"},
		{"wrong actor", "operator:holder", "running", "operator:test", "lease holder"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homePath, targetPath, w := setupHarnessPermissionApplyWork(t, tc.leaseHolder, tc.state)
			installHarnessPermissionGrant(t, homePath, targetPath)
			before, _ := os.ReadFile(targetPath)
			store, _ := work.Open(homePath)
			workBefore, _ := store.Get(w.ID)
			var stdout, stderr bytes.Buffer
			code := runHarnessGenerateApply([]string{"--harness", "claude_code", "--apply", "--work", w.ID, "--actor", tc.actor, "--json"}, &stdout, &stderr, cliTestNow.Add(3*time.Minute))
			if code != 1 || !strings.Contains(strings.ToLower(stderr.String()), tc.want) {
				t.Fatalf("code=%d stderr=%s want=%q", code, stderr.String(), tc.want)
			}
			after, _ := os.ReadFile(targetPath)
			workAfter, _ := store.Get(w.ID)
			if !bytes.Equal(before, after) || !reflect.DeepEqual(workBefore, workAfter) {
				t.Fatal("invalid work admission mutated settings or work")
			}
			if _, err := os.Stat(harness.HarnessArtifactManifestPath(homePath)); !os.IsNotExist(err) {
				t.Fatalf("invalid work admission wrote manifest: %v", err)
			}
		})
	}
}

func TestHarnessGenerateApplyFailureRecordsFailedReceiptWithoutFalseSuccess(t *testing.T) {
	homePath, targetPath, w := setupHarnessPermissionApplyWork(t, "operator:test", "running")
	installHarnessPermissionGrant(t, homePath, targetPath)
	before, _ := os.ReadFile(targetPath)
	previous := applyHarnessPermissions
	applyHarnessPermissions = func(string, harness.PermissionRender, envelope.ActionIntent, envelope.PolicyDecision) (harness.PermissionRender, error) {
		return harness.PermissionRender{}, errors.New("injected permission apply failure")
	}
	t.Cleanup(func() { applyHarnessPermissions = previous })
	var stdout, stderr bytes.Buffer
	code := runHarnessGenerateApply([]string{"--harness", "claude_code", "--apply", "--work", w.ID, "--actor", "operator:test", "--json"}, &stdout, &stderr, cliTestNow.Add(3*time.Minute))
	if code != 1 || !strings.Contains(stderr.String(), "injected permission apply failure") {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	after, _ := os.ReadFile(targetPath)
	if !bytes.Equal(before, after) {
		t.Fatal("injected failure mutated target")
	}
	store, _ := work.Open(homePath)
	recorded, _ := store.Get(w.ID)
	if len(recorded.RunIDs) != 1 || recorded.PolicyDecisionID == "" {
		t.Fatalf("failed action work links = %+v", recorded)
	}
	var failedReceipt envelope.Receipt
	var failedTransition envelope.Transition
	entries, err := os.ReadDir(filepath.Join(homePath, "events"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		switch {
		case strings.HasPrefix(entry.Name(), "rec_"):
			readHarnessApplyJSON(t, filepath.Join(homePath, "events", entry.Name()), &failedReceipt)
		case strings.HasPrefix(entry.Name(), "tr_"):
			readHarnessApplyJSON(t, filepath.Join(homePath, "events", entry.Name()), &failedTransition)
		}
	}
	if failedReceipt.Outcome != "failed" || failedReceipt.StateDelta.Changed || failedReceipt.FailureCause == nil ||
		!containsString(failedReceipt.ProofRefs, "sha256:"+permissionApplyDigest(before)) {
		t.Fatalf("failed receipt = %+v", failedReceipt)
	}
	if failedTransition.ID == "" || !containsString(failedTransition.BeforeRefs, "sha256:"+permissionApplyDigest(before)) ||
		!containsString(failedTransition.AfterRefs, "sha256:"+permissionApplyDigest(before)) {
		t.Fatalf("failed transition = %+v", failedTransition)
	}
}

func setupHarnessPermissionApplyWork(t *testing.T, leaseHolder, state string) (string, string, envelope.WorkEnvelope) {
	t.Helper()
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(t.TempDir(), "settings.json")
	writeHarnessApplyJSON(t, targetPath, map[string]any{"theme": "dark"})
	writeHarnessApplyJSON(t, harness.PermissionAuthorityPath(homePath, "claude_code"), harness.PermissionAuthority{
		SchemaVersion: harness.PermissionAuthoritySchemaVersion, Harness: "claude_code", AuthorityRef: harness.PermissionAuthorityRef("claude_code"), TargetPath: targetPath,
		Permissions: harness.PermissionRules{Allow: []string{"Read"}, DefaultMode: "auto"}, AutoMode: harness.AutoModePolicy{Allow: []string{"$defaults", "Read"}},
	})
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(work.CreateInput{Owner: "harness-settings", Actor: "operator:test", SuccessPredicate: "permissions applied", State: "ready"}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if state == "running" {
		if _, err := store.Claim(w.ID, leaseHolder, cliTestNow.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		w, err = store.Advance(w.ID, work.AdvanceInput{To: "running", Actor: leaseHolder}, cliTestNow.Add(2*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
	}
	return homePath, targetPath, w
}

func installHarnessPermissionGrant(t *testing.T, homePath, targetPath string) {
	t.Helper()
	cfg := policy.DefaultConfig()
	cfg.StandingGrants = []policy.StandingGrant{{
		ID: "grant_harness_permission_apply", Capability: "harness.permissions.apply", MutationClass: "security",
		Reversibility: "easy", BlastRadius: "local", TargetPath: targetPath,
	}}
	writeHarnessApplyJSON(t, filepath.Join(homePath, "config", "policy.yaml"), cfg)
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

func readHarnessApplyJSON(t *testing.T, path string, value any) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, value); err != nil {
		t.Fatal(err)
	}
}
