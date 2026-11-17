package decisions

import (
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/mem"
)

// Job L1 (2026-11-17): a ruling can retire any decision record. Before this
// change AllActiveDecisions matched a `supersedes` entry only against the
// decision_id qualifier, so a claim without one could never retire, and the
// older import of a DEC-00xx page (claim id DEC-0003, no decision_id) came
// back under the same canonical ref once the newer import was retired.

func legacyDecisionClaim(id string, seq int64, object string) mem.Claim {
	return mem.Claim{
		ID:         id,
		Seq:        seq,
		ClaimType:  "decision",
		Object:     object,
		Qualifiers: map[string]string{"kind": KindPolicy, "source": "decisions/" + id + ".md"},
	}
}

func importedDecisionClaim(claimID, decisionID string, seq int64, object string) mem.Claim {
	q := lifecycleQualifiers(KindPolicy, "decisions-repo", decisionID, StatusAccepted)
	return mem.Claim{ID: claimID, Seq: seq, ClaimType: "decision", Object: object, Qualifiers: q}
}

func retiringRuling(claimID, decisionID string, seq int64, names ...string) mem.Claim {
	q := mapWithSupersedes(lifecycleQualifiers(KindRuling, "decisions-repo", decisionID, StatusAccepted), names...)
	return mem.Claim{ID: claimID, Seq: seq, ClaimType: "decision", Object: "retire " + strings.Join(names, ","), Qualifiers: q}
}

// twoImportLedger mirrors the real ledger: every DEC-00xx page exists as an
// older legacy claim (claim id = DEC id, no decision_id) and a newer import
// (claim_..., decision_id = DEC id).
func twoImportLedger() []mem.Claim {
	return []mem.Claim{
		legacyDecisionClaim("DEC-0001", 10, "Keep decision one"),
		legacyDecisionClaim("DEC-0003", 11, "Retire decision three"),
		importedDecisionClaim("claim_dec1_new", "DEC-0001", 200, "Keep decision one"),
		importedDecisionClaim("claim_dec3_new", "DEC-0003", 201, "Retire decision three"),
	}
}

func activeRefs(claims []mem.Claim) map[string]int {
	out := map[string]int{}
	for _, c := range AllActiveDecisions(claims) {
		out[CanonicalRef(c)]++
	}
	return out
}

// (a) The two-import DEC case: naming the decision in any form retires both
// imports, so `decisions show DEC-0003` no longer finds a record.
func TestRetireCoversEveryImportOfOneCanonicalRef(t *testing.T) {
	if _, _, ok := ResolveActive(twoImportLedger(), "DEC-0003"); !ok {
		t.Fatal("fixture: DEC-0003 must be active before any record retires it")
	}
	for _, name := range []string{"DEC-0003", "decision://DEC-0003", "claim_dec3_new"} {
		t.Run(name, func(t *testing.T) {
			claims := append(twoImportLedger(), retiringRuling("claim_retire", "retire-batch", 300, name))
			refs := activeRefs(claims)
			if refs["decision://DEC-0003"] != 0 {
				t.Fatalf("supersedes %q left an import of DEC-0003 active: %v", name, refs)
			}
			if refs["decision://DEC-0001"] != 2 || refs["decision://retire-batch"] != 1 {
				t.Fatalf("supersedes %q retired a record it does not name: %v", name, refs)
			}
			for _, query := range []string{"DEC-0003", "decision://DEC-0003", "claim_dec3_new"} {
				if claim, ref, ok := ResolveActive(claims, query); ok {
					t.Fatalf("ResolveActive(%q) found retired %s (%s)", query, ref, claim.ID)
				}
			}
			claim, ref, ok := ResolveActive(claims, "DEC-0001")
			if !ok || ref != "decision://DEC-0001" || claim.ID != "claim_dec1_new" {
				t.Fatalf("ResolveActive(DEC-0001) = %s %s %v, want newest import of the unnamed decision", claim.ID, ref, ok)
			}
		})
	}
}

