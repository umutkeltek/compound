package routeevidence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/reviewartifact"
)

func TestImportPromotionCriteriaRoundTrip(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join("..", "..", "fixtures", "route", "promotion-criteria.yaml")
	report, err := ImportPromotionCriteria(home, source, t0)
	if err != nil {
		t.Fatalf("import promotion criteria: %v", err)
	}
	if report.AuthorityRef != PromotionCriteriaAuthorityRef || report.EntryCount != 3 || report.SourceAuthorityID == "" {
		t.Fatalf("bad import report: %+v", report)
	}
	if !promotionContainsString(report.CapabilityClasses, "judge-critique") || !promotionContainsString(report.LaneIDs, "local/test-judge") {
		t.Fatalf("bad classes/lanes: %+v %+v", report.CapabilityClasses, report.LaneIDs)
	}
	loaded, path, err := LoadPromotionCriteria(home)
	if err != nil {
		t.Fatalf("load promotion criteria: %v", err)
	}
	if path != PromotionCriteriaPath(home) || loaded.SchemaVersion != PromotionCriteriaSchemaVersion || len(loaded.Entries) != 3 {
		t.Fatalf("loaded = %+v path=%s", loaded, path)
	}
	records, err := LoadPromotionCriteriaSourceAuthority(home)
	if err != nil {
		t.Fatalf("load source authority: %v", err)
	}
	if len(records) != 1 || records[0].AuthorityStatus != "compound_primary" || records[0].EntryCount != 3 {
		t.Fatalf("bad source authority: %+v", records)
	}
}

func TestEvaluateLanePromotionsRecommendations(t *testing.T) {
	tests := []struct {
		name            string
		claimLevel      string
		statuses        []string
		spacing         time.Duration
		panelGuardrails [][]string
		wantRec         string
		wantTarget      string
		wantReason      string
	}{
		{
			name:       "insufficient samples hold",
			claimLevel: "assumed",
			statuses:   []string{"completed", "completed"},
			spacing:    72 * time.Hour,
			wantRec:    promotionRecommendationHoldInsufficient,
			wantReason: "samples 2 below min_samples 3",
		},
		{
			name:       "assumed failing rates hold failing",
			claimLevel: "assumed",
			statuses:   []string{"completed", "failed", "completed", "failed"},
			spacing:    24 * time.Hour,
			wantRec:    promotionRecommendationHoldFailing,
			wantReason: "failing rates exceed criteria",
		},
		{
			name:       "capable failing rates demote",
			claimLevel: "capable",
			statuses:   []string{"completed", "failed", "completed", "failed"},
			spacing:    24 * time.Hour,
			wantRec:    promotionRecommendationDemote,
			wantTarget: "assumed",
			wantReason: "failing rates exceed criteria",
		},
		{
			name:       "passing assumed promotes",
			claimLevel: "assumed",
			statuses:   []string{"completed", "completed", "completed", "completed"},
			spacing:    24 * time.Hour,
			wantRec:    promotionRecommendationPromote,
			wantTarget: "capable",
			wantReason: "criteria satisfied",
		},
		{
			name:       "window not met holds",
			claimLevel: "assumed",
			statuses:   []string{"completed", "completed", "completed", "completed"},
			spacing:    time.Hour,
			wantRec:    promotionRecommendationHoldInsufficient,
			wantReason: "evidence window",
		},
		{
			name:            "malformed judge rate fails",
			claimLevel:      "assumed",
			statuses:        []string{"completed", "completed", "completed", "completed"},
			spacing:         24 * time.Hour,
			panelGuardrails: [][]string{{"judge_json_malformed"}, {"judge_json_malformed"}, {}, {}},
			wantRec:         promotionRecommendationHoldFailing,
			wantReason:      "failing rates exceed criteria",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			laneID := "local/test-judge"
			writePromotionModelCapabilities(t, home, laneID, tt.claimLevel)
			writePromotionCriteria(t, home)
			recordPromotionRows(t, home, laneID, tt.statuses, tt.spacing)
			if tt.panelGuardrails != nil {
				writePromotionPanelRun(t, home, laneID, tt.panelGuardrails)
			}

			reviews, err := EvaluateLanePromotions(home, t0.Add(10*time.Hour))
			if err != nil {
				t.Fatalf("evaluate promotions: %v", err)
			}
			if len(reviews) != 1 {
				t.Fatalf("reviews = %+v, want one", reviews)
			}
			review := reviews[0]
			if review.Recommendation != tt.wantRec || review.RecommendedClaimLevel != tt.wantTarget || !strings.Contains(review.Reason, tt.wantReason) {
				t.Fatalf("review = %+v", review)
			}
			if tt.wantTarget != "" && !strings.Contains(review.ProposedChange, fmt.Sprintf(" to %q", tt.wantTarget)) {
				t.Fatalf("proposed change missing target transition: %q", review.ProposedChange)
			}
			if review.Tallies.Samples != len(tt.statuses) {
				t.Fatalf("samples = %d, want %d", review.Tallies.Samples, len(tt.statuses))
			}
			if review.ArtifactPaths.PlanPath == "" || review.ArtifactPaths.ReviewPath == "" {
				t.Fatalf("missing artifact paths: %+v", review.ArtifactPaths)
			}
			if _, err := os.Stat(review.ArtifactPaths.PlanPath); err != nil {
				t.Fatalf("json artifact missing: %v", err)
			}
			if _, err := os.Stat(review.ArtifactPaths.ReviewPath); err != nil {
				t.Fatalf("md artifact missing: %v", err)
			}
		})
	}
}

