package decisions

import (
	"os"
	"path/filepath"
	"testing"

	"compound/internal/candidates"
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

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
