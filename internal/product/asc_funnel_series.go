package product

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// ascFunnelConversionRateMetric is the derived rate metric: first_time_downloads
// over product_page_views, computed only across coverage.complete_funnel rows.
// It canonicalizes to app_store_product_page_conversion via
// canonicalSignalMetricNames, the same metric name existing hypotheses already
// reference — this bridge is expected to be the thing that finally feeds them.
const ascFunnelConversionRateMetric = "product_page_conversion_rate"

// ASCFunnelSeriesSignalOptions configures LoadASCFunnelSeriesSignal. It mirrors
// ASCSalesSeriesSignalOptions deliberately: same explicit before/after window
// shape, same required fields, so the two adapters read as one family from the
// CLI down.
type ASCFunnelSeriesSignalOptions struct {
	ProductID    string
	AppID        string
	Metric       string
	BeforeWindow string
	AfterWindow  string
	ChangeEvent  string
}

// ascFunnelCoverage decodes only the one coverage flag this adapter depends on.
// Rows produced before the collector started emitting a coverage object at all
// decode to the zero value (CompleteFunnel == false), which is the safe
// default: an unannotated day is treated as unmeasured, never as a true zero.
type ascFunnelCoverage struct {
	CompleteFunnel bool `json:"complete_funnel"`
}

// ascFunnelSeriesRow is the per-day per-app funnel row. It intentionally has no
// Name or Slug field: this adapter must be structurally unable to match rows by
// display name, only by ASCAppID, which is the one identifier the artifact
// owner (the ASC funnel collector) treats as authoritative.
type ascFunnelSeriesRow struct {
	Date               string            `json:"date"`
	ASCAppID           string            `json:"asc_app_id"`
	Impressions        float64           `json:"impressions"`
	ProductPageViews   float64           `json:"product_page_views"`
	FirstTimeDownloads float64           `json:"first_time_downloads"`
	ProceedsUSD        float64           `json:"proceeds_usd"`
	PayingUsers        float64           `json:"paying_users"`
	Coverage           ascFunnelCoverage `json:"coverage"`
}

// ascFunnelRawMetrics are the plain per-day sums this adapter can aggregate.
// The conversion rate is handled separately in aggregateFunnelWindow because it
// is a ratio of two of these, not a sum of one.
var ascFunnelRawMetrics = map[string]func(ascFunnelSeriesRow) float64{
	"impressions":          func(r ascFunnelSeriesRow) float64 { return r.Impressions },
	"product_page_views":   func(r ascFunnelSeriesRow) float64 { return r.ProductPageViews },
	"first_time_downloads": func(r ascFunnelSeriesRow) float64 { return r.FirstTimeDownloads },
	"proceeds_usd":         func(r ascFunnelSeriesRow) float64 { return r.ProceedsUSD },
	"paying_users":         func(r ascFunnelSeriesRow) float64 { return r.PayingUsers },
}

type funnelWindowAggregate struct {
	Value float64
	Rows  int
	Start string
	End   string
}

// LoadASCFunnelSeriesSignal reads an existing ASC funnel daily-app-series JSONL
// export (already produced upstream; this never fetches ASC data itself) and
// turns a before/after window comparison for one product/app/metric into a
// ProductSignal. It is the funnel-schema sibling of LoadASCSalesSeriesSignal.
func LoadASCFunnelSeriesSignal(path string, opts ASCFunnelSeriesSignalOptions) (SignalDocument, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return SignalDocument{}, err
	}
	rows, err := parseASCFunnelSeriesRows(b, path)
	if err != nil {
		return SignalDocument{}, err
	}
	return adaptASCFunnelSeriesSignal(rows, opts)
}

func parseASCFunnelSeriesRows(b []byte, source string) ([]ascFunnelSeriesRow, error) {
	var rows []ascFunnelSeriesRow
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row ascFunnelSeriesRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("parse ASC funnel series %s line %d: %w", source, i+1, err)
		}
		if strings.TrimSpace(row.ASCAppID) == "" || strings.TrimSpace(row.Date) == "" {
			return nil, fmt.Errorf("ASC funnel series %s line %d missing asc_app_id/date", source, i+1)
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("ASC funnel series %s has no rows", source)
	}
	return rows, nil
}

