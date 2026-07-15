package mem

import "strings"

// B1 (agent-lane beliefs) — RED BASELINE STUB.
//
// This is the deliberately permissive first cut: the valve is a no-op and the resolver
// GUESSES an entity when a lane ref does not resolve. It exists only so the falsifier suite
// (belief_test.go) compiles and RUNS RED — every guard the binding taxonomy demands is
// missing here, so the falsifiers b/i/k/MR5/l/vocab must fail. The next commit implements
// the real constructors + effective-class valve and turns them green.

// BeliefCanonClasses is the closed set of canon classes a lane belief may carry.
var BeliefCanonClasses = []string{"status", "research_fact"}

const (
	BeliefPredicateTendsTo   = "tends_to"
	BeliefPredicateCapableOf = "capable_of"
)

// BeliefPredicates are the only predicates a lane belief may use.
var BeliefPredicates = []string{BeliefPredicateTendsTo, BeliefPredicateCapableOf}

// TendencyVocabulary is the closed-small tendency slug set (Q1).
var TendencyVocabulary = []string{
	"falsifier-first",
	"receipt-faithful",
	"scope-creep",
	"adjacent-fixer",
	"ceremony-skipper",
}

// TendencyObjects are the two stances a tendency belief may take.
var TendencyObjects = []string{"observed", "cooled"}

// CapabilityObject is the single object a capability belief carries.
const CapabilityObject = "confirmed"

// LaneResolver is the B0 lane-identity door (satisfied by *capgraph.Store).
type LaneResolver interface {
	ResolveLaneRef(ref string) (nodeID string, ok bool)
}

// TendencyBeliefDraft — RED stub: builds a draft without validating the shape.
func TendencyBeliefDraft(resolver LaneResolver, laneRef, tendencySlug, object, foldTag string) (Candidate, error) {
	entity, err := resolveBeliefEntity(resolver, laneRef)
	if err != nil {
		return Candidate{}, err
	}
	c := Candidate{
		Entity:         entity,
		Predicate:      BeliefPredicateTendsTo,
		Object:         strings.TrimSpace(object),
		Derivation:     "deterministic",
		ProvenanceLane: strings.TrimSpace(foldTag),
		ClaimType:      "status",
		CanonClass:     "status",
		Qualifiers:     map[string]string{"domain": strings.TrimSpace(tendencySlug)},
		Visibility:     "contextual",
	}
	if err := ValidateBeliefDraft(c); err != nil {
		return Candidate{}, err
	}
	return c, nil
}

// CapabilityBeliefDraft — RED stub: builds a draft without validating the shape.
func CapabilityBeliefDraft(resolver LaneResolver, laneRef, taskClassSlug, foldTag string) (Candidate, error) {
	entity, err := resolveBeliefEntity(resolver, laneRef)
	if err != nil {
		return Candidate{}, err
	}
	c := Candidate{
		Entity:         entity,
		Predicate:      BeliefPredicateCapableOf,
		Object:         CapabilityObject,
		Derivation:     "deterministic",
		ProvenanceLane: strings.TrimSpace(foldTag),
		ClaimType:      "",
		CanonClass:     "research_fact",
		Qualifiers:     map[string]string{"domain": strings.TrimSpace(taskClassSlug)},
		Visibility:     "contextual",
	}
	if err := ValidateBeliefDraft(c); err != nil {
		return Candidate{}, err
	}
	return c, nil
}

// resolveBeliefEntity — RED stub: GUESSES the entity when the lane ref does not resolve,
// the exact no-guessing violation falsifier (l) exists to catch.
func resolveBeliefEntity(resolver LaneResolver, laneRef string) (string, error) {
	laneRef = strings.TrimSpace(laneRef)
	if resolver == nil {
		return laneRef, nil // GUESS (stub): no resolver, no refusal
	}
	nodeID, ok := resolver.ResolveLaneRef(laneRef)
	if !ok {
		return laneRef, nil // GUESS (stub): unresolved, no refusal
	}
	return nodeID, nil
}

// ValidateBeliefDraft — RED stub: accepts everything.
func ValidateBeliefDraft(c Candidate) error {
	return nil
}
