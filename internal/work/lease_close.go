package work

// Close-gate lease enforcement. A WorkEnvelope lease (Claim, work.go:295-345) was advisory
// only: nothing stopped a non-holder actor from closing a work another actor's executor was
// still mid-build on. Tonight's twin-collisions (work_b1c9d3d624e1 consumed+closed by a lane
// that never held it; work_fc4a3b26a15a closed by a concurrent session mid-build) are the
// exact regression this file closes. See docs_private/plan/packets/2026-07-09-close-gate-lease.md.
//
// The gate lives here, in the Store, so every close door (Close, ClosePlan preview, and any
// future caller) inherits it — not in the CLI layer. Design:
//   - No lease (never claimed, or lease residue absent) = today's behavior, untouched.
//   - Same actor as the lease holder = today's behavior, untouched (exact string equality).
//   - A different actor with no --steal, or --steal with an empty reason, is REFUSED: the
//     refusal is recorded (ActionIntent + deny PolicyDecision + lifecycle event) so liveness
//     probes can see the contention, and the work is left exactly as it was.
//   - A different actor with --steal and a non-empty reason SUCCEEDS: the steal is recorded
//     (ActionIntent + PolicyDecision + lifecycle event carrying prior holder, reason, and the
//     stealing actor) and the caller proceeds. Coordination is advisory-with-teeth, not a
//     deadlock machine — a dead lane must never brick an envelope.

import (
	"fmt"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/policy"
)

// leaseHolderStatus is the read-only comparison of a close attempt against a work's live
// lease. Holder is empty when there is no lease (or no lease residue) to enforce.
type leaseHolderStatus struct {
	Holder  string // lease holder; "" means no lease = today's behavior
	Blocked bool   // actor is not Holder and no valid steal is presented
	Stolen  bool   // actor is not Holder but a valid --steal --reason is presented
	Message string // holder-named refusal / requirement message, set when Blocked
}

// leaseHolderStatusFor reads workID's lease (if any) live and reports how an attempt by
// actor (to perform the mutation verb named by verb, e.g. "close" or "advance") compares to
// it. Pure/read-only: callers decide whether to record anything. verb only appears in the
// non-holder remedy message; the --steal-without-reason message never mentions it, so
// generalizing this from close-only costs close callers nothing (byte-identical when
// verb == "close").
func (s *Store) leaseHolderStatusFor(workID, actor string, steal bool, stealReason, verb string) leaseHolderStatus {
	lease, err := s.readLease(workID)
	holder := strings.TrimSpace(lease.By)
	if err != nil || holder == "" || holder == actor {
		return leaseHolderStatus{}
	}
	reason := strings.TrimSpace(stealReason)
	if steal && reason != "" {
		return leaseHolderStatus{Holder: holder, Stolen: true}
	}
	if steal {
		return leaseHolderStatus{
			Holder:  holder,
			Blocked: true,
			Message: fmt.Sprintf("work %s is held by %q; --steal requires --reason <non-empty>", workID, holder),
		}
	}
	return leaseHolderStatus{
		Holder:  holder,
		Blocked: true,
		Message: fmt.Sprintf("work %s is held by %q; %s requires --by %s or --steal --reason \"...\"", workID, holder, verb, holder),
	}
}

// leaseHolderCloseGate is the enforcement seam Close calls immediately after
// heldReleaseOperatorError. It refuses (recording a deny) a non-holder close, records an
// explicit steal, and is a no-op for an unclaimed work or a same-actor close.
func (s *Store) leaseHolderCloseGate(w envelope.WorkEnvelope, actor string, in CloseInput, now time.Time) error {
	status := s.leaseHolderStatusFor(w.ID, actor, in.Steal, in.StealReason, "close")
	switch {
	case status.Blocked:
		if err := s.recordLeaseHolderDeny(w, actor, status.Holder, status.Message, "work.close", "close_denied_not_lease_holder", now); err != nil {
			return err
		}
		return fmt.Errorf("cannot close %s: %s", w.ID, status.Message)
	case status.Stolen:
		return s.recordLeaseSteal(w, actor, status.Holder, strings.TrimSpace(in.StealReason), "work.close", "close_lease_stolen", now)
	default:
		return nil
	}
}

