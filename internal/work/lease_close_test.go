package work

// Close-gate lease enforcement (2026-07-09 packet). Tonight's twin-collisions (see
// docs_private/plan/packets/2026-07-09-close-gate-lease.md) showed leases exist but nothing
// enforces them at close: a non-holder actor could close a work another actor's executor
// was still mid-build on. These tests pin the falsifier list from that packet.

import (
	"os"
	"strings"
	"testing"
)

// Falsifier 1 (RED-first): a work claimed by one actor is closeable by a different actor
// with no steal on today's code. This is the exact regression of tonight's canon_class
// close. On the gated code this must refuse, naming the holder and the remedy paths.
func TestCloseRefusesNonLeaseHolder(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "lane-w", SuccessPredicate: "lease enforced"}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Claim(w.ID, "actor-a", fixedNow); err != nil {
		t.Fatalf("claim: %v", err)
	}
	_, err = s.Close(w.ID, CloseInput{Outcome: "cancelled", MoveType: "audit_only", Actor: "actor-b"}, fixedNow)
	if err == nil {
		t.Fatal("expected close by non-holder actor-b to be refused (work held by actor-a)")
	}
	if !strings.Contains(err.Error(), "actor-a") {
		t.Fatalf("refusal error must name the holder actor-a: %v", err)
	}
	if !strings.Contains(err.Error(), "--steal") {
		t.Fatalf("refusal error must name the --steal remedy: %v", err)
	}
	after, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "claimed" {
		t.Fatalf("refused close must not mutate work state: got %q", after.State)
	}
}

// Falsifier 2: --steal with a non-empty reason succeeds, and the trail (lifecycle events)
// carries the prior holder, the reason string, and the stealing actor. --steal with an
// empty reason refuses exactly like a plain non-holder close.
func TestCloseStealOverridesLeaseHolder(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "lane-w", SuccessPredicate: "steal path"}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Claim(w.ID, "actor-a", fixedNow); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// Empty reason refuses even with --steal set.
	if _, err := s.Close(w.ID, CloseInput{Outcome: "cancelled", MoveType: "audit_only", Actor: "actor-b", Steal: true}, fixedNow); err == nil {
		t.Fatal("expected --steal with empty reason to be refused")
	}
	if got, err := s.Get(w.ID); err != nil || got.State != "claimed" {
		t.Fatalf("empty-reason steal attempt must not mutate work: state=%q err=%v", got.State, err)
	}

	res, err := s.Close(w.ID, CloseInput{
		Outcome:     "cancelled",
		MoveType:    "audit_only",
		Actor:       "actor-b",
		Steal:       true,
		StealReason: "actor-a's executor died mid-build",
	}, fixedNow)
	if err != nil {
		t.Fatalf("expected steal with non-empty reason to succeed: %v", err)
	}
	if res.Work.State != "cancelled" {
		t.Fatalf("stolen close should land the work terminal, got %q", res.Work.State)
	}

	_, evs, err := s.Show(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	var stealNote string
	for _, e := range evs {
		if strings.Contains(e.Note, "stolen") {
			stealNote = e.Note
			if e.By != "actor-b" {
				t.Fatalf("steal event must be attributed to the stealing actor actor-b, got %q", e.By)
			}
		}
	}
	if stealNote == "" {
		t.Fatal("trail missing a steal event")
	}
	if !strings.Contains(stealNote, "actor-a") {
		t.Fatalf("steal event must name the prior holder actor-a: %q", stealNote)
	}
	if !strings.Contains(stealNote, "actor-a's executor died mid-build") {
		t.Fatalf("steal event must carry the reason string: %q", stealNote)
	}
	// P0: a close-steal must never leave a lease still naming the ORIGINAL holder —
	// that is what let actor-a's next unguarded mutation pass leaseHolderStatusFor's
	// holder==actor short-circuit as if the steal had never happened. The steal still
	// transfers the file (recordLeaseSteal, the one write path shared by both gates,
	// and the fail-closed point proven by TestAdvanceStealFailsClosedWhenLeaseTransferWriteFails);
	// since I1, the terminal close then RELEASES that transferred lease, so the P0
	// holds in its strongest form: no lease survives the close at all.
	if _, err := s.readLease(w.ID); err == nil {
		t.Fatal("a terminal close must release the lease it stole, leaving no residue behind")
	}
	if _, err := os.Stat(s.leasePath(w.ID)); !os.IsNotExist(err) {
		t.Fatalf("stolen lease file must be gone after the terminal close: stat err = %v", err)
	}
}

