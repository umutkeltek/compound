package work

// Auto-admit chokepoint (W9-J4). AdmitAuto is the ONLY path that releases a
// director-proposed work proposed -> admitted WITHOUT operator provenance. It exists so a
// future trust-dial (W9-J6) has exactly one gated door to walk through, and that door is
// wired through the policy-owned autonomy kill-switch FIRST. The generic Advance seam
// keeps a defense-in-depth copy of the same halt check, so no autonomous admission path
// can ignore the flag. Nothing in this package constructs an eligible AutoAdmitInput, so
// zero autonomy ships by default; the switch only stops what J6 will later start.

import (
	"fmt"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/mem"
	"compound/internal/policy"
)

// AutoAdmitInput carries a trust-dial auto-admit request. Eligibility is supplied by the
// caller: W9-J6 computes it from a clean-window operator grant. Until then no shipped
// caller sets EligibilityReason, so nothing auto-admits. The kill-switch overrides
// eligibility unconditionally.
type AutoAdmitInput struct {
	By                string // autonomous actor requesting admission (default "director")
	EligibilityReason string // non-empty asserts the class is clean-window eligible (W9-J6)
	GrantRef          string // optional clean-window / standing-grant evidence ref
	Note              string
}

// AutoAdmitResult reports the outcome of an auto-admit attempt. On a halt or an
// ineligible request Admitted is false and the work stays proposed.
type AutoAdmitResult struct {
	WorkID    string `json:"work_id"`
	WorkState string `json:"work_state"`
	Class     string `json:"class"`
	Admitted  bool   `json:"admitted"`
	Halted    bool   `json:"halted"`
	Reason    string `json:"reason,omitempty"`
}

// AdmitAuto attempts an autonomous admission of a director-proposed work. Order is
// load-and-shape -> kill-switch (halt always wins, records a deny) -> eligibility (J6) ->
// admit. It never mutates on a blocked, halted, or ineligible request.
func (s *Store) AdmitAuto(workID string, in AutoAdmitInput, now time.Time) (AutoAdmitResult, error) {
	w, err := s.Get(workID)
	if err != nil {
		return AutoAdmitResult{}, err
	}
	if w.State != "proposed" {
		return AutoAdmitResult{WorkID: w.ID, WorkState: w.State}, fmt.Errorf("auto-admit requires proposed work, got %q", w.State)
	}
	if !directorProposedWork(w) {
		return AutoAdmitResult{WorkID: w.ID, WorkState: w.State}, fmt.Errorf("auto-admit only applies to director-proposed work")
	}
	class := autonomyClassOf(w)
	actor := autoAdmitActor(in.By)

	// 1. Kill-switch (W9-J4): a halt overrides any eligibility. Records a deny, stays proposed.
	halted, reason, err := policy.AutonomyHalted(s.home, class)
	if err != nil {
		return AutoAdmitResult{WorkID: w.ID, WorkState: w.State, Class: class}, err
	}
	if halted {
		if recErr := s.recordAutonomyHalt(w, actor, "admitted", reason, now); recErr != nil {
			return AutoAdmitResult{WorkID: w.ID, WorkState: w.State, Class: class}, recErr
		}
		return AutoAdmitResult{WorkID: w.ID, WorkState: "proposed", Class: class, Halted: true, Reason: reason},
			fmt.Errorf("autonomy halted (%s): %s", class, reason)
	}

	// 1.5 Editable-surfaces (EVOLVE-BLOCK): if this proposal declares a target harness
	// surface, it may be admitted only while that surface is actively whitelisted. Runs
	// AFTER the kill-switch (so a halt wins) and BEFORE eligibility. A proposal with no
	// target_surface is ordinary work and passes untouched.
	if err := s.editableSurfaceGuard(w, "admitted", actor, now); err != nil {
		return AutoAdmitResult{WorkID: w.ID, WorkState: "proposed", Class: class, Reason: err.Error()}, err
	}

	// 1.6 Freeze (grants-as-gates, 2026-07-10): a director-proposed admission
	// matching an active freeze decision record is denied, naming the freeze.
	// Runs AFTER the kill-switch and editable-surfaces guard, BEFORE
	// eligibility. An envelope matching no active freeze passes untouched.
	if err := s.freezeGuard(w, "admitted", actor, now); err != nil {
		return AutoAdmitResult{WorkID: w.ID, WorkState: "proposed", Class: class, Reason: err.Error()}, err
	}

	// 2. Eligibility (W9-J6 supplies this; unset by default => nothing auto-admits).
	eligibility := strings.TrimSpace(in.EligibilityReason)
	if eligibility == "" {
		return AutoAdmitResult{WorkID: w.ID, WorkState: "proposed", Class: class, Reason: "no clean-window grant"},
			fmt.Errorf("auto-admit not eligible for class %q: no clean-window grant (W9-J6 supplies eligibility)", class)
	}

	// 3. Cleared: admit through the shared advance write path.
	note := strings.TrimSpace(in.Note)
	if note == "" {
		note = "auto-admitted (" + class + "): " + eligibility
	}
	advanced, err := s.applyAdvance(w, AdvanceInput{Actor: actor, To: "admitted", Note: note}, now)
	if err != nil {
		return AutoAdmitResult{WorkID: w.ID, WorkState: w.State, Class: class}, err
	}
	return AutoAdmitResult{WorkID: advanced.ID, WorkState: advanced.State, Class: class, Admitted: true, Reason: eligibility}, nil
}

