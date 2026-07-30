package awstream

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"compound/internal/activity"
	"compound/internal/envelope"
)

// RawEvent is one (bucket, event) pair captured from AW. The Bucket carries hostname
// and device-id provenance; the Event carries the raw payload.
type RawEvent struct {
	Bucket  Bucket
	AWEvent Event
}

// Normalized is one Tier-0 activity.Event plus the parallel host bindings the caller
// should stamp on any outbound ActionIntent or persisted JSONL header. The two slices
// returned by Normalize stay in lockstep — index N of events corresponds to index N of
// hosts. Neither carries the other inline: Meta is reserved for watcher-shaped keys
// (file, project, pane_cwd) and host provenance lives on the typed envelope.Header.
type Normalized struct {
	Event activity.Event
	Host  envelope.HostBindings
}

// normalizeLogs collects non-fatal diagnostics (e.g., "bucket X has duplicate ts across
// hostnames" or "event Y carried banned meta key X — dropped"). The caller decides
// whether to surface, log, or count these. Normalize itself is pure — diagnostics live
// outside the return values so the F12 deterministic-output invariant holds across
// re-runs with the same input.
type normalizeLogs struct {
	DuplicateTS int      // events dropped because (bucket_type, ts) collided across hostnames
	BannedMeta  int      // events dropped because their data.payload carried a banned meta key
	BadEvent    int      // events dropped because data.payload couldn't be parsed for app/idle
	Notes       []string // human-readable notes (capped at 32 to bound memory)
}

func (l *normalizeLogs) note(s string) {
	if len(l.Notes) >= 32 {
		return
	}
	l.Notes = append(l.Notes, s)
}

// forbiddenMetaKeys mirrors activity.forbiddenMetaKeys — same invariant, applied at
// the AW reader boundary so a hostile or buggy AW watcher can't smuggle content-bearing
// keys through to compound. Kept in sync with activity.forbiddenMetaKeys (the latter is
// the single source of truth for the activity package; awstream's copy is intentionally
// separate so this package has no circular dependency on activity's internals).
var forbiddenMetaKeys = map[string]bool{
	"title":    true,
	"url":      true,
	"document": true,
	"text":     true,
	"content":  true,
}

// Normalize converts AW raw events into Tier-0 activity.Event values joined across
// hostnames. The contract:
//
//   - Input order is irrelevant; output is sorted by (bucket_type, ts) ascending.
//   - Events with the same (bucket_type, ts) from multiple hostnames are kept in
//     alphabetical-hostname order (deterministic). Both are returned; the caller can
//     decide whether to de-dup. (Per the architecture decision: keep both, log it.)
//   - AW `aw-watcher-window_*` events map to KindAppActivated; bundle_id is taken from
//     data.app (preferred) or data.bundle_id. Empty bundle_id is REFUSED (activity's
//     requiresBundle gate fires on drain, but we surface it earlier so the caller
//     can log/count).
//   - AW `aw-watcher-afk_*` events map to KindIdleStarted/KindIdleEnded. Empty
//     bundle_id is OK (afk is session-scoped, not app-scoped).
//   - Events whose data.payload carries a banned meta key (F5) are DROPPED, not
//     forwarded — the invariant is enforced at the read boundary.
//   - Seq is assigned monotonically per output position: 1, 2, 3, ... — deterministic.
//   - engine.id = "compound-activity-awstream"; engine.revision = SchemaVersion.
//
// Returns: events + hosts (parallel slices), and a non-nil Logs if anything was dropped.
// Identical input produces byte-identical events+hosts (F12).
func Normalize(in []RawEvent) (events []activity.Event, hosts []envelope.HostBindings, logs normalizeLogs) {
	if len(in) == 0 {
		return nil, nil, normalizeLogs{}
	}

	// 1. Sort deterministically: bucket_type, ts, hostname.
	sorted := make([]RawEvent, len(in))
	copy(sorted, in)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		ta, tb := a.Bucket.BucketType(), b.Bucket.BucketType()
		if ta != tb {
			return ta < tb
		}
		if a.AWEvent.Timestamp != b.AWEvent.Timestamp {
			return a.AWEvent.Timestamp < b.AWEvent.Timestamp
		}
		return a.Bucket.Hostname < b.Bucket.Hostname
	})

	// 2. Walk and map each RawEvent into a Normalized.
	out := make([]Normalized, 0, len(sorted))
	for _, re := range sorted {
		ev, ok := mapEvent(re, &logs)
		if !ok {
			continue
		}
		out = append(out, ev)
	}

	// 3. Split into parallel slices.
	events = make([]activity.Event, len(out))
	hosts = make([]envelope.HostBindings, len(out))
	for i, n := range out {
		events[i] = n.Event
		hosts[i] = n.Host
	}

	// 4. Detect duplicate (bucket_type, ts) collisions across hostnames and log them.
	for i := 1; i < len(out); i++ {
		a, b := out[i-1], out[i]
		if a.Event.Kind == b.Event.Kind && a.Event.TS == b.Event.TS {
			logs.DuplicateTS++
			logs.note(fmt.Sprintf("duplicate %s at %s across hostnames: %q and %q",
				a.Event.Kind, a.Event.TS, hosts[i-1].Hostname, hosts[i].Hostname))
		}
	}

	return events, hosts, logs
}

