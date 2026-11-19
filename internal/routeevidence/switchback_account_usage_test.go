package routeevidence

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"compound/internal/work"
)

func accountUsageFixture(now time.Time) map[string]any {
	return map[string]any{
		"schema":       "switchback/account-usage@1",
		"purpose":      "usage_observation_not_admission_authority",
		"generated_at": now.Format(time.RFC3339Nano),
		"accounts": []any{map[string]any{
			"provider": "minimax", "slot": "configured", "state": "fresh",
			"account_id": map[string]any{"private_id": "secret-account.invalid"},
			"windows": []any{map[string]any{
				"name": "primary", "used_percent": 20, "remaining_percent": 80,
				"sampled_at": now.Add(-time.Minute).Format(time.RFC3339Nano),
				"resets_at":  now.Add(3 * time.Hour).Format(time.RFC3339Nano), "state": "fresh",
			}},
		}},
		"coverage": []any{},
	}
}

func usageAccount(doc map[string]any) map[string]any {
	return doc["accounts"].([]any)[0].(map[string]any)
}

func usageWindow(doc map[string]any) map[string]any {
	return usageAccount(doc)["windows"].([]any)[0].(map[string]any)
}

func usageBody(t *testing.T, doc map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestSwitchbackUsageFreshnessErrorsAndResetSemantics(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   LaneCapacityState
		bad    bool
	}{
		{"fresh-without-coverage-or-enabled", func(map[string]any) {}, CapacityAlive, false},
		{"walled", func(x map[string]any) { w := usageWindow(x); w["used_percent"] = 100; w["remaining_percent"] = 0 }, CapacityWalled, false},
		{"expiring-at-horizon", func(x map[string]any) { usageWindow(x)["resets_at"] = now.Add(time.Hour).Format(time.RFC3339Nano) }, CapacityExpiring, false},
		{"stale-window-fresh-report", func(x map[string]any) {
			usageWindow(x)["sampled_at"] = now.Add(-16 * time.Minute).Format(time.RFC3339Nano)
		}, CapacityUnknown, false},
		{"future-window", func(x map[string]any) {
			usageWindow(x)["sampled_at"] = now.Add(6 * time.Minute).Format(time.RFC3339Nano)
		}, CapacityUnknown, false},
		{"missing-percent", func(x map[string]any) { delete(usageWindow(x), "used_percent") }, CapacityUnknown, false},
		{"null-percent", func(x map[string]any) { usageWindow(x)["used_percent"] = nil }, CapacityUnknown, false},
		{"invalid-percent", func(x map[string]any) { usageWindow(x)["used_percent"] = -1 }, CapacityUnknown, false},
		{"no-windows", func(x map[string]any) { usageAccount(x)["windows"] = []any{} }, CapacityUnknown, false},
		{"row-stale", func(x map[string]any) { usageAccount(x)["state"] = "stale" }, CapacityUnknown, false},
		{"window-stale", func(x map[string]any) { usageWindow(x)["state"] = "stale" }, CapacityUnknown, false},
		{"window-reset-passed", func(x map[string]any) { usageWindow(x)["state"] = "reset_passed" }, CapacityUnknown, false},
		{"past-reset-fresh-state", func(x map[string]any) { usageWindow(x)["resets_at"] = now.Add(-time.Second).Format(time.RFC3339Nano) }, CapacityUnknown, false},
		{"invalid-reset", func(x map[string]any) { usageWindow(x)["resets_at"] = "bad" }, CapacityUnknown, false},
		{"no-reset", func(x map[string]any) { usageWindow(x)["resets_at"] = nil }, CapacityAlive, false},
		{"refresh-error", func(x map[string]any) { usageAccount(x)["refresh_error"] = "private-secret.invalid" }, CapacityUnknown, false},
		{"login", func(x map[string]any) { usageAccount(x)["relogin_required"] = true }, CapacityDead, false},
		{"disabled", func(x map[string]any) { usageAccount(x)["enabled"] = false }, CapacityDead, false},
		{"unavailable-coverage-overrides", func(x map[string]any) {
			x["coverage"] = []any{map[string]any{"provider": "minimax", "state": "unavailable"}}
		}, CapacityUnknown, false},
		{"other-coverage-irrelevant", func(x map[string]any) {
			x["coverage"] = []any{map[string]any{"provider": "another", "state": "unavailable"}}
		}, CapacityAlive, false},
		{"other-provider", func(x map[string]any) { usageAccount(x)["provider"] = "another" }, CapacityUnknown, false},
		{"schema", func(x map[string]any) { x["schema"] = "wrong" }, "", true},
		{"purpose", func(x map[string]any) { x["purpose"] = "admission_authority" }, "", true},
		{"contradictory-remaining", func(x map[string]any) { usageWindow(x)["remaining_percent"] = 90 }, "", true},
		{"duplicate-window", func(x map[string]any) { usageAccount(x)["windows"] = []any{usageWindow(x), usageWindow(x)} }, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := accountUsageFixture(now)
			tc.mutate(doc)
			got, err := parseSwitchbackAccountUsage(usageBody(t, doc), "minimax", "build-implement", now, 15*time.Minute, 60)
			if tc.bad {
				if err == nil {
					t.Fatalf("accepted malformed report: %+v", got)
				}
				return
			}
			if err != nil || got.State != tc.want || !got.SourceInstalled {
				t.Fatalf("got %+v, %v; want %s", got, err, tc.want)
			}
			if strings.Contains(got.DataRef, "private") || strings.Contains(got.DataRef, "secret") {
				t.Fatalf("account identity or error leaked: %s", got.DataRef)
			}
		})
	}
	if _, err := parseSwitchbackAccountUsage(make([]byte, MaxCodexBarProbeBytes+1), "minimax", "build-implement", now, time.Minute, 60); err == nil {
		t.Fatal("oversized usage accepted")
	}
}

