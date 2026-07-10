package main

import (
	"fmt"
	"time"

	"compound/internal/capgraph"
	"compound/internal/routeevidence"
)

const capabilityResolutionSchema = "compound.capability_resolution.v0.1"

type capabilityResolutionReport struct {
	SchemaVersion     string                            `json:"schema_version"`
	Status            string                            `json:"status"`
	Task              string                            `json:"task"`
	Harness           string                            `json:"harness,omitempty"`
	PrivacySensitive  bool                              `json:"privacy_sensitive"`
	Classification    routeevidence.TaskClassification  `json:"classification"`
	CapabilitySurface capgraph.SurfaceReport            `json:"capability_surface"`
	RoutePlan         routeevidence.CapabilityRoutePlan `json:"route_plan"`
	AuthorityRefs     []string                          `json:"authority_refs"`
}

func buildCapabilityResolutionReport(store *capgraph.Store, homePath, task, harness string, privacySensitive bool, now time.Time) (capabilityResolutionReport, error) {
	classification := routeevidence.ClassifyCapabilityTask(task)
	surface, err := buildCapabilitySurfaceReport(store, homePath, task, harness)
	if err != nil {
		return capabilityResolutionReport{}, err
	}
	surface = surface.LimitOffers(12)
	plan, err := routeevidence.PlanCapabilityRoute(homePath, classification.CapabilityClass, routeevidence.CapabilityRoutePlanOptions{
		PrivacySensitive: privacySensitive,
	}, now)
	if err != nil {
		return capabilityResolutionReport{}, err
	}
	if len(plan.Chain) == 0 {
		return capabilityResolutionReport{}, fmt.Errorf("model-router authority returned no eligible lane for capability class %q", classification.CapabilityClass)
	}
	return capabilityResolutionReport{
		SchemaVersion:     capabilityResolutionSchema,
		Status:            "ok",
		Task:              surface.Task,
		Harness:           surface.Harness,
		PrivacySensitive:  privacySensitive,
		Classification:    classification,
		CapabilitySurface: surface,
		RoutePlan:         plan,
		AuthorityRefs: []string{
			capgraph.CapabilityGraphAuthorityRef,
			routeevidence.ModelCapabilitiesAuthorityRef,
		},
	}, nil
}

func printCapabilityResolution(report capabilityResolutionReport) {
	fmt.Printf("# capability resolution for %q (offered, NOT executed)\n", report.Task)
	fmt.Printf("- status: %s\n", report.Status)
	fmt.Printf("- capability_class: %s\n", report.Classification.CapabilityClass)
	fmt.Printf("- classification_rule: %s\n", report.Classification.RuleID)
	fmt.Printf("- deterministic_first: %s\n", report.Classification.DeterministicFirst)
	fmt.Printf("- evaluation_method: %s\n", report.Classification.EvaluationMethod)
	fmt.Printf("- capability_authority: %s\n", report.CapabilitySurface.AuthorityRef)
	fmt.Printf("- model_router_authority: %s source=%s revision=%s\n", report.RoutePlan.AuthorityRef, report.RoutePlan.SourceAuthorityID, report.RoutePlan.SourceRevision)
	fmt.Printf("- offers: %d shown=%d omitted=%d\n", report.CapabilitySurface.OfferCount, len(report.CapabilitySurface.Offers), report.CapabilitySurface.OmittedOfferCount)
	for _, offer := range report.CapabilitySurface.Offers {
		fmt.Printf("  - %s kind=%s score=%.2f reason=%s\n", offer.Node.ID, offer.Node.Kind, offer.Score, offer.Why)
	}
	fmt.Printf("- route_lanes: %d\n", len(report.RoutePlan.Chain))
	for _, lane := range report.RoutePlan.Chain {
		fmt.Printf("  - %d %s tier=%s role=%s claim=%s\n", lane.Position, lane.ModelID, lane.Tier, lane.GateRole, lane.ClaimLevel)
	}
	fmt.Println("- verdict: projection_only_not_task_success")
}
