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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/home"
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
	WorkID           string
	Owner            string
	Actor            string
	CanonicalSurface string
	TargetSurface    string
	SuccessPredicate string
	DesiredOutcome   string
	NonGoals         []string
	EvidenceRefs     []string
	ContextPackID    string
	RouteDecisionID  string
	State            string // default "ready"
	Risk             string // default "tier1"
	Privacy          string // default "private"
}

// BuildWork constructs a WorkEnvelope WITHOUT persisting it — used both by Create (which
// then persists) and by callers that need a candidate (e.g. a UserPromptSubmit hook).
func BuildWork(in CreateInput, now time.Time) envelope.WorkEnvelope {
	workID := strings.TrimSpace(in.WorkID)
	if workID == "" {
		workID = idFor("work_", in.Owner, in.SuccessPredicate, ts(now))
	}
	w := envelope.WorkEnvelope{
		Header:             newHeader(envelope.SVWork, workID, now, "", in.ContextPackID, orDefault(in.Privacy, "private"), orDefault(in.Risk, "tier1")),
		Owner:              in.Owner,
		CanonicalSurface:   strings.TrimSpace(in.CanonicalSurface),
		TargetSurface:      strings.TrimSpace(in.TargetSurface),
		State:              orDefault(in.State, "ready"),
		DesiredOutcome:     in.DesiredOutcome,
		SuccessPredicate:   in.SuccessPredicate,
		NonGoals:           orEmpty(in.NonGoals),
		RouteDecisionID:    strings.TrimSpace(in.RouteDecisionID),
		RunIDs:             []string{},
		ArtifactIDs:        []string{},
		VerificationIDs:    []string{},
		MemoryCandidateIDs: []string{},
	}
	w.EvidenceRefs = orEmpty(in.EvidenceRefs)
	return w
}

// Create writes a new WorkEnvelope and its first lifecycle event only after the
// admission ActionIntent is allowed by policy.
func (s *Store) Create(in CreateInput, now time.Time) (envelope.WorkEnvelope, error) {
	plan, err := s.CreatePlan(in, now, CreatePlanOptions{Actor: in.Actor})
	if err != nil {
		return plan.Work, err
	}
	attached := []string(nil)
	if in.ContextPackID != "" {
		attached = append(attached, in.ContextPackID)
	}
	return s.admitWork(plan, now, "created", attached)
}

func (s *Store) validateContextPackRef(contextPackID string) error {
	contextPackID = strings.TrimSpace(contextPackID)
	if contextPackID == "" {
		return nil
	}
	var pack envelope.ContextPack
	path := filepath.Join(s.home, "context", "packs", contextPackID+".json")
	if err := s.readJSON(path, &pack); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("context_pack_id %q does not resolve to %s", contextPackID, filepath.ToSlash(filepath.Join("context", "packs", contextPackID+".json")))
		}
		return fmt.Errorf("read context_pack_id %q: %w", contextPackID, err)
	}
	if pack.ID != contextPackID {
		return fmt.Errorf("context_pack_id %q does not match artifact id %q", contextPackID, pack.ID)
	}
	if err := pack.Validate(); err != nil {
		return fmt.Errorf("context_pack_id %q invalid: %w", contextPackID, err)
	}
	if err := s.validateContextPackContractRef(contextPackID, pack.IDs.ContractID); err != nil {
		return err
	}
	return nil
}

func (s *Store) validateContextPackContractRef(contextPackID, contractID string) error {
	contractID = strings.TrimSpace(contractID)
	if contractID == "" {
		return nil
	}
	pattern := filepath.Join(s.home, "contracts", "effective", "*", contractID+".json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("context_pack_id %q contract_id %q lookup failed: %w", contextPackID, contractID, err)
	}
	if len(matches) == 0 {
		return fmt.Errorf("context_pack_id %q references contract_id %q but no %s artifact exists", contextPackID, contractID, filepath.ToSlash(filepath.Join("contracts", "effective", "*", contractID+".json")))
	}
	sort.Strings(matches)
	var ec envelope.EffectiveContract
	if err := s.readJSON(matches[len(matches)-1], &ec); err != nil {
		return fmt.Errorf("read context_pack_id %q contract_id %q: %w", contextPackID, contractID, err)
	}
	if ec.ID != contractID {
		return fmt.Errorf("context_pack_id %q contract_id %q does not match artifact id %q", contextPackID, contractID, ec.ID)
	}
	if err := ec.Validate(); err != nil {
		return fmt.Errorf("context_pack_id %q contract_id %q invalid: %w", contextPackID, contractID, err)
	}
	return nil
}

