package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/mem"
)

func TestRecordReceiptedActionKeepsParentOpenAndIsStateIdempotent(t *testing.T) {
	s := newStore(t)
	w := receiptedActionRunningWork(t, s)
	claimID := receiptedActionRuling(t, s, "receipted-action-ruling", "", fixedNow)
	action := receiptedActionIntent(w, "act_receipted_stop", fixedNow.Add(time.Minute))

	result, err := s.RecordReceiptedAction(w.ID, ReceiptedActionInput{
		Action:                     action,
		OperationalDecisionClaimID: claimID,
		ExpectedWorkState:          "running",
		Summary:                    "continued the exact Codex turn with a current standing-law answer",
		MoveType:                   "control_plane_repair",
		ProofRefs:                  []string{"claim:" + claimID, "session:sess_receipted", "turn:turn_receipted"},
	}, fixedNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("record receipted action: %v", err)
	}
	if result.Replayed {
		t.Fatal("first receipted action reported replay")
	}
	if result.Work.State != "running" || result.Work.IDs.TransitionID != "" {
		t.Fatalf("nonterminal action claimed task closure: %+v", result.Work)
	}
	if result.Action.IDs.WorkID != w.ID || result.Decision.IDs.WorkID != w.ID ||
		result.Receipt.IDs.WorkID != w.ID || result.Transition.IDs.WorkID != w.ID {
		t.Fatalf("receipted action broke one-work-id chain: %+v", result)
	}
	if result.Decision.ActionIntentID != result.Action.ID ||
		!contains(result.Transition.ActionRefs, result.Action.ID) ||
		!contains(result.Transition.AfterRefs, result.Receipt.ID) {
		t.Fatalf("receipted action links incomplete: %+v", result)
	}

	_, events, err := s.Show(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	for _, id := range []string{result.Action.ID, result.Decision.ID, result.Receipt.ID, result.Transition.ID} {
		if !contains(last.Attached, id) {
			t.Fatalf("same-state event missing %s: %+v", id, last)
		}
	}
	doctor, err := s.ProofChainDoctor(fixedNow.Add(2*time.Minute), ProofChainDoctorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range doctor.Findings {
		if strings.Contains(finding.Message, result.Receipt.ID) || strings.Contains(finding.Message, result.Transition.ID) {
			t.Fatalf("receipted nonterminal action is orphaned: %+v", doctor)
		}
	}

	paths := []string{
		s.envPath(result.Action.ID),
		s.envPath(result.Decision.ID),
		s.envPath(result.Receipt.ID),
		s.envPath(result.Transition.ID),
	}
	before := map[string]string{}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = string(body)
	}
	replay, err := s.RecordReceiptedAction(w.ID, ReceiptedActionInput{
		Action:                     action,
		OperationalDecisionClaimID: claimID,
		ExpectedWorkState:          "running",
		Summary:                    "continued the exact Codex turn with a current standing-law answer",
		MoveType:                   "control_plane_repair",
		ProofRefs:                  []string{"claim:" + claimID, "session:sess_receipted", "turn:turn_receipted"},
	}, fixedNow.Add(10*time.Minute))
	if err != nil || !replay.Replayed || replay.Receipt.ID != result.Receipt.ID || replay.Transition.ID != result.Transition.ID {
		t.Fatalf("idempotent replay failed: err=%v result=%+v", err, replay)
	}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != before[path] {
			t.Fatalf("replay rewrote %s", filepath.Base(path))
		}
	}
}

