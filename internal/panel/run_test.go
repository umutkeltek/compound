package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/reviewartifact"
	"compound/internal/routeevidence"
)

type failingLensCaller struct {
	base   FixtureCaller
	lensID string
}

func (c failingLensCaller) Call(ctx context.Context, req CallRequest) (CallResult, error) {
	if req.LensID == c.lensID {
		return CallResult{}, fmt.Errorf("fixture lens failure for %s", req.LensID)
	}
	return c.base.Call(ctx, req)
}

type failingJudgeCaller struct {
	base FixtureCaller
}

func (c failingJudgeCaller) Call(ctx context.Context, req CallRequest) (CallResult, error) {
	if req.LensID == judgePromptID {
		return CallResult{}, fmt.Errorf("fixture judge failure for %s", req.LensID)
	}
	return c.base.Call(ctx, req)
}

type preflightBlockingCaller struct {
	err error
}

func (c preflightBlockingCaller) Preflight(ctx context.Context, run Run) error {
	return c.err
}

func (c preflightBlockingCaller) Call(ctx context.Context, req CallRequest) (CallResult, error) {
	return CallResult{}, fmt.Errorf("call should not run after preflight block")
}

func TestDecodeLensResponseStripsMarkdownFence(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "full fence",
			body: "```json\n{\"confidence\":0.8,\"findings\":[{\"claim\":\"ok\",\"severity\":\"low\",\"evidence_refs\":[\"trace://one\"]}]}\n```",
		},
		{
			name: "trailing closing fence",
			body: "{\"confidence\":0.8,\"findings\":[{\"claim\":\"ok\",\"severity\":\"low\",\"evidence_refs\":[\"trace://one\"]}]}\n```",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeLensResponse([]byte(tt.body))
			if err != nil {
				t.Fatalf("decode fenced lens response: %v", err)
			}
			if got.Confidence != 0.8 || len(got.Findings) != 1 || got.Findings[0].Claim != "ok" {
				t.Fatalf("decoded lens response = %+v", got)
			}
		})
	}
}

func TestExecuteDryRunEndToEndWritesArtifactsEvidenceAndDoesNotMutateInbox(t *testing.T) {
	home := setupPanelPlanHome(t)
	writePanelFixtureTarget(t, home, "mcand_example_api_clean")
	writePanelFixtureTarget(t, home, "mcand_example_api_dissent")
	before := readTestFile(t, filepath.Join(home, "inbox", "memory-candidates", "mcand_example_api_clean.json"))
	run := buildRunnablePlan(t, home, []string{"mcand_example_api_clean", "mcand_example_api_dissent"}, Budget{})

	got, err := ExecuteDryRun(context.Background(), home, run, FixtureCaller{Dir: filepath.Join("..", "..", "fixtures", "panel")}, fixedPanelNow)
	if err != nil {
		t.Fatalf("execute dry run: %v", err)
	}
	if got.Status != "verdicts_recorded" || len(got.Findings) != 2 {
		t.Fatalf("run result = %+v", got)
	}
	for _, finding := range got.Findings {
		if finding.Synthesis.SchemaVersion != SynthesisSchemaVersion || finding.Synthesis.JudgeRouteEvidenceRef == "" {
			t.Fatalf("synthesis not populated: %+v", finding.Synthesis)
		}
		if _, err := os.Stat(finding.VerdictArtifact); err != nil {
			t.Fatalf("missing verdict json %s: %v", finding.VerdictArtifact, err)
		}
		mdPath := strings.TrimSuffix(finding.VerdictArtifact, ".json") + ".md"
		if _, err := os.Stat(mdPath); err != nil {
			t.Fatalf("missing verdict markdown %s: %v", mdPath, err)
		}
	}
	if rows := routeEvidenceRows(t, home); rows == 0 {
		t.Fatalf("route evidence ledger empty")
	}
	after := readTestFile(t, filepath.Join(home, "inbox", "memory-candidates", "mcand_example_api_clean.json"))
	if string(before) != string(after) {
		t.Fatalf("memory candidate changed during dry run")
	}
	store, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadRun(got.ID)
	if err != nil {
		t.Fatalf("load saved run: %v", err)
	}
	if loaded.Status != "verdicts_recorded" || len(loaded.Findings) != 2 {
		t.Fatalf("saved run = %+v", loaded)
	}
}

