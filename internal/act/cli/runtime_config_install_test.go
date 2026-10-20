package cli

import (
	"bytes"
	"compound/internal/act"
	"compound/internal/policy"
	"context"
	"strings"
	"testing"
)

func TestRunRuntimeConfigInstallUsesGloballyResolvedHome(t *testing.T) {
	h := t.TempDir()
	t.Setenv("COMPOUND_HOME", h)
	prior := newRuntimeConfigInstallOwner
	defer func() { newRuntimeConfigInstallOwner = prior }()
	spy := &runtimeInstallerCLISpy{}
	newRuntimeConfigInstallOwner = func(got string) (runtimeConfigInstallOwner, error) {
		if got != h {
			t.Fatalf("home=%q", got)
		}
		return spy, nil
	}
	var stdout, stderr bytes.Buffer
	code := RunRuntimeConfigInstall([]string{"execute", "--plan-ref", "fixture-ref", "--actor", "fixture", "--json"}, &stdout, &stderr)
	if code != 0 || spy.executions != 1 {
		t.Fatalf("code=%d err=%s", code, stderr.String())
	}
	code = RunRuntimeConfigInstall([]string{"recovery-plan", "--plan-ref", "original-ref", "--operation", "rollback", "--work", "work_recovery", "--actor", "fixture", "--json"}, &stdout, &stderr)
	if code != 0 || spy.recoveries != 1 {
		t.Fatalf("recovery code=%d err=%s", code, stderr.String())
	}
}

type runtimeInstallerCLISpy struct{ executions, recoveries int }

func (s *runtimeInstallerCLISpy) Plan(policy.RuntimeConfigInstallRequest, string, string) (act.RuntimeConfigInstallPlanResult, error) {
	return act.RuntimeConfigInstallPlanResult{}, nil
}
func (s *runtimeInstallerCLISpy) PlanRecovery(string, string, string, string) (act.RuntimeConfigInstallPlanResult, error) {
	s.recoveries++
	return act.RuntimeConfigInstallPlanResult{}, nil
}
func (s *runtimeInstallerCLISpy) Execute(context.Context, string, string) (act.RuntimeConfigInstallResult, error) {
	s.executions++
	return act.RuntimeConfigInstallResult{}, nil
}

func TestRuntimeConfigInstallUsageRejectsUnknownFlags(t *testing.T) {
	var stderr bytes.Buffer
	code := RunRuntimeConfigInstall([]string{"plan", "--rogue", "x", "--work", "/w", "--request-file", "/r.json", "--actor", "fixture", "--home", "/h"}, &bytes.Buffer{}, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "compound act runtime-config-install") {
		t.Fatalf("expected usage refusal for unknown flag, code=%d stderr=%q", code, stderr.String())
	}
}

func TestRuntimeConfigInstallUsageRequiresAbsoluteHome(t *testing.T) {
	var stderr bytes.Buffer
	code := RunRuntimeConfigInstall([]string{"execute", "--plan-ref", "x", "--actor", "fixture", "--home", "relative"}, &bytes.Buffer{}, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "compound act runtime-config-install") {
		t.Fatalf("expected usage refusal for relative home, code=%d stderr=%q", code, stderr.String())
	}
}
