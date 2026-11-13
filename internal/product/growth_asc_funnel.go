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
var growthASCFunnelCells = []struct{ cell, metric string }{
	{"impressions", "app_store_connect_impressions"},
	{"product_page_views", "app_store_connect_product_page_views"},
	{"first_time_downloads", "app_store_connect_first_time_downloads"},
}

// growthASCFunnelPoint is one observed analytics cell.
type growthASCFunnelPoint struct {
	appID, date, metric string
	v                   float64
}

// loadGrowthASCFunnelSeries reads the analytics series and keeps only
// observed cells. A missing file means no analytics source, not an error. A
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
func applyGrowthASCFunnel(points []growthASCFunnelPoint, ids map[string]map[*growthGroup]bool, asOf string) {
	for _, p := range points {
		if p.date > asOf || len(ids[p.appID]) != 1 {
			continue
		}
		for g := range ids[p.appID] {
			byDate := g.signals[p.metric]
			if byDate == nil {
				byDate = map[string]growthPV{}
				g.signals[p.metric] = byDate
			}
			byDate[p.date] = growthPV{v: p.v}
		}
	}
}
