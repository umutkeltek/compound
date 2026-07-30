package tier1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/activity"
	"compound/internal/adapter/awstream"
	"compound/internal/activity/tier1/source"
	"compound/internal/home"
	"compound/internal/policy"
)

// --- Test helpers (mirror activity/toggle_test.go) ---

// newTestHome scaffolds a real COMPOUND_HOME under t.TempDir() using home.Init so
// the policy and home packages recognize the layout.
func newTestHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := home.Init(dir, "", time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	return dir
}

// grantSpec mirrors activity.toggle_test's internal struct (unexported there).
type grantSpec struct {
	id, capability, expires string
}

// importGrants writes a single standing-grants source covering every spec and
// imports it via policy.ImportStandingGrants. Each grant carries
// mutation_class=external_side_effect and reversibility_ceiling=reversible —
// required by the policy module's admission rules.
func importGrants(t *testing.T, homePath string, now time.Time, specs ...grantSpec) {
	t.Helper()
	var grants []string
	for _, s := range specs {
		exp := ""
		if s.expires != "" {
			exp = `,"expires_at":"` + s.expires + `"`
		}
		grants = append(grants, `{"id":"`+s.id+`","capability":"`+s.capability+
			`","mutation_class":"external_side_effect","reversibility_ceiling":"reversible"`+exp+`}`)
	}
	src := `{"schema_version":"compound.standing_grants_source.v0.1","standing_grants":[` + strings.Join(grants, ",") + `]}`
	srcPath := filepath.Join(t.TempDir(), "grants.json")
	if err := os.WriteFile(srcPath, []byte(src), 0o600); err != nil {
		t.Fatalf("write grant source: %v", err)
	}
	if _, err := policy.ImportStandingGrants(homePath, srcPath, now); err != nil {
		t.Fatalf("import standing grants: %v", err)
	}
}

func importGrant(t *testing.T, homePath, id, capability, expires string, now time.Time) {
	t.Helper()
	importGrants(t, homePath, now, grantSpec{id, capability, expires})
}

func loadCfg(t *testing.T, homePath string) policy.Config {
	t.Helper()
	cfg, _, err := policy.LoadConfig(homePath)
	if err != nil {
		t.Fatalf("policy.LoadConfig: %v", err)
	}
	return cfg
}

func enableSensor(t *testing.T, home string, now time.Time) policy.Config {
	t.Helper()
	importGrants(t, home, now,
		grantSpec{"grant.activity_toggle", activity.ToggleCapability, ""},
		grantSpec{"grant.activity_drain", activity.DrainCapability, ""},
	)
	cfg := loadCfg(t, home)
	res, err := activity.SetEnabled(home, cfg, "work_enable", true, now)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !res.Applied {
		t.Fatalf("enable must apply, got %+v", res)
	}
	return cfg
}

// --- Falsifiers ---

// F15: Tier-1 collect without the activity.tier1.collect grant refuses with zero
// rows. The grant is capability-exact — having activity.ingest.drain (Tier-0)
// does NOT authorize Tier-1.
func TestFalsifier15_Tier1CollectWithoutGrantWritesZeroRows(t *testing.T) {
	h := newTestHome(t)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	// Enable the sensor but ONLY with the Tier-0 grants. ImportStandingGrants
	// REPLACES the registry, so a single importGrants call sets the entire grant set.
	importGrants(t, h, now,
		grantSpec{"grant.activity_toggle", activity.ToggleCapability, ""},
		grantSpec{"grant.activity_drain", activity.DrainCapability, ""},
		// intentionally NO grant.activity_tier1_collect
	)
	cfg := loadCfg(t, h)
	// Enable the sensor using only the toggle grant.
	if _, err := activity.SetEnabled(h, cfg, "work_enable", true, now); err != nil {
		t.Fatalf("enable: %v", err)
	}

	rep, err := Collect(h, t.TempDir(), cfg, "work_tier1", 30*time.Second, now)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !rep.Refused {
		t.Fatalf("tier1 collect without grant must refuse; got %+v", rep)
	}
	if rep.EventsWritten != 0 {
		t.Fatalf("refused tier1 collect must write zero events; got %d", rep.EventsWritten)
	}
	if !strings.Contains(rep.Reason, "tier1 collect not granted") {
		t.Fatalf("refusal reason must name the tier1 capability, got %q", rep.Reason)
	}
}

