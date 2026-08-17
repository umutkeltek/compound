package operatornotice

// Red-first falsifiers for the two remaining decidability defects.
//
// Closure: the system can prove it ASKED (a NoticeRecord with outcome
// "emitted") and cannot prove it was ANSWERED. Without that, the only
// measurable rate is holds raised per day — the number that already reads
// ~19,619 raised against 3 approvals. The number that matters is decisions
// returned, and nothing computes it.
//
// Volume: with no cap, one flush over a real frontier emits a notification per
// needs-you group (18 groups on the live home as of 2026-08-18). Eighteen
// simultaneous asks is not eighteen decisions; it is one ignored batch. The
// funnel is decisions/day, so the queue must be served one at a time.
//
// Both tests are RED on the current implementation.

import (
	"testing"
	"time"

	"compound/internal/attention"
)

// needsYouGroup builds one BandNeedsYou group directly, so these tests control
// the frontier exactly rather than inferring it from a seeded workbench.
func needsYouGroup(area, itemID string, decidable bool) attention.Group {
	g := attention.Group{
		Key:             "test|" + area,
		Area:            area,
		Band:            attention.BandNeedsYou,
		Source:          attention.SourceReviews,
		Kind:            "adjudicate",
		Count:           1,
		OperatorSummary: area + " needs an operator decision.",
		ItemIDs:         []string{itemID},
		OldestCreatedAt: "2026-08-01T00:00:00Z",
	}
	if decidable {
		g.Action = &attention.Action{
			ID:   "review-resolve-" + area,
			Argv: []string{"compound", "reviews", "resolve", "--area", area},
		}
	}
	return g
}

func reportWith(groups ...attention.Group) attention.Report {
	return attention.Report{Groups: groups}
}

// TestNotifyClosesHoldWhenItLeavesFrontier is the closure falsifier. A hold
// that has left the needs-you frontier has been decided; the record must say
// so, with the latency, so "decisions returned" becomes computable.
//
// RED today: nothing ever revisits a prior record, so an emitted hold stays
// "emitted" forever and Resolved does not exist.
func TestNotifyClosesHoldWhenItLeavesFrontier(t *testing.T) {
	homePath := seedHeldHome(t, "work_closure")
	withFakeExecute(t, "rec_closure", "tr_closure")

	g := needsYouGroup("capability-lifecycle-proposals", "work_closure", true)
	first, err := NotifyFromInputs(homePath, "buzz", reportWith(g), fixedNow)
	if err != nil {
		t.Fatalf("first notify: %v", err)
	}
	if first.Emitted != 1 {
		t.Fatalf("first flush = %+v, want Emitted=1", first)
	}

	// The operator decided it: the hold is gone from the frontier.
	later := fixedNow.Add(90 * time.Minute)
	second, err := NotifyFromInputs(homePath, "buzz", reportWith(), later)
	if err != nil {
		t.Fatalf("second notify: %v", err)
	}
	if second.Resolved != 1 {
		t.Fatalf("a hold that left the frontier was not recorded as resolved: %+v", second)
	}
	if second.OpenHolds != 0 {
		t.Fatalf("open hold count did not fall after closure: %+v", second)
	}

	_, rec, err := findPriorNotification(homePath, HoldIDFor(g))
	if err != nil {
		t.Fatalf("read notice record: %v", err)
	}
	if rec == nil || rec.Outcome != "resolved" {
		t.Fatalf("notice record outcome = %+v, want resolved", rec)
	}
	if rec.ResolvedAt == "" {
		t.Errorf("resolved record carries no ResolvedAt")
	}
	if rec.DecisionLatencySeconds <= 0 {
		t.Errorf("resolved record latency = %d, want > 0", rec.DecisionLatencySeconds)
	}
}

// TestNotifyEmitsAtMostMaxOpen pins the serialization promise: an unanswered
// hold occupies the channel until it is answered. Three simultaneous holds
// produce one ask and two queued, not three asks.
//
// RED today: the loop emits one notification per needs-you group with no cap.
func TestNotifyEmitsAtMostMaxOpen(t *testing.T) {
	homePath := seedHeldHome(t, "work_q1", "work_q2", "work_q3")
	withFakeExecute(t, "rec_q", "tr_q")

	in := reportWith(
		needsYouGroup("area-one", "work_q1", true),
		needsYouGroup("area-two", "work_q2", true),
		needsYouGroup("area-three", "work_q3", true),
	)

	got, err := NotifyFromInputs(homePath, "buzz", in, fixedNow)
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if got.Emitted != 1 {
		t.Errorf("emitted %d holds at once; the operator funnel is one decision at a time: %+v", got.Emitted, got)
	}
	if got.Queued != 2 {
		t.Errorf("queued = %d, want 2 (the holds that did not get the channel): %+v", got.Queued, got)
	}

	// A queued hold is NOT notified, so it must remain eligible: nothing was
	// persisted for it and the next flush can still reach it.
	if _, rec, _ := findPriorNotification(homePath, HoldIDFor(in.Groups[2])); rec != nil {
		t.Errorf("a queued hold wrote a record, so it can never be notified later: %+v", rec)
	}
}

// TestNotifyPrefersDecidableHolds guards the interaction between the cap and
// decidability: when only one ask fits, it must be one the operator can act
// on. An undecidable hold winning the channel starves every decidable one.
//
// RED today: order is frontier order, and decidability is not consulted.
func TestNotifyPrefersDecidableHolds(t *testing.T) {
	homePath := seedHeldHome(t, "work_undecidable", "work_decidable")
	withFakeExecute(t, "rec_pref", "tr_pref")

	undecidable := needsYouGroup("workbench-holds", "work_undecidable", false)
	decidable := needsYouGroup("route-promotions", "work_decidable", true)

	// Undecidable first in frontier order — the cap must still pick the other.
	got, err := NotifyFromInputs(homePath, "buzz", reportWith(undecidable, decidable), fixedNow)
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if got.Emitted != 1 {
		t.Fatalf("emitted = %d, want 1: %+v", got.Emitted, got)
	}

	_, rec, err := findPriorNotification(homePath, HoldIDFor(decidable))
	if err != nil {
		t.Fatalf("read notice record: %v", err)
	}
	if rec == nil || rec.Outcome != "emitted" {
		t.Errorf("the decidable hold did not get the channel; an undecidable ask consumed it: %+v", rec)
	}
}
