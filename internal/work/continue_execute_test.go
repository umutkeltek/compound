package work

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests pin the contract added by R3A: a grant-gated, fail-closed
// execute stage on `compound work continue`. The seam lives behind
// `work.continue.unattended` and refuses with a NAMED reason when any gate
// fails; on a green path it drives ONE ready envelope through claim -> running
// -> verifying and stops there. One bad launch fails that envelope only —
// never the heartbeat pass.
//
// Each test overrides the package-level hooks (executeStageRoutePlanFn,
// executeStageCapacityFn, executeStagePolicyEvalFn, launchStub) to keep the
// fixture hermetic without importing sibling packages — keeping the test in
// the `work` package avoids a routeevidence<->work import cycle.

// seedReadyExecuteWork creates an envelope already in `ready` state. The
// execute stage picks this up after the existing lifecycle branches return
// "waiting"/"none".
func seedReadyExecuteWork(t *testing.T, s *Store, id string) {
	t.Helper()
	w, err := s.Create(CreateInput{
		WorkID:           id,
		Owner:            "compound-core",
		Actor:            "codex",
		State:            "ready",
		Risk:             "tier1",
		Privacy:          "private",
		SuccessPredicate: "execute stage drives the ready envelope",
	}, fixedNow)
	if err != nil {
		t.Fatalf("seed ready work: %v", err)
	}
	if w.State != "ready" {
		t.Fatalf("seeded work state = %q, want ready", w.State)
	}
}

func installExecuteGrant(t *testing.T, home string, withGrant bool) {
	t.Helper()
	dir := filepath.Join(home, "registries")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir registries: %v", err)
	}
	if !withGrant {
		// Leave the registry absent (fail-closed default).
		return
	}
	reg := standingGrantRegistryFixture{
		SchemaVersion: standingGrantsRegistrySchemaFixture,
		Grants: []standingGrantFixture{{
			ID:                   "grant_unattended_work_continue_v1",
			Capability:           "work.continue.unattended",
			MutationClass:        "local_reversible",
			ReversibilityCeiling: "reversible",
			BlastRadius:          "local",
			TargetPath:           "/Users/tester/.compound/**",
			ExpiresAt:            "2099-01-01T00:00:00Z",
		}},
	}
	body, err := json.MarshalIndent(&reg, "", " ")
	if err != nil {
		t.Fatalf("marshal grant registry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "standing-grants.json"), append(body, '\n'), 0o644); err != nil {
		t.Fatalf("write grant registry: %v", err)
	}
}

// restoreAllExecuteHooks stores every package-level hook the seam uses and
// restores it at test end. Tests that override hooks must call this to keep
// state isolated between runs.
func restoreAllExecuteHooks(t *testing.T) {
	t.Helper()
	prev := struct {
		rp        executeRoutePlanFn
		cap       executeCapacityFn
		pol       executePolicyEvalFn
		launch    executeLaunchFn
		ceiling   float64
		freshness time.Duration
	}{
		rp:        executeStageRoutePlanFn,
		cap:       executeStageCapacityFn,
		pol:       executeStagePolicyEvalFn,
		launch:    launchStub,
		ceiling:   executeStageCapacityCeilingPercent,
		freshness: executeStageCapacityFreshnessWindow,
	}
	t.Cleanup(func() {
		executeStageRoutePlanFn = prev.rp
		executeStageCapacityFn = prev.cap
		executeStagePolicyEvalFn = prev.pol
		launchStub = prev.launch
		executeStageCapacityCeilingPercent = prev.ceiling
		executeStageCapacityFreshnessWindow = prev.freshness
	})
}

