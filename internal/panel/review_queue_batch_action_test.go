package panel

import (
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/reviewartifact"
)

func writeOpenVerdict(t *testing.T, homePath, runID, id, decision, object string) {
	t.Helper()
	verdict := verdictArtifactPayload{
		SchemaVersion: "compound.panel_verdict.v0.1",
		ID:            id,
		RunID:         runID,
		Target: TargetInput{
			Kind:    "memory_candidate",
			Ref:     "claim_" + id,
			Payload: []byte(`{"object":"` + object + `"}`),
		},
		Synthesis: Synthesis{Decision: decision},
	}
	if _, err := reviewartifact.Write(homePath, verdictReviewArea, id, verdict, " ", "# verdict\n"); err != nil {
		t.Fatalf("write verdict %s: %v", id, err)
	}
}

// TestPanelVerdictGroupBatchActionWiresApplyForApplyableRun covers the
// coordinator's scope addition: a needs_you panel-verdicts group whose
// underlying run has already reached verdicts_recorded must carry the real
// `panel run --plan <run_id> --apply` batch act, not the generic
// item-specific "no safe batch action" default.
func TestPanelVerdictGroupBatchActionWiresApplyForApplyableRun(t *testing.T) {
	homePath := t.TempDir()
	now := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "test", now); err != nil {
		t.Fatalf("init home: %v", err)
	}
	runID := "prun_test_applyable"
	run := Run{
		SchemaVersion: RunSchemaVersion,
		ID:            runID,
		CreatedAt:     now.Format(time.RFC3339),
		Status:        "verdicts_recorded",
		TargetKind:    "memory_candidate",
	}
	store, err := Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRun(run); err != nil {
		t.Fatalf("save run: %v", err)
	}
	writeOpenVerdict(t, homePath, runID, "panel_verdict_a", "reject", "claim about topic A")
	writeOpenVerdict(t, homePath, runID, "panel_verdict_b", "reject", "claim about topic B")

	report, err := reviewartifact.Queue(homePath, reviewartifact.QueueOptions{Area: verdictReviewArea}, now)
	if err != nil {
		t.Fatal(err)
	}
	AnnotateVerdictQueue(&report)
	reviewartifact.Recount(&report)

	if len(report.Groups) != 1 {
		t.Fatalf("want 1 group, got %d: %+v", len(report.Groups), report.Groups)
	}
	group := report.Groups[0]
	if group.Band != reviewartifact.BandNeedsYou {
		t.Fatalf("group band = %q, want needs_you", group.Band)
	}
	if group.SafeBatchAction == nil {
		t.Fatalf("group SafeBatchAction is nil, want the panel run --apply batch act: %+v", group)
	}
	wantArgv := []string{"compound", "panel", "run", "--plan", runID, "--apply"}
	got := group.SafeBatchAction.Argv
	if len(got) != len(wantArgv) {
		t.Fatalf("argv = %v, want %v", got, wantArgv)
	}
	for i := range wantArgv {
		if got[i] != wantArgv[i] {
			t.Fatalf("argv = %v, want %v", got, wantArgv)
		}
	}
	if group.SafeBatchAction.PolicyNote != "one operator act; each target gets its own PolicyDecision" {
		t.Fatalf("PolicyNote = %q", group.SafeBatchAction.PolicyNote)
	}
	if group.TerminalReason != "" {
		t.Fatalf("TerminalReason = %q, want empty when a batch act is available", group.TerminalReason)
	}
}

// TestPanelVerdictGroupBatchActionUnavailableForNonApplyableRun covers the
// negative case: a run that has not reached verdicts_recorded/applied must
// not offer the apply batch act, and must carry an honest reason.
func TestPanelVerdictGroupBatchActionUnavailableForNonApplyableRun(t *testing.T) {
	homePath := t.TempDir()
	now := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "test", now); err != nil {
		t.Fatalf("init home: %v", err)
	}
	runID := "prun_test_running"
	run := Run{
		SchemaVersion: RunSchemaVersion,
		ID:            runID,
		CreatedAt:     now.Format(time.RFC3339),
		Status:        "running",
		TargetKind:    "memory_candidate",
	}
	store, err := Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRun(run); err != nil {
		t.Fatalf("save run: %v", err)
	}
	writeOpenVerdict(t, homePath, runID, "panel_verdict_c", "reject", "claim about topic C")
	writeOpenVerdict(t, homePath, runID, "panel_verdict_d", "reject", "claim about topic D")

	report, err := reviewartifact.Queue(homePath, reviewartifact.QueueOptions{Area: verdictReviewArea}, now)
	if err != nil {
		t.Fatal(err)
	}
	AnnotateVerdictQueue(&report)
	reviewartifact.Recount(&report)

	if len(report.Groups) != 1 {
		t.Fatalf("want 1 group, got %d: %+v", len(report.Groups), report.Groups)
	}
	group := report.Groups[0]
	if group.SafeBatchAction != nil {
		t.Fatalf("SafeBatchAction = %+v, want nil for a run not yet ready to apply", group.SafeBatchAction)
	}
	if group.TerminalReason == "" {
		t.Fatalf("TerminalReason empty, want an honest reason the batch act is unavailable")
	}
}
