package mem

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"compound/internal/candidates"
	"fmt"

	"compound/internal/eventstore"
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
	return Candidate{Entity: entity, Predicate: pred, Object: obj, ObservationDate: obs, Confidence: 0.9, Derivation: "extracted", PromotionBasis: candidates.BasisExplicitConfirmation, SourceTraceRef: "trace_x", SnippetHash: "deadbeef"}
}

func promoteForTest(t *testing.T, s *Store, candidateID, workID string, now time.Time) (AdjudicateResult, error) {
	t.Helper()
	candidate, err := s.getCandidate(candidateID)
	if err != nil {
		return AdjudicateResult{}, err
	}
	if candidate.PromotionBasis != candidates.BasisExplicitConfirmation {
		return s.Adjudicate(candidateID, "promote", workID, now)
	}
	if candidate.State != "parked" {
		if _, err := s.Adjudicate(candidateID, "park", workID, now); err != nil {
			return AdjudicateResult{}, err
		}
	}
	evidence, err := s.RecordOperatorConfirmation(candidateID, "test fixture confirms "+candidateID, "operator:test", now)
	if err != nil {
		return AdjudicateResult{}, err
	}
	return s.AdjudicateExplicitConfirmation(candidateID, evidence.ID, workID, now)
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
		PromotionBasis:  candidates.BasisExplicitConfirmation,
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

	promoted, err := promoteForTest(t, s, captured[0].ID, "work_reviewed_cheap_lane", t0)
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
	r1, err := promoteForTest(t, s, c1[0].ID, "", t0)
	if err != nil || r1.Claim == nil {
		t.Fatalf("promote 1: %v", err)
	}
	if r1.Superseded != "" {
		t.Errorf("first claim should supersede nothing")
	}
	seq1 := r1.Claim.Seq

	c2, _ := s.Capture([]Candidate{cand(e, p, "go", "2026-06-01T00:00:00Z")}, t0)
	r2, err := promoteForTest(t, s, c2[0].ID, "", t0)
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
	r1, _ := promoteForTest(t, s, c1[0].ID, "", t0)
	c2, _ := s.Capture([]Candidate{cand(e, "lang", "go", "2026-06-01T00:00:00Z")}, t0)
	r2, _ := promoteForTest(t, s, c2[0].ID, "", t0)
	// topic "db": a single version
	c3, _ := s.Capture([]Candidate{cand(e, "db", "postgres", "2026-01-01T00:00:00Z")}, t0)
	_, _ = promoteForTest(t, s, c3[0].ID, "", t0)

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

	validBasis := cand("e", "p", "v2", "")
	validBasis.PromotionBasis = candidates.BasisRuntimeProof
	withBasis, _ := s.Capture([]Candidate{validBasis}, t0)
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
	for _, tc := range []struct {
		state   string
		verdict string
	}{
		{state: "proposed"},
		{state: "rejected", verdict: "reject"},
		{state: "no_promote", verdict: "no_promote"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			s := newStore(t)
			seed := cand("operator", "preference.communication", "prefer concise updates", "")
			seed.PromotionBasis = ""
			seed.ClaimType = "preference"
			captured, err := s.Capture([]Candidate{seed}, t0)
			if err != nil || len(captured) != 1 {
				t.Fatalf("capture = %+v, err=%v", captured, err)
			}
			if tc.verdict != "" {
				if _, err := s.Adjudicate(captured[0].ID, tc.verdict, "work_confirmation_state", t0); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.RecordOperatorConfirmation(captured[0].ID, "Keep this preference.", "operator:test", t0); err == nil || !strings.Contains(err.Error(), "parked") {
				t.Fatalf("%s candidate accepted confirmation: %v", tc.state, err)
			}
		})
	}
}

