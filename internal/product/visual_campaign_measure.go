package product

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"
)

type CampaignArm struct {
	Exposed   int64 `json:"exposed"`
	Converted int64 `json:"converted"`
}

// One report is one locale/target stratum. Deliberately do not pool locales,
// acquisition channels or unrelated platform experiments into a fake A/B test.
type CampaignObservation struct {
	ProductID        string       `json:"product_id"`
	ExperimentID     string       `json:"experiment_id"`
	CampaignSHA256   string       `json:"campaign_sha256"`
	ControlSetSHA256 string       `json:"control_set_sha256"`
	Locale           string       `json:"locale"`
	Target           string       `json:"target"`
	Design           string       `json:"design"` // randomized or observational
	AssignmentRef    string       `json:"assignment_ref"`
	RegisteredAt     string       `json:"registered_at"`
	StartsAt         string       `json:"starts_at"`
	EndsAt           string       `json:"ends_at"`
	ObservedAt       string       `json:"observed_at"`
	Source           CampaignFile `json:"source"`
	Unit             string       `json:"unit"` // unique randomized users; one Bernoulli outcome each
	Control          CampaignArm  `json:"control"`
	Treatment        CampaignArm  `json:"treatment"`
	Confounders      []string     `json:"confounders"`
}

type CampaignMeasurement struct {
	Decision      string         `json:"decision"`
	Reason        string         `json:"reason"`
	ControlRate   float64        `json:"control_rate"`
	TreatmentRate float64        `json:"treatment_rate"`
	AbsoluteLift  float64        `json:"absolute_lift"`
	Interval95    [2]float64     `json:"interval_95"`
	Signal        SignalDocument `json:"signal"`
}

// Platform adapters normalize their export into this evidence document before
// measurement. Counts and assignment identity must agree with the observed row;
// callers cannot cite arbitrary bytes while supplying different counts.
type CampaignExposureExport struct {
	SchemaVersion string              `json:"schema_version"`
	Observation   CampaignObservation `json:"observation"` // source must be empty here
	EvidenceRefs  []string            `json:"evidence_refs"`
}

func LoadCampaignMeasurement(campaignPath, observationPath string, now time.Time) (CampaignMeasurement, error) {
	c, err := LoadVisualCampaign(campaignPath)
	if err != nil {
		return CampaignMeasurement{}, err
	}
	var o CampaignObservation
	if err = loadCampaignJSON(observationPath, &o); err != nil {
		return CampaignMeasurement{}, err
	}
	return MeasureVisualCampaign(c, filepath.Dir(campaignPath), o, filepath.Dir(observationPath), now)
}

