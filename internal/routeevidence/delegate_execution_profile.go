package routeevidence

import (
	"encoding/json"
	"fmt"

	"compound/internal/work"
)

func resolveDelegateExecutionProfile(req work.DelegateExecutionProfileResolveRequest) (*work.DelegateExecutionProfilePlan, error) {
	plan, err := ResolveExecutionProfile(ExecutionProfileIntent{
		ProfileID:           req.Intent.ProfileID,
		Role:                req.Intent.Role,
		CapabilityClass:     req.Intent.CapabilityClass,
		ClientHarnessID:     req.Intent.ClientHarnessID,
		TransportID:         req.Intent.TransportID,
		RouteAlias:          req.Intent.RouteAlias,
		ModelConstraint:     req.Intent.ModelConstraint,
		SemanticEffort:      req.Intent.SemanticEffort,
		NativeEffort:        req.Intent.NativeEffort,
		OrchestrationPolicy: req.Intent.OrchestrationPolicy,
		PrivacyCeiling:      req.Intent.PrivacyCeiling,
	})
	if err != nil {
		return nil, err
	}
	snapshot, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("marshal execution profile plan snapshot: %w", err)
	}
	budget, err := json.Marshal(plan.Budget)
	if err != nil {
		return nil, fmt.Errorf("marshal execution profile budget: %w", err)
	}
	checkpoint, err := json.Marshal(plan.Checkpoint)
	if err != nil {
		return nil, fmt.Errorf("marshal execution profile checkpoint: %w", err)
	}
	var remainingBudget json.RawMessage
	if plan.RemainingBudget != nil {
		remainingBudget, err = json.Marshal(plan.RemainingBudget)
		if err != nil {
			return nil, fmt.Errorf("marshal execution profile remaining budget: %w", err)
		}
	}
	return &work.DelegateExecutionProfilePlan{
		SchemaVersion:           plan.SchemaVersion,
		ID:                      plan.ID,
		ProfileID:               plan.ProfileID,
		Role:                    plan.Role,
		CapabilityClass:         plan.CapabilityClass,
		ClientHarnessID:         plan.ClientHarnessID,
		TransportID:             plan.TransportID,
		RouteAlias:              plan.RouteAlias,
		ModelConstraint:         plan.ModelConstraint,
		SemanticEffort:          plan.SemanticEffort,
		RequestedNativeEffort:   plan.NativeEffort.Requested,
		NativeEffortDisposition: plan.NativeEffort.Disposition,
		NativeEffortReason:      plan.NativeEffort.Reason,
		OrchestrationPolicy:     plan.OrchestrationPolicy,
		PrivacyCeiling:          plan.PrivacyCeiling,
		Budget:                  budget,
		Checkpoint:              checkpoint,
		ParentPlanID:            plan.ParentPlanID,
		RemainingBudget:         remainingBudget,
		InheritedCeilings:       append([]string(nil), plan.InheritedCeilings...),
		Warnings:                append([]string(nil), plan.Warnings...),
		Snapshot:                snapshot,
	}, nil
}

func init() {
	work.DelegateExecutionProfileResolver = resolveDelegateExecutionProfile
}
