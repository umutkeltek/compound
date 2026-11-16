package product

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The daily App Store sales capture appends one row per app and reported day
// to this file under the Compound home. Earlier ledger rows were copied from
// it by a one-time import, so the ledger stopped where that import stopped
// while the capture kept running. The growth projection reads the series
// directly; it never writes the ledger.
var growthASCSalesSeriesRel = filepath.Join("private", "metrics", "raw", "asc-sales-backfill", "daily-app-series.jsonl")

// The collector writes one line per day Apple answered next to the series:
// "reported" or "no_sales", and whether the day is final. A day Apple
// answered with no sales has no series rows, so only this file tells it apart
// from a day nobody fetched.
var growthASCSalesDaysIndexRel = filepath.Join("private", "metrics", "raw", "asc-sales-backfill", "daily-days-index.jsonl")

var growthASCAppRefRE = regexp.MustCompile(`^asc:app:(\d+)$`)

// growthSalesRow is a sales series row as growth reads it. Every number is a
// pointer, so a field the row does not carry stays unknown instead of
// becoming zero: an older series has no download kinds, a newer one has them.
type growthSalesRow struct {
	Date                 string   `json:"date"`
	AppleIdentifier      string   `json:"apple_identifier"`
	DeveloperProceedsUSD *float64 `json:"developer_proceeds_usd"`
	Units                *float64 `json:"units"`
	AppUnits             *float64 `json:"app_units"`
	IAPUnits             *float64 `json:"iap_units"`
	FirstDownloads       *float64 `json:"first_downloads"`
	Redownloads          *float64 `json:"redownloads"`
	Updates              *float64 `json:"updates"`
	Final                *bool    `json:"final"`
}

// ascSalesDayIndexRow is one day Apple answered for the whole account.
type ascSalesDayIndexRow struct {
	Date       string `json:"date"`
	Status     string `json:"status"`
	FetchedOn  string `json:"fetched_on"`
	Final      *bool  `json:"final"`
	RawRows    *int   `json:"raw_rows"`
	SeriesRows *int   `json:"series_rows"`
}

// growthASCSalesValues names the ledger metric each series field feeds. A
// field the row lacks feeds nothing.
func growthASCSalesValues(r growthSalesRow) []struct {
	metric string
	v      *float64
} {
	return []struct {
		metric string
		v      *float64
	}{
		{"app_store_connect_developer_proceeds_usd", r.DeveloperProceedsUSD},
		{"app_store_connect_units", r.Units},
		{"app_store_connect_app_units", r.AppUnits},
		{"app_store_connect_iap_units", r.IAPUnits},
		{growthSalesFirstDownloads, r.FirstDownloads},
		{"app_store_connect_redownloads", r.Redownloads},
		{"app_store_connect_updates", r.Updates},
	}
}

// growthSalesFirstDownloads is the metric for new downloads in the Sales
// report: first-time downloads and paid first purchases, never redownloads,
// updates or in-app purchases.
const growthSalesFirstDownloads = "app_store_connect_first_downloads"

