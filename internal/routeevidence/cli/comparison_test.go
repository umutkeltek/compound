package routeevidencecli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/routeevidence"
)

// routeComparisonReview builds one route outcome review for the CLI fixture.
func routeComparisonReview(id, model, requestID, outcome string, stateChanged bool, tokens int, cost int64) routeevidence.RouteOutcomeReview {
	return routeevidence.RouteOutcomeReview{
		SchemaVersion:       routeevidence.RouteOutcomeReviewSchemaVersion,
		ID:                  id,
		CreatedAt:           "2026-10-27T10:00:00Z",
		Status:              "recorded",
		RouteEvidenceID:     "ev_" + id,
		RequestID:           requestID,
		WorkID:              "work_" + id,
		ReceiptID:           "rec_" + id,
		ReceiptOutcome:      outcome,
		ReceiptStateChanged: stateChanged,
		RouteStatus:         "completed",
		SelectedTarget:      routeevidence.Target{Provider: "fixture-provider", Model: model, Protocol: "mcp"},
		Usage:               routeevidence.Usage{TotalTokens: tokens, PromptTokens: tokens},
		CostMicros:          cost,
		LatencyMS:           120,
		EvidenceRefs:        []string{"ev_" + id, "rec_" + id},
	}
}

// The CLI must reach the assessment and print the verdict. A matched,
// measured comparison with no critical regression recommends the replacement.
func TestRouteComparisonCLIRecommendsMatchedComparison(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "comparison.json")
	body, err := json.Marshal(map[string]any{
		"schema_version":  routeevidence.RouteComparisonInputSchemaVersion,
		"decision_id":     "decision_route_change_fixture",
		"incumbent_route": map[string]any{"provider": "fixture-provider", "model": "model-a", "protocol": "mcp"},
		"candidate_route": map[string]any{"provider": "fixture-provider", "model": "model-b", "protocol": "mcp"},
		"cases": []map[string]any{{
			"case_id": "case_pinned",
			"incumbent": []routeevidence.RouteOutcomeReview{
				routeComparisonReview("inc_a", "model-a", "req_inc_1", "completed", true, 1000, 500),
			},
			"candidate": []routeevidence.RouteOutcomeReview{
				routeComparisonReview("cand_a", "model-b", "req_cand_1", "completed", true, 900, 450),
			},
		}},
		"critical_cases":  []string{"case_pinned"},
		"min_repetitions": 1,
	})
	if err != nil {
		t.Fatalf("marshal comparison input: %v", err)
	}
	if err := os.WriteFile(input, body, 0o600); err != nil {
		t.Fatalf("write comparison input: %v", err)
	}

	out := captureComparisonStdout(t, input, false)
	for _, want := range []string{
		"# route comparison rcmp_",
		"- decision: decision_route_change_fixture",
		"- assessment_class: comparison_supports_replacement",
		"- recommend_replacement: true",
		"- matched_cases: 1",
		"- incumbent_route: fixture-provider/model-a (mcp)",
		"- candidate_route: fixture-provider/model-b (mcp)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("comparison output missing %q:\n%s", want, out)
		}
	}
}

// A comparison whose candidate regressed on a critical case must refuse, and
// the refusal must name the reason.
func TestRouteComparisonCLIRefusesCriticalRegression(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "comparison.json")
	body, err := json.Marshal(map[string]any{
		"schema_version":  routeevidence.RouteComparisonInputSchemaVersion,
		"decision_id":     "decision_route_change_fixture",
		"incumbent_route": map[string]any{"provider": "fixture-provider", "model": "model-a", "protocol": "mcp"},
		"candidate_route": map[string]any{"provider": "fixture-provider", "model": "model-b", "protocol": "mcp"},
		"cases": []map[string]any{{
			"case_id": "case_pinned",
			"incumbent": []routeevidence.RouteOutcomeReview{
				routeComparisonReview("inc_a", "model-a", "req_inc_1", "completed", true, 1000, 500),
			},
			"candidate": []routeevidence.RouteOutcomeReview{
				routeComparisonReview("cand_a", "model-b", "req_cand_1", "failed", false, 1000, 500),
			},
		}},
		"critical_cases":  []string{"case_pinned"},
		"min_repetitions": 1,
	})
	if err != nil {
		t.Fatalf("marshal comparison input: %v", err)
	}
	if err := os.WriteFile(input, body, 0o600); err != nil {
		t.Fatalf("write comparison input: %v", err)
	}

	out := captureComparisonStdout(t, input, false)
	for _, want := range []string{
		"- recommend_replacement: false",
		"- critical_regressions: 1",
		"- blocked:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("comparison output missing %q:\n%s", want, out)
		}
	}
}

