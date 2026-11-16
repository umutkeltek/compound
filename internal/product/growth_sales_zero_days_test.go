package product

import (
	"encoding/json"
	"strings"
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

// An app that sold nothing since the series began has no series row, but the
// ledger holds the older daily rows its import wrote for it. Those rows show
// the account's report lists the app, so its quiet days are zero and its
// sales feed is current, not a feed to refresh.
func TestGrowthSalesZeroDaysCoverAppsKnownFromTheLedger(t *testing.T) {
	home := t.TempDir()
	applyGrowthBundle(t, home, "product:quiet-app", iosProfile)
	applyGrowthBundle(t, home, "product:busy-app", iosProfile)
	ledger := []Signal{ascLedgerSignal("sig_busy", "product:busy-app", "900000051", "app_store_connect_review_count", growthDay(200), 1)}
	for off := 80; off >= 50; off-- { // an older import wrote zeros for the quiet app until day 50
		ledger = append(ledger, ascLedgerSignal("sig_quiet"+growthDay(off), "product:quiet-app", "900000052", "app_store_connect_developer_proceeds_usd", growthDay(off), 0))
	}
	writeASCLedger(t, home, ledger...)
	yes := true
	var series, index []string
	for off := 40; off >= 1; off-- {
		series = append(series, salesKindsLine("900000051", growthDay(off), 3, 0, 0, 0, 2, &yes))
		index = append(index, salesIndexLine(growthDay(off), "reported", 1))
	}
	writeASCSeries(t, home, series...)
	writeASCDaysIndex(t, home, index...)

	app := growthApp(t, buildGrowthT(t, home, growthOpts()), "product:quiet-app")
	if p := app.Kpis["proceeds_usd"]; p.Value == nil || *p.Value != 0 || p.DaysReported != 30 {
		t.Fatalf("proceeds = %+v, want 0 over 30 reported days", p)
	}
	if ue := growthAudit(t, app, "operations_unit_economics"); ue.Status == GrowthStale || strings.Contains(ue.NextMove, "Refresh") {
		t.Fatalf("unit economics = %s %q, want the sales feed current", ue.Status, ue.NextMove)
	}
	if v := growthPoint(t, app.Series["proceeds_usd"], growthDay(45)); v != nil {
		t.Fatalf("a day neither the ledger nor the index covers = %v, want missing", *v)
	}
}

// A days index row that does not count the day's series rows cannot show the
// series is complete for that day, so the day stays missing, never zero.
func TestGrowthSalesZeroDaysNeedTheSeriesRowCount(t *testing.T) {
	home := t.TempDir()
	registerExampleApps(t, home, map[string]string{"product:busy-app": "900000061", "product:small-app": "900000062"})
	yes := true
	var series, index []string
	for off := 12; off >= 1; off-- {
		series = append(series, salesKindsLine("900000061", growthDay(off), 3, 0, 0, 0, 2, &yes))
		n := 1
		if off == 12 {
			series = append(series, salesKindsLine("900000062", growthDay(off), 1, 0, 0, 0, 1, &yes))
			n = 2
		}
		line := salesIndexLine(growthDay(off), "reported", n)
		if off == 5 { // this row does not say how many series rows the day has
			line = `{"date":"` + growthDay(off) + `","status":"reported","final":true}`
		}
		index = append(index, line)
	}
	writeASCSeries(t, home, series...)
	writeASCDaysIndex(t, home, index...)

	small := growthApp(t, buildGrowthT(t, home, growthOpts()), "product:small-app")
	if v := growthPoint(t, small.Series["units"], growthDay(5)); v != nil {
		t.Fatalf("a day without a series row count = %v, want missing", *v)
	}
	if v := growthPoint(t, small.Series["units"], growthDay(4)); v == nil || *v != 0 {
		t.Fatalf("a counted day = %v, want 0", v)
	}
	if u := small.Kpis["units"]; u.DaysReported != 11 {
		t.Fatalf("units = %+v, want 11 of 12 days reported", u)
	}
}
