package panel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/reviewartifact"
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
	if _, err := reviewartifact.Write(home, "panel-verdicts", verdictArtifactID("mcand_example_api_clean"), map[string]string{"id": "preexisting"}, " ", "preexisting\n"); err != nil {
		t.Fatalf("write preexisting verdict: %v", err)
	}
	run := buildRunnablePlan(t, home, []string{"mcand_example_api_clean", "mcand_example_api_dissent"}, Budget{})
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

func buildRunnablePlan(t *testing.T, home string, refs []string, budget Budget) Run {
	t.Helper()
	run, err := BuildPlan(home, PlanInput{TargetKind: "memory_candidate", TargetRefs: refs, States: []string{"parked"}, PrivacyOverrideReason: "synthetic fixtures", Budget: budget})
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
