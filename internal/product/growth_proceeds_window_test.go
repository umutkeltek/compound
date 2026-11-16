package product

import (
	"strings"
	"testing"
)

// The ranking sentence and the proceeds tile restate one number, so they must
// sum the same days: the window that ends on the Sales report's newest final
// day. Days Apple has not finished are in neither, and both carry the dates.
func TestGrowthAllocationProceedsMatchTheTile(t *testing.T) {
	home := t.TempDir()
	registerExampleApps(t, home, map[string]string{"product:example-app": "900000021"})
	yes, no := true, false
	var lines []string
	for off := 100; off >= 0; off-- {
		v, final := 1.0, &yes
		if off <= 2 { // newest days: not final yet, and large
			v, final = 50, &no
		}
		lines = append(lines, salesKindsLine("900000021", growthDay(off), 1, 0, 0, 0, v, final))
	}
	writeASCSeries(t, home, lines...)

	g := buildGrowthT(t, home, growthOpts())
	tile := growthApp(t, g, "product:example-app").Kpis["proceeds_usd"]
	// Newest final day is day 3, so the window is days 3..32: 30 x 1.
	if tile.Value == nil || *tile.Value != 30 || tile.WindowStart != growthDay(32) || tile.WindowEnd != growthDay(3) {
		t.Fatalf("proceeds tile = %+v, want 30 over %s..%s", tile, growthDay(32), growthDay(3))
	}
	var al GrowthAllocation
	for _, a := range g.Portfolio.Allocation {
		if a.ProductID == "product:example-app" {
			al = a
		}
	}
	if al.Focus != GrowthFocusGrow || al.WindowStart != tile.WindowStart || al.WindowEnd != tile.WindowEnd {
		t.Fatalf("allocation = %+v, want grow over the tile's window %s..%s", al, tile.WindowStart, tile.WindowEnd)
	}
	// 90 days to day 3 are all final days of 1.
	for _, want := range []string{"$30 proceeds in 30 days", growthDay(32) + " to " + growthDay(3), "($90 in 90 days)"} {
		if !strings.Contains(al.Reason, want) {
			t.Fatalf("reason %q does not say %q", al.Reason, want)
		}
	}
	if total := g.Portfolio.Totals["proceeds_usd"]; total.WindowStart != tile.WindowStart || total.WindowEnd != tile.WindowEnd {
		t.Fatalf("portfolio proceeds window = %s..%s, want the tile's", total.WindowStart, total.WindowEnd)
	}
	if clicks := growthApp(t, g, "product:example-app").Kpis["web_clicks"]; clicks.WindowEnd != growthDay(0) || clicks.WindowStart != growthDay(29) {
		t.Fatalf("a non-store KPI window = %s..%s, want it to end on as-of", clicks.WindowStart, clicks.WindowEnd)
	}
}
