package work

import (
	"path/filepath"
	"testing"
	"time"

	"compound/internal/home"
)

func TestWorkbenchReportsOpenLoopsAndTerminalProof(t *testing.T) {
	s := newStore(t)
	ready, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "ready work gets claimed"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	held, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "held work gets reviewed", State: "held"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	running, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "running work gets verified"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(running.ID, "codex", fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(running.ID, AdvanceInput{To: "running"}, fixedNow); err != nil {
		t.Fatal(err)
	}
	done, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "terminal work has proof"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(done.ID, "codex", fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(done.ID, AdvanceInput{To: "running"}, fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(done.ID, AdvanceInput{To: "verifying", ArtifactID: "artifact_terminal", VerificationID: "ver_terminal"}, fixedNow); err != nil {
		t.Fatal(err)
	}
	closed, err := s.Close(done.ID, CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "workbench terminal proof",
		Changed:   true,
		ProofRefs: []string{"proof:workbench"},
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}

	report, err := s.Workbench(fixedNow, WorkbenchOptions{RecentLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts.Total != 4 || report.Counts.Open != 3 || report.Counts.Terminal != 1 {
		t.Fatalf("counts = %+v", report.Counts)
	}
	if report.Counts.NeedsClaim != 1 || report.Counts.NeedsReview != 1 || report.Counts.InProgress != 1 {
		t.Fatalf("open counts = %+v", report.Counts)
	}
	if report.Counts.MissingReceipt != 0 || report.Counts.MissingTransition != 0 {
		t.Fatalf("terminal proof counts = %+v", report.Counts)
	}
	assertWorkbenchItem(t, report.Open, ready.ID, "needs_claim", "claim before touching")
	assertWorkbenchItem(t, report.Open, held.ID, "needs_review", "resolve policy/source-control hold, then ready or cancel")
	assertWorkbenchItem(t, report.Open, running.ID, "in_progress", "advance verifying, completed, blocked, held, or failed")
	if len(report.Recent) != 1 {
		t.Fatalf("recent = %+v", report.Recent)
	}
	if report.Recent[0].WorkID != done.ID || report.Recent[0].ReceiptID != closed.Receipt.ID || report.Recent[0].TransitionID != closed.Transition.ID {
		t.Fatalf("terminal item missing receipt/transition proof: %+v", report.Recent[0])
	}
	if report.Recent[0].MoveType != "control_plane_repair" || !report.Recent[0].Changed {
		t.Fatalf("terminal item missing receipt learning fields: %+v", report.Recent[0])
	}
}

func TestWorkbenchExcludesArchivedWorkByDefaultAndResolvesByID(t *testing.T) {
	s := newStore(t)
	ready, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "ready work stays hot"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	done, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "completed work can archive"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(done.ID, "codex", fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(done.ID, AdvanceInput{To: "running"}, fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(done.ID, AdvanceInput{To: "verifying", VerificationID: "ver_archive"}, fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Close(done.ID, CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "archived work remains queryable",
		Changed:   true,
		ProofRefs: []string{"proof:archive"},
	}, fixedNow); err != nil {
		t.Fatal(err)
	}
	if err := home.Init(s.home, "test", fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := home.ArchiveOperationalHistory(s.home, home.OperationalArchiveOptions{Kinds: []string{"work"}}, fixedNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	report, err := s.Workbench(fixedNow.Add(2*time.Hour), WorkbenchOptions{RecentLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts.Open != 1 || report.Counts.Terminal != 0 || len(report.Recent) != 0 {
		t.Fatalf("default workbench should exclude archived terminal work: counts=%+v recent=%+v", report.Counts, report.Recent)
	}
	assertWorkbenchItem(t, report.Open, ready.ID, "needs_claim", "claim before touching")

	withArchived, err := s.Workbench(fixedNow.Add(2*time.Hour), WorkbenchOptions{RecentLimit: 10, IncludeArchived: true})
	if err != nil {
		t.Fatal(err)
	}
	if withArchived.Counts.Open != 1 || withArchived.Counts.Terminal != 1 || len(withArchived.Recent) != 1 || withArchived.Recent[0].WorkID != done.ID {
		t.Fatalf("include archived workbench should include terminal history: counts=%+v recent=%+v", withArchived.Counts, withArchived.Recent)
	}
	got, err := s.Get(done.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != done.ID || got.State != "completed" {
		t.Fatalf("archived work lookup = %+v", got)
	}
}

func TestWorkbenchSurfacesRunObservationEvidence(t *testing.T) {
	s := newStore(t)
	w := runningWork(t, s, "observed run gets verified")
	trace := writeTrace(t, s, "trace_workbench_observed", w.ID)
	result, err := s.ObserveRun(w.ID, ObserveRunInput{
		Actor:   "codex",
		TraceID: trace.ID,
		Note:    "workbench should surface this trace",
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.Workbench(fixedNow, WorkbenchOptions{RecentLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts.Open != 1 || report.Counts.InProgress != 1 || report.Counts.RunObservations != 1 {
		t.Fatalf("workbench counts missing observed run: %+v", report.Counts)
	}
	item := findWorkbenchItem(t, report.Open, w.ID)
	if item.State != "verifying" || item.NextAction != "close completed or failed, or return running" {
		t.Fatalf("observed workbench item has wrong state/action: %+v", item)
	}
	if !contains(item.VerificationIDs, trace.ID) || item.TransitionID != "" {
		t.Fatalf("observed workbench item should expose verification without close proof: %+v", item)
	}
	if len(item.RunObservations) != 1 {
		t.Fatalf("run observations = %+v", item.RunObservations)
	}
	observed := item.RunObservations[0]
	if observed.ID != result.ID || observed.TraceID != trace.ID || observed.PolicyDecisionID == "" || observed.ActionIntentID == "" {
		t.Fatalf("run observation summary = %+v result=%+v", observed, result)
	}
	if observed.Note != "workbench should surface this trace" || observed.ResultPath == "" {
		t.Fatalf("run observation summary missing note/path: %+v", observed)
	}
}

func TestWorkbenchSurfacesDispatchStepRunSummaries(t *testing.T) {
	s := newStore(t)
	ready, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "workbench sees dispatch run"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.DispatchStepRun(fixedNow, DispatchStepRunOptions{
		DispatchStepOptions: DispatchStepOptions{Actor: "codex"},
		MaxSteps:            5,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.Workbench(fixedNow, WorkbenchOptions{RecentLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts.DispatchStepRuns != 1 || len(report.DispatchStepRuns) != 1 {
		t.Fatalf("dispatch step run summaries missing: counts=%+v runs=%+v", report.Counts, report.DispatchStepRuns)
	}
	summary := report.DispatchStepRuns[0]
	if summary.ID != run.ID || summary.Status != "waiting" || summary.StepCount != 2 || summary.WaitReason != "run_evidence" {
		t.Fatalf("dispatch step run summary = %+v run=%+v", summary, run)
	}
	if len(summary.StepActions) != 2 || summary.StepActions[0] != "claim" || summary.StepActions[1] != "advance" {
		t.Fatalf("dispatch step run actions = %+v", summary.StepActions)
	}
	if !contains(summary.StepWorkIDs, ready.ID) || !contains(summary.BlockedOn, "work.run_evidence:"+ready.ID) {
		t.Fatalf("dispatch step run refs missing: %+v", summary)
	}
	if summary.ResultPath != run.ResultPath {
		t.Fatalf("dispatch step run path = %q, want %q", summary.ResultPath, run.ResultPath)
	}
}

func TestWorkbenchDispatchNextSuggestsSafeCommand(t *testing.T) {
	s := newStore(t)
	ready, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "ready work can dispatch"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.Workbench(fixedNow, WorkbenchOptions{RecentLimit: 10, Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	next := report.DispatchNext
	if !next.CanRun || next.Action != "claim" || next.Reason != "ready_work" || next.WorkID != ready.ID {
		t.Fatalf("ready dispatch suggestion = %+v", next)
	}
	want := []string{"compound", "work", "dispatch-step", "--actor", "codex", "--until-wait"}
	if len(next.Command) != len(want) {
		t.Fatalf("dispatch command = %v", next.Command)
	}
	for i := range want {
		if next.Command[i] != want[i] {
			t.Fatalf("dispatch command = %v", next.Command)
		}
	}
}

func TestWorkbenchSurfacesTerminalActivePacketProof(t *testing.T) {
	s := newStore(t)
	packetPath := filepath.Join("..", "..", "fixtures", "work", "goal-packet.md")
	imported, err := s.ImportPacket(packetPath, PacketImportOptions{
		Owner:         "compound-core",
		State:         "ready",
		ContextPackID: "ctx_packet",
	}, fixedNow)
	if err != nil {
		t.Fatalf("import packet: %v", err)
	}
	writeContextPack(t, s, "ctx_packet")
	if _, err := s.Claim(imported.Work.ID, "codex", fixedNow.Add(time.Second)); err != nil {
		t.Fatalf("claim packet work: %v", err)
	}
	if _, err := s.Advance(imported.Work.ID, AdvanceInput{To: "running", RunID: "run_packet"}, fixedNow.Add(2*time.Second)); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := s.Advance(imported.Work.ID, AdvanceInput{To: "verifying", VerificationID: "packet-work-contract:active-packet-lifecycle"}, fixedNow.Add(3*time.Second)); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}
	closed, err := s.Close(imported.Work.ID, CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "active packet workbench proof complete",
		Changed:   true,
		ProofRefs: []string{"packet-work-contract:active-packet-lifecycle"},
	}, fixedNow.Add(4*time.Second))
	if err != nil {
		t.Fatalf("close packet work: %v", err)
	}

	report, err := s.Workbench(fixedNow.Add(5*time.Second), WorkbenchOptions{
		RecentLimit:         10,
		Actor:               "codex",
		ActivePacketSurface: imported.Work.CanonicalSurface,
	})
	if err != nil {
		t.Fatalf("workbench: %v", err)
	}
	if report.Counts.Open != 0 || report.Counts.Terminal != 1 {
		t.Fatalf("workbench counts = %+v", report.Counts)
	}
	if report.ActivePacketProof == nil {
		t.Fatal("workbench missing active packet proof")
	}
	if report.ActivePacketProof.Status != "ok" ||
		report.ActivePacketProof.WorkID != imported.Work.ID ||
		report.ActivePacketProof.ContextPackID != "ctx_packet" ||
		report.ActivePacketProof.ReceiptID != closed.Receipt.ID ||
		report.ActivePacketProof.TransitionID != closed.Transition.ID {
		t.Fatalf("active packet proof = %+v", report.ActivePacketProof)
	}
	next := report.DispatchNext
	if next.CanRun || next.WaitReason != "source_packet_terminal" || next.Reason != "active_packet_terminal" || next.WorkID != imported.Work.ID {
		t.Fatalf("dispatch next = %+v", next)
	}
	if !contains(next.BlockedOn, "work.terminal") || !contains(next.BlockedOn, "work.terminal:"+imported.Work.ID) {
		t.Fatalf("dispatch next blocked_on = %+v", next.BlockedOn)
	}
}

func TestWorkbenchDispatchNextWaitsForOtherActorClaim(t *testing.T) {
	s := newStore(t)
	created, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "claimed by other actor"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(created.ID, "claude", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.Workbench(fixedNow, WorkbenchOptions{RecentLimit: 10, Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	next := report.DispatchNext
	if next.CanRun || next.WaitReason != "claimed_by_other_actor" || !contains(next.BlockedOn, "work.claimed_by_other_actor:"+claimed.ID) {
		t.Fatalf("other actor dispatch suggestion = %+v", next)
	}
	report, err = s.Workbench(fixedNow, WorkbenchOptions{RecentLimit: 10, Actor: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	next = report.DispatchNext
	if !next.CanRun || next.Action != "advance" || next.WorkID != claimed.ID {
		t.Fatalf("claiming actor dispatch suggestion = %+v", next)
	}
}

func TestWorkbenchDispatchNextPrefersProofReadyClose(t *testing.T) {
	s := newStore(t)
	ready, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "ready waits behind proof close"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	running := runningWork(t, s, "proof ready close first")
	verifying, err := s.Advance(running.ID, AdvanceInput{To: "verifying", Actor: "codex", VerificationID: "ver_dispatch_next"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.Workbench(fixedNow, WorkbenchOptions{RecentLimit: 10, Actor: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	next := report.DispatchNext
	if !next.CanRun || next.Action != "close" || next.Reason != "proof_ready_close" || next.WorkID != verifying.ID || next.WorkID == ready.ID {
		t.Fatalf("proof-ready dispatch suggestion = %+v", next)
	}
}

func TestWorkbenchDoesNotCountCancelledWorkAsMissingProof(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "cancelled work is terminal without completion proof"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Close(w.ID, CloseInput{Outcome: "cancelled", MoveType: "audit_only", Summary: "cancel test artifact"}, fixedNow); err != nil {
		t.Fatal(err)
	}
	report, err := s.Workbench(fixedNow, WorkbenchOptions{RecentLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts.Terminal != 1 || report.Counts.MissingReceipt != 0 || report.Counts.MissingTransition != 0 {
		t.Fatalf("cancelled work should not count as missing completion proof: %+v", report.Counts)
	}
	if len(report.Recent) != 1 || report.Recent[0].WorkID != w.ID || report.Recent[0].State != "cancelled" {
		t.Fatalf("recent cancelled work missing: %+v", report.Recent)
	}
}

func findWorkbenchItem(t *testing.T, items []WorkbenchItem, workID string) WorkbenchItem {
	t.Helper()
	for _, item := range items {
		if item.WorkID == workID {
			return item
		}
	}
	t.Fatalf("missing workbench item %s in %+v", workID, items)
	return WorkbenchItem{}
}

func assertWorkbenchItem(t *testing.T, items []WorkbenchItem, workID, reason, next string) {
	t.Helper()
	for _, item := range items {
		if item.WorkID == workID {
			if item.OpenReason != reason || item.NextAction != next {
				t.Fatalf("item %s = %+v", workID, item)
			}
			return
		}
	}
	t.Fatalf("missing workbench item %s in %+v", workID, items)
}
