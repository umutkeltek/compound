package autonomy_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/routeevidence"
	autonomy "compound/internal/routeevidence/autonomy"
	"compound/internal/work"
)

var proofNow = time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

type sampleOptions struct {
	routeOnly        bool
	routeWorkID      *string
	clientProfile    string
	protocol         string
	requestID        string
	selectedModel    string
	resolvedProvider *string
	resolvedModel    *string
	resolutionSource *string
	gatewayRequestID *string
}

type seededSample struct {
	WorkID         string
	ReceiptID      string
	RouteID        string
	RequestID      string
	ReceiptChanged bool
}

func TestCompileRejectsRouteOnlyReceiptAsStateDelta(t *testing.T) {
	home := t.TempDir()
	plan := installPlan(t, home, false)
	seedSample(t, home, plan, "genuine", proofNow.Add(-48*time.Hour), sampleOptions{})
	routeOnly := seedSample(t, home, plan, "route-only", proofNow.Add(-24*time.Hour), sampleOptions{routeOnly: true})
	if !routeOnly.ReceiptChanged {
		t.Fatal("fixture did not reproduce Receipt.EffectiveChanged route-only normalization")
	}

	artifact := compile(t, home, plan, 2)
	if artifact.Status != "ineligible" {
		t.Fatalf("route-only proof counted as task success: %+v", artifact)
	}
	assertReason(t, artifact, "no genuine state delta independent of route evidence")
	assertSample(t, artifact, routeOnly.WorkID, func(sample autonomy.Sample) {
		if sample.ReceiptChanged || sample.Valid {
			t.Fatalf("route-only sample must be unchanged and invalid: %+v", sample)
		}
	})
}

func TestCompileRequiresExactDecidingRouteBinding(t *testing.T) {
	tests := []struct {
		name       string
		opts       sampleOptions
		wantReason string
	}{
		{name: "empty-work-id", opts: sampleOptions{routeWorkID: stringPointer("")}, wantReason: "route evidence work_id is required"},
		{name: "wrong-work-id", opts: sampleOptions{routeWorkID: stringPointer("work_elsewhere")}, wantReason: "route evidence work_id does not match"},
		{name: "wrong-client-profile", opts: sampleOptions{clientProfile: "compound.distiller.extract"}, wantReason: "client_profile"},
		{name: "wrong-protocol", opts: sampleOptions{protocol: "openai-chat-completions"}, wantReason: "protocol"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			plan := installPlan(t, home, false)
			seedSample(t, home, plan, tc.name+"-clean", proofNow.Add(-48*time.Hour), sampleOptions{})
			seedSample(t, home, plan, tc.name+"-bad", proofNow.Add(-24*time.Hour), tc.opts)
			artifact := compile(t, home, plan, 2)
			if artifact.Status != "ineligible" {
				t.Fatalf("bad deciding route binding escaped: %+v", artifact)
			}
			assertReason(t, artifact, tc.wantReason)
		})
	}
}

