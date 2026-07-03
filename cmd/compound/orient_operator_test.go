package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/home"
	"compound/internal/mem"
	"compound/internal/reviewartifact"
	"compound/internal/work"
)

func TestOrientOperatorReportMatchesRawHomeState(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	now := time.Date(2026, 7, 2, 20, 30, 0, 0, time.UTC)
	seedOrientOperatorHome(t, homePath, now)

	report, err := buildOrientOperatorReport(homePath, 5, now)
	if err != nil {
		t.Fatalf("orient operator report: %v", err)
	}

	rawCandidates := countJSONFiles(t, filepath.Join(homePath, candidates.InboxDir(candidates.Memory))) +
		countJSONFiles(t, filepath.Join(homePath, candidates.InboxDir(candidates.Skill)))
	if report.NeedsMe.CandidateTotal != rawCandidates {
		t.Fatalf("candidate total = %d, want raw %d", report.NeedsMe.CandidateTotal, rawCandidates)
	}
	if report.NeedsMe.CandidateActionable != 1 {
		t.Fatalf("candidate actionable = %d, want only the unstated skill candidate actionable: %+v", report.NeedsMe.CandidateActionable, report.NeedsMe)
	}
	memoryInbox := findOrientCandidateInbox(t, report, string(candidates.Memory))
	if memoryInbox.Count != 1 || memoryInbox.ActionableCount != 0 || memoryInbox.StateCounts["promoted"] != 1 {
		t.Fatalf("memory inbox state counts = %+v", memoryInbox)
	}
	skillInbox := findOrientCandidateInbox(t, report, string(candidates.Skill))
	if skillInbox.Count != 1 || skillInbox.ActionableCount != 1 || skillInbox.StateCounts["unspecified"] != 1 {
		t.Fatalf("skill inbox state counts = %+v", skillInbox)
	}
	if report.NeedsMe.ReviewRequired != 1 || report.NeedsMe.PendingPanelNeedsHuman != 1 {
		t.Fatalf("needs-me review counts = %+v, want one review-required needs-human panel verdict", report.NeedsMe)
	}
	if report.NeedsMe.OldestItem == nil || report.NeedsMe.OldestItem.AgeSeconds <= 0 {
		t.Fatalf("oldest item missing age: %+v", report.NeedsMe.OldestItem)
	}
	if report.NeedsMe.OldestActionableItem == nil || report.NeedsMe.OldestActionableItem.ID != "skill_orient_fixture" {
		t.Fatalf("oldest actionable candidate = %+v", report.NeedsMe.OldestActionableItem)
	}
	if report.Running.OpenCount != 1 || report.Running.InProgress != 1 || report.Running.NonStaleLeaseCount != 1 {
		t.Fatalf("running counts = %+v, want one open in-progress active lease", report.Running)
	}
	if len(report.Running.OpenWork) != 1 || report.Running.OpenWork[0].Actor != "codex" || report.Running.OpenWork[0].ClaimedAgeSeconds <= 0 {
		t.Fatalf("open work = %+v, want codex claimed running item with age", report.Running.OpenWork)
	}
	if report.Running.LastDispatchStepRun == nil || report.Running.LastDispatchStepRun.AgeSeconds <= 0 {
		t.Fatalf("last dispatch-step run age missing: %+v", report.Running.LastDispatchStepRun)
	}
	if report.Changed.CurrentClaimSeq != 1 || report.Changed.ClaimsSeqDelta != 1 || !report.Changed.FirstRun {
		t.Fatalf("first changed cursor = %+v, want first-run delta of current seq", report.Changed)
	}
	if len(report.Changed.Recent) == 0 || report.Changed.Recent[0].Summary != "operator orientation proof closed" {
		t.Fatalf("recent changes = %+v, want receipt summary", report.Changed.Recent)
	}
	if !stringSliceContains(report.Changed.Recent[0].EvidenceRefs, "ver_orient_done") {
		t.Fatalf("recent evidence refs = %+v, want receipt proof ref", report.Changed.Recent[0].EvidenceRefs)
	}

	second, err := buildOrientOperatorReport(homePath, 5, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("second orient operator report: %v", err)
	}
	if second.Changed.FirstRun || second.Changed.ClaimsSeqDelta != 0 || second.Changed.LastSeenSeq != 1 {
		t.Fatalf("second changed cursor = %+v, want no seq delta after cursor write", second.Changed)
	}
}

