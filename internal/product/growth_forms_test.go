package product

import (
	"strings"
	"testing"
)

// A product that is both a phone app and a website judges paid advertising
// with two forms: Apple Search Ads for the app and the base row for the site.
// When every ad row is an Apple Search Ads row, both forms read the same rows
// and say the same thing, so the area reports it once, under the app's form.
func TestGrowthSameEvidenceFormsReportOnce(t *testing.T) {
	home := t.TempDir()
	rows := growthDaily("product:dual", "app_store_connect_units", 6, 0, 1)
	rows = append(rows, growthDaily("product:dual", "gsc_clicks", 6, 0, 2)...)
	writeGrowthSignals(t, home, rows...)
	var ads []AdRow
	for off := 6; off >= 0; off-- {
		ads = append(ads, AdRow{ProductID: "product:dual", Date: growthDay(off), Spend: 3, Conversions: 1, ImportedAt: "2026-11-01T00:00:00Z"})
	}
	writeGrowthAds(t, home, ads...)
	app := growthApp(t, buildGrowthT(t, home, growthOpts()), "product:dual")
	if strings.Join(app.Profile.Kinds.Values, ",") != "ios_app,website" {
		t.Fatalf("kinds = %+v", app.Profile.Kinds)
	}
	paid := growthAudit(t, app, "acquisition_paid")
	want := "Apple Search Ads: $21 spent on ads and 7 conversions in 30 days."
	if paid.Status != GrowthMeasured || paid.Summary != want || strings.Join(paid.Forms, "; ") != "Apple Search Ads" {
		t.Fatalf("paid advertising\n status %s\n summary %q\n forms %q\nwant measured, %q, [Apple Search Ads]", paid.Status, paid.Summary, paid.Forms, want)
	}
	if len(paid.Evidence) != 1 || paid.Evidence[0].Metric != growthAppleAds {
		t.Fatalf("the duplicate form's evidence must not be listed twice: %+v", paid.Evidence)
	}

	// One row from another ad platform: the base row now reads rows the app
	// form does not, so both forms stay.
	ads = append(ads, AdRow{ProductID: "product:dual", Platform: "other", Date: growthDay(1), Spend: 4, Conversions: 1, ImportedAt: "2026-11-01T00:00:00Z"})
	writeGrowthAds(t, home, ads...)
	paid = growthAudit(t, growthApp(t, buildGrowthT(t, home, growthOpts()), "product:dual"), "acquisition_paid")
	if len(paid.Forms) != 2 || len(paid.Evidence) != 2 {
		t.Fatalf("forms that read different rows must both stay: %+v", paid)
	}
}

// growthFormFoldAreas has one area whose base row has no evidence rule and
// one whose base row reads data the phone form does not.
const growthFormFoldAreas = `areas:
  - id: alpha_reach
    group: alpha
    title: Reach
    question: "Do people find it?"
    measure: reach
    kinds:
      ios_app:
        title: Store search
        evidence: {metrics: [app_store_connect_impressions], source: App Store Connect analytics}
  - id: alpha_visits
    group: alpha
    title: Visits
    question: "Do people visit?"
    measure: visits
    evidence: {metrics: ["ga4_*"], source: Google Analytics}
    kinds:
      ios_app:
        title: Store visits
        evidence: {metrics: [app_store_connect_impressions], source: App Store Connect analytics}
`

// The base row that stands in for a kind without a form is a missing form
// only when it has an evidence rule that data the kind forms do not read
// could meet. A base row with no rule cannot be measured on its own, so it
// does not hold a measured phone form back to partial.
func TestGrowthBaseFormMissingOnlyWithItsOwnRule(t *testing.T) {
	cat, err := buildTestGrowthCatalog(t, growthTestCore, growthFormFoldAreas)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	applyGrowthBundle(t, home, "product:dual", func(p *Product) {
		p.Profile = ProductProfile{Kinds: []string{"ios_app", "website"}}
	})
	writeGrowthSignals(t, home, growthDaily("product:dual", "app_store_connect_impressions", 6, 0, 40)...)
	o := growthOpts()
	o.catalog = cat
	app := buildGrowthT(t, home, o).Apps[0]

	reach := growthAudit(t, app, "alpha_reach")
	// The summary names the source in plain words, never the metric id.
	want := "Store search: 7 rows from App Store Connect analytics, latest " + growthDay(0) + "."
	if reach.Status != GrowthMeasured || reach.Summary != want || strings.Join(reach.Forms, "; ") != "Store search" || reach.Title != "Reach" {
		t.Fatalf("base row without a rule\n status %s\n summary %q\n forms %q\n title %q\nwant measured, %q", reach.Status, reach.Summary, reach.Forms, reach.Title, want)
	}
	visits := growthAudit(t, app, "alpha_visits")
	if visits.Status != GrowthPartial || strings.Join(visits.Forms, "; ") != "Store visits; Visits" || !strings.Contains(visits.Summary, "Nothing measures Visits yet.") || !strings.Contains(visits.NextMove, "Google Analytics") {
		t.Fatalf("a base row with its own rule stays a missing form: %+v", visits)
	}
	if reach.Reason != "" || visits.Reason != "" {
		t.Fatalf("applicable areas carry no reason: %q %q", reach.Reason, visits.Reason)
	}
}
