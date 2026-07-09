package lanebeliefs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"compound/internal/candidates"
	"compound/internal/mem"
)

const (
	BeliefPredicateTendsTo   = "tends_to"
	BeliefPredicateCapableOf = "capable_of"

	TendencyObjectObserved = "observed"
	TendencyObjectCooled   = "cooled"

	CapabilityObjectConfirmed = "confirmed"

	FoldProvenanceLane   = "deterministic-fold:lanebeliefs"
	BeliefPromotionBasis = candidates.BasisRuntimeProof
)

type BeliefEvidence struct {
	WorkID          string `json:"work_id"`
	EvidenceEventID string `json:"evidence_event_id"`
	MoveType        string `json:"move_type,omitempty"`
	Outcome         string `json:"outcome,omitempty"`
	ReceiptCount    int    `json:"receipt_count,omitempty"`
	TransitionCount int    `json:"transition_count,omitempty"`
	FileChangeCount int    `json:"file_change_count,omitempty"`
}

type TendencyBeliefInput struct {
	SubjectLaneNodeID string
	Domain            string
	Object            string
	Evidence          BeliefEvidence
	Confidence        float64
	ObservationTime   time.Time
}

type CapabilityBeliefInput struct {
	SubjectLaneNodeID string
	Domain            string
	Evidence          BeliefEvidence
	Confidence        float64
	ObservationTime   time.Time
}

type beliefCandidateShape struct {
	Kind              string
	SubjectLaneNodeID string
	Predicate         string
	Domain            string
	Object            string
	ClaimType         string
	CanonClass        string
	Evidence          BeliefEvidence
	Confidence        float64
	ObservationTime   time.Time
	State             string
}

func NewTendencyBeliefCandidate(in TendencyBeliefInput) (mem.Candidate, error) {
	return beliefCandidateFromShape(beliefCandidateShape{
		Kind:              "tendency-belief",
		SubjectLaneNodeID: in.SubjectLaneNodeID,
		Predicate:         BeliefPredicateTendsTo,
		Domain:            in.Domain,
		Object:            in.Object,
		ClaimType:         "status",
		CanonClass:        "status",
		Evidence:          in.Evidence,
		Confidence:        in.Confidence,
		ObservationTime:   in.ObservationTime,
	})
}

func NewCapabilityBeliefCandidate(in CapabilityBeliefInput) (mem.Candidate, error) {
	return beliefCandidateFromShape(beliefCandidateShape{
		Kind:              "capability-belief",
		SubjectLaneNodeID: in.SubjectLaneNodeID,
		Predicate:         BeliefPredicateCapableOf,
		Domain:            in.Domain,
		Object:            CapabilityObjectConfirmed,
		ClaimType:         "",
		CanonClass:        "research_fact",
		Evidence:          in.Evidence,
		Confidence:        in.Confidence,
		ObservationTime:   in.ObservationTime,
	})
}

func beliefCandidateFromShape(shape beliefCandidateShape) (mem.Candidate, error) {
	shape.SubjectLaneNodeID = strings.TrimSpace(shape.SubjectLaneNodeID)
	shape.Predicate = strings.TrimSpace(shape.Predicate)
	shape.Domain = strings.TrimSpace(shape.Domain)
	shape.Object = strings.TrimSpace(shape.Object)
	shape.ClaimType = strings.TrimSpace(shape.ClaimType)
	shape.CanonClass = strings.TrimSpace(shape.CanonClass)
	shape.Kind = strings.TrimSpace(shape.Kind)
	shape.Evidence.WorkID = strings.TrimSpace(shape.Evidence.WorkID)
	shape.Evidence.EvidenceEventID = strings.TrimSpace(shape.Evidence.EvidenceEventID)
	shape.Evidence.MoveType = strings.TrimSpace(shape.Evidence.MoveType)
	shape.Evidence.Outcome = strings.TrimSpace(shape.Evidence.Outcome)
	shape.State = strings.TrimSpace(shape.State)

	if shape.SubjectLaneNodeID == "" {
		return mem.Candidate{}, fmt.Errorf("belief subject lane node id required")
	}
	if shape.Predicate == "" {
		return mem.Candidate{}, fmt.Errorf("belief predicate required")
	}
	if shape.Domain == "" {
		return mem.Candidate{}, fmt.Errorf("belief domain qualifier required")
	}
	if shape.Object == "" {
		return mem.Candidate{}, fmt.Errorf("belief object required")
	}
	if shape.Evidence.WorkID == "" {
		return mem.Candidate{}, fmt.Errorf("belief evidence work_id required")
	}
	if shape.Evidence.EvidenceEventID == "" {
		return mem.Candidate{}, fmt.Errorf("belief evidence_event_id required")
	}
	if shape.Predicate == BeliefPredicateTendsTo && shape.Object != TendencyObjectObserved && shape.Object != TendencyObjectCooled {
		return mem.Candidate{}, fmt.Errorf("tendency-belief object %q not allowed", shape.Object)
	}
	if shape.Predicate == BeliefPredicateCapableOf && shape.Object != CapabilityObjectConfirmed {
		return mem.Candidate{}, fmt.Errorf("capability-belief object %q not allowed", shape.Object)
	}

	effective := mem.EffectiveCanonClass(shape.ClaimType, shape.CanonClass)
	if effective != "status" && effective != "research_fact" {
		return mem.Candidate{}, fmt.Errorf("belief effective canon class %q refused", effective)
	}
	if shape.Confidence <= 0 {
		shape.Confidence = 0.5
	}
	if shape.ObservationTime.IsZero() {
		shape.ObservationTime = time.Unix(0, 0).UTC()
	}
	state := shape.State
	if state == "" {
		state = "parked"
	}
	if state != "proposed" && state != "parked" {
		return mem.Candidate{}, fmt.Errorf("belief candidate state %q refused", state)
	}

	payload := struct {
		Kind      string         `json:"kind"`
		Entity    string         `json:"entity"`
		Predicate string         `json:"predicate"`
		Domain    string         `json:"domain"`
		Object    string         `json:"object"`
		Class     string         `json:"effective_canon_class"`
		Evidence  BeliefEvidence `json:"evidence"`
	}{
		Kind:      shape.Kind,
		Entity:    shape.SubjectLaneNodeID,
		Predicate: shape.Predicate,
		Domain:    shape.Domain,
		Object:    shape.Object,
		Class:     effective,
		Evidence:  shape.Evidence,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return mem.Candidate{}, err
	}
	sum := sha256.Sum256(payloadBytes)
	hash := hex.EncodeToString(sum[:])
	id := "cand_lanebelief_" + hash[:20]

	return mem.Candidate{
		ID:              id,
		Entity:          shape.SubjectLaneNodeID,
		Predicate:       shape.Predicate,
		Object:          shape.Object,
		ObservationDate: shape.ObservationTime.UTC().Format(time.RFC3339),
		Confidence:      shape.Confidence,
		Derivation:      "deterministic",
		ProvenanceLane:  FoldProvenanceLane,
		ClaimType:       shape.ClaimType,
		CanonClass:      shape.CanonClass,
		Qualifiers:      map[string]string{"domain": shape.Domain},
		Tags:            []string{"lane-belief", shape.Kind},
		PromotionBasis:  BeliefPromotionBasis,
		SourceTraceRef:  "compound://events/" + shape.Evidence.EvidenceEventID,
		SnippetHash:     "sha256:" + hash,
		Visibility:      "operational",
		State:           state,
		CreatedAt:       shape.ObservationTime.UTC().Format(time.RFC3339),
	}, nil
}
