package main

import (
	"compound/internal/console"
	"os"
	"path/filepath"
	"testing"
)

func TestConsolePairRefusesNoninteractiveAndExistingOutput(t *testing.T) {
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pipeR.Close()
	defer pipeW.Close()
	if _, _, err := consolePairOutput(nil, pipeW); err == nil {
		t.Fatal("pair code permitted in redirected stdout")
	}
	path := filepath.Join(t.TempDir(), "pair-code")
	output, closeOutput, err := consolePairOutput([]string{"--out", path}, pipeW)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("synthetic-secret")); err != nil {
		t.Fatal(err)
	}
	closeOutput()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("pair output not private")
	}
	if _, _, err := consolePairOutput([]string{"--out", path}, pipeW); err == nil {
		t.Fatal("pair output overwrote existing file")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := consolePairOutput([]string{"--out", link}, pipeW); err == nil {
		t.Fatal("pair output followed symlink")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "synthetic-secret" {
		t.Fatal("existing code changed")
	}
}

func TestConsolePairRequiresExplicitHomeBeforeIssuance(t *testing.T) {
	t.Setenv("COMPOUND_HOME", "")
	if err := runConsolePair(nil, os.Stdout); err == nil {
		t.Fatal("pairing used implicit home")
	}
}

func TestConsoleOperatorSessionSupervisorOwnsAndCleansTransport(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "cc-supervisor-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	t.Setenv("TMPDIR", root)
	t.Setenv("COMPOUND_HOME", filepath.Join(root, "home"))
	t.Setenv(console.CapabilityEnv, consoleTestFormatValidCapability)
	consoleDir := filepath.Join(root, "console")
	if err := os.Mkdir(consoleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consoleDir, "package.json"), []byte(`{"private":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := buildConsoleLifecycleLaunchPlan(consoleLifecycleDev, consoleDir, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = console.EnableOperatorSession(plan)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "check-child.sh")
	const source = `#!/bin/sh
test "$COMPOUND_CONSOLE_OPERATOR_SESSION" = 1 &&
test -S "$COMPOUND_CONSOLE_OPERATOR_SOCKET" &&
test "${#COMPOUND_CONSOLE_OPERATOR_BROKER_KEY}" = 64 &&
test "${#COMPOUND_CONSOLE_OPERATOR_INSTANCE}" = 64
`
	if err := os.WriteFile(script, []byte(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runConsoleSupervisedCommand(plan, []string{script}, make(chan os.Signal, 1)); err != nil {
		t.Fatalf("supervised child lacked native transport: %v", err)
	}
	path, err := console.OperatorSocketPath(plan.Env["COMPOUND_HOME"])
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(filepath.Join(filepath.Dir(path), "owner.lock"))
		_ = os.Remove(filepath.Dir(path))
	}()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("supervisor retained live socket after return: %v", err)
	}
	if _, _, err := console.IssueOperatorPairingCode(plan.Env["COMPOUND_HOME"]); err == nil {
		t.Fatal("stopped supervisor issued pairing code")
	}
	if err := runConsoleSupervisedCommand(plan, []string{filepath.Join(root, "missing-executable")}, make(chan os.Signal, 1)); err == nil {
		t.Fatal("missing child executable accepted")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("start failure retained broker: %v", err)
	}
	for _, key := range []string{console.OperatorBrokerKeyEnv, console.OperatorSocketEnv, console.OperatorInstanceEnv} {
		if _, ok := plan.Env[key]; ok {
			t.Fatal("supervisor placed ephemeral secret in launch plan")
		}
	}
}
