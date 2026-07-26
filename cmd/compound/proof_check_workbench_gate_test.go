package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
)

func proofCheckWorkbenchGateStep(t *testing.T) (repo string, step proofCheckStep) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..")), proofCheckStep{
		Name:     "workbench-projection",
		Command:  []string{compoundTestBinary(t), "workbench", "--json"},
		ProofRef: "workbench-projection-refresh",
	}
}

func proofCheckWorkbenchGateHome(t *testing.T, bare bool) string {
	t.Helper()
	dir := t.TempDir()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	if err := home.Init(dir, "proof-check-test", now); err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	if !bare {
		if err := seedProofCheckWorkbenchHome(dir, now); err != nil {
			t.Fatalf("seed fixture: %v", err)
		}
	}
	return dir
}

// TestProofCheckWorkbenchGateProvesSeededCounts is the positive case: the gate owns its
// home, so it must be green with no live COMPOUND_HOME — and green means the projection
// reproduced the seeded ledger, which is more than this gate ever asserted before.
func TestProofCheckWorkbenchGateProvesSeededCounts(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the compound binary")
	}
	repo, step := proofCheckWorkbenchGateStep(t)
	out, err := runProofCheckWorkbenchGate(repo, step)
	if err != nil {
		t.Fatalf("workbench gate failed on its own seeded home: %v\n%s", err, out)
	}
}

// TestProofCheckWorkbenchGateRefusesEmptyHome is the control that makes the isolation safe.
// An unseeded home projects every count as 0 and exits 0 — exactly the shape the gate used
// to accept — so the assertion must refuse it.
func TestProofCheckWorkbenchGateRefusesEmptyHome(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the compound binary")
	}
	repo, step := proofCheckWorkbenchGateStep(t)
	out, err := runProofCheckWorkbenchInHome(repo, proofCheckWorkbenchGateHome(t, true), step)
	if err == nil {
		t.Fatalf("gate passed on an empty home — the exit-code-only check it replaced:\n%s", out)
	}
	if !strings.Contains(err.Error(), "no longer reflects the seeded ledger") {
		t.Fatalf("gate failed for the wrong reason: %v\n%s", err, out)
	}
}

// TestProofCheckWorkbenchGateCatchesSilentlyDroppedEnvelope is the reason this gate is worth
// keeping rather than retiring. (*work.Store).list appends an envelope only when its JSON
// parses and drops it otherwise, so an unreadable envelope does not fail the projection — it
// disappears from it, and every count silently shrinks. The old exit-code check could not see
// that; these assertions can.
func TestProofCheckWorkbenchGateCatchesSilentlyDroppedEnvelope(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the compound binary")
	}
	repo, step := proofCheckWorkbenchGateStep(t)
	seeded := proofCheckWorkbenchGateHome(t, false)
	corruptOneWorkEnvelope(t, seeded)
	out, err := runProofCheckWorkbenchInHome(repo, seeded, step)
	if err == nil {
		t.Fatalf("gate passed while the projection silently dropped an envelope:\n%s", out)
	}
	if !strings.Contains(err.Error(), "want 4/3/1") {
		t.Fatalf("gate failed for the wrong reason: %v\n%s", err, out)
	}
}

// corruptOneWorkEnvelope makes exactly one envelope unreadable. It writes to the store's
// files directly on purpose: there is no public door that produces an unparsable envelope,
// which is precisely why this failure mode survived unnoticed.
func corruptOneWorkEnvelope(t *testing.T, homePath string) {
	t.Helper()
	dir := filepath.Join(homePath, "work", "work")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read work dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), []byte("{ truncated"), 0o644); err != nil {
			t.Fatalf("corrupt envelope: %v", err)
		}
		return
	}
	t.Fatalf("no work envelope found under %s — the fixture did not write one", dir)
}
