package work

import "testing"

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
