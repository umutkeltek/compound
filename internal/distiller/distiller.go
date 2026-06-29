// Package distiller is the P6 distillation front-end: it turns spine artifacts (a trace) into a
// typed, adjudicated CandidateSet — the layer that makes memory COMPOUND rather than accumulate.
// Invariants (also enforced by internal/mem): the model EXTRACTS, code DECIDES; every candidate
// is adjudicated (reject | park | promote | operationalize | retire); NOTHING reaches ambient
// recall unadjudicated; a self-improvement signal (a better method, a distiller mistake, a
// capability miss) becomes a CANDIDATE — never a silent prompt edit or auto-applied change.
// It extends internal/mem (memory-kind candidates flow through the mem ledger); it is not a
// parallel memory system. See docs/roadmap/phases.md (P6 TraceDistiller) and the
// "Distillation & capability-growth contracts" in docs/architecture/contracts/README.md.
package distiller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/candidates"
	"compound/internal/mem"
)

// Candidate kinds — the typed fan-out. memory routes through the ledger; the rest are candidate
// records; the last three are the self-improving signals that close the loop.
const (
	KindMemory        = "memory"
	KindSkill         = "skill"
	KindDecision      = "decision"
	KindFailure       = "failure_pattern"
	KindTaste         = "taste"
	KindProduct       = "product_hypothesis"
	KindRoute         = "route_observation"
	KindCapabilityObs = "capability_observation"
	KindMethodUpgrade = "method_upgrade"
	KindEvalCase      = "eval_case"
	KindPolicyPatch   = "policy_patch"
)

// Kinds is the typed allowlist. An unknown kind is rejected — it must never flow into a filesystem
// path (the inbox is keyed by kind), and a typed fan-out must stay typed.
var Kinds = []string{
	KindMemory, KindSkill, KindDecision, KindFailure, KindTaste, KindProduct,
	KindRoute, KindCapabilityObs, KindMethodUpgrade, KindEvalCase, KindPolicyPatch,
}

// Verdicts an adjudication can reach.
const (
	Reject         = "reject"
	Park           = "park"
	Promote        = "promote"
	Operationalize = "operationalize"
	Retire         = "retire"
)

// confidenceFloor: below this, a candidate parks regardless of kind (low-confidence never promotes).
const confidenceFloor = 0.5

// Candidate is a typed, untrusted extraction. memory-kind carries entity/predicate/object; other
// kinds carry subject/statement/detail. All cite evidence and the policy version that produced them.
type Candidate struct {
	ID             string            `json:"id"`
	Kind           string            `json:"kind"`
	Entity         string            `json:"entity,omitempty"`
	Predicate      string            `json:"predicate,omitempty"`
	Object         string            `json:"object,omitempty"`
	Subject        string            `json:"subject,omitempty"`
	Statement      string            `json:"statement,omitempty"`
	Detail         string            `json:"detail,omitempty"`
	Confidence     float64           `json:"confidence"`
	Derivation     string            `json:"derivation"`
	ClaimType      string            `json:"claim_type,omitempty"`      // memory-kind: the claim taxonomy (mem.ClaimTypes)
	Qualifiers     map[string]string `json:"qualifiers,omitempty"`      // memory-kind: e.g. {"domain": "languages"} → Topic()
	PromotionBasis string            `json:"promotion_basis,omitempty"` // memory-kind: the basis that allows promotion (candidates.PromotionBases); empty = none
	EvidenceRefs   []string          `json:"evidence_refs"`
	PolicyVersion  string            `json:"policy_version"`
	Verdict        string            `json:"verdict,omitempty"` // stamped at adjudication, so a consumer respects it
}

// Promotion modes: in live_candidate_only (the DEFAULT for real traces) a memory candidate parks
// unless it carries a promotion basis — a model extraction's confidence is never a basis. The
// fixture mode promotes on confidence and is for synthetic fixtures/tests only.
const (
	ModeLiveCandidateOnly  = "live_candidate_only"
	ModeFixtureAutoPromote = "fixture_auto_promote"
)

