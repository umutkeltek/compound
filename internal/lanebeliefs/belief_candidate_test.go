package lanebeliefs

import (
	"strings"
	"testing"
	"time"

	"compound/internal/capgraph"
	"compound/internal/mem"
)

var beliefTestTime = time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

func TestNewTendencyBeliefCandidateShape(t *testing.T) {
	nodeID, ok := capgraph.AgentLaneNodeIDFromShortLaneID("fixture/judge")
	if !ok {
		t.Fatal("fixture lane should resolve")
	}
	cand, err := NewTendencyBeliefCandidate(TendencyBeliefInput{
		SubjectLaneNodeID: nodeID,
		Domain:            "scope-creep",
		Object:            TendencyObjectObserved,
		Evidence: BeliefEvidence{
			WorkID:          "work_scope",
			EvidenceEventID: "rec_scope",
			MoveType:        "control_plane_repair",
			Outcome:         "completed",
			ReceiptCount:    1,
			FileChangeCount: 2,
		},
		Confidence:      0.61,
		ObservationTime: beliefTestTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cand.Entity != nodeID {
		t.Fatalf("entity = %q, want canonical node id %q", cand.Entity, nodeID)
	}
	if cand.Predicate != BeliefPredicateTendsTo || cand.Object != TendencyObjectObserved {
		t.Fatalf("shape = %s/%s, want tends_to/observed", cand.Predicate, cand.Object)
	}
	if cand.ClaimType != "status" || cand.CanonClass != "status" {
		t.Fatalf("class = claim_type %q canon_class %q, want status/status", cand.ClaimType, cand.CanonClass)
	}
	if cand.Qualifiers["domain"] != "scope-creep" {
		t.Fatalf("domain qualifier = %q", cand.Qualifiers["domain"])
	}
	if cand.ProvenanceLane != FoldProvenanceLane || cand.ProvenanceLane == "fixture/judge" || cand.ProvenanceLane == nodeID {
		t.Fatalf("provenance_lane = %q, want fold tag only", cand.ProvenanceLane)
	}
	if cand.PromotionBasis != BeliefPromotionBasis || cand.State != "parked" {
		t.Fatalf("basis/state = %q/%q, want runtime_proof/parked", cand.PromotionBasis, cand.State)
	}
}

func TestNewCapabilityBeliefCandidateShape(t *testing.T) {
	nodeID, ok := capgraph.AgentLaneNodeIDFromShortLaneID("fixture/extract")
	if !ok {
		t.Fatal("fixture lane should resolve")
	}
	cand, err := NewCapabilityBeliefCandidate(CapabilityBeliefInput{
		SubjectLaneNodeID: nodeID,
		Domain:            "tests-from-spec",
		Evidence: BeliefEvidence{
			WorkID:          "work_capability",
			EvidenceEventID: "tr_capability",
			MoveType:        "control_plane_repair",
			Outcome:         "completed",
			ReceiptCount:    1,
		},
		Confidence:      0.71,
		ObservationTime: beliefTestTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cand.Entity != nodeID || cand.Predicate != BeliefPredicateCapableOf || cand.Object != CapabilityObjectConfirmed {
		t.Fatalf("shape = entity %q predicate %q object %q", cand.Entity, cand.Predicate, cand.Object)
	}
	if cand.ClaimType != "" || cand.CanonClass != "research_fact" {
		t.Fatalf("class = claim_type %q canon_class %q, want untyped/research_fact", cand.ClaimType, cand.CanonClass)
	}
	if cand.Qualifiers["domain"] != "tests-from-spec" {
		t.Fatalf("domain qualifier = %q", cand.Qualifiers["domain"])
	}
}

func TestTendencyBeliefRequiresDomainQualifier(t *testing.T) {
	nodeID, ok := capgraph.AgentLaneNodeIDFromShortLaneID("fixture/judge")
	if !ok {
		t.Fatal("fixture lane should resolve")
	}
	_, err := NewTendencyBeliefCandidate(TendencyBeliefInput{
		SubjectLaneNodeID: nodeID,
		Object:            TendencyObjectObserved,
		Evidence:          BeliefEvidence{WorkID: "work_scope", EvidenceEventID: "rec_scope"},
	})
	if err == nil || !strings.Contains(err.Error(), "domain") {
		t.Fatalf("missing domain err = %v, want refusal", err)
	}
}

func TestBeliefCandidateValveRefusesDecisionEffectiveClass(t *testing.T) {
	_, err := beliefCandidateFromShape(beliefCandidateShape{
		SubjectLaneNodeID: "agent-lane:fixture",
		Predicate:         BeliefPredicateTendsTo,
		Domain:            "scope-creep",
		Object:            TendencyObjectObserved,
		ClaimType:         "decision",
		CanonClass:        "",
		Evidence:          BeliefEvidence{WorkID: "work_decision", EvidenceEventID: "rec_decision"},
		ObservationTime:   beliefTestTime,
	})
	if err == nil || !strings.Contains(err.Error(), mem.EffectiveCanonClass("decision", "")) {
		t.Fatalf("decision effective-class err = %v, want refusal", err)
	}
}
