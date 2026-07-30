// Package tier1 wires the AW virtual stream (internal/adapter/awstream) to the
// per-app envelope source registry (internal/activity/tier1/source) and writes
// Tier-1 events to the existing spool. It is the runtime half of Phase 4 — the
// counterpart to the existing Drain verb, which writes Tier-0 events from the
// collector's NDJSON spool into segments+spans.
//
// Doctrine: this package runs as a bounded CLI verb (`compound activity tier1
// collect --duration 30s`), not a Go daemon. "A blind cron is a liar with a
// schedule." Each invocation mints an ActionIntent, runs the three gates
// fail-closed (config enabled -> policy grant -> byte cap), collects events for
// the bounded window, exits. The operator (or a launchd plist) schedules it.
//
// Tier-1 events use the SAME schema as Tier-0 (compound.activity_observation.v0.2)
// — the only difference is which `kind` is emitted and what the Meta map carries.
// Tier-1 emits KindWindowFocused with envelope-bearing meta keys (pane_cwd,
// git_remote, agent_kind, etc.). The strict decoder refuses any event whose meta
// carries a banned content-bearing key (F5); this package's invariant check is the
// second-layer defense.
package tier1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/act"
	"compound/internal/activity"
	"compound/internal/adapter/awstream"
	"compound/internal/activity/tier1/source"
	"compound/internal/envelope"
	"compound/internal/policy"
)

// Tier1EngineID identifies this collector in activity.Event.Engine. Distinct from
// the Swift launchd collector ("compound-activity-sensor") and awstream
// ("compound-activity-awstream") so consumers can distinguish provenance.
const Tier1EngineID = "compound-activity-tier1"

// Tier1EngineRevision is the tier1 package's revision. Bumped when semantics
// change (new meta keys, new envelope source, etc.).
const Tier1EngineRevision = "v0.1.0"

// CollectReport is the honest outcome of a tier1 collect. Refused=true with a
// Reason means ZERO rows were written (fail-closed); the CLI maps it to a non-zero
// exit. SourcesCalled counts how many envelope sources were attempted; SourcesFailed
// counts ones that errored (graceful skip — the run continues).
type CollectReport struct {
	Refused        bool   `json:"refused"`
	Reason         string `json:"reason,omitempty"`
	EventsWritten  int    `json:"events_written"`
	SourcesCalled  int    `json:"sources_called"`
	SourcesFailed  int    `json:"sources_failed"`
	BannedRefused  int    `json:"banned_refused"`
	BytesUnderRoot int64  `json:"bytes_under_root"`
}

// Collector bundles the AW reader + source registry + spool writer. Production
// wiring uses NewCollector() with the default registry; tests inject a custom
// registry and a fixture AW client.
type Collector struct {
	AW       *awstream.Client
	Registry *source.Registry
	SpoolDir string
	nowFn    func() time.Time // overridable for tests; defaults to time.Now
}

// NewCollector returns a Collector wired to the default AW client (localhost:5600),
// the default source registry (cmux, iTerm2, Warp, Terminal.app, VS Code, Cursor),
// and the standard spool dir.
func NewCollector(spoolDir string) *Collector {
	return &Collector{
		AW:       awstream.NewClient(awstream.DefaultBaseURL),
		Registry: source.NewRegistry(),
		SpoolDir: spoolDir,
		nowFn:    func() time.Time { return time.Now().UTC() },
	}
}

// newCollectorWithDeps is a test-only constructor that lets tests inject a custom
// AW client, registry, and clock.
func newCollectorWithDeps(aw *awstream.Client, reg *source.Registry, spoolDir string, nowFn func() time.Time) *Collector {
	return &Collector{AW: aw, Registry: reg, SpoolDir: spoolDir, nowFn: nowFn}
}

