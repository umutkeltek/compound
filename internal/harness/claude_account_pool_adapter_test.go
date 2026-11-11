package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/runattempt"
)

func claudePoolFixture(t *testing.T) (*poolFixture, *ClaudeNativeAccountPoolAdapter) {
	t.Helper()
	f := newPoolFixture(t)
	settings := filepath.Join(f.root, "settings.json")
	if err := os.WriteFile(settings, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := claudePoolContract{
		SchemaVersion: "compound.native_account_pool.v1", Provider: "claude", Accounts: []string{"seat_one", "seat_two"},
		SettingsPath: settings, SettingsSHA256: poolDigest([]byte("{}")), SwitchbackCLI: f.sb,
		CompoundCLI: f.config.CompoundCLI, TimeoutSeconds: 90,
	}
	body, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.contractPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	f.digest = poolDigest(body)
	f.input.Harness = claudePoolAdapterName
	f.input.Args = []string{f.contractPath, f.digest, f.config.CheckerPath, f.config.CheckerSHA256}
	f.input.ExecutionSelectionSnapshot.Harness = claudePoolAdapterName
	// Public CLI binding supplies requested identity on the selection snapshot;
	// the adapter must consume it rather than silently use a native default.
	f.input.ExecutionSelectionSnapshot.RequestedModelID = "synthetic-model"
	f.input.ExecutionSelectionSnapshot.ProviderRoute = "synthetic-native-route"
	return f, NewClaudeNativeAccountPoolAdapter(poolTestStore(t, f.root))
}

func TestClaudePoolPinnedPlanLaunch(t *testing.T) {
	f, a := claudePoolFixture(t)
	if _, ok := NewAdapterRegistry(a.store).AdapterByName(claudePoolAdapterName); !ok {
		t.Fatal("production adapter missing")
	}
	if adapter, ok := NewAdapterRegistry(a.store).AdapterByName("claude_native_account_pool"); !ok || adapter.Name() != claudePoolAdapterName {
		t.Fatal("manifest pool identity did not resolve production adapter")
	}
	if errs := ReadOnlyLaunchReadiness(a.store.Home(), f.input); len(errs) != 0 {
		t.Fatal(errs)
	}
	out, err := a.Run(context.Background(), f.input)
	if err != nil {
		t.Fatal(err)
	}
	if out.ExitCode != 0 || out.RunAttempt.Status != runattempt.StatusSucceeded {
		t.Fatalf("output=%+v", out)
	}
	snap := out.RunAttempt.ExecutionSelection
	if snap == nil || snap.AccountPoolContractRef != f.contractPath || snap.AccountPoolContractSHA256 != "sha256:"+f.digest || snap.NativeSessionID == "" || snap.NativeSessionEvidence != claudeNativeSessionEvidence {
		t.Fatalf("missing pinned selection: %+v", snap)
	}
	if f.input.ExecutionSelectionSnapshot.NativeSessionID != "" {
		t.Fatal("caller snapshot mutated")
	}
	body, err := os.ReadFile(f.marker)
	if err != nil {
		t.Fatal(err)
	}
	var argv []string
	if err := json.Unmarshal(bytesTrim(body), &argv); err != nil {
		t.Fatal(err)
	}
	for flag, want := range map[string]string{"--provider": "claude", "--pool": "seat_one,seat_two", "--permission-mode": "plan", "--max-attempts": "1", "--model": "synthetic-model", "--session-id": snap.NativeSessionID, "--prompt": f.input.Prompt, "--timeout": "90", "--expected-settings-sha256": poolDigest([]byte("{}"))} {
		i := indexOf(argv, flag)
		if i < 0 || i+1 >= len(argv) || argv[i+1] != want {
			t.Errorf("%s missing/different: %v", flag, argv)
		}
	}
	if !poolContainsString(argv, "--require-capture") || poolContainsString(argv, "--dangerously-skip-permissions") {
		t.Fatalf("unsafe flags: %v", argv)
	}
}

func TestClaudePoolRefusesBeforeAttempt(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*poolFixture)
	}{
		{"mutation", func(f *poolFixture) { f.input.PermissionMode = "bypass" }},
		{"default", func(f *poolFixture) { f.input.PermissionMode = "default" }},
		{"acceptEdits", func(f *poolFixture) { f.input.PermissionMode = "acceptEdits" }},
		{"no_reservation", func(f *poolFixture) { f.input.StartGuard = nil }},
		{"no_revalidation", func(f *poolFixture) { f.input.StartRevalidate = nil }},
		{"no_action", func(f *poolFixture) { f.input.ActionIntentID = "" }},
		{"no_selection", func(f *poolFixture) { f.input.ExecutionSelectionSnapshot = nil }},
		{"no_prompt", func(f *poolFixture) { f.input.Prompt = "" }},
		{"effort", func(f *poolFixture) { f.input.ExecutionSelectionSnapshot.RequestedEffort = "high" }},
		{"model_override", func(f *poolFixture) { f.input.RequestedModelID = "unselected-model" }},
		{"provider_override", func(f *poolFixture) { f.input.RequestedProviderRoute = "unselected-route" }},
		{"bad_contract", func(f *poolFixture) { f.input.Args[1] = strings.Repeat("a", 64) }},
		{"bad_checker", func(f *poolFixture) { f.input.Args[3] = strings.Repeat("a", 64) }},
		{"wrong_command", func(f *poolFixture) { f.input.Command = f.config.CompoundCLI }},
		{"timeout", func(f *poolFixture) { f.input.Timeout = 1 }},
		{"root_override", func(f *poolFixture) { f.input.Env = append(f.input.Env, "COMPOUND_HOME=/unowned") }},
		{"profile_override", func(f *poolFixture) { f.input.Env = append(f.input.Env, "CLAUDE_CONFIG_DIR=/unowned") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, a := claudePoolFixture(t)
			test.mutate(f)
			if _, err := a.Run(context.Background(), f.input); err == nil {
				t.Fatal("unsafe run accepted")
			}
			attempts, err := a.store.List()
			if err != nil {
				t.Fatal(err)
			}
			if len(attempts) != 0 {
				t.Fatalf("minted attempts: %v", attempts)
			}
			if _, err := os.Stat(f.marker); !os.IsNotExist(err) {
				t.Fatal("provider launched")
			}
		})
	}
}

