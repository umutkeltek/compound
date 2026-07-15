package mem

import (
	"strings"
	"testing"

	"compound/internal/capgraph"
)

// newLaneResolver opens a capgraph store in a test home, imports exactly ONE agent-lane
// node, and returns the store (the real B0 LaneResolver) plus the imported node id. The
// store is the discriminator falsifier (l) relies on: a bare harness or actor string has a
// deterministic short-id mapping but maps to a node no import produced, so it never resolves.
func newLaneResolver(t *testing.T) (*capgraph.Store, string) {
	t.Helper()
	store, err := capgraph.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open capgraph: %v", err)
	}
	tuple := capgraph.AgentLaneTuple{Harness: "codex", Route: "auto", Model: "test-model", Effort: "balanced"}
	nodeID := capgraph.AgentLaneNodeID(tuple)
	node := capgraph.Node{
		ID:           nodeID,
		Kind:         "agent-lane",
		Name:         "codex / auto / test-model / balanced",
		Weight:       0.25,
		EvidenceOnly: true,
	}
	if err := store.Add(node); err != nil {
		t.Fatalf("seed agent-lane node: %v", err)
	}
	if got, ok := store.ResolveLaneRef(nodeID); !ok || got != nodeID {
		t.Fatalf("fixture lane must resolve: got %q ok=%v", got, ok)
	}
	return store, nodeID
}

const beliefFoldTag = "belief-fold-v1"

// personalFreeText is SYNTHETIC operator-shaped prose (repo is public — never real data).
// The space makes it fail every ref/slug/date/tier check, proving prose cannot smuggle in.
const personalFreeText = "user-home-town: example-city"

// requireNoFreeText asserts a well-formed belief draft carries no free-text-capable field.
func requireNoFreeText(t *testing.T, c Candidate) {
	t.Helper()
	if c.Reason != "" || c.Salience != "" || c.SnippetHash != "" || c.ExpiresAt != "" ||
		c.PromotionBasis != "" || c.AdjudicatedBy != "" || c.AdjudicatedAt != "" ||
		c.SourceTraceRef != "" || len(c.Tags) != 0 || len(c.TopicRefs) != 0 ||
		len(c.EvidenceRefs) != 0 || len(c.Annotations) != 0 || c.Expectation != nil {
		t.Fatalf("belief draft carried a free-text-capable field: %+v", c)
	}
}

