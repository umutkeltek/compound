package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/eventstore"
	"compound/internal/home"
	"compound/internal/runattempt"
	"compound/internal/work"
)

func TestInstalledNativePayloadResolvesOwnedRecipientAndDeliversBoundaryAndInterrupt(t *testing.T) {
	for _, harnessName := range []string{"codex", "claude-zai"} {
		t.Run(harnessName, func(t *testing.T) {
			testCase := newNativeCoordinationCase(t, harnessName)
			t.Setenv("COMPOUND_HOME", testCase.homePath)

			promptPayload := fmt.Sprintf(
				`{"hook_event_name":"UserPromptSubmit","session_id":"native-session","cwd":%q,"prompt":"continue governed work"}`,
				testCase.workspace,
			)
			var promptCode int
			promptOut := captureStdout(t, func() {
				_ = captureStderr(t, func() {
					withStdin(t, promptPayload, func() {
						promptCode = runHookCommand([]string{"user-prompt-submit", "--live", "--harness", harnessName})
					})
				})
			})
			if promptCode != 0 {
				t.Fatalf("native prompt exit=%d\n%s", promptCode, promptOut)
			}
			promptContext := parseNativePromptContext(t, promptOut, "UserPromptSubmit")
			if !strings.Contains(promptContext, testCase.boundaryEvent.ID) ||
				strings.Contains(promptContext, testCase.privateBody) {
				t.Fatalf("native prompt did not receive reference-only boundary state:\n%s", promptContext)
			}

			preToolPayload := fmt.Sprintf(
				`{"hook_event_name":"PreToolUse","session_id":"native-session","cwd":%q,"tool_name":"Write","tool_input":{"file_path":%q,"content":"change"}}`,
				testCase.workspace, filepath.Join(testCase.workspace, "README.md"),
			)
			var preToolCode int
			preToolOut := captureStdout(t, func() {
				_ = captureStderr(t, func() {
					withStdin(t, preToolPayload, func() {
						preToolCode = runHookCommand([]string{"pre-tool-use", "--live", "--harness", harnessName, "--enforce"})
					})
				})
			})
			if preToolCode != 3 {
				t.Fatalf("native pre-tool exit=%d, want governed hold\n%s", preToolCode, preToolOut)
			}
			var response claudeLiveHookResponse
			if err := json.Unmarshal([]byte(preToolOut), &response); err != nil {
				t.Fatalf("parse native pre-tool response: %v\n%s", err, preToolOut)
			}
			if response.HookSpecificOutput.PermissionDecision == "" ||
				!strings.Contains(response.HookSpecificOutput.AdditionalContext, testCase.interruptEvent.ID) ||
				strings.Contains(response.HookSpecificOutput.AdditionalContext, testCase.privateBody) {
				t.Fatalf("native pre-tool did not hold with reference-only interrupt: %+v",
					response.HookSpecificOutput)
			}
		})
	}
}