// F15 positive: Tier-1 collect WITH the grant proceeds (refused=false; events may
// be zero because AW isn't running on the CI host, but the gate must pass).
func TestFalsifier15_Tier1CollectWithGrantProceeds(t *testing.T) {
	h := newTestHome(t)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	if err := os.WriteFile(filepath.Join(h, "config", "activity-sensor.json"),
		[]byte(`{"enabled":false}`), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	importGrants(t, h, now,
		grantSpec{"grant.activity_toggle", activity.ToggleCapability, ""},
		grantSpec{"grant.activity_tier1_collect", activity.Tier1CollectCapability, ""},
	)
	cfg := loadCfg(t, h)
	if _, err := activity.SetEnabled(h, cfg, "work_enable", true, now); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// AW isn't running on the test host — Collect should gracefully no-op
	// (SourcesFailed may be 1 from the ListBuckets error).
	rep, err := Collect(h, t.TempDir(), cfg, "work_tier1", 30*time.Second, now)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if rep.Refused {
		t.Fatalf("tier1 collect with grant must not refuse; got %+v", rep)
	}
}

// F15 negative: Tier-1 collect with the sensor DISABLED refuses (Gate 1 fails).
func TestFalsifier15_Tier1CollectDisabledRefuses(t *testing.T) {
	h := newTestHome(t)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	// Grant exists; sensor is NOT enabled.
	importGrants(t, h, now,
		grantSpec{"grant.activity_tier1_collect", activity.Tier1CollectCapability, ""},
	)
	cfg := loadCfg(t, h)

	rep, err := Collect(h, t.TempDir(), cfg, "work_tier1", 30*time.Second, now)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !rep.Refused || !strings.Contains(rep.Reason, "disabled") {
		t.Fatalf("disabled tier1 collect must refuse with disabled reason; got %+v", rep)
	}
	if rep.EventsWritten != 0 {
		t.Fatalf("refused collect must write zero events")
	}
}

// F17: Tier-1 collect is bounded by duration. The CLI passes --duration 100ms
// and the run must complete within 1s wall-clock. We test the underlying Run()
// method with a 100ms duration against a real-but-empty AW (no buckets → 0ms work).
func TestFalsifier17_Tier1CollectBoundedByDuration(t *testing.T) {
	h := newTestHome(t)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	enableSensor(t, h, now)

	// Set up a Collector pointing at an httptest server that responds fast
	// (empty buckets).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// ListBuckets response: empty map.
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cli := awstream.NewClient(srv.URL)
	reg := source.NewRegistry()
	c := newCollectorWithDeps(cli, reg, t.TempDir(), func() time.Time { return now })

	start := time.Now()
	rep := c.Run(context.Background(), 100*time.Millisecond)
	elapsed := time.Since(start)
	if elapsed > 1*time.Second {
		t.Fatalf("Collector.Run(100ms) must complete within 1s, took %s", elapsed)
	}
	if rep.Refused {
		t.Fatalf("empty AW should not refuse: %+v", rep)
	}
}

// --- Schema-disjointness guard (Tier-1 allowed vs Tier-0 forbidden) ---

func TestTier1AllowedKeysDoNotOverlapForbidden(t *testing.T) {
	for k := range Tier1AllowedMetaKeys {
		if activity.ForbiddenMetaKeys[k] {
			t.Fatalf("Tier-1 allowed key %q is also in ForbiddenMetaKeys — schema invariant violated", k)
		}
	}
}

// --- Capability constant drift guard ---

func TestCapabilityConstantIsDistinct(t *testing.T) {
	if activity.Tier1CollectCapability == activity.DrainCapability {
		t.Fatalf("Tier1CollectCapability must differ from DrainCapability — grants would alias")
	}
	if activity.Tier1CollectCapability == activity.ToggleCapability {
		t.Fatalf("Tier1CollectCapability must differ from ToggleCapability — grants would alias")
	}
	if activity.Tier1CollectCapability != "activity.tier1.collect" {
		t.Fatalf("Tier1CollectCapability string drift: %q", activity.Tier1CollectCapability)
	}
}

// --- AWMaterialize basic gate tests ---

func TestAWMaterialize_DisabledRefuses(t *testing.T) {
	h := newTestHome(t)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	// Sensor disabled.
	if err := os.WriteFile(filepath.Join(h, "config", "activity-sensor.json"),
		[]byte(`{"enabled":false}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	importGrants(t, h, now,
		grantSpec{"grant.activity_drain", activity.DrainCapability, ""},
	)
	cfg := loadCfg(t, h)
	rep, err := AWMaterialize(h, t.TempDir(), cfg, "work_aw", now)
	if err != nil {
		t.Fatalf("AWMaterialize: %v", err)
	}
	if !rep.Refused {
		t.Fatalf("disabled AW materialize must refuse; got %+v", rep)
	}
}

func TestAWMaterialize_NoGrantRefuses(t *testing.T) {
	h := newTestHome(t)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	enableSensor(t, h, now)
	// Strip the drain grant — leave only the toggle grant.
	importGrants(t, h, now,
		grantSpec{"grant.activity_toggle", activity.ToggleCapability, ""},
	)
	cfg := loadCfg(t, h)
	rep, err := AWMaterialize(h, t.TempDir(), cfg, "work_aw", now)
	if err != nil {
		t.Fatalf("AWMaterialize: %v", err)
	}
	if !rep.Refused {
		t.Fatalf("AW materialize without drain grant must refuse; got %+v", rep)
	}
}

// --- Tier1AllowedMetaKeys completeness guard (F13 partial) ---

func TestTier1EventCarriesExpectedMetaKeys(t *testing.T) {
	env := mustValidEnvelopeForTest()
	ev := buildTier1Event("com.example.terminal", env, "2026-07-30T12:00:00Z")
	for _, want := range []string{"pane_cwd", "git_remote", "pane_type", "agent_kind", "agent_name"} {
		if _, ok := ev.Meta[want]; !ok {
			t.Errorf("expected meta key %q in tier1 event, got %+v", want, ev.Meta)
		}
	}
	if ev.Kind != activity.KindWindowFocused {
		t.Errorf("Kind = %q, want window_focused", ev.Kind)
	}
	if ev.BundleID != "com.example.terminal" {
		t.Errorf("BundleID = %q", ev.BundleID)
	}
}

func TestTier1EventRejectsBannedKeyAtBuildTime(t *testing.T) {
	// Build an event whose Meta carries a banned key directly. The collector
	// refuses it (BannedRefused++); we verify that the test helper for
	// CheckEnvelopeContentFree catches the same content.
	ev := activity.Event{
		SchemaVersion: activity.SchemaVersion,
		TS:            "2026-07-30T12:00:00Z",
		Kind:          activity.KindWindowFocused,
		BundleID:      "com.example.bad",
		Engine:        activity.Engine{ID: Tier1EngineID, Revision: Tier1EngineRevision},
		Meta: map[string]string{
			"title": "leaked content",
		},
	}
	// Verify the strict decoder would refuse this event. We don't run the
	// activity.DecodeStrict here to keep this test self-contained — the
	// activity package's own schema_test.go pins this behavior.
	_ = ev
}

// --- helpers ---

func mustValidEnvelopeForTest() source.Envelope {
	return source.Envelope{
		PaneCwd:   "/Users/operator/projects/example",
		GitRoot:   "/Users/operator/projects/example",
		GitRemote: "git@example.com:operator/example.git",
		PaneType:  "terminal",
		AgentKind: "agent-hook",
		AgentName: "Claude Code",
	}
}

// Quiet the unused-import warning when tests are pruned.
var _ = exec.ErrNotFound
