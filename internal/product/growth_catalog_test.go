package product

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// growthCatalogPublicSkills is the closed list of complement names a tracked
// catalog file may name. Private names arrive only through the operator's
// overlay file.
var growthCatalogPublicSkills = map[string]bool{
	"aso": true, "seo-audit": true, "ai-seo": true, "programmatic-seo": true, "schema": true, "site-architecture": true,
	"directory-submissions": true, "launch": true, "competitor-profiling": true, "competitors": true, "customer-research": true,
	"copywriting": true, "product-marketing": true, "marketing-psychology": true, "cro": true, "signup": true, "onboarding": true,
	"paywalls": true, "pricing": true, "churn-prevention": true, "emails": true, "app-store-screenshots": true,
	"security-review": true, "production-audit": true, "api-design": true, "error-handling": true, "database-migrations": true,
	"tdd": true, "diagnosing-bugs": true, "web-design-guidelines": true, "codebase-design": true, "code-review": true,
}

// growthMigratedAreas are the 25 areas the hardcoded catalog held, under their
// new ids.
var growthMigratedAreas = []string{
	"acquisition_search", "acquisition_listing", "acquisition_paid", "acquisition_organic", "acquisition_landing",
	"opportunity_competitors", "activation_onboarding", "activation_time_to_value", "experience_journeys",
	"activation_feature_discovery", "brand_trust", "monetization_purchase_prompt", "monetization_triggers",
	"monetization_pricing", "monetization_free_tier", "monetization_billing_recovery", "retention_repeat_use",
	"retention_recurring_value", "retention_reengagement", "customer_ratings", "customer_feedback", "analytics_funnel",
	"analytics_experiment_design", "operations_unit_economics", "engineering_quality",
}

func TestGrowthCatalogEmbeddedLoadsClean(t *testing.T) {
	cat, err := loadEmbeddedGrowthCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var groups []string
	for _, g := range cat.Groups {
		groups = append(groups, g.ID)
	}
	want := "opportunity strategy brand experience activation acquisition monetization retention customer engineering analytics operations"
	if strings.Join(groups, " ") != want {
		t.Fatalf("groups = %v", groups)
	}
	for _, id := range growthMigratedAreas {
		if _, ok := cat.area(id); !ok {
			t.Errorf("migrated area %s missing", id)
		}
	}
	last := -1
	for _, a := range cat.Areas {
		if gi := cat.groupIdx[a.Group]; gi < last {
			t.Fatalf("area %s is out of group order", a.ID)
		} else {
			last = gi
		}
		skills := append([]string{}, a.Skills...)
		for _, f := range a.Kinds {
			skills = append(skills, f.Skills...)
		}
		for _, s := range skills {
			if !growthCatalogPublicSkills[s] {
				t.Errorf("area %s names skill %q outside the public list", a.ID, s)
			}
		}
		if a.Group == "monetization" && strings.Join(a.Applies["objectives"], ",") != "revenue" {
			t.Errorf("monetization area %s must apply only when the objective is revenue", a.ID)
		}
	}
	areas := cat.publicAreas()
	if areas[0].Number != 1 || areas[len(areas)-1].Number != len(areas) {
		t.Fatalf("areas are not numbered in order")
	}
}

// The branch list in code and the branch files of the pipeline bundle must not
// drift apart.
func TestGrowthCatalogBranchesMatchPipeline(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "..", "skills", "product-pipeline", "branches"))
	if err != nil {
		t.Fatal(err)
	}
	var files, code []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			files = append(files, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	for b := range growthBranches {
		code = append(code, b)
	}
	sort.Strings(files)
	sort.Strings(code)
	if strings.Join(files, " ") != strings.Join(code, " ") {
		t.Fatalf("branches drifted:\nfiles %v\ncode  %v", files, code)
	}
}

const growthTestCore = `schema_version: compound.product_growth_catalog.v0.1
groups:
  - {id: alpha, title: Alpha, question: "Alpha question?", branch: research}
  - {id: beta, title: Beta, question: "Beta question?", branch: delivery}
profile:
  kinds: [ios_app, website]
  audiences: [consumer]
  revenue_models: [one_time]
  objectives: [revenue, adoption]
`