func TestSwitchbackUsageRequiresEveryEnabledAccountUsable(t *testing.T) {
	now := time.Now().UTC()
	doc := accountUsageFixture(now)
	first := usageAccount(doc)
	second := usageAccount(accountUsageFixture(now))
	second["slot"] = "another"
	secondWindow := second["windows"].([]any)[0].(map[string]any)
	secondWindow["used_percent"], secondWindow["remaining_percent"] = 60, 40
	doc["accounts"] = []any{first, second}
	parse := func() LaneCapacity {
		t.Helper()
		got, err := parseSwitchbackAccountUsage(usageBody(t, doc), "minimax", "build-implement", now, 15*time.Minute, 60)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := parse(); got.State != CapacityAlive || got.Remaining != 40 {
		t.Fatalf("did not use conservative minimum without summing: %+v", got)
	}
	secondWindow["used_percent"], secondWindow["remaining_percent"] = 100, 0
	if got := parse(); got.State != CapacityUnknown {
		t.Fatalf("selected best account: %+v", got)
	}
	second["enabled"] = false
	if got := parse(); got.State != CapacityAlive {
		t.Fatalf("disabled row constrained: %+v", got)
	}
	second["enabled"] = true
	secondWindow["used_percent"], secondWindow["remaining_percent"] = 60, 40
	secondWindow["resets_at"] = now.Add(30 * time.Minute).Format(time.RFC3339Nano)
	if got := parse(); got.State != CapacityUnknown {
		t.Fatalf("account cannot outlive run: %+v", got)
	}
	second["slot"] = "configured"
	if _, err := parseSwitchbackAccountUsage(usageBody(t, doc), "minimax", "build-implement", now, 15*time.Minute, 60); err == nil {
		t.Fatal("duplicate account slot accepted")
	}
}

func TestSwitchbackTextCapacityUsesTheConfiguredServicesWindows(t *testing.T) {
	now := time.Now().UTC()
	window := func(model, period string, used int) map[string]any {
		w := usageWindow(accountUsageFixture(now))
		w["model"], w["scope"], w["name"] = model, "generation", model+"/"+period
		w["used_percent"], w["remaining_percent"] = used, 100-used
		return w
	}
	video := window("video", "interval", 100)
	short := window("general", "interval", 1)
	week := window("general", "weekly", 52)
	stale := window("general", "interval", 1)
	stale["sampled_at"] = now.Add(-16 * time.Minute).Format(time.RFC3339Nano)
	mislabeled := window("video", "interval", 100)
	mislabeled["name"] = "general/interval"
	for _, tc := range []struct {
		name, capability string
		windows          []any
		want             LaneCapacityState
	}{
		{"video capped", "build-implement", []any{video, short, week}, CapacityAlive},
		{"text interval capped", "build-implement", []any{video, window("general", "interval", 100), week}, CapacityWalled},
		{"text week capped", "build-implement", []any{short, window("general", "weekly", 100)}, CapacityWalled},
		{"stale text", "build-implement", []any{stale, week, video}, CapacityUnknown},
		{"missing text week", "build-implement", []any{short, video}, CapacityUnknown},
		{"video only", "build-implement", []any{video}, CapacityUnknown},
		{"inconsistent service", "build-implement", []any{mislabeled, short, week}, CapacityUnknown},
		{"other capability", "video-generation", []any{video, short, week}, CapacityWalled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := accountUsageFixture(now)
			usageAccount(doc)["windows"] = tc.windows
			cfg := LaneCapacityConfig{AccountUsageRunner: func(context.Context, string, ...string) ([]byte, error) {
				return usageBody(t, doc), nil
			}}
			source := LaneCapacitySourceRef{Kind: "switchback_account_usage", Ref: "switchback://account-usage/minimax", FreshnessMinutes: 15}
			got, err := GuardDeclaredLaneCapacityWithConfig(t.TempDir(), "fixture", tc.capability, source, now, 60, false, nil, true, cfg)
			if err != nil || got.Capacity.State != tc.want {
				t.Fatalf("capacity state %s, error %v, want %s", got.Capacity.State, err, tc.want)
			}
			if tc.want == CapacityAlive && (got.Capacity.Remaining != 48 || got.Refused || got.ConfirmationRequired) {
				t.Fatalf("unexpected text allowance: %+v", got)
			}
		})
	}
}

