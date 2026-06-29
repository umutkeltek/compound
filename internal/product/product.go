// Package product owns the first product-feedback loop: product registry facts,
// market signals, and hypothesis candidates. Product pages are market-control
// surfaces, not repo summaries; signals are evidence, not truth.
package product

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/candidates"
	"compound/internal/mem"

	"gopkg.in/yaml.v3"
)

const (
	SchemaVersion              = "compound.product_registry.v0.1"
	ProductHypothesisCandidate = "compound.product_hypothesis_candidate.v0.1"
)

type Bundle struct {
	SchemaVersion string       `json:"schema_version" yaml:"schema_version"`
	Product       Product      `json:"product" yaml:"product"`
	Signals       []Signal     `json:"signals,omitempty" yaml:"signals,omitempty"`
	Hypotheses    []Hypothesis `json:"hypotheses,omitempty" yaml:"hypotheses,omitempty"`
	EvidenceRefs  []string     `json:"evidence_refs" yaml:"evidence_refs"`
}

type Product struct {
	ID             string       `json:"id" yaml:"id"`
	Name           string       `json:"name" yaml:"name"`
	OwnerEntity    string       `json:"owner_entity" yaml:"owner_entity"`
	Surfaces       []Surface    `json:"surfaces" yaml:"surfaces"`
	Channels       []string     `json:"channels" yaml:"channels"`
	Positioning    string       `json:"positioning" yaml:"positioning"`
	PrivacyPosture string       `json:"privacy_posture" yaml:"privacy_posture"`
	TasteProfile   TasteProfile `json:"taste_profile,omitempty" yaml:"taste_profile,omitempty"`
}

type Surface struct {
	ID   string `json:"id" yaml:"id"`
	Kind string `json:"kind" yaml:"kind"`
	Ref  string `json:"ref" yaml:"ref"`
}

type TasteProfile struct {
	Standards []string `json:"standards,omitempty" yaml:"standards,omitempty"`
	RedLines  []string `json:"red_lines,omitempty" yaml:"red_lines,omitempty"`
}

type Signal struct {
	ID              string           `json:"id" yaml:"id"`
	ProductID       string           `json:"product_id" yaml:"product_id"`
	Surface         string           `json:"surface" yaml:"surface"`
	ChangeEvent     string           `json:"change_event" yaml:"change_event"`
	MetricName      string           `json:"metric_name" yaml:"metric_name"`
	Before          float64          `json:"before" yaml:"before"`
	After           float64          `json:"after" yaml:"after"`
	Window          string           `json:"window" yaml:"window"`
	Confidence      float64          `json:"confidence" yaml:"confidence"`
	Confounders     []string         `json:"confounders,omitempty" yaml:"confounders,omitempty"`
	Interpretations []Interpretation `json:"interpretations,omitempty" yaml:"interpretations,omitempty"`
	NextAction      string           `json:"next_action,omitempty" yaml:"next_action,omitempty"`
	EvidenceRefs    []string         `json:"evidence_refs" yaml:"evidence_refs"`
	ObservedAt      string           `json:"observed_at,omitempty" yaml:"observed_at,omitempty"`
}

type Interpretation struct {
	Lens  string `json:"lens" yaml:"lens"`
	Claim string `json:"claim" yaml:"claim"`
}

type Hypothesis struct {
	ID                string   `json:"id" yaml:"id"`
	ProductID         string   `json:"product_id" yaml:"product_id"`
	ChangeEvent       string   `json:"change_event" yaml:"change_event"`
	ExpectedEffect    string   `json:"expected_effect" yaml:"expected_effect"`
	Metrics           []string `json:"metrics" yaml:"metrics"`
	ObservationWindow string   `json:"observation_window" yaml:"observation_window"`
	Result            string   `json:"result" yaml:"result"`
	NextAction        string   `json:"next_action" yaml:"next_action"`
	EvidenceRefs      []string `json:"evidence_refs" yaml:"evidence_refs"`
}

type HypothesisCandidate struct {
	SchemaVersion     string   `json:"schema_version"`
	ID                string   `json:"id"`
	Kind              string   `json:"kind"`
	ProductID         string   `json:"product_id"`
	ChangeEvent       string   `json:"change_event"`
	ExpectedEffect    string   `json:"expected_effect"`
	Metrics           []string `json:"metrics"`
	ObservationWindow string   `json:"observation_window"`
	Result            string   `json:"result"`
	NextAction        string   `json:"next_action"`
	Verdict           string   `json:"verdict"`
	EvidenceRefs      []string `json:"evidence_refs"`
	CreatedAt         string   `json:"created_at"`
}

