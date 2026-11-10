package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"compound/internal/runattempt"
)

func poolTestStore(t *testing.T, root string) *runattempt.Store {
	t.Helper()
	home := filepath.Join(root, "attempt-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return runattempt.Open(home)
}

func poolTestDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

type poolFixture struct {
	root, contractPath, marker, native, sb string
	config                                 codexPoolContract
	digest                                 string
	input                                  RunInput
}

func newPoolFixture(t *testing.T) *poolFixture {
	t.Helper()
	root := t.TempDir()
	nativeDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(nativeDir, "codex")
	nativeBody := append([]byte{0xcf, 0xfa, 0xed, 0xfe}, []byte("native-test")...)
	if err := os.WriteFile(native, nativeBody, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", nativeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	compound := filepath.Join(nativeDir, "compound")
	presence := "#!/bin/sh\nprintf '%s' '{\"effective\":\"away\",\"presence\":{\"state\":\"away\",\"from\":\"2020-01-01T00:00:00Z\",\"through\":\"2099-01-01T00:00:00Z\"}}'\n"
	if err := os.WriteFile(compound, []byte(presence), 0o700); err != nil {
		t.Fatal(err)
	}
	sb := filepath.Join(nativeDir, "sb")
	fakeSB := `#!/usr/bin/env python3
import json,os,subprocess,sys
args=sys.argv[1:]
callback=json.loads(args[args.index('--admission-check-command')+1])
result=subprocess.run(callback,capture_output=True,text=True,timeout=8)
if result.returncode or json.loads(result.stdout).get('allow') is not True: sys.exit(75)
with open(os.environ['FAKE_MARKER'],'a') as f: f.write(json.dumps(args)+'\n')
if os.environ.get('FAKE_CAPTURE_FAIL')=='1':
 print('account: native task completed; capture is unverified; task was not replayed',file=sys.stderr)
 sys.exit(74)
print('provider-output')
`
	if err := os.WriteFile(sb, []byte(fakeSB), 0o700); err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	checker := filepath.Clean(filepath.Join(filepath.Dir(file), "../../adapters/native-account-pool/launch.py"))
	checkerBody, err := os.ReadFile(checker)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "compound-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	contractDir := filepath.Join(root, "config", "native-account-pools")
	if err := os.MkdirAll(contractDir, 0o700); err != nil {
		t.Fatal(err)
	}
	f := &poolFixture{
		root: root, contractPath: filepath.Join(contractDir, "approved.json"),
		marker: filepath.Join(root, "launches.jsonl"), native: native, sb: sb,
	}
	f.config = codexPoolContract{
		SchemaVersion: codexPoolSchema, Provider: "codex", ProfileID: "work_profile", PoolName: "work_pool",
		ProfileRevision: "sha256:" + strings.Repeat("a", 64), AuthorityRevision: "sha256:" + strings.Repeat("b", 64),
		Accounts:         []string{"seat1", "seat2"},
		AccountSettings:  map[string]string{"seat1": "sha256:" + strings.Repeat("c", 64), "seat2": "missing"},
		NativeExecutable: native, NativeSHA256: poolTestDigest(nativeBody),
		SwitchbackCLI: sb, CompoundCLI: compound, CompoundHome: home,
		CheckerPath: checker, CheckerSHA256: poolTestDigest(checkerBody),
		ApprovalPolicy: "never", Sandbox: "read-only", TimeoutSeconds: 90, RequireCapture: true,
	}
	f.save(t)
	f.input = RunInput{
		WorkID: "work_codex_pool_test", Harness: codexPoolAdapterName,
		Workspace: root, Command: sb, Args: []string{f.contractPath, f.digest},
		Prompt: "synthetic task", PermissionMode: "plan", Timeout: 2 * time.Minute,
		Now: time.Now().UTC(), ActionIntentID: "act_codex_pool_test",
		Env:                        []string{"FAKE_MARKER=" + f.marker},
		ExecutionSelectionSnapshot: &runattempt.ExecutionSelectionSnapshot{Source: "project_default", Harness: codexPoolAdapterName},
		StartRevalidate:            func() error { return nil },
		StartGuard:                 func(_ context.Context, start func() error) error { return start() },
	}
	return f
}

func (f *poolFixture) save(t *testing.T) {
	t.Helper()
	body, err := json.Marshal(f.config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.contractPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	f.digest = poolTestDigest(body)
}

func TestCodexPoolReadinessNeedsExplicitContract(t *testing.T) {
	got := ReadOnlyLaunchReadiness(t.TempDir(), RunInput{Harness: codexPoolAdapterName})
	if len(got) != 1 || !strings.Contains(got[0], "explicit contract path and SHA256") {
		t.Fatalf("missing contract projected ready: %v", got)
	}
}

func TestCodexPoolSelectionPassesModelButRefusesEffort(t *testing.T) {
	model := &runattempt.ExecutionSelectionSnapshot{
		Source: "project_default", Harness: codexPoolAdapterName, RequestedModelID: "gpt-synthetic",
	}
	if err := CheckSelectionApplicable(codexPoolAdapterName, model); err != nil {
		t.Fatal(err)
	}
	model.RequestedEffort = "high"
	if err := CheckSelectionApplicable(codexPoolAdapterName, model); err == nil {
		t.Fatal("unattested effort accepted")
	}
}

func TestCodexPoolPinnedLaunchAndSelectionEvidence(t *testing.T) {
	f := newPoolFixture(t)
	a := NewCodexNativeAccountPoolAdapter(poolTestStore(t, f.root))
	if _, err := a.Preflight(context.Background(), f.input); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	out, err := a.Run(context.Background(), f.input)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 0 || out.RunAttempt.Status != runattempt.StatusSucceeded {
		t.Fatalf("status=%s exit=%d", out.RunAttempt.Status, out.ExitCode)
	}
	if out.RunAttempt.ExecutionSelection == nil || out.RunAttempt.ExecutionSelection.AccountPoolContractSHA256 != "sha256:"+f.digest || out.RunAttempt.ExecutionSelection.AccountPoolContractRef != f.contractPath {
		t.Fatalf("contract selection evidence missing: %+v", out.RunAttempt.ExecutionSelection)
	}
	body, err := os.ReadFile(f.marker)
	if err != nil {
		t.Fatal(err)
	}
	var argv []string
	if err := json.Unmarshal(bytesTrim(body), &argv); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--profile", "--expected-pool-name", "--expected-profile-revision", "--expected-authority-revision",
		"--expected-native-executable", "--expected-native-sha256", "--expected-account-settings-json",
		"--require-capture", "--approval-policy", "--sandbox", "--admission-check-command"} {
		if !poolContainsString(argv, flag) {
			t.Errorf("missing %s", flag)
		}
	}
	if got := argv[indexOf(argv, "--profile")+1]; got != f.config.ProfileID {
		t.Fatalf("profile=%q", got)
	}
	if poolContainsString(argv, "--pool-name") || argv[indexOf(argv, "--expected-pool-name")+1] != f.config.PoolName {
		t.Fatal("pool must be asserted without a conflicting selector")
	}
	if got := argv[indexOf(argv, "--sandbox")+1]; got != "read-only" {
		t.Fatalf("sandbox=%q", got)
	}
}

func bytesTrim(data []byte) []byte                         { return []byte(strings.TrimSpace(string(data))) }
func poolContainsString(values []string, want string) bool { return indexOf(values, want) >= 0 }
func indexOf(values []string, want string) int {
	for i, v := range values {
		if v == want {
			return i
		}
	}
	return -1
}

func TestCodexPoolDriftAndPermissionRefusals(t *testing.T) {
	f := newPoolFixture(t)
	a := NewCodexNativeAccountPoolAdapter(poolTestStore(t, f.root))
	original := f.input
	f.config.ProfileRevision = "sha256:" + strings.Repeat("d", 64)
	f.save(t)
	if _, err := a.Preflight(context.Background(), original); err == nil || !strings.Contains(err.Error(), "contract revision changed") {
		t.Fatalf("config drift accepted: %v", err)
	}
	f.input.Args[1] = f.digest
	f.input.PermissionMode = "bypass"
	if _, err := a.Preflight(context.Background(), f.input); err == nil || !strings.Contains(err.Error(), "permission mode") {
		t.Fatalf("permission mismatch accepted: %v", err)
	}
	f.config.Sandbox = "workspace-write"
	f.save(t)
	f.input.Args[1] = f.digest
	if _, err := a.Preflight(context.Background(), f.input); err != nil {
		t.Fatalf("governed bypass preflight: %v", err)
	}
	manual := f.input
	manual.StartGuard = nil
	if _, err := a.Run(context.Background(), manual); err == nil || !strings.Contains(err.Error(), "governed work reservation") {
		t.Fatalf("manual pool run accepted: %v", err)
	}
	native, _ := os.ReadFile(f.native)
	if err := os.WriteFile(f.native, append(native, 'x'), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Preflight(context.Background(), f.input); err == nil || !strings.Contains(err.Error(), "native Codex executable changed") {
		t.Fatalf("native drift accepted: %v", err)
	}
	if _, err := os.Stat(f.marker); !os.IsNotExist(err) {
		t.Fatalf("Switchback called on refusal: %v", err)
	}
}

func TestCodexPoolGovernedWorkspaceWritePosture(t *testing.T) {
	f := newPoolFixture(t)
	f.config.Sandbox = "workspace-write"
	f.save(t)
	f.input.Args[1] = f.digest
	f.input.PermissionMode = "bypass"
	a := NewCodexNativeAccountPoolAdapter(poolTestStore(t, f.root))
	out, err := a.Run(context.Background(), f.input)
	if err != nil || out.ExitCode != 0 || out.RunAttempt.Status != runattempt.StatusSucceeded {
		t.Fatalf("governed workspace write: out=%+v err=%v", out, err)
	}
	body, err := os.ReadFile(f.marker)
	if err != nil {
		t.Fatal(err)
	}
	var argv []string
	if err := json.Unmarshal(bytesTrim(body), &argv); err != nil {
		t.Fatal(err)
	}
	if argv[indexOf(argv, "--approval-policy")+1] != "never" ||
		argv[indexOf(argv, "--sandbox")+1] != "workspace-write" {
		t.Fatalf("permission drift in native argv: %v", argv)
	}
	f.config.ApprovalPolicy = "on-request"
	f.save(t)
	f.input.Args[1] = f.digest
	if _, err := a.Preflight(context.Background(), f.input); err == nil {
		t.Fatal("bypass accepted with on-request approval")
	}
	f.input.PermissionMode = "default"
	if _, err := a.Preflight(context.Background(), f.input); err != nil {
		t.Fatalf("default/on-request mapping refused: %v", err)
	}
}

func TestCodexPoolStartGuardRechecksContract(t *testing.T) {
	f := newPoolFixture(t)
	a := NewCodexNativeAccountPoolAdapter(poolTestStore(t, f.root))
	f.input.StartGuard = func(_ context.Context, start func() error) error {
		f.config.AuthorityRevision = "sha256:" + strings.Repeat("e", 64)
		f.save(t)
		return start()
	}
	out, err := a.Run(context.Background(), f.input)
	if err == nil && out.ExitCode == 0 {
		t.Fatal("start drift launched")
	}
	if _, err := os.Stat(f.marker); !os.IsNotExist(err) {
		t.Fatalf("Switchback called after CAS drift: %v", err)
	}
}

func TestCodexPoolCapture74IsFailedAndNotReplayed(t *testing.T) {
	f := newPoolFixture(t)
	f.input.Env = append(f.input.Env, "FAKE_CAPTURE_FAIL=1")
	a := NewCodexNativeAccountPoolAdapter(poolTestStore(t, f.root))
	out, err := a.Run(context.Background(), f.input)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 74 || out.RunAttempt.Status != runattempt.StatusFailed {
		t.Fatalf("capture status=%s exit=%d", out.RunAttempt.Status, out.ExitCode)
	}
	transcript, err := os.ReadFile(out.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(transcript), "native task completed; capture is unverified; task was not replayed") {
		t.Fatal("capture failure lost its non-replay diagnostic")
	}
	body, err := os.ReadFile(f.marker)
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.Split(strings.TrimSpace(string(body)), "\n")) != 1 {
		t.Fatal("capture failure retried")
	}
}
