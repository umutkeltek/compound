package actuator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/harness"
	homepkg "compound/internal/home"
	"compound/internal/hookplan"
	"compound/internal/policy"
	"compound/internal/runattempt"
	"compound/internal/work"
)

var governedClaudeReadinessEvidenceKinds = []string{
	"permission_authority",
	"permission_posture",
	"blocking_hook_plan",
	"blocking_hook_installation",
}

// GovernedHarnessFinalizationInput binds adapter completion to the exact
// running work generation that authorized it.
type GovernedHarnessFinalizationInput struct {
	Store                 *work.Store
	WorkID                string
	Actor                 string
	HarnessName           string
	RunningPolicyID       string
	RunningActionIntentID string
	ExistingRunAttemptIDs []string
	Output                harness.RunOutput
	RunError              error
	Now                   time.Time
}

// GovernedHarnessFinalizationResult keeps CLI rendering outside the actuator
// while preserving every lifecycle and attachment warning.
type GovernedHarnessFinalizationResult struct {
	Succeeded       bool
	WorkID          string
	FinalState      string
	FailedAuditOnly bool
	Warnings        []string
}

// GovernedHarnessLaunchInput carries the resolved adapter and the exact
// claimed/running envelope generations needed to bind the launch guard and
// immutable execution posture.
type GovernedHarnessLaunchInput struct {
	HomePath          string
	Adapter           harness.Adapter
	ClaimedWork       envelope.WorkEnvelope
	RunningWork       envelope.WorkEnvelope
	RunInput          harness.RunInput
	ActionIntentID    string
	ReadinessEvidence []work.DelegateHarnessReadinessEvidence
	ReadinessRequest  *work.DelegateHarnessReadinessRequest
	Now               time.Time
}

// GovernedHarnessReservationInput is the post-preflight execution contract:
// one claimed work generation, one resolved adapter tuple, and one requested
// permission posture.
type GovernedHarnessReservationInput struct {
	HomePath    string
	Store       *work.Store
	ClaimedWork envelope.WorkEnvelope
	Adapter     harness.Adapter
	RunInput    harness.RunInput
	Actor       string
	Target      string
	Note        string
	Now         time.Time
}

// GovernedHarnessReservation is the exact running generation and bound launch
// input returned by ReserveGovernedHarnessLaunch.
type GovernedHarnessReservation struct {
	RunningWork    envelope.WorkEnvelope
	RunInput       harness.RunInput
	ActionIntentID string
}

// GovernedHarnessAdvanceExecution is the shared reserve/run/finalize result
// consumed by explicit and selector-driven governed execution commands.
type GovernedHarnessAdvanceExecution struct {
	RunningWork envelope.WorkEnvelope
	Output      harness.RunOutput
	RunError    error
	Finalized   GovernedHarnessFinalizationResult
}