type Report struct {
	ProductID     string   `json:"product_id"`
	Status        string   `json:"status"`
	EntityID      string   `json:"entity_id"`
	EvidenceIDs   []string `json:"evidence_ids"`
	ClaimIDs      []string `json:"claim_ids"`
	SignalIDs     []string `json:"signal_ids"`
	HypothesisIDs []string `json:"hypothesis_ids"`
}

type Timeline struct {
	ProductID  string
	Signals    []Signal
	Hypotheses []HypothesisCandidate
}

func Load(path string) (Bundle, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Bundle{}, err
	}
	var bundle Bundle
	if err := yaml.Unmarshal(b, &bundle); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

func Apply(homePath string, store *mem.Store, bundle Bundle, sourcePath string, now time.Time) (Report, error) {
	if err := Validate(bundle); err != nil {
		return Report{ProductID: bundle.Product.ID, Status: "fail"}, err
	}
	report := Report{ProductID: bundle.Product.ID, Status: "ok", EntityID: bundle.Product.ID}
	evidence, err := store.RecordEvidence(mem.EvidenceItem{
		SourceKind: "repo",
		SourceID:   "product-registry:" + filepath.Base(sourcePath),
		QuoteText:  bundle.Product.Name + " product registry: " + bundle.Product.Positioning,
		Actor:      "tool",
	}, now)
	if err != nil {
		return report, err
	}
	report.EvidenceIDs = append(report.EvidenceIDs, evidence.ID)

	entity := mem.Entity{
		Slug:        bundle.Product.ID,
		Kind:        "product",
		Aliases:     compact([]string{bundle.Product.Name}),
		Identifiers: productIdentifiers(bundle.Product),
		Parent:      bundle.Product.OwnerEntity,
		Visibility:  "operational",
	}
	if _, err := store.RegisterEntity(entity, now); err != nil {
		return report, err
	}

	captured, err := store.Capture(productClaims(bundle, evidence.ID, sourcePath), now)
	if err != nil {
		return report, err
	}
	for _, c := range captured {
		res, err := store.Adjudicate(c.ID, candidates.Promote, "product-import", now)
		if err != nil {
			return report, err
		}
		if res.Claim != nil {
			report.ClaimIDs = append(report.ClaimIDs, res.Claim.ID)
		}
	}

	for _, signal := range bundle.Signals {
		if signal.ObservedAt == "" {
			signal.ObservedAt = now.UTC().Format(time.RFC3339)
		}
		if err := appendUniqueSignal(homePath, signal); err != nil {
			return report, err
		}
		report.SignalIDs = append(report.SignalIDs, signal.ID)
	}
	for _, hyp := range bundle.Hypotheses {
		c := hypothesisCandidate(hyp, now)
		if err := writeHypothesisCandidate(homePath, c); err != nil {
			return report, err
		}
		report.HypothesisIDs = append(report.HypothesisIDs, c.ID)
	}
	return report, nil
}

func Validate(bundle Bundle) error {
	if bundle.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version must be %s, got %q", SchemaVersion, bundle.SchemaVersion)
	}
	p := bundle.Product
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("product id and name required")
	}
	if strings.TrimSpace(p.Positioning) == "" || strings.TrimSpace(p.PrivacyPosture) == "" {
		return fmt.Errorf("product positioning and privacy_posture required")
	}
	if len(p.Surfaces) == 0 || len(p.Channels) == 0 {
		return fmt.Errorf("product surfaces and channels required")
	}
	if len(bundle.EvidenceRefs) == 0 {
		return fmt.Errorf("evidence_refs required")
	}
	for _, s := range bundle.Signals {
		if s.ProductID != p.ID || strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.MetricName) == "" || len(s.EvidenceRefs) == 0 {
			return fmt.Errorf("invalid signal %q", s.ID)
		}
	}
	for _, h := range bundle.Hypotheses {
		if h.ProductID != p.ID || strings.TrimSpace(h.ID) == "" || strings.TrimSpace(h.ExpectedEffect) == "" || len(h.EvidenceRefs) == 0 {
			return fmt.Errorf("invalid hypothesis %q", h.ID)
		}
		if h.Result != "" && !oneOf(h.Result, []string{"unknown", "supported", "refuted", "inconclusive"}) {
			return fmt.Errorf("hypothesis %q bad result %q", h.ID, h.Result)
		}
	}
	return nil
}

