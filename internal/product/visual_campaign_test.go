package product

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/mem"
)

func campaignFixture(t *testing.T) (VisualCampaign, string, time.Time) {
	t.Helper()
	base, now := t.TempDir(), time.Date(2030, 2, 20, 12, 0, 0, 0, time.UTC)
	im := image.NewRGBA(image.Rect(0, 0, 16, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 16; x++ {
			im.Set(x, y, color.RGBA{uint8(x * 10), uint8(y * 8), 90, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, im); err != nil {
		t.Fatal(err)
	}
	file := campaignTestFile(t, base, "source.png", buf.Bytes())
	c := VisualCampaign{SchemaVersion: VisualCampaignSchema, ID: "campaign_game", ProductID: "product:loop-demo", CreatedAt: now.Add(-30 * 24 * time.Hour).Format(time.RFC3339),
		Context:    CampaignContext{Audience: "friends playing together", Problem: "scoring interrupts the game", Value: "stay in the game together", EvidenceRefs: []string{"fixture:verified-product"}, Constraints: []string{"no fictional functionality"}},
		Direction:  CampaignDirection{Concept: "shared game night", Palette: "warm amber and charcoal", Lighting: "soft practical light", Typography: "clear bold sans", Layout: "headline above scene, capture below", Avoid: []string{"generic business stock photo"}},
		Locales:    []CampaignLocale{{Tag: "en-US", Direction: "ltr", Font: "Example Sans"}, {Tag: "ar", Direction: "rtl", Font: "Example Arabic"}},
		Targets:    []CampaignTarget{{ID: "store-portrait", Surface: "synthetic-store", Width: 16, Height: 24, Format: "png", MaxBytes: 1 << 20, RulesRef: "https://platform.example/rules", RulesCheckedAt: now.Add(-24 * time.Hour).Format(time.RFC3339), RulesValidUntil: now.Add(30 * 24 * time.Hour).Format(time.RFC3339)}},
		References: []CampaignReference{{ID: "style", Role: "style", RightsRef: "fixture:owned", File: file}, {ID: "capture-en", Role: "capture", Locale: "en-US", RightsRef: "fixture:owned", File: file}, {ID: "capture-ar", Role: "capture", Locale: "ar", RightsRef: "fixture:owned", File: file}},
		Scenes:     []CampaignScene{{ID: "friends", Purpose: "show the social benefit", Setting: "friends beside a game table", Action: "celebrating a close game", Composition: "faces and hands frame a clear phone", ReferenceIDs: []string{"style"}, Copy: map[string]CampaignCopy{"en-US": {Headline: "Stay in the game", Caption: "Keep score together", CaptureRef: "capture-en"}, "ar": {Headline: "استمتع باللعب", Caption: "تابع النتيجة", CaptureRef: "capture-ar"}}}},
		Experiment: CampaignExperiment{ID: "exp_game", HypothesisID: "hyp_loop_activation_missing", Metric: "experiment_conversion_rate", ControlSetSHA256: strings.Repeat("a", 64), StartsAt: now.Add(-14 * 24 * time.Hour).Format(time.RFC3339), EndsAt: now.Add(-24 * time.Hour).Format(time.RFC3339), MinPerArm: 1000, MinAbsoluteLift: .01, TreatmentShare: .5},
	}
	packet, err := CompileCampaignPrompts(c, base, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range packet.Requests {
		attached := []string{}
		for _, r := range p.Attachments {
			attached = append(attached, r.File.SHA256)
		}
		c.Assets = append(c.Assets, CampaignAsset{Scene: p.Scene, Locale: p.Locale, Target: p.Target, File: file, PromptID: p.ID, Model: "fixture-renderer", RenderReceipt: "fixture:render", AttachedSHA256: attached, Review: CampaignAssetReview{AssetSHA256: file.SHA256, Reviewer: "fixture-reviewer", EvidenceRefs: []string{"fixture:visual-review"}, Coherence: true, CopyAndLayout: true, CaptureFidelity: true, PlatformRules: true, NativeDetail: true}})
	}
	return c, base, now
}

func campaignTestFile(t *testing.T, base, name string, b []byte) CampaignFile {
	t.Helper()
	if err := os.WriteFile(filepath.Join(base, name), b, 0600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	return CampaignFile{Path: name, SHA256: hex.EncodeToString(h[:])}
}

func campaignObservationFixture(t *testing.T, c VisualCampaign, base string, now time.Time) CampaignObservation {
	t.Helper()
	return CampaignObservation{ProductID: c.ProductID, ExperimentID: c.Experiment.ID, CampaignSHA256: campaignDigest(c), ControlSetSHA256: c.Experiment.ControlSetSHA256, Locale: "en-US", Target: "store-portrait", Design: "randomized", AssignmentRef: "fixture:randomized-assignment", RegisteredAt: c.CreatedAt, StartsAt: c.Experiment.StartsAt, EndsAt: c.Experiment.EndsAt, ObservedAt: now.Format(time.RFC3339), Source: campaignTestFile(t, base, "exposure.json", []byte(`{"fixture":"synthetic exposure report"}`)), Unit: "unique_users", Control: CampaignArm{Exposed: 10000, Converted: 2000}, Treatment: CampaignArm{Exposed: 10000, Converted: 2600}}
}

func TestVisualCampaignAdaptsScenesLocksDesignAndAttachesLocalizedCaptures(t *testing.T) {
	c, base, now := campaignFixture(t)
	first, err := CompileCampaignPrompts(c, base, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Requests) != 2 || first.Requests[1].LocaleRules.Direction != "rtl" || first.Requests[1].Attachments[1].ID != "capture-ar" {
		t.Fatalf("locale/attachment packet: %+v", first)
	}
	c.Scenes = append(c.Scenes, c.Scenes[0])
	c.Scenes[1].ID = "quiet-score"
	c.Scenes[1].Setting = "quiet tabletop detail"
	more, err := CompileCampaignPrompts(c, base, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(more.Requests[2].ImagePrompt, "quiet tabletop detail") || !strings.Contains(more.Requests[2].ImagePrompt, c.Direction.Palette) {
		t.Fatal("scene must vary inside locked design")
	}
	c.ProductID = "product:work-demo"
	c.Context.Audience = "independent workers"
	c.Direction.Concept = "calm after a shift"
	c.Scenes = c.Scenes[:1]
	c.Scenes[0].Setting = "a worker sorting receipts at home"
	c.Scenes[0].Action = "closing the workday"
	second, err := CompileCampaignPrompts(c, base, now)
	if err != nil {
		t.Fatal(err)
	}
	if second.BriefSHA256 == first.BriefSHA256 || second.Requests[0].ID == first.Requests[0].ID || !strings.Contains(second.Requests[0].ImagePrompt, "worker sorting receipts") {
		t.Fatal("project adaptation did not change prompt identity")
	}
	check, err := CheckVisualCampaign(c, base, now)
	if err != nil || check.Ready {
		t.Fatalf("changed brief must invalidate previous renders: %+v %v", check, err)
	}
}

func TestVisualCampaignRejectsUnsafeOrIncompleteAssets(t *testing.T) {
	cases := map[string]func(*VisualCampaign, string){
		"missing locale": func(c *VisualCampaign, _ string) { delete(c.Scenes[0].Copy, "ar") },
		"English capture in Arabic": func(c *VisualCampaign, _ string) {
			x := c.Scenes[0].Copy["ar"]
			x.CaptureRef = "capture-en"
			c.Scenes[0].Copy["ar"] = x
		},
		"stale rules":             func(c *VisualCampaign, _ string) { c.Targets[0].RulesValidUntil = c.CreatedAt },
		"wrong resolution":        func(c *VisualCampaign, _ string) { c.Targets[0].Width = 1320 },
		"missing export":          func(c *VisualCampaign, _ string) { c.Assets = c.Assets[:1] },
		"duplicate export":        func(c *VisualCampaign, _ string) { c.Assets = append(c.Assets, c.Assets[0]) },
		"missing attachment":      func(c *VisualCampaign, _ string) { c.Assets[0].AttachedSHA256 = nil },
		"no actual model":         func(c *VisualCampaign, _ string) { c.Assets[0].Model = "" },
		"no native detail review": func(c *VisualCampaign, _ string) { c.Assets[0].Review.NativeDetail = false },
		"stale review":            func(c *VisualCampaign, _ string) { c.Assets[0].Review.AssetSHA256 = strings.Repeat("b", 64) },
		"changed image": func(c *VisualCampaign, base string) {
			if err := os.WriteFile(filepath.Join(base, "source.png"), []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
		},
		"truncated PNG": func(c *VisualCampaign, base string) {
			b, _ := os.ReadFile(filepath.Join(base, "source.png"))
			f := campaignTestFile(t, base, "broken.png", b[:len(b)/2])
			c.Assets[0].File = f
			c.Assets[0].Review.AssetSHA256 = f.SHA256
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c, base, now := campaignFixture(t)
			mutate(&c, base)
			got, err := CheckVisualCampaign(c, base, now)
			if err == nil && got.Ready {
				t.Fatalf("accepted %s", name)
			}
		})
	}
}

func TestVisualCampaignMeasurementRefusesFalseWins(t *testing.T) {
	c, base, now := campaignFixture(t)
	o := campaignObservationFixture(t, c, base, now)
	got, err := MeasureVisualCampaign(c, base, o, base, now)
	if err != nil || got.Decision != "supported_improvement" || got.Interval95[0] <= .01 {
		t.Fatalf("positive fixture: %+v %v", got, err)
	}
	for name, mutate := range map[string]func(*CampaignObservation){
		"sparse":              func(o *CampaignObservation) { o.Control = CampaignArm{100, 20}; o.Treatment = CampaignArm{100, 80} },
		"allocation mismatch": func(o *CampaignObservation) { o.Treatment = CampaignArm{30000, 15000} },
		"confounded":          func(o *CampaignObservation) { o.Confounders = []string{"simultaneous pricing change"} },
		"observational":       func(o *CampaignObservation) { o.Design = "observational" },
		"too early":           func(o *CampaignObservation) { o.ObservedAt = c.Experiment.StartsAt },
		"wrong campaign":      func(o *CampaignObservation) { o.CampaignSHA256 = strings.Repeat("f", 64) },
		"wrong control":       func(o *CampaignObservation) { o.ControlSetSHA256 = strings.Repeat("f", 64) },
		"wrong product":       func(o *CampaignObservation) { o.ProductID = "product:unrelated" },
		"invalid counts":      func(o *CampaignObservation) { o.Treatment.Converted = o.Treatment.Exposed + 1 },
		"late registration":   func(o *CampaignObservation) { o.RegisteredAt = now.Format(time.RFC3339) },
		"wrong source":        func(o *CampaignObservation) { o.Source.SHA256 = strings.Repeat("f", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := o
			mutate(&changed)
			out, err := MeasureVisualCampaign(c, base, changed, base, now)
			if err == nil && out.Decision == "supported_improvement" {
				t.Fatalf("false win: %+v", out)
			}
		})
	}
}

func TestVisualCampaignCLIToExistingProductLearning(t *testing.T) {
	c, base, now := campaignFixture(t)
	o := campaignObservationFixture(t, c, base, now)
	writeJSON := func(name string, v any) string {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		campaignTestFile(t, base, name, b)
		return filepath.Join(base, name)
	}
	cp, op := writeJSON("campaign.json", c), writeJSON("observation.json", o)
	var stdout, stderr bytes.Buffer
	if code := RunCampaignCLI([]string{"check", cp}, &stdout, &stderr, now); code != 0 {
		t.Fatalf("check: %d %s", code, stderr.String())
	}
	stdout.Reset()
	if code := RunCampaignCLI([]string{"measure", cp, op, "--signal"}, &stdout, &stderr, now); code != 0 {
		t.Fatalf("measure: %d %s", code, stderr.String())
	}
	var signal SignalDocument
	if err := json.Unmarshal(stdout.Bytes(), &signal); err != nil {
		t.Fatal(err)
	}
	home := seedLoopModelHome(t)
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ApplySignal(home, store, signal, op, now); err != nil {
		t.Fatal(err)
	}
	if _, err = ApplySignal(home, store, signal, op, now); err != nil {
		t.Fatalf("idempotent import: %v", err)
	}
	rows, err := LoadProductSignals(home, c.ProductID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, s := range rows {
		if s.ID == signal.Signal.ID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one durable signal, got %d", count)
	}
	loop, err := BuildLoopModel(home, LoopOptions{ProductID: c.ProductID, Now: now, Recent: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(loop.Products) != 1 || len(loop.Products[0].SignalsByMetric[c.Experiment.Metric]) != 1 {
		t.Fatal("campaign signal not consumed by product loop")
	}
	if _, err = BuildReviewPlan(home, c.Experiment.HypothesisID, signal.Signal.ID, "", now); err != nil {
		t.Fatalf("hypothesis handoff: %v", err)
	}
	if _, err = BuildGrowth(home, GrowthOptions{ProductID: c.ProductID, AsOf: now}); err != nil {
		t.Fatalf("growth consumer: %v", err)
	}
	changed := signal
	changed.Signal.After = .99
	if _, err = ApplySignal(home, store, changed, op, now); err == nil {
		t.Fatal("same experiment identity accepted changed result")
	}
	t.Logf("2 locales validated; campaign %.12s; control %.1f%% treatment %.1f%%; existing signal %s, hypothesis review and growth consumed", campaignDigest(c), signal.Signal.Before*100, signal.Signal.After*100, signal.Signal.ID)
}