// The comparison is EVIDENCE: recording it must not mutate routing policy,
// promote a claim, or spend.
func TestRouteComparisonCLIRecordWritesEvidenceOnly(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "comparison.json")
	body, err := json.Marshal(map[string]any{
		"schema_version":  routeevidence.RouteComparisonInputSchemaVersion,
		"decision_id":     "decision_route_change_fixture",
		"incumbent_route": map[string]any{"provider": "fixture-provider", "model": "model-a", "protocol": "mcp"},
		"candidate_route": map[string]any{"provider": "fixture-provider", "model": "model-b", "protocol": "mcp"},
		"cases": []map[string]any{{
			"case_id": "case_pinned",
			"incumbent": []routeevidence.RouteOutcomeReview{
				routeComparisonReview("inc_a", "model-a", "req_inc_1", "completed", true, 1000, 500),
			},
			"candidate": []routeevidence.RouteOutcomeReview{
				routeComparisonReview("cand_a", "model-b", "req_cand_1", "completed", true, 900, 450),
			},
		}},
		"critical_cases":  []string{"case_pinned"},
		"min_repetitions": 1,
	})
	if err != nil {
		t.Fatalf("marshal comparison input: %v", err)
	}
	if err := os.WriteFile(input, body, 0o600); err != nil {
		t.Fatalf("write comparison input: %v", err)
	}

	homePath := t.TempDir()
	out := captureComparisonStdoutIn(t, homePath, input, true)
	if !strings.Contains(out, "- recorded:") {
		t.Fatalf("comparison output missing the recorded path:\n%s", out)
	}
	// The evidence artifact exists.
	recorded := comparisonOutputValue(out, "- recorded:")
	if recorded == "" {
		t.Fatalf("comparison output missing the recorded path:\n%s", out)
	}
	if _, err := os.Stat(recorded); err != nil {
		t.Fatalf("recorded comparison artifact missing: %v", err)
	}
	// It must not have promoted a claim or minted a route decision.
	for _, forbidden := range []string{
		filepath.Join(homePath, "ledger", "claims.jsonl"),
		filepath.Join(homePath, "ledger", "decisions.jsonl"),
	} {
		if _, err := os.Stat(forbidden); !os.IsNotExist(err) {
			t.Fatalf("route comparison must not write %s, stat err=%v", forbidden, err)
		}
	}
}

// captureComparisonStdout runs the comparison body the way cmd/compound does
// and returns what it printed.
func captureComparisonStdout(t *testing.T, input string, record bool) string {
	t.Helper()
	args := []string{"--input", input}
	if record {
		args = append(args, "--record")
	}
	var stdout, stderr bytes.Buffer
	if code := RunComparison(args, t.TempDir(), &stdout, &stderr); code != 0 {
		t.Fatalf("comparison exit code = %d, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// comparisonOutputValue extracts the value after a "key: " marker.
func comparisonOutputValue(out, marker string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, marker) {
			return strings.TrimSpace(strings.TrimPrefix(line, marker))
		}
	}
	return ""
}

// captureComparisonStdoutIn runs the comparison body against a named home and
// returns what it printed.
func captureComparisonStdoutIn(t *testing.T, homePath, input string, record bool) string {
	t.Helper()
	args := []string{"--input", input}
	if record {
		args = append(args, "--record")
	}
	var stdout, stderr bytes.Buffer
	if code := RunComparison(args, homePath, &stdout, &stderr); code != 0 {
		t.Fatalf("comparison exit code = %d, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	return stdout.String()
}
