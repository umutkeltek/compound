package main

import (
	"encoding/json"
	"strings"
	"testing"

	"compound/internal/home"
	"compound/internal/work"
)

func TestWorkProofChainDoctorCLIJSON(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	w, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "proof-chain CLI reports clean terminal work"}, cliTestNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "running", Note: "run"}, cliTestNow); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if _, err := store.Close(w.ID, work.CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "proof-chain CLI covered",
		ProofRefs: []string{"repair:proof-chain-cli"},
	}, cliTestNow); err != nil {
		t.Fatalf("close: %v", err)
	}

	out := captureStdout(t, func() {
		cmdWork([]string{"proof-chain-doctor", "--json"})
	})
	var report work.ProofChainDoctorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse report: %v\n%s", err, out)
	}
	if report.Status != "ok" || report.Counts.TerminalChecked != 1 || report.Counts.Complete != 1 {
		t.Fatalf("report = %+v, want one clean terminal chain", report)
	}
	if len(report.ProofRefs) == 0 || !strings.Contains(strings.Join(report.ProofRefs, ","), "work-proof-chain-terminal-receipt") {
		t.Fatalf("proof refs missing terminal receipt guard: %+v", report.ProofRefs)
	}
}
