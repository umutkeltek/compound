package main

// R3A slice 2 — production wiring tests for the unattended execute stage's
// hooks. The hooks default fail-closed inside internal/work; cmd/compound is
// the composition root that binds them to the real routeevidence / policy /
// actuator resolvers. Each test here pins one wired resolver by driving the
// seam end to end (store.Continue) against a temp home, so a regression in
// the wiring shows up as the wrong `skipped=` refusal reason.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/routeevidence"
	"compound/internal/work"
)

// seedExecuteStageManifest writes a minimal model-capabilities authority whose
// routing_defaults name one lane for work.continue.unattended with the given
// claim level ("capable" -> gate_role eligible, "avoid" -> gate_role avoid).
func seedExecuteStageManifest(t *testing.T, homePath, claimLevel string) {
	t.Helper()
	body := "version: 1\n" +
		"purpose: execute-stage wiring fixture\n" +
		"models:\n" +
		"  claude-minimax (token plan):\n" +
		"    lane: claude-minimax (token plan)\n" +
		"    harness: claude_minimax\n" +
		"    tier: cheap\n" +
		"    packet_ceiling: multi-file\n" +
		"    capabilities:\n" +
		"      work.continue.unattended:\n" +
		"        level: " + claimLevel + "\n" +
		"        evidence: seeded execute-stage wiring fixture\n" +
		"routing_defaults:\n" +
		"  work.continue.unattended:\n" +
		"    - claude-minimax (token plan)\n"
	dir := filepath.Join(homePath, "registries")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model-capabilities.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedExecuteStageGrant installs the unattended standing grant so gate (a)
// passes and the later gates are reachable.
func seedExecuteStageGrant(t *testing.T, homePath string) {
	t.Helper()
	dir := filepath.Join(homePath, "registries")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"schema_version":"compound.standing_grants.v0.1","grants":[{` +
		`"id":"grant_unattended_work_continue_v1","capability":"work.continue.unattended",` +
		`"mutation_class":"local_reversible","reversibility_ceiling":"reversible",` +
		`"blast_radius":"local","target_path":"` + homePath + `/**","expires_at":"2099-01-01T00:00:00Z"}]}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "standing-grants.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func seedExecuteStageReadyWork(t *testing.T, store *work.Store, id string) {
	t.Helper()
	if _, err := store.Create(work.CreateInput{
		WorkID:           id,
		Owner:            "compound-core",
		Actor:            "codex",
		State:            "ready",
		Risk:             "tier1",
		Privacy:          "private",
		SuccessPredicate: "execute-stage wiring fixture predicate",
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

// restoreExecuteStageHooks snapshots every seam hook the wiring installs and
// restores them at test end, so a wiring test never leaks its stubs into the
// package's other tests.
func restoreExecuteStageHooks(t *testing.T) {
	t.Helper()
	restoreRoute := work.SwapExecuteStageRoutePlan(executeStageRouteFromManifest)
	restoreCapacity := work.SwapExecuteStageCapacity(func(string, string, time.Time) (float64, bool, error) {
		return 0, false, nil
	})
	restorePolicy := work.SwapExecuteStagePolicyEval(func(ai work.PolicyActionIntentShim, _ work.PolicyConfigShim, _ time.Time) work.PolicyDecisionShim {
		return work.PolicyDecisionShim{Outcome: "allow", ID: "pol_wire_test_allow"}
	})
	restoreLaunch := work.SwapExecuteStageLaunch(func(*work.Store, string, string, string, string, time.Time) (string, error) {
		t.Error("execute stage must not launch while upstream gates refuse")
		return "ratt_should_not_run", nil
	})
	t.Cleanup(func() {
		restoreRoute()
		restoreCapacity()
		restorePolicy()
		restoreLaunch()
	})
}

// TestContinueRouteHookReachesCapacityGateWithEligibleLane: with a seeded
// route plan naming an eligible lane (gate_role eligible, harness the adapter
// registry knows), the stage passes the route gate and refuses at the CAPACITY
// gate (the stubbed capacity reader reports stale). If the route hook were
// still the fail-closed default, this would come back no_route.
func TestContinueRouteHookReachesCapacityGateWithEligibleLane(t *testing.T) {
	homePath := initializedTestHome(t)
	seedExecuteStageManifest(t, homePath, "capable")
	seedExecuteStageGrant(t, homePath)
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	seedExecuteStageReadyWork(t, store, "work_route_eligible")
	restoreExecuteStageHooks(t)

	res, err := store.Continue(time.Now().UTC(), work.ContinueOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != "capacity_unknown" {
		t.Fatalf("skipped = %q, want capacity_unknown (route gate passed, capacity stub is stale); result=%+v", res.Skipped, res)
	}
	if res.Lane != "claude_minimax" {
		t.Fatalf("lane = %q, want claude_minimax from the seeded route plan", res.Lane)
	}
}

// TestContinueRouteHookReturnsNoRouteOnAvoidGateRole: a route plan whose only
// lane claims level avoid (gate_role avoid) must fail closed at the route gate.
func TestContinueRouteHookReturnsNoRouteOnAvoidGateRole(t *testing.T) {
	homePath := initializedTestHome(t)
	seedExecuteStageManifest(t, homePath, "avoid")
	seedExecuteStageGrant(t, homePath)
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	seedExecuteStageReadyWork(t, store, "work_route_avoid")
	restoreExecuteStageHooks(t)

	res, err := store.Continue(time.Now().UTC(), work.ContinueOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != "no_route" {
		t.Fatalf("skipped = %q, want no_route (gate_role avoid); result=%+v", res.Skipped, res)
	}
	if strings.TrimSpace(res.Lane) != "" {
		t.Fatalf("lane = %q, want empty on no_route", res.Lane)
	}
}

// executeStageCapacityTestConfig isolates the capacity row sources to the
// fixture home so the reader never touches the operator's real CodexBar feed
// (DefaultLaneCapacityConfig points UsageHistoryPath at the user's home).
func executeStageCapacityTestConfig(t *testing.T, homePath string) {
	t.Helper()
	previous := executeStageCapacityConfig
	t.Cleanup(func() { executeStageCapacityConfig = previous })
	executeStageCapacityConfig = func(home string) routeevidence.LaneCapacityConfig {
		cfg := routeevidence.DefaultLaneCapacityConfig(home)
		cfg.Paths.UsageHistoryPath = filepath.Join(homePath, "usage-history.jsonl")
		cfg.Paths.QuotaCachePath = ""
		return cfg
	}
}

// seedExecuteStageCapacityRow appends one CodexBar-shaped usage row for the
// minimax family (what claude_minimax resolves to) sampled ageAgo at used%.
func seedExecuteStageCapacityRow(t *testing.T, homePath string, usedPercent int, ageAgo time.Duration, now time.Time) {
	t.Helper()
	row := fmt.Sprintf(`{"provider":"minimax","windowKind":"rolling","windowMinutes":60,"usedPercent":%d,"resetsAt":%q,"sampledAt":%q}`+"\n",
		usedPercent,
		now.Add(time.Hour).UTC().Format(time.RFC3339),
		now.Add(-ageAgo).UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(homePath, "usage-history.jsonl"), []byte(row), 0o644); err != nil {
		t.Fatal(err)
	}
}

// wiredCapacityHooks installs the real capacity resolver with everything else
// stubbed green (route from the seeded manifest, policy allow, recording
// launch) so a test can observe exactly where the capacity gate lands.
func wiredCapacityHooks(t *testing.T, homePath string) *bool {
	t.Helper()
	restoreExecuteStageHooks(t)
	restoreCapacity := work.SwapExecuteStageCapacity(executeStageCapacityFromSnapshot)
	t.Cleanup(restoreCapacity)
	launched := new(bool)
	restoreLaunch := work.SwapExecuteStageLaunch(func(_ *work.Store, _, lane, _harness, _ string, _ time.Time) (string, error) {
		*launched = true
		if lane != "claude_minimax" {
			t.Errorf("launch lane = %q, want claude_minimax", lane)
		}
		return "ratt_capacity_wire", nil
	})
	t.Cleanup(restoreLaunch)
	return launched
}

// TestContinueCapacityHookPassesFreshSnapshotAt30: a snapshot 2 minutes old
// at 30% passes the capacity gate and the stage reaches the launch.
func TestContinueCapacityHookPassesFreshSnapshotAt30(t *testing.T) {
	homePath := initializedTestHome(t)
	now := time.Now().UTC()
	seedExecuteStageManifest(t, homePath, "capable")
	seedExecuteStageGrant(t, homePath)
	seedExecuteStageCapacityRow(t, homePath, 30, 2*time.Minute, now)
	executeStageCapacityTestConfig(t, homePath)
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	seedExecuteStageReadyWork(t, store, "work_cap_fresh")
	launched := wiredCapacityHooks(t, homePath)

	used, fresh, err := executeStageCapacityFromSnapshot(homePath, "claude_minimax", now)
	if err != nil {
		t.Fatal(err)
	}
	if used != 30 || !fresh {
		t.Fatalf("capacity = (%v, %v, nil), want (30, true)", used, fresh)
	}

	res, err := store.Continue(now, work.ContinueOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !*launched {
		t.Fatalf("fresh 30%% capacity must reach the launch; result=%+v", res)
	}
	if res.Action != "execute" {
		t.Fatalf("action = %q, want execute", res.Action)
	}
}

// TestContinueCapacityHookRefusesStaleSnapshot: a snapshot 11 minutes old is
// outside the 10-minute window — refuse, never skip the check.
func TestContinueCapacityHookRefusesStaleSnapshot(t *testing.T) {
	homePath := initializedTestHome(t)
	now := time.Now().UTC()
	seedExecuteStageManifest(t, homePath, "capable")
	seedExecuteStageGrant(t, homePath)
	seedExecuteStageCapacityRow(t, homePath, 30, 11*time.Minute, now)
	executeStageCapacityTestConfig(t, homePath)
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	seedExecuteStageReadyWork(t, store, "work_cap_stale")
	launched := wiredCapacityHooks(t, homePath)

	_, fresh, err := executeStageCapacityFromSnapshot(homePath, "claude_minimax", now)
	if err != nil {
		t.Fatal(err)
	}
	if fresh {
		t.Fatal("11-minute-old snapshot must not be fresh (window is 10m)")
	}
	res, err := store.Continue(now, work.ContinueOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != "capacity_unknown" {
		t.Fatalf("skipped = %q, want capacity_unknown (stale); result=%+v", res.Skipped, res)
	}
	if *launched {
		t.Fatal("stale capacity must not reach the launch")
	}
}

// TestContinueCapacityHookRefusesCeiling: a fresh snapshot at 90% refuses —
// the ceiling is strict less-than.
func TestContinueCapacityHookRefusesCeiling(t *testing.T) {
	homePath := initializedTestHome(t)
	now := time.Now().UTC()
	seedExecuteStageManifest(t, homePath, "capable")
	seedExecuteStageGrant(t, homePath)
	seedExecuteStageCapacityRow(t, homePath, 90, 2*time.Minute, now)
	executeStageCapacityTestConfig(t, homePath)
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	seedExecuteStageReadyWork(t, store, "work_cap_ceiling")
	launched := wiredCapacityHooks(t, homePath)

	res, err := store.Continue(now, work.ContinueOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != "capacity_unknown" {
		t.Fatalf("skipped = %q, want capacity_unknown (90%% meets the ceiling); result=%+v", res.Skipped, res)
	}
	if *launched {
		t.Fatal("capacity at the ceiling must not reach the launch")
	}
}

// TestContinueCapacityHookRefusesMissingSnapshot: no snapshot at all refuses
// (fail closed, never skip the check).
func TestContinueCapacityHookRefusesMissingSnapshot(t *testing.T) {
	homePath := initializedTestHome(t)
	now := time.Now().UTC()
	seedExecuteStageManifest(t, homePath, "capable")
	seedExecuteStageGrant(t, homePath)
	executeStageCapacityTestConfig(t, homePath)
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	seedExecuteStageReadyWork(t, store, "work_cap_missing")
	launched := wiredCapacityHooks(t, homePath)

	_, fresh, err := executeStageCapacityFromSnapshot(homePath, "claude_minimax", now)
	if err != nil {
		t.Fatal(err)
	}
	if fresh {
		t.Fatal("missing snapshot must not read as fresh")
	}
	res, err := store.Continue(now, work.ContinueOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != "capacity_unknown" {
		t.Fatalf("skipped = %q, want capacity_unknown (missing); result=%+v", res.Skipped, res)
	}
	if *launched {
		t.Fatal("missing capacity must not reach the launch")
	}
}
