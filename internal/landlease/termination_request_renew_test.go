package landlease

import (
	"testing"
	"time"
)

// A termination request is addressed to the holder's running execution, whose
// proof supervisor polls for it every 250 ms. The holder also refreshes its
// lease every 1-5 s. A refresh that drops the request erases it before the
// poll reads it, so the holder never stops and the requester's steal stays
// refused. The request must survive the holder's own renewals and clear only
// when a different holder takes the lease.
func TestTerminationRequestSurvivesHolderRenewal(t *testing.T) {
	renewals := map[string]func(home string, now time.Time) (Lease, error){
		"refresh": func(home string, now time.Time) (Lease, error) {
			return Refresh(home, laneAlpha, time.Minute, now)
		},
		"same-holder-acquire": func(home string, now time.Time) (Lease, error) {
			return Acquire(home, laneAlpha, time.Minute, now)
		},
	}
	for name, renew := range renewals {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if _, err := Acquire(home, laneAlpha, time.Minute, baseNow); err != nil {
				t.Fatal(err)
			}
			requested, err := RequestExecutionTermination(home, laneAlpha, laneBeta, "take over the proof slot", baseNow.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := renew(home, baseNow.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			status, err := Status(home, baseNow.Add(3*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if !status.Held || status.Holder != laneAlpha {
				t.Fatalf("renewal lost the lease: %+v", status)
			}
			if status.TerminationRequestedBy != laneBeta ||
				status.TerminationRequestReason != requested.TerminationRequestReason ||
				status.TerminationRequestedAt != requested.TerminationRequestedAt {
				t.Fatalf("%s erased the pending termination request: %+v", name, status)
			}
		})
	}
}

func TestTerminationRequestClearsWhenAnotherHolderTakesOver(t *testing.T) {
	home := t.TempDir()
	if _, err := Acquire(home, laneAlpha, time.Minute, baseNow); err != nil {
		t.Fatal(err)
	}
	if _, err := RequestExecutionTermination(home, laneAlpha, laneBeta, "take over the proof slot", baseNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(home, laneBeta, time.Minute, baseNow.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	status, err := Status(home, baseNow.Add(2*time.Minute+time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if status.Holder != laneBeta || status.TerminationRequestedBy != "" || status.TerminationRequestReason != "" || status.TerminationRequestedAt != "" {
		t.Fatalf("new holder inherited the old holder's termination request: %+v", status)
	}
}

func TestTerminationRequestDoesNotOutliveExpiredLease(t *testing.T) {
	home := t.TempDir()
	if _, err := Acquire(home, laneAlpha, time.Minute, baseNow); err != nil {
		t.Fatal(err)
	}
	if _, err := RequestExecutionTermination(home, laneAlpha, laneBeta, "take over the proof slot", baseNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(home, laneAlpha, time.Minute, baseNow.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	status, err := Status(home, baseNow.Add(2*time.Minute+time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if status.Holder != laneAlpha || status.TerminationRequestedBy != "" {
		t.Fatalf("re-acquire after expiry kept a stale termination request: %+v", status)
	}
}