func TestCompileRequiresEligibleResolvedModelIdentity(t *testing.T) {
	tests := []struct {
		name             string
		resolvedProvider string
		resolvedModel    string
		resolutionSource string
		gatewayRequestID string
		wantReason       string
	}{
		{name: "missing-provider", resolvedModel: "deepseek/deepseek-v4-pro", resolutionSource: "gateway_route", gatewayRequestID: "gateway-missing-provider", wantReason: "resolved provider is required"},
		{name: "missing-model", resolvedProvider: "deepseek", resolutionSource: "gateway_route", gatewayRequestID: "gateway-missing-model", wantReason: "resolved model is required"},
		{name: "unresolved-adaptive-alias", resolvedProvider: "switchback", resolvedModel: "auto/judge", resolutionSource: "gateway_route", gatewayRequestID: "gateway-unresolved-alias", wantReason: "adaptive route did not resolve to exactly one concrete lane"},
		{name: "adaptive-without-gateway-source", resolvedProvider: "deepseek", resolvedModel: "deepseek/deepseek-v4-pro", resolutionSource: "response_body", wantReason: "adaptive route requires gateway resolution evidence"},
		{name: "adaptive-without-gateway-request", resolvedProvider: "deepseek", resolvedModel: "deepseek/deepseek-v4-pro", resolutionSource: "gateway_route", wantReason: "adaptive route requires gateway request identity"},
		{name: "free-fallback", resolvedProvider: "scout", resolvedModel: "scout/chat", resolutionSource: "gateway_route", gatewayRequestID: "gateway-free", wantReason: "resolved model gate role is tripwire_only"},
		{name: "unknown-override", resolvedProvider: "provider", resolvedModel: "provider/unlisted-model", resolutionSource: "gateway_route", gatewayRequestID: "gateway-unknown", wantReason: "resolved target is not present in current route plan"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			plan := installPlan(t, home, false)
			seedSample(t, home, plan, tc.name+"-clean", proofNow.Add(-48*time.Hour), sampleOptions{})
			seedSample(t, home, plan, tc.name+"-bad", proofNow.Add(-24*time.Hour), sampleOptions{
				resolvedProvider: stringPointer(tc.resolvedProvider),
				resolvedModel:    stringPointer(tc.resolvedModel),
				resolutionSource: stringPointer(tc.resolutionSource),
				gatewayRequestID: stringPointer(tc.gatewayRequestID),
			})

			artifact := compile(t, home, plan, 2)
			if artifact.Status != "ineligible" {
				t.Fatalf("untrusted resolved model identity escaped: %+v", artifact)
			}
			assertReason(t, artifact, tc.wantReason)
		})
	}
}

func TestCompileBindsResolvedProviderAndModelIdentity(t *testing.T) {
	home := t.TempDir()
	plan := installPlan(t, home, false)
	seedSample(t, home, plan, "resolved-first", proofNow.Add(-48*time.Hour), sampleOptions{})
	seedSample(t, home, plan, "resolved-second", proofNow.Add(-24*time.Hour), sampleOptions{})

	artifact := compile(t, home, plan, 2)
	if artifact.Status != "eligible" {
		t.Fatalf("resolved paid judge evidence was rejected: %+v", artifact)
	}
	for _, sample := range artifact.Samples {
		if sample.RouteModelID != "auto/judge" || sample.RouteMode != "adaptive" || sample.ResolvedLaneID != "deepseek/deepseek-v4-pro" || sample.ProviderID != "deepseek" || sample.ModelID != "deepseek/deepseek-v4-pro" || sample.ResolutionSource != "gateway_route" || sample.GateRole != "eligible" {
			t.Fatalf("resolved provider/model identity was not sealed: %+v", sample)
		}
	}
}

func TestCompileAllowsConcreteDirectSelectedModelToEqualResolvedModel(t *testing.T) {
	home := t.TempDir()
	plan := installPlan(t, home, false)
	opts := sampleOptions{
		selectedModel:    "deepseek/deepseek-v4-pro",
		resolvedProvider: stringPointer("deepseek"),
		resolvedModel:    stringPointer("deepseek/deepseek-v4-pro"),
		resolutionSource: stringPointer("gateway_route"),
		gatewayRequestID: stringPointer("gateway-direct-paid"),
	}
	seedSample(t, home, plan, "direct-paid-first", proofNow.Add(-48*time.Hour), opts)
	seedSample(t, home, plan, "direct-paid-second", proofNow.Add(-24*time.Hour), opts)

	artifact := compile(t, home, plan, 2)
	if artifact.Status != "eligible" {
		t.Fatalf("direct paid selected==resolved evidence was rejected: %+v", artifact)
	}
	for _, sample := range artifact.Samples {
		if sample.RouteModelID != "deepseek/deepseek-v4-pro" || sample.RouteMode != "direct" || sample.ResolvedLaneID != "deepseek/deepseek-v4-pro" || sample.ProviderID != "deepseek" || sample.ModelID != "deepseek/deepseek-v4-pro" {
			t.Fatalf("direct paid identity was not sealed: %+v", sample)
		}
	}
}

