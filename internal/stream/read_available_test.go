package stream

import (
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/work"
)

func TestReadAvailableProjectsGovernedCollaborationOnceByCursor(t *testing.T) {
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
		SuccessPredicate: "finite stream replay sees governed collaboration",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	const actor = "codex:delivery"
	if _, err := store.Claim(item.ID, actor, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	artifacts, err := home.NewArtifactStore(homePath)
	if err != nil {
		t.Fatal(err)
	}
	bodyRef, err := artifacts.Put([]byte("full collaboration body"))
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := store.RecordCollaboration(item.ID, work.CollaborationInput{
		Actor:          actor,
		IdempotencyKey: "stream-delivery-1",
		BodyRef:        bodyRef,
		DeliveryClass:  work.CollaborationDeliveryInterrupt,
		Delta: work.CollaborationStateDelta{
			Kind:       "work",
			SubjectRef: item.ID,
			Before:     "running",
			After:      "held",
		},
		Affects: work.CollaborationAffects{WorkIDs: []string{item.ID}},
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}

	frames, cursor, err := ReadAvailable(homePath, 0)
	if err != nil {
		t.Fatal(err)
	}
	var collaborations []Frame
	for _, frame := range frames {
		if frame.Kind == KindCollaborationEvent {
			collaborations = append(collaborations, frame)
		}
	}
	if len(collaborations) != 1 ||
		collaborations[0].Collaboration == nil ||
		collaborations[0].Collaboration.ID != recorded.Event.ID ||
		cursor <= 0 {
		t.Fatalf("finite replay = frames=%+v cursor=%d", collaborations, cursor)
	}

	repeated, sameCursor, err := ReadAvailable(homePath, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(repeated) != 0 || sameCursor != cursor {
		t.Fatalf("cursor replay duplicated frames=%+v cursor=%d want=%d", repeated, sameCursor, cursor)
	}
}