func TestRecordPromotionHoldReviewRejectsMutatingRecommendation(t *testing.T) {
	home := t.TempDir()
	paths, err := reviewartifact.Write(home, routePromotionReviewArea, "route_promotion_promote_guard", PromotionReview{
		SchemaVersion:         PromotionReviewSchemaVersion,
		ID:                    "route_promotion_promote_guard",
		Status:                "review_required",
		Recommendation:        promotionRecommendationPromote,
		LaneID:                "auto/judge",
		CapabilityClass:       "classify-extract",
		CurrentClaimLevel:     "capable",
		RecommendedClaimLevel: "primary",
		Reason:                "samples meet criteria",
	}, " ", "# Route promotion\n")
	if err != nil {
		t.Fatal(err)
	}
	_, err = RecordPromotionHoldReview(home, paths.PlanPath, "recording promote through hold review must fail", t0)
	if err == nil || !strings.Contains(err.Error(), "separate authority review required") {
		t.Fatalf("err = %v, want separate authority review guard", err)
	}
}

func TestEvaluateLanePromotionsArtifactIdempotency(t *testing.T) {
	home := t.TempDir()
	laneID := "local/test-judge"
	writePromotionModelCapabilities(t, home, laneID, "assumed")
	writePromotionCriteria(t, home)
	recordPromotionRows(t, home, laneID, []string{"completed", "completed", "completed", "completed"}, 24*time.Hour)

	first, err := EvaluateLanePromotions(home, t0)
	if err != nil {
		t.Fatalf("evaluate first: %v", err)
	}
	second, err := EvaluateLanePromotions(home, t0.Add(time.Hour))
	if err != nil {
		t.Fatalf("evaluate second: %v", err)
	}
	if len(first) != 1 || len(second) != 1 || first[0].ID != second[0].ID || first[0].ArtifactPaths.PlanPath != second[0].ArtifactPaths.PlanPath {
		t.Fatalf("non-idempotent artifacts: first=%+v second=%+v", first, second)
	}
	entries, err := os.ReadDir(filepath.Join(home, "reviews", routePromotionReviewArea))
	if err != nil {
		t.Fatalf("read review dir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("review dir entries = %d, want json+md only", len(entries))
	}
	data, err := os.ReadFile(second[0].ArtifactPaths.PlanPath)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	var persisted PromotionReview
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("parse artifact: %v", err)
	}
	if persisted.ID != second[0].ID || persisted.CreatedAt != rfc3339(t0.Add(time.Hour)) {
		t.Fatalf("persisted = %+v, second = %+v", persisted, second[0])
	}
}

