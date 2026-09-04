// Package executewire is the composition root for the R3A unattended execute
// stage. internal/work owns the seam and its fail-closed defaults; this
// package binds the package-level hooks to the real resolvers at startup, the
// same pattern internal/work's delegate.go uses for the readiness guard.
// Nesting under internal/work avoids an internal/work -> internal/routeevidence
// import cycle (routeevidence already imports work).
//
// cmd/compound calls Install() from a startup init(); the production wiring
// uses the package-level vars below so tests can swap any single resolver
// without rebuilding the registry.
package executewire

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"compound/internal/actuator"
	"compound/internal/harness"
	"compound/internal/routeevidence"
	autonomyproof "compound/internal/routeevidence/autonomy"
	"compound/internal/runattempt"
	"compound/internal/work"
)

// Install binds the production route / capacity / preflight / launch hooks
// on the internal/work seam. Call exactly once from cmd/compound's startup
// init(). DEFECT 1 fix (AUT-P0-001 2026-09-04): the preflight hook MUST be
// installed before any production caller invokes the seam so a recognized
// refusal (adapter_missing / workspace_unresolvable / lane_unready) leaves
// the envelope byte-identical to before, instead of stranding it in
// `running` with a lease.
func Install() {
	work.SwapExecuteStageRoutePlan(RouteFromManifest)
	work.SwapExecuteStageCapacity(CapacityFromSnapshot)
	work.SwapExecuteStagePreflight(PreflightViaActuator)
	work.SwapExecuteStageLaunch(LaunchViaActuator)
}

// RouteFromManifest resolves the first eligible lane for the capability from
// the home's model-capabilities authority (the route plan the recon named:
// routeevidence.LoadModelCapabilitiesSnapshot). Fail-closed on every input: a
// missing or invalid authority, no routing_defaults entry for the capability,
// a claim whose computed gate role is not "eligible", an entry with no
// harness, or a harness the adapter registry has no executor for all return
// no route.
func RouteFromManifest(homePath, capability string) (string, bool) {
	manifest, _, _, err := routeevidence.LoadModelCapabilitiesSnapshot(homePath)
	if err != nil {
		return "", false
	}
	for _, modelID := range manifest.RoutingDefaults[capability] {
		entry, ok := manifest.Models[modelID]
		if !ok {
			continue
		}
		claim := entry.Capabilities[capability]
		if autonomyproof.GateRole(capability, entry.Tier, strings.TrimSpace(claim.Level)) != "eligible" {
			continue
		}
		harnessName := strings.TrimSpace(entry.Harness)
		if harnessName == "" || !AdapterKnown(homePath, harnessName) {
			continue
		}
		return harnessName, true
	}
	return "", false
}

// AdapterKnown reports whether the harness adapter registry has an executor
// for harnessName. A var so tests can pin the registry lookup without
// building a real one.
var AdapterKnown = func(homePath, harnessName string) bool {
	registry := harness.NewAdapterRegistry(runattempt.Open(homePath))
	_, ok := registry.AdapterByName(harnessName)
	return ok
}

// CapacityConfig builds the reader config for the capacity hook. Production
// uses the operator-owned defaults (CodexBar's usage feed plus the home's own
// projections); tests override the var to point the same reader at fixture
// paths so the operator's real feed can never leak into a test.
var CapacityConfig = routeevidence.DefaultLaneCapacityConfig

// CapacityFromSnapshot reads the lane's capacity through the real routeevidence
// reader (the codexbar-fed snapshot, merged with Compound's own supplement
// feed) and translates it into the seam's (usedPercent, fresh) contract.
// Fail-closed: a read error propagates, and an unknown state or a missing/zero
// observation reports fresh=false — the seam refuses on that signal
// regardless of the percentage, never skipping the check.
func CapacityFromSnapshot(homePath, lane string, now time.Time) (float64, bool, error) {
	capacity, err := routeevidence.ReadLaneCapacityWithConfig(lane, now, CapacityConfig(homePath))
	if err != nil {
		return 0, false, err
	}
	used := 100 - capacity.Remaining
	if used < 0 {
		used = 0
	}
	fresh := capacity.State != routeevidence.CapacityUnknown &&
		work.ExecuteStageCapacityFreshness(capacity.LastProven, now)
	return used, fresh, nil
}