// P0 falsifier, close-path mirror (RED FIRST): actor-a claims; actor-b steals via Advance
// (a different gated verb, proven above to succeed and to transfer the lease); THEN actor-a
// attempts Close WITHOUT --steal. Both gates read the same work/leases/<id>.json through the
// shared leaseHolderStatusFor, so a steal recorded on one verb must be honored by the other —
// otherwise the original holder could route around a steal simply by calling the other
// mutation verb. On the unfixed code this test fails: actor-a's close sails through because
// the lease file was never rewritten by actor-b's advance-steal.
func TestCloseBlockedAfterAdvanceStealTransfersLease(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "lane-w", SuccessPredicate: "cross-verb steal transfer"}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Claim(w.ID, "actor-a", fixedNow); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{
		To:          "running",
		Actor:       "actor-b",
		Steal:       true,
		StealReason: "actor-a's executor died mid-build",
	}, fixedNow); err != nil {
		t.Fatalf("expected actor-b's advance-steal to succeed: %v", err)
	}

	_, err = s.Close(w.ID, CloseInput{Outcome: "completed", MoveType: "audit_only", Actor: "actor-a"}, fixedNow)
	if err == nil {
		t.Fatal("expected actor-a's post-steal close (no --steal) to be BLOCKED: the lease is now actor-b's")
	}
	if !strings.Contains(err.Error(), "actor-b") {
		t.Fatalf("refusal must name the new holder actor-b: %v", err)
	}
	after, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "running" {
		t.Fatalf("actor-a's blocked close must not mutate work state: got %q, want %q", after.State, "running")
	}
}

// Falsifier 3: claimed by A, closed by A — byte-identical behavior to pre-change. No
// refusal/steal events land on the trail beyond the normal claim+close events.
func TestCloseSameActorPassesUnchanged(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "lane-w", SuccessPredicate: "same actor"}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Claim(w.ID, "actor-a", fixedNow); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.Close(w.ID, CloseInput{Outcome: "cancelled", MoveType: "audit_only", Actor: "actor-a"}, fixedNow); err != nil {
		t.Fatalf("same-actor close should succeed: %v", err)
	}
	_, evs, err := s.Show(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantStates := []string{"ready", "claimed", "cancelled"}
	if len(evs) != len(wantStates) {
		t.Fatalf("same-actor close must add no gate events beyond the normal chain: got %d events %+v, want %d", len(evs), evs, len(wantStates))
	}
	for i, want := range wantStates {
		if evs[i].To != want {
			t.Errorf("event[%d].To = %q, want %q", i, evs[i].To, want)
		}
		if strings.Contains(evs[i].Note, "refused") || strings.Contains(evs[i].Note, "stolen") {
			t.Errorf("same-actor close must not add a lease-gate event: %+v", evs[i])
		}
	}
}

// Falsifier 4: an unclaimed (never-claimed) envelope closes exactly as before — fixture
// parity, no lease means no gate interference at all.
func TestCloseUnclaimedWorkUnaffected(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "lane-w", SuccessPredicate: "never claimed"}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Close by an arbitrary actor that never claimed anything — no lease exists at all.
	res, err := s.Close(w.ID, CloseInput{Outcome: "cancelled", MoveType: "audit_only", Actor: "whoever"}, fixedNow)
	if err != nil {
		t.Fatalf("unclaimed close should succeed unconditionally: %v", err)
	}
	if res.Work.State != "cancelled" {
		t.Fatalf("unclaimed close should land the work terminal, got %q", res.Work.State)
	}
	_, evs, err := s.Show(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[0].To != "ready" || evs[1].To != "cancelled" {
		t.Fatalf("unclaimed close must be the plain create+close chain: %+v", evs)
	}
}

