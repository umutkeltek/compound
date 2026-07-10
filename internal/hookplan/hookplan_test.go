package hookplan

import (
	"testing"

	"compound/internal/harness"
)

func TestReconcileObservedExtraNeverBecomesDesired(t *testing.T) {
	plan := HookPlan{
		SchemaVersion: HookPlanSchemaVersion,
		ID:            "hook-plan",
		Revision:      "sha256:plan",
		Bindings: []Binding{
			{
				ID:         "hook://claude_code/SessionStart",
				Harness:    "claude_code",
				Event:      "SessionStart",
				HandlerRef: "$HOME/.claude/hooks/RecallContext.hook.ts",
			},
		},
	}
	observed := InstallationSnapshot{
		SchemaVersion: InstallationSnapshotSchemaVersion,
		Ref:           "harness-installation://live",
		Revision:      "sha256:observed",
		Bindings: []ObservedBinding{
			{
				Harness:    "claude_code",
				Event:      "SessionStart",
				HandlerRef: "$HOME/.claude/hooks/RecallContext.hook.ts",
				Active:     true,
			},
			{
				Harness:    "claude_code",
				Event:      "SessionEnd",
				HandlerRef: "$HOME/.claude/hooks/SessionReceipt.hook.ts",
				Active:     true,
			},
		},
	}

	got, err := Reconcile(plan, observed, harness.DefaultRegistry())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(plan.Bindings) != 1 || plan.Bindings[0].Event != "SessionStart" {
		t.Fatalf("observed bindings changed desired plan: %+v", plan.Bindings)
	}
	if len(got.Deltas) != 2 {
		t.Fatalf("Reconcile() deltas = %+v, want ready + extra_unowned", got.Deltas)
	}
	if got.Deltas[0].State != StateReady || got.Deltas[0].Event != "SessionStart" {
		t.Fatalf("first delta = %+v, want SessionStart ready", got.Deltas[0])
	}
	if got.Deltas[1].State != StateExtraUnowned || got.Deltas[1].Event != "SessionEnd" {
		t.Fatalf("second delta = %+v, want SessionEnd extra_unowned", got.Deltas[1])
	}
}
