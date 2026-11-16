package product

import (
	"encoding/json"
	"testing"
)

// salesIndexLine is one days index row: Apple answered for the whole account
// on date, and the series holds rows of that many apps.
func salesIndexLine(date, status string, seriesRows int) string {
	line, _ := json.Marshal(map[string]any{"date": date, "status": status, "final": true, "series_rows": seriesRows})
	return string(line)
}

// A small app has no Sales row on a day it sold nothing, while the report for
// that day exists. Such a day is zero, not missing. A day the report does not
// cover, a day whose series is incomplete, and days before the app's first
// row stay missing.
func TestGrowthSalesDayWithoutAppRowIsZero(t *testing.T) {
	home := t.TempDir()
	registerExampleApps(t, home, map[string]string{"product:busy-app": "900000011", "product:small-app": "900000012"})
	yes := true
	rows := map[string]int{}
	var series []string
	for off := 40; off >= 1; off-- {
		series = append(series, salesKindsLine("900000011", growthDay(off), 3, 0, 0, 0, 2, &yes))
		rows[growthDay(off)]++
	}
	for _, off := range []int{35, 20, 10} { // the small app's first row is day 35
		series = append(series, salesKindsLine("900000012", growthDay(off), 1, 0, 0, 0, 0.5, &yes))
		rows[growthDay(off)]++
	}
	writeASCSeries(t, home, series...)
	var index []string
	for off := 40; off >= 1; off-- {
		d := growthDay(off)
		switch off {
		case 15: // the index does not list this day: nobody knows it
			continue
		case 25: // the index counts more rows than the series holds: incomplete
			index = append(index, salesIndexLine(d, "reported", rows[d]+1))
			continue
		}
		index = append(index, salesIndexLine(d, "reported", rows[d]))
	}
	writeASCDaysIndex(t, home, index...)

	g := buildGrowthT(t, home, growthOpts())
	small := growthApp(t, g, "product:small-app")
	// Window days 1..30 (newest final day is day 1): 30 days less day 15 and day 25.
	for _, key := range []string{"units", "proceeds_usd", "redownloads"} {
		k := small.Kpis[key]
		if k.DaysReported != 28 || k.Value == nil {
			t.Fatalf("%s = %+v, want 28 of 30 days reported", key, k)
		}
	}
	if u := small.Kpis["units"]; *u.Value != 2 {
		t.Fatalf("units = %v, want the two real rows only (zeros add nothing)", *u.Value)
	}
	if v := growthPoint(t, small.Series["units"], growthDay(5)); v == nil || *v != 0 {
		t.Fatalf("a reported day without a row = %v, want 0", v)
	}
	for _, off := range []int{15, 25, 38} {
		if v := growthPoint(t, small.Series["units"], growthDay(off)); v != nil {
			t.Fatalf("day %d = %v, want missing (not in the index, incomplete series, or before the first row)", off, *v)
		}
	}
	// The busy app has a row every day; nothing changes for it.
	if u := growthApp(t, g, "product:busy-app").Kpis["units"]; u.DaysReported != 30 || *u.Value != 90 {
		t.Fatalf("busy app units = %+v, want 90 over 30 days", u)
	}
}

// A series written before the collector split download kinds carries no
// new-download count. A day without a row then fills only what the series
// carries, so new downloads stay unknown instead of turning into zeros.
func TestGrowthSalesZeroDaysNeverInventUncarriedFields(t *testing.T) {
	home := t.TempDir()
	registerExampleApps(t, home, map[string]string{"product:busy-app": "900000011", "product:small-app": "900000012"})
	var series, index []string
	for off := 12; off >= 1; off-- {
		series = append(series, ascSeriesLine("900000011", growthDay(off), 2, 5, 5, 0))
		n := 1
		if off == 12 {
			series = append(series, ascSeriesLine("900000012", growthDay(off), 1, 1, 1, 0))
			n = 2
		}
		index = append(index, salesIndexLine(growthDay(off), "reported", n))
	}
	writeASCSeries(t, home, series...)
	writeASCDaysIndex(t, home, index...)

	small := growthApp(t, buildGrowthT(t, home, growthOpts()), "product:small-app")
	if u := small.Kpis["units"]; u.HasData || u.Value != nil {
		t.Fatalf("units = %+v, want not known: the series never carried new downloads", u)
	}
	if p := small.Kpis["proceeds_usd"]; p.Value == nil || *p.Value != 1 || p.DaysReported != 12 {
		t.Fatalf("proceeds = %+v, want 1 over 12 reported days", p)
	}
}
