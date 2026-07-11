package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"compound/internal/attention"
	"compound/internal/home"
	"compound/internal/reviewartifact"
	"compound/internal/work"
)

// Blast radius for --operator: cmdWorkbench is the only CLI entrypoint, while
// Store.Workbench feeds console/orient/attention/session JSON consumers. Keep
// this slice CLI-renderer-only so the existing JSON schema remains unchanged.
func TestWorkbenchOperatorModeTranslatesEnvelopeIntoOperatorLanguage(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	ready, err := store.Create(work.CreateInput{
		Owner:            "compound-core",
		CanonicalSurface: "surface:operator-ready",
		SuccessPredicate: "operator can claim ready work",
		DesiredOutcome:   "ready mission can start",
	}, cliTestNow)
	if err != nil {
		t.Fatalf("create ready: %v", err)
	}
	proving, err := store.Create(work.CreateInput{
		Owner:            "compound-core",
		CanonicalSurface: "surface:operator-proving",
		SuccessPredicate: "operator sees proof in progress",
		DesiredOutcome:   "evidence gets checked before landing",
	}, cliTestNow)
	if err != nil {
		t.Fatalf("create proving: %v", err)
	}
	if _, err := store.Claim(proving.ID, "codex", cliTestNow); err != nil {
		t.Fatalf("claim proving: %v", err)
	}
	if _, err := store.Advance(proving.ID, work.AdvanceInput{To: "running"}, cliTestNow); err != nil {
		t.Fatalf("advance proving running: %v", err)
	}
	if _, err := store.Advance(proving.ID, work.AdvanceInput{To: "verifying", ArtifactID: "artifact_operator_mode", VerificationID: "verification_operator_mode"}, cliTestNow); err != nil {
		t.Fatalf("advance proving verifying: %v", err)
	}
	held, err := store.Create(work.CreateInput{
		Owner:            "compound-core",
		CanonicalSurface: "surface:operator-held",
		State:            "held",
		SuccessPredicate: "operator chooses whether to land held work",
		DesiredOutcome:   "human decision unlocks landing",
	}, cliTestNow)
	if err != nil {
		t.Fatalf("create held: %v", err)
	}
	done, err := store.Create(work.CreateInput{
		Owner:            "compound-core",
		CanonicalSurface: "surface:operator-done",
		SuccessPredicate: "operator sees integrated proof",
		DesiredOutcome:   "terminal proof is visible as learning",
	}, cliTestNow)
	if err != nil {
		t.Fatalf("create done: %v", err)
	}
	if _, err := store.Claim(done.ID, "codex", cliTestNow); err != nil {
		t.Fatalf("claim done: %v", err)
	}
	if _, err := store.Advance(done.ID, work.AdvanceInput{To: "running"}, cliTestNow); err != nil {
		t.Fatalf("advance done running: %v", err)
	}
	if _, err := store.Advance(done.ID, work.AdvanceInput{To: "verifying", ArtifactID: "artifact_done_operator_mode", VerificationID: "verification_done_operator_mode"}, cliTestNow); err != nil {
		t.Fatalf("advance done verifying: %v", err)
	}
	closed, err := store.Close(done.ID, work.CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "operator mode synthetic proof",
		Changed:   true,
		ProofRefs: []string{"proof:operator-mode-test"},
	}, cliTestNow)
	if err != nil {
		t.Fatalf("close done: %v", err)
	}

	before := hashHomeDir(t, homePath)
	operator := captureStdout(t, func() {
		cmdWorkbench([]string{"--operator", "--recent", "10"})
	})
	operatorAdvanced := captureStdout(t, func() {
		cmdWorkbench([]string{"--operator", "--operator-advanced", "--recent", "10"})
	})
	operatorAdvancedAlias := captureStdout(t, func() {
		cmdWorkbench([]string{"--operator", "--advanced", "--recent", "10"})
	})
	operatorJSON := captureStdout(t, func() {
		cmdWorkbench([]string{"--json", "--operator", "--recent", "10"})
	})
	after := hashHomeDir(t, homePath)
	if before != after {
		t.Fatalf("compound workbench --operator mutated COMPOUND_HOME: before=%s after=%s", before, after)
	}
	if strings.Contains(operator, "Advanced") {
		t.Fatalf("operator workbench default output must hide Advanced without opt-in:\n%s", operator)
	}
	if !strings.Contains(operatorAdvanced, "Advanced") || !strings.Contains(operatorAdvanced, closed.Receipt.ID) || !strings.Contains(operatorAdvanced, closed.Transition.ID) {
		t.Fatalf("operator workbench advanced output missing raw details:\n%s", operatorAdvanced)
	}
	if !strings.Contains(operatorAdvancedAlias, "Advanced") || !strings.Contains(operatorAdvancedAlias, closed.Receipt.ID) || !strings.Contains(operatorAdvancedAlias, closed.Transition.ID) {
		t.Fatalf("operator workbench --advanced alias output missing raw details:\n%s", operatorAdvancedAlias)
	}
	for _, want := range []string{
		"Operator workbench",
		"Flow: Needs you → Working → Ready",
		"Summary: We noticed",
		"chose",
		"assigned codex",
		"proved it with proof:operator-mode-test",
		"and now need",
		"Mission",
		"Needs you",
		"Working",
		"Ready",
		"Recent proof",
		"Ready",
		"Proving",
		"Needs you",
		"Integrated",
		"Authority: Free to act",
		"Authority: Review before landing",
		"Authority: Needs Umut",
		ready.ID,
		proving.ID,
		held.ID,
		done.ID,
		closed.Receipt.ID,
		closed.Transition.ID,
	} {
		if !strings.Contains(operator, want) {
			t.Fatalf("operator workbench output missing %q:\n%s", want, operator)
		}
	}
	if strings.Contains(operatorJSON, "Operator workbench") || strings.Contains(operatorJSON, "Flow stages:") {
		t.Fatalf("--json --operator must preserve JSON output without operator prose:\n%s", operatorJSON)
	}
	var parsed workbenchCLIOutput
	if err := json.Unmarshal([]byte(operatorJSON), &parsed); err != nil {
		t.Fatalf("parse --json --operator output: %v\n%s", err, operatorJSON)
	}
	if parsed.Counts.Total != 4 || parsed.ReviewQueue == nil {
		t.Fatalf("--json --operator did not preserve workbench JSON contract: counts=%+v review=%+v", parsed.Counts, parsed.ReviewQueue)
	}
}