func TestOrientOperatorCLIPrintsTriptychAndJSON(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	now := time.Date(2026, 7, 2, 20, 45, 0, 0, time.UTC)
	seedOrientOperatorHome(t, homePath, now)

	text := captureStdout(t, func() {
		cmdOrient([]string{"--operator", "--recent", "3"})
	})
	for _, want := range []string{"NEEDS-ME", "RUNNING", "CHANGED", "pending_panel_needs_human=1", "DRILL-DOWN", "compound workbench"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text orient output missing %q:\n%s", want, text)
		}
	}

	out := captureStdout(t, func() {
		cmdOrient([]string{"--operator", "--json", "--recent", "3"})
	})
	var report orientOperatorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse orient operator json: %v\n%s", err, out)
	}
	if report.SchemaVersion != orientOperatorReportSchemaVersion || report.NeedsMe.PendingPanelNeedsHuman != 1 || report.Running.OpenCount != 1 {
		t.Fatalf("json report = %+v", report)
	}
	if report.NeedsMe.CandidateActionable != 1 {
		t.Fatalf("json actionable candidate count = %+v", report.NeedsMe)
	}
}

func TestOrientOperatorSuppressesResolvedDispatchStepRun(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	now := time.Date(2026, 7, 2, 21, 0, 0, 0, time.UTC)
	if err := home.Init(homePath, "test", now.Add(-time.Hour)); err != nil {
		t.Fatalf("init home: %v", err)
	}
	workStore, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	ready, err := workStore.Create(work.CreateInput{Owner: "codex", SuccessPredicate: "resolved dispatch wait"}, now.Add(-30*time.Minute))
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	run, err := workStore.DispatchStepRun(now.Add(-25*time.Minute), work.DispatchStepRunOptions{
		DispatchStepOptions: work.DispatchStepOptions{Actor: "codex"},
		MaxSteps:            5,
	})
	if err != nil {
		t.Fatalf("dispatch step run: %v", err)
	}
	if run.WaitReason != "run_evidence" {
		t.Fatalf("dispatch run wait reason = %q, want run_evidence", run.WaitReason)
	}
	if _, err := workStore.Advance(ready.ID, work.AdvanceInput{To: "verifying", VerificationID: "ver_orient_resolved_dispatch"}, now.Add(-20*time.Minute)); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}
	if _, err := workStore.Close(ready.ID, work.CloseInput{
		Outcome:  "completed",
		MoveType: "outward_delta",
		Summary:  "resolved dispatch wait",
		Changed:  true,
	}, now.Add(-19*time.Minute)); err != nil {
		t.Fatalf("close work: %v", err)
	}

	report, err := buildOrientOperatorReport(homePath, 5, now)
	if err != nil {
		t.Fatalf("orient operator report: %v", err)
	}
	if report.Running.OpenCount != 0 || report.Running.LastDispatchStepRun != nil {
		t.Fatalf("running should not surface resolved dispatch history: %+v", report.Running)
	}
}

func findOrientCandidateInbox(t *testing.T, report orientOperatorReport, kind string) orientCandidateInbox {
	t.Helper()
	for _, inbox := range report.NeedsMe.CandidateInboxes {
		if inbox.Kind == kind {
			return inbox
		}
	}
	t.Fatalf("candidate inbox %q missing: %+v", kind, report.NeedsMe.CandidateInboxes)
	return orientCandidateInbox{}
}

