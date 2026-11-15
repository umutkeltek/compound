package product

import "strings"

// This marker carries missing currency through daily and portfolio aggregation.
// It is not a public KPI or a money amount.
const growthAdsUnconverted = "ads_unconverted_currency"

const growthAdsCurrencyNote = "USD total unavailable: ads include non-USD or unknown currency; no exchange-rate conversion is recorded."

// Reuse the ledger's identity fold and currency-aware totals. A USD subtotal
// must never masquerade as all advertising spend or divide all conversions.
func applyGrowthAds(rows []AdRow, resolve func(string) *growthGroup, asOf string) {
	days := map[*growthGroup]map[string][]AdRow{}
	for _, r := range LatestAdRows(rows) {
		g := resolve(r.ProductID)
		if g == nil || r.Date > asOf {
			continue
		}
		if _, ok := parseGrowthDay(r.Date); !ok {
			continue
		}
		g.rows[strings.ToLower(r.ProductID)]++
		g.adsRows++
		if r.HasRevenue && r.Revenue > 0 {
			g.adsRevenueRows++
		}
		if days[g] == nil {
			days[g] = map[string][]AdRow{}
		}
		days[g][r.Date] = append(days[g][r.Date], r)
		if r.Platform == "apple_search_ads" {
			// This existing source-presence metric is used only for coverage.
			g.addDaily(growthAppleAds, r.Date, r.Spend)
		}
	}
	for g, byDate := range days {
		for date, rows := range byDate {
			total := adsTotals(rows)
			g.addDaily(growthAdsConversions, date, total.Conversions)
			if total.Currency == "USD" && adsSpendIsOneCurrency(total) {
				g.addDaily(growthAdsSpend, date, total.Spend)
			} else {
				g.addDaily(growthAdsUnconverted, date, 1)
			}
		}
	}
}

func (c growthCtx) withholdUnconvertedAds(k *GrowthKpi, daily map[string]map[string]float64, window int) {
	unresolved := daily[growthAdsUnconverted]
	_, current := c.sumRange(unresolved, window-1, 0)
	_, previous := c.sumRange(unresolved, 2*window-1, window)
	if current > 0 {
		k.DaysReported = 0
		spend := daily[growthAdsSpend]
		for off := 0; off < window; off++ {
			date := c.day(off)
			_, usd := spend[date]
			if usd || unresolved[date] > 0 {
				k.DaysReported++
			}
		}
		if latest := latestKey(unresolved); latest > k.LatestDate {
			k.LatestDate = latest
		}
		k.Value, k.HasData, k.Note = nil, false, growthAdsCurrencyNote
	}
	if previous > 0 {
		k.Previous = nil
		if current == 0 {
			k.Note = "Previous " + growthAdsCurrencyNote
		}
	}
}

func (c growthCtx) adsCurrencyGap(g *growthGroup, spec growthAreaSpec) bool {
	if spec.custom != "search_ads" && spec.custom != "unit_economics" {
		return false
	}
	_, days := c.sumRange(g.daily[growthAdsUnconverted], c.opts.WindowDays-1, 0)
	return days > 0
}
