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

// growthSeedFormKinds are the kinds a catalog area may give its own form: the
// kinds where one question can mean a different thing.
var growthSeedFormKinds = map[string]bool{"ios_app": true, "website": true, "web_app": true, "api": true, "dev_tool": true}

// growthJargon are words an owner who does not program should never meet in
// an area question.
var growthJargon = map[string]bool{
	"instrument": true, "instrumented": true, "instrumentation": true, "taxonomy": true, "cohort": true, "cohorts": true,
	"kpi": true, "kpis": true, "metric": true, "metrics": true, "funnel": true, "conversion": true, "churn": true,
	"onboarding": true, "activation": true, "monetization": true, "segment": true, "segments": true,
	"attribution": true, "ux": true, "ui": true, "sdk": true,
}

// growthStoredPredicates lists every fact predicate a fully filled product
// contract stores, so a record rule can only name a fact that exists.
func growthStoredPredicates(t *testing.T) map[string]bool {
	t.Helper()
	p := Product{
		ID: "product:seed", Name: "Seed", Positioning: "a", PrivacyPosture: "b", Channels: []string{"c"},
		Essence: Essence{
			Audience: "d", Problem: "e", Value: "f", Goal: Goal{IncomeTargetUSDMonth: 1, Horizon: "g"},
			Constraints: []string{"h"}, AntiGoals: []string{"i"}, Stage: "j",
		},
		TasteProfile: TasteProfile{Standards: []string{"k"}, RedLines: []string{"l"}},
		Deployment:   Deployment{Place: "m", Method: "n", Rollback: "o", Healthchecks: []string{"p"}},
		Profile: ProductProfile{
			Kinds: []string{"web_app"}, Audiences: []string{"consumer"}, RevenueModels: []string{"none"}, Objectives: []string{"adoption"},
		},
	}
	out := map[string]bool{}
	for _, c := range productClaims(Bundle{Product: p}, "ev_seed", "seed.yaml") {
		out[c.Predicate] = true
	}
	if !out["essence.audience"] || !out["profile.objectives"] || !out["deploy_healthchecks"] || !out["taste.standards"] {
		t.Fatalf("a full product stores too few predicates: %v", out)
	}
	return out
}

// growthNeedsDepth is the longest chain of needs below an area, in steps.
func growthNeedsDepth(cat *growthAreaCatalog, id string, memo map[string]int) int {
	if d, ok := memo[id]; ok {
		return d
	}
	a, _ := cat.area(id)
	depth := 0
	for _, n := range a.Needs {
		if d := growthNeedsDepth(cat, n, memo) + 1; d > depth {
			depth = d
		}
	}
	memo[id] = depth
	return depth
}

func growthCheckWording(t *testing.T, where, title, question string) {
	t.Helper()
	if strings.TrimSpace(title) == "" || strings.Contains(title, "?") || len([]rune(title)) > 50 {
		t.Errorf("%s title %q must be a short label", where, title)
	}
	if !strings.HasSuffix(question, "?") || len([]rune(question)) >= 90 {
		t.Errorf("%s question %q must end in ? and stay under 90 characters", where, question)
	}
	for _, w := range strings.Fields(strings.ToLower(question)) {
		if growthJargon[strings.Trim(w, "?,.")] {
			t.Errorf("%s question %q uses the jargon word %q", where, question, w)
		}
	}
}

// Every catalog row follows the writing rule, names only facts a product
// contract stores, keeps the needs graph shallow and gives forms only to kinds
// where the question changes.
func TestGrowthCatalogSeedRows(t *testing.T) {
	cat, err := loadEmbeddedGrowthCatalog()
	if err != nil {
		t.Fatal(err)
	}
	stored := growthStoredPredicates(t)
	memo := map[string]int{}
	for _, a := range cat.Areas {
		growthCheckWording(t, a.ID, a.Title, a.Question)
		records := append([]string{}, a.Evidence.Record...)
		for kind, f := range a.Kinds {
			if !growthSeedFormKinds[kind] {
				t.Errorf("area %s has a form for %s; forms are only for %v", a.ID, kind, growthSeedFormKinds)
			}
			title, question := f.Title, f.Question
			if title == "" {
				title = a.Title
			}
			if question == "" {
				question = a.Question
			}
			growthCheckWording(t, a.ID+"."+kind, title, question)
			if f.Evidence != nil {
				records = append(records, f.Evidence.Record...)
			}
		}
		for _, p := range records {
			if !stored[p] {
				t.Errorf("area %s records %q, which no product contract stores", a.ID, p)
			}
		}
		if d := growthNeedsDepth(cat, a.ID, memo); d > 3 {
			t.Errorf("area %s sits %d needs deep; the limit is 3", a.ID, d)
		}
	}
}