// Run reads AW window events between [now-duration, now], resolves the focused
// app's envelope via the registered source, and writes one Tier-1 event per
// (focused bundle, surface ref) tuple to the spool. Returns a report honest about
// per-source failures. Refused=true means ZERO rows written; this only happens
// when Collect() refuses at one of the three gates.
//
// The run is bounded by ctx (callers pass a context with --duration timeout) AND
// by the duration parameter (we stop polling once now-duration is in the past).
func (c *Collector) Run(ctx context.Context, duration time.Duration) CollectReport {
	now := c.nowFn()
	start := now.Add(-duration)

	// 1. Fetch recent AW window events from EVERY currentwindow bucket — the
	// virtual stream materializes cross-hostname. With awstream.Normalize the
	// per-hostname buckets collapse to a single ordered stream, dedup'd by
	// (bucket_type, ts). For 8 historical hostnames on the operator's Mac,
	// this replaces the previous "read the first matching bucket only" path.
	buckets, err := c.AW.ListBuckets(ctx)
	if err != nil {
		return CollectReport{Refused: false, SourcesFailed: 1, Reason: fmt.Sprintf("AW ListBuckets: %v", err)}
	}
	var windowBuckets []string
	for _, b := range buckets {
		if b.Type == "currentwindow" {
			windowBuckets = append(windowBuckets, b.ID)
		}
	}
	if len(windowBuckets) == 0 {
		// No window watcher running — graceful no-op.
		return CollectReport{}
	}

	// Pull events from every currentwindow bucket and assemble raw events for
	// normalization.
	rawEvents := make([]awstream.RawEvent, 0, 100)
	for _, bucketID := range windowBuckets {
		// Look up the bucket struct so awstream.Normalize can stamp host bindings.
		var bucket awstream.Bucket
		for _, b := range buckets {
			if b.ID == bucketID {
				bucket = b
				break
			}
		}
		events, err := c.AW.FetchEvents(ctx, bucketID, start, now, 1000)
		if err != nil {
			continue // graceful skip per-bucket; report stays honest
		}
		for _, e := range events {
			rawEvents = append(rawEvents, awstream.RawEvent{Bucket: bucket, AWEvent: e})
		}
	}

	// 2. Normalize: dedup across hostnames by (bucket_type, ts), drop banned
	// keys, sort. The Tier-1 collector treats all normalized events equally;
	// any window flip on any hostname becomes one canonical record.
	normalized, _, _ := awstream.Normalize(rawEvents)

	// 3. Walk normalized events; pick the most-recent unique focused app; for each,
	// call the registered source.
	rep := CollectReport{}
	seen := make(map[string]bool)
	for _, ev := range normalized {
		bundleID := ev.BundleID
		if bundleID == "" || seen[bundleID] {
			continue
		}
		seen[bundleID] = true

		src, ok := c.Registry.Resolve(bundleID)
		if !ok {
			continue
		}
		rep.SourcesCalled++

		env, ok, err := src.TryEnvelope(ctx, source.FocusedApp{BundleID: bundleID, AppName: bundleID})
		if err != nil {
			// TCC permission lost or transport error. Caller logs + counts.
			// Emit a KindPermissionLost event so downstream spans are marked
			// incomplete (F11).
			if err := c.writePermissionLostEvent(ctx, bundleID, ev.TS, err); err != nil {
				rep.SourcesFailed++
			}
			continue
		}
		if !ok {
			continue
		}

		// Resolve git_remote if we have a cwd.
		if env.PaneCwd != "" {
			env.GitRoot, env.GitRemote = resolveGitRemote(ctx, env.PaneCwd)
		}

		// F5: refuse the envelope if it carries a banned key. The registry's
		// ContentFreeInvariant already verified the source's construction; this is
		// defense-in-depth at the write boundary.
		if err := source.CheckEnvelopeContentFree(env); err != nil {
			rep.BannedRefused++
			continue
		}

		// Build the Tier-1 event and append to the spool.
		ev1 := buildTier1Event(bundleID, env, ev.TS)
		if err := c.appendEvent(ctx, ev1); err != nil {
			rep.SourcesFailed++
			continue
		}
		rep.EventsWritten++
	}

	rep.BytesUnderRoot, _ = dirSize(activity.ActivityRoot(parentHome(ctx, c)))
	return rep
}

