package routeevidence

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBuildSycophancyScore_DefaultsAndValidation(t *testing.T) {
	at := time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)
	sc, err := BuildSycophancyScore(BuildSycophancyScoreInput{
		CreatedAt:       at,
		LaneNodeID:      "agent-lane:h--r--m--balanced",
		ShortLaneID:     "h/r",
		ProbeCount:      4,
		MeanStanceShift: 0.62,
		MaxStanceShift:  0.9,
		EvidenceRefs:    []string{"ref-1", "ref-1", "ref-2"},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if sc.SchemaVersion != SycophancyScoreSchemaVersion {
		t.Fatalf("schema = %q", sc.SchemaVersion)
	}
	if sc.Method != MethodPairedStanceProbe {
		t.Fatalf("method defaulted to %q, want %q", sc.Method, MethodPairedStanceProbe)
	}
	if sc.RecordedVia != RecorderGeneric {
		t.Fatalf("recorded_via = %q, want %q", sc.RecordedVia, RecorderGeneric)
	}
	if len(sc.EvidenceRefs) != 2 {
		t.Fatalf("evidence refs = %v, want deduped to 2", sc.EvidenceRefs)
	}
	if sc.ID == "" {
		t.Fatal("id not set")
	}

	// The score carries NO threshold/verdict field — it is evidence, not a gate. This
	// is a design invariant of the slice (predicate law: no threshold before a real
	// lane's shift is recorded). If a pass/fail field is ever added, this test should be
	// revisited deliberately, not silently.
	if err := ValidateSycophancyScore(sc); err != nil {
		t.Fatalf("validate built score: %v", err)
	}
}

func TestBuildSycophancyScore_RejectsMissingLane(t *testing.T) {
	if _, err := BuildSycophancyScore(BuildSycophancyScoreInput{ProbeCount: 1}); err == nil {
		t.Fatal("expected error for missing lane node id")
	}
}

func TestSycophancyScore_DecodeRoundTrip(t *testing.T) {
	sc, err := BuildSycophancyScore(BuildSycophancyScoreInput{
		CreatedAt:  time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC),
		LaneNodeID: "agent-lane:h--r--m--balanced",
		ProbeCount: 2,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	data, err := json.Marshal(sc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := DecodeSycophancyScore(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != sc.ID || got.LaneNodeID != sc.LaneNodeID {
		t.Fatalf("round trip mismatch: %+v vs %+v", got, sc)
	}
	// Unknown fields must be rejected.
	if _, err := DecodeSycophancyScore([]byte(`{"schema_version":"x","bogus":1}`)); err == nil {
		t.Fatal("expected decode error for unknown field / bad schema")
	}
}

func TestRecordAndLoadSycophancyScores(t *testing.T) {
	home := t.TempDir()
	if scores, err := LoadSycophancyScores(home); err != nil || len(scores) != 0 {
		t.Fatalf("empty home load: scores=%d err=%v", len(scores), err)
	}
	first, err := BuildSycophancyScore(BuildSycophancyScoreInput{
		CreatedAt: time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC), LaneNodeID: "agent-lane:one", ProbeCount: 1,
	})
	if err != nil {
		t.Fatalf("build first: %v", err)
	}
	second, err := BuildSycophancyScore(BuildSycophancyScoreInput{
		CreatedAt: time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC), LaneNodeID: "agent-lane:two", ProbeCount: 3,
	})
	if err != nil {
		t.Fatalf("build second: %v", err)
	}
	if err := RecordSycophancyScore(home, first); err != nil {
		t.Fatalf("record first: %v", err)
	}
	if err := RecordSycophancyScore(home, second); err != nil {
		t.Fatalf("record second: %v", err)
	}
	scores, err := LoadSycophancyScores(home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(scores) != 2 {
		t.Fatalf("scores = %d, want 2 (append-only, both runs kept)", len(scores))
	}
	if scores[0].LaneNodeID != "agent-lane:one" || scores[1].LaneNodeID != "agent-lane:two" {
		t.Fatalf("scores not in append order: %q, %q", scores[0].LaneNodeID, scores[1].LaneNodeID)
	}
}

func TestRecordSycophancyScore_RejectsNoHome(t *testing.T) {
	sc, err := BuildSycophancyScore(BuildSycophancyScoreInput{
		CreatedAt: time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC), LaneNodeID: "agent-lane:x", ProbeCount: 1,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := RecordSycophancyScore("", sc); err == nil {
		t.Fatal("expected error for empty home path")
	}
}
