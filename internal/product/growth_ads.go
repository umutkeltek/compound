package product

import "strings"

// This marker carries missing currency through daily and portfolio aggregation.
// It is not a public KPI or a money amount.
const growthAdsUnconverted = "ads_unconverted_currency"

const growthAdsCurrencyNote = "USD total unavailable: ads include non-USD or unknown currency; no exchange-rate conversion is recorded."

// growthAdsLast is how far a product's ad rows reach: row is the newest day
// with a row, ran the newest day a row had spend or impressions above zero.
type growthAdsLast struct{ row, ran string }

// growthAdsFeedMetrics maps each ads metric to the platform whose rows feed it
// ("" for every platform).
var growthAdsFeedMetrics = map[string]string{
	growthAdsSpend: "", growthAdsConversions: "", growthAdsUnconverted: "", growthAppleAds: "apple_search_ads",
}

// Reuse the ledger's identity fold and currency-aware totals. A USD subtotal
// must never masquerade as all advertising spend or divide all conversions.
// imports are the import receipts; they tell how far each platform and
// account's import reached (see growthAdsFeedReach).
func applyGrowthAds(rows []AdRow, imports []AdsImport, resolve func(string) *growthGroup, asOf string) {
	days := map[*growthGroup]map[string][]AdRow{}
	accounts := map[*growthGroup]map[growthAdsAccount]bool{}
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
			accounts[g] = map[growthAdsAccount]bool{}
		}
		days[g][r.Date] = append(days[g][r.Date], r)
		accounts[g][growthAdsAccount{r.Platform, r.Account}] = true
		g.noteAdRow("", r)
		g.noteAdRow(r.Platform, r)
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
	growthAdsFeedReach(accounts, imports, asOf)
}

// growthAdsAccount is one platform and ad account: the unit an import covers.
type growthAdsAccount struct{ platform, account string }

func (g *growthGroup) noteAdRow(platform string, r AdRow) {
	if g.adsLast == nil {
		g.adsLast = map[string]growthAdsLast{}
	}
	last := g.adsLast[platform]
	if r.Date > last.row {
		last.row = r.Date
	}
	if adRowRan(r) && r.Date > last.ran {
		last.ran = r.Date
	}
	g.adsLast[platform] = last
}

// growthAdsFeedReach tells a campaign that stopped from an import that
// stopped. A product's ad rows end either way, but only a stopped import
// needs a refresh. Its ads metrics count as reported through the newest day
// an import for the same platform and account reached (g.reported) only when
// both hold:
//   - its newest rows show no spend and no impressions: it had stopped
//     running before its rows ended;
//   - an import for the same platform and account, for any product, reached a
//     later day than its newest row: the import kept running.
//
// Rows that end on a day the campaign still ran, or an account whose imports
// all end with the product's rows, leave the metrics judged on their rows.
func growthAdsFeedReach(accounts map[*growthGroup]map[growthAdsAccount]bool, imports []AdsImport, asOf string) {
	reach := map[growthAdsAccount]string{}
	for _, imp := range imports {
		k := growthAdsAccount{imp.Platform, imp.Account}
		if _, ok := parseGrowthDay(imp.DateEnd); ok && imp.DateEnd <= asOf && imp.DateEnd > reach[k] {
			reach[k] = imp.DateEnd
		}
	}
	for g, keys := range accounts {
		for metric, platform := range growthAdsFeedMetrics {
			last := g.adsLast[platform]
			if last.row == "" || last.ran >= last.row {
				continue
			}
			newest := ""
			for k := range keys {
				if (platform == "" || k.platform == platform) && reach[k] > newest {
					newest = reach[k]
				}
			}
			if newest > last.row && newest > g.reported[metric] {
				g.reported[metric] = newest
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
