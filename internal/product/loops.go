package product

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"compound/internal/candidates"
	"compound/internal/mem"
)

const LoopModelSchemaVersion = "compound.product_loops.v0.1"

type LoopOptions struct {
	ProductID  string
	Recent     int
	StaleDays  int
	StaleAfter time.Duration
	Now        time.Time
}

type LoopModel struct {
	SchemaVersion string        `json:"schema_version"`
	GeneratedAt   string        `json:"generated_at"`
	Products      []ProductLoop `json:"products"`
}

type ProductLoop struct {
	ProductID        string                  `json:"product_id"`
	Name             string                  `json:"name"`
	SignalsByMetric  map[string][]LoopSignal `json:"signals_by_metric"`
	OpenHypotheses   []LoopHypothesis        `json:"open_hypotheses"`
	Reviews          []LoopReview            `json:"reviews"`
	OpenSourceIssues LoopSourceIssues        `json:"open_source_issues"`
	Freshness        LoopFreshness           `json:"freshness"`
	LoopGaps         []string                `json:"loop_gaps"`
}

type LoopSignal struct {
	SignalID      string  `json:"signal_id"`
	Provider      string  `json:"provider"`
	Metric        string  `json:"metric"`
	Before        float64 `json:"before"`
	After         float64 `json:"after"`
	ObservedAt    string  `json:"observed_at"`
	ChangeEvent   string  `json:"change_event,omitempty"`
	TransitionRef string  `json:"transition_ref,omitempty"`
}

type LoopHypothesis struct {
	ID             string `json:"id"`
	Statement      string `json:"statement"`
	ExpectedEffect string `json:"expected_effect"`
	State          string `json:"state"`
	CreatedAt      string `json:"created_at,omitempty"`
}

type LoopReview struct {
	ID           string `json:"id"`
	HypothesisID string `json:"hypothesis_id"`
	SignalID     string `json:"signal_id,omitempty"`
	Verdict      string `json:"verdict"`
	ReviewedAt   string `json:"reviewed_at,omitempty"`
}

type LoopSourceIssues struct {
	Count int      `json:"count"`
	Refs  []string `json:"refs"`
}

type LoopFreshness struct {
	LatestSignalAt string `json:"latest_signal_at,omitempty"`
	LatestReviewAt string `json:"latest_review_at,omitempty"`
	Stale          bool   `json:"stale"`
}

func BuildLoopModel(homePath string, opts LoopOptions) (LoopModel, error) {
	opts = normalizeLoopOptions(opts)
	store, err := mem.Open(homePath)
	if err != nil {
		return LoopModel{}, err
	}
	authority, err := Authority(homePath, store)
	if err != nil {
		return LoopModel{}, err
	}
	reviews, err := loadReviews(homePath)
	if err != nil {
		return LoopModel{}, err
	}
	names, err := productLoopNames(store)
	if err != nil {
		return LoopModel{}, err
	}
	model := LoopModel{
		SchemaVersion: LoopModelSchemaVersion,
		GeneratedAt:   opts.Now.UTC().Format(time.RFC3339),
		Products:      []ProductLoop{},
	}
	for _, item := range authority.Products {
		if opts.ProductID != "" && item.ProductID != opts.ProductID {
			continue
		}
		loop, err := buildProductLoop(homePath, item.ProductID, names[item.ProductID], reviews, opts)
		if err != nil {
			return LoopModel{}, err
		}
		model.Products = append(model.Products, loop)
	}
	sort.Slice(model.Products, func(i, j int) bool {
		return model.Products[i].ProductID < model.Products[j].ProductID
	})
	return model, nil
}

func (m LoopModel) Validate() error {
	if m.SchemaVersion != LoopModelSchemaVersion {
		return fmt.Errorf("schema_version = %q, want %q", m.SchemaVersion, LoopModelSchemaVersion)
	}
	if strings.TrimSpace(m.GeneratedAt) == "" {
		return fmt.Errorf("generated_at required")
	}
	if _, err := time.Parse(time.RFC3339, m.GeneratedAt); err != nil {
		return fmt.Errorf("generated_at invalid: %w", err)
	}
	for _, product := range m.Products {
		if strings.TrimSpace(product.ProductID) == "" {
			return fmt.Errorf("product_id required")
		}
		if strings.TrimSpace(product.Name) == "" {
			return fmt.Errorf("product %s name required", product.ProductID)
		}
		if product.OpenSourceIssues.Count != len(product.OpenSourceIssues.Refs) {
			return fmt.Errorf("product %s open_source_issues count mismatch", product.ProductID)
		}
		for metric, signals := range product.SignalsByMetric {
			if strings.TrimSpace(metric) == "" {
				return fmt.Errorf("product %s has empty metric key", product.ProductID)
			}
			for _, signal := range signals {
				if strings.TrimSpace(signal.SignalID) == "" {
					return fmt.Errorf("product %s signal_id required", product.ProductID)
				}
				if signal.Metric != metric {
					return fmt.Errorf("product %s signal %s metric %q mismatches key %q", product.ProductID, signal.SignalID, signal.Metric, metric)
				}
			}
		}
	}
	return nil
}

