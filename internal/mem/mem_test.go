package mem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
)

var t0 = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s
}

func cand(entity, pred, obj, obs string) Candidate {
	return Candidate{Entity: entity, Predicate: pred, Object: obj, ObservationDate: obs, Confidence: 0.9, Derivation: "extracted", PromotionBasis: candidates.BasisRuntimeProof, SourceTraceRef: "trace_x", SnippetHash: "deadbeef"}
}

func TestCheapLaneCandidateRequiresAdjudicationAndPromotionPreservesLane(t *testing.T) {
	s := newStore(t)
	captured, err := s.Capture([]Candidate{{
		Entity:          "operator",
		Predicate:       "preference.editor",
		Object:          "prefer reversible edits",
		ObservationDate: "2026-07-01T00:00:00Z",
		Confidence:      1,
		Derivation:      "extracted",
		ProvenanceLane:  "cheap-lane",
		PromotionBasis:  candidates.BasisRuntimeProof,
		SourceTraceRef:  "trace_cheap_lane",
		SnippetHash:     "sha256:cheap_lane",
		Visibility:      "ambient",
	}}, t0)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if len(captured) != 1 || captured[0].State != "proposed" {
		t.Fatalf("captured = %+v, want proposed candidate", captured)
	}
	before, err := s.Recall("operator", RecallOpts{})
	if err != nil {
		t.Fatalf("recall before adjudication: %v", err)
	}
	if len(before.Claims) != 0 {
		t.Fatalf("cheap-lane candidate surfaced before adjudication: %+v", before.Claims)
	}

	promoted, err := s.Adjudicate(captured[0].ID, "promote", "work_reviewed_cheap_lane", t0)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted.Claim == nil || promoted.Claim.ProvenanceLane != "cheap-lane" {
		t.Fatalf("promoted claim provenance lane = %+v, want cheap-lane", promoted.Claim)
	}
}

// The headline P6 proof: a fact changes over time; the ledger keeps BOTH versions; as-of
// recall returns the right one at each seq; the old claim is superseded by an EDGE, not
// deleted; promotion emits a Transition. This is delta-aware memory on the owned ledger.
func TestDeltaLoop(t *testing.T) {
	s := newStore(t)
	e, p := "project:healthapp", "backend_language"

	c1, _ := s.Capture([]Candidate{cand(e, p, "node", "2026-01-01T00:00:00Z")}, t0)
	r1, err := s.Adjudicate(c1[0].ID, "promote", "", t0)
	if err != nil || r1.Claim == nil {
		t.Fatalf("promote 1: %v", err)
	}
	if r1.Superseded != "" {
		t.Errorf("first claim should supersede nothing")
	}
	seq1 := r1.Claim.Seq

	c2, _ := s.Capture([]Candidate{cand(e, p, "go", "2026-06-01T00:00:00Z")}, t0)
	r2, err := s.Adjudicate(c2[0].ID, "promote", "", t0)
	if err != nil || r2.Claim == nil {
		t.Fatalf("promote 2: %v", err)
	}
	// The change is a supersession (edge), and a Transition records old -> new.
	if r2.Superseded != r1.Claim.ID {
		t.Errorf("second claim should supersede the first; got %q", r2.Superseded)
	}
	if r2.Transition == nil {
		t.Fatal("promotion should emit a Transition")
	}
	if err := r2.Transition.Validate(); err != nil {
		t.Fatalf("emitted transition invalid: %v", err)
	}
	if len(r2.Transition.Projections.Memory) == 0 || r2.Transition.BeforeRefs[0] != r1.Claim.ID {
		t.Errorf("transition should project memory and reference the superseded claim")
	}

	// Recall NOW → only the new value.
	now, _ := s.Recall(e, RecallOpts{})
	if len(now.Claims) != 1 || now.Claims[0].Object != "go" {
		t.Fatalf("recall now should return only 'go'; got %+v", objs(now.Claims))
	}
	// Recall AS OF seq1 → the OLD value (MVCC-by-fold: at that point 'node' was current).
	past, _ := s.Recall(e, RecallOpts{AsOfSeq: seq1})
	if len(past.Claims) != 1 || past.Claims[0].Object != "node" {
		t.Fatalf("recall as-of seq1 should return 'node'; got %+v", objs(past.Claims))
	}
	// History is retained: timeline shows both, oldest -> newest.
	tl, _ := s.Timeline(e)
	if len(tl) != 2 || tl[0].Object != "node" || tl[1].Object != "go" {
		t.Fatalf("timeline should show node -> go; got %+v", objs(tl))
	}
}