// F-green: both shapes construct with a resolver-backed lane and match the taxonomy
// field-for-field; the exported valve accepts each.
func TestBeliefGreenBothShapesConstruct(t *testing.T) {
	store, laneID := newLaneResolver(t)

	t.Run("F-green_tendency", func(t *testing.T) {
		c, err := TendencyBeliefDraft(store, laneID, "falsifier-first", "observed", beliefFoldTag)
		if err != nil {
			t.Fatalf("tendency draft: %v", err)
		}
		if c.Entity != laneID {
			t.Fatalf("entity = %q, want resolved node id %q", c.Entity, laneID)
		}
		if c.Predicate != BeliefPredicateTendsTo {
			t.Fatalf("predicate = %q, want %q", c.Predicate, BeliefPredicateTendsTo)
		}
		if c.Object != "observed" {
			t.Fatalf("object = %q, want observed", c.Object)
		}
		if c.ClaimType != "status" || c.CanonClass != "status" {
			t.Fatalf("claim_type/canon_class = %q/%q, want status/status", c.ClaimType, c.CanonClass)
		}
		if len(c.Qualifiers) != 1 || c.Qualifiers["domain"] != "falsifier-first" {
			t.Fatalf("qualifiers = %v, want {domain: falsifier-first}", c.Qualifiers)
		}
		if c.ProvenanceLane != beliefFoldTag {
			t.Fatalf("provenance_lane = %q, want fold tag %q", c.ProvenanceLane, beliefFoldTag)
		}
		if c.Derivation != "deterministic" {
			t.Fatalf("derivation = %q, want deterministic", c.Derivation)
		}
		if c.Visibility == "ambient" {
			t.Fatalf("belief visibility must never be ambient")
		}
		requireNoFreeText(t, c)
		if err := ValidateBeliefDraft(c); err != nil {
			t.Fatalf("valve rejected a well-formed tendency draft: %v", err)
		}
		// cooled is the other admissible stance.
		if _, err := TendencyBeliefDraft(store, laneID, "scope-creep", "cooled", beliefFoldTag); err != nil {
			t.Fatalf("cooled tendency draft: %v", err)
		}
	})

	t.Run("F-green_capability", func(t *testing.T) {
		c, err := CapabilityBeliefDraft(store, laneID, "build-implement", beliefFoldTag)
		if err != nil {
			t.Fatalf("capability draft: %v", err)
		}
		if c.Entity != laneID {
			t.Fatalf("entity = %q, want resolved node id %q", c.Entity, laneID)
		}
		if c.Predicate != BeliefPredicateCapableOf {
			t.Fatalf("predicate = %q, want %q", c.Predicate, BeliefPredicateCapableOf)
		}
		if c.Object != CapabilityObject {
			t.Fatalf("object = %q, want %q", c.Object, CapabilityObject)
		}
		if c.ClaimType != "" {
			t.Fatalf("claim_type = %q, want untyped/empty", c.ClaimType)
		}
		if c.CanonClass != "research_fact" {
			t.Fatalf("canon_class = %q, want research_fact", c.CanonClass)
		}
		if len(c.Qualifiers) != 1 || c.Qualifiers["domain"] != "build-implement" {
			t.Fatalf("qualifiers = %v, want {domain: build-implement}", c.Qualifiers)
		}
		if c.ProvenanceLane != beliefFoldTag {
			t.Fatalf("provenance_lane = %q, want fold tag %q", c.ProvenanceLane, beliefFoldTag)
		}
		if c.Derivation != "deterministic" {
			t.Fatalf("derivation = %q, want deterministic", c.Derivation)
		}
		requireNoFreeText(t, c)
		if err := ValidateBeliefDraft(c); err != nil {
			t.Fatalf("valve rejected a well-formed capability draft: %v", err)
		}
	})
}

// F-l: an unresolvable lane ref (actor string, bare harness, un-imported node id, nil
// resolver) makes the constructor error — a draft is never built on a guessed entity.
func TestBeliefUnresolvableLaneNoDraft(t *testing.T) {
	store, laneID := newLaneResolver(t)
	cases := []struct {
		name    string
		laneRef string
		nilRes  bool
	}{
		{"bare_harness", "codex", false},
		{"actor_string", "agent-harness:deadbeefdeadbeef", false},
		{"unimported_node_id", "agent-lane:codex--auto--other-model--balanced", false},
		{"empty_ref", "", false},
		{"nil_resolver", laneID, true},
	}
	for _, tc := range cases {
		t.Run("F-l_"+tc.name, func(t *testing.T) {
			var res LaneResolver = store
			if tc.nilRes {
				res = nil
			}
			if _, err := TendencyBeliefDraft(res, tc.laneRef, "falsifier-first", "observed", beliefFoldTag); err == nil {
				t.Fatalf("tendency: expected refusal for %q, got a draft (guessed entity)", tc.laneRef)
			}
			if _, err := CapabilityBeliefDraft(res, tc.laneRef, "build-implement", beliefFoldTag); err == nil {
				t.Fatalf("capability: expected refusal for %q, got a draft (guessed entity)", tc.laneRef)
			}
		})
	}
}

// validTendency returns a fresh, valve-accepted tendency draft for mutation tests.
func validTendency(t *testing.T, store *capgraph.Store, laneID string) Candidate {
	t.Helper()
	c, err := TendencyBeliefDraft(store, laneID, "falsifier-first", "observed", beliefFoldTag)
	if err != nil {
		t.Fatalf("build base tendency draft: %v", err)
	}
	return c
}

// F-b: a draft FORCED (struct literal — the constructors will not build it) to a non-belief
// effective class is refused, and the error names the effective class.
func TestBeliefEffectiveClassValveRefuses(t *testing.T) {
	_, laneID := newLaneResolver(t)
	base := func() Candidate {
		return Candidate{
			Entity:         laneID,
			Predicate:      BeliefPredicateTendsTo,
			Object:         "observed",
			ClaimType:      "decision",
			Qualifiers:     map[string]string{"domain": "falsifier-first"},
			Derivation:     "deterministic",
			ProvenanceLane: beliefFoldTag,
			Visibility:     "contextual",
		}
	}
	for _, canon := range []string{"behavior", "principle", "decision"} {
		t.Run("F-b_"+canon, func(t *testing.T) {
			c := base()
			c.CanonClass = canon
			err := ValidateBeliefDraft(c)
			if err == nil {
				t.Fatalf("valve accepted a %s-class belief draft", canon)
			}
			if !strings.Contains(err.Error(), canon) {
				t.Fatalf("error must name the effective class %q: %v", canon, err)
			}
		})
	}
}

