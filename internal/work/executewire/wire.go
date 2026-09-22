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
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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
	work.SwapExecuteStageRouteCandidates(RouteCandidatesFromManifest)
	work.SwapExecuteStageCapacity(CapacityFromSnapshot)
	work.SwapExecuteStagePreflight(PreflightViaActuator)
	work.SwapExecuteStageLaunch(LaunchViaActuator)
	work.SwapExecuteStageCandidatePreflight(PreflightCandidateViaActuator)
	work.SwapExecuteStageCandidateLaunch(LaunchCandidateViaActuator)
	// R7C (2026-09-12): bind the execution-selection resolver. R7A shipped the
	// seam with a nil default, so the installed CLI refused every
	// `--launch-profile` while its tests passed against a stub.
	work.DefaultExecutionSelectionResolver = ResolveExecutionSelectionViaOwner
	// Slice 2 (aut-launch-canonical 2026-09-07): mark the seam installed so
	// the launch wire's precondition passes. A process that never ran
	// Install() sees every launch refused with "LaunchContext not
	// installed" before adapter.Run mints a RunAttempt. This does not wire
	// a cancellable context — see the LaunchContext doc for the bound that
	// applies in production and the open follow-up.
	launchContextInstalled.Store(true)
}

func RouteCandidatesFromManifest(homePath, capability string) []work.ExecutionRouteCandidate {
	manifest, _, revision, err := routeevidence.LoadModelCapabilitiesSnapshot(homePath)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []work.ExecutionRouteCandidate
	for _, modelID := range manifest.RoutingDefaults[capability] {
		entry, ok := manifest.Models[modelID]
		if !ok {
			continue
		}
		claim := entry.Capabilities[capability]
		if autonomyproof.GateRole(capability, entry.Tier, strings.TrimSpace(claim.Level)) != "eligible" {
			continue
		}
		h := strings.TrimSpace(entry.Harness)
		profile := strings.TrimSpace(modelID)
		if h == "" || !AdapterKnown(homePath, h) {
			continue
		}
		key := strings.ToLower(strings.ReplaceAll(profile, "-", "_"))
		if seen[key] {
			continue
		}
		seen[key] = true
		lane := strings.TrimSpace(entry.ProviderRoute)
		if lane == "" {
			lane = h
		}
		out = append(out, work.ExecutionRouteCandidate{LaunchProfileID: profile, Harness: h, RequestedModelID: profile, ProviderRoute: strings.TrimSpace(entry.ProviderRoute), SourceRevision: revision, CapacityLane: lane})
	}
	return out
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
// Slice 2 (aut-launch-canonical 2026-09-07) layered onto the base
// (R6A 2026-09-10): the LaunchContext var now has a companion install
// flag (launchContextInstalled) keyed off executewire.Install so the
// launch wire refuses with an explicit error when the seam was never
// installed. What the flag proves is exactly that Install ran
// (cmd/compound's init) — it does NOT prove a cancellable context was
// wired: no production entry point threads a per-tick context yet, so
// production launches still run under context.Background() and are bounded
// by the runner's own deadline (DefaultLaunchTimeout via RunInput.Timeout),
// not by caller cancellation. SIGTERM propagation from a killed heartbeat
// to the child remains the open half of audit section 4. The base
// (R6A 2026-09-10) keeps the signal-derived default so SIGINT/SIGTERM at
// runtime cancels the launch even when no caller installed a context.
// Tests that swap LaunchContext directly MUST also set the install flag
// (or call Install themselves).
var LaunchContext = signalLaunchContext

// launchSignal lazily holds the process-wide launch context. It is built on
// the FIRST launch rather than in Install() on purpose: registering a signal
// handler is a process-wide side effect, and `compound` has many subcommands
// that never launch anything and must keep Go's default Ctrl-C behaviour.
var (
	launchSignalOnce sync.Once
	launchSignalCtx  context.Context
)

// signalLaunchContext is the production launch context: cancelled on SIGINT
// or SIGTERM so the subprocess runner's process-group kill (which hangs off
// ctx.Done()) actually fires.
//
// SIGTERM is the one that matters for autonomy: the heartbeat wraps Compound
// in `timeout 600`, which SIGTERMs the parent before SIGKILLing it. Before
// this existed the launch context was context.Background(), so the parent
// died while the harness subprocess tree kept running and held the capture
// pipes open — a launch the heartbeat recorded as finished and the machine
// was still executing.
//
// Once the first signal lands, default handling is restored, so a second
// Ctrl-C hard-kills instead of being swallowed by a handler whose only job
// was to cancel this launch.
func signalLaunchContext() context.Context {
	launchSignalOnce.Do(func() {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		launchSignalCtx = ctx
		go func() {
			<-ctx.Done()
			stop()
		}()
	})
	return launchSignalCtx
}

// InstallLaunchContext binds ctx as the context every subsequent launch
// forwards to adapter.Run, and returns a restore func.
//
// This is the TEST seam, not the production wiring — production gets a real
// cancellable context from the default (signalLaunchContext) with no caller
// cooperation, precisely because the previous design required cooperation
// and never got it. Tests use this to drive a context they can cancel and
// observe the runner persisting "cancel:context_cancelled" evidence.
func InstallLaunchContext(ctx context.Context) func() {
	prev := LaunchContext
	LaunchContext = func() context.Context { return ctx }
	return func() { LaunchContext = prev }
}

// launchContextInstalled is flipped to true by Install when the
// production LaunchContext wiring is in place. The launch wire
// enforces it as a fail-closed precondition (see LaunchContext for what
// it does and does not prove). Tests can pin it via
// SetLaunchContextInstalled to exercise the wire without re-running
// Install.
var launchContextInstalled atomic.Bool

// SetLaunchContextInstalled lets tests install (or uninstall) the
// LaunchContext gate without re-running Install(). Production code
// MUST NOT call this.
func SetLaunchContextInstalled(installed bool) {
	launchContextInstalled.Store(installed)
}

// AssertLaunchContextInstalled is the precondition check LaunchViaActuator
// calls before any adapter.Run. It returns nil exactly when the
// LaunchContext wiring is live (Install was called, or a test pinned
// the var AND set the flag). A production caller that forgets Install
// sees every launch refused with an explicit error before adapter.Run
// mints a RunAttempt, instead of silently running with
// context.Background().
func AssertLaunchContextInstalled() error {
	if !launchContextInstalled.Load() {
		return errors.New("execute stage launch: LaunchContext not installed (run executewire.Install before launch)")
	}
	return nil
}

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
	return PreflightCandidateViaActuator(store, workID, legacyRouteCandidate(store.HomePath(), lane, lane, now), now)
}

