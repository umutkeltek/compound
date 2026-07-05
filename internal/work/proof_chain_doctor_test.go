package work

import (
	"strings"
	"testing"
)

func TestProofChainDoctorReportsCleanTerminalWork(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "terminal proof chain"}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Claim(w.ID, "codex", fixedNow); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "running", Note: "run"}, fixedNow); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if _, err := s.Close(w.ID, CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "proof-chain guard covered",
		ProofRefs: []string{"repair:proof-chain-doctor"},
	}, fixedNow); err != nil {
		t.Fatalf("close: %v", err)
	}

	report, err := s.ProofChainDoctor(fixedNow, ProofChainDoctorOptions{})
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if report.Status != "ok" || len(report.Findings) != 0 {
		t.Fatalf("report = %+v, want clean ok", report)
	}
	if report.Counts.TerminalChecked != 1 || report.Counts.Complete != 1 {
		t.Fatalf("counts = %+v, want one complete terminal chain", report.Counts)
	}
}

func TestProofChainDoctorFailsTerminalWorkMissingReceiptTransition(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "broken terminal proof chain"}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	w.State = "completed"
	if err := s.writeJSON(s.workPath(w.ID), &w); err != nil {
		t.Fatalf("write broken work: %v", err)
	}

	report, err := s.ProofChainDoctor(fixedNow, ProofChainDoctorOptions{})
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if report.Status != "fail" {
		t.Fatalf("status = %q, want fail: %+v", report.Status, report)
	}
	if report.Counts.MissingReceipt != 1 || report.Counts.MissingTransition != 1 {
		t.Fatalf("counts = %+v, want missing receipt and transition", report.Counts)
	}
	if !proofChainFindingContains(report, "missing_receipt") || !proofChainFindingContains(report, "missing_transition") {
		t.Fatalf("findings missing expected codes: %+v", report.Findings)
	}
}

func TestProofChainDoctorFailsMismatchedTerminalWorkID(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "mismatched terminal proof chain"}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Claim(w.ID, "codex", fixedNow); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "running", Note: "run"}, fixedNow); err != nil {
		t.Fatalf("advance: %v", err)
	}
	closed, err := s.Close(w.ID, CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Summary:   "proof-chain mismatch covered",
		ProofRefs: []string{"repair:proof-chain-doctor"},
	}, fixedNow)
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	closed.Receipt.IDs.WorkID = "work_other"
	if err := s.writeJSON(s.envPath(closed.Receipt.ID), &closed.Receipt); err != nil {
		t.Fatalf("write corrupted receipt: %v", err)
	}

	report, err := s.ProofChainDoctor(fixedNow, ProofChainDoctorOptions{})
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if report.Status != "fail" || report.Counts.MismatchedWorkID != 1 {
		t.Fatalf("report = %+v, want work-id mismatch failure", report)
	}
	if !proofChainFindingContains(report, "receipt_work_mismatch") {
		t.Fatalf("findings missing receipt mismatch: %+v", report.Findings)
	}
}

func proofChainFindingContains(report ProofChainDoctorReport, code string) bool {
	for _, finding := range report.Findings {
		if finding.Code == code || strings.Contains(finding.Message, code) {
			return true
		}
	}
	return false
}
