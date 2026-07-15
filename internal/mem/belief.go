package mem

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// B1 (agent-lane beliefs): lane-belief shapes + producer-side effective-class valve.
//
// Lane beliefs are ordinary low-risk memory Candidates ABOUT agent-lane nodes. This file is
// the producer-side guard: the only well-formed belief drafts are the two ratified shapes,
// and every field a draft carries is either a closed-vocabulary token or a validated
// slug/id — never operator-personal free text. B1 writes NOTHING to any store; the
// constructors return draft Candidate values and the exported valve refuses malformed ones,
// so B2's deterministic fold (and any future caller) can only ever emit well-formed,
// low-risk, non-clobbering, privacy-safe belief candidates.
//
// Authority: docs_private/plan/packets/2026-07-09-agent-lane-beliefs.md, BINDING TAXONOMY.
//   - Rule 1: the entity is minted ONLY by the B0 lane-identity door (LaneResolver, satisfied
//     by *capgraph.Store.ResolveLaneRef) — an unresolvable ref yields no draft, never a guess.
//   - Rule 2 / MR-6: the valve runs on the EFFECTIVE canon class computed by the EXISTING
//     EffectiveCanonClass (canon_class.go); it never re-derives the class here.
//   - Rule 4: provenance_lane = the fold's OWN tag (not the subject, not an evidence producer).
//   - Rule 7 / MR-5: closed structured vocabulary only; free text is forbidden and the domain
//     qualifier is mandatory (without it, ClusterKey supersession clobbers sibling tendencies).
//
// mem depends on the narrow LaneResolver seam, never on capgraph directly (no import cycle).

// BeliefCanonClasses is the closed set of canon classes a lane belief may carry — a strict
// subset of the low_risk tier. The valve refuses any draft whose EFFECTIVE class is not one
// of these: doctrine about lanes (decision/principle/behavior) stays operator-authored.
var BeliefCanonClasses = []string{"status", "research_fact"}

const (
	BeliefPredicateTendsTo   = "tends_to"
	BeliefPredicateCapableOf = "capable_of"
)

// BeliefPredicates are the only predicates a lane belief may use.
var BeliefPredicates = []string{BeliefPredicateTendsTo, BeliefPredicateCapableOf}

// TendencyVocabulary is the closed-small tendency slug set (Q1: closed-small, extended
// BY EVIDENCE later — never by a caller editing this list). A tendency belief's domain
// qualifier must be one of these.
var TendencyVocabulary = []string{
	"falsifier-first",
	"receipt-faithful",
	"scope-creep",
	"adjacent-fixer",
	"ceremony-skipper",
}

// TendencyObjects are the two stances a tendency belief may take (mutable; supersedes within
// one tendency).
var TendencyObjects = []string{"observed", "cooled"}

// CapabilityObject is the single object a capability belief carries; capability beliefs
// accumulate (they do not supersede).
const CapabilityObject = "confirmed"

const (
	beliefDomainQualifier = "domain"        // the ONLY qualifier key a belief draft may carry
	beliefDerivation      = "deterministic" // beliefs come from a deterministic fold
	beliefVisibility      = "contextual"    // Q2: contextual, NEVER ambient
	agentLaneEntityPrefix = "agent-lane:"   // capgraph agentLaneNodeKind + ":" (stable public id)
	beliefTendencyClaim   = "status"
	beliefTendencyCanon   = "status"
	beliefCapabilityCanon = "research_fact"
)

// LaneResolver is the B0 lane-identity door (satisfied by *capgraph.Store). It is the ONLY
// way a belief entity is minted: a laneRef resolves to its canonical agent-lane node id, or
// it does not resolve and no draft is produced (rule 1 + falsifier l).
type LaneResolver interface {
	ResolveLaneRef(ref string) (nodeID string, ok bool)
}

