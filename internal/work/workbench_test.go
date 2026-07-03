package work

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	if !contains(report.Recent[0].EvidenceRefs, "proof:workbench") {
		t.Fatalf("terminal item missing receipt proof refs: %+v", report.Recent[0])
	}
}

func TestWorkbenchOfferedPacketsSkipsMalformedHeaders(t *testing.T) {
	s := newStore(t)
	packetDir := filepath.Join(t.TempDir(), "packets")
	if err := os.MkdirAll(packetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	good := `# Projection packet
Date: 2026-07-03
Status: ready
Owner: codex
Proof-medium: cli
Success: projection packet surfaces as offered work.

## Objective
Build one read-only door.
`
	if err := os.WriteFile(filepath.Join(packetDir, "good.md"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := `Date: 2026-07-03
Status: ready

## Objective
No title.
`
	if err := os.WriteFile(filepath.Join(packetDir, "bad.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := s.Workbench(fixedNow, WorkbenchOptions{PacketDir: packetDir})
	if err != nil {
		t.Fatal(err)
	}
	if report.OfferedPackets == nil {
		t.Fatal("offered packet surface missing")
	}
	offered := report.OfferedPackets
	if offered.Counts.Total != 2 || offered.Counts.Listed != 1 || offered.Counts.Skipped != 1 || offered.Counts.ByStatus["ready"] != 1 {
		t.Fatalf("offered counts = %+v", offered.Counts)
	}
	if len(offered.Items) != 1 {
		t.Fatalf("offered items = %+v", offered.Items)
	}
	item := offered.Items[0]
	if item.Title != "Projection packet" || item.Status != "ready" || item.Owner != "codex" || item.CanonicalSurface != "packet:projection-packet" {
		t.Fatalf("offered item = %+v", item)
	}
	for _, gate := range []string{"status:ready", "owner:codex", "proof_medium:cli"} {
		if !contains(item.Gates, gate) {
			t.Fatalf("offered gates missing %q: %+v", gate, item.Gates)
		}
	}
	if !packetWarningContains(offered.Warnings, "bad.md") || !packetWarningContains(offered.Warnings, "packet title heading required") {
		t.Fatalf("warnings = %+v", offered.Warnings)
	}
}

func packetWarningContains(warnings []string, needle string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, needle) {
			return true
		}
	}
	return false
}

func TestWorkbenchSurfacesRouteOutcomeReviews(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "route outcome reaches workbench"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(w.ID, "codex", fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "running"}, fixedNow); err != nil {
		t.Fatal(err)
	}
	closed, err := s.Close(w.ID, CloseInput{
		Outcome:   "completed",
		MoveType:  "outward_delta",
		Summary:   "route outcome fixture work",
		Changed:   true,
		ProofRefs: []string{"proof:route-work"},
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	writeWorkbenchRouteOutcomeReview(t, s.home, map[string]any{
		"schema_version":    "compound.route_outcome_review.v0.1",
		"id":                "ror_workbench",
		"created_at":        fixedNow.Add(2 * time.Minute).UTC().Format(time.RFC3339),
		"status":            "recorded",
		"route_evidence_id": "route_workbench",
		"request_id":        "gateway:req_workbench",
		"work_id":           w.ID,
		"receipt_id":        closed.Receipt.ID,
		"receipt_outcome":   "completed",
		"route_status":      "completed",
		"selected_target": map[string]any{
			"provider": "fixture-provider",
			"model":    "fixture-model",
			"protocol": "openai-chat",
		},
		"assessment": map[string]any{
			"class":                "route_supported",
			"should_review_policy": false,
		},
	})

	report, err := s.Workbench(fixedNow.Add(3*time.Minute), WorkbenchOptions{RecentLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Recent) != 1 {
		t.Fatalf("recent = %+v", report.Recent)
	}
	item := report.Recent[0]
	if item.WorkID != w.ID || len(item.RouteOutcomes) != 1 {
		t.Fatalf("route outcome missing from workbench item: %+v", item)
	}
	out := item.RouteOutcomes[0]
	if out.ID != "ror_workbench" || out.RouteEvidenceID != "route_workbench" || out.ReceiptID != closed.Receipt.ID ||
		out.AssessmentClass != "route_supported" || out.Provider != "fixture-provider" {
		t.Fatalf("bad route outcome summary: %+v", out)
	}
	if !contains(item.EvidenceRefs, "route_outcome_review:ror_workbench") || !contains(item.EvidenceRefs, "route_workbench") {
		t.Fatalf("route outcome proof refs missing: %+v", item.EvidenceRefs)
	}
	if !contains(item.ObservatoryTags, "route:outcome") {
		t.Fatalf("route outcome tag missing: %+v", item.ObservatoryTags)
	}
}

func TestWorkbenchResolvesDispatchStepRunBlockedByTerminalWork(t *testing.T) {
	s := newStore(t)
	ready, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "dispatch run stale wait resolves"}, fixedNow)
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
	if run.WaitReason != "run_evidence" || !contains(run.BlockedOn, "work.run_evidence:"+ready.ID) {
		t.Fatalf("dispatch run did not wait on run evidence: %+v", run)
	}
	if _, err := s.Advance(ready.ID, AdvanceInput{To: "verifying", VerificationID: "ver_stale_dispatch"}, fixedNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Close(ready.ID, CloseInput{
		Outcome:  "completed",
		MoveType: "outward_delta",
		Summary:  "dispatch run wait resolved by terminal work",
		Changed:  true,
	}, fixedNow.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	report, err := s.Workbench(fixedNow.Add(3*time.Minute), WorkbenchOptions{RecentLimit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.DispatchStepRuns) != 1 {
		t.Fatalf("dispatch step runs = %+v", report.DispatchStepRuns)
	}
	got := report.DispatchStepRuns[0]
	if got.Status != "resolved" || got.WaitReason != "terminal_work" || got.CanContinue {
		t.Fatalf("dispatch step run summary not resolved: %+v", got)
	}
	if !contains(got.BlockedOn, "work.terminal:"+ready.ID) || got.NextAction != "referenced work is terminal; no dispatch action required" {
		t.Fatalf("dispatch step run terminal proof not surfaced: %+v", got)
	}
}

func TestWorkbenchRecentOrdersByCompletionTimeNotWorkID(t *testing.T) {
	s := newStore(t)
	olderAt := fixedNow
	newerAt := fixedNow.Add(2 * time.Hour)
	var olderInput, newerInput CreateInput
	found := false
	for i := 0; i < 1000; i++ {
		olderInput = CreateInput{Owner: "compound-core", SuccessPredicate: fmt.Sprintf("older terminal %03d", i)}
		newerInput = CreateInput{Owner: "compound-core", SuccessPredicate: fmt.Sprintf("newer terminal %03d", i)}
		older := BuildWork(olderInput, olderAt)
		newer := BuildWork(newerInput, newerAt)
		if older.ID > newer.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("test setup could not find lexicographic id inversion")
	}
	older, err := s.Create(olderInput, olderAt)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := s.Create(newerInput, newerAt)
	if err != nil {
		t.Fatal(err)
	}
	closeWorkbenchTestWork(t, s, older.ID, olderAt.Add(10*time.Minute), "older completion")
	closeWorkbenchTestWork(t, s, newer.ID, newerAt.Add(10*time.Minute), "newer completion")
	report, err := s.Workbench(newerAt.Add(time.Hour), WorkbenchOptions{RecentLimit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Recent) != 2 {
		t.Fatalf("recent len = %d, want 2: %+v", len(report.Recent), report.Recent)
	}
	if report.Recent[0].WorkID != newer.ID {
		t.Fatalf("recent[0] = %s, want newer %s; older id %s sorts higher lexicographically", report.Recent[0].WorkID, newer.ID, older.ID)
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

func TestWorkbenchResolvesNoOpenWorkDispatchStepRun(t *testing.T) {
	s := newStore(t)
	run, err := s.DispatchStepRun(fixedNow, DispatchStepRunOptions{
		DispatchStepOptions: DispatchStepOptions{Actor: "codex"},
		MaxSteps:            5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.WaitReason != "no_open_work" {
		t.Fatalf("fixture dispatch wait = %+v", run)
	}
	report, err := s.Workbench(fixedNow, WorkbenchOptions{RecentLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts.DispatchStepRuns != 1 || len(report.DispatchStepRuns) != 1 {
		t.Fatalf("dispatch step run summaries missing: counts=%+v runs=%+v", report.Counts, report.DispatchStepRuns)
	}
	summary := report.DispatchStepRuns[0]
	if summary.ID != run.ID || summary.Status != "resolved" || summary.WaitReason != "no_open_work" || summary.CanContinue {
		t.Fatalf("no-open-work dispatch summary = %+v run=%+v", summary, run)
	}
	if !contains(summary.BlockedOn, "work.ready_or_source_packet") {
		t.Fatalf("no-open-work dispatch blocked_on changed: %+v", summary)
	}
	if summary.NextAction != "no open work at run time; no dispatch action remains for this run" {
		t.Fatalf("no-open-work dispatch next action = %q", summary.NextAction)
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
	writeContextPack(t, s, "ctx_packet")
	imported, err := s.ImportPacket(packetPath, PacketImportOptions{
		Owner:         "compound-core",
		State:         "ready",
		ContextPackID: "ctx_packet",
	}, fixedNow)
	if err != nil {
		t.Fatalf("import packet: %v", err)
	}
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

func TestWorkbenchDerivesObservatoryTags(t *testing.T) {
	s := newStore(t)
	ready, err := s.Create(CreateInput{
		Owner:            "compound-core",
		SuccessPredicate: "ready work gets readable facets",
		EvidenceRefs:     []string{"packet_sha256:abc"},
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	done, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "completed memory work gets proof facets"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(done.ID, "codex", fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(done.ID, AdvanceInput{To: "running"}, fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(done.ID, AdvanceInput{To: "verifying", VerificationID: "ver_memory"}, fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Close(done.ID, CloseInput{
		Outcome:        "completed",
		MoveType:       "memory_delta",
		Summary:        "memory recipient proof",
		ProofRefs:      []string{"ver_memory"},
		MemoryPromoted: []string{"claim_memory"},
	}, fixedNow); err != nil {
		t.Fatal(err)
	}

	report, err := s.Workbench(fixedNow, WorkbenchOptions{RecentLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	readyItem := findWorkbenchItem(t, report.Open, ready.ID)
	for _, tag := range []string{"state:ready", "attention:needs-claim", "owner:compound-core", "evidence:source"} {
		if !contains(readyItem.ObservatoryTags, tag) {
			t.Fatalf("ready observatory tags missing %q: %+v", tag, readyItem.ObservatoryTags)
		}
	}
	doneItem := findWorkbenchItem(t, report.Recent, done.ID)
	for _, tag := range []string{"state:completed", "attention:terminal", "move:memory-delta", "recipient:memory", "change:changed", "evidence:verification", "proof:complete"} {
		if !contains(doneItem.ObservatoryTags, tag) {
			t.Fatalf("done observatory tags missing %q: %+v", tag, doneItem.ObservatoryTags)
		}
	}
}

func TestWorkbenchUsesEffectiveChangedForLegacyProofBearingReceipt(t *testing.T) {
	s := newStore(t)
	done, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "legacy receipt projects changed"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(done.ID, "codex", fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(done.ID, AdvanceInput{To: "running"}, fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(done.ID, AdvanceInput{To: "verifying", VerificationID: "ver_legacy"}, fixedNow); err != nil {
		t.Fatal(err)
	}
	closed, err := s.Close(done.ID, CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "legacy receipt proof",
		ProofRefs: []string{"proof:legacy"},
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	legacy := closed.Receipt
	legacy.StateDelta.Changed = false
	if err := s.writeJSON(s.envPath(legacy.ID), &legacy); err != nil {
		t.Fatal(err)
	}

	report, err := s.Workbench(fixedNow, WorkbenchOptions{RecentLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	item := findWorkbenchItem(t, report.Recent, done.ID)
	if !item.Changed || !contains(item.ObservatoryTags, "change:changed") {
		t.Fatalf("legacy proof-bearing receipt projected unchanged: %+v", item)
	}
}

func closeWorkbenchTestWork(t *testing.T, s *Store, workID string, at time.Time, summary string) {
	t.Helper()
	if _, err := s.Claim(workID, "codex", at); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(workID, AdvanceInput{To: "running"}, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(workID, AdvanceInput{To: "verifying", VerificationID: "ver_" + workID}, at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Close(workID, CloseInput{
		Outcome:   "completed",
		MoveType:  "outward_delta",
		Summary:   summary,
		Changed:   true,
		ProofRefs: []string{"proof:" + workID},
	}, at.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func writeWorkbenchRouteOutcomeReview(t *testing.T, homePath string, review map[string]any) {
	t.Helper()
	dir := filepath.Join(homePath, "reviews", "route-remorse")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	id, _ := review["id"].(string)
	if id == "" {
		t.Fatal("route outcome review id required")
	}
	data, err := json.MarshalIndent(review, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
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