// Positions folds the ledger into the current durable stance per narrow fact slot, carrying the
// supersession chain old→new — the spine-native convergence of the Gen-1 position.Build. Proven
// here as the first capability landed on the spine toward the wholesale Gen-1 retirement.
func TestPositionsFoldTopicAndChain(t *testing.T) {
	s := newStore(t)
	e := "project:api"
	// topic "lang": node → go (a supersession)
	c1, _ := s.Capture([]Candidate{cand(e, "lang", "node", "2026-01-01T00:00:00Z")}, t0)
	r1, _ := s.Adjudicate(c1[0].ID, "promote", "", t0)
	c2, _ := s.Capture([]Candidate{cand(e, "lang", "go", "2026-06-01T00:00:00Z")}, t0)
	r2, _ := s.Adjudicate(c2[0].ID, "promote", "", t0)
	// topic "db": a single version
	c3, _ := s.Capture([]Candidate{cand(e, "db", "postgres", "2026-01-01T00:00:00Z")}, t0)
	s.Adjudicate(c3[0].ID, "promote", "", t0)

	pos, err := s.Positions()
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 2 {
		t.Fatalf("expected 2 topic-grouped positions; got %d (%+v)", len(pos), pos)
	}
	by := map[string]Position{}
	for _, p := range pos {
		by[p.Topic] = p
	}
	lang := by["lang"]
	if !strings.HasPrefix(lang.ID, "project:api::lang::slot-") || lang.ActiveObject != "go" || lang.ActiveClaimID != r2.Claim.ID {
		t.Errorf("lang position should be active 'go' in its fact slot (claim %s); got %+v", r2.Claim.ID, lang)
	}
	// the chain carries the FULL history oldest→newest, not just the latest value.
	if len(lang.ClaimChain) != 2 || lang.ClaimChain[0] != r1.Claim.ID || lang.ClaimChain[1] != r2.Claim.ID {
		t.Errorf("lang chain should be [node-claim, go-claim] oldest→newest; got %v", lang.ClaimChain)
	}
	if lang.Status != "active" {
		t.Errorf("lang status should be active; got %q", lang.Status)
	}
	if by["db"].ActiveObject != "postgres" || len(by["db"].ClaimChain) != 1 {
		t.Errorf("db position should be single-version postgres; got %+v", by["db"])
	}
}

func TestCandidateFirstNoCanonWithoutPromote(t *testing.T) {
	s := newStore(t)
	c, _ := s.Capture([]Candidate{cand("e", "p", "v", "")}, t0)
	if _, err := s.Adjudicate(c[0].ID, "park", "", t0); err != nil {
		t.Fatal(err)
	}
	if claims, _ := s.loadClaims(); len(claims) != 0 {
		t.Errorf("park must not write a claim; got %d", len(claims))
	}
	c2, _ := s.Capture([]Candidate{cand("e", "p", "v", "")}, t0)
	if _, err := s.Adjudicate(c2[0].ID, "reject", "", t0); err != nil {
		t.Fatal(err)
	}
	if claims, _ := s.loadClaims(); len(claims) != 0 {
		t.Errorf("reject must not write a claim; got %d", len(claims))
	}
	c3, _ := s.Capture([]Candidate{cand("e", "p", "v", "")}, t0)
	res, err := s.Adjudicate(c3[0].ID, "no_promote", "", t0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Candidate.State != "no_promote" {
		t.Fatalf("no_promote candidate state = %q", res.Candidate.State)
	}
	if claims, _ := s.loadClaims(); len(claims) != 0 {
		t.Errorf("no_promote must not write a claim; got %d", len(claims))
	}
}

func TestAdjudicatePromoteRequiresPromotionBasis(t *testing.T) {
	s := newStore(t)
	noBasis := cand("e", "p", "v", "")
	noBasis.PromotionBasis = ""
	got, _ := s.Capture([]Candidate{noBasis}, t0)
	if _, err := s.Adjudicate(got[0].ID, "promote", "", t0); err == nil {
		t.Fatal("promote without promotion_basis should fail at memory authority boundary")
	}
	if claims, _ := s.loadClaims(); len(claims) != 0 {
		t.Fatalf("basis-less promotion wrote %d claim(s)", len(claims))
	}

	withBasis, _ := s.Capture([]Candidate{cand("e", "p", "v2", "")}, t0)
	res, err := s.Adjudicate(withBasis[0].ID, "promote", "", t0)
	if err != nil || res.Claim == nil {
		t.Fatalf("promote with valid basis should pass: %v", err)
	}
	if res.Claim.PromotionBasis != candidates.BasisRuntimeProof {
		t.Fatalf("claim basis = %q", res.Claim.PromotionBasis)
	}
}

func TestAdjudicateRejectsUnprovenExplicitConfirmationBasis(t *testing.T) {
	s := newStore(t)
	seed := cand("operator", "preference.communication", "prefer concise updates", "")
	seed.PromotionBasis = candidates.BasisExplicitConfirmation
	seed.ClaimType = "preference"
	captured, err := s.Capture([]Candidate{seed}, t0)
	if err != nil || len(captured) != 1 {
		t.Fatalf("capture = %+v, err=%v", captured, err)
	}

	if _, err := s.Adjudicate(captured[0].ID, "promote", "work_unproven_confirmation", t0); err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("ordinary adjudication accepted unproven explicit confirmation: %v", err)
	}
	if claims, err := s.loadClaims(); err != nil || len(claims) != 0 {
		t.Fatalf("unproven confirmation wrote claims=%d err=%v", len(claims), err)
	}
}