func TestCompileAllowsEligibleLocalDirectLane(t *testing.T) {
	home := t.TempDir()
	plan := installPlan(t, home, false)
	opts := sampleOptions{
		selectedModel:    "mac/qwen-fixture",
		resolvedProvider: stringPointer("lmstudio"),
		resolvedModel:    stringPointer("qwen-fixture-served"),
		resolutionSource: stringPointer("response_body"),
		gatewayRequestID: stringPointer(""),
	}
	seedSample(t, home, plan, "local-direct-first", proofNow.Add(-48*time.Hour), opts)
	seedSample(t, home, plan, "local-direct-second", proofNow.Add(-24*time.Hour), opts)

	artifact := compile(t, home, plan, 2)
	if artifact.Status != "eligible" {
		t.Fatalf("eligible local direct evidence was rejected: %+v", artifact)
	}
	for _, sample := range artifact.Samples {
		if sample.RouteMode != "direct" || sample.ResolvedLaneID != "mac/qwen-fixture" || sample.ProviderID != "lmstudio" || sample.ModelID != "qwen-fixture-served" || sample.ResolutionSource != "response_body" || sample.GateRole != "eligible" {
			t.Fatalf("local direct identity was not sealed: %+v", sample)
		}
	}
}

func TestCompileRejectsDirectLaneResolvingToDifferentLane(t *testing.T) {
	home := t.TempDir()
	plan := installPlan(t, home, false)
	seedSample(t, home, plan, "direct-mismatch-clean", proofNow.Add(-48*time.Hour), sampleOptions{})
	seedSample(t, home, plan, "direct-mismatch-bad", proofNow.Add(-24*time.Hour), sampleOptions{
		selectedModel:    "deepseek/deepseek-v4-pro",
		resolvedProvider: stringPointer("lmstudio"),
		resolvedModel:    stringPointer("qwen-fixture-served"),
		resolutionSource: stringPointer("response_body"),
		gatewayRequestID: stringPointer(""),
	})

	artifact := compile(t, home, plan, 2)
	if artifact.Status != "ineligible" {
		t.Fatalf("direct lane resolved to a different lane: %+v", artifact)
	}
	assertReason(t, artifact, "direct route resolved outside its declared target")
}

func TestGateRoleFailsClosedForUnknownClaimLevel(t *testing.T) {
	if got := autonomy.GateRole("judge-critique", "cheap", "primray"); got == "eligible" {
		t.Fatalf("unknown claim level became authority: %q", got)
	}
}

func TestCompileRejectsReusedRouteRequestIdentity(t *testing.T) {
	home := t.TempDir()
	plan := installPlan(t, home, false)
	seedSample(t, home, plan, "clean", proofNow.Add(-48*time.Hour), sampleOptions{})
	second := seedSample(t, home, plan, "reused-request", proofNow.Add(-24*time.Hour), sampleOptions{})
	duplicateRouteRequest(t, home, second, proofNow.Add(-24*time.Hour-time.Second))

	artifact := compile(t, home, plan, 2)
	if artifact.Status != "ineligible" {
		t.Fatalf("reused route request identity escaped: %+v", artifact)
	}
	assertReason(t, artifact, "request_id is ambiguous")
}

func TestLoadAndRevalidateRejectsRouterRevisionDrift(t *testing.T) {
	home := t.TempDir()
	plan := installPlan(t, home, false)
	seedSample(t, home, plan, "first", proofNow.Add(-48*time.Hour), sampleOptions{})
	seedSample(t, home, plan, "second", proofNow.Add(-24*time.Hour), sampleOptions{})
	artifact := compile(t, home, plan, 2)
	if artifact.Status != "eligible" {
		t.Fatalf("baseline eligibility failed: %+v", artifact)
	}
	if _, err := autonomy.Record(home, artifact); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(plan.AuthorityPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n# revision drift\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := autonomy.LoadAndRevalidate(home, artifact.Ref(), "job_health", plan.ID); err == nil || !strings.Contains(err.Error(), "stale or source evidence changed") {
		t.Fatalf("router revision drift did not invalidate sealed eligibility: %v", err)
	}
}

