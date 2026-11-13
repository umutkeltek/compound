package product

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Coverage states, per product and catalog report.
const (
	StoreReportFresh         = "fresh"
	StoreReportGaps          = "gaps"
	StoreReportStale         = "stale"
	StoreReportNotFinal      = "not_final"
	StoreReportNotCaptured   = "not_captured"
	StoreReportNotApplicable = "not_applicable"
)

// storeReportsDefaultDays is the coverage window when none is given.
const storeReportsDefaultDays = 60

// storeReportDayGrace is how many days a daily report may lag behind its
// newest due day before it reads as stale rather than late.
const storeReportDayGrace = 3

// StoreReportCoverage is one report's coverage for one product (or for the
// whole account). Day fields hold YYYY-MM-DD for daily reports and the period
// label (YYYY-MM) for monthly and Apple fiscal month reports.
type StoreReportCoverage struct {
	ReportID string `json:"report_id"`
	State    string `json:"state"`
	// Scope is account (one answer covers every app), app (each app's own
	// rows) or none (not captured).
	Scope     string `json:"scope"`
	FirstDay  string `json:"first_day"`
	NewestDay string `json:"newest_day"`
	// AppNewestDay is the product's own newest row in an account-wide
	// report; an app with no activity on a day has no row.
	AppNewestDay string   `json:"app_newest_day,omitempty"`
	DaysPresent  int      `json:"days_present"`
	MissingDays  []string `json:"missing_days"`
	// NotFinalDays are days still inside the publishing lag, or older days
	// the source itself flags as not final; never counted missing.
	NotFinalDays []string `json:"not_final_days"`
	Note         string   `json:"note"`
}

// storeSeriesRow is one series row reduced to what coverage needs.
type storeSeriesRow struct {
	app, day string
	final    *bool
	observed map[string]bool
}

// storeReportFound is what the fast path found for one catalog report.
type storeReportFound struct {
	report     StoreReport
	captured   bool
	answered   map[string]string          // account periods -> reported | no_sales
	notFinal   map[string]bool            // periods the source flags as not final
	appDays    map[string]map[string]bool // app id -> periods with a counted row
	seriesDays map[string]bool            // periods with any series row
	index      map[string]ascSalesDayIndexRow
}

// storeReportScan is what the fast path found for every catalog report: file
// names, the series and the days index, never raw report contents.
type storeReportScan struct {
	cat      *storeReportCatalog
	asOf     time.Time
	found    map[string]*storeReportFound
	appsSeen map[string]map[string]bool // app id -> report ids
}

// scanStoreReports reads the catalog and, for each captured report, its file
// names and series rows.
func scanStoreReports(homePath string, asOf time.Time) (*storeReportScan, error) {
	cat, err := loadStoreReportCatalog(homePath)
	if err != nil {
		return nil, err
	}
	return scanStoreReportsWith(homePath, cat, asOf)
}

