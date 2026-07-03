package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/candidates"
	"compound/internal/home"
	"compound/internal/panel"
	"compound/internal/reviewartifact"
)

func TestPanelLensesCLIJSON(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	manifest := filepath.Join("..", "..", "fixtures", "panel", "lens-manifest.yaml")
	captureStdout(t, func() { cmdPanel([]string{"import-lenses", manifest}) })

	out := captureStdout(t, func() { cmdPanel([]string{"lenses", "--target-kind", "memory_candidate", "--json"}) })
	var memorySelection panel.LensSelection
	if err := json.Unmarshal([]byte(out), &memorySelection); err != nil {
		t.Fatalf("parse memory lens JSON: %v\n%s", err, out)
	}
	if len(memorySelection.Included) != 6 || len(memorySelection.Excluded) != 3 {
		t.Fatalf("memory_candidate selection = %+v, want six included and three comparison-only excluded", memorySelection)
	}

	out = captureStdout(t, func() { cmdPanel([]string{"lenses", "--target-kind", "external_repo", "--json"}) })
	var externalSelection panel.LensSelection
	if err := json.Unmarshal([]byte(out), &externalSelection); err != nil {
		t.Fatalf("parse external lens JSON: %v\n%s", err, out)
	}
	if len(externalSelection.Included) != 1 || externalSelection.Included[0].NodeID != "lens:privacy-risk" {
		t.Fatalf("external_repo included = %+v, want only privacy-risk", externalSelection.Included)
	}
	reasons := map[string]string{}
	for _, excluded := range externalSelection.Excluded {
		reasons[excluded.Ref] = excluded.Reason
	}
	for _, id := range []string{"lens:duplicate-risk", "lens:evidence-strength", "lens:contradiction-with-existing-memory", "lens:fossilization-risk", "lens:comparison-correctness", "lens:comparison-contract-fidelity", "lens:comparison-cost-latency", "lens:inversion-falsification"} {
		if reasons[id] != "trigger_mismatch" {
			t.Fatalf("%s exclusion reason = %q, want trigger_mismatch (all reasons: %+v)", id, reasons[id], reasons)
		}
	}
}

func TestPanelPlanRunShowCLIHappyPath(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	captureStdout(t, func() {
		cmdRoute([]string{"import-model-capabilities", filepath.Join("..", "..", "fixtures", "panel", "model-capabilities.yaml")})
	})
	captureStdout(t, func() {
		cmdPanel([]string{"import-lenses", filepath.Join("..", "..", "fixtures", "panel", "lens-manifest.yaml")})
	})
	seedPanelCLITarget(t, homePath, "mcand_example_api_clean")
	seedPanelCLITarget(t, homePath, "mcand_example_api_dissent")

	planOut := captureStdout(t, func() {
		cmdPanel([]string{"plan", "--target-kind", "memory_candidate", "--target", "mcand_example_api_clean", "--target", "mcand_example_api_dissent", "--state", "parked", "--not-privacy-sensitive", "synthetic fixtures", "--limit", "2", "--max-items", "2", "--json"})
	})
	var planned panel.Run
	if err := json.Unmarshal([]byte(planOut), &planned); err != nil {
		t.Fatalf("parse plan JSON: %v\n%s", err, planOut)
	}
	if planned.Status != "planned" || planned.Route.JudgeLane.ModelID != "panel/cheap-judge" {
		t.Fatalf("planned run = %+v", planned)
	}

	runOut := captureStdout(t, func() {
		cmdPanel([]string{"run", "--plan", planned.ID, "--fixtures", filepath.Join("..", "..", "fixtures", "panel"), "--json"})
	})
	var completed panel.Run
	if err := json.Unmarshal([]byte(runOut), &completed); err != nil {
		t.Fatalf("parse run JSON: %v\n%s", err, runOut)
	}
	if completed.Status != "verdicts_recorded" || len(completed.Findings) != 2 {
		t.Fatalf("completed run = %+v", completed)
	}

	showOut := captureStdout(t, func() { cmdPanel([]string{"show", planned.ID}) })
	for _, want := range []string{"- status: verdicts_recorded", "decision=promote", "verdict:", "omitted: panel/free-judge-tripwire reason=free lane may not judge"} {
		if !strings.Contains(showOut, want) {
			t.Fatalf("show output missing %q:\n%s", want, showOut)
		}
	}

	applyOut := captureStdout(t, func() {
		cmdPanel([]string{"run", "--plan", planned.ID, "--apply", "--json"})
	})
	var applied panel.Run
	if err := json.Unmarshal([]byte(applyOut), &applied); err != nil {
		t.Fatalf("parse apply JSON: %v\n%s", err, applyOut)
	}
	if applied.Status != "applied" || applied.WorkID == "" || applied.ReceiptID == "" || applied.TransitionID == "" {
		t.Fatalf("applied run = %+v", applied)
	}
	applyShow := captureStdout(t, func() {
		cmdPanel([]string{"show", planned.ID})
	})
	for _, want := range []string{"- status: applied", "- work:", "- receipt:", "application: mapped=promote status=applied", "claim:"} {
		if !strings.Contains(applyShow, want) {
			t.Fatalf("apply show output missing %q:\n%s", want, applyShow)
		}
	}
}

