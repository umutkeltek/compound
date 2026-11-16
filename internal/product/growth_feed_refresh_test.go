package product

import (
	"encoding/json"
	"strings"
	"testing"
)

func iosProfile(p *Product) { p.Profile = ProductProfile{Kinds: []string{"ios_app"}} }

// writeGrowthAdsImports writes import receipts: which days an import for a
// platform, account and product covered.
func writeGrowthAdsImports(t *testing.T, home string, imports ...AdsImport) {
	t.Helper()
	var b strings.Builder
	for i, imp := range imports {
		imp.ID = "adsimp_" + string(rune('a'+i))
		if imp.Platform == "" {
			imp.Platform = "apple_search_ads"
		}
		line, _ := json.Marshal(imp)
		b.Write(line)
		b.WriteByte('\n')
	}
	writeHomeFile(t, home, "ledger/product-ads-imports.jsonl", b.String())
}

func growthRefreshTitles(app GrowthApp) []string {
	var out []string
	for _, m := range app.NextMoves {
		if m.Kind == "refresh_data" {
			out = append(out, m.Title)
		}
	}
	return out
}

// "Refresh X" must name the feed that stopped, judged by that feed's own
// newest report: a stopped ratings feed is not the analytics feed, and an
// analytics report that still covers the account is current for an app even
// when Apple has published no product page views or downloads for it for
// weeks. Only a report that stopped for everyone asks for a refresh.
func TestGrowthRefreshNamesTheFeedThatStopped(t *testing.T) {
	build := func(t *testing.T, analyticsUntil int) GrowthApp {
		home := t.TempDir()
		applyGrowthBundle(t, home, "product:rated-app", iosProfile)
		applyGrowthBundle(t, home, "product:busy-app", iosProfile)
		// The only ratings rows are 200 days old; they also link the store app ids.
		writeASCLedger(t, home,
			ascLedgerSignal("sig_reviews", "product:rated-app", "900000041", "app_store_connect_review_count", growthDay(200), 12),
			ascLedgerSignal("sig_reviews_busy", "product:busy-app", "900000043", "app_store_connect_review_count", growthDay(200), 40))
		yes := true
		var sales, index, analytics []string
		for off := 40; off >= 1; off-- {
			sales = append(sales, salesKindsLine("900000041", growthDay(off), 2, 0, 0, 0, 1, &yes))
			index = append(index, salesIndexLine(growthDay(off), "reported", 1))
			if off < analyticsUntil {
				continue
			}
			// The small app: impressions every day, page views and downloads
			// published only until day 30.
			observed := map[string]float64{"impressions": 30}
			if off >= 30 {
				observed["product_page_views"], observed["first_time_downloads"] = 3, 1
			}
			analytics = append(analytics,
				ascFunnelLine("900000041", growthDay(off), observed, nil),
				ascFunnelLine("900000043", growthDay(off), map[string]float64{"impressions": 900, "product_page_views": 90, "first_time_downloads": 30}, nil))
		}
		writeASCSeries(t, home, sales...)
		writeASCDaysIndex(t, home, index...)
		writeASCFunnelSeries(t, home, analytics...)
		return growthApp(t, buildGrowthT(t, home, growthOpts()), "product:rated-app")
	}

	app := build(t, 1)
	ratings := growthAudit(t, app, "customer_ratings")
	if ratings.Status != GrowthStale || !strings.Contains(ratings.NextMove, "App Store ratings") || strings.Contains(ratings.NextMove, "analytics") {
		t.Fatalf("ratings = %s %q, want stale on the App Store ratings feed", ratings.Status, ratings.NextMove)
	}
	listing := growthAudit(t, app, "acquisition_listing")
	if listing.Status == GrowthStale || strings.Contains(listing.NextMove, "Refresh") || !strings.Contains(listing.NextMove, growthDay(1)) {
		t.Fatalf("listing = %s %q, want no refresh: analytics covered the app through %s", listing.Status, listing.NextMove, growthDay(1))
	}
	if got := growthRefreshTitles(app); len(got) != 1 || got[0] != "Refresh App Store ratings" {
		t.Fatalf("refresh moves = %v, want only the ratings feed", got)
	}
	for _, f := range app.Funnel {
		// Current, not stale; it has one day of data in the window, so it is a lower bound.
		if f.ID == "product_page_views" && f.Status != GrowthPartial {
			t.Fatalf("page views stage = %+v, want partial: its report is current, its days are few", f)
		}
	}

	// Analytics stopped on day 30 for every app: that is a stopped feed.
	stopped := growthAudit(t, build(t, 30), "acquisition_listing")
	if stopped.Status != GrowthStale || !strings.Contains(stopped.NextMove, "Refresh App Store Connect analytics") || !strings.Contains(stopped.NextMove, growthDay(30)) {
		t.Fatalf("listing after analytics stopped = %s %q, want a refresh from %s", stopped.Status, stopped.NextMove, growthDay(30))
	}
}

