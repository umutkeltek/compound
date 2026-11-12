package product

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
)

// The daily App Store sales capture appends one row per app and reported day
// to this file under the Compound home. Earlier ledger rows were copied from
// it by a one-time import, so the ledger stopped where that import stopped
// while the capture kept running. The growth projection reads the series
// directly; it never writes the ledger.
var growthASCSalesSeriesRel = filepath.Join("private", "metrics", "raw", "asc-sales-backfill", "daily-app-series.jsonl")

var growthASCAppRefRE = regexp.MustCompile(`^asc:app:(\d+)$`)

// growthASCSalesValues names the ledger metric each series field feeds.
func growthASCSalesValues(r ascSalesSeriesRow) [4]struct {
	metric string
	v      float64
} {
	return [4]struct {
		metric string
		v      float64
	}{
		{"app_store_connect_developer_proceeds_usd", r.DeveloperProceedsUSD},
		{"app_store_connect_units", r.Units},
		{"app_store_connect_app_units", r.AppUnits},
		{"app_store_connect_iap_units", r.IAPUnits},
	}
}

// loadGrowthASCSalesSeries reads the series. A missing file means no sales
// source, not an error. Lines that do not parse or lack an app id or date are
// skipped, the same way the signal ledger reader skips them.
func loadGrowthASCSalesSeries(homePath string) ([]ascSalesSeriesRow, error) {
	var out []ascSalesSeriesRow
	err := readJSONL(filepath.Join(homePath, growthASCSalesSeriesRel), func(line []byte) error {
		var r ascSalesSeriesRow
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
// series does not report stay missing.
func applyGrowthASCSales(rows []ascSalesSeriesRow, ids map[string]map[*growthGroup]bool, asOf string) {
	for _, r := range rows {
		if r.Date > asOf || len(ids[r.AppleIdentifier]) != 1 {
			continue
		}
		var g *growthGroup
		for only := range ids[r.AppleIdentifier] {
			g = only
		}
		for _, mv := range growthASCSalesValues(r) {
			byDate := g.signals[mv.metric]
			if byDate == nil {
				byDate = map[string]growthPV{}
				g.signals[mv.metric] = byDate
			}
			byDate[r.Date] = growthPV{v: mv.v}
		}
	}
}
