package decisions

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/mem"
	"compound/internal/writeback"
)

func TestImportDECLifecycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "decisions", "DEC-0001.md")
	writeFile(t, path, `---
id: DEC-0001
title: Use spine memory for decisions
status: accepted
domains: [architecture]
supersedes: [DEC-0000]
falsifier: Decision imports cannot preserve review triggers.
review_trigger: Re-check when decisions become a first-class ledger.
owner: compound
scope: public-core
---

# Use spine memory for decisions
`)

	got, err := ImportDEC(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("ImportDEC got %d candidate(s), want 1", len(got))
	}

	c := got[0]
	l := LifecycleFromCandidate(c)
	if l.ID != "DEC-0001" || l.Kind != KindPolicy || l.Status != StatusAccepted {
		t.Fatalf("lifecycle = %+v", l)
	}
	if l.Scope != "public-core" || l.Owner != "compound" {
		t.Fatalf("scope/owner not preserved: %+v", l)
	}
	if l.Falsifier == "" || l.ReviewTrigger == "" {
		t.Fatalf("missing lifecycle proof fields: %+v", l)
	}
	if len(l.Supersedes) != 1 || l.Supersedes[0] != "DEC-0000" {
		t.Fatalf("supersedes = %v", l.Supersedes)
	}
	if c.PromotionBasis != candidates.BasisDecisionRecord {
		t.Fatalf("promotion basis = %q", c.PromotionBasis)
	}
	if missing := MissingRequired(l); len(missing) != 0 {
		t.Fatalf("MissingRequired = %v", missing)
	}
}

func TestImportBrainDecisionRecordsEvidenceAndDecision(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "brain", "operating-system", "decision.md")
	writeFile(t, path, `---
id: brain-dec-0001
title: Receipts are proof
kind: doctrine
status: accepted
domains: [governance]
falsifier: Agents can claim success without a state delta.
review_trigger: Revisit when receipts stop changing future runs.
owner: compound
scope: public-core
---

# Receipts are proof

Every non-trivial run leaves evidence before it can be treated as done.
`)
	report, err := ImportBrainDecision(store, path, time.Date(2026, 6, 29, 18, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report.EvidenceID == "" || report.CandidateID == "" || report.ClaimID == "" || report.Verdict != candidates.Promote {
		t.Fatalf("report = %+v", report)
	}
	ev, ok, err := store.GetEvidence(report.EvidenceID)
	if err != nil || !ok {
		t.Fatalf("evidence lookup ok=%v err=%v", ok, err)
	}
	if ev.SourceKind != "note" || ev.SourceID == "" || ev.QuoteText == "" {
		t.Fatalf("evidence = %+v", ev)
	}
	claims, err := store.AllCurrent()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 {
		t.Fatalf("claims = %+v", claims)
	}
	l := LifecycleFromClaim(claims[0])
	if l.ID != "brain-dec-0001" || l.Kind != KindDoctrine || l.Status != StatusAccepted {
		t.Fatalf("lifecycle = %+v", l)
	}
	if l.Falsifier == "" || l.ReviewTrigger == "" || len(l.EvidenceRefs) < 2 || l.EvidenceRefs[1] != report.EvidenceID {
		t.Fatalf("lifecycle evidence/proof fields = %+v", l)
	}
}

func TestImportOperatorLedgerLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operator.md")
	writeFile(t, path, `### O2 — Flip PlacementGuard

Falsifier: Blocking breaks legitimate generated-state writes.
Review trigger: Re-check after three warn-only violations.
`)

	got, err := ImportOperatorLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("ImportOperatorLedger got %d candidate(s), want 1", len(got))
	}

	c := got[0]
	l := LifecycleFromCandidate(c)
	if l.ID != "O2" || l.Kind != KindDoctrine || l.Status != StatusProposed {
		t.Fatalf("lifecycle = %+v", l)
	}
	if c.PromotionBasis != "" {
		t.Fatalf("open operator decision should not auto-promote, got %q", c.PromotionBasis)
	}
	if missing := MissingRequired(l); len(missing) != 0 {
		t.Fatalf("MissingRequired = %v", missing)
	}
}

func TestMissingRequiredLifecycleFields(t *testing.T) {
	missing := MissingRequired(Lifecycle{Status: StatusAccepted, ReviewTrigger: "on drift"})
	if len(missing) != 1 || missing[0] != "falsifier" {
		t.Fatalf("MissingRequired = %v", missing)
	}
}

func TestDoctorReportFromClaims(t *testing.T) {
	claims := []mem.Claim{
		{
			ID:           "claim_decision_1",
			ClaimType:    "decision",
			Object:       "Use governed writeback",
			Qualifiers:   lifecycleQualifiers(KindPolicy, "decisions-repo", "DEC-0001", StatusAccepted),
			EvidenceRefs: []string{"evidence:dec-0001"},
		},
		{
			ID:        "claim_memory_1",
			ClaimType: "memory",
			Object:    "ignored",
		},
	}

	report := DoctorReportFromClaims(claims)
	if report.Total != 1 {
		t.Fatalf("Total = %d, want 1", report.Total)
	}
	if report.Counts[KindPolicy] != 1 || report.Statuses[StatusAccepted] != 1 {
		t.Fatalf("counts/statuses = %+v %+v", report.Counts, report.Statuses)
	}
	if report.MissingLifecycleFields != 1 || len(report.LifecycleGaps) != 1 {
		t.Fatalf("gaps = %+v", report)
	}
	gap := report.LifecycleGaps[0]
	if gap.ClaimID != "claim_decision_1" || gap.DecisionID != "DEC-0001" {
		t.Fatalf("gap identity = %+v", gap)
	}
	if len(gap.Missing) != 2 {
		t.Fatalf("gap missing = %v", gap.Missing)
	}
	if !NeedsWriteback(report) {
		t.Fatal("NeedsWriteback = false, want true")
	}
}

func TestDecisionWritebackGate(t *testing.T) {
	report := DoctorReport{
		Total:                  1,
		Counts:                 map[string]int{KindPolicy: 1},
		Statuses:               map[string]int{StatusAccepted: 1},
		MissingLifecycleFields: 1,
		LifecycleGaps: []LifecycleGap{
			{
				ClaimID:    "claim_decision_1",
				DecisionID: "DEC-0001",
				Kind:       KindPolicy,
				Status:     StatusAccepted,
				Missing:    []string{"falsifier"},
			},
		},
	}
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

	gate := WritebackGate(report, "act_1", "pol_1", now)
	if gate.SchemaVersion != writeback.GateSchemaVersion {
		t.Fatalf("schema = %q", gate.SchemaVersion)
	}
	wantRef := "decisions:lifecycle_gaps:1:" + lifecycleGapDigest(report)
	if gate.CandidateRef != wantRef {
		t.Fatalf("candidate_ref = %q", gate.CandidateRef)
	}
	if gate.CandidateKind != string(candidates.Decision) {
		t.Fatalf("candidate_kind = %q", gate.CandidateKind)
	}
	if gate.OwnerTarget.Kind != "decision_lifecycle" || gate.OwnerTarget.Ref == "" {
		t.Fatalf("owner target = %+v", gate.OwnerTarget)
	}
	if len(gate.EvidenceRefs) < 3 {
		t.Fatalf("evidence refs = %v", gate.EvidenceRefs)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
