// Package work is the lifecycle store that threads ONE work_id through the whole
// chain: intent (WorkEnvelope) -> context (pack id) -> action (run ids) -> close,
// where close emits the Receipt and the apex Transition that prove the chain
// connected. State lives in COMPOUND_HOME (the runtime body), never the repo.
// See docs/architecture/transition.md and docs/architecture/contracts/README.md.
package work

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"compound/internal/envelope"
)

// transitions is the work-state machine. Terminal states (completed/failed/cancelled)
// have no outgoing edges. `claimed` is reached only via Claim (claim-before-you-touch).
var transitions = map[string][]string{
	"proposed":  {"admitted", "ready", "held", "cancelled"},
	"admitted":  {"ready", "held", "cancelled"},
	"ready":     {"claimed", "held", "cancelled"},
	"claimed":   {"running", "ready", "held", "cancelled"},
	"running":   {"verifying", "blocked", "held", "failed", "completed"},
	"verifying": {"completed", "running", "failed"},
	"blocked":   {"running", "cancelled", "failed"},
	"held":      {"ready", "cancelled"},
}

// terminalStates may not advance further; close lands a work in one of them.
var terminalStates = map[string]bool{"completed": true, "failed": true, "cancelled": true}

func canTransition(from, to string) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Store is the work lifecycle store rooted at a COMPOUND_HOME.
type Store struct{ home string }

