package routeevidence

import (
	"encoding/json"
	"fmt"

	"compound/internal/work"
)

func resolveDelegateExecutionProfile(req work.DelegateExecutionProfileResolveRequest) (*work.DelegateExecutionProfileResolution, error) {
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
	disposition := work.DelegateExecutionProfileIncompatible
	if normalizeModelCapabilitySelector(req.SelectedHarness) == normalizeModelCapabilitySelector(plan.ClientHarnessID) {
		disposition = work.DelegateExecutionProfileCompatible
	}
	return &work.DelegateExecutionProfileResolution{
		Snapshot: snapshot,
		Compatibility: work.DelegateExecutionProfileCompatibility{
			Disposition:           disposition,
			SelectedHarness:       req.SelectedHarness,
			SelectedLaneID:        req.SelectedLaneID,
			PlanID:                plan.ID,
			ClientHarnessID:       plan.ClientHarnessID,
			TransportID:           plan.TransportID,
			RequestedNativeEffort: plan.NativeEffort.Requested,
		},
	}, nil
}

func init() {
	work.DelegateExecutionProfileResolver = resolveDelegateExecutionProfile
}