// --- show / list ---

func (s *Store) Get(workID string) (envelope.WorkEnvelope, error) {
	var w envelope.WorkEnvelope
	err := s.readJSON(s.workPath(workID), &w)
	if errors.Is(err, os.ErrNotExist) {
		if archivedPath, ok := s.archivedWorkPath(workID); ok {
			err = s.readJSON(archivedPath, &w)
		}
	}
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
	return s.list(false)
}

func (s *Store) ListWithArchived() ([]envelope.WorkEnvelope, error) {
	return s.list(true)
}

func (s *Store) list(includeArchived bool) ([]envelope.WorkEnvelope, error) {
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
	if includeArchived {
		archived, err := s.archivedWorks()
		if err != nil {
			return nil, err
		}
		out = append(out, archived...)
	}
	return out, nil
}

func (s *Store) archivedWorks() ([]envelope.WorkEnvelope, error) {
	entries, err := home.LoadOperationalArchiveIndex(s.home)
	if err != nil {
		return nil, err
	}
	var out []envelope.WorkEnvelope
	for _, entry := range entries {
		if entry.Kind != "work" || !strings.HasPrefix(entry.OriginalPath, "work/work/") || !strings.HasSuffix(entry.OriginalPath, ".json") {
			continue
		}
		var w envelope.WorkEnvelope
		if err := s.readJSON(filepath.Join(s.home, filepath.FromSlash(entry.ArchivePath)), &w); err == nil {
			out = append(out, w)
		}
	}
	return out, nil
}

func (s *Store) archivedWorkPath(workID string) (string, bool) {
	entries, err := home.LoadOperationalArchiveIndex(s.home)
	if err != nil {
		return "", false
	}
	want := filepath.ToSlash(filepath.Join("work", "work", workID+".json"))
	for _, entry := range entries {
		if entry.Kind == "work" && entry.OriginalPath == want {
			return filepath.Join(s.home, filepath.FromSlash(entry.ArchivePath)), true
		}
	}
	return "", false
}

// --- claim ---

// Claim moves a ready work to claimed only after policy allows the claim
// ActionIntent, then writes the claim envelopes and lease. A second claimant is
// rejected (claim-before-you-touch prevents parallel agents colliding on the
// same surface).
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
	plan, err := s.ClaimPlan(workID, now, ClaimPlanOptions{Actor: by})
	if err != nil {
		return w, err
	}
	if !plan.Policy.Allowed {
		return w, fmt.Errorf("cannot claim work %s: policy %s (decision %s)", workID, plan.Policy.Outcome, plan.Policy.PolicyDecisionID)
	}
	from := w.State
	w.State = "claimed"
	w.Authority.Owner = by
	w.RunIDs = append(w.RunIDs, plan.ActionIntent.ID)
	w.PolicyDecisionID = plan.Decision.ID
	if err := s.writeJSON(s.envPath(plan.ActionIntent.ID), &plan.ActionIntent); err != nil {
		return w, err
	}
	if err := s.writeJSON(s.envPath(plan.Decision.ID), &plan.Decision); err != nil {
		return w, err
	}
	if err := s.writeJSON(s.workPath(workID), &w); err != nil {
		return w, err
	}
	if err := s.writeJSON(s.leasePath(workID), &Lease{WorkID: workID, By: by, ClaimAt: ts(now)}); err != nil {
		return w, err
	}
	return w, s.appendEvent(Event{
		TS:       ts(now),
		WorkID:   workID,
		From:     from,
		To:       "claimed",
		By:       by,
		Note:     "claimed (policy " + plan.Policy.Outcome + ")",
		Attached: []string{plan.ActionIntent.ID, plan.Decision.ID},
	})
}