func TestCompileFailsClosedOnUnindexedArchivedWork(t *testing.T) {
	home := t.TempDir()
	plan := installPlan(t, home, false)
	seedSample(t, home, plan, "indexed-live", proofNow.Add(-48*time.Hour), sampleOptions{})
	orphan := seedSample(t, home, plan, "archive-gap", proofNow.Add(-24*time.Hour), sampleOptions{})
	source := filepath.Join(home, "work", "work", orphan.WorkID+".json")
	target := filepath.Join(home, "archives", "operational", "files", "work", "work", orphan.WorkID+".json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, target); err != nil {
		t.Fatal(err)
	}

	if _, err := compileArtifact(home, plan, 2); err == nil || !strings.Contains(err.Error(), "unindexed work file") {
		t.Fatalf("archive move/index crash gap did not fail closed: %v", err)
	}
}

func TestCompileRejectsAssumedSupervisedOnlyDecidingLane(t *testing.T) {
	home := t.TempDir()
	plan := installPlan(t, home, true)
	seedSample(t, home, plan, "first", proofNow.Add(-48*time.Hour), sampleOptions{})
	seedSample(t, home, plan, "second", proofNow.Add(-24*time.Hour), sampleOptions{})

	artifact := compile(t, home, plan, 2)
	if artifact.Status != "ineligible" {
		t.Fatalf("assumed lane decided autonomously: %+v", artifact)
	}
	assertReason(t, artifact, "supervised_only")
}

func TestCompileUsesLatestUniqueTerminalReceipts(t *testing.T) {
	t.Run("latest-n", func(t *testing.T) {
		home := t.TempDir()
		plan := installPlan(t, home, false)
		old := seedSample(t, home, plan, "old-route-only", proofNow.Add(-72*time.Hour), sampleOptions{routeOnly: true})
		middle := seedSample(t, home, plan, "middle", proofNow.Add(-48*time.Hour), sampleOptions{})
		latest := seedSample(t, home, plan, "latest", proofNow.Add(-24*time.Hour), sampleOptions{})

		artifact := compile(t, home, plan, 2)
		if artifact.Status != "eligible" {
			t.Fatalf("an older failure displaced latest clean evidence: %+v", artifact)
		}
		if len(artifact.Samples) != 2 || artifact.Samples[0].WorkID != latest.WorkID || artifact.Samples[1].WorkID != middle.WorkID {
			t.Fatalf("samples are not latest-first: %+v", artifact.Samples)
		}
		for _, sample := range artifact.Samples {
			if sample.WorkID == old.WorkID {
				t.Fatalf("older sample entered latest-N window: %+v", artifact.Samples)
			}
		}
	})

	t.Run("duplicate-receipt-ref", func(t *testing.T) {
		home := t.TempDir()
		plan := installPlan(t, home, false)
		seedSample(t, home, plan, "first", proofNow.Add(-48*time.Hour), sampleOptions{})
		latest := seedSample(t, home, plan, "latest", proofNow.Add(-24*time.Hour), sampleOptions{})
		appendWorkEvent(t, home, map[string]any{"work_id": latest.WorkID, "to": "completed", "attached": []string{latest.ReceiptID}})

		artifact := compile(t, home, plan, 2)
		if artifact.Status != "ineligible" {
			t.Fatalf("duplicate receipt attachment escaped: %+v", artifact)
		}
		assertReason(t, artifact, "receipt refs; exactly one required")
	})

	t.Run("nonterminal-action-receipt-is-not-terminal-evidence", func(t *testing.T) {
		home := t.TempDir()
		plan := installPlan(t, home, false)
		seedSample(t, home, plan, "first", proofNow.Add(-48*time.Hour), sampleOptions{})
		latest := seedSample(t, home, plan, "latest", proofNow.Add(-24*time.Hour), sampleOptions{})
		appendWorkEvent(t, home, map[string]any{
			"ts":       proofNow.Add(-23 * time.Hour).Format(time.RFC3339),
			"work_id":  latest.WorkID,
			"from":     "running",
			"to":       "running",
			"attached": []string{"rec_intermediate_action"},
		})

		artifact := compile(t, home, plan, 2)
		if artifact.Status != "eligible" {
			t.Fatalf("nonterminal action receipt polluted terminal autonomy evidence: %+v", artifact)
		}
	})
}

