package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/contract"
	"compound/internal/hookplan"
	"compound/internal/work"
)

// The first-party unattended lane's installed PreToolUse is run for real: the
// probe hands the built binary a security-relevant tool call and the lane must
// block it, while the interactive Claude Code binding from the same install
// only observes. A lane that existed only as a name would have no hook to run.
func TestUnattendedLaneInstalledHookBlocksWithRealBinary(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("builds the compound binary: skipped in -short mode")
	}
	real := buildRealCompound(t)
	base := t.TempDir()
	if _, err := hookplan.InstallHooks(base, real, "global", []string{"claude-code", "claude-unattended"}, false); err != nil {
		t.Fatal(err)
	}
	lanePath, ok := hookplan.LaneHookSettingsPath(base, "claude_unattended")
	if !ok {
		t.Fatal("claude_unattended has no lane profile")
	}
	interactivePath := filepath.Join(base, hookplan.ConfigRel("claude-code", "global"))

	rows, failing := buildHookEnforcementRows([]doctorHook{{Harness: "claude-unattended", ConfigPath: lanePath}})
	if failing != 0 || len(rows) != 1 || rows[0].Posture != hookplan.HookPostureEnforced || rows[0].Status != "enforcing" || rows[0].Detail != "" {
		t.Fatalf("lane hook rows = %+v (failing=%d), want one passing enforcing row", rows, failing)
	}
	rows, failing = buildHookEnforcementRows([]doctorHook{{Harness: "claude-code", ConfigPath: interactivePath}})
	if failing != 0 || len(rows) != 1 || rows[0].Posture != hookplan.HookPostureObservationOnly || rows[0].Status != "observing" {
		t.Fatalf("interactive hook rows = %+v (failing=%d), want one passing observing row", rows, failing)
	}
}

// Readiness for the lane reads the lane's own profile: an installed blocking
// hook clears both hook findings, the interactive harness from the same
// install stays non-blocking, and removing the lane's PreToolUse fails it.
func TestUnattendedLaneReadinessReadsItsOwnBlockingHook(t *testing.T) {
	compoundHome := t.TempDir()
	harnessHome := t.TempDir()
	t.Setenv("HOME", harnessHome)
	now := time.Date(2026, 11, 11, 20, 0, 0, 0, time.UTC)
	binary := filepath.Join(harnessHome, ".local", "bin", "compound")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("// compound test fixture: synthetic handler bytes\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	publish := func(report hookplan.HookInstallReport) error {
		_, err := contract.ImportHookInstallReport(compoundHome, harnessHome, report, now)
		return err
	}
	if _, err := hookplan.InstallHooksWithConfigDirAndPublish(harnessHome, binary, "global", []string{"claude-code", "claude-unattended"}, "", false, publish); err != nil {
		t.Fatal(err)
	}

	evaluate := func(harnessName string) work.DelegateHarnessReadinessResult {
		t.Helper()
		result, err := evaluateDelegateHarnessReadiness(work.DelegateHarnessReadinessRequest{
			HomePath: compoundHome, HarnessHome: harnessHome, Harness: harnessName, Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	hookDetails := func(result work.DelegateHarnessReadinessResult) []string {
		var out []string
		for _, detail := range result.Details {
			if strings.HasPrefix(detail, "blocking_hook") {
				out = append(out, detail)
			}
		}
		return out
	}

	lane := evaluate("claude_unattended")
	if got := hookDetails(lane); len(got) != 0 {
		t.Fatalf("installed lane hook findings = %q, want none", got)
	}
	kinds := map[string]bool{}
	for _, item := range lane.Evidence {
		kinds[item.Kind] = true
	}
	if !kinds["blocking_hook_plan"] || !kinds["blocking_hook_installation"] {
		t.Fatalf("lane readiness evidence kinds = %v, want the blocking hook plan and installation", kinds)
	}

	interactive := evaluate("claude")
	if !strings.Contains(strings.Join(hookDetails(interactive), "\n"), "blocking_hook: status=non_blocking") {
		t.Fatalf("interactive Claude Code readiness = %q, want it to stay non-blocking", hookDetails(interactive))
	}

	lanePath, _ := hookplan.LaneHookSettingsPath(harnessHome, "claude_unattended")
	removeDelegateFixturePreToolUse(t, lanePath)
	removed := evaluate("claude_unattended")
	if removed.Ready || !strings.Contains(strings.Join(removed.Details, "\n"), "blocking_hook_live: mutation-covering binding status=missing") {
		t.Fatalf("lane without its PreToolUse = %+v, want blocking_hook_live missing", removed.Details)
	}
}