func TestClaudePoolFinalGuardRechecksSettings(t *testing.T) {
	f, a := claudePoolFixture(t)
	f.input.StartGuard = func(_ context.Context, start func() error) error {
		var c claudePoolContract
		body, _ := os.ReadFile(f.contractPath)
		if err := json.Unmarshal(body, &c); err != nil {
			return err
		}
		if err := os.WriteFile(c.SettingsPath, []byte("{\"drift\":true}"), 0o600); err != nil {
			return err
		}
		return start()
	}
	out, err := a.Run(context.Background(), f.input)
	if err == nil {
		t.Fatal("launch-time settings drift accepted")
	}
	if out.RunAttempt.ID == "" || out.RunAttempt.Status != runattempt.StatusFailed {
		t.Fatalf("attempt not terminal: %+v", out.RunAttempt)
	}
	if _, err := os.Stat(f.marker); !os.IsNotExist(err) {
		t.Fatal("provider launched after drift")
	}
}

func TestClaudePoolCaptureFailureIsTerminal(t *testing.T) {
	f, a := claudePoolFixture(t)
	f.input.Env = append(f.input.Env, "FAKE_CAPTURE_FAIL=1")
	out, err := a.Run(context.Background(), f.input)
	if err != nil {
		t.Fatal(err)
	}
	if out.ExitCode != 74 || out.RunAttempt.Status != runattempt.StatusFailed {
		t.Fatalf("capture failure=%+v", out)
	}
	body, err := os.ReadFile(f.marker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(body), "\n") != 1 {
		t.Fatal("capture failure was replayed")
	}
}