func normalizeLoopOptions(opts LoopOptions) LoopOptions {
	if opts.Recent <= 0 {
		opts.Recent = 10
	}
	if opts.StaleAfter <= 0 {
		days := opts.StaleDays
		if days <= 0 {
			days = 7
		}
		opts.StaleAfter = time.Duration(days) * 24 * time.Hour
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	opts.ProductID = strings.TrimSpace(opts.ProductID)
	return opts
}

func buildProductLoop(homePath, productID, name string, reviews []Review, opts LoopOptions) (ProductLoop, error) {
	tl, err := LoadTimeline(homePath, productID)
	if err != nil {
		return ProductLoop{}, err
	}
	doctor, err := Doctor(homePath, productID)
	if err != nil {
		return ProductLoop{}, err
	}
	if name == "" {
		name = productID
	}
	openHypotheses := openLoopHypotheses(tl.Hypotheses)
	signalsByMetric, includedSignals := loopSignalsByMetric(tl.Signals, tl.Transitions, opts.Recent)
	productReviews := loopReviews(productID, reviews, opts.Recent)
	sourceIssues := loopSourceIssues(doctor.SourceIssues)
	return ProductLoop{
		ProductID:        productID,
		Name:             name,
		SignalsByMetric:  signalsByMetric,
		OpenHypotheses:   openHypotheses,
		Reviews:          productReviews,
		OpenSourceIssues: sourceIssues,
		Freshness:        loopFreshness(tl.Signals, productReviews, opts.Now, opts.StaleAfter),
		LoopGaps:         loopGaps(tl.Hypotheses, tl.Signals, includedSignals, doctor),
	}, nil
}

func productLoopNames(store *mem.Store) (map[string]string, error) {
	names := map[string]string{}
	if store == nil {
		return names, nil
	}
	claims, err := store.AllCurrent()
	if err != nil {
		return nil, err
	}
	for _, claim := range claims {
		if claim.Qualifiers["domain"] != "product" {
			continue
		}
		if claim.Predicate == "name" || claim.Predicate == "display_name" {
			names[claim.Entity] = claim.Object
		}
	}
	return names, nil
}

func loopSignalsByMetric(signals []Signal, transitions []TimelineTransition, recent int) (map[string][]LoopSignal, []Signal) {
	transitionBySignal := map[string]string{}
	for _, tr := range transitions {
		kind, id, ok := strings.Cut(tr.ObjectRef, ":")
		if ok && kind == "product_signal" && id != "" {
			transitionBySignal[id] = tr.ID
		}
	}
	ordered := append([]Signal(nil), signals...)
	sort.Slice(ordered, func(i, j int) bool {
		if compareRFC3339(ordered[i].ObservedAt, ordered[j].ObservedAt) == 0 {
			return ordered[i].ID < ordered[j].ID
		}
		return compareRFC3339(ordered[i].ObservedAt, ordered[j].ObservedAt) > 0
	})
	out := map[string][]LoopSignal{}
	var included []Signal
	for _, signal := range ordered {
		metric := strings.TrimSpace(signal.MetricName)
		if metric == "" {
			metric = "unknown"
		}
		if len(out[metric]) >= recent {
			continue
		}
		out[metric] = append(out[metric], LoopSignal{
			SignalID:      signal.ID,
			Provider:      loopSignalProvider(signal),
			Metric:        metric,
			Before:        signal.Before,
			After:         signal.After,
			ObservedAt:    signal.ObservedAt,
			ChangeEvent:   signal.ChangeEvent,
			TransitionRef: transitionBySignal[signal.ID],
		})
		included = append(included, signal)
	}
	return out, included
}

func loopSignalProvider(signal Signal) string {
	metric := normalizeIDPart(signal.MetricName)
	for key, canonical := range canonicalSignalMetricNames {
		if normalizeIDPart(canonical) != metric {
			continue
		}
		provider, _, ok := strings.Cut(key, ":")
		if ok && provider != "" {
			return provider
		}
	}
	switch {
	case strings.HasPrefix(metric, "gsc_"):
		return "google_search_console"
	case strings.HasPrefix(metric, "ga4_"):
		return "google_analytics"
	case strings.HasPrefix(metric, "adsense_"):
		return "google_adsense"
	case strings.HasPrefix(metric, "app_store_connect_") || strings.HasPrefix(metric, "app_store_"):
		return "app_store_connect"
	case strings.HasPrefix(metric, "revenuecat_"):
		return "revenuecat"
	case strings.HasPrefix(metric, "posthog_"):
		return "posthog"
	default:
		return "unknown"
	}
}

func openLoopHypotheses(hypotheses []HypothesisCandidate) []LoopHypothesis {
	var out []LoopHypothesis
	for _, h := range hypotheses {
		if !isOpenLoopHypothesis(h) {
			continue
		}
		out = append(out, LoopHypothesis{
			ID:             h.ID,
			Statement:      h.ChangeEvent,
			ExpectedEffect: h.ExpectedEffect,
			State:          "open",
			CreatedAt:      h.CreatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if compareRFC3339(out[i].CreatedAt, out[j].CreatedAt) == 0 {
			return out[i].ID < out[j].ID
		}
		return compareRFC3339(out[i].CreatedAt, out[j].CreatedAt) > 0
	})
	return out
}

func isOpenLoopHypothesis(h HypothesisCandidate) bool {
	result := strings.TrimSpace(h.Result)
	verdict := strings.TrimSpace(h.Verdict)
	if result != "" && result != "unknown" {
		return false
	}
	return verdict == "" || verdict == candidates.Park
}

func loopReviews(productID string, reviews []Review, recent int) []LoopReview {
	var out []LoopReview
	for _, review := range reviews {
		if review.ProductID != productID {
			continue
		}
		out = append(out, LoopReview{
			ID:           review.ID,
			HypothesisID: review.HypothesisID,
			SignalID:     productSignalRef(review.EvidenceRefs),
			Verdict:      review.Result,
			ReviewedAt:   firstNonEmpty(review.ReviewedAt, review.AppliedAt),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if compareRFC3339(out[i].ReviewedAt, out[j].ReviewedAt) == 0 {
			return out[i].ID < out[j].ID
		}
		return compareRFC3339(out[i].ReviewedAt, out[j].ReviewedAt) > 0
	})
	if len(out) > recent {
		out = out[:recent]
	}
	return out
}

func productSignalRef(refs []string) string {
	for _, ref := range refs {
		id, ok := strings.CutPrefix(strings.TrimSpace(ref), "product_signal:")
		if ok {
			return id
		}
	}
	return ""
}

func loopSourceIssues(issues []ProductSourceIssue) LoopSourceIssues {
	refs := make([]string, 0, len(issues))
	for _, issue := range issues {
		refs = append(refs, "product_source_issue:"+issue.ID)
	}
	sort.Strings(refs)
	return LoopSourceIssues{Count: len(refs), Refs: refs}
}

func loopFreshness(signals []Signal, reviews []LoopReview, now time.Time, staleAfter time.Duration) LoopFreshness {
	var latestSignalAt string
	for _, signal := range signals {
		latestSignalAt = maxRFC3339(latestSignalAt, signal.ObservedAt)
	}
	var latestReviewAt string
	for _, review := range reviews {
		latestReviewAt = maxRFC3339(latestReviewAt, review.ReviewedAt)
	}
	freshness := LoopFreshness{LatestSignalAt: latestSignalAt, LatestReviewAt: latestReviewAt}
	latest, err := time.Parse(time.RFC3339, latestSignalAt)
	if latestSignalAt == "" || err != nil {
		freshness.Stale = true
		return freshness
	}
	freshness.Stale = now.UTC().Sub(latest) > staleAfter
	return freshness
}

func loopGaps(hypotheses []HypothesisCandidate, signals []Signal, recentSignals []Signal, doctor DoctorReport) []string {
	var gaps []string
	if hasSignalWithoutHypothesis(hypotheses, recentSignals) {
		gaps = append(gaps, "signals_without_hypothesis")
	}
	if hasHypothesisWithoutSignal(hypotheses, signals) {
		gaps = append(gaps, "hypothesis_without_signal")
	}
	if doctor.ReviewReadyCount > 0 {
		gaps = append(gaps, "review_ready")
	}
	return gaps
}

func hasSignalWithoutHypothesis(hypotheses []HypothesisCandidate, signals []Signal) bool {
	for _, signal := range signals {
		if strings.TrimSpace(signal.ChangeEvent) == "" {
			continue
		}
		matched := false
		for _, h := range hypotheses {
			if signalHypothesisMatchReason(h, signal) != "" {
				matched = true
				break
			}
		}
		if !matched {
			return true
		}
	}
	return false
}

func hasHypothesisWithoutSignal(hypotheses []HypothesisCandidate, signals []Signal) bool {
	for _, h := range hypotheses {
		if !isOpenLoopHypothesis(h) {
			continue
		}
		matched := false
		for _, signal := range signals {
			if signalHypothesisMatchReason(h, signal) != "" {
				matched = true
				break
			}
		}
		if !matched {
			return true
		}
	}
	return false
}

func compareRFC3339(a, b string) int {
	at, aerr := time.Parse(time.RFC3339, a)
	bt, berr := time.Parse(time.RFC3339, b)
	switch {
	case aerr == nil && berr == nil:
		if at.Before(bt) {
			return -1
		}
		if at.After(bt) {
			return 1
		}
		return 0
	case a == b:
		return 0
	case a == "":
		return -1
	case b == "":
		return 1
	case a < b:
		return -1
	default:
		return 1
	}
}

func maxRFC3339(a, b string) string {
	if compareRFC3339(a, b) >= 0 {
		return a
	}
	return b
}

func DecodeLoopModel(data []byte) (LoopModel, error) {
	var model LoopModel
	if err := json.Unmarshal(data, &model); err != nil {
		return LoopModel{}, err
	}
	return model, model.Validate()
}
