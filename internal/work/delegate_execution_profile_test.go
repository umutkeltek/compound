package work

import (
	"encoding/json"
	"strings"
	"testing"

	"compound/internal/runattempt"
)

func testDelegateExecutionProfilePlan() *DelegateExecutionProfilePlan {
	return &DelegateExecutionProfilePlan{
		SchemaVersion:           "compound.execution_profile_plan.v0.1",
		ID:                      "epplan_test1234",
		ProfileID:               "coding-implementation",
		Role:                    "executor",
		CapabilityClass:         "build-implement",
		ClientHarnessID:         "claude-code",
		TransportID:             "switchback",
		RouteAlias:              "auto/build",
		ModelConstraint:         "fixture/model",
		SemanticEffort:          "deep",
		RequestedNativeEffort:   "xhigh",
		NativeEffortDisposition: "translated",
		NativeEffortReason:      "mapped through selected client",
		OrchestrationPolicy:     "standard",
		PrivacyCeiling:          "private",
		Budget:                  json.RawMessage(`{"max_requests":40}`),
		Checkpoint:              json.RawMessage(`{"required":false}`),
		Warnings:                []string{"fixture warning"},
		Snapshot:                json.RawMessage(`{"id":"epplan_test1234","profile_id":"coding-implementation"}`),
	}
}

