package hookplan

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/work"
)

type deliveryFixture struct {
	home  string
	store *work.Store
	item  work.CollaborationResult
	work  string
	actor string
	now   time.Time
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
	if _, err := store.Claim(item.ID, actor, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	return &deliveryFixture{home: homePath, store: store, work: item.ID, actor: actor, now: now}
}

func (fixture *deliveryFixture) record(t *testing.T, key, class string, affects work.CollaborationAffects) work.CollaborationEvent {
	t.Helper()
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
		Delta: work.CollaborationStateDelta{
			Kind:       "work",
			SubjectRef: "subject:" + key,
			Before:     "before",
			After:      "after",
		},
		Affects: affects,
	}, fixture.now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return result.Event
}

func wideDeliveryBudget() *DeliveryBudget {
	return &DeliveryBudget{MaxCards: 1, MaxBytes: 4096, MaxTokens: 1024, MaxItems: 8}
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
		report.DeliveredTokens != (len([]byte(report.Native.Context))+3)/4 ||
		len(report.ActionDeltaRefs) != 2 ||
		report.AcknowledgementMessages != 0 {
		t.Fatalf("delivery measurements/action delta = %+v", report)
	}
	if report.DeliveredBytes > request.Budget.MaxBytes || report.DeliveredTokens > request.Budget.MaxTokens {
		t.Fatalf("bounded card exceeded budget: %+v", report)
	}

	repeated, err := DeliverCoordination(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(repeated.Cards) != 0 || repeated.Native != nil || repeated.PendingEvents != 0 {
		t.Fatalf("cursor replay duplicated delivery: %+v", repeated)
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
		Recipient: DeliveryRecipient{WorkID: fixture.work},
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
		state, err := readDeliveryState(path, request.CursorKey)
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

func TestInterruptBypassesNextBoundaryQueue(t *testing.T) {
	fixture := newDeliveryFixture(t)
	affects := work.CollaborationAffects{WorkIDs: []string{fixture.work}}
	deferred := fixture.record(t, "next-boundary", work.CollaborationDeliveryNextBoundary, affects)
	interrupt := fixture.record(t, "interrupt", work.CollaborationDeliveryInterrupt, affects)
	request := DeliveryRequest{
		HomePath:  fixture.home,
		Harness:   "codex",
		CursorKey: "interrupt-session",
		Recipient: DeliveryRecipient{WorkID: fixture.work},
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

func TestSilentStateAndHumanAttentionHaveDistinctSemantics(t *testing.T) {
	fixture := newDeliveryFixture(t)
	affects := work.CollaborationAffects{ResourceRefs: []string{"resource:proof-battery"}}
	fixture.record(t, "silent", work.CollaborationDeliverySilentState, affects)
	human := fixture.record(t, "human", work.CollaborationDeliveryHumanAttention, affects)

	report, err := DeliverCoordination(DeliveryRequest{
		HomePath:  fixture.home,
		Harness:   "claude-code",
		CursorKey: "class-session",
		Recipient: DeliveryRecipient{ResourceRefs: []string{"resource:proof-battery"}},
		Trigger:   DeliveryTriggerInterrupt,
		Budget:    wideDeliveryBudget(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.SilentEvents != 1 || len(report.Cards) != 1 ||
		len(report.Cards[0].Items) != 1 || report.Cards[0].Items[0].EventID != human.ID ||
		!report.Cards[0].RequiresHuman || report.AcknowledgementMessages != 0 {
		t.Fatalf("delivery classes collapsed: %+v", report)
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
		Recipient: DeliveryRecipient{WorkID: fixture.work},
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
	if _, err := RetrieveArtifactBody(fixture.home, event.BodyRef); err == nil {
		t.Fatal("on-demand retrieval unexpectedly succeeded with missing body")
	}
	artifacts, err := home.NewArtifactStore(fixture.home)
	if err != nil {
		t.Fatal(err)
	}
	if ref, err := artifacts.Put(body); err != nil || ref != event.BodyRef {
		t.Fatalf("restore artifact ref=%q err=%v", ref, err)
	}
	got, err := RetrieveArtifactBody(fixture.home, event.BodyRef)
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
	recipient := DeliveryRecipient{PolicyRefs: []string{"policy:budget"}}

	for _, test := range []struct {
		name   string
		budget *DeliveryBudget
	}{
		{
			name:   "card",
			budget: &DeliveryBudget{MaxCards: 0, MaxBytes: 4096, MaxTokens: 1024, MaxItems: 8},
		},
		{
			name:   "token",
			budget: &DeliveryBudget{MaxCards: 1, MaxBytes: 4096, MaxTokens: 1, MaxItems: 8},
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