// ReserveGovernedHarnessLaunch resolves the Claude posture, atomically claims
// the one-shot claimed->running execution generation, binds its ActionIntent,
// and installs the final start guard. No adapter subprocess is constructed or
// started before this returns.
func ReserveGovernedHarnessLaunch(in GovernedHarnessReservationInput) (GovernedHarnessReservation, error) {
	if in.Store == nil {
		return GovernedHarnessReservation{}, errors.New("governed harness reservation requires work store")
	}
	if in.Adapter == nil {
		return GovernedHarnessReservation{}, errors.New("governed harness reservation requires adapter")
	}
	if strings.TrimSpace(in.Target) != "running" {
		return GovernedHarnessReservation{}, fmt.Errorf("governed harness reservation target %q must be running", in.Target)
	}
	claimedPolicyDecisionID := strings.TrimSpace(in.ClaimedWork.PolicyDecisionID)
	var claimedActionIntentID string
	if n := len(in.ClaimedWork.ActionIntentIDs); n > 0 {
		claimedActionIntentID = strings.TrimSpace(in.ClaimedWork.ActionIntentIDs[n-1])
	}
	if claimedPolicyDecisionID == "" || claimedActionIntentID == "" {
		return GovernedHarnessReservation{}, fmt.Errorf("governed harness reservation requires an exact claimed policy/action generation")
	}
	runInput := in.RunInput
	_, claudeFamily := in.Adapter.(harness.GovernedPermissionAdapter)
	var postureEvidence []work.DelegateHarnessReadinessEvidence
	var postureReadinessRequest *work.DelegateHarnessReadinessRequest
	if claudeFamily {
		requestedMode := strings.TrimSpace(runInput.PermissionMode)
		if requestedMode != "plan" {
			runInput.PermissionMode, postureEvidence, postureReadinessRequest = ResolveGovernedClaudePermissionMode(
				in.HomePath,
				in.Store,
				in.ClaimedWork,
				runInput,
				requestedMode,
				in.Actor,
				in.Target,
				in.Now,
			)
		}
	}
	autonomyClass := ""
	if claudeFamily && ClaudeMutationPermissionMode(runInput.PermissionMode) {
		autonomyClass = "build-implement"
	}
	running, err := in.Store.Advance(in.ClaimedWork.ID, work.AdvanceInput{
		To:                       in.Target,
		Actor:                    in.Actor,
		ExpectedWorkState:        "claimed",
		ExpectedPolicyDecisionID: claimedPolicyDecisionID,
		ExpectedActionIntentID:   claimedActionIntentID,
		AutonomyClass:            autonomyClass,
		Note:                     in.Note,
	}, in.Now)
	if err != nil {
		return GovernedHarnessReservation{}, err
	}
	var actionIntentID string
	if n := len(running.ActionIntentIDs); n > 0 {
		actionIntentID = running.ActionIntentIDs[n-1]
	}
	runInput, err = PrepareGovernedHarnessLaunch(GovernedHarnessLaunchInput{
		HomePath:          in.HomePath,
		Adapter:           in.Adapter,
		ClaimedWork:       in.ClaimedWork,
		RunningWork:       running,
		RunInput:          runInput,
		ActionIntentID:    actionIntentID,
		ReadinessEvidence: postureEvidence,
		ReadinessRequest:  postureReadinessRequest,
		Now:               in.Now,
	})
	if err != nil {
		return GovernedHarnessReservation{}, err
	}
	return GovernedHarnessReservation{
		RunningWork:    running,
		RunInput:       runInput,
		ActionIntentID: actionIntentID,
	}, nil
}

// ExecuteGovernedHarnessAdvance runs the one mutation seam shared by explicit
// work advance --execute and selector-driven execution. Callers must complete
// all read-only adapter, workspace, roster, and preflight checks first.
func ExecuteGovernedHarnessAdvance(
	homePath string,
	store *work.Store,
	claimedWork envelope.WorkEnvelope,
	adapter harness.Adapter,
	runInput harness.RunInput,
	actor string,
	target string,
	note string,
	existingRunAttempts []string,
	now time.Time,
) (GovernedHarnessAdvanceExecution, error) {
	reserved, err := ReserveGovernedHarnessLaunch(GovernedHarnessReservationInput{
		HomePath: homePath, Store: store, ClaimedWork: claimedWork, Adapter: adapter,
		RunInput: runInput, Actor: actor, Target: target, Note: note, Now: now,
	})
	if err != nil {
		return GovernedHarnessAdvanceExecution{}, err
	}
	out, runErr := adapter.Run(context.Background(), reserved.RunInput)
	harnessName := strings.TrimSpace(runInput.Harness)
	if harnessName == "" {
		harnessName = adapter.Name()
	}
	finalized, err := FinalizeGovernedHarnessRun(GovernedHarnessFinalizationInput{
		Store:                 store,
		WorkID:                claimedWork.ID,
		Actor:                 actor,
		HarnessName:           harnessName,
		RunningPolicyID:       reserved.RunningWork.PolicyDecisionID,
		RunningActionIntentID: reserved.ActionIntentID,
		ExistingRunAttemptIDs: existingRunAttempts,
		Output:                out,
		RunError:              runErr,
		Now:                   time.Now().UTC(),
	})
	if err != nil {
		return GovernedHarnessAdvanceExecution{}, err
	}
	return GovernedHarnessAdvanceExecution{
		RunningWork: reserved.RunningWork,
		Output:      out,
		RunError:    runErr,
		Finalized:   finalized,
	}, nil
}

