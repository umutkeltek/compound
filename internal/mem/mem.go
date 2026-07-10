// Package mem is P6: memory re-bound through the spine. It is the OWNED ledger — the
// authority — built on the patterns the external-systems audit converged on:
//   - MVCC-by-fold: an append-only, immutable claim log + a monotonic seq; "current" and
//     "as of T" are DERIVED by folding, never by mutating a record. Claims are never deleted.
//   - candidate-first: extracted facts land as untrusted Candidates and are adjudicated
//     reject | park | promote by deterministic code (the LLM only proposes).
//   - contradiction over delete: a promotion that changes a fact SUPERSEDES the prior claim
//     via an edge (the version chain); the old claim stays as history.
//   - provenance everywhere: every claim carries how it was derived + its evidence refs.
//
// A promotion emits a Transition (memory_delta) — memory is one projection of the spine.
// No engine here: the ledger alone answers exact, delta, and as-of queries.
// See docs/memory-architecture.md "Adopted Patterns".
package mem

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"compound/internal/candidates"

	"compound/internal/envelope"
)

// Derivation tags how a claim/candidate was produced (provenance). ambiguous auto-surfaces.
var Derivations = []string{"deterministic", "extracted", "inferred", "ambiguous"}

// ClaimTypes is the claim taxonomy — what KIND of statement a claim is. It sharpens the topic axis
// (Position/Pattern group by Topic(), which prefers qualifiers["domain"] over the bare predicate).
// The spine-native convergence of the Gen-1 memory.ClaimType. Empty is allowed (untyped).
var ClaimTypes = []string{"preference", "decision", "architecture", "status", "constraint"}

// Verdicts are the adjudication outcomes (candidate-first).
var Verdicts = []string{"reject", "park", "promote", "no_promote"}

// Visibilities are the temperature tiers. retired is hidden from default recall but kept;
// volatile is promoted-but-clocked (useful now, expires unless reinforced) and never ambient.
var Visibilities = []string{"ambient", "contextual", "candidate", "volatile", "private", "operational", "retired"}

const (
	ClaimStatePromoted    = "promoted"
	ClaimStateNeedsReview = "needs_review"

	EdgeKindSupersedes    = "supersedes"
	EdgeKindReinforces    = "reinforces"
	EdgeKindNeedsReview   = "needs_review"
	EdgeKindReviewCleared = "needs_review_cleared"
	EdgeKindCorroboration = "corroboration"
)