func LoadTimeline(homePath, productID string) (Timeline, error) {
	signals, err := loadSignals(homePath)
	if err != nil {
		return Timeline{}, err
	}
	hyps, err := loadHypotheses(homePath)
	if err != nil {
		return Timeline{}, err
	}
	tl := Timeline{ProductID: productID}
	for _, s := range signals {
		if s.ProductID == productID {
			tl.Signals = append(tl.Signals, s)
		}
	}
	for _, h := range hyps {
		if h.ProductID == productID {
			tl.Hypotheses = append(tl.Hypotheses, h)
		}
	}
	sort.Slice(tl.Signals, func(i, j int) bool { return tl.Signals[i].ID < tl.Signals[j].ID })
	sort.Slice(tl.Hypotheses, func(i, j int) bool { return tl.Hypotheses[i].ID < tl.Hypotheses[j].ID })
	return tl, nil
}

func productClaims(bundle Bundle, evidenceID, sourcePath string) []mem.Candidate {
	src := "product-registry:" + filepath.Base(sourcePath)
	p := bundle.Product
	add := func(pred, obj string) mem.Candidate {
		return mem.Candidate{
			Entity:         p.ID,
			Predicate:      pred,
			Object:         obj,
			Confidence:     1.0,
			Derivation:     "deterministic",
			ClaimType:      "status",
			Qualifiers:     map[string]string{"domain": "product"},
			PromotionBasis: candidates.BasisTrustedDeterministic,
			SourceTraceRef: evidenceID + ":" + src,
			SnippetHash:    stableID("prod_snip_", p.ID, pred, obj),
			Visibility:     "operational",
		}
	}
	return []mem.Candidate{
		add("positioning", p.Positioning),
		add("privacy_posture", p.PrivacyPosture),
		add("channels", strings.Join(p.Channels, ",")),
		add("surfaces", strings.Join(surfaceRefs(p.Surfaces), ",")),
	}
}

func productIdentifiers(p Product) []string {
	var ids []string
	ids = append(ids, p.ID)
	for _, s := range p.Surfaces {
		if s.Ref != "" {
			ids = append(ids, s.Ref)
		}
	}
	return compact(ids)
}

func surfaceRefs(surfaces []Surface) []string {
	var refs []string
	for _, s := range surfaces {
		if s.Ref != "" {
			refs = append(refs, s.Ref)
		}
	}
	return compact(refs)
}

func hypothesisCandidate(h Hypothesis, now time.Time) HypothesisCandidate {
	result := h.Result
	if result == "" {
		result = "unknown"
	}
	return HypothesisCandidate{
		SchemaVersion:     ProductHypothesisCandidate,
		ID:                h.ID,
		Kind:              string(candidates.ProductHypothesis),
		ProductID:         h.ProductID,
		ChangeEvent:       h.ChangeEvent,
		ExpectedEffect:    h.ExpectedEffect,
		Metrics:           h.Metrics,
		ObservationWindow: h.ObservationWindow,
		Result:            result,
		NextAction:        h.NextAction,
		Verdict:           candidates.Park,
		EvidenceRefs:      h.EvidenceRefs,
		CreatedAt:         now.UTC().Format(time.RFC3339),
	}
}

func appendUniqueSignal(homePath string, signal Signal) error {
	path := signalsLog(homePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	signals, err := loadSignals(homePath)
	if err != nil {
		return err
	}
	for _, existing := range signals {
		if existing.ID == signal.ID {
			return nil
		}
	}
	return appendJSONL(path, &signal)
}

func writeHypothesisCandidate(homePath string, c HypothesisCandidate) error {
	dir := filepath.Join(homePath, candidates.InboxDir(candidates.ProductHypothesis))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, c.ID+".json"), append(b, '\n'), 0o644)
}

func loadSignals(homePath string) ([]Signal, error) {
	var out []Signal
	err := readJSONL(signalsLog(homePath), func(line []byte) error {
		var s Signal
		if err := json.Unmarshal(line, &s); err != nil || s.ID == "" {
			return nil
		}
		out = append(out, s)
		return nil
	})
	return out, err
}

func loadHypotheses(homePath string) ([]HypothesisCandidate, error) {
	dir := filepath.Join(homePath, candidates.InboxDir(candidates.ProductHypothesis))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []HypothesisCandidate
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var h HypothesisCandidate
		if json.Unmarshal(b, &h) == nil && h.ID != "" {
			out = append(out, h)
		}
	}
	return out, nil
}

func signalsLog(homePath string) string {
	return filepath.Join(homePath, "ledger", "product-signals.jsonl")
}

func appendJSONL(path string, v any) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func readJSONL(path string, fn func([]byte) error) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if err := fn([]byte(line)); err != nil {
			return err
		}
	}
	return nil
}

func stableID(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + hex.EncodeToString(sum[:])[:12]
}

func compact(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func oneOf(v string, allowed []string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}