// Falsifier 7: a refused close appends a refusal record/event; a second identical refusal
// does not corrupt state — the trail just grows, and Get() stays coherent.
func TestCloseRefusalAppendsIdempotentTrail(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "lane-w", SuccessPredicate: "idempotent refusal"}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Claim(w.ID, "actor-a", fixedNow); err != nil {
		t.Fatalf("claim: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.Close(w.ID, CloseInput{Outcome: "cancelled", MoveType: "audit_only", Actor: "actor-b"}, fixedNow); err == nil {
			t.Fatalf("refusal %d: expected non-holder close to be refused", i)
		}
	}
	after, err := s.Get(w.ID)
	if err != nil {
		t.Fatalf("get after two refusals: %v", err)
	}
	if after.State != "claimed" {
		t.Fatalf("two refusals must leave work state untouched: got %q", after.State)
	}
	_, evs, err := s.Show(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	refusals := 0
	for _, e := range evs {
		if strings.Contains(e.Note, "refused") {
			refusals++
		}
	}
	if refusals != 2 {
		t.Fatalf("expected 2 refusal events on the trail (idempotent-safe, not deduped-away or corrupted), got %d: %+v", refusals, evs)
	}
}

// ClosePlan (the non-mutating preview) must report the same lease-holder denial the real
// close would hit, without recording or mutating anything — the plan surface reports the
// denial too, per the packet's insertion point at close_plan.go.
func TestClosePlanReportsLeaseHolderDenialWithoutMutating(t *testing.T) {
	s := newStore(t)
	workID := readyRunningWork(t, s) // claimed + advanced to running by "codex"
	before, err := s.Get(workID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.ClosePlan(workID, fixedNow, ClosePlanOptions{Actor: "actor-b", Outcome: "completed", MoveType: "control_plane_repair"})
	if err != nil {
		t.Fatalf("close-plan: %v", err)
	}
	if plan.NoAction == "" || !strings.Contains(plan.NoAction, "codex") {
		t.Fatalf("close-plan must report the lease holder denial, got NoAction=%q", plan.NoAction)
	}
	if len(plan.CloseCommand) != 0 {
		t.Fatalf("close-plan must not offer a close command when lease-blocked: %v", plan.CloseCommand)
	}
	after, err := s.Get(workID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != before.State || len(after.ActionIntentIDs) != len(before.ActionIntentIDs) {
		t.Fatalf("close-plan must not mutate work: before=%+v after=%+v", before, after)
	}

	// With a valid steal, the plan reports steal-readiness and the printed close command
	// carries --steal --reason.
	stealPlan, err := s.ClosePlan(workID, fixedNow, ClosePlanOptions{
		Actor: "actor-b", Outcome: "completed", MoveType: "control_plane_repair",
		Steal: true, StealReason: "codex executor is dead",
	})
	if err != nil {
		t.Fatalf("close-plan with steal: %v", err)
	}
	if len(stealPlan.CloseCommand) == 0 {
		t.Fatalf("steal-ready close-plan must offer a close command: %+v", stealPlan)
	}
	if !commandHasFlagValue(stealPlan.CloseCommand, "--reason", "codex executor is dead") {
		t.Fatalf("steal-ready close command must carry --reason: %v", stealPlan.CloseCommand)
	}
	found := false
	for _, part := range stealPlan.CloseCommand {
		if part == "--steal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("steal-ready close command must carry --steal: %v", stealPlan.CloseCommand)
	}
}
