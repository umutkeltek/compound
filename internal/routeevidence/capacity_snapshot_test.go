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
		},
		ExpectedMinutes: 60,
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

// f2: lack of source rows is an explicit UNKNOWN, and dispatch cannot offer that lane until the
// caller supplies --confirm. Confirmation admits the same lane without inventing health.
func TestCapacityF2UnknownZaiRequiresConfirm(t *testing.T) {
	home := t.TempDir()
	cfg := capacityFixtureConfig(t)
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