func TestCompileAppliesFixedTerminalSampleLookback(t *testing.T) {
	t.Run("stale-only-history-denies", func(t *testing.T) {
		home := t.TempDir()
		plan := installPlan(t, home, false)
		seedSample(t, home, plan, "stale-first", proofNow.Add(-40*24*time.Hour), sampleOptions{})
		seedSample(t, home, plan, "stale-second", proofNow.Add(-35*24*time.Hour), sampleOptions{})

		artifact := compile(t, home, plan, 2)
		if artifact.Status != "ineligible" {
			t.Fatalf("stale-only history authorized admission: %+v", artifact)
		}
		if artifact.TerminalSampleCount != 0 || len(artifact.Samples) != 0 {
			t.Fatalf("stale samples entered the eligibility cohort: %+v", artifact.Samples)
		}
		if artifact.ExcludedStaleTerminalCount != 2 {
			t.Fatalf("excluded stale terminal count = %d, want 2", artifact.ExcludedStaleTerminalCount)
		}
		assertReason(t, artifact, "terminal samples 0 below minimum 2")
	})

	t.Run("old-samples-are-excluded", func(t *testing.T) {
		home := t.TempDir()
		plan := installPlan(t, home, false)
		stale := seedSample(t, home, plan, "stale-route-only", proofNow.Add(-31*24*time.Hour), sampleOptions{routeOnly: true})
		first := seedSample(t, home, plan, "recent-first", proofNow.Add(-48*time.Hour), sampleOptions{})
		second := seedSample(t, home, plan, "recent-second", proofNow.Add(-24*time.Hour), sampleOptions{})

		artifact := compile(t, home, plan, 2)
		if artifact.Status != "eligible" {
			t.Fatalf("stale failure affected in-cohort thresholds: %+v", artifact)
		}
		if artifact.ExcludedStaleTerminalCount != 1 {
			t.Fatalf("excluded stale terminal count = %d, want 1", artifact.ExcludedStaleTerminalCount)
		}
		if len(artifact.Samples) != 2 || artifact.Samples[0].WorkID != second.WorkID || artifact.Samples[1].WorkID != first.WorkID {
			t.Fatalf("recent cohort is not latest-first: %+v", artifact.Samples)
		}
		for _, sample := range artifact.Samples {
			if sample.WorkID == stale.WorkID {
				t.Fatalf("stale sample entered the cohort: %+v", artifact.Samples)
			}
		}
	})

	t.Run("recent-manual-supervised-history-passes-for-admission-only", func(t *testing.T) {
		home := t.TempDir()
		plan := installPlan(t, home, false)
		seedSample(t, home, plan, "manual-first", proofNow.Add(-48*time.Hour), sampleOptions{})
		seedSample(t, home, plan, "manual-second", proofNow.Add(-24*time.Hour), sampleOptions{})

		artifact := compile(t, home, plan, 2)
		if artifact.Status != "eligible" {
			t.Fatalf("recent manually supervised samples did not prove admission-only eligibility: %+v", artifact)
		}
		if artifact.CohortClass != "job_health" || artifact.LookbackDays != 30 {
			t.Fatalf("cohort metadata = class %q lookback %dd", artifact.CohortClass, artifact.LookbackDays)
		}
		wantStart := proofNow.Add(-30 * 24 * time.Hour).Format(time.RFC3339)
		if artifact.LookbackStart != wantStart || artifact.LookbackEnd != proofNow.Format(time.RFC3339) {
			t.Fatalf("lookback = %s..%s, want %s..%s", artifact.LookbackStart, artifact.LookbackEnd, wantStart, proofNow.Format(time.RFC3339))
		}
		if !containsRef(artifact.EvidenceRefs, "cohort:terminal_samples:class=job_health:lookback=30d") {
			t.Fatalf("lookback evidence ref missing from %v", artifact.EvidenceRefs)
		}
	})
}