func TestOperatorConfirmationEvidenceUsesReservedActorBoundDoor(t *testing.T) {
	s := newStore(t)
	seed := cand("operator", "preference.communication", "prefer concise updates", "")
	seed.PromotionBasis = ""
	seed.ClaimType = "preference"
	captured, err := s.Capture([]Candidate{seed}, t0)
	if err != nil || len(captured) != 1 {
		t.Fatalf("capture = %+v, err=%v", captured, err)
	}
	if _, err := s.Adjudicate(captured[0].ID, "park", "work_confirmation", t0); err != nil {
		t.Fatal(err)
	}

	if _, err := s.RecordEvidence(EvidenceItem{
		SourceKind: "operator_confirmation",
		SourceID:   captured[0].ID,
		QuoteText:  "Keep this preference.",
		Actor:      "operator:forged-generic-path",
	}, t0); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("generic evidence path accepted reserved operator confirmation: %v", err)
	}
	if _, err := s.RecordOperatorConfirmation(captured[0].ID, "Keep this preference.", "agent:model", t0); err == nil || !strings.Contains(err.Error(), "operator provenance") {
		t.Fatalf("dedicated confirmation accepted non-operator actor: %v", err)
	}

	first, err := s.RecordOperatorConfirmation(captured[0].ID, "Keep this preference.", "operator:one", t0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.RecordOperatorConfirmation(captured[0].ID, "Keep this preference.", "operator:two", t0)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatalf("confirmation evidence id did not bind actor: %s", first.ID)
	}
	if first.Actor != "operator:one" || first.Visibility != "private" {
		t.Fatalf("confirmation evidence provenance = %+v", first)
	}
}