func TestAdjudicateExplicitConfirmationRechecksParkedState(t *testing.T) {
	s := newStore(t)
	seed := cand("operator", "preference.communication", "prefer concise updates", "")
	seed.PromotionBasis = ""
	seed.ClaimType = "preference"
	captured, err := s.Capture([]Candidate{seed}, t0)
	if err != nil || len(captured) != 1 {
		t.Fatalf("capture = %+v, err=%v", captured, err)
	}
	if _, err := s.Adjudicate(captured[0].ID, "park", "work_confirmation_state", t0); err != nil {
		t.Fatal(err)
	}
	confirmation, err := s.RecordOperatorConfirmation(captured[0].ID, "Keep this preference.", "operator:test", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Adjudicate(captured[0].ID, "reject", "work_confirmation_state", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdjudicateExplicitConfirmation(captured[0].ID, confirmation.ID, "work_confirmation_state", t0); err == nil || !strings.Contains(err.Error(), "parked") {
		t.Fatalf("confirmation consumption accepted candidate after state changed: %v", err)
	}
	if claims, err := s.loadClaims(); err != nil || len(claims) != 0 {
		t.Fatalf("state-raced confirmation wrote claims=%d err=%v", len(claims), err)
	}
}

func TestAdjudicateExplicitConfirmationSerializesTerminalCandidateState(t *testing.T) {
	s := newStore(t)
	seed := cand("operator", "preference.communication", "prefer concise updates", "")
	seed.PromotionBasis = ""
	seed.ClaimType = "preference"
	captured, err := s.Capture([]Candidate{seed}, t0)
	if err != nil || len(captured) != 1 {
		t.Fatalf("capture = %+v, err=%v", captured, err)
	}
	if _, err := s.Adjudicate(captured[0].ID, "park", "work_confirmation_race", t0); err != nil {
		t.Fatal(err)
	}
	confirmation, err := s.RecordOperatorConfirmation(captured[0].ID, "Keep this preference.", "operator:test", t0)
	if err != nil {
		t.Fatal(err)
	}

	lockAttempts := make(chan struct{}, 2)
	s.beforeLockObserver = func() { lockAttempts <- struct{}{} }
	confirmationRechecked := make(chan struct{})
	releaseConfirmation := make(chan struct{})
	var pauseOnce sync.Once
	s.entityLoader = func() ([]Entity, error) {
		pauseOnce.Do(func() {
			close(confirmationRechecked)
			<-releaseConfirmation
		})
		return s.loadEntities()
	}

	type adjudication struct {
		result AdjudicateResult
		err    error
	}
	promoteDone := make(chan adjudication, 1)
	go func() {
		result, err := s.AdjudicateExplicitConfirmation(captured[0].ID, confirmation.ID, "work_confirmation_race", t0)
		promoteDone <- adjudication{result: result, err: err}
	}()
	<-lockAttempts
	<-confirmationRechecked

	rejectStarted := make(chan struct{})
	rejectDone := make(chan adjudication, 1)
	go func() {
		close(rejectStarted)
		result, err := s.Adjudicate(captured[0].ID, "reject", "work_confirmation_race", t0)
		rejectDone <- adjudication{result: result, err: err}
	}()
	<-rejectStarted

	select {
	case <-lockAttempts:
		close(releaseConfirmation)
	case rejected := <-rejectDone:
		close(releaseConfirmation)
		promoted := <-promoteDone
		t.Fatalf("reject bypassed mem.lock after confirmation recheck: reject=%+v err=%v promote=%+v err=%v", rejected.result, rejected.err, promoted.result, promoted.err)
	}

	promoted := <-promoteDone
	if promoted.err != nil || promoted.result.Claim == nil {
		t.Fatalf("confirmation = %+v, err=%v", promoted.result, promoted.err)
	}
	rejected := <-rejectDone
	if rejected.err == nil || !strings.Contains(rejected.err.Error(), "already promoted") {
		t.Fatalf("terminal write after promotion = %+v, err=%v", rejected.result, rejected.err)
	}
	latest, err := s.getCandidate(captured[0].ID)
	if err != nil || latest.State != "promoted" {
		t.Fatalf("candidate state = %+v, err=%v", latest, err)
	}
	claims, err := s.loadClaims()
	if err != nil || len(claims) != 1 || claims[0].ID != promoted.result.Claim.ID {
		t.Fatalf("claims = %+v, err=%v", claims, err)
	}
}

type adjudicationOutcome struct {
	result AdjudicateResult
	err    error
}

func pauseAdjudicationAfterCandidateRecheck(t *testing.T, s *Store, run func() (AdjudicateResult, error)) (<-chan struct{}, func(), <-chan adjudicationOutcome) {
	t.Helper()
	lockAttempts := make(chan struct{}, 8)
	s.beforeLockObserver = func() { lockAttempts <- struct{}{} }
	rechecked := make(chan struct{})
	release := make(chan struct{})
	var pauseOnce sync.Once
	s.entityLoader = func() ([]Entity, error) {
		pauseOnce.Do(func() {
			close(rechecked)
			<-release
		})
		return s.loadEntities()
	}
	var releaseOnce sync.Once
	releaseNow := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseNow)

	done := make(chan adjudicationOutcome, 1)
	go func() {
		result, err := run()
		done <- adjudicationOutcome{result: result, err: err}
	}()
	select {
	case <-lockAttempts:
	case outcome := <-done:
		t.Fatalf("adjudication completed before lock attempt: result=%+v err=%v", outcome.result, outcome.err)
	}
	select {
	case <-rechecked:
	case outcome := <-done:
		t.Fatalf("adjudication completed before candidate recheck: result=%+v err=%v", outcome.result, outcome.err)
	}
	return lockAttempts, releaseNow, done
}

func TestConcurrentOrdinaryPromotionCannotRejectPromotedCandidate(t *testing.T) {
	s := newStore(t)
	seed := cand("project:api", "runtime", "go", "")
	seed.PromotionBasis = candidates.BasisTrustedDeterministic
	seed.ClaimType = "status"
	captured, err := s.Capture([]Candidate{seed}, t0)
	if err != nil || len(captured) != 1 {
		t.Fatalf("capture = %+v, err=%v", captured, err)
	}

	lockAttempts, release, firstDone := pauseAdjudicationAfterCandidateRecheck(t, s, func() (AdjudicateResult, error) {
		return s.Adjudicate(captured[0].ID, "promote", "work_ordinary_race", t0)
	})
	secondDone := make(chan adjudicationOutcome, 1)
	go func() {
		result, err := s.Adjudicate(captured[0].ID, "promote", "work_ordinary_race", t0)
		secondDone <- adjudicationOutcome{result: result, err: err}
	}()
	select {
	case <-lockAttempts:
	case outcome := <-secondDone:
		release()
		<-firstDone
		t.Fatalf("second promotion bypassed shared lock: result=%+v err=%v", outcome.result, outcome.err)
	}
	release()

	first := <-firstDone
	if first.err != nil || first.result.Claim == nil {
		t.Fatalf("first promotion = %+v, err=%v", first.result, first.err)
	}
	second := <-secondDone
	if second.err == nil || !strings.Contains(second.err.Error(), "already promoted") {
		t.Fatalf("second promotion = %+v, err=%v", second.result, second.err)
	}
	latest, err := s.getCandidate(captured[0].ID)
	if err != nil || latest.State != "promoted" {
		t.Fatalf("candidate state = %+v, err=%v", latest, err)
	}
}

func TestAnnotationCannotOverwriteConcurrentConfirmation(t *testing.T) {
	s := newStore(t)
	seed := cand("operator", "preference.communication", "prefer concise updates", "")
	seed.PromotionBasis = ""
	seed.ClaimType = "preference"
	captured, err := s.Capture([]Candidate{seed}, t0)
	if err != nil || len(captured) != 1 {
		t.Fatalf("capture = %+v, err=%v", captured, err)
	}
	if _, err := s.Adjudicate(captured[0].ID, "park", "work_annotation_race", t0); err != nil {
		t.Fatal(err)
	}
	confirmation, err := s.RecordOperatorConfirmation(captured[0].ID, "Keep this preference.", "operator:test", t0)
	if err != nil {
		t.Fatal(err)
	}

	lockAttempts, release, promoteDone := pauseAdjudicationAfterCandidateRecheck(t, s, func() (AdjudicateResult, error) {
		return s.AdjudicateExplicitConfirmation(captured[0].ID, confirmation.ID, "work_annotation_race", t0)
	})
	type annotationOutcome struct {
		candidate Candidate
		err       error
	}
	annotationDone := make(chan annotationOutcome, 1)
	go func() {
		candidate, err := s.AnnotateCandidate(captured[0].ID, CandidateAnnotation{Kind: "panel_verdict", Decision: "review"}, t0)
		annotationDone <- annotationOutcome{candidate: candidate, err: err}
	}()
	select {
	case <-lockAttempts:
	case outcome := <-annotationDone:
		release()
		promoted := <-promoteDone
		t.Fatalf("annotation bypassed mem.lock after confirmation recheck: annotation=%+v err=%v promote=%+v err=%v", outcome.candidate, outcome.err, promoted.result, promoted.err)
	}
	release()

	promoted := <-promoteDone
	if promoted.err != nil || promoted.result.Claim == nil {
		t.Fatalf("confirmation = %+v, err=%v", promoted.result, promoted.err)
	}
	annotated := <-annotationDone
	if annotated.err == nil || !strings.Contains(annotated.err.Error(), "already promoted") {
		t.Fatalf("annotation after promotion = %+v, err=%v", annotated.candidate, annotated.err)
	}
	latest, err := s.getCandidate(captured[0].ID)
	if err != nil || latest.State != "promoted" {
		t.Fatalf("candidate state = %+v, err=%v", latest, err)
	}
}

func TestDecayCannotOverwriteConcurrentConfirmation(t *testing.T) {
	s := newStore(t)
	seed := cand("project:api", "status", "prefer concise updates", "")
	seed.PromotionBasis = ""
	seed.ClaimType = "status"
	seed.ExpiresAt = t0.Add(-time.Hour).Format(time.RFC3339)
	captured, err := s.Capture([]Candidate{seed}, t0)
	if err != nil || len(captured) != 1 {
		t.Fatalf("capture = %+v, err=%v", captured, err)
	}
	if _, err := s.Adjudicate(captured[0].ID, "park", "work_decay_race", t0); err != nil {
		t.Fatal(err)
	}
	confirmation, err := s.RecordOperatorConfirmation(captured[0].ID, "Keep this preference.", "operator:test", t0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.AllCandidates()
	if err != nil || len(snapshot) != 1 {
		t.Fatalf("candidate snapshot = %+v, err=%v", snapshot, err)
	}

	lockAttempts, release, promoteDone := pauseAdjudicationAfterCandidateRecheck(t, s, func() (AdjudicateResult, error) {
		return s.AdjudicateExplicitConfirmation(captured[0].ID, confirmation.ID, "work_decay_race", t0)
	})
	type decayOutcome struct {
		actions []SynthesisDecayAction
		err     error
	}
	decayDone := make(chan decayOutcome, 1)
	go func() {
		actions, err := s.decayExpiredCandidates(snapshot, t0)
		decayDone <- decayOutcome{actions: actions, err: err}
	}()
	select {
	case <-lockAttempts:
	case outcome := <-decayDone:
		release()
		promoted := <-promoteDone
		t.Fatalf("decay bypassed mem.lock after confirmation recheck: decay=%+v err=%v promote=%+v err=%v", outcome.actions, outcome.err, promoted.result, promoted.err)
	}
	release()

	promoted := <-promoteDone
	if promoted.err != nil || promoted.result.Claim == nil {
		t.Fatalf("confirmation = %+v, err=%v", promoted.result, promoted.err)
	}
	decayed := <-decayDone
	if decayed.err != nil || len(decayed.actions) != 0 {
		t.Fatalf("decay after promotion = %+v, err=%v", decayed.actions, decayed.err)
	}
	latest, err := s.getCandidate(captured[0].ID)
	if err != nil || latest.State != "promoted" {
		t.Fatalf("candidate state = %+v, err=%v", latest, err)
	}
}

func TestPromotionDoesNotMarkCandidateBeforeTransitionWrite(t *testing.T) {
	s := newStore(t)
	seed := cand("project:api", "runtime", "go", "")
	seed.PromotionBasis = candidates.BasisTrustedDeterministic
	seed.ClaimType = "status"
	captured, err := s.Capture([]Candidate{seed}, t0)
	if err != nil || len(captured) != 1 {
		t.Fatalf("capture = %+v, err=%v", captured, err)
	}
	eventsPath := filepath.Join(s.home, "events")
	if err := os.RemoveAll(eventsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(eventsPath, []byte("blocks transition directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Adjudicate(captured[0].ID, "promote", "work_transition_failure", t0); err == nil {
		t.Fatal("promotion succeeded despite blocked transition write")
	}
	latest, err := s.getCandidate(captured[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.State == "promoted" {
		t.Fatalf("candidate marked promoted before transition existed: %+v", latest)
	}
	leakedClaims, err := s.loadClaims()
	if err != nil || len(leakedClaims) != 1 {
		t.Fatalf("partial promotion claims = %+v, err=%v", leakedClaims, err)
	}
	if err := os.Remove(eventsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(eventsPath, 0o755); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.Adjudicate(captured[0].ID, "promote", "work_transition_failure", t0)
	if err != nil || recovered.Claim == nil || recovered.Transition == nil {
		t.Fatalf("partial promotion did not recover: %+v, err=%v", recovered, err)
	}
	if recovered.Claim.ID != leakedClaims[0].ID {
		t.Fatalf("recovery minted another claim: recovered=%s leaked=%s", recovered.Claim.ID, leakedClaims[0].ID)
	}
	claimsAfterRecovery, err := s.loadClaims()
	if err != nil || len(claimsAfterRecovery) != 1 {
		t.Fatalf("recovery claims = %+v, err=%v", claimsAfterRecovery, err)
	}
	latest, err = s.getCandidate(captured[0].ID)
	if err != nil || latest.State != "promoted" {
		t.Fatalf("recovered candidate = %+v, err=%v", latest, err)
	}
}

func TestPartialPromotionRecoveryUsesHistoricalClaimAfterInterveningPromotion(t *testing.T) {
	s := newStore(t)
	first := cand("project:api", "runtime", "go", "")
	first.PromotionBasis = candidates.BasisTrustedDeterministic
	first.ClaimType = "status"
	firstCaptured, err := s.Capture([]Candidate{first}, t0)
	if err != nil || len(firstCaptured) != 1 {
		t.Fatalf("capture first = %+v, err=%v", firstCaptured, err)
	}
	second := cand("project:api", "runtime", "rust", "")
	second.PromotionBasis = candidates.BasisTrustedDeterministic
	second.ClaimType = "status"
	secondCaptured, err := s.Capture([]Candidate{second}, t0.Add(time.Second))
	if err != nil || len(secondCaptured) != 1 {
		t.Fatalf("capture second = %+v, err=%v", secondCaptured, err)
	}

	eventsPath := filepath.Join(s.home, "events")
	if err := os.RemoveAll(eventsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(eventsPath, []byte("blocks first transition directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Adjudicate(firstCaptured[0].ID, "promote", "work_first_partial", t0); err == nil {
		t.Fatal("first promotion succeeded despite blocked transition write")
	}
	partialClaims, err := s.loadClaims()
	if err != nil || len(partialClaims) != 1 {
		t.Fatalf("partial promotion claims = %+v, err=%v", partialClaims, err)
	}
	firstClaimID := partialClaims[0].ID

	if err := os.Remove(eventsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(eventsPath, 0o755); err != nil {
		t.Fatal(err)
	}
	intervening, err := s.Adjudicate(secondCaptured[0].ID, "promote", "work_second", t0.Add(time.Second))
	if err != nil || intervening.Claim == nil {
		t.Fatalf("intervening promotion = %+v, err=%v", intervening, err)
	}
	if intervening.Claim.Supersedes != firstClaimID {
		t.Fatalf("intervening claim supersedes = %q, want %q", intervening.Claim.Supersedes, firstClaimID)
	}

	recovered, err := s.Adjudicate(firstCaptured[0].ID, "promote", "work_first_partial", t0.Add(2*time.Second))
	if err != nil || recovered.Claim == nil || recovered.Transition == nil {
		t.Fatalf("historical partial promotion did not recover: %+v, err=%v", recovered, err)
	}
	if recovered.Claim.ID != firstClaimID {
		t.Fatalf("recovery minted another claim: recovered=%s original=%s", recovered.Claim.ID, firstClaimID)
	}
	claimsAfterRecovery, err := s.loadClaims()
	if err != nil || len(claimsAfterRecovery) != 2 {
		t.Fatalf("recovery claims = %+v, err=%v", claimsAfterRecovery, err)
	}
	latestFirst, err := s.getCandidate(firstCaptured[0].ID)
	if err != nil || latestFirst.State != "promoted" {
		t.Fatalf("recovered first candidate = %+v, err=%v", latestFirst, err)
	}
	current, ok, err := s.currentClusterClaim(secondCaptured[0], 0)
	if err != nil || !ok || current.ID != intervening.Claim.ID {
		t.Fatalf("current claim after recovery = %+v ok=%t err=%v, want %s", current, ok, err, intervening.Claim.ID)
	}
	if exists, err := s.eventStore.Exists(recovered.Transition.ID); err != nil || !exists {
		t.Fatalf("recovered transition missing: exists=%t err=%v", exists, err)
	}
}

func TestPartialPromotionRecoveryReusesPersistedTransition(t *testing.T) {
	s := newStore(t)
	seed := cand("project:api", "runtime", "go", "")
	seed.PromotionBasis = candidates.BasisTrustedDeterministic
	seed.ClaimType = "status"
	captured, err := s.Capture([]Candidate{seed}, t0)
	if err != nil || len(captured) != 1 {
		t.Fatalf("capture = %+v, err=%v", captured, err)
	}

	writeAttempts := 0
	s.adjudicationCandidateWriter = func(id string, payload any) error {
		writeAttempts++
		if writeAttempts == 1 {
			return os.ErrPermission
		}
		return candidates.WriteCandidate(s.home, candidates.Memory, id, payload)
	}
	if _, err := s.Adjudicate(captured[0].ID, "promote", "work_projection_failure", t0); err == nil {
		t.Fatal("promotion succeeded despite injected candidate projection failure")
	}
	claimsAfterFailure, err := s.loadClaims()
	if err != nil || len(claimsAfterFailure) != 1 {
		t.Fatalf("partial promotion claims = %+v, err=%v", claimsAfterFailure, err)
	}
	eventsAfterFailure, err := s.eventStore.Scan(eventstore.ScanOptions{})
	if err != nil || len(eventsAfterFailure) != 1 {
		t.Fatalf("partial promotion events = %+v, err=%v", eventsAfterFailure, err)
	}
	firstEventID := eventsAfterFailure[0].ID
	firstEvent, err := eventsAfterFailure[0].Read()
	if err != nil {
		t.Fatal(err)
	}
	firstEventBytes := firstEvent.Bytes

	recovered, err := s.Adjudicate(captured[0].ID, "promote", "work_projection_failure", t0.Add(time.Second))
	if err != nil || recovered.Claim == nil || recovered.Transition == nil {
		t.Fatalf("projection failure did not recover: %+v, err=%v", recovered, err)
	}
	if recovered.Claim.ID != claimsAfterFailure[0].ID {
		t.Fatalf("recovery minted another claim: recovered=%s original=%s", recovered.Claim.ID, claimsAfterFailure[0].ID)
	}
	eventsAfterRecovery, err := s.eventStore.Scan(eventstore.ScanOptions{})
	if err != nil || len(eventsAfterRecovery) != 1 {
		t.Fatalf("recovery duplicated transition: %+v, err=%v", eventsAfterRecovery, err)
	}
	if eventsAfterRecovery[0].ID != firstEventID {
		t.Fatalf("recovery replaced transition identity: got=%s want=%s", eventsAfterRecovery[0].ID, firstEventID)
	}
	eventAfterRecovery, err := eventsAfterRecovery[0].Read()
	if err != nil {
		t.Fatal(err)
	}
	eventBytesAfterRecovery := eventAfterRecovery.Bytes
	if string(eventBytesAfterRecovery) != string(firstEventBytes) {
		t.Fatal("recovery rewrote the already-persisted transition")
	}
	wantBytes, err := json.Marshal(recovered.Transition)
	if err != nil {
		t.Fatal(err)
	}
	wantBytes = append(wantBytes, '\n')
	if !bytes.Equal(eventBytesAfterRecovery, wantBytes) {
		t.Fatalf("promotion transition bytes changed\ngot:  %q\nwant: %q", eventBytesAfterRecovery, wantBytes)
	}
}

func TestPartialPromotionRecoveryRejectsCorruptStableTransition(t *testing.T) {
	s := newStore(t)
	seed := cand("project:api", "runtime", "go", "")
	seed.PromotionBasis = candidates.BasisTrustedDeterministic
	seed.ClaimType = "status"
	captured, err := s.Capture([]Candidate{seed}, t0)
	if err != nil || len(captured) != 1 {
		t.Fatalf("capture = %+v, err=%v", captured, err)
	}

	eventsPath := filepath.Join(s.home, "events")
	if err := os.RemoveAll(eventsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(eventsPath, []byte("blocks first transition directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Adjudicate(captured[0].ID, "promote", "work_corrupt_transition", t0); err == nil {
		t.Fatal("promotion succeeded despite blocked transition write")
	}
	partialClaims, err := s.loadClaims()
	if err != nil || len(partialClaims) != 1 {
		t.Fatalf("partial promotion claims = %+v, err=%v", partialClaims, err)
	}
	if err := os.Remove(eventsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(eventsPath, 0o755); err != nil {
		t.Fatal(err)
	}
	stablePath := filepath.Join(eventsPath, idFor("tr_", partialClaims[0].ID)+".json")
	if err := os.WriteFile(stablePath, []byte("{\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Adjudicate(captured[0].ID, "promote", "work_corrupt_transition", t0.Add(time.Second)); err == nil {
		t.Fatal("recovery ignored corrupt stable transition")
	}
	events, err := os.ReadDir(eventsPath)
	if err != nil || len(events) != 1 || events[0].Name() != filepath.Base(stablePath) {
		t.Fatalf("corrupt recovery minted another transition: %+v, err=%v", events, err)
	}
	latest, err := s.getCandidate(captured[0].ID)
	if err != nil || latest.State == "promoted" {
		t.Fatalf("corrupt recovery promoted candidate: %+v, err=%v", latest, err)
	}
}

func TestPartialPromotionRecoveryUsesClaimDerivedTransitionIdentity(t *testing.T) {
	s := newStore(t)
	seed := cand("project:api", "runtime", "go", "")
	seed.PromotionBasis = candidates.BasisTrustedDeterministic
	seed.ClaimType = "status"
	captured, err := s.Capture([]Candidate{seed}, t0)
	if err != nil || len(captured) != 1 {
		t.Fatalf("capture = %+v, err=%v", captured, err)
	}

	eventsPath := filepath.Join(s.home, "events")
	if err := os.RemoveAll(eventsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(eventsPath, []byte("blocks first transition directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Adjudicate(captured[0].ID, "promote", "work_transition_identity", t0); err == nil {
		t.Fatal("promotion succeeded despite blocked transition write")
	}
	partialClaims, err := s.loadClaims()
	if err != nil || len(partialClaims) != 1 {
		t.Fatalf("partial promotion claims = %+v, err=%v", partialClaims, err)
	}
	if err := os.Remove(eventsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(eventsPath, 0o755); err != nil {
		t.Fatal(err)
	}
	unrelated := s.memoryTransition(partialClaims[0], Claim{}, false, "work_unrelated", t0.Add(time.Second))
	unrelated.ID = "tr_unrelated"
	if _, err := s.eventStore.PutJSON(unrelated.ID, &unrelated, "  "); err != nil {
		t.Fatal(err)
	}

	recovered, err := s.Adjudicate(captured[0].ID, "promote", "work_transition_identity", t0.Add(2*time.Second))
	if err != nil || recovered.Transition == nil {
		t.Fatalf("identity recovery = %+v, err=%v", recovered, err)
	}
	wantID := idFor("tr_", partialClaims[0].ID)
	if recovered.Transition.ID != wantID {
		t.Fatalf("recovery reused unrelated transition %s, want claim-derived %s", recovered.Transition.ID, wantID)
	}
	events, err := s.eventStore.Scan(eventstore.ScanOptions{})
	if err != nil || len(events) != 2 {
		t.Fatalf("identity recovery events = %+v, err=%v", events, err)
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
	nonOperator := EvidenceItem{
		SourceKind: OperatorConfirmationSourceKind,
		SourceID:   candidateID,
		QuoteText:  "Agent attempted confirmation.",
		Actor:      "agent:test",
	}
	// Well-formed but agent-attributed: mint a content-valid address and write
	// raw, because RecordOperatorConfirmation would refuse the actor at the
	// door. The fold accepts the row; the confirmation door must still reject
	// it on provenance.
	nonOperator.ContentHash = evidenceContentHash(nonOperator)
	nonOperator.ID = "ev_" + nonOperator.ContentHash[:12]
	if err := appendJSONL(s.evidenceLog(), &nonOperator); err != nil {
		t.Fatal(err)
	}
	wrongKind, err := s.RecordEvidence(EvidenceItem{
		SourceKind: "session_note",
		SourceID:   candidateID,
		QuoteText:  "Operator-like text in the wrong evidence kind.",
		Actor:      "operator:test",
	}, t0)
	if err != nil {
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

	// A tampered row — same ev_ id, rewritten content — now fails the fold
	// itself: Evidence() revalidates content addresses, so a forgery can no
	// longer silently last-win. Proven in its own store so the poisoned log
	// does not wedge this test's happy path below.
	t.Run("tampered content address", func(t *testing.T) {
		s2 := newStore(t)
		captured2, err := s2.Capture([]Candidate{seed}, t0)
		if err != nil || len(captured2) != 1 {
			t.Fatalf("capture = %+v, err=%v", captured2, err)
		}
		if _, err := s2.Adjudicate(captured2[0].ID, "park", "work_user_shadow", t0); err != nil {
			t.Fatal(err)
		}
		tampered, err := s2.RecordOperatorConfirmation(captured2[0].ID, "Tamper-check confirmation.", "operator:test", t0)
		if err != nil {
			t.Fatal(err)
		}
		tampered.Actor = "operator:tampered-after-write"
		if err := appendJSONL(s2.evidenceLog(), &tampered); err != nil {
			t.Fatal(err)
		}
		if _, err := s2.Evidence(); err == nil {
			t.Fatal("evidence fold must fail loud on a tampered content address")
		}
		if _, err := s2.AdjudicateExplicitConfirmation(captured2[0].ID, tampered.ID, "work_user_shadow", t0); err == nil {
			t.Fatal("citing a tampered confirmation row must fail")
		}
		if claims, err := s2.loadClaims(); err != nil || len(claims) != 0 {
			t.Fatalf("tampered confirmation wrote claims=%d err=%v", len(claims), err)
		}
	})

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
	_, _ = promoteForTest(t, s, c1[0].ID, "", t0)
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
	_, _ = promoteForTest(t, s, got[0].ID, "", t0)
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
	if _, err := promoteForTest(t, s, c[0].ID, "", t0); err != nil {
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
	if _, err := promoteForTest(t, s, c[0].ID, "", t0); err != nil {
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
	if _, err := promoteForTest(t, s, c[0].ID, "", t0); err != nil {
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
	if _, err := promoteForTest(t, s, first[0].ID, "", t0); err != nil {
		t.Fatal(err)
	}
	second, _ := s.Capture([]Candidate{cand("Compound", "status", "title", "")}, t0)
	if _, err := promoteForTest(t, s, second[0].ID, "", t0); err != nil {
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
	_, _ = promoteForTest(t, s, c[0].ID, "", t0)
	r := s.DoctorAt(nil, t0.Add(time.Minute))
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
	if _, err := promoteForTest(t, s, c[0].ID, "", t0); err != nil {
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

	r := s.DoctorAt(&LexicalEngine{}, t0.Add(time.Minute))
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
		_, _ = promoteForTest(t, s, c[0].ID, "", t0)
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
	_, _ = promoteForTest(t, s, got[0].ID, "", t0)
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
		r, err := promoteForTest(t, s, c[0].ID, "", t0)
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
	if _, err := promoteForTest(t, s, c[0].ID, "", t0); err == nil {
		t.Error("a corrupt seq counter must fail loud, not silently reset")
	}
}

// Even if the supersedes EDGE append is lost/torn, the new claim's own Supersedes pointer must keep
// recall to a single current claim (no two simultaneously-current versions).
func TestFoldHonorsSupersedesWithoutEdge(t *testing.T) {
	s := newStore(t)
	c1, _ := s.Capture([]Candidate{cand("e", "p", "v1", "")}, t0)
	_, _ = promoteForTest(t, s, c1[0].ID, "", t0)
	c2, _ := s.Capture([]Candidate{cand("e", "p", "v2", "")}, t0)
	_, _ = promoteForTest(t, s, c2[0].ID, "", t0)
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

// TestStoreCurrentClaimReadsBackPromotedValueAndFollowsReimport proves the
// exported CurrentClaim door: internal/authority (and any other package)
// reads the current promoted current_path claim without reaching into mem's
// private currentClaim, and a later re-import (new object, same
// entity+predicate) is picked up as the newest current value.
func TestStoreCurrentClaimReadsBackPromotedValueAndFollowsReimport(t *testing.T) {
	s := newStore(t)

	if _, ok, err := s.CurrentClaim("source:absorbed-os", "current_path"); err != nil {
		t.Fatalf("CurrentClaim on empty store: %v", err)
	} else if ok {
		t.Fatalf("CurrentClaim on empty store should report not-found")
	}

	c1, err := s.Capture([]Candidate{cand("source:absorbed-os", "current_path", "/root/first", "")}, t0)
	if err != nil {
		t.Fatalf("capture first: %v", err)
	}
	if _, err := promoteForTest(t, s, c1[0].ID, "", t0); err != nil {
		t.Fatalf("promote first: %v", err)
	}

	claim, ok, err := s.CurrentClaim("source:absorbed-os", "current_path")
	if err != nil {
		t.Fatalf("CurrentClaim after first promote: %v", err)
	}
	if !ok || claim.Object != "/root/first" {
		t.Fatalf("CurrentClaim = %+v, ok=%v; want /root/first", claim, ok)
	}

	c2, err := s.Capture([]Candidate{cand("source:absorbed-os", "current_path", "/root/second", "")}, t0.Add(time.Minute))
	if err != nil {
		t.Fatalf("capture second (re-import): %v", err)
	}
	if _, err := promoteForTest(t, s, c2[0].ID, "", t0.Add(time.Minute)); err != nil {
		t.Fatalf("promote second: %v", err)
	}

	claim, ok, err = s.CurrentClaim("source:absorbed-os", "current_path")
	if err != nil {
		t.Fatalf("CurrentClaim after re-import: %v", err)
	}
	if !ok || claim.Object != "/root/second" {
		t.Fatalf("CurrentClaim after re-import = %+v, ok=%v; want newest /root/second", claim, ok)
	}
}

// TestAdjudicateAsOperatorStampsProvenance is the falsifier for the
// keep-aside-drain lane (work_4033b98c5837): a candidate the operator
// explicitly kept aside via the CLI ratification door must carry visible
// adjudicated_by/adjudicated_at provenance in the candidates-list
// projection, while the plain (unstamped) Adjudicate door used by automated
// callers (panel apply, decisions import) leaves a candidate looking exactly
// like one still sitting untouched since ingestion.
func TestAdjudicateAsOperatorStampsProvenance(t *testing.T) {
	s := newStore(t)

	// An ordinary automated park (e.g. panel apply, decisions import) must
	// NOT gain operator provenance — only the CLI's ratification door does.
	autoSeed := cand("decision:auto", "decided", "auto-parked candidate", "")
	autoCaptured, err := s.Capture([]Candidate{autoSeed}, t0)
	if err != nil || len(autoCaptured) != 1 {
		t.Fatalf("capture auto = %+v, err=%v", autoCaptured, err)
	}
	if _, err := s.Adjudicate(autoCaptured[0].ID, "park", "panel-apply", t0); err != nil {
		t.Fatalf("automated park: %v", err)
	}
	autoGot, err := s.getCandidate(autoCaptured[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if autoGot.AdjudicatedBy != "" || autoGot.AdjudicatedAt != "" {
		t.Fatalf("automated Adjudicate stamped provenance it must not: by=%q at=%q", autoGot.AdjudicatedBy, autoGot.AdjudicatedAt)
	}

	// The operator ratification door — AdjudicateAsOperator, what `compound
	// memory adjudicate` calls — stamps provenance visible on the candidate.
	opSeed := cand("decision:op", "decided", "operator-parked candidate", "")
	opCaptured, err := s.Capture([]Candidate{opSeed}, t0)
	if err != nil || len(opCaptured) != 1 {
		t.Fatalf("capture operator = %+v, err=%v", opCaptured, err)
	}
	if _, err := s.AdjudicateAsOperator(opCaptured[0].ID, "park", "", "", t0); err != nil {
		t.Fatalf("operator park: %v", err)
	}
	opGot, err := s.getCandidate(opCaptured[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if opGot.State != "parked" {
		t.Fatalf("operator-parked candidate state = %q, want parked", opGot.State)
	}
	if opGot.AdjudicatedBy != "operator" {
		t.Fatalf("adjudicated_by = %q, want default %q", opGot.AdjudicatedBy, "operator")
	}
	if opGot.AdjudicatedAt != rfc3339(t0) {
		t.Fatalf("adjudicated_at = %q, want %q", opGot.AdjudicatedAt, rfc3339(t0))
	}

	// An explicit actor overrides the "operator" default.
	namedSeed := cand("decision:named", "decided", "named-operator-parked candidate", "")
	namedCaptured, err := s.Capture([]Candidate{namedSeed}, t0)
	if err != nil || len(namedCaptured) != 1 {
		t.Fatalf("capture named = %+v, err=%v", namedCaptured, err)
	}
	if _, err := s.AdjudicateAsOperator(namedCaptured[0].ID, "park", "", "operator:example", t0); err != nil {
		t.Fatalf("named operator park: %v", err)
	}
	namedGot, err := s.getCandidate(namedCaptured[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if namedGot.AdjudicatedBy != "operator:example" {
		t.Fatalf("adjudicated_by = %q, want %q", namedGot.AdjudicatedBy, "operator:example")
	}

	// candidates-list projection (what the console standing-law queue reads)
	// must surface the stamp too, not just the raw candidate file.
	report, err := s.ListCandidates(t0, CandidateListOptions{State: "parked"})
	if err != nil {
		t.Fatalf("ListCandidates: %v", err)
	}
	byID := map[string]CandidateListItem{}
	for _, item := range report.Items {
		byID[item.ID] = item
	}
	if got := byID[opCaptured[0].ID]; got.AdjudicatedBy != "operator" {
		t.Fatalf("candidates-list projection missing adjudicated_by for operator-parked candidate: %+v", got)
	}
	if got := byID[autoCaptured[0].ID]; got.AdjudicatedBy != "" {
		t.Fatalf("candidates-list projection stamped adjudicated_by on an automated park: %+v", got)
	}
}

// TestAdjudicateAsOperatorRefusesNonOperatorActor is the falsifier for the
// operator-door provenance check: the ratification door must refuse a rented
// executor identity (e.g. --by codex) instead of stamping operator
// provenance, and the refusal must leave the candidate untouched.
func TestAdjudicateAsOperatorRefusesNonOperatorActor(t *testing.T) {
	s := newStore(t)
	captured, err := s.Capture([]Candidate{cand("decision:refused", "decided", "executor-forged ratification", "")}, t0)
	if err != nil || len(captured) != 1 {
		t.Fatalf("capture = %+v, err=%v", captured, err)
	}
	if _, err := s.AdjudicateAsOperator(captured[0].ID, "park", "", "codex", t0); err == nil {
		t.Fatal("operator ratification door must refuse a non-operator actor")
	}
	got, err := s.getCandidate(captured[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "proposed" {
		t.Fatalf("refused adjudication changed state to %q, want proposed", got.State)
	}
	if got.AdjudicatedBy != "" || got.AdjudicatedAt != "" {
		t.Fatalf("refused adjudication stamped provenance: by=%q at=%q", got.AdjudicatedBy, got.AdjudicatedAt)
	}
}

// TestValidateAdjudicationIntentEnforcesEachRuleInIsolation is the falsifier
// for the adjudicate() refactor that extracts the pre-lock rule set into a
// pure validateAdjudicationIntent(c, intent) function. The function must be
// pure: no I/O, no Store, no time.Now, no candidate mutation. Each rule
// listed below maps to a corresponding check inside the original god
// function (internal/mem/mem.go adjudicate, lines 543-572). A test failure
// here means a rule was dropped, softened, or silently started mutating
// state.
func TestValidateAdjudicationIntentEnforcesEachRuleInIsolation(t *testing.T) {
	// The candidate fixture is reused across all subtests. The
	// validateAdjudicationIntent function must not mutate it; each
	// subtest takes a fresh copy so a mutation regression in one rule
	// cannot leak into another.
	cleanCandidate := func() Candidate {
		c := cand("e", "p", "v", "")
		c.PromotionBasis = candidates.BasisRuntimeProof // not BasisExplicitConfirmation
		c.State = "proposed"
		return c
	}

	cases := []struct {
		name    string
		mutate  func(*Candidate)          // adjust candidate before validation
		intent  func() AdjudicationIntent // build the intent under test
		wantErr string                    // substring expected in the error
	}{
		{
			name:    "rejects unknown verdict",
			mutate:  func(c *Candidate) {},
			intent:  func() AdjudicationIntent { return AdjudicationIntent{Verdict: "admit"} },
			wantErr: "verdict must be one of",
		},
		{
			name:    "rejects already-promoted candidate",
			mutate:  func(c *Candidate) { c.State = "promoted" },
			intent:  func() AdjudicationIntent { return AdjudicationIntent{Verdict: "promote"} },
			wantErr: "already promoted",
		},
		{
			name: "rejects reserved operational decision authority qualifier on ordinary promote",
			mutate: func(c *Candidate) {
				c.Qualifiers = map[string]string{OperationalDecisionAuthorityQualifier: OperationalDecisionAuthorityValue}
			},
			intent:  func() AdjudicationIntent { return AdjudicationIntent{Verdict: "promote"} },
			wantErr: "reserved operational decision authority qualifier",
		},
		{
			name:    "rejects reserved operational decision kind on ordinary promote",
			mutate:  func(c *Candidate) { c.Qualifiers = map[string]string{"kind": "grant"} },
			intent:  func() AdjudicationIntent { return AdjudicationIntent{Verdict: "promote"} },
			wantErr: "reserved operational decision kind",
		},
		{
			name:    "rejects operational decision promote against non-decision claim_type",
			mutate:  func(c *Candidate) { c.ClaimType = "preference" },
			intent:  func() AdjudicationIntent { return AdjudicationIntent{Verdict: "promote", OperationalDecision: true} },
			wantErr: "claim_type must be decision",
		},
		{
			name:    "rejects explicit confirmation paired with non-promote verdict",
			mutate:  func(c *Candidate) {},
			intent:  func() AdjudicationIntent { return AdjudicationIntent{Verdict: "park", ExplicitConfirmationRef: "ev_x"} },
			wantErr: "explicit confirmation is only valid for ordinary memory promotion",
		},
		{
			name:   "rejects explicit confirmation paired with operational decision",
			mutate: func(c *Candidate) { c.ClaimType = "decision" },
			intent: func() AdjudicationIntent {
				return AdjudicationIntent{Verdict: "promote", OperationalDecision: true, ExplicitConfirmationRef: "ev_x"}
			},
			wantErr: "explicit confirmation is only valid for ordinary memory promotion",
		},
		{
			name:    "rejects promotion with explicit-confirmation basis but no confirmation ref",
			mutate:  func(c *Candidate) { c.PromotionBasis = candidates.BasisExplicitConfirmation },
			intent:  func() AdjudicationIntent { return AdjudicationIntent{Verdict: "promote"} },
			wantErr: "explicit confirmation requires governed confirmation evidence",
		},
		{
			name:   "accepts clean ordinary promote",
			mutate: func(c *Candidate) {},
			intent: func() AdjudicationIntent {
				return AdjudicationIntent{Verdict: "promote"}
			},
			wantErr: "",
		},
		{
			name:   "accepts clean non-promote (park)",
			mutate: func(c *Candidate) {},
			intent: func() AdjudicationIntent {
				return AdjudicationIntent{Verdict: "park"}
			},
			wantErr: "",
		},
		{
			name:   "accepts operational-decision promote on a decision-claim_type candidate",
			mutate: func(c *Candidate) { c.ClaimType = "decision" },
			intent: func() AdjudicationIntent {
				return AdjudicationIntent{Verdict: "promote", OperationalDecision: true}
			},
			wantErr: "",
		},
		{
			name:   "accepts explicit-confirmation promote on a parked candidate",
			mutate: func(c *Candidate) { c.PromotionBasis = candidates.BasisExplicitConfirmation; c.State = "parked" },
			intent: func() AdjudicationIntent {
				return AdjudicationIntent{Verdict: "promote", ExplicitConfirmationRef: "ev_x"}
			},
			wantErr: "",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			c := cleanCandidate()
			tc.mutate(&c)
			// Snapshot the candidate before validation; if the function
			// mutates it the snapshot will diverge and the test fails.
			beforeJSON, _ := json.Marshal(c)
			err := validateAdjudicationIntent(c, tc.intent())
			afterJSON, _ := json.Marshal(c)
			if string(beforeJSON) != string(afterJSON) {
				t.Fatalf("validateAdjudicationIntent mutated its candidate: before=%s after=%s", beforeJSON, afterJSON)
			}
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestDoctorFreshnessPromotionStalledSignalsFail: when the newest claim is
// older than the staleness threshold AND the inbox still has candidates,
// promotion reports fail and stamps the freshness fields. This is the
// stall signature that the pre-freshness doctor masked by reporting ok.
func TestDoctorFreshnessPromotionStalledSignalsFail(t *testing.T) {
	s := newStore(t)
	c, _ := s.Capture([]Candidate{cand("e", "p", "v", "")}, t0)
	_, _ = promoteForTest(t, s, c[0].ID, "", t0)
	// Advance past memDoctorThresholds.ClaimStaleAfter (72h) + slack so the fixture
	// ages into the staleness band while the inbox remains non-empty.
	now := t0.Add(memDoctorThresholds.ClaimStaleAfter + time.Hour)
	r := s.DoctorAt(nil, now)
	promotion := hopByName(t, r, "promotion")
	if promotion.Status != "fail" {
		t.Fatalf("stalled promotion status = %q, want fail (detail=%q)", promotion.Status, promotion.Detail)
	}
	if promotion.NewestClaimAt == "" {
		t.Fatalf("stalled promotion must carry newest_claim_at, got %+v", promotion)
	}
	capture := hopByName(t, r, "capture")
	if capture.NewestCandidateAt == "" {
		t.Fatalf("capture hop must carry newest_candidate_at, got %+v", capture)
	}
	if capture.InboxBacklog == 0 {
		t.Fatalf("capture hop must carry inbox_backlog > 0 when inbox non-empty, got %+v", capture)
	}
}

// TestDoctorFreshnessQuietInboxNotStalled: a stale claim with an empty
// inbox is a quiet system, NOT a stall — the doctor must report ok.
func TestDoctorFreshnessQuietInboxNotStalled(t *testing.T) {
	s := newStore(t)
	c, _ := s.Capture([]Candidate{cand("e", "p", "v", "")}, t0)
	_, _ = promoteForTest(t, s, c[0].ID, "", t0)
	// Remove the candidate so the inbox is empty.
	if err := os.Remove(filepath.Join(s.candDir(), c[0].ID+".json")); err != nil {
		t.Fatal(err)
	}
	now := t0.Add(memDoctorThresholds.ClaimStaleAfter + time.Hour)
	r := s.DoctorAt(nil, now)
	promotion := hopByName(t, r, "promotion")
	if promotion.Status != "ok" {
		t.Fatalf("quiet inbox promotion status = %q, want ok (quiet != stalled)", promotion.Status)
	}
}

// TestDoctorFreshnessInboxBacklogWarnAndFail: the inbox-backlog thresholds
// flip the capture hop's status independently of the promotion gate.
func TestDoctorFreshnessInboxBacklogWarnAndFail(t *testing.T) {
	// Shrink production thresholds so the test exercises the boundary in
	// reasonable time. Defaults are 5_000 (warn) and 50_000 (fail); the
	// test uses 10 and 20 respectively.
	orig := memDoctorThresholds
	t.Cleanup(func() { memDoctorThresholds = orig })
	memDoctorThresholds.InboxBacklogWarn = 10
	memDoctorThresholds.InboxBacklogFail = 20

	// No claims — promotion has no stall signature. Capture status is
	// driven purely by inbox size.
	// Use small thresholds for the test: production values
	// (memDoctorInboxBacklogWarn=5_000, memDoctorInboxBacklogFail=50_000)
	// are too slow to instantiate in a unit test.
	cases := []struct {
		name    string
		backlog int
		want    string
	}{
		{"empty", 0, "empty"},
		{"under_warn", memDoctorThresholds.InboxBacklogWarn - 1, "ok"},
		{"at_warn", memDoctorThresholds.InboxBacklogWarn, "warn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			// Pre-write empty directory — read fresh count.
			before, _ := os.ReadDir(s.candDir())
			for _, e := range before {
				_ = os.Remove(filepath.Join(s.candDir(), e.Name()))
			}
			for i := 0; i < tc.backlog; i++ {
				// Unique per candidate so Capture does not dedupe-as-echo.
				c := cand("e", "p", fmt.Sprintf("v-%d", i), "")
				c.ID = fmt.Sprintf("cand-%05d", i)
				if _, err := s.Capture([]Candidate{c}, t0); err != nil {
					t.Fatal(err)
				}
			}
			r := s.DoctorAt(nil, t0.Add(time.Minute))
			h := hopByName(t, r, "capture")
			if h.Status != tc.want {
				t.Fatalf("backlog=%d capture status = %q, want %q (detail=%q)",
					tc.backlog, h.Status, tc.want, h.Detail)
			}
		})
	}
}

// TestAdjudicatePendingDispositions: the inbox walker applies each
// candidate's own admission-class disposition: source_metadata +
// annotated_evidence candidates auto-reject (no_promote); bare proposed
// candidates park. Re-running the verb is idempotent because parked /
// promoted candidates carry durable state and the walker skips them.
func TestAdjudicatePendingDispositions(t *testing.T) {
	s := newStore(t)
	// 3 source_metadata candidates -> no_promote
	for i := 0; i < 3; i++ {
		c := cand("e", "p", fmt.Sprintf("v-%d", i), "")
		c.Qualifiers = map[string]string{"source": "knowledge_import"}
		c.ID = fmt.Sprintf("cand-import-%d", i)
		if _, err := s.Capture([]Candidate{c}, t0); err != nil {
			t.Fatal(err)
		}
	}
	// 2 bare proposed candidates -> park
	for i := 0; i < 2; i++ {
		c := cand("e", "p", fmt.Sprintf("bare-%d", i), "")
		c.ID = fmt.Sprintf("cand-bare-%d", i)
		if _, err := s.Capture([]Candidate{c}, t0); err != nil {
			t.Fatal(err)
		}
	}
	r := s.AdjudicatePending(AdjudicatePendingOptions{}, t0.Add(time.Minute))
	// source_metadata + annotated_evidence candidates auto-park at
	// admission (their State is already 'parked'); the walker skips
	// them. Only the 2 bare proposed candidates get adjudicated here.
	if r.Adjudicated != 2 {
		t.Fatalf("adjudicated = %d, want 2 (only the bare proposed candidates)", r.Adjudicated)
	}
	if r.VerdictCounts["park"] != 2 {
		t.Fatalf("verdict counts = %+v, want park=2", r.VerdictCounts)
	}
	if r.Scanned != 5 {
		t.Fatalf("scanned = %d, want 5", r.Scanned)
	}
	if r.Skipped != 3 {
		t.Fatalf("skipped = %d, want 3 (already-parked source_metadata)", r.Skipped)
	}

	// Idempotency: re-running sees nothing to do.
	r2 := s.AdjudicatePending(AdjudicatePendingOptions{}, t0.Add(2*time.Minute))
	if r2.Adjudicated != 0 || r2.Skipped != 5 {
		t.Fatalf("re-run adjudicated=%d skipped=%d, want 0/5", r2.Adjudicated, r2.Skipped)
	}
}

// TestAdjudicatePendingRefusesFixedPromote: the walker refuses --verdict
// promote even when the operator passes it — promotion is per-id only.
func TestAdjudicatePendingRefusesFixedPromote(t *testing.T) {
	s := newStore(t)
	c := cand("e", "p", "v", "")
	c.ID = "cand-promote-attempt"
	if _, err := s.Capture([]Candidate{c}, t0); err != nil {
		t.Fatal(err)
	}
	r := s.AdjudicatePending(AdjudicatePendingOptions{Verdict: "promote"}, t0.Add(time.Minute))
	if r.Adjudicated != 0 {
		t.Fatalf("batch refused promote but adjudicated=%d", r.Adjudicated)
	}
	if len(r.Errors) != 1 || !strings.Contains(r.Errors[0].Err, "refuses --verdict promote") {
		t.Fatalf("expected promote-refusal error, got %+v", r.Errors)
	}
}

// TestAdjudicatePendingCounterReflectsActualCloses: regression for the
// AdjudicatePending counter bug. Three candidates captured with
// PromotionBasis = BasisExplicitConfirmation land in state "proposed"
// (no source qualifier, so dispositionVerdict returns "park"). The
// batch must adjudicate all 3 and report Adjudicated=3, Skipped=0,
// verdict_counts["park"]=3. Prior to the fix, the loop relied on the
// stale snapshot returned by AllCandidates() at the top of the verb;
// a successful Adjudicate that flipped State to "parked" was not
// reflected in the local var `c`, but a concurrent writer
// (admission/auto-park race) could close candidates between the
// snapshot and the iteration, leaving the loop with a populated
// Skipped bucket and an empty Adjudicated bucket. The fix re-reads
// each candidate's live state at the top of the loop iteration so
// the counter reflects what actually closed in this pass.
func TestAdjudicatePendingCounterReflectsActualCloses(t *testing.T) {
	s := newStore(t)
	for i := range 3 {
		c := cand("e", "p", fmt.Sprintf("counter-%d", i), "")
		c.ID = fmt.Sprintf("cand-counter-%d", i)
		c.PromotionBasis = candidates.BasisExplicitConfirmation
		if _, err := s.Capture([]Candidate{c}, t0); err != nil {
			t.Fatal(err)
		}
	}
	r := s.AdjudicatePending(AdjudicatePendingOptions{}, t0.Add(time.Minute))
	if r.Adjudicated != 3 {
		t.Fatalf("adjudicated = %d, want 3 (bug observation: counter underreports successful closes)", r.Adjudicated)
	}
	if r.Skipped != 0 {
		t.Fatalf("skipped = %d, want 0 (3 bare proposed candidates must not be skipped)", r.Skipped)
	}
	if r.VerdictCounts["park"] != 3 {
		t.Fatalf("verdict counts = %+v, want park=3", r.VerdictCounts)
	}
}

// TestAdjudicatePendingLimitCapsAdjudicatedNotSkipped: --limit bounds
// ADJUDICATED candidates, not scanning. Falsifies the old no-op surface
// where --limit was parsed, echoed as "reserved", and the whole inbox was
// adjudicated anyway. Capture mints hash IDs, so candidate order is not
// under the test's control — every assertion below is order-independent.
func TestAdjudicatePendingLimitCapsAdjudicatedNotSkipped(t *testing.T) {
	s := newStore(t)
	// 3 admission-parked candidates (skipped by every pass; they must
	// never consume the cap — the cap counts Adjudicated, not Scanned).
	for i := 0; i < 3; i++ {
		pc := cand("e", "p", fmt.Sprintf("parked-%d", i), "")
		pc.Qualifiers = map[string]string{"source": "knowledge_import"}
		if _, err := s.Capture([]Candidate{pc}, t0); err != nil {
			t.Fatal(err)
		}
	}
	// 2 bare proposed candidates (adjudicable).
	for i := 0; i < 2; i++ {
		if _, err := s.Capture([]Candidate{cand("e", "p", fmt.Sprintf("bare-%d", i), "")}, t0); err != nil {
			t.Fatal(err)
		}
	}
	// Dry-run honors the cap: plans exactly one adjudication, then stops.
	rd := s.AdjudicatePending(AdjudicatePendingOptions{DryRun: true, Limit: 1}, t0.Add(time.Minute))
	if rd.Adjudicated != 1 || !rd.LimitReached {
		t.Fatalf("limited dry-run adjudicated=%d limitReached=%v, want 1/true", rd.Adjudicated, rd.LimitReached)
	}
	// Real capped pass: exactly one candidate is closed, cap flagged.
	r := s.AdjudicatePending(AdjudicatePendingOptions{Limit: 1}, t0.Add(2*time.Minute))
	if r.Adjudicated != 1 || !r.LimitReached {
		t.Fatalf("limited pass adjudicated=%d limitReached=%v, want 1/true (skips must not consume the cap)", r.Adjudicated, r.LimitReached)
	}
	if r.Limit != 1 {
		t.Fatalf("report limit=%d, want echo of opts.Limit", r.Limit)
	}
	// The drain is resumable: an uncapped pass finishes the remainder.
	r2 := s.AdjudicatePending(AdjudicatePendingOptions{}, t0.Add(3*time.Minute))
	if r2.Adjudicated != 1 || r2.LimitReached {
		t.Fatalf("resume pass adjudicated=%d limitReached=%v, want 1/false", r2.Adjudicated, r2.LimitReached)
	}
	// Idempotent end state: nothing adjudicable, all five closed.
	r3 := s.AdjudicatePending(AdjudicatePendingOptions{}, t0.Add(4*time.Minute))
	if r3.Adjudicated != 0 || r3.Skipped != 5 {
		t.Fatalf("final pass adjudicated=%d skipped=%d, want 0/5", r3.Adjudicated, r3.Skipped)
	}
}