// PrepareGovernedHarnessLaunch binds the ActionIntent, exact reservation
// guard, Claude lane tuple, and immutable authority posture before Adapter.Run
// can construct or start the subprocess.
func PrepareGovernedHarnessLaunch(in GovernedHarnessLaunchInput) (harness.RunInput, error) {
	if in.Adapter == nil {
		return harness.RunInput{}, errors.New("governed harness launch requires adapter")
	}
	runInput := in.RunInput
	runInput.ActionIntentID = in.ActionIntentID
	runInput.StartGuard = NewGovernedExecutionReservationStartGuard(
		in.HomePath,
		in.RunningWork.ID,
		in.RunningWork.PolicyDecisionID,
		in.ActionIntentID,
	)
	if _, claudeFamily := in.Adapter.(harness.GovernedPermissionAdapter); !claudeFamily {
		return runInput, nil
	}
	readinessRequest := in.ReadinessRequest
	if readinessRequest == nil {
		request := GovernedClaudeReadinessRequestForRun(in.HomePath, in.ClaimedWork, runInput, in.Now)
		readinessRequest = &request
	}
	runInput.StartGuard = NewGovernedClaudeLaneStartGuard(
		in.HomePath,
		in.Adapter.Name(),
		*readinessRequest,
		in.RunningWork.PolicyDecisionID,
		in.ActionIntentID,
	)
	authorities := make([]runattempt.ExecutionPostureEvidence, 0, len(in.ReadinessEvidence))
	for _, item := range in.ReadinessEvidence {
		authorities = append(authorities, runattempt.ExecutionPostureEvidence{
			Kind: item.Kind, Ref: item.Ref, Revision: item.Revision,
		})
	}
	runInput.ExecutionPosture = &runattempt.ExecutionPosture{
		PermissionMode:    runInput.PermissionMode,
		PolicyDecisionID:  in.RunningWork.PolicyDecisionID,
		AuthorityEvidence: authorities,
	}
	if ClaudeMutationPermissionMode(runInput.PermissionMode) {
		runInput.StartGuard = NewGovernedClaudeStartGuard(
			in.HomePath,
			in.Adapter.Name(),
			*readinessRequest,
			in.ReadinessEvidence,
			in.RunningWork.PolicyDecisionID,
			in.ActionIntentID,
		)
	}
	return runInput, nil
}

