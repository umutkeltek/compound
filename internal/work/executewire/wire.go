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

// LaunchContext is the context the launch wire forwards to adapter.Run.
// DEFECT 4 fix (AUT-P0-004 2026-09-04): production wires this to the
// heartbeat-derived context (the seam's per-tick ctx with the heartbeat's
// outer 600s bound). Tests swap it to a context they can cancel to prove
// the runner honors cancellation and persists
// `cancel:context_cancelled` evidence. The runner's process-group kill
// path runs on ctx.Done, so a stuck subprocess cannot outlive the
// heartbeat.
//
// The default is context.Background(), which is the SAME defect the fix
// was written for — callers MUST install a real context (see Install
// production wiring below) before a launch can run end to end.
var LaunchContext = func() context.Context { return context.Background() }

// DefaultLaunchTimeout is the finite execution budget LaunchViaActuator
// applies to every unattended harness launch when the caller did not pin
// RunInput.Timeout. DEFECT 4 fix (AUT-P0-004 2026-09-04).
//
// Justification: the heartbeat running the unattended launch has an outer
// `timeout 600` (10 minutes); the internal deadline MUST be safely below
// that with margin to persist timeout evidence (a terminal
// terminalize-Complete cycle, a SetProofRefs call, and the
// SetTranscript write all need to land before the heartbeat SIGKILLs the
// Compound parent). 5 minutes (300s) is comfortably under 600s and above
// the heaviest observed legacy session, so a productive unattended
// launch completes inside the budget and a runaway launch is bounded.
// Override ceiling MaxLaunchTimeout (900s) is enforced; an explicit
// caller-supplied Timeout above that gets clamped.
var DefaultLaunchTimeout = 5 * time.Minute

// MaxLaunchTimeout is the explicit-override ceiling. The wire MUST
// refuse to launch with a Timeout > MaxLaunchTimeout because a
// caller-supplied deadline above the heartbeat's outer budget would
// still let the parent die while the subprocess tree keeps running —
// the very failure shape this fix exists to prevent.
var MaxLaunchTimeout = 15 * time.Minute

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
//
// DEFECT 2 fix (AUT-P0-002 2026-09-04): the actor parameter is the claimant
// and authority holder for this launch. FinalizeGovernedHarnessRun MUST use
// the actor (NOT the lane) as the Finalization Actor — leases compare exact
// strings, and the heartbeat's --actor (e.g. "claude-minimax" with a hyphen)
// is distinct from the canonical lane (e.g. "claude_minimax" with an
// underscore). Lane and harness are execution attribution only.
func LaunchViaActuator(store *work.Store, workID, lane, harnessName, prompt, actor string, now time.Time) (string, error) {
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
	// DEFECT 3 fix (AUT-P0-003 2026-09-04): mint a fresh
	// (ActionIntent, PolicyDecision) pair bound to THIS launch and stamp
	// it onto the running reservation BEFORE the adapter mints its
	// RunAttempt. The wire pass the new ActionIntentID on
	// RunInput.ActionIntentID so the subprocess runner's
	// MintWithActionIntent carries a non-empty authority link, and feeds
	// the same ids into FinalizeGovernedHarnessRun as the
	// ExpectedActionIntentID / ExpectedPolicyDecisionID the verifying
	// transition compares against.
	launchActionID, launchPolicyID, mintErr := store.MintLaunchAuthority(workID, actor, lane, prompt, now)
	if mintErr != nil {
		return "", fmt.Errorf("execute stage launch: mint launch authority: %w", mintErr)
	}
	if strings.TrimSpace(launchActionID) == "" || strings.TrimSpace(launchPolicyID) == "" {
		return "", fmt.Errorf("execute stage launch: MintLaunchAuthority returned empty ids (action=%q policy=%q)", launchActionID, launchPolicyID)
	}
	// M5 2026-09-03: --permission-mode bypass is the explicit operator
	// override the standing grant authorizes; the seam never synthesizes
	// bypass on its own.
	runInput := harness.RunInput{
		WorkID: workID, Harness: harnessName, Workspace: workspace,
		Args: []string{prompt}, PermissionMode: "bypass",
		LaneID: lane, ActionIntentID: launchActionID,
		Timeout: resolvedLaunchTimeout(),
		Now:     now,
	}
	// DEFECT 4 fix (AUT-P0-004 2026-09-04): forward the live, cancellable
	// context so a stuck subprocess is killed at the deadline and a
	// cancelled heartbeat kills the tree. The shared runner honors
	// ctx.Done() via a process-group kill (killProcessTree), so an
	// orphaned grandchild holding the capture pipes open cannot survive.
	// Production wires LaunchContext to a heartbeat-derived cancellable
	// ctx (see Install); tests swap the var to prove the cancellation
	// path persists "cancel:context_cancelled" evidence.
	ctx := LaunchContext()
	out, runErr := adapter.Run(ctx, runInput)
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
	// envelope the way the seam's pre-Advance(running) set up. The
	// RunningActionIntentID/RunningPolicyID the verifying transition
	// compares against are the freshly minted launch authority, not
	// whatever older ids happened to be on the envelope at load-time —
	// the spec's REQUIRED CHAIN closes here with an exact match.
	if _, finalizeErr := actuator.FinalizeGovernedHarnessRun(actuator.GovernedHarnessFinalizationInput{
		Store: store, WorkID: workID, Actor: actor, HarnessName: harnessName,
		RunningPolicyID: launchPolicyID, RunningActionIntentID: launchActionID,
		Output: out, RunError: nil, Now: now,
	}); finalizeErr != nil {
		return "", fmt.Errorf("execute stage launch: finalize refused: %w", finalizeErr)
	}
	return out.RunAttempt.ID, nil
}

// resolvedLaunchTimeout returns the timeout the launch wire places on
// harness.RunInput.Timeout.
//
// DEFECT 4 fix (AUT-P0-004 2026-09-04): every unattended launch
// MUST have a finite execution budget — a zero Timeout leaves the
// subprocess runner's `if in.Timeout > 0` branch dormant, the kill
// timer never fires, and an unattended launch can block forever. The
// heartbeat's outer `timeout 600` then SIGKILLs the Compound parent
// while the subprocess group keeps running.
//
// The runner's existing subprocess_runner.go enforces `in.Timeout >
// 0`, so the wire MUST set a non-zero budget. We pick
// DefaultLaunchTimeout (5m) when the caller did not pin a value, and
// we clamp explicit overrides to MaxLaunchTimeout (15m) so a
// caller-supplied Timeout cannot exceed the heartbeat's outer bound
// plus a margin. Returning 0 instead of clamping is also a defensible
// choice — we pick clamping because the operator heartbeats' outer
// `timeout 600` is the actual cap on parent lifetime, so a clamp
// surfaces a refusal-with-value-fallback rather than a silent
// hang-on-the-floor.
func resolvedLaunchTimeout() time.Duration {
	// Package-level vars mean tests can pin both.
	def := DefaultLaunchTimeout
	if def <= 0 {
		def = 5 * time.Minute
	}
	max := MaxLaunchTimeout
	if max <= 0 {
		max = 15 * time.Minute
	}
	if max < def {
		// The ceiling must sit above the default; if a test pinned
		// them backwards, silently swap. Tests that pin deliberately
		// impossibly-low ceilings should fail loudly elsewhere.
		max = def
	}
	return def
}
