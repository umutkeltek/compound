package director

import (
	"testing"
	"time"

	"compound/internal/policy"
	"compound/internal/work"
)

// TestKillSwitchHaltsDirectorAutoAdmit wires the halt to the real typed
// job_health path: even a threshold-met class grant stays proposed while the
// halt is engaged, then the same sealed eligibility admits after it is cleared.
func TestKillSwitchHaltsDirectorAutoAdmit(t *testing.T) {
	home := seededDirectorHome(t)
	now := directorTestNow.Add(96 * time.Hour)
	seedDirectorApplyGrant(t, home, now.Add(-72*time.Hour))
	seedJobHealthAutonomyGrant(t, home, "job_health", 2, 1, 0, now.Add(-72*time.Hour))
	seedJobHealthAutonomySample(t, home, "halt-a", now.Add(-48*time.Hour), "completed", "auto/judge", true)
	seedJobHealthAutonomySample(t, home, "halt-b", now.Add(-24*time.Hour), "completed", "auto/judge", true)
	seedFailingJobHealth(t, home)
	if err := policy.WriteAutonomyHalt(home, policy.AutonomyHalt{Global: true, Reason: "incident review"}); err != nil {
		t.Fatalf("engage halt: %v", err)
	}

	report, err := Propose(home, Options{Apply: true, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	proposal := findProposalByKind(report, "job_health")
	if proposal == nil || proposal.AutoAdmit == nil || proposal.AutoAdmitResult == nil {
		t.Fatalf("expected one typed job_health auto-admit attempt, got %+v", report)
	}
	store, err := work.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if !proposal.AutoAdmitResult.Halted || proposal.AutoAdmitResult.Admitted {
		t.Fatalf("global halt must deny director auto-admit: %+v", proposal)
	}
	if got, _ := store.Get(proposal.WorkID); got.State != "proposed" {
		t.Fatalf("halted director work must stay proposed, got %q", got.State)
	}

	// Halt cleared: the same eligible auto-admit now proceeds to admitted.
	if err := policy.WriteAutonomyHalt(home, policy.AutonomyHalt{}); err != nil {
		t.Fatalf("clear halt: %v", err)
	}
	admitted, err := store.AdmitAuto(proposal.WorkID, *proposal.AutoAdmit, now.Add(time.Minute))
	if err != nil || !admitted.Admitted {
		t.Fatalf("cleared halt must let director auto-admit proceed: err=%v res=%+v", err, admitted)
	}
	if got, _ := store.Get(proposal.WorkID); got.State != "admitted" {
		t.Fatalf("director work should be admitted after clearing halt, got %q", got.State)
	}
}