// autonomyHaltGuard is the defense-in-depth kill-switch check inside the generic Advance
// seam. It denies an autonomous (non-operator) admission of director-proposed work while
// the policy-owned halt is engaged, and records a deny PolicyDecision. It is a no-op for
// operator admits, non-proposed work, and an unset halt.
func (s *Store) autonomyHaltGuard(w envelope.WorkEnvelope, to, actor string, now time.Time) error {
	if !isAutonomousAdmission(w, to, actor) {
		return nil
	}
	class := autonomyClassOf(w)
	halted, reason, err := policy.AutonomyHalted(s.home, class)
	if err != nil {
		return err
	}
	if !halted {
		return nil
	}
	if err := s.recordAutonomyHalt(w, actor, to, reason, now); err != nil {
		return err
	}
	return fmt.Errorf("autonomy halted (%s): %s", class, reason)
}

// recordAutonomyHalt writes the ActionIntent and a deny PolicyDecision for a halted
// autonomous admission, plus a lifecycle event, so the halt leaves an auditable trail
// while the work stays in place.
func (s *Store) recordAutonomyHalt(w envelope.WorkEnvelope, actor, to, reason string, now time.Time) error {
	ai := s.advanceActionIntent(w, actor, to, now, AdvancePlanOptions{Actor: actor, To: to})
	if err := ai.Validate(); err != nil {
		return err
	}
	pd := policy.AutonomyHaltDecision(ai, reason, now)
	if err := pd.Validate(); err != nil {
		return err
	}
	if err := s.writeJSON(s.envPath(ai.ID), &ai); err != nil {
		return err
	}
	if err := s.writeJSON(s.envPath(pd.ID), &pd); err != nil {
		return err
	}
	return s.appendEvent(Event{
		TS:       ts(now),
		WorkID:   w.ID,
		From:     w.State,
		To:       w.State,
		By:       actor,
		Note:     "autonomy halt: " + reason,
		Attached: []string{ai.ID, pd.ID},
	})
}

// editableSurfaceGuard is the editable-surfaces (EVOLVE-BLOCK) check at the admission seam.
// It denies a system-originated (director-proposed, non-operator) admission that declares a
// target_surface when that surface is not ACTIVELY whitelisted in the policy-owned
// editable-surfaces registry, and records a deny PolicyDecision. It is a no-op for operator
// admits, non-proposed work, and — critically — for any work WITHOUT a target_surface, so
// ordinary proposals are unaffected. This registry only constrains explicit harness-edit
// proposals.
func (s *Store) editableSurfaceGuard(w envelope.WorkEnvelope, to, actor string, now time.Time) error {
	if !isAutonomousAdmission(w, to, actor) {
		return nil
	}
	surface := strings.TrimSpace(w.TargetSurface)
	if surface == "" {
		return nil
	}
	allowed, reason, err := policy.EditableSurfaceAllowed(s.home, surface)
	if err != nil {
		return err
	}
	if allowed {
		return nil
	}
	if err := s.recordEditableSurfaceDeny(w, actor, to, reason, now); err != nil {
		return err
	}
	return fmt.Errorf("editable surface denied (%s): %s", surface, reason)
}