// TestContinueExecutesOneReadyEnvelopeUnderGrant — the green path. Grant is
// installed, risk=private/tier1, route plan names an eligible lane, capacity
// is 30% at 2m old, the policy hook allows, the stubbed launch returns
// success. ContinueResult must carry Action=execute, one ratt id, and the
// envelope must be at least at "verifying" — never closed, never landed.
func TestContinueExecutesOneReadyEnvelopeUnderGrant(t *testing.T) {
	s := newStore(t)
	seedReadyExecuteWork(t, s, "work_exec_ok")
	installExecuteGrant(t, s.home, true)
	restoreAllExecuteHooks(t)
	executeStageRoutePlanFn = func(home, capability string) (lane string, ok bool) {
		return "claude-minimax", true
	}
	executeStageCapacityFn = func(home, lane string, now time.Time) (usedPercent float64, fresh bool, err error) {
		return 30, true, nil
	}
	executeStagePolicyEvalFn = func(ai policyActionIntentShim, cfg policyConfigShim, now time.Time) policyDecisionShim {
		return policyDecisionShim{Outcome: "allow", ID: "pol_test_allow"}
	}
	launchStub = func(_ *Store, workID, _, _, _ string, _ time.Time) (string, error) {
		if workID != "work_exec_ok" {
			return "", &executeStageBadWorkIDErr{got: workID}
		}
		return "ratt_exec_ok", nil
	}

	res, err := s.Continue(fixedNow.Add(time.Minute), ContinueOptions{Actor: "codex"})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if res.Action != "execute" {
		t.Fatalf("action = %q, want execute; result=%+v", res.Action, res)
	}
	if res.Skipped != "" {
		t.Fatalf("skipped = %q, want empty on the green path", res.Skipped)
	}
	if len(res.RunAttemptIDs) != 1 || res.RunAttemptIDs[0] != "ratt_exec_ok" {
		t.Fatalf("run_attempt_ids = %v, want [ratt_exec_ok]", res.RunAttemptIDs)
	}
	if res.WorkID != "work_exec_ok" {
		t.Fatalf("work_id = %q, want work_exec_ok", res.WorkID)
	}
	if !strings.Contains(res.Human, "executed") {
		t.Fatalf("human line %q must say what happened in plain words", res.Human)
	}
	after, err := s.Get("work_exec_ok")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if after.State == "completed" || after.State == "failed" {
		t.Fatalf("execute stage terminalized work (state=%q); must stop at verifying", after.State)
	}
	if after.State != "verifying" && after.State != "running" {
		t.Fatalf("executed work state = %q, want running or verifying", after.State)
	}
}

// TestContinueRefusesExecuteWithoutGrant — the refusal default. With no
// grant in the registry, the seam logs skipped=no_grant and falls through
// to DispatchStep so the existing claim path keeps making heartbeat
// progress. The launch MUST NOT be called.
func TestContinueRefusesExecuteWithoutGrant(t *testing.T) {
	s := newStore(t)
	seedReadyExecuteWork(t, s, "work_exec_no_grant")
	installExecuteGrant(t, s.home, false)
	restoreAllExecuteHooks(t)
	executeStageRoutePlanFn = func(home, capability string) (lane string, ok bool) {
		return "claude-minimax", true
	}
	executeStageCapacityFn = func(home, lane string, now time.Time) (usedPercent float64, fresh bool, err error) {
		return 30, true, nil
	}
	called := false
	launchStub = func(*Store, string, string, string, string, time.Time) (string, error) {
		called = true
		return "", nil
	}

	res, err := s.Continue(fixedNow.Add(time.Minute), ContinueOptions{Actor: "codex"})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	// The seam refused; the existing DispatchStep path claimed the
	// envelope. Skipped=no_grant is observable in the JSON for the
	// heartbeat log.
	if res.Skipped != "no_grant" {
		t.Fatalf("skipped = %q, want no_grant", res.Skipped)
	}
	if called {
		t.Fatal("execute stage must not launch when the grant is missing")
	}
	if !strings.Contains(res.Human, "grant_unattended_work_continue_v1") {
		t.Fatalf("human line must name the missing grant so an operator can act; got %q", res.Human)
	}
}

