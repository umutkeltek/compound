package corpus

import (
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInterruptedRefreshCoveragePreservesLastObservation(t *testing.T) {
	hp := t.TempDir()
	lane := fairRefreshLane(t, "interrupted-fixture")
	now := time.Now().UTC()
	path := refreshPath(hp, lane)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	prior := Coverage{Lane: lane.Lane, Status: "refreshing", LastAttemptAt: now.Add(-RefreshTimeout).Format(time.RFC3339Nano), CheckedAt: now.Add(-time.Hour).Format(time.RFC3339Nano), IndexedAt: now.Add(-time.Hour).Format(time.RFC3339Nano), Items: 7, Chunks: 9}
	if err := writeJSONFile(path, prior); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readRefreshCoverage(hp, lane, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "interrupted" || got.Error == "" || got.Items != 7 || got.Chunks != 9 || got.CheckedAt != prior.CheckedAt || got.IndexedAt != prior.IndexedAt {
		t.Fatalf("lost interrupted coverage: %+v", got)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("reader rewrote durable coverage")
	}
	prior.LastAttemptAt = now.Add(-RefreshTimeout + time.Second).Format(time.RFC3339Nano)
	if err := writeJSONFile(path, prior); err != nil {
		t.Fatal(err)
	}
	got, err = readRefreshCoverage(hp, lane, now)
	if err != nil || got.Status != "refreshing" {
		t.Fatalf("premature interruption: %+v %v", got, err)
	}
}

func TestRefreshDueKeepsFreshFailuresRetryable(t *testing.T) {
	hp := t.TempDir()
	lane := fairRefreshLane(t, "failed-fixture")
	now := time.Now().UTC()
	if err := os.MkdirAll(filepath.Join(hp, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	body, err := yaml.Marshal(map[string]any{"corpus_lanes": []LaneConfig{lane}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hp, "config", "memory-engine.yaml"), body, 0600); err != nil {
		t.Fatal(err)
	}
	path := refreshPath(hp, lane)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"capture_failed", "index_failed", "cancelled", "interrupted", "ok"} {
		c := Coverage{Lane: lane.Lane, Status: state, CheckedAt: now.Format(time.RFC3339Nano)}
		if err := writeJSONFile(path, c); err != nil {
			t.Fatal(err)
		}
		due, err := RefreshDue(hp, now)
		if err != nil || due != (state != "ok") {
			t.Fatalf("state %s due=%v err=%v", state, due, err)
		}
	}
}