func TestOperatorConfirmationRequiresParkedCandidate(t *testing.T) {
	for _, state := range []string{"proposed", "rejected", "no_promote"} {
		t.Run(state, func(t *testing.T) {
			s := newStore(t)
			seed := cand("operator", "preference.communication", "prefer concise updates", "")
			seed.PromotionBasis = ""
			seed.ClaimType = "preference"
			captured, err := s.Capture([]Candidate{seed}, t0)
			if err != nil || len(captured) != 1 {
				t.Fatalf("capture = %+v, err=%v", captured, err)
			}
			if state != "proposed" {
				if _, err := s.Adjudicate(captured[0].ID, state, "work_confirmation_state", t0); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.RecordOperatorConfirmation(captured[0].ID, "Keep this preference.", "operator:test", t0); err == nil || !strings.Contains(err.Error(), "parked") {
				t.Fatalf("%s candidate accepted confirmation: %v", state, err)
			}
		})
	}
}

func TestAdjudicateExplicitConfirmationRequiresMatchingOperatorEvidence(t *testing.T) {
	s := newStore(t)
	spanRef := "transcript-span:sha256:session:line:1:sha256:preference"
	seed := cand("operator", "preference.communication", "prefer concise progress updates", "")
	seed.PromotionBasis = ""
	seed.ClaimType = "preference"
	seed.SourceTraceRef = ""
	seed.EvidenceRefs = []string{spanRef, spanRef}
	captured, err := s.Capture([]Candidate{seed}, t0)
	if err != nil || len(captured) != 1 {
		t.Fatalf("capture = %+v, err=%v", captured, err)
	}
	candidateID := captured[0].ID
	if _, err := s.Adjudicate(candidateID, "park", "work_user_shadow", t0); err != nil {
		t.Fatalf("park: %v", err)
	}
	if _, err := s.Adjudicate(candidateID, "promote", "work_user_shadow", t0); err == nil {
		t.Fatal("ordinary promotion must fail before confirmation evidence exists")
	}

	otherSeed := seed
	otherSeed.Object = "prefer detailed weekly reviews"
	otherCaptured, err := s.Capture([]Candidate{otherSeed}, t0)
	if err != nil || len(otherCaptured) != 1 {
		t.Fatalf("capture other = %+v, err=%v", otherCaptured, err)
	}
	if _, err := s.Adjudicate(otherCaptured[0].ID, "park", "work_user_shadow", t0); err != nil {
		t.Fatal(err)
	}
	mismatched, err := s.RecordOperatorConfirmation(otherCaptured[0].ID, "Confirmed for another candidate.", "operator:test", t0)
	if err != nil {
		t.Fatal(err)
	}
	nonOperator := writeRawEvidence(t, s, EvidenceItem{
		ID:         "ev_non_operator_confirmation",
		SourceKind: "operator_confirmation",
		SourceID:   candidateID,
		QuoteText:  "Agent attempted confirmation.",
		Actor:      "agent:test",
	})
	wrongKind, err := s.RecordEvidence(EvidenceItem{
		SourceKind: "session_note",
		SourceID:   candidateID,
		QuoteText:  "Operator-like text in the wrong evidence kind.",
		Actor:      "operator:test",
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	tampered, err := s.RecordOperatorConfirmation(candidateID, "Tamper-check confirmation.", "operator:test", t0)
	if err != nil {
		t.Fatal(err)
	}
	tampered.Actor = "operator:tampered-after-write"
	if err := appendJSONL(s.evidenceLog(), &tampered); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		ref  string
	}{
		{name: "missing", ref: ""},
		{name: "bogus", ref: "ev_missing"},
		{name: "mismatched candidate", ref: mismatched.ID},
		{name: "non operator", ref: nonOperator.ID},
		{name: "wrong kind", ref: wrongKind.ID},
		{name: "tampered content address", ref: tampered.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.AdjudicateExplicitConfirmation(candidateID, tc.ref, "work_user_shadow", t0); err == nil {
				t.Fatalf("confirmation ref %q should fail", tc.ref)
			}
			if claims, err := s.loadClaims(); err != nil || len(claims) != 0 {
				t.Fatalf("invalid confirmation wrote claims=%d err=%v", len(claims), err)
			}
		})
	}

	confirmation, err := s.RecordOperatorConfirmation(candidateID, "Yes, keep this as my communication preference.", "operator:test", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Adjudicate(candidateID, "promote", "work_user_shadow", t0); err == nil {
		t.Fatal("ordinary promotion must fail even after confirmation evidence exists")
	}
	res, err := s.AdjudicateExplicitConfirmation(candidateID, confirmation.ID, "work_user_shadow", t0)
	if err != nil {
		t.Fatalf("explicit confirmation: %v", err)
	}
	if res.Claim == nil || res.Claim.PromotionBasis != candidates.BasisExplicitConfirmation {
		t.Fatalf("promoted claim = %+v", res.Claim)
	}
	wantEvidence := []string{candidateID, spanRef, confirmation.ID}
	if len(res.Claim.EvidenceRefs) != len(wantEvidence) {
		t.Fatalf("claim evidence = %v, want %v", res.Claim.EvidenceRefs, wantEvidence)
	}
	for i, want := range wantEvidence {
		if res.Claim.EvidenceRefs[i] != want {
			t.Fatalf("claim evidence[%d] = %q, want %q (%v)", i, res.Claim.EvidenceRefs[i], want, res.Claim.EvidenceRefs)
		}
	}
}

func writeRawEvidence(t *testing.T, s *Store, evidence EvidenceItem) EvidenceItem {
	t.Helper()
	if evidence.ID == "" {
		t.Fatal("raw evidence fixture needs an id")
	}
	if evidence.ContentHash == "" {
		evidence.ContentHash = "legacy-fixture"
	}
	if evidence.ObservedAt == "" {
		evidence.ObservedAt = rfc3339(t0)
	}
	if err := appendJSONL(s.evidenceLog(), &evidence); err != nil {
		t.Fatal(err)
	}
	return evidence
}

func TestOperationalDecisionFoldIgnoresGenericSameSlotShadow(t *testing.T) {
	s := newStore(t)
	legacy := Claim{
		ID:         "claim_legacy_freeze_authority",
		Seq:        1,
		Entity:     "decision:legacy-freeze",
		Predicate:  "decided",
		Object:     "legacy operator freeze",
		ClaimType:  "decision",
		Visibility: "operational",
		Qualifiers: map[string]string{
			"kind":             "freeze",
			"source":           "decisions-repo",
			"decision_id":      "legacy-freeze",
			"lifecycle_status": "accepted",
			"reviewed_by":      "operator:test",
		},
	}
	shadow := Claim{
		ID:         "claim_generic_shadow",
		Seq:        2,
		Entity:     legacy.Entity,
		Predicate:  legacy.Predicate,
		Object:     "generic same-slot shadow",
		ClaimType:  "decision",
		Visibility: "operational",
		Supersedes: legacy.ID,
		Qualifiers: map[string]string{
			"kind":             "doctrine",
			"source":           "generic-memory",
			"decision_id":      "legacy-freeze",
			"lifecycle_status": "accepted",
		},
	}
	if err := appendJSONL(s.claimsLog(), &legacy); err != nil {
		t.Fatal(err)
	}
	if err := appendJSONL(s.claimsLog(), &shadow); err != nil {
		t.Fatal(err)
	}
	if err := appendJSONL(s.edgesLog(), &Edge{Seq: shadow.Seq, Kind: EdgeKindSupersedes, From: shadow.ID, To: legacy.ID}); err != nil {
		t.Fatal(err)
	}
	ordinary, err := s.AllCurrent()
	if err != nil {
		t.Fatal(err)
	}
	if len(ordinary) != 1 || ordinary[0].ID != shadow.ID {
		t.Fatalf("fixture did not reproduce generic MVCC shadow: %+v", ordinary)
	}
	operational, err := s.AllCurrentOperationalDecisions()
	if err != nil {
		t.Fatal(err)
	}
	if len(operational) != 1 || operational[0].ID != legacy.ID {
		t.Fatalf("generic shadow erased operational authority: %+v", operational)
	}
}

func TestOperationalDecisionFoldIgnoresGenericReviewMark(t *testing.T) {
	s := newStore(t)
	protected := Claim{
		ID:         "claim_stamped_freeze_authority",
		Seq:        1,
		Entity:     "decision:stamped-freeze",
		Predicate:  "decided",
		Object:     "operator freeze",
		ClaimType:  "decision",
		Visibility: "operational",
		Qualifiers: map[string]string{
			OperationalDecisionAuthorityQualifier: OperationalDecisionAuthorityValue,
			"kind":                                "freeze",
			"source":                              "decisions-repo",
			"decision_id":                         "stamped-freeze",
			"lifecycle_status":                    "accepted",
			"reviewed_by":                         "operator:test",
		},
	}
	if err := appendJSONL(s.claimsLog(), &protected); err != nil {
		t.Fatal(err)
	}
	if err := appendJSONL(s.edgesLog(), &Edge{
		Seq:          2,
		Kind:         EdgeKindNeedsReview,
		From:         "synth_contradiction_generic",
		To:           protected.ID,
		Confidence:   1,
		EvidenceRefs: []string{"cand_generic"},
	}); err != nil {
		t.Fatal(err)
	}
	ordinary, err := s.AllCurrent()
	if err != nil {
		t.Fatal(err)
	}
	if len(ordinary) != 0 {
		t.Fatalf("fixture did not reproduce generic review quarantine: %+v", ordinary)
	}
	operational, err := s.AllCurrentOperationalDecisions()
	if err != nil {
		t.Fatal(err)
	}
	if len(operational) != 1 || operational[0].ID != protected.ID {
		t.Fatalf("generic review mark erased operational authority: %+v", operational)
	}
	if operational[0].State != ClaimStatePromoted || len(operational[0].ReviewRefs) != 0 || operational[0].ConfidenceDelta != 0 {
		t.Fatalf("protected authority leaked derived review state: %+v", operational[0])
	}
}

func TestDuplicateRejected(t *testing.T) {
	s := newStore(t)
	c1, _ := s.Capture([]Candidate{cand("e", "p", "same", "")}, t0)
	s.Adjudicate(c1[0].ID, "promote", "", t0)
	c2, _ := s.Capture([]Candidate{cand("e", "p", "same", "")}, t0)
	if len(c2) != 0 {
		t.Fatalf("duplicate capture should reinforce current claim without a candidate; got %d", len(c2))
	}
	if claims, _ := s.loadClaims(); len(claims) != 1 {
		t.Errorf("duplicate must not append a second claim; got %d", len(claims))
	}
}

func TestRecallRelevanceGate(t *testing.T) {
	s := newStore(t)
	c := cand("e", "secret", "v", "")
	c.Visibility = "retired"
	got, _ := s.Capture([]Candidate{c}, t0)
	s.Adjudicate(got[0].ID, "promote", "", t0)
	// retired is hidden from default recall...
	def, _ := s.Recall("e", RecallOpts{})
	if len(def.Claims) != 0 || def.Filtered != 1 {
		t.Errorf("retired claim should be gated out of default recall; got %d claims, %d filtered", len(def.Claims), def.Filtered)
	}
	// ...but available when explicitly included.
	inc, _ := s.Recall("e", RecallOpts{IncludeRetired: true})
	if len(inc.Claims) != 1 {
		t.Errorf("retired claim should appear with IncludeRetired; got %d", len(inc.Claims))
	}
}

func TestRecallAndTimelineResolveCanonicalEntityToken(t *testing.T) {
	s := newStore(t)
	c, _ := s.Capture([]Candidate{cand("compound", "capability_authority_surface", "owned graph", "")}, t0)
	if _, err := s.Adjudicate(c[0].ID, "promote", "", t0); err != nil {
		t.Fatal(err)
	}

	recall, err := s.Recall("Compound", RecallOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if recall.Entity != "compound" || len(recall.Claims) != 1 || recall.Claims[0].Predicate != "capability_authority_surface" {
		t.Fatalf("recall Compound = entity %q claims %+v; want canonical compound claim", recall.Entity, recall.Claims)
	}

	timeline, err := s.Timeline("Compound")
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) != 1 || timeline[0].Entity != "compound" {
		t.Fatalf("timeline Compound = %+v; want canonical compound claim", timeline)
	}
}

func TestRecallResolvesEntityRegistryAlias(t *testing.T) {
	s := newStore(t)
	if _, err := s.RegisterEntity(Entity{Slug: "project:compound", Kind: "project", Aliases: []string{"Compound"}}, t0); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Capture([]Candidate{cand("project:compound", "status", "active", "")}, t0)
	if _, err := s.Adjudicate(c[0].ID, "promote", "", t0); err != nil {
		t.Fatal(err)
	}

	recall, err := s.Recall("Compound", RecallOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if recall.Entity != "project:compound" || len(recall.Claims) != 1 {
		t.Fatalf("recall Compound alias = entity %q claims %+v; want project:compound", recall.Entity, recall.Claims)
	}
}

func TestRecallPrefersUniqueCaseFoldedClaimBeforeAlias(t *testing.T) {
	s := newStore(t)
	if _, err := s.RegisterEntity(Entity{Slug: "repo:compound", Kind: "repo", Aliases: []string{"compound"}}, t0); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Capture([]Candidate{cand("compound", "capability_authority_surface", "owned graph", "")}, t0)
	if _, err := s.Adjudicate(c[0].ID, "promote", "", t0); err != nil {
		t.Fatal(err)
	}

	recall, err := s.Recall("Compound", RecallOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if recall.Entity != "compound" || len(recall.Claims) != 1 {
		t.Fatalf("recall Compound = entity %q claims %+v; want unique lower-case claim before alias", recall.Entity, recall.Claims)
	}
}

func TestRecallDoesNotGuessAmbiguousCaseFoldedEntity(t *testing.T) {
	s := newStore(t)
	first, _ := s.Capture([]Candidate{cand("compound", "status", "lower", "")}, t0)
	if _, err := s.Adjudicate(first[0].ID, "promote", "", t0); err != nil {
		t.Fatal(err)
	}
	second, _ := s.Capture([]Candidate{cand("Compound", "status", "title", "")}, t0)
	if _, err := s.Adjudicate(second[0].ID, "promote", "", t0); err != nil {
		t.Fatal(err)
	}

	recall, err := s.Recall("COMPOUND", RecallOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if recall.Entity != "COMPOUND" || len(recall.Claims) != 0 {
		t.Fatalf("ambiguous case-folded recall guessed entity %q claims %+v", recall.Entity, recall.Claims)
	}
}

func TestRecallIncludeEvidenceSurfacesAnnotatedCandidatesOnlyWhenRequested(t *testing.T) {
	s := newStore(t)
	got, err := s.Capture([]Candidate{cand("knowledge:paper:alpha", "finding", "useful", "")}, t0)
	if err != nil {
		t.Fatal(err)
	}
	annotated, err := s.AnnotateCandidate(got[0].ID, CandidateAnnotation{
		Kind:             "panel_verdict",
		Decision:         "annotate",
		OriginalDecision: "needs_human",
		RunID:            "prun_fixture",
		Rationale:        "panel found useful but not canon",
		Confidence:       0.77,
		EvidenceRefs:     []string{"panel_run:prun_fixture"},
	}, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if annotated.State != "parked" || len(annotated.Annotations) != 1 {
		t.Fatalf("annotated candidate = %+v", annotated)
	}
	def, err := s.Recall("knowledge:paper:alpha", RecallOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(def.Claims) != 0 || len(def.EvidenceCandidates) != 0 {
		t.Fatalf("default recall must stay promoted-only, got claims=%d evidence=%d", len(def.Claims), len(def.EvidenceCandidates))
	}
	withEvidence, err := s.Recall("knowledge:paper:alpha", RecallOpts{IncludeEvidence: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(withEvidence.Claims) != 0 || len(withEvidence.EvidenceCandidates) != 1 {
		t.Fatalf("include-evidence recall = claims %d evidence %d", len(withEvidence.Claims), len(withEvidence.EvidenceCandidates))
	}
	if ann := withEvidence.EvidenceCandidates[0].Annotations[0]; ann.Decision != "annotate" || ann.OriginalDecision != "needs_human" || ann.RunID != "prun_fixture" {
		t.Fatalf("annotation = %+v", ann)
	}
}

func TestDoctorReportsHops(t *testing.T) {
	s := newStore(t)
	c, _ := s.Capture([]Candidate{cand("e", "p", "v", "")}, t0)
	s.Adjudicate(c[0].ID, "promote", "", t0)
	r := s.Doctor()
	if !r.OK {
		t.Errorf("doctor should be OK after a clean promote; note=%q", r.Note)
	}
	want := map[string]string{"capture": "ok", "promotion": "ok", "sync/embedding": "skipped", "retrieval": "ok", "recall-quality": "ok", "authority": "ok"}
	seen := map[string]bool{}
	for _, h := range r.Hops {
		seen[h.Name] = true
		if exp, ok := want[h.Name]; ok && h.Status != exp {
			t.Errorf("hop %q status = %q, want %q (%s)", h.Name, h.Status, exp, h.Detail)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("missing hop %q", name)
		}
	}
}

func TestDoctorReportsRecallQualityHop(t *testing.T) {
	s := newStore(t)
	c, _ := s.Capture([]Candidate{cand("compound", "capability_authority_surface", "owned graph", "")}, t0)
	if _, err := s.Adjudicate(c[0].ID, "promote", "", t0); err != nil {
		t.Fatal(err)
	}

	r := s.Doctor()
	h := hopByName(t, r, "recall-quality")
	if h.Status != "ok" {
		t.Fatalf("recall-quality hop status=%q detail=%q", h.Status, h.Detail)
	}
	if !strings.Contains(h.Detail, "Compound") || !strings.Contains(h.Detail, "compound") {
		t.Fatalf("recall-quality detail should name probe and canonical entity, got %q", h.Detail)
	}
}

// The rented-engine seam: NoEngine raises ErrUnsupported EXPLICITLY (never a silent empty),
// and a real engine answers breadth gated by the ledger's allowlist.
func TestDoctorWithConfiguredEngineReportsHealthyHop(t *testing.T) {
	s := storeWith(t, "project:api", "language", "go")

	r := s.DoctorWithEngine(&LexicalEngine{})
	if !r.OK {
		t.Fatalf("doctor with lexical engine should pass: %+v", r)
	}
	h := hopByName(t, r, "sync/embedding")
	if h.Status != "ok" {
		t.Fatalf("configured engine hop status=%q detail=%q", h.Status, h.Detail)
	}
	if !strings.Contains(h.Detail, "lexical") || !strings.Contains(h.Detail, "ledger_unchanged=true") {
		t.Fatalf("configured engine hop should report rented engine and ledger immutability, got %q", h.Detail)
	}
}

func TestDoctorWithConfiguredEngineFailsDegradedHop(t *testing.T) {
	s := storeWith(t, "project:api", "language", "go")

	r := s.DoctorWithEngine(unavailableEngine{})
	if r.OK {
		t.Fatalf("doctor should fail when configured engine is degraded: %+v", r)
	}
	h := hopByName(t, r, "sync/embedding")
	if h.Status != "fail" || !strings.Contains(h.Detail, "degraded") {
		t.Fatalf("configured engine degradation should be explicit fail, got status=%q detail=%q", h.Status, h.Detail)
	}
}

func hopByName(t *testing.T, r Report, name string) Hop {
	t.Helper()
	for _, h := range r.Hops {
		if h.Name == name {
			return h
		}
	}
	t.Fatalf("missing hop %q in %+v", name, r.Hops)
	return Hop{}
}

func TestEngineSeam(t *testing.T) {
	s := newStore(t)
	for _, o := range []string{"node", "go"} { // node first, then go supersedes it → go is current
		c, _ := s.Capture([]Candidate{cand("project:api", "language", o, "")}, t0)
		s.Adjudicate(c[0].ID, "promote", "", t0)
	}
	// NoEngine: breadth is unsupported, and it says so explicitly.
	if _, err := s.QueryBreadth("language", 10, NoEngine{}); err != ErrUnsupported {
		t.Errorf("NoEngine breadth should return ErrUnsupported, got %v", err)
	}
	if _, err := (NoEngine{}).Query("x", GateList, nil, 1); err != ErrUnsupported {
		t.Errorf("NoEngine.Query must raise, not silently no-op; got %v", err)
	}
	// LexicalEngine: real breadth result, and only the CURRENT value (node was superseded by go).
	hits, err := s.QueryBreadth("language", 10, &LexicalEngine{})
	if err != nil {
		t.Fatalf("lexical query: %v", err)
	}
	if len(hits) != 1 || hits[0].Object != "go" {
		t.Errorf("breadth should be gated to the current claim 'go'; got %v", objs(hits))
	}
}

// The relevance gate is honored at the engine via the allowlist: a retired claim is never in
// the allowed set, so breadth cannot surface it.
func TestEngineRelevanceGate(t *testing.T) {
	s := newStore(t)
	c := cand("project:api", "secret_key", "abc123", "")
	c.Visibility = "retired"
	got, _ := s.Capture([]Candidate{c}, t0)
	s.Adjudicate(got[0].ID, "promote", "", t0)
	hits, err := s.QueryBreadth("secret", 10, &LexicalEngine{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("retired claim must be gated out of breadth via the allowlist; got %v", objs(hits))
	}
}

// A lost/zeroed seq counter must SELF-HEAL above everything on disk (not reset to 1 and mint a
// duplicate seq that corrupts the fold); a corrupt counter must fail loud.
func TestNextSeqSelfHeal(t *testing.T) {
	s := newStore(t)
	promote := func(obj string) Claim {
		c, _ := s.Capture([]Candidate{cand("e", "p", obj, "")}, t0)
		r, err := s.Adjudicate(c[0].ID, "promote", "", t0)
		if err != nil || r.Claim == nil {
			t.Fatalf("promote %s: %v", obj, err)
		}
		return *r.Claim
	}
	promote("v1")
	if c2 := promote("v2"); c2.Seq != 2 {
		t.Fatalf("expected seq 2, got %d", c2.Seq)
	}
	// simulate a crash-mid-write / partial restore that zeroes the counter but keeps the logs.
	if err := os.WriteFile(filepath.Join(s.home, "ledger", "seq"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if c3 := promote("v3"); c3.Seq != 3 {
		t.Errorf("a zeroed counter must self-heal to 3 (above on-disk), got %d", c3.Seq)
	}
	seqs := map[int64]bool{}
	claims, _ := s.loadClaims()
	for _, c := range claims {
		if seqs[c.Seq] {
			t.Errorf("duplicate seq %d in the ledger", c.Seq)
		}
		seqs[c.Seq] = true
	}
	// a corrupt (non-numeric) counter must fail loud, not reset.
	os.WriteFile(filepath.Join(s.home, "ledger", "seq"), []byte("garbage"), 0o644)
	c, _ := s.Capture([]Candidate{cand("e", "p", "v4", "")}, t0)
	if _, err := s.Adjudicate(c[0].ID, "promote", "", t0); err == nil {
		t.Error("a corrupt seq counter must fail loud, not silently reset")
	}
}

// Even if the supersedes EDGE append is lost/torn, the new claim's own Supersedes pointer must keep
// recall to a single current claim (no two simultaneously-current versions).
func TestFoldHonorsSupersedesWithoutEdge(t *testing.T) {
	s := newStore(t)
	c1, _ := s.Capture([]Candidate{cand("e", "p", "v1", "")}, t0)
	s.Adjudicate(c1[0].ID, "promote", "", t0)
	c2, _ := s.Capture([]Candidate{cand("e", "p", "v2", "")}, t0)
	s.Adjudicate(c2[0].ID, "promote", "", t0)
	// drop the edges log entirely (simulate a lost/torn supersedes-edge append).
	os.Remove(filepath.Join(s.home, "ledger", "edges.jsonl"))
	rr, _ := s.Recall("e", RecallOpts{})
	if len(rr.Claims) != 1 || rr.Claims[0].Object != "v2" {
		t.Errorf("with the supersedes edge lost, the claim's Supersedes pointer must keep recall to one current (v2); got %v", objs(rr.Claims))
	}
}

func objs(cs []Claim) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Object
	}
	return out
}