// FinalizeGovernedHarnessRun advances or closes only the exact running
// reservation that launched the adapter. A newer lifecycle generation wins;
// the refused attempt remains reverse-linked by work+intent without mutating
// that newer WorkEnvelope generation.
func FinalizeGovernedHarnessRun(in GovernedHarnessFinalizationInput) (GovernedHarnessFinalizationResult, error) {
	if in.Store == nil {
		return GovernedHarnessFinalizationResult{}, errors.New("governed harness finalization requires work store")
	}
	now := in.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	allRunAttempts := append([]string(nil), in.ExistingRunAttemptIDs...)
	if in.Output.RunAttempt.ID != "" {
		allRunAttempts = append(allRunAttempts, in.Output.RunAttempt.ID)
	}

	result := GovernedHarnessFinalizationResult{
		Succeeded: in.RunError == nil && in.Output.RunAttempt.Status == runattempt.StatusSucceeded,
	}
	var reservationErr error
	if result.Succeeded {
		advanced, err := in.Store.Advance(in.WorkID, work.AdvanceInput{
			To:                       "verifying",
			Actor:                    in.Actor,
			ExpectedWorkState:        "running",
			ExpectedPolicyDecisionID: in.RunningPolicyID,
			ExpectedActionIntentID:   in.RunningActionIntentID,
			RunAttemptIDs:            allRunAttempts,
			VerificationID:           in.Output.TranscriptPath,
			Note:                     "harness " + in.HarnessName + " " + in.Output.RunAttempt.Status,
		}, now)
		if errors.Is(err, work.ErrExecutionReservationChanged) {
			reservationErr = err
		} else if err != nil {
			return GovernedHarnessFinalizationResult{}, err
		} else {
			result.WorkID = advanced.ID
			result.FinalState = advanced.State
		}
	} else {
		closeIn := work.CloseInput{
			Outcome:                  "failed",
			MoveType:                 "audit_only",
			Actor:                    in.Actor,
			ExpectedWorkState:        "running",
			ExpectedPolicyDecisionID: in.RunningPolicyID,
			ExpectedActionIntentID:   in.RunningActionIntentID,
			Summary:                  "harness " + in.HarnessName + " " + in.Output.RunAttempt.Status,
			Changed:                  true,
			RunAttemptIDs:            allRunAttempts,
		}
		if in.Output.TranscriptPath != "" {
			closeIn.ProofRefs = []string{in.Output.TranscriptPath}
		}
		if _, err := in.Store.Close(in.WorkID, closeIn, now); errors.Is(err, work.ErrExecutionReservationChanged) {
			reservationErr = err
		} else if err != nil {
			return GovernedHarnessFinalizationResult{}, err
		} else {
			result.WorkID = in.WorkID
			result.FinalState = "failed"
			result.FailedAuditOnly = true
		}
	}
	if reservationErr != nil {
		result.Succeeded = false
		result.Warnings = append(result.Warnings, fmt.Sprintf("%v; preserving the newer work lifecycle state", reservationErr))
		result.Warnings = append(result.Warnings, ReportRevokedExecutionAttempt(
			in.Store,
			in.WorkID,
			in.Output.RunAttempt.ID,
		)...)
	}
	return result, nil
}

// ReportRevokedExecutionAttempt surfaces the explicit reverse-link-only race
// exception. It deliberately does not touch the WorkEnvelope: even a
// same-state Advance mints a new policy/action generation and would revoke a
// newer running reservation while trying to preserve it.
func ReportRevokedExecutionAttempt(store *work.Store, workID, executionAttemptID string) []string {
	if store == nil || strings.TrimSpace(executionAttemptID) == "" {
		return nil
	}
	current, err := store.Get(workID)
	if err != nil {
		return []string{fmt.Sprintf("report revoked execution attempt %s: read work: %v", executionAttemptID, err)}
	}
	if current.State == "completed" || current.State == "failed" || current.State == "cancelled" {
		return []string{fmt.Sprintf(
			"execution attempt %s remains reverse-linked only: work %s became terminal (%s), terminal receipt/work hashes are immutable, and the newer lifecycle generation is preserved",
			executionAttemptID,
			workID,
			current.State,
		)}
	}
	return []string{fmt.Sprintf(
		"execution attempt %s remains reverse-linked only: work %s moved to a newer lifecycle generation (%s); preserving its state, policy, and action identity",
		executionAttemptID,
		workID,
		current.State,
	)}
}

// ResolveGovernedClaudePermissionMode resolves every non-plan Claude-family
// posture. Plan is the fail-closed result. An omitted mode selects bypass; an
// explicit mutation-capable mode is preserved only for claimed work whose
// prospective running advance is policy-allowed, whose autonomy halt is clear,
// and whose blocking authorities are currently attested.
func ResolveGovernedClaudePermissionMode(
	homePath string,
	store *work.Store,
	wenv envelope.WorkEnvelope,
	runInput harness.RunInput,
	requestedMode string,
	actor string,
	target string,
	now time.Time,
) (string, []work.DelegateHarnessReadinessEvidence, *work.DelegateHarnessReadinessRequest) {
	const fallback = "plan"
	resolvedMode := strings.TrimSpace(requestedMode)
	if resolvedMode == "" {
		resolvedMode = "bypass"
	}
	if store == nil || wenv.State != "claimed" {
		return fallback, nil, nil
	}
	plan, err := store.AdvancePlan(wenv.ID, now, work.AdvancePlanOptions{Actor: actor, To: target})
	if err != nil || plan.Status != "advance_policy_allowed" || !plan.Policy.Allowed || plan.NoAction != "" {
		return fallback, nil, nil
	}
	halted, _, err := policy.AutonomyHalted(homePath, "build-implement")
	if err != nil || halted {
		return fallback, nil, nil
	}
	readinessGuard := work.DelegateHarnessReadiness
	if readinessGuard == nil {
		return fallback, nil, nil
	}
	readinessRequest := GovernedClaudeReadinessRequestForRun(homePath, wenv, runInput, now)
	readiness, err := readinessGuard(readinessRequest)
	if err != nil || !readiness.Ready {
		return fallback, nil, nil
	}
	selected, ok := selectGovernedClaudeReadinessEvidence(readinessRequest.Harness, readiness.Evidence)
	if !ok {
		return fallback, nil, nil
	}
	return resolvedMode, selected, &readinessRequest
}

