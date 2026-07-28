package work

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
		Snapshot:                json.RawMessage(`{"budget":{"max_requests":40},"client_harness_id":"claude-code","id":"epplan_test1234","native_effort":{"disposition":"translated","reason":"mapped through selected client","requested":"xhigh"},"profile_id":"coding-implementation","schema_version":"compound.execution_profile_plan.v0.1","transport_id":"switchback","warnings":["fixture warning"]}`),
	}
}

func testDelegateExecutionProfileResolution(plan *DelegateExecutionProfilePlan, selectedHarness, selectedLane, disposition string) *DelegateExecutionProfileResolution {
	return &DelegateExecutionProfileResolution{
		Snapshot: append(json.RawMessage(nil), plan.Snapshot...),
		Compatibility: DelegateExecutionProfileCompatibility{
			Disposition:           disposition,
			SelectedHarness:       selectedHarness,
			SelectedLaneID:        selectedLane,
			PlanID:                plan.ID,
			ClientHarnessID:       plan.ClientHarnessID,
			TransportID:           plan.TransportID,
			RequestedNativeEffort: plan.RequestedNativeEffort,
		},
	}
}

func testCompatibleDelegateExecutionProfileResolution(plan *DelegateExecutionProfilePlan) *DelegateExecutionProfileResolution {
	return testDelegateExecutionProfileResolution(plan, "claude_code", "claude_code", DelegateExecutionProfileCompatible)
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
	DelegateExecutionProfileResolver = func(DelegateExecutionProfileResolveRequest) (*DelegateExecutionProfileResolution, error) {
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

func TestDelegateExecutionProfileRejectsNonCanonicalSnapshotBeforeReadinessOrMutation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		snapshot json.RawMessage
	}{
		{name: "missing", snapshot: nil},
		{name: "invalid JSON", snapshot: json.RawMessage(`{"id":`)},
		{name: "oversized", snapshot: json.RawMessage(bytes.Repeat([]byte(" "), delegateExecutionProfileSnapshotMaxBytes+1))},
		{
			name:     "noncanonical schema whitespace",
			snapshot: json.RawMessage(`{"client_harness_id":"claude-code","id":"epplan_test1234","native_effort":{"requested":"xhigh"},"profile_id":"coding-implementation","schema_version":" compound.execution_profile_plan.v0.1 ","transport_id":"switchback"}`),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			originalProfileResolver := DelegateExecutionProfileResolver
			originalReadiness := DelegateHarnessReadiness
			originalCapacity := LaneCapacityGuard
			defer func() {
				DelegateExecutionProfileResolver = originalProfileResolver
				DelegateHarnessReadiness = originalReadiness
				LaneCapacityGuard = originalCapacity
			}()

			plan := testDelegateExecutionProfilePlan()
			plan.Snapshot = tc.snapshot
			DelegateExecutionProfileResolver = func(DelegateExecutionProfileResolveRequest) (*DelegateExecutionProfileResolution, error) {
				return testCompatibleDelegateExecutionProfileResolution(plan), nil
			}
			readinessCalls := 0
			capacityCalls := 0
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
				ExecutionProfile: DelegateExecutionProfileIntent{ProfileID: plan.ProfileID},
				Now:              fixedNow,
			}, runner)
			if err != nil {
				t.Fatalf("noncanonical snapshot returned infrastructure error: %v", err)
			}
			if result.Refusal == nil || result.Refusal.Reason != "execution_profile_unresolved" {
				t.Fatalf("unexpected refusal: %+v", result)
			}
			after, err := runattempt.Open(store.home).List()
			if err != nil {
				t.Fatal(err)
			}
			if readinessCalls != 0 || capacityCalls != 0 || len(runner.creates) != 0 || len(after) != len(before) {
				t.Fatalf("noncanonical profile crossed mutation boundary: readiness=%d capacity=%d creates=%d attempts=%d->%d",
					readinessCalls, capacityCalls, len(runner.creates), len(before), len(after))
			}
			if events := capturedDelegateEvents(t, store); len(events) != 0 {
				t.Fatalf("noncanonical profile persisted delegate events: %+v", events)
			}
		})
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
	plan.Snapshot = json.RawMessage(`{"client_harness_id":"codex","id":"epplan_test1234","native_effort":{"disposition":"translated","reason":"mapped through selected client","requested":"xhigh"},"profile_id":"coding-implementation","schema_version":"compound.execution_profile_plan.v0.1","transport_id":"switchback"}`)
	DelegateExecutionProfileResolver = func(DelegateExecutionProfileResolveRequest) (*DelegateExecutionProfileResolution, error) {
		return testDelegateExecutionProfileResolution(plan, "claude_code", "claude_code", DelegateExecutionProfileIncompatible), nil
	}
	readinessCalls := 0
	capacityCalls := 0
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
		ExecutionProfile: DelegateExecutionProfileIntent{ProfileID: plan.ProfileID},
		Now:              fixedNow,
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.Refusal == nil || result.Refusal.Reason != "execution_profile_incompatible" {
		t.Fatalf("unexpected refusal: %+v", result)
	}
	after, err := runattempt.Open(store.home).List()
	if err != nil {
		t.Fatal(err)
	}
	if readinessCalls != 0 || capacityCalls != 0 || len(runner.creates) != 0 || len(after) != len(before) {
		t.Fatalf("incompatible profile crossed activation boundary: readiness=%d capacity=%d creates=%d attempts=%d->%d",
			readinessCalls, capacityCalls, len(runner.creates), len(before), len(after))
	}
	if events := capturedDelegateEvents(t, store); len(events) != 0 {
		t.Fatalf("incompatible profile persisted delegate events: %+v", events)
	}
}

