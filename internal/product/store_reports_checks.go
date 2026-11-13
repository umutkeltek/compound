package product

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Check results.
const (
	StoreCheckOK     = "ok"
	StoreCheckFailed = "failed"
	StoreCheckNotRun = "not_run"
)

// The four exactness checks, in order. Each compares one store source with
// another using its own arithmetic, independent of how a collector did it.
var storeReportCheckNames = []string{"series_equals_raw", "sales_equals_finance", "downloads_agree", "days_final"}

// The catalog rows the checks read. The raw formats are App Store Connect's.
const (
	storeCheckSalesReport     = "asc_sales_summary_daily"
	storeCheckFinanceReport   = "asc_finance_financial_monthly"
	storeCheckDownloadsReport = "asc_analytics_downloads"
)

// storeCheckCarryDays is how many last days of an Apple fiscal month may land
// in the next month's Finance report.
const storeCheckCarryDays = 4

// Download agreement tolerance per 7-day block: the larger of these.
const (
	storeCheckDownloadsAbs = 3
	storeCheckDownloadsRel = 0.10
)

// StoreReportCheck is one check result for one app (or the whole account)
// and one period.
type StoreReportCheck struct {
	Check      string   `json:"check"`
	Status     string   `json:"status"`
	ProductID  string   `json:"product_id"`
	AppID      string   `json:"app_id"`
	Period     string   `json:"period"`
	Field      string   `json:"field"`
	Expected   *float64 `json:"expected"`
	Actual     *float64 `json:"actual"`
	Difference *float64 `json:"difference"`
	Note       string   `json:"note"`
}

// StoreReportCheckCounts counts checks by result.
type StoreReportCheckCounts struct {
	OK     int `json:"ok"`
	Failed int `json:"failed"`
	NotRun int `json:"not_run"`
}

func storeCheckNum(v float64) *float64 { v = math.Round(v*10000) / 10000; return &v }

func storeCheckDiff(check, product, app, period, field string, expected, actual float64, note string) StoreReportCheck {
	return StoreReportCheck{Check: check, Status: StoreCheckFailed, ProductID: product, AppID: app, Period: period, Field: field,
		Expected: storeCheckNum(expected), Actual: storeCheckNum(actual), Difference: storeCheckNum(actual - expected), Note: note}
}

// storeWindow is the check window: the days ending on as-of.
type storeWindow struct {
	asOf  time.Time
	days  int
	start string
	end   string
}

func newStoreWindow(asOf time.Time, days int) storeWindow {
	return storeWindow{asOf: asOf, days: days, start: growthDayStr(asOf.AddDate(0, 0, -(days - 1))), end: growthDayStr(asOf)}
}

func (w storeWindow) has(day string) bool { return day >= w.start && day <= w.end }

func (w storeWindow) period() string { return w.start + ".." + w.end }

// checkDaysFinal is the fast check: every day older than the Sales report's
// lag, from its first day on, is answered (a report or "no sales"), and a day
// with a report has rows in the series.
func checkDaysFinal(s *storeReportScan, w storeWindow) []StoreReportCheck {
	const name = "days_final"
	f := s.found[storeCheckSalesReport]
	if f == nil || !f.captured {
		return []StoreReportCheck{{Check: name, Status: StoreCheckNotRun, Period: w.period(), Note: "The Sales report is not captured."}}
	}
	first := ""
	for d := range f.answered {
		if first == "" || d < first {
			first = d
		}
	}
	lag := f.report.PublishedAfter.LagDays
	var out []StoreReportCheck
	for t := w.asOf.AddDate(0, 0, -(w.days - 1)); !t.After(w.asOf); t = t.AddDate(0, 0, 1) {
		d := growthDayStr(t)
		if d < first || int(w.asOf.Sub(t).Hours()/24) <= lag {
			continue
		}
		switch status := f.answered[d]; {
		case status == "":
			out = append(out, StoreReportCheck{Check: name, Status: StoreCheckFailed, Period: d, Field: "day", Note: "Apple's answer for this day was never captured."})
		case status == "reported" && !f.seriesDays[d]:
			out = append(out, StoreReportCheck{Check: name, Status: StoreCheckFailed, Period: d, Field: "day", Note: "A raw report exists for this day, but the series has no rows."})
		}
	}
	if len(out) == 0 {
		out = append(out, StoreReportCheck{Check: name, Status: StoreCheckOK, Period: w.period(), Note: "Every day older than the publishing lag is answered and in the series."})
	}
	return out
}