// AdapterFor returns the registered adapter for harnessName, same registry
// `work_advance_execute.go` uses; tests pin via this var.
var AdapterFor = func(homePath, harnessName string) (harness.Adapter, bool) {
	registry := harness.NewAdapterRegistry(runattempt.Open(homePath))
	adapter, ok := registry.AdapterByName(harnessName)
	return adapter, ok
}

// LaunchReadiness is the readiness guard the launch wire calls before
// handing the envelope to the adapter. Tests override the var to inject a
// controlled verdict. Packet deliverable 4: the launch MUST refuse (not
// silently plan) when the lane's readiness is unmet; an unconfigured guard
// is the most unmet a guard can be.
var LaunchReadiness = func(req work.DelegateHarnessReadinessRequest) (work.DelegateHarnessReadinessResult, error) {
	if work.DelegateHarnessReadiness == nil {
		return work.DelegateHarnessReadinessResult{Ready: false, Details: []string{"delegate readiness guard not installed"}}, nil
	}
	return work.DelegateHarnessReadiness(req)
}

// PreflightViaActuator is the production preflight hook the seam calls
// BEFORE Claim/Advance (DEFECT 1 fix, AUT-P0-001 2026-09-04). It resolves
// every prerequisite that can fail before governed work enters running:
// adapter enrollment, workspace resolvability, and lane readiness. The
// returned error MUST carry a recognized skip prefix (adapter_missing:,
// workspace_unresolvable:, lane_unready:) so the seam can surface the
// refusal as a preflight refusal without any state mutation. The launch
// wire (LaunchViaActuator) re-resolves the same values when it runs; the
// preflight is read-only and runs against the envelope's pre-claim state.
//
// Wire contract: this function never mints a RunAttempt, never mutates the
// envelope, never grants a lease, and never advances any state. A bare
// error (infra failure inside the preflight itself) is also reported
// without mutation — the seam fails closed.
func PreflightViaActuator(store *work.Store, workID, lane string, now time.Time) error {
	if store == nil {
		return errors.New("execute stage preflight: nil work store")
	}
	workID = strings.TrimSpace(workID)
	lane = strings.TrimSpace(lane)
	if workID == "" || lane == "" {
		return fmt.Errorf("execute stage preflight: workID=%q lane=%q; both required", workID, lane)
	}
	homePath := store.HomePath()

	// 1. Resolve adapter enrollment. A missing adapter means the route
	// plan names a lane the harness registry cannot drive — refuse with
	// adapter_missing:<lane>.
	_, ok := AdapterFor(homePath, lane)
	if !ok {
		return errors.New("adapter_missing:" + lane)
	}

	// 2. Resolve workspace from the envelope's git_delivery. The
	// production launch wire reads the same field; doing the check here
	// lets a missing-git_delivery envelope surface as a refusal BEFORE
	// the seam advances the envelope to running.
	wenv, err := store.Get(workID)
	if err != nil {
		return fmt.Errorf("workspace_unresolvable:load envelope %s: %w", workID, err)
	}
	workspace := ""
	if wenv.GitDelivery != nil {
		workspace = strings.TrimSpace(wenv.GitDelivery.TargetWorktreeRoot)
	}
	if workspace == "" {
		return errors.New("workspace_unresolvable:envelope " + workID + " has no git_delivery.target_worktree_root")
	}

	// 3. Run the lane readiness check. The launch wire runs the same
	// guard, but doing it here lets the seam refuse without ever
	// claiming the envelope.
	readinessReq := work.DelegateHarnessReadinessRequest{
		HomePath: homePath, HarnessHome: homePath, WorkID: workID,
		Workspace: workspace, Harness: lane, LaneID: lane,
		Args: []string{}, RequestedModelID: lane, RequestedProviderRoute: lane, Now: now,
	}
	readiness, readinessErr := LaunchReadiness(readinessReq)
	if readinessErr != nil {
		return fmt.Errorf("execute stage preflight: readiness check errored: %w", readinessErr)
	}
	if !readiness.Ready {
		detail := strings.Join(readiness.Details, "; ")
		if detail == "" {
			detail = "delegate readiness reported not-ready without details"
		}
		return errors.New("lane_unready:" + detail)
	}
	return nil
}