// recordEditableSurfaceDeny writes the ActionIntent and a deny PolicyDecision for a
// system-originated proposal blocked by the editable-surfaces registry, plus a lifecycle
// event, so the denial leaves an auditable trail while the work stays in place.
func (s *Store) recordEditableSurfaceDeny(w envelope.WorkEnvelope, actor, to, reason string, now time.Time) error {
	ai := s.advanceActionIntent(w, actor, to, now, AdvancePlanOptions{Actor: actor, To: to})
	if err := ai.Validate(); err != nil {
		return err
	}
	pd := policy.EditableSurfaceDenyDecision(ai, reason, now)
	if err := pd.Validate(); err != nil {
		return err
	}
	if err := s.writeJSON(s.envPath(ai.ID), &ai); err != nil {
		return err
	}
	if err := s.writeJSON(s.envPath(pd.ID), &pd); err != nil {
		return err
	}
	return s.appendEvent(Event{
		TS:       ts(now),
		WorkID:   w.ID,
		From:     w.State,
		To:       w.State,
		By:       actor,
		Note:     "editable-surface deny: " + reason,
		Attached: []string{ai.ID, pd.ID},
	})
}

// isAutonomousAdmission is true only for a non-operator release of director-proposed work
// out of `proposed` — the exact shape a trust-dial auto-admit or director-originated
// advance takes. Operator admits, claims, and ordinary advances are excluded.
func isAutonomousAdmission(w envelope.WorkEnvelope, to, actor string) bool {
	return w.State == "proposed" && to != "" && to != "proposed" && directorProposedWork(w) && !hasOperatorProvenance(actor)
}

// autonomyClassOf derives the autonomy class of a director-proposed work from its
// director_trigger evidence ref (e.g. director_trigger:job_health:<digest> -> job_health).
// Director work without a trigger ref falls back to the generic class "director".
func autonomyClassOf(w envelope.WorkEnvelope) string {
	for _, ref := range w.EvidenceRefs {
		ref = strings.TrimSpace(ref)
		if !strings.HasPrefix(ref, "director_trigger:") {
			continue
		}
		rest := strings.TrimPrefix(ref, "director_trigger:")
		if i := strings.Index(rest, ":"); i > 0 {
			return rest[:i]
		}
		if rest != "" {
			return rest
		}
	}
	return "director"
}

// autoAdmitActor stamps an autonomous-admission provenance so the actor is never mistaken
// for an operator. hasOperatorProvenance is false for it, so the kill-switch always applies.
func autoAdmitActor(by string) string {
	by = strings.TrimSpace(by)
	if by == "" {
		by = "director"
	}
	if hasAutoAdmitProvenance(by) {
		return by
	}
	return "auto-admit:" + by
}

func hasAutoAdmitProvenance(actor string) bool {
	actor = strings.ToLower(strings.TrimSpace(actor))
	return actor == "auto-admit" || strings.HasPrefix(actor, "auto-admit:") || strings.HasPrefix(actor, "auto-admit-")
}

// --- Freeze admission guard (grants-as-gates, 2026-07-10) ---
//
// The sibling of autonomyHaltGuard/editableSurfaceGuard at the same
// admission seam. Its source of truth is a decision RECORD (an accepted
// kind=freeze claim in the decisions store), never markdown/worklog/brain
// prose (falsifier e). Matching is literal (policy.FreezeMatch): an owner
// glob or a keyword substring against the canonical surface/evidence refs,
// never inferred.
//
// This reads mem.Claim qualifiers directly instead of going through
// internal/decisions' ActiveDecisions/LifecycleFromClaim helpers: decisions
// imports internal/writeback, which imports internal/work, so internal/work
// importing internal/decisions would be an import cycle. The qualifier keys
// (kind, lifecycle_status, decision_id, freeze_owner_globs, freeze_keywords,
// supersedes, reviewed_by) mirror internal/decisions' schema exactly —
// `compound decisions record --kind freeze` is the only writer of these
// qualifiers, so the two stay in lockstep by construction.
const (
	freezeDecisionKind            = "freeze"
	freezeDecisionStatusAccepted  = "accepted"
	freezeDecisionSupersedesField = "supersedes"
)