func seedOrientOperatorHome(t *testing.T, homePath string, now time.Time) {
	t.Helper()
	if err := home.Init(homePath, "test", now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("init home: %v", err)
	}
	memStore, err := mem.Open(homePath)
	if err != nil {
		t.Fatalf("open memory: %v", err)
	}
	candidatesOut, err := memStore.Capture([]mem.Candidate{{
		ID:              "cand_orient_seq",
		Entity:          "compound",
		Predicate:       "has_operator_orient_fixture",
		Object:          "true",
		ObservationDate: now.Add(-90 * time.Minute).Format(time.RFC3339),
		Confidence:      0.91,
		Derivation:      "deterministic",
		ClaimType:       "status",
		PromotionBasis:  candidates.BasisRuntimeProof,
		SourceTraceRef:  "trace://orient-operator-fixture",
		SnippetHash:     "sha256:orientoperator",
		Visibility:      "contextual",
		CreatedAt:       now.Add(-90 * time.Minute).Format(time.RFC3339),
	}}, now.Add(-90*time.Minute))
	if err != nil {
		t.Fatalf("capture memory candidate: %v", err)
	}
	if _, err := memStore.Adjudicate(candidatesOut[0].ID, candidates.Promote, "work_orient_fixture", now.Add(-80*time.Minute)); err != nil {
		t.Fatalf("promote memory candidate: %v", err)
	}
	if err := candidates.WriteCandidate(homePath, candidates.Skill, "skill_orient_fixture", map[string]any{
		"id":         "skill_orient_fixture",
		"kind":       "skill",
		"created_at": now.Add(-70 * time.Minute).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("write skill candidate: %v", err)
	}
	oldCandidatePath := filepath.Join(homePath, candidates.InboxDir(candidates.Skill), "skill_orient_fixture.json")
	if err := os.Chtimes(oldCandidatePath, now.Add(-70*time.Minute), now.Add(-70*time.Minute)); err != nil {
		t.Fatalf("chtimes skill candidate: %v", err)
	}
	if _, err := reviewartifact.Write(homePath, "panel-verdicts", "panel_verdict_orient_needs_human", map[string]any{
		"schema_version": "compound.panel_verdict.v0.1",
		"id":             "panel_verdict_orient_needs_human",
		"created_at":     now.Add(-65 * time.Minute).Format(time.RFC3339),
		"run_id":         "prun_orient_needs_human",
		"target": map[string]any{
			"kind":    "memory_candidate",
			"ref":     "cand_orient_seq",
			"summary": "candidate needs operator decision",
		},
		"lens_findings": []map[string]any{{
			"lens_ref":           "lens:operator",
			"route_evidence_ref": "route_orient_operator",
			"status":             "failed",
		}},
		"synthesis": map[string]any{
			"schema_version": "compound.panel_synthesis.v0.1",
			"decision":       "needs_human",
			"confidence":     0.72,
			"rationale":      "operator must decide",
		},
		"evidence_refs": []string{"route_orient_operator"},
	}, " ", "# Panel verdict\n"); err != nil {
		t.Fatalf("write panel verdict: %v", err)
	}

	workStore, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	running, err := workStore.Create(work.CreateInput{Owner: "codex", SuccessPredicate: "operator orient running proof"}, now.Add(-50*time.Minute))
	if err != nil {
		t.Fatalf("create running work: %v", err)
	}
	if _, err := workStore.Claim(running.ID, "codex", now.Add(-45*time.Minute)); err != nil {
		t.Fatalf("claim running work: %v", err)
	}
	if _, err := workStore.Advance(running.ID, work.AdvanceInput{To: "running", RunID: "run_orient_operator"}, now.Add(-40*time.Minute)); err != nil {
		t.Fatalf("advance running work: %v", err)
	}
	done, err := workStore.Create(work.CreateInput{Owner: "codex", SuccessPredicate: "operator orient closed proof"}, now.Add(-35*time.Minute))
	if err != nil {
		t.Fatalf("create done work: %v", err)
	}
	if _, err := workStore.Claim(done.ID, "codex", now.Add(-34*time.Minute)); err != nil {
		t.Fatalf("claim done work: %v", err)
	}
	if _, err := workStore.Advance(done.ID, work.AdvanceInput{To: "running", RunID: "run_orient_done"}, now.Add(-33*time.Minute)); err != nil {
		t.Fatalf("advance done running: %v", err)
	}
	if _, err := workStore.Advance(done.ID, work.AdvanceInput{To: "verifying", VerificationID: "ver_orient_done"}, now.Add(-32*time.Minute)); err != nil {
		t.Fatalf("advance done verifying: %v", err)
	}
	if _, err := workStore.Close(done.ID, work.CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Actor:     "codex",
		Summary:   "operator orientation proof closed",
		Changed:   true,
		ProofRefs: []string{"ver_orient_done"},
	}, now.Add(-31*time.Minute)); err != nil {
		t.Fatalf("close done work: %v", err)
	}
	if _, err := workStore.DispatchStepRun(now.Add(-30*time.Minute), work.DispatchStepRunOptions{MaxSteps: 1, DispatchStepOptions: work.DispatchStepOptions{Actor: "codex"}}); err != nil {
		t.Fatalf("dispatch step run: %v", err)
	}
}

func countJSONFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			count++
		}
	}
	return count
}
