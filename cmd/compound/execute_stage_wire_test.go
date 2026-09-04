package main

// R3A slice 2 — production wiring tests for the unattended execute stage's
// hooks. The hooks default fail-closed inside internal/work; cmd/compound is
// the composition root that binds them to the real routeevidence / policy /
// actuator resolvers. Each test here pins one wired resolver by driving the
// seam end to end (store.Continue) against a temp home, so a regression in
// the wiring shows up as the wrong `skipped=` refusal reason.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/routeevidence"
	"compound/internal/runattempt"
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

// argvRecordingAdapter is a stub harness.Adapter that records the RunInput it
// was called with so a test can assert the production wire built argv that
// would translate to `claude --print --dangerously-skip-permissions <prompt>`.
type argvRecordingAdapter struct {
	name   string
	lastIn harness.RunInput
	calls  int
}

func (a *argvRecordingAdapter) Name() string { return a.name }

func (a *argvRecordingAdapter) Run(_ context.Context, in harness.RunInput) (harness.RunOutput, error) {
	a.calls++
	a.lastIn = in
	now := time.Now().UTC()
	return harness.RunOutput{
		RunAttempt: runattempt.RunAttempt{
			ID:          "ratt_argv_recording",
			Harness:     a.name,
			WorkID:      in.WorkID,
			Workspace:   in.Workspace,
			Command:     in.Command,
			Status:      runattempt.StatusSucceeded,
			CreatedAt:   now,
			CompletedAt: now,
		},
	}, nil
}

// installRecordingLaunchAdapter swaps executeStageAdapterFor to return a
// argvRecordingAdapter and the readiness guard to a one-line allow. The test
// owns both via closures so the production wire's argv-assembly path can be
// driven end-to-end without spawning a real subprocess.
func installRecordingLaunchAdapter(t *testing.T, harnessName string) *argvRecordingAdapter {
	t.Helper()
	rec := &argvRecordingAdapter{name: harnessName}
	prevAdapter := executeStageAdapterFor
	executeStageAdapterFor = func(home, name string) (harness.Adapter, bool) {
		if name == harnessName {
			return rec, true
		}
		return nil, false
	}
	t.Cleanup(func() { executeStageAdapterFor = prevAdapter })
	prevReadiness := executeStageLaunchReadiness
	executeStageLaunchReadiness = func(req work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
		return work.DelegateHarnessReadinessResult{Ready: true}, nil
	}
	t.Cleanup(func() { executeStageLaunchReadiness = prevReadiness })
	return rec
}

// seedExecuteStageReadyWorkWithDelivery seeds a ready envelope that already
// carries git_delivery. The launch wire uses git_delivery.target_worktree_root
// as the harness RunInput.Workspace, so a launch test must seed it. The
// fixture initializes a real git repo at the test home root because
// `validateBoundGitDelivery` rejects paths that are not inside a worktree.
func seedExecuteStageReadyWorkWithDelivery(t *testing.T, store *work.Store, id, targetRoot string) {
	t.Helper()
	sha := initGitRepoOrSkip(t, targetRoot)
	if _, err := store.Create(work.CreateInput{
		WorkID:           id,
		Owner:            "compound-core",
		Actor:            "codex",
		State:            "ready",
		Risk:             "tier1",
		Privacy:          "private",
		SuccessPredicate: "execute-stage launch wiring fixture predicate",
		GitDelivery: &envelope.GitDeliveryContract{
			TargetWorktreeRoot: targetRoot,
			TargetLocalRef:     "refs/heads/main",
			ExpectedBaseSHA:    sha,
			PublicationMode:    "local_only",
		},
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

// initGitRepoOrSkip turns targetRoot into a minimal git repo so
// validateBoundGitDelivery accepts it as a workspace. Returns the empty-commit
// SHA used as the work envelope's expected_base_sha. Idempotent: a pre-existing
// .git directory is left alone. Skips (rather than fails) when git is missing
// from the PATH — the launch wire is gated on the env having the binary in
// real production, and a missing-git test environment should not turn into a
// red test on unrelated infrastructure.
func initGitRepoOrSkip(t *testing.T, targetRoot string) string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(targetRoot, ".git")); err == nil {
		// Already a repo; read the head SHA so the caller can pin it as
		// expected_base_sha.
		return gitHeadSHA(t, targetRoot)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH; cannot init a workspace repo: %v", err)
	}
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "test"},
		{"commit", "--allow-empty", "--quiet", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = targetRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, string(out))
		}
	}
	return gitHeadSHA(t, targetRoot)
}