func TestClaudePoolSelectionRefusesEffort(t *testing.T) {
	snapshot := &runattempt.ExecutionSelectionSnapshot{RequestedModelID: "synthetic-model"}
	if err := CheckSelectionApplicable(claudePoolAdapterName, snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.RequestedEffort = "high"
	if err := CheckSelectionApplicable(claudePoolAdapterName, snapshot); err == nil {
		t.Fatal("unattested effort accepted")
	}
}

func claudeReadOnlyPoolFixture(t *testing.T) (*poolFixture, *ClaudeNativeAccountPoolAdapter, string) {
	t.Helper()
	f, a := claudePoolFixture(t)
	var config claudePoolContract
	body, _ := os.ReadFile(f.contractPath)
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	config.SchemaVersion = "compound.native_account_pool.v3"
	body, _ = json.Marshal(config)
	if err := os.WriteFile(f.contractPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	f.input.Args[1] = poolDigest(body)
	authority := filepath.Join(f.root, "authority-valid")
	if err := os.WriteFile(authority, []byte("yes"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `#!/usr/bin/env python3
import json,os,sys
expected=['work','execution-authority','--work','work_codex_pool_test','--policy-decision','pol_pool_fixture','--action-intent','act_codex_pool_test','--permission-mode','plan','--json']
assert sys.argv[1:]==expected
print(json.dumps({'allow':open(os.environ['AUTHORITY_FILE']).read()=='yes'}))
`
	if err := os.WriteFile(config.CompoundCLI, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	f.input.Env = append(f.input.Env, "AUTHORITY_FILE="+authority)
	// The admission helper uses inherited environment for its read-only probe.
	t.Setenv("AUTHORITY_FILE", authority)
	f.input.ExecutionPosture = &runattempt.ExecutionPosture{PermissionMode: "plan", PolicyDecisionID: "pol_pool_fixture"}
	return f, a, authority
}

func TestClaudeReadOnlyPoolExactReservationAndCallback(t *testing.T) {
	f, a, _ := claudeReadOnlyPoolFixture(t)
	out, err := a.Run(context.Background(), f.input)
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("governed read-only launch: %+v / %v", out, err)
	}
	if out.RunAttempt.ExecutionPosture == nil || out.RunAttempt.ExecutionPosture.PolicyDecisionID != "pol_pool_fixture" {
		t.Fatal("exact policy evidence missing")
	}
	body, err := os.ReadFile(out.TranscriptPath)
	if err != nil || !strings.Contains(string(body), "provider-output") {
		t.Fatalf("worker output missing: %s / %v", body, err)
	}
}

func TestClaudeReadOnlyPoolRequiresGovernedPosture(t *testing.T) {
	f, a, _ := claudeReadOnlyPoolFixture(t)
	if _, err := a.Preflight(context.Background(), f.input); err != nil {
		t.Fatal(err)
	}
	f.input.ExecutionPosture = nil
	if _, err := a.Run(context.Background(), f.input); err == nil {
		t.Fatal("unbound v3 reservation accepted")
	}
	attempts, err := a.store.List()
	if err != nil || len(attempts) != 0 {
		t.Fatalf("unbound launch minted: %v / %v", attempts, err)
	}
}

func TestClaudeReadOnlyPoolPerAccountCallbackRefusesRevocation(t *testing.T) {
	f, a, authority := claudeReadOnlyPoolFixture(t)
	body, err := os.ReadFile(f.sb)
	if err != nil {
		t.Fatal(err)
	}
	// Revoke after the exact guarded outer subprocess has started. The
	// Switchback-owned callback must still stop native inference.
	body = []byte(strings.Replace(string(body), "args=sys.argv[1:]", "args=sys.argv[1:]\nopen(os.environ['AUTHORITY_FILE'],'w').write('no')", 1))
	if err := os.WriteFile(f.sb, body, 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := a.Run(context.Background(), f.input)
	if err != nil {
		t.Fatal(err)
	}
	if out.ExitCode != 75 || out.RunAttempt.Status != runattempt.StatusFailed {
		t.Fatalf("revoked account callback=%+v", out)
	}
	if _, err := os.Stat(f.marker); !os.IsNotExist(err) {
		t.Fatal("provider launched after revocation")
	}
	if b, _ := os.ReadFile(authority); string(b) != "no" {
		t.Fatal("revocation control never ran")
	}
}
