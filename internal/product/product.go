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
	"compound/internal/envelope"
	"compound/internal/mem"
	"compound/internal/writeback"

	"gopkg.in/yaml.v3"
)

const (
	SchemaVersion              = "compound.product_registry.v0.1"
	SignalSchemaVersion        = "compound.product_signal.v0.1"
	ProductHypothesisCandidate = "compound.product_hypothesis_candidate.v0.1"
	ReviewSchemaVersion        = "compound.product_review.v0.1"
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

type SignalDocument struct {
	SchemaVersion string       `json:"schema_version" yaml:"schema_version"`
	Source        SignalSource `json:"source" yaml:"source"`
	Signal        Signal       `json:"signal" yaml:"signal"`
	EvidenceRefs  []string     `json:"evidence_refs,omitempty" yaml:"evidence_refs,omitempty"`
}

type SignalSource struct {
	Kind string `json:"kind" yaml:"kind"`
	Ref  string `json:"ref" yaml:"ref"`
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

type Review struct {
	SchemaVersion string   `json:"schema_version" yaml:"schema_version"`
	ID            string   `json:"id" yaml:"id"`
	ProductID     string   `json:"product_id" yaml:"product_id"`
	HypothesisID  string   `json:"hypothesis_id" yaml:"hypothesis_id"`
	Result        string   `json:"result" yaml:"result"`
	Rationale     string   `json:"rationale" yaml:"rationale"`
	Confidence    float64  `json:"confidence" yaml:"confidence"`
	NextAction    string   `json:"next_action,omitempty" yaml:"next_action,omitempty"`
	EvidenceRefs  []string `json:"evidence_refs" yaml:"evidence_refs"`
	ReviewedAt    string   `json:"reviewed_at,omitempty" yaml:"reviewed_at,omitempty"`
	AppliedAt     string   `json:"applied_at,omitempty" yaml:"applied_at,omitempty"`
	EvidenceID    string   `json:"evidence_id,omitempty" yaml:"evidence_id,omitempty"`
	SourceRef     string   `json:"source_ref,omitempty" yaml:"source_ref,omitempty"`
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
	TransitionIDs []string `json:"transition_ids,omitempty"`
}

type ReviewReport struct {
	Status       string   `json:"status"`
	ProductID    string   `json:"product_id"`
	HypothesisID string   `json:"hypothesis_id"`
	ReviewID     string   `json:"review_id"`
	Result       string   `json:"result"`
	EvidenceID   string   `json:"evidence_id"`
	EvidenceRefs []string `json:"evidence_refs"`
	TransitionID string   `json:"transition_id,omitempty"`
}

type Timeline struct {
	ProductID  string
	Signals    []Signal
	Hypotheses []HypothesisCandidate
}

type DoctorReport struct {
	ProductID           string                `json:"product_id"`
	Status              string                `json:"status"`
	SignalCount         int                   `json:"signal_count"`
	HypothesisCount     int                   `json:"hypothesis_count"`
	PendingReviewCount  int                   `json:"pending_review_count"`
	PendingReview       []HypothesisCandidate `json:"pending_review,omitempty"`
	MissingEvidenceRefs []string              `json:"missing_evidence_refs,omitempty"`
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

func LoadSignal(path string) (SignalDocument, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return SignalDocument{}, err
	}
	var doc SignalDocument
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return SignalDocument{}, err
	}
	return doc, nil
}

func LoadReview(path string) (Review, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Review{}, err
	}
	var review Review
	if err := yaml.Unmarshal(b, &review); err != nil {
		return Review{}, err
	}
	return review, nil
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

func ApplySignal(homePath string, store *mem.Store, doc SignalDocument, sourcePath string, now time.Time) (Report, error) {
	if err := ValidateSignalDocument(doc); err != nil {
		return Report{ProductID: doc.Signal.ProductID, Status: "fail"}, err
	}
	signal := doc.Signal
	if signal.ObservedAt == "" {
		signal.ObservedAt = now.UTC().Format(time.RFC3339)
	}
	evidence, err := store.RecordEvidence(mem.EvidenceItem{
		SourceKind: "repo",
		SourceID:   "product-signal:" + filepath.Base(sourcePath) + ":" + doc.Source.Kind + ":" + doc.Source.Ref,
		QuoteText:  fmt.Sprintf("%s %s %.4g -> %.4g (%s)", signal.ProductID, signal.MetricName, signal.Before, signal.After, signal.Window),
		Actor:      "tool",
	}, now)
	if err != nil {
		return Report{}, err
	}
	signal.EvidenceRefs = compact(append(append(signal.EvidenceRefs, evidence.ID), doc.EvidenceRefs...))
	if err := appendUniqueSignal(homePath, signal); err != nil {
		return Report{}, err
	}
	tr, err := BuildLoopTransition(LoopTransitionInput{
		Kind:         "product_signal",
		ProductID:    signal.ProductID,
		ObjectID:     signal.ID,
		SourceTrace:  "product_signal:" + signal.ID,
		BeforeRefs:   doc.EvidenceRefs,
		AfterRefs:    append([]string{"product_signal:" + signal.ID, evidence.ID}, signal.EvidenceRefs...),
		EvidenceRefs: signal.EvidenceRefs,
		Summary:      fmt.Sprintf("product signal %s recorded; no product surface changed", signal.ID),
	}, now)
	if err != nil {
		return Report{}, err
	}
	if err := RecordTransition(homePath, tr); err != nil {
		return Report{}, err
	}
	return Report{
		ProductID:     signal.ProductID,
		Status:        "ok",
		EvidenceIDs:   []string{evidence.ID},
		SignalIDs:     []string{signal.ID},
		TransitionIDs: []string{tr.ID},
	}, nil
}

func ApplyReview(homePath string, store *mem.Store, review Review, sourcePath string, now time.Time) (ReviewReport, error) {
	if err := ValidateReview(review); err != nil {
		return ReviewReport{ProductID: review.ProductID, HypothesisID: review.HypothesisID, ReviewID: review.ID, Status: "fail"}, err
	}
	candidate, err := loadHypothesisCandidate(homePath, review.HypothesisID)
	if err != nil {
		return ReviewReport{}, err
	}
	if candidate.ProductID != review.ProductID {
		return ReviewReport{}, fmt.Errorf("review product_id %q does not match hypothesis product_id %q", review.ProductID, candidate.ProductID)
	}
	if review.ReviewedAt == "" {
		review.ReviewedAt = now.UTC().Format(time.RFC3339)
	}
	review.AppliedAt = now.UTC().Format(time.RFC3339)
	review.SourceRef = "product-review:" + filepath.Base(sourcePath)
	evidence, err := store.RecordEvidence(mem.EvidenceItem{
		SourceKind: "repo",
		SourceID:   review.SourceRef,
		QuoteText:  review.Rationale,
		Actor:      "tool",
	}, now)
	if err != nil {
		return ReviewReport{}, err
	}
	review.EvidenceID = evidence.ID
	if err := appendUniqueReview(homePath, review); err != nil {
		return ReviewReport{}, err
	}
	candidate.Result = review.Result
	candidate.NextAction = review.NextAction
	candidate.EvidenceRefs = compact(append(append(candidate.EvidenceRefs, evidence.ID, "product_review:"+review.ID), review.EvidenceRefs...))
	if err := writeHypothesisCandidate(homePath, candidate); err != nil {
		return ReviewReport{}, err
	}
	tr, err := BuildLoopTransition(LoopTransitionInput{
		Kind:         "product_review",
		ProductID:    review.ProductID,
		ObjectID:     review.ID,
		SourceTrace:  "product_review:" + review.ID,
		BeforeRefs:   compact(append([]string{"product_hypothesis:" + candidate.ID}, review.EvidenceRefs...)),
		AfterRefs:    compact(append([]string{"product_hypothesis:" + candidate.ID, "product_review:" + review.ID, evidence.ID}, candidate.EvidenceRefs...)),
		EvidenceRefs: candidate.EvidenceRefs,
		Summary:      fmt.Sprintf("product review %s set hypothesis %s to %s; no product surface changed", review.ID, candidate.ID, review.Result),
	}, now)
	if err != nil {
		return ReviewReport{}, err
	}
	if err := RecordTransition(homePath, tr); err != nil {
		return ReviewReport{}, err
	}
	return ReviewReport{
		Status:       "updated",
		ProductID:    review.ProductID,
		HypothesisID: review.HypothesisID,
		ReviewID:     review.ID,
		Result:       review.Result,
		EvidenceID:   evidence.ID,
		EvidenceRefs: candidate.EvidenceRefs,
		TransitionID: tr.ID,
	}, nil
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
		if err := validateSignal(s); err != nil || s.ProductID != p.ID {
			if err != nil {
				return err
			}
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

func validateSignal(s Signal) error {
	if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.ProductID) == "" || strings.TrimSpace(s.MetricName) == "" {
		return fmt.Errorf("invalid signal %q", s.ID)
	}
	if len(s.EvidenceRefs) == 0 {
		return fmt.Errorf("signal %q evidence_refs required", s.ID)
	}
	if s.Confidence < 0 || s.Confidence > 1 {
		return fmt.Errorf("signal %q confidence must be between 0 and 1", s.ID)
	}
	return nil
}

func ValidateSignalDocument(doc SignalDocument) error {
	if doc.SchemaVersion != SignalSchemaVersion {
		return fmt.Errorf("schema_version must be %s, got %q", SignalSchemaVersion, doc.SchemaVersion)
	}
	if strings.TrimSpace(doc.Source.Kind) == "" || strings.TrimSpace(doc.Source.Ref) == "" {
		return fmt.Errorf("signal source kind ref required")
	}
	if err := validateSignal(doc.Signal); err != nil {
		return err
	}
	return nil
}

func ValidateReview(review Review) error {
	if review.SchemaVersion != ReviewSchemaVersion {
		return fmt.Errorf("schema_version must be %s, got %q", ReviewSchemaVersion, review.SchemaVersion)
	}
	if strings.TrimSpace(review.ID) == "" || strings.TrimSpace(review.ProductID) == "" || strings.TrimSpace(review.HypothesisID) == "" {
		return fmt.Errorf("review id product_id hypothesis_id required")
	}
	if strings.ContainsAny(review.HypothesisID, `/\`) {
		return fmt.Errorf("review hypothesis_id %q must not contain path separators", review.HypothesisID)
	}
	if !oneOf(review.Result, []string{"supported", "refuted", "inconclusive"}) {
		return fmt.Errorf("review result must be supported|refuted|inconclusive, got %q", review.Result)
	}
	if strings.TrimSpace(review.Rationale) == "" {
		return fmt.Errorf("review rationale required")
	}
	if review.Confidence < 0 || review.Confidence > 1 {
		return fmt.Errorf("review confidence must be between 0 and 1")
	}
	if len(review.EvidenceRefs) == 0 {
		return fmt.Errorf("review evidence_refs required")
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

func Doctor(homePath, productID string) (DoctorReport, error) {
	tl, err := LoadTimeline(homePath, productID)
	if err != nil {
		return DoctorReport{}, err
	}
	report := DoctorReport{
		ProductID:       productID,
		Status:          "ok",
		SignalCount:     len(tl.Signals),
		HypothesisCount: len(tl.Hypotheses),
	}
	for _, h := range tl.Hypotheses {
		if len(h.EvidenceRefs) == 0 {
			report.MissingEvidenceRefs = append(report.MissingEvidenceRefs, h.ID)
			report.Status = "needs_review"
		}
		if h.Verdict == candidates.Park && h.Result == "unknown" && strings.TrimSpace(h.NextAction) != "" {
			report.PendingReview = append(report.PendingReview, h)
		}
	}
	report.PendingReviewCount = len(report.PendingReview)
	if report.PendingReviewCount > 0 {
		report.Status = "needs_review"
	}
	return report, nil
}

func NeedsWriteback(report DoctorReport) bool {
	return report.PendingReviewCount > 0 || len(report.MissingEvidenceRefs) > 0
}

func EvidenceRefs(report DoctorReport) []string {
	refs := []string{
		"product_id:" + report.ProductID,
		fmt.Sprintf("product:signals:%d", report.SignalCount),
		fmt.Sprintf("product:hypotheses:%d", report.HypothesisCount),
		fmt.Sprintf("product:pending_review:%d", report.PendingReviewCount),
	}
	for _, h := range report.PendingReview {
		refs = append(refs, "product_hypothesis:"+h.ID+":"+h.Result)
		refs = append(refs, h.EvidenceRefs...)
	}
	for _, id := range report.MissingEvidenceRefs {
		refs = append(refs, "product_hypothesis_missing_evidence:"+id)
	}
	return compact(refs)
}

func WritebackGate(report DoctorReport, actionIntentRef, policyDecisionRef string, now time.Time) writeback.OwnerWritebackGate {
	return writeback.OwnerWritebackGate{
		SchemaVersion:     writeback.GateSchemaVersion,
		CreatedAt:         now.UTC().Format(time.RFC3339),
		CandidateRef:      fmt.Sprintf("product:hypothesis_review:%s:%s", report.ProductID, productDoctorDigest(report)),
		CandidateKind:     string(candidates.ProductHypothesis),
		OwnerTarget:       writeback.OwnerTarget{Kind: "product_strategy", Ref: report.ProductID},
		GateBasis:         fmt.Sprintf("product doctor found %d pending hypothesis review(s) and %d missing-evidence hypothesis record(s)", report.PendingReviewCount, len(report.MissingEvidenceRefs)),
		ActionIntentRef:   actionIntentRef,
		MutationClass:     "local_reversible",
		PolicyDecisionRef: policyDecisionRef,
		EvidenceRefs:      EvidenceRefs(report),
	}
}

type LoopTransitionInput struct {
	Kind         string
	ProductID    string
	ObjectID     string
	SourceTrace  string
	BeforeRefs   []string
	AfterRefs    []string
	EvidenceRefs []string
	Summary      string
}

func BuildLoopTransition(in LoopTransitionInput, now time.Time) (envelope.Transition, error) {
	if strings.TrimSpace(in.Kind) == "" || strings.TrimSpace(in.ProductID) == "" || strings.TrimSpace(in.ObjectID) == "" {
		return envelope.Transition{}, fmt.Errorf("product loop transition kind product_id object_id required")
	}
	if strings.TrimSpace(in.SourceTrace) == "" {
		in.SourceTrace = in.Kind + ":" + in.ObjectID
	}
	evidenceRefs := compact(in.EvidenceRefs)
	afterRefs := compact(in.AfterRefs)
	if len(evidenceRefs) == 0 {
		evidenceRefs = afterRefs
	}
	if in.Summary == "" {
		in.Summary = fmt.Sprintf("%s %s recorded for %s", in.Kind, in.ObjectID, in.ProductID)
	}
	tr := envelope.Transition{
		Header: envelope.Header{
			SchemaVersion: envelope.SVTransition,
			ID:            stableID("tr_", in.Kind, in.ProductID, in.ObjectID),
			CreatedAt:     now.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
			IDs:           envelope.IDs{WorkID: stableID("work_product_", in.Kind, in.ProductID, in.ObjectID)},
			Scope:         envelope.Scope{ProductID: in.ProductID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  evidenceRefs,
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		SourceTraceID: in.SourceTrace,
		BeforeRefs:    compact(in.BeforeRefs),
		ActionRefs:    []string{in.SourceTrace},
		AfterRefs:     afterRefs,
		StateDelta:    in.Summary,
		Projections: envelope.Projections{
			Product:        compact(append([]string{in.ProductID, in.Kind + ":" + in.ObjectID}, afterRefs...)),
			HumanAttention: []string{},
		},
		SecondOrder: envelope.SecondOrder{FutureBehaviorChanged: true},
	}
	if err := tr.Validate(); err != nil {
		return envelope.Transition{}, err
	}
	return tr, nil
}

func RecordTransition(homePath string, tr envelope.Transition) error {
	if err := tr.Validate(); err != nil {
		return err
	}
	path := filepath.Join(homePath, "events", tr.ID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(&tr, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
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
			if !sameSignal(existing, signal) {
				return fmt.Errorf("product signal %q already exists with different content", signal.ID)
			}
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

func loadHypothesisCandidate(homePath, id string) (HypothesisCandidate, error) {
	path := filepath.Join(homePath, candidates.InboxDir(candidates.ProductHypothesis), id+".json")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return HypothesisCandidate{}, fmt.Errorf("product hypothesis %q not found", id)
	}
	if err != nil {
		return HypothesisCandidate{}, err
	}
	var h HypothesisCandidate
	if err := json.Unmarshal(b, &h); err != nil {
		return HypothesisCandidate{}, fmt.Errorf("parse product hypothesis %q: %w", id, err)
	}
	if h.ID == "" {
		return HypothesisCandidate{}, fmt.Errorf("product hypothesis %q is missing id", id)
	}
	return h, nil
}

func appendUniqueReview(homePath string, review Review) error {
	path := reviewsLog(homePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	reviews, err := loadReviews(homePath)
	if err != nil {
		return err
	}
	for _, existing := range reviews {
		if existing.ID == review.ID {
			if !sameReview(existing, review) {
				return fmt.Errorf("product review %q already exists with different content", review.ID)
			}
			return nil
		}
	}
	return appendJSONL(path, &review)
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

func loadReviews(homePath string) ([]Review, error) {
	var out []Review
	err := readJSONL(reviewsLog(homePath), func(line []byte) error {
		var r Review
		if err := json.Unmarshal(line, &r); err != nil || r.ID == "" {
			return nil
		}
		out = append(out, r)
		return nil
	})
	return out, err
}

func signalsLog(homePath string) string {
	return filepath.Join(homePath, "ledger", "product-signals.jsonl")
}

func reviewsLog(homePath string) string {
	return filepath.Join(homePath, "ledger", "product-reviews.jsonl")
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

func productDoctorDigest(report DoctorReport) string {
	refs := EvidenceRefs(report)
	sort.Strings(refs)
	return stableID("", refs...)
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

func sameReview(a, b Review) bool {
	return a.ProductID == b.ProductID &&
		a.HypothesisID == b.HypothesisID &&
		a.Result == b.Result &&
		a.Rationale == b.Rationale &&
		a.Confidence == b.Confidence &&
		a.NextAction == b.NextAction &&
		strings.Join(a.EvidenceRefs, "\x00") == strings.Join(b.EvidenceRefs, "\x00")
}

func sameSignal(a, b Signal) bool {
	a.ObservedAt = ""
	b.ObservedAt = ""
	return a.ID == b.ID &&
		a.ProductID == b.ProductID &&
		a.Surface == b.Surface &&
		a.ChangeEvent == b.ChangeEvent &&
		a.MetricName == b.MetricName &&
		a.Before == b.Before &&
		a.After == b.After &&
		a.Window == b.Window &&
		a.Confidence == b.Confidence &&
		a.NextAction == b.NextAction &&
		strings.Join(a.Confounders, "\x00") == strings.Join(b.Confounders, "\x00") &&
		strings.Join(a.EvidenceRefs, "\x00") == strings.Join(b.EvidenceRefs, "\x00") &&
		sameInterpretations(a.Interpretations, b.Interpretations)
}

func sameInterpretations(a, b []Interpretation) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func oneOf(v string, allowed []string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}
