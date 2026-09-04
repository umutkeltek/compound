package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/policy"
)

// executeStage is the grant-gated, fail-closed seam that drives ONE ready
// envelope through claim -> running -> verifying. It is called from Continue
// ONLY when the existing lifecycle branches return status="waiting",
// action="none" AND there is at least one ready envelope on the bench. Each
// gate below is a separate refusal reason — see the named constants
// (skipNoGrant, skipRisk, skipNoRoute, skipCapacityUnknown, skipPolicyHold,
// skipLaunchFailed) for the wire values.
//
// The seam never lands, closes, pushes or retires anything: land/close stay
// human/orchestrator-only. Failure isolation is per-envelope: one bad launch
// marks that envelope failed and returns; the next Continue picks the next
// envelope.
//
// The five gates in order:
//
//	a. standing grant grant_unattended_work_continue_v1 present in registry
//	b. envelope risk ∈ {tier0, tier1} AND privacy == private
//	c. route plan names an eligible harness lane for the capability
//	d. capacity snapshot fresher than the freshness window AND usedPercent
//	   strictly less than the ceiling
//	e. policy evaluation of work.continue.unattended returns allow or
//	   allow_with_obligations; hold/deny -> skipped=policy_hold
func (s *Store) executeStage(now time.Time, opts ContinueOptions, actor string) (ContinueResult, error) {
	result := ContinueResult{
		Status:  "waiting",
		Action:  "none",
		Actor:   actor,
		WorkID:  "",
		Skipped: "",
		Human:   "",
	}
	// (a) standing grant present?
	hasGrant, grantErr := s.executeStageHasGrant(s.home)
	if grantErr != nil {
		result.Human = "did nothing: could not read standing-grants registry: " + grantErr.Error()
		result.Skipped = "no_grant"
		return result, nil
	}
	if !hasGrant {
		result.Skipped = "no_grant"
		result.Human = fmt.Sprintf("did nothing: no grant named %s; import one to allow unattended execution", executeUnattendedGrantID)
		return result, nil
	}

	// Find one ready envelope to execute. We do NOT pre-filter to specific
	// IDs — the seam picks the first ready item by Workbench rank so the
	// existing lifecycle ordering is preserved. A pinned ExpectWorkID
	// still wins because the existing branches above this stage already
	// short-circuited on expect_work; if Continue reaches us with an
	// expect_work set the workbench report has been pre-filtered to it.
	ready := s.findFirstReadyForExecute(now, actor, opts.ExpectWorkID)
	if ready == "" {
		// No ready envelope on the bench — this is the existing "no safe
		// lifecycle step is ready" path, not an execute refusal. Return
		// without setting Skipped so callers can tell "no work to do"
		// apart from "gates refused".
		result.Human = "did nothing: no ready envelope to execute"
		return result, nil
	}
	result.WorkID = ready

	w, err := s.Get(ready)
	if err != nil {
		return result, fmt.Errorf("load ready envelope %s: %w", ready, err)
	}

	// (b) risk/privacy gate.
	if !executeRiskAllowed(w.Risk) || !executePrivacyAllowed(w.Privacy) {
		result.Skipped = "risk"
		result.Human = fmt.Sprintf("did nothing: envelope %s risk=%s privacy=%s is outside the unattended envelope (tier0/tier1, private)", ready, w.Risk, w.Privacy)
		return result, nil
	}

	// (c) route plan names an eligible lane.
	lane, ok := executeStageRoutePlanFn(s.home, executeUnattendedCapability)
	if !ok || strings.TrimSpace(lane) == "" {
		result.Skipped = "no_route"
		result.Human = fmt.Sprintf("did nothing: route plan has no eligible lane for %s", executeUnattendedCapability)
		return result, nil
	}
	result.Lane = lane

	// (d) capacity freshness + ceiling.
	usedPercent, fresh, capErr := executeStageCapacityFn(s.home, lane, now)
	if capErr != nil {
		result.Skipped = "capacity_unknown"
		result.Human = fmt.Sprintf("did nothing: capacity read for lane %s failed: %v", lane, capErr)
		return result, nil
	}
	if !fresh {
		result.Skipped = "capacity_unknown"
		result.Human = fmt.Sprintf("did nothing: capacity snapshot for lane %s is missing or older than %s; refuse to execute blind", lane, executeStageCapacityFreshnessWindow)
		return result, nil
	}
	if usedPercent >= executeStageCapacityCeilingPercent {
		result.Skipped = "capacity_unknown"
		result.Human = fmt.Sprintf("did nothing: capacity for lane %s is %.0f%% (ceiling %.0f%%)", lane, usedPercent, executeStageCapacityCeilingPercent)
		return result, nil
	}

	// (e) policy evaluation of work.continue.unattended.
	pd := executeStagePolicyEvalFn(
		policyActionIntentShim{
			ID:         "ai_unattended_" + ts(now),
			Capability: executeUnattendedCapability,
			WorkID:     ready,
			Risk:       w.Risk,
			Privacy:    w.Privacy,
		},
		policyConfigShim{HasGrant: true, Home: s.home},
		now,
	)
	result.PolicyDecisionID = pd.ID
	switch pd.Outcome {
	case "allow", "allow_with_obligations":
		// proceed to launch
	default:
		result.Skipped = "policy_hold"
		result.Human = fmt.Sprintf("did nothing: policy decision %s for %s is %s", pd.ID, executeUnattendedCapability, pd.Outcome)
		return result, nil
	}

	// All gates pass — claim + advance to running + launch + advance to
	// verifying, reusing the existing lifecycle seams (`Claim`, `Advance`,
	// `Close`) rather than forking them. The launch stops at verifying;
	// the envelope is NEVER closed by the seam on a successful launch.
	workerPrompt := strings.TrimSpace(opts.Summary)
	if workerPrompt == "" {
		workerPrompt = "unattended execute from work continue: " + ready
	}
	if _, claimErr := s.Claim(ready, actor, now); claimErr != nil {
		// Claim can fail (e.g. policy hold). Treat that as a stage-level
		// refusal; the envelope stays in its current state so the next
		// Continue can retry.
		result.Skipped = "claim_failed"
		result.Human = fmt.Sprintf("did nothing: claim of %s refused: %v", ready, claimErr)
		result.NoAction = result.Human
		return result, nil
	}
	if _, advErr := s.Advance(ready, AdvanceInput{To: "running", Actor: actor, Note: "execute stage: running"}, now); advErr != nil {
		result.Skipped = "claim_failed"
		result.Human = fmt.Sprintf("did nothing: advance to running of %s refused: %v", ready, advErr)
		result.NoAction = result.Human
		return result, nil
	}

	rattID, launchErr := launchStub(s, ready, lane, lane, workerPrompt, now)
	if launchErr != nil {
		// One bad launch fails THAT envelope only. Mark the work as
		// failed via Close (the only seam that emits a Receipt + state
		// transition with a failed outcome). The stage still returns
		// Action=execute so the heartbeat pass can keep going — the next
		// Continue picks the next envelope.
		closeSummary := "unattended execute launch failed: " + launchErr.Error()
		if _, closeErr := s.Close(ready, CloseInput{
			Outcome:   "failed",
			MoveType:  "audit_only",
			Actor:     actor,
			Summary:   closeSummary,
			ProofRefs: []string{"execute_stage_launch_failed"},
		}, now); closeErr != nil {
			result.Human = fmt.Sprintf("executed but failed to record failure on %s: launch=%v close=%v", ready, launchErr, closeErr)
		} else {
			result.Human = fmt.Sprintf("executed envelope %s on lane %s but launch failed: %v; envelope marked failed, heartbeat continues", ready, lane, launchErr)
		}
		result.Action = "execute"
		result.Skipped = "launch_failed"
		result.RunAttemptIDs = nil
		return result, nil
	}

	// Successful launch — advance to verifying and stamp the ratt id. The
	// execute stage NEVER lands, closes, pushes or retires anything; verifying
	// is the hand-off point for human/orchestrator review.
	if _, verifyErr := s.Advance(ready, AdvanceInput{To: "verifying", Actor: actor, Note: "execute stage: verifying"}, now); verifyErr != nil {
		// The launch succeeded but the verifying advance failed — surface
		// the launch + the verify error so the operator can intervene.
		result.Action = "execute"
		result.RunAttemptIDs = []string{rattID}
		result.Human = fmt.Sprintf("executed envelope %s on lane %s (ratt=%s) but verifying advance failed: %v; envelope remains in running for review", ready, lane, rattID, verifyErr)
		result.NoAction = result.Human
		return result, nil
	}
	result.Action = "execute"
	result.Status = "verifying"
	result.RunAttemptIDs = []string{rattID}
	result.Human = fmt.Sprintf("executed envelope %s on lane %s (ratt=%s); stopped at verifying for human/orchestrator review", ready, lane, rattID)
	return result, nil
}