// GovernedClaudeReadinessRequestForRun binds readiness to the exact resolved
// lane tuple and activation command that will cross cmd.Start.
func GovernedClaudeReadinessRequestForRun(homePath string, wenv envelope.WorkEnvelope, runInput harness.RunInput, now time.Time) work.DelegateHarnessReadinessRequest {
	activationArgs := append([]string(nil), runInput.Args...)
	if strings.TrimSpace(runInput.Prompt) != "" {
		activationArgs = []string{runInput.Prompt}
	}
	return work.DelegateHarnessReadinessRequest{
		HomePath:               homePath,
		WorkID:                 wenv.ID,
		ContextPackID:          wenv.IDs.ContextPackID,
		Workspace:              runInput.Workspace,
		Harness:                normalizeDelegateReadinessHarness(runInput.Harness),
		LaneID:                 runInput.LaneID,
		RequestedModelID:       runInput.RequestedModelID,
		RequestedProviderRoute: runInput.RequestedProviderRoute,
		Command:                runInput.Command,
		Args:                   activationArgs,
		Now:                    now,
	}
}

// ClaudeMutationPermissionMode reports modes that can mutate without a
// per-tool prompt and therefore require the full authority tuple.
func ClaudeMutationPermissionMode(mode string) bool {
	switch strings.TrimSpace(mode) {
	case "default", "acceptEdits", "bypass":
		return true
	default:
		return false
	}
}

// NewGovernedClaudeLaneStartGuard revalidates every Claude-family lane tuple
// and exact running reservation immediately around cmd.Start.
func NewGovernedClaudeLaneStartGuard(
	homePath string,
	adapterName string,
	readinessRequest work.DelegateHarnessReadinessRequest,
	expectedPolicyDecisionID string,
	expectedActionIntentID string,
) func(context.Context, func() error) error {
	return func(ctx context.Context, start func() error) error {
		if start == nil {
			return fmt.Errorf("governed Claude start operation required")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return homepkg.WithAutonomyAdmissionLock(homePath, func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := revalidateGovernedClaudeLane(ctx, adapterName, readinessRequest); err != nil {
				return err
			}
			if err := RevalidateGovernedExecutionReservation(
				homePath,
				readinessRequest.WorkID,
				expectedPolicyDecisionID,
				expectedActionIntentID,
			); err != nil {
				return err
			}
			return start()
		})
	}
}

