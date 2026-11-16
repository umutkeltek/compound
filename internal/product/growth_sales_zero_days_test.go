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

// The Sales report never holds a zero row, so a zero row in the ledger was
// written by an older import and does not show the report lists the app. An
// app known only from such rows stays unknown, and its advice says no Sales
// report lists it, never "refresh". A ledger row above zero does show the
// report listed the app: its quiet days from then on are zero.
func TestGrowthSalesZeroDaysCoverAppsKnownFromTheLedger(t *testing.T) {
	home := t.TempDir()
	applyGrowthBundle(t, home, "product:unlisted-app", iosProfile)
	applyGrowthBundle(t, home, "product:sold-app", iosProfile)
	applyGrowthBundle(t, home, "product:busy-app", iosProfile)
	ledger := []Signal{ascLedgerSignal("sig_busy", "product:busy-app", "900000051", "app_store_connect_review_count", growthDay(200), 1)}
	for off := 80; off >= 50; off-- { // an older import wrote rows until day 50
		ledger = append(ledger,
			ascLedgerSignal("sig_unlisted"+growthDay(off), "product:unlisted-app", "900000052", "app_store_connect_developer_proceeds_usd", growthDay(off), 0))
		v := 0.0
		if off == 70 { // one real sale
			v = 3
		}
		ledger = append(ledger,
			ascLedgerSignal("sig_sold"+growthDay(off), "product:sold-app", "900000053", "app_store_connect_developer_proceeds_usd", growthDay(off), v))
	}
	writeASCLedger(t, home, ledger...)
	yes := true
	var series, index []string
	for off := 90; off >= 1; off-- {
		series = append(series, salesKindsLine("900000051", growthDay(off), 3, 0, 0, 0, 2, &yes))
		index = append(index, salesIndexLine(growthDay(off), "reported", 1))
	}
	writeASCSeries(t, home, series...)
	writeASCDaysIndex(t, home, index...)

	g := buildGrowthT(t, home, growthOpts())
	unlisted := growthApp(t, g, "product:unlisted-app")
	if p := unlisted.Kpis["proceeds_usd"]; p.Value != nil || p.HasData || p.DaysReported != 0 {
		t.Fatalf("unlisted proceeds = %+v, want unknown: no Sales report lists the app", p)
	}
	if v := growthPoint(t, unlisted.Series["proceeds_usd"], growthDay(10)); v != nil {
		t.Fatalf("unlisted app day 10 = %v, want missing", *v)
	}
	ue := growthAudit(t, unlisted, "operations_unit_economics")
	if ue.Status == GrowthStale || strings.Contains(ue.NextMove, "Refresh") ||
		!strings.Contains(ue.NextMove, "No App Store Connect Sales report lists this app") || !strings.Contains(ue.NextMove, growthDay(1)) {
		t.Fatalf("unlisted unit economics = %s %q, want a statement that no Sales report lists it", ue.Status, ue.NextMove)
	}
	if got := growthRefreshTitles(unlisted); len(got) != 0 {
		t.Fatalf("unlisted refresh moves = %v, want none", got)
	}

	sold := growthApp(t, g, "product:sold-app")
	if p := sold.Kpis["proceeds_usd"]; p.Value == nil || *p.Value != 0 || p.DaysReported != 30 {
		t.Fatalf("sold proceeds = %+v, want 0 over 30 reported days", p)
	}
	if ue := growthAudit(t, sold, "operations_unit_economics"); ue.Status == GrowthStale || strings.Contains(ue.NextMove, "Refresh") {
		t.Fatalf("sold unit economics = %s %q, want the sales feed current", ue.Status, ue.NextMove)
	}
	if v := growthPoint(t, sold.Series["proceeds_usd"], growthDay(75)); v == nil || *v != 0 {
		t.Fatalf("sold app day 75 (a ledger zero before its first sale) = %v, want the ledger's 0 kept", v)
	}
	if v := growthPoint(t, sold.Series["proceeds_usd"], growthDay(85)); v != nil {
		t.Fatalf("sold app day 85 (before any ledger row) = %v, want missing", *v)
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

// An app that a shared store id also names may have sold under that id, whose
// rows nobody can attribute. Its days without a row of its own are not zero.
func TestGrowthSalesZeroDaysSkipAppsWithASharedID(t *testing.T) {
	home := t.TempDir()
	registerExampleApps(t, home,
		map[string]string{"product:busy-app": "900000071", "product:app-a": "900000072", "product:app-b": "900000073"},
		ascLedgerSignal("sig_shared_a", "product:app-a", "900000074", "app_store_connect_review_count", growthDay(200), 1),
		ascLedgerSignal("sig_shared_b", "product:app-b", "900000074", "app_store_connect_review_count", growthDay(200), 1))
	yes := true
	var series, index []string
	for off := 12; off >= 1; off-- {
		day := []string{salesKindsLine("900000071", growthDay(off), 3, 0, 0, 0, 2, &yes)}
		if off == 12 {
			day = append(day, salesKindsLine("900000072", growthDay(off), 1, 0, 0, 0, 1, &yes))
		}
		if off == 6 { // only the shared id sold
			day = append(day, salesKindsLine("900000074", growthDay(off), 4, 0, 0, 0, 3, &yes))
		}
		series = append(series, day...)
		index = append(index, salesIndexLine(growthDay(off), "reported", len(day)))
	}
	writeASCSeries(t, home, series...)
	writeASCDaysIndex(t, home, index...)

	g := buildGrowthT(t, home, growthOpts())
	a := growthApp(t, g, "product:app-a")
	if v := growthPoint(t, a.Series["units"], growthDay(6)); v != nil {
		t.Fatalf("app-a on the day only the shared id sold = %v, want missing", *v)
	}
	if u := a.Kpis["units"]; u.DaysReported != 1 {
		t.Fatalf("app-a units = %+v, want only its own row's day", u)
	}
	if u := growthApp(t, g, "product:busy-app").Kpis["units"]; u.DaysReported != 12 {
		t.Fatalf("busy app units = %+v, want 12 days", u)
	}
}
