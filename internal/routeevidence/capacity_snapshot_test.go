package routeevidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/work"
)

var capacityNow = time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)

func capacityFixtureConfig(t *testing.T) LaneCapacityConfig {
	t.Helper()
	dir := t.TempDir()
	return LaneCapacityConfig{
		Paths: LaneCapacityPaths{
			UsageHistoryPath:      filepath.Join(dir, "usage-history.jsonl"),
			QuotaCachePath:        filepath.Join(dir, "quota-cache.json"),
			AccountProjectionPath: filepath.Join(dir, "account-projection.json"),
			AliasesPath:           filepath.Join(dir, "lane-aliases.json"),
		},
		ExpectedMinutes: 60,
	}
}

// writeLaneAliasesFile writes the operator-owned lane-alias override projection
// (`{"schema_version":"compound.lane_aliases.v0.1","aliases":{...}}`) to the fixture config's
// AliasesPath.
func writeLaneAliasesFile(t *testing.T, cfg LaneCapacityConfig, aliases map[string]string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"schema_version": "compound.lane_aliases.v0.1",
		"aliases":        aliases,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.Paths.AliasesPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// appendCapacityUsageRow appends a second (or later) CodexBar usage row to an existing fixture
// file, used when a test needs rows for two different provider families in one file.
func appendCapacityUsageRow(t *testing.T, cfg LaneCapacityConfig, provider string, used float64, resetsAt, sampledAt time.Time) {
	t.Helper()
	row := map[string]any{
		"provider":      provider,
		"windowKind":    "5h",
		"windowMinutes": 300,
		"usedPercent":   used,
		"resetsAt":      resetsAt.UTC().Format(time.RFC3339),
		"sampledAt":     sampledAt.UTC().Format(time.RFC3339),
	}
	body, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(cfg.Paths.UsageHistoryPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(body, '\n')); err != nil {
		t.Fatal(err)
	}
}

func writeCapacityUsage(t *testing.T, cfg LaneCapacityConfig, provider string, used float64, resetsAt, sampledAt time.Time) {
	t.Helper()
	row := map[string]any{
		"provider":      provider,
		"windowKind":    "5h",
		"windowMinutes": 300,
		"usedPercent":   used,
		"resetsAt":      resetsAt.UTC().Format(time.RFC3339),
		"sampledAt":     sampledAt.UTC().Format(time.RFC3339),
	}
	body, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.Paths.UsageHistoryPath, append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeQuotaCache(t *testing.T, cfg LaneCapacityConfig, status int, modifiedAt time.Time) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"byAccountId": map[string]any{
			"fixture-org": map[string]any{"status": status},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.Paths.QuotaCachePath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(cfg.Paths.QuotaCachePath, modifiedAt, modifiedAt); err != nil {
		t.Fatal(err)
	}
}

func writeDeadAccountProjection(t *testing.T, cfg LaneCapacityConfig, lane string, observedAt time.Time) {
	t.Helper()
	projection := AccountProjection{
		SchemaVersion:  AccountProjectionSchema,
		ImportedAt:     observedAt.UTC().Format(time.RFC3339),
		SourceRevision: "fixture-dead",
		Providers: map[string]AccountProjectionEntry{
			lane: {
				Provider:            lane,
				HealthyAccountCount: 0,
				TotalAccountCount:   1,
				FreshestSampledAt:   observedAt.UTC().Format(time.RFC3339),
				SourceRevision:      "fixture-dead",
				ImportedAt:          observedAt.UTC().Format(time.RFC3339),
			},
		},
	}
	body, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.Paths.AccountProjectionPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func installCapacityGuard(t *testing.T, cfg LaneCapacityConfig) {
	t.Helper()
	previous := work.LaneCapacityGuard
	work.LaneCapacityGuard = WorkLaneCapacityGuard(cfg)
	t.Cleanup(func() { work.LaneCapacityGuard = previous })
}

func readyDispatchStore(t *testing.T, home string) *work.Store {
	t.Helper()
	store, err := work.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(work.CreateInput{
		Owner:            "fixture",
		SuccessPredicate: "capacity permits dispatch",
		DesiredOutcome:   "exercise capacity veto",
		State:            "ready",
	}, capacityNow.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	return store
}

// f1: a fresh, exhausted five-hour CodexBar row must veto the lane at the dispatch seam and
// cite both the exact row timestamp and the routing-default alternative ladder.
func TestCapacityF1DispatchPlanRefusesFreshExhaustedCodex(t *testing.T) {
	home := t.TempDir()
	cfg := capacityFixtureConfig(t)
	sampledAt := capacityNow.Add(-5 * time.Minute)
	writeCapacityUsage(t, cfg, "codex", 100, capacityNow.Add(2*time.Hour), sampledAt)
	accountAdmissionRouteChain(t, home, []string{"codex/gpt", "zai/lane", "switchback/fallback"})
	installCapacityGuard(t, cfg)

	plan, err := readyDispatchStore(t, home).DispatchPlan(capacityNow, work.DispatchPlanOptions{
		Actor:           "codex",
		CapabilityClass: "build-implement",
	})
	if err != nil {
		t.Fatalf("dispatch plan: %v", err)
	}
	if plan.Status != "capacity_refused" || plan.Selected != nil {
		t.Fatalf("fresh exhausted codex lane was offered: %+v", plan)
	}
	reason := plan.NoAction
	for _, want := range []string{"lane codex", "state=walled", sampledAt.Format(time.RFC3339), "alternative_ladder", "zai/lane"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("refusal %q does not cite %q", reason, want)
		}
	}
}

// f2: lack of source rows while a capacity source IS wired is an explicit UNKNOWN, and dispatch
// cannot offer that lane until the caller supplies --confirm. Confirmation admits the same lane
// without inventing health. (The fully-unwired home fails OPEN — see
// TestCapacityUnknownWithoutAnySourceFailsOpen.)
func TestCapacityF2UnknownZaiRequiresConfirm(t *testing.T) {
	home := t.TempDir()
	cfg := capacityFixtureConfig(t)
	writeCapacityUsage(t, cfg, "codex", 10, capacityNow.Add(2*time.Hour), capacityNow.Add(-5*time.Minute))
	installCapacityGuard(t, cfg)
	store := readyDispatchStore(t, home)

	plan, err := store.DispatchPlan(capacityNow, work.DispatchPlanOptions{Actor: "zai"})
	if err != nil {
		t.Fatalf("dispatch plan: %v", err)
	}
	if plan.Status != "capacity_confirmation_required" || plan.Selected != nil || !strings.Contains(plan.NoAction, "--confirm") {
		t.Fatalf("unknown zai lane did not require confirmation: %+v", plan)
	}
	if plan.LaneCapacity == nil || plan.LaneCapacity.State != string(CapacityUnknown) {
		t.Fatalf("unknown capacity was not annotated: %+v", plan.LaneCapacity)
	}

	confirmed, err := store.DispatchPlan(capacityNow, work.DispatchPlanOptions{Actor: "zai", Confirm: true})
	if err != nil {
		t.Fatalf("confirmed dispatch plan: %v", err)
	}
	if confirmed.Status != "ready_to_claim" || confirmed.Selected == nil || confirmed.LaneCapacity == nil || confirmed.LaneCapacity.State != string(CapacityUnknown) {
		t.Fatalf("confirmed unknown lane was not admitted with its annotation: %+v", confirmed)
	}
}

// Fail-open when UNWIRED: a home with no capacity source installed at all (fresh adopter — no
// CodexBar, no quota cache, no account projection) dispatches with a warning annotation instead
// of demanding --confirm forever. This is the account-projection fail-open precedent applied to
// the veto. Wired-but-blind (a source file exists, the lane has no rows) still requires
// confirmation — the empty-file mutation below proves the distinction has teeth.
func TestCapacityUnknownWithoutAnySourceFailsOpen(t *testing.T) {
	home := t.TempDir()
	cfg := capacityFixtureConfig(t)
	installCapacityGuard(t, cfg)

	plan, err := readyDispatchStore(t, home).DispatchPlan(capacityNow, work.DispatchPlanOptions{Actor: "zai"})
	if err != nil {
		t.Fatalf("dispatch plan: %v", err)
	}
	if plan.Status != "ready_to_claim" || plan.Selected == nil {
		t.Fatalf("unwired home did not fail open: %+v", plan)
	}
	if plan.LaneCapacity == nil || plan.LaneCapacity.State != string(CapacityUnknown) {
		t.Fatalf("unwired capacity was not annotated unknown: %+v", plan.LaneCapacity)
	}
	warned := false
	for _, w := range plan.Warnings {
		if strings.Contains(w, "no capacity source installed") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("unwired dispatch missing warning: %+v", plan.Warnings)
	}

	if err := os.WriteFile(cfg.Paths.UsageHistoryPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	blind, err := readyDispatchStore(t, t.TempDir()).DispatchPlan(capacityNow, work.DispatchPlanOptions{Actor: "zai"})
	if err != nil {
		t.Fatalf("wired-blind dispatch plan: %v", err)
	}
	if blind.Status != "capacity_confirmation_required" || blind.Selected != nil {
		t.Fatalf("wired-but-blind lane did not require confirmation: %+v", blind)
	}
}

// f3: a quota-cache 429 older than six hours is historical evidence, never a current wall.
func TestCapacityF3Stale429DoesNotRefuse(t *testing.T) {
	cfg := capacityFixtureConfig(t)
	writeQuotaCache(t, cfg, 429, capacityNow.Add(-6*time.Hour-time.Second))

	capacity, err := ReadLaneCapacityWithConfig("codex", capacityNow, cfg)
	if err != nil {
		t.Fatalf("read lane capacity: %v", err)
	}
	if capacity.State == CapacityWalled || capacity.State == CapacityDead {
		t.Fatalf("stale 429 became a live refusal: %+v", capacity)
	}
	if capacity.State != CapacityUnknown {
		t.Fatalf("stale 429 with no live usage row should leave capacity unknown: %+v", capacity)
	}
}

// f4: a hard refusal goes through the canonical execution-route-evidence ledger exactly once;
// an allowed lane writes no route-evidence row.
func TestCapacityF4RefusalRecordsExactlyOneRouteEvidenceAndAllowRecordsNone(t *testing.T) {
	t.Run("refusal", func(t *testing.T) {
		home := t.TempDir()
		cfg := capacityFixtureConfig(t)
		writeCapacityUsage(t, cfg, "codex", 100, capacityNow.Add(2*time.Hour), capacityNow.Add(-5*time.Minute))
		installCapacityGuard(t, cfg)
		store := readyDispatchStore(t, home)

		for i := 0; i < 2; i++ {
			plan, err := store.DispatchPlan(capacityNow, work.DispatchPlanOptions{Actor: "codex"})
			if err != nil {
				t.Fatalf("dispatch refusal %d: %v", i+1, err)
			}
			if plan.Status != "capacity_refused" {
				t.Fatalf("dispatch %d was not refused: %+v", i+1, plan)
			}
		}

		body, err := os.ReadFile(ExecutionRouteEvidenceLedgerPath(home))
		if err != nil {
			t.Fatalf("read route evidence: %v", err)
		}
		lines := strings.FieldsFunc(strings.TrimSpace(string(body)), func(r rune) bool { return r == '\n' })
		if len(lines) != 1 {
			t.Fatalf("refusal wrote %d rows, want exactly one:\n%s", len(lines), body)
		}
		evidence, err := Decode([]byte(lines[0]))
		if err != nil {
			t.Fatalf("refusal row is not schema-valid route evidence: %v", err)
		}
		if evidence.Status != "capacity_refused" || evidence.SelectedTarget.Provider != "codex" {
			t.Fatalf("wrong refusal evidence: %+v", evidence)
		}
	})

	t.Run("allow", func(t *testing.T) {
		home := t.TempDir()
		cfg := capacityFixtureConfig(t)
		writeCapacityUsage(t, cfg, "codex", 25, capacityNow.Add(2*time.Hour), capacityNow.Add(-5*time.Minute))
		installCapacityGuard(t, cfg)

		plan, err := readyDispatchStore(t, home).DispatchPlan(capacityNow, work.DispatchPlanOptions{Actor: "codex"})
		if err != nil {
			t.Fatalf("dispatch allow: %v", err)
		}
		if plan.Status != "ready_to_claim" || plan.Selected == nil {
			t.Fatalf("alive lane was not allowed: %+v", plan)
		}
		if _, err := os.Stat(ExecutionRouteEvidenceLedgerPath(home)); !os.IsNotExist(err) {
			t.Fatalf("allowed lane wrote route evidence: %v", err)
		}
	})
}

func TestReadLaneCapacityFresh429IsWalled(t *testing.T) {
	cfg := capacityFixtureConfig(t)
	writeQuotaCache(t, cfg, 429, capacityNow.Add(-time.Hour))

	capacity, err := ReadLaneCapacityWithConfig("codex", capacityNow, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if capacity.State != CapacityWalled || !capacity.LastProven.Equal(capacityNow.Add(-time.Hour)) {
		t.Fatalf("fresh 429 did not produce a timestamped wall: %+v", capacity)
	}
}

func TestReadLaneCapacityDeadAccountProjectionFailsClosed(t *testing.T) {
	cfg := capacityFixtureConfig(t)
	observedAt := capacityNow.Add(-10 * time.Minute)
	writeDeadAccountProjection(t, cfg, "zai", observedAt)

	capacity, err := ReadLaneCapacityWithConfig("zai", capacityNow, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if capacity.State != CapacityDead || !capacity.LastProven.Equal(observedAt) {
		t.Fatalf("dead projected account did not fail closed: %+v", capacity)
	}
}

func TestReadLaneCapacityResetInsideExpectedWindowIsExpiring(t *testing.T) {
	cfg := capacityFixtureConfig(t)
	reset := capacityNow.Add(30 * time.Minute)
	writeCapacityUsage(t, cfg, "codex", 20, reset, capacityNow.Add(-5*time.Minute))

	capacity, err := ReadLaneCapacityWithConfig("codex", capacityNow, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if capacity.State != CapacityExpiring || !capacity.ResetsAt.Equal(reset) {
		t.Fatalf("reset inside expected window was not expiring: %+v", capacity)
	}
}

func TestPlanCapabilityRouteFiltersAndAnnotatesCapacityWithoutSelecting(t *testing.T) {
	home := t.TempDir()
	cfg := capacityFixtureConfig(t)
	writeCapacityUsage(t, cfg, "codex", 100, capacityNow.Add(2*time.Hour), capacityNow.Add(-5*time.Minute))
	accountAdmissionRouteChain(t, home, []string{"codex/gpt", "zai/lane", "switchback/fallback"})

	plan, err := PlanCapabilityRoute(home, "build-implement", CapabilityRoutePlanOptions{
		CapacityConfig: &cfg,
	}, capacityNow)
	if err != nil {
		t.Fatalf("plan route: %v", err)
	}
	if _, ok := laneByID(plan.Chain, "codex/gpt"); ok {
		t.Fatalf("walled codex lane remained offerable: %+v", plan.Chain)
	}
	codex, ok := laneByID(plan.Omitted, "codex/gpt")
	if !ok || codex.Capacity == nil || codex.Capacity.State != CapacityWalled {
		t.Fatalf("walled codex lane was not retained with capacity annotation: %+v", plan.Omitted)
	}
	if !strings.Contains(codex.OmitReason, "alternative_ladder") || !strings.Contains(codex.OmitReason, "zai/lane") {
		t.Fatalf("capacity omit reason lacks routing-default ladder: %q", codex.OmitReason)
	}
	if !plan.CapacityConfirmationRequired {
		t.Fatalf("unknown zai lane must make route confirmation-required: %+v", plan)
	}
	if fallback, ok := laneByID(plan.Chain, "switchback/fallback"); !ok || fallback.Capacity != nil {
		t.Fatalf("ungated fallback should remain offerable without invented capacity: %+v", plan.Chain)
	}

	body, err := os.ReadFile(ExecutionRouteEvidenceLedgerPath(home))
	if err != nil {
		t.Fatalf("read refusal evidence: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 1 {
		t.Fatalf("one capacity-filtered lane wrote %d refusal rows: %s", len(lines), body)
	}
	if _, err := Decode([]byte(lines[0])); err != nil {
		t.Fatalf("planner refusal row invalid: %v", err)
	}
}

func TestPlanCapabilityRouteUnknownLaneRequiresAndHonorsConfirm(t *testing.T) {
	home := t.TempDir()
	cfg := capacityFixtureConfig(t)
	writeCapacityUsage(t, cfg, "codex", 10, capacityNow.Add(2*time.Hour), capacityNow.Add(-5*time.Minute))
	accountAdmissionRouteChain(t, home, []string{"zai/lane", "switchback/fallback"})

	held, err := PlanCapabilityRoute(home, "build-implement", CapabilityRoutePlanOptions{
		CapacityConfig: &cfg,
	}, capacityNow)
	if err != nil {
		t.Fatalf("plan unconfirmed: %v", err)
	}
	if !held.CapacityConfirmationRequired {
		t.Fatalf("unknown lane did not require confirmation: %+v", held)
	}
	zaiHeld, ok := laneByID(held.Omitted, "zai/lane")
	if !ok || zaiHeld.Capacity == nil || zaiHeld.Capacity.State != CapacityUnknown || !strings.Contains(zaiHeld.OmitReason, "--confirm required") {
		t.Fatalf("unknown lane was not held with evidence: %+v", held.Omitted)
	}

	confirmed, err := PlanCapabilityRoute(home, "build-implement", CapabilityRoutePlanOptions{
		CapacityConfig: &cfg,
		Confirm:        true,
	}, capacityNow)
	if err != nil {
		t.Fatalf("plan confirmed: %v", err)
	}
	zaiAllowed, ok := laneByID(confirmed.Chain, "zai/lane")
	if !ok || zaiAllowed.Capacity == nil || zaiAllowed.Capacity.State != CapacityUnknown {
		t.Fatalf("confirmed unknown lane was not admitted with annotation: %+v", confirmed)
	}
	if _, err := os.Stat(ExecutionRouteEvidenceLedgerPath(home)); !os.IsNotExist(err) {
		t.Fatalf("unknown confirmation wrote hard-refusal route evidence: %v", err)
	}
}

// --- lane-capacity-alias resolution (packet-s1-lane-capacity-alias, work_416bb6b78bb6) ---

// Structural: real dispatch lane names (sub-lane suffixes, operator-chosen prefixes) must resolve
// to their capacity family, with zai/glm taking precedence over a claude prefix, and truly unknown
// names staying unknown. Existing exact names are a regression guard.
func TestNormalizeLaneCapacityKeyStructuralInferenceAndPrecedence(t *testing.T) {
	cases := []struct {
		name string
		lane string
		want string
	}{
		{"prefix-claude", "claude-main", "claude"},
		{"prefix-claude-sub-lane", "claude-main:sub-lane", "claude"},
		{"prefix-claude-opus", "claude-opus-x", "claude"},
		{"sub-lane-codex", "codex:root-1:candidate-x", "codex"},
		{"zai-beats-claude-prefix", "claude-zai", "zai"},
		{"zai-beats-claude-prefix-sub-lane", "claude-zai:worker-2", "zai"},
		{"unknown-stays-unknown", "synthesis", ""},
		{"exact-codex", "codex", "codex"},
		{"exact-claude", "claude", "claude"},
		{"exact-claude-code", "claude-code", "claude"},
		{"exact-anthropic", "anthropic", "claude"},
		{"exact-zai", "zai", "zai"},
		{"exact-z-dot-ai", "z.ai", "zai"},
		{"exact-glm", "glm", "zai"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeLaneCapacityKey(tc.lane); got != tc.want {
				t.Fatalf("normalizeLaneCapacityKey(%q) = %q, want %q", tc.lane, got, tc.want)
			}
		})
	}
}

// Alias override: an unresolvable lane name pointed at a family via the operator-owned alias
// file resolves to that family's live capacity row.
func TestReadLaneCapacityAliasOverrideResolvesUnknownLane(t *testing.T) {
	cfg := capacityFixtureConfig(t)
	writeLaneAliasesFile(t, cfg, map[string]string{"special-lane": "zai"})
	writeCapacityUsage(t, cfg, "zai", 20, capacityNow.Add(2*time.Hour), capacityNow.Add(-5*time.Minute))

	capacity, err := ReadLaneCapacityWithConfig("special-lane", capacityNow, cfg)
	if err != nil {
		t.Fatalf("read lane capacity: %v", err)
	}
	if capacity.State == CapacityUnknown {
		t.Fatalf("alias override did not resolve special-lane to the zai row: %+v", capacity)
	}
	if !strings.Contains(capacity.DataRef, "provider=zai") {
		t.Fatalf("resolved capacity is not the zai fixture row: %+v", capacity)
	}
}

// Alias override beats prefix inference: an alias pinning "claude-main" to codex must win over
// the structural claude-prefix rule, given fresh rows for both families.
func TestReadLaneCapacityAliasOverrideBeatsPrefixInference(t *testing.T) {
	cfg := capacityFixtureConfig(t)
	writeLaneAliasesFile(t, cfg, map[string]string{"claude-main": "codex"})
	writeCapacityUsage(t, cfg, "claude", 5, capacityNow.Add(2*time.Hour), capacityNow.Add(-5*time.Minute))
	appendCapacityUsageRow(t, cfg, "codex", 40, capacityNow.Add(2*time.Hour), capacityNow.Add(-5*time.Minute))

	capacity, err := ReadLaneCapacityWithConfig("claude-main", capacityNow, cfg)
	if err != nil {
		t.Fatalf("read lane capacity: %v", err)
	}
	if !strings.Contains(capacity.DataRef, "provider=codex") {
		t.Fatalf("alias override did not beat prefix inference (want the codex row): %+v", capacity)
	}
}

// A missing alias file is a no-op: structural prefix inference still resolves the lane.
func TestReadLaneCapacityMissingAliasFileLeavesStructuralBehaviorUnchanged(t *testing.T) {
	cfg := capacityFixtureConfig(t)
	// cfg.Paths.AliasesPath is a path capacityFixtureConfig built but never wrote to.
	writeCapacityUsage(t, cfg, "claude", 5, capacityNow.Add(2*time.Hour), capacityNow.Add(-5*time.Minute))

	capacity, err := ReadLaneCapacityWithConfig("claude-main", capacityNow, cfg)
	if err != nil {
		t.Fatalf("read lane capacity: %v", err)
	}
	if !strings.Contains(capacity.DataRef, "provider=claude") {
		t.Fatalf("structural inference broke with no alias file present: %+v", capacity)
	}
}

// A malformed alias file must not be silently swallowed, but the note only surfaces on a lane
// that would otherwise be unknown — a lane the structural rules already resolve stays clean.
func TestReadLaneCapacityMalformedAliasFileSurfacesNoteOnlyWhenUnknown(t *testing.T) {
	cfg := capacityFixtureConfig(t)
	if err := os.WriteFile(cfg.Paths.AliasesPath, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}

	unknown, err := ReadLaneCapacityWithConfig("synthesis", capacityNow, cfg)
	if err != nil {
		t.Fatalf("read lane capacity: %v", err)
	}
	if unknown.State != CapacityUnknown {
		t.Fatalf("unknown lane with a malformed alias file should stay unknown: %+v", unknown)
	}
	if !strings.Contains(unknown.DataRef, "lane-aliases.json unreadable") {
		t.Fatalf("malformed alias file was silently swallowed: %+v", unknown)
	}

	writeCapacityUsage(t, cfg, "claude", 5, capacityNow.Add(2*time.Hour), capacityNow.Add(-5*time.Minute))
	known, err := ReadLaneCapacityWithConfig("claude-main", capacityNow, cfg)
	if err != nil {
		t.Fatalf("read lane capacity: %v", err)
	}
	if strings.Contains(known.DataRef, "lane-aliases.json unreadable") {
		t.Fatalf("alias-unreadable note leaked onto a structurally-resolved lane: %+v", known)
	}
}

// Integration: a real dispatch sub-lane name resolves to its family's live CodexBar row, and the
// family's freshness window is still enforced after resolution (untouched by this change).
func TestReadLaneCapacitySubLaneIntegrationFreshnessEnforced(t *testing.T) {
	cfg := capacityFixtureConfig(t)
	writeCapacityUsage(t, cfg, "claude", 10, capacityNow.Add(2*time.Hour), capacityNow.Add(-5*time.Minute))

	capacity, err := ReadLaneCapacityWithConfig("claude-main:sub-lane", capacityNow, cfg)
	if err != nil {
		t.Fatalf("read lane capacity: %v", err)
	}
	if capacity.State != CapacityAlive {
		t.Fatalf("sub-lane name did not resolve to the live claude row: %+v", capacity)
	}

	stale := capacityFixtureConfig(t)
	writeCapacityUsage(t, stale, "claude", 10, capacityNow.Add(2*time.Hour), capacityNow.Add(-16*time.Minute))
	staleCapacity, err := ReadLaneCapacityWithConfig("claude-main:sub-lane", capacityNow, stale)
	if err != nil {
		t.Fatalf("read stale lane capacity: %v", err)
	}
	if staleCapacity.State != CapacityUnknown {
		t.Fatalf("claude's 15m freshness window was not enforced after sub-lane resolution: %+v", staleCapacity)
	}
}