func TestSwitchbackDeclaredUsageReadOnlyRunnerAndPolicy(t *testing.T) {
	now := time.Now().UTC()
	home := t.TempDir()
	source := LaneCapacitySourceRef{Kind: "switchback_account_usage", Ref: "switchback://account-usage/minimax", FreshnessMinutes: 15}
	doc := accountUsageFixture(now)
	calls := 0
	cfg := LaneCapacityConfig{AccountUsageRunner: func(ctx context.Context, command string, args ...string) ([]byte, error) {
		calls++
		if command != "sb" || !reflect.DeepEqual(args, []string{"limits", "--provider", "minimax", "--json"}) {
			t.Fatalf("unexpected command: %s %v", command, args)
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > CodexBarProbeTimeout {
			t.Fatal("observation lacks bounded deadline")
		}
		return usageBody(t, doc), nil
	}}
	guard := func(confirm bool) LaneCapacityDecision {
		t.Helper()
		got, err := GuardDeclaredLaneCapacityWithConfig(home, "fixture", "build-implement", source, now, 60, confirm, nil, true, cfg)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := guard(false); got.Refused || got.ConfirmationRequired || calls != 1 {
		t.Fatalf("fresh usage: %+v calls=%d", got, calls)
	}
	usageAccount(doc)["refresh_error"] = "secret-account.invalid"
	if got := guard(false); !got.ConfirmationRequired {
		t.Fatalf("unknown ungated: %+v", got)
	}
	if got := guard(true); got.ConfirmationRequired || got.Refused {
		t.Fatalf("existing confirm policy lost: %+v", got)
	}
	delete(usageAccount(doc), "refresh_error")
	w := usageWindow(doc)
	w["used_percent"], w["remaining_percent"] = 100, 0
	if got := guard(true); !got.Refused {
		t.Fatalf("exhausted admitted by confirm: %+v", got)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("read-only guard wrote evidence: %v %v", entries, err)
	}
	cfg.AccountUsageRunner = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("secret-account.invalid")
	}
	if _, err := GuardDeclaredLaneCapacityWithConfig(home, "fixture", "build-implement", source, now, 60, false, nil, true, cfg); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("failed source fell back or leaked: %v", err)
	}
	for _, ref := range []string{"switchback://account-usage/-flag", "switchback://account-usage/../secret", "switchback://account-usage/minimax?x", "codexbar://provider/minimax"} {
		if _, err := switchbackUsageProviderFromRef(ref); err == nil {
			t.Fatalf("accepted %s", ref)
		}
	}
}

