package activity

import (
	"sort"
	"time"
)

// DwellFloorSec is the minimum active duration a foreground interval must hold to survive as its own
// span. Shorter focus flickers (a 3-second glance at Slack while coding) merge into their neighbor
// rather than minting a misleading span. Deterministic, no ML.
const DwellFloorSec = 15

// rawSpan is a foreground interval before dwell-floor merging.
type rawSpan struct {
	start  time.Time
	end    time.Time
	bundle string
	app    string
}

// Segment reduces a day's raw events (any order) into deterministic ActivitySpans. It:
//   - coalesces consecutive same-bundle foreground into one span,
//   - closes the current span on idle_started/screen_locked/capture_gap (idle/lock time is a gap in
//     foreground, never a span),
//   - merges sub-DwellFloor flickers into their neighbor,
//   - marks EVERY span in the window coverage=incomplete if a capture_gap, permission_lost, or a seq
//     discontinuity was seen (silent outage must never read as clean activity — F11).
//
// Input events are assumed to belong to one day; ordering is normalized by seq here. Segment is pure
// and deterministic: identical input yields byte-identical spans (F12).
func Segment(events []Event) []ActivitySpan {
	if len(events) == 0 {
		return []ActivitySpan{}
	}
	ordered := make([]Event, len(events))
	copy(ordered, events)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Seq < ordered[j].Seq })

	incomplete := false
	var raws []rawSpan
	var cur *rawSpan
	var lastTS time.Time
	var lastSeq int64
	haveSeq := false

	closeCur := func(at time.Time) {
		if cur != nil {
			cur.end = at
			raws = append(raws, *cur)
			cur = nil
		}
	}

	for _, ev := range ordered {
		t, err := time.Parse(time.RFC3339, ev.TS)
		if err != nil {
			// A malformed ts survived decode only if callers bypass DecodeStrict; treat defensively
			// as a coverage gap rather than silently dropping.
			incomplete = true
			continue
		}
		if haveSeq && ev.Seq != lastSeq+1 {
			incomplete = true // a seq discontinuity means the collector dropped events in this window
		}
		lastSeq = ev.Seq
		haveSeq = true
		if t.After(lastTS) {
			lastTS = t
		}

		switch ev.Kind {
		case KindAppActivated:
			if cur != nil && cur.bundle == ev.BundleID {
				continue // same app in foreground; coalesce (keep the earlier start)
			}
			closeCur(t)
			cur = &rawSpan{start: t, bundle: ev.BundleID, app: ev.AppName}
		case KindIdleStarted, KindScreenLocked:
			closeCur(t) // idle/lock ends the foreground interval; the gap itself is not a span
		case KindCaptureGap:
			incomplete = true
			closeCur(t)
		case KindPermissionLost:
			incomplete = true
			closeCur(t)
		case KindIdleEnded, KindScreenUnlocked:
			// Foreground is unknown until the next app_activated; nothing to open here.
		case KindWindowFocused:
			// Reserved for Tier 1; unemitted in slice 1. No-op if it appears.
		}
	}
	// Close a trailing open foreground at the last observed timestamp. If that equals its start
	// (the opener was the final event), it is a zero-length span the dwell floor drops — honest:
	// we do not know how long an unclosed foreground lasted, so we do not count it.
	closeCur(lastTS)

	merged := mergeDwellFloor(raws)
	coverage := CoverageComplete
	if incomplete {
		coverage = CoverageIncomplete
	}
	spans := make([]ActivitySpan, 0, len(merged))
	for _, r := range merged {
		cat, _ := Classify(r.bundle)
		spans = append(spans, ActivitySpan{
			Start:     r.start.UTC().Format(time.RFC3339),
			End:       r.end.UTC().Format(time.RFC3339),
			ActiveSec: int64(r.end.Sub(r.start).Seconds()),
			BundleID:  r.bundle,
			AppName:   r.app,
			Category:  cat,
			Coverage:  coverage,
		})
	}
	return spans
}

// mergeDwellFloor drops sub-floor flickers, absorbing their time into a neighbor, and coalesces
// adjacent contiguous same-bundle survivors. Deterministic forward pass.
func mergeDwellFloor(raws []rawSpan) []rawSpan {
	var out []rawSpan
	var carryStart time.Time
	haveCarry := false
	for _, s := range raws {
		if s.end.Sub(s.start) >= time.Duration(DwellFloorSec)*time.Second {
			start := s.start
			if haveCarry {
				start = carryStart // a leading flicker with no prior survivor prepends to this span
				haveCarry = false
			}
			s.start = start
			if n := len(out); n > 0 && out[n-1].bundle == s.bundle && !out[n-1].end.Before(s.start) {
				// contiguous same-bundle (e.g. same app on both sides of an absorbed flicker) -> coalesce
				if s.end.After(out[n-1].end) {
					out[n-1].end = s.end
				}
				continue
			}
			out = append(out, s)
			continue
		}
		// Sub-floor flicker: absorb into the previous survivor, else carry to prepend to the next.
		if n := len(out); n > 0 {
			if s.end.After(out[n-1].end) {
				out[n-1].end = s.end
			}
		} else if !haveCarry {
			carryStart = s.start
			haveCarry = true
		}
	}
	return out
}