func TestDelegateExecutionProfileRefusesUnknownBeforeReadinessCapacityMintOrCreate(t *testing.T) {
	originalProfileResolver := DelegateExecutionProfileResolver
	originalReadiness := DelegateHarnessReadiness
	originalCapacity := LaneCapacityGuard
	defer func() {
		DelegateExecutionProfileResolver = originalProfileResolver
		DelegateHarnessReadiness = originalReadiness
		LaneCapacityGuard = originalCapacity
	}()

	readinessCalls := 0
	capacityCalls := 0
	DelegateExecutionProfileResolver = func(DelegateExecutionProfileResolveRequest) (*DelegateExecutionProfilePlan, error) {
		return nil, errTestExecutionProfileUnknown
	}
	DelegateHarnessReadiness = func(DelegateHarnessReadinessRequest) (DelegateHarnessReadinessResult, error) {
		readinessCalls++
		return DelegateHarnessReadinessResult{Ready: true}, nil
	}
	LaneCapacityGuard = func(LaneCapacityGuardRequest) (LaneCapacityGuardResult, error) {
		capacityCalls++
		return LaneCapacityGuardResult{Capacity: DispatchLaneCapacity{State: "ready"}}, nil
	}

	store := newStore(t)
	workID := prepareClaimedWork(t, store, "codex", "", "")
	before, err := runattempt.Open(store.home).List()
	if err != nil {
		t.Fatal(err)
	}
	runner := newFakeRunner()
	result, err := store.Delegate(DelegateOptions{
		WorkID:           workID,
		Harness:          "claude_code",
		Command:          "claude",
		Workspace:        t.TempDir(),
		ExecutionProfile: DelegateExecutionProfileIntent{ProfileID: "missing"},
		Now:              fixedNow,
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.Refusal == nil || result.Refusal.Reason != "execution_profile_unresolved" {
		t.Fatalf("unexpected refusal: %+v", result)
	}
	after, err := runattempt.Open(store.home).List()
	if err != nil {
		t.Fatal(err)
	}
	if readinessCalls != 0 || capacityCalls != 0 || len(runner.creates) != 0 || len(after) != len(before) {
		t.Fatalf("unknown profile crossed mutation boundary: readiness=%d capacity=%d creates=%d attempts=%d->%d",
			readinessCalls, capacityCalls, len(runner.creates), len(before), len(after))
	}
	if countDelegateEventKind(t, store, "work.delegate.refused") != 0 {
		t.Fatal("unknown profile persisted a refusal event")
	}
}

func TestDelegateExecutionProfileRejectsHarnessFamilyMismatchBeforeMutation(t *testing.T) {
	originalProfileResolver := DelegateExecutionProfileResolver
	originalReadiness := DelegateHarnessReadiness
	originalCapacity := LaneCapacityGuard
	defer func() {
		DelegateExecutionProfileResolver = originalProfileResolver
		DelegateHarnessReadiness = originalReadiness
		LaneCapacityGuard = originalCapacity
	}()

	plan := testDelegateExecutionProfilePlan()
	plan.ClientHarnessID = "codex"
	DelegateExecutionProfileResolver = func(DelegateExecutionProfileResolveRequest) (*DelegateExecutionProfilePlan, error) {
		return plan, nil
	}
	readinessCalls := 0
	DelegateHarnessReadiness = func(DelegateHarnessReadinessRequest) (DelegateHarnessReadinessResult, error) {
		readinessCalls++
		return DelegateHarnessReadinessResult{Ready: true}, nil
	}
	LaneCapacityGuard = fakeGuard(false, false, "ready", "ok")

	store := newStore(t)
	workID := prepareClaimedWork(t, store, "codex", "", "")
	runner := newFakeRunner()
	result, err := store.Delegate(DelegateOptions{
		WorkID:           workID,
		Harness:          "claude_code",
		Command:          "claude",
		Workspace:        t.TempDir(),
		ExecutionProfile: DelegateExecutionProfileIntent{ProfileID: plan.ProfileID},
		Now:              fixedNow,
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.Refusal == nil || result.Refusal.Reason != "execution_profile_incompatible" {
		t.Fatalf("unexpected refusal: %+v", result)
	}
	if readinessCalls != 0 || len(runner.creates) != 0 {
		t.Fatalf("incompatible profile crossed activation boundary: readiness=%d creates=%d", readinessCalls, len(runner.creates))
	}
}

func TestDelegateExecutionProfileRoundTripsBindingReadinessAndRunAttempt(t *testing.T) {
	originalProfileResolver := DelegateExecutionProfileResolver
	originalReadiness := DelegateHarnessReadiness
	originalCapacity := LaneCapacityGuard
	defer func() {
		DelegateExecutionProfileResolver = originalProfileResolver
		DelegateHarnessReadiness = originalReadiness
		LaneCapacityGuard = originalCapacity
	}()

	plan := testDelegateExecutionProfilePlan()
	resolverCalls := 0
	DelegateExecutionProfileResolver = func(req DelegateExecutionProfileResolveRequest) (*DelegateExecutionProfilePlan, error) {
		resolverCalls++
		if req.Intent.ProfileID != plan.ProfileID ||
			req.Intent.RouteAlias != "auto/build" ||
			req.Intent.NativeEffort != "xhigh" {
			t.Fatalf("resolver request lost selectors: %+v", req)
		}
		return plan, nil
	}
	DelegateHarnessReadiness = func(DelegateHarnessReadinessRequest) (DelegateHarnessReadinessResult, error) {
		return DelegateHarnessReadinessResult{
			Ready: true,
			Evidence: []DelegateHarnessReadinessEvidence{{
				Kind: "harness",
				Ref:  "harness://claude_code",
			}},
		}, nil
	}
	LaneCapacityGuard = fakeGuard(false, false, "ready", "ok")

	store := newStore(t)
	workID := prepareClaimedWork(t, store, "codex", "", "")
	runner := newFakeRunner()
	result, err := store.Delegate(DelegateOptions{
		WorkID:    workID,
		Harness:   "claude_code",
		Command:   "claude",
		Workspace: t.TempDir(),
		ExecutionProfile: DelegateExecutionProfileIntent{
			ProfileID:    plan.ProfileID,
			RouteAlias:   "auto/build",
			NativeEffort: "xhigh",
		},
		Now: fixedNow,
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "spawned" || result.ExecutionProfile == nil || result.ExecutionProfile.ID != plan.ID {
		t.Fatalf("profile missing from result: %+v", result)
	}
	if resolverCalls != 2 {
		t.Fatalf("execution profile resolved %d time(s), want admission plus pre-mint revalidation", resolverCalls)
	}
	if result.Binding == nil || result.Binding.ExecutionProfile == nil || result.Binding.ExecutionProfile.ID != plan.ID {
		t.Fatalf("profile missing from binding: %+v", result.Binding)
	}
	event, err := jsonFile(t, store, result.EventID)
	if err != nil {
		t.Fatal(err)
	}
	eventProfile, ok := event["execution_profile"].(map[string]any)
	if !ok || eventProfile["id"] != plan.ID {
		t.Fatalf("profile missing from spawned event: %+v", event)
	}
	foundProfileEvidence := false
	for _, evidence := range result.Readiness.Evidence {
		if evidence.Kind == "execution_profile" && evidence.Ref == "execution-profile://"+plan.ID {
			foundProfileEvidence = true
		}
	}
	if !foundProfileEvidence || strings.TrimSpace(result.Readiness.SnapshotRevision) == "" {
		t.Fatalf("profile absent from canonical readiness snapshot: %+v", result.Readiness)
	}

	recovered, alive, err := store.RecoverBoundSession(workID)
	if err != nil {
		t.Fatal(err)
	}
	if !alive || recovered.ExecutionProfile == nil || recovered.ExecutionProfile.ID != plan.ID {
		t.Fatalf("profile did not recover through event binding: alive=%v binding=%+v", alive, recovered)
	}

	attempt, err := runattempt.Open(store.home).Get(result.RunAttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.ExecutionProfilePlanID != plan.ID ||
		attempt.RequestedClientHarnessID != plan.ClientHarnessID ||
		attempt.RequestedTransportID != plan.TransportID ||
		attempt.RequestedNativeEffort != plan.RequestedNativeEffort {
		t.Fatalf("RunAttempt lost requested profile identity: %+v", attempt)
	}
	if attempt.ModelID != "" || attempt.ProviderRoute != "" {
		t.Fatalf("intent stamped observed fields: %+v", attempt)
	}
}

func TestDelegateWithoutExecutionProfilePreservesLegacyBehavior(t *testing.T) {
	originalProfileResolver := DelegateExecutionProfileResolver
	originalReadiness := DelegateHarnessReadiness
	originalCapacity := LaneCapacityGuard
	defer func() {
		DelegateExecutionProfileResolver = originalProfileResolver
		DelegateHarnessReadiness = originalReadiness
		LaneCapacityGuard = originalCapacity
	}()
	calls := 0
	DelegateExecutionProfileResolver = func(DelegateExecutionProfileResolveRequest) (*DelegateExecutionProfilePlan, error) {
		calls++
		return testDelegateExecutionProfilePlan(), nil
	}
	DelegateHarnessReadiness = func(DelegateHarnessReadinessRequest) (DelegateHarnessReadinessResult, error) {
		return DelegateHarnessReadinessResult{Ready: true}, nil
	}
	LaneCapacityGuard = fakeGuard(false, false, "ready", "ok")

	store := newStore(t)
	workID := prepareClaimedWork(t, store, "codex", "", "")
	result, err := store.Delegate(DelegateOptions{
		WorkID:    workID,
		Harness:   "claude_code",
		Command:   "claude",
		Workspace: t.TempDir(),
		Now:       fixedNow,
	}, newFakeRunner())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "spawned" || result.ExecutionProfile != nil || calls != 0 {
		t.Fatalf("legacy delegate changed: calls=%d result=%+v", calls, result)
	}
}

var errTestExecutionProfileUnknown = &testExecutionProfileError{"unknown profile"}

type testExecutionProfileError struct{ message string }

func (e *testExecutionProfileError) Error() string { return e.message }