// TestContinueRefusesExecuteOnStaleCapacity — capacity snapshot 11m old must
// fail closed, never skip the check. Refusal is skipped=capacity_unknown.
func TestContinueRefusesExecuteOnStaleCapacity(t *testing.T) {
	s := newStore(t)
	seedReadyExecuteWork(t, s, "work_exec_stale")
	installExecuteGrant(t, s.home, true)
	restoreAllExecuteHooks(t)
	executeStageRoutePlanFn = func(home, capability string) (lane string, ok bool) {
		return "claude-minimax", true
	}
	executeStageCapacityFn = func(home, lane string, now time.Time) (usedPercent float64, fresh bool, err error) {
		return 30, false, nil // fresh=false signals stale snapshot
	}
	launchStub = func(*Store, string, string, string, string, time.Time) (string, error) {
		return "ratt_should_not_run", nil
	}

	res, err := s.Continue(fixedNow.Add(time.Minute), ContinueOptions{Actor: "codex"})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if res.Skipped != "capacity_unknown" {
		t.Fatalf("skipped = %q, want capacity_unknown; result=%+v", res.Skipped, res)
	}
	if len(res.RunAttemptIDs) != 0 {
		t.Fatalf("no ratt should be minted on stale capacity, got %v", res.RunAttemptIDs)
	}
}

// TestContinueRefusesExecuteAtCapacityCeiling — fresh capacity but
// usedPercent==90 must refuse (ceiling is strict-less-than, not <=).
func TestContinueRefusesExecuteAtCapacityCeiling(t *testing.T) {
	s := newStore(t)
	seedReadyExecuteWork(t, s, "work_exec_ceiling")
	installExecuteGrant(t, s.home, true)
	restoreAllExecuteHooks(t)
	executeStageRoutePlanFn = func(home, capability string) (lane string, ok bool) {
		return "claude-minimax", true
	}
	executeStageCapacityFn = func(home, lane string, now time.Time) (usedPercent float64, fresh bool, err error) {
		return 90, true, nil
	}
	launchStub = func(*Store, string, string, string, string, time.Time) (string, error) {
		return "ratt_should_not_run", nil
	}

	res, err := s.Continue(fixedNow.Add(time.Minute), ContinueOptions{Actor: "codex"})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if res.Skipped != "capacity_unknown" {
		t.Fatalf("skipped = %q, want capacity_unknown (ceiling is strict <); result=%+v", res.Skipped, res)
	}
}

// TestContinueRefusesExecuteOnPolicyHold — when the policy evaluator returns
// a hold/deny for work.continue.unattended, the stage refuses with
// skipped=policy_hold and surfaces the decision id.
func TestContinueRefusesExecuteOnPolicyHold(t *testing.T) {
	s := newStore(t)
	seedReadyExecuteWork(t, s, "work_exec_policy_hold")
	installExecuteGrant(t, s.home, true)
	restoreAllExecuteHooks(t)
	executeStageRoutePlanFn = func(home, capability string) (lane string, ok bool) {
		return "claude-minimax", true
	}
	executeStageCapacityFn = func(home, lane string, now time.Time) (usedPercent float64, fresh bool, err error) {
		return 30, true, nil
	}
	executeStagePolicyEvalFn = func(ai policyActionIntentShim, cfg policyConfigShim, now time.Time) policyDecisionShim {
		return policyDecisionShim{Outcome: "hold", ID: "pol_hold_test"}
	}
	launchStub = func(*Store, string, string, string, string, time.Time) (string, error) {
		return "ratt_should_not_run", nil
	}

	res, err := s.Continue(fixedNow.Add(time.Minute), ContinueOptions{Actor: "codex"})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if res.Skipped != "policy_hold" {
		t.Fatalf("skipped = %q, want policy_hold; result=%+v", res.Skipped, res)
	}
	if res.PolicyDecisionID != "pol_hold_test" {
		t.Fatalf("policy_decision_id = %q, want pol_hold_test", res.PolicyDecisionID)
	}
	if len(res.RunAttemptIDs) != 0 {
		t.Fatalf("no ratt on policy hold, got %v", res.RunAttemptIDs)
	}
}

