package director

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestProposeApplyWritesConfinedToProposalSurfaces is the non-autonomous-class
// boundary expressed as an enforced invariant: this review_debt apply run may
// only touch proposal-preview and run-receipt surfaces. The separate
// job_health success-path boundary test pins the one admitted exception.
// If the director ever gains a new write surface (memory, policy owners,
// config, source/product owners, scheduler state), the observed write set
// escapes the allowlist and this test FAILS.
func TestProposeApplyWritesConfinedToProposalSurfaces(t *testing.T) {
	home := seededDirectorHome(t)
	seedDirectorApplyGrant(t, home, directorTestNow)
	seedReviewDebt(t, home, "synthetic-review-debt", "review-debt-01")

	before := snapshotHomeTree(t, home)

	report, err := Propose(home, Options{Apply: true, Now: directorTestNow})
	if err != nil {
		t.Fatal(err)
	}
	if report.ProposalCount != 1 {
		t.Fatalf("expected one proposal so the create-plan surface is exercised: %+v", report)
	}
	if report.RunWorkID == "" || report.RunReceiptID == "" || report.RunTransitionID == "" {
		t.Fatalf("expected an enclosing run envelope so the run-receipt surface is exercised: %+v", report)
	}

	after := snapshotHomeTree(t, home)
	writeSet := diffHomeTree(before, after)
	if len(writeSet) == 0 {
		t.Fatalf("director --apply produced a proposal but wrote nothing; snapshot logic is wrong")
	}

	// The complete set of prefixes a suggestion-only director is allowed to
	// write, pinned to the observed --apply write set. Every entry is either a
	// proposed create-plan preview, the director run report, or the enclosing
	// run WorkEnvelope's own proof (work snapshot + its ActionIntent /
	// PolicyDecision / Receipt / Transition envelopes and the work event log).
	//
	// It deliberately does NOT include memory (mem/, knowledge/, candidates/),
	// policy owners, config, source / product / review owners, or scheduler
	// state. If the director ever gains such a write surface, a path escapes
	// this allowlist and the test FAILS — that failure is the whole point.
	allowedPrefixes := []string{
		"create-plans/",         // proposed create-plan previews (State:"proposed")
		"projections/director/", // director run report under projections/director/runs/
		"work/work/",            // enclosing run WorkEnvelope snapshot (closed immediately)
		"work/leases/",          // run WorkEnvelope lease lifecycle (part of the same store)
		"work/action-contexts/", // immutable nil/action-context admission snapshot for the run WorkEnvelope
		"events/",               // run ActionIntent/PolicyDecision/Receipt/Transition + work event log
		// DELIBERATE allowlist extension: a harness-edit proposal (7th class)
		// additionally emits ONE propose-only operator review card per proposal
		// here. It is a review artifact that mutates no owner state and admits
		// nothing; the surface is exercised by
		// TestHarnessEditProposeApplyWritesCardConfined.
		"reviews/harness-edit-proposals/",
	}

	var escaped []string
	for _, rel := range writeSet {
		if !hasAllowedPrefix(rel, allowedPrefixes) {
			escaped = append(escaped, rel)
		}
	}
	if len(escaped) > 0 {
		t.Fatalf("director --apply wrote OUTSIDE the suggestion-only allowlist: %v\nfull write set: %v", escaped, writeSet)
	}

	// Pin the shape: the load-bearing proposal + run surfaces must appear, so a
	// future regression that stops emitting proposals or the run receipt can't
	// vacuously pass the allowlist check.
	sawCreatePlan := false
	sawRunReport := false
	sawRunWork := false
	for _, rel := range writeSet {
		switch {
		case strings.HasPrefix(rel, "create-plans/"):
			sawCreatePlan = true
		case strings.HasPrefix(rel, "projections/director/"):
			sawRunReport = true
		case strings.HasPrefix(rel, "work/work/"):
			sawRunWork = true
		}
	}
	if !sawCreatePlan {
		t.Fatalf("expected a create-plan preview write; write set: %v", writeSet)
	}
	if !sawRunReport {
		t.Fatalf("expected a director run report write; write set: %v", writeSet)
	}
	if !sawRunWork {
		t.Fatalf("expected the enclosing run WorkEnvelope write; write set: %v", writeSet)
	}
}

func hasAllowedPrefix(rel string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}

// snapshotHomeTree records every regular file under home as a
// slash-relative path mapped to a content hash, so later diffing detects
// both created and modified files.
func snapshotHomeTree(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(home, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot home tree: %v", err)
	}
	return out
}

// diffHomeTree returns the sorted set of relative paths that were created or
// modified between before and after.
func diffHomeTree(before, after map[string]string) []string {
	var changed []string
	for rel, hash := range after {
		if prior, ok := before[rel]; !ok || prior != hash {
			changed = append(changed, rel)
		}
	}
	sort.Strings(changed)
	return changed
}