func TestDelegateExecutionProfileRejectsUnknownCodexLookalikeBeforeMutation(t *testing.T) {
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
	plan.Snapshot = json.RawMessage(`{"client_harness_id":"codex","id":"epplan_test1234","native_effort":{"requested":"xhigh"},"profile_id":"coding-implementation","schema_version":"compound.execution_profile_plan.v0.1","transport_id":"switchback"}`)
	DelegateExecutionProfileResolver = func(DelegateExecutionProfileResolveRequest) (*DelegateExecutionProfileResolution, error) {
		return testDelegateExecutionProfileResolution(plan, "codex-lookalike", "codex-lookalike", DelegateExecutionProfileIncompatible), nil
	}
	readinessCalls := 0
	capacityCalls := 0
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
		Harness:          "codex-lookalike",
		Command:          "codex",
		Workspace:        t.TempDir(),
		ExecutionProfile: DelegateExecutionProfileIntent{ProfileID: plan.ProfileID},
		Now:              fixedNow,
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.Refusal == nil || result.Refusal.Reason != "execution_profile_incompatible" {
		t.Fatalf("unknown lookalike was accepted: %+v", result)
	}
	after, err := runattempt.Open(store.home).List()
	if err != nil {
		t.Fatal(err)
	}
	if readinessCalls != 0 || capacityCalls != 0 || len(runner.creates) != 0 || len(after) != len(before) {
		t.Fatalf("lookalike profile crossed mutation boundary: readiness=%d capacity=%d creates=%d attempts=%d->%d",
			readinessCalls, capacityCalls, len(runner.creates), len(before), len(after))
	}
	if events := capturedDelegateEvents(t, store); len(events) != 0 {
		t.Fatalf("lookalike profile persisted delegate events: %+v", events)
	}
}