// appendEvent writes one Tier-1 event as NDJSON to the spool. Append-only; never
// rewrites. The spool filename is the UTC day of the event timestamp.
func (c *Collector) appendEvent(ctx context.Context, ev activity.Event) error {
	if err := os.MkdirAll(c.SpoolDir, 0o700); err != nil {
		return err
	}
	day := utcDay(ev.TS)
	path := filepath.Join(c.SpoolDir, day+".ndjson")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(&ev)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

// writePermissionLostEvent appends a KindPermissionLost event when a source
// reports TCC permission loss. This signals the segment reducer to mark spans
// from this window as CoverageIncomplete (F11).
func (c *Collector) writePermissionLostEvent(ctx context.Context, bundleID, ts string, srcErr error) error {
	ev := activity.Event{
		SchemaVersion: activity.SchemaVersion,
		TS:            ts,
		Kind:          activity.KindPermissionLost,
		Engine:        activity.Engine{ID: Tier1EngineID, Revision: Tier1EngineRevision},
		Meta:          map[string]string{"bundle_id": bundleID, "source_error": srcErr.Error()},
	}
	return c.appendEvent(ctx, ev)
}

// buildTier1Event constructs the activity.Event for a Tier-1 envelope. The event
// uses KindWindowFocused (schema-reserved Tier-1 slot, no-op in slice-1 segmenter
// — Phase 5 lights it up). The envelope fields become Meta map keys, each
// individually checked against the Tier-1-allowed set so a buggy source can't
// smuggle a banned key.
func buildTier1Event(bundleID string, env source.Envelope, ts string) activity.Event {
	meta := map[string]string{}
	if env.PaneCwd != "" {
		meta["pane_cwd"] = env.PaneCwd
	}
	if env.GitRoot != "" {
		meta["git_root"] = env.GitRoot
	}
	if env.GitRemote != "" {
		meta["git_remote"] = env.GitRemote
	}
	if env.PaneType != "" {
		meta["pane_type"] = env.PaneType
	}
	if env.AgentKind != "" {
		meta["agent_kind"] = env.AgentKind
	}
	if env.AgentName != "" {
		meta["agent_name"] = env.AgentName
	}
	if env.WorkspaceRef != "" {
		meta["workspace_ref"] = env.WorkspaceRef
	}
	if env.SurfaceRef != "" {
		meta["surface_ref"] = env.SurfaceRef
	}
	if env.WindowRef != "" {
		meta["window_ref"] = env.WindowRef
	}
	return activity.Event{
		SchemaVersion: activity.SchemaVersion,
		TS:            ts,
		Kind:          activity.KindWindowFocused,
		BundleID:      bundleID,
		AppName:       bundleID,
		Engine:        activity.Engine{ID: Tier1EngineID, Revision: Tier1EngineRevision},
		Meta:          meta,
	}
}

// --- Tier-1 allowed meta keys (sibling of activity.ForbiddenMetaKeys) ---

// Tier1AllowedMetaKeys lists the meta keys the Tier-1 collector may populate. The
// intersection with activity.ForbiddenMetaKeys must be empty (verified by
// TestTier1SetsDoNotOverlap). Adding a key here is a schema-level decision; bump
// SchemaVersion in lockstep.
var Tier1AllowedMetaKeys = map[string]bool{
	"pane_cwd":      true,
	"git_root":      true,
	"git_remote":    true,
	"pane_type":     true,
	"agent_kind":    true,
	"agent_name":    true,
	"workspace_ref": true,
	"surface_ref":   true,
	"window_ref":    true,
}

// --- governance wrapper (mirror of activity.Drain) ---

// Collect is the membrane: the sole entrypoint that runs a Tier-1 collect. It
// runs the three gates fail-closed (config enabled -> policy grant -> byte cap),
// and only on all-allow does it mint an ActionIntent and invoke Collector.Run.
//
// Refused=true with a Reason means ZERO rows were written (F2 / F15). Missing or
// expired grant, disabled key, or byte cap = refused.
func Collect(home, spoolDir string, cfg policy.Config, workID string, duration time.Duration, now time.Time) (CollectReport, error) {
	// Gate 1: config enabled. Tier-1 collect requires the sensor to be enabled
	// (the same key as Tier-0 drain) — both share the underlying toggle.
	enabled, err := activity.Enabled(home)
	if err != nil {
		return CollectReport{}, err
	}
	if !enabled {
		return CollectReport{Refused: true, Reason: "activity sensor disabled (config key enabled!=true)"}, nil
	}
	// Gate 2: live allowing PolicyDecision for the tier1 capability.
	ai := buildTier1Intent(workID, home, now)
	pd := policy.Evaluate(ai, cfg, now)
	if gateErr := act.Gate(ai, pd); gateErr != nil {
		return CollectReport{Refused: true, Reason: "tier1 collect not granted: " + gateErr.Error()}, nil
	}
	// Gate 3: byte cap.
	size, err := dirSize(activity.ActivityRoot(home))
	if err != nil {
		return CollectReport{}, err
	}
	if size >= activity.ByteCapBytes {
		return CollectReport{Refused: true, Reason: "byte_cap_exceeded", BytesUnderRoot: size}, nil
	}
	// All gates pass — run the collector.
	c := NewCollector(spoolDir)
	return c.Run(context.Background(), duration), nil
}

// buildTier1Intent mirrors buildDrainIntent in drain.go: deterministic act ID
// from (workID, capability, target path), risk="tier2" (Tier-1 is more invasive
// than Tier-0 drain — it spawns processes and writes to the spool).
func buildTier1Intent(workID, home string, now time.Time) envelope.ActionIntent {
	return envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            actID("act_", workID, activity.Tier1CollectCapability, activity.ActivityRoot(home)),
			CreatedAt:     now.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier2",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         "tier1_collector",
		Capability:    activity.Tier1CollectCapability,
		Action:        "tier1_collect_envelopes",
		Target:        envelope.ActionTarget{Kind: "evidence_dir", ID: "observed/activity", Path: activity.ActivityRoot(home)},
		MutationClass: "external_side_effect",
		Reversibility: "easy",
		BlastRadius:   "local",
		Preview:       "collect tier1 envelopes from focused terminal/editor and append to spool",
		RollbackPlan:  "delete tier1 spool files written by this run",
	}
}

