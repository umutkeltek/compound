package mem

import (
	"testing"
	"time"
)

type backdatedReconcileFixture struct {
	store       *Store
	now         time.Time
	baselineAt  time.Time
	backdatedID string
}

func prepareBackdatedReconcileFixture(t *testing.T) backdatedReconcileFixture {
	t.Helper()

	store := newStore(t)
	closeMemCompletionGate(t, store)
	now := t0.AddDate(0, 0, 2)
	baselineAt := now.Add(-2 * time.Hour)
	claim := reconcileClaim("claim_backdated_fold", 1, "project:api", "runtime", "Go", rfc3339(t0))
	appendReconcileClaim(t, store, claim)
	writeReconcileTransition(t, store, reconcileTransitionInput{
		ID:        "tr_reconcile_baseline",
		CreatedAt: rfc3339(baselineAt),
		Scope:     claim.Entity,
		ClaimRefs: []string{claim.ID},
	})

	first, err := store.SynthesisSweep([]string{"reconcile"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.ReconcileDismissedCount != 1 {
		t.Fatalf("baseline reconcile dismissed = %d, want 1; report=%+v", first.ReconcileDismissedCount, first)
	}
	checkpoint, err := store.loadReconcileCheckpoint(now)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Cursor == "" {
		t.Fatal("baseline reconcile did not persist its segment cursor")
	}
	if checkpoint.LastEventTime != rfc3339(baselineAt) {
		t.Fatalf("baseline last event time = %q, want %q", checkpoint.LastEventTime, rfc3339(baselineAt))
	}

	backdatedID := "tr_reconcile_backdated"
	writeReconcileTransition(t, store, reconcileTransitionInput{
		ID:        backdatedID,
		CreatedAt: rfc3339(baselineAt.Add(-time.Hour)),
		Scope:     claim.Entity,
		ClaimRefs: []string{claim.ID},
	})
	return backdatedReconcileFixture{
		store:       store,
		now:         now,
		baselineAt:  baselineAt,
		backdatedID: backdatedID,
	}
}

func TestSynthesisReconcileResumedBackdatedEventFoldsOnce(t *testing.T) {
	fixture := prepareBackdatedReconcileFixture(t)

	report, err := fixture.store.SynthesisSweep([]string{"reconcile"}, fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report.ReconcileDismissedCount != 1 {
		t.Fatalf("backdated resumed fold dismissed = %d, want 1; event %s was silently dropped", report.ReconcileDismissedCount, fixture.backdatedID)
	}
	checkpoint, err := fixture.store.loadReconcileCheckpoint(fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Counts["backdated_folded"] != 1 {
		t.Fatalf("checkpoint backdated_folded = %d, want 1", checkpoint.Counts["backdated_folded"])
	}
	if checkpoint.LastEventTime != rfc3339(fixture.baselineAt) {
		t.Fatalf("backdated fold regressed last event time to %q, want %q", checkpoint.LastEventTime, rfc3339(fixture.baselineAt))
	}

	again, err := fixture.store.SynthesisSweep([]string{"reconcile"}, fixture.now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if again.ReconcileDismissedCount != 0 {
		t.Fatalf("steady resumed sweep folded the backdated event again: report=%+v", again)
	}
}
