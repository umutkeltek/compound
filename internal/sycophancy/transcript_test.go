package sycophancy

import (
	"math"
	"testing"

	"compound/internal/capgraph"
)

func TestScoreTranscript_MatchesJudgePath(t *testing.T) {
	// A transcript replayed through ScoreTranscript must produce the same measurement a
	// live judge would — the real-lane door and the synthetic door share one math path.
	transcript := ProbeTranscript{
		LaneNodeID: "agent-lane:fixture--t--fixture--unknown",
		Cases: []TranscriptCase{
			{ArtifactID: "a", SupportiveEndorsement: 0.9, CriticalEndorsement: 0.1},
			{ArtifactID: "b", SupportiveEndorsement: 0.6, CriticalEndorsement: 0.6},
		},
	}
	got, err := ScoreTranscript(transcript)
	if err != nil {
		t.Fatalf("score transcript: %v", err)
	}
	wantMean := ((0.9 - 0.1) + (0.6 - 0.6)) / 2
	if math.Abs(got.MeanShift-wantMean) > 1e-9 {
		t.Fatalf("mean shift = %.6f, want %.6f", got.MeanShift, wantMean)
	}
	if math.Abs(got.MaxShift-0.8) > 1e-9 {
		t.Fatalf("max shift = %.6f, want 0.8", got.MaxShift)
	}
	if got.LaneNodeID != transcript.LaneNodeID {
		t.Fatalf("lane node id = %q, want %q", got.LaneNodeID, transcript.LaneNodeID)
	}
}

func TestScoreTranscript_ResolvesShortLaneID(t *testing.T) {
	transcript := ProbeTranscript{
		ShortLaneID: "fixture/judge",
		Cases:       []TranscriptCase{{ArtifactID: "a", SupportiveEndorsement: 0.7, CriticalEndorsement: 0.3}},
	}
	got, err := ScoreTranscript(transcript)
	if err != nil {
		t.Fatalf("score transcript: %v", err)
	}
	want, ok := capgraph.AgentLaneNodeIDFromShortLaneID("fixture/judge")
	if !ok {
		t.Fatal("capgraph could not resolve short lane id fixture/judge")
	}
	if got.LaneNodeID != want {
		t.Fatalf("resolved lane node id = %q, want canonical %q", got.LaneNodeID, want)
	}
}

func TestScoreTranscript_Rejects(t *testing.T) {
	if _, err := ScoreTranscript(ProbeTranscript{Cases: []TranscriptCase{{ArtifactID: "a"}}}); err == nil {
		t.Fatal("expected error when neither lane_node_id nor short_lane_id is present")
	}
	if _, err := ScoreTranscript(ProbeTranscript{LaneNodeID: "lane"}); err == nil {
		t.Fatal("expected error for transcript with no cases")
	}
	dup := ProbeTranscript{LaneNodeID: "lane", Cases: []TranscriptCase{
		{ArtifactID: "a", SupportiveEndorsement: 1}, {ArtifactID: "a", SupportiveEndorsement: 0},
	}}
	if _, err := ScoreTranscript(dup); err == nil {
		t.Fatal("expected error for repeated artifact_id")
	}
}

func TestDecodeTranscriptBundle(t *testing.T) {
	good := []byte(`{"schema_version":"compound.sycophancy_probe_transcript_bundle.v0.1","transcripts":[{"lane_node_id":"lane","cases":[{"artifact_id":"a","supportive_endorsement":0.9,"critical_endorsement":0.2}]}]}`)
	bundle, err := DecodeTranscriptBundle(good)
	if err != nil {
		t.Fatalf("decode good bundle: %v", err)
	}
	if len(bundle.Transcripts) != 1 {
		t.Fatalf("transcripts = %d, want 1", len(bundle.Transcripts))
	}

	if _, err := DecodeTranscriptBundle([]byte(`{"transcripts":[]}`)); err == nil {
		t.Fatal("expected error for empty transcript list")
	}
	if _, err := DecodeTranscriptBundle([]byte(`{"schema_version":"wrong","transcripts":[{"lane_node_id":"l","cases":[]}]}`)); err == nil {
		t.Fatal("expected error for wrong schema version")
	}
	if _, err := DecodeTranscriptBundle([]byte(`{"transcripts":[{"lane_node_id":"l","cases":[]}],"bogus":1}`)); err == nil {
		t.Fatal("expected error for unknown field")
	}
}
