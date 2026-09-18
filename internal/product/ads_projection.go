package product

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The ads projection is the read model behind the console's ads panel and
// the `compound product ads --json` verb. It is a fold over the ads ledger:
// no ledger write, no fetch, every number traceable to imported rows.

type AdsProjectionOptions struct {
	ProductID     string
	Platform      string
	WindowDays    int
	AsOf          time.Time
	StaleAfter    time.Duration
	RecentImports int
	TopCampaigns  int
}

type AdsProjection struct {
	SchemaVersion string            `json:"schema_version"`
	AuthorityRef  string            `json:"authority_ref"`
	GeneratedAt   string            `json:"generated_at"`
	Status        string            `json:"status"`
	AsOf          string            `json:"as_of"`
	Window        AdsWindow         `json:"window"`
	Filters       AdsFilters        `json:"filters"`
	Counts        AdsCounts         `json:"counts"`
	Totals        AdsTotals         `json:"totals"`
	Previous      AdsTotals         `json:"previous"`
	Platforms     []AdsPlatformView `json:"platforms"`
	Products      []AdsProductView  `json:"products"`
	Campaigns     []AdsCampaignView `json:"campaigns"`
	Imports       []AdsImport       `json:"imports"`
	Gaps          []AdsGap          `json:"gaps"`
}

type AdsWindow struct {
	Days          int    `json:"days"`
	Start         string `json:"start"`
	End           string `json:"end"`
	PreviousStart string `json:"previous_start"`
	PreviousEnd   string `json:"previous_end"`
}

type AdsFilters struct {
	ProductID string `json:"product_id,omitempty"`
	Platform  string `json:"platform,omitempty"`
}

type AdsCounts struct {
	Rows                  int `json:"rows"`
	RowsInWindow          int `json:"rows_in_window"`
	Campaigns             int `json:"campaigns"`
	CampaignsActive       int `json:"campaigns_active"`
	Products              int `json:"products"`
	PlatformsConnected    int `json:"platforms_connected"`
	PlatformsStale        int `json:"platforms_stale"`
	PlatformsNotConnected int `json:"platforms_not_connected"`
	Imports               int `json:"imports"`
}

// AdsTotals is the one aggregate grammar reused at every level. Ratios are
// 0 when their denominator is 0; readers test the denominator, never the
// ratio, before rendering. Currency is the single currency of the rows,
// "MIXED" when they disagree (spend is then NOT a meaningful sum and
// SpendByCurrency is the honest figure), or "" when no row carried one.
//
// RevenueCoverage is the fraction of rows in the window that actually
// reported a revenue value (0 when none did, 1 when every row did). The
// HasRevenue flag is kept for backwards compatibility; a partial
// coverage MUST NOT be reported as a complete ROAS — RevenueCoverage
// below 1 means ROAS is "matched-population revenue over total spend",
// not "platform total revenue over total spend" (audit 2026-09-16).
type AdsTotals struct {
	Spend       float64 `json:"spend"`
	Impressions float64 `json:"impressions"`
	Clicks      float64 `json:"clicks"`
	Conversions float64 `json:"conversions"`
	Revenue     float64 `json:"revenue"`
	HasRevenue  bool    `json:"has_revenue"`
	// RevenueCoverage: rows-with-revenue / rows-total in the window.
	// 0 when no rows reported revenue; 1 when every row did; a
	// fractional value is partial coverage and the projection must NOT
	// render ROAS as a "complete" ratio in that case.
	RevenueCoverage float64            `json:"revenue_coverage"`
	CTR             float64            `json:"ctr"`
	CPC             float64            `json:"cpc"`
	CPA             float64            `json:"cpa"`
	CPM             float64            `json:"cpm"`
	ROAS            float64            `json:"roas"`
	Currency        string             `json:"currency,omitempty"`
	MixedCurrency   bool               `json:"mixed_currency"`
	SpendByCurrency map[string]float64 `json:"spend_by_currency,omitempty"`
	Days            int                `json:"days"`
	Rows            int                `json:"rows"`
}

type AdsDailyPoint struct {
	Date        string  `json:"date"`
	Spend       float64 `json:"spend"`
	Impressions float64 `json:"impressions"`
	Clicks      float64 `json:"clicks"`
	Conversions float64 `json:"conversions"`
	Revenue     float64 `json:"revenue"`
}

type AdsMove struct {
	Text    string `json:"text"`
	Command string `json:"command"`
}