func TestWorkbenchOperatorDefaultIsBoundedGroupedAndJSONStable(t *testing.T) {
	newVerboseOperatorWorkbenchFixture(t, 30)

	operator := captureStdout(t, func() {
		cmdWorkbench([]string{"--operator", "--recent", "10"})
	})
	lineCount := countNonEmptyLines(operator)
	if lineCount > 42 {
		t.Fatalf("default operator output is too verbose: lines=%d\n%s", lineCount, operator)
	}
	if strings.Contains(operator, "Advanced") {
		t.Fatalf("default operator output must hide Advanced without explicit opt-in:\n%s", operator)
	}
	if strings.Contains(operator, "clear 30 blocked items") || strings.Contains(operator, "clear 30 blocked item") {
		t.Fatalf("operator summary must not tell Umut to clear all historical holds:\n%s", operator)
	}
	if !strings.Contains(operator, "repeated held private config gate") {
		t.Fatalf("operator summary should name the actual top priority:\n%s", operator)
	}
	if !strings.Contains(operator, "29 similar") {
		t.Fatalf("duplicate held rows should be grouped with a compact similar-count:\n%s", operator)
	}
	if got := strings.Count(operator, "work_dup_hold_"); got > 2 {
		t.Fatalf("duplicate held work IDs rendered individually (%d occurrences):\n%s", got, operator)
	}
	for _, want := range []string{
		"Needs you",
		"Working",
		"Ready",
		"Recent proof",
		"omitted",
	} {
		if !strings.Contains(operator, want) {
			t.Fatalf("bounded operator output missing %q:\n%s", want, operator)
		}
	}

	baseJSON := captureStdout(t, func() {
		cmdWorkbench([]string{"--json", "--recent", "10"})
	})
	operatorJSON := captureStdout(t, func() {
		cmdWorkbench([]string{"--json", "--operator", "--recent", "10"})
	})
	base := normalizedWorkbenchOutput(t, baseJSON)
	withOperator := normalizedWorkbenchOutput(t, operatorJSON)
	if !reflect.DeepEqual(base, withOperator) {
		t.Fatalf("--operator changed JSON contract\nbase=%+v\noperator=%+v", base, withOperator)
	}
}

