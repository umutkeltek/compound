package panel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/envelope"
	"compound/internal/mem"
	"compound/internal/work"
)

func TestApplyVerdictMappingAndProofChain(t *testing.T) {
	home := t.TempDir()
	writePolicyHoldFixture(t, home, "mcand_policy_hold")
	cases := []struct {
		id          string
		decision    string
		basis       string
		wantMapped  string
		wantState   string
		wantApplied bool
	}{
		{id: "mcand_promote_valid", decision: "promote", basis: candidates.BasisDecisionRecord, wantMapped: "promote", wantState: "promoted", wantApplied: true},
		{id: "mcand_promote_no_basis", decision: "promote", wantMapped: "park", wantState: "parked", wantApplied: true},
		{id: "mcand_park", decision: "park", basis: candidates.BasisRuntimeProof, wantMapped: "park", wantState: "parked", wantApplied: true},
		{id: "mcand_reject", decision: "reject", basis: candidates.BasisRuntimeProof, wantMapped: "reject", wantState: "rejected", wantApplied: true},
		{id: "mcand_merge", decision: "merge_existing", basis: candidates.BasisRuntimeProof, wantMapped: "merge_existing", wantState: "rejected", wantApplied: true},
		{id: "mcand_needs_human", decision: "needs_human", basis: candidates.BasisRuntimeProof, wantMapped: "needs_human", wantState: "parked", wantApplied: false},
		{id: "mcand_needs_trial", decision: "needs_trial", basis: candidates.BasisRuntimeProof, wantMapped: "needs_trial", wantState: "parked", wantApplied: false},
		{id: "mcand_policy_hold", decision: "park", basis: candidates.BasisRuntimeProof, wantMapped: "park", wantState: "parked", wantApplied: false},
	}
	findings := make([]TargetFinding, 0, len(cases))
	targets := make([]Target, 0, len(cases))
	for _, tc := range cases {
		writeMemoryCandidateRecord(t, home, tc.id, map[string]any{
			"id":               tc.id,
			"entity":           "service:panel-apply",
			"predicate":        "has_" + strings.TrimPrefix(tc.id, "mcand_"),
			"object":           "true",
			"claim_type":       "status",
			"confidence":       0.91,
			"promotion_basis":  tc.basis,
			"state":            "parked",
			"visibility":       "contextual",
			"derivation":       "deterministic",
			"source_trace_ref": "trace://" + tc.id,
			"snippet_hash":     "sha256:" + tc.id,
			"observation_date": "2026-07-01T00:00:00Z",
			"created_at":       "2026-07-01T10:00:00Z",
		})
		artifact := writeApplyVerdictArtifact(t, home, tc.id, tc.decision)
		findings = append(findings, TargetFinding{
			TargetRef:       tc.id,
			VerdictArtifact: artifact,
			Synthesis:       Synthesis{SchemaVersion: SynthesisSchemaVersion, Decision: tc.decision, Confidence: 0.9, Rationale: "synthetic " + tc.decision},
			LensFindings:    []LensFinding{{LensRef: "lens:test", Status: "completed"}},
		})
		targets = append(targets, Target{Ref: tc.id, Privacy: "contextual", StateBefore: "parked"})
	}
	run := Run{
		SchemaVersion: RunSchemaVersion,
		ID:            "prun_20260701_apply",
		CreatedAt:     panelTestNow.Format(time.RFC3339),
		Status:        "verdicts_recorded",
		TargetKind:    "memory_candidate",
		Supervised:    true,
		Supervision:   "operator",
		Targets:       targets,
		Findings:      findings,
		CountsBefore:  map[string]int{"parked": len(cases)},
		CountsAfter:   map[string]int{"parked": len(cases)},
		EvidenceRefs:  []string{},
	}
	beforeClaims := countLines(t, filepath.Join(home, "ledger", "claims.jsonl"))
	now := incrementingPanelNow()
	got, err := Apply(home, run, now)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.Status != "applied" || got.WorkID == "" || got.ReceiptID == "" || got.TransitionID == "" {
		t.Fatalf("apply proof refs not populated: %+v", got)
	}
	if got.CountsAfter["promoted"] != 1 || got.CountsAfter["rejected"] != 2 || got.CountsAfter["parked"] != 5 {
		t.Fatalf("counts_after = %+v", got.CountsAfter)
	}
	if countLines(t, filepath.Join(home, "ledger", "claims.jsonl")) != beforeClaims+1 {
		t.Fatalf("claims log did not grow by one")
	}
	if got.CountsAfter["parked"] != 5 {
		t.Fatalf("parked count = %d, want 5", got.CountsAfter["parked"])
	}
	if lines := countLines(t, filepath.Join(home, "ledger", "no-promote-decisions.jsonl")); lines != 3 {
		t.Fatalf("no-promote rows = %d, want 3", lines)
	}
	byID := map[string]TargetFinding{}
	for _, finding := range got.Findings {
		byID[finding.TargetRef] = finding
	}
	for _, tc := range cases {
		finding := byID[tc.id]
		if finding.Application.MappedVerdict != tc.wantMapped || finding.Application.Applied != tc.wantApplied {
			t.Fatalf("%s application = %+v, want mapped=%s applied=%t", tc.id, finding.Application, tc.wantMapped, tc.wantApplied)
		}
		cand := readCandidateForTest(t, home, tc.id)
		if cand.State != tc.wantState {
			t.Fatalf("%s state = %q, want %q", tc.id, cand.State, tc.wantState)
		}
	}
	if byID["mcand_promote_valid"].Application.ClaimRef == "" || byID["mcand_promote_valid"].Application.TransitionRef == "" {
		t.Fatalf("promote application missing claim/transition: %+v", byID["mcand_promote_valid"].Application)
	}
	for _, id := range []string{"mcand_promote_no_basis", "mcand_park", "mcand_merge"} {
		if byID[id].Application.NoPromoteRef == "" {
			t.Fatalf("%s missing no-promote ref: %+v", id, byID[id].Application)
		}
	}
	if !strings.Contains(byID["mcand_policy_hold"].Application.Note, "policy") {
		t.Fatalf("policy hold note = %q", byID["mcand_policy_hold"].Application.Note)
	}
	assertApplyWorkProof(t, home, got, byID["mcand_promote_valid"].Application.TransitionRef)

	again, err := Apply(home, got, now)
	if err != nil {
		t.Fatalf("reapply: %v", err)
	}
	if countLines(t, filepath.Join(home, "ledger", "claims.jsonl")) != beforeClaims+1 {
		t.Fatalf("reapply appended duplicate claim")
	}
	if lines := countLines(t, filepath.Join(home, "ledger", "no-promote-decisions.jsonl")); lines != 3 {
		t.Fatalf("reapply no-promote rows = %d, want 3", lines)
	}
	if again.Status != "applied" || again.ReceiptID == "" || again.TransitionID == "" {
		t.Fatalf("reapply run = %+v", again)
	}
}