// ---------------------------------------------------------------------------
// raw daily Sales rows

// salesKind sorts a Product Type Identifier into the kind it counts as.
func salesKind(t string) string {
	t = strings.TrimSpace(t)
	switch t {
	case "1", "1F", "1T", "F1", "1E", "1EP", "1EU", "1-B", "F1-B":
		return "first_downloads"
	case "3", "3F", "3T", "F3":
		return "redownloads"
	case "7", "7F", "7T", "F7":
		return "updates"
	}
	base := strings.TrimSuffix(t, "-M")
	switch base {
	case "IA1", "IA9", "IAY", "IAC", "FI1":
		return "iap_units"
	}
	return "other_units"
}

var salesKinds = []string{"first_downloads", "redownloads", "updates", "iap_units", "other_units"}

type rawSalesRow struct {
	day, kind, appleID, parent, sku, currency string
	units, proceeds                           float64 // proceeds per unit as Apple prints it
}

// rawSales is every raw daily Sales row, newest capture per day.
type rawSales struct {
	days    map[string][]rawSalesRow
	skuApp  map[string]string // app SKU -> app id
	iapApp  map[string]string // in-app purchase id -> parent app id
	readErr []string
}

var salesDayRE = regexp.MustCompile(`(\d{4}-\d{2}-\d{2})\.tsv$`)

// loadRawSales reads the newest raw TSV per day for the Sales report's file
// capture. SKU and parent maps cover every day read, because an in-app sale's
// parent app may have no row of its own that day.
func loadRawSales(homePath string, r StoreReport) (*rawSales, error) {
	out := &rawSales{days: map[string][]rawSalesRow{}, skuApp: map[string]string{}, iapApp: map[string]string{}}
	if r.Capture.Files == nil {
		return out, nil
	}
	newest := map[string]string{}
	for _, g := range r.Capture.Files.Globs {
		matches, err := filepath.Glob(filepath.Join(homePath, filepath.FromSlash(g)))
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			sub := salesDayRE.FindStringSubmatch(filepath.Base(m))
			if sub == nil {
				continue
			}
			if m > newest[sub[1]] {
				newest[sub[1]] = m
			}
		}
	}
	parentSKU := map[string]string{}
	for _, day := range growthSortedKeys(newest) {
		raw, err := os.ReadFile(newest[day])
		if err != nil {
			out.readErr = append(out.readErr, day+": "+err.Error())
			continue
		}
		rows, err := parseRawSalesTSV(raw, day)
		if err != nil {
			out.readErr = append(out.readErr, day+": "+err.Error())
			continue
		}
		out.days[day] = rows
		for _, row := range rows {
			if row.parent == "" {
				out.skuApp[row.sku] = row.appleID
			} else {
				parentSKU[row.appleID] = row.parent
			}
		}
	}
	for iap, sku := range parentSKU {
		if app, ok := out.skuApp[sku]; ok {
			out.iapApp[iap] = app
		}
	}
	return out, nil
}

func parseRawSalesTSV(raw []byte, day string) ([]rawSalesRow, error) {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("empty file")
	}
	col := map[string]int{}
	for i, h := range strings.Split(lines[0], "\t") {
		col[strings.TrimSpace(h)] = i
	}
	for _, need := range []string{"Product Type Identifier", "Units", "Developer Proceeds", "Currency of Proceeds", "Apple Identifier", "Parent Identifier", "SKU"} {
		if _, ok := col[need]; !ok {
			return nil, fmt.Errorf("missing column %q", need)
		}
	}
	get := func(cells []string, name string) string {
		if i := col[name]; i < len(cells) {
			return strings.TrimSpace(cells[i])
		}
		return ""
	}
	var out []rawSalesRow
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cells := strings.Split(line, "\t")
		units, err := strconv.ParseFloat(get(cells, "Units"), 64)
		if err != nil {
			return nil, fmt.Errorf("units %q: %w", get(cells, "Units"), err)
		}
		proceeds := 0.0
		if p := get(cells, "Developer Proceeds"); p != "" {
			if proceeds, err = strconv.ParseFloat(p, 64); err != nil {
				return nil, fmt.Errorf("developer proceeds %q: %w", p, err)
			}
		}
		out = append(out, rawSalesRow{
			day: day, kind: salesKind(get(cells, "Product Type Identifier")), appleID: get(cells, "Apple Identifier"),
			parent: get(cells, "Parent Identifier"), sku: get(cells, "SKU"), currency: get(cells, "Currency of Proceeds"),
			units: units, proceeds: proceeds,
		})
	}
	return out, nil
}

