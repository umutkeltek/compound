package work

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSweepStalePersistenceFaultsRecoverExactly(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		stage            string
		pending          bool
		recoveryRequired bool
		state            string
		eventDelta       int
		leasePresent     bool
	}{
		{name: "before pending persist", stage: sweepStaleFaultBeforePendingPersist, state: "claimed", leasePresent: true},
		{name: "after pending persist", stage: sweepStaleFaultAfterPendingPersist, pending: true, recoveryRequired: true, state: "claimed", leasePresent: true},
		{name: "before event publish", stage: sweepStaleFaultBeforeEventPublish, pending: true, recoveryRequired: true, state: "claimed", leasePresent: true},
		{name: "before work publish", stage: sweepStaleFaultBeforeWorkPublish, pending: true, recoveryRequired: true, state: "claimed", eventDelta: 1, leasePresent: true},
		{name: "after publication", stage: sweepStaleFaultAfterPublication, pending: true, recoveryRequired: true, state: "cancelled", eventDelta: 1, leasePresent: true},
		{name: "before lease release", stage: sweepStaleFaultBeforeLeaseRelease, pending: true, recoveryRequired: true, state: "cancelled", eventDelta: 1, leasePresent: true},
		{name: "before pending clear", stage: sweepStaleFaultBeforePendingClear, pending: true, recoveryRequired: true, state: "cancelled", eventDelta: 1},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			s := newStore(t)
			w := staleEnvelope(t, s, "claimed", 30*24*time.Hour, fixedNow)
			beforeEvents, err := s.eventsFor(w.ID)
			if err != nil {
				t.Fatal(err)
			}
			fired := false
			injected := errors.New("injected " + test.stage)
			s.sweepStaleFault = func(stage string) error {
				if stage == test.stage && !fired {
					fired = true
					return injected
				}
				return nil
			}

			failed, err := s.SweepStaleApply(72*time.Hour, newFakeProbe(), fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			s.sweepStaleFault = nil
			if !fired {
				t.Fatalf("fault stage %q was not reached", test.stage)
			}
			if failed.Reclaimed != 0 || failed.Skipped != 1 || len(failed.Candidates) != 1 {
				t.Fatalf("failed report = %+v, want one skipped candidate", failed)
			}
			gotRecoveryRequired := strings.Contains(failed.Candidates[0].ReclaimError, "sweep-stale recovery_required")
			if gotRecoveryRequired != test.recoveryRequired {
				t.Fatalf("recovery_required = %v, want %v: %s", gotRecoveryRequired, test.recoveryRequired, failed.Candidates[0].ReclaimError)
			}
			pendingPath := s.sweepStalePendingPath(w.ID)
			_, statErr := os.Stat(pendingPath)
			if (statErr == nil) != test.pending {
				t.Fatalf("pending present = %v, want %v: %v", statErr == nil, test.pending, statErr)
			}
			current, err := s.Get(w.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.State != test.state {
				t.Fatalf("state after %s = %q, want %q", test.stage, current.State, test.state)
			}
			afterEvents, err := s.eventsFor(w.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(afterEvents) != len(beforeEvents)+test.eventDelta {
				t.Fatalf("event count after %s = %d, want %d", test.stage, len(afterEvents), len(beforeEvents)+test.eventDelta)
			}
			if !reflect.DeepEqual(afterEvents[:len(beforeEvents)], beforeEvents) {
				t.Fatalf("event prefix changed after %s", test.stage)
			}
			_, leaseErr := s.readLease(w.ID)
			if (leaseErr == nil) != test.leasePresent {
				t.Fatalf("lease present after %s = %v, want %v: %v", test.stage, leaseErr == nil, test.leasePresent, leaseErr)
			}

			recovered, err := s.SweepStaleApply(72*time.Hour, newFakeProbe(), fixedNow.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if recovered.Reclaimed != 1 || len(recovered.Candidates) != 1 || !recovered.Candidates[0].Reclaimed {
				t.Fatalf("recovery report = %+v, want one exact completed reclaim", recovered)
			}
			current, err = s.Get(w.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.State != "cancelled" {
				t.Fatalf("recovered state = %q, want cancelled", current.State)
			}
			finalEvents, err := s.eventsFor(w.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(finalEvents) != len(beforeEvents)+1 || !reflect.DeepEqual(finalEvents[:len(beforeEvents)], beforeEvents) {
				t.Fatalf("recovered event history = %+v, want unchanged prefix plus one event", finalEvents)
			}
			if _, err := s.readLease(w.ID); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("lease after recovery: %v", err)
			}
			if _, err := os.Stat(pendingPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("pending frame after recovery: %v", err)
			}
		})
	}
}

func TestSweepStalePendingFencesLifecycleMutation(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	w := staleEnvelope(t, s, "claimed", 30*24*time.Hour, fixedNow)
	fired := false
	s.sweepStaleFault = func(stage string) error {
		if stage == sweepStaleFaultAfterPendingPersist && !fired {
			fired = true
			return errors.New("freeze before publication")
		}
		return nil
	}
	report, err := s.SweepStaleApply(72*time.Hour, newFakeProbe(), fixedNow)
	s.sweepStaleFault = nil
	if err != nil {
		t.Fatal(err)
	}
	if !fired || len(report.Candidates) != 1 || !strings.Contains(report.Candidates[0].ReclaimError, "recovery_required") {
		t.Fatalf("setup report = %+v, want frozen recovery frame", report)
	}

	if _, err := s.Claim(w.ID, "codex", fixedNow.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "recovery_required") {
		t.Fatalf("Claim error = %v, want sweep recovery fence", err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "claimed", Actor: "codex"}, fixedNow.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "recovery_required") {
		t.Fatalf("Advance error = %v, want sweep recovery fence", err)
	}
	if _, err := s.Close(w.ID, CloseInput{}, fixedNow.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "recovery_required") {
		t.Fatalf("Close error = %v, want sweep recovery fence", err)
	}
	current, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != "claimed" {
		t.Fatalf("fenced mutations changed state to %q", current.State)
	}
	if _, err := s.readLease(w.ID); err != nil {
		t.Fatalf("fenced mutations changed lease: %v", err)
	}

	recovered, err := s.SweepStaleApply(72*time.Hour, newFakeProbe(), fixedNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Reclaimed != 1 {
		t.Fatalf("recovery report = %+v, want one completed reclaim", recovered)
	}
}