func scanStoreReportsWith(homePath string, cat *storeReportCatalog, asOf time.Time) (*storeReportScan, error) {
	s := &storeReportScan{cat: cat, asOf: asOf, found: map[string]*storeReportFound{}, appsSeen: map[string]map[string]bool{}}
	asOfStr := growthDayStr(asOf)
	cache := map[string][]storeSeriesRow{}
	for _, r := range cat.Reports {
		f := &storeReportFound{report: r, answered: map[string]string{}, notFinal: map[string]bool{}, appDays: map[string]map[string]bool{}, seriesDays: map[string]bool{}, index: map[string]ascSalesDayIndexRow{}}
		s.found[r.ID] = f
		if se := r.Capture.Series; se != nil {
			key := se.Path + "\x00" + se.AppField + "\x00" + se.DayField
			rows, ok := cache[key]
			if !ok {
				var err error
				if rows, err = readStoreSeries(filepath.Join(homePath, filepath.FromSlash(se.Path)), se.AppField, se.DayField); err != nil {
					return nil, err
				}
				cache[key] = rows
			}
			for _, row := range rows {
				if row.day > asOfStr {
					continue
				}
				if len(se.Cells) > 0 && !row.anyObserved(se.Cells) {
					continue
				}
				f.seriesDays[row.day] = true
				if f.appDays[row.app] == nil {
					f.appDays[row.app] = map[string]bool{}
				}
				f.appDays[row.app][row.day] = true
				if s.appsSeen[row.app] == nil {
					s.appsSeen[row.app] = map[string]bool{}
				}
				s.appsSeen[row.app][r.ID] = true
				if se.DayScope == "account" {
					f.answered[row.day] = "reported"
					if row.final != nil && !*row.final {
						f.notFinal[row.day] = true
					}
				}
			}
			if se.DaysIndex != "" && se.DayScope == "account" {
				var idx []ascSalesDayIndexRow
				err := readJSONL(filepath.Join(homePath, filepath.FromSlash(se.DaysIndex)), func(line []byte) error {
					var d ascSalesDayIndexRow
					if json.Unmarshal(line, &d) == nil {
						if _, ok := parseGrowthDay(strings.TrimSpace(d.Date)); ok {
							d.Date = strings.TrimSpace(d.Date)
							idx = append(idx, d)
						}
					}
					return nil
				})
				if err != nil {
					return nil, err
				}
				for _, d := range idx {
					if d.Date > asOfStr {
						continue
					}
					f.index[d.Date] = d
					f.markAnswered(d.Date, d.Status)
					if d.Final != nil {
						f.notFinal[d.Date] = !*d.Final
					}
				}
			}
		}
		if fc := r.Capture.Files; fc != nil {
			re := regexp.MustCompile(fc.PeriodPattern)
			for _, g := range fc.Globs {
				matches, err := filepath.Glob(filepath.Join(homePath, filepath.FromSlash(g)))
				if err != nil {
					return nil, err
				}
				for _, m := range matches {
					base := filepath.Base(m)
					sub := re.FindStringSubmatch(base)
					if sub == nil {
						continue
					}
					period := sub[1]
					if span, ok := storePeriodSpan(r.Grain, period); !ok || growthDayStr(span.start) > asOfStr {
						continue
					}
					status := "reported"
					if strings.Contains(base, ".no-sales.") {
						status = "no_sales"
					}
					f.markAnswered(period, status)
				}
			}
		}
		f.captured = len(f.answered) > 0 || len(f.appDays) > 0
	}
	return s, nil
}

// markAnswered records the store's answer for a period; a real report wins
// over a no-sales answer (the store can publish a day it first called empty).
func (f *storeReportFound) markAnswered(period, status string) {
	if status != "reported" && status != "no_sales" {
		return
	}
	if f.answered[period] == "reported" {
		return
	}
	f.answered[period] = status
}

func (r storeSeriesRow) anyObserved(cells []string) bool {
	for _, c := range cells {
		if r.observed[c] {
			return true
		}
	}
	return false
}

// readStoreSeries reads a JSONL series generically: the app and day fields
// the catalog names, the row's final flag, and which cells' metric_evidence
// says observed. A missing file is an empty series; bad lines are skipped.
func readStoreSeries(path, appField, dayField string) ([]storeSeriesRow, error) {
	var out []storeSeriesRow
	err := readJSONL(path, func(line []byte) error {
		var m map[string]json.RawMessage
		if json.Unmarshal(line, &m) != nil {
			return nil
		}
		row := storeSeriesRow{app: jsonScalar(m, appField), day: jsonScalar(m, dayField)}
		if row.app == "" {
			return nil
		}
		if _, ok := parseGrowthDay(row.day); !ok {
			return nil
		}
		if raw, ok := m["final"]; ok {
			var b bool
			if json.Unmarshal(raw, &b) == nil {
				row.final = &b
			}
		}
		if raw, ok := m["metric_evidence"]; ok {
			var ev map[string]struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(raw, &ev) == nil {
				row.observed = map[string]bool{}
				for cell, e := range ev {
					if e.Status == "observed" {
						row.observed[cell] = true
					}
				}
			}
		}
		out = append(out, row)
		return nil
	})
	return out, err
}

