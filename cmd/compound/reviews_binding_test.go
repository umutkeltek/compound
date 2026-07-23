package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/home"
	"compound/internal/reviewartifact"
	"compound/internal/work"
)

const reviewsBindingSubprocessEnv = "COMPOUND_REVIEWS_BINDING_SUBPROCESS"
const reviewsBindingArgsEnv = "COMPOUND_REVIEWS_BINDING_ARGS"

func TestReviewsBindingSubprocess(t *testing.T) {
	if os.Getenv(reviewsBindingSubprocessEnv) == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv(reviewsBindingArgsEnv)), &args); err != nil {
		t.Fatal(err)
	}
	cmdReviews(args)
}

func TestReviewsBindingPlanThenGovernedRecordPreservesCoreReview(t *testing.T) {
	homePath := filepath.Join(t.TempDir(), ".compound")
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	paths, err := reviewartifact.Write(homePath, "panel-verdicts", "pver_binding_cli", map[string]any{
		"id": "pver_binding_cli", "status": "review_required",
	}, " ", "# panel verdict\n")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(paths.PlanPath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(work.CreateInput{
		WorkID: "work_binding_governor", Owner: "review-binding-test",
		SuccessPredicate: "binding record is governed",
	}, cliTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(work.CreateInput{
		WorkID: "work_panel_fallback", Owner: "panel-review-owner",
		CanonicalSurface: "review-area:panel-verdicts",
		SuccessPredicate: "panel review area remains actively owned",
	}, cliTestNow); err != nil {
		t.Fatal(err)
	}

	code, output := runReviewsBindingCLI(t, homePath,
		"binding-plan", "--item", "pver_binding_cli", "--area", "panel-verdicts",
		"--fallback-work", "work_panel_fallback", "--json",
	)
	if code != 0 {
		t.Fatalf("binding-plan exit=%d output=%s", code, output)
	}
	var plan reviewsBindingPlanResult
	if err := json.NewDecoder(strings.NewReader(output)).Decode(&plan); err != nil {
		t.Fatalf("parse binding plan: %v\n%s", err, output)
	}
	if plan.Binding.ReviewSHA256 == "" || plan.Recorded {
		t.Fatalf("binding plan = %+v", plan)
	}
	if matches, _ := filepath.Glob(strings.TrimSuffix(paths.PlanPath, ".json") + ".work-binding.*.json"); len(matches) != 0 {
		t.Fatalf("read-only plan wrote bindings: %v", matches)
	}

	code, output = runReviewsBindingCLI(t, homePath,
		"binding-record", "--item", "pver_binding_cli", "--area", "panel-verdicts",
		"--fallback-work", "work_panel_fallback",
		"--expected-review-sha256", plan.Binding.ReviewSHA256,
		"--governed-work", "work_binding_governor", "--actor", "operator:test", "--json",
	)
	if code != 0 {
		t.Fatalf("binding-record exit=%d output=%s", code, output)
	}
	var recorded reviewsBindingPlanResult
	if err := json.NewDecoder(strings.NewReader(output)).Decode(&recorded); err != nil {
		t.Fatalf("parse binding record: %v\n%s", err, output)
	}
	if !recorded.Recorded || recorded.ActionIntent == nil || recorded.PolicyDecision == nil ||
		recorded.PolicyAuthority == nil || !recorded.PolicyAuthority.Allowed {
		t.Fatalf("governed binding record = %+v", recorded)
	}
	after, err := os.ReadFile(paths.PlanPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("binding record rewrote core review\nbefore=%s\nafter=%s", before, after)
	}
	report, err := reviewartifact.Queue(homePath, reviewartifact.QueueOptions{Area: "panel-verdicts"}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Items) != 1 || report.Items[0].WorkBinding == nil ||
		report.Items[0].WorkBinding.FallbackWorkID != "work_panel_fallback" {
		t.Fatalf("queue did not load recorded binding: %+v", report.Items)
	}
}

func runReviewsBindingCLI(t *testing.T, homePath string, args ...string) (int, string) {
	t.Helper()
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestReviewsBindingSubprocess$")
	cmd.Env = append(os.Environ(),
		reviewsBindingSubprocessEnv+"=1",
		reviewsBindingArgsEnv+"="+string(encoded),
		"COMPOUND_HOME="+homePath,
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(output)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatal(err)
	}
	return exitErr.ExitCode(), string(output)
}
