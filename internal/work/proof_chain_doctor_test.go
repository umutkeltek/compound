package work

import (
	"strings"
	"testing"

	"compound/internal/envelope"
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

func TestProofChainDoctorFailsTerminalPolicyActionMismatch(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "policy action mismatch"}, fixedNow)
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
		Summary:   "policy action mismatch covered",
		ProofRefs: []string{"repair:proof-chain-doctor"},
	}, fixedNow)
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	var pd envelope.PolicyDecision
	if err := s.readJSON(s.envPath(closed.Work.PolicyDecisionID), &pd); err != nil {
		t.Fatalf("read policy decision: %v", err)
	}
	pd.ActionIntentID = "act_missing_from_work"
	if err := s.writeJSON(s.envPath(pd.ID), &pd); err != nil {
		t.Fatalf("write corrupted policy decision: %v", err)
	}

	report, err := s.ProofChainDoctor(fixedNow, ProofChainDoctorOptions{})
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if report.Status != "fail" || report.Counts.MismatchedActionPolicy != 1 {
		t.Fatalf("report = %+v, want action/policy mismatch failure", report)
	}
	if !proofChainFindingContains(report, "policy_action_mismatch") {
		t.Fatalf("findings missing policy action mismatch: %+v", report.Findings)
	}
}

func TestProofChainDoctorFailsMissingTransitionActionRef(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "transition action ref mismatch"}, fixedNow)
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
		Summary:   "transition action ref mismatch covered",
		ProofRefs: []string{"repair:proof-chain-doctor"},
	}, fixedNow)
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	closed.Transition.ActionRefs = nil
	if err := s.writeJSON(s.envPath(closed.Transition.ID), &closed.Transition); err != nil {
		t.Fatalf("write corrupted transition: %v", err)
	}

	report, err := s.ProofChainDoctor(fixedNow, ProofChainDoctorOptions{})
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if report.Status != "fail" || report.Counts.MissingTransitionActionRef == 0 {
		t.Fatalf("report = %+v, want transition action-ref failure", report)
	}
	if !proofChainFindingContains(report, "transition_missing_action_ref") {
		t.Fatalf("findings missing transition action-ref mismatch: %+v", report.Findings)
	}
}

func TestProofChainDoctorFindsOrphanRuntimeEnvelopes(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "orphan envelope scan"}, fixedNow)
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
		Summary:   "orphan envelope scan covered",
		ProofRefs: []string{"repair:proof-chain-doctor"},
	}, fixedNow)
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	var ai envelope.ActionIntent
	if err := s.readJSON(s.envPath(closed.Work.RunIDs[len(closed.Work.RunIDs)-1]), &ai); err != nil {
		t.Fatalf("read action intent: %v", err)
	}
	ai.ID = "act_orphan_extra"
	if err := s.writeJSON(s.envPath(ai.ID), &ai); err != nil {
		t.Fatalf("write orphan action intent: %v", err)
	}
	var pd envelope.PolicyDecision
	if err := s.readJSON(s.envPath(closed.Work.PolicyDecisionID), &pd); err != nil {
		t.Fatalf("read policy decision: %v", err)
	}
	pd.ID = "pol_orphan_extra"
	if err := s.writeJSON(s.envPath(pd.ID), &pd); err != nil {
		t.Fatalf("write orphan policy decision: %v", err)
	}
	rec := closed.Receipt
	rec.ID = "rec_orphan_extra"
	if err := s.writeJSON(s.envPath(rec.ID), &rec); err != nil {
		t.Fatalf("write orphan receipt: %v", err)
	}
	tr := closed.Transition
	tr.ID = "tr_orphan_extra"
	if err := s.writeJSON(s.envPath(tr.ID), &tr); err != nil {
		t.Fatalf("write orphan transition: %v", err)
	}

	report, err := s.ProofChainDoctor(fixedNow, ProofChainDoctorOptions{})
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if report.Status != "fail" ||
		report.Counts.OrphanActionIntent != 1 ||
		report.Counts.OrphanPolicyDecision != 1 ||
		report.Counts.OrphanReceipt != 1 ||
		report.Counts.OrphanTransition != 1 {
		t.Fatalf("orphan counts = %+v", report.Counts)
	}
	for _, code := range []string{"orphan_action_intent", "orphan_policy_decision", "orphan_receipt", "orphan_transition"} {
		if !proofChainFindingContains(report, code) {
			t.Fatalf("findings missing %s: %+v", code, report.Findings)
		}
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
