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
	filtered, err := ExternalRepoIntakeReportForHome(home, "https://github.com/example/high-value.git")
	if err != nil {
		t.Fatalf("filtered report should not fail: %v", err)
	}
	if filtered.RepoQuery == "" || len(filtered.Matches) != 1 || filtered.Matches[0].Repo != "example/high-value" {
		t.Fatalf("filtered matches = %+v", filtered)
	}
	if filtered.Matches[0].NotToDo != "do not auto-run social research" {
		t.Fatalf("filtered metadata missing: %+v", filtered.Matches[0])
	}
	partial, err := ExternalRepoIntakeReportForHome(home, "merge")
	if err != nil {
		t.Fatalf("partial report should not fail: %v", err)
	}
	if len(partial.Matches) != 1 || partial.Matches[0].Repo != "example/merge" {
		t.Fatalf("partial matches = %+v", partial.Matches)
	}
	if len(report.Boundaries) != 2 || report.Boundaries[1] != "no install" {
		t.Fatalf("boundaries = %+v", report.Boundaries)
	}
}

func TestExternalRepoIntakeReportSourceListCoverage(t *testing.T) {
	home := t.TempDir()
	batchDir := filepath.Join(home, "inbox", "external-repo-intake", "2026-07-01-test")
	if err := os.MkdirAll(batchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mapPath := filepath.Join(batchDir, "external-repo-intake-map.json")
	writeJSON(t, mapPath, map[string]any{
		"items": []map[string]any{
			{
				"repo":             "example/known",
				"owner":            "procedure:skill-ops",
				"next_review_step": "merge deltas only",
				"not_to_do":        "do not install wholesale",
			},
		},
	})
	writeJSON(t, filepath.Join(batchDir, "run-summary.json"), map[string]any{
		"schema_version":  "compound.external_repo_intake_run.v0.1",
		"id":              "2026-07-01-test",
		"created_at":      "2026-07-01T12:00:00Z",
		"map_path":        mapPath,
		"candidate_count": 1,
		"decisions": []map[string]any{
			{
				"repo":              "example/known",
				"decision":          "merge_existing",
				"lane":              "external_skill_candidate",
				"evidence_strength": 0.8,
			},
		},
		"counts_by_decision": map[string]any{"merge_existing": 1},
		"counts_by_lane":     map[string]any{"external_skill_candidate": 1},
	})
	sourceListPath := filepath.Join(home, "repos.md")
	if err := os.WriteFile(sourceListPath, []byte("https://github.com/example/known\nhttps://github.com/example/known\nhttps://github.com/example/unknown\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evalDir := filepath.Join(home, "inbox", "capability-evaluations")
	if err := os.MkdirAll(evalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(evalDir, "receipt-only-mention.json"), map[string]any{
		"schema_version":    1,
		"id":                "capability-evaluation.external.other.20260701",
		"source_receipt":    "file:///tmp/repos.md#github.com/example/unknown",
		"capability":        "procedure:skill-ops",
		"verdict":           "downgrade_to_index",
		"rubric_normalized": 0.4,
		"signals": map[string]any{
			"external_repo":  "example/other",
			"external_url":   "https://github.com/example/other",
			"existing_owner": "procedure:skill-ops",
		},
	})

	report, err := ExternalRepoIntakeReportForHomeWithSourceList(home, sourceListPath)
	if err != nil {
		t.Fatalf("source list report should not fail: %v", err)
	}
	if report.SourceListPath != sourceListPath || report.SourceListTotal != 2 || report.SourceListKnown != 1 || report.SourceListUnknown != 1 {
		t.Fatalf("source list counts = %+v", report)
	}
	if got := report.SourceListItems[0]; got.Status != "known" || got.Normalized != "example/known" || got.MatchCount != 1 {
		t.Fatalf("known item = %+v", got)
	}
	if report.SourceListByDecision["merge_existing"] != 1 || report.SourceListByLane["external_skill_candidate"] != 1 || report.SourceListByOwner["procedure:skill-ops"] != 1 {
		t.Fatalf("source-list aggregate counts missing: decision=%+v lane=%+v owner=%+v", report.SourceListByDecision, report.SourceListByLane, report.SourceListByOwner)
	}
	if report.SourceListItems[0].Matches[0].Owner != "procedure:skill-ops" {
		t.Fatalf("known item metadata missing: %+v", report.SourceListItems[0].Matches[0])
	}
	if got := report.SourceListItems[1]; got.Status != "unknown" || got.Normalized != "example/unknown" || got.MatchCount != 0 {
		t.Fatalf("unknown item = %+v", got)
	}
}

func TestExternalRepoIntakeReportIncludesMatchingEvaluationReferences(t *testing.T) {
	home := t.TempDir()
	batchDir := filepath.Join(home, "inbox", "external-repo-intake", "2026-07-01-test")
	if err := os.MkdirAll(batchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mapPath := filepath.Join(batchDir, "external-repo-intake-map.json")
	writeJSON(t, mapPath, map[string]any{
		"items": []map[string]any{
			{"repo": "example/repo"},
		},
	})
	writeJSON(t, filepath.Join(batchDir, "run-summary.json"), map[string]any{
		"schema_version":  "compound.external_repo_intake_run.v0.1",
		"id":              "2026-07-01-test",
		"created_at":      "2026-07-01T12:00:00Z",
		"map_path":        mapPath,
		"candidate_count": 1,
		"decisions": []map[string]any{
			{
				"repo":              "example/repo",
				"decision":          "merge_existing",
				"lane":              "external_skill_candidate",
				"evidence_strength": 0.6,
			},
		},
		"counts_by_decision": map[string]any{"merge_existing": 1},
		"counts_by_lane":     map[string]any{"external_skill_candidate": 1},
	})

	evalDir := filepath.Join(home, "inbox", "capability-evaluations")
	if err := os.MkdirAll(evalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(evalDir, "karpathy-gist.json"), map[string]any{
		"schema_version":    1,
		"id":                "capability-evaluation.external.karpathy.gist.20260701",
		"created_at":        "2026-07-01T15:14:30Z",
		"source_receipt":    "file:///tmp/repos.md#gist.github.com/karpathy/442a6bf555914893e9891c11519de94f",
		"capability":        "procedure:skill-ops",
		"verdict":           "downgrade_to_index",
		"local_state_only":  true,
		"rubric_normalized": 0.38,
		"signals": map[string]any{
			"kind":                "external_reference_only_source",
			"cluster":             "skill-source-intake",
			"external_repo":       "gist:karpathy/442a6bf555914893e9891c11519de94f",
			"external_url":        "https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f",
			"existing_owner":      "procedure:skill-ops",
			"required_next_proof": "revisit only if a concrete task cites the gist",
			"main_risks":          "do not treat gist notes as installable repos",
			"distinctive_claim":   "reference-only source",
			"owned_gap_observed":  "no runtime adoption gap",
			"comparison_scope":    "Desktop source list coverage",
		},
	})

	writeJSON(t, filepath.Join(evalDir, "skills-sh-marketplace.json"), map[string]any{
		"schema_version":    1,
		"id":                "capability-evaluation.external.skills-sh-marketplace.20260701",
		"created_at":        "2026-07-01T15:41:40Z",
		"source_receipt":    "compound://reviews/external-repo-intake/2026-07-01-external-skill-repo-map#skills-sh-marketplace",
		"capability":        "procedure:skill-ops",
		"verdict":           "downgrade_to_index",
		"local_state_only":  true,
		"rubric_normalized": 0.66,
		"signals": map[string]any{
			"kind":                "external_catalog_reference",
			"cluster":             "skill-catalog-source-discovery",
			"external_repo":       "skills.sh marketplace",
			"external_url":        "https://www.skills.sh",
			"existing_owner":      "procedure:skill-ops",
			"required_next_proof": "review child skills individually through capability intake",
			"main_risks":          "do not bulk install marketplace skills",
			"distinctive_claim":   "marketplace catalog source, not runtime authority",
			"owned_gap_observed":  "query coverage for marketplace source itself",
			"comparison_scope":    "Marketplace catalog lookup and child-skill candidate triage",
		},
	})

	report, err := ExternalRepoIntakeReportForHome(home, "gist.github.com/karpathy/442a6bf555914893e9891c11519de94f")
	if err != nil {
		t.Fatalf("report should include evaluation references: %v", err)
	}
	if len(report.Matches) != 1 {
		t.Fatalf("matches = %+v", report.Matches)
	}
	match := report.Matches[0]
	if match.Repo != "gist:karpathy/442a6bf555914893e9891c11519de94f" {
		t.Fatalf("repo = %q", match.Repo)
	}
	if match.Decision != "reference_only" || match.Lane != "skill-source-intake" {
		t.Fatalf("decision/lane = %+v", match)
	}
	if match.Owner != "procedure:skill-ops" || match.GraphHint != "downgrade_to_index" {
		t.Fatalf("owner/hint = %+v", match)
	}
	if match.EvidenceStrength != 0.38 {
		t.Fatalf("evidence strength = %.2f", match.EvidenceStrength)
	}

	repoReport, err := ExternalRepoIntakeReportForHome(home, "example/repo")
	if err != nil {
		t.Fatalf("repo report should still work: %v", err)
	}
	if len(repoReport.Matches) != 1 || repoReport.Matches[0].Repo != "example/repo" {
		t.Fatalf("repo matches = %+v", repoReport.Matches)
	}
	marketplaceReport, err := ExternalRepoIntakeReportForHome(home, "skills.sh")
	if err != nil {
		t.Fatalf("marketplace report should not fail: %v", err)
	}
	if len(marketplaceReport.Matches) != 1 || marketplaceReport.Matches[0].Repo != "skills.sh marketplace" {
		t.Fatalf("marketplace matches = %+v", marketplaceReport.Matches)
	}
	if marketplaceReport.Matches[0].Decision != "reference_only" || marketplaceReport.Matches[0].GraphHint != "downgrade_to_index" {
		t.Fatalf("marketplace decision should stay index-only: %+v", marketplaceReport.Matches[0])
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