// Policy is the VERSIONED distillation method — so a method change is legible, not silent prompt drift.
type Policy struct {
	ID            string   `json:"id"`
	Version       int      `json:"version"`
	PromotionMode string   `json:"promotion_mode"`
	Lenses        []string `json:"lenses"`
}

// DefaultPolicy is live_candidate_only: nothing model-extracted promotes to canon without a basis.
func DefaultPolicy() Policy {
	return Policy{ID: "default", Version: 1, PromotionMode: ModeLiveCandidateOnly,
		Lenses: []string{"memory", "skill", "decision", "failure", "capability", "method"}}
}

// FixturePolicy is DefaultPolicy in fixture_auto_promote mode — synthetic fixtures/tests only.
func FixturePolicy() Policy { p := DefaultPolicy(); p.PromotionMode = ModeFixtureAutoPromote; return p }

// PolicyVersionString is the stable stamp every candidate carries.
func (p Policy) Stamp() string { return fmt.Sprintf("%s.v%d", p.ID, p.Version) }

// PriorState is RetrievedPriorState: the canon the critic compares an extraction against, so it can
// tell new from duplicate from supersession/contradiction (distill against canon, not blind).
type PriorState struct {
	Current []mem.Claim
}

func LoadPriorState(store *mem.Store) (PriorState, error) {
	cur, err := store.AllCurrent()
	return PriorState{Current: cur}, err
}

// Critique is the per-candidate verdict before adjudication.
type Critique struct {
	IsNew                bool   `json:"is_new"`
	Duplicate            bool   `json:"duplicate"`
	Supersedes           string `json:"supersedes,omitempty"` // current claim id, for memory kind
	Contradicts          string `json:"contradicts,omitempty"`
	ExposesMethodFailure bool   `json:"exposes_method_failure"`
	Notes                string `json:"notes"`
}

// AdjudicationDecision is the durable verdict a candidate receives.
type AdjudicationDecision struct {
	CandidateID    string   `json:"candidate_id"`
	Kind           string   `json:"kind"`
	Verdict        string   `json:"verdict"`
	Relation       string   `json:"relation,omitempty"`        // vs prior canon: new|duplicate|supersedes|contradicts
	PriorClaimID   string   `json:"prior_claim_id,omitempty"`  // the canon claim the relation points at
	Basis          string   `json:"basis"`                     // the reason text for the verdict
	PromotionBasis string   `json:"promotion_basis,omitempty"` // the structured basis that allowed a memory promote
	EvidenceRefs   []string `json:"evidence_refs"`
	PolicyVersion  string   `json:"policy_version"`
	ClaimID        string   `json:"claim_id,omitempty"` // set when a memory candidate promotes to a claim
	CreatedAt      string   `json:"created_at"`
}

// Trace is the structured input the reference extractor consumes (the LLM extractor produces this
// shape from raw text — deferred). raw refs travel as evidence_refs on each candidate.
type Trace struct {
	TraceRef   string      `json:"trace_ref"`
	Candidates []Candidate `json:"candidates"`
}

// Extractor turns a raw trace into a Trace of candidates. Reference (deterministic) here; the LLM
// extractor (free model via the gateway) is the documented swap.
type Extractor interface {
	Extract(raw []byte, policy Policy) (Trace, error)
}

// ReferenceExtractor parses an already-structured trace and stamps stable ids + the policy version.
type ReferenceExtractor struct{}

