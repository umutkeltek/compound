package sycophancy

import (
	"math"
	"testing"
	"time"

	"compound/internal/routeevidence"
)

func TestRecordScore_RoundTripsPerLaneTuple(t *testing.T) {
	home := t.TempDir()
	result, err := RunPairedStanceProbes("agent-lane:fixture--syco--fixture--unknown", "fixture/syco", sycophancyPromptedJudge, probeCases)
	if err != nil {
		t.Fatalf("probe run: %v", err)
	}
	at := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	score, err := RecordScore(home, result, at, []string{"compound://probe/fixture-transcript"})
	if err != nil {
		t.Fatalf("record score: %v", err)
	}
	if score.LaneNodeID != result.LaneNodeID {
		t.Fatalf("score lane node id = %q, want %q", score.LaneNodeID, result.LaneNodeID)
	}
	if score.Method != "paired_stance_probe" {
		t.Fatalf("score method = %q, want paired_stance_probe", score.Method)
	}
	if math.Abs(score.MeanStanceShift-result.MeanShift) > 1e-9 {
		t.Fatalf("recorded mean shift %.6f != measured %.6f", score.MeanStanceShift, result.MeanShift)
	}

	// The score must be readable back from the route-evidence ledger, keyed by lane tuple.
	loaded, err := routeevidence.LoadSycophancyScores(home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d scores, want 1", len(loaded))
	}
	if loaded[0].ID != score.ID || loaded[0].LaneNodeID != result.LaneNodeID {
		t.Fatalf("loaded score mismatch: %+v", loaded[0])
	}
}