func TestDelegateExecutionProfileAuthorityDriftRefusesBeforeMintCreateOrPersistence(t *testing.T) {
	originalProfileResolver := DelegateExecutionProfileResolver
	originalReadiness := DelegateHarnessReadiness
	originalCapacity := LaneCapacityGuard
	defer func() {
		DelegateExecutionProfileResolver = originalProfileResolver
		DelegateHarnessReadiness = originalReadiness
		LaneCapacityGuard = originalCapacity
	}()

	admittedPlan := testDelegateExecutionProfilePlan()
	driftedPlan := cloneDelegateExecutionProfilePlan(admittedPlan)
	driftedPlan.Snapshot = json.RawMessage(`{"budget":{"max_requests":20},"client_harness_id":"claude-code","id":"epplan_test1234","native_effort":{"disposition":"translated","reason":"mapped through selected client","requested":"xhigh"},"profile_id":"coding-implementation","schema_version":"compound.execution_profile_plan.v0.1","transport_id":"switchback","warnings":["authority drift"]}`)
	resolverCalls := 0
	DelegateExecutionProfileResolver = func(DelegateExecutionProfileResolveRequest) (*DelegateExecutionProfileResolution, error) {
		resolverCalls++
		if resolverCalls == 1 {
			return testCompatibleDelegateExecutionProfileResolution(admittedPlan), nil
		}
		return testCompatibleDelegateExecutionProfileResolution(driftedPlan), nil
	}
	readinessCalls := 0
	DelegateHarnessReadiness = func(DelegateHarnessReadinessRequest) (DelegateHarnessReadinessResult, error) {
		readinessCalls++
		return DelegateHarnessReadinessResult{Ready: true}, nil
	}
	var capacityReadOnly []bool
	LaneCapacityGuard = func(req LaneCapacityGuardRequest) (LaneCapacityGuardResult, error) {
		capacityReadOnly = append(capacityReadOnly, req.ReadOnly)
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
		ExecutionProfile: DelegateExecutionProfileIntent{ProfileID: admittedPlan.ProfileID},
		Now:              fixedNow,
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.Refusal == nil || result.Refusal.Reason != "authority_changed" {
		t.Fatalf("unexpected refusal: %+v", result)
	}
	after, err := runattempt.Open(store.home).List()
	if err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 2 || readinessCalls != 2 {
		t.Fatalf("authority was not checked at admission and pre-mint: resolver=%d readiness=%d", resolverCalls, readinessCalls)
	}
	if len(capacityReadOnly) != 2 || capacityReadOnly[0] || !capacityReadOnly[1] {
		t.Fatalf("capacity checks = %v, want admission mutation check then evidence-only recheck", capacityReadOnly)
	}
	if len(runner.creates) != 0 || len(after) != len(before) {
		t.Fatalf("authority drift crossed activation boundary: creates=%d attempts=%d->%d", len(runner.creates), len(before), len(after))
	}
	if events := capturedDelegateEvents(t, store); len(events) != 0 {
		t.Fatalf("authority drift persisted delegate events: %+v", events)
	}
}

func TestDelegateExecutionProfileUnchangedSnapshotWithDTOIdentityDriftRefusesBeforeMint(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*DelegateExecutionProfilePlan)
	}{
		{name: "plan id", mutate: func(plan *DelegateExecutionProfilePlan) { plan.ID = "epplan_tampered" }},
		{name: "plan id surrounding whitespace", mutate: func(plan *DelegateExecutionProfilePlan) { plan.ID = " " + plan.ID + " " }},
		{name: "client harness", mutate: func(plan *DelegateExecutionProfilePlan) { plan.ClientHarnessID = "codex" }},
		{name: "transport", mutate: func(plan *DelegateExecutionProfilePlan) { plan.TransportID = "native" }},
		{name: "native effort", mutate: func(plan *DelegateExecutionProfilePlan) { plan.RequestedNativeEffort = "low" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			originalProfileResolver := DelegateExecutionProfileResolver
			originalReadiness := DelegateHarnessReadiness
			originalCapacity := LaneCapacityGuard
			defer func() {
				DelegateExecutionProfileResolver = originalProfileResolver
				DelegateHarnessReadiness = originalReadiness
				LaneCapacityGuard = originalCapacity
			}()

			admittedPlan := testDelegateExecutionProfilePlan()
			driftedPlan := cloneDelegateExecutionProfilePlan(admittedPlan)
			tc.mutate(driftedPlan)
			resolverCalls := 0
			DelegateExecutionProfileResolver = func(DelegateExecutionProfileResolveRequest) (*DelegateExecutionProfileResolution, error) {
				resolverCalls++
				if resolverCalls == 1 {
					return testCompatibleDelegateExecutionProfileResolution(admittedPlan), nil
				}
				return testCompatibleDelegateExecutionProfileResolution(driftedPlan), nil
			}
			DelegateHarnessReadiness = func(DelegateHarnessReadinessRequest) (DelegateHarnessReadinessResult, error) {
				return DelegateHarnessReadinessResult{Ready: true}, nil
			}
			LaneCapacityGuard = fakeGuard(false, false, "ready", "ok")

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
				ExecutionProfile: DelegateExecutionProfileIntent{ProfileID: admittedPlan.ProfileID},
				Now:              fixedNow,
			}, runner)
			if err != nil {
				t.Fatal(err)
			}
			if result.Refusal == nil || result.Refusal.Reason != "authority_changed" {
				t.Fatalf("unchanged canonical body with %s drift was accepted: %+v", tc.name, result)
			}
			after, err := runattempt.Open(store.home).List()
			if err != nil {
				t.Fatal(err)
			}
			if len(runner.creates) != 0 || len(after) != len(before) {
				t.Fatalf("DTO drift crossed activation boundary: creates=%d attempts=%d->%d", len(runner.creates), len(before), len(after))
			}
			if events := capturedDelegateEvents(t, store); len(events) != 0 {
				t.Fatalf("DTO drift persisted delegate events: %+v", events)
			}
		})
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
	DelegateExecutionProfileResolver = func(req DelegateExecutionProfileResolveRequest) (*DelegateExecutionProfileResolution, error) {
		resolverCalls++
		if req.Intent.ProfileID != plan.ProfileID ||
			req.Intent.RouteAlias != "auto/build" ||
			req.Intent.NativeEffort != "xhigh" ||
			req.SelectedHarness != "claude_code" ||
			req.SelectedLaneID != "claude_code" {
			t.Fatalf("resolver request lost selectors: %+v", req)
		}
		return testCompatibleDelegateExecutionProfileResolution(plan), nil
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
	profileProof := result.Readiness.ExecutionProfile
	if profileProof == nil ||
		profileProof.Ref != "execution-profile://"+plan.ID ||
		!bytes.Equal(profileProof.Snapshot, plan.Snapshot) ||
		strings.TrimSpace(profileProof.Revision) == "" ||
		strings.TrimSpace(result.Readiness.SnapshotRevision) == "" {
		t.Fatalf("profile absent from canonical readiness snapshot: %+v", result.Readiness)
	}

	recovered, alive, err := store.RecoverBoundSession(workID)
	if err != nil {
		t.Fatal(err)
	}
	if !alive || recovered.ExecutionProfile == nil || recovered.ExecutionProfile.ID != plan.ID {
		t.Fatalf("profile did not recover through event binding: alive=%v binding=%+v", alive, recovered)
	}
	if recovered.Readiness == nil ||
		recovered.Readiness.ExecutionProfile == nil ||
		!bytes.Equal(recovered.Readiness.ExecutionProfile.Snapshot, plan.Snapshot) {
		t.Fatalf("canonical execution profile body did not survive event recovery: %+v", recovered.Readiness)
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

func TestRecoverBoundSessionRejectsTamperedExecutionProfileProof(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*delegateEvent)
	}{
		{
			name: "profile snapshot body",
			mutate: func(event *delegateEvent) {
				event.ExecutionProfile.Snapshot = json.RawMessage(`{"client_harness_id":"claude-code","id":"epplan_tampered","native_effort":{"requested":"xhigh"},"profile_id":"coding-implementation","schema_version":"compound.execution_profile_plan.v0.1","transport_id":"switchback"}`)
			},
		},
		{
			name: "readiness body",
			mutate: func(event *delegateEvent) {
				event.Readiness.ExecutionProfile.Snapshot = json.RawMessage(`{"id":"epplan_tampered"}`)
			},
		},
		{
			name: "readiness digest",
			mutate: func(event *delegateEvent) {
				event.Readiness.ExecutionProfile.Revision = "sha256:tampered"
			},
		},
		{
			name: "readiness ref",
			mutate: func(event *delegateEvent) {
				event.Readiness.ExecutionProfile.Ref = "execution-profile://epplan_tampered"
			},
		},
		{
			name: "coherently resealed profile with stale readiness snapshot revision",
			mutate: func(event *delegateEvent) {
				const resealedID = "epplan_resealed"
				resealed := bytes.Replace(
					event.ExecutionProfile.Snapshot,
					[]byte(event.ExecutionProfile.ID),
					[]byte(resealedID),
					1,
				)
				event.ExecutionProfile.ID = resealedID
				event.ExecutionProfile.Snapshot = resealed
				event.Readiness.ExecutionProfile.Ref = "execution-profile://" + resealedID
				event.Readiness.ExecutionProfile.Snapshot = append(json.RawMessage(nil), resealed...)
				sum := sha256.Sum256(resealed)
				event.Readiness.ExecutionProfile.Revision = "sha256:" + hex.EncodeToString(sum[:])
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			originalProfileResolver := DelegateExecutionProfileResolver
			originalReadiness := DelegateHarnessReadiness
			originalCapacity := LaneCapacityGuard
			defer func() {
				DelegateExecutionProfileResolver = originalProfileResolver
				DelegateHarnessReadiness = originalReadiness
				LaneCapacityGuard = originalCapacity
			}()

			plan := testDelegateExecutionProfilePlan()
			DelegateExecutionProfileResolver = func(DelegateExecutionProfileResolveRequest) (*DelegateExecutionProfileResolution, error) {
				return testCompatibleDelegateExecutionProfileResolution(plan), nil
			}
			DelegateHarnessReadiness = func(DelegateHarnessReadinessRequest) (DelegateHarnessReadinessResult, error) {
				return DelegateHarnessReadinessResult{Ready: true}, nil
			}
			LaneCapacityGuard = fakeGuard(false, false, "ready", "ok")

			store := newStore(t)
			workID := prepareClaimedWork(t, store, "codex", "", "")
			result, err := store.Delegate(DelegateOptions{
				WorkID:           workID,
				Harness:          "claude_code",
				Command:          "claude",
				Workspace:        t.TempDir(),
				ExecutionProfile: DelegateExecutionProfileIntent{ProfileID: plan.ProfileID},
				Now:              fixedNow,
			}, newFakeRunner())
			if err != nil {
				t.Fatal(err)
			}
			var persisted delegateEvent
			if err := store.readEventJSON(result.EventID, &persisted); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&persisted)
			if err := store.events.Remove(result.EventID); err != nil {
				t.Fatal(err)
			}
			if err := store.writeEventJSON(result.EventID, persisted); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.RecoverBoundSession(workID); err == nil || !strings.Contains(err.Error(), "execution profile") {
				t.Fatalf("tampered %s recovery error = %v", tc.name, err)
			} else if strings.Contains(tc.name, "stale readiness snapshot") && !strings.Contains(err.Error(), "snapshot_revision") {
				t.Fatalf("coherently resealed profile was not rejected by the aggregate readiness revision: %v", err)
			}
		})
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
	DelegateExecutionProfileResolver = func(DelegateExecutionProfileResolveRequest) (*DelegateExecutionProfileResolution, error) {
		calls++
		return testCompatibleDelegateExecutionProfileResolution(testDelegateExecutionProfilePlan()), nil
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