func newVerboseOperatorWorkbenchFixture(t *testing.T, duplicateHolds int) string {
	t.Helper()
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	for i := 0; i < duplicateHolds; i++ {
		if _, err := store.Create(work.CreateInput{
			WorkID:           fmt.Sprintf("work_dup_hold_%02d", i),
			Owner:            "compound-core",
			State:            "held",
			CanonicalSurface: "source:private-config",
			SuccessPredicate: "repeated held private config gate",
			DesiredOutcome:   "private config unlocks source refresh",
		}, cliTestNow.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("create duplicate held work %d: %v", i, err)
		}
	}
	for i := 0; i < 4; i++ {
		running, err := store.Create(work.CreateInput{
			WorkID:           fmt.Sprintf("work_running_%02d", i),
			Owner:            "compound-core",
			CanonicalSurface: fmt.Sprintf("surface:running-%02d", i),
			SuccessPredicate: fmt.Sprintf("working mission %02d proves the active lane", i),
			DesiredOutcome:   fmt.Sprintf("working outcome %02d", i),
		}, cliTestNow.Add(time.Duration(duplicateHolds+i)*time.Minute))
		if err != nil {
			t.Fatalf("create running work %d: %v", i, err)
		}
		if _, err := store.Claim(running.ID, "codex", cliTestNow.Add(time.Duration(duplicateHolds+i)*time.Minute)); err != nil {
			t.Fatalf("claim running work %d: %v", i, err)
		}
		if _, err := store.Advance(running.ID, work.AdvanceInput{To: "running"}, cliTestNow.Add(time.Duration(duplicateHolds+i)*time.Minute)); err != nil {
			t.Fatalf("advance running work %d: %v", i, err)
		}
	}
	for i := 0; i < 5; i++ {
		if _, err := store.Create(work.CreateInput{
			WorkID:           fmt.Sprintf("work_ready_%02d", i),
			Owner:            "compound-core",
			CanonicalSurface: fmt.Sprintf("surface:ready-%02d", i),
			SuccessPredicate: fmt.Sprintf("ready mission %02d is dispatchable", i),
			DesiredOutcome:   fmt.Sprintf("ready outcome %02d", i),
		}, cliTestNow.Add(time.Duration(duplicateHolds+10+i)*time.Minute)); err != nil {
			t.Fatalf("create ready work %d: %v", i, err)
		}
	}
	done, err := store.Create(work.CreateInput{
		WorkID:           "work_recent_done",
		Owner:            "compound-core",
		CanonicalSurface: "surface:recent-proof",
		SuccessPredicate: "recent proof stays visible but bounded",
		DesiredOutcome:   "recent learning is available",
	}, cliTestNow.Add(time.Duration(duplicateHolds+20)*time.Minute))
	if err != nil {
		t.Fatalf("create recent proof work: %v", err)
	}
	if _, err := store.Claim(done.ID, "codex", cliTestNow.Add(time.Duration(duplicateHolds+20)*time.Minute)); err != nil {
		t.Fatalf("claim recent proof work: %v", err)
	}
	if _, err := store.Advance(done.ID, work.AdvanceInput{To: "running"}, cliTestNow.Add(time.Duration(duplicateHolds+20)*time.Minute)); err != nil {
		t.Fatalf("advance recent proof running: %v", err)
	}
	if _, err := store.Advance(done.ID, work.AdvanceInput{To: "verifying", VerificationID: "verification_recent_done"}, cliTestNow.Add(time.Duration(duplicateHolds+20)*time.Minute)); err != nil {
		t.Fatalf("advance recent proof verifying: %v", err)
	}
	if _, err := store.Close(done.ID, work.CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "recent proof fixture",
		Changed:   true,
		ProofRefs: []string{"proof:recent-done"},
	}, cliTestNow.Add(time.Duration(duplicateHolds+20)*time.Minute)); err != nil {
		t.Fatalf("close recent proof work: %v", err)
	}
	return homePath
}

