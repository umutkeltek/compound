package lanebeliefs

import (
	"testing"

	"compound/internal/capgraph"
	"compound/internal/envelope"
)

func TestResolveReceiptLaneUnresolvedWhenReceiptHasNoStampedLaneID(t *testing.T) {
	resolution := ResolveReceiptLane(envelope.Receipt{
		Header: envelope.Header{
			SchemaVersion: envelope.SVReceipt,
			ID:            "rec_fixture",
			Producer:      envelope.Producer{Kind: "test", Name: "fixture"},
		},
	})
	if resolution.Outcome != LaneResolutionUnresolved {
		t.Fatalf("outcome = %q, want %q", resolution.Outcome, LaneResolutionUnresolved)
	}
	if resolution.NodeID != "" {
		t.Fatalf("unresolved receipt guessed node id %q", resolution.NodeID)
	}
}

func TestResolveReceiptLaneUsesCapgraphCanonicalNodeID(t *testing.T) {
	rec := envelope.Receipt{
		Header: envelope.Header{
			SchemaVersion: envelope.SVReceipt,
			ID:            "rec_fixture",
			Producer:      envelope.Producer{Kind: "test", Name: "fixture", LaneID: "fixture/judge"},
		},
	}
	resolution := ResolveReceiptLane(rec)
	if resolution.Outcome != LaneResolutionResolved {
		t.Fatalf("outcome = %q, want %q", resolution.Outcome, LaneResolutionResolved)
	}
	want, ok := capgraph.AgentLaneNodeIDFromShortLaneID("fixture/judge")
	if !ok {
		t.Fatal("fixture lane id should map")
	}
	if resolution.NodeID != want {
		t.Fatalf("resolver node id = %q, want capgraph id %q", resolution.NodeID, want)
	}
}