const growthTestAreas = `areas:
  - {id: alpha_one, group: alpha, title: One, question: "One?", measure: one}
  - {id: beta_two, group: beta, title: Two, question: "Two?", measure: two, needs: [alpha_one], applies: {objectives: [revenue]}}
`

func buildTestGrowthCatalog(t *testing.T, docs ...string) (*growthAreaCatalog, error) {
	t.Helper()
	var files []growthCatalogFile
	for _, d := range docs {
		f, err := parseGrowthCatalogFile([]byte(d))
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return buildGrowthCatalog(files)
}

func TestGrowthCatalogValidationRejectsBrokenRows(t *testing.T) {
	if _, err := buildTestGrowthCatalog(t, growthTestCore, growthTestAreas); err != nil {
		t.Fatalf("baseline catalog must load: %v", err)
	}
	cases := map[string]struct{ areas, want string }{
		"duplicate id": {`areas:
  - {id: alpha_one, group: alpha, title: One, question: "One?", measure: one}
  - {id: alpha_one, group: alpha, title: Again, question: "Again?", measure: again}
`, "declared twice"},
		"unknown group": {`areas:
  - {id: gamma_one, group: gamma, title: One, question: "One?", measure: one}
`, "unknown group"},
		"id outside its group": {`areas:
  - {id: beta_one, group: alpha, title: One, question: "One?", measure: one}
`, "must start with its group id"},
		"unknown need": {`areas:
  - {id: alpha_one, group: alpha, title: One, question: "One?", measure: one, needs: [alpha_none]}
`, "needs unknown area"},
		"needs cycle": {`areas:
  - {id: alpha_one, group: alpha, title: One, question: "One?", measure: one, needs: [beta_two]}
  - {id: beta_two, group: beta, title: Two, question: "Two?", measure: two, needs: [beta_three]}
  - {id: beta_three, group: beta, title: Three, question: "Three?", measure: three, needs: [alpha_one]}
`, "cycle"},
		"branch outside the pipeline": {`areas:
  - {id: alpha_one, group: alpha, title: One, question: "One?", measure: one, branch: marketing}
`, "not a product-pipeline branch"},
		"applies value outside the profile": {`areas:
  - {id: alpha_one, group: alpha, title: One, question: "One?", measure: one, applies: {objectives: [fame]}}
`, "not a declared profile value"},
		"applies on an undeclared dimension": {`areas:
  - {id: alpha_one, group: alpha, title: One, question: "One?", measure: one, applies: {stage: [live]}}
`, "undeclared profile dimension"},
		"form for an undeclared kind": {`areas:
  - id: alpha_one
    group: alpha
    title: One
    question: "One?"
    measure: one
    kinds: {smart_fridge: {title: Fridge}}
`, "undeclared kind"},
		"missing measure": {`areas:
  - {id: alpha_one, group: alpha, title: One, question: "One?"}
`, "needs a title, a question and a measure"},
		"unknown custom evaluator": {`areas:
  - {id: alpha_one, group: alpha, title: One, question: "One?", measure: one, evidence: {custom: magic}}
`, "unknown custom evaluator"},
		"unknown field": {`areas:
  - {id: alpha_one, group: alpha, title: One, question: "One?", measure: one, colour: red}
`, "colour"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := buildTestGrowthCatalog(t, growthTestCore, tc.areas)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
	badGroup := strings.Replace(growthTestCore, "branch: delivery", "branch: sales", 1)
	if _, err := buildTestGrowthCatalog(t, badGroup, growthTestAreas); err == nil || !strings.Contains(err.Error(), "not a product-pipeline branch") {
		t.Fatalf("group branch outside the pipeline: err = %v", err)
	}
}

// A kind form overrides only what it sets and inherits the rest.
func TestGrowthCatalogFormInheritsBaseRow(t *testing.T) {
	cat, err := buildTestGrowthCatalog(t, growthTestCore, `areas:
  - id: alpha_one
    group: alpha
    title: Base title
    question: "Base question?"
    measure: base measure
    skills: [cro]
    evidence: {metrics: ["ga4_*"], source: Base source}
    kinds:
      ios_app:
        title: App title
        evidence: {metrics: [app_store_connect_impressions], source: App source}
`)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := cat.area("alpha_one")
	app, base, web := a.spec("ios_app"), a.spec(""), a.spec("website")
	if app.Title != "App title" || app.Question != "Base question?" || app.source != "App source" || strings.Join(app.Skills, ",") != "cro" {
		t.Fatalf("ios form = %+v", app)
	}
	if base.Title != "Base title" || base.source != "Base source" || base.Kind != "" || web.Kind != "" || web.source != "Base source" {
		t.Fatalf("base = %+v, website = %+v", base, web)
	}
	if got := cat.publicAreas()[0]; got.Branch != "research" || got.Kinds["ios_app"].Title != "App title" || len(got.Kinds) != 1 {
		t.Fatalf("public area = %+v", got)
	}
}

func writeGrowthOverlay(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, "config")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "product-growth-catalog.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGrowthCatalogOverlayAddsSkillsAndAreas(t *testing.T) {
	embedded, err := loadEmbeddedGrowthCatalog()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	writeGrowthOverlay(t, home, `areas:
  - id: acquisition_search
    skills: [example-search-helper]
  - id: retention_widget_use
    group: retention
    title: Widget use
    question: "Do people add the widget to their home screen?"
    measure: widget use
    needs: [retention_repeat_use]
`)
	writeGrowthSignals(t, home, growthDaily("product:alpha", "app_store_connect_impressions", 5, 0, 10)...)
	g := buildGrowthT(t, home, growthOpts())
	if g.Catalog.Overlay != GrowthCatalogOverlayPath || g.Catalog.OverlayError != "" || g.Catalog.Areas != len(embedded.Areas)+1 {
		t.Fatalf("catalog = %+v", g.Catalog)
	}
	idx := map[string]int{}
	for i, a := range g.Areas {
		idx[a.ID] = i
	}
	search := g.Areas[idx["acquisition_search"]]
	if !containsString(search.Skills, "seo-audit") || !containsString(search.Skills, "example-search-helper") || !containsString(search.Kinds["ios_app"].Skills, "example-search-helper") {
		t.Fatalf("overlay skill must append, not replace: %+v", search)
	}
	if !containsString(growthAudit(t, g.Apps[0], "acquisition_search").Skills, "example-search-helper") {
		t.Fatal("the audit row must carry the overlay skill")
	}
	w, ok := idx["retention_widget_use"]
	if !ok || g.Areas[w].Group != "retention" || idx["retention_repeat_use"] > w || w > idx["customer_feedback"] {
		t.Fatalf("new area must sit in its group: %v", idx)
	}
	if a := growthAudit(t, g.Apps[0], "retention_widget_use"); a.Status != GrowthBlind || a.AreaID != "retention_widget_use" {
		t.Fatalf("new area audit = %+v", a)
	}
	// The embedded catalog is untouched for the next caller.
	if again, _ := loadEmbeddedGrowthCatalog(); len(again.Areas) != len(embedded.Areas) {
		t.Fatal("an overlay leaked into the embedded catalog")
	}
}

func TestGrowthCatalogBrokenOverlayServesEmbedded(t *testing.T) {
	embedded, _ := loadEmbeddedGrowthCatalog()
	for name, body := range map[string]string{
		"unknown group": "areas:\n  - {id: nowhere_one, group: nowhere, title: One, question: \"One?\", measure: one}\n",
		"bad yaml":      "areas: [\n",
		"bad branch":    "areas:\n  - {id: acquisition_search, branch: shouting}\n",
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeGrowthOverlay(t, home, body)
			writeGrowthSignals(t, home, growthDaily("product:alpha", "gsc_clicks", 3, 0, 1)...)
			g := buildGrowthT(t, home, growthOpts())
			if g.Catalog.OverlayError == "" || !strings.Contains(g.Catalog.OverlayError, GrowthCatalogOverlayPath) || g.Catalog.Overlay != "" {
				t.Fatalf("catalog = %+v", g.Catalog)
			}
			if len(g.Areas) != len(embedded.Areas) || len(g.Apps[0].Audit) != len(embedded.Areas) {
				t.Fatalf("broken overlay must serve the embedded catalog: %d areas", len(g.Areas))
			}
		})
	}
}