// F-i: bare claim_type=decision with empty canon_class DERIVES operator_permanent; the valve
// runs on the DERIVED class and refuses, with the derivation visible in the error.
func TestBeliefDerivedOperatorPermanentRefused(t *testing.T) {
	_, laneID := newLaneResolver(t)
	c := Candidate{
		Entity:         laneID,
		Predicate:      BeliefPredicateTendsTo,
		Object:         "observed",
		ClaimType:      "decision",
		CanonClass:     "", // empty → EffectiveCanonClass derives "decision" (operator_permanent)
		Qualifiers:     map[string]string{"domain": "falsifier-first"},
		Derivation:     "deterministic",
		ProvenanceLane: beliefFoldTag,
		Visibility:     "contextual",
	}
	err := ValidateBeliefDraft(c)
	if err == nil {
		t.Fatalf("valve accepted a bare claim_type=decision draft")
	}
	msg := err.Error()
	for _, want := range []string{"decision", "operator_permanent", `claim_type="decision"`} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error must make the derivation visible (missing %q): %v", want, err)
		}
	}
}

// F-k: personal-shaped free text planted in every free-text-capable field is refused — none
// of it can reach a valid draft.
func TestBeliefFreeTextRefused(t *testing.T) {
	store, laneID := newLaneResolver(t)
	// sanity: the unmutated base draft is accepted.
	if err := ValidateBeliefDraft(validTendency(t, store, laneID)); err != nil {
		t.Fatalf("base draft must be valid: %v", err)
	}
	mutations := []struct {
		field  string
		mutate func(*Candidate)
	}{
		{"reason", func(c *Candidate) { c.Reason = personalFreeText }},
		{"salience", func(c *Candidate) { c.Salience = personalFreeText }},
		{"snippet_hash", func(c *Candidate) { c.SnippetHash = personalFreeText }},
		{"tags", func(c *Candidate) { c.Tags = []string{personalFreeText} }},
		{"topic_refs", func(c *Candidate) { c.TopicRefs = []string{personalFreeText} }},
		{"expectation", func(c *Candidate) {
			c.Expectation = &CandidateExpectation{Predicate: "note", Object: "x", Detail: personalFreeText}
		}},
		{"annotations", func(c *Candidate) {
			c.Annotations = []CandidateAnnotation{{ID: "ann_1", Rationale: personalFreeText}}
		}},
		{"promotion_basis", func(c *Candidate) { c.PromotionBasis = personalFreeText }},
		{"expires_at", func(c *Candidate) { c.ExpiresAt = personalFreeText }},
		{"adjudicated_by", func(c *Candidate) { c.AdjudicatedBy = personalFreeText }},
		{"observation_date", func(c *Candidate) { c.ObservationDate = personalFreeText }},
		{"source_trace_ref", func(c *Candidate) { c.SourceTraceRef = personalFreeText }},
		{"evidence_refs", func(c *Candidate) { c.EvidenceRefs = []string{personalFreeText} }},
		{"visibility", func(c *Candidate) { c.Visibility = personalFreeText }},
		{"qualifier_extra_key", func(c *Candidate) { c.Qualifiers["note"] = personalFreeText }},
	}
	for _, m := range mutations {
		t.Run("F-k_"+m.field, func(t *testing.T) {
			c := validTendency(t, store, laneID)
			m.mutate(&c)
			if err := ValidateBeliefDraft(c); err == nil {
				t.Fatalf("valve accepted planted free text in %q", m.field)
			}
		})
	}
}

