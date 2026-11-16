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
	// AssessDays is how old an assessment may get before it reads as stale.
	AssessDays int
	// Area selects one area for BuildGrowthArea; BuildGrowth ignores it.
	Area string

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
	// Estimate marks a number that is not the source's own figure (proceeds
	// in US dollars are converted by the collector); Note says how.
	Estimate bool   `json:"estimate,omitempty"`
	Note     string `json:"note,omitempty"`
	// WindowStart and WindowEnd are the first and last day Value sums (a
	// store KPI's window ends on the Sales report's newest final day), so a
	// reader can print the dates instead of only "N days".
	WindowStart string `json:"window_start"`
	WindowEnd   string `json:"window_end"`
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
	ID string `json:"id"`
	// Label names what Value counts; for the downloads stage it follows the
	// source ("New downloads" from the Sales report, else "First-time
	// downloads" from analytics) while ID stays first_time_downloads.
	Label string   `json:"label"`
	Value *float64 `json:"value"`
	// AsOf is the newest day behind Value.
	AsOf   string `json:"as_of"`
	Status string `json:"status"`
	Note   string `json:"note"`
	// WindowStart and WindowEnd are the days Value sums when it is a window
	// total. Every such stage uses the same window, the store KPIs' window,
	// so stages compare with each other and with the tiles. Both are empty
	// when Value is one reading on AsOf; DaysReported counts the window's
	// days that had a value.
	WindowStart  string `json:"window_start,omitempty"`
	WindowEnd    string `json:"window_end,omitempty"`
	DaysReported int    `json:"days_reported,omitempty"`
}

type GrowthEvidence struct {
	Metric     string `json:"metric"`
	Rows       int    `json:"rows"`
	LatestDate string `json:"latest_date"`
}

type GrowthAudit struct {
	AreaID    string           `json:"area_id"`
	Status    string           `json:"status"`
	Basis     string           `json:"basis"`
	Title     string           `json:"title"`
	Question  string           `json:"question"`
	Forms     []string         `json:"forms"`
	Summary   string           `json:"summary"`
	Reason    string           `json:"reason"`
	Evidence  []GrowthEvidence `json:"evidence"`
	NextMove  string           `json:"next_move"`
	BlockedBy []string         `json:"blocked_by"`
	Branch    string           `json:"branch"`
	Skills    []string         `json:"skills"`

	gap        string              // next-move kind for a gap: connect_data, record_answer or assess
	assessment bool                // the status rests on a learned-fact assessment
	stale      []growthStaleSource // the sources that stopped, when the status is stale on data
}

type GrowthCoverage struct {
	Measured      int     `json:"measured"`
	Partial       int     `json:"partial"`
	Stale         int     `json:"stale"`
	Blind         int     `json:"blind"`
	NotApplicable int     `json:"not_applicable"`
	Unknown       int     `json:"unknown"`
	Deferred      int     `json:"deferred"`
	Score         float64 `json:"score"`
}

type GrowthHypotheses struct {
	Open     int `json:"open"`
	Reviewed int `json:"reviewed"`
}

type GrowthNextMove struct {
	AreaID string `json:"area_id"`
	// AreaIDs lists every area the move covers, in catalog order: [area_id]
	// for one area, all of them for a folded refresh, empty for set_profile.
	AreaIDs   []string `json:"area_ids"`
	Kind      string   `json:"kind"`
	Title     string   `json:"title"`
	Why       string   `json:"why"`
	Unblocks  int      `json:"unblocks"`
	BlockedBy []string `json:"blocked_by"`
	Branch    string   `json:"branch"`
	Skills    []string `json:"skills"`
	Brief     string   `json:"brief"`
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
	// Reports is the store report coverage for the app's linked store app
	// ids, from the fast path only (file names and the two series).
	Reports GrowthAppReports `json:"reports"`
}

