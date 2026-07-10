package decisions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/mem"
)

// TestRecordRequiresReviewedBy is falsifier d (664e1a7 parity): `decisions
// record` refuses without --reviewed-by, and writes nothing to disk.
func TestRecordRequiresReviewedBy(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Record(store, home, RecordInput{
		Kind:  KindGrant,
		Title: "Grant director apply",
		Scope: "director-apply",
	})
	if err == nil {
		t.Fatal("record without --reviewed-by must be refused")
	}
	if !strings.Contains(err.Error(), "reviewed-by") && !strings.Contains(err.Error(), "reviewed_by") {
		t.Fatalf("refusal must name reviewed-by, got %v", err)
	}
	entries, readErr := os.ReadDir(filepath.Join(home, "decisions", "operator"))
	if readErr == nil && len(entries) != 0 {
		t.Fatalf("no file should be written on refusal, found %v", entries)
	}
}

// TestRecordRequiresKnownKind proves --kind is restricted to grant|freeze|ruling.
func TestRecordRequiresKnownKind(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Record(store, home, RecordInput{
		Kind:       "policy",
		Title:      "Not a grant/freeze/ruling",
		ReviewedBy: "umut",
	})
	if err == nil {
		t.Fatal("record with an unknown --kind must be refused")
	}
}