// app is the app a raw row belongs to: an in-app row's parent through the
// report's own Parent Identifier, else the row's Apple Identifier.
func (rs *rawSales) app(row rawSalesRow) string {
	if row.parent != "" {
		if app, ok := rs.skuApp[row.parent]; ok {
			return app
		}
	}
	return row.appleID
}

// money is a row's signed proceeds: Apple prints proceeds per unit, and a
// refund row has negative units.
func (row rawSalesRow) money() float64 {
	if row.units < 0 {
		return -math.Abs(row.proceeds) * math.Abs(row.units)
	}
	return row.proceeds * math.Abs(row.units)
}

// ---------------------------------------------------------------------------
// series_equals_raw

type salesCell struct {
	kinds    map[string]float64
	units    float64
	proceeds map[string]float64
}

type seriesSalesRow struct {
	kinds    map[string]*float64
	units    *float64
	proceeds map[string]float64
	hasPBC   bool
}

func readSeriesSales(path string) (map[string]map[string]seriesSalesRow, error) {
	out := map[string]map[string]seriesSalesRow{} // day -> app -> row
	err := readJSONL(path, func(line []byte) error {
		var m map[string]json.RawMessage
		if json.Unmarshal(line, &m) != nil {
			return nil
		}
		app, day := jsonScalar(m, "apple_identifier"), jsonScalar(m, "date")
		if app == "" {
			return nil
		}
		if _, ok := parseGrowthDay(day); !ok {
			return nil
		}
		row := seriesSalesRow{kinds: map[string]*float64{}}
		num := func(k string) *float64 {
			raw, ok := m[k]
			if !ok {
				return nil
			}
			var v float64
			if json.Unmarshal(raw, &v) != nil {
				return nil
			}
			return &v
		}
		row.units = num("units")
		for _, k := range salesKinds {
			row.kinds[k] = num(k)
		}
		if raw, ok := m["proceeds_by_currency"]; ok && json.Unmarshal(raw, &row.proceeds) == nil {
			row.hasPBC = true
		}
		if out[day] == nil {
			out[day] = map[string]seriesSalesRow{}
		}
		out[day][app] = row
		return nil
	})
	return out, err
}

