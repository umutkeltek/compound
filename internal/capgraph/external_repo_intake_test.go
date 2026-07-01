package capgraph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExternalRepoIntakeReportForHome(t *testing.T) {
	home := t.TempDir()

	missing, err := ExternalRepoIntakeReportForHome(home)
	if err != nil {
		t.Fatalf("missing report should not fail: %v", err)
	}
	if missing.Status != "missing" || missing.AuthorityRef != ExternalRepoIntakeAuthorityRef {
		t.Fatalf("missing report = %+v", missing)
	}

	batchDir := filepath.Join(home, "inbox", "external-repo-intake", "2026-07-01-test")
	if err := os.MkdirAll(batchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mapPath := filepath.Join(batchDir, "external-repo-intake-map.json")
	writeJSON(t, mapPath, map[string]any{
		"boundaries": []string{"review-only", "no install"},
		"items": []map[string]any{
			{
				"repo":             "example/high-value",
				"owner":            "deep-research",
				"next_review_step": "run freshness eval",
				"not_to_do":        "do not auto-run social research",
			},
			{
				"repo":             "example/merge",
				"owner":            "existing-owner",
				"next_review_step": "merge deltas only",
				"not_to_do":        "do not duplicate skill",
			},
		},
	})
	summaryPath := filepath.Join(batchDir, "run-summary.json")
	writeJSON(t, summaryPath, map[string]any{
		"schema_version":          "compound.external_repo_intake_run.v0.1",
		"id":                      "2026-07-01-test",
		"created_at":              "2026-07-01T12:00:00Z",
		"map_path":                mapPath,
		"source_observation_path": filepath.Join(batchDir, "source-observation.json"),
		"candidate_count":         2,
		"source_observe_output": []string{
			"# source observation srcobs_external_test",
			"- source_candidate: src_cand_external_test",
			"- source_no_promote: nop_external_test",
		},
		"counts_by_decision": map[string]any{
			"keep_needs_review": 1,
			"merge_existing":    1,
		},
		"counts_by_lane": map[string]any{
			"freshness_research_skill_candidate": 1,
			"owned_skill_upstream":               1,
		},
		"decisions": []map[string]any{
			{
				"repo":              "example/high-value",
				"decision":          "keep_needs_review",
				"lane":              "freshness_research_skill_candidate",
				"status":            "recorded",
				"evidence_strength": 0.82,
				"duplicate_risk":    0.2,
				"review_id":         "scr_high",
			},
			{
				"repo":              "example/merge",
				"decision":          "merge_existing",
				"lane":              "owned_skill_upstream",
				"status":            "recorded",
				"evidence_strength": 0.7,
				"duplicate_risk":    0.95,
				"review_id":         "scr_merge",
			},
		},
	})

	report, err := ExternalRepoIntakeReportForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || report.BatchCount != 1 || report.CandidateCount != 2 {
		t.Fatalf("report counts = %+v", report)
	}
	if report.SourceObservationID != "srcobs_external_test" {
		t.Fatalf("source observation = %q", report.SourceObservationID)
	}
	if len(report.SourceCandidateIDs) != 1 || report.SourceCandidateIDs[0] != "src_cand_external_test" {
		t.Fatalf("source candidates = %+v", report.SourceCandidateIDs)
	}
	if len(report.NoPromoteIDs) != 1 || report.NoPromoteIDs[0] != "nop_external_test" {
		t.Fatalf("no-promote ids = %+v", report.NoPromoteIDs)
	}
	if report.OpenReviewCount != 1 || report.MergeExistingCount != 1 || report.ReferenceOnlyCount != 0 {
		t.Fatalf("decision counts = %+v", report)
	}
	if len(report.HighValue) != 1 || report.HighValue[0].Repo != "example/high-value" {
		t.Fatalf("high value = %+v", report.HighValue)
	}
	if report.HighValue[0].Owner != "deep-research" || report.HighValue[0].NextReviewStep != "run freshness eval" {
		t.Fatalf("high value metadata = %+v", report.HighValue[0])
	}
	if len(report.Boundaries) != 2 || report.Boundaries[1] != "no install" {
		t.Fatalf("boundaries = %+v", report.Boundaries)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