type GrowthAllocation struct {
	ProductID string `json:"product_id"`
	Name      string `json:"name"`
	Rank      int    `json:"rank"`
	Focus     string `json:"focus"`
	Reason    string `json:"reason"`
	Rule      string `json:"rule"`
	// WindowStart and WindowEnd are the days the window_days proceeds in
	// Reason cover: the same window as the proceeds_usd KPI, so the sentence
	// and the tile give one amount. Reason's 90-day proceeds end on the same day.
	WindowStart string `json:"window_start"`
	WindowEnd   string `json:"window_end"`
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
	AssessDays    int                 `json:"assess_days"`
	Boundary      string              `json:"boundary"`
	Catalog       GrowthCatalogInfo   `json:"catalog"`
	ProfileDims   map[string][]string `json:"profile_dimensions"`
	Groups        []GrowthAreaGroup   `json:"groups"`
	Areas         []GrowthArea        `json:"areas"`
	Portfolio     GrowthPortfolio     `json:"portfolio"`
	Apps          []GrowthApp         `json:"apps"`
}

// GrowthKpiKeys lists the KPI keys in contract order. "units" is new
// downloads from the Sales report (first-time downloads and paid first
// purchases); redownloads, updates and in-app purchases have their own keys
// and never count toward it. "first_time_downloads" is the analytics number.
var GrowthKpiKeys = []string{"proceeds_usd", "units", "first_time_downloads", "web_clicks", "web_impressions", "ad_spend_usd", "ad_conversions", "ad_cpa_usd", "iap_units", "updates", "redownloads"}

// GrowthSeriesKeys lists the series keys in contract order.
var GrowthSeriesKeys = []string{"proceeds_usd", "units", "web_clicks", "ad_spend_usd", "ad_conversions"}

var growthKpiUnit = map[string]string{
	"proceeds_usd": "usd", "units": "count", "first_time_downloads": "count", "web_clicks": "count",
	"web_impressions": "count", "ad_spend_usd": "usd", "ad_conversions": "count", "ad_cpa_usd": "usd",
	"iap_units": "count", "updates": "count", "redownloads": "count",
}

// growthKpiMetric maps a KPI key to the daily metric that feeds it.
var growthKpiMetric = map[string]string{
	"proceeds_usd":         "app_store_connect_developer_proceeds_usd",
	"units":                growthSalesFirstDownloads,
	"first_time_downloads": "app_store_connect_first_time_downloads",
	"web_clicks":           "gsc_clicks",
	"web_impressions":      "gsc_impressions",
	"ad_spend_usd":         "ads_spend",
	"ad_conversions":       "ads_conversions",
	"iap_units":            "app_store_connect_iap_units",
	"updates":              "app_store_connect_updates",
	"redownloads":          "app_store_connect_redownloads",
}

// growthStoreKpis are the KPIs the Sales report feeds. Their window ends on
// the report's newest final day (see growthStoreWindowEnd).
var growthStoreKpis = map[string]bool{"proceeds_usd": true, "units": true, "iap_units": true, "updates": true, "redownloads": true}

// growthProceedsNote says once why proceeds in US dollars are an estimate.
const growthProceedsNote = "Estimate: the sales collector converts each currency to US dollars with a fixed rate table; Apple pays in each currency and its Finance report has no US dollar total."

// Pseudo metrics synthesized from non-signal ledgers.
const (
	growthAdsSpend       = "ads_spend"
	growthAdsConversions = "ads_conversions"
	growthAppleAds       = "apple_search_ads_spend"
	growthContentPosts   = "content_posts"
)

