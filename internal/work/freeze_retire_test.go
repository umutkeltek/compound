package work

import (
	"strings"
	"testing"
	"time"

	"compound/internal/mem"
)

// Job L1 (2026-11-17): a ruling can retire any decision record. The freeze
// guard, the autonomy grant check and the meaning-directive projection used to
// match `supersedes` only against decision_id; they now share mem's naming
// rule (decision id, claim id or decision:// ref).

func seededDecisionClaimID(t *testing.T, s *Store, decisionID string) string {
	t.Helper()
	memStore, err := mem.Open(s.home)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := memStore.AllCurrent()
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range claims {
		if claim.Qualifiers["decision_id"] == decisionID {
			return claim.ID
		}
	}
	t.Fatalf("no claim for decision %q", decisionID)
	return ""
}

// (d) A freeze retired by its claim id no longer blocks through freezeGuard;
// an unrelated freeze still blocks, and the unrelated autonomy grant stays
// active (AdmitAuto re-validates it from the store).
func TestFreezeRetiredByClaimIDNoLongerBlocks(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	eligible := typedJobHealthEligibility(t, s, fixedNow)
	seedActiveFreeze(t, s, "direction-freeze-2026-07-10", nil, []string{"job_health"}, fixedNow)
	seedActiveFreeze(t, s, "unrelated-freeze", nil, []string{"absorption_debt"}, fixedNow)
	freezeClaimID := seededDecisionClaimID(t, s, "direction-freeze-2026-07-10")
	seedFreezeUndo(t, s, "lift-by-claim-id", freezeClaimID, fixedNow.Add(time.Minute))

	lifted := makeProposedDirectorWork(t, s, "job_health", "work_freeze_claim_lifted", fixedNow)
	res, err := s.AdmitAuto(lifted, eligible, fixedNow)
	if err != nil || !res.Admitted {
		t.Fatalf("freeze retired by claim id must no longer block, and the grant must stay active: err=%v res=%+v", err, res)
	}

	stillFrozen := makeProposedDirectorWork(t, s, "absorption_debt", "work_freeze_unrelated", fixedNow)
	if res, err := s.AdmitAuto(stillFrozen, eligible, fixedNow); err == nil || res.Admitted || !strings.Contains(err.Error(), "unrelated-freeze") {
		t.Fatalf("unrelated freeze must still block: err=%v res=%+v", err, res)
	}
}

// The two-import case for a freeze: an older import (claim id = freeze id, no
// decision_id) and a newer one (decision_id = freeze id) are released together.
func TestActiveFreezeRecordsReleaseEveryImportOfOneRef(t *testing.T) {
	t.Parallel()
	freeze := func(claimID, decisionID string) mem.Claim {
		q := map[string]string{"kind": "freeze", "lifecycle_status": "accepted", "reviewed_by": "operator:test", "freeze_keywords": "absorption_debt"}
		if decisionID != "" {
			q["decision_id"] = decisionID
		}
		return mem.Claim{ID: claimID, ClaimType: "decision", Qualifiers: q}
	}
	lift := mem.Claim{ID: "claim_lift", ClaimType: "decision", Qualifiers: map[string]string{
		"kind": "ruling", "decision_id": "lift", "lifecycle_status": "accepted", "reviewed_by": "operator:test", "supersedes": "legacy-freeze",
		mem.OperationalDecisionAuthorityQualifier: mem.OperationalDecisionAuthorityValue,
	}}
	claims := []mem.Claim{freeze("legacy-freeze", ""), freeze("claim_newer", "legacy-freeze"), freeze("claim_other", "other-freeze"), lift}
	got := activeFreezeRecords(claims)
	if len(got) != 1 || got[0].ID != "other-freeze" {
		t.Fatalf("active freezes = %+v, want only other-freeze", got)
	}
}

// Guard: only freezes join a canonical id, so a generic claim that shares a
// freeze's decision_id cannot link a named id to that freeze.
func TestActiveFreezeRecordsNamedGenericClaimDoesNotReleaseFreeze(t *testing.T) {
	t.Parallel()
	freeze := mem.Claim{ID: "claim_freeze", ClaimType: "decision", Qualifiers: map[string]string{
		"kind": "freeze", "decision_id": "freeze-x", "lifecycle_status": "accepted", "reviewed_by": "operator:test", "freeze_keywords": "absorption_debt",
	}}
	generic := mem.Claim{ID: "claim_generic", ClaimType: "decision", Qualifiers: map[string]string{
		"kind": "ruling", "decision_id": "freeze-x", "lifecycle_status": "accepted",
	}}
	lift := mem.Claim{ID: "claim_lift", ClaimType: "decision", Qualifiers: map[string]string{
		"kind": "ruling", "decision_id": "lift", "lifecycle_status": "accepted", "reviewed_by": "operator:test", "supersedes": "claim_generic",
		mem.OperationalDecisionAuthorityQualifier: mem.OperationalDecisionAuthorityValue,
	}}
	if got := activeFreezeRecords([]mem.Claim{freeze, generic, lift}); len(got) != 1 || got[0].ID != "freeze-x" {
		t.Fatalf("naming a generic claim released the freeze: %+v", got)
	}
}

// An autonomy grant retired by its claim id is no longer active for AdmitAuto.
func TestAutonomyGrantRetiredByClaimIDRefusesAdmission(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	eligible := typedJobHealthEligibility(t, s, fixedNow)
	grantClaimID := seededDecisionClaimID(t, s, "job-health-autonomy-test")
	seedDecisionClaim(t, s, "ruling", "retire-autonomy-grant", map[string]string{"supersedes": grantClaimID}, fixedNow)

	id := makeProposedDirectorWork(t, s, "job_health", "work_grant_retired", fixedNow)
	res, err := s.AdmitAuto(id, eligible, fixedNow)
	if err == nil || res.Admitted || !strings.Contains(err.Error(), "autonomy grant") {
		t.Fatalf("grant retired by claim id must not admit: err=%v res=%+v", err, res)
	}
}

// The meaning-directive projection drops a decision named by claim id.
func TestCurrentActiveMeaningDecisionsRetireByClaimID(t *testing.T) {
	t.Parallel()
	decision := func(claimID, decisionID, supersedes string) mem.Claim {
		return mem.Claim{ID: claimID, ClaimType: "decision", Qualifiers: map[string]string{
			"decision_id": decisionID, "lifecycle_status": "accepted", "supersedes": supersedes,
		}}
	}
	got := currentActiveMeaningDecisions([]mem.Claim{
		decision("claim_a", "a", ""),
		decision("claim_b", "b", ""),
		decision("claim_r", "r", "claim_a"),
	})
	if _, ok := got["decision://a"]; ok {
		t.Fatalf("decision named by claim id is still current: %v", got)
	}
	if _, ok := got["decision://b"]; !ok {
		t.Fatalf("unnamed decision must stay current: %v", got)
	}
}
