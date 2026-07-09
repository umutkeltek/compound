package lanebeliefs

import (
	"strings"

	"compound/internal/capgraph"
	"compound/internal/envelope"
)

const (
	LaneResolutionResolved   = "resolved"
	LaneResolutionUnresolved = "unresolved"
)

type LaneResolution struct {
	Outcome string `json:"outcome"`
	LaneID  string `json:"lane_id,omitempty"`
	NodeID  string `json:"node_id,omitempty"`
}

func ResolveReceiptLane(receipt envelope.Receipt) LaneResolution {
	laneID := strings.TrimSpace(receipt.Producer.LaneID)
	if laneID == "" {
		return LaneResolution{Outcome: LaneResolutionUnresolved}
	}
	nodeID, ok := capgraph.AgentLaneNodeIDFromShortLaneID(laneID)
	if !ok {
		return LaneResolution{Outcome: LaneResolutionUnresolved, LaneID: laneID}
	}
	return LaneResolution{Outcome: LaneResolutionResolved, LaneID: laneID, NodeID: nodeID}
}