func TestNativeCoordinationCursorIsStableAcrossSuccessorRunAttempts(t *testing.T) {
	testCase := newNativeCoordinationCase(t, "codex")
	payload := []byte(fmt.Sprintf(
		`{"hook_event_name":"UserPromptSubmit","session_id":"stable-session","cwd":%q,"prompt":"continue"}`,
		testCase.workspace,
	))
	first, err := liveHookFixtureForHome(hookCommandArgs{
		HookName: "user-prompt-submit", Harness: "codex", Live: true,
	}, payload, cliTestNow, testCase.homePath)
	if err != nil {
		t.Fatal(err)
	}
	if first.CoordinationDelivery == nil {
		t.Fatal("first native run did not resolve a coordination recipient")
	}
	firstRun := first.CoordinationDelivery.Recipient.RunAttemptID
	if _, err := runattempt.Open(testCase.homePath).Complete(
		firstRun, runattempt.StatusFailed, cliTestNow.Add(10*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	successor := testCase.attachAttempt(t, "successor", cliTestNow.Add(11*time.Second))

	second, err := liveHookFixtureForHome(hookCommandArgs{
		HookName: "user-prompt-submit", Harness: "codex", Live: true,
	}, payload, cliTestNow.Add(12*time.Second), testCase.homePath)
	if err != nil {
		t.Fatal(err)
	}
	if second.CoordinationDelivery == nil ||
		second.CoordinationDelivery.Recipient.RunAttemptID != successor.ID ||
		first.CoordinationDelivery.CursorKey != second.CoordinationDelivery.CursorKey ||
		strings.Contains(second.CoordinationDelivery.CursorKey, "run_attempt=") {
		t.Fatalf("successor run forked semantic cursor: first=%+v second=%+v",
			first.CoordinationDelivery, second.CoordinationDelivery)
	}
}

type nativeCoordinationCase struct {
	homePath       string
	workspace      string
	harness        string
	sender         string
	receiver       string
	workID         string
	store          *work.Store
	attempt        runattempt.RunAttempt
	boundaryEvent  work.CollaborationEvent
	interruptEvent work.CollaborationEvent
	privateBody    string
}

func newNativeCoordinationCase(t *testing.T, harnessName string) *nativeCoordinationCase {
	t.Helper()
	homePath := t.TempDir()
	workspace := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	const (
		sender   = "codex:sender"
		receiver = "codex:receiver"
	)
	item, err := store.Create(work.CreateInput{
		Owner: sender, SuccessPredicate: "native hook receives governed coordination",
		EvidenceRefs: []string{"scope:hook-delivery", "dependency:stream", "resource:proof"},
	}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(item.ID, sender, cliTestNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	testCase := &nativeCoordinationCase{
		homePath: homePath, workspace: workspace, harness: harnessName,
		sender: sender, receiver: receiver, workID: item.ID, store: store,
		privateBody: "PRIVATE_NATIVE_COORDINATION_BODY",
	}
	testCase.attempt = testCase.attachAttempt(t, "initial", cliTestNow.Add(2*time.Second))

	action := nativeInterruptAction(testCase.workID, testCase.receiver, testCase.attempt.ID, cliTestNow)
	decision := nativeInterruptDecision(testCase.workID, testCase.attempt.ID, action.ID, cliTestNow)
	events, err := eventstore.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := events.PutJSON(action.ID, action, " "); err != nil {
		t.Fatal(err)
	}
	if _, err := events.PutJSON(decision.ID, decision, " "); err != nil {
		t.Fatal(err)
	}
	if err := runattempt.Open(homePath).AttachActionIntent(testCase.attempt.ID, action.ID); err != nil {
		t.Fatal(err)
	}
	artifacts, err := home.NewArtifactStore(homePath)
	if err != nil {
		t.Fatal(err)
	}
	bodyRef, err := artifacts.Put([]byte(testCase.privateBody))
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := store.RecordCollaboration(item.ID, work.CollaborationInput{
		Actor: sender, IdempotencyKey: "native-boundary", BodyRef: bodyRef,
		DeliveryClass: work.CollaborationDeliveryNextBoundary,
		Delta: work.CollaborationStateDelta{
			Kind: "work", SubjectRef: item.ID, Before: "claimed", After: "changed",
		},
		Affects: work.CollaborationAffects{WorkIDs: []string{item.ID}},
	}, cliTestNow.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	interrupt, err := store.RecordCollaboration(item.ID, work.CollaborationInput{
		Actor: sender, IdempotencyKey: "native-interrupt", BodyRef: bodyRef,
		DeliveryClass: work.CollaborationDeliveryInterrupt,
		Delta: work.CollaborationStateDelta{
			Kind: "run_attempt", SubjectRef: testCase.attempt.ID,
			Before: "active", After: "invalidated",
		},
		Affects: work.CollaborationAffects{
			WorkIDs: []string{item.ID}, RunAttemptIDs: []string{testCase.attempt.ID},
			PolicyRefs: []string{decision.ID},
		},
	}, cliTestNow.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	testCase.boundaryEvent = boundary.Event
	testCase.interruptEvent = interrupt.Event
	return testCase
}

func (testCase *nativeCoordinationCase) attachAttempt(t *testing.T, suffix string, now time.Time) runattempt.RunAttempt {
	t.Helper()
	attempts := runattempt.Open(testCase.homePath)
	attempt, err := attempts.Mint(testCase.workID, testCase.receiver, testCase.harness, now)
	if err != nil {
		t.Fatal(err)
	}
	if attempt, err = attempts.SetOrigin(attempt.ID, runattempt.OriginHarnessExecution); err != nil {
		t.Fatal(err)
	}
	if attempt, err = attempts.SetWorkspace(attempt.ID, testCase.workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := testCase.store.Advance(testCase.workID, work.AdvanceInput{
		To: "claimed", Actor: testCase.sender, RunAttemptIDs: []string{attempt.ID},
		Note: "native " + suffix + " run attached",
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if attempt, err = attempts.MarkRunning(attempt.ID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	return attempt
}

func nativeInterruptAction(workID, actor, runAttemptID string, now time.Time) envelope.ActionIntent {
	return envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent, ID: "act_native_interrupt",
			CreatedAt: now.UTC().Format(time.RFC3339),
			Producer:  envelope.Producer{Kind: "hook", Name: "compound"},
			IDs:       envelope.IDs{WorkID: workID},
			Authority: envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:   "private", Risk: "tier2", EvidenceRefs: []string{runAttemptID},
			Uncertainty: envelope.Uncertainty{Level: "known"},
		},
		Actor: actor, Capability: "work.execute", Action: "continue_run",
		Target:        envelope.ActionTarget{Kind: "work", ID: workID},
		MutationClass: "local_reversible", Reversibility: "easy", BlastRadius: "repo",
	}
}

func nativeInterruptDecision(workID, runAttemptID, actionID string, now time.Time) envelope.PolicyDecision {
	return envelope.PolicyDecision{
		Header: envelope.Header{
			SchemaVersion: envelope.SVPolicyDecision, ID: "pol_native_interrupt",
			CreatedAt: now.Add(time.Second).UTC().Format(time.RFC3339),
			Producer:  envelope.Producer{Kind: "policy", Name: "compound"},
			IDs:       envelope.IDs{WorkID: workID},
			Authority: envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:   "private", Risk: "tier2", EvidenceRefs: []string{runAttemptID},
			Uncertainty: envelope.Uncertainty{Level: "known"},
		},
		ActionIntentID: actionID, Outcome: "hold",
		Reasons: []string{"active native action invalidated"}, Obligations: []string{},
	}
}