func (ReferenceExtractor) Extract(raw []byte, policy Policy) (Trace, error) {
	var t Trace
	if err := json.Unmarshal(raw, &t); err != nil {
		return Trace{}, fmt.Errorf("parse trace: %w", err)
	}
	for i := range t.Candidates {
		c := &t.Candidates[i]
		if !contains(Kinds, c.Kind) {
			return Trace{}, fmt.Errorf("candidate %d has unknown kind %q (allowed: %v)", i, c.Kind, Kinds)
		}
		if c.Derivation == "" {
			c.Derivation = "extracted"
		}
		c.PolicyVersion = policy.Stamp()
		// Stable deterministic id over ALL identity-significant content (incl. Detail, which the
		// capability bridge keys on) ⇒ replay = same ids, and two candidates differing only in
		// Detail get DISTINCT ids (no silent inbox-file collision).
		c.ID = idFor("cand_", c.Kind, c.Entity, c.Predicate, c.Object, c.Subject, c.Statement, c.Detail, t.TraceRef)
		if len(c.EvidenceRefs) == 0 {
			c.EvidenceRefs = []string{t.TraceRef}
		}
	}
	return t, nil
}

// Critique grades a candidate against prior canon. For memory kind it detects duplicate vs
// supersession vs new; for other kinds it is new (slice-1 non-memory dedup is by id only).
func critique(c Candidate, prior PriorState) Critique {
	if c.Kind == KindEvalCase {
		return Critique{IsNew: true, ExposesMethodFailure: true, Notes: "distiller mistake → eval corpus"}
	}
	if c.Kind != KindMemory {
		return Critique{IsNew: true}
	}
	for _, cl := range prior.Current {
		if cl.Entity == c.Entity && cl.Predicate == c.Predicate {
			if cl.Object == c.Object {
				return Critique{Duplicate: true, Notes: "matches current claim " + cl.ID}
			}
			if c.Confidence < cl.Confidence {
				// a weaker candidate conflicting with stronger canon is a CONTRADICTION, not a
				// supersession — it must not silently overwrite higher-confidence current truth.
				return Critique{Contradicts: cl.ID, Notes: fmt.Sprintf("conflicts with higher-confidence %s (%.2f < %.2f)", cl.ID, c.Confidence, cl.Confidence)}
			}
			return Critique{Supersedes: cl.ID, Notes: "changes " + cl.Object + " → " + c.Object}
		}
	}
	return Critique{IsNew: true}
}

// adjudicate decides the verdict. Code decides (not the model): low confidence parks; a duplicate
// rejects; new/changed memory promotes; a distiller mistake or a capability observation
// OPERATIONALIZES (into its corpus/graph); a method upgrade or policy patch PARKS (an operator
// reviews — a method change is never auto-applied); other kinds park as candidates.
func adjudicate(c Candidate, crit Critique, mode string) (string, string) {
	if c.Confidence < confidenceFloor {
		return Park, fmt.Sprintf("confidence %.2f below floor %.2f", c.Confidence, confidenceFloor)
	}
	switch c.Kind {
	case KindMemory:
		if crit.Duplicate {
			return Reject, "duplicate of current canon"
		}
		if crit.Contradicts != "" {
			return Park, "contradicts higher-confidence canon " + crit.Contradicts + " — operator review"
		}
		// memory is evidence, not truth: in live mode a model-extracted fact PARKS unless it carries
		// a promotion basis. High confidence is not a basis — that is the whole point.
		if mode != ModeFixtureAutoPromote && !candidates.ValidBasis(c.PromotionBasis) {
			return Park, "no promotion basis (extraction confidence is not a basis) — parked for operator/basis"
		}
		return Promote, "new or superseding memory (basis: " + c.PromotionBasis + ")" // mem ledger performs the actual supersession
	case KindEvalCase:
		return Operationalize, "added to the distiller's eval corpus"
	case KindCapabilityObs:
		return Operationalize, "feeds the capability graph (missed_when/false_triggered_by)"
	case KindMethodUpgrade, KindPolicyPatch:
		return Park, "method/policy change requires operator review (never auto-applied)"
	default:
		return Park, "candidate parked for operationalization/review"
	}
}

// RunResult is the outcome of distilling one trace.
type RunResult struct {
	TraceRef  string                 `json:"trace_ref"`
	Policy    string                 `json:"policy"`
	Decisions []AdjudicationDecision `json:"decisions"`
}