type AdsPlatformView struct {
	Platform          string          `json:"platform"`
	Label             string          `json:"label"`
	Status            string          `json:"status"`
	ExportHint        string          `json:"export_hint"`
	FirstDate         string          `json:"first_date,omitempty"`
	LastDate          string          `json:"last_date,omitempty"`
	LastImportAt      string          `json:"last_import_at,omitempty"`
	DaysSinceLastDate int             `json:"days_since_last_date"`
	Totals            AdsTotals       `json:"totals"`
	Previous          AdsTotals       `json:"previous"`
	Daily             []AdsDailyPoint `json:"daily"`
	Products          []string        `json:"products"`
	CampaignsActive   int             `json:"campaigns_active"`
	CampaignsTotal    int             `json:"campaigns_total"`
	NextMove          AdsMove         `json:"next_move"`
}

type AdsPlatformShare struct {
	Platform string  `json:"platform"`
	Label    string  `json:"label"`
	Spend    float64 `json:"spend"`
	Share    float64 `json:"share"`
}

type AdsProductView struct {
	ProductID  string             `json:"product_id"`
	Totals     AdsTotals          `json:"totals"`
	Previous   AdsTotals          `json:"previous"`
	ByPlatform []AdsPlatformShare `json:"by_platform"`
	Daily      []AdsDailyPoint    `json:"daily"`
}

type AdsCampaignView struct {
	Platform     string          `json:"platform"`
	Label        string          `json:"label"`
	ProductID    string          `json:"product_id"`
	Account      string          `json:"account,omitempty"`
	CampaignID   string          `json:"campaign_id"`
	CampaignName string          `json:"campaign_name"`
	Status       string          `json:"status"`
	FirstDate    string          `json:"first_date"`
	LastDate     string          `json:"last_date"`
	Totals       AdsTotals       `json:"totals"`
	Previous     AdsTotals       `json:"previous"`
	Daily        []AdsDailyPoint `json:"daily"`
	SourceRef    string          `json:"source_ref"`
}

type AdsGap struct {
	Kind      string `json:"kind"`
	Platform  string `json:"platform,omitempty"`
	ProductID string `json:"product_id,omitempty"`
	Text      string `json:"text"`
	Command   string `json:"command,omitempty"`
}

