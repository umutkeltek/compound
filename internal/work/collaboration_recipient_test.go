package work

import (
	"reflect"
	"testing"
	"time"

	"compound/internal/runattempt"
)

func TestResolveCollaborationRecipientRequiresExactOwnedSessionBinding(t *testing.T) {
	t.Parallel()
	t.Run("sole workspace candidate does not authorize unknown session", func(t *testing.T) {
		store := newStore(t)
		workspace := t.TempDir()
		item, err := store.Create(CreateInput{
			Owner: "codex:holder", SuccessPredicate: "native session authority is exact",
		}, fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Claim(item.ID, "codex:holder", fixedNow.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		attempt := bindCollaborationRecipientForTest(
			t, store, item.ID, "codex:holder", "codex:holder", "codex", "owned-session", workspace,
			fixedNow.Add(2*time.Second),
		)
		binding, alive, err := store.latestDelegateBinding(item.ID)
		if err != nil || !alive || binding == nil || binding.Session != "owned-session" ||
			binding.RunAttemptID != attempt.ID {
			t.Fatalf("test binding was not current: binding=%+v alive=%v err=%v", binding, alive, err)
		}
		current, err := store.Get(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !collaborationContains(current.RunAttemptIDs, attempt.ID) ||
			attempt.Origin != runattempt.OriginHarnessExecution ||
			attempt.Workspace != workspace {
			t.Fatalf("test attempt was not resolvable: work=%+v attempt=%+v", current, attempt)
		}

		resolved, ok, err := store.ResolveCollaborationRecipient(CollaborationRecipientQuery{
			Harness: "codex", SessionID: "owned-session", Cwd: workspace,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !ok || resolved.RunAttemptID != attempt.ID {
			t.Fatalf("exact owned session did not resolve: ok=%v recipient=%+v", ok, resolved)
		}
		if unknown, ok, err := store.ResolveCollaborationRecipient(CollaborationRecipientQuery{
			Harness: "codex", SessionID: "unknown-session", Cwd: workspace,
		}); err != nil || ok || !reflect.DeepEqual(unknown, CollaborationRecipientResolution{}) {
			t.Fatalf("unknown session inherited sole workspace recipient: ok=%v recipient=%+v err=%v",
				ok, unknown, err)
		}
	})

	t.Run("two sessions sharing workspace and harness select only exact binding", func(t *testing.T) {
		store := newStore(t)
		workspace := t.TempDir()
		var attempts []runattempt.RunAttempt
		for index, session := range []string{"session-one", "session-two"} {
			item, err := store.Create(CreateInput{
				Owner: "codex:holder", SuccessPredicate: "session exactness " + session,
			}, fixedNow.Add(time.Duration(index)*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Claim(item.ID, "codex:holder", fixedNow.Add(time.Duration(index+2)*time.Second)); err != nil {
				t.Fatal(err)
			}
			attempts = append(attempts, bindCollaborationRecipientForTest(
				t, store, item.ID, "codex:holder", "codex:holder", "codex", session, workspace,
				fixedNow.Add(time.Duration(index+4)*time.Second),
			))
		}
		for index, session := range []string{"session-one", "session-two"} {
			resolved, ok, err := store.ResolveCollaborationRecipient(CollaborationRecipientQuery{
				Harness: "codex", SessionID: session, Cwd: workspace,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !ok || resolved.RunAttemptID != attempts[index].ID {
				t.Fatalf("%s resolved recipient = %+v ok=%v, want %s",
					session, resolved, ok, attempts[index].ID)
			}
		}
		if unknown, ok, err := store.ResolveCollaborationRecipient(CollaborationRecipientQuery{
			Harness: "codex", SessionID: "unknown-session", Cwd: workspace,
		}); err != nil || ok || !reflect.DeepEqual(unknown, CollaborationRecipientResolution{}) {
			t.Fatalf("unknown session resolved among exact candidates: ok=%v recipient=%+v err=%v",
				ok, unknown, err)
		}
	})
}

func TestResolveCollaborationRecipientAmbiguousSpawnIsNotAbsent(t *testing.T) {
	t.Parallel()
	store := newStore(t)
	workspace := t.TempDir()
	for _, id := range []string{"work_first", "work_second"} {
		item, err := store.Create(CreateInput{WorkID: id, Owner: "codex:holder", SuccessPredicate: "ambiguous session refuses"}, fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Claim(item.ID, "codex:holder", fixedNow); err != nil {
			t.Fatal(err)
		}
		bindCollaborationRecipientForTest(t, store, item.ID, "codex:holder", "codex:holder", "codex", "same-session", workspace, fixedNow)
	}
	resolved, ok, err := store.ResolveCollaborationRecipient(CollaborationRecipientQuery{Harness: "codex", SessionID: "same-session", Cwd: workspace})
	if err != nil || ok || resolved.UnresolvedReason == "" {
		t.Fatalf("ambiguous typed identity reported absence: ok=%v resolved=%+v err=%v", ok, resolved, err)
	}
}

func bindCollaborationRecipientForTest(
	t *testing.T,
	store *Store,
	workID string,
	leaseHolder string,
	recipientActor string,
	harness string,
	session string,
	workspace string,
	now time.Time,
) runattempt.RunAttempt {
	t.Helper()
	attempts := runattempt.Open(store.home)
	attempt, err := attempts.Mint(workID, recipientActor, harness, now)
	if err != nil {
		t.Fatal(err)
	}
	if attempt, err = attempts.SetOrigin(attempt.ID, runattempt.OriginHarnessExecution); err != nil {
		t.Fatal(err)
	}
	if attempt, err = attempts.SetWorkspace(attempt.ID, workspace); err != nil {
		t.Fatal(err)
	}
	binding := &DelegateBinding{
		WorkID: workID, Session: session, Harness: harness, Holder: recipientActor,
		RecordedAt: now.UTC().Format(time.RFC3339), RunAttemptID: attempt.ID,
	}
	eventID, err := store.recordDelegateSpawned(workID, binding, false, attempt.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Get(workID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.appendDelegateLifecycleEvent(
		workID, item.State, leaseHolder, eventID, "test exact native session binding", now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(workID, AdvanceInput{
		To: item.State, Actor: leaseHolder, RunAttemptIDs: []string{attempt.ID},
		Note: "test native run attached",
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if attempt, err = attempts.MarkRunning(attempt.ID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	return attempt
}