func countNonEmptyLines(s string) int {
	count := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func normalizedWorkbenchOutput(t *testing.T, raw string) workbenchCLIOutput {
	t.Helper()
	var out workbenchCLIOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("parse workbench JSON: %v\n%s", err, raw)
	}
	out.GeneratedAt = ""
	return out
}

// Falsifier f1: a fixture where the review queue and attention projection disagree
// must use attention's needs_you count for the stable workbench review_required headline.
func TestWorkbenchHeadlineUsesAttentionProjection(t *testing.T) {
	homePath := newWorkbenchAttentionFixture(t)

	attentionJSON := captureStdout(t, func() {
		cmdAttention([]string{"--json", "--home", homePath})
	})
	var attentionReport attention.Report
	if err := json.Unmarshal([]byte(attentionJSON), &attentionReport); err != nil {
		t.Fatalf("parse attention JSON: %v\n%s", err, attentionJSON)
	}
	if attentionReport.Counts.NeedsYou != 2 {
		t.Fatalf("fixture attention needs_you = %d, want 2", attentionReport.Counts.NeedsYou)
	}

	workbenchJSON := captureStdout(t, func() {
		cmdWorkbench([]string{"--json"})
	})
	var workbenchReport struct {
		ReviewQueue *struct {
			Counts struct {
				ReviewRequired int `json:"review_required"`
			} `json:"counts"`
		} `json:"review_queue"`
	}
	if err := json.Unmarshal([]byte(workbenchJSON), &workbenchReport); err != nil {
		t.Fatalf("parse workbench JSON: %v\n%s", err, workbenchJSON)
	}
	if workbenchReport.ReviewQueue == nil {
		t.Fatalf("workbench review queue missing: %s", workbenchJSON)
	}
	if got := workbenchReport.ReviewQueue.Counts.ReviewRequired; got != attentionReport.Counts.NeedsYou {
		t.Fatalf("workbench review_required = %d, want attention needs_you = %d", got, attentionReport.Counts.NeedsYou)
	}

	human := captureStdout(t, func() {
		cmdWorkbench(nil)
	})
	wantHeadline := fmt.Sprintf("review_queue total=1 review_required=%d", attentionReport.Counts.NeedsYou)
	if !strings.Contains(human, wantHeadline) {
		t.Fatalf("workbench headline missing %q:\n%s", wantHeadline, human)
	}
}

func newWorkbenchAttentionFixture(t *testing.T) string {
	t.Helper()
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	if _, err := reviewartifact.Write(homePath, "product-reviews", "prp_attention_meter", map[string]any{
		"id":            "prp_attention_meter",
		"status":        "review_required",
		"product_id":    "product:fixture",
		"hypothesis_id": "hyp_fixture",
		"signal_id":     "sig_fixture",
	}, " ", "# Product review\n"); err != nil {
		t.Fatalf("write review fixture: %v", err)
	}
	if _, err := reviewartifact.WriteRaw(homePath, "product-reviews", "prp_attention_meter.review.yaml", []byte("schema_version: compound.product_review.v0.1\n")); err != nil {
		t.Fatalf("write review sidecar: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	if _, err := store.Create(work.CreateInput{
		Owner:            "compound-core",
		SuccessPredicate: "fixture work is visible to the attention meter",
	}, cliTestNow); err != nil {
		t.Fatalf("create work fixture: %v", err)
	}
	return homePath
}

