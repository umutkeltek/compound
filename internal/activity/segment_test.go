package activity

import (
	"testing"
	"time"
)

func ev(seq int64, min int, kind, bundle, app string) Event {
	ts := time.Date(2026, 7, 15, 10, min, 0, 0, time.UTC).Format(time.RFC3339)
	return Event{SchemaVersion: SchemaVersion, Seq: seq, TS: ts, Kind: kind, BundleID: bundle, AppName: app, Engine: Engine{ID: "e", Revision: "v0.1"}}
}

func evSec(seq int64, min, sec int, kind, bundle, app string) Event {
	ts := time.Date(2026, 7, 15, 10, min, sec, 0, time.UTC).Format(time.RFC3339)
	return Event{SchemaVersion: SchemaVersion, Seq: seq, TS: ts, Kind: kind, BundleID: bundle, AppName: app, Engine: Engine{ID: "e", Revision: "v0.1"}}
}

func TestSegmentCoalescesSameBundle(t *testing.T) {
	events := []Event{
		ev(1, 0, KindAppActivated, "com.apple.dt.Xcode", "Xcode"),
		ev(2, 10, KindAppActivated, "com.apple.dt.Xcode", "Xcode"), // duplicate activation, same app
		ev(3, 30, KindAppActivated, "com.google.Chrome", "Chrome"),
		ev(4, 45, KindIdleStarted, "", ""),
	}
	spans := Segment(events)
	if len(spans) != 2 {
		t.Fatalf("want 2 spans (coalesced Xcode + Chrome), got %d: %+v", len(spans), spans)
	}
	if spans[0].BundleID != "com.apple.dt.Xcode" || spans[0].ActiveSec != 30*60 {
		t.Fatalf("Xcode span wrong: %+v", spans[0])
	}
	if spans[0].Category != CategoryCoding || spans[1].Category != CategoryBrowsing {
		t.Fatalf("categories wrong: %s %s", spans[0].Category, spans[1].Category)
	}
	if spans[0].Coverage != CoverageComplete {
		t.Fatalf("clean stream must be complete coverage, got %s", spans[0].Coverage)
	}
}

func TestSegmentIdleClosesSpan(t *testing.T) {
	events := []Event{
		ev(1, 0, KindAppActivated, "com.apple.dt.Xcode", "Xcode"),
		ev(2, 20, KindIdleStarted, "", ""),
		ev(3, 50, KindIdleEnded, "", ""),
		ev(4, 55, KindAppActivated, "com.apple.dt.Xcode", "Xcode"),
		ev(5, 70, KindScreenLocked, "", ""),
	}
	spans := Segment(events)
	// Xcode [0,20) then idle gap [20,50) then Xcode [55,70). Two distinct spans — idle must NOT
	// coalesce the two Xcode intervals across the gap.
	if len(spans) != 2 {
		t.Fatalf("idle must split into 2 spans, got %d: %+v", len(spans), spans)
	}
	if spans[0].ActiveSec != 20*60 || spans[1].ActiveSec != 15*60 {
		t.Fatalf("idle-split durations wrong: %+v", spans)
	}
}

// TestFalsifier11_CaptureGapMarksIncomplete: a capture_gap event marks EVERY span in the window
// incomplete, so the window is later excluded from block inference.
func TestFalsifier11_CaptureGapMarksIncomplete(t *testing.T) {
	events := []Event{
		ev(1, 0, KindAppActivated, "com.google.Chrome", "Chrome"),
		ev(2, 20, KindCaptureGap, "", ""),
		ev(3, 40, KindAppActivated, "com.google.Chrome", "Chrome"),
		ev(4, 60, KindIdleStarted, "", ""),
	}
	spans := Segment(events)
	for _, s := range spans {
		if s.Coverage != CoverageIncomplete {
			t.Fatalf("capture_gap must mark spans incomplete, got %s on %+v", s.Coverage, s)
		}
	}
}

// TestFalsifier11_SeqDiscontinuityMarksIncomplete: a jump in the monotonic seq means the collector
// dropped events; the window is incomplete even without an explicit capture_gap.
func TestFalsifier11_SeqDiscontinuityMarksIncomplete(t *testing.T) {
	events := []Event{
		ev(1, 0, KindAppActivated, "com.google.Chrome", "Chrome"),
		ev(5, 30, KindIdleStarted, "", ""), // seq jumps 1 -> 5 (events 2,3,4 lost)
	}
	spans := Segment(events)
	if len(spans) == 0 {
		t.Fatalf("expected at least one span")
	}
	for _, s := range spans {
		if s.Coverage != CoverageIncomplete {
			t.Fatalf("seq discontinuity must mark incomplete, got %s", s.Coverage)
		}
	}
}

// TestSegmentMergesFlicker: a sub-DwellFloor glance at Slack while coding does not mint a Slack span;
// the coding span absorbs it.
func TestSegmentMergesFlicker(t *testing.T) {
	events := []Event{
		evSec(1, 0, 0, KindAppActivated, "com.apple.dt.Xcode", "Xcode"),
		evSec(2, 20, 0, KindAppActivated, "com.tinyspeck.slackmacgap", "Slack"), // 3s glance
		evSec(3, 20, 3, KindAppActivated, "com.apple.dt.Xcode", "Xcode"),
		evSec(4, 40, 0, KindIdleStarted, "", ""),
	}
	spans := Segment(events)
	if len(spans) != 1 {
		t.Fatalf("flicker must merge away leaving one Xcode span, got %d: %+v", len(spans), spans)
	}
	if spans[0].BundleID != "com.apple.dt.Xcode" {
		t.Fatalf("surviving span should be Xcode, got %s", spans[0].BundleID)
	}
	if spans[0].ActiveSec != 40*60 {
		t.Fatalf("merged Xcode span should span full 40min (absorbing the flicker), got %d sec", spans[0].ActiveSec)
	}
}
