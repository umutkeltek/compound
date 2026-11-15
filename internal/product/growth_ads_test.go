package product

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGrowthAdsUsesLedgerIdentityAndAppendOrder(t *testing.T) {
	home := t.TempDir()
	writeGrowthSignals(t, home)
	a := AdRow{ProductID: "product:alpha", Account: "account-a", CampaignID: "same", Date: growthDay(2), Currency: "USD", Spend: 11, Conversions: 8, ImportedAt: "2026-11-12T12:00:00Z"}
	b := a
	b.Account, b.Spend, b.Conversions = "account-b", 7, 3
	latest := a
	latest.Spend, latest.Conversions, latest.ImportedAt = 5, 1, "2026-11-01T00:00:00Z"
	// A newer ledger generation may have an older source timestamp and a new ID.
	writeGrowthAds(t, home, a, b, latest, latest)
	model := buildGrowthT(t, home, growthOpts())
	app := growthApp(t, model, "product:alpha")
	for key, want := range map[string]float64{"ad_spend_usd": 12, "ad_conversions": 4, "ad_cpa_usd": 3} {
		for _, got := range []GrowthKpi{app.Kpis[key], model.Portfolio.Totals[key]} {
			if got.Value == nil || *got.Value != want || !got.HasData {
				t.Fatalf("%s = %+v, want %v", key, got, want)
			}
		}
	}
	if got := growthPoint(t, app.Series["ad_spend_usd"], growthDay(2)); got == nil || *got != 12 {
		t.Fatalf("daily spend = %v", got)
	}
}

func TestGrowthAdsCurrencyGapsReachKpisSeriesAndPortfolio(t *testing.T) {
	for _, tc := range []struct {
		name, currency         string
		otherDay, otherProduct bool
	}{
		{name: "same observation different currencies", currency: "EUR"},
		{name: "unknown currency"},
		{name: "unknown on another day", otherDay: true},
		{name: "EUR on another day", currency: "EUR", otherDay: true},
		{name: "portfolio EUR app", currency: "EUR", otherProduct: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeGrowthSignals(t, home)
			usd := AdRow{ProductID: "product:alpha", Account: "account-a", CampaignID: "same", Date: growthDay(2), Currency: "USD", Spend: 10, Conversions: 2}
			foreign := usd
			foreign.Currency, foreign.Spend, foreign.Conversions = tc.currency, 100, 3
			if tc.otherDay {
				foreign.Date = growthDay(3)
			}
			if tc.otherProduct {
				foreign.ProductID = "product:beta"
			}
			writeGrowthAds(t, home, usd, foreign, foreign)
			model := buildGrowthT(t, home, growthOpts())
			app := growthApp(t, model, foreign.ProductID)
			for _, kpis := range []map[string]GrowthKpi{app.Kpis, model.Portfolio.Totals} {
				for _, key := range []string{"ad_spend_usd", "ad_cpa_usd"} {
					k := kpis[key]
					if k.Value != nil || k.HasData || !strings.Contains(k.Note, "currency") {
						t.Fatalf("%s must be unavailable with reason: %+v", key, k)
					}
					raw, err := json.Marshal(k)
					if err != nil || !strings.Contains(string(raw), "\"value\":null") {
						t.Fatalf("display contract: %s, %v", raw, err)
					}
				}
			}
			want := 5.0
			if tc.otherProduct {
				want = 3
			}
			if got := app.Kpis["ad_conversions"].Value; got == nil || *got != want {
				t.Fatalf("conversions lost account/currency identity: %v", got)
			}
			if got := model.Portfolio.Totals["ad_conversions"].Value; got == nil || *got != 5 {
				t.Fatalf("portfolio conversions = %v", got)
			}
			if got := growthPoint(t, model.Portfolio.Series["ad_spend_usd"], foreign.Date); got != nil {
				t.Fatalf("unconverted day exposed as USD: %v", *got)
			}
			if tc.otherProduct {
				usdApp := growthApp(t, model, usd.ProductID)
				if got := usdApp.Kpis["ad_spend_usd"].Value; got == nil || *got != 10 {
					t.Fatal("unrelated USD product was poisoned")
				}
			}
			baseline, _ := json.Marshal(model)
			for n := 0; n < 5; n++ {
				again, _ := json.Marshal(buildGrowthT(t, home, growthOpts()))
				if string(again) != string(baseline) {
					t.Fatal("projection is nondeterministic")
				}
			}
		})
	}
}

func TestGrowthAdsCurrencyIsScopedToEachComparisonWindow(t *testing.T) {
	home := t.TempDir()
	writeGrowthSignals(t, home)
	usd := AdRow{ProductID: "product:alpha", Date: growthDay(2), Currency: "USD", Spend: 12, Conversions: 4}
	old := usd
	old.Currency, old.Date = "EUR", growthDay(32)
	writeGrowthAds(t, home, usd, old)
	model := buildGrowthT(t, home, growthOpts())
	for _, kpis := range []map[string]GrowthKpi{growthApp(t, model, usd.ProductID).Kpis, model.Portfolio.Totals} {
		for key, want := range map[string]float64{"ad_spend_usd": 12, "ad_cpa_usd": 3} {
			k := kpis[key]
			if k.Value == nil || *k.Value != want || !k.HasData || k.Previous != nil || !strings.HasPrefix(k.Note, "Previous") {
				t.Fatalf("comparison window %s = %+v", key, k)
			}
		}
	}
	old.Date = growthDay(62)
	writeGrowthAds(t, home, usd, old)
	k := growthApp(t, buildGrowthT(t, home, growthOpts()), usd.ProductID).Kpis["ad_spend_usd"]
	if k.Value == nil || *k.Value != 12 || k.Note != "" {
		t.Fatalf("out-of-window currency poisoned current KPI: %+v", k)
	}
}
