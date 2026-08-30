package main

import (
	"encoding/json"
	"strings"
	"testing"

	"compound/internal/home"
	"compound/internal/runattempt"
	"compound/internal/work"
)

// Falsifier (CLI write door): `compound work close --opens-branch slug:owner:kind`
// parses the repeatable flag (owner may itself contain colons) and lands the
// branch on the emitted transition.
func TestWorkCloseOpensBranchFlagLandsOnTransition(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := store.Create(work.CreateInput{Owner: "branch-lifecycle", SuccessPredicate: "cli close declares a branch"}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(ready.ID, "codex", cliTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(ready.ID, work.AdvanceInput{To: "running", Actor: "codex"}, cliTestNow); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		cmdWork([]string{"close", ready.ID, "--outcome", "completed", "--move-type", "rule_delta", "--summary", "branch via CLI", "--proof", "check:cli-branch-door", "--by", "codex", "--opens-branch", "branch-lifecycle:claude:orchestrator:opens"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("close output missing JSON:\n%s", out)
	}
	var closeResult work.CloseResult
	if err := json.Unmarshal([]byte(out[start:]), &closeResult); err != nil {
		t.Fatalf("parse close output: %v\n%s", err, out)
	}
	if len(closeResult.Transition.OpensBranches) != 1 {
		t.Fatalf("transition opens_branches = %+v, want exactly one entry", closeResult.Transition.OpensBranches)
	}
	got := closeResult.Transition.OpensBranches[0]
	if got.Branch != "branch-lifecycle" || got.Owner != "claude:orchestrator" || got.Kind != "opens" {
		t.Fatalf("opens_branch entry wrong (colon-bearing owner must survive): %+v", got)
	}
}

// Falsifier (fail-fast parse): malformed --opens-branch values are rejected by
// the parser before any close runs — wrong segment count, empty slug, empty
// owner, empty kind.
func TestParseOpensBranchFlagRejectsMalformed(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{
		"",
		"branch-lifecycle",                      // missing owner+kind
		"branch-lifecycle:claude",               // missing kind
		":claude:orchestrator:opens",            // empty slug
		"branch-lifecycle::opens",               // empty owner
		"branch-lifecycle:claude:orchestrator:", // empty kind
		"branch-lifecycle:claude:orchestrator:frobnicate", // bogus kind
	} {
		if _, err := parseOpensBranchFlag(bad); err == nil {
			t.Fatalf("parseOpensBranchFlag(%q) must fail", bad)
		}
	}
	ref, err := parseOpensBranchFlag("branch-lifecycle:claude:orchestrator:opens")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Branch != "branch-lifecycle" || ref.Owner != "claude:orchestrator" || ref.Kind != "opens" {
		t.Fatalf("colon-bearing owner must parse whole: %+v", ref)
	}
}

func TestParseAcknowledgeAbandonedHappy(t *testing.T) {
	t.Parallel()
	got, err := parseAcknowledgeAbandoned(
		[]string{
			"work_abc123",
			"--acknowledge-abandoned", "ratt_20260823T120000Z_aaaaaaa",
			"--reason", "mis-dispatch killed; real attempt did the work",
			"--changed",
		},
		"",
	)
	if err != nil {
		t.Fatalf("parseAcknowledgeAbandoned: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("requests = %d, want 1", len(got))
	}
	if got[0].RunAttemptID != "ratt_20260823T120000Z_aaaaaaa" {
		t.Fatalf("RunAttemptID = %q", got[0].RunAttemptID)
	}
	if got[0].Owner != runattempt.TerminalizationOwnerWorkAcknowledgeAbandoned {
		t.Fatalf("Owner = %q, want %q", got[0].Owner, runattempt.TerminalizationOwnerWorkAcknowledgeAbandoned)
	}
	if got[0].TargetStatus != runattempt.StatusAbandoned {
		t.Fatalf("TargetStatus = %q, want %q", got[0].TargetStatus, runattempt.StatusAbandoned)
	}
	if got[0].Reason != "mis-dispatch killed; real attempt did the work" {
		t.Fatalf("Reason = %q", got[0].Reason)
	}
}

func TestParseAcknowledgeAbandonedRequiresReason(t *testing.T) {
	t.Parallel()
	_, err := parseAcknowledgeAbandoned(
		[]string{"work_abc123", "--acknowledge-abandoned", "ratt_aaaa", "--reason", ""},
		"",
	)
	if err == nil || !strings.Contains(err.Error(), "non-empty --reason") {
		t.Fatalf("err = %v, want non-empty --reason refusal", err)
	}
}

func TestParseAcknowledgeAbandonedRequiresRattID(t *testing.T) {
	t.Parallel()
	_, err := parseAcknowledgeAbandoned(
		[]string{"work_abc123", "--acknowledge-abandoned"},
		"",
	)
	if err == nil || !strings.Contains(err.Error(), "requires a following ratt_") {
		t.Fatalf("err = %v, want requires-ratt-id refusal", err)
	}
}