// storeSpan is a period's first and last day.
type storeSpan struct{ start, end time.Time }

// storePeriodSpan returns the days a period label covers for a grain.
func storePeriodSpan(grain, label string) (storeSpan, bool) {
	switch grain {
	case "day":
		t, ok := parseGrowthDay(label)
		return storeSpan{t, t}, ok
	case "month":
		t, err := time.Parse("2006-01", label)
		if err != nil {
			return storeSpan{}, false
		}
		return storeSpan{t, t.AddDate(0, 1, -1)}, true
	case "fiscal_month":
		return appleFiscalPeriod(label)
	}
	return storeSpan{}, false
}

// storePeriodOf returns the label of the period that holds day t.
func storePeriodOf(grain string, t time.Time) string {
	switch grain {
	case "day":
		return growthDayStr(t)
	case "month":
		return t.Format("2006-01")
	case "fiscal_month":
		return appleFiscalLabel(t)
	}
	return ""
}

// appleFiscalWeeks is the length in weeks of each period of an Apple fiscal
// year; the last period also takes a 53rd week when the year has one.
var appleFiscalWeeks = [12]int{5, 4, 4, 5, 4, 4, 5, 4, 4, 5, 4, 4}

// appleFiscalYearStart is the first day of Apple fiscal year fy: the Sunday
// after the last Saturday of September of the year before.
func appleFiscalYearStart(fy int) time.Time {
	t := time.Date(fy-1, time.September, 30, 0, 0, 0, 0, time.UTC)
	for t.Weekday() != time.Saturday {
		t = t.AddDate(0, 0, -1)
	}
	return t.AddDate(0, 0, 1)
}

// appleFiscalPeriod returns the days of an Apple fiscal period labelled
// YYYY-PP (fiscal year, period 01..12), as the Finance report uses them.
func appleFiscalPeriod(label string) (storeSpan, bool) {
	t, err := time.Parse("2006-01", label)
	if err != nil {
		return storeSpan{}, false
	}
	fy, p := t.Year(), int(t.Month())
	start := appleFiscalYearStart(fy)
	for i := 0; i < p-1; i++ {
		start = start.AddDate(0, 0, 7*appleFiscalWeeks[i])
	}
	end := start.AddDate(0, 0, 7*appleFiscalWeeks[p-1]-1)
	if p == 12 {
		end = appleFiscalYearStart(fy+1).AddDate(0, 0, -1)
	}
	return storeSpan{start, end}, true
}

