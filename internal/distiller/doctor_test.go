package distiller

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDoctorReportsSelfImprovementState(t *testing.T) {
	home, store := setup(t)
	raw := readTraceFixture(t, "distill-session.json")

	if _, err := Run(home, store, raw, DefaultPolicy(), ReferenceExtractor{}, t0); err != nil {
		t.Fatalf("run trace fixture: %v", err)
	}

	report, err := Doctor(home)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if report.Status != "attention" {
		t.Fatalf("status = %q, want attention", report.Status)
	}
	if report.TotalDecisions != 6 {
		t.Fatalf("total decisions = %d, want 6", report.TotalDecisions)
	}
	if report.MemoryPromoted != 0 || report.MemoryParked != 2 || report.MemoryRejected != 0 {
		t.Fatalf("memory counts promoted/parked/rejected = %d/%d/%d, want 0/2/0",
			report.MemoryPromoted, report.MemoryParked, report.MemoryRejected)
	}
	if report.EvalCases != 1 {
		t.Fatalf("eval cases = %d, want 1", report.EvalCases)
	}
	if report.MethodUpgradeDebt != 1 || report.PolicyPatchDebt != 0 || report.SelfImprovementDebt != 1 {
		t.Fatalf("self-improvement debt method/policy/total = %d/%d/%d, want 1/0/1",
			report.MethodUpgradeDebt, report.PolicyPatchDebt, report.SelfImprovementDebt)
	}
	if report.CapabilityObservations != 1 || report.OperationalizedCapabilityObservations != 1 {
		t.Fatalf("capability observations candidate/operationalized = %d/%d, want 1/1",
			report.CapabilityObservations, report.OperationalizedCapabilityObservations)
	}
	if report.CandidateInboxCounts[KindEvalCase] != 1 {
		t.Fatalf("eval-case inbox count = %d, want 1", report.CandidateInboxCounts[KindEvalCase])
	}
	if report.CandidateInboxCounts[KindMethodUpgrade] != 1 {
		t.Fatalf("method-upgrade inbox count = %d, want 1", report.CandidateInboxCounts[KindMethodUpgrade])
	}
}

func TestDoctorReportsDuplicateNoPromoteChurn(t *testing.T) {
	home, store := setup(t)
	raw := readTraceFixture(t, "distill-duplicate-memory.json")

	if _, err := Run(home, store, raw, DefaultPolicy(), ReferenceExtractor{}, t0); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if _, err := Run(home, store, raw, DefaultPolicy(), ReferenceExtractor{}, t0); err != nil {
		t.Fatalf("duplicate run: %v", err)
	}

	report, err := Doctor(home)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if report.Status != "attention" {
		t.Fatalf("status = %q, want attention", report.Status)
	}
	if report.MemoryPromoted != 1 || report.MemoryRejected != 1 {
		t.Fatalf("memory promoted/rejected = %d/%d, want 1/1", report.MemoryPromoted, report.MemoryRejected)
	}
	if report.DuplicateMemoryRejections != 1 {
		t.Fatalf("duplicate memory rejections = %d, want 1", report.DuplicateMemoryRejections)
	}
	if report.NoPromoteRows != 1 || report.DuplicateMemoryNoPromoteRows != 1 {
		t.Fatalf("no-promote rows duplicate/total = %d/%d, want 1/1",
			report.DuplicateMemoryNoPromoteRows, report.NoPromoteRows)
	}
}

func TestDoctorWritebackGateForMethodPolicyDebt(t *testing.T) {
	home, store := setup(t)
	raw := readTraceFixture(t, "distill-session.json")

	if _, err := Run(home, store, raw, DefaultPolicy(), ReferenceExtractor{}, t0); err != nil {
		t.Fatalf("run trace fixture: %v", err)
	}
	report, err := Doctor(home)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !NeedsWriteback(report) {
		t.Fatal("NeedsWriteback = false, want true")
	}

	gate := WritebackGate(report, "act_distiller", "pol_distiller", t0)
	if gate.CandidateKind != KindMethodUpgrade {
		t.Fatalf("candidate_kind = %q, want %q", gate.CandidateKind, KindMethodUpgrade)
	}
	if gate.OwnerTarget.Kind != "distillation_policy" || gate.OwnerTarget.Ref != "distillation_policy:default.v1" {
		t.Fatalf("owner target = %+v", gate.OwnerTarget)
	}
	if gate.ActionIntentRef != "act_distiller" || gate.PolicyDecisionRef != "pol_distiller" {
		t.Fatalf("gate action/policy refs = %q/%q", gate.ActionIntentRef, gate.PolicyDecisionRef)
	}
	if !hasString(gate.EvidenceRefs, "distiller:method_upgrades:1") || !hasString(gate.EvidenceRefs, "candidate_kind:"+KindMethodUpgrade) {
		t.Fatalf("evidence refs missing method upgrade markers: %v", gate.EvidenceRefs)
	}
	if gate.CandidateRef == "" || gate.GateBasis == "" {
		t.Fatalf("gate missing candidate ref or basis: %+v", gate)
	}
}

func TestDuplicateChurnDoesNotNeedWriteback(t *testing.T) {
	home, store := setup(t)
	raw := readTraceFixture(t, "distill-duplicate-memory.json")

	if _, err := Run(home, store, raw, DefaultPolicy(), ReferenceExtractor{}, t0); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if _, err := Run(home, store, raw, DefaultPolicy(), ReferenceExtractor{}, t0); err != nil {
		t.Fatalf("duplicate run: %v", err)
	}
	report, err := Doctor(home)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if NeedsWriteback(report) {
		t.Fatal("duplicate churn should be handled by no-promote, not policy writeback")
	}
}

func TestDoctorReportsDuplicateCandidateChurn(t *testing.T) {
	home, store := setup(t)
	first := `{"trace_ref":"t1","candidates":[{"kind":"skill","subject":"paywall-cro","statement":"use this lens","confidence":0.9}]}`
	dupe := `{"trace_ref":"t2","candidates":[{"kind":"skill","subject":"paywall-cro","statement":"use this lens","confidence":0.9}]}`
	if _, err := Run(home, store, []byte(first), DefaultPolicy(), ReferenceExtractor{}, t0); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if _, err := Run(home, store, []byte(dupe), DefaultPolicy(), ReferenceExtractor{}, t0); err != nil {
		t.Fatalf("duplicate run: %v", err)
	}

	report, err := Doctor(home)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if report.DuplicateCandidateRejections != 1 {
		t.Fatalf("duplicate candidate rejections = %d, want 1", report.DuplicateCandidateRejections)
	}
	if report.DuplicateCandidateNoPromoteRows != 1 {
		t.Fatalf("duplicate candidate no-promote rows = %d, want 1", report.DuplicateCandidateNoPromoteRows)
	}
	if NeedsWriteback(report) {
		t.Fatal("duplicate candidate churn should be handled by no-promote, not policy writeback")
	}
}

func readTraceFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "traces", name))
	if err != nil {
		t.Fatalf("read trace fixture %s: %v", name, err)
	}
	return data
}
