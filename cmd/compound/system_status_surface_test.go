package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"compound/internal/routeevidence"
	"compound/internal/statussurface"
)

func TestSystemStatusSurfaceClaudeSnapshotAndRender(t *testing.T) {
	homePath := t.TempDir()
	now := time.Date(2026, 7, 17, 2, 0, 0, 0, time.UTC)
	fixture := `{
  "session_id":"must-not-persist",
  "message":"CLI_BODY_MUST_NOT_PERSIST",
  "model":{"display_name":"Sonnet 4.6"},
  "context_window":{"context_window_size":200000,"used_percentage":48},
  "rate_limits":{
    "five_hour":{"used_percentage":61},
    "seven_day":{"used_percentage":76}
  }
}`

	out, err := runSystemStatusSurface(
		[]string{"snapshot", "--harness", "claude"},
		strings.NewReader(fixture), homePath, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot statussurface.Snapshot
	if err := json.Unmarshal([]byte(out), &snapshot); err != nil {
		t.Fatalf("parse snapshot: %v\n%s", err, out)
	}
	if snapshot.Harness != statussurface.HarnessClaude || snapshot.InferenceClass != statussurface.InferenceCapacity {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if !snapshot.FreshUntil.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("snapshot output freshness = %s, want persisted TTL boundary", snapshot.FreshUntil)
	}
	persisted, err := os.ReadFile(statussurface.SnapshotPath(homePath, statussurface.HarnessClaude))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"CLI_BODY_MUST_NOT_PERSIST", "must-not-persist", "session_id", "message"} {
		if strings.Contains(string(persisted), forbidden) {
			t.Fatalf("persisted CLI snapshot retained %q: %s", forbidden, persisted)
		}
	}

	line, err := runSystemStatusSurface(
		[]string{"render", "--harness", "claude", "--theme", "auto", "--width", "120", "--no-color"},
		strings.NewReader(fixture), homePath, now.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "!! CLAUDE:CONSERVE !! left: ctx [#####-----] 52%") || !strings.Contains(line, "5h 39%") || !strings.Contains(line, "wk 24%") {
		t.Fatalf("render = %q", line)
	}
}

func TestSystemStatusSurfaceCodexTheme(t *testing.T) {
	out, err := runSystemStatusSurface(
		[]string{"theme", "--harness", "codex", "--theme", "pai-nexus"},
		strings.NewReader(""), t.TempDir(), time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<plist version=\"1.0\">") || !strings.Contains(out, "PAI Nexus") {
		t.Fatalf("theme output = %q", out)
	}
}

func TestBuildLiveClaudeStatusSnapshotSupplementsMissingWeeklyCapacity(t *testing.T) {
	homePath := t.TempDir()
	now := time.Date(2026, 7, 17, 2, 0, 0, 0, time.UTC)
	fixture := `{
	  "model":{"display_name":"Fable 5"},
	  "context_window":{"context_window_size":1000000,"used_percentage":20}
	}`
	reset := now.Add(72 * time.Hour)
	called := false
	snapshot, err := buildLiveStatusSnapshotWithCapacityReader(
		statussurface.HarnessClaude,
		strings.NewReader(fixture),
		homePath,
		now,
		func(lane string, sampledAt time.Time) (routeevidence.LaneCapacity, error) {
			called = true
			if lane != "claude" || !sampledAt.Equal(now) {
				t.Fatalf("capacity request = lane %q at %s", lane, sampledAt)
			}
			return routeevidence.LaneCapacity{
				State: routeevidence.CapacityAlive, Remaining: 22, ResetsAt: reset, Source: "codexbar/usage-history",
			}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("Claude live builder did not consult Compound lane capacity")
	}
	if snapshot.Mode != statussurface.ModeConserve || snapshot.InferenceClass != statussurface.InferenceCapacity {
		t.Fatalf("adaptive snapshot = mode %q class %q", snapshot.Mode, snapshot.InferenceClass)
	}
	line, err := statussurface.Render(snapshot, statussurface.RenderOptions{Theme: statussurface.ThemeAuto, Width: 120})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToUpper(line), "PAI") || !strings.Contains(line, "wk 22%") {
		t.Fatalf("live fallback render = %q", line)
	}
}
