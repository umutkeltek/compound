package hook

import (
	"strings"
	"testing"
	"time"

	"compound/internal/harness"
	"compound/internal/home"
	"compound/internal/hookplan"
	"compound/internal/work"
)

func TestLiveHookConsumesLifecycleDeliveryWithoutReadingArtifactBody(t *testing.T) {
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
		SuccessPredicate: "live hook consumes relevant coordination",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	const sender = "codex:sender"
	if _, err := store.Claim(item.ID, sender, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	artifacts, err := home.NewArtifactStore(homePath)
	if err != nil {
		t.Fatal(err)
	}
	const privateBody = "private body must remain behind its artifact reference"
	bodyRef, err := artifacts.Put([]byte(privateBody))
	if err != nil {
		t.Fatal(err)
	}
	event, err := store.RecordCollaboration(item.ID, work.CollaborationInput{
		Actor:          sender,
		IdempotencyKey: "hook-interrupt",
		BodyRef:        bodyRef,
		DeliveryClass:  work.CollaborationDeliveryInterrupt,
		Delta: work.CollaborationStateDelta{
			Kind:       "policy",
			SubjectRef: "policy:review",
			Before:     "allow",
			After:      "hold",
		},
		Affects: work.CollaborationAffects{WorkIDs: []string{item.ID}},
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}

	result, err := Run(Fixture{
		Hook:    "post-tool-use",
		Harness: "codex",
		Live:    true,
		WorkID:  item.ID,
		Tool:    "Bash",
		CoordinationDelivery: &CoordinationDeliveryInput{
			CursorKey: "hook-session",
			Recipient: hookplan.DeliveryRecipient{Actor: "codex:receiver", WorkID: item.ID},
			Trigger:   hookplan.DeliveryTriggerInterrupt,
		},
	}, Deps{
		Registry: harness.DefaultRegistry(),
		HomePath: homePath,
		Store:    store,
	}, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if result.CoordinationDelivery == nil || result.CoordinationDelivery.Native == nil ||
		!strings.Contains(result.ContextOutput, event.Event.ID) ||
		!strings.Contains(result.ContextOutput, bodyRef) ||
		strings.Contains(result.ContextOutput, privateBody) {
		t.Fatalf("live hook delivery = %+v context=%q", result.CoordinationDelivery, result.ContextOutput)
	}
	if result.CoordinationDelivery.DeliveredBytes != len([]byte(result.CoordinationDelivery.Native.Context)) ||
		result.CoordinationDelivery.DeliveredTokens == 0 ||
		len(result.CoordinationDelivery.ActionDeltaRefs) != 1 ||
		result.CoordinationDelivery.ReportRef == "" ||
		!containsString(result.Event.SideEffects, result.CoordinationDelivery.ReportRef) {
		t.Fatalf("delivery observability missing: report=%+v event=%+v", result.CoordinationDelivery, result.Event)
	}
}