// An ads campaign that stopped spending is not a stopped import, and a
// stopped import is not a stopped campaign. Only an import that covers this
// product (its own receipts, or an account-wide receipt without a product)
// and reached past its last row shows the import kept running: then the
// advice is to confirm the campaign stopped, never to refresh. When the
// product's own import stopped, it is a refresh, even if another product's
// import on the same account kept going; if its last rows had no spend or
// impressions, the advice says so, so a paused campaign is not mistaken for
// missing spend.
func TestGrowthStoppedCampaignIsNotAStaleImport(t *testing.T) {
	home := t.TempDir()
	for _, id := range []string{"product:stopped-ads", "product:dropped-ads", "product:running-ads", "product:cut-off-ads"} {
		applyGrowthBundle(t, home, id, iosProfile)
	}
	var rows []AdRow
	for off := 40; off >= 30; off-- {
		spend, shown := 0.0, 0.0
		if off >= 36 {
			spend, shown = 2, 50
		}
		rows = append(rows,
			AdRow{Currency: "USD", ProductID: "product:stopped-ads", Account: "acct-1", Date: growthDay(off), Spend: spend, Impressions: shown},
			AdRow{Currency: "USD", ProductID: "product:dropped-ads", Account: "acct-2", Date: growthDay(off), Spend: spend, Impressions: shown},
			// Still spending on its last row: the import for it stopped.
			AdRow{Currency: "USD", ProductID: "product:cut-off-ads", Account: "acct-1", CampaignID: "camp2", Date: growthDay(off), Spend: 1, Impressions: 20})
	}
	for off := 40; off >= 0; off-- {
		rows = append(rows, AdRow{Currency: "USD", ProductID: "product:running-ads", Account: "acct-1", CampaignID: "camp3", Date: growthDay(off), Spend: 1, Impressions: 20})
	}
	writeGrowthAds(t, home, rows...)
	writeGrowthAdsImports(t, home,
		AdsImport{ProductID: "product:stopped-ads", Account: "acct-1", DateStart: growthDay(43), DateEnd: growthDay(30)},
		AdsImport{ProductID: "product:cut-off-ads", Account: "acct-1", DateStart: growthDay(43), DateEnd: growthDay(30)},
		AdsImport{ProductID: "product:running-ads", Account: "acct-1", DateStart: growthDay(13), DateEnd: growthDay(0)},
		// An account-wide import with no product kept running for acct-2.
		AdsImport{Account: "acct-2", DateStart: growthDay(13), DateEnd: growthDay(0)},
	)

	g := buildGrowthT(t, home, growthOpts())
	// Its own import stopped with its rows; another product's import on the
	// same account kept running, which says nothing about this product.
	stopped := growthApp(t, g, "product:stopped-ads")
	paid := growthAudit(t, stopped, "acquisition_paid")
	if paid.Status != GrowthStale || !strings.Contains(paid.NextMove, "Refresh") ||
		!strings.Contains(paid.NextMove, "no spend or impressions after "+growthDay(36)) {
		t.Fatalf("own import stopped = %s %q, want a refresh that says the last rows had no spend after %s", paid.Status, paid.NextMove, growthDay(36))
	}
	if got := growthRefreshTitles(stopped); len(got) == 0 {
		t.Fatal("own import stopped: want a refresh move")
	}
	dropped := growthApp(t, g, "product:dropped-ads")
	if a := growthAudit(t, dropped, "acquisition_paid"); a.Status == GrowthStale || strings.Contains(a.NextMove, "Refresh") ||
		!strings.Contains(a.NextMove, growthDay(36)) || !strings.Contains(a.NextMove, growthDay(0)) {
		t.Fatalf("account-wide import kept running = %s %q, want a check that names the last spend (%s) and the import (%s)", a.Status, a.NextMove, growthDay(36), growthDay(0))
	}
	if got := growthRefreshTitles(dropped); len(got) != 0 {
		t.Fatalf("account-wide import kept running: refresh moves = %v, want none", got)
	}
	cut := growthApp(t, g, "product:cut-off-ads")
	if a := growthAudit(t, cut, "acquisition_paid"); a.Status != GrowthStale || !strings.Contains(a.NextMove, "Refresh") || strings.Contains(a.NextMove, "no spend") {
		t.Fatalf("rows that stop while spending = %s %q, want a plain refresh", a.Status, a.NextMove)
	}
}
