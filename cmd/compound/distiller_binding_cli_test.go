package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"compound/internal/candidates"
	"compound/internal/distiller"
)

// The S4 binding gate is enforced at the CLI, not only inside the package: a
// regression-fix claim whose before proof is missing, or whose before proof
// also passed, must be refused by `compound distiller improvement-review`.
//
// These run the real binary because the CLI exits non-zero on a refused
// review, which would abort the in-process test binary.
func TestDistillerImprovementReviewCLIRefusesUnboundRegressionFix(t *testing.T) {
	binary := compoundTestBinary(t)

	cases := []struct {
		name   string
		mutate func(t *testing.T, review *distiller.ImprovementReview)
		want   string
	}{
		{
			name: "missing before proof",
			mutate: func(t *testing.T, review *distiller.ImprovementReview) {
				review.BeforeEvalProofRefs = nil
			},
			want: "before",
		},
		{
			name: "both green pair",
			mutate: func(t *testing.T, review *distiller.ImprovementReview) {
				// Point the before ref at the PASSING after proof: a both-green
				// pair establishes no fix, because nothing failed before.
				review.BeforeEvalProofRefs = []string{review.EvalProofRefs[0]}
			},
			want: "fail",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			homePath := initializedTestHome(t)
			t.Setenv("COMPOUND_HOME", homePath)

			candidateID := "method_upgrade_cli_binding_" + strings.ReplaceAll(tc.name, " ", "_")
			writeCLIBindingCandidate(t, homePath, candidateID)

			planOut := runCLIDistiller(t, binary, homePath, "improvement-plan", "--candidate", candidateID)
			reviewPath := outputValue(planOut, "- review_yaml:")
			if reviewPath == "" {
				t.Fatalf("improvement-plan did not print a review yaml:\n%s", planOut)
			}

			beforeOut := runCLIDistiller(t, binary, homePath, "eval",
				filepath.Join(testRepoRoot, "fixtures", "distiller", "llm-offline-eval-before.json"), "--record-before")
			beforeRef := outputValue(beforeOut, "- proof_ref:")
			if beforeRef == "" {
				t.Fatalf("record-before printed no proof_ref:\n%s", beforeOut)
			}
			afterOut := runCLIDistiller(t, binary, homePath, "eval",
				filepath.Join(testRepoRoot, "fixtures", "distiller", "llm-offline-eval.json"), "--record")
			afterRef := outputValue(afterOut, "- proof_ref:")
			if afterRef == "" {
				t.Fatalf("record printed no proof_ref:\n%s", afterOut)
			}

			var review distiller.ImprovementReview
			if err := yaml.Unmarshal([]byte(readFile(t, reviewPath)), &review); err != nil {
				t.Fatalf("parse review yaml: %v", err)
			}
			review.Decision = distiller.ImprovementReviewDecisionAccept
			review.Rationale = "reviewed: the pinned replay case exercises the extraction method end to end."
			review.Confidence = 0.8
			review.EvalProofRefs = []string{afterRef}
			review.BeforeEvalProofRefs = []string{beforeRef}
			review.ReplayCases = []distiller.ReplayCaseBinding{{
				CaseID:        "eval_llm_response_adapter_without_authority",
				Assertion:     "decisions",
				RegressionFix: true,
			}}
			tc.mutate(t, &review)

			reviewData, err := yaml.Marshal(review)
			if err != nil {
				t.Fatalf("marshal review: %v", err)
			}
			editedPath := filepath.Join(t.TempDir(), "edited.review.yaml")
			if err := os.WriteFile(editedPath, reviewData, 0o644); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command(binary, "distiller", "improvement-review", editedPath)
			cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("CLI accepted an unbound regression-fix claim:\n%s", out)
			}
			if !strings.Contains(strings.ToLower(string(out)), tc.want) {
				t.Fatalf("refusal must mention %q:\n%s", tc.want, out)
			}
		})
	}
}

// writeCLIBindingCandidate writes a method_upgrade candidate into the home's
// candidate inbox so the CLI improvement-plan path has something to plan.
func writeCLIBindingCandidate(t *testing.T, homePath, candidateID string) {
	t.Helper()
	dir := filepath.Join(homePath, candidates.InboxDir(candidates.MethodUpgrade))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir candidate inbox: %v", err)
	}
	candidate := distiller.Candidate{
		ID:           candidateID,
		Kind:         distiller.KindMethodUpgrade,
		Subject:      "conversation_mining",
		Statement:    "narrow the conversation-mining import so ungrounded facts never become candidates",
		Detail:       "self_improvement",
		Confidence:   0.86,
		EvidenceRefs: []string{"source_observation:convmin"},
		Verdict:      candidates.NoPromote,
	}
	data, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		t.Fatalf("marshal candidate: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, candidateID+".json"), data, 0o644); err != nil {
		t.Fatalf("write candidate: %v", err)
	}
}

// runCLIDistiller runs the real compound binary for a distiller subcommand.
func runCLIDistiller(t *testing.T, binary, homePath string, args ...string) string {
	t.Helper()
	cmd := exec.Command(binary, append([]string{"distiller"}, args...)...)
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compound distiller %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
