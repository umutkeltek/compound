package routeevidence

import (
	"encoding/json"
	"testing"

	"compound/internal/work"
)

func TestResolveDelegateExecutionProfileUsesCanonicalOwnerAndLosslessSnapshot(t *testing.T) {
	resolved, err := resolveDelegateExecutionProfile(work.DelegateExecutionProfileResolveRequest{
		Intent: work.DelegateExecutionProfileIntent{
			ProfileID:       "coding-implementation",
			ClientHarnessID: "codex",
			TransportID:     "switchback",
			RouteAlias:      "auto/build",
			ModelConstraint: "fixture/model",
			SemanticEffort:  "deep",
			NativeEffort:    "xhigh",
			PrivacyCeiling:  "private",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID == "" || resolved.ProfileID != "coding-implementation" ||
		resolved.ClientHarnessID != "codex" || resolved.TransportID != "switchback" ||
		resolved.RequestedNativeEffort != "xhigh" {
		t.Fatalf("resolver lost canonical plan fields: %+v", resolved)
	}
	var canonical ExecutionProfilePlan
	if err := json.Unmarshal(resolved.Snapshot, &canonical); err != nil {
		t.Fatalf("lossless snapshot is not canonical plan JSON: %v", err)
	}
	if canonical.ID != resolved.ID || canonical.NativeEffort.Requested != resolved.RequestedNativeEffort {
		t.Fatalf("snapshot and DTO diverged: canonical=%+v dto=%+v", canonical, resolved)
	}
}