var (
	// beliefSlugRe matches a lowercase kebab slug (tendency domain, task-class domain, fold tag).
	beliefSlugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	// beliefRefRe matches a whitespace-free ref/id (evidence refs, source trace, candidate id).
	beliefRefRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_./-]*$`)
)

// TendencyBeliefDraft builds a tendency-belief DRAFT: entity = the resolved agent-lane node,
// predicate tends_to, domain = tendencySlug (∈ TendencyVocabulary), object ∈ {observed,
// cooled}, claim_type/canon_class = status, provenance_lane = foldTag. It writes nothing; an
// invalid draft is unrepresentable — the constructor routes through ValidateBeliefDraft and
// returns the valve's error instead of a partial draft.
func TendencyBeliefDraft(resolver LaneResolver, laneRef, tendencySlug, object, foldTag string) (Candidate, error) {
	entity, err := resolveBeliefEntity(resolver, laneRef)
	if err != nil {
		return Candidate{}, err
	}
	foldTag = strings.TrimSpace(foldTag)
	if foldTag == "" {
		return Candidate{}, fmt.Errorf("tendency belief draft: empty fold tag (provenance_lane is the fold's own tag; mandatory)")
	}
	c := Candidate{
		Entity:         entity,
		Predicate:      BeliefPredicateTendsTo,
		Object:         strings.TrimSpace(object),
		Derivation:     beliefDerivation,
		ProvenanceLane: foldTag,
		ClaimType:      beliefTendencyClaim,
		CanonClass:     beliefTendencyCanon,
		Qualifiers:     map[string]string{beliefDomainQualifier: strings.TrimSpace(tendencySlug)},
		Visibility:     beliefVisibility,
	}
	if err := ValidateBeliefDraft(c); err != nil {
		return Candidate{}, err
	}
	return c, nil
}

// CapabilityBeliefDraft builds a capability-belief DRAFT: entity = the resolved agent-lane
// node, predicate capable_of, domain = taskClassSlug (a validated lowercase slug; task
// classes grow — the cross-lane gate, not a closed list, is what stops fabricated
// capabilities), object confirmed, claim_type "" (untyped), canon_class research_fact,
// provenance_lane = foldTag. It writes nothing and routes through the valve.
func CapabilityBeliefDraft(resolver LaneResolver, laneRef, taskClassSlug, foldTag string) (Candidate, error) {
	entity, err := resolveBeliefEntity(resolver, laneRef)
	if err != nil {
		return Candidate{}, err
	}
	foldTag = strings.TrimSpace(foldTag)
	if foldTag == "" {
		return Candidate{}, fmt.Errorf("capability belief draft: empty fold tag (provenance_lane is the fold's own tag; mandatory)")
	}
	c := Candidate{
		Entity:         entity,
		Predicate:      BeliefPredicateCapableOf,
		Object:         CapabilityObject,
		Derivation:     beliefDerivation,
		ProvenanceLane: foldTag,
		ClaimType:      "",
		CanonClass:     beliefCapabilityCanon,
		Qualifiers:     map[string]string{beliefDomainQualifier: strings.TrimSpace(taskClassSlug)},
		Visibility:     beliefVisibility,
	}
	if err := ValidateBeliefDraft(c); err != nil {
		return Candidate{}, err
	}
	return c, nil
}

// resolveBeliefEntity mints the belief entity through the B0 door and only the B0 door. A nil
// resolver, an empty ref, or a ref that does not resolve to an imported agent-lane node all
// error — the entity is never guessed from the raw ref (rule 1 + falsifier l).
func resolveBeliefEntity(resolver LaneResolver, laneRef string) (string, error) {
	if resolver == nil {
		return "", fmt.Errorf("belief draft: nil lane resolver (entities come only from the B0 ResolveLaneRef door)")
	}
	laneRef = strings.TrimSpace(laneRef)
	if laneRef == "" {
		return "", fmt.Errorf("belief draft: empty lane ref")
	}
	nodeID, ok := resolver.ResolveLaneRef(laneRef)
	if !ok {
		return "", fmt.Errorf("belief draft: lane ref %q does not resolve to an imported agent-lane node (no guessed entity)", laneRef)
	}
	return nodeID, nil
}

// ValidateBeliefDraft is the exported producer-side valve. It is callable on ANY candidate —
// the constructors route through it (so an invalid draft is unrepresentable) and it also
// guards B2's fold path and any future caller. It refuses anything that is not one of the two
// ratified belief shapes, running the rule-2 effective-class check on EffectiveCanonClass and
// requiring every field to be a closed-vocabulary token or a validated slug/id.
func ValidateBeliefDraft(c Candidate) error {
	// 1. Entity: an agent-lane node id from the B0 resolver (rule 1). Never empty, never guessed.
	if strings.TrimSpace(c.Entity) == "" {
		return fmt.Errorf("belief draft: empty entity (must be a resolved agent-lane node id)")
	}
	if !strings.HasPrefix(c.Entity, agentLaneEntityPrefix) || len(c.Entity) <= len(agentLaneEntityPrefix) {
		return fmt.Errorf("belief draft: entity %q is not an agent-lane node id (want %q… from the B0 resolver)", c.Entity, agentLaneEntityPrefix)
	}

	// 2. Predicate ∈ the closed belief predicates; it selects the shape.
	if !oneOf(c.Predicate, BeliefPredicates) {
		return fmt.Errorf("belief draft: predicate %q is not a belief predicate (allowed: %v)", c.Predicate, BeliefPredicates)
	}

	// 3. THE rule-2 / MR-6 effective-class valve: compute the EFFECTIVE class from the EXISTING
	// EffectiveCanonClass (never re-derived here) and refuse anything outside the belief classes,
	// naming the derived class + its risk tier + the derivation so the refusal is legible.
	// (A bare claim_type=decision with empty canon_class derives operator_permanent — falsifier i.)
	eff := EffectiveCanonClass(c.ClaimType, c.CanonClass)
	if !oneOf(eff, BeliefCanonClasses) {
		return fmt.Errorf("belief draft: effective canon class %q (derived from claim_type=%q canon_class=%q; risk tier %q) is not a belief class (allowed: %v)",
			eff, c.ClaimType, c.CanonClass, CanonRiskTier(eff), BeliefCanonClasses)
	}

	// 4. Per-shape exact taxonomy fields.
	switch c.Predicate {
	case BeliefPredicateTendsTo:
		if c.ClaimType != beliefTendencyClaim || c.CanonClass != beliefTendencyCanon {
			return fmt.Errorf("tendency belief: claim_type and canon_class must both be %q (got claim_type=%q canon_class=%q)", beliefTendencyClaim, c.ClaimType, c.CanonClass)
		}
		if !oneOf(c.Object, TendencyObjects) {
			return fmt.Errorf("tendency belief: object %q not allowed (allowed: %v)", c.Object, TendencyObjects)
		}
	case BeliefPredicateCapableOf:
		if c.ClaimType != "" {
			return fmt.Errorf("capability belief: claim_type must be untyped/empty (got %q)", c.ClaimType)
		}
		if c.CanonClass != beliefCapabilityCanon {
			return fmt.Errorf("capability belief: canon_class must be %q (got %q)", beliefCapabilityCanon, c.CanonClass)
		}
		if c.Object != CapabilityObject {
			return fmt.Errorf("capability belief: object must be %q (got %q)", CapabilityObject, c.Object)
		}
	}

	// 5. Domain qualifier (MR-5): mandatory, the ONLY qualifier key, validated per shape.
	if err := validateBeliefQualifiers(c); err != nil {
		return err
	}

	// 6. Free-text refusal (rule 7 / falsifier k): every free-text-capable field is EMPTY.
	if err := refuseBeliefFreeText(c); err != nil {
		return err
	}

	// 7. Provenance + lifecycle fields are validated slugs/ids/enums, never free text.
	return validateBeliefProvenance(c)
}

// validateBeliefQualifiers enforces MR-5: exactly one qualifier, the mandatory "domain" key,
// validated against the shape's vocabulary/slug rule. Any extra key or a free-text value is a
// clobber/privacy hole and is refused.
func validateBeliefQualifiers(c Candidate) error {
	if len(c.Qualifiers) != 1 {
		return fmt.Errorf("belief draft: qualifiers must be exactly {%q: <slug>} (closed structured vocabulary), got %d key(s): %v",
			beliefDomainQualifier, len(c.Qualifiers), sortedQualifierKeys(c.Qualifiers))
	}
	domain, ok := c.Qualifiers[beliefDomainQualifier]
	if !ok {
		return fmt.Errorf("belief draft: missing mandatory %q qualifier (MR-5: without it ClusterKey supersession clobbers sibling tendencies)", beliefDomainQualifier)
	}
	if strings.TrimSpace(domain) == "" {
		return fmt.Errorf("belief draft: empty %q qualifier (MR-5)", beliefDomainQualifier)
	}
	switch c.Predicate {
	case BeliefPredicateTendsTo:
		if !oneOf(domain, TendencyVocabulary) {
			return fmt.Errorf("tendency belief: domain %q is not in the closed tendency vocabulary (allowed: %v)", domain, TendencyVocabulary)
		}
	case BeliefPredicateCapableOf:
		if !beliefSlugRe.MatchString(domain) {
			return fmt.Errorf("capability belief: task-class domain %q must be a lowercase slug", domain)
		}
	}
	return nil
}

// refuseBeliefFreeText requires every free-text-capable Candidate field to be empty on a
// belief draft (rule 7 / falsifier k): a belief carries only closed vocabulary + validated
// slugs/ids, so operator-personal prose has no field to hide in.
func refuseBeliefFreeText(c Candidate) error {
	checks := []struct {
		field string
		empty bool
	}{
		{"reason", c.Reason == ""},
		{"salience", c.Salience == ""},
		{"snippet_hash", c.SnippetHash == ""},
		{"expires_at", c.ExpiresAt == ""},           // belief classes are not volatile
		{"promotion_basis", c.PromotionBasis == ""}, // B1/B2 drafts are propose-only
		{"adjudicated_by", c.AdjudicatedBy == ""},
		{"adjudicated_at", c.AdjudicatedAt == ""},
		{"tags", len(c.Tags) == 0},
		{"topic_refs", len(c.TopicRefs) == 0},
		{"annotations", len(c.Annotations) == 0},
		{"expectation", c.Expectation == nil},
	}
	for _, f := range checks {
		if !f.empty {
			return fmt.Errorf("belief draft: field %q must be empty (rule 7: a belief carries only closed vocabulary + validated slugs/ids, never free text)", f.field)
		}
	}
	return nil
}

// validateBeliefProvenance validates the provenance + lifecycle fields as slugs/ids/enums so
// none can carry prose: provenance_lane is the fold's own slug tag (rule 4), evidence/source
// refs are whitespace-free ids, visibility is a known non-ambient tier, and any timestamps
// are RFC3339 — not free text.
func validateBeliefProvenance(c Candidate) error {
	if strings.TrimSpace(c.ProvenanceLane) == "" {
		return fmt.Errorf("belief draft: empty provenance_lane (rule 4: the fold's own tag is mandatory)")
	}
	if !beliefSlugRe.MatchString(c.ProvenanceLane) {
		return fmt.Errorf("belief draft: provenance_lane %q must be a lowercase slug (the fold's own tag, never free text)", c.ProvenanceLane)
	}
	if c.Derivation != beliefDerivation {
		return fmt.Errorf("belief draft: derivation must be %q (deterministic fold), got %q", beliefDerivation, c.Derivation)
	}
	if c.Confidence < 0 || c.Confidence > 1 {
		return fmt.Errorf("belief draft: confidence %v out of range [0,1]", c.Confidence)
	}
	if !oneOf(c.Visibility, Visibilities) {
		return fmt.Errorf("belief draft: visibility %q is not a known tier (allowed: %v)", c.Visibility, Visibilities)
	}
	if c.Visibility == "ambient" {
		return fmt.Errorf("belief draft: visibility must never be ambient (beliefs are never ambient)")
	}
	if c.State != "" && c.State != "proposed" {
		return fmt.Errorf("belief draft: state %q not allowed on a draft (want empty or %q)", c.State, "proposed")
	}
	if c.ObservationDate != "" {
		if _, err := time.Parse(time.RFC3339, c.ObservationDate); err != nil {
			return fmt.Errorf("belief draft: observation_date %q must be RFC3339, not free text", c.ObservationDate)
		}
	}
	if c.CreatedAt != "" {
		if _, err := time.Parse(time.RFC3339, c.CreatedAt); err != nil {
			return fmt.Errorf("belief draft: created_at %q must be RFC3339, not free text", c.CreatedAt)
		}
	}
	if c.ID != "" && !beliefRefRe.MatchString(c.ID) {
		return fmt.Errorf("belief draft: id %q must be a ref-shaped id, not free text", c.ID)
	}
	if c.SourceTraceRef != "" && !beliefRefRe.MatchString(c.SourceTraceRef) {
		return fmt.Errorf("belief draft: source_trace_ref %q must be a ref/id, not free text", c.SourceTraceRef)
	}
	for _, ref := range c.EvidenceRefs {
		if !beliefRefRe.MatchString(ref) {
			return fmt.Errorf("belief draft: evidence_ref %q must be a ref/id, not free text", ref)
		}
	}
	return nil
}

func sortedQualifierKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