// freezeGuard is the enforcement seam AdmitAuto and Advance both call: a
// no-op for operator admits, non-proposed work, and when nothing active
// matches. A matching freeze refuses the admission, recording a deny.
func (s *Store) freezeGuard(w envelope.WorkEnvelope, to, actor string, now time.Time) error {
	if !isAutonomousAdmission(w, to, actor) {
		return nil
	}
	matched, rec, err := s.matchingFreeze(w)
	if err != nil {
		return err
	}
	if !matched {
		return nil
	}
	if err := s.recordFreezeDeny(w, actor, to, rec, now); err != nil {
		return err
	}
	return fmt.Errorf("freeze active (%s): envelope matches an active freeze scope", rec.ID)
}

// matchingFreeze loads active freeze decision records from the decisions
// store (mem claims) — never markdown/brain paths — and reports whether the
// envelope matches one.
func (s *Store) matchingFreeze(w envelope.WorkEnvelope) (bool, policy.FreezeRecord, error) {
	memStore, err := mem.Open(s.home)
	if err != nil {
		return false, policy.FreezeRecord{}, err
	}
	claims, err := memStore.AllCurrent()
	if err != nil {
		return false, policy.FreezeRecord{}, err
	}
	records := activeFreezeRecords(claims)
	matched, rec := policy.FreezeMatch(w.Owner, w.CanonicalSurface, w.EvidenceRefs, records)
	return matched, rec, nil
}

// activeFreezeRecords projects current decision claims into policy.FreezeRecord
// values: accepted kind=freeze claims not named in any other current
// record's supersedes qualifier (undo = supersede, falsifier c).
func activeFreezeRecords(claims []mem.Claim) []policy.FreezeRecord {
	superseded := map[string]bool{}
	for _, c := range claims {
		if c.ClaimType != "decision" {
			continue
		}
		for _, id := range splitFreezeCSV(c.Qualifiers[freezeDecisionSupersedesField]) {
			superseded[id] = true
		}
	}
	var out []policy.FreezeRecord
	for _, c := range claims {
		if c.ClaimType != "decision" {
			continue
		}
		q := c.Qualifiers
		if q["kind"] != freezeDecisionKind || q["lifecycle_status"] != freezeDecisionStatusAccepted {
			continue
		}
		id := q["decision_id"]
		if id != "" && superseded[id] {
			continue
		}
		out = append(out, policy.FreezeRecord{
			ID:         id,
			Title:      c.Object,
			ReviewedBy: q["reviewed_by"],
			OwnerGlobs: splitFreezeCSV(q["freeze_owner_globs"]),
			Keywords:   splitFreezeCSV(q["freeze_keywords"]),
		})
	}
	return out
}

func splitFreezeCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// recordFreezeDeny writes the ActionIntent and a deny PolicyDecision for an
// autonomous admission blocked by an active freeze, plus a lifecycle event
// naming the freeze record, so the deny leaves an auditable trail while the
// work stays in place.
func (s *Store) recordFreezeDeny(w envelope.WorkEnvelope, actor, to string, rec policy.FreezeRecord, now time.Time) error {
	reason := fmt.Sprintf("freeze active (%s): %s matches an active freeze scope; operator must record a superseding decision to unfreeze", rec.ID, w.ID)
	ai := s.advanceActionIntent(w, actor, to, now, AdvancePlanOptions{Actor: actor, To: to})
	if err := ai.Validate(); err != nil {
		return err
	}
	pd := policy.FreezeDenyDecision(ai, reason, now)
	if err := pd.Validate(); err != nil {
		return err
	}
	if err := s.writeJSON(s.envPath(ai.ID), &ai); err != nil {
		return err
	}
	if err := s.writeJSON(s.envPath(pd.ID), &pd); err != nil {
		return err
	}
	return s.appendEvent(Event{
		TS:       ts(now),
		WorkID:   w.ID,
		From:     w.State,
		To:       w.State,
		By:       actor,
		Note:     "freeze deny: " + reason,
		Attached: []string{ai.ID, pd.ID},
	})
}