// Run is the loop: load prior state → extract → critique → adjudicate → route. memory candidates
// flow through the mem ledger (which performs the real dup/supersede); all other candidates are
// written as candidate records under COMPOUND_HOME/inbox/<kind>/ and NEVER become claims here.
// Every candidate yields an AdjudicationDecision (appended to the ledger), so nothing is silent.
func Run(homePath string, store *mem.Store, raw []byte, policy Policy, ext Extractor, now time.Time) (RunResult, error) {
	res := RunResult{Policy: policy.Stamp()}
	prior, err := LoadPriorState(store)
	if err != nil {
		return res, err
	}
	t, err := ext.Extract(raw, policy)
	if err != nil {
		return res, err
	}
	res.TraceRef = t.TraceRef

	for _, c := range t.Candidates {
		crit := critique(c, prior)
		verdict, basis := adjudicate(c, crit, policy.PromotionMode)
		c.Verdict = verdict // stamp onto the written record so a consumer respects adjudication
		rel, priorClaim := "new", ""
		switch {
		case crit.Duplicate:
			rel = "duplicate"
		case crit.Contradicts != "":
			rel, priorClaim = "contradicts", crit.Contradicts
		case crit.Supersedes != "":
			rel, priorClaim = "supersedes", crit.Supersedes
		}
		dec := AdjudicationDecision{
			CandidateID: c.ID, Kind: c.Kind, Verdict: verdict, Relation: rel, PriorClaimID: priorClaim,
			Basis: basis, PromotionBasis: c.PromotionBasis, EvidenceRefs: c.EvidenceRefs,
			PolicyVersion: c.PolicyVersion, CreatedAt: rfc3339(now),
		}

		switch {
		case c.Kind == KindMemory && verdict == Promote:
			// route through the mem ledger — it is the source of truth for memory dup/supersede.
			got, err := store.Capture([]mem.Candidate{{
				Entity: c.Entity, Predicate: c.Predicate, Object: c.Object,
				Confidence: c.Confidence, Derivation: c.Derivation,
				ClaimType: c.ClaimType, Qualifiers: c.Qualifiers, // taxonomy + topic axis
				PromotionBasis: c.PromotionBasis, // persist the "why" of promotion onto the claim
				SourceTraceRef: t.TraceRef, Visibility: "operational",
			}}, now)
			if err != nil {
				return res, err
			}
			ar, err := store.Adjudicate(got[0].ID, "promote", "", now)
			if err != nil {
				return res, err
			}
			if ar.Claim != nil {
				dec.ClaimID = ar.Claim.ID
			} else {
				// mem detected a duplicate the prior-state critic missed (race / same-trace dup).
				dec.Verdict, dec.Basis = Reject, "duplicate per mem ledger"
			}
		default:
			// candidate-first: write the record to its inbox; it never becomes a claim here.
			if err := writeCandidate(homePath, c); err != nil {
				return res, err
			}
		}
		if err := appendDecision(homePath, dec); err != nil {
			return res, err
		}
		res.Decisions = append(res.Decisions, dec)
	}
	return res, nil
}

// --- persistence (under COMPOUND_HOME) ---

func writeCandidate(homePath string, c Candidate) error {
	if !contains(Kinds, c.Kind) { // belt-and-suspenders: Kind keys a path, so it must be a known typed kind
		return fmt.Errorf("refusing to write candidate with unknown kind %q", c.Kind)
	}
	rel := candidates.InboxDir(candidates.Kind(c.Kind)) // one canonical inbox per kind (no three-names drift)
	if rel == "" {
		return fmt.Errorf("candidate kind %q has no inbox mapping", c.Kind)
	}
	dir := filepath.Join(homePath, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(&c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, c.ID+".json"), append(b, '\n'), 0o644)
}

func appendDecision(homePath string, d AdjudicationDecision) error {
	dir := filepath.Join(homePath, "ledger")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "adjudications.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(&d)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func idFor(prefix string, parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return prefix + hex.EncodeToString(h[:])[:12]
}
func rfc3339(now time.Time) string { return now.UTC().Format(time.RFC3339) }

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}