func writePolicyHoldFixture(t *testing.T, home, candidateID string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("forbidden_paths:\n  - '**/" + candidateID + ".json'\n")
	if err := os.WriteFile(filepath.Join(home, "config", "policy.yaml"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeApplyVerdictArtifact(t *testing.T, home, targetID, decision string) string {
	t.Helper()
	path := filepath.Join(home, "reviews", "panel-verdicts", targetID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"target": targetID, "decision": decision}
	data, err := json.MarshalIndent(payload, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertApplyWorkProof(t *testing.T, home string, run Run, memoryTransitionID string) {
	t.Helper()
	store, err := work.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Get(run.WorkID)
	if err != nil {
		t.Fatalf("get work: %v", err)
	}
	if w.State != "completed" || w.IDs.TransitionID != run.TransitionID {
		t.Fatalf("work proof = state %s transition %s, run transition %s", w.State, w.IDs.TransitionID, run.TransitionID)
	}
	for _, id := range []string{run.ReceiptID, run.TransitionID, memoryTransitionID} {
		if _, err := os.Stat(filepath.Join(home, "events", id+".json")); err != nil {
			t.Fatalf("missing event envelope %s: %v", id, err)
		}
	}
	var receipt envelope.Receipt
	readJSONForTest(t, filepath.Join(home, "events", run.ReceiptID+".json"), &receipt)
	if receipt.MoveType != "memory_delta" || !containsString(receipt.Memory.Promoted, "mcand_promote_valid") || !containsString(receipt.Memory.Parked, "mcand_park") || !containsString(receipt.Memory.Rejected, "mcand_reject") {
		t.Fatalf("receipt memory delta = %+v move=%s", receipt.Memory, receipt.MoveType)
	}
}

func readCandidateForTest(t *testing.T, home, id string) mem.Candidate {
	t.Helper()
	var cand mem.Candidate
	if err := candidates.ReadCandidate(home, candidates.Memory, id, &cand); err != nil {
		t.Fatal(err)
	}
	return cand
}

func readJSONForTest(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "\n") + 1
}

func incrementingPanelNow() func() time.Time {
	var tick int
	return func() time.Time {
		tick++
		return panelTestNow.Add(time.Duration(tick) * time.Second)
	}
}

func TestApplyFailSoftTerminalizesEnvelopeOnTargetFailure(t *testing.T) {
	home := setupPanelPlanHome(t)
	writePanelFixtureTarget(t, home, "mcand_example_api_clean")
	writePanelFixtureTarget(t, home, "mcand_example_api_dissent")
	run := buildRunnablePlan(t, home, []string{"mcand_example_api_clean", "mcand_example_api_dissent"}, Budget{})
	run, err := ExecuteDryRun(context.Background(), home, run, FixtureCaller{Dir: filepath.Join("..", "..", "fixtures", "panel")}, fixedPanelNow)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	// Break the second target after verdicts: apply must fail soft, not strand the run.
	if err := os.Remove(filepath.Join(home, "inbox", "memory-candidates", "mcand_example_api_dissent.json")); err != nil {
		t.Fatalf("remove candidate: %v", err)
	}
	got, err := Apply(home, run, fixedPanelNow)
	if err == nil || !strings.Contains(err.Error(), "target failure") {
		t.Fatalf("want completed-with-failures error, got %v", err)
	}
	if got.Status != "applied" {
		t.Fatalf("run must not strand at running: status=%q", got.Status)
	}
	if got.ReceiptID == "" || got.TransitionID == "" {
		t.Fatalf("work envelope must terminalize: receipt=%q transition=%q", got.ReceiptID, got.TransitionID)
	}
	var appliedOK, failedNoted bool
	for _, f := range got.Findings {
		if f.TargetRef == "mcand_example_api_clean" && f.Application.Applied {
			appliedOK = true
		}
		if f.TargetRef == "mcand_example_api_dissent" && !f.Application.Applied && f.Application.Note != "" {
			failedNoted = true
		}
	}
	if !appliedOK || !failedNoted {
		t.Fatalf("fail-soft accounting wrong: %+v", got.Findings)
	}
}