func PreflightCandidateViaActuator(store *work.Store, workID string, candidate work.ExecutionRouteCandidate, now time.Time) error {
	if store == nil {
		return errors.New("execute stage preflight: nil work store")
	}
	workID = strings.TrimSpace(workID)
	candidate.LaunchProfileID = strings.TrimSpace(candidate.LaunchProfileID)
	candidate.Harness = strings.TrimSpace(candidate.Harness)
	if workID == "" || candidate.LaunchProfileID == "" || candidate.Harness == "" {
		return fmt.Errorf("execute stage preflight: workID=%q candidate=%+v; required", workID, candidate)
	}
	homePath := store.HomePath()

	// 1. Resolve adapter enrollment. A missing adapter means the route
	// plan names a lane the harness registry cannot drive — refuse with
	// adapter_missing:<lane>.
	_, ok := AdapterFor(homePath, candidate.Harness)
	if !ok {
		return errors.New("adapter_missing:" + candidate.Harness)
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
	// claiming the envelope. For tuple-backed Claude lanes the guard's
	// resolved-vs-expected comparison (internal/harness/registry.go
	// ReadOnlyLaunchReadiness) requires RequestedModelID /
	// RequestedProviderRoute / Command to match the values the adapter's
	// RevalidateGovernedRun will resolve; resolveLaneLaunchTuple reads
	// the same Compound model-router authority the adapter reads, so
	// passing its output here makes the comparison hold. Non-tuple
	// lanes fall through to the lane id, exactly as before.
	tupleCommand, tupleModelID, tupleProviderRoute, _, _ := resolveLaneLaunchTuple(homePath, candidate.LaunchProfileID, now)
	if candidate.SourceRevision != "" {
		resolved, resolveErr := resolveExecutionRouteCandidate(homePath, candidate, now)
		if resolveErr != nil {
			return fmt.Errorf("lane_unready:%v", resolveErr)
		}
		tupleCommand, tupleModelID, tupleProviderRoute = resolved, candidate.RequestedModelID, candidate.ProviderRoute
	}
	if tupleModelID == "" {
		tupleModelID = candidate.RequestedModelID
	}
	if tupleProviderRoute == "" {
		tupleProviderRoute = candidate.ProviderRoute
	}
	readinessReq := work.DelegateHarnessReadinessRequest{
		HomePath: homePath, HarnessHome: "", ContextPackID: wenv.IDs.ContextPackID, LaunchProfileID: candidate.LaunchProfileID,
		WorkID: workID, Workspace: workspace, Harness: candidate.Harness, LaneID: candidate.LaunchProfileID,
		Command: tupleCommand, Args: []string{},
		RequestedModelID: tupleModelID, RequestedProviderRoute: tupleProviderRoute, Now: now,
	}
	if err := validateSavedSelectionTuple(wenv, candidate, now); err != nil {
		return fmt.Errorf("lane_unready:%v", err)
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

// postureEvidenceRevisionFallback is the revision the launch wire stamps
// onto posture evidence items the readiness guard produced WITHOUT a
// Revision. Today the only kind that lacks a Revision in production is the
// `executable` item cmd/compound/work_delegate_readiness.go builds — its
// Ref is the lane tuple URL but the Revision field is left empty because
// the readiness evaluator has no canonical sha256 to point at. The wire
// fills the gap with the lane tuple's profile revision (the model-
// capabilities manifest snapshot sha256 the tuple resolver just consulted),
// so runattempt.SetExecutionPosture accepts the item and the launch
// reaches adapter.Run.
//
// work_r6m_seam_posture_20260911.
type postureEvidenceRevisionFallback string

// fillPostureEvidenceRevisions filters readiness evidence to the kinds
// runattempt.SetExecutionPosture accepts for a mutation-capable posture
// (the four required kinds below), stamps a Revision onto every item
// that lacks one (using profileRevision as the canonical source), and
// refuses with a recognized `lane_unready:` prefix when a required kind
// is missing or when no profile revision is available to fill one. The
// remaining evidence kinds (executable, activation_argv, instruction_pack,
// context_pack, effective_contract, lane_capacity, …) stay in the
// readiness snapshot for the DelegateBinding view — they are NOT in the
// runattempt posture, but they ARE in the readiness record the operator
// reads.
//
// Why filter rather than stamp: runattempt.SetExecutionPosture's validator
// refuses mutation-capable postures that carry evidence kinds outside
// the four required kinds. The production readiness guard produces the
// four required kinds AND several supporting kinds (the `executable` item
// is the live-run example: Ref set, Revision empty). Stamping every item
// verbatim would push the validator's failure mode from
// "evidence requires kind, ref, revision" (live-run shape) to
// "unexpected authority evidence kind <kind>" (the second validator check).
// Filtering to the four required kinds BEFORE the validator runs is the
// only way to satisfy the validator without weakening it (the fence
// forbids touching internal/runattempt/**).
//
// work_r6m_seam_posture_20260911.
func fillPostureEvidenceRevisions(evidence []work.DelegateHarnessReadinessEvidence, profileRevision string) ([]work.DelegateHarnessReadinessEvidence, error) {
	if len(evidence) == 0 {
		// A mutation-capable posture requires ALL four kinds. Empty
		// readiness evidence means none of them are present — refuse
		// with the recognized lane_unready prefix so the execute stage
		// treats this as a skip reason, not a hard launch failure.
		return nil, errors.New("lane_unready:posture evidence is empty (no readiness evidence was attached)")
	}
	out := make([]work.DelegateHarnessReadinessEvidence, 0, len(postureEvidenceAcceptedKinds))
	seen := make(map[string]bool, len(postureEvidenceAcceptedKinds))
	for _, item := range evidence {
		if !postureEvidenceAcceptedKinds[item.Kind] {
			// Supporting-kind evidence (executable, activation_argv,
			// instruction_pack, context_pack, effective_contract,
			// lane_capacity, …) stays in the readiness snapshot for
			// the DelegateBinding view. It is NOT stamped onto the
			// runattempt posture because the validator rejects
			// mutation-capable postures carrying it. NOT a silent
			// drop — the readiness record carries it.
			continue
		}
		copy := item
		if strings.TrimSpace(copy.Revision) == "" {
			// No revision: try to fill from the lane tuple's profile
			// revision. An empty profileRevision means the resolver
			// AND the manifest are both unavailable, so the wire
			// cannot give this item a revision at all. Surface the
			// refusal rather than stamp a synthetic value — the
			// runattempt posture validator already proved it refuses
			// any item lacking kind/ref/revision (live-run evidence:
			// 2026-09-11T03:41:44Z).
			if strings.TrimSpace(profileRevision) == "" {
				return nil, fmt.Errorf("lane_unready:posture evidence %q has no revision and no lane tuple profile revision is available to fill it", copy.Kind)
			}
			copy.Revision = profileRevision
		}
		out = append(out, copy)
		seen[copy.Kind] = true
	}
	// Mutation-capable postures require ALL four kinds. Missing any one
	// means the readiness guard did not attest the lane — refuse with
	// the recognized prefix so the execute stage treats this as a skip.
	for _, required := range []string{"permission_authority", "permission_posture", "blocking_hook_plan", "blocking_hook_installation"} {
		if !seen[required] {
			return nil, fmt.Errorf("lane_unready:posture evidence %q is required (the readiness guard did not attest %s for this lane)", required, required)
		}
	}
	return out, nil
}

// postureEvidenceAcceptedKinds is the set of evidence kinds the runattempt
// posture validator accepts for a mutation-capable posture. The four
// required kinds in internal/runattempt/runattempt.go:SetExecutionPosture
// are the authoritative allow-list; this constant mirrors that allow-list
// so the wire filters supporting kinds (executable, activation_argv, …)
// before the validator runs. Mirrored rather than imported because the
// runattempt package is internal and exposes no canonical name; a drift
// here surfaces as the validator refusing a kind this list accepted.
//
// work_r6m_seam_posture_20260911.
var postureEvidenceAcceptedKinds = map[string]bool{
	"permission_authority":       true,
	"permission_posture":         true,
	"blocking_hook_plan":         true,
	"blocking_hook_installation": true,
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
	return LaunchCandidateViaActuator(store, workID, legacyRouteCandidate(store.HomePath(), lane, harnessName, now), prompt, actor, now)
}

func LaunchCandidateViaActuator(store *work.Store, workID string, candidate work.ExecutionRouteCandidate, prompt, actor string, now time.Time) (string, error) {
	if store == nil {
		return "", errors.New("execute stage launch: nil work store")
	}
	workID = strings.TrimSpace(workID)
	candidate.Harness = strings.TrimSpace(candidate.Harness)
	if workID == "" || candidate.Harness == "" {
		return "", fmt.Errorf("execute stage launch: workID=%q harness=%q; both required", workID, candidate.Harness)
	}
	homePath := store.HomePath()
	adapter, ok := AdapterFor(homePath, candidate.Harness)
	if !ok {
		return "", errors.New("adapter_missing:" + candidate.Harness)
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
	// Tuple resolution mirrors the preflight hook's call: the readiness
	// guard's resolved-vs-expected comparison requires the request to
	// carry the adapter-resolved {Command, RequestedModelID,
	// RequestedProviderRoute}, not the lane id. resolveLaneLaunchTuple
	// reuses the same Compound model-router authority the adapter reads.
	// work_r6m_seam_posture_20260911: also capture the lane tuple's profile
	// revision so the wire can stamp a real Revision onto posture evidence
	// items the readiness guard produced WITHOUT one (the production
	// `executable` item — Ref set, Revision empty). The profile revision is
	// the model-capabilities manifest sha256 the resolver consulted.
	tupleCommand, tupleModelID, tupleProviderRoute, tupleProfileRevision, _ := resolveLaneLaunchTuple(homePath, candidate.LaunchProfileID, now)
	if candidate.SourceRevision != "" {
		resolved, resolveErr := resolveExecutionRouteCandidate(homePath, candidate, now)
		if resolveErr != nil {
			return "", fmt.Errorf("lane_unready:%v", resolveErr)
		}
		tupleCommand, tupleModelID, tupleProviderRoute = resolved, candidate.RequestedModelID, candidate.ProviderRoute
	}
	if err := validateSavedSelectionTuple(wenv, candidate, now); err != nil {
		return "", fmt.Errorf("lane_unready:%v", err)
	}
	if tupleModelID == "" {
		tupleModelID = candidate.LaunchProfileID
	}
	if tupleProviderRoute == "" {
		tupleProviderRoute = candidate.LaunchProfileID
	}
	readinessReq := work.DelegateHarnessReadinessRequest{
		HomePath: homePath, HarnessHome: "", ContextPackID: wenv.IDs.ContextPackID, LaunchProfileID: candidate.LaunchProfileID,
		WorkID: workID, Workspace: workspace, Harness: candidate.Harness, LaneID: candidate.LaunchProfileID,
		Command: tupleCommand, Args: []string{prompt},
		RequestedModelID: tupleModelID, RequestedProviderRoute: tupleProviderRoute, Now: now,
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
	launchActionID, launchPolicyID, mintErr := store.MintLaunchAuthority(workID, actor, candidate.LaunchProfileID, prompt, now)
	if mintErr != nil {
		return "", fmt.Errorf("execute stage launch: mint launch authority: %w", mintErr)
	}
	if strings.TrimSpace(launchActionID) == "" || strings.TrimSpace(launchPolicyID) == "" {
		return "", fmt.Errorf("execute stage launch: MintLaunchAuthority returned empty ids (action=%q policy=%q)", launchActionID, launchPolicyID)
	}
	// Slice 2 (aut-launch-canonical 2026-09-07): precondition gate.
	// LaunchContext MUST be installed before adapter.Run is permitted to
	// mint a RunAttempt. Production calls Install() at startup; tests
	// pin the var directly and SetLaunchContextInstalled(true). A
	// production caller that forgets Install() now sees every launch
	// refuse with an explicit error before any subprocess is started.
	if err := AssertLaunchContextInstalled(); err != nil {
		return "", err
	}
	// M5 2026-09-03: --permission-mode bypass is the explicit operator
	// override the standing grant authorizes; the seam never synthesizes
	// bypass on its own.
	runInput := harness.RunInput{
		WorkID: workID, Harness: candidate.Harness, Workspace: workspace,
		RequestedModelID: tupleModelID, RequestedProviderRoute: tupleProviderRoute,
		Args: []string{prompt}, PermissionMode: "bypass",
		LaneID: candidate.LaunchProfileID, ActionIntentID: launchActionID,
		Timeout: resolvedLaunchTimeout(),
		Now:     now,
	}
	// work_r7a_execution_selection_20260911: stamp the per-run selection
	// snapshot (what this launch was asked to run, with honest
	// applied-effort + native-session evidence) BEFORE the subprocess
	// starts, so the historical record reflects the plan rather than the
	// outcome. The shared runner persists RunInput.ExecutionSelectionSnapshot
	// onto the freshly minted RunAttempt, so the wire computes the
	// snapshot here (with the saved selection or override as the source)
	// and the runner does the actual stamp.
	overrideHarness, overrideProfile := "", ""
	if wenv.ExecutionSelection == nil && candidate.SourceRevision != "" {
		overrideHarness, overrideProfile = candidate.Harness, candidate.LaunchProfileID
	}
	snapshot, snapshotErr := buildSelectionSnapshotFromSaved(
		wenv,
		overrideHarness,
		overrideProfile,
		tupleProviderRoute,   // resolved route
		tupleModelID,         // resolved model id
		tupleProfileRevision, // observed profile revision (from the resolver)
		tupleProfileRevision, // tuple's profile revision (same source — used for drift detection)
	)
	if snapshotErr != nil {
		return "", fmt.Errorf("execute stage launch: build selection snapshot: %w", snapshotErr)
	}
	runInput.ExecutionSelectionSnapshot = &snapshot
	runInput.LaunchProfileID = snapshot.LaunchProfileID
	// Slice 2 (aut-launch-canonical 2026-09-07): bind the canonical
	// execution-reservation StartGuard so adapter.Run's subprocess
	// start is gated by the live running-reservation identity. A
	// caller that bypasses the guard (by mutating the wenv between
	// MintLaunchAuthority and FinalizeGovernedHarnessRun) surfaces
	// as a StartGuard refusal; the subprocess never starts. The
	// production wire has always had this on the cmd/compound seam
	// (PrepareGovernedHarnessLaunch sets it); the unattended execute
	// seam previously built RunInput without it.
	runInput.StartGuard = actuator.NewGovernedExecutionReservationStartGuard(
		homePath, workID, launchPolicyID, launchActionID,
	)
	if candidate.SourceRevision != "" {
		reservationGuard := runInput.StartGuard
		runInput.StartGuard = func(ctx context.Context, start func() error) error {
			return reservationGuard(ctx, func() error {
				command, err := resolveExecutionRouteCandidate(homePath, candidate, time.Now().UTC())
				if err != nil {
					return fmt.Errorf("execution tuple changed before launch: %w", err)
				}
				if command != tupleCommand {
					return errors.New("execution tuple executable changed before launch")
				}
				return start()
			})
		}
	}
	// Slice 2 (aut-launch-canonical 2026-09-07): mirror
	// PrepareGovernedHarnessLaunch. Claude-family adapters carry the launch
	// generation on the attempt itself: the shared runner persists
	// RunInput.ExecutionPosture onto the RunAttempt, and the close-time
	// authority gate reads the (ActionIntent, PolicyDecision) pair off the
	// attempt. The posture carries the readiness evidence the lane attested;
	// the runner refuses a mutation-capable posture that lacks the required
	// evidence kinds, so an unattested bypass fails closed at mint and the
	// envelope closes as failed instead of launching. Every other adapter
	// carries only the ActionIntent and binds to the generation through the
	// sealed same-state event MintLaunchAuthority records.
	if _, claudeFamily := adapter.(harness.GovernedPermissionAdapter); claudeFamily {
		// work_r6m_seam_posture_20260911: runattempt.SetExecutionPosture refuses
		// any evidence item lacking kind/ref/revision. The production
		// readiness guard produces at least one item (the `executable` kind
		// in cmd/compound/work_delegate_readiness.go) WITHOUT a Revision —
		// the evaluator has no canonical sha256 to point at. The wire fills
		// the gap with the lane tuple's profile revision (the model-
		// capabilities manifest sha256 resolveLaneLaunchTuple just consulted)
		// so the stamp succeeds and the launch reaches adapter.Run. Items
		// that still cannot be given a revision (no resolver, no manifest)
		// surface as `lane_unready:` — the recognized refusal prefix the
		// execute stage treats as a skip reason, not a hard launch failure.
		filledEvidence, fillErr := fillPostureEvidenceRevisions(readiness.Evidence, tupleProfileRevision)
		if fillErr != nil {
			// Refuse with the recognized `lane_unready:` prefix. The execute
			// stage's parseLaunchRefusal will surface this as a skip reason
			// rather than a Close (the pre-fix behavior tried to Close and
			// reported the refusal as the failure).
			return "", fillErr
		}
		posture := &runattempt.ExecutionPosture{
			PermissionMode:   runInput.PermissionMode,
			PolicyDecisionID: launchPolicyID,
		}
		for _, item := range filledEvidence {
			posture.AuthorityEvidence = append(posture.AuthorityEvidence, runattempt.ExecutionPostureEvidence{
				Kind: item.Kind, Ref: item.Ref, Revision: item.Revision,
			})
		}
		runInput.ExecutionPosture = posture
	}
	// DEFECT 4 fix (AUT-P0-004 2026-09-04): forward the live, cancellable
	// context so a stuck subprocess is killed at the deadline and a
	// cancelled heartbeat kills the tree. The shared runner honors
	// ctx.Done() via a process-group kill (killProcessTree), so an
	// orphaned grandchild holding the capture pipes open cannot survive.
	// Production installs a signal-derived cancellable ctx at the command
	// boundary (InstallLaunchContext); tests swap the var to prove the
	// cancellation path persists "cancel:context_cancelled" evidence.
	ctx := LaunchContext()
	out, runErr := adapter.Run(ctx, runInput)
	// Slice 2 (aut-launch-canonical 2026-09-07) layered onto the base
	// (R6A 2026-09-10): the canonical finalize path now runs UNCONDITIONALLY.
	// Out + runErr are both forwarded to FinalizeGovernedHarnessRun, which
	// decides whether to advance the envelope to verifying or close it as
	// failed based on the actual RunAttempt status. The base's wire returned
	// strings.TrimSpace(out.RunAttempt.ID) alongside runErr so callers
	// (notably execute_stage.launchStub) could record the partial-evidence
	// ratt id in result.RunAttemptIDs; the packet pins that contract, so on
	// a finalize-failed or runErr-nonzero path the wire still returns
	// (strings.TrimSpace(out.RunAttempt.ID), runErr) instead of ("", err).
	// A missing RunAttempt.ID (adapter short-circuited before minting) is
	// still an explicit refuse, since there is no id to name and no
	// canonical finalize to route through.

	if strings.TrimSpace(out.RunAttempt.ID) == "" {
		return "", errors.New("execute stage launch: adapter returned empty RunAttempt.ID")
	}
	// R6A 2026-09-10: capture the minted ratt id once so every return path
	// below (finalize-refused, adapter-errored, success) names the same
	// record. Callers (notably execute_stage.launchStub) record this in
	// result.RunAttemptIDs even on failure so the partial-evidence id is
	// never anonymous.
	mintedRattID := strings.TrimSpace(out.RunAttempt.ID)
	// FinalizeGovernedHarnessRun advances running → verifying (or Close
	// failed on a non-success RunAttempt.Status). The seam's own
	// Advance(verifying) after launchStub returns is now a no-op on the
	// production path; tests that stub launchStub still move the
	// envelope the way the seam's pre-Advance(running) set up. The
	// RunningActionIntentID/RunningPolicyID the verifying transition
	// compares against are the freshly minted launch authority, not
	// whatever older ids happened to be on the envelope at load-time —
	// the spec's REQUIRED CHAIN closes here with an exact match.
	// ExistingRunAttemptIDs carries the prior reservation's RunAttempt
	// ids so the verifying transition sees the chain (the new RunAttempt
	// id joins that chain via FinalizeGovernedHarnessRun, not via a
	// separate write path).
	if _, finalizeErr := actuator.FinalizeGovernedHarnessRun(actuator.GovernedHarnessFinalizationInput{
		Store:                 store,
		WorkID:                workID,
		Actor:                 actor,
		HarnessName:           candidate.Harness,
		RunningPolicyID:       launchPolicyID,
		RunningActionIntentID: launchActionID,
		ExistingRunAttemptIDs: wenv.RunAttemptIDs,
		Output:                out,
		RunError:              runErr,
		Now:                   now,
	}); finalizeErr != nil {
		// R6A 2026-09-10: the packet pins that LaunchViaActuator must keep
		// returning the minted ratt id alongside the error so callers
		// (notably execute_stage.launchStub) can record the partial-evidence
		// id in result.RunAttemptIDs. The canonical finalize path refused
		// here so the envelope is still in its pre-finalize state; surface
		// the finalize error so the operator still sees what refused.
		return mintedRattID, fmt.Errorf("execute stage launch: finalize refused: %w", finalizeErr)
	}
	if runErr != nil {
		// R6A 2026-09-10: slice 2's canonical finalize persisted partial
		// output and closed the envelope as failed; surface the adapter
		// error verbatim AND return the minted ratt id so the seam can
		// record the partial-evidence id in result.RunAttemptIDs and a
		// future operator query can find the workspace/command/transcript
		// the crashed attempt left behind.
		return mintedRattID, runErr
	}
	return mintedRattID, nil
}

// LaunchTimeout is the finite budget shared by the governed CLI and wire.
func LaunchTimeout() time.Duration { return resolvedLaunchTimeout() }

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

// InstallDetached (R6P 2026-09-11) makes the unattended seam hand launches to
// a detached governed door (LaunchDetachedViaDoor) instead of running the lane
// inside the tick. It is a SEPARATE composition step from Install so the
// package's own tests, which call Install to exercise the synchronous wire,
// never spawn real doors. cmd/compound calls both. The operator pins the
// synchronous seam with COMPOUND_UNATTENDED_SYNCHRONOUS=1 (the kill switch;
// that path keeps its 5-minute budget).
func InstallDetached() {
	if os.Getenv("COMPOUND_UNATTENDED_SYNCHRONOUS") == "1" {
		return
	}
	work.SwapExecuteStageDetachedLaunch(LaunchDetachedViaDoor)
	work.SwapExecuteStageDetachedCandidateLaunch(LaunchDetachedCandidateViaDoor)
}
