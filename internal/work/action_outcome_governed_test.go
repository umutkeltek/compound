package work

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
)

func TestRecordReceiptedActionPersistsExactGovernedOutcome(t *testing.T) {
	for _, tc := range []struct {
		name         string
		outcome      string
		changed      bool
		failureCause *envelope.FailureCause
	}{
		{name: "completed", outcome: "completed", changed: true},
		{name: "failed", outcome: "failed", changed: false, failureCause: &envelope.FailureCause{SurfaceOutcome: "permission apply failed", Mechanism: "concurrent target mutation"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			w := governedActionRunningWork(t, s, "operator:test")
			action, decision := governedActionApproval(w, "operator:test", fixedNow.Add(time.Minute))
			result, err := s.RecordReceiptedAction(w.ID, ReceiptedActionInput{
				Action:               action,
				PreEvaluatedDecision: &decision,
				ExpectedWorkState:    "running",
				Outcome:              tc.outcome,
				Changed:              tc.changed,
				Summary:              "permission activation " + tc.outcome,
				MoveType:             "control_plane_repair",
				ProofRefs:            []string{"artifact:permission-render"},
				BeforeRefs:           []string{"sha256:preimage"},
				AfterRefs:            []string{"sha256:postimage"},
				FilesChanged:         []string{"/synthetic/settings.json"},
				FailureCause:         tc.failureCause,
			}, fixedNow.Add(2*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision.ID != decision.ID || !reflect.DeepEqual(result.Decision, decision) {
				t.Fatalf("persisted decision differs from evaluated decision:\n got=%+v\nwant=%+v", result.Decision, decision)
			}
			if result.Receipt.Outcome != tc.outcome || result.Receipt.StateDelta.Changed != tc.changed ||
				!reflect.DeepEqual(result.Receipt.FailureCause, tc.failureCause) {
				t.Fatalf("receipt outcome = %+v", result.Receipt)
			}
			if !contains(result.Receipt.ProofRefs, "sha256:preimage") || !contains(result.Receipt.ProofRefs, "sha256:postimage") ||
				!contains(result.Transition.BeforeRefs, "sha256:preimage") || !contains(result.Transition.AfterRefs, "sha256:postimage") {
				t.Fatalf("pre/post proof refs missing: receipt=%v transition=%+v", result.Receipt.ProofRefs, result.Transition)
			}
			if len(result.Receipt.StateDelta.FilesChanged) != 1 || result.Receipt.StateDelta.FilesChanged[0] != "/synthetic/settings.json" {
				t.Fatalf("files_changed = %v", result.Receipt.StateDelta.FilesChanged)
			}
			if result.Action.IDs.WorkID != w.ID || result.Decision.IDs.WorkID != w.ID || result.Receipt.IDs.WorkID != w.ID || result.Transition.IDs.WorkID != w.ID {
				t.Fatalf("governed outcome broke work chain: %+v", result)
			}
		})
	}
}

func TestReceiptedActionReservationBlocksLifecycleUntilFinalizeOrAbort(t *testing.T) {
	newInput := func(w envelope.WorkEnvelope, action envelope.ActionIntent, decision envelope.PolicyDecision) ReceiptedActionInput {
		return ReceiptedActionInput{
			Action: action, PreEvaluatedDecision: &decision, ExpectedWorkState: "running",
			Outcome: "completed", Changed: true, Summary: "permission activation completed", MoveType: "control_plane_repair",
			BeforeRefs: []string{"sha256:preimage"}, AfterRefs: []string{"sha256:postimage"},
		}
	}
	for _, tc := range []struct {
		name     string
		complete func(t *testing.T, s *Store, w envelope.WorkEnvelope, in ReceiptedActionInput, at time.Time)
	}{
		{name: "finalize", complete: func(t *testing.T, s *Store, w envelope.WorkEnvelope, in ReceiptedActionInput, at time.Time) {
			if result, err := s.RecordReservedReceiptedAction(w.ID, in, at); err != nil || result.Receipt.ID == "" {
				t.Fatalf("finalize reservation: result=%+v err=%v", result, err)
			}
		}},
		{name: "abort", complete: func(t *testing.T, s *Store, w envelope.WorkEnvelope, in ReceiptedActionInput, _ time.Time) {
			if err := s.AbortReceiptedActionReservation(w.ID, in.Action.ID); err != nil {
				t.Fatalf("abort reservation: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			w := governedActionRunningWork(t, s, "operator:test")
			action, decision := governedActionApproval(w, "operator:test", fixedNow.Add(time.Minute))
			in := newInput(w, action, decision)
			authorizedAt := fixedNow.Add(2 * time.Minute)
			if err := s.ReserveReceiptedAction(w.ID, in, authorizedAt, func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Advance(w.ID, AdvanceInput{To: "verifying", Actor: "operator:test"}, fixedNow.Add(3*time.Minute)); err == nil ||
				!strings.Contains(err.Error(), "pending receipted action "+action.ID) {
				t.Fatalf("advance crossed reservation: %v", err)
			}
			tc.complete(t, s, w, in, authorizedAt)
			if advanced, err := s.Advance(w.ID, AdvanceInput{To: "verifying", Actor: "operator:test"}, fixedNow.Add(4*time.Minute)); err != nil || advanced.State != "verifying" {
				t.Fatalf("advance after reservation completion: work=%+v err=%v", advanced, err)
			}
		})
	}
}

func TestReceiptedActionReservationAuthorizationFailureDoesNotBlockWork(t *testing.T) {
	s := newStore(t)
	w := governedActionRunningWork(t, s, "operator:test")
	action, decision := governedActionApproval(w, "operator:test", fixedNow.Add(time.Minute))
	in := ReceiptedActionInput{
		Action: action, PreEvaluatedDecision: &decision, ExpectedWorkState: "running",
		Outcome: "completed", Changed: true, Summary: "permission activation completed", MoveType: "control_plane_repair",
	}
	injected := errors.New("authorization revoked")
	if err := s.ReserveReceiptedAction(w.ID, in, fixedNow.Add(2*time.Minute), func() error { return injected }); !errors.Is(err, injected) {
		t.Fatalf("ReserveReceiptedAction error = %v", err)
	}
	if advanced, err := s.Advance(w.ID, AdvanceInput{To: "verifying", Actor: "operator:test"}, fixedNow.Add(3*time.Minute)); err != nil || advanced.State != "verifying" {
		t.Fatalf("authorization failure left reservation: work=%+v err=%v", advanced, err)
	}
}

func TestRecordReservedReceiptedActionReplaysCompletedOutcomeAfterLifecycleAdvances(t *testing.T) {
	s := newStore(t)
	w := governedActionRunningWork(t, s, "operator:test")
	action, decision := governedActionApproval(w, "operator:test", fixedNow.Add(time.Minute))
	in := ReceiptedActionInput{
		Action: action, PreEvaluatedDecision: &decision, ExpectedWorkState: "running",
		Outcome: "completed", Changed: true, Summary: "permission activation completed", MoveType: "control_plane_repair",
		BeforeRefs: []string{"sha256:preimage"}, AfterRefs: []string{"sha256:postimage"},
	}
	authorizedAt := fixedNow.Add(2 * time.Minute)
	if err := s.ReserveReceiptedAction(w.ID, in, authorizedAt, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	first, err := s.RecordReservedReceiptedAction(w.ID, in, authorizedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "verifying", Actor: "operator:test"}, fixedNow.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	replay, err := s.RecordReservedReceiptedAction(w.ID, in, authorizedAt)
	if err != nil {
		t.Fatalf("replay finalized reservation after lifecycle advance: %v", err)
	}
	if !replay.Replayed || replay.Receipt.ID != first.Receipt.ID || replay.Transition.ID != first.Transition.ID {
		t.Fatalf("replay = %+v, want exact completed transaction %+v", replay, first)
	}
}

func TestCompletedReceiptedActionPersistsExactOutputDigests(t *testing.T) {
	s, w, in, authorizedAt, result := completedGovernedAction(t)
	path := filepath.Join(s.home, "work", "receipted-action-completions", w.ID, in.Action.ID+".json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var completion map[string]any
	if err := json.Unmarshal(body, &completion); err != nil {
		t.Fatal(err)
	}
	if got := completion["schema_version"]; got != "compound.receipted_action_completion.v0.1" {
		t.Fatalf("completion schema_version = %v", got)
	}
	for key, wantID := range map[string]string{
		"receipt_sha256":    result.Receipt.ID,
		"transition_sha256": result.Transition.ID,
		"event_sha256":      result.Action.ID,
	} {
		got, ok := completion[key].(string)
		if !ok || len(got) != 64 {
			t.Fatalf("completion %s = %v, want SHA-256 binding for %s", key, completion[key], wantID)
		}
	}
	if replay, err := s.RecordReservedReceiptedAction(w.ID, in, authorizedAt); err != nil || !replay.Replayed {
		t.Fatalf("exact completion did not replay: result=%+v err=%v", replay, err)
	}
}

func TestCompletedReceiptedActionRejectsLegacyAndMissingExactOutputDigests(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{
			name: "legacy completion schema",
			mutate: func(completion map[string]any) {
				completion["schema_version"] = receiptedActionReservationSchemaVersion
				delete(completion, "receipt_sha256")
				delete(completion, "transition_sha256")
				delete(completion, "event_sha256")
			},
			want: "schema_version",
		},
		{name: "missing receipt digest", mutate: func(completion map[string]any) { delete(completion, "receipt_sha256") }, want: "receipt_sha256"},
		{name: "missing transition digest", mutate: func(completion map[string]any) { delete(completion, "transition_sha256") }, want: "transition_sha256"},
		{name: "missing event digest", mutate: func(completion map[string]any) { delete(completion, "event_sha256") }, want: "event_sha256"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, w, in, authorizedAt, _ := completedGovernedAction(t)
			path := filepath.Join(s.home, "work", "receipted-action-completions", w.ID, in.Action.ID+".json")
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var completion map[string]any
			if err := json.Unmarshal(body, &completion); err != nil {
				t.Fatal(err)
			}
			completion["schema_version"] = "compound.receipted_action_completion.v0.1"
			tc.mutate(completion)
			body, err = json.Marshal(completion)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, body, 0o644); err != nil {
				t.Fatal(err)
			}
			if replay, err := s.RecordReservedReceiptedAction(w.ID, in, authorizedAt); err == nil || replay.Replayed || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("completion tamper replayed: result=%+v err=%v, want %q", replay, err, tc.want)
			}
		})
	}
}

func TestCompletedReceiptedActionRejectsSchemaValidExactOutputTamper(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, *Store, ReceiptedActionResult)
	}{
		{name: "receipt header", mutate: func(t *testing.T, s *Store, result ReceiptedActionResult) {
			tamperGovernedReceipt(t, s, result, func(receipt *envelope.Receipt) { receipt.Authority.Owner = "tampered" })
		}},
		{name: "receipt producer", mutate: func(t *testing.T, s *Store, result ReceiptedActionResult) {
			tamperGovernedReceipt(t, s, result, func(receipt *envelope.Receipt) { receipt.Producer.Name = "tampered" })
		}},
		{name: "receipt skills missed", mutate: func(t *testing.T, s *Store, result ReceiptedActionResult) {
			tamperGovernedReceipt(t, s, result, func(receipt *envelope.Receipt) { receipt.Skills.Missed = []string{"skill:tampered"} })
		}},
		{name: "receipt cost", mutate: func(t *testing.T, s *Store, result ReceiptedActionResult) {
			tamperGovernedReceipt(t, s, result, func(receipt *envelope.Receipt) { tokens := 1; receipt.Cost.Tokens = &tokens })
		}},
		{name: "receipt memory", mutate: func(t *testing.T, s *Store, result ReceiptedActionResult) {
			tamperGovernedReceipt(t, s, result, func(receipt *envelope.Receipt) { receipt.Memory.Candidates = []string{"candidate:tampered"} })
		}},
		{name: "receipt next", mutate: func(t *testing.T, s *Store, result ReceiptedActionResult) {
			tamperGovernedReceipt(t, s, result, func(receipt *envelope.Receipt) { receipt.Next.SuggestedWork = []string{"work_tampered"} })
		}},
		{name: "receipt artifacts", mutate: func(t *testing.T, s *Store, result ReceiptedActionResult) {
			tamperGovernedReceipt(t, s, result, func(receipt *envelope.Receipt) { receipt.StateDelta.Artifacts = []string{"artifact:tampered"} })
		}},
		{name: "receipt verification", mutate: func(t *testing.T, s *Store, result ReceiptedActionResult) {
			tamperGovernedReceipt(t, s, result, func(receipt *envelope.Receipt) { receipt.StateDelta.Verification = []string{"check:tampered"} })
		}},
		{name: "transition header", mutate: func(t *testing.T, s *Store, result ReceiptedActionResult) {
			tamperGovernedTransition(t, s, result, func(transition *envelope.Transition) { transition.Authority.Owner = "tampered" })
		}},
		{name: "transition producer", mutate: func(t *testing.T, s *Store, result ReceiptedActionResult) {
			tamperGovernedTransition(t, s, result, func(transition *envelope.Transition) { transition.Producer.Name = "tampered" })
		}},
		{name: "transition risk projection", mutate: func(t *testing.T, s *Store, result ReceiptedActionResult) {
			tamperGovernedTransition(t, s, result, func(transition *envelope.Transition) { transition.Projections.Risk = []string{"risk:tampered"} })
		}},
		{name: "transition should harden", mutate: func(t *testing.T, s *Store, result ReceiptedActionResult) {
			tamperGovernedTransition(t, s, result, func(transition *envelope.Transition) { transition.SecondOrder.ShouldHarden = true })
		}},
		{name: "transition should create rule", mutate: func(t *testing.T, s *Store, result ReceiptedActionResult) {
			tamperGovernedTransition(t, s, result, func(transition *envelope.Transition) { transition.SecondOrder.ShouldCreateRule = true })
		}},
		{name: "transition should decay", mutate: func(t *testing.T, s *Store, result ReceiptedActionResult) {
			tamperGovernedTransition(t, s, result, func(transition *envelope.Transition) { transition.SecondOrder.ShouldDecay = true })
		}},
		{name: "transition revisit at", mutate: func(t *testing.T, s *Store, result ReceiptedActionResult) {
			tamperGovernedTransition(t, s, result, func(transition *envelope.Transition) {
				transition.SecondOrder.RevisitAt = fixedNow.Add(24 * time.Hour).Format(time.RFC3339)
			})
		}},
		{name: "lifecycle equal state", mutate: func(t *testing.T, s *Store, result ReceiptedActionResult) {
			tamperGovernedLifecycleEvent(t, s, result.Action.ID, func(event *Event) { event.From, event.To = "verifying", "verifying" })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, w, in, authorizedAt, result := completedGovernedAction(t)
			tc.mutate(t, s, result)
			if replay, err := s.RecordReservedReceiptedAction(w.ID, in, authorizedAt); err == nil || replay.Replayed || !strings.Contains(err.Error(), "digest") {
				t.Fatalf("schema-valid tamper replayed: result=%+v err=%v", replay, err)
			}
		})
	}
}

func completedGovernedAction(t *testing.T) (*Store, envelope.WorkEnvelope, ReceiptedActionInput, time.Time, ReceiptedActionResult) {
	t.Helper()
	s := newStore(t)
	w := governedActionRunningWork(t, s, "operator:test")
	action, decision := governedActionApproval(w, "operator:test", fixedNow.Add(time.Minute))
	in := ReceiptedActionInput{
		Action: action, PreEvaluatedDecision: &decision, ExpectedWorkState: "running",
		Outcome: "completed", Changed: true, Summary: "permission activation completed", MoveType: "control_plane_repair",
		BeforeRefs: []string{"sha256:preimage"}, AfterRefs: []string{"sha256:postimage"},
	}
	authorizedAt := fixedNow.Add(2 * time.Minute)
	if err := s.ReserveReceiptedAction(w.ID, in, authorizedAt, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	result, err := s.RecordReservedReceiptedAction(w.ID, in, authorizedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "verifying", Actor: "operator:test"}, fixedNow.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	return s, w, in, authorizedAt, result
}

func tamperGovernedReceipt(t *testing.T, s *Store, result ReceiptedActionResult, mutate func(*envelope.Receipt)) {
	t.Helper()
	var receipt envelope.Receipt
	if err := s.readJSON(s.envPath(result.Receipt.ID), &receipt); err != nil {
		t.Fatal(err)
	}
	mutate(&receipt)
	if err := receipt.Validate(); err != nil {
		t.Fatalf("receipt tamper is not schema-valid: %v", err)
	}
	if err := s.writeJSON(s.envPath(receipt.ID), &receipt); err != nil {
		t.Fatal(err)
	}
}

func tamperGovernedTransition(t *testing.T, s *Store, result ReceiptedActionResult, mutate func(*envelope.Transition)) {
	t.Helper()
	var transition envelope.Transition
	if err := s.readJSON(s.envPath(result.Transition.ID), &transition); err != nil {
		t.Fatal(err)
	}
	mutate(&transition)
	if err := transition.Validate(); err != nil {
		t.Fatalf("transition tamper is not schema-valid: %v", err)
	}
	if err := s.writeJSON(s.envPath(transition.ID), &transition); err != nil {
		t.Fatal(err)
	}
}

func tamperGovernedLifecycleEvent(t *testing.T, s *Store, actionID string, mutate func(*Event)) {
	t.Helper()
	body, err := os.ReadFile(s.eventsLog())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	matched := 0
	for i, line := range lines {
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if !contains(event.Attached, actionID) {
			continue
		}
		mutate(&event)
		if event.From != event.To {
			t.Fatalf("lifecycle tamper is not an equal-state event: %+v", event)
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		lines[i] = string(encoded)
		matched++
	}
	if matched != 1 {
		t.Fatalf("matched %d lifecycle events for %s", matched, actionID)
	}
	if err := os.WriteFile(s.eventsLog(), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAbortReceiptedActionReservationRefusesAfterReceiptPublicationBegins(t *testing.T) {
	s := newStore(t)
	w := governedActionRunningWork(t, s, "operator:test")
	action, decision := governedActionApproval(w, "operator:test", fixedNow.Add(time.Minute))
	in := ReceiptedActionInput{
		Action: action, PreEvaluatedDecision: &decision, ExpectedWorkState: "running",
		Outcome: "completed", Changed: true, Summary: "permission activation completed", MoveType: "control_plane_repair",
		BeforeRefs: []string{"sha256:preimage"}, AfterRefs: []string{"sha256:postimage"},
	}
	authorizedAt := fixedNow.Add(2 * time.Minute)
	if err := s.ReserveReceiptedAction(w.ID, in, authorizedAt, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("receipt transaction interrupted after action publication")
	previous := receiptedActionAfterWrite
	receiptedActionAfterWrite = func(stage string) error {
		if stage == "action" {
			return injected
		}
		return nil
	}
	if _, err := s.RecordReservedReceiptedAction(w.ID, in, authorizedAt); !errors.Is(err, injected) {
		t.Fatalf("RecordReservedReceiptedAction error = %v", err)
	}
	receiptedActionAfterWrite = previous
	t.Cleanup(func() { receiptedActionAfterWrite = previous })
	if err := s.AbortReceiptedActionReservation(w.ID, action.ID); err == nil || !strings.Contains(err.Error(), "publication began") {
		t.Fatalf("AbortReceiptedActionReservation error = %v", err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "verifying", Actor: "operator:test"}, fixedNow.Add(3*time.Minute)); err == nil || !strings.Contains(err.Error(), "pending receipted action "+action.ID) {
		t.Fatalf("partial receipt transaction no longer blocked lifecycle: %v", err)
	}
	if result, err := s.RecordReservedReceiptedAction(w.ID, in, authorizedAt); err != nil || result.Receipt.ID == "" {
		t.Fatalf("recover partial reserved transaction: result=%+v err=%v", result, err)
	}
}

func TestRecordReceiptedActionRefusesInvalidPreEvaluatedSecurityDecisionWithoutWriting(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(t *testing.T, s *Store) envelope.WorkEnvelope
		mutate func(*envelope.ActionIntent, *envelope.PolicyDecision)
		want   string
	}{
		{
			name: "ready work",
			setup: func(t *testing.T, s *Store) envelope.WorkEnvelope {
				w, err := s.Create(CreateInput{Owner: "operator:test", Actor: "operator:test", SuccessPredicate: "permission outcome", State: "ready"}, fixedNow)
				if err != nil {
					t.Fatal(err)
				}
				return w
			},
			want: "running",
		},
		{
			name: "wrong lease holder",
			setup: func(t *testing.T, s *Store) envelope.WorkEnvelope {
				return governedActionRunningWork(t, s, "operator:lease-holder")
			},
			want: "lease holder",
		},
		{
			name:  "non operator actor",
			setup: func(t *testing.T, s *Store) envelope.WorkEnvelope { return governedActionRunningWork(t, s, "codex") },
			mutate: func(action *envelope.ActionIntent, _ *envelope.PolicyDecision) {
				action.Actor = "codex"
			},
			want: "operator provenance",
		},
		{
			name: "mismatched decision",
			setup: func(t *testing.T, s *Store) envelope.WorkEnvelope {
				return governedActionRunningWork(t, s, "operator:test")
			},
			mutate: func(_ *envelope.ActionIntent, decision *envelope.PolicyDecision) {
				decision.ActionIntentID = "act_other"
			},
			want: "bind",
		},
		{
			name: "held decision",
			setup: func(t *testing.T, s *Store) envelope.WorkEnvelope {
				return governedActionRunningWork(t, s, "operator:test")
			},
			mutate: func(_ *envelope.ActionIntent, decision *envelope.PolicyDecision) {
				decision.Outcome = "hold"
			},
			want: "allowed",
		},
		{
			name: "expired decision",
			setup: func(t *testing.T, s *Store) envelope.WorkEnvelope {
				return governedActionRunningWork(t, s, "operator:test")
			},
			mutate: func(_ *envelope.ActionIntent, decision *envelope.PolicyDecision) {
				decision.ExpiresAt = fixedNow.Add(time.Minute).Format(time.RFC3339)
			},
			want: "expired",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			w := tc.setup(t, s)
			action, decision := governedActionApproval(w, "operator:test", fixedNow.Add(time.Minute))
			if tc.mutate != nil {
				tc.mutate(&action, &decision)
			}
			before, err := os.ReadFile(s.workPath(w.ID))
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.RecordReceiptedAction(w.ID, ReceiptedActionInput{
				Action: action, PreEvaluatedDecision: &decision, ExpectedWorkState: "running",
				Outcome: "completed", Changed: true, Summary: "permission activation completed", MoveType: "control_plane_repair",
				BeforeRefs: []string{"sha256:preimage"}, AfterRefs: []string{"sha256:postimage"},
			}, fixedNow.Add(2*time.Minute))
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Fatalf("RecordReceiptedAction error = %v, want containing %q", err, tc.want)
			}
			after, readErr := os.ReadFile(s.workPath(w.ID))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatal("refused pre-evaluated action changed work")
			}
			for _, id := range []string{action.ID, decision.ID} {
				if _, statErr := os.Stat(s.envPath(id)); !os.IsNotExist(statErr) {
					t.Fatalf("refused pre-evaluated action persisted %s: %v", id, statErr)
				}
			}
		})
	}
}

func governedActionRunningWork(t *testing.T, s *Store, leaseHolder string) envelope.WorkEnvelope {
	t.Helper()
	w, err := s.Create(CreateInput{Owner: "operator:test", Actor: "operator:test", SuccessPredicate: "permission outcome", State: "ready"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(w.ID, leaseHolder, fixedNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	w, err = s.Advance(w.ID, AdvanceInput{To: "running", Actor: leaseHolder}, fixedNow.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func governedActionApproval(w envelope.WorkEnvelope, actor string, now time.Time) (envelope.ActionIntent, envelope.PolicyDecision) {
	action := envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent, ID: "act_permission_governed", CreatedAt: now.UTC().Format(time.RFC3339),
			Producer: envelope.Producer{Kind: "cli", Name: "compound"}, IDs: envelope.IDs{WorkID: w.ID},
			Authority: envelope.Authority{SourceClass: "runtime", Owner: "compound", Status: "current"}, Privacy: "private", Risk: "tier1",
			EvidenceRefs: []string{"sha256:approved-render"}, Uncertainty: envelope.Uncertainty{Level: "known"},
		},
		Actor: actor, Capability: "harness.permissions.apply", Action: "reconcile_harness_permissions",
		Target:        envelope.ActionTarget{Kind: "harness_settings", ID: "claude_code", Path: "/synthetic/settings.json"},
		MutationClass: "security", Reversibility: "easy", BlastRadius: "local", RollbackPlan: "restore exact pre-image",
	}
	decision := envelope.PolicyDecision{
		Header: envelope.Header{
			SchemaVersion: envelope.SVPolicyDecision, ID: "pol_permission_governed", CreatedAt: now.UTC().Format(time.RFC3339),
			Producer: envelope.Producer{Kind: "cli", Name: "compound"}, IDs: envelope.IDs{WorkID: w.ID},
			Authority: envelope.Authority{SourceClass: "runtime", Status: "current"}, Privacy: "private", Risk: "tier1",
			EvidenceRefs: []string{"grant:permission"}, Uncertainty: envelope.Uncertainty{Level: "known"},
		},
		ActionIntentID: action.ID, Outcome: "allow_with_obligations", Reasons: []string{"covered by grant"},
		Obligations: []string{"emit receipt"}, StandingGrantID: "grant_permission",
	}
	return action, decision
}
