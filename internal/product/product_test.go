package product

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/envelope"
	"compound/internal/mem"
	"compound/internal/writeback"
)

var tProduct = time.Date(2026, 6, 29, 14, 0, 0, 0, time.UTC)

func TestApplyImportsProductSignalAndHypothesisCandidate(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Load("../../fixtures/product/market-loop.yaml")
	if err != nil {
		t.Fatal(err)
	}

	report, err := Apply(home, store, bundle, "fixtures/product/market-loop.yaml", tProduct)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || report.EntityID != "product:focus-notes" || len(report.ClaimIDs) == 0 || len(report.SignalIDs) != 1 || len(report.HypothesisIDs) != 1 {
		t.Fatalf("report = %+v", report)
	}

	res, err := store.ResolveEntity("appstore:123456789")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "resolved" || res.Slug != "product:focus-notes" {
		t.Fatalf("product identifier should resolve, got %+v", res)
	}

	tl, err := LoadTimeline(home, "product:focus-notes")
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Signals) != 1 || tl.Signals[0].MetricName != "app_store_product_page_conversion" {
		t.Fatalf("signals = %+v", tl.Signals)
	}
	if len(tl.Hypotheses) != 1 || tl.Hypotheses[0].Verdict != candidates.Park {
		t.Fatalf("hypotheses = %+v", tl.Hypotheses)
	}

	if _, err := os.Stat(filepath.Join(home, candidates.InboxDir(candidates.ProductHypothesis), "hyp_focus_visuals_conversion.json")); err != nil {
		t.Fatal(err)
	}

	doctor, err := Doctor(home, "product:focus-notes")
	if err != nil {
		t.Fatal(err)
	}
	if doctor.Status != "needs_review" || doctor.SignalCount != 1 || doctor.HypothesisCount != 1 || doctor.PendingReviewCount != 1 {
		t.Fatalf("doctor = %+v", doctor)
	}
	if !NeedsWriteback(doctor) {
		t.Fatal("NeedsWriteback = false, want true")
	}
	gate := WritebackGate(doctor, "act_product_review", "pol_product_review", tProduct)
	if gate.CandidateKind != string(candidates.ProductHypothesis) || gate.OwnerTarget.Kind != "product_strategy" || gate.OwnerTarget.Ref != "product:focus-notes" {
		t.Fatalf("gate = %+v", gate)
	}
	checked, err := writeback.EvaluateGate(gate, nil, tProduct)
	if err != nil {
		t.Fatal(err)
	}
	if checked.Status != writeback.GateNoPolicy {
		t.Fatalf("gate status = %q, want %q", checked.Status, writeback.GateNoPolicy)
	}

	review, err := LoadReview("../../fixtures/product/review.yaml")
	if err != nil {
		t.Fatal(err)
	}
	reviewReport, err := ApplyReview(home, store, review, "fixtures/product/review.yaml", tProduct.Add(14*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if reviewReport.Status != "updated" || reviewReport.Result != "inconclusive" || reviewReport.EvidenceID == "" {
		t.Fatalf("review report = %+v", reviewReport)
	}
	if reviewReport.TransitionID == "" {
		t.Fatalf("review transition missing: %+v", reviewReport)
	}
	reviewTransition := readTransition(t, home, reviewReport.TransitionID)
	if reviewTransition.Scope.ProductID != "product:focus-notes" || !hasString(reviewTransition.Projections.Product, "product_review:review_focus_visuals_20260713") {
		t.Fatalf("review transition = %+v", reviewTransition)
	}
	reviewed, err := LoadTimeline(home, "product:focus-notes")
	if err != nil {
		t.Fatal(err)
	}
	if len(reviewed.Hypotheses) != 1 || reviewed.Hypotheses[0].Result != "inconclusive" {
		t.Fatalf("reviewed hypotheses = %+v", reviewed.Hypotheses)
	}
	if !hasString(reviewed.Hypotheses[0].EvidenceRefs, reviewReport.EvidenceID) || !hasString(reviewed.Hypotheses[0].EvidenceRefs, "product_review:review_focus_visuals_20260713") {
		t.Fatalf("review evidence refs = %+v", reviewed.Hypotheses[0].EvidenceRefs)
	}
	postReview, err := Doctor(home, "product:focus-notes")
	if err != nil {
		t.Fatal(err)
	}
	if postReview.Status != "ok" || postReview.PendingReviewCount != 0 {
		t.Fatalf("post-review doctor = %+v", postReview)
	}

	signalDoc, err := LoadSignal("../../fixtures/product/signal.asc.yaml")
	if err != nil {
		t.Fatal(err)
	}
	signalReport, err := ApplySignal(home, store, signalDoc, "fixtures/product/signal.asc.yaml", tProduct.Add(14*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if signalReport.Status != "ok" || len(signalReport.SignalIDs) != 1 || len(signalReport.EvidenceIDs) != 1 {
		t.Fatalf("signal report = %+v", signalReport)
	}
	if len(signalReport.TransitionIDs) != 1 {
		t.Fatalf("signal transitions = %+v", signalReport.TransitionIDs)
	}
	signalTransition := readTransition(t, home, signalReport.TransitionIDs[0])
	if signalTransition.Scope.ProductID != "product:focus-notes" || !hasString(signalTransition.Projections.Product, "product_signal:sig_focus_conversion_14d_20260713") {
		t.Fatalf("signal transition = %+v", signalTransition)
	}
	withSignal, err := LoadTimeline(home, "product:focus-notes")
	if err != nil {
		t.Fatal(err)
	}
	if len(withSignal.Signals) != 2 {
		t.Fatalf("signals after standalone import = %+v", withSignal.Signals)
	}
	importedSignal := findSignal(withSignal.Signals, "sig_focus_conversion_14d_20260713")
	if importedSignal.ID == "" {
		t.Fatalf("standalone signal missing: %+v", withSignal.Signals)
	}
	if !hasString(importedSignal.EvidenceRefs, signalReport.EvidenceIDs[0]) || !hasString(importedSignal.EvidenceRefs, "fixture:asc-export") {
		t.Fatalf("signal evidence refs = %+v", importedSignal.EvidenceRefs)
	}

	exportDoc, err := LoadSignal("../../fixtures/product/source-export.asc.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if exportDoc.SchemaVersion != SignalSchemaVersion || exportDoc.Signal.ID != "sig_focus_asc_export_conversion_20260713" || exportDoc.Signal.MetricName != "app_store_product_page_conversion" {
		t.Fatalf("export adapter = %+v", exportDoc)
	}
	exportReport, err := ApplySignal(home, store, exportDoc, "fixtures/product/source-export.asc.yaml", tProduct.Add(14*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if exportReport.Status != "ok" || len(exportReport.SignalIDs) != 1 || len(exportReport.EvidenceIDs) != 1 || len(exportReport.TransitionIDs) != 1 {
		t.Fatalf("export report = %+v", exportReport)
	}
	exportTransition := readTransition(t, home, exportReport.TransitionIDs[0])
	if exportTransition.Scope.ProductID != "product:focus-notes" || !hasString(exportTransition.Projections.Product, "product_signal:sig_focus_asc_export_conversion_20260713") {
		t.Fatalf("export transition = %+v", exportTransition)
	}
	withExport, err := LoadTimeline(home, "product:focus-notes")
	if err != nil {
		t.Fatal(err)
	}
	exportedSignal := findSignal(withExport.Signals, "sig_focus_asc_export_conversion_20260713")
	if exportedSignal.ID == "" || exportedSignal.MetricName != "app_store_product_page_conversion" {
		t.Fatalf("exported signal = %+v", exportedSignal)
	}
	if !hasString(exportedSignal.EvidenceRefs, "fixture:asc:conversion:before") || !hasString(exportedSignal.EvidenceRefs, "fixture:asc:conversion:after") || !hasString(exportedSignal.EvidenceRefs, exportReport.EvidenceIDs[0]) {
		t.Fatalf("exported evidence refs = %+v", exportedSignal.EvidenceRefs)
	}
	if len(withExport.Transitions) != 3 {
		t.Fatalf("timeline transitions = %+v", withExport.Transitions)
	}
	if !hasTimelineObject(withExport.Transitions, "product_signal:sig_focus_asc_export_conversion_20260713") || !hasTimelineObject(withExport.Transitions, "product_review:review_focus_visuals_20260713") {
		t.Fatalf("timeline transitions missing product refs: %+v", withExport.Transitions)
	}
}

func TestValidateRejectsSignalForOtherProduct(t *testing.T) {
	b := validBundle()
	b.Signals[0].ProductID = "product:other"
	if err := Validate(b); err == nil {
		t.Fatal("signal for another product should be rejected")
	}
}

func TestValidateReviewRejectsUnknownResult(t *testing.T) {
	review := validReview()
	review.Result = "unknown"
	if err := ValidateReview(review); err == nil {
		t.Fatal("review with unknown result should be rejected")
	}
}

func TestValidateSignalDocumentRejectsMissingSource(t *testing.T) {
	doc := validSignalDocument()
	doc.Source.Kind = ""
	if err := ValidateSignalDocument(doc); err == nil {
		t.Fatal("signal document without source should be rejected")
	}
}

func TestValidateSignalExportRejectsUnsupportedSource(t *testing.T) {
	doc := validSignalExportDocument()
	doc.Source.Kind = "spreadsheet"
	if _, err := AdaptSignalExport(doc); err == nil {
		t.Fatal("unsupported signal export source should be rejected")
	}
}

func TestSignalDoctorRejectsUnknownExportFieldsAndRecordsNoPromote(t *testing.T) {
	_, err := LoadSignal("../../fixtures/product/source-export.bad-extra.yaml")
	if err == nil {
		t.Fatal("unknown export field should fail strict load")
	}
	report := SignalDoctor("../../fixtures/product/source-export.bad-extra.yaml")
	if report.Status != "fail" || report.SourceHash == "" || !checkStatus(report.Checks, "strict_contract", "fail") {
		t.Fatalf("signal doctor report = %+v", report)
	}
	if report.ProductID != "product:focus-notes" || report.SourceKind != "app_store_connect" {
		t.Fatalf("signal doctor hints = %+v", report)
	}
	home := t.TempDir()
	nop, appended, err := RecordSignalDoctorNoPromote(home, report, tProduct)
	if err != nil {
		t.Fatal(err)
	}
	if !appended || nop.ID == "" || nop.OwnerTarget.Kind != "product_signal_source" || !hasString(nop.EvidenceRefs, report.SourceHash) || !hasString(nop.EvidenceRefs, "product_id:product:focus-notes") {
		t.Fatalf("no-promote = %+v appended=%v", nop, appended)
	}
	replayed, appended, err := RecordSignalDoctorNoPromote(home, report, tProduct.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if appended || replayed.ID != nop.ID {
		t.Fatalf("replay no-promote = %+v appended=%v, want existing %s", replayed, appended, nop.ID)
	}
	tl, err := LoadTimeline(home, "product:focus-notes")
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.SourceIssues) != 1 || tl.SourceIssues[0].ID != nop.ID {
		t.Fatalf("timeline source issues = %+v", tl.SourceIssues)
	}
	doctor, err := Doctor(home, "product:focus-notes")
	if err != nil {
		t.Fatal(err)
	}
	if doctor.Status != "needs_review" || doctor.SourceIssueCount != 1 || len(doctor.SourceIssues) != 1 || !NeedsWriteback(doctor) {
		t.Fatalf("source issue doctor = %+v", doctor)
	}
	refs := EvidenceRefs(doctor)
	if !hasString(refs, "product_source_issue:"+nop.ID) || !hasString(refs, "product:source_issues:1") {
		t.Fatalf("doctor evidence refs = %+v", refs)
	}
	gate := WritebackGate(doctor, "act_source_issue", "pd_source_issue", tProduct)
	if gate.CandidateKind != "" || gate.CandidateRef == "" || gate.OwnerTarget.Ref != "product:focus-notes" {
		t.Fatalf("source issue gate = %+v", gate)
	}
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	review := validSourceIssueReview(nop.ID)
	reviewReport, err := ApplySourceIssueReview(home, store, review, "fixtures/product/source-issue-review.yaml", tProduct.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if reviewReport.Status != "closed" || reviewReport.IssueID != nop.ID || reviewReport.TransitionID == "" || reviewReport.EvidenceID == "" {
		t.Fatalf("source issue review report = %+v", reviewReport)
	}
	closed, err := LoadTimeline(home, "product:focus-notes")
	if err != nil {
		t.Fatal(err)
	}
	if len(closed.SourceIssues) != 1 || closed.SourceIssues[0].Status != "closed" || closed.SourceIssues[0].ReviewID != review.ID {
		t.Fatalf("closed source issues = %+v", closed.SourceIssues)
	}
	if !hasTimelineObject(closed.Transitions, "product_source_issue_review:"+review.ID) {
		t.Fatalf("timeline transitions missing source issue review: %+v", closed.Transitions)
	}
	postReviewDoctor, err := Doctor(home, "product:focus-notes")
	if err != nil {
		t.Fatal(err)
	}
	if postReviewDoctor.Status != "ok" || postReviewDoctor.SourceIssueCount != 0 || NeedsWriteback(postReviewDoctor) {
		t.Fatalf("post source issue review doctor = %+v", postReviewDoctor)
	}
}

func validBundle() Bundle {
	return Bundle{
		SchemaVersion: SchemaVersion,
		Product: Product{
			ID:             "product:x",
			Name:           "X",
			OwnerEntity:    "project:x",
			Surfaces:       []Surface{{ID: "store", Kind: "app_store", Ref: "appstore:1"}},
			Channels:       []string{"app_store"},
			Positioning:    "Focused utility",
			PrivacyPosture: "local_first",
		},
		Signals: []Signal{{
			ID:           "sig_x",
			ProductID:    "product:x",
			Surface:      "store",
			ChangeEvent:  "changed screenshots",
			MetricName:   "conversion",
			Before:       0.1,
			After:        0.12,
			Window:       "7d",
			Confidence:   0.5,
			EvidenceRefs: []string{"fixture"},
		}},
		Hypotheses: []Hypothesis{{
			ID:                "hyp_x",
			ProductID:         "product:x",
			ChangeEvent:       "changed screenshots",
			ExpectedEffect:    "conversion improves",
			Metrics:           []string{"conversion"},
			ObservationWindow: "14d",
			Result:            "unknown",
			NextAction:        "wait",
			EvidenceRefs:      []string{"fixture"},
		}},
		EvidenceRefs: []string{"fixture"},
	}
}

func validReview() Review {
	return Review{
		SchemaVersion: ReviewSchemaVersion,
		ID:            "review_x",
		ProductID:     "product:x",
		HypothesisID:  "hyp_x",
		Result:        "inconclusive",
		Rationale:     "movement exists but evidence is not causal",
		Confidence:    0.5,
		EvidenceRefs:  []string{"fixture"},
	}
}

func validSourceIssueReview(issueID string) SourceIssueReview {
	return SourceIssueReview{
		SchemaVersion: SourceIssueReviewSchemaVersion,
		ID:            "source_issue_review_x",
		ProductID:     "product:focus-notes",
		IssueID:       issueID,
		Result:        "resolved",
		Rationale:     "source contract was reviewed and the adapter/source follow-up is captured",
		EvidenceRefs:  []string{"fixture:source-issue-review"},
	}
}

func validSignalDocument() SignalDocument {
	b := validBundle()
	return SignalDocument{
		SchemaVersion: SignalSchemaVersion,
		Source:        SignalSource{Kind: "app_store_connect", Ref: "fixture"},
		Signal:        b.Signals[0],
	}
}

func validSignalExportDocument() SignalExportDocument {
	return SignalExportDocument{
		SchemaVersion: SignalExportSchemaVersion,
		Source:        SignalSource{Kind: "app_store_connect", Ref: "fixture"},
		ProductID:     "product:x",
		Surface:       "store",
		ChangeEvent:   "changed screenshots",
		Metric: ExportMetric{
			Name:   "product_page_conversion_rate",
			Before: ExportMetricPoint{Value: 0.1, Ref: "fixture:before"},
			After:  ExportMetricPoint{Value: 0.12, Ref: "fixture:after"},
		},
		Window:       "14d",
		Confidence:   0.5,
		EvidenceRefs: []string{"fixture:export"},
	}
}

func hasString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func checkStatus(checks []SignalCheck, name, status string) bool {
	for _, c := range checks {
		if c.Name == name && c.Status == status {
			return true
		}
	}
	return false
}

func hasTimelineObject(transitions []TimelineTransition, objectRef string) bool {
	for _, tr := range transitions {
		if tr.ObjectRef == objectRef {
			return true
		}
	}
	return false
}

func findSignal(signals []Signal, id string) Signal {
	for _, s := range signals {
		if s.ID == id {
			return s
		}
	}
	return Signal{}
}

func readTransition(t *testing.T, home, id string) envelope.Transition {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "events", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var tr envelope.Transition
	if err := json.Unmarshal(b, &tr); err != nil {
		t.Fatal(err)
	}
	if err := tr.Validate(); err != nil {
		t.Fatal(err)
	}
	return tr
}