func TestPanelVerdictRecordCLIAnnotatesQueue(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	paths, err := reviewartifact.Write(homePath, "panel-verdicts", "panel_verdict_record_cli", map[string]any{
		"schema_version": "compound.panel_verdict.v0.1",
		"id":             "panel_verdict_record_cli",
		"created_at":     cliTestNow.Format("2006-01-02T15:04:05Z"),
		"run_id":         "prun_record_cli",
		"target": map[string]any{
			"kind":    "memory_candidate",
			"ref":     "cand_panel_record_cli",
			"summary": "candidate needs human adjudication",
		},
		"lens_findings": []map[string]any{{
			"lens_ref":           "lens:privacy",
			"route_evidence_ref": "route_panel_record",
			"status":             "failed",
		}},
		"synthesis": map[string]any{
			"schema_version": "compound.panel_synthesis.v0.1",
			"decision":       "needs_human",
			"confidence":     0.62,
			"rationale":      "privacy lens failed",
		},
		"evidence_refs": []string{"route_panel_record"},
	}, " ", "# Panel verdict\n")
	if err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		cmdPanel([]string{"verdict-record", "--review", paths.PlanPath, "--rationale", "owner reviewed panel request; keep target parked until human evidence exists", "--reviewed-by", "codex", "--json"})
	})
	var record panel.VerdictHumanReviewReport
	if err := json.Unmarshal([]byte(out), &record); err != nil {
		t.Fatalf("parse verdict-record JSON: %v\n%s", err, out)
	}
	if record.Status != "recorded" || record.Decision != "needs_human" || record.ReviewPath == "" {
		t.Fatalf("record = %+v, want recorded needs_human", record)
	}
	if record.ReviewedBy != "codex" {
		t.Fatalf("reviewed_by = %q, want codex", record.ReviewedBy)
	}

	queueOut := captureStdout(t, func() {
		cmdReviews([]string{"--area", "panel-verdicts", "--json"})
	})
	var report reviewartifact.QueueReport
	if err := json.Unmarshal([]byte(queueOut), &report); err != nil {
		t.Fatalf("parse reviews report: %v\n%s", err, queueOut)
	}
	if report.Counts.Total != 1 || report.Counts.ReviewRequired != 0 || report.Counts.OK != 1 {
		t.Fatalf("recorded panel verdict should be ok: %+v", report.Counts)
	}
	if report.Counts.ByStatus["recorded"] != 1 {
		t.Fatalf("recorded panel verdict status missing: %+v", report.Counts.ByStatus)
	}
	if len(report.Items) != 1 || report.Items[0].Status != "recorded" || report.Items[0].Readiness != "review_recorded" {
		t.Fatalf("panel verdict recorded hold should be review_recorded: %+v", report.Items)
	}
	item := report.Items[0]
	if len(item.Actions) != 0 || !strings.Contains(item.NextAction, "keep target parked") {
		t.Fatalf("recorded panel verdict should expose no-action next step: %+v", item)
	}
}

func seedPanelCLITarget(t *testing.T, homePath, id string) {
	t.Helper()
	data := readFile(t, filepath.Join("..", "..", "fixtures", "panel", "targets", id+".json"))
	var payload map[string]any
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		t.Fatalf("decode target fixture %s: %v", id, err)
	}
	if err := candidates.WriteCandidate(homePath, candidates.Memory, id, payload); err != nil {
		t.Fatalf("write candidate %s: %v", id, err)
	}
}