// Falsifier f2 (rescoped): when the attention projection's inputs cannot be
// assembled (here, an executor source file the projection tries to read is
// unreadable), the workbench must not silently fall back to recomputing an
// independent review_required total. It must flag attention_unavailable and
// keep the last-known review-artifact-derived count instead.
func TestWorkbenchAttentionUnavailableKeepsLastKnownCountAndFlags(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	if _, err := reviewartifact.Write(homePath, "product-reviews", "prp_meter_unavailable", map[string]any{
		"id":            "prp_meter_unavailable",
		"status":        "review_required",
		"product_id":    "product:fixture",
		"hypothesis_id": "hyp_fixture",
		"signal_id":     "sig_fixture",
	}, " ", "# Product review\n"); err != nil {
		t.Fatalf("write review fixture: %v", err)
	}
	if _, err := reviewartifact.WriteRaw(homePath, "product-reviews", "prp_meter_unavailable.review.yaml", []byte("schema_version: compound.product_review.v0.1\n")); err != nil {
		t.Fatalf("write review sidecar: %v", err)
	}

	// ExecutorsProjection treats a regular hook-events file it cannot open
	// (permission denied, not "does not exist") as a hard error — that is
	// the attention-unavailable path this falsifier exercises.
	hookEventsPath := filepath.Join(homePath, "events", "hook-events.ndjson")
	if err := os.WriteFile(hookEventsPath, []byte(`{"created_at":"2026-07-10T00:00:00Z","harness":"codex","hook":"stop"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write hook events fixture: %v", err)
	}
	if err := os.Chmod(hookEventsPath, 0o000); err != nil {
		t.Fatalf("chmod hook events fixture: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(hookEventsPath, 0o644) })

	workbenchJSON := captureStdout(t, func() {
		cmdWorkbench([]string{"--json"})
	})
	var workbenchReport struct {
		ReviewQueue *struct {
			Counts struct {
				ReviewRequired int `json:"review_required"`
			} `json:"counts"`
			AttentionUnavailable bool `json:"attention_unavailable"`
		} `json:"review_queue"`
	}
	if err := json.Unmarshal([]byte(workbenchJSON), &workbenchReport); err != nil {
		t.Fatalf("parse workbench JSON: %v\n%s", err, workbenchJSON)
	}
	if workbenchReport.ReviewQueue == nil {
		t.Fatalf("workbench review queue missing: %s", workbenchJSON)
	}
	if !workbenchReport.ReviewQueue.AttentionUnavailable {
		t.Fatalf("expected attention_unavailable=true when the attention projection's inputs error: %s", workbenchJSON)
	}
	if got := workbenchReport.ReviewQueue.Counts.ReviewRequired; got != 1 {
		t.Fatalf("workbench review_required = %d, want last-known review-artifact count 1 (no silent independent recompute): %s", got, workbenchJSON)
	}
}

// Falsifier f3: compound workbench is a read model. Running it (JSON and
// human-readable) — including the new attention-meter seam added by this
// packet — must never write to COMPOUND_HOME.
func TestWorkbenchCommandIsReadOnly(t *testing.T) {
	homePath := newWorkbenchAttentionFixture(t)

	before := hashHomeDir(t, homePath)
	captureStdout(t, func() {
		cmdWorkbench([]string{"--json"})
	})
	captureStdout(t, func() {
		cmdWorkbench(nil)
	})
	after := hashHomeDir(t, homePath)
	if before != after {
		t.Fatalf("compound workbench mutated COMPOUND_HOME: before=%s after=%s", before, after)
	}
}
