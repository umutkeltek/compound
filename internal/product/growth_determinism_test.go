package product

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"compound/internal/candidates"
)

// Same input, same output: the projection built from one home with one
// injected clock is the same bytes every time. Map iteration order is the
// usual way this breaks, so the fixture holds ties a map walk could break
// either way: two activation metrics with the same latest date and
// different values, and two products whose data covers the same areas.
func TestGrowthSameInputSameBytes(t *testing.T) {
	home := t.TempDir()
	applyGrowthBundle(t, home, "product:steady", func(p *Product) {
		p.Profile = ProductProfile{Kinds: []string{"ios_app", "website"}, Objectives: []string{"revenue"}}
		p.Essence = Essence{Audience: "Night-shift nurses", Problem: "Charting by hand", Value: "Charts in minutes"}
		p.Learned = []LearnedFact{{At: growthAsOf.AddDate(0, 0, -5), Fact: "Two of three users finished setup.", EvidenceRef: "fixture:setup", Area: "activation_setup_completion"}}
	})
	var rows []growthRow
	for _, product := range []string{"product:steady", "product:other"} {
		rows = append(rows, growthDaily(product, "app_store_connect_units", 20, 0, 2)...)
		rows = append(rows, growthDaily(product, "app_store_connect_developer_proceeds_usd", 20, 0, 3)...)
		rows = append(rows, growthDaily(product, "gsc_clicks", 20, 0, 5)...)
		rows = append(rows, growthDaily(product, "posthog_activation_done", 6, 2, 7)...)
		rows = append(rows, growthDaily(product, "posthog_onboarding_done", 6, 2, 9)...)
		rows = append(rows, growthDaily(product, "posthog_paywall_views", 6, 2, 4)...)
		rows = append(rows, growthRow{product: product, metric: "app_store_connect_redownloads", date: growthDay(40), value: 1})
	}
	writeGrowthSignals(t, home, rows...)
	var ads []AdRow
	for off := 9; off >= 0; off-- {
		ads = append(ads,
			AdRow{ProductID: "product:steady", Date: growthDay(off), Spend: 2, Conversions: 1, ImportedAt: "2026-11-01T00:00:00Z"},
			AdRow{ProductID: "product:other", Platform: "other", Date: growthDay(off), Spend: 1, Conversions: 1, ImportedAt: "2026-11-01T00:00:00Z"})
	}
	writeGrowthAds(t, home, ads...)
	dir := filepath.Join(home, candidates.InboxDir(candidates.ProductHypothesis))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	h, _ := json.Marshal(HypothesisCandidate{ID: "hyp_1", ProductID: "product:steady", ChangeEvent: "Shorter paywall", Metrics: []string{"posthog_paywall_views"}})
	if err := os.WriteFile(filepath.Join(dir, "hyp_1.json"), h, 0o644); err != nil {
		t.Fatal(err)
	}

	build := func() []byte {
		raw, err := json.Marshal(buildGrowthT(t, home, growthOpts()))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	first := build()
	for i := 0; i < 8; i++ {
		if again := build(); !bytes.Equal(first, again) {
			n := 0
			for n < len(first) && n < len(again) && first[n] == again[n] {
				n++
			}
			from := max(0, n-160)
			t.Fatalf("build %d differs at byte %d:\nfirst %s\nagain %s", i+2, n, first[from:min(len(first), n+80)], again[from:min(len(again), n+80)])
		}
	}
}