func TestPanelRunCLILiveGatewayEndToEnd(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	t.Setenv("FAKE_EMPTY", "")
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	captureStdout(t, func() {
		cmdRoute([]string{"import-model-capabilities", filepath.Join("..", "..", "fixtures", "panel", "model-capabilities.yaml")})
	})
	captureStdout(t, func() {
		cmdPanel([]string{"import-lenses", filepath.Join("..", "..", "fixtures", "panel", "lens-manifest.yaml")})
	})
	seedPanelCLITarget(t, homePath, "mcand_example_api_clean")

	modelCalls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("authorization = %q, want empty for FAKE_EMPTY", got)
		}
		var body struct {
			Model    string              `json:"model"`
			Messages []map[string]string `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode gateway request: %v", err)
		}
		modelCalls[body.Model]++
		user := ""
		if len(body.Messages) > 1 {
			user = body.Messages[1]["content"]
		}
		content := `{"confidence":0.8,"findings":[]}`
		if strings.Contains(user, "lens_outputs") {
			content = `{"decision":"promote","confidence":0.82,"rationale":"httptest judge","dissent":[]}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": content}}},
			"usage":   map[string]int{"total_tokens": 5},
		})
	}))
	defer server.Close()

	planOut := captureStdout(t, func() {
		cmdPanel([]string{"plan", "--target-kind", "memory_candidate", "--target", "mcand_example_api_clean", "--state", "parked", "--not-privacy-sensitive", "synthetic fixtures", "--max-usd-micros", "1", "--max-items", "1", "--json"})
	})
	var planned panel.Run
	if err := json.Unmarshal([]byte(planOut), &planned); err != nil {
		t.Fatalf("parse plan JSON: %v; output=%s", err, planOut)
	}
	runOut := captureStdout(t, func() {
		cmdPanel([]string{"run", "--plan", planned.ID, "--openai-compatible-url", server.URL, "--api-key-env", "FAKE_EMPTY", "--model-override", "panel/free-extract=served-lens", "--model-override", "panel/cheap-judge=served-judge", "--json"})
	})
	var completed panel.Run
	if err := json.Unmarshal([]byte(runOut), &completed); err != nil {
		t.Fatalf("parse run JSON: %v; output=%s", err, runOut)
	}
	if completed.Status != "verdicts_recorded" || len(completed.Findings) != 1 {
		t.Fatalf("completed run = %+v", completed)
	}
	for _, want := range []string{"override:model:panel/free-extract=served-lens", "override:model:panel/cheap-judge=served-judge", "cost_unknown"} {
		if !containsString(completed.EvidenceRefs, want) {
			t.Fatalf("evidence refs missing %q: %v", want, completed.EvidenceRefs)
		}
	}
	if modelCalls["served-lens"] != len(completed.LensSet.Included) || modelCalls["served-judge"] != 1 {
		t.Fatalf("model calls = %+v", modelCalls)
	}
	rowsBefore := panelCLIRouteEvidenceRows(t, homePath)
	callsBefore := modelCalls["served-lens"] + modelCalls["served-judge"]
	rerunOut := captureStdout(t, func() {
		cmdPanel([]string{"run", "--plan", planned.ID, "--openai-compatible-url", server.URL, "--api-key-env", "FAKE_EMPTY", "--model-override", "panel/free-extract=served-lens", "--model-override", "panel/cheap-judge=served-judge", "--json"})
	})
	var rerun panel.Run
	if err := json.Unmarshal([]byte(rerunOut), &rerun); err != nil {
		t.Fatalf("parse rerun JSON: %v; output=%s", err, rerunOut)
	}
	if rerun.Status != "verdicts_recorded" || panelCLIRouteEvidenceRows(t, homePath) != rowsBefore {
		t.Fatalf("rerun status=%s before=%d after=%d", rerun.Status, rowsBefore, panelCLIRouteEvidenceRows(t, homePath))
	}
	if callsAfter := modelCalls["served-lens"] + modelCalls["served-judge"]; callsAfter != callsBefore {
		t.Fatalf("rerun called gateway: before=%d after=%d", callsBefore, callsAfter)
	}
}

func TestPanelRunHumanOutputShowsBlockedNextAction(t *testing.T) {
	out := captureStdout(t, func() {
		printPanelRun(panel.Run{
			ID:            "prun_blocked_budget",
			Status:        "blocked",
			TargetKind:    "memory_candidate",
			StoppedReason: "budget:metered_lane_requires_max_usd_micros",
			NextAction:    "create a new panel plan with explicit --max-usd-micros plus --max-items or --max-tokens, then run only that bounded plan",
		})
	})
	for _, want := range []string{
		"- status: blocked",
		"- stopped_reason: budget:metered_lane_requires_max_usd_micros",
		"- next_action: create a new panel plan with explicit --max-usd-micros plus --max-items or --max-tokens",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("panel run output missing %q:\n%s", want, out)
		}
	}
}

func panelCLIRouteEvidenceRows(t *testing.T, homePath string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(homePath, "ledger", "execution-route-evidence.jsonl"))
	if err != nil {
		t.Fatalf("read route evidence: %v", err)
	}
	rows := 0
	for _, raw := range strings.Split(string(data), string([]byte{10})) {
		if strings.TrimSpace(raw) != "" {
			rows++
		}
	}
	return rows
}