// SchemaVersion is the activity-package schema version this writer emits. Kept here so
// awstream can declare its output version without importing internal/activity's
// unexported SchemaVersion constant. Update in lockstep with internal/activity.
//
// v0.2 (Phase 5): Tier-1 envelope events are first-class — KindWindowFocused lights
// up reducer semantics (coalesces on bundle like app_activated). awstream emits only
// KindAppActivated + KindIdleStarted/Ended (Tier-0 kinds); Tier-1 events come from
// the tier1 collector, not this one.
const SchemaVersion = "compound.activity_observation.v0.2"

// EngineID identifies this collector in the activity.Event.Engine field. Distinguishes
// awstream-sourced events from the Swift launchd collector's output (whose engine.id
// is "compound-activity-sensor").
const EngineID = "compound-activity-awstream"

// EngineRevision is the awstream package's revision. Bumped when normalize semantics
// change (new mapping, new field, etc.). Surfaces in the activity.Event.Engine.Revision
// field so downstream consumers can detect normalization drift.
const EngineRevision = "v0.2.0"

// mapEvent maps a single AW event to a Tier-0 activity.Event. Returns ok=false to
// signal a drop; logs are appended in that case. Two paths:
//
//   - window watcher: data.app (or data.bundle_id) is the bundle; data.title and
//     data.url are present but IGNORED (F5 — they would otherwise be smuggled through
//     if we mirrored them into Meta).
//   - afk watcher: data.status is "afk" or "not-afk"; maps to KindIdleStarted/Ended.
//
// Events whose data.payload carries a banned meta key (title/url/document/text/content)
// are dropped. We don't try to strip them — the watcher that emitted them is hostile
// or buggy, and stripping silently would mask the failure.
func mapEvent(re RawEvent, logs *normalizeLogs) (Normalized, bool) {
	bucketType := re.Bucket.BucketType()
	switch bucketType {
	case "window":
		return mapWindowEvent(re, logs)
	case "afk":
		return mapAFKEvent(re, logs)
	case "vscode":
		// vscode watcher carries project/file — for Tier-1 we read these via the
		// tier1/source/editor.go path (which uses project only, never file). For
		// Tier-0 (this function) we treat vscode like a regular window app: bundle
		// comes from data.app or data.bundle_id, file/title/url are ignored.
		return mapWindowEvent(re, logs)
	case "web-brave", "web-chrome", "web-firefox", "web-tab":
		// Web watchers carry URL/title — Tier-0 ignores both. Surface as window-class
		// events with the browser bundle id when data.app is present.
		return mapWindowEvent(re, logs)
	default:
		// Unknown watcher type: forward as a no-op KindCaptureGap (gap marker) so the
		// segment reducer doesn't open a foreground interval we can't classify.
		ev := activity.Event{
			SchemaVersion: SchemaVersion,
			TS:            re.AWEvent.Timestamp,
			Kind:          activity.KindCaptureGap,
			Engine:        activity.Engine{ID: EngineID, Revision: EngineRevision},
			Meta:          map[string]string{},
		}
		return Normalized{Event: ev, Host: hostBindingsFor(re)}, true
	}
}