// checkSeriesEqualsRaw compares, per app and day in the window, every unit
// kind and proceeds per currency in the series with sums of the raw rows.
func checkSeriesEqualsRaw(homePath string, s *storeReportScan, rs *rawSales, w storeWindow, link func(string) string) ([]StoreReportCheck, error) {
	const name = "series_equals_raw"
	sales, _ := s.cat.report(storeCheckSalesReport)
	f := s.found[storeCheckSalesReport]
	if f == nil || !f.captured || sales.Capture.Series == nil {
		return []StoreReportCheck{{Check: name, Status: StoreCheckNotRun, Period: w.period(), Note: "The Sales series is not captured."}}, nil
	}
	if len(rs.days) == 0 {
		return []StoreReportCheck{{Check: name, Status: StoreCheckNotRun, Period: w.period(), Note: "No raw Sales files were found."}}, nil
	}
	series, err := readSeriesSales(filepath.Join(homePath, filepath.FromSlash(sales.Capture.Series.Path)))
	if err != nil {
		return nil, err
	}
	failed := map[string]bool{}
	apps := map[string]bool{}
	var out []StoreReportCheck
	for _, day := range growthSortedKeys(rs.days) {
		if !w.has(day) {
			continue
		}
		raw := map[string]*salesCell{}
		for _, row := range rs.days[day] {
			app := rs.app(row)
			c := raw[app]
			if c == nil {
				c = &salesCell{kinds: map[string]float64{}, proceeds: map[string]float64{}}
				raw[app] = c
			}
			c.units += row.units
			c.kinds[row.kind] += row.units
			if row.proceeds != 0 {
				c.proceeds[row.currency] += row.money()
			}
		}
		keys := map[string]bool{}
		for app := range raw {
			keys[app] = true
		}
		for app := range series[day] {
			keys[app] = true
		}
		for _, app := range growthSortedKeys(keys) {
			apps[app] = true
			c, sr := raw[app], series[day][app]
			_, inSeries := series[day][app]
			fail := func(field string, expected, actual float64, note string) {
				out = append(out, storeCheckDiff(name, link(app), app, day, field, expected, actual, note))
				failed[app] = true
			}
			switch {
			case c == nil:
				fail("units", 0, deref(sr.units), "The series has a row the raw report does not: no raw row belongs to this app id on this day.")
				continue
			case !inSeries:
				fail("units", c.units, 0, "The raw report has rows for this app, but the series has none.")
				continue
			}
			if sr.units != nil && !storeNear(*sr.units, c.units) {
				fail("units", c.units, *sr.units, "")
			}
			for _, k := range salesKinds {
				if v := sr.kinds[k]; v != nil && !storeNear(*v, c.kinds[k]) {
					fail(k, c.kinds[k], *v, "")
				}
			}
			if sr.hasPBC {
				cur := map[string]bool{}
				for k := range c.proceeds {
					cur[k] = true
				}
				for k := range sr.proceeds {
					cur[k] = true
				}
				for _, k := range growthSortedKeys(cur) {
					if !storeNearMoney(sr.proceeds[k], c.proceeds[k]) {
						fail("proceeds:"+k, c.proceeds[k], sr.proceeds[k], "")
					}
				}
			}
		}
	}
	for _, app := range growthSortedKeys(apps) {
		if !failed[app] {
			out = append(out, StoreReportCheck{Check: name, Status: StoreCheckOK, ProductID: link(app), AppID: app, Period: w.period(), Note: "Every day's unit kinds and proceeds per currency equal the raw rows."})
		}
	}
	return out, nil
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func storeNear(a, b float64) bool      { return math.Abs(a-b) < 1e-6 }
func storeNearMoney(a, b float64) bool { return math.Abs(a-b) < 0.005 }

// ---------------------------------------------------------------------------
// sales_equals_finance

type financeMonth struct {
	label      string
	start, end string
	sums       map[string][2]float64 // app|currency -> units, money
}

// loadFinanceMonths reads the newest Finance file per fiscal label.
func loadFinanceMonths(homePath string, r StoreReport, rs *rawSales) ([]financeMonth, []string, error) {
	if r.Capture.Files == nil {
		return nil, nil, nil
	}
	re := regexp.MustCompile(r.Capture.Files.PeriodPattern)
	newest := map[string]string{}
	for _, g := range r.Capture.Files.Globs {
		matches, err := filepath.Glob(filepath.Join(homePath, filepath.FromSlash(g)))
		if err != nil {
			return nil, nil, err
		}
		for _, m := range matches {
			if sub := re.FindStringSubmatch(filepath.Base(m)); sub != nil && m > newest[sub[1]] {
				newest[sub[1]] = m
			}
		}
	}
	var out []financeMonth
	var problems []string
	for _, label := range growthSortedKeys(newest) {
		fm, err := readFinanceFile(newest[label], label, rs)
		if err != nil {
			problems = append(problems, label+": "+err.Error())
			continue
		}
		out = append(out, fm)
	}
	return out, problems, nil
}

func readFinanceFile(path, label string, rs *rawSales) (financeMonth, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return financeMonth{}, err
	}
	if strings.HasSuffix(path, ".gz") {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return financeMonth{}, err
		}
		if raw, err = io.ReadAll(zr); err != nil {
			return financeMonth{}, err
		}
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	fm := financeMonth{label: label, sums: map[string][2]float64{}}
	if span, ok := appleFiscalPeriod(label); ok {
		fm.start, fm.end = growthDayStr(span.start), growthDayStr(span.end)
	}
	if len(lines) == 0 {
		return fm, nil
	}
	head := strings.Split(lines[0], "\t")
	col := map[string]int{}
	for i, h := range head {
		col[strings.TrimSpace(h)] = i
	}
	for _, need := range []string{"Start Date", "End Date", "Quantity", "Extended Partner Share", "Partner Share Currency", "Apple Identifier"} {
		if _, ok := col[need]; !ok {
			return financeMonth{}, fmt.Errorf("missing column %q", need)
		}
	}
	apps := map[string]bool{}
	for _, a := range rs.skuApp {
		apps[a] = true
	}
	for _, line := range lines[1:] {
		cells := strings.Split(line, "\t")
		if len(cells) != len(head) {
			continue // the trailer: totals and per-country lines
		}
		get := func(name string) string { return strings.TrimSpace(cells[col[name]]) }
		if s, ok := financeDate(get("Start Date")); ok {
			fm.start = s
		}
		if e, ok := financeDate(get("End Date")); ok {
			fm.end = e
		}
		q, err1 := strconv.ParseFloat(get("Quantity"), 64)
		m, err2 := strconv.ParseFloat(get("Extended Partner Share"), 64)
		if err1 != nil || err2 != nil {
			return financeMonth{}, fmt.Errorf("bad number in row")
		}
		id := get("Apple Identifier")
		app := id
		if !apps[id] {
			if parent, ok := rs.iapApp[id]; ok {
				app = parent
			}
		}
		key := app + "|" + get("Partner Share Currency")
		v := fm.sums[key]
		v[0] += q
		v[1] += m
		fm.sums[key] = v
	}
	return fm, nil
}