func writePromotionModelCapabilities(t *testing.T, home, laneID, claimLevel string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "model-capabilities.yaml")
	content := fmt.Sprintf(`version: 1
purpose: "promotion test model router"
rules:
  - "promotion tests only"
capability_classes:
  - judge-critique
models:
  %q:
    lane: %q
    tier: cheap
    packet_ceiling: panel-target
    capabilities:
      judge-critique:
        level: %s
        evidence: "fixture claim"
routing_defaults:
  judge-critique:
    - %q
`, laneID, laneID+" (fixture)", claimLevel, laneID)
	if err := os.WriteFile(source, []byte(content), 0o644); err != nil {
		t.Fatalf("write model capabilities: %v", err)
	}
	if _, err := ImportModelCapabilitiesYAML(home, source, t0); err != nil {
		t.Fatalf("import model capabilities: %v", err)
	}
}

func writePromotionCriteria(t *testing.T, home string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "promotion-criteria.yaml")
	content := `schema_version: compound.promotion_criteria.v0.1
entries:
  - lane_id: "*"
    capability_class: judge-critique
    min_samples: 3
    max_error_rate: 0.25
    max_malformed_rate: 0.25
    min_window_days: 3
    notes: "test criteria"
`
	if err := os.WriteFile(source, []byte(content), 0o644); err != nil {
		t.Fatalf("write promotion criteria: %v", err)
	}
	if _, err := ImportPromotionCriteria(home, source, t0); err != nil {
		t.Fatalf("import promotion criteria: %v", err)
	}
}

func recordPromotionRows(t *testing.T, home, laneID string, statuses []string, spacing time.Duration) {
	t.Helper()
	for i, status := range statuses {
		ev, err := Build(BuildInput{
			CreatedAt:     t0.Add(time.Duration(i) * spacing),
			RequestID:     fmt.Sprintf("promotion:%s:%d", strings.ReplaceAll(laneID, "/", "_"), i),
			ClientProfile: "fixture.promotion",
			Protocol:      "fixture",
			Provider:      "fixture-provider",
			Model:         laneID,
			Status:        status,
			EvidenceRefs:  []string{ModelCapabilitiesAuthorityRef},
		})
		if err != nil {
			t.Fatalf("build route evidence: %v", err)
		}
		if err := RecordEvidence(home, ev); err != nil {
			t.Fatalf("record route evidence: %v", err)
		}
	}
}

func writePromotionPanelRun(t *testing.T, home, laneID string, guardrails [][]string) {
	t.Helper()
	findings := make([]map[string]any, 0, len(guardrails))
	for i, item := range guardrails {
		findings = append(findings, map[string]any{
			"synthesis": map[string]any{
				"schema_version":           "compound.panel_synthesis.v0.1",
				"decision":                 "needs_human",
				"guardrails_applied":       item,
				"judge_route_evidence_ref": fmt.Sprintf("route_panel_%d", i),
			},
		})
	}
	run := map[string]any{
		"id": "prun_promotion_fixture",
		"route": map[string]any{
			"judge_lane": map[string]any{"model_id": laneID},
		},
		"findings": findings,
	}
	data, err := json.MarshalIndent(run, "", " ")
	if err != nil {
		t.Fatalf("marshal panel run: %v", err)
	}
	dir := filepath.Join(home, "panel-runs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir panel runs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prun_promotion_fixture.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write panel run: %v", err)
	}
}

func promotionContainsString(set []string, value string) bool {
	for _, item := range set {
		if item == value {
			return true
		}
	}
	return false
}
