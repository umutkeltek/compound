package hookplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/eventstore"
	"compound/internal/home"
	"compound/internal/runattempt"
	"compound/internal/work"
)

type deliveryFixture struct {
	home         string
	store        *work.Store
	item         work.CollaborationResult
	work         string
	actor        string
	now          time.Time
	runAttemptID string
}

func newDeliveryFixture(t *testing.T) *deliveryFixture {
	t.Helper()
	homePath := t.TempDir()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "test", now); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Create(work.CreateInput{
		Owner:            "delivery",
		SuccessPredicate: "relevant coordination reaches its receiver",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	const actor = "codex:sender"
	claimed, err := store.Claim(item.ID, actor, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return &deliveryFixture{
		home: homePath, store: store, work: item.ID, actor: actor, now: now,
		runAttemptID: claimed.RunAttemptIDs[len(claimed.RunAttemptIDs)-1],
	}
}

func (fixture *deliveryFixture) record(t *testing.T, key, class string, affects work.CollaborationAffects) work.CollaborationEvent {
	return fixture.recordDelta(t, key, class, work.CollaborationStateDelta{
		Kind:       "work",
		SubjectRef: "subject:" + key,
		Before:     "before",
		After:      "after",
	}, affects)
}

func (fixture *deliveryFixture) recordDelta(t *testing.T, key, class string, delta work.CollaborationStateDelta, affects work.CollaborationAffects) work.CollaborationEvent {
	t.Helper()
	switch class {
	case work.CollaborationDeliveryInterrupt:
		attemptID := fixture.runAttemptID
		if len(affects.RunAttemptIDs) > 0 {
			attemptID = affects.RunAttemptIDs[0]
		} else {
			affects.RunAttemptIDs = []string{attemptID}
		}
		policyRef := fixture.recordInterruptEvidence(t, attemptID)
		affects.PolicyRefs = append(affects.PolicyRefs, policyRef)
		delta = work.CollaborationStateDelta{
			Kind: "run_attempt", SubjectRef: attemptID, Before: "active", After: "invalidated",
		}
	case work.CollaborationDeliveryHumanAttention:
		policyRef := fixture.recordHumanAttentionEvidence(t)
		affects.PolicyRefs = append(affects.PolicyRefs, policyRef, "compound://registries/operator-state")
		delta = work.CollaborationStateDelta{
			Kind: "authority", SubjectRef: policyRef, Before: "pending", After: "unresolved",
		}
	}
	artifacts, err := home.NewArtifactStore(fixture.home)
	if err != nil {
		t.Fatal(err)
	}
	bodyRef, err := artifacts.Put([]byte("full body for " + key))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.store.RecordCollaboration(fixture.work, work.CollaborationInput{
		Actor:          fixture.actor,
		IdempotencyKey: key,
		BodyRef:        bodyRef,
		DeliveryClass:  class,
		Delta:          delta,
		Affects:        affects,
	}, fixture.now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return result.Event
}

func (fixture *deliveryFixture) recordInterruptEvidence(t *testing.T, attemptID string) string {
	t.Helper()
	suffix := attemptID
	if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}
	actionID := "act_test_interrupt_" + suffix
	policyID := "pol_test_interrupt_" + suffix
	attempts := runattempt.Open(fixture.home)
	attempt, err := attempts.Get(attemptID)
	if err != nil {
		t.Fatal(err)
	}
	action := deliveryEvidenceAction(actionID, fixture.work, attempt.Actor, "easy", fixture.now)
	decision := deliveryEvidenceDecision(policyID, fixture.work, actionID, []string{attemptID}, fixture.now)
	events, err := eventstore.Open(fixture.home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := events.PutJSON(action.ID, action, "  "); err != nil {
		t.Fatal(err)
	}
	if _, err := events.PutJSON(decision.ID, decision, "  "); err != nil {
		t.Fatal(err)
	}
	if err := attempts.AttachActionIntent(attemptID, actionID); err != nil {
		t.Fatal(err)
	}
	return policyID
}

func (fixture *deliveryFixture) recordHumanAttentionEvidence(t *testing.T) string {
	t.Helper()
	action := deliveryEvidenceAction("act_test_human_attention", fixture.work, fixture.actor, "irreversible", fixture.now)
	decision := deliveryEvidenceDecision(
		"pol_test_human_attention", fixture.work, action.ID,
		[]string{"compound://registries/operator-state"}, fixture.now,
	)
	events, err := eventstore.Open(fixture.home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := events.PutJSON(action.ID, action, "  "); err != nil {
		t.Fatal(err)
	}
	if _, err := events.PutJSON(decision.ID, decision, "  "); err != nil {
		t.Fatal(err)
	}
	return decision.ID
}

func deliveryEvidenceAction(id, workID, actor, reversibility string, now time.Time) envelope.ActionIntent {
	return envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent, ID: id, CreatedAt: now.UTC().Format(time.RFC3339),
			Producer:  envelope.Producer{Kind: "hook", Name: "compound"},
			IDs:       envelope.IDs{WorkID: workID},
			Authority: envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:   "private", Risk: "tier2", EvidenceRefs: []string{},
			Uncertainty: envelope.Uncertainty{Level: "known"},
		},
		Actor: actor, Capability: "work.execute", Action: "continue_run",
		Target:        envelope.ActionTarget{Kind: "work", ID: workID},
		MutationClass: "local_reversible", Reversibility: reversibility, BlastRadius: "repo",
	}
}

func deliveryEvidenceDecision(id, workID, actionID string, evidence []string, now time.Time) envelope.PolicyDecision {
	return envelope.PolicyDecision{
		Header: envelope.Header{
			SchemaVersion: envelope.SVPolicyDecision, ID: id, CreatedAt: now.Add(time.Second).UTC().Format(time.RFC3339),
			Producer:  envelope.Producer{Kind: "policy", Name: "compound"},
			IDs:       envelope.IDs{WorkID: workID},
			Authority: envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:   "private", Risk: "tier2", EvidenceRefs: evidence,
			Uncertainty: envelope.Uncertainty{Level: "known"},
		},
		ActionIntentID: actionID, Outcome: "hold",
		Reasons: []string{"governed state requires hold"}, Obligations: []string{},
	}
}

func (fixture *deliveryFixture) appendLegacyEvent(t *testing.T, key string) work.CollaborationEvent {
	t.Helper()
	artifacts, err := home.NewArtifactStore(fixture.home)
	if err != nil {
		t.Fatal(err)
	}
	bodyRef, err := artifacts.Put([]byte("legacy body for " + key))
	if err != nil {
		t.Fatal(err)
	}
	event := work.CollaborationEvent{
		SchemaVersion:    work.CollaborationLegacySchemaVersion,
		ID:               "wco_legacyfresh0001",
		Kind:             work.CollaborationKindObserved,
		CreatedAt:        fixture.now.Add(2 * time.Second).Format(time.RFC3339),
		WorkID:           fixture.work,
		Actor:            fixture.actor,
		IdempotencyKey:   key,
		BodyRef:          bodyRef,
		Visibility:       "work",
		Privacy:          "private",
		ActionIntentID:   "act_legacyfresh0001",
		PolicyDecisionID: "pol_legacyfresh0001",
	}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	store, err := eventstore.Open(fixture.home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutJSON(event.ID, event, "  "); err != nil {
		t.Fatal(err)
	}
	row, err := json.Marshal(work.Event{
		TS:       fixture.now.Add(2 * time.Second).Format(time.RFC3339),
		WorkID:   fixture.work,
		From:     "claimed",
		To:       "claimed",
		By:       fixture.actor,
		Attached: []string{event.ActionIntentID, event.PolicyDecisionID, event.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendWorkLog(append(row, '\n'), nil); err != nil {
		t.Fatal(err)
	}
	return event
}

func wideDeliveryBudget() *DeliveryBudget {
	return &DeliveryBudget{MaxCards: 1, MaxBytes: 4096, MaxEstimatedTokens: 1024, MaxItems: 8}
}

func TestIrrelevantEventsNeverEnterContext(t *testing.T) {
	fixture := newDeliveryFixture(t)
	fixture.record(t, "irrelevant", work.CollaborationDeliveryInterrupt, work.CollaborationAffects{
		WorkIDs: []string{"work_someone_else"},
	})

	report, err := DeliverCoordination(DeliveryRequest{
		HomePath:  fixture.home,
		Harness:   "codex",
		CursorKey: "irrelevant-session",
		Recipient: DeliveryRecipient{Actor: "codex:receiver", WorkID: "work_receiver"},
		Trigger:   DeliveryTriggerInterrupt,
		Budget:    wideDeliveryBudget(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Cards) != 0 || report.Native != nil || report.DeliveredBytes != 0 ||
		report.IrrelevantEvents != 1 || report.PendingEvents != 0 {
		t.Fatalf("irrelevant event entered delivery: %+v", report)
	}
}

func TestRepeatedCompatibleEventsDeliverOnceAsOneBoundedCard(t *testing.T) {
	fixture := newDeliveryFixture(t)
	affects := work.CollaborationAffects{WorkIDs: []string{fixture.work}}
	first := fixture.record(t, "coalesce-1", work.CollaborationDeliveryNextBoundary, affects)
	second := fixture.record(t, "coalesce-2", work.CollaborationDeliveryNextBoundary, affects)
	request := DeliveryRequest{
		HomePath:  fixture.home,
		Harness:   "codex",
		CursorKey: "coalesce-session",
		Recipient: DeliveryRecipient{Actor: "codex:receiver", WorkID: fixture.work},
		Trigger:   DeliveryTriggerBoundary,
		Budget:    wideDeliveryBudget(),
	}

	report, err := DeliverCoordination(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Cards) != 1 || len(report.Cards[0].Items) != 2 {
		t.Fatalf("compatible events did not coalesce: %+v", report)
	}
	gotIDs := []string{report.Cards[0].Items[0].EventID, report.Cards[0].Items[1].EventID}
	if !reflect.DeepEqual(gotIDs, []string{first.ID, second.ID}) {
		t.Fatalf("coalesced event order = %v", gotIDs)
	}
	if report.Native == nil ||
		report.DeliveredBytes != len([]byte(report.Native.Context)) ||
		report.EstimatedDeliveredTokens != (len([]byte(report.Native.Context))+3)/4 ||
		len(report.DeclaredStateDeltaRefs) != 2 ||
		report.AcknowledgementMessages != 0 {
		t.Fatalf("delivery measurements/action delta = %+v", report)
	}
	if report.DeliveredBytes > request.Budget.MaxBytes || report.EstimatedDeliveredTokens > request.Budget.MaxEstimatedTokens {
		t.Fatalf("bounded card exceeded budget: %+v", report)
	}
	if report.EstimatedDeliveredTokens > DeliveryClassBudgetHypotheses[work.CollaborationDeliveryNextBoundary].MaxEstimatedTokensPerCard {
		t.Fatalf("next-boundary card exceeded class hypothesis: %+v", report)
	}
	for _, event := range []work.CollaborationEvent{first, second} {
		native := compileNativeDelivery("codex", makeDeliveryCard([]work.CollaborationEvent{event}))
		if estimatedDeliveredTokenCount(native.Context) >
			DeliveryClassBudgetHypotheses[work.CollaborationDeliveryNextBoundary].MaxEstimatedTokensPerItem {
			t.Fatalf("next-boundary item exceeded class hypothesis: %q", native.Context)
		}
	}

	repeated, err := DeliverCoordination(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(repeated.Cards) != 0 || repeated.Native != nil || repeated.PendingEvents != 0 {
		t.Fatalf("cursor replay duplicated delivery: %+v", repeated)
	}
}

func TestDistinctEventsWithSameSubjectAndEffectiveStateInjectOnce(t *testing.T) {
	fixture := newDeliveryFixture(t)
	affects := work.CollaborationAffects{WorkIDs: []string{fixture.work}}
	for _, key := range []string{"same-state-1", "same-state-2"} {
		fixture.recordDelta(t, key, work.CollaborationDeliveryNextBoundary, work.CollaborationStateDelta{
			Kind: "reservation", SubjectRef: "resource:proof-battery", Before: "free", After: "held",
		}, affects)
	}

	report, err := DeliverCoordination(DeliveryRequest{
		HomePath: fixture.home, Harness: "codex", CursorKey: "semantic-same-state",
		Recipient: DeliveryRecipient{WorkID: fixture.work, RunAttemptID: fixture.runAttemptID},
		Trigger:   DeliveryTriggerBoundary, Budget: wideDeliveryBudget(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Cards) != 1 || len(report.Cards[0].Items) != 1 ||
		report.Cards[0].Items[0].Delta.SubjectRef != "resource:proof-battery" ||
		report.Cards[0].Items[0].Delta.After != "held" {
		t.Fatalf("semantic duplicate entered context: %+v", report)
	}
}

func TestBoundaryCoalescingDeliversOnlyLatestSubjectState(t *testing.T) {
	fixture := newDeliveryFixture(t)
	affects := work.CollaborationAffects{WorkIDs: []string{fixture.work}}
	for i, transition := range []struct{ before, after string }{
		{before: "free", after: "reserved"},
		{before: "reserved", after: "held"},
		{before: "held", after: "released"},
	} {
		fixture.recordDelta(t, "transition-"+string(rune('1'+i)), work.CollaborationDeliveryNextBoundary, work.CollaborationStateDelta{
			Kind: "reservation", SubjectRef: "resource:proof-battery",
			Before: transition.before, After: transition.after,
		}, affects)
	}

	report, err := DeliverCoordination(DeliveryRequest{
		HomePath: fixture.home, Harness: "claude-code", CursorKey: "semantic-latest",
		Recipient: DeliveryRecipient{WorkID: fixture.work, RunAttemptID: fixture.runAttemptID},
		Trigger:   DeliveryTriggerBoundary, Budget: wideDeliveryBudget(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Cards) != 1 || len(report.Cards[0].Items) != 1 ||
		report.Cards[0].Items[0].Delta.Before != "held" ||
		report.Cards[0].Items[0].Delta.After != "released" {
		t.Fatalf("boundary card did not collapse to latest state: %+v", report)
	}
	ledger, err := eventstore.Open(fixture.home)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range report.Plan.Intentions {
		if _, err := ledger.Read(event.EventID); err != nil {
			t.Fatalf("governed ledger ref unavailable for %s: %v", event.EventID, err)
		}
	}
}

func TestDeliveryStatePersistsSemanticRevisionWithoutTerminalEventIDs(t *testing.T) {
	fixture := newDeliveryFixture(t)
	fixture.record(t, "semantic-state", work.CollaborationDeliveryNextBoundary, work.CollaborationAffects{WorkIDs: []string{fixture.work}})
	request := DeliveryRequest{
		HomePath: fixture.home, Harness: "codex", CursorKey: "semantic-state-cursor",
		Recipient: DeliveryRecipient{WorkID: fixture.work, RunAttemptID: fixture.runAttemptID},
		Trigger:   DeliveryTriggerBoundary, Budget: wideDeliveryBudget(),
	}
	if _, err := DeliverCoordination(request); err != nil {
		t.Fatal(err)
	}
	var stateBody []byte
	if err := withDeliveryStateLock(fixture.home, request.CursorKey, func(path string) error {
		var err error
		stateBody, err = os.ReadFile(path)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stateBody), "terminal_event_refs") ||
		!strings.Contains(string(stateBody), `"recipient_revision"`) ||
		!strings.Contains(string(stateBody), `"semantic_revision"`) {
		t.Fatalf("delivery state is event-ID history, not semantic cursor state: %s", stateBody)
	}
}

func TestLegacyEventIsSilentOnFreshCursor(t *testing.T) {
	fixture := newDeliveryFixture(t)
	event := fixture.appendLegacyEvent(t, "legacy-fresh")
	report, err := DeliverCoordination(DeliveryRequest{
		HomePath: fixture.home, Harness: "codex", CursorKey: "legacy-fresh-cursor",
		Recipient: DeliveryRecipient{WorkID: fixture.work, RunAttemptID: fixture.runAttemptID},
		Trigger:   DeliveryTriggerBoundary, Budget: wideDeliveryBudget(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Native != nil || len(report.Cards) != 0 || report.SilentEvents != 1 {
		t.Fatalf("legacy event entered fresh context: event=%s report=%+v", event.ID, report)
	}
}

func TestSemanticDedupSurvivesCursorReset(t *testing.T) {
	fixture := newDeliveryFixture(t)
	fixture.record(t, "cursor-reset", work.CollaborationDeliveryInterrupt, work.CollaborationAffects{
		WorkIDs: []string{fixture.work},
	})
	request := DeliveryRequest{
		HomePath:  fixture.home,
		Harness:   "codex",
		CursorKey: "cursor-reset-session",
		Recipient: DeliveryRecipient{WorkID: fixture.work, RunAttemptID: fixture.runAttemptID},
		Trigger:   DeliveryTriggerInterrupt,
		Budget:    wideDeliveryBudget(),
	}
	first, err := DeliverCoordination(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Cards) != 1 {
		t.Fatalf("first delivery = %+v", first)
	}
	if err := withDeliveryStateLock(fixture.home, request.CursorKey, func(path string) error {
		state, err := readDeliveryState(path, request.CursorKey, deliveryRecipientRevision(request.Recipient))
		if err != nil {
			return err
		}
		state.Cursor = 0
		return writeDeliveryState(path, state)
	}); err != nil {
		t.Fatal(err)
	}

	replayed, err := DeliverCoordination(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed.Cards) != 0 || replayed.Native != nil || replayed.PendingEvents != 0 {
		t.Fatalf("semantic replay duplicated delivery: %+v", replayed)
	}
}

func TestRecipientRevisionEvolutionReevaluatesScopeDependencyAndRun(t *testing.T) {
	t.Run("scope", func(t *testing.T) {
		fixture := newDeliveryFixture(t)
		event := fixture.record(t, "scope-evolution", work.CollaborationDeliveryNextBoundary, work.CollaborationAffects{
			ScopeRefs: []string{"scope:new"},
		})
		request := DeliveryRequest{
			HomePath: fixture.home, Harness: "codex", CursorKey: "scope-evolution",
			Recipient: DeliveryRecipient{ScopeRefs: []string{"scope:old"}},
			Trigger:   DeliveryTriggerBoundary, Budget: wideDeliveryBudget(),
		}
		first, err := DeliverCoordination(request)
		if err != nil {
			t.Fatal(err)
		}
		if first.Native != nil || first.IrrelevantEvents != 1 {
			t.Fatalf("old scope received event: %+v", first)
		}
		request.Recipient.ScopeRefs = []string{"scope:new"}
		evolved, err := DeliverCoordination(request)
		if err != nil {
			t.Fatal(err)
		}
		if evolved.Native == nil || !strings.Contains(evolved.Native.Context, event.ID) {
			t.Fatalf("newly relevant scope event was lost: %+v", evolved)
		}
	})

	t.Run("dependency", func(t *testing.T) {
		fixture := newDeliveryFixture(t)
		event := fixture.record(t, "dependency-evolution", work.CollaborationDeliveryNextBoundary, work.CollaborationAffects{
			DependencyRefs: []string{"dependency:new"},
		})
		request := DeliveryRequest{
			HomePath: fixture.home, Harness: "claude-code", CursorKey: "dependency-evolution",
			Recipient: DeliveryRecipient{DependencyRefs: []string{"dependency:old"}},
			Trigger:   DeliveryTriggerBoundary, Budget: wideDeliveryBudget(),
		}
		if first, err := DeliverCoordination(request); err != nil {
			t.Fatal(err)
		} else if first.Native != nil || first.IrrelevantEvents != 1 {
			t.Fatalf("old dependency received event: %+v", first)
		}
		request.Recipient.DependencyRefs = []string{"dependency:new"}
		evolved, err := DeliverCoordination(request)
		if err != nil {
			t.Fatal(err)
		}
		if evolved.Native == nil || !strings.Contains(evolved.Native.Context, event.ID) {
			t.Fatalf("newly relevant dependency event was lost: %+v", evolved)
		}
	})

	t.Run("run_attempt", func(t *testing.T) {
		fixture := newDeliveryFixture(t)
		attempt, err := runattempt.Open(fixture.home).Mint(
			fixture.work, fixture.actor, "codex", fixture.now.Add(2*time.Second),
		)
		if err != nil {
			t.Fatal(err)
		}
		event := fixture.record(t, "run-evolution", work.CollaborationDeliveryInterrupt, work.CollaborationAffects{
			WorkIDs: []string{fixture.work}, RunAttemptIDs: []string{attempt.ID},
		})
		request := DeliveryRequest{
			HomePath: fixture.home, Harness: "codex", CursorKey: "run-evolution",
			Recipient: DeliveryRecipient{
				Actor: fixture.actor, WorkID: fixture.work, RunAttemptID: fixture.runAttemptID,
			},
			Trigger: DeliveryTriggerInterrupt, Budget: wideDeliveryBudget(),
		}
		if first, err := DeliverCoordination(request); err != nil {
			t.Fatal(err)
		} else if first.Native != nil || first.IrrelevantEvents != 1 {
			t.Fatalf("unaddressed run received interrupt: %+v", first)
		}
		request.Recipient.RunAttemptID = attempt.ID
		evolved, err := DeliverCoordination(request)
		if err != nil {
			t.Fatal(err)
		}
		if evolved.Native == nil || !evolved.ExecutionHeld ||
			!strings.Contains(evolved.Native.Context, event.ID) {
			t.Fatalf("addressed run lost interrupt after revision evolution: %+v", evolved)
		}
	})
}

func TestInterruptBypassesNextBoundaryQueue(t *testing.T) {
	fixture := newDeliveryFixture(t)
	affects := work.CollaborationAffects{WorkIDs: []string{fixture.work}}
	deferred := fixture.record(t, "next-boundary", work.CollaborationDeliveryNextBoundary, affects)
	interrupt := fixture.record(t, "interrupt", work.CollaborationDeliveryInterrupt, affects)
	request := DeliveryRequest{
		HomePath:  fixture.home,
		Harness:   "codex",
		CursorKey: "interrupt-session",
		Recipient: DeliveryRecipient{WorkID: fixture.work, RunAttemptID: fixture.runAttemptID},
		Trigger:   DeliveryTriggerInterrupt,
		Budget:    wideDeliveryBudget(),
	}

	immediate, err := DeliverCoordination(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(immediate.Cards) != 1 || len(immediate.Cards[0].Items) != 1 ||
		immediate.Cards[0].Items[0].EventID != interrupt.ID ||
		immediate.PendingEvents != 1 {
		t.Fatalf("interrupt did not bypass queue: %+v", immediate)
	}
	if immediate.EstimatedDeliveredTokens > DeliveryClassBudgetHypotheses[work.CollaborationDeliveryInterrupt].MaxEstimatedTokensPerCard {
		t.Fatalf("interrupt card exceeded class hypothesis: %+v", immediate)
	}

	request.Trigger = DeliveryTriggerBoundary
	boundary, err := DeliverCoordination(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(boundary.Cards) != 1 || len(boundary.Cards[0].Items) != 1 ||
		boundary.Cards[0].Items[0].EventID != deferred.ID ||
		boundary.PendingEvents != 0 {
		t.Fatalf("boundary queue did not drain exactly once: %+v", boundary)
	}
}

func TestInterruptCannotTerminalizeAtNextBoundaryBeforePreToolUse(t *testing.T) {
	fixture := newDeliveryFixture(t)
	interrupt := fixture.record(t, "interrupt-pretool-only", work.CollaborationDeliveryInterrupt, work.CollaborationAffects{
		WorkIDs: []string{fixture.work},
	})
	request := DeliveryRequest{
		HomePath: fixture.home, Harness: "codex", CursorKey: "interrupt-pretool-only",
		Recipient: DeliveryRecipient{
			Actor: fixture.actor, WorkID: fixture.work, RunAttemptID: fixture.runAttemptID,
		},
		Trigger: DeliveryTriggerBoundary, Budget: wideDeliveryBudget(),
	}
	boundary, err := DeliverCoordination(request)
	if err != nil {
		t.Fatal(err)
	}
	if boundary.Native != nil || boundary.ExecutionHeld || boundary.PendingEvents != 1 {
		t.Fatalf("next boundary terminalized interrupt before PreToolUse: %+v", boundary)
	}
	request.Trigger = DeliveryTriggerInterrupt
	preTool, err := DeliverCoordination(request)
	if err != nil {
		t.Fatal(err)
	}
	if preTool.Native == nil || !preTool.ExecutionHeld || preTool.PendingEvents != 0 ||
		!strings.Contains(preTool.Native.Context, interrupt.ID) {
		t.Fatalf("PreToolUse did not consume pending interrupt: %+v", preTool)
	}
}

func TestDeliveryClassEscalationChangesSemanticRevisionAndHoldsInterrupt(t *testing.T) {
	fixture := newDeliveryFixture(t)
	delta := work.CollaborationStateDelta{
		Kind: "run_attempt", SubjectRef: fixture.runAttemptID,
		Before: "active", After: "invalidated",
	}
	affects := work.CollaborationAffects{
		WorkIDs: []string{fixture.work}, RunAttemptIDs: []string{fixture.runAttemptID},
	}
	firstEvent := fixture.recordDelta(
		t, "boundary-before-escalation", work.CollaborationDeliveryNextBoundary, delta, affects,
	)
	request := DeliveryRequest{
		HomePath: fixture.home, Harness: "codex", CursorKey: "class-escalation",
		Recipient: DeliveryRecipient{
			Actor: fixture.actor, WorkID: fixture.work, RunAttemptID: fixture.runAttemptID,
		},
		Trigger: DeliveryTriggerBoundary, Budget: wideDeliveryBudget(),
	}
	first, err := DeliverCoordination(request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Native == nil || !strings.Contains(first.Native.Context, firstEvent.ID) {
		t.Fatalf("initial boundary state was not delivered: %+v", first)
	}

	interrupt := fixture.recordDelta(
		t, "interrupt-after-escalation", work.CollaborationDeliveryInterrupt, delta, affects,
	)
	request.Trigger = DeliveryTriggerInterrupt
	escalated, err := DeliverCoordination(request)
	if err != nil {
		t.Fatal(err)
	}
	if escalated.Native == nil || !escalated.ExecutionHeld ||
		!strings.Contains(escalated.Native.Context, interrupt.ID) {
		t.Fatalf("semantic class escalation did not interrupt: %+v", escalated)
	}
}

func TestInterruptRequiresExactRecipientRunAttempt(t *testing.T) {
	fixture := newDeliveryFixture(t)
	addressed, err := runattempt.Open(fixture.home).Mint(
		fixture.work, "codex:receiver", "codex", fixture.now.Add(2*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	other, err := runattempt.Open(fixture.home).Mint(
		fixture.work, "codex:receiver", "codex", fixture.now.Add(3*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.record(t, "exact-attempt", work.CollaborationDeliveryInterrupt, work.CollaborationAffects{
		WorkIDs: []string{fixture.work}, RunAttemptIDs: []string{addressed.ID},
	})
	base := DeliveryRequest{
		HomePath: fixture.home, Harness: "codex", Trigger: DeliveryTriggerInterrupt,
		Budget: wideDeliveryBudget(),
	}
	addressedRequest := base
	addressedRequest.CursorKey = "attempt-addressed"
	addressedRequest.Recipient = DeliveryRecipient{
		Actor: "codex:receiver", WorkID: fixture.work, RunAttemptID: addressed.ID,
	}
	got, err := DeliverCoordination(addressedRequest)
	if err != nil {
		t.Fatal(err)
	}
	if got.Native == nil || got.DeliveredBytes == 0 {
		t.Fatalf("addressed attempt did not receive interrupt: %+v", got)
	}

	otherRequest := base
	otherRequest.CursorKey = "attempt-other"
	otherRequest.Recipient = DeliveryRecipient{
		Actor: "codex:receiver", WorkID: fixture.work, RunAttemptID: other.ID,
	}
	notAddressed, err := DeliverCoordination(otherRequest)
	if err != nil {
		t.Fatal(err)
	}
	if notAddressed.Native != nil || notAddressed.DeliveredBytes != 0 {
		t.Fatalf("same actor/work but unrelated attempt received interrupt: %+v", notAddressed)
	}
}

func TestActorMatchAloneDoesNotEstablishRelevance(t *testing.T) {
	fixture := newDeliveryFixture(t)
	fixture.record(t, "actor-route-only", work.CollaborationDeliveryNextBoundary, work.CollaborationAffects{
		Actors: []string{"codex:receiver"},
	})
	report, err := DeliverCoordination(DeliveryRequest{
		HomePath: fixture.home, Harness: "codex", CursorKey: "actor-route-only",
		Recipient: DeliveryRecipient{Actor: "codex:receiver"},
		Trigger:   DeliveryTriggerBoundary, Budget: wideDeliveryBudget(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Native != nil || report.DeliveredBytes != 0 {
		t.Fatalf("actor match established relevance: %+v", report)
	}
}

func TestSilentStateAndHumanAttentionHaveDistinctSemantics(t *testing.T) {
	fixture := newDeliveryFixture(t)
	affects := work.CollaborationAffects{ResourceRefs: []string{"resource:proof-battery"}}
	fixture.record(t, "silent", work.CollaborationDeliverySilentState, affects)
	human := fixture.record(t, "human", work.CollaborationDeliveryHumanAttention, affects)

	report, err := DeliverCoordination(DeliveryRequest{
		HomePath:  fixture.home,
		Harness:   "claude-code",
		CursorKey: "class-session",
		Recipient: DeliveryRecipient{
			OperatorStateRef: "compound://registries/operator-state",
			ResourceRefs:     []string{"resource:proof-battery"},
		},
		Trigger: DeliveryTriggerInterrupt,
		Budget:  wideDeliveryBudget(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.SilentEvents != 1 || len(report.Cards) != 1 ||
		len(report.Cards[0].Items) != 1 || report.Cards[0].Items[0].EventID != human.ID ||
		!report.Cards[0].RequiresHuman || report.AcknowledgementMessages != 0 {
		t.Fatalf("delivery classes collapsed: %+v", report)
	}
	if report.EstimatedDeliveredTokens > DeliveryClassBudgetHypotheses[work.CollaborationDeliveryHumanAttention].MaxEstimatedTokensPerCard {
		t.Fatalf("human-attention card exceeded class hypothesis: %+v", report)
	}
}

func TestHumanAttentionDoesNotEnterOrdinaryAgentSteering(t *testing.T) {
	fixture := newDeliveryFixture(t)
	fixture.record(t, "human-operator-only", work.CollaborationDeliveryHumanAttention, work.CollaborationAffects{
		WorkIDs: []string{fixture.work},
	})
	report, err := DeliverCoordination(DeliveryRequest{
		HomePath: fixture.home, Harness: "claude-code", CursorKey: "human-agent",
		Recipient: DeliveryRecipient{Actor: "claude:receiver", WorkID: fixture.work},
		Trigger:   DeliveryTriggerInterrupt, Budget: wideDeliveryBudget(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Native != nil || report.DeliveredBytes != 0 {
		t.Fatalf("human attention entered ordinary agent context: %+v", report)
	}
}

func TestArtifactBodiesAreRetrievedOnlyOnDemand(t *testing.T) {
	fixture := newDeliveryFixture(t)
	event := fixture.record(t, "artifact-demand", work.CollaborationDeliveryInterrupt, work.CollaborationAffects{
		WorkIDs: []string{fixture.work},
	})
	digest := strings.TrimPrefix(event.BodyRef, home.ArtifactRefPrefix)
	artifactPath := filepath.Join(fixture.home, "artifacts", "sha256", digest)
	body, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(artifactPath); err != nil {
		t.Fatal(err)
	}

	report, err := DeliverCoordination(DeliveryRequest{
		HomePath:  fixture.home,
		Harness:   "codex",
		CursorKey: "artifact-session",
		Recipient: DeliveryRecipient{WorkID: fixture.work, RunAttemptID: fixture.runAttemptID},
		Trigger:   DeliveryTriggerInterrupt,
		Budget:    wideDeliveryBudget(),
	})
	if err != nil {
		t.Fatalf("delivery eagerly retrieved artifact body: %v", err)
	}
	if len(report.Cards) != 1 ||
		!reflect.DeepEqual(report.Cards[0].Items[0].ArtifactRefs, []string{event.BodyRef}) {
		t.Fatalf("delivery card lost artifact reference: %+v", report)
	}
	artifacts, err := home.NewArtifactStore(fixture.home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := artifacts.Get(event.BodyRef); err == nil {
		t.Fatal("on-demand retrieval unexpectedly succeeded with missing body")
	}
	if ref, err := artifacts.Put(body); err != nil || ref != event.BodyRef {
		t.Fatalf("restore artifact ref=%q err=%v", ref, err)
	}
	got, err := artifacts.Get(event.BodyRef)
	if err != nil || !reflect.DeepEqual(got, body) {
		t.Fatalf("on-demand body=%q err=%v", got, err)
	}
}

func TestCodexAndClaudeCompileEquivalentMeaning(t *testing.T) {
	fixture := newDeliveryFixture(t)
	fixture.record(t, "parity", work.CollaborationDeliveryNextBoundary, work.CollaborationAffects{
		DependencyRefs: []string{"dependency:base"},
	})
	base := DeliveryRequest{
		HomePath:  fixture.home,
		Recipient: DeliveryRecipient{DependencyRefs: []string{"dependency:base"}},
		Trigger:   DeliveryTriggerBoundary,
		Budget:    wideDeliveryBudget(),
	}
	codexRequest := base
	codexRequest.Harness = "codex"
	codexRequest.CursorKey = "parity-codex"
	claudeRequest := base
	claudeRequest.Harness = "claude-code"
	claudeRequest.CursorKey = "parity-claude"

	codex, err := DeliverCoordination(codexRequest)
	if err != nil {
		t.Fatal(err)
	}
	claude, err := DeliverCoordination(claudeRequest)
	if err != nil {
		t.Fatal(err)
	}
	if codex.Native == nil || claude.Native == nil ||
		codex.Native.MeaningDigest != claude.Native.MeaningDigest ||
		!reflect.DeepEqual(codex.Native.Semantic, claude.Native.Semantic) {
		t.Fatalf("native semantic parity failed: codex=%+v claude=%+v", codex.Native, claude.Native)
	}
}

func TestCardAndTokenBudgetsFailClosedWithoutLosingPendingEvents(t *testing.T) {
	fixture := newDeliveryFixture(t)
	fixture.record(t, "budget", work.CollaborationDeliveryInterrupt, work.CollaborationAffects{
		PolicyRefs: []string{"policy:budget"},
	})
	recipient := DeliveryRecipient{
		WorkID: fixture.work, RunAttemptID: fixture.runAttemptID,
		PolicyRefs: []string{"policy:budget"},
	}

	for _, test := range []struct {
		name   string
		budget *DeliveryBudget
	}{
		{
			name:   "card",
			budget: &DeliveryBudget{MaxCards: 0, MaxBytes: 4096, MaxEstimatedTokens: 1024, MaxItems: 8},
		},
		{
			name:   "token",
			budget: &DeliveryBudget{MaxCards: 1, MaxBytes: 4096, MaxEstimatedTokens: 1, MaxItems: 8},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := DeliveryRequest{
				HomePath:  fixture.home,
				Harness:   "codex",
				CursorKey: "budget-" + test.name,
				Recipient: recipient,
				Trigger:   DeliveryTriggerInterrupt,
				Budget:    test.budget,
			}
			held, err := DeliverCoordination(request)
			if err != nil {
				t.Fatal(err)
			}
			if !held.FailedClosed || len(held.Cards) != 0 || held.PendingEvents != 1 || held.Cursor == 0 {
				t.Fatalf("budget did not fail closed safely: %+v", held)
			}

			request.Budget = wideDeliveryBudget()
			recovered, err := DeliverCoordination(request)
			if err != nil {
				t.Fatal(err)
			}
			if recovered.FailedClosed || len(recovered.Cards) != 1 || recovered.PendingEvents != 0 {
				t.Fatalf("budget recovery lost pending event: %+v", recovered)
			}
		})
	}
}

func TestPerClassEstimatedTokenBudgetsAreExplicitHypotheses(t *testing.T) {
	want := map[string]DeliveryClassBudget{
		work.CollaborationDeliverySilentState:    {MaxEstimatedTokensPerItem: 0, MaxEstimatedTokensPerCard: 0},
		work.CollaborationDeliveryNextBoundary:   {MaxEstimatedTokensPerItem: 120, MaxEstimatedTokensPerCard: 240},
		work.CollaborationDeliveryInterrupt:      {MaxEstimatedTokensPerItem: 80, MaxEstimatedTokensPerCard: 80},
		work.CollaborationDeliveryHumanAttention: {MaxEstimatedTokensPerItem: 180, MaxEstimatedTokensPerCard: 180},
	}
	if !reflect.DeepEqual(DeliveryClassBudgetHypotheses, want) {
		t.Fatalf("delivery class budget hypotheses = %+v, want %+v", DeliveryClassBudgetHypotheses, want)
	}
}

func TestUnchangedRecipientActionMeasuresZeroAfterDelivery(t *testing.T) {
	fixture := newDeliveryFixture(t)
	fixture.record(t, "measure-zero", work.CollaborationDeliveryNextBoundary, work.CollaborationAffects{
		WorkIDs: []string{fixture.work},
	})
	report, err := DeliverCoordination(DeliveryRequest{
		HomePath: fixture.home, Harness: "codex", CursorKey: "measure-zero",
		Recipient: DeliveryRecipient{WorkID: fixture.work},
		Trigger:   DeliveryTriggerBoundary, Budget: wideDeliveryBudget(),
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := MeasureDeliveryOutcome(fixture.home, report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.CausedActionRefs) != 0 || len(receipt.ObservedActionRefs) != 0 ||
		len(receipt.ObservedRunAttemptRefs) != 0 ||
		len(receipt.ObservedWorkRefs) != 0 {
		t.Fatalf("unchanged recipient measured a caused action: %+v", receipt)
	}
}

func TestPostDeliveryMeasurementUsesNewOwnedActionAndWorkRefs(t *testing.T) {
	fixture := newDeliveryFixture(t)
	fixture.record(t, "measure-action", work.CollaborationDeliveryNextBoundary, work.CollaborationAffects{
		WorkIDs: []string{fixture.work},
	})
	report, err := DeliverCoordination(DeliveryRequest{
		HomePath: fixture.home, Harness: "claude-code", CursorKey: "measure-action",
		Recipient: DeliveryRecipient{WorkID: fixture.work},
		Trigger:   DeliveryTriggerBoundary, Budget: wideDeliveryBudget(),
	})
	if err != nil {
		t.Fatal(err)
	}
	action := deliveryEvidenceAction("act_post_delivery_observed", fixture.work, fixture.actor, "easy", fixture.now.Add(4*time.Second))
	action.EvidenceRefs = []string{report.ReportRef}
	decision := deliveryEvidenceDecision(
		"pol_post_delivery_observed", fixture.work, action.ID, []string{report.ReportRef}, fixture.now.Add(4*time.Second),
	)
	decision.Outcome = "allow"
	decision.Reasons = []string{"test observed governed action"}
	if _, err := fixture.store.RecordAction(fixture.work, action, decision, fixture.now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	receipt, err := MeasureDeliveryOutcome(fixture.home, report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(receipt.CausedActionRefs, []string{action.ID}) ||
		!reflect.DeepEqual(receipt.ObservedActionRefs, []string{action.ID}) ||
		len(receipt.ObservedRunAttemptRefs) != 0 ||
		len(receipt.ObservedWorkRefs) != 1 {
		t.Fatalf("post-delivery action measurement = %+v", receipt)
	}
}

func TestPostDeliveryTemporalActionWithoutCausalRefRemainsObservedOnly(t *testing.T) {
	fixture := newDeliveryFixture(t)
	fixture.record(t, "measure-temporal-action", work.CollaborationDeliveryNextBoundary, work.CollaborationAffects{
		WorkIDs: []string{fixture.work},
	})
	report, err := DeliverCoordination(DeliveryRequest{
		HomePath: fixture.home, Harness: "codex", CursorKey: "measure-temporal-action",
		Recipient: DeliveryRecipient{WorkID: fixture.work},
		Trigger:   DeliveryTriggerBoundary, Budget: wideDeliveryBudget(),
	})
	if err != nil {
		t.Fatal(err)
	}
	action := deliveryEvidenceAction(
		"act_temporal_after_delivery", fixture.work, fixture.actor, "easy", fixture.now.Add(4*time.Second),
	)
	action.EvidenceRefs = []string{"evidence:unrelated"}
	decision := deliveryEvidenceDecision(
		"pol_temporal_after_delivery", fixture.work, action.ID,
		[]string{report.ReportRef}, fixture.now.Add(4*time.Second),
	)
	decision.Outcome = "allow"
	decision.Reasons = []string{"unrelated governed action"}
	if _, err := fixture.store.RecordAction(
		fixture.work, action, decision, fixture.now.Add(5*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	receipt, err := MeasureDeliveryOutcome(fixture.home, report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.CausedActionRefs) != 0 ||
		!reflect.DeepEqual(receipt.ObservedActionRefs, []string{action.ID}) {
		t.Fatalf("temporal-only action was mislabeled as caused: %+v", receipt)
	}
}