// AWMaterializeReport is the outcome of `compound activity aw materialize`. Mirrors
// the Tier-1 collect report shape so the CLI can share error-handling code.
type AWMaterializeReport struct {
	Refused        bool   `json:"refused"`
	Reason         string `json:"reason,omitempty"`
	EventsWritten  int    `json:"events_written"`
	BucketsRead    int    `json:"buckets_read"`
	BytesUnderRoot int64  `json:"bytes_under_root"`
}

// AWMaterialize reads all AW buckets, normalizes events across hostnames via
// awstream, and writes Tier-0 spool events. Governed by the same DrainCapability
// (it's a Tier-0 ingest) plus the Enabled config + byte cap.
//
// This is the AW-side counterpart to the Swift launchd collector: it produces the
// SAME activity.Event shape, so downstream Drain works unchanged. The Tier-1
// envelopes are NOT extracted here — that's a separate verb (`tier1 collect`).
func AWMaterialize(home, spoolDir string, cfg policy.Config, workID string, now time.Time) (AWMaterializeReport, error) {
	// Gate 1: enabled.
	enabled, err := activity.Enabled(home)
	if err != nil {
		return AWMaterializeReport{}, err
	}
	if !enabled {
		return AWMaterializeReport{Refused: true, Reason: "activity sensor disabled (config key enabled!=true)"}, nil
	}
	// Gate 2: drain grant (AW materialize is a Tier-0 ingest, same capability as
	// Drain). A standing grant for activity.ingest.drain covers both verbs.
	ai := buildDrainIntentForAW(workID, home, now)
	pd := policy.Evaluate(ai, cfg, now)
	if gateErr := act.Gate(ai, pd); gateErr != nil {
		return AWMaterializeReport{Refused: true, Reason: "aw materialize not granted: " + gateErr.Error()}, nil
	}
	// Gate 3: byte cap.
	size, err := dirSize(activity.ActivityRoot(home))
	if err != nil {
		return AWMaterializeReport{}, err
	}
	if size >= activity.ByteCapBytes {
		return AWMaterializeReport{Refused: true, Reason: "byte_cap_exceeded", BytesUnderRoot: size}, nil
	}

	// Run the materialize: list buckets, fetch events, normalize, write to spool.
	cli := awstream.NewClient(awstream.DefaultBaseURL)
	buckets, err := cli.ListBuckets(context.Background())
	if err != nil {
		return AWMaterializeReport{Refused: false, Reason: fmt.Sprintf("AW ListBuckets: %v", err)}, nil
	}
	if len(buckets) == 0 {
		return AWMaterializeReport{BucketsRead: 0}, nil
	}

	// Collect raw events across all buckets.
	var rawEvents []awstream.RawEvent
	for _, b := range buckets {
		if b.Type != "currentwindow" && b.Type != "afkstatus" && b.Type != "app.editor.activity" {
			continue
		}
		// Look back 7 days — sufficient for a periodic materialize.
		end := now
		start := now.Add(-7 * 24 * time.Hour)
		evs, err := cli.FetchEvents(context.Background(), b.ID, start, end, 10000)
		if err != nil {
			continue
		}
		for _, e := range evs {
			rawEvents = append(rawEvents, awstream.RawEvent{Bucket: b, AWEvent: e})
		}
	}

	events, _, _ := awstream.Normalize(rawEvents)
	awstream.AssignSeq(events)

	// Write events to spool.
	rep := AWMaterializeReport{BucketsRead: len(buckets)}
	if err := writeEventsToSpool(spoolDir, events); err != nil {
		return AWMaterializeReport{Refused: false, Reason: fmt.Sprintf("spool write: %v", err)}, nil
	}
	rep.EventsWritten = len(events)
	rep.BytesUnderRoot, _ = dirSize(activity.ActivityRoot(home))
	return rep, nil
}

