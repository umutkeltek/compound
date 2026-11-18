package mem

import "testing"

func TestRecallHonorsOperatorDecisionRetirement(t *testing.T) {
	store := newStore(t)
	old := stampedDecision("claim_prior", "prior", "")
	replace := stampedDecision("claim_later", "later", "decision://prior,claim_page")
	page := Claim{ID: "claim_page", ClaimType: "decision", Qualifiers: map[string]string{"decision_id": "page"}}
	for index, c := range []Claim{old, page, replace} {
		c.Seq, c.Entity, c.Predicate, c.Visibility = int64(index+1), "decision:"+DecisionCanonicalID(c), "decided", "operational"
		if err := appendJSONL(store.claimsLog(), c); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		entity string
		asOf   int64
		want   int
	}{
		{"decision:prior", 1, 1},
		{"decision:prior", 0, 0},
		{"decision:page", 0, 0},
		{"decision:later", 0, 1},
	} {
		got, err := store.Recall(tc.entity, RecallOpts{AsOfSeq: tc.asOf})
		if err != nil || len(got.Claims) != tc.want {
			t.Errorf("Recall(%s, %d) returned %d claims, err=%v; want %d", tc.entity, tc.asOf, len(got.Claims), err, tc.want)
		}
	}
	for _, check := range []struct {
		seq  int64
		want int
	}{{1, 1}, {0, 0}} {
		rows, _, _, err := store.derivePositions(PositionFoldOptions{Entity: "decision:prior", AsOfSeq: check.seq}, t0)
		if err != nil || len(rows) != check.want {
			t.Fatalf("positions at seq %d: %d rows, err=%v; want %d", check.seq, len(rows), err, check.want)
		}
	}
	view, err := store.CurrentView()
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"claim_prior": false, "claim_page": false, "claim_later": true} {
		if _, found := view.Claim(id); found != want {
			t.Errorf("CurrentView.Claim(%s) found=%t, want %t", id, found, want)
		}
	}
	for _, position := range view.Positions() {
		if position.ActiveClaimID == old.ID || position.ActiveClaimID == page.ID {
			t.Errorf("snapshot still injects retired position %s", position.ActiveClaimID)
		}
	}
	history, err := store.AllCurrent()
	if err != nil || len(history) != 3 {
		t.Fatalf("decision inventory lost its history: %d rows, err=%v", len(history), err)
	}
}

func TestRecallDecisionRetirementAuthority(t *testing.T) {
	law := stampedDecision("claim_law", "law", "")
	generic := Claim{ID: "claim_generic", ClaimType: "decision", Qualifiers: map[string]string{"decision_id": "law", "supersedes": "law"}}
	// The signed retirement names the generic page; the page shares a canonical
	// name with the ruling. Only the named page should leave recall.
	signed := stampedDecision("claim_signed", "retire-page", generic.ID)
	claims := []Claim{law, generic, signed}
	got := map[string]bool{}
	applyRecallDecisionRetirements(claims, got, 0)
	if got[law.ID] || !got[generic.ID] || got[signed.ID] {
		t.Fatalf("retirements with copied alias: %v", got)
	}
	for _, tc := range []struct {
		name   string
		change func(*Claim)
	}{
		{"needs review", func(c *Claim) { c.State = ClaimStateNeedsReview }},
		{"retired", func(c *Claim) { c.Visibility = "retired" }},
		{"proposed", func(c *Claim) { c.Qualifiers["lifecycle_status"] = "proposed" }},
		{"later", func(c *Claim) { c.Seq = 3 }},
		{"unattested", func(c *Claim) { delete(c.Qualifiers, OperationalDecisionAuthorityQualifier) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := stampedDecision("claim_r", "replacement", law.ID)
			tc.change(&r)
			got := map[string]bool{}
			applyRecallDecisionRetirements([]Claim{law, r}, got, 2)
			if got[law.ID] {
				t.Fatal("ineligible replacement hid current ruling")
			}
		})
	}
}