// LoadAdsProjection folds the ads ledger into the panel read model.
func LoadAdsProjection(homePath string, opts AdsProjectionOptions) (AdsProjection, error) {
	now := opts.AsOf
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	days := opts.WindowDays
	if days <= 0 {
		days = adsDefaultWindowDays
	}
	staleAfter := opts.StaleAfter
	if staleAfter <= 0 {
		staleAfter = adsDefaultStaleAfter
	}
	recent := opts.RecentImports
	if recent <= 0 {
		recent = 10
	}
	topCampaigns := opts.TopCampaigns
	if topCampaigns <= 0 {
		topCampaigns = 50
	}
	filterProduct := strings.TrimSpace(opts.ProductID)
	filterPlatform := normalizeIDPart(opts.Platform)
	if filterPlatform != "" {
		if _, ok := LookupAdPlatform(filterPlatform); !ok {
			return AdsProjection{}, fmt.Errorf("ads projection: unknown platform %q (known: %s)", opts.Platform, strings.Join(AdPlatformIDs(), ", "))
		}
	}

	asOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	windowEnd := asOfDay
	windowStart := asOfDay.AddDate(0, 0, -(days - 1))
	prevEnd := windowStart.AddDate(0, 0, -1)
	prevStart := prevEnd.AddDate(0, 0, -(days - 1))
	window := AdsWindow{
		Days:          days,
		Start:         windowStart.Format("2006-01-02"),
		End:           windowEnd.Format("2006-01-02"),
		PreviousStart: prevStart.Format("2006-01-02"),
		PreviousEnd:   prevEnd.Format("2006-01-02"),
	}
	inWindow := func(date string) bool { return date >= window.Start && date <= window.End }
	inPrevious := func(date string) bool { return date >= window.PreviousStart && date <= window.PreviousEnd }

	history, err := LoadAdRows(homePath)
	if err != nil {
		return AdsProjection{}, err
	}
	imports, err := LoadAdsImports(homePath)
	if err != nil {
		return AdsProjection{}, err
	}
	all := LatestAdRows(history)
	rows := make([]AdRow, 0, len(all))
	for _, r := range all {
		if filterProduct != "" && r.ProductID != filterProduct {
			continue
		}
		if filterPlatform != "" && r.Platform != filterPlatform {
			continue
		}
		rows = append(rows, r)
	}

	proj := AdsProjection{
		SchemaVersion: AdsProjectionSchemaVersion,
		AuthorityRef:  AdsAuthorityRef,
		GeneratedAt:   now.Format(time.RFC3339),
		AsOf:          window.End,
		Window:        window,
		Filters:       AdsFilters{ProductID: filterProduct, Platform: filterPlatform},
		Platforms:     []AdsPlatformView{},
		Products:      []AdsProductView{},
		Campaigns:     []AdsCampaignView{},
		Imports:       []AdsImport{},
		Gaps:          []AdsGap{},
	}
	proj.Counts.Rows = len(rows)
	proj.Counts.Imports = len(imports)

	var windowRows, prevRows []AdRow
	for _, r := range rows {
		switch {
		case inWindow(r.Date):
			windowRows = append(windowRows, r)
		case inPrevious(r.Date):
			prevRows = append(prevRows, r)
		}
	}
	proj.Counts.RowsInWindow = len(windowRows)
	proj.Totals = adsTotals(windowRows)
	proj.Previous = adsTotals(prevRows)

	lastImportByPlatform := map[string]string{}
	for _, imp := range imports {
		if filterProduct != "" && imp.ProductID != filterProduct {
			continue
		}
		if imp.ImportedAt > lastImportByPlatform[imp.Platform] {
			lastImportByPlatform[imp.Platform] = imp.ImportedAt
		}
	}

	// Platforms: the whole registry, so "not connected" is a rendered state.
	for _, p := range adPlatforms {
		if filterPlatform != "" && p.ID != filterPlatform {
			continue
		}
		var platformRows, pWindow, pPrev []AdRow
		products := map[string]struct{}{}
		campaigns := map[string]string{}
		for _, r := range rows {
			if r.Platform != p.ID {
				continue
			}
			platformRows = append(platformRows, r)
			products[r.ProductID] = struct{}{}
			key := r.ProductID + "\x00" + r.Account + "\x00" + r.CampaignID
			if r.Date > campaigns[key] {
				campaigns[key] = r.Date
			}
			if inWindow(r.Date) {
				pWindow = append(pWindow, r)
			} else if inPrevious(r.Date) {
				pPrev = append(pPrev, r)
			}
		}
		view := AdsPlatformView{
			Platform:     p.ID,
			Label:        p.Label,
			ExportHint:   p.ExportHint,
			Totals:       adsTotals(pWindow),
			Previous:     adsTotals(pPrev),
			Daily:        adsDaily(pWindow, windowStart, days),
			Products:     adsSortedKeys(products),
			LastImportAt: lastImportByPlatform[p.ID],
		}
		for _, r := range platformRows {
			if view.FirstDate == "" || r.Date < view.FirstDate {
				view.FirstDate = r.Date
			}
			if r.Date > view.LastDate {
				view.LastDate = r.Date
			}
		}
		activeCutoff := asOfDay.Add(-staleAfter).Format("2006-01-02")
		for _, last := range campaigns {
			view.CampaignsTotal++
			if last >= activeCutoff {
				view.CampaignsActive++
			}
		}
		if view.LastDate != "" {
			if last, err := time.Parse("2006-01-02", view.LastDate); err == nil {
				view.DaysSinceLastDate = int(asOfDay.Sub(last).Hours() / 24)
			}
		}
		productArg := "<product:id>"
		if filterProduct != "" {
			productArg = filterProduct
		} else if len(view.Products) == 1 {
			productArg = view.Products[0]
		}
		importCmd := fmt.Sprintf("compound product ads import --platform %s --product %s --report <export.csv>", p.ID, productArg)
		switch {
		case len(platformRows) == 0:
			view.Status = "not_connected"
			text := fmt.Sprintf("Not connected. Export a daily campaign report from %s and import it.", p.Label)
			if p.ID == "other" {
				text = "Any other platform: rename the export's headers to the canonical names and import it under `other`."
			}
			view.NextMove = AdsMove{Text: text, Command: importCmd}
			proj.Counts.PlatformsNotConnected++
			if p.ID != "other" {
				proj.Gaps = append(proj.Gaps, AdsGap{Kind: "platform_not_connected", Platform: p.ID, Text: p.Label + " has no imported report yet.", Command: importCmd})
			}
		case view.LastDate < activeCutoff:
			view.Status = "stale"
			view.NextMove = AdsMove{
				Text:    fmt.Sprintf("Newest row is %d days old. Export the latest %s report and import it.", view.DaysSinceLastDate, p.Label),
				Command: importCmd,
			}
			proj.Counts.PlatformsStale++
			proj.Gaps = append(proj.Gaps, AdsGap{Kind: "platform_stale", Platform: p.ID, Text: fmt.Sprintf("%s rows stop at %s (%d days ago).", p.Label, view.LastDate, view.DaysSinceLastDate), Command: importCmd})
		default:
			view.Status = "connected"
			view.NextMove = AdsMove{
				Text:    "Current. Turn this window into a product signal so the loop can review it against a hypothesis.",
				Command: fmt.Sprintf("compound product ads signal --platform %s --product %s --before %s/%s --after %s/%s", p.ID, productArg, window.PreviousStart, window.PreviousEnd, window.Start, window.End),
			}
			proj.Counts.PlatformsConnected++
		}
		proj.Platforms = append(proj.Platforms, view)
	}

	// Products: roll-up with platform split.
	productRows := map[string][]AdRow{}
	for _, r := range rows {
		productRows[r.ProductID] = append(productRows[r.ProductID], r)
	}
	for _, productID := range adsSortedKeys(adsProductSet(productRows)) {
		prs := productRows[productID]
		var w, prev []AdRow
		byPlatform := map[string]float64{}
		for _, r := range prs {
			if inWindow(r.Date) {
				w = append(w, r)
				byPlatform[r.Platform] += r.Spend
			} else if inPrevious(r.Date) {
				prev = append(prev, r)
			}
		}
		view := AdsProductView{
			ProductID:  productID,
			Totals:     adsTotals(w),
			Previous:   adsTotals(prev),
			Daily:      adsDaily(w, windowStart, days),
			ByPlatform: []AdsPlatformShare{},
		}
		for _, p := range adPlatforms {
			spend, ok := byPlatform[p.ID]
			if !ok {
				continue
			}
			share := 0.0
			if view.Totals.Spend > 0 && !view.Totals.MixedCurrency {
				share = spend / view.Totals.Spend
			}
			view.ByPlatform = append(view.ByPlatform, AdsPlatformShare{Platform: p.ID, Label: p.Label, Spend: spend, Share: share})
		}
		proj.Products = append(proj.Products, view)
	}
	sort.SliceStable(proj.Products, func(i, j int) bool {
		return proj.Products[i].Totals.Spend > proj.Products[j].Totals.Spend
	})
	proj.Counts.Products = len(proj.Products)

	// Campaigns: every campaign with a row in the window or the previous one.
	type campaignKey struct{ platform, product, account, campaign string }
	campaignRows := map[campaignKey][]AdRow{}
	var campaignOrder []campaignKey
	for _, r := range rows {
		k := campaignKey{r.Platform, r.ProductID, r.Account, r.CampaignID}
		if _, seen := campaignRows[k]; !seen {
			campaignOrder = append(campaignOrder, k)
		}
		campaignRows[k] = append(campaignRows[k], r)
	}
	activeCutoff := asOfDay.Add(-staleAfter).Format("2006-01-02")
	for _, k := range campaignOrder {
		crs := campaignRows[k]
		var w, prev []AdRow
		view := AdsCampaignView{Platform: k.platform, ProductID: k.product, Account: k.account, CampaignID: k.campaign}
		if p, ok := LookupAdPlatform(k.platform); ok {
			view.Label = p.Label
		}
		for _, r := range crs {
			if view.CampaignName == "" || r.Date >= view.LastDate {
				view.CampaignName = r.CampaignName
				view.SourceRef = r.SourceRef
			}
			if view.FirstDate == "" || r.Date < view.FirstDate {
				view.FirstDate = r.Date
			}
			if r.Date > view.LastDate {
				view.LastDate = r.Date
			}
			if inWindow(r.Date) {
				w = append(w, r)
			} else if inPrevious(r.Date) {
				prev = append(prev, r)
			}
		}
		proj.Counts.Campaigns++
		if view.LastDate >= activeCutoff {
			view.Status = "active"
			proj.Counts.CampaignsActive++
		} else {
			view.Status = "quiet"
		}
		if len(w) == 0 && len(prev) == 0 {
			continue
		}
		view.Totals = adsTotals(w)
		view.Previous = adsTotals(prev)
		view.Daily = adsDaily(w, windowStart, days)
		proj.Campaigns = append(proj.Campaigns, view)
	}
	sort.SliceStable(proj.Campaigns, func(i, j int) bool {
		a, b := proj.Campaigns[i], proj.Campaigns[j]
		if a.Totals.Spend != b.Totals.Spend {
			return a.Totals.Spend > b.Totals.Spend
		}
		return a.LastDate > b.LastDate
	})
	if len(proj.Campaigns) > topCampaigns {
		proj.Campaigns = proj.Campaigns[:topCampaigns]
	}

	// Imports: newest first, bounded.
	filteredImports := make([]AdsImport, 0, len(imports))
	for _, imp := range imports {
		if filterProduct != "" && imp.ProductID != filterProduct {
			continue
		}
		if filterPlatform != "" && imp.Platform != filterPlatform {
			continue
		}
		filteredImports = append(filteredImports, imp)
	}
	for i := len(filteredImports) - 1; i >= 0 && len(proj.Imports) < recent; i-- {
		proj.Imports = append(proj.Imports, filteredImports[i])
	}

	// Cross-cutting gaps.
	if proj.Totals.MixedCurrency {
		proj.Gaps = append(proj.Gaps, AdsGap{Kind: "mixed_currency", Text: "Rows in this window carry more than one currency; the spend total is not summed across them. Re-import with --currency or read spend_by_currency."})
	}
	if len(windowRows) > 0 && !proj.Totals.HasRevenue {
		proj.Gaps = append(proj.Gaps, AdsGap{Kind: "no_revenue", Text: "No imported report carries a conversion value, so ROAS is undefined. Export with a revenue/conversion-value column when the platform has one."})
	}
	if len(windowRows) > 0 && proj.Totals.Conversions == 0 {
		proj.Gaps = append(proj.Gaps, AdsGap{Kind: "no_conversions", Text: "No conversions in this window: either the reports carry no installs/conversions column or nothing converted. CPA is undefined."})
	}

	switch {
	case len(rows) == 0:
		proj.Status = "empty"
	case len(windowRows) == 0:
		proj.Status = "quiet"
	default:
		proj.Status = "ok"
	}
	return proj, nil
}

