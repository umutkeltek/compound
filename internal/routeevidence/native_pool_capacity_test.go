package routeevidence

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func poolFixture(t *testing.T, now time.Time, states ...string) []byte {
	t.Helper()
	rows := []map[string]any{}
	counts := map[string]int{"available": 0, "exhausted": 0, "needs_login": 0, "disabled": 0, "unknown": 0}
	for i, state := range states {
		counts[state]++
		row := map[string]any{"id": string(rune('a' + i)), "state": state, "reason": "fixture", "resets_at_ms": nil, "windows": []any{}}
		if state == "available" {
			row["windows"] = []map[string]any{
				{"name": "five_hour", "used_percent": 20, "sampled_at": now.Format(time.RFC3339Nano), "resets_at": now.Add(3 * time.Hour).Format(time.RFC3339Nano)},
				{"name": "seven_day", "used_percent": 30, "sampled_at": now.Format(time.RFC3339Nano), "resets_at": nil},
			}
		}
		if state == "exhausted" {
			row["resets_at_ms"] = now.Add(time.Hour).UnixMilli()
		}
		rows = append(rows, row)
	}
	state := "unavailable"
	if counts["exhausted"] > 0 {
		state = "exhausted"
	}
	if counts["unknown"] > 0 {
		state = "unknown"
	}
	if counts["available"] > 0 {
		state = "available"
	}
	body, err := json.Marshal(map[string]any{"schema": "switchback/native-pool-capacity@1", "pool": "team", "provider": "claude", "authority_revision": "sha256:" + strings.Repeat("a", 64), "generated_at_ms": now.UnixMilli(), "freshness_seconds": 900, "state": state, "counts": counts, "accounts": rows, "model_scope": "general", "purpose": "capacity_observation_not_admission_authority"})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestNativePoolCapacityUsesAvailableMembersWithoutSummingQuota(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		states []string
		want   LaneCapacityState
	}{
		{[]string{"exhausted", "needs_login", "available", "available"}, CapacityAlive},
		{[]string{"exhausted", "needs_login"}, CapacityWalled},
		{[]string{"unknown", "exhausted"}, CapacityUnknown},
		{[]string{"disabled", "needs_login"}, CapacityDead},
	} {
		got, err := parseNativePoolCapacity(poolFixture(t, now, tc.states...), "team", now, 15*time.Minute, 60)
		if err != nil || got.State != tc.want || !got.SourceInstalled {
			t.Fatalf("%v: %+v %v", tc.states, got, err)
		}
		if got.State == CapacityAlive && got.Remaining != 70 {
			t.Fatalf("invented aggregate quota: %+v", got)
		}
	}
	got, err := parseNativePoolCapacity(poolFixture(t, now, "available"), "team", now, 15*time.Minute, 180)
	if err != nil || got.State != CapacityExpiring {
		t.Fatalf("exact run horizon not enforced: %+v %v", got, err)
	}
}

func TestNativePoolCapacityRefusesMismatchedStaleOrMalformedEvidence(t *testing.T) {
	now := time.Now().UTC()
	base := poolFixture(t, now, "available")
	for _, mutate := range []func(map[string]any){
		func(x map[string]any) { x["pool"] = "another" },
		func(x map[string]any) { x["state"] = "unknown" },
		func(x map[string]any) { x["generated_at_ms"] = now.Add(-16 * time.Minute).UnixMilli() },
		func(x map[string]any) { x["generated_at_ms"] = now.Add(time.Hour).UnixMilli() },
		func(x map[string]any) { x["authority_revision"] = "bad" },
		func(x map[string]any) { x["counts"].(map[string]any)["unknown"] = 1 },
		func(x map[string]any) {
			counts := x["counts"].(map[string]any)
			delete(counts, "unknown")
			counts["unexpected"] = 0
		},
		func(x map[string]any) { x["accounts"] = append(x["accounts"].([]any), x["accounts"].([]any)[0]) },
		func(x map[string]any) { x["accounts"].([]any)[0].(map[string]any)["windows"] = []any{} },
		func(x map[string]any) {
			x["accounts"].([]any)[0].(map[string]any)["windows"].([]any)[0].(map[string]any)["used_percent"] = 100
		},
		func(x map[string]any) {
			x["accounts"].([]any)[0].(map[string]any)["windows"].([]any)[0].(map[string]any)["sampled_at"] = now.Add(-16 * time.Minute).Format(time.RFC3339Nano)
		},
	} {
		var x map[string]any
		if err := json.Unmarshal(base, &x); err != nil {
			t.Fatal(err)
		}
		mutate(x)
		body, _ := json.Marshal(x)
		if _, err := parseNativePoolCapacity(body, "team", now, 15*time.Minute, 60); err == nil {
			t.Fatalf("accepted invalid observation %s", body)
		}
	}
}

