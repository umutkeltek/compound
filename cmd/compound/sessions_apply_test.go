package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/authority"
	"compound/internal/candidates"
	"compound/internal/capgraph"
	"compound/internal/home"
	"compound/internal/mem"
	"compound/internal/work"
)

func TestSessionsMineApplyRoutesSyntheticFindingsProposeOnly(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	writeSessionMiningFixture(t, homePath)

	out := captureStdout(t, func() {
		cmdSessions([]string{"mine", "--since", "48h", "--apply", "--json"})
	})
	var report sessionMiningReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse sessions mine apply report: %v\n%s", err, out)
	}
	if report.Apply == nil || report.Apply.Status != "review_required" || report.DryRun {
		t.Fatalf("apply report = %+v", report.Apply)
	}
	if len(report.PreferencesFound) == 0 || len(report.RulesToRetire) == 0 || len(report.CandidateSkillUpdates) == 0 {
		t.Fatalf("expected preference, retire, and skill update findings: prefs=%d retire=%d skill=%d",
			len(report.PreferencesFound), len(report.RulesToRetire), len(report.CandidateSkillUpdates))
	}
	assertSessionFindingsHaveEvidenceRefs(t, report.PreferencesFound)
	assertSessionFindingsHaveEvidenceRefs(t, report.RulesToRetire)
	assertSessionFindingsHaveEvidenceRefs(t, report.CandidateSkillUpdates)
	if len(report.Apply.PreferenceCandidateIDs) == 0 || len(report.Apply.RetireProposalIDs) == 0 || len(report.Apply.SkillUpdateProposalIDs) == 0 {
		t.Fatalf("apply routes missing: %+v", report.Apply)
	}

	memStore, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	memoryCandidates, err := memStore.AllCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(memoryCandidates) == 0 {
		t.Fatal("expected preference memory candidate")
	}
	if memoryCandidates[0].ClaimType != "preference" || memoryCandidates[0].Visibility != "contextual" || memoryCandidates[0].PromotionBasis != "" {
		t.Fatalf("preference candidate must be contextual and non-promotable by default: %+v", memoryCandidates[0])
	}

	retirePath := filepath.Join(homePath, "reviews", "distiller-improvements", report.Apply.RetireProposalIDs[0]+".json")
	var retirePlan struct {
		Status       string   `json:"status"`
		CandidateID  string   `json:"candidate_id"`
		EvidenceRefs []string `json:"evidence_refs"`
		Warnings     []string `json:"warnings"`
	}
	readJSONFile(t, retirePath, &retirePlan)
	if retirePlan.Status != "review_required" || retirePlan.CandidateID == "" || len(retirePlan.EvidenceRefs) == 0 {
		t.Fatalf("retire plan must be propose-only with evidence refs: %+v", retirePlan)
	}
	if !stringSliceContains(retirePlan.Warnings, "does not mutate distiller policy") {
		t.Fatalf("retire plan missing no-mutation warning: %+v", retirePlan.Warnings)
	}

	skillPath := filepath.Join(homePath, "reviews", capgraph.CapabilityLifecycleProposalReviewArea, report.Apply.SkillUpdateProposalIDs[0]+".json")
	var skillProposal struct {
		Status       string   `json:"status"`
		ProposalKind string   `json:"proposal_kind"`
		EvidenceRefs []string `json:"evidence_refs"`
		Warnings     []string `json:"warnings"`
	}
	readJSONFile(t, skillPath, &skillProposal)
	if skillProposal.Status != "review_required" || skillProposal.ProposalKind != "revise" || len(skillProposal.EvidenceRefs) == 0 {
		t.Fatalf("skill proposal must be a revise review artifact with evidence refs: %+v", skillProposal)
	}
	if !stringSliceContains(skillProposal.Warnings, "does not mutate CapabilityGraph") {
		t.Fatalf("skill proposal missing no-mutation warning: %+v", skillProposal.Warnings)
	}
}

func TestSessionsMineApplyDoesNotMutateWithoutOperatorReview(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	writeSessionMiningFixture(t, homePath)

	_ = captureStdout(t, func() {
		cmdSessions([]string{"mine", "--since", "48h", "--apply", "--json"})
	})

	for _, path := range []string{
		filepath.Join(homePath, "ledger", "claims.jsonl"),
		filepath.Join(homePath, "ledger", "adjudications.jsonl"),
		filepath.Join(homePath, "registries", "capability-graph.json"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("sessions mine --apply must not mutate %s without review, stat err=%v", path, err)
		}
	}
	assertNoReviewDecisionArtifacts(t, homePath)
	assertCandidateInboxStates(t, filepath.Join(homePath, candidates.InboxDir(candidates.Memory)), "proposed")
	assertCandidateInboxStates(t, filepath.Join(homePath, candidates.InboxDir(candidates.PolicyPatch)), candidates.NoPromote)
}