func TestExecuteDryRunFailSoftLens(t *testing.T) {
	home := setupPanelPlanHome(t)
	writePanelFixtureTarget(t, home, "mcand_example_api_clean")
	run := buildRunnablePlan(t, home, []string{"mcand_example_api_clean"}, Budget{})
	got, err := ExecuteDryRun(context.Background(), home, run, failingLensCaller{base: FixtureCaller{Dir: filepath.Join("..", "..", "fixtures", "panel")}, lensID: "lens:duplicate-risk"}, fixedPanelNow)
	if err != nil {
		t.Fatalf("execute dry run: %v", err)
	}
	if got.Status != "verdicts_recorded" || len(got.Findings) != 1 {
		t.Fatalf("run result = %+v", got)
	}
	foundFailed := false
	for _, lens := range got.Findings[0].LensFindings {
		if lens.LensRef == "lens:duplicate-risk" && lens.Status == "failed" && strings.Contains(lens.Error, "fixture lens failure") {
			foundFailed = true
		}
	}
	if !foundFailed {
		t.Fatalf("failed lens finding not recorded: %+v", got.Findings[0].LensFindings)
	}
}

func TestExecuteDryRunTerminalizesJudgeFailure(t *testing.T) {
	home := setupPanelPlanHome(t)
	writePanelFixtureTarget(t, home, "mcand_example_api_clean")
	run := buildRunnablePlan(t, home, []string{"mcand_example_api_clean"}, Budget{})
	got, err := ExecuteDryRun(context.Background(), home, run, failingJudgeCaller{base: FixtureCaller{Dir: filepath.Join("..", "..", "fixtures", "panel")}}, fixedPanelNow)
	if err == nil {
		t.Fatalf("execute dry run succeeded; want judge failure")
	}
	if got.Status != "failed" || !strings.Contains(got.StoppedReason, "target mcand_example_api_clean") || !strings.Contains(got.StoppedReason, "fixture judge failure") {
		t.Fatalf("failed run state = status %q reason %q", got.Status, got.StoppedReason)
	}
	store, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadRun(run.ID)
	if err != nil {
		t.Fatalf("load saved run: %v", err)
	}
	if loaded.Status != got.Status || loaded.StoppedReason != got.StoppedReason {
		t.Fatalf("saved failed run = status %q reason %q, want status %q reason %q", loaded.Status, loaded.StoppedReason, got.Status, got.StoppedReason)
	}
	if len(got.Findings) != 0 {
		t.Fatalf("judge failure must not record incomplete target finding: %+v", got.Findings)
	}
	if rows := routeEvidenceRows(t, home); rows == 0 {
		t.Fatalf("judge failure should still leave route evidence")
	}
}

func TestExecuteDryRunMalformedJudgeNeedsHuman(t *testing.T) {
	home := setupPanelPlanHome(t)
	writePanelFixtureTarget(t, home, "mcand_example_api_malformed")
	run := buildRunnablePlan(t, home, []string{"mcand_example_api_malformed"}, Budget{})
	got, err := ExecuteDryRun(context.Background(), home, run, FixtureCaller{Dir: filepath.Join("..", "..", "fixtures", "panel")}, fixedPanelNow)
	if err != nil {
		t.Fatalf("execute dry run: %v", err)
	}
	synthesis := got.Findings[0].Synthesis
	if synthesis.Decision != "needs_human" || !containsString(synthesis.GuardrailsApplied, "judge_json_malformed") {
		t.Fatalf("synthesis = %+v, want needs_human with malformed guardrail", synthesis)
	}
	if rows := routeEvidenceRows(t, home); rows < 8 {
		t.Fatalf("route evidence rows = %d, want lens calls plus judge retry", rows)
	}
}

func TestExecuteDryRunBudgetStop(t *testing.T) {
	home := setupPanelPlanHome(t)
	writePanelFixtureTarget(t, home, "mcand_example_api_clean")
	writePanelFixtureTarget(t, home, "mcand_example_api_dissent")
	run := buildRunnablePlan(t, home, []string{"mcand_example_api_clean", "mcand_example_api_dissent"}, Budget{MaxItems: 1})
	got, err := ExecuteDryRun(context.Background(), home, run, FixtureCaller{Dir: filepath.Join("..", "..", "fixtures", "panel")}, fixedPanelNow)
	if err != nil {
		t.Fatalf("execute dry run: %v", err)
	}
	if got.Status != "stopped_budget" || got.StoppedReason != "budget:max_items" || len(got.Findings) != 1 {
		t.Fatalf("budget run = %+v", got)
	}
}