// TestContinueRefusesExecuteOnRisk — a tier2 (or non-private) envelope must
// be skipped=risk even when every other gate would pass.
func TestContinueRefusesExecuteOnRisk(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{
		WorkID:           "work_exec_tier2",
		Owner:            "compound-core",
		Actor:            "codex",
		State:            "ready",
		Risk:             "tier2",
		Privacy:          "private",
		SuccessPredicate: "tier2 work should be refused by the execute stage",
	}, fixedNow)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if w.State != "ready" {
		t.Fatalf("seed state = %q, want ready", w.State)
	}
	installExecuteGrant(t, s.home, true)
	restoreAllExecuteHooks(t)
	executeStageRoutePlanFn = func(home, capability string) (lane string, ok bool) {
		return "claude-minimax", true
	}
	executeStageCapacityFn = func(home, lane string, now time.Time) (usedPercent float64, fresh bool, err error) {
		return 30, true, nil
	}
	launchStub = func(*Store, string, string, string, string, time.Time) (string, error) {
		return "ratt_should_not_run", nil
	}

	res, err := s.Continue(fixedNow.Add(time.Minute), ContinueOptions{Actor: "codex"})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if res.Skipped != "risk" {
		t.Fatalf("skipped = %q, want risk; result=%+v", res.Skipped, res)
	}
}

// TestContinueRefusesExecuteOnNoRoute — when the route plan names no
// eligible lane for the work.continue.unattended capability, the seam
// refuses skipped=no_route.
func TestContinueRefusesExecuteOnNoRoute(t *testing.T) {
	s := newStore(t)
	seedReadyExecuteWork(t, s, "work_exec_no_route")
	installExecuteGrant(t, s.home, true)
	restoreAllExecuteHooks(t)
	executeStageRoutePlanFn = func(home, capability string) (lane string, ok bool) {
		return "", false
	}
	executeStageCapacityFn = func(home, lane string, now time.Time) (usedPercent float64, fresh bool, err error) {
		return 30, true, nil
	}
	launchStub = func(*Store, string, string, string, string, time.Time) (string, error) {
		return "ratt_should_not_run", nil
	}

	res, err := s.Continue(fixedNow.Add(time.Minute), ContinueOptions{Actor: "codex"})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if res.Skipped != "no_route" {
		t.Fatalf("skipped = %q, want no_route; result=%+v", res.Skipped, res)
	}
}