// (b) A claim with no decision_id (knowledge_page, brain-D*, some brain-ledger
// records) retires when a record names its claim id; its unnamed sibling stays.
func TestRetireByClaimIDWithoutDecisionID(t *testing.T) {
	page := func(id string) mem.Claim {
		return mem.Claim{ID: id, Seq: 50, ClaimType: "decision", Object: "page " + id,
			Qualifiers: map[string]string{"kind": KindPolicy, "source": "knowledge_page", "lifecycle_status": StatusAccepted}}
	}
	claims := []mem.Claim{page("claim_brain_d4"), page("claim_brain_d5"), retiringRuling("claim_retire", "retire-d4", 60, "claim_brain_d4")}
	refs := activeRefs(claims)
	if refs["decision://claim_brain_d4"] != 0 {
		t.Fatalf("claim without decision_id stayed active after a record named its claim id: %v", refs)
	}
	if refs["decision://claim_brain_d5"] != 1 || refs["decision://retire-d4"] != 1 {
		t.Fatalf("unnamed records must stay active: %v", refs)
	}
	if _, _, ok := ResolveActive(claims, "claim_brain_d4"); ok {
		t.Fatal("decisions show still finds the retired claim")
	}
}

// (c) One --undo-of carrying a comma list retires every decision it names,
// through the real record path: a decision id, the claim id of a claim with
// no decision_id, and a decision:// ref. The unnamed record stays.
func TestRetireCommaListInOneUndoOf(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	record := func(in RecordInput) {
		t.Helper()
		in.ReviewedBy = "operator:test"
		in.Now = now
		now = now.Add(time.Minute)
		if _, err := Record(store, home, in); err != nil {
			t.Fatal(err)
		}
	}
	record(RecordInput{Kind: KindGrant, ID: "grant-a", Title: "Grant A", Scope: "director-apply"})
	pageClaimID := promoteDecisionWithoutID(t, store, "Knowledge page decision", now)
	record(RecordInput{Kind: KindRuling, ID: "ruling-c", Title: "Ruling C"})
	record(RecordInput{Kind: KindRuling, ID: "keep-d", Title: "Unrelated ruling D"})
	record(RecordInput{Kind: KindRuling, ID: "retire-batch", Title: "Retire A, the page and C",
		UndoOf: "grant-a, " + pageClaimID + " ,decision://ruling-c"})

	for _, query := range []string{"grant-a", pageClaimID, "ruling-c"} {
		if rec, err := LoadActive(store, query); err == nil {
			t.Fatalf("LoadActive(%q) found %s after the comma list named it", query, rec.Ref)
		}
	}
	for _, query := range []string{"keep-d", "retire-batch"} {
		if _, err := LoadActive(store, query); err != nil {
			t.Fatalf("LoadActive(%q): %v (an unnamed record must stay active)", query, err)
		}
	}
}

// Goal 3, unchanged: a retired record does not come back when the record that
// retired it is itself retired — every accepted record's list keeps counting.
// The decision-id case held before this change and must keep holding; the
// claim-id case applies the same rule to the new naming forms.
func TestRetiredRecordStaysRetiredWhenItsRetirerIsRetired(t *testing.T) {
	for name, firstRetirer := range map[string][]string{
		"by decision id (rule unchanged)": {"a", "b"},
		"by claim id and legacy id":       {"claim_a", "LEGACY-B"},
	} {
		t.Run(name, func(t *testing.T) {
			b := legacyDecisionClaim("LEGACY-B", 2, "B")
			if firstRetirer[1] == "b" {
				b = importedDecisionClaim("claim_b", "b", 2, "B")
			}
			claims := []mem.Claim{
				importedDecisionClaim("claim_a", "a", 1, "A"),
				b,
				retiringRuling("claim_r1", "r1", 3, firstRetirer...),
				retiringRuling("claim_r2", "r2", 4, "r1"),
			}
			refs := activeRefs(claims)
			if len(refs) != 1 || refs["decision://r2"] != 1 {
				t.Fatalf("records came back when their retirer was retired: want only r2 active, got %v", refs)
			}
		})
	}
}

