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

// growthASCAppIDs maps an App Store app id to the groups whose ledger rows
// cite it ("asc:app:<id>") or whose registry identifiers name it.
func growthASCAppIDs(signals []Signal, groups []*growthGroup, resolve func(string) *growthGroup) map[string]map[*growthGroup]bool {
	out := map[string]map[*growthGroup]bool{}
	add := func(id string, g *growthGroup) {
		if out[id] == nil {
			out[id] = map[*growthGroup]bool{}
		}
		out[id][g] = true
	}
	for _, s := range signals {
		for _, ref := range s.EvidenceRefs {
			m := growthASCAppRefRE.FindStringSubmatch(strings.TrimSpace(ref))
			if m == nil {
				continue
			}
			if g := resolve(s.ProductID); g != nil {
				add(m[1], g)
			}
		}
	}
	for _, g := range groups {
		for token := range g.tokens {
			if token != "" && strings.Trim(token, "0123456789") == "" {
				add(token, g)
			}
		}
	}
	return out
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