func financeDate(s string) (string, bool) {
	t, err := time.Parse("01/02/2006", s)
	if err != nil {
		return "", false
	}
	return growthDayStr(t), true
}

// paidSums sums raw paid rows (nonzero proceeds) per app|currency over days
// from..to, and per day.
func (rs *rawSales) paidSums(from, to string) (map[string][2]float64, map[string]map[string][2]float64) {
	total := map[string][2]float64{}
	byDay := map[string]map[string][2]float64{}
	for day, rows := range rs.days {
		if day < from || day > to {
			continue
		}
		for _, row := range rows {
			if row.proceeds == 0 {
				continue
			}
			key := rs.app(row) + "|" + row.currency
			v := total[key]
			v[0] += row.units
			v[1] += row.money()
			total[key] = v
			if byDay[key] == nil {
				byDay[key] = map[string][2]float64{}
			}
			d := byDay[key][day]
			d[0] += row.units
			d[1] += row.money()
			byDay[key][day] = d
		}
	}
	return total, byDay
}

// checkSalesEqualsFinance compares, per app, currency and Apple fiscal month,
// the daily Sales sums with the Finance report. The only difference allowed is
// a carry-over: sales in a month's last days that land in the next month's
// Finance report.
func checkSalesEqualsFinance(homePath string, s *storeReportScan, rs *rawSales, link func(string) string) ([]StoreReportCheck, error) {
	const name = "sales_equals_finance"
	fin, _ := s.cat.report(storeCheckFinanceReport)
	if f := s.found[storeCheckFinanceReport]; f == nil || !f.captured {
		return []StoreReportCheck{{Check: name, Status: StoreCheckNotRun, Note: "The Finance report is not captured."}}, nil
	}
	if len(rs.days) == 0 {
		return []StoreReportCheck{{Check: name, Status: StoreCheckNotRun, Note: "No raw Sales files were found."}}, nil
	}
	months, problems, err := loadFinanceMonths(homePath, fin, rs)
	if err != nil {
		return nil, err
	}
	var out []StoreReportCheck
	for _, p := range problems {
		out = append(out, StoreReportCheck{Check: name, Status: StoreCheckNotRun, Period: strings.SplitN(p, ":", 2)[0], Note: "Finance file unreadable: " + p})
	}
	rawFirst := growthSortedKeys(rs.days)[0]
	carried := map[string]carry{} // key -> what the last checked month carried out
	checkedLabel := ""            // the last month checked
	for i, m := range months {
		if m.start == "" || m.end == "" || m.start < rawFirst {
			out = append(out, StoreReportCheck{Check: name, Status: StoreCheckNotRun, Period: m.label, Note: "The raw Sales files do not cover this fiscal month."})
			continue
		}
		prevChecked := checkedLabel != "" && checkedLabel == storePrevLabel(m.label)
		checkedLabel = m.label
		hasNext := i+1 < len(months) && months[i+1].label == storeNextLabel(m.label)
		sales, byDay := rs.paidSums(m.start, m.end)
		prevSpan, _ := appleFiscalPeriod(storePrevLabel(m.label))
		_, prevByDay := rs.paidSums(growthDayStr(prevSpan.end.AddDate(0, 0, -(storeCheckCarryDays-1))), growthDayStr(prevSpan.end))
		lastFrom := growthDayStr(mustDay(m.end).AddDate(0, 0, -(storeCheckCarryDays - 1)))
		keys := map[string]bool{}
		for k := range sales {
			keys[k] = true
		}
		for k := range m.sums {
			keys[k] = true
		}
		next := map[string]carry{}
		for _, key := range growthSortedKeys(keys) {
			app, cur, _ := strings.Cut(key, "|")
			product := link(app)
			s, f := sales[key], m.sums[key]
			// Candidate carry-outs: subsets of this month's last days.
			var lastDays []string
			for d := range byDay[key] {
				if d >= lastFrom {
					lastDays = append(lastDays, d)
				}
			}
			sort.Strings(lastDays)
			// Candidate carry-ins: what the previous month carried out, or,
			// when that month was not checked, subsets of its last days.
			var inOptions []carry
			if prevChecked {
				inOptions = []carry{carried[key]} // empty when nothing was carried
			} else {
				var prevDays []string
				for d := range prevByDay[key] {
					prevDays = append(prevDays, d)
				}
				sort.Strings(prevDays)
				inOptions = storeDaySubsets(prevDays, prevByDay[key])
			}
			outOptions := storeDaySubsets(lastDays, byDay[key])
			// The fewest days explain it best; between equal sets, the later
			// days (the last days of a month are the ones that carry over).
			best, bestIn, found := carry{}, carry{}, false
			for ii := len(inOptions) - 1; ii >= 0; ii-- {
				in := inOptions[ii]
				for oi := len(outOptions) - 1; oi >= 0; oi-- {
					o := outOptions[oi]
					u := s[0] - o.sum[0] + in.sum[0]
					mo := s[1] - o.sum[1] + in.sum[1]
					if storeNear(u, f[0]) && math.Abs(mo-f[1]) < 0.005*float64(1+len(o.days)+len(in.days)) {
						if !found || len(o.days)+len(in.days) < len(best.days)+len(bestIn.days) {
							best, bestIn, found = o, in, true
						}
					}
				}
			}
			r := StoreReportCheck{Check: name, ProductID: product, AppID: app, Period: m.label + " (" + m.start + ".." + m.end + ")", Field: "proceeds:" + cur,
				Expected: storeCheckNum(f[1]), Actual: storeCheckNum(s[1]), Difference: storeCheckNum(s[1] - f[1])}
			units := fmt.Sprintf("units: sales %s, finance %s.", growthNum(s[0]), growthNum(f[0]))
			switch {
			case !found:
				r.Status, r.Note = StoreCheckFailed, "Sales and Finance differ beyond a carry-over; "+units
			case len(best.days) > 0 && !hasNext:
				r.Status = StoreCheckNotRun
				r.Note = fmt.Sprintf("Sales on %s may land in %s's Finance report, which is not captured yet; %s", strings.Join(best.days, ", "), storeNextLabel(m.label), units)
			default:
				r.Status = StoreCheckOK
				var notes []string
				if len(bestIn.days) > 0 {
					notes = append(notes, "includes sales from "+strings.Join(bestIn.days, ", ")+" of the month before (carry-over)")
				}
				if len(best.days) > 0 {
					notes = append(notes, "sales on "+strings.Join(best.days, ", ")+" are carried to "+storeNextLabel(m.label))
				}
				if len(notes) == 0 {
					r.Note = "Equal; " + units
				} else {
					r.Note = "Equal after carry-over: " + strings.Join(notes, "; ") + ". " + units
				}
			}
			out = append(out, r)
			if found {
				next[key] = best
			}
		}
		carried = next
	}
	return out, nil
}