// unnamedRetireLedger holds records that nothing retires: an entry that names
// no decision, a record that is not accepted, and a non-decision claim.
func unnamedRetireLedger() []mem.Claim {
	return []mem.Claim{
		importedDecisionClaim("claim_x", "x", 1, "X"),
		legacyDecisionClaim("LEGACY-Y", 2, "Y"),
		{ID: "claim_z", Seq: 3, ClaimType: "decision", Object: "Z",
			Qualifiers: map[string]string{"kind": KindPolicy, "source": "knowledge_page"}},
		retiringRuling("claim_s", "s", 4, "decision://", " ", "unknown-id", "decision://unknown"),
		{ID: "claim_p", Seq: 5, ClaimType: "decision", Object: "proposed retirer",
			Qualifiers: mapWithSupersedes(lifecycleQualifiers(KindRuling, "decisions-repo", "p", StatusProposed), "x", "LEGACY-Y", "claim_z")},
		{ID: "claim_mem", Seq: 6, ClaimType: "memory", Object: "not a decision",
			Qualifiers: map[string]string{"supersedes": "x,LEGACY-Y,claim_z"}},
	}
}

// Guard: an entry that names no decision retires nothing, and a record that is
// not accepted never retires anything (also by claim id).
func TestRetireNamesNothingRetiresNothing(t *testing.T) {
	refs := activeRefs(unnamedRetireLedger())
	for _, ref := range []string{"decision://x", "decision://LEGACY-Y", "decision://claim_z", "decision://s"} {
		if refs[ref] != 1 {
			t.Fatalf("%s retired although no accepted record names it: %v", ref, refs)
		}
	}
}

// (d) A freeze retired by its claim id stops being active for every freeze
// reader in this package, and an unrelated grant stays active.
func TestRetireFreezeByClaimIDKeepsUnrelatedGrant(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	for _, in := range []RecordInput{
		{Kind: KindFreeze, ID: "freeze-x", Title: "Freeze absorption work", Keywords: []string{"absorption_debt"}},
		{Kind: KindGrant, ID: "grant-y", Title: "Let the director apply", Scope: "director-apply"},
	} {
		in.ReviewedBy, in.Now = "operator:test", now
		now = now.Add(time.Minute)
		if _, err := Record(store, home, in); err != nil {
			t.Fatal(err)
		}
	}
	freezeClaimID := activeClaimIDForDecision(t, store, "freeze-x")
	if _, err := Record(store, home, RecordInput{Kind: KindRuling, ID: "lift-freeze-x", Title: "Lift the freeze",
		ReviewedBy: "operator:test", UndoOf: freezeClaimID, Now: now}); err != nil {
		t.Fatal(err)
	}
	claims, err := store.AllCurrentOperationalDecisions()
	if err != nil {
		t.Fatal(err)
	}
	if got := ActiveDecisions(claims, KindFreeze); len(got) != 0 {
		t.Fatalf("freeze retired by claim id is still active: %+v", got)
	}
	if got := AuthoritativeActiveDecisions(claims, KindFreeze); len(got) != 0 {
		t.Fatalf("freeze retired by claim id is still authoritative: %+v", got)
	}
	for name, got := range map[string][]mem.Claim{
		"ActiveDecisions":              ActiveDecisions(claims, KindGrant),
		"AuthoritativeActiveDecisions": AuthoritativeActiveDecisions(claims, KindGrant),
	} {
		if len(got) != 1 || LifecycleFromClaim(got[0]).ID != "grant-y" {
			t.Fatalf("%s(grant) = %+v, want the unrelated grant-y", name, got)
		}
	}
}

// promoteDecisionWithoutID promotes one accepted decision claim that carries
// no decision_id, like a knowledge_page or brain-D* import, and returns its
// claim id.
func promoteDecisionWithoutID(t *testing.T, store *mem.Store, object string, now time.Time) string {
	t.Helper()
	captured, err := store.Capture([]mem.Candidate{{
		Entity: "decision:page", Predicate: "decided", Object: object,
		Confidence: 1, Derivation: "deterministic", ClaimType: "decision",
		Qualifiers:     map[string]string{"kind": KindPolicy, "source": "knowledge_page", "lifecycle_status": StatusAccepted},
		PromotionBasis: candidates.BasisDecisionRecord, SourceTraceRef: "ev_page", SnippetHash: "page", Visibility: "ambient",
	}}, now)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.Adjudicate(captured[0].ID, "promote", "test", now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Claim.Qualifiers["decision_id"] != "" {
		t.Fatalf("fixture claim must have no decision_id: %+v", result.Claim.Qualifiers)
	}
	return result.Claim.ID
}