// --- advance ---

type AdvanceInput struct {
	To             string
	Actor          string
	RunID          string
	ArtifactID     string
	VerificationID string
	ContextPackID  string
	// EvidenceRefs are immutable proof attachments for a specialized admitted
	// transition (currently evidence-threshold autonomy). Generic CLI advances
	// leave this empty. applyAdvance persists them on both the WorkEnvelope and
	// the exact lifecycle event so replay does not depend on a mutable sidecar.
	EvidenceRefs []string
	Note         string
	// Steal and StealReason mirror CloseInput's lease escape hatch (see
	// leaseHolderAdvanceGate in lease_advance.go): advancing a work held by a different
	// actor is refused unless Steal is true AND StealReason is non-empty.
	Steal       bool
	StealReason string
}

// Advance moves a work along a legal edge of the state machine, optionally attaching
// linked envelope ids (runs = executed actions, artifacts = outputs).
func (s *Store) Advance(workID string, in AdvanceInput, now time.Time) (envelope.WorkEnvelope, error) {
	var out envelope.WorkEnvelope
	var advanceErr error
	err := home.WithAutonomyAdmissionLock(s.home, func() error {
		out, advanceErr = s.advanceLocked(workID, in, now)
		return advanceErr
	})
	return out, err
}

func (s *Store) advanceLocked(workID string, in AdvanceInput, now time.Time) (envelope.WorkEnvelope, error) {
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
	actor := strings.TrimSpace(in.Actor)
	if actor == "" {
		actor = "codex"
	}
	// Kill-switch (W9-J4), defense-in-depth: an autonomous (non-operator) admission of a
	// director-proposed work out of `proposed` is denied while the policy-owned autonomy
	// halt is engaged, even through this generic seam. Manual operator admit, normal
	// claim, and normal advance never enter this branch; when the halt is unset it is a
	// no-op, so behavior is unchanged.
	if err := s.autonomyHaltGuard(w, in.To, actor, now); err != nil {
		return w, err
	}
	// Editable-surfaces (EVOLVE-BLOCK), defense-in-depth: a system-originated admission of
	// a director-proposed work that declares a target_surface is denied through this generic
	// seam too, unless that surface is actively whitelisted. The halt guard above runs first,
	// so a halt wins; work without a target_surface is unaffected.
	if err := s.editableSurfaceGuard(w, in.To, actor, now); err != nil {
		return w, err
	}
	// Freeze (grants-as-gates, 2026-07-10), defense-in-depth: a system-originated
	// admission matching an active freeze decision record is denied through this
	// generic seam too, mirroring the halt/editable-surface guards above.
	// Operator-provenance advances are excluded by isAutonomousAdmission the same
	// way (Law 6: the operator can always act; the system cannot ignore its own
	// freeze).
	if err := s.freezeGuard(w, in.To, actor, now); err != nil {
		return w, err
	}
	if err := heldReleaseOperatorError(w.State, in.To, actor); err != nil {
		return w, err
	}
	if err := directorProposedReleaseOperatorError(w, in.To, actor); err != nil {
		return w, err
	}
	if err := s.leaseHolderAdvanceGate(w, actor, in, now); err != nil {
		return w, err
	}
	return s.applyAdvance(w, in, now)
}