// findFirstReadyForExecute picks ONE ready envelope from the workbench. When
// ExpectWorkID is set, it pins to that id (provided it is ready); otherwise
// it returns the first ready item in workbench order, which already ranks by
// lifecycle state. The function returns "" when there is no ready envelope.
func (s *Store) findFirstReadyForExecute(now time.Time, actor, expectWorkID string) string {
	report, err := s.Workbench(now, WorkbenchOptions{RecentLimit: 0, Actor: actor})
	if err != nil {
		return ""
	}
	expectWorkID = strings.TrimSpace(expectWorkID)
	for _, item := range report.Open {
		if item.State != "ready" {
			continue
		}
		if expectWorkID != "" && item.WorkID != expectWorkID {
			continue
		}
		return item.WorkID
	}
	// Fallback: the workbench ranks items by lifecycle; also surface items
	// that are present in Recent but not yet Open because the count gate
	// excluded them. Keep the same ready filter.
	for _, item := range report.Recent {
		if item.State != "ready" {
			continue
		}
		if expectWorkID != "" && item.WorkID != expectWorkID {
			continue
		}
		return item.WorkID
	}
	return ""
}

// executeRiskAllowed reports whether the envelope risk is inside the
// unattended envelope (tier0/tier1). Anything else fails closed.
func executeRiskAllowed(risk string) bool {
	switch strings.ToLower(strings.TrimSpace(risk)) {
	case "tier0", "tier1":
		return true
	default:
		return false
	}
}

