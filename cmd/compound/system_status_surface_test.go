package main

import (
	"context"
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
	  "context_window":{"context_window_size":1000000,"used_percentage":20},
	  "rate_limits":{"five_hour":{"used_percentage":37}}
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

func TestSupplementLiveClaudeCapacityProbesOnlyWhenOwnedSourcesAreEmpty(t *testing.T) {
	now := time.Date(2026, 7, 17, 2, 0, 0, 0, time.UTC)
	snapshot, err := statussurface.BuildClaudeSnapshot([]byte(`{"context_window":{"used_percentage":20}}`), now, statussurface.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	probeCalls := 0
	merged := supplementLiveClaudeCapacity(
		snapshot,
		statussurface.Snapshot{},
		false,
		now,
		func(string, time.Time) (routeevidence.LaneCapacity, error) {
			return routeevidence.LaneCapacity{State: routeevidence.CapacityUnknown}, nil
		},
		func(time.Time) (claudeCapacityEvidence, error) {
			probeCalls++
			return claudeCapacityEvidence{
				FiveHour: statussurface.CapacityEvidence{Status: statussurface.StatusLive, RemainingPercent: 63, Source: "compound/codexbar-live"},
				Weekly:   statussurface.CapacityEvidence{Status: statussurface.StatusLive, RemainingPercent: 24, Source: "compound/codexbar-live"},
			}, nil
		},
	)
	if probeCalls != 1 {
		t.Fatalf("live probe calls = %d, want 1", probeCalls)
	}
	line, err := statussurface.Render(merged, statussurface.RenderOptions{Theme: statussurface.ThemeAuto, Width: 120})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "5h 63%") || !strings.Contains(line, "wk 24%") {
		t.Fatalf("live probe did not reach render: %q", line)
	}

	probeCalls = 0
	fromCache := supplementLiveClaudeCapacity(
		snapshot,
		merged,
		true,
		now.Add(time.Minute),
		func(string, time.Time) (routeevidence.LaneCapacity, error) {
			return routeevidence.LaneCapacity{State: routeevidence.CapacityUnknown}, nil
		},
		func(time.Time) (claudeCapacityEvidence, error) {
			probeCalls++
			return claudeCapacityEvidence{}, nil
		},
	)
	if probeCalls != 0 {
		t.Fatalf("fresh reduced projection still triggered %d live probe(s)", probeCalls)
	}
	line, err = statussurface.Render(fromCache, statussurface.RenderOptions{Theme: statussurface.ThemeAuto, Width: 120})
	if err != nil || !strings.Contains(line, "5h 63%") || !strings.Contains(line, "wk 24%") {
		t.Fatalf("fresh capacity projection was not reused: line=%q err=%v", line, err)
	}
}

func TestParseCodexBarClaudeCapacitySelectsFreshConstrainingWeeklyWindow(t *testing.T) {
	now := time.Date(2026, 7, 17, 20, 0, 0, 0, time.UTC)
	body := []byte(`[
	  {
	    "provider":"claude",
	    "usage":{
	      "accountEmail":"must-not-survive@example.invalid",
	      "updatedAt":"2026-07-17T19:59:00Z",
	      "primary":{"usedPercent":37,"windowMinutes":300,"resetsAt":"2026-07-17T21:59:59Z"},
	      "secondary":{"usedPercent":74,"windowMinutes":10080,"resetsAt":"2026-07-21T16:00:00Z"},
	      "extraRateWindows":[
	        {"id":"daily-routines","window":{"usedPercent":0,"windowMinutes":10080}},
	        {"id":"model-scoped","window":{"usedPercent":78,"windowMinutes":10080,"resetsAt":"2026-07-21T16:00:00Z"}}
	      ]
	    }
	  }
	]`)
	evidence, err := parseCodexBarClaudeCapacity(body, now)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.FiveHour.Status != statussurface.StatusLive || evidence.FiveHour.RemainingPercent != 63 || evidence.FiveHour.Source != "compound/codexbar-live" {
		t.Fatalf("five-hour capacity evidence = %+v", evidence.FiveHour)
	}
	if evidence.Weekly.Status != statussurface.StatusLive || evidence.Weekly.RemainingPercent != 22 || evidence.Weekly.Source != "compound/codexbar-live" {
		t.Fatalf("capacity evidence = %+v", evidence)
	}
	if evidence.FiveHour.ResetsAt.Format(time.RFC3339) != "2026-07-17T21:59:59Z" || evidence.Weekly.ResetsAt.Format(time.RFC3339) != "2026-07-21T16:00:00Z" {
		t.Fatalf("capacity resets = five-hour %s weekly %s", evidence.FiveHour.ResetsAt, evidence.Weekly.ResetsAt)
	}
	if strings.Contains(evidence.FiveHour.Source+evidence.Weekly.Source, "must-not-survive") {
		t.Fatalf("capacity evidence retained identity: %+v", evidence)
	}

	stale := strings.Replace(string(body), "2026-07-17T19:59:00Z", "2026-07-17T19:40:00Z", 1)
	if _, err := parseCodexBarClaudeCapacity([]byte(stale), now); err == nil {
		t.Fatal("stale CodexBar evidence was accepted")
	}
}

func TestProbeCodexBarClaudeCapacityUsesBoundedDirectArgv(t *testing.T) {
	now := time.Date(2026, 7, 17, 20, 0, 0, 0, time.UTC)
	called := false
	evidence, err := probeCodexBarClaudeCapacityWithRunner(now, func(ctx context.Context, command string, args ...string) ([]byte, error) {
		called = true
		if command != "codexbar" {
			t.Fatalf("probe command = %q", command)
		}
		got := strings.Join(args, " ")
		if got != "usage --provider claude --source auto --format json --no-color" {
			t.Fatalf("probe args = %q", got)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("probe context has no deadline")
		}
		return []byte(`[{
		  "provider":"claude",
		  "usage":{"updatedAt":"2026-07-17T19:59:00Z","primary":{"usedPercent":37,"windowMinutes":300,"resetsAt":"2026-07-17T21:59:59Z"},"secondary":{"usedPercent":74,"windowMinutes":10080,"resetsAt":"2026-07-21T16:00:00Z"}}
		}]`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called || evidence.FiveHour.RemainingPercent != 63 || evidence.Weekly.RemainingPercent != 26 {
		t.Fatalf("probe result = called %v evidence %+v", called, evidence)
	}
}
