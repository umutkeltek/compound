package routeevidence

import (
	"encoding/json"
	"testing"

	"compound/internal/work"
)

func TestResolveDelegateExecutionProfileUsesCanonicalOwnerAndLosslessSnapshot(t *testing.T) {
	resolved, err := resolveDelegateExecutionProfile(work.DelegateExecutionProfileResolveRequest{
		SelectedHarness: "codex",
		SelectedLaneID:  "codex",
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
	if resolved.Compatibility.Disposition != work.DelegateExecutionProfileCompatible ||
		resolved.Compatibility.SelectedHarness != "codex" ||
		resolved.Compatibility.SelectedLaneID != "codex" {
		t.Fatalf("resolver lost owner compatibility attestation: %+v", resolved)
	}
	var canonical ExecutionProfilePlan
	if err := json.Unmarshal(resolved.Snapshot, &canonical); err != nil {
		t.Fatalf("lossless snapshot is not canonical plan JSON: %v", err)
	}
	if canonical.ID != resolved.Compatibility.PlanID ||
		canonical.ClientHarnessID != resolved.Compatibility.ClientHarnessID ||
		canonical.TransportID != resolved.Compatibility.TransportID ||
		canonical.NativeEffort.Requested != resolved.Compatibility.RequestedNativeEffort {
		t.Fatalf("snapshot and attestation diverged: canonical=%+v attestation=%+v", canonical, resolved.Compatibility)
	}
}

func TestResolveDelegateExecutionProfileAttestsOnlyExactRegisteredHarnessFamilies(t *testing.T) {
	for _, tc := range []struct {
		name          string
		selected      string
		clientHarness string
		want          string
	}{
		{name: "registered Claude family", selected: "claude-code", clientHarness: "claude-code", want: work.DelegateExecutionProfileCompatible},
		{name: "registered Codex family", selected: "codex", clientHarness: "codex", want: work.DelegateExecutionProfileCompatible},
		{name: "unknown Claude lookalike", selected: "claude-code-preview", clientHarness: "claude-code", want: work.DelegateExecutionProfileIncompatible},
		{name: "unknown Codex lookalike", selected: "codex-preview", clientHarness: "codex", want: work.DelegateExecutionProfileIncompatible},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolved, err := resolveDelegateExecutionProfile(work.DelegateExecutionProfileResolveRequest{
				SelectedHarness: tc.selected,
				SelectedLaneID:  tc.selected,
				Intent: work.DelegateExecutionProfileIntent{
					ProfileID:       "coding-implementation",
					ClientHarnessID: tc.clientHarness,
					TransportID:     "switchback",
					SemanticEffort:  "deep",
					NativeEffort:    "xhigh",
					PrivacyCeiling:  "private",
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Compatibility.Disposition != tc.want {
				t.Fatalf("compatibility for %q = %+v, want %q", tc.selected, resolved.Compatibility, tc.want)
			}
		})
	}
}
