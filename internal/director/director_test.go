package director

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/reviewartifact"
	"compound/internal/routeevidence"
	"compound/internal/work"
)

var directorTestNow = time.Date(2026, 7, 6, 9, 30, 0, 0, time.UTC)

func TestBuildProposalRequiresEvidenceRefs(t *testing.T) {
	home := seededDirectorHome(t)
	store, err := work.Open(home)
	if err != nil {
		t.Fatal(err)
	}

	proposal, skipped := buildProposal(home, store, candidate{
		Kind:             "review_debt",
		SourceID:         "synthetic-review",
		Title:            "Resolve synthetic review debt",
		Consequence:      "Adding this creates bounded synthetic review work",
		CapabilityClass:  "judge-critique",
		Owner:            "compound-tests",
		SuccessPredicate: "resolve synthetic review debt",
	}, directorTestNow)
	if skipped == nil || skipped.Reason != "missing_evidence_refs" {
		t.Fatalf("buildProposal skip = %+v, want missing_evidence_refs", skipped)
	}
	if proposal.ID != "" {
		t.Fatalf("proposal should not be built without evidence: %+v", proposal)
	}
}

func TestProposeDedupesPriorDirectorProposal(t *testing.T) {
	home := seededDirectorHome(t)
	seedReviewDebt(t, home, "synthetic-review-debt", "review-debt-01")

	first, err := Propose(home, Options{Apply: true, Now: directorTestNow})
	if err != nil {
		t.Fatal(err)
	}
	if first.ProposalCount != 1 || first.Proposals[0].PlanPath == "" || first.Proposals[0].WorkState != "proposed" {
		t.Fatalf("first director apply should record one proposed plan: %+v", first)
	}
	assertHasRef(t, first.Proposals[0].EvidenceRefs, "proposed_by:director")
	assertHasPrefix(t, first.Proposals[0].EvidenceRefs, "director_trigger:")
	assertHasPrefix(t, first.Proposals[0].EvidenceRefs, "route_plan:")
	assertHasRef(t, first.Proposals[0].EvidenceRefs, "review:synthetic-review-debt/review-debt-01")
	if !strings.HasPrefix(first.Proposals[0].Consequence, "Adding this creates ") {
		t.Fatalf("proposal consequence should name add effect: %q", first.Proposals[0].Consequence)
	}
	if len(first.Proposals[0].RoutePlan.Chain) == 0 {
		t.Fatalf("proposal missing suggested route chain: %+v", first.Proposals[0].RoutePlan)
	}
	store, err := work.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(first.Proposals[0].WorkID); err == nil {
		t.Fatalf("director apply must record a create-plan preview, not create work %s", first.Proposals[0].WorkID)
	}

	second, err := Propose(home, Options{Apply: true, Now: directorTestNow.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if second.ProposalCount != 0 || second.SuppressedDuplicateCount != 1 {
		t.Fatalf("second apply should suppress prior director proposal: %+v", second)
	}
	dup := second.SuppressedDuplicates[0]
	if dup.Reason != "prior_director_proposal" || len(dup.ExistingPlanIDs) == 0 || len(dup.EvidenceRefs) == 0 {
		t.Fatalf("prior proposal duplicate should carry plan ids and evidence refs: %+v", dup)
	}
}

func TestProposeDedupesOpenWork(t *testing.T) {
	home := seededDirectorHome(t)
	seedReviewDebt(t, home, "synthetic-review-debt", "review-debt-01")

	dryRun, err := Propose(home, Options{Now: directorTestNow})
	if err != nil {
		t.Fatal(err)
	}
	if dryRun.ProposalCount != 1 {
		t.Fatalf("dry run should expose proposal before open-work duplicate: %+v", dryRun)
	}

	store, err := work.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(work.CreateInput{
		Owner:            "compound-tests",
		Actor:            "operator-test",
		SuccessPredicate: "existing open work blocks duplicate director proposal",
		EvidenceRefs:     []string{dryRun.Proposals[0].TriggerRef},
		State:            "ready",
	}, directorTestNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	afterOpenWork, err := Propose(home, Options{Now: directorTestNow.Add(2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if afterOpenWork.ProposalCount != 0 || afterOpenWork.SuppressedDuplicateCount != 1 {
		t.Fatalf("open work should suppress proposal: %+v", afterOpenWork)
	}
	dup := afterOpenWork.SuppressedDuplicates[0]
	if dup.Reason != "open_work" || len(dup.ExistingWorkIDs) == 0 || len(dup.EvidenceRefs) == 0 {
		t.Fatalf("open-work duplicate should carry work ids and evidence refs: %+v", dup)
	}
}

func TestProposeCapsRunAtFive(t *testing.T) {
	home := seededDirectorHome(t)
	for i := 0; i < 7; i++ {
		seedReviewDebt(t, home, "synthetic-review-debt", "review-debt-cap-"+string(rune('a'+i)))
	}

	report, err := Propose(home, Options{Now: directorTestNow})
	if err != nil {
		t.Fatal(err)
	}
	if report.ProposalCount != 5 || report.SkippedCount != 2 {
		t.Fatalf("director run should cap proposals at five: %+v", report)
	}
	for _, skipped := range report.Skipped {
		if skipped.Reason != "cap_reached" || len(skipped.EvidenceRefs) == 0 {
			t.Fatalf("cap skip should preserve evidence refs: %+v", skipped)
		}
	}
}

func TestDirectorProposalsCannotAdvancePastProposedWithoutOperator(t *testing.T) {
	home := seededDirectorHome(t)
	seedReviewDebt(t, home, "synthetic-review-debt", "review-debt-01")

	report, err := Propose(home, Options{Apply: true, Now: directorTestNow})
	if err != nil {
		t.Fatal(err)
	}
	if report.ProposalCount != 1 {
		t.Fatalf("director apply should record one proposal: %+v", report)
	}
	store, err := work.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := store.AdmitCreatePlanPath(report.Proposals[0].PlanPath, directorTestNow.Add(time.Minute), work.AdmitCreatePlanOptions{Actor: "operator-test"})
	if err != nil {
		t.Fatal(err)
	}
	if admitted.WorkState != "proposed" {
		t.Fatalf("admitted director work should remain proposed: %+v", admitted)
	}

	plan, err := store.AdvancePlan(admitted.WorkID, directorTestNow.Add(2*time.Minute), work.AdvancePlanOptions{Actor: "codex", To: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status == "advance_policy_allowed" || plan.NoAction == "" || !strings.Contains(plan.NoAction, "operator provenance") {
		t.Fatalf("non-operator advance plan should be blocked by provenance: %+v", plan)
	}
	if _, err := store.Advance(admitted.WorkID, work.AdvanceInput{Actor: "codex", To: "ready"}, directorTestNow.Add(3*time.Minute)); err == nil || !strings.Contains(err.Error(), "operator provenance") {
		t.Fatalf("non-operator advance should fail on operator provenance, got %v", err)
	}
	after, err := store.Get(admitted.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "proposed" {
		t.Fatalf("failed non-operator advance mutated director work: %+v", after)
	}

	if _, err := store.Advance(admitted.WorkID, work.AdvanceInput{Actor: "operator-test", To: "ready"}, directorTestNow.Add(4*time.Minute)); err != nil {
		t.Fatalf("operator provenance should be able to release proposed director work: %v", err)
	}
}

func seededDirectorHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	source := filepath.Join("..", "..", "fixtures", "route", "model-capabilities.yaml")
	if _, err := routeevidence.ImportModelCapabilitiesYAML(home, source, directorTestNow); err != nil {
		t.Fatalf("import route fixture: %v", err)
	}
	return home
}

func seedReviewDebt(t *testing.T, home, area, id string) {
	t.Helper()
	payload := map[string]any{
		"id":            id,
		"status":        "review_required",
		"owner":         "compound-tests",
		"evidence_refs": []string{"synthetic:evidence:" + id},
		"next_action":   "decide bounded synthetic follow-up",
	}
	if _, err := reviewartifact.Write(home, area, id, payload, "  ", "synthetic review debt\n"); err != nil {
		t.Fatalf("write review debt: %v", err)
	}
}

func assertHasRef(t *testing.T, refs []string, want string) {
	t.Helper()
	for _, ref := range refs {
		if ref == want {
			return
		}
	}
	t.Fatalf("refs missing %q: %v", want, refs)
}

func assertHasPrefix(t *testing.T, refs []string, wantPrefix string) {
	t.Helper()
	for _, ref := range refs {
		if strings.HasPrefix(ref, wantPrefix) {
			return
		}
	}
	t.Fatalf("refs missing prefix %q: %v", wantPrefix, refs)
}