// applyAdvance performs the state write for a legal, gate-cleared advance. It is shared
// by Advance (after its provenance/halt guards) and AdmitAuto (after its kill-switch and
// eligibility gates) so both mutate through one audited path.
func (s *Store) applyAdvance(w envelope.WorkEnvelope, in AdvanceInput, now time.Time) (envelope.WorkEnvelope, error) {
	workID := w.ID
	plan, err := s.AdvancePlan(workID, now, AdvancePlanOptions{Actor: in.Actor, To: in.To})
	if err != nil {
		return w, err
	}
	if !plan.Policy.Allowed {
		return w, fmt.Errorf("cannot advance work %s: policy %s (decision %s)", workID, plan.Policy.Outcome, plan.Policy.PolicyDecisionID)
	}
	if err := s.validateContextPackRef(in.ContextPackID); err != nil {
		return w, err
	}
	from := w.State
	w.State = in.To
	w.RunIDs = append(w.RunIDs, plan.ActionIntent.ID)
	w.PolicyDecisionID = plan.Decision.ID
	attached := []string{plan.ActionIntent.ID, plan.Decision.ID}
	if len(in.EvidenceRefs) > 0 {
		w.EvidenceRefs = mergeUnique(w.EvidenceRefs, in.EvidenceRefs)
		attached = append(attached, in.EvidenceRefs...)
	}
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
	if err := s.writeJSON(s.envPath(plan.ActionIntent.ID), &plan.ActionIntent); err != nil {
		return w, err
	}
	if err := s.writeJSON(s.envPath(plan.Decision.ID), &plan.Decision); err != nil {
		return w, err
	}
	if err := s.writeJSON(s.workPath(workID), &w); err != nil {
		return w, err
	}
	note := strings.TrimSpace(in.Note)
	if note == "" {
		note = "advanced (policy " + plan.Policy.Outcome + ")"
	} else {
		note += " (policy " + plan.Policy.Outcome + ")"
	}
	return w, s.appendEvent(Event{TS: ts(now), WorkID: workID, From: from, To: in.To, Note: note, Attached: attached})
}

// --- act (governed mutation) ---

// RecordAction records a performed ActionIntent against a work — but ONLY if its
// PolicyDecision allows it. This is the enforcement seam: a mutating adapter cannot
// record an ungoverned mutation, so no model bypasses policy by prompt obedience.
// (Decoupled from the policy package on purpose: the store enforces on the decision's
// own outcome, so the gate holds even if a caller skips the evaluator.)
func (s *Store) RecordAction(workID string, ai envelope.ActionIntent, pd envelope.PolicyDecision, now time.Time) (envelope.WorkEnvelope, error) {
	var out envelope.WorkEnvelope
	var recordErr error
	err := home.WithAutonomyAdmissionLock(s.home, func() error {
		out, recordErr = s.recordActionLocked(workID, ai, pd, now)
		return recordErr
	})
	return out, err
}

func (s *Store) recordActionLocked(workID string, ai envelope.ActionIntent, pd envelope.PolicyDecision, now time.Time) (envelope.WorkEnvelope, error) {
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
	Actor    string
	LaneID   string
	Summary  string
	Changed  bool
	// Steal and StealReason are the close-gate lease escape hatch: closing a work held by a
	// different actor is refused unless Steal is true AND StealReason is non-empty. A steal
	// records the prior holder + reason + stealing actor on the envelope trail (see
	// leaseHolderStatusFor / recordLeaseSteal in lease_close.go). Coordination is
	// advisory-with-teeth, not a deadlock machine — a dead lane must never brick an envelope.
	Steal                      bool
	StealReason                string
	UsedCapabilities           []string
	MissedCapabilities         []string
	FalseTriggeredCapabilities []string
	ProofRefs                  []string
	MemoryCandidates           []string
	MemoryPromoted             []string
	MemoryParked               []string
	MemoryRejected             []string
	FailureSurfaceOutcome      string
	FailureMechanism           string
	// UsedDeclared stamps the emitted receipt's skills.used_declared. It is the honesty
	// flag the injected-ref reinforcement projection reads: true means this close made an
	// affirmative statement about what was used (the CLI passed --used, or a session-end
	// consumption detector ran), so an injected ref absent from Used is honestly ignored;
	// false (the default) leaves absence as unknown. Additive.
	UsedDeclared bool
	// ConsumedRefs are RAW injected-ref consumption signals (e.g. claim://<id>) merged
	// verbatim into the receipt's Skills.Used so the usage projection's pack.included↔used
	// join can score them CONSUMED. Unlike UsedCapabilities they are NOT capability-normalized
	// (a claim ref is not a capability node); they are kept out of the skill typed projections.
	ConsumedRefs []string
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
	var out CloseResult
	var closeErr error
	err := home.WithAutonomyAdmissionLock(s.home, func() error {
		out, closeErr = s.closeLocked(workID, in, now)
		return closeErr
	})
	return out, err
}