func adsTotals(rows []AdRow) AdsTotals {
	t := AdsTotals{Rows: len(rows)}
	if len(rows) == 0 {
		return t
	}
	currencies := map[string]float64{}
	days := map[string]struct{}{}
	rowsWithRevenue := 0
	for _, r := range rows {
		t.Spend += r.Spend
		t.Impressions += r.Impressions
		t.Clicks += r.Clicks
		t.Conversions += r.Conversions
		if r.HasRevenue {
			t.HasRevenue = true
			t.Revenue += r.Revenue
			rowsWithRevenue++
		}
		if r.Currency != "" {
			currencies[r.Currency] += r.Spend
		}
		days[r.Date] = struct{}{}
	}
	t.Days = len(days)
	t.RevenueCoverage = float64(rowsWithRevenue) / float64(len(rows))
	switch len(currencies) {
	case 0:
		t.Currency = ""
	case 1:
		for c := range currencies {
			t.Currency = c
		}
	default:
		t.Currency = adsCurrencyMixed
		t.MixedCurrency = true
		t.SpendByCurrency = currencies
	}
	if t.Impressions > 0 {
		t.CTR = t.Clicks / t.Impressions
		t.CPM = t.Spend / t.Impressions * 1000
	}
	if t.Clicks > 0 {
		t.CPC = t.Spend / t.Clicks
	}
	if t.Conversions > 0 {
		t.CPA = t.Spend / t.Conversions
	}
	// ROAS is the platform-total ratio only when EVERY contributing row
	// in the window reported revenue. Partial coverage (some rows
	// empty) means we have matched-population revenue over total spend,
	// which is NOT the same number — leave ROAS at 0 and let the
	// projection surface RevenueCoverage so the consumer can decide
	// (audit 2026-09-16 finding 3).
	if t.HasRevenue && t.Spend > 0 && t.RevenueCoverage >= 1 {
		t.ROAS = t.Revenue / t.Spend
	}
	return t
}

// adsDaily returns one point per day of the window, zero-filled, so a
// sparkline reads gaps as gaps rather than compressing them away.
func adsDaily(rows []AdRow, start time.Time, days int) []AdsDailyPoint {
	byDate := map[string]*AdsDailyPoint{}
	out := make([]AdsDailyPoint, days)
	for i := 0; i < days; i++ {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		out[i] = AdsDailyPoint{Date: date}
		byDate[date] = &out[i]
	}
	for _, r := range rows {
		p, ok := byDate[r.Date]
		if !ok {
			continue
		}
		p.Spend += r.Spend
		p.Impressions += r.Impressions
		p.Clicks += r.Clicks
		p.Conversions += r.Conversions
		if r.HasRevenue {
			p.Revenue += r.Revenue
		}
	}
	return out
}

func adsSortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func adsProductSet(m map[string][]AdRow) map[string]struct{} {
	out := make(map[string]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}