// growthSeedAreas is the full seed catalog: ten areas per group.
var growthSeedAreas = map[string][]string{
	"opportunity": {"opportunity_problem", "opportunity_audience_segments", "opportunity_jobs", "opportunity_market_size",
		"opportunity_willingness_to_pay", "opportunity_alternatives", "opportunity_competitors", "opportunity_competitor_pricing",
		"opportunity_gaps", "opportunity_demand_validation"},
	"strategy": {"strategy_vision", "strategy_value_proposition", "strategy_differentiation", "strategy_positioning",
		"strategy_audience_priority", "strategy_business_model", "strategy_scope", "strategy_roadmap", "strategy_defensibility",
		"strategy_fit"},
	"brand": {"brand_name", "brand_mark", "brand_visual_identity", "brand_typography", "brand_voice", "brand_messaging",
		"brand_creative_direction", "brand_guidelines", "brand_trust", "brand_recognition"},
	"experience": {"experience_information_architecture", "experience_navigation", "experience_journeys", "experience_ui",
		"experience_design_system", "experience_accessibility", "experience_platform_fit", "experience_states",
		"experience_personalization", "experience_usability"},
	"activation": {"activation_first_contact", "activation_onboarding", "activation_signup", "activation_permissions",
		"activation_time_to_value", "activation_setup_completion", "activation_templates", "activation_feature_discovery",
		"activation_criteria", "activation_first_success"},
	"acquisition": {"acquisition_channels", "acquisition_search", "acquisition_listing", "acquisition_landing",
		"acquisition_organic", "acquisition_paid", "acquisition_community", "acquisition_partnerships", "acquisition_launches",
		"acquisition_attribution"},
	"monetization": {"monetization_model", "monetization_pricing", "monetization_free_tier", "monetization_purchase_prompt",
		"monetization_triggers", "monetization_checkout", "monetization_regional", "monetization_offers",
		"monetization_billing_recovery", "monetization_price_tests"},
	"retention": {"retention_repeat_use", "retention_recurring_value", "retention_lifecycle_messages", "retention_reengagement",
		"retention_churn", "retention_offboarding", "retention_customer_success", "retention_expansion", "retention_referrals",
		"retention_winback"},
	"customer": {"customer_feedback", "customer_ratings", "customer_support", "customer_interviews", "customer_segments",
		"customer_satisfaction", "customer_requests", "customer_reputation", "customer_complaints", "customer_roadmap_loop"},
	"engineering": {"engineering_architecture", "engineering_performance", "engineering_reliability", "engineering_security",
		"engineering_data_integrity", "engineering_quality", "engineering_release", "engineering_compatibility",
		"engineering_cost", "engineering_debt"},
	"analytics": {"analytics_objectives", "analytics_instrumentation", "analytics_funnel", "analytics_cohorts",
		"analytics_experiment_design", "analytics_ab_tests", "analytics_validity", "analytics_causal", "analytics_memory",
		"analytics_anomalies"},
	"operations": {"operations_unit_economics", "operations_forecast", "operations_compliance", "operations_ip",
		"operations_platform_risk", "operations_automation", "operations_allocation", "operations_incidents",
		"operations_decision_rights", "operations_lifecycle"},
}

// The embedded catalog holds exactly the 120 seed areas, ten per group, and
// every monetization area waits on the core promise and the audience.
func TestGrowthCatalogSeedIDs(t *testing.T) {
	cat, err := loadEmbeddedGrowthCatalog()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, a := range cat.Areas {
		got[a.Group] = append(got[a.Group], a.ID)
	}
	total := 0
	for _, g := range cat.Groups {
		want := append([]string{}, growthSeedAreas[g.ID]...)
		have := append([]string{}, got[g.ID]...)
		sort.Strings(want)
		sort.Strings(have)
		if len(want) != 10 || strings.Join(have, " ") != strings.Join(want, " ") {
			t.Errorf("group %s areas = %v, want %v", g.ID, have, want)
		}
		total += len(have)
	}
	if len(cat.Groups) != len(growthSeedAreas) || total != 120 || len(cat.Areas) != 120 {
		t.Fatalf("catalog holds %d groups and %d areas, want %d and 120", len(cat.Groups), len(cat.Areas), len(growthSeedAreas))
	}
	for _, id := range growthSeedAreas["monetization"] {
		a, _ := cat.area(id)
		if !containsString(a.Applies["objectives"], "revenue") || !containsString(a.Needs, "strategy_value_proposition") ||
			!containsString(a.Needs, "opportunity_audience_segments") {
			t.Errorf("monetization area %s = applies %v needs %v", id, a.Applies, a.Needs)
		}
	}
	// A phone app has no server health checks to put on file.
	rel, _ := cat.area("engineering_reliability")
	if s := rel.spec("ios_app"); s.Kind != "ios_app" || len(s.record) != 0 || s.hasSpec {
		t.Fatalf("the phone form of engineering_reliability = %+v", s)
	}
	if s := rel.spec("web_app"); strings.Join(s.record, ",") != "deploy_healthchecks" {
		t.Fatalf("the base row of engineering_reliability = %+v", s)
	}
}
