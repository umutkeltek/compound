package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/home"
	"compound/internal/mem"
	"compound/internal/reviewartifact"
)

func TestSynthesisSweepCLIWritesReportAndDoesNotMutateMemory(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}

	first := mem.Candidate{
		Entity:         "project:api",
		Predicate:      "runtime",
		Object:         "Go",
		Confidence:     0.9,
		Derivation:     "extracted",
		ClaimType:      "decision",
		Qualifiers:     map[string]string{"domain": "language"},
		SourceTraceRef: "trace_a",
		SnippetHash:    "hash_a",
		Visibility:     "public",
	}
	second := first
	second.Predicate = "implementation_language"
	second.Object = " go "
	second.SourceTraceRef = "trace_b"
	second.SnippetHash = "hash_b"
	if _, err := store.Capture([]mem.Candidate{first, second}, time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	claimsPath := filepath.Join(homePath, "ledger", "claims.jsonl")
	appendClaimLine(t, claimsPath, mem.Claim{
		ID:         "claim_go",
		Seq:        1,
		Entity:     "project:api",
		Predicate:  "runtime",
		Object:     "Go",
		ClaimType:  "decision",
		Qualifiers: map[string]string{"domain": "language"},
		Confidence: 0.9,
		Visibility: "public",
		CreatedAt:  "2026-07-02T00:00:00Z",
	})
	appendClaimLine(t, claimsPath, mem.Claim{
		ID:         "claim_rust",
		Seq:        2,
		Entity:     "project:api",
		Predicate:  "implementation_language",
		Object:     "Rust",
		ClaimType:  "decision",
		Qualifiers: map[string]string{"domain": "language"},
		Confidence: 0.8,
		Visibility: "public",
		CreatedAt:  "2026-07-02T00:00:00Z",
	})

	inboxBefore := snapshotTree(t, filepath.Join(homePath, candidates.InboxDir(candidates.Memory)))
	claimsBefore := readFileOrEmpty(t, claimsPath)

	out := captureStdout(t, func() {
		cmdSynthesis([]string{"sweep", "--stage", "cluster,contradict", "--json"})
	})
	var result synthesisSweepOutput
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("parse synthesis output: %v\n%s", err, out)
	}
	if result.WorkID == "" || result.ReceiptID == "" || result.TransitionID == "" {
		t.Fatalf("run did not record work/receipt/transition: %+v", result)
	}
	if result.Report.EchoFoldProposalCount != 1 || result.Report.ContradictionProposalCount != 1 {
		t.Fatalf("report counts = echo %d contradiction %d, want 1/1", result.Report.EchoFoldProposalCount, result.Report.ContradictionProposalCount)
	}
	if result.ReportPath == "" {
		t.Fatal("expected report artifact path")
	}
	if _, err := os.Stat(result.ReportPath); err != nil {
		t.Fatalf("report artifact missing: %v", err)
	}
	if len(result.ContradictionReviewPaths) != 1 {
		t.Fatalf("contradiction review paths = %v, want one", result.ContradictionReviewPaths)
	}

	queue, err := buildReviewQueueReport(homePath, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Counts.Total != 2 || queue.Counts.ReviewRequired != 0 || queue.Counts.OK != 2 || queue.Counts.ByStatus["recorded"] != 2 {
		t.Fatalf("synthesis reviews should be recorded, not unknown review debt: %+v", queue.Counts)
	}
	for _, item := range queue.Items {
		if item.Status != "recorded" || item.Readiness != "review_recorded" {
			t.Fatalf("synthesis item not recorded/review_recorded: %+v", item)
		}
		if item.NotToDo == "" || !strings.Contains(item.NotToDo, "do not bulk-apply") {
			t.Fatalf("synthesis item missing mutation guardrail: %+v", item)
		}
		if len(item.Actions) != 0 {
			t.Fatalf("proposal-only synthesis item must not expose apply actions: %+v", item)
		}
	}

	inboxAfter := snapshotTree(t, filepath.Join(homePath, candidates.InboxDir(candidates.Memory)))
	claimsAfter := readFileOrEmpty(t, claimsPath)
	if !reflect.DeepEqual(inboxAfter, inboxBefore) {
		t.Fatalf("memory candidate inbox mutated:\nbefore=%v\nafter=%v", inboxBefore, inboxAfter)
	}
	if string(claimsAfter) != string(claimsBefore) {
		t.Fatalf("claims.jsonl mutated:\nbefore=%s\nafter=%s", claimsBefore, claimsAfter)
	}
}

func TestSynthesisReviewQueueAnnotatesLegacyStatuslessArtifacts(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	if _, err := reviewartifact.Write(homePath, synthesisContradictionArea, "synthesis_contradiction_legacy", map[string]any{
		"schema_version": "compound.synthesis_contradiction_review.v0.1",
		"id":             "synthesis_contradiction_legacy",
		"work_id":        "work_legacy",
		"proposal": map[string]any{
			"id":          "synth_contradiction_legacy",
			"cluster_key": "project:api\x1flanguage\x1fdecision",
			"claim_ids":   []string{"claim_go", "claim_rust"},
			"objects":     []string{"Go", "Rust"},
		},
	}, " ", "# Legacy synthesis contradiction\n"); err != nil {
		t.Fatal(err)
	}

	queue, err := buildReviewQueueReport(homePath, synthesisContradictionArea, false)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Counts.Total != 1 || queue.Counts.ReviewRequired != 0 || queue.Counts.OK != 1 || queue.Counts.ByStatus["recorded"] != 1 {
		t.Fatalf("legacy statusless synthesis item should be projected recorded: %+v", queue.Counts)
	}
	item := queue.Items[0]
	if item.Status != "recorded" || item.Readiness != "review_recorded" || item.SourceKind != "memory-synthesis-contradiction" {
		t.Fatalf("legacy synthesis item missing projection metadata: %+v", item)
	}
	if !strings.Contains(item.NextAction, "supersession") || !strings.Contains(item.NotToDo, "do not bulk-apply") {
		t.Fatalf("legacy synthesis item missing operator guardrails: %+v", item)
	}
	if len(item.Actions) != 0 {
		t.Fatalf("legacy synthesis item must not expose apply actions: %+v", item)
	}
}

func appendClaimLine(t *testing.T, path string, claim mem.Claim) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(claim); err != nil {
		t.Fatal(err)
	}
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func readFileOrEmpty(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return data
}