func TestExecuteDryRunResumeSkipsExistingVerdictArtifact(t *testing.T) {
	home := setupPanelPlanHome(t)
	writePanelFixtureTarget(t, home, "mcand_example_api_clean")
	writePanelFixtureTarget(t, home, "mcand_example_api_dissent")
	run := buildRunnablePlan(t, home, []string{"mcand_example_api_clean", "mcand_example_api_dissent"}, Budget{})
	if _, err := reviewartifact.Write(home, "panel-verdicts", verdictArtifactID(run.ID, "mcand_example_api_clean"), map[string]string{"id": "preexisting"}, " ", "preexisting\n"); err != nil {
		t.Fatalf("write preexisting verdict: %v", err)
	}
	got, err := ExecuteDryRun(context.Background(), home, run, FixtureCaller{Dir: filepath.Join("..", "..", "fixtures", "panel")}, fixedPanelNow)
	if err != nil {
		t.Fatalf("execute dry run: %v", err)
	}
	if got.Status != "verdicts_recorded" || len(got.Findings) != 2 {
		t.Fatalf("resume run = %+v", got)
	}
	if got.Findings[0].TargetRef != "mcand_example_api_clean" || len(got.Findings[0].LensFindings) == 0 || got.Findings[0].LensFindings[0].Status != "skipped" {
		t.Fatalf("first finding not skipped: %+v", got.Findings[0])
	}
}

func TestExecuteDryRunReJudgesTargetFromDifferentRun(t *testing.T) {
	home := setupPanelPlanHome(t)
	writePanelFixtureTarget(t, home, "mcand_example_api_clean")
	run := buildRunnablePlan(t, home, []string{"mcand_example_api_clean"}, Budget{})
	if _, err := reviewartifact.Write(home, "panel-verdicts", verdictArtifactID("prun_other_run", "mcand_example_api_clean"), map[string]string{"id": "stale"}, " ", "stale\n"); err != nil {
		t.Fatalf("write stale verdict: %v", err)
	}
	got, err := ExecuteDryRun(context.Background(), home, run, FixtureCaller{Dir: filepath.Join("..", "..", "fixtures", "panel")}, fixedPanelNow)
	if err != nil {
		t.Fatalf("execute dry run: %v", err)
	}
	if len(got.Findings) != 1 || got.Findings[0].Synthesis.Decision == "" || got.Findings[0].LensFindings[0].Status == "skipped" {
		t.Fatalf("target was not re-judged despite artifact from another run: %+v", got.Findings)
	}
}

func TestExecuteDryRunSelfJudgmentGuardrail(t *testing.T) {
	home := setupPanelPlanHome(t)
	writePanelFixtureTarget(t, home, "mcand_example_api_clean")
	run := buildRunnablePlan(t, home, []string{"mcand_example_api_clean"}, Budget{})
	run.Route.JudgeLane.ModelID = run.Route.LensLane.ModelID
	got, err := ExecuteDryRun(context.Background(), home, run, FixtureCaller{Dir: filepath.Join("..", "..", "fixtures", "panel")}, fixedPanelNow)
	if err != nil {
		t.Fatalf("execute dry run: %v", err)
	}
	syn := got.Findings[0].Synthesis
	if syn.Decision == "promote" {
		t.Fatalf("self-judgment must not promote: %+v", syn)
	}
	found := false
	for _, g := range syn.GuardrailsApplied {
		if g == "self_judgment" {
			found = true
		}
	}
	if !found {
		t.Fatalf("self_judgment guardrail missing: %+v", syn.GuardrailsApplied)
	}
}

func TestTotalRunTokensIncludesJudgeUsage(t *testing.T) {
	run := Run{Findings: []TargetFinding{{
		LensFindings: []LensFinding{{Usage: Usage{TotalTokens: 10}}, {Usage: Usage{TotalTokens: 5}}},
		Synthesis:    Synthesis{JudgeUsage: Usage{TotalTokens: 7}},
	}}}
	if got := totalRunTokens(run); got != 22 {
		t.Fatalf("totalRunTokens = %d, want 22 (judge usage must count)", got)
	}
}

func buildRunnablePlan(t *testing.T, home string, refs []string, budget Budget) Run {
	t.Helper()
	run, err := BuildPlan(home, PlanInput{TargetKind: "memory_candidate", TargetRefs: refs, States: []string{"parked"}, PrivacyOverrideReason: "synthetic fixtures", Budget: budget}, fixedPanelNow)
	if err != nil {
		t.Fatalf("build runnable plan: %v", err)
	}
	return run
}

