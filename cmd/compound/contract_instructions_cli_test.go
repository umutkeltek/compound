package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"compound/internal/contract"
	"compound/internal/home"
)

const contractInstructionsChildEnv = "COMPOUND_TEST_CONTRACT_INSTRUCTIONS_CHILD"

func TestContractCompileInstructionsCheckFailsOnDriftAndPassesOnExactBlock(t *testing.T) {
	if os.Getenv(contractInstructionsChildEnv) == "1" {
		cmdContract([]string{
			"compile",
			"--harness", "codex",
			"--cwd", os.Getenv("COMPOUND_TEST_INSTRUCTION_REPO"),
			"--task", "task-scoped text must not enter the static block",
			"--repo-kind", "control-plane",
			"--mode", "execute",
			"--instructions",
			"--check-instructions",
		})
		return
	}

	homePath := filepath.Join(t.TempDir(), ".compound")
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	repoRoot := t.TempDir()
	target := filepath.Join(repoRoot, "AGENTS.md")
	if err := os.WriteFile(target, []byte("# unmanaged\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func() ([]byte, error) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestContractCompileInstructionsCheckFailsOnDriftAndPassesOnExactBlock$")
		cmd.Env = append(os.Environ(),
			contractInstructionsChildEnv+"=1",
			"COMPOUND_HOME="+homePath,
			"COMPOUND_TEST_INSTRUCTION_REPO="+repoRoot,
		)
		return cmd.CombinedOutput()
	}

	driftOutput, err := run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("drift check exit = %v, want 1; output:\n%s", err, driftOutput)
	}
	var drift contract.InstructionBlockRender
	if err := json.NewDecoder(bytes.NewReader(driftOutput)).Decode(&drift); err != nil {
		t.Fatalf("parse drift render: %v\n%s", err, driftOutput)
	}
	if !drift.Drifted || drift.Content == "" || drift.TargetPath != target {
		t.Fatalf("drift render = %+v", drift)
	}
	if err := os.WriteFile(target, []byte("# unmanaged\n\n"+drift.Content), 0o644); err != nil {
		t.Fatal(err)
	}

	cleanOutput, err := run()
	if err != nil {
		t.Fatalf("exact block check failed: %v\n%s", err, cleanOutput)
	}
	var clean contract.InstructionBlockRender
	if err := json.NewDecoder(bytes.NewReader(cleanOutput)).Decode(&clean); err != nil {
		t.Fatalf("parse clean render: %v\n%s", err, cleanOutput)
	}
	if clean.Drifted || clean.ObservedSHA256 != clean.ContentSHA256 {
		t.Fatalf("clean render = %+v", clean)
	}
}
