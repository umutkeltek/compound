package work

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/eventstore"
	"compound/internal/policy"
	"compound/internal/runattempt"
)

func TestRecordCollaborationPersistsCompactDeliveryMetadata(t *testing.T) {
	store := newStore(t)
	const actor = "codex:delivery"
	item, err := store.Create(CreateInput{
		Owner:            "delivery",
		SuccessPredicate: "coordination delivery metadata remains content free",
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(item.ID, actor, fixedNow.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	runAttemptID, policyRef := recordInterruptEligibilityEvidence(t, store, claimed, actor)

	input := CollaborationInput{
		Actor:          actor,
		IdempotencyKey: "delivery-metadata-1",
		TargetActor:    "claude:reviewer",
		BodyRef:        collaborationBodyRef(t, store, "full private coordination body"),
		DeliveryClass:  CollaborationDeliveryInterrupt,
		Delta: CollaborationStateDelta{
			Kind:       "run_attempt",
			SubjectRef: runAttemptID,
			Before:     "active",
			After:      "invalidated",
		},
		Affects: CollaborationAffects{
			Actors:         []string{"claude:reviewer"},
			WorkIDs:        []string{item.ID},
			RunAttemptIDs:  []string{runAttemptID},
			ScopeRefs:      []string{"scope:internal/hookplan"},
			DependencyRefs: []string{"work:dependency"},
			PolicyRefs:     []string{policyRef},
			BaseRevisions:  []string{"git:af012403"},
			ResourceRefs:   []string{"resource:proof-battery"},
		},
	}
	recorded, err := store.RecordCollaboration(item.ID, input, fixedNow.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if recorded.Event.DeliveryClass != input.DeliveryClass ||
		recorded.Event.Delta == nil || *recorded.Event.Delta != input.Delta ||
		recorded.Event.Affects == nil || len(recorded.Event.Affects.ResourceRefs) != 1 {
		t.Fatalf("delivery metadata lost: %+v", recorded.Event)
	}
	if recorded.Event.SchemaVersion != CollaborationSchemaVersion ||
		recorded.Event.SchemaVersion == CollaborationLegacySchemaVersion {
		t.Fatalf("new collaboration schema = %q", recorded.Event.SchemaVersion)
	}

	raw, err := json.Marshal(recorded.Event)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"full private coordination body",
		`"message"`,
		`"prompt"`,
		`"transcript"`,
	} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("delivery metadata leaked %q: %s", forbidden, raw)
		}
	}
}

func recordInterruptEligibilityEvidence(t *testing.T, store *Store, item envelope.WorkEnvelope, actor string) (string, string) {
	t.Helper()
	if len(item.RunAttemptIDs) == 0 {
		t.Fatal("claimed work lacks run attempt")
	}
	runAttemptID := item.RunAttemptIDs[len(item.RunAttemptIDs)-1]
	action := envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent, ID: "act_interrupt_evidence",
			CreatedAt: fixedNow.Format(time.RFC3339),
			Producer:  envelope.Producer{Kind: "hook", Name: "compound"},
			IDs:       envelope.IDs{WorkID: item.ID},
			Authority: envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:   "private", Risk: "tier2", EvidenceRefs: []string{},
			Uncertainty: envelope.Uncertainty{Level: "known"},
		},
		Actor: actor, Capability: "work.execute", Action: "continue_run",
		Target:        envelope.ActionTarget{Kind: "work", ID: item.ID},
		MutationClass: "local_reversible", Reversibility: "easy", BlastRadius: "repo",
	}
	decision := envelope.PolicyDecision{
		Header: envelope.Header{
			SchemaVersion: envelope.SVPolicyDecision, ID: "pol_interrupt_evidence",
			CreatedAt: fixedNow.Add(time.Second).Format(time.RFC3339),
			Producer:  envelope.Producer{Kind: "policy", Name: "compound"},
			IDs:       envelope.IDs{WorkID: item.ID},
			Authority: envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:   "private", Risk: "tier2", EvidenceRefs: []string{runAttemptID},
			Uncertainty: envelope.Uncertainty{Level: "known"},
		},
		ActionIntentID: action.ID, Outcome: "hold",
		Reasons: []string{"active action invalidated by governed state"}, Obligations: []string{},
	}
	events, err := eventstore.Open(store.home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := events.PutJSON(action.ID, action, "  "); err != nil {
		t.Fatal(err)
	}
	if _, err := events.PutJSON(decision.ID, decision, "  "); err != nil {
		t.Fatal(err)
	}
	if err := runattempt.Open(store.home).AttachActionIntent(runAttemptID, action.ID); err != nil {
		t.Fatal(err)
	}
	return runAttemptID, decision.ID
}