// mapWindowEvent handles window-class watchers (window, vscode, web-*). Pulls bundle_id
// from data.app (preferred) or data.bundle_id. Refuses any event whose data payload
// carries a banned meta key (F5).
func mapWindowEvent(re RawEvent, logs *normalizeLogs) (Normalized, bool) {
	if len(re.AWEvent.Data) == 0 || string(re.AWEvent.Data) == "null" {
		logs.BadEvent++
		return Normalized{}, false
	}
	// Parse the data payload into a generic map for key-by-key inspection.
	var data map[string]json.RawMessage
	if err := json.Unmarshal(re.AWEvent.Data, &data); err != nil {
		logs.BadEvent++
		logs.note(fmt.Sprintf("window event data not parseable: %v", err))
		return Normalized{}, false
	}
	// F5: refuse any payload that carries a banned content-bearing meta key.
	for k := range data {
		if forbiddenMetaKeys[strings.ToLower(strings.TrimSpace(k))] {
			logs.BannedMeta++
			logs.note(fmt.Sprintf("window event refused: data carries banned meta key %q", k))
			return Normalized{}, false
		}
	}
	// Bundle id: prefer data.app, fall back to data.bundle_id. AW's window watcher
	// writes "app"; some watchers write "bundle_id". Either is acceptable.
	var bundle string
	if raw, ok := data["app"]; ok {
		_ = json.Unmarshal(raw, &bundle)
	}
	if bundle == "" {
		if raw, ok := data["bundle_id"]; ok {
			_ = json.Unmarshal(raw, &bundle)
		}
	}
	if bundle == "" {
		logs.BadEvent++
		logs.note(fmt.Sprintf("window event refused: data.app/bundle_id missing or empty"))
		return Normalized{}, false
	}
	// App name: optional. data.app_name (vscode watcher) or just reuse the bundle id.
	var appName string
	if raw, ok := data["app_name"]; ok {
		_ = json.Unmarshal(raw, &appName)
	}
	if appName == "" {
		appName = bundle
	}
	ev := activity.Event{
		SchemaVersion: SchemaVersion,
		TS:            re.AWEvent.Timestamp,
		Kind:          activity.KindAppActivated,
		BundleID:      bundle,
		AppName:       appName,
		Engine:        activity.Engine{ID: EngineID, Revision: EngineRevision},
		Meta:          map[string]string{},
	}
	return Normalized{Event: ev, Host: hostBindingsFor(re)}, true
}

// mapAFKEvent handles aw-watcher-afk_* events. data.status is "afk" or "not-afk"; maps
// to KindIdleStarted/Ended. Empty bundle_id is OK (afk is session-scoped).
func mapAFKEvent(re RawEvent, logs *normalizeLogs) (Normalized, bool) {
	var data map[string]json.RawMessage
	if err := json.Unmarshal(re.AWEvent.Data, &data); err != nil {
		logs.BadEvent++
		logs.note(fmt.Sprintf("afk event data not parseable: %v", err))
		return Normalized{}, false
	}
	var status string
	if raw, ok := data["status"]; ok {
		_ = json.Unmarshal(raw, &status)
	}
	kind := activity.KindIdleEnded
	switch status {
	case "afk":
		kind = activity.KindIdleStarted
	case "not-afk":
		kind = activity.KindIdleEnded
	default:
		logs.BadEvent++
		logs.note(fmt.Sprintf("afk event refused: data.status = %q (want afk|not-afk)", status))
		return Normalized{}, false
	}
	ev := activity.Event{
		SchemaVersion: SchemaVersion,
		TS:            re.AWEvent.Timestamp,
		Kind:          kind,
		Engine:        activity.Engine{ID: EngineID, Revision: EngineRevision},
		Meta:          map[string]string{},
	}
	return Normalized{Event: ev, Host: hostBindingsFor(re)}, true
}

// hostBindingsFor builds an envelope.HostBindings from the source bucket. Empty fields
// are allowed — HostBindings.Validate() permits them; the caller decides whether to
// stamp an empty value or omit.
func hostBindingsFor(re RawEvent) envelope.HostBindings {
	return envelope.HostBindings{
		HostID:   re.Bucket.DeviceID(),
		Hostname: re.Bucket.Hostname,
		DeviceID: re.Bucket.Client,
	}
}

// AssignSeq fills in Event.Seq monotonically (1..N) on the given slice in place. Done
// after Normalize so all callers see the same ordering. Idempotent — calling twice on
// the same input produces identical output.
func AssignSeq(events []activity.Event) {
	for i := range events {
		events[i].Seq = int64(i + 1)
	}
}
