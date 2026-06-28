// Package mem is P6: memory re-bound through the spine. It is the OWNED ledger — the
// authority — built on the patterns the external-systems audit converged on:
//   - MVCC-by-fold: an append-only, immutable claim log + a monotonic seq; "current" and
//     "as of T" are DERIVED by folding, never by mutating a record. Claims are never deleted.
//   - candidate-first: extracted facts land as untrusted Candidates and are adjudicated
//     reject | park | promote by deterministic code (the LLM only proposes).
//   - contradiction over delete: a promotion that changes a fact SUPERSEDES the prior claim
//     via an edge (the version chain); the old claim stays as history.
//   - provenance everywhere: every claim carries how it was derived + its evidence refs.
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
	"time"

	"compound/internal/envelope"
)

// Derivation tags how a claim/candidate was produced (provenance). ambiguous auto-surfaces.
var Derivations = []string{"deterministic", "extracted", "inferred", "ambiguous"}

// Verdicts are the adjudication outcomes (candidate-first).
var Verdicts = []string{"reject", "park", "promote"}

// Visibilities are the temperature tiers. retired is hidden from default recall but kept.
var Visibilities = []string{"ambient", "contextual", "candidate", "private", "operational", "retired"}

// Candidate is an untrusted, proposed fact awaiting adjudication (lives in the inbox).
type Candidate struct {
	ID              string  `json:"id"`
	Entity          string  `json:"entity"`
	Predicate       string  `json:"predicate"`
	Object          string  `json:"object"`
	ObservationDate string  `json:"observation_date"` // temporal grounding (when the fact was true)
	Confidence      float64 `json:"confidence"`
	Derivation      string  `json:"derivation"`
	SourceTraceRef  string  `json:"source_trace_ref"` // provenance: where it came from
	SnippetHash     string  `json:"snippet_hash"`     // bounded snippet, not the raw body
	Visibility      string  `json:"visibility"`
	State           string  `json:"state"` // proposed | parked | promoted | rejected
	Reason          string  `json:"reason,omitempty"`
	CreatedAt       string  `json:"created_at"`
}

// Claim is a durable, IMMUTABLE assertion in the ledger. It is never mutated or deleted;
// supersession and validity are derived from the seq order + supersedes edges (MVCC-by-fold).
type Claim struct {
	ID           string   `json:"id"`
	Seq          int64    `json:"seq"`        // monotonic version clock
	Entity       string   `json:"entity"`
	Predicate    string   `json:"predicate"`
	Object       string   `json:"object"`
	ValidFrom    string   `json:"valid_from"` // observation date (immutable)
	Confidence   float64  `json:"confidence"`
	Derivation   string   `json:"derivation"`    // provenance: how derived
	EvidenceRefs []string `json:"evidence_refs"` // provenance: source candidate/trace
	Supersedes   string   `json:"supersedes,omitempty"`
	Visibility   string   `json:"visibility"`
	CreatedAt    string   `json:"created_at"`
}

// Edge is an append-only relation between claims. Its seq is when the relation took effect,
// so "current as of T" can fold supersession deterministically.
type Edge struct {
	Seq        int64   `json:"seq"`
	Kind       string  `json:"kind"` // supersedes | contradicts | relates
	From       string  `json:"from"`
	To         string  `json:"to"`
	Confidence float64 `json:"confidence"`
	CreatedAt  string  `json:"created_at"`
}

// Store is the memory ledger rooted at a COMPOUND_HOME.
type Store struct{ home string }