func adaptASCFunnelSeriesSignal(rows []ascFunnelSeriesRow, opts ASCFunnelSeriesSignalOptions) (SignalDocument, error) {
	productID := strings.TrimSpace(opts.ProductID)
	appID := strings.TrimSpace(opts.AppID)
	metric := strings.TrimSpace(opts.Metric)
	if metric == "" {
		metric = ascFunnelConversionRateMetric
	}
	changeEvent := strings.TrimSpace(opts.ChangeEvent)
	if productID == "" || appID == "" || changeEvent == "" {
		return SignalDocument{}, fmt.Errorf("ASC funnel series product_id app_id change_event required")
	}
	beforeRange, err := parseDateRange(opts.BeforeWindow)
	if err != nil {
		return SignalDocument{}, fmt.Errorf("ASC funnel series before window: %w", err)
	}
	afterRange, err := parseDateRange(opts.AfterWindow)
	if err != nil {
		return SignalDocument{}, fmt.Errorf("ASC funnel series after window: %w", err)
	}
	before, err := aggregateFunnelWindow(rows, appID, metric, beforeRange)
	if err != nil {
		return SignalDocument{}, err
	}
	after, err := aggregateFunnelWindow(rows, appID, metric, afterRange)
	if err != nil {
		return SignalDocument{}, err
	}
	metricName := canonicalMetricName("app_store_connect", metric)
	sourceRef := "asc:funnel_series:" + appID + ":" + beforeRange.Raw + ":" + afterRange.Raw
	confounders := []string{
		"explicit window comparison; causality is not implied",
		"seasonality, channel mix, price, reviews, and release timing may confound movement",
		"aggregated only over days where coverage.complete_funnel is true; incomplete-coverage days are excluded from the count, never treated as a true zero",
	}
	if normalizeIDPart(metric) == ascFunnelConversionRateMetric {
		confounders = append(confounders, "first_time_downloads can exceed product_page_views on the same day because iOS allows installing directly from the search card; a rate above 1.0 reflects that install path, not a data error")
	}
	evidenceRefs := compact([]string{
		"asc:funnel_series:" + appID,
		"asc:funnel_series:metric:" + normalizeIDPart(metric),
		"asc:funnel_series:before:" + beforeRange.Raw,
		"asc:funnel_series:after:" + afterRange.Raw,
		fmt.Sprintf("asc:funnel_series:before_complete_funnel_rows:%d", before.Rows),
		fmt.Sprintf("asc:funnel_series:after_complete_funnel_rows:%d", after.Rows),
		"asc:funnel_series:before_value:" + shortFloat(before.Value),
		"asc:funnel_series:after_value:" + shortFloat(after.Value),
	})
	return SignalDocument{
		SchemaVersion: SignalSchemaVersion,
		Source: SignalSource{
			Kind: "app_store_connect",
			Ref:  sourceRef,
		},
		Signal: Signal{
			ID:          stableID("sig_", productID, "app_store_connect", appID, metricName, beforeRange.Raw, afterRange.Raw, changeEvent),
			ProductID:   productID,
			Surface:     "app_store",
			ChangeEvent: changeEvent,
			MetricName:  metricName,
			Before:      before.Value,
			After:       after.Value,
			Window:      "before " + beforeRange.Raw + "; after " + afterRange.Raw,
			Confidence:  0.6,
			Confounders: confounders,
			Interpretations: []Interpretation{{
				Lens:  "window_comparison",
				Claim: "records before/after App Store Connect funnel metric movement for review, not proof of causality",
			}},
			NextAction:   "Review against the experiment ledger and confounders before promoting any product hypothesis.",
			EvidenceRefs: evidenceRefs,
			ObservedAt:   afterRange.End.Format("2006-01-02") + "T00:00:00Z",
		},
		EvidenceRefs: evidenceRefs,
	}, nil
}

// aggregateFunnelWindow sums (or, for the conversion rate, ratios) exactly one
// metric for exactly one asc_app_id across one date window. Two invariants are
// load-bearing here, both directly load-bearing for kill-criteria evidence
// quality, and both covered by tests that fail if either is dropped:
//
//  1. Matching is by row.ASCAppID only. The row type has no Name/Slug field, so
//     there is nothing else this function could match on even by accident.
//  2. Only rows with Coverage.CompleteFunnel == true contribute. A day without
//     confirmed complete-funnel coverage has impressions/product_page_views
//     fields that read as 0 in the source artifact because they are missing,
//     not because nothing happened; including them would silently deflate
//     every rate computed across the window.
func aggregateFunnelWindow(rows []ascFunnelSeriesRow, appID, metric string, window dateRange) (funnelWindowAggregate, error) {
	normalizedMetric := normalizeIDPart(metric)
	isRate := normalizedMetric == ascFunnelConversionRateMetric
	rawMetric, isRaw := ascFunnelRawMetrics[normalizedMetric]
	if !isRate && !isRaw {
		return funnelWindowAggregate{}, fmt.Errorf("ASC funnel series metric %q unsupported", metric)
	}
	var out funnelWindowAggregate
	var downloadsSum, pageViewsSum float64
	for _, row := range rows {
		if strings.TrimSpace(row.ASCAppID) != appID {
			continue
		}
		if !row.Coverage.CompleteFunnel {
			continue
		}
		day, err := time.Parse("2006-01-02", strings.TrimSpace(row.Date))
		if err != nil {
			return funnelWindowAggregate{}, fmt.Errorf("ASC funnel series row date %q: %w", row.Date, err)
		}
		if day.Before(window.Start) || day.After(window.End) {
			continue
		}
		if isRate {
			downloadsSum += row.FirstTimeDownloads
			pageViewsSum += row.ProductPageViews
		} else {
			out.Value += rawMetric(row)
		}
		out.Rows++
		if out.Start == "" || row.Date < out.Start {
			out.Start = row.Date
		}
		if row.Date > out.End {
			out.End = row.Date
		}
	}
	if out.Rows == 0 {
		return funnelWindowAggregate{}, fmt.Errorf("ASC funnel series app_id %q has no coverage.complete_funnel rows in %s", appID, window.Raw)
	}
	if isRate {
		if pageViewsSum <= 0 {
			return funnelWindowAggregate{}, fmt.Errorf("ASC funnel series app_id %q has no product_page_views in %s to compute a conversion rate", appID, window.Raw)
		}
		out.Value = downloadsSum / pageViewsSum
	}
	return out, nil
}