// loadGrowthASCSalesSeries reads the series. A missing file means no sales
// source, not an error. Lines that do not parse or lack an app id or date are
// skipped, the same way the signal ledger reader skips them.
func loadGrowthASCSalesSeries(homePath string) ([]growthSalesRow, error) {
	var out []growthSalesRow
	err := readJSONL(filepath.Join(homePath, growthASCSalesSeriesRel), func(line []byte) error {
		var r growthSalesRow
		if json.Unmarshal(line, &r) != nil {
			return nil
		}
		r.AppleIdentifier = strings.TrimSpace(r.AppleIdentifier)
		r.Date = strings.TrimSpace(r.Date)
		if r.AppleIdentifier == "" || r.Date == "" {
			return nil
		}
		if _, ok := parseGrowthDay(r.Date); !ok {
			return nil
		}
		out = append(out, r)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return out, err
}

// loadASCSalesDaysIndex reads the days index. A missing file is normal (an
// older collector does not write one); bad lines are skipped.
func loadASCSalesDaysIndex(homePath string) ([]ascSalesDayIndexRow, error) {
	var out []ascSalesDayIndexRow
	err := readJSONL(filepath.Join(homePath, growthASCSalesDaysIndexRel), func(line []byte) error {
		var r ascSalesDayIndexRow
		if json.Unmarshal(line, &r) != nil {
			return nil
		}
		r.Date = strings.TrimSpace(r.Date)
		if _, ok := parseGrowthDay(r.Date); !ok {
			return nil
		}
		out = append(out, r)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return out, err
}

// growthStoreFinalLookback is how far behind as-of the newest final sales
// day may be for store KPIs to end their window on it. Apple publishes a day
// about one day late and can still correct it for a few days.
const growthStoreFinalLookback = 4

// growthStoreWindowEnd returns how many days before as-of the store KPI
// window ends: on the source's newest final day when that day is at most
// growthStoreFinalLookback days old. A source with no final day, or whose
// newest final day is older, keeps the window ending on as-of (and so reads
// stale the usual way).
func growthStoreWindowEnd(rows []growthSalesRow, index []ascSalesDayIndexRow, asOf time.Time) int {
	asOfStr := growthDayStr(asOf)
	newest := ""
	for _, r := range rows {
		if r.Final != nil && *r.Final && r.Date <= asOfStr && r.Date > newest {
			newest = r.Date
		}
	}
	for _, d := range index {
		if d.Final != nil && *d.Final && d.Date <= asOfStr && d.Date > newest {
			newest = d.Date
		}
	}
	t, ok := parseGrowthDay(newest)
	if !ok {
		return 0
	}
	age := int(asOf.Sub(t).Hours() / 24)
	if age < 0 || age > growthStoreFinalLookback {
		return 0
	}
	return age
}

// growthSalesDayExists names the days index statuses that mean Apple's Sales
// report for the whole account exists for the day. Any other status says
// nothing about the day.
var growthSalesDayExists = map[string]bool{"reported": true, "no_sales": true}

// growthSalesSoleApps lists, in a stable order, the apps whose every store
// app id names them alone. An app that a shared id also names is left out.
func growthSalesSoleApps(ids map[string]map[*growthGroup]bool) []*growthGroup {
	shared := map[*growthGroup]bool{}
	for _, groups := range ids {
		if len(groups) > 1 {
			for g := range groups {
				shared[g] = true
			}
		}
	}
	seen := map[*growthGroup]bool{}
	var out []*growthGroup
	for _, appID := range growthSortedKeys(ids) {
		if len(ids[appID]) != 1 {
			continue
		}
		for g := range ids[appID] {
			if !shared[g] && !seen[g] {
				seen[g] = true
				out = append(out, g)
			}
		}
	}
	return out
}

// growthSalesDayComplete reports whether the series holds every row of the
// day's Sales report. A reported day needs the index to count its series rows
// and the series to hold exactly that many: a row without the count does not
// show the series is complete. A no-sales day is complete when the series has
// no row for it, since Apple said the whole account sold nothing; a count, if
// given, must be zero.
func growthSalesDayComplete(d ascSalesDayIndexRow, seriesRows int) bool {
	switch d.Status {
	case "reported":
		return d.SeriesRows != nil && *d.SeriesRows == seriesRows
	case "no_sales":
		return seriesRows == 0 && (d.SeriesRows == nil || *d.SeriesRows == 0)
	}
	return false
}

// applyGrowthASCSalesZeroDays writes a zero for an app on a day the Sales
// report exists but holds no sales data for the app. Apple's Sales report
// lists every app that had a unit or proceeds that day, so no row means
// nothing happened: the day is zero, not missing. Only a day nobody fetched
// stays missing.
//
// The rule holds within these limits, and outside them a day stays missing:
//   - the days index must list the day as reported or no sales, on or before
//     as-of, and the series must be complete for it (growthSalesDayComplete);
//   - every store app id that names the app must name it alone: rows of an
//     id two apps share are not applied to either, so the app may have sold
//     under it on a day it has no row of its own;
//   - the Sales report must have listed the app: zeros start on its first
//     series row, or on its first older ledger row with a value above zero.
//     Apple's report never holds a zero row, so a zero ledger row was written
//     by an import, not read from the report, and proves nothing. Before the
//     first listing the app may not have been on sale;
//   - a day on which the app has any sales data on file (a series row, or a
//     ledger copy) is left as it is, so a row that lacks a field keeps that
//     field unknown;
//   - a field is filled only where the series carries it: on a day with rows,
//     some row of that day must carry the field; on a day with no rows at all,
//     the series must carry it on an earlier day. An older series without
//     download kinds therefore never turns new downloads into zero.
//
// An app that passes the id rule but that no Sales report ever listed gets
// no zero; g.salesUnlisted records the newest report day, so its sales
// evidence reads as "not in any Sales report" rather than a feed to refresh.
//
// The index, the series and the ledger rows describe one account's report;
// an app from an account the index does not cover must have no sales data
// here.
func applyGrowthASCSalesZeroDays(rows []growthSalesRow, index []ascSalesDayIndexRow, ids map[string]map[*growthGroup]bool, asOf string) {
	if len(index) == 0 || len(rows) == 0 {
		return
	}
	perDay := map[string]int{}              // series rows per day
	carried := map[string]map[string]bool{} // day -> metric some row carries
	firstCarried := map[string]string{}     // metric -> first day a row carries it
	firstRow := map[string]string{}         // store app id -> its first series day
	for _, r := range rows {
		perDay[r.Date]++
		if first, ok := firstRow[r.AppleIdentifier]; !ok || r.Date < first {
			firstRow[r.AppleIdentifier] = r.Date
		}
		for _, mv := range growthASCSalesValues(r) {
			if mv.v == nil {
				continue
			}
			if carried[r.Date] == nil {
				carried[r.Date] = map[string]bool{}
			}
			carried[r.Date][mv.metric] = true
			if first, ok := firstCarried[mv.metric]; !ok || r.Date < first {
				firstCarried[mv.metric] = r.Date
			}
		}
	}
	newestReport := ""
	for _, d := range index {
		if growthSalesDayExists[d.Status] && d.Date <= asOf && d.Date > newestReport {
			newestReport = d.Date
		}
	}
	metrics := growthASCSalesValues(growthSalesRow{})
	for _, g := range growthSalesSoleApps(ids) {
		// The first day the Sales report listed the app: its first series
		// row under any of its ids, or its first ledger value above zero.
		first := ""
		for appID, groups := range ids {
			if day, ok := firstRow[appID]; ok && groups[g] && (first == "" || day < first) {
				first = day
			}
		}
		onFile := map[string]bool{} // days the app has sales data on file
		for _, mv := range metrics {
			for d, pv := range g.signals[mv.metric] {
				onFile[d] = true
				if pv.v > 0 && (first == "" || d < first) {
					first = d
				}
			}
		}
		if first == "" {
			g.salesUnlisted = newestReport
			continue
		}
		for _, d := range index {
			if !growthSalesDayExists[d.Status] || d.Date > asOf || d.Date < first || onFile[d.Date] {
				continue
			}
			if !growthSalesDayComplete(d, perDay[d.Date]) {
				continue
			}
			for _, mv := range metrics {
				known := carried[d.Date][mv.metric]
				if perDay[d.Date] == 0 {
					firstDay, ok := firstCarried[mv.metric]
					known = ok && firstDay <= d.Date
				}
				if !known {
					continue
				}
				byDate := g.signals[mv.metric]
				if byDate == nil {
					byDate = map[string]growthPV{}
					g.signals[mv.metric] = byDate
				}
				byDate[d.Date] = growthPV{}
			}
		}
	}
}

// applyGrowthASCSales folds series rows into the groups' periodic signals.
// Only an app id that maps to exactly one app is used; an unknown or
// ambiguous id is skipped rather than guessed. The series is the source the
// ledger rows were copied from, so for the same app and day it wins. Days the
// series does not report stay missing, and a field a row lacks stays missing.
func applyGrowthASCSales(rows []growthSalesRow, ids map[string]map[*growthGroup]bool, asOf string) {
	for _, r := range rows {
		if r.Date > asOf || len(ids[r.AppleIdentifier]) != 1 {
			continue
		}
		var g *growthGroup
		for only := range ids[r.AppleIdentifier] {
			g = only
		}
		for _, mv := range growthASCSalesValues(r) {
			if mv.v == nil {
				continue
			}
			byDate := g.signals[mv.metric]
			if byDate == nil {
				byDate = map[string]growthPV{}
				g.signals[mv.metric] = byDate
			}
			byDate[r.Date] = growthPV{v: *mv.v}
		}
	}
}
