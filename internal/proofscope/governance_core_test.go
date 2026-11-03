package proofscope

import (
	"reflect"
	"testing"
)

func TestGovernanceCoreMatchesTheJudgeOfProofsOnly(t *testing.T) {
	core := []string{
		"internal/work/land_check.go", "internal/work/proof_presubmit.go", "internal/work/work.go",
		"internal/work/verifier_verdict.go", "cmd/compound/proof_check_dev.go", "internal/policy/x/y.go",
		"internal/envelope/envelope.go", "internal/hook/hook.go", "internal/landlease/lease.go",
		"internal/proofscope/presubmit.go", "scripts/testbattery/main.go",
	}
	notCore := []string{
		"internal/work/workbench.go", "internal/work/executewire/proof_x.go", "cmd/compound/work_cli.go",
		"skills/compound-land/SKILL.md", "AGENTS.md", "docs/architecture/runtime.md", "internal/memory/x.go",
		"internal/work/work.go.orig", "internal/policyx/a.go",
	}
	for _, p := range core {
		if !IsGovernanceCore(p) {
			t.Errorf("%s must be governance core", p)
		}
	}
	for _, p := range notCore {
		if IsGovernanceCore(p) {
			t.Errorf("%s must not be governance core", p)
		}
	}
	got := GovernanceCore(append(append([]string{""}, notCore...), "internal/work/work.go", "internal/hook/a.go"))
	if want := []string{"internal/hook/a.go", "internal/work/work.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("GovernanceCore = %v; want %v", got, want)
	}
}