func MeasureVisualCampaign(c VisualCampaign, base string, o CampaignObservation, observationBase string, now time.Time) (CampaignMeasurement, error) {
	var out CampaignMeasurement
	check, err := CheckVisualCampaign(c, base, now)
	if err != nil {
		return out, err
	}
	if !check.Ready {
		return out, fmt.Errorf("campaign is not ready: %v", check.Issues)
	}
	e := c.Experiment
	if o.ProductID != c.ProductID || o.ExperimentID != e.ID || o.CampaignSHA256 != check.CampaignSHA256 || o.ControlSetSHA256 != e.ControlSetSHA256 {
		return out, fmt.Errorf("experiment/product/control/treatment identity mismatch")
	}
	lok, tok := false, false
	for _, l := range c.Locales {
		lok = lok || o.Locale == l.Tag
	}
	for _, t := range c.Targets {
		tok = tok || o.Target == t.ID
	}
	if !lok || !tok {
		return out, fmt.Errorf("observation locale/target is outside campaign")
	}
	if (o.Design != "randomized" && o.Design != "observational") || o.Unit != "unique_users" || !campaignNonempty(o.AssignmentRef) {
		return out, fmt.Errorf("design, assignment evidence and unique_users denominator required")
	}
	start, _ := time.Parse(time.RFC3339, e.StartsAt)
	end, _ := time.Parse(time.RFC3339, e.EndsAt)
	registered, rerr := time.Parse(time.RFC3339, o.RegisteredAt)
	observed, oerr := time.Parse(time.RFC3339, o.ObservedAt)
	created, _ := time.Parse(time.RFC3339, c.CreatedAt)
	if rerr != nil || oerr != nil || registered.After(start) || registered.Before(created) || o.StartsAt != e.StartsAt || o.EndsAt != e.EndsAt || observed.Before(end) || observed.After(now) || now.Before(end) {
		return out, fmt.Errorf("fixed experiment window incomplete or registration/window identity invalid; do not declare a result yet")
	}
	// Preserve the actual export bytes' identity; an evidence URL alone cannot
	// detect a report replaced in place. Intake remains evidence, not promotion.
	if !campaignHashPattern.MatchString(o.Source.SHA256) || o.Source.Path == "" {
		return out, fmt.Errorf("observation source file and hash required")
	}
	b, err := os.ReadFile(campaignFilePath(observationBase, o.Source))
	if err != nil {
		return out, err
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != o.Source.SHA256 || len(b) == 0 {
		return out, fmt.Errorf("observation source changed or empty")
	}
	var source CampaignExposureExport
	if err := json.Unmarshal(b, &source); err != nil {
		return out, fmt.Errorf("decode normalized exposure export: %w", err)
	}
	expected := o
	expected.Source = CampaignFile{}
	if source.SchemaVersion != "compound.campaign_exposure.v0.1" || !campaignEvidence(source.EvidenceRefs) || campaignDigest(source.Observation) != campaignDigest(expected) {
		return out, fmt.Errorf("exposure export does not match supplied counts, assignment or experiment identity")
	}
	for _, arm := range []CampaignArm{o.Control, o.Treatment} {
		if arm.Exposed <= 0 || arm.Exposed > 1000000000 || arm.Converted < 0 || arm.Converted > arm.Exposed {
			return out, fmt.Errorf("invalid exposure/conversion counts")
		}
	}
	out.ControlRate = float64(o.Control.Converted) / float64(o.Control.Exposed)
	out.TreatmentRate = float64(o.Treatment.Converted) / float64(o.Treatment.Exposed)
	out.AbsoluteLift = out.TreatmentRate - out.ControlRate
	cl, ch := campaignWilson(o.Control)
	tl, th := campaignWilson(o.Treatment)
	// Newcombe independent proportions interval, using Wilson score bounds.
	out.Interval95 = [2]float64{out.AbsoluteLift - math.Hypot(out.TreatmentRate-tl, ch-out.ControlRate), out.AbsoluteLift + math.Hypot(th-out.TreatmentRate, out.ControlRate-cl)}
	out.Decision, out.Reason = "inconclusive", "interval does not establish the predeclared practical improvement"
	confounders := append([]string{}, o.Confounders...)
	if o.Design != "randomized" {
		out.Decision, out.Reason = "observational", "comparison is observational; no causal win can be attributed to this creative package"
		confounders = append(confounders, "nonrandomized allocation")
	} else {
		total := float64(o.Control.Exposed + o.Treatment.Exposed)
		z := math.Abs(float64(o.Treatment.Exposed)-total*e.TreatmentShare) / math.Sqrt(total*e.TreatmentShare*(1-e.TreatmentShare))
		sparse := o.Control.Exposed < e.MinPerArm || o.Treatment.Exposed < e.MinPerArm || o.Control.Converted < 20 || o.Treatment.Converted < 20 || o.Control.Exposed-o.Control.Converted < 20 || o.Treatment.Exposed-o.Treatment.Converted < 20
		switch {
		case z > 3.290527:
			out.Reason = "sample allocation mismatch; inspect assignment/instrumentation before interpreting"
			confounders = append(confounders, "sample ratio mismatch")
		case sparse:
			out.Reason = "insufficient sample or outcomes; plan another fixed-window test without changing this test's stopping rule"
		case len(confounders) > 0:
			out.Reason = "unresolved confounders; investigate before attributing impact"
		case out.Interval95[0] > e.MinAbsoluteLift:
			out.Decision, out.Reason = "supported_improvement", "95% interval clears the predeclared absolute lift for this creative package and locale"
		case out.Interval95[1] < 0:
			out.Decision, out.Reason = "evidence_of_harm", "95% interval is below zero; consider restoring control through release authority"
		}
	}
	evidence := append([]string{}, c.Context.EvidenceRefs...)
	evidence = append(evidence, source.EvidenceRefs...)
	evidence = append(evidence, "campaign-sha256:"+check.CampaignSHA256, "observation-sha256:"+campaignDigest(o), "source-sha256:"+o.Source.SHA256, o.AssignmentRef, "hypothesis:"+e.HypothesisID)
	confidence := 0.0 // descriptive input confidence is not posterior P(winner)
	if out.Decision == "supported_improvement" || out.Decision == "evidence_of_harm" {
		confidence = 0.95
	}
	out.Signal = SignalDocument{SchemaVersion: SignalSchemaVersion, Source: SignalSource{Kind: "visual_campaign_experiment", Ref: e.ID}, EvidenceRefs: evidence, Signal: Signal{
		ID: stableID("sig_", c.ProductID, e.ID, o.Locale, o.Target), ProductID: c.ProductID, Surface: o.Target + ":" + o.Locale, ChangeEvent: "creative package experiment " + e.ID + " (hypothesis " + e.HypothesisID + ")", MetricName: e.Metric, Before: out.ControlRate, After: out.TreatmentRate, Window: e.StartsAt + "/" + e.EndsAt, Confidence: confidence, Confounders: confounders, ObservedAt: o.ObservedAt, EvidenceRefs: evidence,
		Interpretations: []Interpretation{{Lens: "experiment_design", Claim: "before=concurrent control rate; after=treatment rate; not a temporal before/after comparison"}, {Lens: "campaign_measurement", Claim: out.Decision + ": " + out.Reason}, {Lens: "attribution", Claim: "Effect applies to the full creative package in this locale/target; no individual scene, model or skill credit is established."}}, NextAction: "Review hypothesis " + e.HypothesisID + " against this signal; retain/rollback only through existing release authority. " + out.Reason,
	}}
	return out, ValidateSignalDocument(out.Signal)
}

func campaignWilson(a CampaignArm) (float64, float64) {
	n, p, z := float64(a.Exposed), float64(a.Converted)/float64(a.Exposed), 1.959963984540054
	d := 1 + z*z/n
	center := (p + z*z/(2*n)) / d
	half := z * math.Sqrt(p*(1-p)/n+z*z/(4*n*n)) / d
	return center - half, center + half
}
