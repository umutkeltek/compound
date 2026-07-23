package work

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/runattempt"
)

var fixedNow = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s
}

// The headline proof for P3: one work_id threads create -> claim -> advance -> close,
// and close emits a Receipt and the apex Transition that BOTH carry that work_id and
// reference the chain (context pack -> runs -> artifacts).
func TestWorkLifecycleThreadsOneID(t *testing.T) {
	s := newStore(t)
	writeContextPack(t, s, "ctx_abc123")
	w, err := s.Create(CreateInput{Owner: "memory-lane", SuccessPredicate: "timeline shows old->new", ContextPackID: "ctx_abc123", DesiredOutcome: "alive memory"}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.State != "ready" {
		t.Fatalf("want ready, got %q", w.State)
	}
	id := w.ID

	if _, err := s.Claim(id, "claude_code", fixedNow); err != nil {
		t.Fatalf("claim: %v", err)
	}
	workRunAttemptID, err := runattempt.Mint(fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(id, AdvanceInput{To: "running", Actor: "claude_code", RunAttemptIDs: []string{workRunAttemptID}, Note: "executing"}, fixedNow); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := s.Advance(id, AdvanceInput{To: "verifying", Actor: "claude_code", ArtifactID: "artifact_timeline", VerificationID: "ver_001"}, fixedNow); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}
	res, err := s.Close(id, CloseInput{Outcome: "completed", MoveType: "memory_delta", Actor: "claude_code", Summary: "timeline capability proven", Changed: true, MemoryPromoted: []string{"claim_timeline"}}, fixedNow)
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	// Every emitted envelope is valid and round-trips through the spine.
	for name, e := range map[string]envelope.Envelope{"work": &res.Work, "receipt": &res.Receipt, "transition": &res.Transition} {
		if err := e.Validate(); err != nil {
			t.Errorf("%s invalid: %v", name, err)
		}
		if err := envelope.RoundTrip(e); err != nil {
			t.Errorf("%s round-trip: %v", name, err)
		}
	}

	// The single work_id threads through receipt and transition.
	if res.Receipt.IDs.WorkID != id {
		t.Errorf("receipt work_id = %q, want %q", res.Receipt.IDs.WorkID, id)
	}
	if res.Transition.IDs.WorkID != id {
		t.Errorf("transition work_id = %q, want %q", res.Transition.IDs.WorkID, id)
	}
	if res.Work.State != "completed" {
		t.Errorf("final state = %q, want completed", res.Work.State)
	}
	if res.Work.IDs.TransitionID != res.Transition.ID {
		t.Errorf("work not linked to its closing transition")
	}

	// The transition references the chain: context pack (before), run (action), artifact+receipt (after).
	if got := res.Transition.BeforeRefs; len(got) != 1 || got[0] != "ctx_abc123" {
		t.Errorf("transition before_refs = %v, want [ctx_abc123]", got)
	}
	if !contains(res.Work.RunAttemptIDs, workRunAttemptID) {
		t.Errorf("work RunAttemptIDs = %v, want it to include %q", res.Work.RunAttemptIDs, workRunAttemptID)
	}
	if contains(res.Work.ActionIntentIDs, "run_001") {
		t.Errorf("ActionIntentIDs must not contain legacy run id after S1; got %v", res.Work.ActionIntentIDs)
	}
	if !contains(res.Transition.AfterRefs, "artifact_timeline") || !contains(res.Transition.AfterRefs, res.Receipt.ID) {
		t.Errorf("transition after_refs = %v, want artifact_timeline + receipt id", res.Transition.AfterRefs)
	}
	if res.Receipt.MoveType != "memory_delta" {
		t.Errorf("receipt move_type = %q", res.Receipt.MoveType)
	}
	if !contains(res.Receipt.Memory.Promoted, "claim_timeline") {
		t.Errorf("memory_delta receipt missing promoted memory proof: %+v", res.Receipt.Memory)
	}

	// The lifecycle log records the full sequence for this work.
	_, evs, err := s.Show(id)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	gotStates := make([]string, len(evs))
	for i, e := range evs {
		gotStates[i] = e.To
	}
	want := []string{"ready", "claimed", "running", "verifying", "completed"}
	if len(gotStates) != len(want) {
		t.Fatalf("event states = %v, want %v", gotStates, want)
	}
	for i := range want {
		if gotStates[i] != want[i] {
			t.Errorf("event[%d] = %q, want %q", i, gotStates[i], want[i])
		}
	}
}

func TestWorkLifecycleKeepsRunAttemptIDsUniqueAcrossAdvanceAndClose(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "codex", SuccessPredicate: "run attempt references stay unique"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(w.ID, "codex", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed.RunAttemptIDs) != 1 {
		t.Fatalf("claim attempts=%v", claimed.RunAttemptIDs)
	}
	runAttemptID := claimed.RunAttemptIDs[0]
	running, err := s.Advance(w.ID, AdvanceInput{To: "running", Actor: "codex", RunAttemptIDs: []string{runAttemptID}}, fixedNow.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if got := running.RunAttemptIDs; len(got) != 1 || got[0] != runAttemptID {
		t.Fatalf("advance duplicated RunAttempt id: %v", got)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "verifying", Actor: "codex"}, fixedNow.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	closed, err := s.Close(w.ID, CloseInput{
		Outcome: "completed", MoveType: "control_plane_repair", Actor: "codex", Summary: "unique run evidence", Changed: true,
		RunAttemptIDs: []string{runAttemptID}, ProofRefs: []string{"test://unique-run-attempt"},
	}, fixedNow.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if got := closed.Work.RunAttemptIDs; len(got) != 1 || got[0] != runAttemptID {
		t.Fatalf("close duplicated RunAttempt id: %v", got)
	}
}

// TestWorkPredictionSurpriseOnClose covers F1: a staked WorkPrediction diffs against the actual
// close and bakes a Receipt.Surprise -- diverged, confirmed (Diverged=false is signal too), and
// entirely absent when no prediction was staked (the marshaled JSON must lack "surprise", never
// serialize a zero-value struct).
func TestWorkPredictionSurpriseOnClose(t *testing.T) {
	lifecycleToVerifying := func(t *testing.T, s *Store, prediction *envelope.WorkPrediction) envelope.WorkEnvelope {
		t.Helper()
		w, err := s.Create(CreateInput{
			Owner:            "memory-lane",
			SuccessPredicate: "surprise diffs correctly",
			Prediction:       prediction,
		}, fixedNow)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := s.Claim(w.ID, "claude_code", fixedNow); err != nil {
			t.Fatalf("claim: %v", err)
		}
		if _, err := s.Advance(w.ID, AdvanceInput{To: "running", Actor: "claude_code"}, fixedNow); err != nil {
			t.Fatalf("advance running: %v", err)
		}
		if _, err := s.Advance(w.ID, AdvanceInput{To: "verifying", Actor: "claude_code"}, fixedNow); err != nil {
			t.Fatalf("advance verifying: %v", err)
		}
		return w
	}

	t.Run("diverged", func(t *testing.T) {
		s := newStore(t)
		w := lifecycleToVerifying(t, s, &envelope.WorkPrediction{Outcome: "completed", MoveType: "rule_delta"})
		res, err := s.Close(w.ID, CloseInput{Outcome: "completed", MoveType: "control_plane_repair", Actor: "claude_code", ProofRefs: []string{"check:proof"}}, fixedNow)
		if err != nil {
			t.Fatalf("close: %v", err)
		}
		surprise := res.Receipt.Surprise
		if surprise == nil {
			t.Fatal("want non-nil Surprise on a diverged close")
		}
		if !surprise.Diverged {
			t.Error("want Diverged=true")
		}
		if surprise.PredictedOutcome != "completed" || surprise.PredictedMoveType != "rule_delta" {
			t.Errorf("predicted = %s/%s, want completed/rule_delta", surprise.PredictedOutcome, surprise.PredictedMoveType)
		}
		if surprise.ActualOutcome != "completed" || surprise.ActualMoveType != "control_plane_repair" {
			t.Errorf("actual = %s/%s, want completed/control_plane_repair", surprise.ActualOutcome, surprise.ActualMoveType)
		}
	})

	t.Run("confirmed", func(t *testing.T) {
		s := newStore(t)
		w := lifecycleToVerifying(t, s, &envelope.WorkPrediction{Outcome: "completed", MoveType: "rule_delta"})
		res, err := s.Close(w.ID, CloseInput{Outcome: "completed", MoveType: "rule_delta", Actor: "claude_code", ProofRefs: []string{"check:proof"}}, fixedNow)
		if err != nil {
			t.Fatalf("close: %v", err)
		}
		surprise := res.Receipt.Surprise
		if surprise == nil {
			t.Fatal("want non-nil Surprise even on a matching close (confirmations are signal too)")
		}
		if surprise.Diverged {
			t.Error("want Diverged=false on a matching close")
		}
		if surprise.PredictedOutcome != surprise.ActualOutcome || surprise.PredictedMoveType != surprise.ActualMoveType {
			t.Errorf("matching close fields should be equal: %+v", surprise)
		}
	})

	t.Run("no prediction staked", func(t *testing.T) {
		s := newStore(t)
		w := lifecycleToVerifying(t, s, nil)
		res, err := s.Close(w.ID, CloseInput{Outcome: "completed", MoveType: "outward_delta", Actor: "claude_code", Changed: true}, fixedNow)
		if err != nil {
			t.Fatalf("close: %v", err)
		}
		if res.Receipt.Surprise != nil {
			t.Fatalf("want nil Surprise when no prediction staked, got %+v", res.Receipt.Surprise)
		}
		raw, err := json.Marshal(res.Receipt)
		if err != nil {
			t.Fatalf("marshal receipt: %v", err)
		}
		if strings.Contains(string(raw), "surprise") {
			t.Fatalf("marshaled receipt JSON must omit surprise entirely when unset: %s", raw)
		}
	})
}

func TestIllegalTransitionRejected(t *testing.T) {
	s := newStore(t)
	w, _ := s.Create(CreateInput{Owner: "o", SuccessPredicate: "p"}, fixedNow)
	// ready cannot jump straight to completed.
	if _, err := s.Advance(w.ID, AdvanceInput{To: "completed"}, fixedNow); err == nil {
		t.Error("expected illegal transition ready->completed to be rejected")
	}
	// cannot close from ready either (no edge to a terminal except via cancel).
	if _, err := s.Close(w.ID, CloseInput{Outcome: "completed", MoveType: "outward_delta"}, fixedNow); err == nil {
		t.Error("expected close ready->completed to be rejected")
	}
	// ready CAN be cancelled.
	if _, err := s.Close(w.ID, CloseInput{Outcome: "cancelled", MoveType: "audit_only"}, fixedNow); err != nil {
		t.Errorf("ready->cancelled should be allowed: %v", err)
	}
}

func TestClaimConflict(t *testing.T) {
	s := newStore(t)
	w, _ := s.Create(CreateInput{Owner: "o", SuccessPredicate: "p"}, fixedNow)
	beforeRuns := len(w.ActionIntentIDs)
	claimed, err := s.Claim(w.ID, "agent_a", fixedNow)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if claimed.PolicyDecisionID == "" || len(claimed.ActionIntentIDs) != beforeRuns+1 {
		t.Fatalf("claim must record policy/action chain: runs=%v policy=%q", claimed.ActionIntentIDs, claimed.PolicyDecisionID)
	}
	if exists, err := s.eventExists(claimed.ActionIntentIDs[beforeRuns]); err != nil || !exists {
		t.Fatalf("claim action envelope missing: %v", err)
	}
	if exists, err := s.eventExists(claimed.PolicyDecisionID); err != nil || !exists {
		t.Fatalf("claim policy envelope missing: %v", err)
	}
	if _, err := s.Claim(w.ID, "agent_b", fixedNow); err == nil {
		t.Error("expected second claimant to be rejected (claim before you touch)")
	}
	// Same actor re-claims idempotently.
	reclaimed, err := s.Claim(w.ID, "agent_a", fixedNow)
	if err != nil {
		t.Errorf("idempotent re-claim by same actor should succeed: %v", err)
	}
	if len(reclaimed.ActionIntentIDs) != len(claimed.ActionIntentIDs) || reclaimed.PolicyDecisionID != claimed.PolicyDecisionID {
		t.Errorf("idempotent re-claim should not append policy/action records: before=%+v after=%+v", claimed, reclaimed)
	}
}

// F2 (T1): claim mints a claim_reservation RunAttempt. When a
// harness_execution attempt attaches via a later Advance, the still-pending
// claim-mint attempt must be superseded (terminalized abandoned, reason
// naming the exec attempt); the exec attempt itself is untouched. Red at
// base (claim-mint attempt stays pending forever), green after
// supersede-at-attach lands.
func TestAdvanceSupersedesClaimReservationOnHarnessExecutionAttach(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "o", SuccessPredicate: "p"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(w.ID, "agent_a", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed.RunAttemptIDs) != 1 {
		t.Fatalf("claim must mint exactly one RunAttempt, got %v", claimed.RunAttemptIDs)
	}
	claimAttemptID := claimed.RunAttemptIDs[0]

	// Simulate a real harness-execution mint (subprocess_runner's shape):
	// mint + stamp origin=harness_execution.
	store := runattempt.Open(s.home)
	execAttempt, err := store.Mint(w.ID, "agent_a", "claude-code", fixedNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetOrigin(execAttempt.ID, runattempt.OriginHarnessExecution); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Advance(w.ID, AdvanceInput{To: "running", Actor: "agent_a", RunAttemptIDs: []string{execAttempt.ID}, Note: "executing"}, fixedNow.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	claimRA, err := store.Get(claimAttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if claimRA.Status != runattempt.StatusAbandoned {
		t.Fatalf("claim-mint attempt status = %q, want abandoned", claimRA.Status)
	}
	if !strings.Contains(claimRA.SweepReason, execAttempt.ID) {
		t.Fatalf("supersede reason must name the exec attempt %q, got %q", execAttempt.ID, claimRA.SweepReason)
	}
	execRA, err := store.Get(execAttempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if execRA.Status != runattempt.StatusPending {
		t.Fatalf("exec attempt must be untouched by supersede, status = %q, want pending", execRA.Status)
	}
}

// F2 (T2): a legacy RunAttempt with empty Origin already on the envelope
// must NOT be touched by supersede-at-attach -- only claim_reservation
// origin attempts are superseded. Preservation falsifier (green throughout;
// proves the "legacy attempts are never touched" half of decision 2).
func TestAdvanceDoesNotSupersedeLegacyOriginlessAttempt(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "o", SuccessPredicate: "p"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(w.ID, "agent_a", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	claimAttemptID := claimed.RunAttemptIDs[0]

	// Simulate a pre-F2 legacy record by stripping "origin" from the raw
	// on-disk JSON (there is deliberately no setter to clear Origin back to
	// empty -- provenance is stamped once, not rewritten).
	legacyPath := filepath.Join(s.home, "run_attempts", claimAttemptID+".json")
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	delete(raw, "origin")
	rewritten, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, rewritten, 0o644); err != nil {
		t.Fatal(err)
	}

	store := runattempt.Open(s.home)
	execAttempt, err := store.Mint(w.ID, "agent_a", "claude-code", fixedNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetOrigin(execAttempt.ID, runattempt.OriginHarnessExecution); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Advance(w.ID, AdvanceInput{To: "running", Actor: "agent_a", RunAttemptIDs: []string{execAttempt.ID}, Note: "executing"}, fixedNow.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	legacyRA, err := runattempt.Open(s.home).Get(claimAttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if legacyRA.Status != runattempt.StatusPending {
		t.Fatalf("legacy origin-less attempt must NOT be superseded, status = %q, want pending", legacyRA.Status)
	}
}

func TestListLeasesProjectsClaimedWork(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "operator", SuccessPredicate: "project lease"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(w.ID, "codex", fixedNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	leases, err := s.ListLeases()
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 {
		t.Fatalf("leases = %+v, want one", leases)
	}
	if leases[0].WorkID != w.ID || leases[0].By != "codex" || leases[0].ClaimAt == "" {
		t.Fatalf("lease projection = %+v", leases[0])
	}
}

func TestClaimBlockedByPolicyDoesNotMutate(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "o", SuccessPredicate: "p"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	baselineRuns := append([]string{}, w.ActionIntentIDs...)
	baselinePolicy := w.PolicyDecisionID
	if err := os.MkdirAll(filepath.Join(s.home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	policyYAML := []byte(`class_outcome:
  local_reversible:
    outcome: hold
`)
	if err := os.WriteFile(filepath.Join(s.home, "config", "policy.yaml"), policyYAML, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(w.ID, "agent_a", fixedNow); err == nil {
		t.Fatal("claim should be blocked by policy hold")
	}
	after, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "ready" || len(after.ActionIntentIDs) != len(baselineRuns) || after.PolicyDecisionID != baselinePolicy {
		t.Fatalf("blocked claim mutated work: %+v", after)
	}
	if _, err := s.readLease(w.ID); err == nil {
		t.Fatal("blocked claim must not write lease")
	}
}

func TestCreateRequiresOwnerAndPredicate(t *testing.T) {
	s := newStore(t)
	if _, err := s.Create(CreateInput{Owner: "", SuccessPredicate: "p"}, fixedNow); err == nil {
		t.Error("create without owner should fail")
	}
	if _, err := s.Create(CreateInput{Owner: "o", SuccessPredicate: ""}, fixedNow); err == nil {
		t.Error("create without success_predicate should fail")
	}
}

func TestTerminalCannotAdvance(t *testing.T) {
	s := newStore(t)
	w, _ := s.Create(CreateInput{Owner: "o", SuccessPredicate: "p"}, fixedNow)
	if _, err := s.Close(w.ID, CloseInput{Outcome: "cancelled", MoveType: "audit_only"}, fixedNow); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "running"}, fixedNow); err == nil {
		t.Error("advancing a terminal work should fail")
	}
}

func TestClosePreservesExplicitCapabilityFeedback(t *testing.T) {
	s := newStore(t)
	w, _ := s.Create(CreateInput{Owner: "o", SuccessPredicate: "p"}, fixedNow)
	if _, err := s.Claim(w.ID, "codex", fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "running"}, fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "verifying"}, fixedNow); err != nil {
		t.Fatal(err)
	}

	closed, err := s.Close(w.ID, CloseInput{
		Outcome:                    "failed",
		MoveType:                   "outward_delta",
		Summary:                    "pricing capability feedback",
		Changed:                    true,
		UsedCapabilities:           []string{"skill:golang-testing", "not-a-capability-node"},
		MissedCapabilities:         []string{"skill:pricing"},
		FalseTriggeredCapabilities: []string{"capability://skill:generic-copy"},
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(closed.Receipt.Skills.Missed, "capability://skill:pricing") {
		t.Fatalf("receipt missed skills = %v", closed.Receipt.Skills.Missed)
	}
	if !contains(closed.Receipt.Skills.FalseTriggered, "capability://skill:generic-copy") {
		t.Fatalf("receipt false_triggered skills = %v", closed.Receipt.Skills.FalseTriggered)
	}
	if !contains(closed.Receipt.Skills.Used, "capability://skill:golang-testing") {
		t.Fatalf("receipt used skills = %v", closed.Receipt.Skills.Used)
	}
	if contains(closed.Receipt.Skills.Used, "not-a-capability-node") {
		t.Fatalf("non graph node leaked into used skills: %v", closed.Receipt.Skills.Used)
	}
	if !containsProjection(closed.Transition.Projections.Typed, "skill", "capability://skill:pricing", "missed") {
		t.Fatalf("transition typed projections missing missed skill: %+v", closed.Transition.Projections.Typed)
	}
	if !containsProjection(closed.Transition.Projections.Typed, "skill", "capability://skill:generic-copy", "false_triggered") {
		t.Fatalf("transition typed projections missing false-triggered skill: %+v", closed.Transition.Projections.Typed)
	}
	if !containsProjection(closed.Transition.Projections.Typed, "skill", "capability://skill:golang-testing", "used") {
		t.Fatalf("transition typed projections missing used skill: %+v", closed.Transition.Projections.Typed)
	}
}

// The store enforces governance structurally: a denied/held decision cannot be recorded
// as a performed action; an allowing one links the action + policy into the work chain.
func TestContextPackOffersAreTypedAsOfferedNotUsed(t *testing.T) {
	s := newStore(t)
	pack := envelope.ContextPack{
		Header: envelope.Header{
			SchemaVersion: envelope.SVContextPack,
			ID:            "ctx_offers",
			CreatedAt:     fixedNow.Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "test", Name: "work"},
			Authority:     envelope.Authority{SourceClass: "runtime"},
			Privacy:       "private",
			Risk:          "tier1",
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Included: []envelope.IncludedRef{},
		Excluded: []envelope.ExcludedRef{},
		CapabilityOffers: []envelope.CapabilityOffer{{
			Ref:  "capability://skill:pricing",
			Kind: "skill",
			Name: "Pricing",
		}},
	}
	packPath := filepath.Join(s.home, "context", "packs", pack.ID+".json")
	if err := os.MkdirAll(filepath.Dir(packPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.writeJSON(packPath, &pack); err != nil {
		t.Fatal(err)
	}
	w, _ := s.Create(CreateInput{Owner: "o", SuccessPredicate: "p", ContextPackID: pack.ID}, fixedNow)
	if _, err := s.Claim(w.ID, "codex", fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "running"}, fixedNow); err != nil {
		t.Fatal(err)
	}
	closed, err := s.Close(w.ID, CloseInput{Outcome: "completed", MoveType: "outward_delta", Summary: "handoff proof", ProofRefs: []string{"artifact:handoff"}}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(closed.Receipt.Skills.Candidate, "capability://skill:pricing") {
		t.Fatalf("receipt candidate/offers = %v", closed.Receipt.Skills.Candidate)
	}
	if !contains(closed.Receipt.Skills.CapabilitiesOffered, "capability://skill:pricing") {
		t.Fatalf("receipt capabilities_offered = %v", closed.Receipt.Skills.CapabilitiesOffered)
	}
	if contains(closed.Receipt.Skills.Used, "capability://skill:pricing") {
		t.Fatalf("offered capability must not count as used: %v", closed.Receipt.Skills.Used)
	}
	if !containsProjection(closed.Transition.Projections.Typed, "skill", "capability://skill:pricing", "offered") {
		t.Fatalf("offer should be typed as offered: %+v", closed.Transition.Projections.Typed)
	}
	if containsProjection(closed.Transition.Projections.Typed, "skill", "capability://skill:pricing", "used") {
		t.Fatalf("offer must not be typed as used: %+v", closed.Transition.Projections.Typed)
	}
}

// TestCloseStampsUsedDeclaredAndConsumedRefs proves the close-receipt honesty stamp:
// UsedDeclared flows onto the emitted receipt, and raw ConsumedRefs (a claim:// injected
// ref, NOT a capability) land verbatim in Skills.Used (so the usage projection can score
// them consumed) WITHOUT masquerading as a used skill in the typed projections.
func TestCloseStampsUsedDeclaredAndConsumedRefs(t *testing.T) {
	const claimRef = "claim://claim_seed01"

	mkClosable := func(s *Store, pred string) envelope.WorkEnvelope {
		w, err := s.Create(CreateInput{Owner: "o", SuccessPredicate: pred}, fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Claim(w.ID, "codex", fixedNow); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Advance(w.ID, AdvanceInput{To: "running"}, fixedNow); err != nil {
			t.Fatal(err)
		}
		return w
	}

	// Declared close carrying a raw consumed claim ref.
	s := newStore(t)
	w := mkClosable(s, "declared-consumed")
	closed, err := s.Close(w.ID, CloseInput{
		Outcome: "completed", MoveType: "outward_delta", Summary: "shipped", Changed: true,
		UsedDeclared: true, ConsumedRefs: []string{claimRef},
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !closed.Receipt.Skills.UsedDeclared {
		t.Fatalf("declared close must stamp used_declared=true, got receipt skills %+v", closed.Receipt.Skills)
	}
	if !contains(closed.Receipt.Skills.Used, claimRef) {
		t.Fatalf("consumed claim ref must land verbatim in Skills.Used: %v", closed.Receipt.Skills.Used)
	}
	// Purity: the claim ref is a reinforcement signal, not a used SKILL.
	if containsProjection(closed.Transition.Projections.Typed, "skill", claimRef, "used") {
		t.Fatalf("consumed claim ref must not be typed as a used skill: %+v", closed.Transition.Projections.Typed)
	}
	// Round-trip through persistence: the on-disk receipt carries the flag and the ref.
	persisted := readReceiptFromEvents(t, s, closed.Receipt.ID)
	if !persisted.Skills.UsedDeclared || !contains(persisted.Skills.Used, claimRef) {
		t.Fatalf("persisted receipt lost declared/consumed signal: %+v", persisted.Skills)
	}

	// Undeclared close (default): no flag, no consumed refs.
	s2 := newStore(t)
	w2 := mkClosable(s2, "undeclared")
	closed2, err := s2.Close(w2.ID, CloseInput{Outcome: "completed", MoveType: "outward_delta", Summary: "shipped", Changed: true}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if closed2.Receipt.Skills.UsedDeclared {
		t.Fatalf("undeclared close must leave used_declared=false, got %+v", closed2.Receipt.Skills)
	}
	if contains(closed2.Receipt.Skills.Used, claimRef) {
		t.Fatalf("undeclared close must not carry consumed refs: %v", closed2.Receipt.Skills.Used)
	}
}

func readReceiptFromEvents(t *testing.T, s *Store, recID string) envelope.Receipt {
	t.Helper()
	var rec envelope.Receipt
	if err := s.readJSON(s.envPath(recID), &rec); err != nil {
		t.Fatalf("read persisted receipt %s: %v", recID, err)
	}
	return rec
}

func TestRecordActionGate(t *testing.T) {
	s := newStore(t)
	w, _ := s.Create(CreateInput{Owner: "o", SuccessPredicate: "p"}, fixedNow)
	if _, err := s.Claim(w.ID, "claude_code", fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "running", Actor: "claude_code"}, fixedNow); err != nil {
		t.Fatal(err)
	}
	baseline, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	baselineRuns := append([]string{}, baseline.ActionIntentIDs...)
	baselinePolicy := baseline.PolicyDecisionID

	mkAI := func(id string) envelope.ActionIntent {
		return envelope.ActionIntent{
			Header: envelope.Header{
				SchemaVersion: envelope.SVActionIntent, ID: id, CreatedAt: fixedNow.Format(time.RFC3339),
				Producer: envelope.Producer{Kind: "hook", Name: "compound"}, IDs: envelope.IDs{WorkID: w.ID},
				Authority: envelope.Authority{SourceClass: "runtime"}, Privacy: "private", Risk: "tier1",
				EvidenceRefs: []string{}, Uncertainty: envelope.Uncertainty{Level: "known"},
			},
			Capability: "capability://skill:filesystem-write", Action: "write_file",
			Target:        envelope.ActionTarget{Kind: "file", Path: "docs/x.md"},
			MutationClass: "local_reversible", Reversibility: "easy",
		}
	}
	mkPD := func(id, aiID, outcome string, obl []string) envelope.PolicyDecision {
		return envelope.PolicyDecision{
			Header: envelope.Header{
				SchemaVersion: envelope.SVPolicyDecision, ID: id, CreatedAt: fixedNow.Format(time.RFC3339),
				Producer: envelope.Producer{Kind: "cli", Name: "compound"}, IDs: envelope.IDs{WorkID: w.ID},
				Authority: envelope.Authority{SourceClass: "runtime"}, Privacy: "private", Risk: "tier1",
				EvidenceRefs: []string{}, Uncertainty: envelope.Uncertainty{Level: "known"},
			},
			ActionIntentID: aiID, Outcome: outcome, Obligations: obl,
		}
	}

	// Denied action is refused and NOT recorded.
	denyAI := mkAI("act_deny")
	if _, err := s.RecordAction(w.ID, denyAI, mkPD("pol_deny", "act_deny", "deny", nil), fixedNow); err == nil {
		t.Error("denied action should be refused")
	}
	w2, _ := s.Get(w.ID)
	if len(w2.ActionIntentIDs) != len(baselineRuns) || w2.PolicyDecisionID != baselinePolicy || contains(w2.ActionIntentIDs, "act_deny") {
		t.Errorf("denied action must not be recorded; runs=%v policy=%q", w2.ActionIntentIDs, w2.PolicyDecisionID)
	}
	// Held action is also refused.
	if _, err := s.RecordAction(w.ID, denyAI, mkPD("pol_hold", "act_deny", "hold", nil), fixedNow); err == nil {
		t.Error("held action should be refused")
	}
	// Allowing decision records the action + links policy into the work.
	okAI := mkAI("act_ok")
	wOK, err := s.RecordAction(w.ID, okAI, mkPD("pol_ok", "act_ok", "allow_with_obligations", []string{"emit a Receipt"}), fixedNow)
	if err != nil {
		t.Fatalf("allowed action should record: %v", err)
	}
	if !contains(wOK.ActionIntentIDs, "act_ok") || wOK.PolicyDecisionID != "pol_ok" {
		t.Errorf("allowed action should be linked; runs=%v policy=%q", wOK.ActionIntentIDs, wOK.PolicyDecisionID)
	}
	// Mismatched action/decision is rejected.
	if _, err := s.RecordAction(w.ID, okAI, mkPD("pol_x", "act_other", "allow", nil), fixedNow); err == nil {
		t.Error("decision for a different action should be rejected")
	}
	closed, err := s.Close(w.ID, CloseInput{Outcome: "completed", MoveType: "outward_delta", Actor: "claude_code", Summary: "wrote file", Changed: true}, fixedNow)
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if !contains(closed.Receipt.Skills.Used, "capability://skill:filesystem-write") {
		t.Fatalf("receipt used skills = %v, want capability://skill:filesystem-write", closed.Receipt.Skills.Used)
	}
	if contains(closed.Receipt.Skills.Used, "filesystem.delete") {
		t.Fatalf("denied capability leaked into used skills: %v", closed.Receipt.Skills.Used)
	}
	if contains(closed.Receipt.Skills.Used, "work.claim") {
		t.Fatalf("internal claim capability leaked into used skills: %v", closed.Receipt.Skills.Used)
	}
	if contains(closed.Receipt.Skills.Used, "work.create") {
		t.Fatalf("internal create capability leaked into used skills: %v", closed.Receipt.Skills.Used)
	}
	if len(closed.Receipt.Skills.Candidate) != 0 {
		t.Fatalf("no context pack offers in this test, got candidates %v", closed.Receipt.Skills.Candidate)
	}
	if !contains(closed.Transition.Projections.Skill, "capability://skill:filesystem-write") {
		t.Fatalf("transition skill projections = %v, want capability://skill:filesystem-write", closed.Transition.Projections.Skill)
	}
	if !containsProjection(closed.Transition.Projections.Typed, "skill", "capability://skill:filesystem-write", "used") {
		t.Fatalf("transition typed projections = %+v, want used capability://skill:filesystem-write", closed.Transition.Projections.Typed)
	}
}

func TestCloseInfersChangedForCompletedNonAuditMoveTypes(t *testing.T) {
	tests := []struct {
		name        string
		in          CloseInput
		wantChanged bool
	}{
		{
			name:        "completed control plane repair",
			in:          CloseInput{Outcome: "completed", MoveType: "control_plane_repair", Summary: "repair landed", ProofRefs: []string{"proof:repair"}},
			wantChanged: true,
		},
		{
			name:        "completed rule delta",
			in:          CloseInput{Outcome: "completed", MoveType: "rule_delta", Summary: "guard landed", ProofRefs: []string{"guard:repeat-failure"}},
			wantChanged: true,
		},
		{
			name:        "completed outward delta",
			in:          CloseInput{Outcome: "completed", MoveType: "outward_delta", Summary: "handoff landed", ProofRefs: []string{"artifact:handoff"}},
			wantChanged: true,
		},
		{
			name:        "completed memory delta",
			in:          CloseInput{Outcome: "completed", MoveType: "memory_delta", Summary: "memory promoted", MemoryPromoted: []string{"claim_1"}},
			wantChanged: true,
		},
		{
			name:        "completed audit",
			in:          CloseInput{Outcome: "completed", MoveType: "audit_only", Summary: "observed only"},
			wantChanged: false,
		},
		{
			name:        "failed repair",
			in:          CloseInput{Outcome: "failed", MoveType: "control_plane_repair", Summary: "repair failed", ProofRefs: []string{"proof:repair"}},
			wantChanged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			w, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: tt.name}, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Claim(w.ID, "codex", fixedNow); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Advance(w.ID, AdvanceInput{To: "running"}, fixedNow); err != nil {
				t.Fatal(err)
			}
			res, err := s.Close(w.ID, tt.in, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			if res.Receipt.StateDelta.Changed != tt.wantChanged {
				t.Fatalf("changed = %v, want %v; receipt=%+v", res.Receipt.StateDelta.Changed, tt.wantChanged, res.Receipt)
			}
		})
	}
}

func TestCloseMoveTypeProofValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("fsync-heavy: skipped in -short mode")
	}
	tests := []struct {
		name    string
		in      CloseInput
		wantErr bool
	}{
		{name: "memory needs memory proof", in: CloseInput{Outcome: "completed", MoveType: "memory_delta", Changed: true}, wantErr: true},
		{name: "memory with promoted proof", in: CloseInput{Outcome: "completed", MoveType: "memory_delta", Changed: true, MemoryPromoted: []string{"claim_1"}}},
		{name: "rule needs rule proof", in: CloseInput{Outcome: "completed", MoveType: "rule_delta", Changed: true, ProofRefs: []string{"artifact:not-rule"}}, wantErr: true},
		{name: "rule with guard proof", in: CloseInput{Outcome: "completed", MoveType: "rule_delta", Changed: true, ProofRefs: []string{"guard:repeat-failure"}}},
		{name: "control repair needs proof", in: CloseInput{Outcome: "completed", MoveType: "control_plane_repair", Changed: true}, wantErr: true},
		{name: "control repair with gate proof", in: CloseInput{Outcome: "completed", MoveType: "control_plane_repair", Changed: true, ProofRefs: []string{"owg_gate"}}},
		{name: "outward completed needs change or proof", in: CloseInput{Outcome: "completed", MoveType: "outward_delta"}, wantErr: true},
		{name: "outward with blocker handoff proof", in: CloseInput{Outcome: "completed", MoveType: "outward_delta", ProofRefs: []string{"artifact:handoff"}}},
		{name: "audit does not claim future behavior", in: CloseInput{Outcome: "completed", MoveType: "audit_only", Changed: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			w, _ := s.Create(CreateInput{Owner: "o", SuccessPredicate: "p"}, fixedNow)
			if _, err := s.Claim(w.ID, "codex", fixedNow); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Advance(w.ID, AdvanceInput{To: "running"}, fixedNow); err != nil {
				t.Fatal(err)
			}
			res, err := s.Close(w.ID, tt.in, fixedNow)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected close error")
				}
				return
			}
			if err != nil {
				t.Fatalf("close: %v", err)
			}
			if tt.in.MoveType == "audit_only" && res.Transition.SecondOrder.FutureBehaviorChanged {
				t.Fatalf("audit_only transition claimed future behavior changed")
			}
		})
	}
}

func TestCloseEnforcesPacketProofMedium(t *testing.T) {
	store := newStore(t)
	packetPath := filepath.Join(t.TempDir(), "visual.md")
	if err := os.WriteFile(packetPath, []byte(`# Visual packet
Date: 2026-07-02
Status: ready
Owner: codex
Proof-medium: web-ui
Success: close requires visual proof.

## Objective

Build a web UI.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := store.ImportPacket(packetPath, PacketImportOptions{State: "ready", Actor: "codex"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	workID := report.Work.ID
	if report.Work.ProofMedium != "web-ui" {
		t.Fatalf("proof medium = %q", report.Work.ProofMedium)
	}
	if _, err := store.Claim(workID, "codex", fixedNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(workID, AdvanceInput{To: "running"}, fixedNow.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(workID, AdvanceInput{To: "verifying"}, fixedNow.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Close(workID, CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "visual work",
		Changed:   true,
		ProofRefs: []string{"test:unit"},
	}, fixedNow.Add(4*time.Second)); err == nil || !strings.Contains(err.Error(), "proof_medium web-ui") {
		t.Fatalf("close err = %v, want proof_medium web-ui gate", err)
	}
	closed, err := store.Close(workID, CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "visual work",
		Changed:   true,
		ProofRefs: []string{"artifact:screenshot-home", "design-review:passed"},
	}, fixedNow.Add(5*time.Second))
	if err != nil {
		t.Fatalf("close with visual proof: %v", err)
	}
	if closed.Work.State != "completed" {
		t.Fatalf("state = %q", closed.Work.State)
	}
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

func containsProjection(set []envelope.ProjectionRef, kind, ref, role string) bool {
	for _, p := range set {
		if p.Kind == kind && p.Ref == ref && p.Role == role {
			return true
		}
	}
	return false
}
