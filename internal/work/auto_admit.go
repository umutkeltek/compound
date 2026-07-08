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