// Validate checks the options after defaults are applied.
func (o GrowthOptions) Validate() error {
	if o.WindowDays <= 0 || o.SeriesDays <= 0 || o.StaleDays <= 0 || o.AssessDays <= 0 {
		return fmt.Errorf("window, series-days, stale-days and assess-days must be positive")
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
	if o.AssessDays == 0 {
		o.AssessDays = 180
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
	releaseAppIDs   []string        // App Store app ids the release identity names
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

// BuildGrowth folds the signal ledger, the daily App Store sales and analytics
// series, ads rows, hypotheses, reviews and product registry into the growth
// projection.
// It reads only; it never opens the memory store for writing.
func BuildGrowth(homePath string, opts GrowthOptions) (Growth, error) {
	opts = normalizeGrowthOptions(opts)
	if err := opts.Validate(); err != nil {
		return Growth{}, err
	}
	w, err := loadGrowthWorld(homePath, opts)
	if err != nil {
		return Growth{}, err
	}
	return w.build(opts), nil
}

// growthWorld is what the growth projection and the store report view both
// resolve from the home: the catalogs, the product groups with their daily
// signals, the store app identity and what the store captures hold.
type growthWorld struct {
	cat       *growthAreaCatalog
	all       []*growthGroup // every product group
	groups    []*growthGroup // after the --product filter
	identity  map[string][]storeAppClaim
	ascSales  []growthSalesRow
	salesDays []ascSalesDayIndexRow
	stores    *storeReportScan
	fast      []StoreReportCheck // fast checks, run once
}

// loadGrowthWorld reads the home once. opts must be normalized.
func loadGrowthWorld(homePath string, opts GrowthOptions) (*growthWorld, error) {
	cat := opts.catalog
	if cat == nil {
		loaded, err := loadGrowthCatalog(homePath)
		if err != nil {
			return nil, fmt.Errorf("growth catalog: %w", err)
		}
		cat = loaded
	}
	store, err := mem.OpenScan(homePath)
	if err != nil {
		return nil, err
	}
	idx, err := loadWhereIndex(store)
	if err != nil {
		return nil, err
	}
	signals, err := loadSignals(homePath)
	if err != nil {
		return nil, err
	}
	adRows, err := LoadAdRows(homePath)
	if err != nil {
		return nil, err
	}
	hyps, err := loadHypotheses(homePath)
	if err != nil {
		return nil, err
	}
	reviews, err := loadReviews(homePath)
	if err != nil {
		return nil, err
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
	// lookup finds a known group without adding one, for sources that may
	// name a product Compound does not know.
	lookup := func(id string) *growthGroup {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil
		}
		if g := byID[strings.ToLower(id)]; g != nil {
			return g
		}
		if key := identityKey(id); key != "" {
			for _, g := range groups {
				if g.tokens[key] {
					return g
				}
			}
		}
		return nil
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
		return nil, err
	}
	salesDays, err := loadASCSalesDaysIndex(homePath)
	if err != nil {
		return nil, err
	}
	identity, err := storeAppIdentity(homePath, signals, groups, resolve, lookup)
	if err != nil {
		return nil, err
	}
	ascAppIDs := storeAppGroups(identity)
	applyGrowthASCSales(ascSales, ascAppIDs, asOfStr)
	applyGrowthASCSalesZeroDays(ascSales, salesDays, ascAppIDs, asOfStr)
	ascFunnel, err := loadGrowthASCFunnelSeries(homePath)
	if err != nil {
		return nil, err
	}
	applyGrowthASCFunnel(ascFunnel, ascAppIDs, asOfStr)

	applyGrowthAds(adRows, resolve, asOfStr)

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

	all := groups
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
			return nil, fmt.Errorf("no product matches %q", opts.ProductID)
		}
		if len(picked) > 1 {
			return nil, fmt.Errorf("%q names more than one product", opts.ProductID)
		}
		groups = picked
	}
	stores, err := scanStoreReports(homePath, opts.AsOf)
	if err != nil {
		return nil, err
	}
	return &growthWorld{cat: cat, all: all, groups: groups, identity: identity, ascSales: ascSales, salesDays: salesDays, stores: stores}, nil
}

// build turns what the home holds into the growth projection.
func (w *growthWorld) build(opts GrowthOptions) Growth {
	cat, groups, asOfStr := w.cat, w.groups, growthDayStr(opts.AsOf)
	out := Growth{
		SchemaVersion: GrowthSchemaVersion,
		GeneratedAt:   opts.Now.Format(time.RFC3339),
		AsOf:          asOfStr,
		WindowDays:    opts.WindowDays,
		SeriesDays:    opts.SeriesDays,
		StaleDays:     opts.StaleDays,
		AssessDays:    opts.AssessDays,
		Boundary:      growthBoundary,
		Catalog:       cat.info(),
		ProfileDims:   cat.profileDimensions(),
		Groups:        append([]GrowthAreaGroup{}, cat.Groups...),
		Areas:         cat.publicAreas(),
	}
	ctx := growthCtx{opts: opts, cat: cat, storeEnd: growthStoreWindowEnd(w.ascSales, w.salesDays, opts.AsOf), world: w}
	type ranked struct {
		app   GrowthApp
		alloc GrowthAllocation
		p90   float64
		c90   float64
	}
	var apps []ranked
	portfolioDaily := map[string]map[string]float64{}
	portfolioMetrics := []string{growthAdsUnconverted}
	for _, key := range GrowthKpiKeys {
		if metric, ok := growthKpiMetric[key]; ok {
			portfolioMetrics = append(portfolioMetrics, metric)
		}
	}
	for _, g := range groups {
		app, facts := ctx.buildApp(g)
		alloc := ctx.allocate(app, facts)
		apps = append(apps, ranked{app, alloc, facts.proceeds90, facts.clicks90})
		for _, metric := range portfolioMetrics {
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
	return out
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
			if c, ok := idx.claim(e.Slug, "release_app_store_connect_app_id"); ok && strings.TrimSpace(c.Object) != "" {
				g.releaseAppIDs = appendUnique(g.releaseAppIDs, strings.TrimSpace(c.Object))
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
	// storeEnd is how many days before as-of the store KPI window ends: the
	// Sales report's newest final day, or 0.
	storeEnd int
	// world gives store report coverage per app; nil in briefs.
	world *growthWorld
}

type growthFacts struct {
	hasAnyData bool
	// proceeds30 (window_days long) and proceeds90 end on the proceeds KPI's
	// window end, so the allocation sentence restates the tile's amount;
	// proceedsFrom and proceedsTo are proceeds30's first and last day.
	proceeds90   float64
	proceeds30   float64
	proceedsFrom string
	proceedsTo   string
	clicks90     float64
	adConv90     float64
	salesLatest  string
	salesStale   bool
	kind         string
	areaStatus   map[string]string
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

// windowEnd is how many days before as-of the window for a KPI key ends. A
// store KPI's window ends on the Sales report's newest final day, so a day
// Apple has not published (or may still correct) is neither counted nor read
// as a missing day; every other window ends on as-of. Every number that
// restates a store KPI (the allocation reason, the funnel) uses this end too.
func (c growthCtx) windowEnd(key string) int {
	if growthStoreKpis[key] {
		return c.storeEnd
	}
	return 0
}

func (c growthCtx) kpiFor(key string, daily map[string]map[string]float64) GrowthKpi {
	k := GrowthKpi{DaysInWindow: c.opts.WindowDays, Unit: growthKpiUnit[key]}
	w := c.opts.WindowDays
	end := c.windowEnd(key)
	k.WindowStart, k.WindowEnd = c.day(end+w-1), c.day(end)
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
	// The previous window shifts with the current one.
	v, d := c.sumRange(m, end+w-1, end)
	p, pd := c.sumRange(m, end+2*w-1, end+w)
	k.DaysReported = d
	k.LatestDate = latestKey(m)
	if end > 0 {
		k.LatestDate = latestKeyUpTo(m, c.day(end))
		if d > 0 {
			k.LatestDate = c.day(end)
		}
	}
	if d > 0 {
		k.Value, k.HasData = growthPtr(v), true
	}
	if pd > 0 {
		k.Previous = growthPtr(p)
	}
	if key == "proceeds_usd" {
		k.Estimate, k.Note = true, growthProceedsNote
	}
	return k
}

// latestKeyUpTo is the newest date in m on or before last.
func latestKeyUpTo(m map[string]float64, last string) string {
	latest := ""
	for d := range m {
		if d <= last && d > latest {
			latest = d
		}
	}
	return latest
}

func (c growthCtx) kpis(daily map[string]map[string]float64) map[string]GrowthKpi {
	out := map[string]GrowthKpi{}
	for _, key := range GrowthKpiKeys {
		k := c.kpiFor(key, daily)
		if key == "ad_spend_usd" || key == "ad_cpa_usd" {
			c.withholdUnconvertedAds(&k, daily, c.opts.WindowDays)
		}
		out[key] = k
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
			if v, ok := m[d]; ok && (key != "ad_spend_usd" || daily[growthAdsUnconverted][d] == 0) {
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
		metric == "app_store_connect_iap_units", metric == "app_store_connect_app_units", metric == growthSalesFirstDownloads:
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
	proceedsEnd := c.windowEnd("proceeds_usd")
	facts.proceeds90, _ = c.sumRange(g.daily[growthKpiMetric["proceeds_usd"]], proceedsEnd+89, proceedsEnd)
	facts.proceeds30, _ = c.sumRange(g.daily[growthKpiMetric["proceeds_usd"]], proceedsEnd+c.opts.WindowDays-1, proceedsEnd)
	facts.proceedsFrom, facts.proceedsTo = c.day(proceedsEnd+c.opts.WindowDays-1), c.day(proceedsEnd)
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
			facts.salesLatest = latestKey(mergeDaily(g.daily[growthKpiMetric["proceeds_usd"]], g.daily["app_store_connect_units"], g.daily[growthKpiMetric["units"]]))
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

	app.Audit = c.audit(g, app)
	for _, a := range app.Audit {
		facts.areaStatus[a.AreaID] = a.Status
	}
	app.Coverage = growthCoverage(app.Audit)
	app.NextMoves = c.nextMoves(g, app)
	app.Reports = GrowthAppReports{Command: storeReportsCommand(app.ProductID)}
	if c.world != nil {
		app.Reports = c.world.appReportCounts(g, app.ProductID)
	}
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

// growthSortedKeys lists a map's keys in order, so a walk over it does not
// depend on map order.
func growthSortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// latestPoint returns the newest reading across periodic and range rows.
// When two metrics share the newest date, the first metric by name wins.
func (c growthCtx) latestPoint(g *growthGroup, globs []string) (v float64, date string, ok bool) {
	for _, metric := range growthSortedKeys(g.daily) {
		if !growthMatchAny(globs, metric) || strings.HasPrefix(metric, "ads_") {
			continue
		}
		for d, val := range g.daily[metric] {
			if d > date {
				v, date, ok = val, d, true
			}
		}
	}
	for _, metric := range growthSortedKeys(g.ranges) {
		if !growthMatchAny(globs, metric) {
			continue
		}
		for d, pv := range g.ranges[metric] {
			if d > date {
				v, date, ok = pv.v, d, true
			}
		}
	}
	return
}

// growthFunnelRead is the number behind one funnel stage: a total over the
// funnel window, or, when the window has no data, the newest single reading.
type growthFunnelRead struct {
	v    float64
	date string // newest day behind v
	days int    // days summed; 0 for a single reading
}

// funnelWindow returns how many days before as-of the funnel window ends and
// its first and last day. It is the store KPIs' window (window_days long,
// ending on the Sales report's newest final day), so every stage summed over
// it compares with the others and with the tiles.
func (c growthCtx) funnelWindow() (end int, from, to string) {
	end = c.windowEnd("units")
	return end, c.day(end + c.opts.WindowDays - 1), c.day(end)
}

// funnelTotal sums one daily metric over the funnel window; ok is false when no
// day in the window has a value.
func (c growthCtx) funnelTotal(g *growthGroup, metric string) (growthFunnelRead, bool) {
	end, _, to := c.funnelWindow()
	m := g.daily[metric]
	v, days := c.sumRange(m, end+c.opts.WindowDays-1, end)
	if days == 0 {
		return growthFunnelRead{}, false
	}
	return growthFunnelRead{v: v, date: latestKeyUpTo(m, to), days: days}, true
}

// funnelReading is the newest single reading of the metrics, never a sum.
func (c growthCtx) funnelReading(g *growthGroup, globs ...string) (growthFunnelRead, bool) {
	v, date, ok := c.latestPoint(g, globs)
	return growthFunnelRead{v: v, date: date}, ok
}

// growthSalesDownloadsNote and growthAnalyticsDownloadsNote say where the
// downloads stage's number comes from; its label names the same count.
const (
	growthSalesDownloadsNote     = "Counted by the Sales report (exact)."
	growthAnalyticsDownloadsNote = "Counted by App Store analytics (noised; the Sales report has no download kinds for this app)."
)

// funnel builds the seven stages. App Store impressions, product page views,
// downloads and purchases are totals over one window, the store KPIs' window,
// so each stage can be compared with the next and with the tiles; a stage
// whose window has no data shows its newest reading instead and says so.
// Activation, paywall views and renewals come from product usage events that
// may be rates or running counts, so they stay one reading: summing them could
// change what the number means.
func (c growthCtx) funnel(g *growthGroup, profile GrowthProfile) []GrowthFunnelStage {
	type def struct {
		id, label string
		storeOnly bool
		// fetch returns the number, the label and the noun that name what it
		// counts, and a sentence on its source; ok is false when nothing does.
		fetch func() (r growthFunnelRead, label, noun, source string, ok bool)
	}
	first := func(reads ...func() (growthFunnelRead, bool)) (growthFunnelRead, bool) {
		for _, read := range reads {
			if r, ok := read(); ok {
				return r, true
			}
		}
		return growthFunnelRead{}, false
	}
	total := func(metric string) func() (growthFunnelRead, bool) {
		return func() (growthFunnelRead, bool) { return c.funnelTotal(g, metric) }
	}
	reading := func(globs ...string) func() (growthFunnelRead, bool) {
		return func() (growthFunnelRead, bool) { return c.funnelReading(g, globs...) }
	}
	plain := func(label, noun string, reads ...func() (growthFunnelRead, bool)) func() (growthFunnelRead, string, string, string, bool) {
		return func() (growthFunnelRead, string, string, string, bool) {
			r, ok := first(reads...)
			return r, label, noun, "", ok
		}
	}
	defs := []def{
		{"impressions", "App Store impressions", true, plain("App Store impressions", "impressions",
			total("app_store_connect_impressions"), reading("app_store_connect_impressions"), reading("app.impressions"))},
		{"product_page_views", "Product page views", true, plain("Product page views", "product page views",
			total("app_store_connect_product_page_views"), reading("app_store_connect_product_page_views"))},
		{"first_time_downloads", "First-time downloads", true, func() (growthFunnelRead, string, string, string, bool) {
			// The Sales report counts every new download exactly (the units
			// KPI); analytics adds noise. Use Sales where it has the number,
			// and name the stage after what it counts.
			if r, ok := first(total(growthSalesFirstDownloads), reading(growthSalesFirstDownloads)); ok {
				return r, "New downloads", "new downloads", growthSalesDownloadsNote, true
			}
			r, ok := first(total("app_store_connect_first_time_downloads"), reading("app_store_connect_first_time_downloads"), reading("app.downloads_first_time"))
			return r, "First-time downloads", "first-time downloads", growthAnalyticsDownloadsNote, ok
		}},
		{"activation", "Activation", false, plain("Activation", "activations", reading("posthog_*activation*", "posthog_*onboarding*"))},
		{"paywall_views", "Paywall views", true, plain("Paywall views", "paywall views", reading("posthog_*paywall*"))},
		{"purchases", "Purchases", false, plain("Purchases", "purchases",
			total("app_store_connect_iap_units"), reading("app_store_connect_iap_units"),
			total("app_store_connect_app_units"), reading("app_store_connect_app_units"))},
		{"renewals", "Renewals", true, plain("Renewals", "renewals", reading("posthog_*renew*", "app_store_connect_*renew*"))},
	}
	_, from, to := c.funnelWindow()
	out := make([]GrowthFunnelStage, 0, len(defs))
	for _, d := range defs {
		st := GrowthFunnelStage{ID: d.id, Label: d.label}
		if d.storeOnly && !profile.isStoreApp() {
			st.Status = GrowthNotApplicable
			st.Note = "Does not apply: this product is not sold in an app store."
			out = append(out, st)
			continue
		}
		r, label, noun, source, ok := d.fetch()
		if !ok {
			st.Status = GrowthBlind
			st.Note = "Nothing measures this stage today."
			if d.id == "purchases" {
				if len(g.daily["app_store_connect_units"]) > 0 || len(g.ranges["app_store_connect_units"]) > 0 {
					st.Note = "Units are tracked, but purchases are not separated from free downloads."
				}
			}
			out = append(out, st)
			continue
		}
		st.Label = label
		st.Value, st.AsOf = growthPtr(r.v), r.date
		count := growthCount(r.v, strings.TrimSuffix(noun, "s"), noun)
		age := c.age(r.date)
		switch {
		case r.days > 0:
			st.WindowStart, st.WindowEnd, st.DaysReported = from, to, r.days
			st.Status = GrowthMeasured
			st.Note = fmt.Sprintf("%s from %s to %s (%d of %s reported).", count, from, to, r.days, growthDays(c.opts.WindowDays))
			if age > c.opts.StaleDays {
				st.Status = GrowthStale
				st.Note = fmt.Sprintf("%s from %s to %s, but the newest data is from %s, %s ago.", count, from, to, r.date, growthDays(age))
			}
		case age > c.opts.StaleDays:
			st.Status = GrowthStale
			st.Note = fmt.Sprintf("Last reading was %s on %s, %s ago.", count, r.date, growthDays(age))
		default:
			st.Status = GrowthMeasured
			st.Note = fmt.Sprintf("%s, one reading on %s, not a total over %s to %s.", count, r.date, from, to)
		}
		if source != "" {
			st.Note += " " + source
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

// growthFormResult is one form's verdict from its metric, custom, record and
// assessment evidence.
type growthFormResult struct {
	spec     growthAreaSpec
	status   string
	basis    string
	summary  string
	nextMove string
	evidence []GrowthEvidence
	stale    []growthStaleSource // the sources that stopped, for a stale metric verdict
}

// evalMetrics runs the metric and custom evaluators for one form. ok is false
// when the form declares no metric or custom evidence at all.
func (c growthCtx) evalMetrics(g *growthGroup, app GrowthApp, spec growthAreaSpec, funnelFresh, funnelAny int) (growthFormResult, bool) {
	if len(spec.patterns)+len(spec.capped)+len(spec.companion) == 0 && spec.custom == "" {
		return growthFormResult{}, false
	}
	stale := c.opts.StaleDays
	a := GrowthAudit{AreaID: spec.ID, Evidence: []GrowthEvidence{}}
	full := c.evidence(g, spec.patterns)
	if c.adsCurrencyGap(g, spec) {
		full = append(full, c.evidence(g, []string{growthAdsUnconverted})...)
	}
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
	r := growthFormResult{spec: spec, status: status, basis: "metric", evidence: a.Evidence}
	if status == GrowthBlind {
		r.basis = "none"
	}
	if status == GrowthStale {
		r.stale = staleSources(staleEv)
	}
	r.summary = c.summary(g, spec, status, missing, all, latest, staleEv, app)
	r.nextMove = c.nextMove(g, spec, status, missing, latest, staleEv)
	return r, true
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
		// Currency coverage and report freshness are different questions.
		// USD and unconverted observations belong to one ads source.
		ads := growthEv{GrowthEvidence: GrowthEvidence{Metric: growthAdsSpend}}
		for _, e := range all {
			if e.Metric == growthAdsSpend || e.Metric == growthAdsUnconverted {
				ads.Rows += e.Rows
				if e.LatestDate > ads.LatestDate {
					ads.LatestDate = e.LatestDate
				}
				continue
			}
			if c.age(e.LatestDate) > stale {
				staleEv = append(staleEv, e)
			}
		}
		if ads.LatestDate != "" && c.age(ads.LatestDate) > stale {
			staleEv = append(staleEv, ads)
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
	if c.adsCurrencyGap(g, spec) {
		return GrowthPartial, growthAdsCurrencyNote, nil
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
		s := c.staleSummary(g, spec, ev, staleEv)
		if c.adsCurrencyGap(g, spec) {
			s += " " + growthAdsCurrencyNote
		}
		return s
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
			s := fmt.Sprintf("%s per new download (%s proceeds on %s in the Sales report, %s).", growthUSD(*p.Value / *u.Value), growthUSD(*p.Value), growthCount(*u.Value, "new download", "new downloads"), growthDays(c.opts.WindowDays))
			if missing != "" {
				s += " " + missing
			}
			return s
		}
	}
	// Name where the rows come from as a plain source, never a metric id.
	names := make([]string, 0, 3)
	seen := map[string]bool{}
	rows := 0
	for _, e := range ev {
		rows += e.Rows
		if src := growthMetricSource(e.Metric); !seen[src] && len(names) < 3 {
			seen[src] = true
			names = append(names, src)
		}
	}
	s := ""
	if len(ev) > 0 {
		s = fmt.Sprintf("%s from %s, latest %s.", growthCount(float64(rows), "row", "rows"), growthAnd(names), latest)
	} else {
		s = "Tracked from product records."
	}
	if status == GrowthPartial && missing != "" {
		s += " " + missing
	}
	// Analytics downloads are noised; where the Sales report has the exact
	// count, say which number to trust.
	if k := app.Kpis["units"]; k.HasData && growthMatchAny(spec.patterns, "app_store_connect_first_time_downloads") {
		s += fmt.Sprintf(" Downloads use the Sales report: %s in %s.", growthCount(*k.Value, "new download", "new downloads"), growthDays(c.opts.WindowDays))
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
			if e.Metric == growthAdsUnconverted {
				continue
			}
			if isStale[e.Metric] {
				continue
			}
			label := "sales data is current"
			if e.Metric == growthAdsSpend {
				label = "ad spend is current"
				if c.adsCurrencyGap(g, spec) {
					label = "ads have reports with unconverted currency"
				}
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
	if missing == growthAdsCurrencyNote {
		return "Review spend by currency in the ads report; record a supported conversion before comparing USD costs."
	}
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

// ---------------------------------------------------------------------------
// allocation

const growthAllocationRule = "Last 90 days; proceeds end on the same day as the proceeds tile (the Sales report's newest final day). fix_data: earned proceeds but sales data is stale. grow: proceeds in the window with fresh data. measure: has web clicks or ad conversions but no proceeds or blind activation. hold: little proceeds and traffic. Ranked by 90-day proceeds, then web clicks."

func (c growthCtx) allocate(app GrowthApp, f growthFacts) GrowthAllocation {
	al := GrowthAllocation{ProductID: app.ProductID, Name: app.Name, Rule: growthAllocationRule, WindowStart: f.proceedsFrom, WindowEnd: f.proceedsTo}
	activationBlind := true
	for _, a := range app.Audit {
		if (a.AreaID == "activation_onboarding" || a.AreaID == "activation_time_to_value") && (a.Status == GrowthMeasured || a.Status == GrowthPartial || a.Status == GrowthStale) {
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
		al.Reason = fmt.Sprintf("%s proceeds in %s, %s to %s (%s in 90 days), with sales data fresh through %s.", growthUSD(f.proceeds30), growthDays(c.opts.WindowDays), f.proceedsFrom, f.proceedsTo, growthUSD(f.proceeds90), f.salesLatest)
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

var growthStatuses = map[string]bool{GrowthMeasured: true, GrowthPartial: true, GrowthStale: true, GrowthBlind: true, GrowthNotApplicable: true, GrowthUnknown: true, GrowthDeferred: true}

var growthFunnelStatuses = map[string]bool{GrowthMeasured: true, GrowthStale: true, GrowthBlind: true, GrowthNotApplicable: true}

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
	if g.WindowDays <= 0 || g.SeriesDays <= 0 || g.StaleDays <= 0 || g.AssessDays <= 0 {
		return fmt.Errorf("window_days, series_days and stale_days must be positive")
	}
	if len(g.Groups) == 0 || len(g.Areas) == 0 {
		return fmt.Errorf("catalog incomplete: %d groups, %d areas", len(g.Groups), len(g.Areas))
	}
	if c := g.Catalog; c.Source != "embedded" || c.Groups != len(g.Groups) || c.Areas != len(g.Areas) || c.SchemaVersion == "" {
		return fmt.Errorf("catalog block %+v does not match %d groups and %d areas", c, len(g.Groups), len(g.Areas))
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
			if !growthFunnelStatuses[f.Status] {
				return fmt.Errorf("app %s funnel %s status %q invalid", app.ProductID, f.ID, f.Status)
			}
		}
		cv := app.Coverage
		if cv.Measured+cv.Partial+cv.Stale+cv.Blind+cv.NotApplicable+cv.Unknown+cv.Deferred != len(g.Areas) {
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
		if len(app.NextMoves) > growthMaxMoves {
			return fmt.Errorf("app %s has %d next_moves, want at most %d", app.ProductID, len(app.NextMoves), growthMaxMoves)
		}
		for _, m := range app.NextMoves {
			if !growthMoveKinds[m.Kind] || strings.TrimSpace(m.Title) == "" || strings.TrimSpace(m.Brief) == "" {
				return fmt.Errorf("app %s next move %q (%s) is incomplete", app.ProductID, m.Title, m.Kind)
			}
			if m.AreaIDs == nil || (m.AreaID == "") != (len(m.AreaIDs) == 0) || (m.AreaID != "" && m.AreaIDs[0] != m.AreaID) {
				return fmt.Errorf("app %s next move %q area_ids %v must start with area_id %q", app.ProductID, m.Title, m.AreaIDs, m.AreaID)
			}
		}
	}
	return nil
}