// appleFiscalLabel is the Apple fiscal period label that holds day t.
func appleFiscalLabel(t time.Time) string {
	fy := t.Year()
	if !t.Before(appleFiscalYearStart(fy + 1)) {
		fy++
	}
	for p := 1; p <= 12; p++ {
		label := time.Date(fy, time.Month(p), 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
		if span, _ := appleFiscalPeriod(label); !t.After(span.end) {
			return label
		}
	}
	return ""
}

// coverage is one report's coverage over appIDs (or the whole account when
// account is true) across the window of days ending on as-of.
func (s *storeReportScan) coverage(reportID string, appIDs []string, account bool, days int) StoreReportCoverage {
	f := s.found[reportID]
	r := f.report
	cov := StoreReportCoverage{ReportID: reportID, Scope: "none", MissingDays: []string{}, NotFinalDays: []string{}}
	if r.Capture.None() {
		cov.State, cov.Note = StoreReportNotCaptured, "No collector captures this report."
		return cov
	}
	cov.Scope = "account"
	if se := r.Capture.Series; se != nil && se.DayScope == "app" {
		cov.Scope = "app"
	}
	if !account && len(appIDs) == 0 {
		cov.State, cov.Note = StoreReportNotApplicable, "No store app id is linked to this product."
		return cov
	}
	if !f.captured {
		cov.State, cov.Note = StoreReportNotCaptured, "The catalog names a capture, but nothing was found under the Compound home."
		return cov
	}
	appPresent := map[string]bool{}
	for _, id := range appIDs {
		for d := range f.appDays[id] {
			appPresent[d] = true
		}
	}
	if account {
		for _, byDay := range f.appDays {
			for d := range byDay {
				appPresent[d] = true
			}
		}
	}
	present := appPresent
	if cov.Scope == "account" {
		present = map[string]bool{}
		for p := range f.answered {
			present[p] = true
		}
		if !account {
			cov.AppNewestDay = newestKey(appPresent)
		}
	}
	if len(present) == 0 {
		cov.State, cov.Note = StoreReportNotCaptured, "The capture has no rows for this app."
		return cov
	}
	labels := growthSortedKeys(present)
	cov.FirstDay, cov.NewestDay = labels[0], labels[len(labels)-1]
	firstSpan, _ := storePeriodSpan(r.Grain, cov.FirstDay)

	lag := r.PublishedAfter.LagDays
	windowStart := s.asOf.AddDate(0, 0, -(days - 1))
	var windowLabels []string
	seen := map[string]bool{}
	for t := windowStart; !t.After(s.asOf); t = t.AddDate(0, 0, 1) {
		if l := storePeriodOf(r.Grain, t); l != "" && !seen[l] {
			seen[l] = true
			windowLabels = append(windowLabels, l)
		}
	}
	lastDue, presentDue, flagged := "", 0, 0
	for _, l := range windowLabels {
		span, ok := storePeriodSpan(r.Grain, l)
		if !ok {
			continue
		}
		due := int(s.asOf.Sub(span.end).Hours()/24) > lag
		switch {
		case !due:
			cov.NotFinalDays = append(cov.NotFinalDays, l)
		case present[l]:
			presentDue++
			lastDue = l
			if f.notFinal[l] {
				cov.NotFinalDays = append(cov.NotFinalDays, l)
				flagged++
			}
		default:
			lastDue = l
			if span.start.After(firstSpan.start) {
				cov.MissingDays = append(cov.MissingDays, l)
			}
		}
	}
	cov.DaysPresent = presentDue
	newestSpan, _ := storePeriodSpan(r.Grain, cov.NewestDay)
	lastDueSpan, hasDue := storePeriodSpan(r.Grain, lastDue)
	grace := 0
	if r.Grain == "day" {
		grace = storeReportDayGrace
	}
	switch {
	case hasDue && int(lastDueSpan.start.Sub(newestSpan.end).Hours()/24) > grace:
		cov.State = StoreReportStale
		cov.Note = "The newest captured " + storeGrainNoun(r.Grain) + " is " + cov.NewestDay + "; " + lastDue + " should be published by now."
	case len(cov.MissingDays) > 0:
		cov.State = StoreReportGaps
		cov.Note = growthCount(float64(len(cov.MissingDays)), storeGrainNoun(r.Grain), storeGrainNoun(r.Grain)+"s") + " missing in the window."
	case flagged > 0 || (presentDue == 0 && len(cov.NotFinalDays) > 0):
		cov.State = StoreReportNotFinal
		cov.Note = "Some " + storeGrainNoun(r.Grain) + "s are captured but not final yet."
	default:
		cov.State = StoreReportFresh
	}
	return cov
}

func storeGrainNoun(grain string) string {
	switch grain {
	case "day":
		return "day"
	case "week":
		return "week"
	}
	return "period"
}

func newestKey(m map[string]bool) string {
	newest := ""
	for k := range m {
		if k > newest {
			newest = k
		}
	}
	return newest
}

// sortedAppsSeen lists every app id the captures hold.
func (s *storeReportScan) sortedAppsSeen() []string {
	ids := make([]string, 0, len(s.appsSeen))
	for id := range s.appsSeen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
