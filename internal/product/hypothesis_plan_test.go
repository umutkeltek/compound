package product

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/candidates"
	"compound/internal/mem"
)

func TestHypothesisPlanRecordsReviewArtifactsWithoutMutatingTarget(t *testing.T) {
	home := t.TempDir()
	candidateID := "hyp_focus_visuals_conversion"
	dir := filepath.Join(home, candidates.InboxDir(candidates.ProductHypothesis))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	candidate := hypothesisPlanCandidate{
		SchemaVersion:     ProductHypothesisCandidate,
		ID:                candidateID,
		Kind:              string(candidates.ProductHypothesis),
		ProductID:         "product:focus-notes",
		ChangeEvent:       "Changed first screenshot feature focused capture moment.",
		ExpectedEffect:    "conversion improves because screenshot communicates capture speed",
		Metrics:           []string{"app_store_product_page_conversion"},
		ObservationWindow: "14d after change",
		Result:            "unknown",
		NextAction:        "review conversion after full window",
		Confidence:        0.42,
		Verdict:           candidates.Park,
		EvidenceRefs:      []string{"fixture:app-store-metrics", "product_signal:sig_focus_screenshot"},
	}
	data, err := json.MarshalIndent(candidate, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, candidateID+".json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(t.TempDir(), "product-strategy.md")
	original := []byte("# Product Strategy\n\nExisting strategy source.\n")
	if err := os.WriteFile(targetPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildHypothesisPlan(home, candidateID, targetPath, tProduct)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != HypothesisPlanSchemaVersion ||
		plan.Status != "review_required" ||
		plan.CandidateID != candidateID ||
		plan.CandidateVerdict != candidates.Park ||
		plan.ProductID != "product:focus-notes" ||
		plan.HypothesisKey == "" ||
		!strings.HasPrefix(plan.ReviewDraft.ReviewID, "review_") ||
		!plan.TargetExists ||
		plan.TargetHash == "" ||
		len(plan.ReviewDraft.RequiredEvidence) != 4 {
		t.Fatalf("bad hypothesis plan: %+v", plan)
	}
	paths, err := RecordHypothesisPlan(home, plan)
	if err != nil {
		t.Fatal(err)
	}
	var recorded HypothesisPlan
	recordedData, err := os.ReadFile(paths.PlanPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(recordedData, &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.ID != plan.ID || recorded.ReviewDraft.DecisionGate == "" {
		t.Fatalf("recorded plan = %+v", recorded)
	}
	review, err := os.ReadFile(paths.ReviewPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(review), "does not mutate product strategy") ||
		!strings.Contains(string(review), "Required Evidence Before Action") ||
		!strings.Contains(string(review), "does not") {
		t.Fatalf("review missing no-mutation/evidence text:\n%s", review)
	}
	signalDraft, err := os.ReadFile(paths.SignalDraftPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(signalDraft), "TODO_before_value") ||
		!strings.Contains(string(signalDraft), "product_hypothesis:"+candidateID) {
		t.Fatalf("signal draft missing guarded placeholders/evidence:\n%s", signalDraft)
	}
	if _, err := LoadSignal(paths.SignalDraftPath); err == nil {
		t.Fatal("generated signal draft should fail until real metric values are filled")
	}
	signalYAML := strings.ReplaceAll(string(signalDraft), "TODO_provider", "app_store_connect")
	signalYAML = strings.ReplaceAll(signalYAML, "TODO_surface", "app-store-page")
	signalYAML = strings.ReplaceAll(signalYAML, "TODO_before_value", "0.041")
	signalYAML = strings.ReplaceAll(signalYAML, "TODO_after_value", "0.049")
	signalPath := filepath.Join(t.TempDir(), "product-signal.yaml")
	if err := os.WriteFile(signalPath, []byte(signalYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	signalDoc, err := LoadSignal(signalPath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	signalReport, err := ApplySignal(home, store, signalDoc, signalPath, tProduct.AddDate(0, 0, 14))
	if err != nil {
		t.Fatal(err)
	}
	if signalReport.Status != "ok" || len(signalReport.SignalIDs) != 1 || len(signalReport.TransitionIDs) != 1 {
		t.Fatalf("signal report = %+v", signalReport)
	}
	after, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("target mutated:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("hypothesis plan must not promote claims, stat err=%v", err)
	}
}

func TestHypothesisPlanAcceptsDistillerShapedProductCandidate(t *testing.T) {
	home := t.TempDir()
	candidateID := "cand_product_hypothesis"
	dir := filepath.Join(home, candidates.InboxDir(candidates.ProductHypothesis))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	candidate := hypothesisPlanCandidate{
		ID:           candidateID,
		Kind:         string(candidates.ProductHypothesis),
		Subject:      "product:focus-notes:app-store",
		Statement:    "screenshot clarity may improve product page conversion",
		Detail:       "Changed first screenshot to show fast capture moment",
		Confidence:   0.81,
		Verdict:      candidates.Park,
		EvidenceRefs: []string{"trace:product-hypothesis"},
	}
	data, err := json.MarshalIndent(candidate, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, candidateID+".json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildHypothesisPlan(home, candidateID, "", tProduct)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ProductID != "product:focus-notes" ||
		plan.ChangeEvent != "Changed first screenshot to show fast capture moment" ||
		plan.ExpectedEffect != "screenshot clarity may improve product page conversion" ||
		len(plan.Metrics) != 1 ||
		plan.Metrics[0] != "metric selection required" ||
		plan.ObservationWindow != "review window required" {
		t.Fatalf("distiller-shaped plan = %+v", plan)
	}
}

// TestReadHypothesisPlanCandidateRejectsPathTraversalID is the S3 red
// falsifier: readHypothesisPlanCandidate must reject a path-traversal-shaped
// candidate id before it ever touches disk. A candidate file with no "id"
// field is planted OUTSIDE inbox/product-hypotheses/ so a pre-fix reader
// (bare filepath.Join, id equality checked only AFTER reading) succeeds and
// defaults the empty id to the traversal string — proving the escape rather
// than a coincidental not-exist error.
func TestReadHypothesisPlanCandidateRejectsPathTraversalID(t *testing.T) {
	home := t.TempDir()
	inboxDir := filepath.Join(home, candidates.InboxDir(candidates.ProductHypothesis))
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideDir := filepath.Join(home, "outside-hypothesis-secret")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	planted := hypothesisPlanCandidate{Kind: string(candidates.ProductHypothesis), Subject: "escaped read"}
	data, err := json.MarshalIndent(planted, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "planted.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	traversalID := "../../outside-hypothesis-secret/planted"
	if _, err := readHypothesisPlanCandidate(home, traversalID); err == nil {
		t.Fatalf("path-traversal-shaped candidate id %q must be rejected before touching disk", traversalID)
	}
}