func TestRecordReceiptedActionSerializesDecisionRevocation(t *testing.T) {
	s := newStore(t)
	w := receiptedActionRunningWork(t, s)
	const decisionID = "receipted-action-race"
	claimID := receiptedActionRuling(t, s, decisionID, "", fixedNow)
	action := receiptedActionIntent(w, "act_receipted_race", fixedNow.Add(time.Minute))
	in := ReceiptedActionInput{
		Action:                     action,
		OperationalDecisionClaimID: claimID,
		ExpectedWorkState:          "running",
		Summary:                    "continue under the current ruling",
		MoveType:                   "control_plane_repair",
		ProofRefs:                  []string{"claim:" + claimID},
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	previousHook := receiptedActionBeforeCommit
	receiptedActionBeforeCommit = func() {
		close(entered)
		<-release
	}
	defer func() { receiptedActionBeforeCommit = previousHook }()

	type actionOutcome struct {
		result ReceiptedActionResult
		err    error
	}
	actionDone := make(chan actionOutcome, 1)
	go func() {
		result, err := s.RecordReceiptedAction(w.ID, in, fixedNow.Add(time.Minute))
		actionDone <- actionOutcome{result: result, err: err}
	}()
	<-entered

	revokeDone := make(chan error, 1)
	go func() {
		_, err := receiptedActionRecordRuling(s, "receipted-action-race-revoked", decisionID, fixedNow.Add(2*time.Minute))
		revokeDone <- err
	}()
	select {
	case err := <-revokeDone:
		t.Fatalf("revocation crossed the receipted-action admission lock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	first := <-actionDone
	if first.err != nil || first.result.Receipt.ID == "" {
		t.Fatalf("action holding the lock should linearize first: err=%v result=%+v", first.err, first.result)
	}
	if err := <-revokeDone; err != nil {
		t.Fatal(err)
	}
	if replay, err := s.RecordReceiptedAction(w.ID, in, fixedNow.Add(3*time.Minute)); err == nil || replay.Replayed || !strings.Contains(err.Error(), "not a current authoritative operational decision") {
		t.Fatalf("revoked ruling authorized replay: err=%v result=%+v", err, replay)
	}
}

func receiptedActionRunningWork(t *testing.T, s *Store) envelope.WorkEnvelope {
	t.Helper()
	w, err := s.Create(CreateInput{Owner: "codex", SuccessPredicate: "parent task remains open", Risk: "tier1", Privacy: "private"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(w.ID, "codex", fixedNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	w, err = s.Advance(w.ID, AdvanceInput{To: "running", Actor: "codex"}, fixedNow.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func receiptedActionIntent(w envelope.WorkEnvelope, id string, now time.Time) envelope.ActionIntent {
	return envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            id,
			CreatedAt:     now.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "hook", Name: "compound", LaneID: "codex"},
			IDs:           envelope.IDs{WorkID: w.ID, SessionID: "sess_receipted", ContextPackID: w.IDs.ContextPackID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{"session:sess_receipted", "turn:turn_receipted"},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         "codex",
		Capability:    "harness.stop_feedback",
		Action:        "resume_lane",
		Target:        envelope.ActionTarget{Kind: "harness_turn", ID: "sess_receipted:turn_receipted"},
		MutationClass: "local_reversible",
		Reversibility: "easy",
		BlastRadius:   "local",
		RollbackPlan:  "retain the Stop event and review any contradiction",
	}
}

func receiptedActionRuling(t *testing.T, s *Store, id, undoOf string, now time.Time) string {
	t.Helper()
	claimID, err := receiptedActionRecordRuling(s, id, undoOf, now)
	if err != nil {
		t.Fatal(err)
	}
	return claimID
}

func receiptedActionRecordRuling(s *Store, id, undoOf string, now time.Time) (string, error) {
	store, err := mem.Open(s.home)
	if err != nil {
		return "", err
	}
	var claimID string
	err = home.WithAutonomyAdmissionLock(s.home, func() error {
		qualifiers := map[string]string{
			"kind":             "ruling",
			"source":           "decisions-repo",
			"decision_id":      id,
			"lifecycle_status": "accepted",
			"reviewed_by":      "operator:test",
			"scope":            "reversible work inside an existing envelope",
		}
		if strings.TrimSpace(undoOf) != "" {
			qualifiers["supersedes"] = undoOf
		}
		captured, err := store.Capture([]mem.Candidate{{
			Entity:         "decision:" + id,
			Predicate:      "decided",
			Object:         "Reversible actions inside a scoped grant proceed without operator approval.",
			Confidence:     1,
			Derivation:     "deterministic",
			ClaimType:      "decision",
			Qualifiers:     qualifiers,
			PromotionBasis: candidates.BasisDecisionRecord,
			SourceTraceRef: "test:" + id,
			SnippetHash:    id,
			Visibility:     "operational",
		}}, now)
		if err != nil {
			return err
		}
		result, err := store.AdjudicateOperationalDecision(captured[0].ID, "promote", "test-seed", now)
		if err != nil {
			return err
		}
		if result.Claim != nil {
			claimID = result.Claim.ID
		}
		return nil
	})
	return claimID, err
}