// TestContinueExecuteFailureFailsOnlyThatEnvelope — when the launch returns
// an error, the envelope must be in a failed state and ContinueResult must
// still report Action=execute with a human-readable reason. The next call to
// Continue must pick the next envelope, not the failed one.
func TestContinueExecuteFailureFailsOnlyThatEnvelope(t *testing.T) {
	s := newStore(t)
	failedID := "work_exec_fail"
	nextID := "work_exec_next"
	seedReadyExecuteWork(t, s, failedID)
	seedReadyExecuteWork(t, s, nextID)
	installExecuteGrant(t, s.home, true)
	restoreAllExecuteHooks(t)
	executeStageRoutePlanFn = func(home, capability string) (lane string, ok bool) {
		return "claude-minimax", true
	}
	executeStageCapacityFn = func(home, lane string, now time.Time) (usedPercent float64, fresh bool, err error) {
		return 30, true, nil
	}
	executeStagePolicyEvalFn = func(ai policyActionIntentShim, cfg policyConfigShim, now time.Time) policyDecisionShim {
		return policyDecisionShim{Outcome: "allow", ID: "pol_test_allow"}
	}
	launched := 0
	launchStub = func(store *Store, workID, _, _, _ string, _ time.Time) (string, error) {
		launched++
		if workID == failedID {
			return "", &executeStageLaunchErr{msg: "synthetic harness exit 1"}
		}
		return "ratt_next", nil
	}

	res, err := s.Continue(fixedNow.Add(time.Minute), ContinueOptions{Actor: "codex"})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if res.Action != "execute" {
		t.Fatalf("action = %q, want execute (one bad launch must not flip to none); result=%+v", res.Action, res)
	}
	if res.WorkID != failedID {
		t.Fatalf("work_id = %q, want %s", res.WorkID, failedID)
	}
	if res.Skipped == "" || res.Skipped == "no_grant" {
		t.Fatalf("skipped = %q, want a non-empty refusal reason naming the launch failure", res.Skipped)
	}
	if !strings.Contains(strings.ToLower(res.Human), "fail") && !strings.Contains(res.Human, failedID) {
		t.Fatalf("human line %q must surface the failure", res.Human)
	}
	failedAfter, err := s.Get(failedID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if failedAfter.State != "failed" {
		t.Fatalf("failed envelope state = %q, want failed", failedAfter.State)
	}
	if launched != 1 {
		t.Fatalf("launch count = %d, want 1 (one launch per Continue)", launched)
	}

	// Next Continue must skip the failed envelope and pick the next one.
	res2, err := s.Continue(fixedNow.Add(2*time.Minute), ContinueOptions{Actor: "codex"})
	if err != nil {
		t.Fatalf("continue2: %v", err)
	}
	if res2.Action != "execute" {
		t.Fatalf("continue2 action = %q, want execute", res2.Action)
	}
	if res2.WorkID != nextID {
		t.Fatalf("continue2 work_id = %q, want %s", res2.WorkID, nextID)
	}
	if launched != 2 {
		t.Fatalf("launch count = %d after second Continue, want 2", launched)
	}
}

// TestContinueExecuteNeverLandsOrCloses — the execute stage stops at
// verifying. After a successful launch the envelope is at most "verifying";
// Close is never called by the stage.
func TestContinueExecuteNeverLandsOrCloses(t *testing.T) {
	s := newStore(t)
	seedReadyExecuteWork(t, s, "work_exec_no_close")
	installExecuteGrant(t, s.home, true)
	restoreAllExecuteHooks(t)
	executeStageRoutePlanFn = func(home, capability string) (lane string, ok bool) {
		return "claude-minimax", true
	}
	executeStageCapacityFn = func(home, lane string, now time.Time) (usedPercent float64, fresh bool, err error) {
		return 30, true, nil
	}
	executeStagePolicyEvalFn = func(ai policyActionIntentShim, cfg policyConfigShim, now time.Time) policyDecisionShim {
		return policyDecisionShim{Outcome: "allow", ID: "pol_test_allow"}
	}
	launchStub = func(*Store, string, string, string, string, time.Time) (string, error) {
		return "ratt_no_close", nil
	}

	res, err := s.Continue(fixedNow.Add(time.Minute), ContinueOptions{Actor: "codex"})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if res.Action != "execute" {
		t.Fatalf("action = %q, want execute", res.Action)
	}
	after, err := s.Get("work_exec_no_close")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	switch after.State {
	case "verifying", "running":
		// good — stage stopped here
	case "ready":
		t.Fatalf("execute stage left envelope in ready; it must move at least to running")
	default:
		t.Fatalf("execute stage terminalized work (state=%q); must stop at running/verifying", after.State)
	}
	if after.State == "completed" || after.State == "failed" || after.State == "cancelled" {
		t.Fatalf("execute stage terminalized work (state=%q); close must remain human/orchestrator", after.State)
	}
}

// Local fixture types mirroring the policy package — kept here so the test
// package does not need to import internal/policy (which would force a second
// dependency and risks an import cycle).

type standingGrantRegistryFixture struct {
	SchemaVersion string                 `json:"schema_version"`
	Grants        []standingGrantFixture `json:"grants"`
}

type standingGrantFixture struct {
	ID                   string `json:"id"`
	Capability           string `json:"capability"`
	MutationClass        string `json:"mutation_class"`
	ReversibilityCeiling string `json:"reversibility_ceiling"`
	BlastRadius          string `json:"blast_radius"`
	TargetPath           string `json:"target_path"`
	ExpiresAt            string `json:"expires_at"`
}

const standingGrantsRegistrySchemaFixture = "compound.standing_grants.v0.1"

// shim types for the policy-eval hook live in continue.go (the seam-level
// types) — tests reuse those declarations and only override the package-
// level hook function.

// Local error sentinels used by launchStub.

type executeStageBadWorkIDErr struct{ got string }

func (e *executeStageBadWorkIDErr) Error() string {
	return "execute stage stub got unexpected work id: " + e.got
}

type executeStageLaunchErr struct{ msg string }

func (e *executeStageLaunchErr) Error() string { return e.msg }

// installProposedUnattendedGrant writes the OPERATOR-PROPOSED grant shape
// (docs_private/operator-grants/PROPOSED-standing-grants-2026-09-04-unattended-execute.json)
// into a temp home's standing-grants registry, with target_path scoped to the
// temp home the way the operator file scopes it to their real home. Extra
// proposal fields (predicate, lanes, capacity ceiling) stay verbatim: the
// registry must import the proposed shape as-is.
func installProposedUnattendedGrant(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, "registries")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir registries: %v", err)
	}
	body := `{"schema_version":"compound.standing_grants.v0.1","grants":[{` +
		`"id":"grant_unattended_work_continue_v1",` +
		`"capability":"work.continue.unattended",` +
		`"mutation_class":"local_reversible",` +
		`"reversibility_ceiling":"reversible",` +
		`"blast_radius":"local",` +
		`"target_path":"` + home + `/**",` +
		`"predicate":"risk in {tier0, tier1} && privacy == 'private' && route_plan.gate_role == 'eligible' && capacity_snapshot.freshness_minutes < 10 && capacity_snapshot.used_percent < 90 && policy_decision.outcome in {allow, allow_with_obligations}",` +
		`"max_per_tick":1,` +
		`"lanes":["claude-minimax","claude-zai"],` +
		`"capacity_ceiling_percent":90,` +
		`"operator_review":false,` +
		`"expires_at":"2099-01-01T00:00:00Z"}]}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "standing-grants.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write grant registry: %v", err)
	}
}

// TestExecuteStagePolicyEvalHoldsWhenNoGrantCoversIntent — the wired policy
// default must fail closed when no standing grant covers the intent. The
// local_reversible class baseline alone would allow_with_obligations; an
// allow that is not backed by grant_unattended_work_continue_v1 is a hold.
func TestExecuteStagePolicyEvalHoldsWhenNoGrantCoversIntent(t *testing.T) {
	s := newStore(t)
	restoreAllExecuteHooks(t)

	got := executeStagePolicyEvalFn(
		policyActionIntentShim{ID: "ai_pol_test", Capability: executeUnattendedCapability, WorkID: "work_pol_test", Risk: "tier1", Privacy: "private"},
		policyConfigShim{HasGrant: true, Home: s.home},
		fixedNow,
	)
	if got.Outcome != "hold" {
		t.Fatalf("outcome = %q, want hold (no grant covers the intent); decision=%+v", got.Outcome, got)
	}
}

// TestExecuteStagePolicyEvalAllowsUnderProposedGrantShape — with the proposed
// operator grant imported into the home, the same evaluation returns
// allow_with_obligations, so the seam's policy gate opens only through the
// operator's signed grant.
func TestExecuteStagePolicyEvalAllowsUnderProposedGrantShape(t *testing.T) {
	s := newStore(t)
	installProposedUnattendedGrant(t, s.home)
	restoreAllExecuteHooks(t)

	got := executeStagePolicyEvalFn(
		policyActionIntentShim{ID: "ai_pol_test", Capability: executeUnattendedCapability, WorkID: "work_pol_test", Risk: "tier1", Privacy: "private"},
		policyConfigShim{HasGrant: true, Home: s.home},
		fixedNow,
	)
	if got.Outcome != "allow_with_obligations" {
		t.Fatalf("outcome = %q, want allow_with_obligations under the proposed grant; decision=%+v", got.Outcome, got)
	}
	if got.ID == "" {
		t.Fatal("decision id must be surfaced so refusals are auditable")
	}
}
