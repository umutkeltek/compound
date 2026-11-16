package product

import (
	"testing"
	"time"
)

// A campaign whose rows keep arriving with zero spend and zero impressions is
// not running: it is quiet, and the projection says since when. Impressions
// without spend still count as running, because a pay-per-tap campaign can
// show ads that nobody taps.
func TestAdsCampaignIsActiveOnlyWithSpendOrImpressions(t *testing.T) {
	home := t.TempDir()
	report := writeAdsCSV(t, "campaigns.csv",
		"date,campaign_id,campaign_name,spend,impressions,clicks,conversions,currency\n"+
			// Spending every day.
			"2026-09-08,c1,Spending,4,100,5,1,USD\n"+
			"2026-09-09,c1,Spending,4,100,5,1,USD\n"+
			"2026-09-10,c1,Spending,4,100,5,1,USD\n"+
			// Stopped on 09-04: rows still arrive, every number zero.
			"2026-09-04,c2,Stopped,3,80,4,0,USD\n"+
			"2026-09-05,c2,Stopped,0,0,0,0,USD\n"+
			"2026-09-08,c2,Stopped,0,0,0,0,USD\n"+
			"2026-09-09,c2,Stopped,0,0,0,0,USD\n"+
			"2026-09-10,c2,Stopped,0,0,0,0,USD\n"+
			// Shown but never tapped: no spend, still running.
			"2026-09-10,c3,Shown,0,40,0,0,USD\n")
	if _, err := ImportAdsReport(home, report, AdsImportOptions{
		Platform: "other", ProductID: "product:demo.example",
		Now: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	proj, err := LoadAdsProjection(home, AdsProjectionOptions{AsOf: time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC), WindowDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	if proj.Counts.Campaigns != 3 || proj.Counts.CampaignsActive != 2 {
		t.Fatalf("counts = %+v, want 3 campaigns and 2 active", proj.Counts)
	}
	byID := map[string]AdsCampaignView{}
	for _, c := range proj.Campaigns {
		byID[c.CampaignID] = c
	}
	for id, want := range map[string]struct{ status, lastActive string }{
		"c1": {"active", "2026-09-10"},
		"c2": {"quiet", "2026-09-04"},
		"c3": {"active", "2026-09-10"},
	} {
		c, ok := byID[id]
		if !ok || c.Status != want.status || c.LastActiveDate != want.lastActive {
			t.Fatalf("campaign %s = status %q last active %q, want %q since %q", id, c.Status, c.LastActiveDate, want.status, want.lastActive)
		}
	}
	if c := byID["c2"]; c.LastDate != "2026-09-10" {
		t.Fatalf("a quiet campaign keeps its newest row date: %+v", c)
	}
	var other *AdsPlatformView
	for i := range proj.Platforms {
		if proj.Platforms[i].Platform == "other" {
			other = &proj.Platforms[i]
		}
	}
	if other == nil || other.Status != "connected" || other.CampaignsActive != 2 || other.CampaignsTotal != 3 {
		t.Fatalf("platform = %+v, want the report current and 2 of 3 campaigns active", other)
	}
}
