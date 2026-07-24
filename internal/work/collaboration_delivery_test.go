package work

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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
	if _, err := store.Claim(item.ID, actor, fixedNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	input := CollaborationInput{
		Actor:          actor,
		IdempotencyKey: "delivery-metadata-1",
		TargetActor:    "claude:reviewer",
		BodyRef:        collaborationBodyRef(t, store, "full private coordination body"),
		DeliveryClass:  CollaborationDeliveryInterrupt,
		Delta: CollaborationStateDelta{
			Kind:       "policy",
			SubjectRef: "policy:review-gate",
			Before:     "allow",
			After:      "hold",
		},
		Affects: CollaborationAffects{
			Actors:         []string{"claude:reviewer"},
			WorkIDs:        []string{item.ID},
			ScopeRefs:      []string{"scope:internal/hookplan"},
			DependencyRefs: []string{"work:dependency"},
			PolicyRefs:     []string{"policy:review-gate"},
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
