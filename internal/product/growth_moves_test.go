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

// Three areas read App Store Connect sales and one reads Search Console. Sales
// three waits on an open gap; Beta one and Beta two wait on stale areas.
const growthRefreshAreas = `areas:
  - {id: alpha_sales_one, group: alpha, title: Sales one, question: "Sales one?", measure: sales one, evidence: {metrics: [app_store_connect_units], source: App Store Connect sales}}
  - {id: alpha_sales_two, group: alpha, title: Sales two, question: "Sales two?", measure: sales two, evidence: {metrics: [app_store_connect_units], source: App Store Connect sales}}
  - {id: alpha_sales_three, group: alpha, title: Sales three, question: "Sales three?", measure: sales three, needs: [beta_four], evidence: {metrics: [app_store_connect_developer_proceeds_usd], source: App Store Connect sales}}
  - {id: alpha_search, group: alpha, title: Search, question: "Search?", measure: search, evidence: {metrics: ["gsc_*"], source: Search Console}}
  - {id: beta_one, group: beta, title: Beta one, question: "Beta one?", measure: beta one, needs: [alpha_sales_two]}
  - {id: beta_two, group: beta, title: Beta two, question: "Beta two?", measure: beta two, needs: [alpha_search]}
  - {id: beta_three, group: beta, title: Beta three, question: "Beta three?", measure: beta three}
  - {id: beta_four, group: beta, title: Beta four, question: "Beta four?", measure: beta four}
`

// One stopped source is one action: stale areas that name the same source
// fold into one refresh move, and the freed slots go to the next moves.
func TestGrowthRefreshMovesFoldBySource(t *testing.T) {
	cat, err := buildTestGrowthCatalog(t, growthTestCore, growthRefreshAreas)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	rows := growthDaily("product:alpha", "app_store_connect_units", 40, 30, 1)
	rows = append(rows, growthDaily("product:alpha", "app_store_connect_developer_proceeds_usd", 50, 40, 2)...)
	rows = append(rows, growthDaily("product:alpha", "gsc_clicks", 30, 20, 3)...)
	writeGrowthSignals(t, home, rows...)
	o := growthOpts()
	o.catalog = cat
	app := buildGrowthT(t, home, o).Apps[0]
	for _, id := range []string{"alpha_sales_one", "alpha_sales_two", "alpha_sales_three", "alpha_search"} {
		if s := growthAudit(t, app, id).Status; s != GrowthStale {
			t.Fatalf("%s = %s, want stale", id, s)
		}
	}
	moves := app.NextMoves
	if len(moves) != growthMaxMoves {
		t.Fatalf("moves = %+v", moves)
	}
	sales, search := moves[0], moves[1]
	// Sales three stopped on day 40, Sales one and two on day 30: say both ends.
	wantWhy := "The data stopped between " + growthDay(40) + " and " + growthDay(30) + ". 3 areas use it: Sales one, Sales two, Sales three."
	if sales.Kind != "refresh_data" || sales.Title != "Refresh App Store Connect sales" || sales.Why != wantWhy {
		t.Fatalf("sales move = %+v\nwant why %q", sales, wantWhy)
	}
	// The folded move names every area it covers, in catalog order, so a page
	// can mark each one; area_id stays the first.
	if got := strings.Join(sales.AreaIDs, ","); got != "alpha_sales_one,alpha_sales_two,alpha_sales_three" {
		t.Fatalf("folded move area_ids = %q, want all three sales areas", got)
	}
	for _, m := range moves[1:] {
		if len(m.AreaIDs) != 1 || m.AreaIDs[0] != m.AreaID {
			t.Fatalf("a one-area move lists exactly its area: %+v", m)
		}
	}
	if sales.AreaID != "alpha_sales_one" || sales.Brief != growthBrief("product:alpha", "alpha_sales_one") || sales.Branch != "research" ||
		sales.Unblocks != 1 || len(sales.BlockedBy) != 0 || sales.BlockedBy == nil {
		t.Fatalf("sales move must point at its first area, count beta_one once and wait on nothing: %+v", sales)
	}
	if search.Kind != "refresh_data" || search.Title != "Refresh Search Console" || search.AreaID != "alpha_search" || search.Unblocks != 1 ||
		search.Why != "The last data is from "+growthDay(20)+". 1 area uses it: Search." {
		t.Fatalf("search move = %+v", search)
	}
	for _, m := range moves[2:] {
		if m.Kind == "refresh_data" {
			t.Fatalf("one stopped source must be one move, got %+v", moves)
		}
	}
	if moves[2].AreaID != "beta_four" || moves[2].Title != "Assess: Beta four" {
		t.Fatalf("the freed slots go to the next moves in order, titled \"<verb>: <area>\": %+v", moves[2:])
	}
}

// When every folded area stopped on the same day, the why line keeps the one
// date ("The last data is from ...").
func TestGrowthRefreshWhySameDay(t *testing.T) {
	cat, err := buildTestGrowthCatalog(t, growthTestCore, growthRefreshAreas)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	rows := growthDaily("product:alpha", "app_store_connect_units", 40, 30, 1)
	rows = append(rows, growthDaily("product:alpha", "app_store_connect_developer_proceeds_usd", 40, 30, 2)...)
	writeGrowthSignals(t, home, rows...)
	o := growthOpts()
	o.catalog = cat
	sales := buildGrowthT(t, home, o).Apps[0].NextMoves[0]
	want := "The last data is from " + growthDay(30) + ". 3 areas use it: Sales one, Sales two, Sales three."
	if sales.Title != "Refresh App Store Connect sales" || sales.Why != want || len(sales.AreaIDs) != 3 {
		t.Fatalf("sales move = %+v\nwant why %q", sales, want)
	}
}

// One-area moves read "<what to do>: <area title>"; only a folded refresh
// names its source. Every kind's verb is pinned here.
func TestGrowthMoveTitlesUseColonForm(t *testing.T) {
	for verb, want := range map[string]string{
		"Refresh data":          "Refresh data: Main tasks",
		"Assess again":          "Assess again: Main tasks",
		"Connect data":          "Connect data: Main tasks",
		"Record the answer":     "Record the answer: Main tasks",
		"Assess":                "Assess: Main tasks",
		"Review the hypothesis": "Review the hypothesis: Main tasks",
		"Run an experiment":     "Run an experiment: Main tasks",
	} {
		if got := growthMoveTitle(verb, "Main tasks"); got != want {
			t.Fatalf("growthMoveTitle(%q) = %q, want %q", verb, got, want)
		}
	}
}
