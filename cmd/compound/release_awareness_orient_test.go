package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"compound/internal/product"
	"compound/internal/sourceobs"
)

// orientReleaseAwarenessFrom adapts an internal ReleaseAwareness into the
// orient-only JSON shape. It must:
//   - return nil for nil input (so orient omits the field)
//   - keep all fields intact in the orient-side projection
func TestOrientReleaseAwarenessFrom_PassesThrough(t *testing.T) {
	aw := &product.ReleaseAwareness{
		SchemaVersion:    product.ReleaseAwarenessSchemaVersion,
		ProductID:        "product:alpha",
		Status:           "conflicting",
		ContractVersion:  "1.0.0",
		OutdatedContract: true,
		LatestObservedAt: "2026-09-06T11:30:00Z",
		LatestVersion:    "1.2.0",
		LatestChannel:    "app_store_connect",
		LatestPlatform:   "ios",
		LatestState:      "live",
		ObservationCount: 2,
		Conflict:         []string{"app_store_connect/ios:1.1.5|live", "app_store_connect/ios:1.2.0|live"},
		Boundary:         "evidence-only",
		NextAction:       "operator-reviewed reconcile required",
		ChannelPlatforms: []product.ReleaseChannelPlatformView{{
			Channel: "app_store_connect", Platform: "ios",
			Latest:         sourceobs.ReleaseObservation{ProductID: "product:alpha", Channel: "app_store_connect", Platform: "ios", ObservedVersion: "1.2.0", ObservedState: "live"},
			StateSet:       []string{"live"},
			VersionSet:     []string{"1.1.5", "1.2.0"},
			CurrentVersion: "1.2.0",
		}},
	}
	got := orientReleaseAwarenessFrom(aw)
	if got == nil {
		t.Fatalf("expected non-nil orient view")
	}
	if got.Status != "conflicting" {
		t.Fatalf("status lost: %q", got.Status)
	}
	if !got.OutdatedContract {
		t.Fatalf("outdated_contract lost")
	}
	if got.LatestVersion != "1.2.0" {
		t.Fatalf("latest_version lost: %q", got.LatestVersion)
	}
	if len(got.ChannelPlatforms) != 1 || got.ChannelPlatforms[0].Channel != "app_store_connect" {
		t.Fatalf("channel_platforms lost: %+v", got.ChannelPlatforms)
	}
	// JSON shape must be stable for downstream consumers.
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"status":"conflicting"`) {
		t.Fatalf("orient JSON missing status=conflicting: %s", string(data))
	}
	if !strings.Contains(string(data), `"outdated_contract":true`) {
		t.Fatalf("orient JSON missing outdated_contract=true: %s", string(data))
	}
}

// nil must produce nil; orient uses this to omit the field for products
// without release observations.
func TestOrientReleaseAwarenessFrom_NilReturnsNil(t *testing.T) {
	if got := orientReleaseAwarenessFrom(nil); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

// Sanity: orientReleaseAwarenessFrom is deterministic across calls.
func TestOrientReleaseAwarenessFrom_Deterministic(t *testing.T) {
	aw := &product.ReleaseAwareness{
		ProductID:        "product:alpha",
		Status:           "current",
		LatestObservedAt: time.Date(2026, 9, 6, 11, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}
	a := orientReleaseAwarenessFrom(aw)
	b := orientReleaseAwarenessFrom(aw)
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatalf("orient projection must be deterministic: %s vs %s", ja, jb)
	}
}