// CandidateExpectation is the structured later fact that would make a parked seed matter.
type CandidateExpectation struct {
	Entity    string `json:"entity,omitempty"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
	Detail    string `json:"detail,omitempty"`
}

// Candidate is an untrusted, proposed fact awaiting adjudication (lives in the inbox).
type Candidate struct {
	ID              string                `json:"id"`
	Entity          string                `json:"entity"`
	Predicate       string                `json:"predicate"`
	Object          string                `json:"object"`
	ObservationDate string                `json:"observation_date"` // temporal grounding (when the fact was true)
	Confidence      float64               `json:"confidence"`
	Derivation      string                `json:"derivation"`
	ProvenanceLane  string                `json:"provenance_lane,omitempty"` // provenance: producing model/lane when known
	ClaimType       string                `json:"claim_type,omitempty"`      // the claim taxonomy (mem.ClaimTypes); empty = untyped
	CanonClass      string                `json:"canon_class,omitempty"`     // kind-of-truth tier (mem.CanonClasses); empty = derive from ClaimType
	Qualifiers      map[string]string     `json:"qualifiers,omitempty"`      // e.g. {"domain": "languages"} → sharpens Topic()
	TopicRefs       []string              `json:"topic_refs,omitempty"`      // optional concept taxonomy refs; old rows omit this safely
	Tags            []string              `json:"tags,omitempty"`
	Salience        string                `json:"salience,omitempty"`
	Expectation     *CandidateExpectation `json:"expectation,omitempty"`
	ExpiresAt       string                `json:"expires_at,omitempty"`
	PromotionBasis  string                `json:"promotion_basis,omitempty"` // WHY it may promote (candidates.PromotionBases); empty = none
	SourceTraceRef  string                `json:"source_trace_ref"`          // provenance: where it came from
	SnippetHash     string                `json:"snippet_hash"`              // bounded snippet, not the raw body
	Visibility      string                `json:"visibility"`
	State           string                `json:"state"` // proposed | parked | promoted | rejected
	Reason          string                `json:"reason,omitempty"`
	Annotations     []CandidateAnnotation `json:"annotations,omitempty"`
	CreatedAt       string                `json:"created_at"`
}

// Claim is a durable, IMMUTABLE assertion in the ledger. It is never mutated or deleted;
// supersession and validity are derived from the seq order + supersedes edges (MVCC-by-fold).
type CandidateAnnotation struct {
	ID               string                    `json:"id"`
	Kind             string                    `json:"kind"`
	Decision         string                    `json:"decision"`
	OriginalDecision string                    `json:"original_decision,omitempty"`
	RunID            string                    `json:"run_id,omitempty"`
	VerdictArtifact  string                    `json:"verdict_artifact,omitempty"`
	Rationale        string                    `json:"rationale,omitempty"`
	Confidence       float64                   `json:"confidence,omitempty"`
	EvidenceRefs     []string                  `json:"evidence_refs,omitempty"`
	LensFindings     []CandidateAnnotationLens `json:"lens_findings,omitempty"`
	CreatedAt        string                    `json:"created_at"`
}

type CandidateAnnotationLens struct {
	LensRef          string                       `json:"lens_ref"`
	Status           string                       `json:"status,omitempty"`
	Confidence       float64                      `json:"confidence,omitempty"`
	RouteEvidenceRef string                       `json:"route_evidence_ref,omitempty"`
	Findings         []CandidateAnnotationFinding `json:"findings,omitempty"`
}

type CandidateAnnotationFinding struct {
	Claim        string   `json:"claim,omitempty"`
	Severity     string   `json:"severity,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

type Claim struct {
	ID              string            `json:"id"`
	Seq             int64             `json:"seq"` // monotonic version clock
	Entity          string            `json:"entity"`
	Predicate       string            `json:"predicate"`
	Object          string            `json:"object"`
	ValidFrom       string            `json:"valid_from"` // observation date (immutable)
	Confidence      float64           `json:"confidence"`
	Derivation      string            `json:"derivation"`                // provenance: how derived
	ProvenanceLane  string            `json:"provenance_lane,omitempty"` // provenance: producing model/lane when known
	ClaimType       string            `json:"claim_type,omitempty"`      // the claim taxonomy (mem.ClaimTypes)
	CanonClass      string            `json:"canon_class,omitempty"`     // kind-of-truth tier (mem.CanonClasses); empty = derive from ClaimType
	Qualifiers      map[string]string `json:"qualifiers,omitempty"`      // e.g. {"domain": "languages"} → Topic()
	TopicRefs       []string          `json:"topic_refs,omitempty"`      // optional concept taxonomy refs; additive for older rows
	PromotionBasis  string            `json:"promotion_basis,omitempty"` // WHY it was promoted (the recallable "why" of canon)
	EvidenceRefs    []string          `json:"evidence_refs"`             // provenance: source candidate/trace
	Supersedes      string            `json:"supersedes,omitempty"`
	Visibility      string            `json:"visibility"`
	ExpiresAt       string            `json:"expires_at,omitempty"`       // volatile-only claim clock ("useful now, expires unless reinforced"); empty on every other visibility
	CooledReason    string            `json:"cooled_reason,omitempty"`    // why a version was cooled (volatile_expired_unreinforced); NOT a Qualifier — that map is identity-bearing (Topic/dup-detection)
	State           string            `json:"state,omitempty"`            // derived: promoted | needs_review
	ReviewRefs      []string          `json:"review_refs,omitempty"`      // append-only review marker refs
	ConfidenceDelta float64           `json:"confidence_delta,omitempty"` // latest contradicting evidence delta
	CreatedAt       string            `json:"created_at"`
}

// Topic is the axis Position/Pattern group by: the claim's domain qualifier if set, else the
// predicate. So domain-tagged claims cluster by domain while plain ones stay predicate-grouped.
func (c Claim) Topic() string {
	return topicFromQualifiers(c.Predicate, c.Qualifiers)
}

// Edge is an append-only relation between claims. Its seq is when the relation took effect,
// so "current as of T" can fold supersession deterministically.
type Edge struct {
	Seq             int64    `json:"seq"`
	Kind            string   `json:"kind"` // supersedes | reinforces | needs_review | relation kinds
	From            string   `json:"from"`
	To              string   `json:"to"`
	Confidence      float64  `json:"confidence"`
	ConfidenceDelta float64  `json:"confidence_delta,omitempty"`
	EvidenceRefs    []string `json:"evidence_refs,omitempty"`
	CreatedAt       string   `json:"created_at"`
}

// Position is the CURRENT durable stance per fact slot. Topic remains the broad domain axis, while
// predicate and claim type keep independent facts in that domain from sharing a version chain. The
// active claim is the current (non-superseded) version; the chain is the FULL supersession history
// (MVCC-by-fold), so a Position carries old→new, not just the latest value.
type Position struct {
	SchemaVersion         string   `json:"schema_version"`
	ID                    string   `json:"id"` // readable entity::topic prefix plus a fact-slot hash
	Entity                string   `json:"entity"`
	Topic                 string   `json:"topic"` // mem's topic axis = domain qualifier, else predicate
	Predicate             string   `json:"predicate,omitempty"`
	ActiveClaimID         string   `json:"active_claim_id"`
	ActiveObject          string   `json:"active_object"` // the current value (convenience)
	Status                string   `json:"status"`        // active | retired | contested
	SupportingClaimRefs   []string `json:"supporting_claim_refs,omitempty"`
	SupersededHistoryRefs []string `json:"superseded_history_refs,omitempty"`
	ClaimChain            []string `json:"claim_chain"` // all versions oldest→newest (the supersession history)
	ReviewRefs            []string `json:"review_refs,omitempty"`
	EvidenceRefs          []string `json:"evidence_refs,omitempty"`
	TopicRefs             []string `json:"topic_refs,omitempty"`
	FoldedAt              string   `json:"folded_at,omitempty"`
	AsOfSeq               int64    `json:"as_of_seq,omitempty"`
}

// Store is the memory ledger rooted at a COMPOUND_HOME.
type Store struct{ home string }

func Open(homePath string) (*Store, error) {
	if strings.TrimSpace(homePath) == "" {
		return nil, fmt.Errorf("no COMPOUND_HOME (run `compound home init`)")
	}
	for _, d := range []string{candidates.InboxDir(candidates.Memory), "ledger", "events"} {
		if err := os.MkdirAll(filepath.Join(homePath, d), 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{home: homePath}, nil
}

func (s *Store) candDir() string {
	return filepath.Join(s.home, candidates.InboxDir(candidates.Memory))
}
func (s *Store) claimsLog() string          { return filepath.Join(s.home, "ledger", "claims.jsonl") }
func (s *Store) edgesLog() string           { return filepath.Join(s.home, "ledger", "edges.jsonl") }
func (s *Store) seqFile() string            { return filepath.Join(s.home, "ledger", "seq") }
func (s *Store) eventPath(id string) string { return filepath.Join(s.home, "events", id+".json") }

// --- capture (candidate-first) ---

// Capture writes proposed candidates to the inbox. The LLM-extraction step is upstream; this
// is the deterministic intake — nothing here is canon until adjudicated.
func (s *Store) Capture(cands []Candidate, now time.Time) ([]Candidate, error) {
	out := make([]Candidate, 0, len(cands))
	for _, c := range cands {
		if c.Derivation == "" {
			c.Derivation = "extracted"
		}
		if !oneOf(c.Derivation, Derivations) {
			return out, fmt.Errorf("candidate %s: bad derivation %q", c.Entity, c.Derivation)
		}
		if c.ClaimType != "" && !oneOf(c.ClaimType, ClaimTypes) {
			return out, fmt.Errorf("candidate %s: bad claim_type %q (allowed: %v)", c.Entity, c.ClaimType, ClaimTypes)
		}
		if c.CanonClass != "" {
			if c.CanonClass == CanonClassUnclassified {
				return out, fmt.Errorf("candidate %s: canon_class %q is derived-only and never storable (allowed: %v)", c.Entity, c.CanonClass, CanonClasses)
			}
			if !oneOf(c.CanonClass, CanonClasses) {
				return out, fmt.Errorf("candidate %s: bad canon_class %q (allowed: %v)", c.Entity, c.CanonClass, CanonClasses)
			}
		}
		c.TopicRefs = normalizeStringRefs(c.TopicRefs)
		if c.Visibility == "" {
			c.Visibility = "operational"
		}
		if err := validateVolatileWrite("candidate "+c.Entity, c.Visibility, c.ExpiresAt, c.ClaimType, c.CanonClass); err != nil {
			return out, err
		}
		c.ID = idFor("cand_", c.Entity, c.Predicate, c.Object, ts(now))
		echo, err := s.reinforceIfEcho(c, now)
		if err != nil {
			return out, err
		}
		if echo {
			continue
		}
		c.State = "proposed"
		c.CreatedAt = rfc3339(now)
		// Decisions-down write seam: knowledge tiers + disposition (decay_park / auto_file / operator).
		c, _ = AdmitCandidateAt(c, now)
		if err := candidates.WriteCandidate(s.home, candidates.Memory, c.ID, &c); err != nil {
			return out, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *Store) getCandidate(id string) (Candidate, error) {
	var c Candidate
	err := readJSON(filepath.Join(s.candDir(), id+".json"), &c)
	return c, err
}

func (s *Store) AnnotateCandidate(candID string, ann CandidateAnnotation, now time.Time) (Candidate, error) {
	c, err := s.getCandidate(candID)
	if err != nil {
		return Candidate{}, err
	}
	if c.State == "promoted" {
		return Candidate{}, fmt.Errorf("candidate %s already promoted", candID)
	}
	if strings.TrimSpace(ann.ID) == "" {
		ann.ID = idFor("ann_", candID, ann.Kind, ann.Decision, ts(now))
	}
	if strings.TrimSpace(ann.Kind) == "" {
		ann.Kind = "panel_verdict"
	}
	if strings.TrimSpace(ann.Decision) == "" {
		ann.Decision = "annotate"
	}
	if strings.TrimSpace(ann.CreatedAt) == "" {
		ann.CreatedAt = rfc3339(now)
	}
	if !candidateHasAnnotation(c.Annotations, ann.ID) {
		c.Annotations = append(c.Annotations, ann)
	}
	c.State = "parked"
	if strings.TrimSpace(c.Reason) == "" {
		c.Reason = "annotated evidence; not promoted"
	}
	if err := candidates.WriteCandidate(s.home, candidates.Memory, c.ID, &c); err != nil {
		return Candidate{}, err
	}
	return c, nil
}

func candidateHasAnnotation(annotations []CandidateAnnotation, id string) bool {
	for _, ann := range annotations {
		if ann.ID == id {
			return true
		}
	}
	return false
}

// AllCandidates returns inbox candidates in deterministic id order.
func (s *Store) AllCandidates() ([]Candidate, error) {
	entries, err := os.ReadDir(s.candDir())
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var c Candidate
		if err := readJSON(filepath.Join(s.candDir(), entry.Name()), &c); err != nil {
			return out, err
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// --- adjudicate (code decides) ---

type AdjudicateResult struct {
	Verdict    string               `json:"verdict"`
	Candidate  Candidate            `json:"candidate"`
	Claim      *Claim               `json:"claim,omitempty"`
	Superseded string               `json:"superseded,omitempty"`
	Transition *envelope.Transition `json:"transition,omitempty"`
	Note       string               `json:"note,omitempty"`
}

// Adjudicate applies a verdict to a candidate. promote writes an immutable Claim (next seq,
// provenance), supersedes the prior current claim if the value changed (edge, not delete),
// and emits a Transition (memory_delta). A duplicate (same current value) is rejected.
func (s *Store) Adjudicate(candID, verdict, workID string, now time.Time) (AdjudicateResult, error) {
	var res AdjudicateResult
	if !oneOf(verdict, Verdicts) {
		return res, fmt.Errorf("verdict must be one of %v, got %q", Verdicts, verdict)
	}
	c, err := s.getCandidate(candID)
	if err != nil {
		return res, err
	}
	res.Candidate = c
	res.Verdict = verdict
	if c.State == "promoted" {
		return res, fmt.Errorf("candidate %s already promoted", candID)
	}

	if verdict != "promote" {
		c.State = nonPromoteState(verdict)
		if err := candidates.WriteCandidate(s.home, candidates.Memory, c.ID, &c); err != nil {
			return res, err
		}
		res.Candidate = c
		return res, nil
	}

	if !candidates.ValidBasis(c.PromotionBasis) {
		basis, err := s.corroborationPromotionBasis(c.ID)
		if err != nil {
			return res, err
		}
		c.PromotionBasis = basis
	}
	if !candidates.ValidBasis(c.PromotionBasis) {
		return res, fmt.Errorf("candidate %s cannot promote without valid promotion_basis (got %q; allowed: %v)", candID, c.PromotionBasis, candidates.PromotionBases)
	}
	// Defense-in-depth for the single claim-append door: a clockless or
	// doctrine-class volatile candidate that predates the Capture guard (or was
	// hand-written) must still never become a ledger claim.
	if err := validateVolatileWrite("candidate "+c.ID, c.Visibility, c.ExpiresAt, c.ClaimType, c.CanonClass); err != nil {
		return res, err
	}

	// promote: the prior-lookup + seq allocation + claim/edge append run under one lock so
	// seq stays monotonic-unique and supersession is consistent even across concurrent writers
	// (single-writer is the norm; the lock makes MVCC-by-fold mechanically true, not just a phrase).
	var (
		claim         Claim
		prior         Claim
		hasPrior, dup bool
	)
	lockErr := s.withLock(func() error {
		p, hp, err := s.currentClusterClaim(c, 0)
		if err != nil {
			return err
		}
		prior, hasPrior = p, hp
		if isDuplicatePromotion(c, prior, hasPrior) {
			dup = true
			return nil // same value already current → no redundant version
		}
		seq, err := s.nextSeq()
		if err != nil {
			return err
		}
		claim = promotedClaim(c, prior, hasPrior, seq, now)
		if err := appendJSONL(s.claimsLog(), &claim); err != nil {
			return err
		}
		if hasPrior {
			edge := supersedesEdge(claim, prior, c, seq, now)
			return appendJSONL(s.edgesLog(), &edge)
		}
		return nil
	})
	if lockErr != nil {
		return res, lockErr
	}
	if dup {
		c.State = "rejected"
		c.Reason = "duplicate of current claim " + prior.ID
		_ = candidates.WriteCandidate(s.home, candidates.Memory, c.ID, &c)
		res.Verdict, res.Candidate, res.Note = "reject", c, "already current: "+prior.ID
		return res, nil
	}
	if hasPrior {
		res.Superseded = prior.ID
	}

	// emit the Transition (memory_delta) — memory is a projection of the spine.
	tr := s.memoryTransition(claim, prior, hasPrior, workID, now)
	if err := tr.Validate(); err != nil {
		return res, fmt.Errorf("emitted transition invalid: %w", err)
	}
	if err := writeJSON(s.eventPath(tr.ID), &tr); err != nil {
		return res, err
	}

	c.State = "promoted"
	if err := candidates.WriteCandidate(s.home, candidates.Memory, c.ID, &c); err != nil {
		return res, err
	}
	res.Candidate, res.Claim, res.Transition = c, &claim, &tr
	return res, nil
}

// The promotion decision is pure: nonPromoteState / isDuplicatePromotion /
// promotedClaim / supersedesEdge compute the verdict's effect from a candidate
// and the prior current claim, with no I/O. Adjudicate is the thin apply: it
// reads the candidate + prior, allocates the seq under the lock, then writes what
// these decide. The split lets the decision be table-tested without a
// COMPOUND_HOME (see TestPromotionDecisionIsPure).

// nonPromoteState maps a non-promote verdict to the candidate's resulting state.
func nonPromoteState(verdict string) string {
	return map[string]string{"reject": "rejected", "park": "parked", "no_promote": "no_promote"}[verdict]
}

func (s *Store) corroborationPromotionBasis(candidateID string) (string, error) {
	marks, err := s.candidateReviewMarksAsOf(0)
	if err != nil {
		return "", err
	}
	if mark, ok := marks[candidateID]; ok && len(mark.Refs) > 0 {
		return candidates.BasisRuntimeProof, nil
	}
	return "", nil
}

// isDuplicatePromotion reports whether promoting c would only restate the current
// claim (same object and qualifiers), so no new version is needed.
func isDuplicatePromotion(c Candidate, prior Claim, hasPrior bool) bool {
	if !hasPrior {
		return false
	}
	if c.Entity != "" || c.Predicate != "" || c.ClaimType != "" {
		candidateKey := ClaimSlotKey(c.Entity, c.Predicate, c.ClaimType, c.Qualifiers, "")
		priorKey := ClaimSlotKey(prior.Entity, prior.Predicate, prior.ClaimType, prior.Qualifiers, prior.ID)
		if !sameClusterKey(candidateKey, priorKey) {
			return false
		}
	}
	return SameClaimObject(prior.Object, c.Object) && sameQualifiers(prior.Qualifiers, c.Qualifiers)
}

func sameQualifiers(a, b map[string]string) bool { return SameClaimQualifiers(a, b) }

// promotedClaim builds the durable Claim a promotion appends at the allocated
// seq: the candidate's value + taxonomy, superseding the prior claim when one
// exists.
func promotedClaim(c Candidate, prior Claim, hasPrior bool, seq int64, now time.Time) Claim {
	claim := Claim{
		ID:             idFor("claim_", c.Entity, c.Predicate, c.Object, strconv.FormatInt(seq, 10)),
		Seq:            seq,
		Entity:         c.Entity,
		Predicate:      c.Predicate,
		Object:         c.Object,
		ValidFrom:      orDef(c.ObservationDate, rfc3339(now)),
		Confidence:     c.Confidence,
		Derivation:     c.Derivation,
		ProvenanceLane: c.ProvenanceLane,
		ClaimType:      c.ClaimType,  // carry the taxonomy + topic qualifiers onto the durable claim
		CanonClass:     c.CanonClass, // carry explicit kind-of-truth; empty rows derive via EffectiveCanonClass
		Qualifiers:     c.Qualifiers,
		TopicRefs:      normalizeStringRefs(c.TopicRefs),
		PromotionBasis: c.PromotionBasis, // carry the "why" of promotion onto the durable claim
		EvidenceRefs:   []string{c.ID, c.SourceTraceRef},
		Visibility:     orDef(c.Visibility, "operational"),
		CreatedAt:      rfc3339(now),
	}
	// The clock crosses onto the durable claim ONLY for volatile: a decay-parked
	// candidate's ExpiresAt is candidate-lifecycle state and must not leak onto
	// claims of any other visibility.
	if isVolatileVisibility(claim.Visibility) {
		claim.ExpiresAt = strings.TrimSpace(c.ExpiresAt)
	}
	if hasPrior {
		claim.Supersedes = prior.ID
	}
	return claim
}

// supersedesEdge builds the supersedes edge from a promoted claim to the prior.
func supersedesEdge(claim, prior Claim, c Candidate, seq int64, now time.Time) Edge {
	return Edge{Seq: seq, Kind: EdgeKindSupersedes, From: claim.ID, To: prior.ID, Confidence: c.Confidence, CreatedAt: rfc3339(now)}
}

func reinforcesEdge(from string, prior Claim, c Candidate, seq int64, now time.Time) Edge {
	return Edge{Seq: seq, Kind: EdgeKindReinforces, From: from, To: prior.ID, Confidence: c.Confidence, CreatedAt: rfc3339(now)}
}

func (s *Store) reinforceIfEcho(c Candidate, now time.Time) (bool, error) {
	var reinforced bool
	err := s.withLock(func() error {
		prior, hasPrior, err := s.currentClusterClaim(c, 0)
		if err != nil {
			return err
		}
		if hasPrior {
			// An exact fact slot wins over a broad domain echo. A changed value
			// must proceed to adjudication and supersede its own prior version.
			if !isDuplicatePromotion(c, prior, true) {
				return nil
			}
		} else {
			prior, hasPrior, err = s.currentEchoClaim(c, 0)
			if err != nil {
				return err
			}
			if !hasPrior {
				return nil
			}
		}
		evidence, err := s.RecordEvidence(EvidenceItem{
			SourceKind: "memory-candidate",
			SourceID:   firstNonEmpty(c.SourceTraceRef, c.ID),
			QuoteText:  c.Entity + " " + c.Predicate + " " + c.Object,
			EventTime:  c.ObservationDate,
			Actor:      "agent",
			Visibility: c.Visibility,
		}, now)
		if err != nil {
			return err
		}
		seq, err := s.nextSeq()
		if err != nil {
			return err
		}
		edge := reinforcesEdge(evidence.ID, prior, c, seq, now)
		if err := appendJSONL(s.edgesLog(), &edge); err != nil {
			return err
		}
		reinforced = true
		return nil
	})
	return reinforced, err
}

func (s *Store) memoryTransition(claim Claim, prior Claim, hasPrior bool, workID string, now time.Time) envelope.Transition {
	wid := workID
	if wid == "" {
		wid = "work_mem_" + shortHash(claim.ID) // the adjudication is the unit of work
	}
	before := []string{}
	delta := "claim added: " + claim.Entity + "." + claim.Predicate + " = " + claim.Object
	if hasPrior {
		before = []string{prior.ID}
		delta = "claim superseded: " + claim.Entity + "." + claim.Predicate + ": " + prior.Object + " -> " + claim.Object
	}
	return envelope.Transition{
		Header: envelope.Header{
			SchemaVersion: envelope.SVTransition,
			ID:            idFor("tr_", claim.ID, ts(now)),
			CreatedAt:     rfc3339(now),
			Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
			IDs:           envelope.IDs{WorkID: wid},
			Scope:         envelope.Scope{EntityID: claim.Entity},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  claim.EvidenceRefs,
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		BeforeRefs: before,
		ActionRefs: []string{},
		AfterRefs:  []string{claim.ID},
		StateDelta: delta,
		Projections: envelope.Projections{
			Memory: []string{claim.ID}, Skill: []string{}, Product: []string{}, Cost: []string{}, Risk: []string{}, Taste: []string{}, Routing: []string{}, HumanAttention: []string{},
			Typed: []envelope.ProjectionRef{{Kind: "memory", Ref: claim.ID, Role: "promoted", EvidenceRef: strings.Join(claim.EvidenceRefs, ",")}},
		},
		SecondOrder: envelope.SecondOrder{FutureBehaviorChanged: true},
	}
}

// --- recall (relevance-gated, as-of-aware fold) ---

type RecallOpts struct {
	AsOfSeq            int64    // 0 = now (latest)
	IncludeRetired     bool     // default false
	IncludeNeedsReview bool     // default false: held claims do not keep ruling recall
	IncludeEvidence    bool     // include annotated candidates; default false
	MinConfidence      float64  // hard floor
	Visibilities       []string // allowed tiers; empty = all (minus retired unless IncludeRetired)
	Topic              string   // optional topic subtree root; filters claims by topic_refs
}

type RecallResult struct {
	Entity             string      `json:"entity"`
	AsOfSeq            int64       `json:"as_of_seq"`
	Topic              string      `json:"topic,omitempty"`
	TopicSubtree       []string    `json:"topic_subtree,omitempty"`
	Claims             []Claim     `json:"claims"`
	EvidenceCandidates []Candidate `json:"evidence_candidates,omitempty"`
	Filtered           int         `json:"filtered"`
	Coverage           string      `json:"coverage"`
}

// Recall returns the current claims for an entity AS OF a seq, after HARD filters
// (visibility/retired/confidence) run BEFORE the fold — never a post-filter. It returns a
// coverage verdict and never fabricates: an empty result says so plainly.
func (s *Store) Recall(entity string, opts RecallOpts) (RecallResult, error) {
	claims, err := s.loadClaimsAsOf(opts.AsOfSeq)
	if err != nil {
		return RecallResult{Entity: entity}, err
	}
	entity = strings.TrimSpace(entity)
	if entity != "" {
		entity, err = s.canonicalRecallEntity(entity, claims, opts.IncludeEvidence)
		if err != nil {
			return RecallResult{Entity: entity}, err
		}
	}
	topicSet, topicSubtree, err := s.topicScopeSet(opts.Topic)
	if err != nil {
		return RecallResult{Entity: entity}, err
	}
	res := RecallResult{Entity: entity, Topic: strings.TrimSpace(opts.Topic), TopicSubtree: topicSubtree}
	asOf := opts.AsOfSeq
	if asOf == 0 {
		asOf = s.maxSeq(claims)
	}
	res.AsOfSeq = asOf

	// 1. HARD gates first (scope/time/visibility/confidence).
	var gated []Claim
	for _, c := range claims {
		if entity != "" && c.Entity != entity {
			continue
		}
		if c.Seq > asOf {
			continue
		}
		if len(topicSet) > 0 && !claimHasTopicRef(c, topicSet) {
			continue
		}
		if !opts.IncludeRetired && c.Visibility == "retired" {
			res.Filtered++
			continue
		}
		if !opts.IncludeNeedsReview && c.State == ClaimStateNeedsReview {
			res.Filtered++
			continue
		}
		if opts.MinConfidence > 0 && c.Confidence < opts.MinConfidence {
			res.Filtered++
			continue
		}
		if len(opts.Visibilities) > 0 && !oneOf(c.Visibility, opts.Visibilities) {
			res.Filtered++
			continue
		}
		gated = append(gated, c)
	}

	// 2. Fold supersession as-of seq: a claim superseded by an edge with seq <= asOf is not current.
	superseded, err := s.supersededAsOf(asOf)
	if err != nil {
		return res, err
	}
	for _, c := range gated {
		if !superseded[c.ID] {
			res.Claims = append(res.Claims, c)
		}
	}
	sort.Slice(res.Claims, func(i, j int) bool { return res.Claims[i].Seq < res.Claims[j].Seq })

	if opts.IncludeEvidence {
		evidence, err := s.annotatedEvidenceCandidates(entity, opts)
		if err != nil {
			return res, err
		}
		res.EvidenceCandidates = evidence
	}

	switch {
	case len(res.Claims) > 0 && len(res.EvidenceCandidates) > 0:
		res.Coverage = fmt.Sprintf("%d current claim(s) for %q as seq %d (%d gated); %d annotated evidence candidate(s)", len(res.Claims), entity, asOf, res.Filtered, len(res.EvidenceCandidates))
	case len(res.EvidenceCandidates) > 0:
		res.Coverage = fmt.Sprintf("no current claims recorded for %q as seq %d; %d annotated evidence candidate(s)", entity, asOf, len(res.EvidenceCandidates))
	case len(res.Claims) > 0:
		res.Coverage = fmt.Sprintf("%d current claim(s) for %q as of seq %d (%d gated)", len(res.Claims), entity, asOf, res.Filtered)
	case res.Filtered > 0:
		res.Coverage = fmt.Sprintf("no claims pass the gate for %q (%d filtered by visibility/confidence) — not fabricating", entity, res.Filtered)
	default:
		res.Coverage = fmt.Sprintf("no claims recorded for %q as of seq %d", entity, asOf)
	}
	return res, nil
}

// Timeline walks the supersession chain for an entity, oldest→newest — the "what changed"
// view (delta-aware memory on the owned ledger, no engine).
func (s *Store) annotatedEvidenceCandidates(entity string, opts RecallOpts) ([]Candidate, error) {
	candidates, err := s.AllCandidates()
	if err != nil {
		return nil, err
	}
	topicSet, _, err := s.topicScopeSet(opts.Topic)
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0)
	for _, c := range candidates {
		if c.Entity != entity || len(c.Annotations) == 0 {
			continue
		}
		if len(topicSet) > 0 && !candidateHasTopicRef(c, topicSet) {
			continue
		}
		if !opts.IncludeRetired && c.Visibility == "retired" {
			continue
		}
		if opts.MinConfidence > 0 && c.Confidence < opts.MinConfidence {
			continue
		}
		if len(opts.Visibilities) > 0 && !oneOf(c.Visibility, opts.Visibilities) {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return out, nil
}

func candidateHasTopicRef(candidate Candidate, topicSet map[string]bool) bool {
	for _, ref := range candidate.TopicRefs {
		if topicSet[ref] {
			return true
		}
	}
	return false
}

func (s *Store) Timeline(entity string) ([]Claim, error) {
	claims, err := s.loadClaims()
	if err != nil {
		return nil, err
	}
	entity, err = s.canonicalRecallEntity(entity, claims, false)
	if err != nil {
		return nil, err
	}
	var out []Claim
	for _, c := range claims {
		if c.Entity == entity {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

func (s *Store) canonicalRecallEntity(entity string, claims []Claim, includeEvidence bool) (string, error) {
	entity = strings.TrimSpace(entity)
	if entity == "" {
		return entity, nil
	}
	for _, c := range claims {
		if c.Entity == entity {
			return entity, nil
		}
	}
	matches := map[string]struct{}{}
	for _, c := range claims {
		if strings.EqualFold(c.Entity, entity) {
			matches[c.Entity] = struct{}{}
		}
	}
	if len(matches) == 1 {
		for match := range matches {
			return match, nil
		}
	}
	if len(matches) > 1 {
		return entity, nil
	}
	if r, err := s.ResolveEntity(entity); err != nil {
		return "", err
	} else if r.Status == "resolved" {
		return r.Slug, nil
	} else if r.Status == "multi" {
		return entity, nil
	}

	if includeEvidence {
		candidates, err := s.AllCandidates()
		if err != nil {
			return "", err
		}
		for _, c := range candidates {
			if c.Entity == entity {
				return entity, nil
			}
			if strings.EqualFold(c.Entity, entity) {
				matches[c.Entity] = struct{}{}
			}
		}
	}
	if len(matches) != 1 {
		return entity, nil
	}
	for match := range matches {
		return match, nil
	}
	return entity, nil
}

// currentClaim returns the current claim for entity+predicate as of asOf (0 = now), if any.
func (s *Store) currentClaim(entity, predicate string, asOf int64) (Claim, bool, error) {
	claims, err := s.loadClaimsAsOf(asOf)
	if err != nil {
		return Claim{}, false, err
	}
	if asOf == 0 {
		asOf = s.maxSeq(claims)
	}
	superseded, err := s.supersededAsOf(asOf)
	if err != nil {
		return Claim{}, false, err
	}
	var best Claim
	found := false
	for _, c := range claims {
		if c.Entity == entity && c.Predicate == predicate && c.Seq <= asOf && !superseded[c.ID] {
			if !found || c.Seq > best.Seq {
				best, found = c, true
			}
		}
	}
	return best, found, nil
}

func (s *Store) currentClusterClaim(cand Candidate, asOf int64) (Claim, bool, error) {
	claims, err := s.loadClaimsAsOf(asOf)
	if err != nil {
		return Claim{}, false, err
	}
	if asOf == 0 {
		asOf = s.maxSeq(claims)
	}
	want, err := s.candidateClusterKey(cand)
	if err != nil {
		return Claim{}, false, err
	}
	superseded, err := s.supersededAsOf(asOf)
	if err != nil {
		return Claim{}, false, err
	}
	var best Claim
	found := false
	for _, claim := range claims {
		if claim.Seq > asOf || superseded[claim.ID] {
			continue
		}
		got, err := s.claimClusterKey(claim)
		if err != nil {
			return Claim{}, false, err
		}
		if sameClusterKey(got, want) && (!found || claim.Seq > best.Seq) {
			best, found = claim, true
		}
	}
	return best, found, nil
}

func (s *Store) currentEchoClaim(cand Candidate, asOf int64) (Claim, bool, error) {
	claims, err := s.loadClaimsAsOf(asOf)
	if err != nil {
		return Claim{}, false, err
	}
	if asOf == 0 {
		asOf = s.maxSeq(claims)
	}
	want, err := s.candidateEchoKey(cand)
	if err != nil {
		return Claim{}, false, err
	}
	superseded, err := s.supersededAsOf(asOf)
	if err != nil {
		return Claim{}, false, err
	}
	var best Claim
	found := false
	for _, claim := range claims {
		if claim.Seq > asOf || superseded[claim.ID] || !SameClaimObject(claim.Object, cand.Object) || !sameQualifiers(claim.Qualifiers, cand.Qualifiers) {
			continue
		}
		got, err := s.claimEchoKey(claim)
		if err != nil {
			return Claim{}, false, err
		}
		if sameClusterKey(got, want) && (!found || claim.Seq > best.Seq) {
			best, found = claim, true
		}
	}
	return best, found, nil
}

// --- doctor (which hop) ---

type Hop struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | empty | skipped | fail
	Detail string `json:"detail"`
}
type Report struct {
	Hops              []Hop              `json:"hops"`
	OK                bool               `json:"ok"`
	Note              string             `json:"note"`
	SemanticReadiness *SemanticReadiness `json:"semantic_readiness,omitempty"`
}

type SemanticReadiness struct {
	Status               string `json:"status"`
	Profile              string `json:"profile,omitempty"`
	ConfigPath           string `json:"config_path,omitempty"`
	Sidecar              string `json:"sidecar"`
	Engine               string `json:"engine,omitempty"`
	EmbedderConfigured   bool   `json:"embedder_configured"`
	EmbedderIdentity     string `json:"embedder_identity,omitempty"`
	EmbedderGuard        string `json:"embedder_guard,omitempty"`
	VectorCacheStatus    string `json:"vector_cache_status"`
	VectorCachePath      string `json:"vector_cache_path,omitempty"`
	VectorCacheCorpusGen int64  `json:"vector_cache_corpus_gen,omitempty"`
	VectorCacheClaims    int    `json:"vector_cache_claims,omitempty"`
	VectorCacheEmbedded  int    `json:"vector_cache_embedded,omitempty"`
	Detail               string `json:"detail"`
}

// Doctor reports WHICH hop is healthy or failed, not just "missing" — the fix differs per hop.
func (s *Store) Doctor() Report {
	return s.DoctorWithEngine(nil)
}

// DoctorWithEngine reports the normal memory chain plus an optional configured
// rented breadth engine. The ledger still owns truth; the engine hop only proves
// the configured index can serve a current claim without mutating ledger bytes.
func (s *Store) DoctorWithEngine(eng Engine) Report {
	r := Report{OK: true}
	add := func(name, status, detail string) {
		r.Hops = append(r.Hops, Hop{name, status, detail})
		if status == "fail" {
			r.OK = false
		}
	}

	cands, _ := os.ReadDir(s.candDir())
	add("capture", statusOf(len(cands) > 0), fmt.Sprintf("%d candidate(s) in inbox", len(cands)))

	claims, err := s.loadClaims()
	if err != nil {
		add("promotion", "fail", "cannot read claims: "+err.Error())
	} else {
		add("promotion", statusOf(len(claims) > 0), fmt.Sprintf("%d claim(s) promoted to ledger", len(claims)))
	}
	if eng == nil {
		add("sync/embedding", "skipped", "no engine wired (ledger answers exact/as-of without one)")
	} else {
		s.doctorEngineHop(eng, add)
	}

	// retrieval: do a recall on an arbitrary recorded entity and confirm it returns.
	if len(claims) > 0 {
		rr, err := s.Recall(claims[0].Entity, RecallOpts{})
		if err != nil {
			add("retrieval", "fail", err.Error())
		} else {
			add("retrieval", statusOf(len(rr.Claims) > 0), rr.Coverage)
		}
	} else {
		add("retrieval", "empty", "no claims to retrieve")
	}
	s.doctorRecallQualityHop(add)

	// authority framing: every claim must carry provenance (derivation + evidence).
	missing := 0
	for _, c := range claims {
		if c.Derivation == "" || len(c.EvidenceRefs) == 0 {
			missing++
		}
	}
	if missing > 0 {
		add("authority", "fail", fmt.Sprintf("%d claim(s) missing provenance", missing))
	} else {
		add("authority", statusOf(len(claims) > 0), "all claims carry derivation + evidence")
	}

	if r.OK {
		r.Note = "memory hops healthy (or empty/skipped); no failed hop"
	} else {
		r.Note = "a memory hop FAILED — fix at that hop, not 'memory missing'"
	}
	return r
}

func (s *Store) doctorRecallQualityHop(add func(name, status, detail string)) {
	cur, err := s.currentVisible()
	if err != nil {
		add("recall-quality", "fail", "cannot read current claims: "+err.Error())
		return
	}
	if len(cur) == 0 {
		add("recall-quality", "empty", "no current visible claims to probe")
		return
	}
	foldCounts := map[string]int{}
	for _, c := range cur {
		if alternateCaseProbe(c.Entity) == "" {
			continue
		}
		foldCounts[strings.ToLower(c.Entity)]++
	}
	for _, c := range cur {
		probe := alternateCaseProbe(c.Entity)
		if probe == "" || foldCounts[strings.ToLower(c.Entity)] != 1 {
			continue
		}
		rr, err := s.Recall(probe, RecallOpts{})
		if err != nil {
			add("recall-quality", "fail", fmt.Sprintf("%q probe failed: %v", probe, err))
			return
		}
		for _, got := range rr.Claims {
			if got.Entity == c.Entity {
				add("recall-quality", "ok", fmt.Sprintf("%q resolved %q with %d current claim(s)", probe, c.Entity, len(rr.Claims)))
				return
			}
		}
		add("recall-quality", "fail", fmt.Sprintf("%q did not resolve canonical entity %q: %s", probe, c.Entity, rr.Coverage))
		return
	}
	add("recall-quality", "empty", "no unambiguous alternate-case entity to probe")
}

func alternateCaseProbe(entity string) string {
	runes := []rune(strings.TrimSpace(entity))
	for i, r := range runes {
		switch {
		case 'a' <= r && r <= 'z':
			runes[i] = r - ('a' - 'A')
			return string(runes)
		case 'A' <= r && r <= 'Z':
			runes[i] = r + ('a' - 'A')
			return string(runes)
		}
	}
	return ""
}

func (s *Store) doctorEngineHop(eng Engine, add func(name, status, detail string)) {
	caps := eng.Capabilities()
	cur, err := s.currentVisible()
	if err != nil {
		add("sync/embedding", "fail", "cannot read current claims: "+err.Error())
		return
	}
	if len(cur) == 0 {
		add("sync/embedding", "empty", fmt.Sprintf("%s configured; no current visible claims to index", doctorEngineName(caps)))
		return
	}

	report, err := s.EngineDoctor(ClaimIndexText(cur[0]), 1, eng)
	detail := fmt.Sprintf("%s ledger_unchanged=%t hits=%d ledger_gen=%d engine_gen=%d", doctorEngineName(report.Engine), report.LedgerUnchanged, len(report.Hits), report.LedgerGen, report.Engine.CorpusGen)
	if err != nil {
		add("sync/embedding", "fail", detail+": "+err.Error())
		return
	}
	if report.Degraded {
		add("sync/embedding", "fail", detail+": degraded "+report.Error)
		return
	}
	if len(report.Hits) == 0 {
		add("sync/embedding", "fail", detail+": configured engine returned no hit for a current claim")
		return
	}
	add("sync/embedding", "ok", detail)
}

func doctorEngineName(caps EngineCapabilities) string {
	if strings.TrimSpace(caps.Name) != "" {
		return caps.Name
	}
	return "configured-engine"
}

// --- ledger internals (MVCC-by-fold) ---

// withLock serializes the seq-allocate + append critical section with an exclusive advisory
// file lock under COMPOUND_HOME/lock. The lock is released when the fd closes, so a crashed
// writer never wedges the ledger. (Darwin/Linux; compound's targets.)
func (s *Store) withLock(fn func() error) error {
	lockPath := filepath.Join(s.home, "lock", "mem.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

// nextSeq mints the next monotonic-unique seq under the caller's lock. It FAILS LOUD on a corrupt
// counter (never silently resets to 1), SELF-HEALS from the immutable logs (the counter must exceed
// every seq already on disk — claims and edges share the seq space), and persists atomically
// (temp + rename) so an O_TRUNC-then-crash can't leave an empty counter.
func (s *Store) nextSeq() (int64, error) {
	cur := int64(0)
	if b, err := os.ReadFile(s.seqFile()); err == nil {
		if t := strings.TrimSpace(string(b)); t != "" {
			v, perr := strconv.ParseInt(t, 10, 64)
			if perr != nil {
				return 0, fmt.Errorf("corrupt seq counter %q: %w", t, perr)
			}
			cur = v
		}
	}
	hi, err := s.maxLedgerSeq()
	if err != nil {
		return 0, err
	}
	if hi > cur {
		cur = hi // a lost/zeroed counter heals to above everything on disk
	}
	cur++
	tmp := s.seqFile() + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(cur, 10)), 0o644); err != nil {
		return 0, err
	}
	return cur, os.Rename(tmp, s.seqFile())
}

// maxLedgerSeq is the highest seq across claims AND edges (they share the seq space: relate-edges
// and supersedes edges both draw from nextSeq).
func (s *Store) maxLedgerSeq() (int64, error) {
	claims, err := s.loadClaimsRaw()
	if err != nil {
		return 0, err
	}
	hi := s.maxSeq(claims)
	err = readJSONL(s.edgesLog(), func(line []byte) error {
		var e Edge
		if json.Unmarshal(line, &e) == nil && e.Seq > hi {
			hi = e.Seq
		}
		return nil
	})
	return hi, err
}

func (s *Store) loadClaims() ([]Claim, error) {
	return s.loadClaimsAsOf(0)
}

func (s *Store) loadClaimsAsOf(asOf int64) ([]Claim, error) {
	claims, err := s.loadClaimsRaw()
	if err != nil {
		return nil, err
	}
	return s.decorateClaimReviewStates(claims, asOf)
}

func (s *Store) loadClaimsRaw() ([]Claim, error) {
	var out []Claim
	err := readJSONL(s.claimsLog(), func(line []byte) error {
		var c Claim
		if err := json.Unmarshal(line, &c); err != nil {
			return err
		}
		out = append(out, c)
		return nil
	})
	return out, err
}

type claimReviewMark struct {
	Refs            []string
	ConfidenceDelta float64
}

type candidateReviewMark struct {
	Refs []string
}

func (s *Store) decorateClaimReviewStates(claims []Claim, asOf int64) ([]Claim, error) {
	marks, err := s.claimReviewMarksAsOf(asOf)
	if err != nil {
		return nil, err
	}
	out := make([]Claim, len(claims))
	for i, claim := range claims {
		claim.State = ClaimStatePromoted
		if mark, ok := marks[claim.ID]; ok {
			claim.State = ClaimStateNeedsReview
			claim.ReviewRefs = append([]string(nil), mark.Refs...)
			claim.ConfidenceDelta = mark.ConfidenceDelta
		}
		out[i] = claim
	}
	return out, nil
}

func (s *Store) claimReviewMarksAsOf(asOf int64) (map[string]claimReviewMark, error) {
	type sourceState struct {
		markSeq         int64
		clearSeq        int64
		confidenceDelta float64
	}
	byClaim := map[string]map[string]*sourceState{}
	err := readJSONL(s.edgesLog(), func(line []byte) error {
		var e Edge
		if err := json.Unmarshal(line, &e); err != nil {
			return err
		}
		if e.Kind != EdgeKindNeedsReview && e.Kind != EdgeKindReviewCleared {
			return nil
		}
		if asOf > 0 && e.Seq > asOf {
			return nil
		}
		sources := byClaim[e.To]
		if sources == nil {
			sources = map[string]*sourceState{}
			byClaim[e.To] = sources
		}
		state := sources[e.From]
		if state == nil {
			state = &sourceState{}
			sources[e.From] = state
		}
		switch e.Kind {
		case EdgeKindNeedsReview:
			if e.Seq >= state.markSeq {
				state.markSeq = e.Seq
				state.confidenceDelta = e.ConfidenceDelta
			}
		case EdgeKindReviewCleared:
			if e.Seq >= state.clearSeq {
				state.clearSeq = e.Seq
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := map[string]claimReviewMark{}
	for claimID, sources := range byClaim {
		var mark claimReviewMark
		var latestSeq int64
		for source, state := range sources {
			if state.markSeq == 0 || state.clearSeq > state.markSeq {
				continue
			}
			if source != "" {
				mark.Refs = append(mark.Refs, source)
			}
			if state.markSeq >= latestSeq {
				latestSeq = state.markSeq
				mark.ConfidenceDelta = state.confidenceDelta
			}
		}
		sort.Strings(mark.Refs)
		if latestSeq > 0 {
			out[claimID] = mark
		}
	}
	return out, nil
}

func (s *Store) candidateReviewMarksAsOf(asOf int64) (map[string]candidateReviewMark, error) {
	marks := map[string]candidateReviewMark{}
	err := readJSONL(s.edgesLog(), func(line []byte) error {
		var e Edge
		if err := json.Unmarshal(line, &e); err != nil {
			return err
		}
		if e.Kind != EdgeKindCorroboration {
			return nil
		}
		if asOf > 0 && e.Seq > asOf {
			return nil
		}
		mark := marks[e.To]
		mark.Refs = append(mark.Refs, e.From)
		marks[e.To] = mark
		return nil
	})
	for id, mark := range marks {
		sort.Strings(mark.Refs)
		marks[id] = mark
	}
	return marks, err
}

func (s *Store) supersededAsOf(asOf int64) (map[string]bool, error) {
	out := map[string]bool{}
	err := readJSONL(s.edgesLog(), func(line []byte) error {
		var e Edge
		if err := json.Unmarshal(line, &e); err != nil {
			return err
		}
		if e.Kind == EdgeKindSupersedes && e.Seq <= asOf {
			out[e.To] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Also honor each claim's own Supersedes pointer, so a lost/torn supersedes-edge append can
	// never leave two "current" claims for one entity+predicate (the new claim records what it replaced).
	claims, err := s.loadClaimsRaw()
	if err != nil {
		return nil, err
	}
	for _, c := range claims {
		if c.Supersedes != "" && c.Seq <= asOf {
			out[c.Supersedes] = true
		}
	}
	return out, nil
}

func (s *Store) maxSeq(claims []Claim) int64 {
	var m int64
	for _, c := range claims {
		if c.Seq > m {
			m = c.Seq
		}
	}
	return m
}

// --- small io helpers ---

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("not found: %s", filepath.Base(path))
		}
		return err
	}
	return json.Unmarshal(b, v)
}

func appendJSONL(path string, v any) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func readJSONL(path string, fn func([]byte) error) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if err := fn([]byte(line)); err != nil {
			return err
		}
	}
	return nil
}

func idFor(prefix string, parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return prefix + hex.EncodeToString(h[:])[:12]
}
func shortHash(s string) string    { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:])[:12] }
func ts(now time.Time) string      { return strconv.FormatInt(now.UTC().UnixNano(), 10) }
func rfc3339(now time.Time) string { return now.UTC().Format(time.RFC3339) }

func statusOf(ok bool) string {
	if ok {
		return "ok"
	}
	return "empty"
}
func oneOf(v string, set []string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}
func orDef(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