func installPlan(t *testing.T, home string, assumed bool) autonomy.RoutePlanSnapshot {
	t.Helper()
	source := filepath.Join("..", "..", "..", "fixtures", "route", "model-capabilities.yaml")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	claimLevel := "primary"
	gateRole := "eligible"
	if assumed {
		data = []byte(strings.Replace(string(data), "judge-critique:\n        level: primary", "judge-critique:\n        level: assumed", 1))
		claimLevel = "assumed"
		gateRole = "supervised_only"
	}
	path := filepath.Join(home, "registries", "model-capabilities.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	revision := "sha256:" + hex.EncodeToString(sum[:])
	return autonomy.RoutePlanSnapshot{
		ID:              autonomy.RoutePlanID("judge-critique", false, path, revision),
		AuthorityPath:   path,
		SourceRevision:  revision,
		CapabilityClass: "judge-critique",
		Chain: []autonomy.LaneSnapshot{
			{ModelID: "auto/judge", ProviderRoute: "auto/judge", Resolution: autonomy.LaneResolution{Mode: "adaptive"}, Tier: "cheap", ClaimLevel: claimLevel, GateRole: gateRole},
			{ModelID: "deepseek/deepseek-v4-pro", ProviderRoute: "deepseek/deepseek-v4-pro", Resolution: autonomy.LaneResolution{Mode: "direct", Targets: []autonomy.ResolvedTarget{{Provider: "deepseek", Model: "deepseek/deepseek-v4-pro"}}}, Tier: "cheap", ClaimLevel: "primary", GateRole: "eligible"},
			{ModelID: "scout/chat", ProviderRoute: "scout/chat", Resolution: autonomy.LaneResolution{Mode: "direct", Targets: []autonomy.ResolvedTarget{{Provider: "scout", Model: "scout/chat"}}}, Tier: "free", ClaimLevel: "capable", GateRole: "tripwire_only"},
			{ModelID: "mac/qwen-fixture", ProviderRoute: "direct", Resolution: autonomy.LaneResolution{Mode: "direct", Targets: []autonomy.ResolvedTarget{{Provider: "lmstudio", Model: "qwen-fixture-served"}}}, Tier: "local-batch", ClaimLevel: "primary", GateRole: "eligible"},
		},
	}
}

func compile(t *testing.T, home string, plan autonomy.RoutePlanSnapshot, samples int) autonomy.Artifact {
	t.Helper()
	artifact, err := compileArtifact(home, plan, samples)
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func compileArtifact(home string, plan autonomy.RoutePlanSnapshot, samples int) (autonomy.Artifact, error) {
	return autonomy.Compile(home, autonomy.GrantSnapshot{
		DecisionID:         "job-health-autonomy-test",
		ClaimID:            "claim_job_health_autonomy_test",
		GrantRef:           "grant:job-health-autonomy-test",
		ClaimRef:           "claim:claim_job_health_autonomy_test",
		ReviewedBy:         "operator-test",
		Class:              "job_health",
		MinTerminalSamples: samples,
		MinWindowDays:      1,
		MaxFailures:        0,
	}, plan, proofNow)
}

func seedSample(t *testing.T, home string, plan autonomy.RoutePlanSnapshot, id string, closedAt time.Time, opts sampleOptions) seededSample {
	t.Helper()
	store, err := work.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	workID := "work_autonomy_" + strings.ReplaceAll(id, "-", "_")
	w, err := store.Create(work.CreateInput{
		WorkID:           workID,
		Owner:            "compound-authority",
		Actor:            "operator-test",
		SuccessPredicate: "prove autonomy sample " + id,
		EvidenceRefs: []string{
			"proposed_by:director",
			"director_trigger:job_health:" + id,
			"route_plan:" + plan.ID,
		},
		RouteDecisionID: plan.ID,
		State:           "ready",
	}, closedAt.Add(-4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(w.ID, "sample-executor", closedAt.Add(-3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{Actor: "sample-executor", To: "running"}, closedAt.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	routeWorkID := w.ID
	if opts.routeWorkID != nil {
		routeWorkID = *opts.routeWorkID
	}
	clientProfile := opts.clientProfile
	if clientProfile == "" {
		clientProfile = "compound.panel.judge"
	}
	protocol := opts.protocol
	if protocol == "" {
		protocol = "panel-lane"
	}
	requestID := opts.requestID
	if requestID == "" {
		requestID = "request-" + id
	}
	resolvedModel := "deepseek/deepseek-v4-pro"
	if opts.resolvedModel != nil {
		resolvedModel = *opts.resolvedModel
	}
	resolvedProvider := "deepseek"
	if opts.resolvedProvider != nil {
		resolvedProvider = *opts.resolvedProvider
	}
	resolutionSource := "gateway_route"
	if opts.resolutionSource != nil {
		resolutionSource = *opts.resolutionSource
	}
	gatewayRequestID := "gateway-" + id
	if opts.gatewayRequestID != nil {
		gatewayRequestID = *opts.gatewayRequestID
	}
	selectedModel := opts.selectedModel
	if selectedModel == "" {
		selectedModel = "auto/judge"
	}
	ev, err := routeevidence.Build(routeevidence.BuildInput{
		CreatedAt:     closedAt.Add(-time.Minute),
		RequestID:     requestID,
		WorkID:        routeWorkID,
		ClientProfile: clientProfile,
		Protocol:      protocol,
		Provider:      "compound-route-plan",
		Model:            selectedModel,
		ResolvedProvider: resolvedProvider,
		ResolvedModel:    resolvedModel,
		ResolutionSource: resolutionSource,
		GatewayRequestID: gatewayRequestID,
		Status:        "completed",
		EvidenceRefs:  []string{routeevidence.ModelCapabilitiesAuthorityRef},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined, err := routeevidence.RecordJoined(home, ev)
	if err != nil {
		t.Fatal(err)
	}
	proofRefs := []string{joined.Evidence.ID}
	if !opts.routeOnly {
		proofRefs = append(proofRefs, "check:state-delta:"+id)
	}
	closed, err := store.Close(w.ID, work.CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Actor:     "sample-executor",
		Summary:   "autonomy sample " + id + " changed owner state",
		Changed:   false,
		ProofRefs: proofRefs,
	}, closedAt)
	if err != nil {
		t.Fatal(err)
	}
	return seededSample{WorkID: w.ID, ReceiptID: closed.Receipt.ID, RouteID: joined.Evidence.ID, RequestID: requestID, ReceiptChanged: closed.Receipt.StateDelta.Changed}
}

func duplicateRouteRequest(t *testing.T, home string, sample seededSample, createdAt time.Time) {
	t.Helper()
	ev, err := routeevidence.Build(routeevidence.BuildInput{
		CreatedAt:     createdAt,
		RequestID:     sample.RequestID,
		WorkID:        sample.WorkID,
		ClientProfile: "compound.panel.judge",
		Protocol:      "panel-lane",
		Provider:      "compound-route-plan",
		Model:         "auto/judge",
		ResolvedProvider: "deepseek",
		ResolvedModel:    "deepseek/deepseek-v4-pro",
		ResolutionSource: "gateway_route",
		GatewayRequestID: "gateway-duplicate-" + sample.RequestID,
		Status:        "completed",
		EvidenceRefs:  []string{routeevidence.ModelCapabilitiesAuthorityRef},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := routeevidence.RecordEvidence(home, ev); err != nil {
		t.Fatal(err)
	}
}

func appendWorkEvent(t *testing.T, home string, event any) {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "events", "work.jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertReason(t *testing.T, artifact autonomy.Artifact, want string) {
	t.Helper()
	if !strings.Contains(strings.Join(artifact.Reasons, "\n"), want) {
		t.Fatalf("reason %q missing from %v", want, artifact.Reasons)
	}
}

func assertSample(t *testing.T, artifact autonomy.Artifact, workID string, check func(autonomy.Sample)) {
	t.Helper()
	for _, sample := range artifact.Samples {
		if sample.WorkID == workID {
			check(sample)
			return
		}
	}
	t.Fatalf("sample %s missing from %+v", workID, artifact.Samples)
}

func containsRef(refs []string, want string) bool {
	for _, ref := range refs {
		if ref == want {
			return true
		}
	}
	return false
}

func stringPointer(value string) *string { return &value }
