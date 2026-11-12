package product

import (
	"fmt"
	"math"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"compound/internal/mem"
)

// GrowthSchemaVersion is the contract version of the app growth projection.
const GrowthSchemaVersion = "compound.product_growth.v0.2"

const growthBoundary = "read-only projection over product signals, ads rows, hypotheses and reviews; evidence, not truth; writes nothing"

// Area and funnel statuses.
const (
	GrowthMeasured      = "measured"
	GrowthPartial       = "partial"
	GrowthStale         = "stale"
	GrowthBlind         = "blind"
	GrowthNotApplicable = "not_applicable"
)

// Allocation focus classes.
const (
	GrowthFocusGrow    = "grow"
	GrowthFocusFixData = "fix_data"
	GrowthFocusMeasure = "measure"
	GrowthFocusHold    = "hold"
)

// GrowthOptions tunes the projection. Now and AsOf are injected so tests never
// depend on the wall clock.
type GrowthOptions struct {
	ProductID  string
	Now        time.Time
	AsOf       time.Time
	WindowDays int
	SeriesDays int
	StaleDays  int

	// catalog replaces the loaded catalog; tests only.
	catalog *growthAreaCatalog
}

// GrowthArea is one catalog area as the projection publishes it.
type GrowthArea struct {
	ID       string                    `json:"id"`
	Number   int                       `json:"number"`
	Group    string                    `json:"group"`
	Title    string                    `json:"title"`
	Question string                    `json:"question"`
	Inputs   []string                  `json:"inputs"`
	Applies  map[string][]string       `json:"applies"`
	Needs    []string                  `json:"needs"`
	Branch   string                    `json:"branch"`
	Skills   []string                  `json:"skills"`
	Kinds    map[string]GrowthAreaForm `json:"kinds"`
}

// GrowthAreaForm is what an area means for one product kind.
type GrowthAreaForm struct {
	Title    string   `json:"title"`
	Question string   `json:"question"`
	Inputs   []string `json:"inputs"`
	Skills   []string `json:"skills"`
}

type GrowthKpi struct {
	Value        *float64 `json:"value"`
	Previous     *float64 `json:"previous"`
	HasData      bool     `json:"has_data"`
	LatestDate   string   `json:"latest_date"`
	DaysReported int      `json:"days_reported"`
	DaysInWindow int      `json:"days_in_window"`
	Unit         string   `json:"unit"`
}

type GrowthPoint struct {
	Date  string   `json:"date"`
	Value *float64 `json:"value"`
}

type GrowthEssence struct {
	Audience string `json:"audience"`
	Problem  string `json:"problem"`
	Value    string `json:"value"`
}

type GrowthSourceFreshness struct {
	Source     string `json:"source"`
	LatestDate string `json:"latest_date"`
	DaysSince  int    `json:"days_since"`
	Stale      bool   `json:"stale"`
}

type GrowthFreshness struct {
	LatestDate string                  `json:"latest_date"`
	Stale      bool                    `json:"stale"`
	Sources    []GrowthSourceFreshness `json:"sources"`
}

type GrowthFunnelStage struct {
	ID     string   `json:"id"`
	Label  string   `json:"label"`
	Value  *float64 `json:"value"`
	AsOf   string   `json:"as_of"`
	Status string   `json:"status"`
	Note   string   `json:"note"`
}

type GrowthEvidence struct {
	Metric     string `json:"metric"`
	Rows       int    `json:"rows"`
	LatestDate string `json:"latest_date"`
}

type GrowthAudit struct {
	AreaID   string           `json:"area_id"`
	Status   string           `json:"status"`
	Summary  string           `json:"summary"`
	Evidence []GrowthEvidence `json:"evidence"`
	NextMove string           `json:"next_move"`
}

type GrowthCoverage struct {
	Measured      int     `json:"measured"`
	Partial       int     `json:"partial"`
	Stale         int     `json:"stale"`
	Blind         int     `json:"blind"`
	NotApplicable int     `json:"not_applicable"`
	Score         float64 `json:"score"`
}

type GrowthHypotheses struct {
	Open     int `json:"open"`
	Reviewed int `json:"reviewed"`
}