func TestCollaborationLegacyEventRemainsReadable(t *testing.T) {
	event := CollaborationEvent{
		SchemaVersion:    CollaborationLegacySchemaVersion,
		ID:               "wco_0123456789abcdef",
		Kind:             CollaborationKindObserved,
		CreatedAt:        fixedNow.Format(time.RFC3339),
		WorkID:           "work_legacy",
		Actor:            "codex:legacy",
		IdempotencyKey:   "legacy-event",
		BodyRef:          "artifact:sha256:" + strings.Repeat("1", 64),
		Visibility:       "work",
		Privacy:          "private",
		ActionIntentID:   "act_legacy",
		PolicyDecisionID: "pol_legacy",
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("legacy collaboration event became unreadable: %v", err)
	}
}

func TestRecordCollaborationRejectsUnknownDeliveryClassBeforeEventWrite(t *testing.T) {
	store := newStore(t)
	const actor = "codex:delivery"
	item, err := store.Create(CreateInput{Owner: "delivery", SuccessPredicate: "unknown delivery class is rejected"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(item.ID, actor, fixedNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = store.RecordCollaboration(item.ID, CollaborationInput{
		Actor:          actor,
		IdempotencyKey: "unknown-delivery",
		BodyRef:        collaborationBodyRef(t, store, "unknown class body"),
		DeliveryClass:  "later_maybe",
		Delta:          CollaborationStateDelta{Kind: "work", SubjectRef: item.ID, After: "changed"},
	}, fixedNow.Add(2*time.Second))
	if err == nil || !strings.Contains(err.Error(), "delivery_class") {
		t.Fatalf("unknown delivery class error = %v", err)
	}
}

func TestRecordCollaborationRejectsInterruptWithoutExactInvalidationEvidence(t *testing.T) {
	store := newStore(t)
	const actor = "codex:delivery"
	item, err := store.Create(CreateInput{Owner: "delivery", SuccessPredicate: "interrupt requires exact invalidation evidence"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(item.ID, actor, fixedNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = store.RecordCollaboration(item.ID, CollaborationInput{
		Actor:          actor,
		IdempotencyKey: "interrupt-without-evidence",
		BodyRef:        collaborationBodyRef(t, store, "unproved interrupt"),
		DeliveryClass:  CollaborationDeliveryInterrupt,
		Delta:          CollaborationStateDelta{Kind: "run_attempt", SubjectRef: "ratt_missing", After: "invalidated"},
		Affects:        CollaborationAffects{WorkIDs: []string{item.ID}},
	}, fixedNow.Add(2*time.Second))
	if err == nil || !strings.Contains(err.Error(), "interrupt") {
		t.Fatalf("interrupt without exact evidence error = %v", err)
	}
}

func TestRecordCollaborationRejectsHumanAttentionWithoutUnresolvedAuthorityEvidence(t *testing.T) {
	store := newStore(t)
	const actor = "codex:delivery"
	item, err := store.Create(CreateInput{Owner: "delivery", SuccessPredicate: "human attention requires unresolved authority"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(item.ID, actor, fixedNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = store.RecordCollaboration(item.ID, CollaborationInput{
		Actor:          actor,
		IdempotencyKey: "human-without-evidence",
		BodyRef:        collaborationBodyRef(t, store, "unproved human attention"),
		DeliveryClass:  CollaborationDeliveryHumanAttention,
		Delta:          CollaborationStateDelta{Kind: "authority", SubjectRef: "policy:missing", After: "unresolved"},
		Affects:        CollaborationAffects{WorkIDs: []string{item.ID}},
	}, fixedNow.Add(2*time.Second))
	if err == nil || !strings.Contains(err.Error(), "human_attention") {
		t.Fatalf("human attention without authority evidence error = %v", err)
	}
}

func TestRecordCollaborationAcceptsUnresolvedOperatorGatesBeyondIrreversibility(t *testing.T) {
	tests := []struct {
		name          string
		mutationClass string
		capability    string
		targetKind    string
	}{
		{name: "public", mutationClass: "public", capability: "work.execute", targetKind: "release"},
		{name: "security", mutationClass: "security", capability: "work.execute", targetKind: "policy"},
		{name: "financial", mutationClass: "financial", capability: "work.execute", targetKind: "account"},
		{name: "destructive", mutationClass: "destructive", capability: "work.execute", targetKind: "artifact"},
		{name: "authority-changing", mutationClass: "local_reversible", capability: "authority.change", targetKind: "authority"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newStore(t)
			const actor = "codex:delivery"
			item, err := store.Create(CreateInput{
				Owner: actor, SuccessPredicate: "unresolved operator gate reaches operator state",
			}, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Claim(item.ID, actor, fixedNow.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			suffix := strings.ReplaceAll(tt.name, "-", "_")
			action := envelope.ActionIntent{
				Header: envelope.Header{
					SchemaVersion: envelope.SVActionIntent,
					ID:            "act_human_" + suffix,
					CreatedAt:     fixedNow.Format(time.RFC3339),
					Producer:      envelope.Producer{Kind: "hook", Name: "compound"},
					IDs:           envelope.IDs{WorkID: item.ID},
					Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
					Privacy:       "private",
					Risk:          "tier2",
					EvidenceRefs:  []string{},
					Uncertainty:   envelope.Uncertainty{Level: "known"},
				},
				Actor: actor, Capability: tt.capability, Action: "governed_change",
				Target:        envelope.ActionTarget{Kind: tt.targetKind, ID: item.ID},
				MutationClass: tt.mutationClass, Reversibility: "easy", BlastRadius: "repo",
			}
			decision := envelope.PolicyDecision{
				Header: envelope.Header{
					SchemaVersion: envelope.SVPolicyDecision,
					ID:            "pol_human_" + suffix,
					CreatedAt:     fixedNow.Add(time.Second).Format(time.RFC3339),
					Producer:      envelope.Producer{Kind: "policy", Name: "compound"},
					IDs:           envelope.IDs{WorkID: item.ID},
					Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
					Privacy:       "private",
					Risk:          "tier2",
					EvidenceRefs:  []string{policy.OperatorStateAuthorityRef},
					Uncertainty:   envelope.Uncertainty{Level: "known"},
				},
				ActionIntentID: action.ID, Outcome: "hold",
				Reasons: []string{"operator authority unresolved"}, Obligations: []string{},
			}
			events, err := eventstore.Open(store.home)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := events.PutJSON(action.ID, action, "  "); err != nil {
				t.Fatal(err)
			}
			if _, err := events.PutJSON(decision.ID, decision, "  "); err != nil {
				t.Fatal(err)
			}
			_, err = store.RecordCollaboration(item.ID, CollaborationInput{
				Actor: actor, IdempotencyKey: "human-" + tt.name,
				BodyRef:       collaborationBodyRef(t, store, "operator gate "+tt.name),
				DeliveryClass: CollaborationDeliveryHumanAttention,
				Delta: CollaborationStateDelta{
					Kind: "authority", SubjectRef: decision.ID, Before: "pending", After: "unresolved",
				},
				Affects: CollaborationAffects{
					WorkIDs:    []string{item.ID},
					PolicyRefs: []string{decision.ID, policy.OperatorStateAuthorityRef},
				},
			}, fixedNow.Add(2*time.Second))
			if err != nil {
				t.Fatalf("%s operator gate rejected: %v", tt.name, err)
			}
		})
	}
}

func TestRetrieveCollaborationBodyRequiresExactActiveRecipientAndWritesNoAcknowledgement(t *testing.T) {
	store := newStore(t)
	const (
		sender   = "codex:sender"
		receiver = "claude:receiver"
	)
	item, err := store.Create(CreateInput{Owner: "delivery", SuccessPredicate: "body retrieval is exact and read only"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(item.ID, sender, fixedNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	attempts := runattempt.Open(store.home)
	addressed, err := attempts.Mint(item.ID, receiver, "claude-code", fixedNow.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := attempts.Mint(item.ID, receiver, "claude-code", fixedNow.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	bodyRef := collaborationBodyRef(t, store, "governed body fetched on demand")
	recorded, err := store.RecordCollaboration(item.ID, CollaborationInput{
		Actor: sender, IdempotencyKey: "body-door", TargetActor: receiver,
		BodyRef: bodyRef, DeliveryClass: CollaborationDeliveryNextBoundary,
		Delta: CollaborationStateDelta{Kind: "work", SubjectRef: item.ID, After: "changed"},
		Affects: CollaborationAffects{
			WorkIDs: []string{item.ID}, RunAttemptIDs: []string{addressed.ID},
		},
	}, fixedNow.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.eventsFor(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RetrieveCollaborationBody(CollaborationBodyRequest{
		WorkID: item.ID, EventID: recorded.Event.ID, Actor: receiver, RunAttemptID: unrelated.ID,
	}); err == nil {
		t.Fatal("unrelated attempt fetched collaboration body")
	}
	if _, err := attempts.Complete(addressed.ID, runattempt.StatusFailed, fixedNow.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RetrieveCollaborationBody(CollaborationBodyRequest{
		WorkID: item.ID, EventID: recorded.Event.ID, Actor: receiver, RunAttemptID: addressed.ID,
	}); err == nil {
		t.Fatal("terminal attempt fetched collaboration body")
	}

	active, err := attempts.Mint(item.ID, receiver, "claude-code", fixedNow.Add(6*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	recorded, err = store.RecordCollaboration(item.ID, CollaborationInput{
		Actor: sender, IdempotencyKey: "body-door-active", TargetActor: receiver,
		BodyRef: bodyRef, DeliveryClass: CollaborationDeliveryNextBoundary,
		Delta: CollaborationStateDelta{Kind: "work", SubjectRef: item.ID, After: "changed_again"},
		Affects: CollaborationAffects{
			WorkIDs: []string{item.ID}, RunAttemptIDs: []string{active.ID},
		},
	}, fixedNow.Add(7*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.RetrieveCollaborationBody(CollaborationBodyRequest{
		WorkID: item.ID, EventID: recorded.Event.ID, Actor: receiver, RunAttemptID: active.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != "governed body fetched on demand" || got.BodyRef != bodyRef {
		t.Fatalf("retrieved body = %+v", got)
	}
	after, err := store.eventsFor(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("successful body fetch emitted acknowledgement/event: before=%d after=%d", len(before), len(after))
	}
}

func TestRetrieveUntargetedCollaborationBodyUsesExactActiveRunRelevanceWithoutAcknowledgement(t *testing.T) {
	store := newStore(t)
	const (
		sender   = "codex:sender"
		receiver = "claude:receiver"
	)
	item, err := store.Create(CreateInput{
		Owner: sender, SuccessPredicate: "relevant broadcast body remains on demand",
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(item.ID, sender, fixedNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	attempt, err := runattempt.Open(store.home).Mint(
		item.ID, receiver, "claude-code", fixedNow.Add(2*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	bodyRef := collaborationBodyRef(t, store, "broadcast body fetched on demand")
	recorded, err := store.RecordCollaboration(item.ID, CollaborationInput{
		Actor: sender, IdempotencyKey: "body-door-broadcast",
		BodyRef: bodyRef, DeliveryClass: CollaborationDeliveryNextBoundary,
		Delta: CollaborationStateDelta{Kind: "work", SubjectRef: item.ID, After: "changed"},
		Affects: CollaborationAffects{
			WorkIDs: []string{item.ID}, RunAttemptIDs: []string{attempt.ID},
		},
	}, fixedNow.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.eventsFor(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.RetrieveCollaborationBody(CollaborationBodyRequest{
		WorkID: item.ID, EventID: recorded.Event.ID, Actor: receiver, RunAttemptID: attempt.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != "broadcast body fetched on demand" || got.BodyRef != bodyRef {
		t.Fatalf("retrieved broadcast body = %+v", got)
	}
	after, err := store.eventsFor(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("broadcast body fetch emitted acknowledgement/event: before=%d after=%d", len(before), len(after))
	}
}