// Open roots a store at homePath and ensures the work/event habitat dirs exist.
func Open(homePath string) (*Store, error) {
	if strings.TrimSpace(homePath) == "" {
		return nil, fmt.Errorf("no COMPOUND_HOME (run `compound home init`)")
	}
	for _, d := range []string{"work/work", "work/leases", "events"} {
		if err := os.MkdirAll(filepath.Join(homePath, d), 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{home: homePath}, nil
}

func (s *Store) workPath(id string) string  { return filepath.Join(s.home, "work/work", id+".json") }
func (s *Store) leasePath(id string) string { return filepath.Join(s.home, "work/leases", id+".json") }
func (s *Store) eventsLog() string          { return filepath.Join(s.home, "events", "work.jsonl") }
func (s *Store) envPath(id string) string   { return filepath.Join(s.home, "events", id+".json") }

// Event is one append-only lifecycle record.
type Event struct {
	TS       string   `json:"ts"`
	WorkID   string   `json:"work_id"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	By       string   `json:"by,omitempty"`
	Note     string   `json:"note,omitempty"`
	Attached []string `json:"attached,omitempty"`
}

// Lease records who holds a claimed work (claim-before-you-touch / D7).
type Lease struct {
	WorkID  string `json:"work_id"`
	By      string `json:"by"`
	ClaimAt string `json:"claimed_at"`
}

// --- create ---

type CreateInput struct {
	Owner            string
	SuccessPredicate string
	DesiredOutcome   string
	NonGoals         []string
	ContextPackID    string
	State            string // default "ready"
	Risk             string // default "tier1"
	Privacy          string // default "private"
}

// BuildWork constructs a WorkEnvelope WITHOUT persisting it — used both by Create (which
// then persists) and by callers that need a candidate (e.g. a UserPromptSubmit hook).
func BuildWork(in CreateInput, now time.Time) envelope.WorkEnvelope {
	return envelope.WorkEnvelope{
		Header:             newHeader(envelope.SVWork, idFor("work_", in.Owner, in.SuccessPredicate, ts(now)), now, "", in.ContextPackID, orDefault(in.Privacy, "private"), orDefault(in.Risk, "tier1")),
		Owner:              in.Owner,
		State:              orDefault(in.State, "ready"),
		DesiredOutcome:     in.DesiredOutcome,
		SuccessPredicate:   in.SuccessPredicate,
		NonGoals:           orEmpty(in.NonGoals),
		RunIDs:             []string{},
		ArtifactIDs:        []string{},
		VerificationIDs:    []string{},
		MemoryCandidateIDs: []string{},
	}
}

// Create writes a new WorkEnvelope and its first lifecycle event, returning the work.
func (s *Store) Create(in CreateInput, now time.Time) (envelope.WorkEnvelope, error) {
	w := BuildWork(in, now)
	state := w.State
	if err := w.Validate(); err != nil {
		return w, err
	}
	if err := s.writeJSON(s.workPath(w.ID), &w); err != nil {
		return w, err
	}
	attached := []string(nil)
	if in.ContextPackID != "" {
		attached = []string{in.ContextPackID}
	}
	if err := s.appendEvent(Event{TS: ts(now), WorkID: w.ID, From: "", To: state, Note: "created", Attached: attached}); err != nil {
		return w, err
	}
	return w, nil
}

// --- show / list ---

func (s *Store) Get(workID string) (envelope.WorkEnvelope, error) {
	var w envelope.WorkEnvelope
	err := s.readJSON(s.workPath(workID), &w)
	return w, err
}

// Show returns a work and its lifecycle log (oldest first).
func (s *Store) Show(workID string) (envelope.WorkEnvelope, []Event, error) {
	w, err := s.Get(workID)
	if err != nil {
		return w, nil, err
	}
	evs, err := s.eventsFor(workID)
	return w, evs, err
}

func (s *Store) List() ([]envelope.WorkEnvelope, error) {
	dir := filepath.Join(s.home, "work/work")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []envelope.WorkEnvelope
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var w envelope.WorkEnvelope
		if s.readJSON(filepath.Join(dir, e.Name()), &w) == nil {
			out = append(out, w)
		}
	}
	return out, nil
}

// --- claim ---

// Claim moves a ready work to claimed and writes a lease. A second claimant is rejected
// (claim-before-you-touch prevents parallel agents colliding on the same surface).
func (s *Store) Claim(workID, by string, now time.Time) (envelope.WorkEnvelope, error) {
	w, err := s.Get(workID)
	if err != nil {
		return w, err
	}
	if strings.TrimSpace(by) == "" {
		return w, fmt.Errorf("claim requires --by <actor>")
	}
	if w.State == "claimed" {
		if l, err := s.readLease(workID); err == nil && l.By != by {
			return w, fmt.Errorf("work %s already claimed by %q", workID, l.By)
		}
		return w, nil // idempotent re-claim by same actor
	}
	if !canTransition(w.State, "claimed") {
		return w, fmt.Errorf("cannot claim from state %q (must be ready)", w.State)
	}
	from := w.State
	w.State = "claimed"
	w.Authority.Owner = by
	if err := s.writeJSON(s.workPath(workID), &w); err != nil {
		return w, err
	}
	if err := s.writeJSON(s.leasePath(workID), &Lease{WorkID: workID, By: by, ClaimAt: ts(now)}); err != nil {
		return w, err
	}
	return w, s.appendEvent(Event{TS: ts(now), WorkID: workID, From: from, To: "claimed", By: by, Note: "claimed"})
}

// --- advance ---

type AdvanceInput struct {
	To            string
	RunID         string
	ArtifactID    string
	VerificationID string
	ContextPackID string
	Note          string
}

// Advance moves a work along a legal edge of the state machine, optionally attaching
// linked envelope ids (runs = executed actions, artifacts = outputs).
func (s *Store) Advance(workID string, in AdvanceInput, now time.Time) (envelope.WorkEnvelope, error) {
	w, err := s.Get(workID)
	if err != nil {
		return w, err
	}
	if terminalStates[w.State] {
		return w, fmt.Errorf("work %s is %s (terminal); cannot advance", workID, w.State)
	}
	if in.To == "" {
		return w, fmt.Errorf("advance requires --to <state>")
	}
	if in.To != w.State && !canTransition(w.State, in.To) {
		return w, fmt.Errorf("illegal transition %s -> %s (allowed: %s)", w.State, in.To, strings.Join(transitions[w.State], ", "))
	}
	from := w.State
	w.State = in.To
	var attached []string
	if in.RunID != "" {
		w.RunIDs = append(w.RunIDs, in.RunID)
		attached = append(attached, in.RunID)
	}
	if in.ArtifactID != "" {
		w.ArtifactIDs = append(w.ArtifactIDs, in.ArtifactID)
		attached = append(attached, in.ArtifactID)
	}
	if in.VerificationID != "" {
		w.VerificationIDs = append(w.VerificationIDs, in.VerificationID)
		attached = append(attached, in.VerificationID)
	}
	if in.ContextPackID != "" {
		w.IDs.ContextPackID = in.ContextPackID
		attached = append(attached, in.ContextPackID)
	}
	if err := s.writeJSON(s.workPath(workID), &w); err != nil {
		return w, err
	}
	return w, s.appendEvent(Event{TS: ts(now), WorkID: workID, From: from, To: in.To, Note: in.Note, Attached: attached})
}

// --- act (governed mutation) ---

// RecordAction records a performed ActionIntent against a work — but ONLY if its
// PolicyDecision allows it. This is the enforcement seam: a mutating adapter cannot
// record an ungoverned mutation, so no model bypasses policy by prompt obedience.
// (Decoupled from the policy package on purpose: the store enforces on the decision's
// own outcome, so the gate holds even if a caller skips the evaluator.)
func (s *Store) RecordAction(workID string, ai envelope.ActionIntent, pd envelope.PolicyDecision, now time.Time) (envelope.WorkEnvelope, error) {
	w, err := s.Get(workID)
	if err != nil {
		return w, err
	}
	if terminalStates[w.State] {
		return w, fmt.Errorf("work %s is %s (terminal); cannot record an action", workID, w.State)
	}
	if ai.IDs.WorkID != workID {
		return w, fmt.Errorf("action work_id %q does not match work %q", ai.IDs.WorkID, workID)
	}
	if pd.ActionIntentID != ai.ID {
		return w, fmt.Errorf("policy decision is for action %q, not %q", pd.ActionIntentID, ai.ID)
	}
	if err := ai.Validate(); err != nil {
		return w, fmt.Errorf("action intent invalid: %w", err)
	}
	if err := pd.Validate(); err != nil {
		return w, fmt.Errorf("policy decision invalid: %w", err)
	}
	// THE GATE: refuse to record a performed action unless policy allows it.
	if pd.Outcome != "allow" && pd.Outcome != "allow_with_obligations" {
		return w, fmt.Errorf("cannot record action %s: policy %s (a mutating adapter requires an allowing PolicyDecision)", ai.ID, pd.Outcome)
	}
	if err := s.writeJSON(s.envPath(ai.ID), &ai); err != nil {
		return w, err
	}
	if err := s.writeJSON(s.envPath(pd.ID), &pd); err != nil {
		return w, err
	}
	w.RunIDs = append(w.RunIDs, ai.ID) // the performed action is an action ref in the chain
	w.PolicyDecisionID = pd.ID
	if err := s.writeJSON(s.workPath(workID), &w); err != nil {
		return w, err
	}
	return w, s.appendEvent(Event{TS: ts(now), WorkID: workID, From: w.State, To: w.State, Note: "action recorded: " + ai.Action + " (policy " + pd.Outcome + ")", Attached: []string{ai.ID, pd.ID}})
}

// --- close ---

type CloseInput struct {
	Outcome  string // completed | failed | cancelled
	MoveType string // outward_delta | memory_delta | rule_delta | control_plane_repair | audit_only
	Summary  string
	Changed  bool
}

// CloseResult is the connected chain proof: the final work plus the Receipt and the
// apex Transition emitted at close, all carrying the same work_id.
type CloseResult struct {
	Work       envelope.WorkEnvelope `json:"work"`
	Receipt    envelope.Receipt      `json:"receipt"`
	Transition envelope.Transition   `json:"transition"`
}

var receiptOutcome = map[string]string{"completed": "completed", "failed": "failed", "cancelled": "no_op"}

// Close lands a work in a terminal state and emits the Receipt + Transition that tie the
// whole chain (context -> runs -> outputs) to this work_id.
func (s *Store) Close(workID string, in CloseInput, now time.Time) (CloseResult, error) {
	var res CloseResult
	w, err := s.Get(workID)
	if err != nil {
		return res, err
	}
	if !terminalStates[in.Outcome] {
		return res, fmt.Errorf("close --outcome must be completed|failed|cancelled, got %q", in.Outcome)
	}
	if w.State != in.Outcome && !canTransition(w.State, in.Outcome) {
		return res, fmt.Errorf("cannot close %s from state %q to %q", workID, w.State, in.Outcome)
	}
	if !oneOf(in.MoveType, envelope.MoveTypes) {
		return res, fmt.Errorf("close --move-type must be one of %v, got %q", envelope.MoveTypes, in.MoveType)
	}

	// Receipt — how the run is learned from.
	rOut := receiptOutcome[in.Outcome]
	rec := envelope.Receipt{
		Header:   newHeader(envelope.SVReceipt, idFor("rec_", workID, ts(now)), now, workID, w.IDs.ContextPackID, w.Privacy, w.Risk),
		Outcome:  rOut,
		MoveType: in.MoveType,
		StateDelta: envelope.StateDelta{
			Changed:      in.Changed,
			Summary:      in.Summary,
			FilesChanged: []string{},
			Artifacts:    orEmpty(w.ArtifactIDs),
			Verification: orEmpty(w.VerificationIDs),
		},
		Cost:   envelope.ReceiptCost{ModelCalls: []string{}},
		Skills: envelope.ReceiptSkills{Used: []string{}, Missed: []string{}, Candidate: []string{}},
		Memory: envelope.ReceiptMemory{Candidates: orEmpty(w.MemoryCandidateIDs), Promoted: []string{}, Parked: []string{}},
		Next:   envelope.ReceiptNext{SuggestedWork: []string{}},
	}
	if err := rec.Validate(); err != nil {
		return res, fmt.Errorf("emitted receipt invalid: %w", err)
	}

	// Transition — the apex object: what changed across spaces, tied to this work_id.
	before := []string{}
	if w.IDs.ContextPackID != "" {
		before = []string{w.IDs.ContextPackID}
	}
	after := append(append([]string{}, orEmpty(w.ArtifactIDs)...), rec.ID)
	tr := envelope.Transition{
		Header:     newHeader(envelope.SVTransition, idFor("tr_", workID, ts(now)), now, workID, w.IDs.ContextPackID, w.Privacy, w.Risk),
		BeforeRefs: before,
		ActionRefs: orEmpty(w.RunIDs),
		AfterRefs:  after,
		StateDelta: in.Summary,
		Projections: envelope.Projections{
			Memory: []string{}, Skill: []string{}, Product: []string{}, Cost: []string{},
			Risk: []string{}, Taste: []string{}, Routing: []string{}, HumanAttention: []string{},
		},
		SecondOrder: envelope.SecondOrder{FutureBehaviorChanged: in.MoveType != "audit_only"},
	}
	if err := tr.Validate(); err != nil {
		return res, fmt.Errorf("emitted transition invalid: %w", err)
	}

	// Persist the emitted envelopes, then land the work terminal and link the transition.
	if err := s.writeJSON(s.envPath(rec.ID), &rec); err != nil {
		return res, err
	}
	if err := s.writeJSON(s.envPath(tr.ID), &tr); err != nil {
		return res, err
	}
	from := w.State
	w.State = in.Outcome
	w.IDs.TransitionID = tr.ID
	if err := s.writeJSON(s.workPath(workID), &w); err != nil {
		return res, err
	}
	if err := s.appendEvent(Event{TS: ts(now), WorkID: workID, From: from, To: in.Outcome, Note: "closed: " + in.MoveType, Attached: []string{rec.ID, tr.ID}}); err != nil {
		return res, err
	}
	return CloseResult{Work: w, Receipt: rec, Transition: tr}, nil
}

// --- persistence helpers ---

func (s *Store) writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func (s *Store) readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("not found: %s", filepath.Base(path))
		}
		return err
	}
	return json.Unmarshal(b, v)
}

func (s *Store) readLease(workID string) (Lease, error) {
	var l Lease
	err := s.readJSON(s.leasePath(workID), &l)
	return l, err
}

func (s *Store) appendEvent(e Event) error {
	f, err := os.OpenFile(s.eventsLog(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func (s *Store) eventsFor(workID string) ([]Event, error) {
	b, err := os.ReadFile(s.eventsLog())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Event
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var e Event
		if json.Unmarshal([]byte(line), &e) == nil && e.WorkID == workID {
			out = append(out, e)
		}
	}
	return out, nil
}

// --- small helpers ---

func newHeader(sv, id string, now time.Time, workID, contextPackID, privacy, risk string) envelope.Header {
	return envelope.Header{
		SchemaVersion: sv,
		ID:            id,
		CreatedAt:     now.UTC().Format(time.RFC3339),
		Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
		IDs:           envelope.IDs{WorkID: workID, ContextPackID: contextPackID},
		Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
		Privacy:       orDefault(privacy, "private"),
		Risk:          orDefault(risk, "tier1"),
		EvidenceRefs:  []string{},
		Uncertainty:   envelope.Uncertainty{Level: "known"},
	}
}

func idFor(prefix string, parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return prefix + hex.EncodeToString(h[:])[:12]
}

func ts(now time.Time) string { return strconv.FormatInt(now.UTC().UnixNano(), 10) }

func oneOf(v string, set []string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