func mustDay(s string) time.Time { t, _ := parseGrowthDay(s); return t }

// storeDaySubsets lists every subset of days (at most storeCheckCarryDays)
// with its summed units and money, the empty subset first.
func storeDaySubsets(days []string, byDay map[string][2]float64) []carry {
	out := []carry{{}}
	if len(days) > storeCheckCarryDays {
		days = days[len(days)-storeCheckCarryDays:]
	}
	for mask := 1; mask < 1<<len(days); mask++ {
		var c carry
		for i, d := range days {
			if mask&(1<<i) != 0 {
				c.days = append(c.days, d)
				v := byDay[d]
				c.sum[0] += v[0]
				c.sum[1] += v[1]
			}
		}
		out = append(out, c)
	}
	return out
}

// carry is a set of days and their summed paid units and money.
type carry struct {
	days []string
	sum  [2]float64
}

func storeNextLabel(label string) string {
	t, err := time.Parse("2006-01", label)
	if err != nil {
		return ""
	}
	return t.AddDate(0, 1, 0).Format("2006-01")
}

func storePrevLabel(label string) string {
	t, err := time.Parse("2006-01", label)
	if err != nil {
		return ""
	}
	return t.AddDate(0, -1, 0).Format("2006-01")
}

// ---------------------------------------------------------------------------
// downloads_agree

