package director

import (
	"testing"
	"time"

	"compound/internal/policy"
	"compound/internal/work"
)

// TestKillSwitchHaltsDirectorAutoAdmit wires W9-J4 to a REAL director proposal: a
// review-debt work the director actually proposed cannot be autonomously admitted while
// the policy-owned halt is engaged, and can once the operator clears it. Manual
// materialization of the create-plan (proposed) is unaffected either way.
func TestKillSwitchHaltsDirectorAutoAdmit(t *testing.T) {
	home := seededDirectorHome(t)
	seedReviewDebt(t, home, "synthetic-review-debt", "review-debt-01")

	report, err := Propose(home, Options{Apply: true, Now: directorTestNow})
	if err != nil {
		t.Fatal(err)
	}
	if report.ProposalCount != 1 {
		t.Fatalf("expected one director proposal, got %+v", report)
	}

	store, err := work.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	materialized, err := store.AdmitCreatePlanPath(report.Proposals[0].PlanPath, directorTestNow.Add(time.Minute), work.AdmitCreatePlanOptions{Actor: "operator-test"})
	if err != nil {
		t.Fatal(err)
	}
	if materialized.WorkState != "proposed" {
		t.Fatalf("materialized director work should be proposed, got %q", materialized.WorkState)
	}
	grant := work.AutoAdmitInput{By: "director", EligibilityReason: "clean-window: 5 clean runs"}

	// Halt engaged: autonomous admission of the real proposal is denied; it stays proposed.
	if err := policy.WriteAutonomyHalt(home, policy.AutonomyHalt{Global: true, Reason: "incident review"}); err != nil {
		t.Fatalf("engage halt: %v", err)
	}
	halted, err := store.AdmitAuto(materialized.WorkID, grant, directorTestNow.Add(2*time.Minute))
	if err == nil || !halted.Halted || halted.Admitted {
		t.Fatalf("global halt must deny director auto-admit: err=%v res=%+v", err, halted)
	}
	if got, _ := store.Get(materialized.WorkID); got.State != "proposed" {
		t.Fatalf("halted director work must stay proposed, got %q", got.State)
	}

	// Halt cleared: the same eligible auto-admit now proceeds to admitted.
	if err := policy.WriteAutonomyHalt(home, policy.AutonomyHalt{}); err != nil {
		t.Fatalf("clear halt: %v", err)
	}
	admitted, err := store.AdmitAuto(materialized.WorkID, grant, directorTestNow.Add(3*time.Minute))
	if err != nil || !admitted.Admitted {
		t.Fatalf("cleared halt must let director auto-admit proceed: err=%v res=%+v", err, admitted)
	}
	if got, _ := store.Get(materialized.WorkID); got.State != "admitted" {
		t.Fatalf("director work should be admitted after clearing halt, got %q", got.State)
	}
}
