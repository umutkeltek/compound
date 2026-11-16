package product

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// The App Store analytics capture keeps one row per app and day in this file
// under the Compound home. Each cell carries its own state; only a cell whose
// evidence says "observed" is a number. The growth projection reads the
// series directly, beside the sales series; it never writes the ledger.
var growthASCFunnelSeriesRel = filepath.Join("private", "metrics", "raw", "asc-funnel", "daily-app-series.jsonl")

// growthASCFunnelCells are the analytics cells growth reads, each with the
// ledger metric it feeds. The names are the ones the growth catalog already
// judges, so the areas they feed need no catalog change.
//
// report names the Apple report each cell is published in. Apple publishes
// each report for the whole account, so an observed cell of any app shows
// the report exists for that day (see applyGrowthASCFunnel).
var growthASCFunnelCells = []struct{ cell, metric, report string }{
	{"impressions", "app_store_connect_impressions", "discovery_and_engagement"},
	{"product_page_views", "app_store_connect_product_page_views", "discovery_and_engagement"},
	{"first_time_downloads", "app_store_connect_first_time_downloads", "downloads"},
}

// growthASCFunnelPoint is one observed analytics cell. A point with no metric
// only says the series has a row for the app that day.
type growthASCFunnelPoint struct {
	appID, date, metric string
	v                   float64
}

// loadGrowthASCFunnelSeries reads the analytics series and keeps only
// observed cells, plus one row marker per app and day. A missing file means no analytics source, not an error. A
// line that does not parse or lacks an app id or date is skipped. Two rows for
// one app and day are both dropped: the day is not known, so it stays missing
// rather than guessed.
func loadGrowthASCFunnelSeries(homePath string) ([]growthASCFunnelPoint, error) {
	var rows []ascFunnelSeriesRow
	count := map[string]int{}
	err := readJSONL(filepath.Join(homePath, growthASCFunnelSeriesRel), func(line []byte) error {
		var r ascFunnelSeriesRow
		if json.Unmarshal(line, &r) != nil {
			return nil
		}
		r.ASCAppID, r.Date = strings.TrimSpace(r.ASCAppID), strings.TrimSpace(r.Date)
		if r.ASCAppID == "" || r.Date == "" {
			return nil
		}
		if _, ok := parseGrowthDay(r.Date); !ok {
			return nil
		}
		count[r.ASCAppID+"|"+r.Date]++
		rows = append(rows, r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out []growthASCFunnelPoint
	for _, r := range rows {
		if count[r.ASCAppID+"|"+r.Date] != 1 {
			continue
		}
		out = append(out, growthASCFunnelPoint{appID: r.ASCAppID, date: r.Date})
		for _, c := range growthASCFunnelCells {
			contract, err := ascFunnelMetric(c.cell)
			if err != nil {
				continue
			}
			v, err := ascFunnelObservedValue(r, c.cell, contract)
			if err != nil {
				continue
			}
			out = append(out, growthASCFunnelPoint{appID: r.ASCAppID, date: r.Date, metric: c.metric, v: v})
		}
	}
	return out, nil
}

// applyGrowthASCFunnel folds observed analytics cells into the groups'
// periodic signals, under the same app id rule as the sales series: only an
// id that maps to exactly one app counts. The series wins over a ledger copy
// on a day it observed; ledger rows stay only on days it did not.
//
// It also records how far each report covered the app (g.reported): through
// the newest day the app has a row while some app has an observed cell from
// that report. Apple publishes each report for the whole account, so on such
// a day a cell the app lacks is a value Apple did not publish (too small, or
// none), not a report that stopped; refreshing would not bring it. The rule
// assumes the series covers one account, as the sales days index does.
func applyGrowthASCFunnel(points []growthASCFunnelPoint, ids map[string]map[*growthGroup]bool, asOf string) {
	published := map[string]map[string]bool{} // report -> days some app has an observed cell
	for _, p := range points {
		if p.metric == "" {
			continue
		}
		for _, c := range growthASCFunnelCells {
			if c.metric == p.metric {
				if published[c.report] == nil {
					published[c.report] = map[string]bool{}
				}
				published[c.report][p.date] = true
			}
		}
	}
	for _, p := range points {
		if p.date > asOf || len(ids[p.appID]) != 1 {
			continue
		}
		for g := range ids[p.appID] {
			if p.metric == "" {
				for _, c := range growthASCFunnelCells {
					if published[c.report][p.date] && p.date > g.reported[c.metric] {
						g.reported[c.metric] = p.date
					}
				}
				continue
			}
			byDate := g.signals[p.metric]
			if byDate == nil {
				byDate = map[string]growthPV{}
				g.signals[p.metric] = byDate
			}
			byDate[p.date] = growthPV{v: p.v}
		}
	}
}