func TestSwitchbackUsageManifestRoutesAndCoordination(t *testing.T) {
	now := time.Now().UTC()
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), "source.yaml")
	body := `version: 1
purpose: fixture
capability_classes: [build-implement]
models:
  fixture:
    lane: fixture
    harness: switchback
    provider_route: fixture
    tier: cheap
    capacity_source: {kind: switchback_account_usage, freshness_minutes: 15, ref: "switchback://account-usage/minimax"}
    capabilities: {build-implement: {level: capable, evidence: fixture}}
routing_defaults:
  build-implement: [fixture]
`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportModelCapabilitiesYAML(home, path, now); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(body, "freshness_minutes: 15", "freshness_minutes: 61", 1),
		strings.Replace(body, "account-usage/minimax", "account-usage/../secret", 1),
	} {
		badPath := filepath.Join(t.TempDir(), "invalid.yaml")
		if err := os.WriteFile(badPath, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ImportModelCapabilitiesYAML(home, badPath, now); err == nil {
			t.Fatal("accepted malformed source")
		}
	}
	doc := accountUsageFixture(now)
	w := usageWindow(doc)
	w["used_percent"], w["remaining_percent"] = 100, 0
	cfg := LaneCapacityConfig{AccountUsageRunner: func(context.Context, string, ...string) ([]byte, error) { return usageBody(t, doc), nil }}
	plan, err := PlanCapabilityRoute(home, "build-implement", CapabilityRoutePlanOptions{CapacityConfig: &cfg, SuppressRefusalEvidence: true}, now)
	if err != nil || len(plan.Chain) != 0 || len(plan.Omitted) != 1 || plan.Omitted[0].Capacity.State != CapacityWalled {
		t.Fatalf("declared usage not consumed by route: %+v %v", plan, err)
	}
	before := coordinationCapacitySnapshot(t, home)
	observation := NewCoordinationCapacityObservation(cfg, home, now)
	result, err := observation.Guard()(work.LaneCapacityGuardRequest{
		HomePath: home, Lane: "fixture", Now: now, ReadOnly: true, TupleBacked: true,
		CapacitySource: &work.LaneCapacitySource{Kind: "switchback_account_usage", Ref: "switchback://account-usage/minimax", FreshnessMinutes: 15},
	})
	if err != nil || !result.Refused || !strings.Contains(result.Capacity.DataRef, "switchback://account-usage/minimax") {
		t.Fatalf("coordination lost declared source: %+v %v", result, err)
	}
	if after := coordinationCapacitySnapshot(t, home); before != after {
		t.Fatal("coordination wrote runtime evidence")
	}
}

func TestSwitchbackUsageOnlyMeasuredGenerationWindowsConstrainCapacity(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		want   LaneCapacityState
	}{
		{"generation-and-exhausted-MCP", func(x map[string]any) {
			usageWindow(x)["scope"] = "generation"
			tool := usageWindow(accountUsageFixture(now))
			tool["name"], tool["scope"] = "monthly_mcp", "tools"
			tool["used_percent"], tool["remaining_percent"] = 100, 0
			usageAccount(x)["windows"] = []any{tool, usageWindow(x)}
		}, CapacityAlive},
		{"general-generation", func(x map[string]any) { usageWindow(x)["scope"] = "general" }, CapacityAlive},
		{"tool-only", func(x map[string]any) { usageWindow(x)["scope"] = "tools" }, CapacityUnknown},
		{"unknown-scope", func(x map[string]any) { usageWindow(x)["scope"] = "another" }, CapacityUnknown},
		{"account-estimated", func(x map[string]any) { usageAccount(x)["estimated"] = true }, CapacityUnknown},
		{"account-estimated-kind", func(x map[string]any) { usageAccount(x)["usage_kind"] = "estimated" }, CapacityUnknown},
		{"window-estimated", func(x map[string]any) { usageWindow(x)["estimated"] = true }, CapacityUnknown},
		{"window-estimated-kind", func(x map[string]any) { usageWindow(x)["usage_kind"] = "estimated" }, CapacityUnknown},
		{"generation-state-recomputed-from-sample", func(x map[string]any) {
			w := usageWindow(x)
			w["scope"] = "generation"
			w["sampled_at"] = now.Add(-16 * time.Minute).Format(time.RFC3339Nano)
		}, CapacityUnknown},
		{"generation-state-recomputed-from-reset", func(x map[string]any) {
			w := usageWindow(x)
			w["scope"] = "generation"
			w["resets_at"] = now.Add(-time.Second).Format(time.RFC3339Nano)
		}, CapacityUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := accountUsageFixture(now)
			tc.mutate(doc)
			got, err := parseSwitchbackAccountUsage(usageBody(t, doc), "minimax", "build-implement", now, 15*time.Minute, 60)
			if err != nil || got.State != tc.want {
				t.Fatalf("got %+v, %v; want %s", got, err, tc.want)
			}
			if tc.want == CapacityAlive && got.Remaining != 80 {
				t.Fatalf("tool window constrained generation: %+v", got)
			}
		})
	}
}
