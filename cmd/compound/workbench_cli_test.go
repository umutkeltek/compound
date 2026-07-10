package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/attention"
	"compound/internal/home"
	"compound/internal/reviewartifact"
	"compound/internal/work"
)

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