// LaunchViaActuator is the production launch hook. It runs the SAME machinery
// `compound work advance --execute` uses (harness.Run via the registered
// adapter, then FinalizeGovernedHarnessRun to advance the envelope from
// running to verifying), passing --permission-mode bypass so the unattended
// envelope drives code-editing lanes rather than the safe plan default. A
// lane whose readiness guard reports not-ready is refused with
// skipped=lane_unready:<detail> rather than silently planned. The seam has
// already moved the envelope from ready → claimed → running; this function
// takes the running envelope, runs the adapter, and returns the RunAttempt
// id the adapter minted.
func LaunchViaActuator(store *work.Store, workID, lane, harnessName, prompt string, now time.Time) (string, error) {
	if store == nil {
		return "", errors.New("execute stage launch: nil work store")
	}
	workID = strings.TrimSpace(workID)
	harnessName = strings.TrimSpace(harnessName)
	if workID == "" || harnessName == "" {
		return "", fmt.Errorf("execute stage launch: workID=%q harness=%q; both required", workID, harnessName)
	}
	homePath := store.HomePath()
	adapter, ok := AdapterFor(homePath, harnessName)
	if !ok {
		return "", errors.New("adapter_missing:" + harnessName)
	}
	wenv, err := store.Get(workID)
	if err != nil {
		return "", fmt.Errorf("execute stage launch: load envelope %s: %w", workID, err)
	}
	if wenv.State != "running" {
		return "", fmt.Errorf("execute stage launch: envelope %s is %s, expected running", workID, wenv.State)
	}
	workspace := ""
	if wenv.GitDelivery != nil {
		workspace = strings.TrimSpace(wenv.GitDelivery.TargetWorktreeRoot)
	}
	if workspace == "" {
		return "", fmt.Errorf("workspace_unresolvable:envelope %s has no git_delivery.target_worktree_root", workID)
	}
	// M5 2026-09-03: --permission-mode bypass is the explicit operator
	// override the standing grant authorizes; the seam never synthesizes
	// bypass on its own.
	runInput := harness.RunInput{
		WorkID: workID, Harness: harnessName, Workspace: workspace,
		Args: []string{prompt}, PermissionMode: "bypass",
		LaneID: lane, Now: now,
	}
	readinessReq := work.DelegateHarnessReadinessRequest{
		HomePath: homePath, HarnessHome: homePath, WorkID: workID,
		Workspace: workspace, Harness: harnessName, LaneID: lane,
		Args: []string{prompt}, RequestedModelID: lane,
		RequestedProviderRoute: lane, Now: now,
	}
	readiness, readinessErr := LaunchReadiness(readinessReq)
	if readinessErr != nil {
		return "", fmt.Errorf("execute stage launch: readiness check errored: %w", readinessErr)
	}
	if !readiness.Ready {
		detail := strings.Join(readiness.Details, "; ")
		if detail == "" {
			detail = "delegate readiness reported not-ready without details"
		}
		return "", fmt.Errorf("lane_unready:%s", detail)
	}
	out, runErr := adapter.Run(context.Background(), runInput)
	if runErr != nil {
		return "", runErr
	}
	if strings.TrimSpace(out.RunAttempt.ID) == "" {
		return "", errors.New("execute stage launch: adapter returned empty RunAttempt.ID")
	}
	// FinalizeGovernedHarnessRun advances running → verifying (or Close
	// failed on a non-success RunAttempt.Status). The seam's own
	// Advance(verifying) after launchStub returns is now a no-op on the
	// production path; tests that stub launchStub still move the
	// envelope the way the seam's pre-Advance(running) set up.
	policyID := strings.TrimSpace(wenv.PolicyDecisionID)
	actionIntentID := ""
	if n := len(wenv.ActionIntentIDs); n > 0 {
		actionIntentID = strings.TrimSpace(wenv.ActionIntentIDs[n-1])
	}
	if _, finalizeErr := actuator.FinalizeGovernedHarnessRun(actuator.GovernedHarnessFinalizationInput{
		Store: store, WorkID: workID, Actor: lane, HarnessName: harnessName,
		RunningPolicyID: policyID, RunningActionIntentID: actionIntentID,
		Output: out, RunError: nil, Now: now,
	}); finalizeErr != nil {
		return "", fmt.Errorf("execute stage launch: finalize refused: %w", finalizeErr)
	}
	return out.RunAttempt.ID, nil
}
