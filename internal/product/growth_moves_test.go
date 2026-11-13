package product

import (
	"strings"
	"testing"
)

// growthUnblocksCore uses two embedded group ids so a product contract may
// set one aside: deferrals are checked against the embedded catalog.
const growthUnblocksCore = `schema_version: compound.product_growth_catalog.v0.1
groups:
  - {id: opportunity, title: Opportunity, question: "Opportunity question?", branch: research}
  - {id: strategy, title: Strategy, question: "Strategy question?", branch: strategy}
profile:
  kinds: [ios_app, website]
  audiences: [consumer]
  revenue_models: [one_time]
  objectives: [revenue, adoption]
`

// Root has four dependents: one that applies, one the profile cannot judge
// (audiences are never inferred), one that does not apply and one set aside.
const growthUnblocksAreas = `areas:
  - {id: opportunity_root, group: opportunity, title: Root, question: "Root?", measure: root}
  - {id: opportunity_open, group: opportunity, title: Open, question: "Open?", measure: open, needs: [opportunity_root]}
  - {id: opportunity_unsure, group: opportunity, title: Unsure, question: "Unsure?", measure: unsure, needs: [opportunity_root], applies: {audiences: [consumer]}}
  - {id: opportunity_other, group: opportunity, title: Other, question: "Other?", measure: other, needs: [opportunity_root], applies: {objectives: [adoption]}}
  - {id: strategy_later, group: strategy, title: Later, question: "Later?", measure: later, needs: [opportunity_open]}
`

// A move unblocks only the areas that apply to this product: measured,
// partial, stale or blind. Unknown, not-applicable and set-aside dependents
// do not count, in the move or in the brief.
func TestGrowthUnblocksCountsOnlyApplicableAreas(t *testing.T) {
	cat, err := buildTestGrowthCatalog(t, growthUnblocksCore, growthUnblocksAreas)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	applyGrowthBundle(t, home, "product:solo", func(p *Product) {
		p.Profile = ProductProfile{Kinds: []string{"website"}, Objectives: []string{"revenue"},
			Deferred: []ProfileDeferral{{Area: "strategy", Reason: "not this year"}}}
	})
	o := growthOpts()
	o.catalog = cat
	app := buildGrowthT(t, home, o).Apps[0]
	want := map[string]string{
		"opportunity_root": GrowthBlind, "opportunity_open": GrowthBlind, "opportunity_unsure": GrowthUnknown,
		"opportunity_other": GrowthNotApplicable, "strategy_later": GrowthDeferred,
	}
	for id, status := range want {
		if got := growthAudit(t, app, id).Status; got != status {
			t.Fatalf("%s = %s, want %s", id, got, status)
		}
	}
	var root *GrowthNextMove
	for i := range app.NextMoves {
		if app.NextMoves[i].AreaID == "opportunity_root" {
			root = &app.NextMoves[i]
		}
	}
	if root == nil || root.Unblocks != 1 || !strings.Contains(root.Why, "1 other area needs it first.") {
		t.Fatalf("root move = %+v, want unblocks 1 (only opportunity_open applies)", root)
	}
	o.ProductID, o.Area = "product:solo", "opportunity_root"
	b, err := BuildGrowthArea(home, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Unblocks) != 1 || b.Unblocks[0].AreaID != "opportunity_open" {
		t.Fatalf("brief unblocks = %+v", b.Unblocks)
	}
}