// recordLeaseHolderDeny writes the ActionIntent and a deny PolicyDecision for a mutation
// (capability/action name the caller's verb, e.g. work.close/close_denied_not_lease_holder
// or work.advance/advance_denied_not_lease_holder) attempted by an actor who is not the
// current lease holder (including an invalid steal attempt), plus a refusal lifecycle
// event, so the denial leaves an auditable trail while the work stays in place — a second
// identical refusal only appends another event line, it never corrupts state
// (idempotent-safe trail). The event Note prefix is derived from capability ("work.close" ->
// "close refused: ", "work.advance" -> "advance refused: ") so this stays one write path for
// every gated verb; close's Note stays byte-identical to before generalization.
func (s *Store) recordLeaseHolderDeny(w envelope.WorkEnvelope, actor, holder, message, capability, action string, now time.Time) error {
	ai := s.leaseGateActionIntent(w, actor, capability, action, now)
	if err := ai.Validate(); err != nil {
		return err
	}
	pd := policy.LeaseHolderDenyDecision(ai, message, now)
	if err := pd.Validate(); err != nil {
		return err
	}
	if err := s.writeEventJSON(ai.ID, &ai); err != nil {
		return err
	}
	if err := s.writeEventJSON(pd.ID, &pd); err != nil {
		return err
	}
	return s.appendEvent(Event{
		TS:       ts(now),
		WorkID:   w.ID,
		From:     w.State,
		To:       w.State,
		By:       actor,
		Note:     refusalNotePrefix(capability) + message,
		Attached: []string{ai.ID, pd.ID},
	})
}

// recordLeaseSteal writes the ActionIntent and a recorded PolicyDecision for an explicit
// lease steal (--steal --reason) on the mutation named by capability/action, plus a
// lifecycle event carrying the prior holder, the reason, and the stealing actor — the same
// auditable trail a refusal leaves, so a steal is a loud, recorded override, not a silent
// bypass.
func (s *Store) recordLeaseSteal(w envelope.WorkEnvelope, actor, holder, reason, capability, action string, now time.Time) error {
	ai := s.leaseGateActionIntent(w, actor, capability, action, now)
	if err := ai.Validate(); err != nil {
		return err
	}
	pd := policy.LeaseHolderDenyDecision(ai, fmt.Sprintf("lease held by %q stolen by %q: %s", holder, actor, reason), now)
	if err := pd.Validate(); err != nil {
		return err
	}
	if err := s.writeEventJSON(ai.ID, &ai); err != nil {
		return err
	}
	if err := s.writeEventJSON(pd.ID, &pd); err != nil {
		return err
	}
	return s.appendEvent(Event{
		TS:       ts(now),
		WorkID:   w.ID,
		From:     w.State,
		To:       w.State,
		By:       actor,
		Note:     fmt.Sprintf("lease stolen from %q: %s", holder, reason),
		Attached: []string{ai.ID, pd.ID},
	})
}

// refusalNotePrefix keeps the close refusal event Note byte-identical ("close refused: ...")
// while giving other gated verbs (advance: "advance refused: ...") their own prefix, derived
// from the capability ("work.<verb>") rather than adding yet another parameter everywhere.
func refusalNotePrefix(capability string) string {
	return strings.TrimPrefix(capability, "work.") + " refused: "
}

// leaseGateActionIntent builds the ActionIntent shared by recordLeaseHolderDeny and
// recordLeaseSteal — a distinct, non-mutating "audit only" action against the work, kept
// separate from the actual mutation's own ActionIntent (closeActionIntent in close_plan.go,
// advanceActionIntent in advance_plan.go) so the lease-gate record never masquerades as the
// mutation itself. capability/action are supplied by the caller (e.g. "work.close" /
// "close_denied_not_lease_holder", "work.advance" / "advance_denied_not_lease_holder") so
// this one builder serves every gated verb.
func (s *Store) leaseGateActionIntent(w envelope.WorkEnvelope, actor, capability, action string, now time.Time) envelope.ActionIntent {
	targetPath := s.workPath(w.ID)
	return envelope.ActionIntent{
		Header: newHeader(
			envelope.SVActionIntent,
			idFor("act_", capability, action, "work", w.ID, targetPath, actor, ts(now)),
			now,
			w.ID,
			w.IDs.ContextPackID,
			w.Privacy,
			w.Risk,
		),
		Actor:         actor,
		Capability:    capability,
		Action:        action,
		Target:        envelope.ActionTarget{Kind: "work", ID: w.ID, Path: targetPath},
		MutationClass: "local_reversible",
		Reversibility: "manual",
		BlastRadius:   "local",
		Preview:       fmt.Sprintf("%s work %s", action, w.ID),
		RollbackPlan:  "no mutation recorded; work state unchanged",
	}
}