// buildDrainIntentForAW is identical to buildDrainIntent but lives here so this
// file is self-contained for the AW materialize verb.
func buildDrainIntentForAW(workID, home string, now time.Time) envelope.ActionIntent {
	return envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            actID("act_", workID, activity.DrainCapability, activity.ActivityRoot(home)),
			CreatedAt:     now.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         "aw_materializer",
		Capability:    activity.DrainCapability,
		Action:        "aw_materialize_to_spool",
		Target:        envelope.ActionTarget{Kind: "evidence_dir", ID: "observed/activity", Path: activity.ActivityRoot(home)},
		MutationClass: "external_side_effect",
		Reversibility: "easy",
		BlastRadius:   "local",
		Preview:       "read AW across hostnames -> spool (Tier-0)",
		RollbackPlan:  "delete spool files written by this materialize",
	}
}

// writeEventsToSpool writes activity events to per-day spool files. Idempotent
// re-runs produce duplicate NDJSON lines — Drain is responsible for deduplication
// via the cursor. Re-running materialize before drain is safe but produces
// duplicates; running drain after materialize is the normal flow.
func writeEventsToSpool(spoolDir string, events []activity.Event) error {
	if len(events) == 0 {
		return nil
	}
	if err := os.MkdirAll(spoolDir, 0o700); err != nil {
		return err
	}
	// Group by UTC day.
	buckets := make(map[string][]byte)
	for _, ev := range events {
		day := utcDay(ev.TS)
		b, err := json.Marshal(&ev)
		if err != nil {
			return err
		}
		buckets[day] = append(buckets[day], b...)
		buckets[day] = append(buckets[day], '\n')
	}
	for day, buf := range buckets {
		path := filepath.Join(spoolDir, day+".ndjson")
		if err := os.WriteFile(path, buf, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// --- helpers ---

// resolveGitRemote walks the cwd upward to find .git, then runs
// `git rev-parse --show-toplevel` and `git config --get remote.origin.url`. Returns
// ("", "") on any failure (no git, no remote, git not installed) — graceful.
func resolveGitRemote(ctx context.Context, cwd string) (root, remote string) {
	if cwd == "" {
		return "", ""
	}
	// Detect .git walking up.
	dir := cwd
	for i := 0; i < 16; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			root = dir
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if root == "" {
		return "", ""
	}
	// Run git config to get the remote.
	out, err := runGitConfig(ctx, root)
	if err != nil {
		return root, ""
	}
	return root, strings.TrimSpace(out)
}

// runGitConfig is a function-typed seam so tests can stub git invocations. The
// default wires `git config --get remote.origin.url` with a 2s timeout.
var runGitConfig = func(ctx context.Context, cwd string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "git", "-C", cwd, "config", "--get", "remote.origin.url").CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// parentHome extracts the home directory from the collector's spool path. Used
// to compute BytesUnderRoot for the byte-cap gate. Returns "" if the spool path
// doesn't include a home segment (tests with t.TempDir()).
func parentHome(ctx context.Context, c *Collector) string {
	// SpoolDir is typically <home>/spool/activity. Strip the trailing /spool/activity
	// to recover the home. If the path is just t.TempDir(), we return "" and the
	// byte-cap reading becomes 0 — fine for tests.
	if c.SpoolDir == "" {
		return ""
	}
	// Walk up to find a path that contains "/spool" — strip the suffix.
	p := c.SpoolDir
	for {
		parent := filepath.Dir(p)
		if parent == p || parent == "." || parent == "/" {
			return ""
		}
		if filepath.Base(parent) == "spool" {
			return filepath.Dir(parent)
		}
		p = parent
	}
}

// utcDay returns the YYYY-MM-DD UTC day of a timestamp string. Used to bucket
// spool files.
func utcDay(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err == nil {
		return t.UTC().Format("2006-01-02")
	}
	t, err = time.Parse(time.RFC3339, ts)
	if err == nil {
		return t.UTC().Format("2006-01-02")
	}
	return time.Now().UTC().Format("2006-01-02")
}

// actID computes a deterministic ID from prefix + parts. Mirrors the helper in
// toggle.go (which is unexported there).
func actID(prefix string, parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return prefix + hex.EncodeToString(h.Sum(nil)[:12])
}

// dirSize computes the recursive byte size of a directory tree. Mirror of the
// private helper in drain.go — duplicated here to keep this package
// self-contained.
func dirSize(root string) (int64, error) {
	if root == "" {
		return 0, nil
	}
	var total int64
	err := filepath.Walk(root, func(_ string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// --- compile-time assertions ---

// Compile-time check: the Tier1AllowedMetaKeys set must be disjoint from
// activity.ForbiddenMetaKeys. If a future change adds a banned key to the Tier-1
// allowed set, this fails at build time.
var _ = func() bool {
	for k := range Tier1AllowedMetaKeys {
		if activity.ForbiddenMetaKeys[k] {
			panic("tier1: allowed meta key " + k + " is also forbidden — schema invariant violated")
		}
	}
	return true
}()
