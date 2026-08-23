// Wedge-prevention tests for run-attempt authority: an attempt may be bound
// to exactly ONE (act_, pol_) pair per work. The wedge (lived 2026-08-23,
// envelope work_1d968c3f0522) happened when two lifecycle events attached the
// same ratt_ id with different pairs: the close-time gate failed permanently
// with `runattempt ... authority generation is ambiguous (pairs=2)`.
//
// Falsifiers:
//   1. PREVENTION: a second advance that re-attaches a ratt_ id with a
//      DIFFERENT (act_, pol_) pair is REFUSED at advance time with a truthful
//      error naming the existing event, the existing pair, the new pair, and
//      remediation. Pre-fix histories still surface at close (defense in
//      depth) with an error that NAMES both pairs + both events.
//   2. SAME-PAIR IDEMPOTENT RE-ATTACH: a re-attach with the SAME (act_, pol_)
//      pair is allowed; close succeeds under that pair.

package work

import (
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/runattempt"
)

// writeWedgeLifecycleEvent synthesizes one lifecycle event carrying an
// action + policy + ratt_ triple. Tests use this to construct wedged histories
// deterministically (the production path does not mint two events with the
// same ratt_ + different pairs; that was a bug, not a feature).
func writeWedgeLifecycleEvent(t *testing.T, s *Store, workID, from, to, note string, actionID, policyID, rattID string, at time.Time) {
	t.Helper()
	ev := Event{
		TS:       ts(at),
		WorkID:   workID,
		From:     from,
		To:       to,
		Note:     note,
		Attached: []string{actionID, policyID, rattID},
	}
	if err := s.appendEventUnlocked(ev); err != nil {
		t.Fatalf("append wedge event: %v", err)
	}
}

// writeActionIntentJSON persists a synthetic act_ envelope so
// resolveRunAttemptAuthorityFromEvents can read it back. The fixture must
// pass Validate() — mutation_class=read skips the target.id/path requirement.
func writeActionIntentJSON(t *testing.T, s *Store, id, workID string, at time.Time) {
	t.Helper()
	ai := envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            id,
			CreatedAt:     at.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "cli", Name: "test"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         "codex",
		Capability:    "wedge.test",
		Action:        "wedge-test:fake",
		MutationClass: "read",
		Reversibility: "easy",
	}
	if err := ai.Validate(); err != nil {
		t.Fatalf("fixture action_intent %s invalid: %v", id, err)
	}
	if _, err := s.events.PutJSON(id, &ai, "  "); err != nil {
		// PutJSON refuses overwrites with different content. Wedge fixtures
		// are deterministic: the test author picks one specific (id, ts)
		// tuple and the store is per-test (t.TempDir), so a duplicate id is
		// always a test bug.
		t.Fatalf("put action intent %s: %v", id, err)
	}
}