func TestSessionsMineDailyDryRunDigestJobRosterFixture(t *testing.T) {
	homePath := t.TempDir()
	source := "../../fixtures/authority/job-roster-sessions-mine-digest.json"
	now := time.Date(2026, 7, 6, 8, 0, 0, 0, time.UTC)
	report, err := authority.ImportJobRoster(homePath, source, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.JobCount != 1 || report.EnabledCount != 0 {
		t.Fatalf("job roster report = %+v", report)
	}
	roster, err := authority.LoadJobRosterAuthority(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(roster.Jobs) != 1 {
		t.Fatalf("jobs = %+v", roster.Jobs)
	}
	job := roster.Jobs[0]
	if job.JobID != "sessions-mine-daily-dry-run" || job.State != "pending_operator_scheduling" || job.Enabled {
		t.Fatalf("sessions mine digest job should be registered but unloaded: %+v", job)
	}
	if !strings.Contains(job.Script, "sessions mine --since 24h --dry-run --json") {
		t.Fatalf("unexpected job script: %+v", job)
	}
}

func writeSessionMiningFixture(t *testing.T, homePath string) {
	t.Helper()
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(work.CreateInput{
		WorkID:           "work_review_rule",
		Owner:            "codex",
		State:            "held",
		SuccessPredicate: "review stale decision-card-only rule",
		DesiredOutcome:   "operator review decides whether the rule changes future execution",
		EvidenceRefs:     []string{"session:sess_rule", "hook:hook_rule_review"},
	}, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	events := []sessionHookEvent{
		sessionMiningTestEvent("hook_pref_correction_1", "sess_pref_1", "work_pref_1", "UserPromptSubmit", "operator_correction:prefer reversible edits before wider rewrites"),
		sessionMiningTestEvent("hook_pref_correction_2", "sess_pref_2", "work_pref_2", "SessionEnd", "operator_correction:prefer reversible edits before wider rewrites"),
		sessionMiningTestEvent("hook_override_1", "sess_override_1", "work_override_1", "PreToolUse", "manual_override:skip auto-install suggestion"),
		sessionMiningTestEvent("hook_override_2", "sess_override_2", "work_override_2", "PreToolUse", "manual_override:skip auto-install suggestion"),
		sessionMiningTestEvent("hook_tool_1", "sess_tool_1", "work_tool_1", "PostToolUse", "tool:rg"),
		sessionMiningTestEvent("hook_tool_2", "sess_tool_2", "work_tool_2", "PostToolUse", "tool:rg"),
		sessionMiningTestEvent("hook_lane_1", "sess_lane_1", "work_lane_1", "SessionEnd", "lane:local-dry-run"),
		sessionMiningTestEvent("hook_lane_2", "sess_lane_2", "work_lane_2", "SessionEnd", "lane:local-dry-run"),
	}
	writeSessionHookEvents(t, homePath, events)
}

func sessionMiningTestEvent(id, sessionID, workID, hookName, sideEffect string) sessionHookEvent {
	var ev sessionHookEvent
	ev.ID = id
	ev.CreatedAt = time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	ev.Harness = "codex"
	ev.HookName = hookName
	ev.HookCapability = "session_mining_fixture"
	ev.Status = "ok"
	ev.SideEffects = []string{sideEffect}
	ev.EvidenceRefs = []string{"hook:" + id, "session:" + sessionID}
	ev.IDs.SessionID = sessionID
	ev.IDs.WorkID = workID
	return ev
}

func writeSessionHookEvents(t *testing.T, homePath string, events []sessionHookEvent) {
	t.Helper()
	path := filepath.Join(homePath, "events", "hook-events.ndjson")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			t.Fatal(err)
		}
	}
}

func assertSessionFindingsHaveEvidenceRefs(t *testing.T, findings []sessionMiningFinding) {
	t.Helper()
	for _, finding := range findings {
		if len(finding.EvidenceRefs) == 0 {
			t.Fatalf("finding missing evidence refs: %+v", finding)
		}
	}
}

func assertNoReviewDecisionArtifacts(t *testing.T, homePath string) {
	t.Helper()
	root := filepath.Join(homePath, "reviews")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := filepath.Base(path)
		if strings.HasSuffix(name, ".reviewed.json") || strings.HasSuffix(name, ".decision.json") || strings.HasSuffix(name, ".apply.json") {
			t.Fatalf("unexpected reviewed/mutating artifact without operator review: %s", path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func assertCandidateInboxStates(t *testing.T, dir, wantState string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected candidates in %s", dir)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var payload struct {
			State   string `json:"state"`
			Verdict string `json:"verdict"`
		}
		readJSONFile(t, filepath.Join(dir, entry.Name()), &payload)
		got := firstNonEmpty(payload.State, payload.Verdict)
		if got != wantState {
			t.Fatalf("candidate %s state/verdict = %q, want %q", entry.Name(), got, wantState)
		}
	}
}

func readJSONFile(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}
