package work

import (
	"errors"
	"os"
	"os/exec"
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
	workbench, err := s.Workbench(fixedNow.Add(2*time.Minute), WorkbenchOptions{RecentLimit: 10, Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if workbench.Counts.Open != 1 || workbench.Counts.Terminal != 0 || workbench.Counts.InProgress != 1 || len(workbench.Open) != 1 || len(workbench.Recent) != 0 {
		t.Fatalf("nonterminal action changed parent completion counts: %+v", workbench.Counts)
	}
	open := workbench.Open[0]
	if open.State != "running" || open.ReceiptID != "" || open.TransitionID != "" || open.MoveType != "" || open.Changed {
		t.Fatalf("intermediate action proof leaked into parent completion projection: %+v", open)
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

func TestRecordReceiptedActionTerminalProjectionUsesClosingTransitionReceipt(t *testing.T) {
	s := newStore(t)
	w := receiptedActionRunningWork(t, s)
	claimID := receiptedActionRuling(t, s, "receipted-action-terminal-projection", "", fixedNow)
	intermediate, err := s.RecordReceiptedAction(w.ID, ReceiptedActionInput{
		Action:                     receiptedActionIntent(w, "act_receipted_terminal_projection", fixedNow.Add(time.Minute)),
		OperationalDecisionClaimID: claimID,
		ExpectedWorkState:          "running",
		Summary:                    "resume the lane without completing the parent work",
		MoveType:                   "control_plane_repair",
		ProofRefs:                  []string{"claim:" + claimID},
	}, fixedNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	eventsBeforeClose, err := os.ReadFile(s.eventsLog())
	if err != nil {
		t.Fatal(err)
	}
	closed, err := s.Close(w.ID, CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Actor:     "codex",
		Summary:   "parent work completed with its own terminal proof",
		Changed:   true,
		ProofRefs: []string{"packet-work-contract:parent-terminal-proof"},
	}, fixedNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	// Simulate Close stopping after its terminal WorkEnvelope write but before
	// its final lifecycle event. The canonical transition still identifies the
	// closing receipt; the earlier ask-resume event must never substitute for it.
	if err := os.WriteFile(s.eventsLog(), eventsBeforeClose, 0o644); err != nil {
		t.Fatal(err)
	}

	workbench, err := s.Workbench(fixedNow.Add(3*time.Minute), WorkbenchOptions{RecentLimit: 10, Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(workbench.Recent) != 1 {
		t.Fatalf("terminal parent missing from workbench: %+v", workbench)
	}
	item := workbench.Recent[0]
	if item.ReceiptID != closed.Receipt.ID || item.TransitionID != closed.Transition.ID {
		t.Fatalf("terminal projection paired receipt=%q transition=%q, want closing %q/%q (intermediate receipt %q)", item.ReceiptID, item.TransitionID, closed.Receipt.ID, closed.Transition.ID, intermediate.Receipt.ID)
	}
	if item.ReceiptID == intermediate.Receipt.ID {
		t.Fatalf("intermediate ask-resume receipt projected as parent completion: %+v", item)
	}
}

func TestRecordReceiptedActionRecoversEveryWriteBoundary(t *testing.T) {
	boundaries := []string{"action", "decision", "receipt", "transition", "work", "event"}
	phases := []string{"before_publish", "after_publish"}
	for _, phase := range phases {
		for _, boundary := range boundaries {
			t.Run(phase+"/"+boundary, func(t *testing.T) {
				s := newStore(t)
				w := receiptedActionRunningWork(t, s)
				claimID := receiptedActionRuling(t, s, "receipted-action-recovery-"+phase+"-"+boundary, "", fixedNow)
				action := receiptedActionIntent(w, "act_receipted_recovery_"+phase+"_"+boundary, fixedNow.Add(time.Minute))
				in := ReceiptedActionInput{
					Action:                     action,
					OperationalDecisionClaimID: claimID,
					ExpectedWorkState:          "running",
					Summary:                    "continued the exact Codex turn after a recoverable write failure",
					MoveType:                   "control_plane_repair",
					ProofRefs:                  []string{"claim:" + claimID, "session:sess_receipted", "turn:turn_receipted"},
				}

				injected := errors.New("injected receipted-action write failure " + phase + " " + boundary)
				failed := false
				previousBeforeHook := receiptedActionBeforePublish
				previousHook := receiptedActionAfterWrite
				receiptedActionBeforePublish = func(got string) error {
					if phase == "before_publish" && got == boundary && !failed {
						failed = true
						return injected
					}
					return nil
				}
				receiptedActionAfterWrite = func(got string) error {
					if phase == "after_publish" && got == boundary && !failed {
						failed = true
						return injected
					}
					return nil
				}
				defer func() {
					receiptedActionBeforePublish = previousBeforeHook
					receiptedActionAfterWrite = previousHook
				}()

				if first, err := s.RecordReceiptedAction(w.ID, in, fixedNow.Add(time.Minute)); !errors.Is(err, injected) || first.Receipt.ID != "" {
					t.Fatalf("boundary %s did not expose the injected failure: err=%v result=%+v", boundary, err, first)
				}
				receiptedActionBeforePublish = func(string) error { return nil }
				receiptedActionAfterWrite = func(string) error { return nil }

				retryIn := in
				retryIn.Action.CreatedAt = fixedNow.Add(2 * time.Minute).UTC().Format(time.RFC3339)
				recovered, err := s.RecordReceiptedAction(w.ID, retryIn, fixedNow.Add(2*time.Minute))
				if err != nil {
					t.Fatalf("boundary %s retry did not recover: %v", boundary, err)
				}
				wantReplayed := phase == "after_publish" || boundary != "action"
				if recovered.Replayed != wantReplayed {
					t.Fatalf("boundary %s/%s retry replayed=%t, want %t: %+v", phase, boundary, recovered.Replayed, wantReplayed, recovered)
				}
				assertReceiptedActionConverged(t, s, w.ID, recovered)

				replayIn := in
				replayIn.Action.CreatedAt = fixedNow.Add(3 * time.Minute).UTC().Format(time.RFC3339)
				replay, err := s.RecordReceiptedAction(w.ID, replayIn, fixedNow.Add(3*time.Minute))
				if err != nil || !replay.Replayed || replay.Receipt.ID != recovered.Receipt.ID || replay.Transition.ID != recovered.Transition.ID {
					t.Fatalf("boundary %s stable replay failed: err=%v result=%+v", boundary, err, replay)
				}
				assertReceiptedActionConverged(t, s, w.ID, replay)
			})
		}
	}
}

func TestRecordReceiptedActionRefusesConflictingPartialWork(t *testing.T) {
	s := newStore(t)
	w := receiptedActionRunningWork(t, s)
	claimID := receiptedActionRuling(t, s, "receipted-action-conflicting-work", "", fixedNow)
	action := receiptedActionIntent(w, "act_receipted_conflicting_work", fixedNow.Add(time.Minute))
	in := ReceiptedActionInput{
		Action:                     action,
		OperationalDecisionClaimID: claimID,
		ExpectedWorkState:          "running",
		Summary:                    "continue only from a consistent partial transaction",
		MoveType:                   "control_plane_repair",
		ProofRefs:                  []string{"claim:" + claimID},
	}

	injected := errors.New("injected after work link")
	previousHook := receiptedActionAfterWrite
	receiptedActionAfterWrite = func(boundary string) error {
		if boundary == "work" {
			return injected
		}
		return nil
	}
	defer func() { receiptedActionAfterWrite = previousHook }()
	if _, err := s.RecordReceiptedAction(w.ID, in, fixedNow.Add(time.Minute)); !errors.Is(err, injected) {
		t.Fatalf("did not stop after work boundary: %v", err)
	}
	receiptedActionAfterWrite = func(string) error { return nil }

	conflicting, err := s.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	conflicting.IDs.TransitionID = idFor("tr_", "receipted-action", w.ID, action.ID, claimID)
	if err := s.writeJSON(s.workPath(w.ID), &conflicting); err != nil {
		t.Fatal(err)
	}
	if result, err := s.RecordReceiptedAction(w.ID, in, fixedNow.Add(2*time.Minute)); err == nil || !strings.Contains(err.Error(), "conflicts with persisted work") {
		t.Fatalf("conflicting partial work was recovered: err=%v result=%+v", err, result)
	}
	events, err := s.admissionEventsFor(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if contains(event.Attached, action.ID) {
			t.Fatalf("conflicting partial transaction appended an event: %+v", event)
		}
	}
}

func TestRecordReceiptedActionRefusesInvalidWorkEnvelope(t *testing.T) {
	s := newStore(t)
	w := receiptedActionRunningWork(t, s)
	claimID := receiptedActionRuling(t, s, "receipted-action-invalid-work", "", fixedNow)
	action := receiptedActionIntent(w, "act_receipted_invalid_work", fixedNow.Add(time.Minute))
	invalid := w
	invalid.Owner = ""
	if err := s.writeJSON(s.workPath(w.ID), &invalid); err != nil {
		t.Fatal(err)
	}

	result, err := s.RecordReceiptedAction(w.ID, ReceiptedActionInput{
		Action:                     action,
		OperationalDecisionClaimID: claimID,
		ExpectedWorkState:          "running",
		Summary:                    "do not complete an invalid partial work envelope",
		MoveType:                   "control_plane_repair",
		ProofRefs:                  []string{"claim:" + claimID},
	}, fixedNow.Add(time.Minute))
	if err == nil || !strings.Contains(err.Error(), "work envelope invalid") {
		t.Fatalf("invalid work envelope was completed: err=%v result=%+v", err, result)
	}
	if _, statErr := os.Stat(s.envPath(action.ID)); !os.IsNotExist(statErr) {
		t.Fatalf("invalid work envelope wrote action artifact: %v", statErr)
	}
}

func TestRecordReceiptedActionDiscardsTornStagedEventWrite(t *testing.T) {
	s := newStore(t)
	w := receiptedActionRunningWork(t, s)
	claimID := receiptedActionRuling(t, s, "receipted-action-torn-event", "", fixedNow)
	action := receiptedActionIntent(w, "act_receipted_torn_event", fixedNow.Add(time.Minute))
	in := ReceiptedActionInput{
		Action:                     action,
		OperationalDecisionClaimID: claimID,
		ExpectedWorkState:          "running",
		Summary:                    "recover after a torn event append",
		MoveType:                   "control_plane_repair",
		ProofRefs:                  []string{"claim:" + claimID},
	}

	injected := errors.New("injected torn event append")
	previousWriter := writeWorkEvent
	failed := false
	writeWorkEvent = func(file *os.File, data []byte) (int, error) {
		if !failed {
			failed = true
			n, _ := file.Write(data[:len(data)/2])
			return n, injected
		}
		return file.Write(data)
	}
	defer func() { writeWorkEvent = previousWriter }()
	if _, err := s.RecordReceiptedAction(w.ID, in, fixedNow.Add(time.Minute)); !errors.Is(err, injected) {
		t.Fatalf("torn event append did not return injected error: %v", err)
	}
	if _, err := s.admissionEventsFor(w.ID); err != nil {
		t.Fatalf("torn event append left a malformed canonical tail: %v", err)
	}
	writeWorkEvent = previousWriter
	recovered, err := s.RecordReceiptedAction(w.ID, in, fixedNow.Add(2*time.Minute))
	if err != nil || !recovered.Replayed {
		t.Fatalf("retry after torn event append did not recover: err=%v result=%+v", err, recovered)
	}
	assertReceiptedActionConverged(t, s, w.ID, recovered)
}

func TestRecordReceiptedActionCrashDuringEventWriteLeavesCanonicalLogIntact(t *testing.T) {
	const crashEnv = "COMPOUND_TEST_RECEIPTED_ACTION_EVENT_CRASH"
	if os.Getenv(crashEnv) == "1" {
		homePath := os.Getenv("COMPOUND_TEST_HOME")
		workID := os.Getenv("COMPOUND_TEST_WORK_ID")
		claimID := os.Getenv("COMPOUND_TEST_CLAIM_ID")
		actionID := os.Getenv("COMPOUND_TEST_ACTION_ID")
		s, err := Open(homePath)
		if err != nil {
			_, _ = os.Stderr.WriteString("open crash-test store: " + err.Error())
			os.Exit(81)
		}
		w, err := s.Get(workID)
		if err != nil {
			_, _ = os.Stderr.WriteString("read crash-test work: " + err.Error())
			os.Exit(82)
		}
		in := receiptedActionCrashInput(w, claimID, actionID)
		writeWorkEvent = func(file *os.File, data []byte) (int, error) {
			n, err := file.Write(data[:len(data)/2])
			if err != nil {
				_, _ = os.Stderr.WriteString("write crash-test event prefix: " + err.Error())
				os.Exit(83)
			}
			if err := file.Sync(); err != nil {
				_, _ = os.Stderr.WriteString("sync crash-test event prefix: " + err.Error())
				os.Exit(84)
			}
			os.Exit(86) // bypass rollback/defer paths like a killed writer
			return n, nil
		}
		if _, err := s.RecordReceiptedAction(workID, in, fixedNow.Add(time.Minute)); err != nil {
			_, _ = os.Stderr.WriteString("record crash-test action: " + err.Error())
			os.Exit(85)
		}
		os.Exit(87)
	}

	s := newStore(t)
	w := receiptedActionRunningWork(t, s)
	claimID := receiptedActionRuling(t, s, "receipted-action-event-crash", "", fixedNow)
	actionID := "act_receipted_event_crash"
	in := receiptedActionCrashInput(w, claimID, actionID)
	before, err := os.ReadFile(s.eventsLog())
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestRecordReceiptedActionCrashDuringEventWriteLeavesCanonicalLogIntact$")
	cmd.Env = append(os.Environ(),
		crashEnv+"=1",
		"COMPOUND_TEST_HOME="+s.home,
		"COMPOUND_TEST_WORK_ID="+w.ID,
		"COMPOUND_TEST_CLAIM_ID="+claimID,
		"COMPOUND_TEST_ACTION_ID="+actionID,
	)
	output, runErr := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 86 {
		t.Fatalf("crash helper exited without reaching the event write: err=%v output=%s", runErr, output)
	}
	afterCrash, err := os.ReadFile(s.eventsLog())
	if err != nil {
		t.Fatal(err)
	}
	if string(afterCrash) != string(before) {
		t.Fatalf("crashed event writer changed canonical log: before=%d bytes after=%d bytes", len(before), len(afterCrash))
	}

	recovered, err := s.RecordReceiptedAction(w.ID, in, fixedNow.Add(2*time.Minute))
	if err != nil || !recovered.Replayed {
		t.Fatalf("retry after event-writer crash did not recover: err=%v result=%+v", err, recovered)
	}
	assertReceiptedActionConverged(t, s, w.ID, recovered)
}

func TestRecordReceiptedActionControlledEventSerializesGenericAppend(t *testing.T) {
	s := newStore(t)
	w := receiptedActionRunningWork(t, s)
	claimID := receiptedActionRuling(t, s, "receipted-action-event-lock", "", fixedNow)
	in := receiptedActionCrashInput(w, claimID, "act_receipted_event_lock")

	controlledReady := make(chan struct{})
	releaseControlled := make(chan struct{})
	previousBeforeHook := receiptedActionBeforePublish
	receiptedActionBeforePublish = func(boundary string) error {
		if boundary == "event" {
			close(controlledReady)
			<-releaseControlled
		}
		return nil
	}
	defer func() { receiptedActionBeforePublish = previousBeforeHook }()

	type recordResult struct {
		result ReceiptedActionResult
		err    error
	}
	controlledDone := make(chan recordResult, 1)
	go func() {
		result, err := s.RecordReceiptedAction(w.ID, in, fixedNow.Add(time.Minute))
		controlledDone <- recordResult{result: result, err: err}
	}()
	<-controlledReady

	generic := Event{TS: ts(fixedNow.Add(2 * time.Minute)), WorkID: "work_concurrent_event", From: "running", To: "verifying", Note: "generic append racing controlled publication"}
	genericDone := make(chan error, 1)
	go func() { genericDone <- s.appendEvent(generic) }()
	select {
	case err := <-genericDone:
		t.Fatalf("generic append crossed controlled publication lock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseControlled)
	controlled := <-controlledDone
	if controlled.err != nil {
		t.Fatalf("controlled publication failed: %v", controlled.err)
	}
	if err := <-genericDone; err != nil {
		t.Fatalf("generic append failed after controlled publication: %v", err)
	}
	assertReceiptedActionConverged(t, s, w.ID, controlled.result)

	body, err := os.ReadFile(s.eventsLog())
	if err != nil {
		t.Fatal(err)
	}
	events, err := decodeAdmissionEvents(body, "")
	if err != nil {
		t.Fatal(err)
	}
	matching := 0
	for _, event := range events {
		if event.WorkID == generic.WorkID && event.Note == generic.Note {
			matching++
		}
	}
	if matching != 1 {
		t.Fatalf("generic append survived controlled rename %d times, want exactly once: %+v", matching, events)
	}
}

func receiptedActionCrashInput(w envelope.WorkEnvelope, claimID, actionID string) ReceiptedActionInput {
	return ReceiptedActionInput{
		Action:                     receiptedActionIntent(w, actionID, fixedNow.Add(time.Minute)),
		OperationalDecisionClaimID: claimID,
		ExpectedWorkState:          "running",
		Summary:                    "recover after the event writer exits mid-publication",
		MoveType:                   "control_plane_repair",
		ProofRefs:                  []string{"claim:" + claimID},
	}
}

func TestRecordReceiptedActionDiscardsTornStagedJSONWrites(t *testing.T) {
	for _, boundary := range []string{"action", "decision", "receipt", "transition", "work"} {
		t.Run(boundary, func(t *testing.T) {
			s := newStore(t)
			w := receiptedActionRunningWork(t, s)
			claimID := receiptedActionRuling(t, s, "receipted-action-torn-json-"+boundary, "", fixedNow)
			action := receiptedActionIntent(w, "act_receipted_torn_json_"+boundary, fixedNow.Add(time.Minute))
			in := ReceiptedActionInput{
				Action:                     action,
				OperationalDecisionClaimID: claimID,
				ExpectedWorkState:          "running",
				Summary:                    "recover after a torn staged JSON write",
				MoveType:                   "control_plane_repair",
				ProofRefs:                  []string{"claim:" + claimID},
			}

			injected := errors.New("injected torn staged JSON write at " + boundary)
			previousWriter := writeReceiptedActionTemp
			failed := false
			writeReceiptedActionTemp = func(got string, file *os.File, data []byte) error {
				if got == boundary && !failed {
					failed = true
					_, _ = file.Write(data[:len(data)/2])
					return injected
				}
				return previousWriter(got, file, data)
			}
			defer func() { writeReceiptedActionTemp = previousWriter }()
			if _, err := s.RecordReceiptedAction(w.ID, in, fixedNow.Add(time.Minute)); !errors.Is(err, injected) {
				t.Fatalf("torn %s stage did not return injected error: %v", boundary, err)
			}
			if _, err := s.Get(w.ID); err != nil {
				t.Fatalf("torn %s stage corrupted canonical work: %v", boundary, err)
			}
			writeReceiptedActionTemp = previousWriter
			recovered, err := s.RecordReceiptedAction(w.ID, in, fixedNow.Add(2*time.Minute))
			if err != nil {
				t.Fatalf("retry after torn %s stage did not recover: %v", boundary, err)
			}
			assertReceiptedActionConverged(t, s, w.ID, recovered)
		})
	}
}

func assertReceiptedActionConverged(t *testing.T, s *Store, workID string, result ReceiptedActionResult) {
	t.Helper()
	for _, item := range []struct {
		id string
	}{
		{result.Action.ID},
		{result.Decision.ID},
		{result.Receipt.ID},
		{result.Transition.ID},
	} {
		if item.id == "" {
			t.Fatalf("recovered transaction has an empty artifact id: %+v", result)
		}
		if _, err := os.Stat(s.envPath(item.id)); err != nil {
			t.Fatalf("artifact %s missing: %v", item.id, err)
		}
	}
	for _, item := range []struct {
		id     string
		target envelope.Envelope
	}{
		{result.Action.ID, &envelope.ActionIntent{}},
		{result.Decision.ID, &envelope.PolicyDecision{}},
		{result.Receipt.ID, &envelope.Receipt{}},
		{result.Transition.ID, &envelope.Transition{}},
	} {
		if err := s.readJSON(s.envPath(item.id), item.target); err != nil {
			t.Fatalf("read recovered artifact %s: %v", item.id, err)
		}
		if err := item.target.Validate(); err != nil {
			t.Fatalf("recovered artifact %s invalid: %v", item.id, err)
		}
	}
	if result.Action.IDs.WorkID != workID || result.Decision.IDs.WorkID != workID || result.Receipt.IDs.WorkID != workID || result.Transition.IDs.WorkID != workID {
		t.Fatalf("recovered artifacts do not share work %s: %+v", workID, result)
	}
	w, err := s.Get(workID)
	if err != nil {
		t.Fatal(err)
	}
	if w.State != "running" || w.IDs.TransitionID != "" || !contains(w.RunIDs, result.Action.ID) || w.PolicyDecisionID != result.Decision.ID {
		t.Fatalf("recovered parent link is inconsistent: %+v", w)
	}
	events, err := s.admissionEventsFor(workID)
	if err != nil {
		t.Fatal(err)
	}
	matching := 0
	for _, event := range events {
		if contains(event.Attached, result.Action.ID) && contains(event.Attached, result.Decision.ID) &&
			contains(event.Attached, result.Receipt.ID) && contains(event.Attached, result.Transition.ID) {
			matching++
		}
	}
	if matching != 1 {
		t.Fatalf("recovered transaction has %d matching events, want exactly one: %+v", matching, events)
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
