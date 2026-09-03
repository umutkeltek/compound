package main

// Composition root for the R3A unattended execute stage. internal/work owns
// the seam and its fail-closed defaults; this file binds the package-level
// hooks to the real resolvers at startup, the same pattern
// work_delegate_readiness.go uses for the delegate readiness guard. Keeping
// the wiring here avoids an internal/work -> internal/routeevidence import
// cycle (routeevidence already imports work).

import (
	"strings"

	"compound/internal/harness"
	"compound/internal/routeevidence"
	autonomyproof "compound/internal/routeevidence/autonomy"
	"compound/internal/runattempt"
	"compound/internal/work"
)

func init() {
	work.SwapExecuteStageRoutePlan(executeStageRouteFromManifest)
}

// executeStageRouteFromManifest resolves the first eligible lane for the
// capability from the home's model-capabilities authority (the route plan the
// recon named: routeevidence.LoadModelCapabilitiesSnapshot). Fail-closed on
// every input: a missing or invalid authority, no routing_defaults entry for
// the capability, a claim whose computed gate role is not "eligible", an entry
// with no harness, or a harness the adapter registry has no executor for all
// return no route.
func executeStageRouteFromManifest(homePath, capability string) (string, bool) {
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
		if harnessName == "" || !executeStageAdapterKnown(homePath, harnessName) {
			continue
		}
		return harnessName, true
	}
	return "", false
}

// executeStageAdapterKnown reports whether the harness adapter registry has
// an executor for harnessName. A var so tests can pin the registry lookup
// without building a real one.
var executeStageAdapterKnown = func(homePath, harnessName string) bool {
	registry := harness.NewAdapterRegistry(runattempt.Open(homePath))
	_, ok := registry.AdapterByName(harnessName)
	return ok
}
