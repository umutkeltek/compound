// Package activity is compound's Tier-0 ambient-activity observed family: a content-free stream of
// what the operator does OUTSIDE agent sessions (app focus, idle, lock), entering through the
// existing observed membrane, never as a new store.
//
// It owns a typed schema family (compound.activity_observation.v0.1 events + ActivitySpan reductions)
// under COMPOUND_HOME/observed/activity/. It is content-free BY SCHEMA (no title/url/document field
// exists), day/size-rotated (never per-event files — the eventstore 53k-file lesson), and mints ZERO
// automatic memory candidates. Deliberate non-imports (enforced by imports_test.go): internal/mem,
// internal/candidates (no auto-candidate minting), internal/eventstore (segments land only under
// observed/activity/, never in the event store). Governance (enable/disable/drain grant-gate) rides
// the generic internal/act + internal/policy seams; this package writes segment/span files directly.
package activity

// SchemaVersion is the closed event vocabulary version. A raw event carrying any other
// schema_version is refused by the strict decoder.
const SchemaVersion = "compound.activity_observation.v0.1"

// SpansSchemaVersion versions the deterministic span reduction file.
const SpansSchemaVersion = "compound.activity_spans.v0.1"

// Event kinds — the closed vocabulary. app_activated/idle_*/screen_* + capture_gap are emitted by the
// slice-1 collector; window_focused/permission_lost are schema-valid but reserved for a Tier-1 slice.
const (
	KindAppActivated   = "app_activated"
	KindWindowFocused  = "window_focused"
	KindIdleStarted    = "idle_started"
	KindIdleEnded      = "idle_ended"
	KindScreenLocked   = "screen_locked"
	KindScreenUnlocked = "screen_unlocked"
	KindCaptureGap     = "capture_gap"
	KindPermissionLost = "permission_lost"
)

// knownKinds is the closed set the strict decoder accepts. An event with any other kind is refused.
var knownKinds = map[string]bool{
	KindAppActivated:   true,
	KindWindowFocused:  true,
	KindIdleStarted:    true,
	KindIdleEnded:      true,
	KindScreenLocked:   true,
	KindScreenUnlocked: true,
	KindCaptureGap:     true,
	KindPermissionLost: true,
}

// forbiddenMetaKeys are content-bearing keys that must NEVER appear in a Tier-0 event's meta map. The
// schema has no title/url/document field, but a buggy or hostile collector could smuggle content
// through meta; the decoder refuses any event carrying one of these (belt-and-braces on top of the
// content-free schema — falsifier F5).
var forbiddenMetaKeys = map[string]bool{
	"title":    true,
	"url":      true,
	"document": true,
	"text":     true,
	"content":  true,
}

// Category is the coarse, content-free classification of an app. Deterministic longest-prefix map,
// never ML. An unmapped bundle id is CategoryUnknown at confidence 0.
type Category string

const (
	CategoryCoding   Category = "coding"
	CategoryBrowsing Category = "browsing"
	CategoryDesign   Category = "design"
	CategoryComms    Category = "comms"
	CategoryStoreOps Category = "store-ops"
	CategoryDocs     Category = "docs"
	CategoryMedia    Category = "media"
	CategorySystem   Category = "system"
	CategoryUnknown  Category = "unknown"
)

// Coverage marks whether a span's day-window is trustworthy for block inference.
const (
	CoverageComplete   = "complete"
	CoverageIncomplete = "incomplete"
)

// Engine identifies the collector that produced an event.
type Engine struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

// Event is one raw activity observation. Content-free by construction: there is no title, url, or
// document field. Meta is a bounded optional map for coarse annotations (never content).
type Event struct {
	SchemaVersion string            `json:"schema_version"`
	Seq           int64             `json:"seq"`
	TS            string            `json:"ts"`
	Kind          string            `json:"kind"`
	BundleID      string            `json:"bundle_id"`
	AppName       string            `json:"app_name"`
	Engine        Engine            `json:"engine"`
	Meta          map[string]string `json:"meta,omitempty"`
}

// ActivitySpan is the durable, consumer-facing unit: a deterministic reduction of coalesced raw
// events into a dwell interval. Dozens per day, not thousands. Segment-not-event granularity is the
// growth control.
type ActivitySpan struct {
	Start     string   `json:"start"`
	End       string   `json:"end"`
	ActiveSec int64    `json:"active_sec"`
	BundleID  string   `json:"bundle_id"`
	AppName   string   `json:"app_name"`
	Category  Category `json:"category"`
	Coverage  string   `json:"coverage"`
}

// SpanFile is the atomic-rewrite per-day span reduction persisted under observed/activity/spans/.
type SpanFile struct {
	SchemaVersion string         `json:"schema_version"`
	Date          string         `json:"date"`
	GeneratedAt   string         `json:"generated_at"`
	Spans         []ActivitySpan `json:"spans"`
}
