package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/mem"
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

	inboxAfter := snapshotTree(t, filepath.Join(homePath, candidates.InboxDir(candidates.Memory)))
	claimsAfter := readFileOrEmpty(t, claimsPath)
	if !reflect.DeepEqual(inboxAfter, inboxBefore) {
		t.Fatalf("memory candidate inbox mutated:\nbefore=%v\nafter=%v", inboxBefore, inboxAfter)
	}
	if string(claimsAfter) != string(claimsBefore) {
		t.Fatalf("claims.jsonl mutated:\nbefore=%s\nafter=%s", claimsBefore, claimsAfter)
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