func fixedPanelNow() time.Time { return panelTestNow }

func routeEvidenceRows(t *testing.T, home string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "ledger", "execution-route-evidence.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "\n") + 1
}

func TestRecordPanelRouteEvidenceIncludesGatewayMetadata(t *testing.T) {
	home := t.TempDir()
	_, err := recordPanelRouteEvidence(home, fixedPanelNow(), "work_1", "panel:req_1", "compound.panel.judge", LaneRef{ModelID: "auto/judge", Tier: "cheap"}, CallResult{
		TotalTokens:      7,
		LatencyMS:        12,
		ResolvedProvider: "nvidia",
		ResolvedModel:    "nvidia/minimaxai/minimax-m3",
		ResolutionSource: "gateway_route",
		GatewayRequestID: "req_switchback_1",
	}, "completed")
	if err != nil {
		t.Fatalf("record route evidence: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, "ledger", "execution-route-evidence.jsonl"))
	if err != nil {
		t.Fatalf("read route evidence ledger: %v", err)
	}
	var row routeevidence.Evidence
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &row); err != nil {
		t.Fatalf("parse route evidence row: %v", err)
	}
	if row.SelectedTarget.Model != "auto/judge" {
		t.Fatalf("selected target model = %q, want requested alias", row.SelectedTarget.Model)
	}
	if row.ResolvedProvider != "nvidia" || row.ResolvedModel != "nvidia/minimaxai/minimax-m3" || row.ResolutionSource != "gateway_route" || row.GatewayRequestID != "req_switchback_1" {
		t.Fatalf("gateway metadata = provider:%q resolved:%q source:%q request:%q", row.ResolvedProvider, row.ResolvedModel, row.ResolutionSource, row.GatewayRequestID)
	}
}

func TestExecuteDryRunBlocksAndPersistsPreflightFailure(t *testing.T) {
	home := setupPanelPlanHome(t)
	writePanelFixtureTarget(t, home, "mcand_example_api_clean")
	run := buildRunnablePlan(t, home, []string{"mcand_example_api_clean"}, Budget{})
	got, err := ExecuteDryRun(context.Background(), home, run, preflightBlockingCaller{err: fmt.Errorf("lm studio context preflight: model %q loaded_context_length=8192 below max_context_length=262144", "ornith-1.0-35b")}, fixedPanelNow)
	if err == nil {
		t.Fatal("expected preflight error")
	}
	if got.Status != "blocked" || !strings.Contains(got.StoppedReason, "loaded_context_length=8192") || !strings.Contains(got.NextAction, "lms ps") || len(got.Findings) != 0 {
		t.Fatalf("blocked run = %+v", got)
	}
	store, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadRun(run.ID)
	if err != nil {
		t.Fatalf("load saved run: %v", err)
	}
	if loaded.Status != got.Status || loaded.StoppedReason != got.StoppedReason || loaded.NextAction != got.NextAction {
		t.Fatalf("saved run = %+v, want %+v", loaded, got)
	}
	if rows := routeEvidenceRows(t, home); rows != 0 {
		t.Fatalf("route evidence rows = %d, want no model call before preflight passes", rows)
	}
}

func TestExecuteDryRunRecordsCostUnknownForUSDCapWithoutCostMetadata(t *testing.T) {
	home := setupPanelPlanHome(t)
	writePanelFixtureTarget(t, home, "mcand_example_api_clean")
	unbounded := buildRunnablePlan(t, home, []string{"mcand_example_api_clean"}, Budget{MaxUSDMicros: 1})
	if _, err := ExecuteDryRun(context.Background(), home, unbounded, FixtureCaller{Dir: filepath.Join("..", "..", "fixtures", "panel")}, fixedPanelNow); err == nil {
		t.Fatal("usd-only budget must fail closed without cost data")
	}

	run := buildRunnablePlan(t, home, []string{"mcand_example_api_clean"}, Budget{MaxUSDMicros: 1, MaxItems: 1})
	got, err := ExecuteDryRun(context.Background(), home, run, FixtureCaller{Dir: filepath.Join("..", "..", "fixtures", "panel")}, fixedPanelNow)
	if err != nil {
		t.Fatalf("execute dry run: %v", err)
	}
	if !containsString(got.EvidenceRefs, "cost_unknown") {
		t.Fatalf("run = %+v, want cost_unknown evidence when usd cap is bounded by items", got)
	}
}

func TestExecuteDryRunBlocksGatewayMeteredLaneWithoutUSDBudget(t *testing.T) {
	home := setupPanelPlanHome(t)
	writePanelFixtureTarget(t, home, "mcand_example_api_clean")
	run := buildRunnablePlan(t, home, []string{"mcand_example_api_clean"}, Budget{MaxItems: 1})
	got, err := ExecuteDryRun(context.Background(), home, run, GatewayCaller{BaseURL: "http://127.0.0.1:1/v1"}, fixedPanelNow)
	if err == nil {
		t.Fatalf("execute dry run succeeded; want metered budget block")
	}
	if got.Status != "blocked" || got.StoppedReason != "budget:metered_lane_requires_max_usd_micros" {
		t.Fatalf("blocked run = status %q reason %q", got.Status, got.StoppedReason)
	}
	if !strings.Contains(got.NextAction, "--max-usd-micros") || !strings.Contains(got.NextAction, "--max-items or --max-tokens") {
		t.Fatalf("blocked run next_action = %q", got.NextAction)
	}
	store, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadRun(run.ID)
	if err != nil {
		t.Fatalf("load saved run: %v", err)
	}
	if loaded.Status != got.Status || loaded.StoppedReason != got.StoppedReason || loaded.NextAction != got.NextAction {
		t.Fatalf("saved blocked run = status %q reason %q next %q, want status %q reason %q next %q", loaded.Status, loaded.StoppedReason, loaded.NextAction, got.Status, got.StoppedReason, got.NextAction)
	}
	if rows := routeEvidenceRows(t, home); rows != 0 {
		t.Fatalf("metered budget block should happen before route calls; route evidence rows = %d", rows)
	}
}

func TestExecuteDryRunSkipsDuplicateRouteEvidenceOnResume(t *testing.T) {
	home := setupPanelPlanHome(t)
	writePanelFixtureTarget(t, home, "mcand_example_api_clean")
	run := buildRunnablePlan(t, home, []string{"mcand_example_api_clean"}, Budget{})
	lensRef := run.LensSet.Included[0].Ref
	requestID := "panel:" + run.ID + ":" + targetDigest("mcand_example_api_clean") + ":" + lensRef
	ev, err := routeevidence.Build(routeevidence.BuildInput{
		CreatedAt:     fixedPanelNow(),
		RequestID:     requestID,
		WorkID:        run.WorkID,
		ClientProfile: "compound.panel.lens",
		Protocol:      "panel-lane",
		Provider:      "compound-route-plan",
		Model:         run.Route.LensLane.ModelID,
		Usage:         routeevidence.Usage{TotalTokens: 7},
		Status:        "completed",
		EvidenceRefs:  []string{routeevidence.ModelCapabilitiesAuthorityRef},
	})
	if err != nil {
		t.Fatalf("build route evidence: %v", err)
	}
	if _, err := routeevidence.RecordJoined(home, ev); err != nil {
		t.Fatalf("preseed route evidence: %v", err)
	}

	got, err := ExecuteDryRun(context.Background(), home, run, FixtureCaller{Dir: filepath.Join("..", "..", "fixtures", "panel")}, fixedPanelNow)
	if err != nil {
		t.Fatalf("execute dry run: %v", err)
	}
	if got.Status != "verdicts_recorded" {
		t.Fatalf("run status = %s", got.Status)
	}
	if rows := routeEvidenceRequestRows(t, home, requestID); rows != 1 {
		t.Fatalf("request_id rows = %d, want 1", rows)
	}
	if got.Findings[0].LensFindings[0].RouteEvidenceRef != ev.ID {
		t.Fatalf("route evidence ref = %q, want preseeded %q", got.Findings[0].LensFindings[0].RouteEvidenceRef, ev.ID)
	}
}

func routeEvidenceRequestRows(t *testing.T, home, requestID string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "ledger", "execution-route-evidence.jsonl"))
	if err != nil {
		t.Fatalf("read route evidence ledger: %v", err)
	}
	rows := 0
	for i, raw := range strings.Split(string(data), string([]byte{10})) {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		var row struct {
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("parse route evidence line %d: %v", i+1, err)
		}
		if row.RequestID == requestID {
			rows++
		}
	}
	return rows
}