// TestRecordWritesUnderOperatorDirAndImportsThroughDecisionsStore is the
// headline Slice 1 proof: a grant record lands under
// $COMPOUND_HOME/decisions/operator/ (never the repo, never ~/.compound —
// tests use temp homes only) and is imported through the SAME
// ImportPolicyDecision path every other policy decision uses, landing in the
// store with real lifecycle qualifiers (kind, status, scope, reviewed_by).
func TestRecordWritesUnderOperatorDirAndImportsThroughDecisionsStore(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	report, err := Record(store, home, RecordInput{
		Kind:       KindGrant,
		ID:         "director-apply-grant-2026-07-10",
		Title:      "Grant director --apply",
		ReviewedBy: "umut",
		Scope:      "director-apply",
		Now:        now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(report.Path, filepath.Join(home, "decisions", "operator")) {
		t.Fatalf("record path = %q, want under decisions/operator/", report.Path)
	}
	if _, statErr := os.Stat(report.Path); statErr != nil {
		t.Fatalf("record file not written: %v", statErr)
	}
	if report.Import.ClaimID == "" {
		t.Fatalf("grant record should promote to a current claim: %+v", report.Import)
	}
	l := report.Import.Lifecycle
	if l.ID != "director-apply-grant-2026-07-10" || l.Kind != KindGrant || l.Status != StatusAccepted {
		t.Fatalf("lifecycle = %+v", l)
	}
	if l.Scope != "director-apply" || l.ReviewedBy != "umut" {
		t.Fatalf("scope/reviewed_by not preserved: %+v", l)
	}
	if l.Falsifier == "" || l.ReviewTrigger == "" {
		t.Fatalf("record must synthesize lifecycle proof so it can promote: %+v", l)
	}
	claims, err := store.AllCurrent()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range claims {
		if c.ID == report.Import.ClaimID {
			found = true
		}
	}
	if !found {
		t.Fatalf("claim %s not visible via AllCurrent()", report.Import.ClaimID)
	}
}

// TestRecordFreezeScopeQualifiers proves a freeze record's explicit scope
// (owner globs / predicate keywords) round-trips through the store.
func TestRecordFreezeScopeQualifiers(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Record(store, home, RecordInput{
		Kind:       KindFreeze,
		ID:         "direction-freeze-2026-07-10",
		Title:      "Freeze new-organ/absorption imports",
		ReviewedBy: "umut",
		OwnerGlobs: []string{"compound-authority"},
		Keywords:   []string{"absorption_debt", "harness_edit"},
		Now:        time.Date(2026, 7, 10, 12, 5, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	l := report.Import.Lifecycle
	if len(l.FreezeOwnerGlobs) != 1 || l.FreezeOwnerGlobs[0] != "compound-authority" {
		t.Fatalf("freeze_owner_globs = %v", l.FreezeOwnerGlobs)
	}
	if len(l.FreezeKeywords) != 2 {
		t.Fatalf("freeze_keywords = %v", l.FreezeKeywords)
	}
}

// TestRecordIdempotentReplayReusesClaim is falsifier f: re-recording the same
// grant (same id, same content) is a no-op — never a duplicate active record.
func TestRecordIdempotentReplayReusesClaim(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	in := RecordInput{
		Kind:       KindGrant,
		ID:         "director-apply-grant-2026-07-10",
		Title:      "Grant director --apply",
		ReviewedBy: "umut",
		Scope:      "director-apply",
		Now:        time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
	}
	first, err := Record(store, home, in)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.AllCandidates()
	if err != nil {
		t.Fatal(err)
	}
	in.Now = time.Date(2026, 7, 10, 12, 1, 0, 0, time.UTC)
	second, err := Record(store, home, in)
	if err != nil {
		t.Fatal(err)
	}
	if second.Import.ClaimID != first.Import.ClaimID {
		t.Fatalf("idempotent replay should reuse the claim: first=%s second=%s", first.Import.ClaimID, second.Import.ClaimID)
	}
	after, err := store.AllCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("idempotent replay created a duplicate candidate: before=%d after=%d", len(before), len(after))
	}
}

// TestRecordUndoOfSupersedesPriorGrant is falsifier c: an --undo-of record
// supersedes the prior grant/freeze; ActiveDecisions no longer returns it,
// but the prior claim stays in history (never deleted).
func TestRecordUndoOfSupersedesPriorGrant(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := Record(store, home, RecordInput{
		Kind:       KindGrant,
		ID:         "director-apply-grant-2026-07-10",
		Title:      "Grant director --apply",
		ReviewedBy: "umut",
		Scope:      "director-apply",
		Now:        time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := store.AllCurrent()
	if err != nil {
		t.Fatal(err)
	}
	active := ActiveDecisions(claims, KindGrant)
	if len(active) != 1 || LifecycleFromClaim(active[0]).ID != "director-apply-grant-2026-07-10" {
		t.Fatalf("grant should be active before undo: %+v", active)
	}

	undo, err := Record(store, home, RecordInput{
		Kind:       KindGrant,
		ID:         "director-apply-grant-2026-07-10-revoke",
		Title:      "Revoke director --apply grant",
		ReviewedBy: "umut",
		UndoOf:     "director-apply-grant-2026-07-10",
		Now:        time.Date(2026, 7, 10, 13, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if undo.Import.Lifecycle.Supersedes[0] != "director-apply-grant-2026-07-10" {
		t.Fatalf("undo record should carry supersedes: %+v", undo.Import.Lifecycle)
	}

	claimsAfter, err := store.AllCurrent()
	if err != nil {
		t.Fatal(err)
	}
	activeAfter := ActiveDecisions(claimsAfter, KindGrant)
	for _, c := range activeAfter {
		if LifecycleFromClaim(c).ID == "director-apply-grant-2026-07-10" {
			t.Fatalf("undo must flip the prior grant inactive: %+v", activeAfter)
		}
	}
	// Falsifier c: prior record stays in history — its claim is not deleted.
	foundPrior := false
	for _, c := range claimsAfter {
		if c.ID == grant.Import.ClaimID {
			foundPrior = true
		}
	}
	if !foundPrior {
		t.Fatalf("prior grant claim %s must stay in history", grant.Import.ClaimID)
	}
}

// TestActiveDecisionsFiltersKindStatusAndSupersession is the unit-level proof
// for the read helper both the director grant consumer and the freeze admit
// guard share.
func TestActiveDecisionsFiltersKindStatusAndSupersession(t *testing.T) {
	claims := []mem.Claim{
		{
			ID:         "claim_grant_active",
			ClaimType:  "decision",
			Qualifiers: lifecycleQualifiers(KindGrant, "decisions-repo", "grant-active", StatusAccepted),
		},
		{
			ID:         "claim_grant_superseded",
			ClaimType:  "decision",
			Qualifiers: lifecycleQualifiers(KindGrant, "decisions-repo", "grant-old", StatusAccepted),
		},
		{
			ID:         "claim_grant_undo",
			ClaimType:  "decision",
			Qualifiers: mapWithSupersedes(lifecycleQualifiers(KindGrant, "decisions-repo", "grant-undo", StatusAccepted), "grant-old"),
		},
		{
			ID:         "claim_freeze_active",
			ClaimType:  "decision",
			Qualifiers: lifecycleQualifiers(KindFreeze, "decisions-repo", "freeze-active", StatusAccepted),
		},
		{
			ID:        "claim_memory",
			ClaimType: "memory",
		},
	}
	active := ActiveDecisions(claims, KindGrant)
	if len(active) != 2 {
		t.Fatalf("active grants = %d, want 2 (grant-active, grant-undo): %+v", len(active), active)
	}
	ids := map[string]bool{}
	for _, c := range active {
		ids[LifecycleFromClaim(c).ID] = true
	}
	if !ids["grant-active"] || !ids["grant-undo"] || ids["grant-old"] {
		t.Fatalf("active grant ids = %v", ids)
	}
	freezes := ActiveDecisions(claims, KindFreeze)
	if len(freezes) != 1 || LifecycleFromClaim(freezes[0]).ID != "freeze-active" {
		t.Fatalf("active freezes = %+v", freezes)
	}

	for _, status := range []string{StatusProposed, StatusRejected, StatusDeprecated, StatusSuperseded} {
		t.Run("non-accepted superseder "+status, func(t *testing.T) {
			withInactiveSuperseder := []mem.Claim{
				{
					ID:         "claim_prior_" + status,
					ClaimType:  "decision",
					Qualifiers: lifecycleQualifiers(KindGrant, "decisions-repo", "grant-prior-"+status, StatusAccepted),
				},
				{
					ID:        "claim_inactive_superseder_" + status,
					ClaimType: "decision",
					Qualifiers: mapWithSupersedes(
						lifecycleQualifiers(KindGrant, "decisions-repo", "grant-inactive-"+status, status),
						"grant-prior-"+status,
					),
				},
			}
			got := ActiveDecisions(withInactiveSuperseder, KindGrant)
			if len(got) != 1 || LifecycleFromClaim(got[0]).ID != "grant-prior-"+status {
				t.Fatalf("%s superseder deactivated accepted predecessor: %+v", status, got)
			}
		})
	}
}

func mapWithSupersedes(q map[string]string, ids ...string) map[string]string {
	out := cloneStringMap(q)
	out["supersedes"] = strings.Join(ids, ",")
	return out
}
