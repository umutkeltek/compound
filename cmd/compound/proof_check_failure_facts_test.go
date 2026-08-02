package main

import (
	"compound/internal/work"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestProofCheckFailedStepFactsUseFullOutputBeforeTailTruncation(t *testing.T) {
	originalResolver := proofCheckRuntimeExecutable
	proofCheckRuntimeExecutable = func(string) (string, error) { return "/bin/sh", nil }
	t.Cleanup(func() { proofCheckRuntimeExecutable = originalResolver })

	prefix := strings.Repeat("x", 5000)
	result := runProofCheckStep(t.TempDir(), proofCheckStep{
		Name:     "synthetic-failure",
		Command:  []string{"sh", "-c", "printf '%s\\n' '" + prefix + "'; printf '%s\\n' 'root failure fact' >&2; exit 1"},
		ProofRef: "synthetic-failure-proof",
	}, "")
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if strings.Contains(result.OutputTail, prefix) {
		t.Fatalf("output tail retained the deliberately truncated prefix")
	}
	want := failureFactForTest("root failure fact")
	if !containsString(result.FailureFacts, want) {
		t.Fatalf("failure_facts = %v, want %q derived from full output", result.FailureFacts, want)
	}
}

func TestProofCheckFailureFactsAreDeterministicUniqueAndEmptyOnSuccess(t *testing.T) {
	got := work.ProofFailureFacts([]byte(" beta  failure \nalpha failure\nbeta failure\n\n"), errors.New("failed"))
	want := []string{failureFactForTest("alpha failure"), failureFactForTest("beta failure")}
	if len(got) != len(want) {
		t.Fatalf("failure_facts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("failure_facts = %v, want %v", got, want)
		}
	}
	if got := work.ProofFailureFacts([]byte("ignored"), nil); len(got) != 0 {
		t.Fatalf("successful output produced failure facts: %v", got)
	}
}

func failureFactForTest(line string) string {
	normalized := strings.Join(strings.Fields(line), " ")
	sum := sha256.Sum256([]byte(normalized))
	return "sha256:" + hex.EncodeToString(sum[:])
}
