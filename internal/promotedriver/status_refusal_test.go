package promotedriver

import (
	"testing"

	"compound/internal/candidates"
	"compound/internal/mem"
)

// Status never promotes (operator ruling 2026-11-03): a basis-eligible status
// row is refused by the driver and left exactly as it was, while an ordinary
// basis-eligible row in the same pass still promotes.
func TestStatusCandidateWithBasisIsRefusedAndLeftUntouched(t *testing.T) {
	home := t.TempDir()
	seedCandidate(t, home, "cand_status", mem.Candidate{Entity: "repo:example", Predicate: "untracked_count", Object: "3", ClaimType: "status", PromotionBasis: candidates.BasisTrustedDeterministic})
	seedCandidate(t, home, "cand_fact", mem.Candidate{Entity: "proj", Predicate: "language", Object: "go", ClaimType: "architecture", PromotionBasis: candidates.BasisTrustedDeterministic})

	plan, err := Run(home, Options{}, testNow)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Promote.StatusRefused) != 1 || plan.Promote.StatusRefused[0] != "cand_status" {
		t.Fatalf("plan must refuse the status row, got %+v", plan.Promote)
	}
	report, err := Run(home, Options{Apply: true}, testNow)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.Promote.Promoted != 1 || len(report.Promote.StatusRefused) != 1 || report.Promote.StatusRefused[0] != "cand_status" {
		t.Fatalf("want 1 promoted and cand_status refused, got %+v", report.Promote)
	}
	states := candidateStates(t, home)
	if states["cand_fact"] != "promoted" || states["cand_status"] != "proposed" {
		t.Fatalf("states = %v, want fact promoted and status untouched (proposed)", states)
	}
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if timeline, err := store.Timeline("repo:example"); err != nil || len(timeline) != 0 {
		t.Fatalf("status row must not reach the ledger: %v %v", timeline, err)
	}
}