// writePolicyDecisionJSON persists a synthetic pol_ envelope with the given
// action_intent_id binding. The close-time authority gate calls
// decision.Validate() + policy.Allowed(decision) on it, so Outcome must be
// "allow" (or "allow_with_obligations") and ActionIntentID must match the
// act_ id it pairs with.
func writePolicyDecisionJSON(t *testing.T, s *Store, id, workID, actionID string, at time.Time) {
	t.Helper()
	pd := envelope.PolicyDecision{
		Header: envelope.Header{
			SchemaVersion: envelope.SVPolicyDecision,
			ID:            id,
			CreatedAt:     at.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "cli", Name: "test"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		ActionIntentID: actionID,
		Outcome:        "allow",
		Reasons:        []string{"wedge fixture decision"},
	}
	if err := pd.Validate(); err != nil {
		t.Fatalf("fixture policy_decision %s invalid: %v", id, err)
	}
	if _, err := s.events.PutJSON(id, &pd, "  "); err != nil {
		t.Fatalf("put policy decision %s: %v", id, err)
	}
}

// mintExecutionRunAttempt mints a harness_execution-origin RunAttempt without
// binding an ActionIntent (so resolveRunAttemptAuthorityFromEvents falls
// through to the event-iteration path that scans every attaching event).
func mintExecutionRunAttempt(t *testing.T, s *Store, workID string, at time.Time) runattempt.RunAttempt {
	t.Helper()
	raStore := runattempt.Open(s.home)
	ra, err := raStore.Mint(workID, "codex", runattempt.HarnessPlaceholder, at)
	if err != nil {
		t.Fatalf("mint RunAttempt: %v", err)
	}
	if _, err := raStore.SetOrigin(ra.ID, runattempt.OriginHarnessExecution); err != nil {
		t.Fatalf("SetOrigin: %v", err)
	}
	return ra
}

// Falsifier 1 (close-time error naming, RED before fix): a wedged-history
// work whose close is attempted must fail with an error that NAMES both
// (act_, pol_) pairs + both event timestamps + a remediation hint. This
// stays valid as defense-in-depth even after the prevention ships — older
// histories already on disk need a readable failure mode.
func TestCloseWedgeNamesBothPairsAndRemediation(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "wedge-test", SuccessPredicate: "wedge resolves with named pairs"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(w.ID, "codex", fixedNow); err != nil {
		t.Fatal(err)
	}

	ra := mintExecutionRunAttempt(t, s, w.ID, fixedNow)

	// First (legitimate) attach: real running advance with ratt_. The
	// advance mints its own (act_first, pol_first) and stamps them on the
	// lifecycle event; we capture them here.
	advanced, err := s.Advance(w.ID, AdvanceInput{
		To: "running", Actor: "codex", RunAttemptIDs: []string{ra.ID},
	}, fixedNow)
	if err != nil {
		t.Fatalf("first advance: %v", err)
	}
	firstAction := advanced.ActionIntentIDs[len(advanced.ActionIntentIDs)-1]
	firstPolicy := advanced.PolicyDecisionID

	// Seed a SECOND lifecycle event that re-attaches the SAME ratt_ but
	// with a DIFFERENT (act_, pol_) pair. This is the wedge shape — the
	// close-time gate must surface both pairs and remediation, not just
	// "pairs=2".
	writeActionIntentJSON(t, s, "act_wedge_second", w.ID, fixedNow.Add(time.Minute))
	writePolicyDecisionJSON(t, s, "pol_wedge_second", w.ID, "act_wedge_second", fixedNow.Add(time.Minute))
	writeWedgeLifecycleEvent(t, s, w.ID, "running", "verifying", "manual advance re-attach",
		"act_wedge_second", "pol_wedge_second", ra.ID, fixedNow.Add(2*time.Minute))

	// Close MUST fail with an authority-generation error that NAMES both
	// pairs AND offers a remediation hint.
	_, closeErr := s.Close(w.ID, CloseInput{
		Outcome:       "completed",
		MoveType:      "outward_delta",
		Actor:         "codex",
		Summary:       "wedge should refuse",
		Changed:       true,
		ProofRefs:     []string{"proof:wedge"},
		RunAttemptIDs: []string{ra.ID},
	}, fixedNow.Add(10*time.Minute))
	if closeErr == nil {
		t.Fatal("close accepted a wedged history; want authority-generation error")
	}
	msg := closeErr.Error()
	for _, want := range []string{
		firstAction, firstPolicy,
		"act_wedge_second", "pol_wedge_second",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("close error missing %q: %s", want, msg)
		}
	}
	if !strings.Contains(msg, "remedia") && !strings.Contains(msg, "Reconcile") {
		t.Errorf("close error missing remediation hint: %s", msg)
	}
}