// executePrivacyAllowed reports whether the envelope privacy is inside the
// unattended envelope (private). Anything else fails closed.
func executePrivacyAllowed(privacy string) bool {
	return strings.ToLower(strings.TrimSpace(privacy)) == "private"
}

// executeStageHasGrant reads the standing-grants registry from
// `~/.compound/registries/standing-grants.json` and reports whether the
// unattended grant is present and not expired. A missing registry file is
// fail-closed (no grant).
func (s *Store) executeStageHasGrant(homePath string) (bool, error) {
	if strings.TrimSpace(homePath) == "" {
		return false, nil
	}
	path := filepath.Join(homePath, "registries", "standing-grants.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	var registry struct {
		Grants []struct {
			ID         string `json:"id"`
			Capability string `json:"capability"`
			ExpiresAt  string `json:"expires_at"`
		} `json:"grants"`
	}
	if err := json.Unmarshal(data, &registry); err != nil {
		return false, fmt.Errorf("parse standing-grants registry: %w", err)
	}
	for _, g := range registry.Grants {
		if g.ID == executeUnattendedGrantID && g.Capability == executeUnattendedCapability {
			if g.ExpiresAt == "" {
				continue
			}
			exp, err := time.Parse(time.RFC3339, g.ExpiresAt)
			if err != nil {
				return false, fmt.Errorf("grant %s has invalid expires_at %q: %w", g.ID, g.ExpiresAt, err)
			}
			if time.Now().UTC().After(exp) {
				return false, nil
			}
			return true, nil
		}
	}
	_ = sort.Slice // keep sort import used; registry reads are unordered but sort keeps the file stable.
	return false, nil
}