type GrowthNextMove struct {
	AreaID string `json:"area_id"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Why    string `json:"why"`
}

type GrowthApp struct {
	ProductID  string                   `json:"product_id"`
	Name       string                   `json:"name"`
	Aliases    []string                 `json:"aliases"`
	Kind       string                   `json:"kind"`
	Essence    *GrowthEssence           `json:"essence"`
	Profile    GrowthProfile            `json:"profile"`
	Freshness  GrowthFreshness          `json:"freshness"`
	Kpis       map[string]GrowthKpi     `json:"kpis"`
	Series     map[string][]GrowthPoint `json:"series"`
	Funnel     []GrowthFunnelStage      `json:"funnel"`
	Audit      []GrowthAudit            `json:"audit"`
	Coverage   GrowthCoverage           `json:"coverage"`
	Hypotheses GrowthHypotheses         `json:"hypotheses"`
	NextMoves  []GrowthNextMove         `json:"next_moves"`
}

type GrowthAllocation struct {
	ProductID string `json:"product_id"`
	Name      string `json:"name"`
	Rank      int    `json:"rank"`
	Focus     string `json:"focus"`
	Reason    string `json:"reason"`
	Rule      string `json:"rule"`
}

type GrowthPortfolio struct {
	AppCount        int                      `json:"app_count"`
	AppsWithoutData []string                 `json:"apps_without_data"`
	Totals          map[string]GrowthKpi     `json:"totals"`
	Series          map[string][]GrowthPoint `json:"series"`
	Allocation      []GrowthAllocation       `json:"allocation"`
}

type Growth struct {
	SchemaVersion string              `json:"schema_version"`
	GeneratedAt   string              `json:"generated_at"`
	AsOf          string              `json:"as_of"`
	WindowDays    int                 `json:"window_days"`
	SeriesDays    int                 `json:"series_days"`
	StaleDays     int                 `json:"stale_days"`
	Boundary      string              `json:"boundary"`
	ProfileDims   map[string][]string `json:"profile_dimensions"`
	Groups        []GrowthAreaGroup   `json:"groups"`
	Areas         []GrowthArea        `json:"areas"`
	Portfolio     GrowthPortfolio     `json:"portfolio"`
	Apps          []GrowthApp         `json:"apps"`
}

// GrowthKpiKeys lists the KPI keys in contract order.
var GrowthKpiKeys = []string{"proceeds_usd", "units", "first_time_downloads", "web_clicks", "web_impressions", "ad_spend_usd", "ad_conversions", "ad_cpa_usd"}

// GrowthSeriesKeys lists the series keys in contract order.
var GrowthSeriesKeys = []string{"proceeds_usd", "units", "web_clicks", "ad_spend_usd", "ad_conversions"}

var growthKpiUnit = map[string]string{
	"proceeds_usd": "usd", "units": "count", "first_time_downloads": "count", "web_clicks": "count",
	"web_impressions": "count", "ad_spend_usd": "usd", "ad_conversions": "count", "ad_cpa_usd": "usd",
}

// growthKpiMetric maps a KPI key to the daily metric that feeds it.
var growthKpiMetric = map[string]string{
	"proceeds_usd":         "app_store_connect_developer_proceeds_usd",
	"units":                "app_store_connect_units",
	"first_time_downloads": "app_store_connect_first_time_downloads",
	"web_clicks":           "gsc_clicks",
	"web_impressions":      "gsc_impressions",
	"ad_spend_usd":         "ads_spend",
	"ad_conversions":       "ads_conversions",
}

// Pseudo metrics synthesized from non-signal ledgers.
const (
	growthAdsSpend       = "ads_spend"
	growthAdsConversions = "ads_conversions"
	growthAppleAds       = "apple_search_ads_spend"
	growthContentPosts   = "content_posts"
)

// Validate checks the options after defaults are applied.
func (o GrowthOptions) Validate() error {
	if o.WindowDays <= 0 || o.SeriesDays <= 0 || o.StaleDays <= 0 {
		return fmt.Errorf("window, series-days and stale-days must be positive")
	}
	if o.AsOf.IsZero() || o.Now.IsZero() {
		return fmt.Errorf("as-of and now required")
	}
	return nil
}

func normalizeGrowthOptions(o GrowthOptions) GrowthOptions {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	o.Now = o.Now.UTC()
	if o.AsOf.IsZero() {
		o.AsOf = o.Now
	}
	o.AsOf = time.Date(o.AsOf.Year(), o.AsOf.Month(), o.AsOf.Day(), 0, 0, 0, 0, time.UTC)
	if o.WindowDays == 0 {
		o.WindowDays = 30
	}
	if o.SeriesDays == 0 {
		o.SeriesDays = 120
	}
	if o.StaleDays == 0 {
		o.StaleDays = 14
	}
	o.ProductID = strings.TrimSpace(o.ProductID)
	return o
}

// ---------------------------------------------------------------------------
// catalog

// ---------------------------------------------------------------------------
// data model

type growthPV struct {
	v  float64
	at time.Time
}

type growthGroup struct {
	idx        int
	ids        []string
	idSet      map[string]bool
	registered map[string]bool
	rows       map[string]int
	tokens     map[string]bool // lowercased aliases, identifiers, ids, identity keys
	surfaces   string
	essence    *GrowthEssence

	essenceRec      *Essence        // the full essence on file, when any
	profile         ProductProfile  // declared profile facts
	profileDeclared map[string]bool // dimensions with a declared value
	subscriptionIDs bool            // subscription product ids are on file
	learned         []LearnedFact   // learned facts across the group's ids
	claims          map[string]string

	signals map[string]map[string]growthPV // metric -> date -> value (periodic rows)
	ranges  map[string]map[string]growthPV // metric -> end date -> value (range snapshots)
	daily   map[string]map[string]float64  // synthesized + final daily maps by metric
	adRows  map[string]growthPV

	adsRevenueRows, adsRows int
	hyps                    []HypothesisCandidate
	reviews                 []Review
	issues                  int
	canonical               string
}

var (
	growthDailyRE = regexp.MustCompile(`^(?:daily )?snapshot (\d{4}-\d{2}-\d{2})`)
	growthAfterRE = regexp.MustCompile(`after (\d{4}-\d{2}-\d{2})(?:/(\d{4}-\d{2}-\d{2}))?`)
)

func parseGrowthDay(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", s)
	return t, err == nil
}

func growthDayStr(t time.Time) string { return t.UTC().Format("2006-01-02") }

func growthMatch(pattern, metric string) bool {
	ok, err := path.Match(pattern, metric)
	return err == nil && ok
}

func (g *growthGroup) put(m map[string]map[string]growthPV, metric, date string, v float64, at time.Time) {
	byDate := m[metric]
	if byDate == nil {
		byDate = map[string]growthPV{}
		m[metric] = byDate
	}
	if prior, ok := byDate[date]; ok && at.Before(prior.at) {
		return
	}
	byDate[date] = growthPV{v: v, at: at}
}

// ---------------------------------------------------------------------------
// build

// BuildGrowth folds the signal ledger, the daily App Store sales series, ads
// rows, hypotheses, reviews and product registry into the growth projection.
// It reads only; it never opens the memory store for writing.
func BuildGrowth(homePath string, opts GrowthOptions) (Growth, error) {
	opts = normalizeGrowthOptions(opts)
	if err := opts.Validate(); err != nil {
		return Growth{}, err
	}
	cat := opts.catalog
	if cat == nil {
		loaded, err := loadEmbeddedGrowthCatalog()
		if err != nil {
			return Growth{}, fmt.Errorf("growth catalog: %w", err)
		}
		cat = loaded
	}
	store, err := mem.OpenScan(homePath)
	if err != nil {
		return Growth{}, err
	}
	idx, err := loadWhereIndex(store)
	if err != nil {
		return Growth{}, err
	}
	signals, err := loadSignals(homePath)
	if err != nil {
		return Growth{}, err
	}
	adRows, err := LoadAdRows(homePath)
	if err != nil {
		return Growth{}, err
	}
	hyps, err := loadHypotheses(homePath)
	if err != nil {
		return Growth{}, err
	}
	reviews, err := loadReviews(homePath)
	if err != nil {
		return Growth{}, err
	}
	drafts, _ := ListContentDrafts(homePath, "")

	groups, byID := growthGroups(idx)
	resolve := func(id string) *growthGroup {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil
		}
		if g := byID[strings.ToLower(id)]; g != nil {
			return g
		}
		key := identityKey(id)
		for _, g := range groups {
			if g.tokens[key] {
				byID[strings.ToLower(id)] = g
				g.addID(id)
				return g
			}
		}
		g := newGrowthGroup(len(groups))
		g.addID(id)
		groups = append(groups, g)
		byID[strings.ToLower(id)] = g
		return g
	}

	asOfStr := growthDayStr(opts.AsOf)
	for _, s := range signals {
		g := resolve(s.ProductID)
		metric := strings.TrimSpace(s.MetricName)
		if g == nil || metric == "" || strings.HasPrefix(metric, "ads_") {
			continue
		}
		g.rows[strings.ToLower(s.ProductID)]++
		at, perr := time.Parse(time.RFC3339, s.ObservedAt)
		if perr != nil {
			at = time.Time{}
		}
		if m := growthDailyRE.FindStringSubmatch(s.Window); m != nil {
			if m[1] <= asOfStr {
				g.put(g.signals, metric, m[1], s.After, at)
			}
			continue
		}
		date := ""
		if m := growthAfterRE.FindStringSubmatch(s.Window); m != nil {
			date = m[1]
			if m[2] != "" {
				date = m[2]
			}
		} else if len(s.ObservedAt) >= 10 {
			date = s.ObservedAt[:10]
		}
		if _, ok := parseGrowthDay(date); ok && date <= asOfStr {
			g.put(g.ranges, metric, date, s.After, at)
		}
	}

	ascSales, err := loadGrowthASCSalesSeries(homePath)
	if err != nil {
		return Growth{}, err
	}
	applyGrowthASCSales(ascSales, growthASCAppIDs(signals, groups, resolve), asOfStr)

	// Ads: dedupe a re-imported (platform, campaign, date), keep the latest import.
	type adKey struct{ g, platform, campaign, date string }
	latestAd := map[adKey]AdRow{}
	adGroup := map[adKey]*growthGroup{}
	for _, r := range adRows {
		g := resolve(r.ProductID)
		if g == nil || r.Date > asOfStr {
			continue
		}
		if _, ok := parseGrowthDay(r.Date); !ok {
			continue
		}
		k := adKey{fmt.Sprint(g.idx), r.Platform, r.CampaignID, r.Date}
		if prior, ok := latestAd[k]; ok && r.ImportedAt < prior.ImportedAt {
			continue
		}
		latestAd[k] = r
		adGroup[k] = g
	}
	adKeys := make([]adKey, 0, len(latestAd))
	for k := range latestAd {
		adKeys = append(adKeys, k)
	}
	sort.Slice(adKeys, func(i, j int) bool {
		a, b := adKeys[i], adKeys[j]
		if a.g != b.g {
			return a.g < b.g
		}
		if a.platform != b.platform {
			return a.platform < b.platform
		}
		if a.campaign != b.campaign {
			return a.campaign < b.campaign
		}
		return a.date < b.date
	})
	for _, k := range adKeys {
		r, g := latestAd[k], adGroup[k]
		g.rows[strings.ToLower(r.ProductID)]++
		g.adsRows++
		if r.HasRevenue && r.Revenue > 0 {
			g.adsRevenueRows++
		}
		g.addDaily(growthAdsSpend, r.Date, r.Spend)
		g.addDaily(growthAdsConversions, r.Date, r.Conversions)
		if r.Platform == "apple_search_ads" {
			g.addDaily(growthAppleAds, r.Date, r.Spend)
		}
	}

	for _, d := range drafts {
		if d.Post == nil || len(d.Post.PostedAt) < 10 {
			continue
		}
		if g := resolve(d.ProductID); g != nil && d.Post.PostedAt[:10] <= asOfStr {
			g.addDaily(growthContentPosts, d.Post.PostedAt[:10], 1)
		}
	}
	for _, h := range hyps {
		if g := resolve(h.ProductID); g != nil {
			g.hyps = append(g.hyps, h)
		}
	}
	for _, r := range reviews {
		if g := resolve(r.ProductID); g != nil {
			g.reviews = append(g.reviews, r)
		}
	}
	for _, g := range groups {
		seen := map[string]bool{}
		for _, id := range g.ids {
			if issues, err := loadProductSourceIssues(homePath, id); err == nil {
				for _, is := range openProductSourceIssues(issues) {
					if !seen[is.ID] {
						seen[is.ID] = true
						g.issues++
					}
				}
			}
		}
	}

	for _, g := range groups {
		g.finalize(idx)
		for metric, byDate := range g.signals {
			m := g.daily[metric]
			if m == nil {
				m = map[string]float64{}
				g.daily[metric] = m
			}
			for d, pv := range byDate {
				m[d] = pv.v
			}
		}
	}

	if opts.ProductID != "" {
		var picked []*growthGroup
		q := strings.ToLower(opts.ProductID)
		qk := identityKey(opts.ProductID)
		for _, g := range groups {
			if g.tokens[q] || (qk != "" && g.tokens[qk]) {
				picked = append(picked, g)
			}
		}
		if len(picked) == 0 {
			return Growth{}, fmt.Errorf("no product matches %q", opts.ProductID)
		}
		if len(picked) > 1 {
			return Growth{}, fmt.Errorf("%q names more than one product", opts.ProductID)
		}
		groups = picked
	}

	out := Growth{
		SchemaVersion: GrowthSchemaVersion,
		GeneratedAt:   opts.Now.Format(time.RFC3339),
		AsOf:          asOfStr,
		WindowDays:    opts.WindowDays,
		SeriesDays:    opts.SeriesDays,
		StaleDays:     opts.StaleDays,
		Boundary:      growthBoundary,
		ProfileDims:   cat.profileDimensions(),
		Groups:        append([]GrowthAreaGroup{}, cat.Groups...),
		Areas:         cat.publicAreas(),
	}
	ctx := growthCtx{opts: opts, cat: cat}
	type ranked struct {
		app   GrowthApp
		alloc GrowthAllocation
		p90   float64
		c90   float64
	}
	var apps []ranked
	portfolioDaily := map[string]map[string]float64{}
	for _, g := range groups {
		app, facts := ctx.buildApp(g)
		alloc := ctx.allocate(app, facts)
		apps = append(apps, ranked{app, alloc, facts.proceeds90, facts.clicks90})
		for _, key := range GrowthKpiKeys {
			metric, ok := growthKpiMetric[key]
			if !ok {
				continue
			}
			pm := portfolioDaily[metric]
			if pm == nil {
				pm = map[string]float64{}
				portfolioDaily[metric] = pm
			}
			for d, v := range g.daily[metric] {
				pm[d] += v
			}
		}
		if !facts.hasAnyData {
			out.Portfolio.AppsWithoutData = append(out.Portfolio.AppsWithoutData, app.ProductID)
		}
	}
	sort.SliceStable(apps, func(i, j int) bool {
		a, b := apps[i], apps[j]
		if a.p90 != b.p90 {
			return a.p90 > b.p90
		}
		if a.c90 != b.c90 {
			return a.c90 > b.c90
		}
		return a.app.ProductID < b.app.ProductID
	})
	for i := range apps {
		apps[i].alloc.Rank = i + 1
		out.Apps = append(out.Apps, apps[i].app)
		out.Portfolio.Allocation = append(out.Portfolio.Allocation, apps[i].alloc)
	}
	if out.Apps == nil {
		out.Apps = []GrowthApp{}
	}
	if out.Portfolio.Allocation == nil {
		out.Portfolio.Allocation = []GrowthAllocation{}
	}
	if out.Portfolio.AppsWithoutData == nil {
		out.Portfolio.AppsWithoutData = []string{}
	}
	sort.Strings(out.Portfolio.AppsWithoutData)
	out.Portfolio.AppCount = len(out.Apps)
	out.Portfolio.Totals = ctx.kpis(portfolioDaily)
	out.Portfolio.Series = ctx.series(portfolioDaily)
	return out, nil
}

func newGrowthGroup(i int) *growthGroup {
	return &growthGroup{
		idx: i, idSet: map[string]bool{}, registered: map[string]bool{}, rows: map[string]int{}, tokens: map[string]bool{},
		profileDeclared: map[string]bool{}, claims: map[string]string{},
		signals: map[string]map[string]growthPV{}, ranges: map[string]map[string]growthPV{},
		daily: map[string]map[string]float64{},
	}
}

func (g *growthGroup) addID(id string) {
	if g.idSet[id] {
		return
	}
	g.idSet[id] = true
	g.ids = append(g.ids, id)
	g.tokens[strings.ToLower(id)] = true
	if k := identityKey(id); k != "" {
		g.tokens[k] = true
	}
}

func (g *growthGroup) addDaily(metric, date string, v float64) {
	m := g.daily[metric]
	if m == nil {
		m = map[string]float64{}
		g.daily[metric] = m
	}
	m[date] += v
}

// growthGroups seeds one group per where-registry product (its members are the
// spelling variants of one product).
func growthGroups(idx *whereIndex) ([]*growthGroup, map[string]*growthGroup) {
	var groups []*growthGroup
	byID := map[string]*growthGroup{}
	for _, root := range idx.groups() {
		g := newGrowthGroup(len(groups))
		for _, i := range idx.memberIndexes(root) {
			e := idx.entities[i]
			g.addID(e.Slug)
			byID[strings.ToLower(e.Slug)] = g
			for _, a := range e.Aliases {
				g.tokens[strings.ToLower(strings.TrimSpace(a))] = true
			}
			for _, id := range e.Identifiers {
				g.tokens[strings.ToLower(strings.TrimSpace(id))] = true
			}
			if c, ok := idx.claim(e.Slug, "surfaces"); ok {
				g.surfaces += " " + strings.ToLower(c.Object)
			}
			if c, ok := idx.claim(e.Slug, "release_subscription_ids"); ok && strings.TrimSpace(c.Object) != "" {
				g.subscriptionIDs = true
			}
			if e.CreatedAt != "" || len(e.Aliases) > 0 || len(e.Identifiers) > 0 {
				g.registered[e.Slug] = true
			}
		}
		if len(g.ids) > 0 {
			groups = append(groups, g)
		}
	}
	return groups, byID
}

func (g *growthGroup) finalize(idx *whereIndex) {
	sort.Strings(g.ids)
	best := ""
	pick := func(candidates func(string) bool) {
		for _, id := range g.ids {
			if !candidates(id) {
				continue
			}
			if best == "" || g.rows[strings.ToLower(id)] > g.rows[strings.ToLower(best)] {
				best = id
			}
		}
	}
	pick(func(id string) bool { return g.registered[id] })
	if best == "" {
		pick(func(string) bool { return true })
	}
	g.canonical = best
	for _, id := range g.ids {
		facts := idx.claims[id]
		if len(facts) == 0 {
			continue
		}
		rec := essenceRecordFromFacts(id, facts)
		g.learned = append(g.learned, rec.Learned...)
		if hasEssenceFacts(facts) && g.essenceRec == nil {
			e := rec.Essence
			g.essenceRec = &e
		}
		if hasEssenceFacts(facts) && g.essence == nil {
			e := rec.Essence
			if e.Audience != "" || e.Problem != "" || e.Value != "" {
				g.essence = &GrowthEssence{Audience: e.Audience, Problem: e.Problem, Value: e.Value}
			}
		}
		if len(g.profileDeclared) == 0 {
			g.profile, g.profileDeclared = profileFromFacts(facts)
		}
		for predicate, c := range facts {
			if _, seen := g.claims[predicate]; !seen {
				g.claims[predicate] = c.Object
			}
		}
	}
}

func growthHumanize(id string) string {
	id = strings.TrimPrefix(strings.TrimSpace(id), "product:")
	words := strings.FieldsFunc(id, func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	if len(words) == 0 {
		return id
	}
	return strings.Join(words, " ")
}

// ---------------------------------------------------------------------------
// per-app computation

type growthCtx struct {
	opts GrowthOptions
	cat  *growthAreaCatalog
}

type growthFacts struct {
	hasAnyData  bool
	proceeds90  float64
	proceeds30  float64
	clicks90    float64
	adConv90    float64
	salesLatest string
	salesStale  bool
	kind        string
	areaStatus  map[string]string
}

func (c growthCtx) day(offset int) string { return growthDayStr(c.opts.AsOf.AddDate(0, 0, -offset)) }

func (c growthCtx) age(date string) int {
	t, ok := parseGrowthDay(date)
	if !ok {
		return 0
	}
	return int(c.opts.AsOf.Sub(t).Hours() / 24)
}

func growthRound(v float64) float64 { return math.Round(v*10000) / 10000 }

func growthPtr(v float64) *float64 { v = growthRound(v); return &v }

func (c growthCtx) sumRange(m map[string]float64, fromOffset, toOffset int) (sum float64, days int) {
	// offsets are days before as-of, inclusive; from >= to.
	for off := toOffset; off <= fromOffset; off++ {
		if v, ok := m[c.day(off)]; ok {
			sum += v
			days++
		}
	}
	return
}

func latestKey(m map[string]float64) string {
	latest := ""
	for d := range m {
		if d > latest {
			latest = d
		}
	}
	return latest
}

func (c growthCtx) kpiFor(key string, daily map[string]map[string]float64) GrowthKpi {
	k := GrowthKpi{DaysInWindow: c.opts.WindowDays, Unit: growthKpiUnit[key]}
	w := c.opts.WindowDays
	if key == "ad_cpa_usd" {
		spend, conv := daily[growthKpiMetric["ad_spend_usd"]], daily[growthKpiMetric["ad_conversions"]]
		s, sd := c.sumRange(spend, w-1, 0)
		cv, _ := c.sumRange(conv, w-1, 0)
		ps, psd := c.sumRange(spend, 2*w-1, w)
		pc, _ := c.sumRange(conv, 2*w-1, w)
		k.DaysReported = sd
		k.LatestDate = latestKey(spend)
		if sd > 0 && cv > 0 {
			k.Value = growthPtr(s / cv)
			k.HasData = true
		}
		if psd > 0 && pc > 0 {
			k.Previous = growthPtr(ps / pc)
		}
		return k
	}
	m := daily[growthKpiMetric[key]]
	v, d := c.sumRange(m, w-1, 0)
	p, pd := c.sumRange(m, 2*w-1, w)
	k.DaysReported = d
	k.LatestDate = latestKey(m)
	if d > 0 {
		k.Value, k.HasData = growthPtr(v), true
	}
	if pd > 0 {
		k.Previous = growthPtr(p)
	}
	return k
}

func (c growthCtx) kpis(daily map[string]map[string]float64) map[string]GrowthKpi {
	out := map[string]GrowthKpi{}
	for _, key := range GrowthKpiKeys {
		out[key] = c.kpiFor(key, daily)
	}
	return out
}

func (c growthCtx) series(daily map[string]map[string]float64) map[string][]GrowthPoint {
	out := map[string][]GrowthPoint{}
	for _, key := range GrowthSeriesKeys {
		m := daily[growthKpiMetric[key]]
		pts := make([]GrowthPoint, 0, c.opts.SeriesDays)
		for off := c.opts.SeriesDays - 1; off >= 0; off-- {
			d := c.day(off)
			p := GrowthPoint{Date: d}
			if v, ok := m[d]; ok {
				p.Value = growthPtr(v)
			}
			pts = append(pts, p)
		}
		out[key] = pts
	}
	return out
}

type growthEv struct {
	GrowthEvidence
	rows90 int
}

// evidence collects, per matching metric, row counts and the latest date.
func (c growthCtx) evidence(g *growthGroup, globs []string) []growthEv {
	cut := c.day(90)
	names := map[string]bool{}
	for metric := range g.daily {
		names[metric] = true
	}
	for metric := range g.ranges {
		names[metric] = true
	}
	var sorted []string
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	var out []growthEv
	for _, metric := range sorted {
		match := false
		for _, gl := range globs {
			if growthMatch(gl, metric) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		dates := map[string]bool{}
		for d := range g.daily[metric] {
			dates[d] = true
		}
		for d := range g.ranges[metric] {
			dates[d] = true
		}
		ev := growthEv{GrowthEvidence: GrowthEvidence{Metric: metric, Rows: len(dates)}}
		for d := range dates {
			if d > ev.LatestDate {
				ev.LatestDate = d
			}
			if d > cut {
				ev.rows90++
			}
		}
		out = append(out, ev)
	}
	return out
}

// growthCount renders a count with the right singular or plural noun.
func growthCount(v float64, one, many string) string {
	if v == 1 {
		return growthNum(v) + " " + one
	}
	return growthNum(v) + " " + many
}

func growthDays(n int) string { return growthCount(float64(n), "day", "days") }

// growthMetricSource names the source a metric comes from.
func growthMetricSource(metric string) string {
	switch {
	case metric == "app_store_connect_developer_proceeds_usd", metric == "app_store_connect_units",
		metric == "app_store_connect_iap_units", metric == "app_store_connect_app_units":
		return "App Store Connect sales"
	case strings.HasPrefix(metric, "app_store_"), strings.HasPrefix(metric, "app."):
		return "App Store Connect analytics"
	case strings.HasPrefix(metric, "gsc_"):
		return "Search Console"
	case strings.HasPrefix(metric, "ga4_"):
		return "Google Analytics"
	case strings.HasPrefix(metric, "posthog_"):
		return "product usage events"
	case metric == growthAppleAds:
		return "Apple Search Ads import"
	case strings.HasPrefix(metric, "ads_"):
		return "ads import"
	case metric == growthContentPosts:
		return "content post records"
	}
	return metric
}

type growthStaleSource struct{ source, date string }

// staleSources folds stale evidence to one entry per source, newest date each,
// in a stable order.
func staleSources(stale []growthEv) []growthStaleSource {
	by := map[string]string{}
	for _, e := range stale {
		src := growthMetricSource(e.Metric)
		if e.LatestDate > by[src] {
			by[src] = e.LatestDate
		}
	}
	var out []growthStaleSource
	for src, d := range by {
		out = append(out, growthStaleSource{src, d})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].source < out[j].source })
	return out
}

func growthSum(m map[string]float64) float64 {
	t := 0.0
	for _, v := range m {
		t += v
	}
	return t
}

func growthPct(cur, prev *float64) string {
	if cur == nil || prev == nil || *prev == 0 {
		return ""
	}
	p := (*cur - *prev) / *prev * 100
	switch {
	case math.Abs(p) < 0.5:
		return "flat"
	case p > 0:
		return fmt.Sprintf("up %.0f%%", p)
	}
	return fmt.Sprintf("down %.0f%%", -p)
}

func growthNum(v float64) string {
	if v == math.Trunc(v) {
		s := fmt.Sprintf("%.0f", v)
		return growthThousands(s)
	}
	return growthThousands(fmt.Sprintf("%.2f", v))
}

func growthThousands(s string) string {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := b.String()
	if frac != "" {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

func growthUSD(v float64) string { return "$" + growthNum(math.Round(v*100)/100) }

func (c growthCtx) buildApp(g *growthGroup) (GrowthApp, growthFacts) {
	profile := c.profile(g)
	facts := growthFacts{kind: profile.firstKind(), areaStatus: map[string]string{}}
	name := growthHumanize(g.canonical)
	app := GrowthApp{
		ProductID: g.canonical,
		Name:      name,
		Kind:      facts.kind,
		Essence:   g.essence,
		Profile:   profile,
		Aliases:   []string{},
		Kpis:      c.kpis(g.daily),
		Series:    c.series(g.daily),
	}
	for _, id := range g.ids {
		if id != g.canonical {
			app.Aliases = append(app.Aliases, id)
		}
	}
	facts.hasAnyData = len(g.signals) > 0 || len(g.ranges) > 0 || g.adsRows > 0
	facts.proceeds90, _ = c.sumRange(g.daily[growthKpiMetric["proceeds_usd"]], 89, 0)
	facts.proceeds30, _ = c.sumRange(g.daily[growthKpiMetric["proceeds_usd"]], c.opts.WindowDays-1, 0)
	facts.clicks90, _ = c.sumRange(g.daily[growthKpiMetric["web_clicks"]], 89, 0)
	facts.adConv90, _ = c.sumRange(g.daily[growthKpiMetric["ad_conversions"]], 89, 0)

	// freshness
	type src struct {
		name  string
		globs []string
	}
	sources := []src{
		{"app_store_connect", []string{"app_store_connect_*", "app_store_product_page_conversion", "app.*"}},
		{"search_console", []string{"gsc_*"}},
		{"apple_search_ads", []string{growthAppleAds}},
		{"posthog", []string{"posthog_*"}},
		{"ga4", []string{"ga4_*"}},
	}
	app.Freshness.Sources = []GrowthSourceFreshness{}
	for _, s := range sources {
		latest := ""
		for metric, m := range g.daily { // periodic rows first
			if growthMatchAny(s.globs, metric) {
				if d := latestKey(m); d > latest {
					latest = d
				}
			}
		}
		if latest == "" {
			for metric, byDate := range g.ranges {
				if growthMatchAny(s.globs, metric) {
					for d := range byDate {
						if d > latest {
							latest = d
						}
					}
				}
			}
		}
		if latest == "" {
			continue
		}
		since := c.age(latest)
		sf := GrowthSourceFreshness{Source: s.name, LatestDate: latest, DaysSince: since, Stale: since > c.opts.StaleDays}
		app.Freshness.Sources = append(app.Freshness.Sources, sf)
		if latest > app.Freshness.LatestDate {
			app.Freshness.LatestDate = latest
		}
		if sf.Stale {
			app.Freshness.Stale = true
		}
		if s.name == "app_store_connect" {
			facts.salesLatest = latestKey(mergeDaily(g.daily[growthKpiMetric["proceeds_usd"]], g.daily[growthKpiMetric["units"]]))
		}
	}
	if facts.salesLatest != "" {
		facts.salesStale = c.age(facts.salesLatest) > c.opts.StaleDays
	}

	app.Funnel = c.funnel(g, profile)
	open, reviewed := 0, map[string]bool{}
	for _, h := range g.hyps {
		if isOpenLoopHypothesis(h) {
			open++
		}
	}
	for _, r := range g.reviews {
		reviewed[r.HypothesisID+"|"+r.ID] = true
	}
	app.Hypotheses = GrowthHypotheses{Open: open, Reviewed: len(reviewed)}

	app.Audit = c.audit(g, app, facts.kind)
	for _, a := range app.Audit {
		facts.areaStatus[a.AreaID] = a.Status
		switch a.Status {
		case GrowthMeasured:
			app.Coverage.Measured++
		case GrowthPartial:
			app.Coverage.Partial++
		case GrowthStale:
			app.Coverage.Stale++
		case GrowthBlind:
			app.Coverage.Blind++
		default:
			app.Coverage.NotApplicable++
		}
	}
	if applicable := len(app.Audit) - app.Coverage.NotApplicable; applicable > 0 {
		app.Coverage.Score = growthRound((float64(app.Coverage.Measured) + 0.5*float64(app.Coverage.Partial) + 0.25*float64(app.Coverage.Stale)) / float64(applicable))
	}
	app.NextMoves = c.nextMoves(app)
	return app, facts
}

func growthMatchAny(globs []string, metric string) bool {
	for _, gl := range globs {
		if growthMatch(gl, metric) {
			return true
		}
	}
	return false
}

func mergeDaily(maps ...map[string]float64) map[string]float64 {
	out := map[string]float64{}
	for _, m := range maps {
		for d, v := range m {
			out[d] = v
		}
	}
	return out
}

// latestPoint returns the newest reading across periodic and range rows.
func (c growthCtx) latestPoint(g *growthGroup, globs []string) (v float64, date string, ok bool) {
	for metric, m := range g.daily {
		if !growthMatchAny(globs, metric) || strings.HasPrefix(metric, "ads_") {
			continue
		}
		for d, val := range m {
			if d > date {
				v, date, ok = val, d, true
			}
		}
	}
	for metric, m := range g.ranges {
		if !growthMatchAny(globs, metric) {
			continue
		}
		for d, pv := range m {
			if d > date {
				v, date, ok = pv.v, d, true
			}
		}
	}
	return
}

// windowOrLatest sums the metric over the window when periodic rows fall in it,
// else reports the newest periodic row (stale) or newest range row.
func (c growthCtx) windowOrLatest(g *growthGroup, metric string) (v float64, date string, ok bool) {
	m := g.daily[metric]
	if s, d := c.sumRange(m, c.opts.WindowDays-1, 0); d > 0 {
		return s, latestKey(m), true
	}
	if d := latestKey(m); d != "" {
		return m[d], d, true
	}
	if r := g.ranges[metric]; len(r) > 0 {
		for d, pv := range r {
			if d > date {
				v, date, ok = pv.v, d, true
			}
		}
	}
	return
}

func (c growthCtx) funnel(g *growthGroup, profile GrowthProfile) []GrowthFunnelStage {
	type def struct {
		id, label string
		storeOnly bool
		fetch     func() (float64, string, bool)
		noun      string
	}
	defs := []def{
		{"impressions", "App Store impressions", true, func() (float64, string, bool) {
			if v, d, ok := c.latestPoint(g, []string{"app_store_connect_impressions"}); ok {
				return v, d, ok
			}
			return c.latestPoint(g, []string{"app.impressions"})
		}, "impressions"},
		{"product_page_views", "Product page views", true, func() (float64, string, bool) {
			return c.latestPoint(g, []string{"app_store_connect_product_page_views"})
		}, "product page views"},
		{"first_time_downloads", "First-time downloads", true, func() (float64, string, bool) {
			if v, d, ok := c.windowOrLatest(g, "app_store_connect_first_time_downloads"); ok {
				return v, d, ok
			}
			return c.latestPoint(g, []string{"app.downloads_first_time"})
		}, "first-time downloads"},
		{"activation", "Activation", false, func() (float64, string, bool) {
			return c.latestPoint(g, []string{"posthog_*activation*", "posthog_*onboarding*"})
		}, "activations"},
		{"paywall_views", "Paywall views", true, func() (float64, string, bool) {
			return c.latestPoint(g, []string{"posthog_*paywall*"})
		}, "paywall views"},
		{"purchases", "Purchases", false, func() (float64, string, bool) {
			if v, d, ok := c.windowOrLatest(g, "app_store_connect_iap_units"); ok {
				return v, d, ok
			}
			return c.windowOrLatest(g, "app_store_connect_app_units")
		}, "purchases"},
		{"renewals", "Renewals", true, func() (float64, string, bool) {
			return c.latestPoint(g, []string{"posthog_*renew*", "app_store_connect_*renew*"})
		}, "renewals"},
	}
	out := make([]GrowthFunnelStage, 0, len(defs))
	for _, d := range defs {
		st := GrowthFunnelStage{ID: d.id, Label: d.label}
		if d.storeOnly && !profile.isStoreApp() {
			st.Status = GrowthNotApplicable
			st.Note = "Does not apply: this product is not sold in an app store."
			out = append(out, st)
			continue
		}
		v, date, ok := d.fetch()
		if !ok {
			st.Status = GrowthBlind
			st.Note = "Nothing measures this stage today."
			if d.id == "purchases" {
				if _, _, has := c.windowOrLatest(g, "app_store_connect_units"); has {
					st.Note = "Units are tracked, but purchases are not separated from free downloads."
				}
			}
			out = append(out, st)
			continue
		}
		st.Value, st.AsOf = growthPtr(v), date
		if age := c.age(date); age > c.opts.StaleDays {
			st.Status = GrowthStale
			st.Note = fmt.Sprintf("Last reading was %s on %s, %s ago.", growthCount(v, strings.TrimSuffix(d.noun, "s"), d.noun), date, growthDays(age))
		} else {
			st.Status = GrowthMeasured
			st.Note = fmt.Sprintf("%s, latest data %s.", growthCount(v, strings.TrimSuffix(d.noun, "s"), d.noun), date)
		}
		out = append(out, st)
	}
	return out
}

// ---------------------------------------------------------------------------
// audit

var growthPaywallText = regexp.MustCompile(`(?i)paywall`)

func (g *growthGroup) mentionsPaywall() bool {
	for _, h := range g.hyps {
		if growthPaywallText.MatchString(h.ChangeEvent + " " + h.ExpectedEffect + " " + strings.Join(h.Metrics, " ")) {
			return true
		}
	}
	for _, r := range g.reviews {
		if growthPaywallText.MatchString(r.Rationale + " " + r.NextAction) {
			return true
		}
	}
	return false
}

func (g *growthGroup) hasOpenHypothesisFor(spec growthAreaSpec) bool {
	globs := append(append([]string{}, spec.patterns...), spec.capped...)
	for _, h := range g.hyps {
		if !isOpenLoopHypothesis(h) {
			continue
		}
		for _, m := range h.Metrics {
			if growthMatchAny(globs, strings.ToLower(strings.TrimSpace(m))) {
				return true
			}
		}
		if spec.paywall && growthPaywallText.MatchString(h.ChangeEvent+" "+h.ExpectedEffect) {
			return true
		}
	}
	return false
}

func (c growthCtx) audit(g *growthGroup, app GrowthApp, kind string) []GrowthAudit {
	out := make([]GrowthAudit, 0, len(c.cat.Areas))
	stale := c.opts.StaleDays
	funnelFresh, funnelAny := 0, 0
	for _, f := range app.Funnel {
		switch f.Status {
		case GrowthMeasured:
			funnelFresh++
			funnelAny++
		case GrowthStale, GrowthPartial:
			funnelAny++
		}
	}
	for _, area := range c.cat.Areas {
		spec := area.spec(kind)
		a := GrowthAudit{AreaID: spec.ID, Evidence: []GrowthEvidence{}}
		full := c.evidence(g, spec.patterns)
		capped := c.evidence(g, spec.capped)
		all := append(append([]growthEv{}, full...), capped...)
		for _, e := range all {
			a.Evidence = append(a.Evidence, e.GrowthEvidence)
		}
		latest, rows90 := "", 0
		for _, e := range all {
			if e.LatestDate > latest {
				latest = e.LatestDate
			}
			rows90 += e.rows90
		}
		missing := ""
		var staleEv []growthEv
		status := GrowthMeasured
		switch spec.custom {
		case "feedback":
			status, missing = growthFeedback(g, c, &a)
		case "funnel":
			switch {
			case funnelFresh >= 5:
			case funnelAny >= 2:
				status, missing = GrowthPartial, fmt.Sprintf("Only %d of 7 funnel stages are fresh. Instrument the missing stages.", funnelFresh)
			default:
				status = GrowthBlind
			}
		case "experiments":
			cut := c.day(90)
			recent := 0
			for _, r := range g.reviews {
				if d := firstNonEmpty(r.ReviewedAt, r.AppliedAt); len(d) >= 10 && d[:10] > cut {
					recent++
				}
			}
			switch {
			case recent > 0:
			case app.Hypotheses.Open > 0:
				status, missing = GrowthPartial, "No hypothesis has been reviewed in 90 days. Review an open hypothesis."
			default:
				status = GrowthBlind
			}
			a.Evidence = append(a.Evidence, GrowthEvidence{Metric: "hypotheses_open", Rows: app.Hypotheses.Open}, GrowthEvidence{Metric: "reviews", Rows: len(g.reviews)})
		default:
			if len(all) == 0 {
				status = GrowthBlind
			}
		}
		if spec.custom == "" || spec.custom == "organic" || spec.custom == "search_ads" || spec.custom == "web_traffic" || spec.custom == "pricing" || spec.custom == "unit_economics" {
			status, missing, staleEv = c.metricStatus(g, spec, full, capped, latest, rows90, stale)
		}
		if status == GrowthBlind && spec.paywall && g.mentionsPaywall() {
			status, missing = GrowthPartial, "Only hypotheses and reviews mention the paywall. Add paywall events."
		}
		a.Status = status
		a.Summary = c.summary(g, spec, status, missing, all, latest, staleEv, app)
		a.NextMove = c.nextMove(g, spec, status, missing, latest, staleEv)
		out = append(out, a)
	}
	return out
}

func growthFeedback(g *growthGroup, c growthCtx, a *GrowthAudit) (string, string) {
	reviewRows := 0
	for _, e := range c.evidence(g, []string{"app_store_connect_review_count"}) {
		a.Evidence = append(a.Evidence, e.GrowthEvidence)
		reviewRows += e.Rows
	}
	a.Evidence = append(a.Evidence, GrowthEvidence{Metric: "open_source_issues", Rows: g.issues})
	switch {
	case g.issues > 0 && reviewRows > 0:
		return GrowthMeasured, ""
	case g.issues > 0:
		return GrowthPartial, "Only open source issues are tracked. Connect store reviews."
	case reviewRows > 0:
		return GrowthPartial, "Only the store review count is tracked. Collect written feedback."
	}
	return GrowthBlind, ""
}

func (c growthCtx) metricStatus(g *growthGroup, spec growthAreaSpec, full, capped []growthEv, latest string, rows90, stale int) (string, string, []growthEv) {
	all := append(append([]growthEv{}, full...), capped...)
	if len(all) == 0 {
		return GrowthBlind, "", nil
	}
	var staleEv []growthEv
	if spec.custom == "unit_economics" {
		for _, e := range all {
			if c.age(e.LatestDate) > stale {
				staleEv = append(staleEv, e)
			}
		}
	} else if c.age(latest) > stale {
		for _, e := range all {
			if e.LatestDate == latest {
				staleEv = append(staleEv, e)
			}
		}
	}
	if len(staleEv) > 0 {
		return GrowthStale, "", staleEv
	}
	has := func(glob string) bool {
		for _, e := range all {
			if growthMatch(glob, e.Metric) {
				return true
			}
		}
		return false
	}
	switch spec.custom {
	case "unit_economics":
		if !has("app_store_connect_developer_proceeds_usd") {
			return GrowthPartial, "Ad spend is known but sales proceeds are missing.", nil
		}
		if !has(growthAdsSpend) {
			return GrowthPartial, "Proceeds are known but there is no ad spend data.", nil
		}
		if g.adsRevenueRows == 0 {
			return GrowthPartial, "Ads have no revenue linked, so return on ad spend is unknown.", nil
		}
	case "pricing":
		return GrowthPartial, "Revenue per unit is known. Price response is not.", nil
	}
	if len(full) == 0 && len(capped) > 0 {
		return GrowthPartial, "Only a weak proxy exists for this area.", nil
	}
	if rows90 < 3 {
		return GrowthPartial, fmt.Sprintf("Only %d rows in the last 90 days.", rows90), nil
	}
	for _, comp := range spec.companion {
		if !has(comp) {
			return GrowthPartial, fmt.Sprintf("Missing companion data: %s.", comp), nil
		}
	}
	return GrowthMeasured, "", nil
}

func (c growthCtx) summary(g *growthGroup, spec growthAreaSpec, status, missing string, ev []growthEv, latest string, staleEv []growthEv, app GrowthApp) string {
	switch status {
	case GrowthBlind:
		if spec.source == "" && spec.custom == "" {
			return fmt.Sprintf("No data source. Nothing measures %s today.", spec.measure)
		}
		if len(spec.Inputs) == 0 || spec.source == "" {
			return fmt.Sprintf("No data source. Nothing measures %s today.", spec.measure)
		}
		if spec.custom == "organic" {
			return "No posted content is recorded for this app."
		}
		if growthVendorNeutral(spec) {
			return fmt.Sprintf("Nothing measures %s today.", spec.measure)
		}
		return fmt.Sprintf("No rows from %s for this app. Nothing measures %s today.", spec.source, spec.measure)
	case GrowthStale:
		return c.staleSummary(g, spec, ev, staleEv)
	}
	switch spec.custom {
	case "web_traffic":
		k := app.Kpis["web_clicks"]
		if k.HasData {
			s := fmt.Sprintf("%s from web search in %s", growthCount(*k.Value, "click", "clicks"), growthDays(c.opts.WindowDays))
			if p := growthPct(k.Value, k.Previous); p != "" {
				s += ", " + p
			}
			return s + "."
		}
	case "search_ads":
		sp, cv := app.Kpis["ad_spend_usd"], app.Kpis["ad_conversions"]
		if sp.HasData && cv.HasData {
			return fmt.Sprintf("%s spent on ads and %s in %s.", growthUSD(*sp.Value), growthCount(*cv.Value, "conversion", "conversions"), growthDays(c.opts.WindowDays))
		}
	case "pricing", "unit_economics":
		p, u := app.Kpis["proceeds_usd"], app.Kpis["units"]
		if p.HasData && u.HasData && *u.Value > 0 {
			s := fmt.Sprintf("%s per unit (%s proceeds on %s in %s).", growthUSD(*p.Value / *u.Value), growthUSD(*p.Value), growthCount(*u.Value, "unit", "units"), growthDays(c.opts.WindowDays))
			if missing != "" {
				s += " " + missing
			}
			return s
		}
	}
	names := make([]string, 0, 3)
	rows := 0
	for i, e := range ev {
		rows += e.Rows
		if i < 3 {
			names = append(names, e.Metric)
		}
	}
	s := ""
	if len(ev) > 0 {
		s = fmt.Sprintf("%s across %s, latest %s.", growthCount(float64(rows), "row", "rows"), strings.Join(names, ", "), latest)
	} else {
		s = "Tracked from product records."
	}
	if status == GrowthPartial && missing != "" {
		s += " " + missing
	}
	switch spec.custom {
	case "funnel":
		fresh := 0
		for _, f := range app.Funnel {
			if f.Status == GrowthMeasured {
				fresh++
			}
		}
		s = fmt.Sprintf("%d of 7 funnel stages have fresh data.", fresh)
		if status == GrowthPartial {
			s += " Instrument the missing stages."
		}
	case "experiments":
		s = fmt.Sprintf("%s and %s recorded.", growthCount(float64(app.Hypotheses.Open), "open hypothesis", "open hypotheses"), growthCount(float64(len(g.reviews)), "review", "reviews"))
		if status == GrowthPartial {
			s += " None was reviewed in the last 90 days."
		}
	case "feedback":
		s = fmt.Sprintf("%s tracked.", growthCount(float64(g.issues), "open source issue", "open source issues"))
		if missing != "" {
			s += " " + missing
		}
	}
	return s
}

// staleSummary names the stale input and its own date, never the newest date
// across inputs.
const growthPrivacyMove = "Choose a way to measure this that keeps the app's privacy promise: on-device counts, opt-in feedback, or a few user interviews."

// growthVendorNeutral marks areas whose evidence comes from product-usage
// analytics. Some apps promise users no third-party analytics, so blind text
// for these never names a vendor or prescribes a tool.
func growthVendorNeutral(spec growthAreaSpec) bool {
	return strings.Contains(strings.ToLower(spec.source), "posthog")
}

func (c growthCtx) staleSummary(g *growthGroup, spec growthAreaSpec, ev, staleEv []growthEv) string {
	srcs := staleSources(staleEv)
	if spec.custom == "unit_economics" {
		isStale := map[string]bool{}
		var parts []string
		for _, e := range staleEv {
			isStale[e.Metric] = true
			label := "Sales data"
			if e.Metric == growthAdsSpend {
				label = "Ad spend data"
			}
			parts = append(parts, fmt.Sprintf("%s stopped on %s (%s ago)", label, e.LatestDate, growthDays(c.age(e.LatestDate))))
		}
		sort.Strings(parts)
		s := strings.Join(parts, "; ")
		for _, e := range ev {
			if isStale[e.Metric] {
				continue
			}
			label := "sales data is current"
			if e.Metric == growthAdsSpend {
				label = "ad spend is current"
			}
			s += "; " + label
		}
		if g.adsRows > 0 && g.adsRevenueRows == 0 {
			s += "; ads have no revenue linked"
		}
		return s + "."
	}
	var parts []string
	for _, x := range srcs {
		parts = append(parts, fmt.Sprintf("%s stopped on %s (%s ago)", x.source, x.date, growthDays(c.age(x.date))))
	}
	return fmt.Sprintf("Data from %s. Numbers for %s are out of date.", strings.Join(parts, "; "), spec.measure)
}

func (c growthCtx) nextMove(g *growthGroup, spec growthAreaSpec, status, missing, latest string, staleEv []growthEv) string {
	switch status {
	case GrowthBlind:
		if spec.source == "" || len(spec.Inputs) == 0 {
			return fmt.Sprintf("No source exists yet. Decide how to measure %s.", spec.measure)
		}
		if spec.custom == "organic" {
			return "Record posts and their results so reach can be compared."
		}
		if growthVendorNeutral(spec) {
			return growthPrivacyMove
		}
		return fmt.Sprintf("Connect %s.", spec.source)
	case GrowthStale:
		srcs := staleSources(staleEv)
		if len(srcs) == 0 {
			return fmt.Sprintf("Refresh %s. The last data is from %s.", firstNonEmpty(spec.source, spec.measure), latest)
		}
		names := make([]string, 0, len(srcs))
		oldest := srcs[0].date
		for _, x := range srcs {
			names = append(names, x.source)
			if x.date < oldest {
				oldest = x.date
			}
		}
		return fmt.Sprintf("Refresh %s. The last data is from %s.", strings.Join(names, " and "), oldest)
	case GrowthPartial:
		if missing != "" {
			return missing
		}
		return fmt.Sprintf("Add the missing piece of %s data.", spec.measure)
	}
	if g.hasOpenHypothesisFor(spec) {
		return "Review the open hypothesis."
	}
	return "Write a hypothesis and test it."
}

func (c growthCtx) nextMoves(app GrowthApp) []GrowthNextMove {
	byID := map[string]growthAreaSpec{}
	for _, area := range c.cat.Areas {
		byID[area.ID] = area.spec(app.Kind)
	}
	rank := func(a GrowthAudit) int {
		spec := byID[a.AreaID]
		if len(spec.Inputs) == 0 && a.Status == GrowthBlind {
			return 2 // no source exists yet: after anything fixable
		}
		switch a.Status {
		case GrowthBlind, GrowthStale, GrowthPartial:
			return 0
		case GrowthMeasured:
			return 1
		}
		return 3
	}
	var picks []GrowthAudit
	for tier := 0; tier <= 2 && len(picks) < 3; tier++ {
		for _, a := range app.Audit {
			if rank(a) == tier && len(picks) < 3 {
				picks = append(picks, a)
			}
		}
	}
	out := make([]GrowthNextMove, 0, len(picks))
	for _, a := range picks {
		spec := byID[a.AreaID]
		m := GrowthNextMove{AreaID: a.AreaID, Why: a.Summary}
		switch a.Status {
		case GrowthBlind, GrowthPartial:
			m.Kind, m.Title = "connect_data", "Connect data for "+spec.Title
		case GrowthStale:
			m.Kind, m.Title = "refresh_data", "Refresh data for "+spec.Title
		default:
			if strings.HasPrefix(a.NextMove, "Review") {
				m.Kind, m.Title = "review_hypothesis", "Review the open hypothesis for "+spec.Title
			} else {
				m.Kind, m.Title = "run_experiment", "Run an experiment on "+spec.Title
			}
		}
		out = append(out, m)
	}
	return out
}

// ---------------------------------------------------------------------------
// allocation

const growthAllocationRule = "Last 90 days. fix_data: earned proceeds but sales data is stale. grow: proceeds in the window with fresh data. measure: has web clicks or ad conversions but no proceeds or blind activation. hold: little proceeds and traffic. Ranked by 90-day proceeds, then web clicks."

func (c growthCtx) allocate(app GrowthApp, f growthFacts) GrowthAllocation {
	al := GrowthAllocation{ProductID: app.ProductID, Name: app.Name, Rule: growthAllocationRule}
	activationBlind := true
	for _, a := range app.Audit {
		if (a.AreaID == "activation_onboarding" || a.AreaID == "activation_time_to_value") && a.Status != GrowthBlind && a.Status != GrowthNotApplicable {
			activationBlind = false
		}
	}
	traffic := f.clicks90 > 0 || f.adConv90 > 0
	switch {
	case f.proceeds90 > 0 && f.salesStale:
		al.Focus = GrowthFocusFixData
		al.Reason = fmt.Sprintf("%s proceeds in 90 days, but sales data stopped on %s (%s ago). Refresh it before deciding.", growthUSD(f.proceeds90), f.salesLatest, growthDays(c.age(f.salesLatest)))
	case f.proceeds30 > 0:
		al.Focus = GrowthFocusGrow
		al.Reason = fmt.Sprintf("%s proceeds in %s (%s in 90 days) with sales data fresh through %s.", growthUSD(f.proceeds30), growthDays(c.opts.WindowDays), growthUSD(f.proceeds90), f.salesLatest)
	case traffic && (f.proceeds90 == 0 || activationBlind):
		al.Focus = GrowthFocusMeasure
		al.Reason = fmt.Sprintf("%s and %s in 90 days, with %s proceeds and activation not measured.", growthCount(f.clicks90, "web click", "web clicks"), growthCount(f.adConv90, "ad conversion", "ad conversions"), growthUSD(f.proceeds90))
	default:
		al.Focus = GrowthFocusHold
		if !f.hasAnyData {
			al.Reason = "No data from any source for this app."
		} else {
			al.Reason = fmt.Sprintf("%s proceeds, %s and %s in 90 days.", growthUSD(f.proceeds90), growthCount(f.clicks90, "web click", "web clicks"), growthCount(f.adConv90, "ad conversion", "ad conversions"))
		}
	}
	return al
}

// ---------------------------------------------------------------------------
// validation

var growthStatuses = map[string]bool{GrowthMeasured: true, GrowthPartial: true, GrowthStale: true, GrowthBlind: true, GrowthNotApplicable: true}

// Validate checks the projection against the contract shape.
func (g Growth) Validate() error {
	if g.SchemaVersion != GrowthSchemaVersion {
		return fmt.Errorf("schema_version = %q, want %q", g.SchemaVersion, GrowthSchemaVersion)
	}
	if _, err := time.Parse(time.RFC3339, g.GeneratedAt); err != nil {
		return fmt.Errorf("generated_at invalid: %w", err)
	}
	if _, ok := parseGrowthDay(g.AsOf); !ok {
		return fmt.Errorf("as_of invalid: %q", g.AsOf)
	}
	if g.WindowDays <= 0 || g.SeriesDays <= 0 || g.StaleDays <= 0 {
		return fmt.Errorf("window_days, series_days and stale_days must be positive")
	}
	if len(g.Groups) == 0 || len(g.Areas) == 0 {
		return fmt.Errorf("catalog incomplete: %d groups, %d areas", len(g.Groups), len(g.Areas))
	}
	groupIDs := map[string]bool{}
	for _, gr := range g.Groups {
		groupIDs[gr.ID] = true
	}
	for i, a := range g.Areas {
		if !groupIDs[a.Group] || a.Number != i+1 {
			return fmt.Errorf("area %s has group %q and number %d at position %d", a.ID, a.Group, a.Number, i+1)
		}
	}
	if g.Portfolio.AppCount != len(g.Apps) || len(g.Portfolio.Allocation) != len(g.Apps) {
		return fmt.Errorf("portfolio app_count/allocation does not match apps")
	}
	for key, pts := range g.Portfolio.Series {
		if len(pts) != g.SeriesDays {
			return fmt.Errorf("portfolio series %s has %d points, want %d", key, len(pts), g.SeriesDays)
		}
	}
	for _, key := range GrowthKpiKeys {
		if _, ok := g.Portfolio.Totals[key]; !ok {
			return fmt.Errorf("portfolio totals missing %s", key)
		}
	}
	for _, al := range g.Portfolio.Allocation {
		switch al.Focus {
		case GrowthFocusGrow, GrowthFocusFixData, GrowthFocusMeasure, GrowthFocusHold:
		default:
			return fmt.Errorf("allocation %s focus %q invalid", al.ProductID, al.Focus)
		}
	}
	for _, app := range g.Apps {
		if strings.TrimSpace(app.ProductID) == "" || strings.TrimSpace(app.Name) == "" {
			return fmt.Errorf("app product_id and name required")
		}
		if len(app.Audit) != len(g.Areas) {
			return fmt.Errorf("app %s audit has %d entries, want %d", app.ProductID, len(app.Audit), len(g.Areas))
		}
		for i, a := range app.Audit {
			if a.AreaID != g.Areas[i].ID {
				return fmt.Errorf("app %s audit[%d] = %s, want %s", app.ProductID, i, a.AreaID, g.Areas[i].ID)
			}
			if !growthStatuses[a.Status] {
				return fmt.Errorf("app %s area %s status %q invalid", app.ProductID, a.AreaID, a.Status)
			}
		}
		if len(app.Funnel) != 7 {
			return fmt.Errorf("app %s funnel has %d stages, want 7", app.ProductID, len(app.Funnel))
		}
		for _, f := range app.Funnel {
			if !growthStatuses[f.Status] {
				return fmt.Errorf("app %s funnel %s status %q invalid", app.ProductID, f.ID, f.Status)
			}
		}
		cv := app.Coverage
		if cv.Measured+cv.Partial+cv.Stale+cv.Blind+cv.NotApplicable != len(g.Areas) {
			return fmt.Errorf("app %s coverage counts do not sum to %d", app.ProductID, len(g.Areas))
		}
		if cv.Score < 0 || cv.Score > 1 {
			return fmt.Errorf("app %s coverage score out of range", app.ProductID)
		}
		for _, key := range GrowthSeriesKeys {
			if len(app.Series[key]) != g.SeriesDays {
				return fmt.Errorf("app %s series %s has %d points, want %d", app.ProductID, key, len(app.Series[key]), g.SeriesDays)
			}
		}
		for _, key := range GrowthKpiKeys {
			if _, ok := app.Kpis[key]; !ok {
				return fmt.Errorf("app %s kpis missing %s", app.ProductID, key)
			}
		}
		if len(app.NextMoves) > 3 {
			return fmt.Errorf("app %s has %d next_moves, want at most 3", app.ProductID, len(app.NextMoves))
		}
	}
	return nil
}
