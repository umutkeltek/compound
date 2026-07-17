package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/authority"
	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/policy"
	"compound/internal/reviewartifact"
	"compound/internal/work"
)

func TestBuildReviewQueueReportStampsCurrentOwnerAuthority(t *testing.T) {
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	store := openWorkbenchAuthorityWorkStore(t, homePath)
	workID := "work_review_authority_live"
	if _, err := store.Create(work.CreateInput{
		WorkID:           workID,
		Owner:            "source-control-cleanup",
		SuccessPredicate: "recorded review report is linked to its owner work",
	}, cliTestNow); err != nil {
		t.Fatal(err)
	}
	recordWorkbenchAuthorityReview(t, homePath, workID, cliTestNow.Add(time.Minute))
	before := workbenchAuthorityWorkCount(t, homePath)

	first, err := buildReviewQueueReport(homePath, authority.SourceControlCleanupReviewArea, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildReviewQueueReport(homePath, authority.SourceControlCleanupReviewArea, false)
	if err != nil {
		t.Fatal(err)
	}
	action := workbenchAuthorityAction(t, first)
	again := workbenchAuthorityAction(t, second)
	if action.ActionIntent == nil || action.PolicyDecision == nil || action.PolicyAuthority == nil {
		t.Fatalf("live owner action did not receive a complete current authority bundle: %+v", action)
	}
	if err := policy.ValidateAuthorityBundle(*action.ActionIntent, *action.PolicyDecision, *action.PolicyAuthority); err != nil {
		t.Fatalf("stamped authority bundle invalid: %v", err)
	}
	if action.ActionIntent.IDs.WorkID != workID || !action.PolicyAuthority.Current || !action.PolicyAuthority.Allowed || action.PolicyAuthority.PolicySource != "built-in" {
		t.Fatalf("stamped authority did not come from current owner/policy: %+v", action)
	}
	if action.ActionIntent.ID != again.ActionIntent.ID || action.PolicyAuthority.Revision != again.PolicyAuthority.Revision {
		t.Fatalf("equivalent fresh renders changed stable owner/policy identity: first=%+v second=%+v", action, again)
	}
	if after := workbenchAuthorityWorkCount(t, homePath); after != before {
		t.Fatalf("review rendering created WorkEnvelopes: before=%d after=%d", before, after)
	}
}

func TestBuildReviewQueueReportLeavesMissingAndTerminalOwnersUnstamped(t *testing.T) {
	tests := []struct {
		name     string
		workID   string
		terminal bool
	}{
		{name: "missing", workID: "work_review_authority_missing"},
		{name: "terminal", workID: "work_review_authority_terminal", terminal: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			homePath := t.TempDir()
			if err := home.Init(homePath, "test", cliTestNow); err != nil {
				t.Fatal(err)
			}
			if tc.terminal {
				store := openWorkbenchAuthorityWorkStore(t, homePath)
				if _, err := store.Create(work.CreateInput{WorkID: tc.workID, Owner: "source-control-cleanup", SuccessPredicate: "terminal owner stays non-runnable"}, cliTestNow); err != nil {
					t.Fatal(err)
				}
				if _, err := store.Claim(tc.workID, "test-executor", cliTestNow.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				if _, err := store.Advance(tc.workID, work.AdvanceInput{Actor: "test-executor", To: "running"}, cliTestNow.Add(2*time.Second)); err != nil {
					t.Fatal(err)
				}
				if _, err := store.Close(tc.workID, work.CloseInput{
					Outcome:   "completed",
					MoveType:  "control_plane_repair",
					Actor:     "test-executor",
					Summary:   "terminal authority owner fixture",
					Changed:   true,
					ProofRefs: []string{"proof:terminal-authority-owner"},
				}, cliTestNow.Add(3*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			recordWorkbenchAuthorityReview(t, homePath, tc.workID, cliTestNow.Add(time.Minute))
			report, err := buildReviewQueueReport(homePath, authority.SourceControlCleanupReviewArea, false)
			if err != nil {
				t.Fatal(err)
			}
			action := workbenchAuthorityAction(t, report)
			if action.ActionIntent == nil || action.AuthorityContext == nil {
				t.Fatalf("fixture owner seed missing: %+v", action)
			}
			if action.PolicyDecision != nil || action.PolicyAuthority != nil {
				t.Fatalf("%s WorkEnvelope received runnable authority: %+v", tc.name, action)
			}
		})
	}
}

func TestStampReviewActionAuthorityFailsClosedForIncompleteSeeds(t *testing.T) {
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	workID := "work_review_authority_seed"
	store := openWorkbenchAuthorityWorkStore(t, homePath)
	if _, err := store.Create(work.CreateInput{WorkID: workID, Owner: "source-control-cleanup", SuccessPredicate: "only complete owner seeds are stamped"}, cliTestNow); err != nil {
		t.Fatal(err)
	}
	recordWorkbenchAuthorityReview(t, homePath, workID, cliTestNow.Add(time.Minute))
	base, err := reviewartifact.Queue(homePath, reviewartifact.QueueOptions{Area: authority.SourceControlCleanupReviewArea}, cliTestNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	authority.AnnotateSourceControlCleanupQueue(&base)
	seed := workbenchAuthorityAction(t, base)

	tests := []struct {
		name   string
		mutate func(*reviewartifact.Action)
	}{
		{name: "missing action intent", mutate: func(action *reviewartifact.Action) { action.ActionIntent = nil }},
		{name: "missing authority context", mutate: func(action *reviewartifact.Action) { action.AuthorityContext = nil }},
		{name: "malformed action intent", mutate: func(action *reviewartifact.Action) { action.ActionIntent.Capability = "" }},
		{name: "malformed authority context", mutate: func(action *reviewartifact.Action) { action.AuthorityContext.TargetScope = "" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action := cloneWorkbenchAuthoritySeed(seed)
			action.PolicyDecision = &envelope.PolicyDecision{Header: envelope.Header{ID: "pol_stale"}}
			action.PolicyAuthority = &policy.AuthorityFace{SchemaVersion: policy.AuthorityFaceSchema, Current: true, Revision: "stale"}
			tc.mutate(&action)
			report := reviewartifact.QueueReport{Items: []reviewartifact.QueueItem{{Actions: []reviewartifact.Action{action}}}}
			if err := stampReviewActionAuthority(homePath, &report, cliTestNow.Add(3*time.Minute)); err != nil {
				t.Fatal(err)
			}
			got := report.Items[0].Actions[0]
			if got.PolicyDecision != nil || got.PolicyAuthority != nil {
				t.Fatalf("incomplete seed retained stale or runnable authority: %+v", got)
			}
		})
	}
}

func TestBuildReviewQueueReportCarriesCurrentPolicyHold(t *testing.T) {
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	workID := "work_review_authority_held"
	store := openWorkbenchAuthorityWorkStore(t, homePath)
	if _, err := store.Create(work.CreateInput{WorkID: workID, Owner: "source-control-cleanup", SuccessPredicate: "held current policy stays visible"}, cliTestNow); err != nil {
		t.Fatal(err)
	}
	recordWorkbenchAuthorityReview(t, homePath, workID, cliTestNow.Add(time.Minute))
	if err := os.WriteFile(filepath.Join(homePath, "config", "policy.yaml"), []byte("class_outcome:\n  local_reversible:\n    outcome: hold\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := buildReviewQueueReport(homePath, authority.SourceControlCleanupReviewArea, false)
	if err != nil {
		t.Fatal(err)
	}
	action := workbenchAuthorityAction(t, report)
	if action.PolicyDecision == nil || action.PolicyAuthority == nil {
		t.Fatalf("valid held evaluation was erased: %+v", action)
	}
	if action.PolicyDecision.Outcome != "hold" || !action.PolicyAuthority.Current || action.PolicyAuthority.Allowed {
		t.Fatalf("current policy hold was not carried exactly: %+v", action)
	}
	if err := policy.ValidateAuthorityBundle(*action.ActionIntent, *action.PolicyDecision, *action.PolicyAuthority); err != nil {
		t.Fatalf("held authority bundle invalid: %v", err)
	}
}

func TestBuildReviewQueueReportFailsWhenPolicyCannotLoad(t *testing.T) {
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homePath, "config", "policy.yaml"), []byte("class_outcome: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := buildReviewQueueReport(homePath, "", false); err == nil || !strings.Contains(err.Error(), "policy") {
		t.Fatalf("invalid current policy did not fail the review report: %v", err)
	}
}

func openWorkbenchAuthorityWorkStore(t *testing.T, homePath string) *work.Store {
	t.Helper()
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func recordWorkbenchAuthorityReview(t *testing.T, homePath, workID string, now time.Time) authority.SourceControlReviewPlanPaths {
	t.Helper()
	snapshot := authority.SourceControlSnapshot{
		SchemaVersion:           authority.SourceControlSchemaVersion,
		RepoID:                  "repo:workbench-authority",
		GitRoot:                 filepath.Join(homePath, "source-repo"),
		Head:                    strings.Repeat("a", 40),
		Status:                  "dirty",
		Unstaged:                1,
		ChangedPaths:            1,
		ChangedStateFingerprint: "sha256:" + strings.Repeat("b", 64),
		EvidenceRef:             "source-control:repo:workbench-authority:fixture",
	}
	plan, err := authority.BuildSourceControlReviewPlan(snapshot, workID, now)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := authority.RecordSourceControlReviewPlan(homePath, plan)
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func workbenchAuthorityAction(t *testing.T, report reviewartifact.QueueReport) reviewartifact.Action {
	t.Helper()
	for _, item := range report.Items {
		if item.Area == authority.SourceControlCleanupReviewArea && len(item.Actions) == 1 {
			return item.Actions[0]
		}
	}
	t.Fatalf("source-control authority action missing from report: %+v", report.Items)
	return reviewartifact.Action{}
}

func workbenchAuthorityWorkCount(t *testing.T, homePath string) int {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(homePath, "work", "work", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return len(paths)
}

func cloneWorkbenchAuthoritySeed(in reviewartifact.Action) reviewartifact.Action {
	out := in
	if in.ActionIntent != nil {
		intent := *in.ActionIntent
		intent.EvidenceRefs = append([]string{}, in.ActionIntent.EvidenceRefs...)
		out.ActionIntent = &intent
	}
	if in.AuthorityContext != nil {
		context := *in.AuthorityContext
		context.ExpectedProof = append([]string{}, in.AuthorityContext.ExpectedProof...)
		context.EvidenceRefs = append([]string{}, in.AuthorityContext.EvidenceRefs...)
		out.AuthorityContext = &context
	}
	return out
}