// F-MR5: a missing/empty domain qualifier is refused; two tendencies on one lane yield
// DISTINCT Topic() values (the clobber the mandatory domain prevents).
func TestBeliefDomainQualifierMR5(t *testing.T) {
	store, laneID := newLaneResolver(t)

	t.Run("F-MR5_missing_domain_refused", func(t *testing.T) {
		empty := Candidate{
			Entity: laneID, Predicate: BeliefPredicateTendsTo, Object: "observed",
			ClaimType: "status", CanonClass: "status",
			Qualifiers: map[string]string{}, Derivation: "deterministic",
			ProvenanceLane: beliefFoldTag, Visibility: "contextual",
		}
		if err := ValidateBeliefDraft(empty); err == nil {
			t.Fatalf("valve accepted a belief draft with no domain qualifier")
		}
		nilQ := empty
		nilQ.Qualifiers = nil
		if err := ValidateBeliefDraft(nilQ); err == nil {
			t.Fatalf("valve accepted a belief draft with nil qualifiers")
		}
	})

	t.Run("F-MR5_two_tendencies_distinct_topic", func(t *testing.T) {
		t1, err := TendencyBeliefDraft(store, laneID, "falsifier-first", "observed", beliefFoldTag)
		if err != nil {
			t.Fatalf("t1: %v", err)
		}
		t2, err := TendencyBeliefDraft(store, laneID, "receipt-faithful", "observed", beliefFoldTag)
		if err != nil {
			t.Fatalf("t2: %v", err)
		}
		topic1 := topicFromQualifiers(t1.Predicate, t1.Qualifiers)
		topic2 := topicFromQualifiers(t2.Predicate, t2.Qualifiers)
		if topic1 == topic2 {
			t.Fatalf("sibling tendencies must not share a Topic() cluster key: both %q", topic1)
		}
		if topic1 != "falsifier-first" || topic2 != "receipt-faithful" {
			t.Fatalf("Topic() must be the domain slug: got %q, %q", topic1, topic2)
		}
	})
}

// F-vocab: an unknown tendency slug is refused; product_metric canon class is refused; the
// wrong object for a shape is refused; an empty fold tag is refused.
func TestBeliefVocabularyRefusals(t *testing.T) {
	store, laneID := newLaneResolver(t)

	t.Run("F-vocab_unknown_tendency_slug", func(t *testing.T) {
		if _, err := TendencyBeliefDraft(store, laneID, "made-up-tendency", "observed", beliefFoldTag); err == nil {
			t.Fatalf("constructor accepted an unknown tendency slug")
		}
	})

	t.Run("F-vocab_product_metric_refused", func(t *testing.T) {
		c := Candidate{
			Entity: laneID, Predicate: BeliefPredicateCapableOf, Object: "confirmed",
			ClaimType: "", CanonClass: "product_metric",
			Qualifiers: map[string]string{"domain": "build-implement"},
			Derivation: "deterministic", ProvenanceLane: beliefFoldTag, Visibility: "contextual",
		}
		if err := ValidateBeliefDraft(c); err == nil {
			t.Fatalf("valve accepted a product_metric belief draft (low_risk but not a belief class)")
		}
	})

	t.Run("F-vocab_wrong_object_tendency", func(t *testing.T) {
		if _, err := TendencyBeliefDraft(store, laneID, "falsifier-first", "confirmed", beliefFoldTag); err == nil {
			t.Fatalf("constructor accepted object=confirmed for a tendency belief")
		}
	})

	t.Run("F-vocab_wrong_object_capability", func(t *testing.T) {
		c := Candidate{
			Entity: laneID, Predicate: BeliefPredicateCapableOf, Object: "observed",
			ClaimType: "", CanonClass: "research_fact",
			Qualifiers: map[string]string{"domain": "build-implement"},
			Derivation: "deterministic", ProvenanceLane: beliefFoldTag, Visibility: "contextual",
		}
		if err := ValidateBeliefDraft(c); err == nil {
			t.Fatalf("valve accepted object=observed for a capability belief")
		}
	})

	t.Run("F-vocab_empty_fold_tag", func(t *testing.T) {
		if _, err := TendencyBeliefDraft(store, laneID, "falsifier-first", "observed", ""); err == nil {
			t.Fatalf("constructor accepted an empty fold tag")
		}
		if _, err := CapabilityBeliefDraft(store, laneID, "build-implement", ""); err == nil {
			t.Fatalf("constructor accepted an empty fold tag")
		}
	})
}