// NewGovernedClaudeStartGuard adds halt and exact blocking-authority
// revalidation for mutation-capable Claude-family modes.
func NewGovernedClaudeStartGuard(
	homePath string,
	adapterName string,
	readinessRequest work.DelegateHarnessReadinessRequest,
	expected []work.DelegateHarnessReadinessEvidence,
	expectedPolicyDecisionID string,
	expectedActionIntentID string,
) func(context.Context, func() error) error {
	expected = append([]work.DelegateHarnessReadinessEvidence(nil), expected...)
	return func(ctx context.Context, start func() error) error {
		if start == nil {
			return fmt.Errorf("governed Claude start operation required")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// Permission publication takes the permission lock before it enters
		// work's autonomy-admission lock. Keep the same order here.
		return harness.WithPermissionPostureLock(homePath, func() error {
			return homepkg.WithAutonomyAdmissionLock(homePath, func() error {
				halted, reason, err := policy.AutonomyHalted(homePath, "build-implement")
				if err != nil {
					return fmt.Errorf("revalidate autonomy halt before launch: %w", err)
				}
				if halted {
					return fmt.Errorf("autonomy halted before launch: %s", reason)
				}
				if err := revalidateGovernedClaudeLane(ctx, adapterName, readinessRequest); err != nil {
					return err
				}
				readinessGuard := work.DelegateHarnessReadiness
				if readinessGuard == nil {
					return fmt.Errorf("governed Claude readiness unavailable before launch")
				}
				readinessRequest.Now = time.Now().UTC()
				readiness, err := readinessGuard(readinessRequest)
				if err != nil {
					return fmt.Errorf("revalidate governed Claude readiness before launch: %w", err)
				}
				if !readiness.Ready {
					return fmt.Errorf("governed Claude readiness became stale before launch: %s", strings.Join(readiness.Details, "; "))
				}
				observed, ok := selectGovernedClaudeReadinessEvidence(readinessRequest.Harness, readiness.Evidence)
				if !ok || !sameGovernedClaudeReadinessEvidence(expected, observed) {
					return fmt.Errorf("governed Claude authority revisions changed before launch")
				}
				if err := RevalidateGovernedExecutionReservation(
					homePath,
					readinessRequest.WorkID,
					expectedPolicyDecisionID,
					expectedActionIntentID,
				); err != nil {
					return err
				}
				return start()
			})
		})
	}
}

// NewGovernedExecutionReservationStartGuard protects every adapter, including
// non-Claude harnesses, from starting after its running generation changed.
func NewGovernedExecutionReservationStartGuard(
	homePath string,
	workID string,
	expectedPolicyDecisionID string,
	expectedActionIntentID string,
) func(context.Context, func() error) error {
	return func(ctx context.Context, start func() error) error {
		if start == nil {
			return fmt.Errorf("governed start operation required")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return homepkg.WithAutonomyAdmissionLock(homePath, func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := RevalidateGovernedExecutionReservation(homePath, workID, expectedPolicyDecisionID, expectedActionIntentID); err != nil {
				return err
			}
			return start()
		})
	}
}

// RevalidateGovernedExecutionReservation checks the exact running
// state/policy/action generation under the caller's admission lock.
func RevalidateGovernedExecutionReservation(homePath, workID, expectedPolicyDecisionID, expectedActionIntentID string) error {
	workID = strings.TrimSpace(workID)
	expectedPolicyDecisionID = strings.TrimSpace(expectedPolicyDecisionID)
	expectedActionIntentID = strings.TrimSpace(expectedActionIntentID)
	if workID == "" || expectedPolicyDecisionID == "" || expectedActionIntentID == "" {
		return fmt.Errorf("governed execution reservation identity is incomplete")
	}
	store, err := work.OpenReadOnly(homePath)
	if err != nil {
		return fmt.Errorf("revalidate governed execution reservation: %w", err)
	}
	current, err := store.Get(workID)
	if err != nil {
		return fmt.Errorf("revalidate governed execution reservation: %w", err)
	}
	var currentActionIntentID string
	if n := len(current.ActionIntentIDs); n > 0 {
		currentActionIntentID = current.ActionIntentIDs[n-1]
	}
	if current.State != "running" || current.PolicyDecisionID != expectedPolicyDecisionID || currentActionIntentID != expectedActionIntentID {
		return fmt.Errorf(
			"governed execution reservation changed before launch: state=%s policy=%s action_intent=%s",
			current.State,
			current.PolicyDecisionID,
			currentActionIntentID,
		)
	}
	return nil
}

func revalidateGovernedClaudeLane(ctx context.Context, adapterName string, expected work.DelegateHarnessReadinessRequest) error {
	registry := harness.NewAdapterRegistry(runattempt.Open(expected.HomePath))
	adapter, ok := registry.AdapterByName(strings.TrimSpace(adapterName))
	if !ok {
		return fmt.Errorf("governed Claude lane adapter %q disappeared before launch", expected.Harness)
	}
	revalidator, ok := adapter.(harness.GovernedPermissionAdapter)
	if !ok {
		return fmt.Errorf("governed Claude lane adapter %q cannot revalidate launch authority", expected.Harness)
	}
	resolved, err := revalidator.RevalidateGovernedRun(ctx, harness.RunInput{
		WorkID:                 expected.WorkID,
		Harness:                strings.TrimSpace(adapterName),
		Workspace:              expected.Workspace,
		Command:                expected.Command,
		Args:                   append([]string(nil), expected.Args...),
		PermissionMode:         "plan",
		LaneID:                 expected.LaneID,
		RequestedModelID:       expected.RequestedModelID,
		RequestedProviderRoute: expected.RequestedProviderRoute,
		Now:                    time.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("revalidate governed Claude lane before launch: %w", err)
	}
	if normalizeDelegateReadinessHarness(resolved.Harness) != normalizeDelegateReadinessHarness(expected.Harness) ||
		resolved.Command != expected.Command ||
		resolved.LaneID != expected.LaneID ||
		resolved.RequestedModelID != expected.RequestedModelID ||
		resolved.RequestedProviderRoute != expected.RequestedProviderRoute {
		return fmt.Errorf("governed Claude lane tuple changed before launch")
	}
	return nil
}

func sameGovernedClaudeReadinessEvidence(a, b []work.DelegateHarnessReadinessEvidence) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func selectGovernedClaudeReadinessEvidence(harnessName string, evidence []work.DelegateHarnessReadinessEvidence) ([]work.DelegateHarnessReadinessEvidence, bool) {
	harnessName = normalizeDelegateReadinessHarness(harnessName)
	if harnessName == "" {
		return nil, false
	}
	required := make(map[string]bool, len(governedClaudeReadinessEvidenceKinds))
	for _, kind := range governedClaudeReadinessEvidenceKinds {
		required[kind] = true
	}
	selected := make(map[string]work.DelegateHarnessReadinessEvidence, len(required))
	for _, item := range evidence {
		if !required[item.Kind] {
			continue
		}
		if strings.TrimSpace(item.Ref) == "" || strings.TrimSpace(item.Revision) == "" ||
			!GovernedClaudeEvidenceRefMatchesHarness(harnessName, item.Kind, item.Ref) {
			return nil, false
		}
		if _, duplicate := selected[item.Kind]; duplicate {
			return nil, false
		}
		selected[item.Kind] = work.DelegateHarnessReadinessEvidence{
			Kind: strings.TrimSpace(item.Kind), Ref: strings.TrimSpace(item.Ref), Revision: strings.TrimSpace(item.Revision),
		}
	}
	out := make([]work.DelegateHarnessReadinessEvidence, 0, len(required))
	for _, kind := range governedClaudeReadinessEvidenceKinds {
		item, ok := selected[kind]
		if !ok {
			return nil, false
		}
		out = append(out, item)
	}
	return out, true
}

// GovernedClaudeEvidenceRefMatchesHarness accepts only the named harness's
// canonical permission/posture/install refs and either a bare PreToolUse ref
// or the exact canonical matcher digest.
func GovernedClaudeEvidenceRefMatchesHarness(harnessName, kind, ref string) bool {
	harnessName = normalizeDelegateReadinessHarness(harnessName)
	ref = strings.TrimSpace(ref)
	switch strings.TrimSpace(kind) {
	case "permission_authority":
		return ref == harness.PermissionAuthorityRef(harnessName)
	case "permission_posture":
		return ref == "harness-posture://"+harnessName
	case "blocking_hook_plan":
		base := "hook://" + harnessName + "/PreToolUse"
		if ref == base {
			return true
		}
		matcherRevision := strings.TrimPrefix(ref, base+"?matcher=")
		if matcherRevision == ref {
			return false
		}
		digest := sha256.Sum256([]byte(hookplan.HarnessMatcher(harnessName)))
		return matcherRevision == hex.EncodeToString(digest[:])[:12]
	case "blocking_hook_installation":
		return ref == "harness-installation://live"
	default:
		return false
	}
}

func normalizeDelegateReadinessHarness(value string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(value), "-", "_")
	if normalized == "claude" {
		return "claude_code"
	}
	return normalized
}