// Falsifier 2 (prevention, RED before fix): the SECOND advance that tries
// to attach an already-attached ratt_ with a DIFFERENT (act_, pol_) pair is
// REFUSED at advance time, before any state mutation. This is the new
// behavior — it prevents the wedge from being written in the first place.
func TestAdvanceRefusesReattachWithDifferentPair(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "wedge-prevention", SuccessPredicate: "advance refuses different-pair re-attach"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(w.ID, "codex", fixedNow); err != nil {
		t.Fatal(err)
	}

	// First attach: real running advance with ratt_. Establishes a known
	// (act_, pol_) pair binding for this ratt_.
	ra := mintExecutionRunAttempt(t, s, w.ID, fixedNow)
	first, err := s.Advance(w.ID, AdvanceInput{
		To: "running", Actor: "codex", RunAttemptIDs: []string{ra.ID},
	}, fixedNow)
	if err != nil {
		t.Fatalf("first advance: %v", err)
	}
	_ = first.ActionIntentIDs[len(first.ActionIntentIDs)-1]
	_ = first.PolicyDecisionID

	// Seed a second event that re-attaches the SAME ratt_ but with a
	// DIFFERENT (act_, pol_) pair. This is the wedge shape — without
	// prevention, the SECOND advance (which naturally mints a fresh pair)
	// would silently write a second attaching event.
	writeActionIntentJSON(t, s, "act_pretend_second", w.ID, fixedNow.Add(time.Minute))
	writePolicyDecisionJSON(t, s, "pol_pretend_second", w.ID, "act_pretend_second", fixedNow.Add(time.Minute))
	writeWedgeLifecycleEvent(t, s, w.ID, "running", "verifying", "hostile re-attach",
		"act_pretend_second", "pol_pretend_second", ra.ID, fixedNow.Add(2*time.Minute))

	// Now attempt the second advance. With prevention in place, this MUST
	// fail at advance time (before state mutation), naming the existing
	// attaching event's pair and the conflict.
	_, err = s.Advance(w.ID, AdvanceInput{
		To: "verifying", Actor: "codex", RunAttemptIDs: []string{ra.ID},
	}, fixedNow.Add(3*time.Minute))
	if err == nil {
		t.Fatal("advance accepted different-pair re-attach; want refusal")
	}
	msg := err.Error()
	for _, want := range []string{ra.ID, "verifying", "hostile re-attach", "remedia"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal error missing %q: %s", want, msg)
		}
	}

	// Belt-and-braces: state must NOT have moved past "running".
	post, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if post.State != "running" {
		t.Fatalf("state moved to %q despite advance refusal; want running", post.State)
	}
}

// Falsifier 3 (idempotent re-attach, GREEN): a re-attach that uses the
// SAME (act_, pol_) pair is allowed (per packet: legitimate same-id
// re-attach patterns stay allowed). This is the EXCEPTION the prevention
// preserves — the resolveRunAttemptAuthorityFromEvents gate collapses
// same-pair reattaches to a single pair because the pairs map dedupes on
// (act_, pol_) equality.
func TestAdvanceAllowsReattachWithSamePair(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "wedge-prevention", SuccessPredicate: "advance allows same-pair re-attach"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(w.ID, "codex", fixedNow); err != nil {
		t.Fatal(err)
	}
	ra := mintExecutionRunAttempt(t, s, w.ID, fixedNow)
	first, err := s.Advance(w.ID, AdvanceInput{
		To: "running", Actor: "codex", RunAttemptIDs: []string{ra.ID},
	}, fixedNow)
	if err != nil {
		t.Fatalf("first advance: %v", err)
	}
	firstAction := first.ActionIntentIDs[len(first.ActionIntentIDs)-1]
	firstPolicy := first.PolicyDecisionID

	// Seed a SECOND lifecycle event attaching the SAME ratt_ with the
	// SAME (act_first, pol_first) pair. The act_ and pol_ envelopes were
	// already written by the first advance (and would collide on
	// overwrite) so we only need to write the event itself.
	writeWedgeLifecycleEvent(t, s, w.ID, "running", "verifying", "same-pair re-link",
		firstAction, firstPolicy, ra.ID, fixedNow.Add(time.Minute))

	// The close-time gate must see exactly ONE pair, not two, even though
	// two events attached it. This proves the same-pair re-link is
	// treated as idempotent by the authority gate.
	got, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	loadedRA, err := runattempt.Open(s.home).Get(ra.ID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.eventsFor(got.ID)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := s.resolveRunAttemptAuthorityFromEvents(got, loadedRA, events)
	if err != nil {
		t.Fatalf("same-pair re-attach must resolve to a single authority: %v", err)
	}
	if authority.ActionIntentID != firstAction || authority.PolicyDecisionID != firstPolicy {
		t.Fatalf("authority = (%s,%s); want (%s,%s)",
			authority.ActionIntentID, authority.PolicyDecisionID, firstAction, firstPolicy)
	}
}