func Open(homePath string) (*Store, error) {
	if strings.TrimSpace(homePath) == "" {
		return nil, fmt.Errorf("no COMPOUND_HOME (run `compound home init`)")
	}
	for _, d := range []string{"inbox/memory-candidates", "ledger", "events"} {
		if err := os.MkdirAll(filepath.Join(homePath, d), 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{home: homePath}, nil
}

func (s *Store) candDir() string    { return filepath.Join(s.home, "inbox", "memory-candidates") }
func (s *Store) claimsLog() string  { return filepath.Join(s.home, "ledger", "claims.jsonl") }
func (s *Store) edgesLog() string   { return filepath.Join(s.home, "ledger", "edges.jsonl") }
func (s *Store) seqFile() string    { return filepath.Join(s.home, "ledger", "seq") }
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
		if c.Visibility == "" {
			c.Visibility = "operational"
		}
		c.ID = idFor("cand_", c.Entity, c.Predicate, c.Object, ts(now))
		c.State = "proposed"
		c.CreatedAt = rfc3339(now)
		if err := writeJSON(filepath.Join(s.candDir(), c.ID+".json"), &c); err != nil {
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
		c.State = map[string]string{"reject": "rejected", "park": "parked"}[verdict]
		if err := writeJSON(filepath.Join(s.candDir(), c.ID+".json"), &c); err != nil {
			return res, err
		}
		res.Candidate = c
		return res, nil
	}

	// promote: find the prior CURRENT claim on this entity+predicate.
	prior, hasPrior, err := s.currentClaim(c.Entity, c.Predicate, 0)
	if err != nil {
		return res, err
	}
	if hasPrior && prior.Object == c.Object {
		// same value already current → duplicate; do not append a redundant version.
		c.State = "rejected"
		c.Reason = "duplicate of current claim " + prior.ID
		_ = writeJSON(filepath.Join(s.candDir(), c.ID+".json"), &c)
		res.Verdict = "reject"
		res.Candidate = c
		res.Note = "already current: " + prior.ID
		return res, nil
	}

	seq, err := s.nextSeq()
	if err != nil {
		return res, err
	}
	claim := Claim{
		ID:           idFor("claim_", c.Entity, c.Predicate, c.Object, strconv.FormatInt(seq, 10)),
		Seq:          seq,
		Entity:       c.Entity,
		Predicate:    c.Predicate,
		Object:       c.Object,
		ValidFrom:    orDef(c.ObservationDate, rfc3339(now)),
		Confidence:   c.Confidence,
		Derivation:   c.Derivation,
		EvidenceRefs: []string{c.ID, c.SourceTraceRef},
		Visibility:   orDef(c.Visibility, "operational"),
		CreatedAt:    rfc3339(now),
	}
	if hasPrior {
		claim.Supersedes = prior.ID
	}
	if err := appendJSONL(s.claimsLog(), &claim); err != nil {
		return res, err
	}
	if hasPrior {
		if err := appendJSONL(s.edgesLog(), &Edge{Seq: seq, Kind: "supersedes", From: claim.ID, To: prior.ID, Confidence: c.Confidence, CreatedAt: rfc3339(now)}); err != nil {
			return res, err
		}
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
	if err := writeJSON(filepath.Join(s.candDir(), c.ID+".json"), &c); err != nil {
		return res, err
	}
	res.Candidate, res.Claim, res.Transition = c, &claim, &tr
	return res, nil
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
		BeforeRefs:  before,
		ActionRefs:  []string{},
		AfterRefs:   []string{claim.ID},
		StateDelta:  delta,
		Projections: envelope.Projections{Memory: []string{claim.ID}, Skill: []string{}, Product: []string{}, Cost: []string{}, Risk: []string{}, Taste: []string{}, Routing: []string{}, HumanAttention: []string{}},
		SecondOrder: envelope.SecondOrder{FutureBehaviorChanged: true},
	}
}

// --- recall (relevance-gated, as-of-aware fold) ---

type RecallOpts struct {
	AsOfSeq        int64    // 0 = now (latest)
	IncludeRetired bool     // default false
	MinConfidence  float64  // hard floor
	Visibilities   []string // allowed tiers; empty = all (minus retired unless IncludeRetired)
}

type RecallResult struct {
	Entity   string  `json:"entity"`
	AsOfSeq  int64   `json:"as_of_seq"`
	Claims   []Claim `json:"claims"`
	Filtered int     `json:"filtered"`
	Coverage string  `json:"coverage"`
}

// Recall returns the current claims for an entity AS OF a seq, after HARD filters
// (visibility/retired/confidence) run BEFORE the fold — never a post-filter. It returns a
// coverage verdict and never fabricates: an empty result says so plainly.
func (s *Store) Recall(entity string, opts RecallOpts) (RecallResult, error) {
	res := RecallResult{Entity: entity}
	claims, err := s.loadClaims()
	if err != nil {
		return res, err
	}
	asOf := opts.AsOfSeq
	if asOf == 0 {
		asOf = s.maxSeq(claims)
	}
	res.AsOfSeq = asOf

	// 1. HARD gates first (scope/time/visibility/confidence).
	var gated []Claim
	for _, c := range claims {
		if c.Entity != entity || c.Seq > asOf {
			continue
		}
		if !opts.IncludeRetired && c.Visibility == "retired" {
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

	switch {
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
func (s *Store) Timeline(entity string) ([]Claim, error) {
	claims, err := s.loadClaims()
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

// currentClaim returns the current claim for entity+predicate as of asOf (0 = now), if any.
func (s *Store) currentClaim(entity, predicate string, asOf int64) (Claim, bool, error) {
	claims, err := s.loadClaims()
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

// --- doctor (which hop) ---

type Hop struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | empty | skipped | fail
	Detail string `json:"detail"`
}
type Report struct {
	Hops []Hop  `json:"hops"`
	OK   bool   `json:"ok"`
	Note string `json:"note"`
}

// Doctor reports WHICH hop is healthy or failed, not just "missing" — the fix differs per hop.
func (s *Store) Doctor() Report {
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
	add("sync/embedding", "skipped", "no engine wired (ledger answers exact/as-of without one)")

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

// --- ledger internals (MVCC-by-fold) ---

func (s *Store) nextSeq() (int64, error) {
	cur := int64(0)
	if b, err := os.ReadFile(s.seqFile()); err == nil {
		cur, _ = strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	}
	cur++
	return cur, os.WriteFile(s.seqFile(), []byte(strconv.FormatInt(cur, 10)), 0o644)
}

func (s *Store) loadClaims() ([]Claim, error) {
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

func (s *Store) supersededAsOf(asOf int64) (map[string]bool, error) {
	out := map[string]bool{}
	err := readJSONL(s.edgesLog(), func(line []byte) error {
		var e Edge
		if err := json.Unmarshal(line, &e); err != nil {
			return err
		}
		if e.Kind == "supersedes" && e.Seq <= asOf {
			out[e.To] = true
		}
		return nil
	})
	return out, err
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
func shortHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:])[:12] }
func ts(now time.Time) string   { return strconv.FormatInt(now.UTC().UnixNano(), 10) }
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