func TestNativePoolCapacityCodexWindowsAndRunHorizon(t *testing.T) {
	now := time.Now().UTC()
	var report map[string]any
	if err := json.Unmarshal(poolFixture(t, now, "available", "available"), &report); err != nil {
		t.Fatal(err)
	}
	report["provider"] = "codex"
	accounts := report["accounts"].([]any)
	for i, value := range accounts {
		windows := value.(map[string]any)["windows"].([]any)
		windows[0].(map[string]any)["name"] = "primary"
		windows[1].(map[string]any)["name"] = "secondary"
		if i == 0 {
			windows[0].(map[string]any)["resets_at"] = now.Add(30 * time.Minute).Format(time.RFC3339Nano)
		} else {
			windows[1].(map[string]any)["used_percent"] = 60
		}
	}
	body, _ := json.Marshal(report)
	capacity, err := parseNativePoolCapacity(body, "team", now, 15*time.Minute, 60)
	if err != nil || capacity.State != CapacityAlive || capacity.Remaining != 40 {
		t.Fatalf("did not choose member that outlives run: %+v %v", capacity, err)
	}
	windows := accounts[1].(map[string]any)["windows"].([]any)
	windows[1].(map[string]any)["name"] = "primary"
	body, _ = json.Marshal(report)
	if _, err := parseNativePoolCapacity(body, "team", now, 15*time.Minute, 60); err == nil {
		t.Fatal("duplicate primary window replaced missing secondary")
	}
}

func TestNativePoolDeclaredGuardUsesReadOnlyOwnerAndPreservesUnknownGate(t *testing.T) {
	now := time.Now().UTC()
	home := t.TempDir()
	calls := 0
	source := LaneCapacitySourceRef{Kind: "switchback_native_pool", Ref: "switchback://account-pools/team", FreshnessMinutes: 15}
	cfg := LaneCapacityConfig{NativePoolRunner: func(ctx context.Context, command string, args ...string) ([]byte, error) {
		calls++
		if command != "sb" || !reflect.DeepEqual(args, []string{"profile", "pools", "capacity", "team", "--freshness-seconds", "900", "--json"}) {
			t.Fatalf("unexpected mutation/probe: %s %v", command, args)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded observation")
		}
		return poolFixture(t, now, "unknown"), nil
	}}
	got, err := GuardDeclaredLaneCapacityWithConfig(home, "fixture", "build-implement", source, now, 60, false, nil, true, cfg)
	if err != nil || !got.ConfirmationRequired || calls != 1 {
		t.Fatalf("unknown became an ungated lane: %+v %v calls=%d", got, err, calls)
	}
	cfg.NativePoolRunner = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("offline") }
	if _, err := GuardDeclaredLaneCapacityWithConfig(home, "fixture", "build-implement", source, now, 60, false, nil, true, cfg); err == nil {
		t.Fatal("failed source fell back")
	}
	for _, ref := range []string{"switchback://account-pools/../secret", "switchback://account-pools/-flag", "codexbar://provider/claude"} {
		if _, err := nativePoolFromRef(ref); err == nil {
			t.Fatalf("accepted %s", ref)
		}
	}
}
