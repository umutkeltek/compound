package main

import (
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
	"compound/internal/work"
)

func TestWorkAdvanceClaudePoolConsumesSelectedModelOnPublicPath(t *testing.T) {
	now := time.Now().UTC()
	homePath := initializedTestHome(t)
	t.Setenv("COMPOUND_HOME", homePath)
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(work.CreateInput{Owner: "pool-selection-test", SuccessPredicate: "selected model reaches native account transport"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(w.ID, "pool-selection-test", now); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(work.SwapExecutionSelectionResolver(func(req work.ExecutionSelectionResolveRequest) (work.ExecutionSelectionResolution, error) {
		return work.ExecutionSelectionResolution{LaunchProfileID: req.LaunchProfileID, LaunchProfileRevision: "sha256:" + strings.Repeat("a", 64), Harness: "claude-native-account-pool", RequestedModelID: "fixture-native-model", ProviderRoute: "fixture-native-provider"}, nil
	}))
	root := t.TempDir()
	marker := filepath.Join(root, "native-argv.json")
	t.Setenv("POOL_SELECTION_MARKER", marker)
	sb := filepath.Join(root, "sb")
	script := `#!/usr/bin/env python3
import json,os,subprocess,sys
args=sys.argv[1:]
callback=json.loads(args[args.index('--admission-check-command')+1])
check=subprocess.run(callback,capture_output=True,text=True,timeout=10)
if check.returncode or json.loads(check.stdout).get('allow') is not True: sys.exit(75)
with open(os.environ['POOL_SELECTION_MARKER'],'w') as out: json.dump(args,out)
print('synthetic-native-output')
`
	if err := os.WriteFile(sb, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(root, "settings.json")
	if err := os.WriteFile(settings, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest := func(body []byte) string { sum := sha256.Sum256(body); return hex.EncodeToString(sum[:]) }
	_, file, _, _ := runtime.Caller(0)
	checker := filepath.Clean(filepath.Join(filepath.Dir(file), "../../adapters/native-account-pool/launch.py"))
	checkerBody, err := os.ReadFile(checker)
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]any{"schema_version": "compound.native_account_pool.v3", "provider": "claude", "accounts": []string{"seat_one", "seat_two"}, "settings_path": settings, "settings_sha256": digest([]byte("{}")), "switchback_cli": sb, "compound_cli": compoundTestBinary(t), "timeout_seconds": 20}
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config", "native-account-pools", "test.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		cmdWork([]string{"advance", w.ID, "--to", "running", "--by", "pool-selection-test", "--execute", "--harness", "claude-native-account-pool", "--launch-profile", "fixture-native-profile", "--permission-mode", "plan", "--timeout", "30s", "--workspace", root, "--prompt", "bounded read-only fixture", "--", sb, configPath, digest(body), checker, digest(checkerBody)})
	})
	argvBody, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	var argv []string
	if err := json.Unmarshal(argvBody, &argv); err != nil {
		t.Fatal(err)
	}
	found := false
	for i, arg := range argv {
		if arg == "--model" && i+1 < len(argv) && argv[i+1] == "fixture-native-model" {
			found = true
		}
	}
	if !found {
		t.Fatalf("selected model was recorded but not launched: %v", argv)
	}
	final, err := store.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != "verifying" {
		t.Fatalf("native callback did not finalize governed work: %s", final.State)
	}
	attempt, err := runattempt.Open(homePath).Get(final.RunAttemptIDs[len(final.RunAttemptIDs)-1])
	if err != nil {
		t.Fatal(err)
	}
	if attempt.ExecutionSelection == nil || attempt.ExecutionSelection.RequestedModelID != "fixture-native-model" || attempt.ExecutionSelection.ProviderRoute != "fixture-native-provider" || attempt.ExecutionSelection.Source != "override" || attempt.ExecutionPosture == nil || attempt.ExecutionPosture.PolicyDecisionID == "" {
		t.Fatalf("public selection or exact policy evidence missing: %+v", attempt)
	}
}