// checkDownloadsAgree compares, per app and 7-day block of final days, the
// analytics first-time downloads with the Sales report's first downloads, on
// the days analytics observed. Analytics adds noise, so a small difference is
// allowed: the larger of 3 and 10%.
func checkDownloadsAgree(homePath string, s *storeReportScan, w storeWindow, link func(string) string) ([]StoreReportCheck, error) {
	const name = "downloads_agree"
	sales, _ := s.cat.report(storeCheckSalesReport)
	dl, _ := s.cat.report(storeCheckDownloadsReport)
	fs, fd := s.found[storeCheckSalesReport], s.found[storeCheckDownloadsReport]
	if fs == nil || !fs.captured || fd == nil || !fd.captured || sales.Capture.Series == nil {
		return []StoreReportCheck{{Check: name, Status: StoreCheckNotRun, Period: w.period(), Note: "The Sales series or the analytics downloads are not captured."}}, nil
	}
	points, err := loadGrowthASCFunnelSeries(homePath)
	if err != nil {
		return nil, err
	}
	analytics := map[string]map[string]float64{} // app -> day -> value
	for _, p := range points {
		if p.metric != "app_store_connect_first_time_downloads" {
			continue
		}
		if analytics[p.appID] == nil {
			analytics[p.appID] = map[string]float64{}
		}
		analytics[p.appID][p.date] = p.v
	}
	series, err := readSeriesSales(filepath.Join(homePath, filepath.FromSlash(sales.Capture.Series.Path)))
	if err != nil {
		return nil, err
	}
	lag := max(sales.PublishedAfter.LagDays, dl.PublishedAfter.LagDays)
	end := w.asOf.AddDate(0, 0, -(lag + 1))
	start := mustDay(w.start)
	var out []StoreReportCheck
	for _, app := range growthSortedKeys(analytics) {
		for blockEnd := end; !blockEnd.Before(start.AddDate(0, 0, 6)); blockEnd = blockEnd.AddDate(0, 0, -7) {
			blockStart := blockEnd.AddDate(0, 0, -6)
			var a, sv float64
			n, unknownKinds := 0, false
			for t := blockStart; !t.After(blockEnd); t = t.AddDate(0, 0, 1) {
				d := growthDayStr(t)
				av, ok := analytics[app][d]
				if !ok || fs.answered[d] == "" {
					continue
				}
				row, has := series[d][app]
				if has && row.kinds["first_downloads"] == nil {
					unknownKinds = true
					continue
				}
				n++
				a += av
				if has {
					sv += *row.kinds["first_downloads"]
				}
			}
			period := growthDayStr(blockStart) + ".." + growthDayStr(blockEnd)
			if n == 0 {
				if unknownKinds {
					out = append(out, StoreReportCheck{Check: name, Status: StoreCheckNotRun, ProductID: link(app), AppID: app, Period: period, Field: "first_time_downloads", Note: "The Sales series has no download kinds for these days."})
				}
				continue
			}
			tol := math.Max(storeCheckDownloadsAbs, storeCheckDownloadsRel*math.Max(a, sv))
			r := StoreReportCheck{Check: name, ProductID: link(app), AppID: app, Period: period, Field: "first_time_downloads",
				Expected: storeCheckNum(sv), Actual: storeCheckNum(a), Difference: storeCheckNum(a - sv)}
			note := fmt.Sprintf("Sales %s, analytics %s, on %s analytics observed; allowed difference %s.", growthNum(sv), growthNum(a), growthDays(n), growthNum(math.Round(tol*100)/100))
			if math.Abs(a-sv) <= tol {
				r.Status, r.Note = StoreCheckOK, note
			} else {
				r.Status, r.Note = StoreCheckFailed, note
			}
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		out = append(out, StoreReportCheck{Check: name, Status: StoreCheckNotRun, Period: w.period(), Note: "No 7-day block has analytics downloads and final Sales days."})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// running them

// runStoreReportChecks runs the fast check, and the three raw checks when
// full is set; otherwise those report not_run.
func runStoreReportChecks(homePath string, s *storeReportScan, w storeWindow, full bool, link func(string) string) ([]StoreReportCheck, error) {
	out := checkDaysFinal(s, w)
	if !full {
		for _, name := range storeReportCheckNames[:3] {
			out = append(out, StoreReportCheck{Check: name, Status: StoreCheckNotRun, Period: w.period(), Note: "Reads raw report files; run with --check."})
		}
		return sortStoreChecks(out), nil
	}
	sales, _ := s.cat.report(storeCheckSalesReport)
	rs, err := loadRawSales(homePath, sales)
	if err != nil {
		return nil, err
	}
	for _, p := range rs.readErr {
		out = append(out, StoreReportCheck{Check: "series_equals_raw", Status: StoreCheckNotRun, Period: strings.SplitN(p, ":", 2)[0], Note: "Raw Sales file unreadable: " + p})
	}
	ser, err := checkSeriesEqualsRaw(homePath, s, rs, w, link)
	if err != nil {
		return nil, err
	}
	fin, err := checkSalesEqualsFinance(homePath, s, rs, link)
	if err != nil {
		return nil, err
	}
	dl, err := checkDownloadsAgree(homePath, s, w, link)
	if err != nil {
		return nil, err
	}
	out = append(append(append(out, ser...), fin...), dl...)
	return sortStoreChecks(out), nil
}

// sortStoreChecks orders results: failures first, then by check, app and
// period, so the same input gives the same bytes.
func sortStoreChecks(in []StoreReportCheck) []StoreReportCheck {
	rank := map[string]int{StoreCheckFailed: 0, StoreCheckNotRun: 1, StoreCheckOK: 2}
	order := map[string]int{}
	for i, n := range storeReportCheckNames {
		order[n] = i
	}
	sort.SliceStable(in, func(i, j int) bool {
		a, b := in[i], in[j]
		if rank[a.Status] != rank[b.Status] {
			return rank[a.Status] < rank[b.Status]
		}
		if order[a.Check] != order[b.Check] {
			return order[a.Check] < order[b.Check]
		}
		if a.ProductID != b.ProductID {
			return a.ProductID < b.ProductID
		}
		if a.AppID != b.AppID {
			return a.AppID < b.AppID
		}
		if a.Period != b.Period {
			return a.Period < b.Period
		}
		return a.Field < b.Field
	})
	return in
}

// countStoreChecks counts, per check, one result for one product: failed if
// any of its results failed, ok if any passed, else not run. Account-wide
// results (no app id) count for every product.
func countStoreChecks(checks []StoreReportCheck, productID string) StoreReportCheckCounts {
	var c StoreReportCheckCounts
	for _, name := range storeReportCheckNames {
		ok, failed := false, false
		for _, r := range checks {
			if r.Check != name || (r.AppID != "" && r.ProductID != productID) {
				continue
			}
			switch r.Status {
			case StoreCheckFailed:
				failed = true
			case StoreCheckOK:
				ok = true
			}
		}
		switch {
		case failed:
			c.Failed++
		case ok:
			c.OK++
		default:
			c.NotRun++
		}
	}
	return c
}