func gitHeadSHA(t *testing.T, targetRoot string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = targetRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestContinueLaunchHookBuildsBypassArgv: with the production launch wire
// installed and a recording adapter stubbed in, the seam drives an envelope
// from ready through the gate chain. The recording adapter captures RunInput;
// PermissionMode must be "bypass" (which the Claude adapter translates to
// `--dangerously-skip-permissions` on argv), the workspace must come from
// the envelope's git_delivery, and the prompt must be carried through.
func TestContinueLaunchHookBuildsBypassArgv(t *testing.T) {
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
	seedExecuteStageReadyWorkWithDelivery(t, store, "work_launch_argv", homePath)
	rec := installRecordingLaunchAdapter(t, "claude_minimax")
	// restoreExecuteStageHooks stubs capacity to (0, false); swap in the real
	// capacity resolver so the gate chain reaches the launch (this test
	// proves argv-assembly on the green path).
	restoreCapacity := work.SwapExecuteStageCapacity(executeStageCapacityFromSnapshot)
	t.Cleanup(restoreCapacity)
	restoreExecuteStageHooksExcept(t, "capacity")

	res, err := store.Continue(now, work.ContinueOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "execute" {
		t.Fatalf("action = %q, want execute (full gate chain green); result=%+v", res.Action, res)
	}
	if rec.calls != 1 {
		t.Fatalf("recording adapter calls = %d, want 1", rec.calls)
	}
	if rec.lastIn.PermissionMode != "bypass" {
		t.Fatalf("permission_mode = %q, want bypass (the unattended envelope's mode); argv would not include --dangerously-skip-permissions", rec.lastIn.PermissionMode)
	}
	if rec.lastIn.Workspace != homePath {
		// macOS TempDir resolves /var/folders → /private/var/folders; compare
		// the canonical paths so the test passes on every platform.
		canonicalHome, _ := filepath.EvalSymlinks(homePath)
		canonicalGot, _ := filepath.EvalSymlinks(rec.lastIn.Workspace)
		if canonicalGot != canonicalHome {
			t.Fatalf("workspace = %q (canonical %q), want %q (canonical %q) (from git_delivery)", rec.lastIn.Workspace, canonicalGot, homePath, canonicalHome)
		}
	}
	if rec.lastIn.Harness != "claude_minimax" {
		t.Fatalf("harness = %q, want claude_minimax (the route plan's lane)", rec.lastIn.Harness)
	}
	if len(rec.lastIn.Args) == 0 || strings.TrimSpace(rec.lastIn.Args[0]) == "" {
		t.Fatalf("launch argv missing the prompt; got args=%v", rec.lastIn.Args)
	}
	// The Claude adapter translates PermissionMode="bypass" to the argv
	// fragment `--dangerously-skip-permissions`. The recording adapter does
	// not run the translation, but the test pin is the seam's
	// PermissionMode signal, which the adapter consumes as documented in
	// internal/harness/claude_adapter.go:211-217
	// (`claudePermissionArgs("bypass") = []string{"--dangerously-skip-permissions"}`).
}

// TestContinueLaunchHookRefusesLaneUnready: when the readiness guard reports
// not-ready, the wire refuses with lane_unready:<detail>. The envelope must
// not be advanced past running and no RunAttempt may be minted.
func TestContinueLaunchHookRefusesLaneUnready(t *testing.T) {
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
	seedExecuteStageReadyWorkWithDelivery(t, store, "work_launch_unready", homePath)
	rec := installRecordingLaunchAdapter(t, "claude_minimax")
	// Force readiness to refuse AFTER installRecordingLaunchAdapter set it to
	// ready. Capacity is stubbed stale by restoreExecuteStageHooks so this
	// test can run without seeding a fresh row, but in this case we want the
	// readiness guard to fire FIRST — capacity is the seam's gate (d)
	// AFTER the route gate (c); readiness is a launch-side check inside the
	// wire itself, so we still need to satisfy the seam's capacity gate.
	restoreCapacity := work.SwapExecuteStageCapacity(executeStageCapacityFromSnapshot)
	t.Cleanup(restoreCapacity)
	prevReadiness := executeStageLaunchReadiness
	executeStageLaunchReadiness = func(req work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
		return work.DelegateHarnessReadinessResult{Ready: false, Details: []string{"claude tuple not installed", "permission posture unproved"}}, nil
	}
	t.Cleanup(func() { executeStageLaunchReadiness = prevReadiness })
	restoreExecuteStageHooksExcept(t, "capacity")

	res, err := store.Continue(now, work.ContinueOptions{Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != "lane_unready" {
		t.Fatalf("skipped = %q, want lane_unready (readiness guard refused); result=%+v", res.Skipped, res)
	}
	if !strings.HasPrefix(res.Human, "did nothing: ") || !strings.Contains(res.Human, "claude tuple not installed") {
		t.Fatalf("human line %q must surface the readiness refusal detail", res.Human)
	}
	if rec.calls != 0 {
		t.Fatalf("readiness refusal must not reach the adapter; got %d calls", rec.calls)
	}
	after, err := store.Get("work_launch_unready")
	if err != nil {
		t.Fatal(err)
	}
	if after.State == "completed" || after.State == "failed" {
		t.Fatalf("readiness refusal must not terminalize the envelope; state = %q", after.State)
	}
}

// restoreExecuteStageHooksExcept is a sibling of restoreExecuteStageHooks that
// leaves the named hook untouched (the caller has already swapped in its own
// resolver). It is how the launch-wire tests keep the production route +
// policy + launch resolvers but pin capacity to whatever the caller wants.
func restoreExecuteStageHooksExcept(t *testing.T, skip string) {
	t.Helper()
	restoreRoute := work.SwapExecuteStageRoutePlan(executeStageRouteFromManifest)
	restorePolicy := work.SwapExecuteStagePolicyEval(func(ai work.PolicyActionIntentShim, _ work.PolicyConfigShim, _ time.Time) work.PolicyDecisionShim {
		return work.PolicyDecisionShim{Outcome: "allow", ID: "pol_wire_test_allow"}
	})
	t.Cleanup(func() {
		restoreRoute()
		restorePolicy()
	})
	_ = skip
}