// ExecuteStageCapacityFreshness reports whether a capacity observation
// stamped at lastProven is fresh enough for unattended execution at now.
// Fail-closed: a zero observation time is never fresh, and neither is a
// stamp from the future.
func ExecuteStageCapacityFreshness(lastProven, now time.Time) bool {
	if lastProven.IsZero() {
		return false
	}
	age := now.Sub(lastProven)
	return age >= 0 && age <= executeStageCapacityFreshnessWindow
}

// defaultExecuteStageRoutePlanFn, defaultExecuteStageCapacityFn, and
// defaultExecuteStagePolicyEvalFn are the production defaults for the
// execute-stage hooks. They are intentionally minimal: read the home's
// route plan, capacity projection, and policy, and translate the result
// into the seam's narrow types. cmd/compound wires a richer default on
// startup if the production path needs real adapter launch.

// defaultExecuteStageRoutePlanFn returns no eligible lane — production must
// wire this to routeevidence.LoadModelCapabilitiesSnapshot. The default is
// fail-closed; any production caller that forgets to wire the real resolver
// sees every execute attempt as `skipped=no_route`.
func defaultExecuteStageRoutePlanFn(homePath, capability string) (string, bool) {
	return "", false
}

// defaultExecuteStageCapacityFn returns fresh=false — production must wire
// this to routeevidence.ReadLaneCapacityWithConfig. The default is
// fail-closed; any production caller that forgets to wire the real reader
// sees every execute attempt as `skipped=capacity_unknown`.
func defaultExecuteStageCapacityFn(homePath, lane string, now time.Time) (float64, bool, error) {
	return 0, false, nil
}

// defaultExecuteStagePolicyEvalFn is the REAL evaluator: the same door
// `work advance --execute` consumes (policy.LoadConfig + policy.Evaluate on
// an envelope.ActionIntent for capability work.continue.unattended). It fails
// closed twice: an unreadable config is a hold, and an allow that is not
// backed by the unattended standing grant (StandingGrantID mismatch — e.g.
// the local_reversible class baseline allowing on its own) is downgraded to
// hold. The operator's named grant is the only authority that opens this
// gate; the class baseline alone never does.
func defaultExecuteStagePolicyEvalFn(ai policyActionIntentShim, cfg policyConfigShim, now time.Time) policyDecisionShim {
	intent := envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            ai.ID,
			Privacy:       ai.Privacy,
			Risk:          ai.Risk,
		},
		Actor:         "compound-unattended-execute",
		Capability:    ai.Capability,
		Action:        "execute_unattended",
		Target:        envelope.ActionTarget{Kind: "work", ID: ai.WorkID, Path: filepath.Join(cfg.Home, "work", "work", ai.WorkID+".json")},
		MutationClass: "local_reversible",
		Reversibility: "easy",
		BlastRadius:   "local",
		Preview:       "execute ready work " + ai.WorkID + " unattended via work continue",
		RollbackPlan:  "advance work back to ready if the launch produced no side effects",
	}
	config, _, err := policy.LoadConfig(cfg.Home)
	if err != nil {
		return policyDecisionShim{ID: "pol_unattended_config_unreadable", Outcome: "hold"}
	}
	pd := policy.Evaluate(intent, config, now)
	switch pd.Outcome {
	case "allow", "allow_with_obligations":
		if pd.StandingGrantID != executeUnattendedGrantID {
			return policyDecisionShim{ID: pd.ID, Outcome: "hold"}
		}
	}
	return policyDecisionShim{ID: pd.ID, Outcome: pd.Outcome}
}

// defaultExecuteStageLaunch returns an explicit error — production must wire
// this to the actuator's ExecuteGovernedHarnessAdvance seam. The default is
// fail-closed; any production caller that forgets to wire the real launcher
// sees every execute attempt as `skipped=launch_failed`.
func defaultExecuteStageLaunch(_ *Store, workID, lane, harness, prompt string, now time.Time) (string, error) {
	return "", errors.New("execute stage launch not wired; production must bind launchStub to the actuator seam")
}