func (s *Store) closeLocked(workID string, in CloseInput, now time.Time) (CloseResult, error) {
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
	producerLaneID := strings.TrimSpace(in.LaneID)
	if producerLaneID == "" {
		producerLaneID = strings.TrimSpace(in.Actor)
	}
	actor := strings.TrimSpace(in.Actor)
	if actor == "" {
		actor = "codex"
	}
	if err := heldReleaseOperatorError(w.State, in.Outcome, actor); err != nil {
		return res, err
	}
	if err := s.leaseHolderCloseGate(w, actor, in, now); err != nil {
		return res, err
	}
	if !oneOf(in.MoveType, envelope.MoveTypes) {
		return res, fmt.Errorf("close --move-type must be one of %v, got %q", envelope.MoveTypes, in.MoveType)
	}
	plan, err := s.ClosePlan(workID, now, ClosePlanOptions{Actor: in.Actor, Outcome: in.Outcome, MoveType: in.MoveType})
	if err != nil {
		return res, err
	}
	if !plan.Policy.Allowed {
		return res, fmt.Errorf("cannot close work %s: policy %s (decision %s)", workID, plan.Policy.Outcome, plan.Policy.PolicyDecisionID)
	}
	w.RunIDs = append(w.RunIDs, plan.ActionIntent.ID)
	w.PolicyDecisionID = plan.Decision.ID
	capabilityRefs, err := s.capabilityRefsForContextPack(w.IDs.ContextPackID)
	if err != nil {
		return res, err
	}
	usedCapabilitiesForRuns, err := s.usedCapabilitiesForRuns(w.RunIDs, capabilityRefs)
	if err != nil {
		return res, err
	}
	usedCapabilities := mergeUnique(normalizeCapabilityRefs(in.UsedCapabilities), usedCapabilitiesForRuns)
	missedCapabilities := normalizeCapabilityRefs(in.MissedCapabilities)
	falseTriggeredCapabilities := normalizeCapabilityRefs(in.FalseTriggeredCapabilities)
	skillProjectionRefs := mergeUnique(usedCapabilities, capabilityRefs, missedCapabilities, falseTriggeredCapabilities)
	proofRefs := mergeUnique(in.ProofRefs, w.ArtifactIDs, w.VerificationIDs)
	if err := validateProofMedium(w.ProofMedium, in.Outcome, proofRefs); err != nil {
		return res, err
	}
	memoryCandidates := mergeUnique(w.MemoryCandidateIDs, in.MemoryCandidates)

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
		Cost: envelope.ReceiptCost{ModelCalls: []string{}},
		Skills: envelope.ReceiptSkills{
			Used:                usedCapabilities,
			Missed:              missedCapabilities,
			FalseTriggered:      falseTriggeredCapabilities,
			Candidate:           capabilityRefs,
			CapabilitiesOffered: capabilityRefs,
			UsedDeclared:        in.UsedDeclared,
		},
		Memory: envelope.ReceiptMemory{
			Candidates: memoryCandidates,
			Promoted:   orEmpty(in.MemoryPromoted),
			Parked:     orEmpty(in.MemoryParked),
			Rejected:   orEmpty(in.MemoryRejected),
		},
		Next:      envelope.ReceiptNext{SuggestedWork: []string{}},
		ProofRefs: proofRefs,
	}
	if producerLaneID != "" {
		rec.Producer.LaneID = producerLaneID
	}
	if fc := failureCause(in.FailureSurfaceOutcome, in.FailureMechanism); fc != nil {
		rec.FailureCause = fc
	}
	rec.StateDelta.Changed = rec.EffectiveChanged()
	if err := rec.Validate(); err != nil {
		return res, fmt.Errorf("emitted receipt invalid: %w", err)
	}

	// Transition — the apex object: what changed across spaces, tied to this work_id.
	before := []string{}
	if w.IDs.ContextPackID != "" {
		before = []string{w.IDs.ContextPackID}
	}
	after := append(append([]string{}, orEmpty(w.ArtifactIDs)...), rec.ID)
	typedProjectionRefs := closeProjectionRefs(rec, w.IDs.ContextPackID)
	// Raw injected-ref consumption (e.g. session-end claim:// refs) lands in the receipt's
	// Skills.Used AFTER the skill typed projections are built, so it feeds the usage
	// projection's consumed join without masquerading as a used SKILL. Not capability-
	// normalized: a claim ref must match pack.included[].ref verbatim.
	if len(in.ConsumedRefs) > 0 {
		rec.Skills.Used = mergeUnique(rec.Skills.Used, in.ConsumedRefs)
	}
	tr := envelope.Transition{
		Header:     newHeader(envelope.SVTransition, idFor("tr_", workID, ts(now)), now, workID, w.IDs.ContextPackID, w.Privacy, w.Risk),
		BeforeRefs: before,
		ActionRefs: orEmpty(w.RunIDs),
		AfterRefs:  after,
		StateDelta: in.Summary,
		Projections: envelope.Projections{
			Memory: []string{}, Skill: skillProjectionRefs, Product: []string{}, Cost: []string{},
			Risk: []string{}, Taste: []string{}, Routing: []string{}, HumanAttention: []string{},
			Typed: typedProjectionRefs,
		},
		SecondOrder: envelope.SecondOrder{
			FutureBehaviorChanged: in.MoveType != "audit_only",
			ShouldHarden:          shouldHardenOnClose(w.TargetSurface, in.Outcome, in.ProofRefs),
		},
	}
	if producerLaneID != "" {
		tr.Producer.LaneID = producerLaneID
	}
	if err := tr.Validate(); err != nil {
		return res, fmt.Errorf("emitted transition invalid: %w", err)
	}

	// Persist the emitted envelopes, then land the work terminal and link the transition.
	if err := s.writeJSON(s.envPath(plan.ActionIntent.ID), &plan.ActionIntent); err != nil {
		return res, err
	}
	if err := s.writeJSON(s.envPath(plan.Decision.ID), &plan.Decision); err != nil {
		return res, err
	}
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
	if err := s.appendEvent(Event{TS: ts(now), WorkID: workID, From: from, To: in.Outcome, Note: "closed: " + in.MoveType + " (policy " + plan.Policy.Outcome + ")", Attached: []string{plan.ActionIntent.ID, plan.Decision.ID, rec.ID, tr.ID}}); err != nil {
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

func (s *Store) capabilityRefsForContextPack(contextPackID string) ([]string, error) {
	if strings.TrimSpace(contextPackID) == "" {
		return []string{}, nil
	}
	path := filepath.Join(s.home, "context", "packs", contextPackID+".json")
	var pack envelope.ContextPack
	if err := s.readJSON(path, &pack); err != nil {
		if strings.HasPrefix(err.Error(), "not found:") {
			return []string{}, nil
		}
		return nil, fmt.Errorf("read context pack %s for capability refs: %w", contextPackID, err)
	}
	if err := pack.Validate(); err != nil {
		return nil, fmt.Errorf("context pack %s invalid: %w", contextPackID, err)
	}
	seen := map[string]bool{}
	refs := []string{}
	for _, offer := range pack.CapabilityOffers {
		ref := normalizeCapabilityRef(offer.Ref)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	for _, included := range pack.Included {
		ref := normalizeCapabilityRef(included.Ref)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	return refs, nil
}

func (s *Store) usedCapabilitiesForRuns(runIDs, offeredRefs []string) ([]string, error) {
	seen := map[string]bool{}
	used := []string{}
	offeredByNode := capabilityRefsByNodeID(offeredRefs)
	for _, runID := range runIDs {
		var ai envelope.ActionIntent
		if err := s.readJSON(s.envPath(runID), &ai); err != nil {
			if strings.HasPrefix(err.Error(), "not found:") {
				continue
			}
			return nil, fmt.Errorf("read action intent %s for used capabilities: %w", runID, err)
		}
		if err := ai.Validate(); err != nil {
			return nil, fmt.Errorf("action intent %s invalid: %w", runID, err)
		}
		if strings.TrimSpace(ai.Capability) == "" || internalLifecycleCapability(ai.Capability) {
			continue
		}
		ref := normalizeCapabilityRef(ai.Capability)
		if ref == "" {
			continue
		}
		if offeredRef := offeredByNode[capabilityNodeID(ref)]; offeredRef != "" {
			ref = offeredRef
		}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		used = append(used, ref)
	}
	return used, nil
}

func normalizeCapabilityRefs(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		ref := normalizeCapabilityRef(value)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	return out
}

func normalizeCapabilityRef(value string) string {
	nodeID := capabilityNodeID(value)
	if !realCapabilityNodeID(nodeID) {
		return ""
	}
	return "capability://" + nodeID
}

func capabilityRefsByNodeID(refs []string) map[string]string {
	out := map[string]string{}
	for _, ref := range refs {
		normalized := normalizeCapabilityRef(ref)
		if normalized == "" {
			continue
		}
		out[capabilityNodeID(normalized)] = normalized
	}
	return out
}

func capabilityNodeID(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "capability://")
	return strings.TrimSpace(ref)
}

func realCapabilityNodeID(nodeID string) bool {
	for _, prefix := range []string{
		"lens:",
		"procedure:",
		"script:",
		"tool:",
		"mcp:",
		"model_profile:",
		"harness:",
		"eval:",
		"workflow:",
		"adapter:",
		"doctrine:",
		"taste_standard:",
		"skill:",
		"instruction_rule:",
	} {
		if strings.HasPrefix(nodeID, prefix) && strings.TrimSpace(strings.TrimPrefix(nodeID, prefix)) != "" {
			return true
		}
	}
	return false
}

func internalLifecycleCapability(capability string) bool {
	switch capability {
	case "work.create", "work.claim", "work.advance", "work.close":
		return true
	default:
		return false
	}
}

func validateProofMedium(medium, outcome string, proofRefs []string) error {
	medium = strings.TrimSpace(strings.ToLower(medium))
	if medium == "" || medium == "none" || outcome != "completed" {
		return nil
	}
	switch medium {
	case "web-ui":
		if !containsProofText(proofRefs, "screenshot") || !containsAnyProofText(proofRefs, []string{"design-review", "design_review"}) {
			return fmt.Errorf("proof_medium web-ui requires screenshot artifact and design-review verdict proof refs")
		}
	case "ios":
		if !containsProofText(proofRefs, "screenshot") {
			return fmt.Errorf("proof_medium ios requires screenshot proof ref")
		}
	case "cli", "api":
		if !containsAnyProofText(proofRefs, []string{"command-output", "command_output", "test", "tests"}) {
			return fmt.Errorf("proof_medium %s requires command-output or test proof ref", medium)
		}
	default:
		return fmt.Errorf("proof_medium %q is not supported", medium)
	}
	return nil
}

func containsProofText(values []string, needle string) bool {
	return containsAnyProofText(values, []string{needle})
}

func containsAnyProofText(values []string, needles []string) bool {
	for _, value := range values {
		lower := strings.ToLower(value)
		for _, needle := range needles {
			if strings.Contains(lower, strings.ToLower(needle)) {
				return true
			}
		}
	}
	return false
}

func mergeUnique(groups ...[]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, group := range groups {
		for _, v := range group {
			v = strings.TrimSpace(v)
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func closeProjectionRefs(rec envelope.Receipt, contextPackID string) []envelope.ProjectionRef {
	var out []envelope.ProjectionRef
	add := func(kind, ref, role, evidence string) {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return
		}
		out = append(out, envelope.ProjectionRef{Kind: kind, Ref: ref, Role: role, EvidenceRef: evidence})
	}
	for _, ref := range rec.Skills.Candidate {
		add("skill", ref, "offered", contextPackID)
	}
	for _, ref := range rec.Skills.Used {
		add("skill", ref, "used", rec.ID)
	}
	for _, ref := range rec.Skills.Missed {
		add("skill", ref, "missed", rec.ID)
	}
	for _, ref := range rec.Skills.FalseTriggered {
		add("skill", ref, "false_triggered", rec.ID)
	}
	for _, ref := range rec.Memory.Candidates {
		add("memory", ref, "candidate", rec.ID)
	}
	for _, ref := range rec.Memory.Promoted {
		add("memory", ref, "promoted", rec.ID)
	}
	for _, ref := range rec.Memory.Parked {
		add("memory", ref, "parked", rec.ID)
	}
	for _, ref := range rec.Memory.Rejected {
		add("memory", ref, "rejected", rec.ID)
	}
	for _, ref := range rec.ProofRefs {
		if strings.HasPrefix(ref, "failure:") || strings.HasPrefix(ref, "failure_pattern:") || strings.HasPrefix(ref, "rule:") || strings.HasPrefix(ref, "check:") || strings.HasPrefix(ref, "guard:") {
			add("rule", ref, "proof", rec.ID)
			continue
		}
		add("control_plane", ref, "proof", rec.ID)
	}
	return out
}

func (s *Store) readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("not found: %s: %w", filepath.Base(path), err)
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

// ListLeases returns the read-only lease projection for claimed work. The work
// package owns the lease files; operator surfaces should consume this projection
// instead of reading COMPOUND_HOME/work/leases directly.
func (s *Store) ListLeases() ([]Lease, error) {
	dir := filepath.Join(s.home, "work", "leases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	leases := make([]Lease, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var lease Lease
		if err := s.readJSON(filepath.Join(dir, entry.Name()), &lease); err != nil {
			return nil, err
		}
		if strings.TrimSpace(lease.WorkID) == "" {
			lease.WorkID = strings.TrimSuffix(entry.Name(), ".json")
		}
		leases = append(leases, lease)
	}
	sort.Slice(leases, func(i, j int) bool {
		if leases[i].ClaimAt == leases[j].ClaimAt {
			return leases[i].WorkID < leases[j].WorkID
		}
		return leases[i].ClaimAt < leases[j].ClaimAt
	})
	return leases, nil
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

// shouldHardenOnClose reports whether a close should stamp
// SecondOrder.ShouldHarden=true. It is true only on an ACCEPTED close (outcome
// "completed") of an explicit harness-edit envelope — one that declared a
// TargetSurface (EVOLVE-BLOCK) — AND that close carries a bench-compare ACCEPT
// proof ref, so "completed" cannot self-certify a harness edit. A rejected/failed/
// cancelled close of the same envelope stamps false but still emits its Receipt +
// Transition, so "rejections are logged without changing the active harness." An
// ordinary work (no TargetSurface) never hardens on close. Derived, not a new
// CloseInput flag: the close outcome already carries whether the harness edit was
// accepted; the proof ref carries whether the gate actually accepted it.
func shouldHardenOnClose(targetSurface, outcome string, proofRefs []string) bool {
	return strings.TrimSpace(targetSurface) != "" &&
		outcome == "completed" &&
		hasBenchCompareAcceptProof(proofRefs)
}

// hasBenchCompareAcceptProof reports whether the close's proof refs contain a
// bench-compare accept record. Accepted spellings include bench_compare_accept:<id>
// and bench_compare:accept:<id>; matched case-insensitively on a ref that contains
// both "bench_compare" and "accept". A plain bench_report:... ref does not qualify —
// the harness edit must prove the gate accepted, not merely that a report exists.
func hasBenchCompareAcceptProof(refs []string) bool {
	for _, r := range refs {
		r = strings.ToLower(strings.TrimSpace(r))
		if strings.Contains(r, "bench_compare") && strings.Contains(r, "accept") {
			return true
		}
	}
	return false
}

// failureCause builds the optional causal-failure annotation; nil when both
// fields are empty so the receipt stays byte-identical to pre-field behavior.
func failureCause(surfaceOutcome, mechanism string) *envelope.FailureCause {
	surfaceOutcome = strings.TrimSpace(surfaceOutcome)
	mechanism = strings.TrimSpace(mechanism)
	if surfaceOutcome == "" && mechanism == "" {
		return nil
	}
	return &envelope.FailureCause{SurfaceOutcome: surfaceOutcome, Mechanism: mechanism}
}
