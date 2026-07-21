package routeevidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/candidates"
	"compound/internal/writeback"
)

func TestImportModelCapabilitiesYAMLFeedsCapabilityRoutePlan(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join("..", "..", "fixtures", "route", "model-capabilities.yaml")

	report, err := ImportModelCapabilitiesYAML(home, source, t0)
	if err != nil {
		t.Fatalf("import model capabilities: %v", err)
	}
	if report.AuthorityRef != ModelCapabilitiesAuthorityRef ||
		report.ModelCount != 5 ||
		report.CapabilityClassCount != 2 ||
		report.RoutingDefaultCount != 2 ||
		report.SourceAuthorityID == "" {
		t.Fatalf("bad import report: %+v", report)
	}
	if _, err := os.Stat(ModelCapabilitiesPath(home)); err != nil {
		t.Fatalf("authority not written: %v", err)
	}
	records, err := LoadModelCapabilitiesSourceAuthority(home)
	if err != nil {
		t.Fatalf("load source authority: %v", err)
	}
	if len(records) != 1 ||
		records[0].AuthorityStatus != "compound_primary" ||
		records[0].SourceRole != "evidence_only_input_after_import" ||
		records[0].RoutingDefaultCount != 2 {
		t.Fatalf("bad source authority records: %+v", records)
	}

	plan, err := PlanCapabilityRoute(home, "judge-critique", CapabilityRoutePlanOptions{}, t0)
	if err != nil {
		t.Fatalf("plan judge route: %v", err)
	}
	if plan.SchemaVersion != CapabilityRoutePlanSchemaVersion ||
		plan.AuthorityRef != ModelCapabilitiesAuthorityRef ||
		plan.SourceAuthorityID != report.SourceAuthorityID ||
		plan.RoutingRule != "routing_defaults[judge-critique]" ||
		len(plan.Chain) != 3 ||
		len(plan.LaneJustification) != 3 {
		t.Fatalf("bad route plan: %+v", plan)
	}
	if plan.Chain[0].ModelID != "auto/judge" ||
		plan.Chain[0].GateRole != "eligible" ||
		plan.Chain[0].CostClass != "api-marginal" ||
		plan.Chain[0].Harness != "switchback" ||
		plan.Chain[0].ProviderRoute != "auto/judge" ||
		plan.Chain[0].Resolution.Mode != "adaptive" ||
		plan.Chain[0].EffortDefault != "high" ||
		plan.Chain[1].ModelID != "deepseek/deepseek-v4-pro" ||
		plan.Chain[1].GateRole != "eligible" ||
		plan.Chain[1].ProviderRoute != "deepseek/deepseek-v4-pro" ||
		plan.Chain[1].Resolution.Mode != "direct" ||
		len(plan.Chain[1].Resolution.Targets) != 1 ||
		plan.Chain[1].Resolution.Targets[0].Provider != "deepseek" ||
		plan.Chain[2].ModelID != "scout/chat" ||
		plan.Chain[2].GateRole != "tripwire_only" ||
		plan.Chain[2].CostClass != "free" {
		t.Fatalf("bad chain: %+v", plan.Chain)
	}
	if !strings.Contains(strings.Join(plan.Warnings, "\n"), "free judge/critique lane scout/chat may object but must not gate verdicts") {
		t.Fatalf("missing free-lane warning: %+v", plan.Warnings)
	}
}

func TestImportModelCapabilitiesRejectsUnknownGateVocabulary(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "route", "model-capabilities.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		old     string
		bad     string
		wantErr string
	}{
		{name: "claim-level", old: "level: primary", bad: "level: primray", wantErr: "unknown claim level"},
		{name: "tier", old: "tier: cheap", bad: "tier: experimental", wantErr: "unknown tier"},
		{name: "resolution-mode", old: "mode: adaptive", bad: "mode: guessed", wantErr: "unknown resolution mode"},
		{name: "resolution-target", old: "provider: deepseek", bad: "provider: ''", wantErr: "requires provider and model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "source.yaml")
			data := []byte(strings.Replace(string(fixture), tc.old, tc.bad, 1))
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := ImportModelCapabilitiesYAML(home, path, t0); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("unknown gate vocabulary was imported: %v", err)
			}
			if _, err := os.Stat(ModelCapabilitiesPath(home)); !os.IsNotExist(err) {
				t.Fatalf("invalid authority was written: %v", err)
			}
		})
	}
}

func TestLegacyModelCapabilitiesWithoutResolutionStillRoutesButCannotClaimResolvedIdentity(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "legacy.yaml")
	legacy := `version: 1
purpose: legacy route authority
rules: []
capability_classes: [judge-critique]
models:
  legacy/judge:
    lane: legacy judge
    provider_route: direct
    tier: cheap
    packet_ceiling: single-target
    capabilities:
      judge-critique: {level: primary, evidence: legacy fixture}
routing_defaults:
  judge-critique: [legacy/judge]
`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportModelCapabilitiesYAML(home, path, t0); err != nil {
		t.Fatalf("legacy route authority should remain routable: %v", err)
	}
	plan, err := PlanCapabilityRoute(home, "judge-critique", CapabilityRoutePlanOptions{}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Chain) != 1 || plan.Chain[0].ModelID != "legacy/judge" || plan.Chain[0].Resolution.Mode != "" || len(plan.Chain[0].Resolution.Targets) != 0 {
		t.Fatalf("legacy lane gained invented resolution identity: %+v", plan.Chain)
	}
}

func TestPrivacySensitiveCapabilityRouteKeepsOnlyLocalBatch(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join("..", "..", "fixtures", "route", "model-capabilities.yaml")
	if _, err := ImportModelCapabilitiesYAML(home, source, t0); err != nil {
		t.Fatalf("import model capabilities: %v", err)
	}

	plan, err := PlanCapabilityRoute(home, "classify-extract", CapabilityRoutePlanOptions{PrivacySensitive: true}, t0)
	if err != nil {
		t.Fatalf("plan privacy route: %v", err)
	}
	if !plan.PrivacySensitive || plan.Status != "planned" || len(plan.Chain) != 1 || len(plan.Omitted) != 2 {
		t.Fatalf("bad privacy plan: %+v", plan)
	}
	if plan.Chain[0].ModelID != "mac/qwen-fixture" ||
		plan.Chain[0].Tier != "local-batch" ||
		!plan.Chain[0].SupervisionRequired ||
		plan.Chain[0].GateRole != "supervised_only" {
		t.Fatalf("privacy lane = %+v", plan.Chain[0])
	}
	for _, lane := range plan.Omitted {
		if lane.OmitReason != "privacy-sensitive route requires local-batch tier" {
			t.Fatalf("bad omitted lane: %+v", lane)
		}
	}
	if !strings.Contains(strings.Join(plan.Warnings, "\n"), "privacy-sensitive plan omitted non-local-batch lanes") {
		t.Fatalf("missing privacy warning: %+v", plan.Warnings)
	}
}

func TestCapabilityRoutePlanSurfacesPendingCostObservation(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join("..", "..", "fixtures", "route", "model-capabilities.yaml")
	if _, err := ImportModelCapabilitiesYAML(home, source, t0); err != nil {
		t.Fatalf("import model capabilities: %v", err)
	}

	report, err := DoctorProviderLedgerCosts(filepath.Join("..", "..", "fixtures", "route", "provider-calls-cost-gaps.ndjson"), ProviderLedgerImportOptions{})
	if err != nil {
		t.Fatalf("doctor provider costs: %v", err)
	}
	report, err = RecordCostDoctorObservation(home, report, t0)
	if err != nil {
		t.Fatalf("record cost observation: %v", err)
	}

	plan, err := PlanCapabilityRoute(home, "judge-critique", CapabilityRoutePlanOptions{}, t0)
	if err != nil {
		t.Fatalf("plan judge route: %v", err)
	}
	if len(plan.PendingObservations) != 1 {
		t.Fatalf("pending observations = %+v, want one cost observation", plan.PendingObservations)
	}
	pending := plan.PendingObservations[0]
	if pending.CandidateID != report.CandidateID ||
		pending.NoPromoteID != report.NoPromoteID ||
		pending.OwnerTargetKind != "route_cost_coverage" ||
		pending.ReviewCommand != "compound route plan --candidate "+report.CandidateID {
		t.Fatalf("bad pending observation: %+v report=%+v", pending, report)
	}
	if !strings.Contains(strings.Join(pending.MatchReasons, "\n"), "global:provider_cost_coverage") {
		t.Fatalf("missing global cost match reason: %+v", pending.MatchReasons)
	}
	if !strings.Contains(strings.Join(plan.Warnings, "\n"), "pending route observations require owner review") {
		t.Fatalf("missing pending observation warning: %+v", plan.Warnings)
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("route plan must not promote claims, stat err=%v", err)
	}
}

func TestCapabilityRoutePlanMatchesPendingObservationByModel(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join("..", "..", "fixtures", "route", "model-capabilities.yaml")
	if _, err := ImportModelCapabilitiesYAML(home, source, t0); err != nil {
		t.Fatalf("import model capabilities: %v", err)
	}
	candidate := routeCandidate{
		ID:           "route_outcome_auto_judge",
		Kind:         string(candidates.RouteObservation),
		Subject:      "route-outcome:auto/judge",
		Statement:    "auto/judge produced failed receipt; review before reuse",
		Detail:       "receipt=rec_failed route_status=completed",
		Confidence:   0.9,
		EvidenceRefs: []string{"route:auto_judge", "receipt:failed"},
		Verdict:      candidates.NoPromote,
	}
	writeRouteCandidateFixture(t, home, candidate)
	store, err := writeback.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	nop, _, err := store.RecordNoPromote(writeback.NoPromoteDecision{
		CandidateRef:  candidate.ID,
		CandidateKind: string(candidates.RouteObservation),
		OwnerTarget:   writeback.OwnerTarget{Kind: "route_outcome", Ref: "auto/judge"},
		Basis:         "route outcome requires owner review before route policy changes",
		EvidenceRefs:  candidate.EvidenceRefs,
	}, t0)
	if err != nil {
		t.Fatalf("record no-promote: %v", err)
	}

	judgePlan, err := PlanCapabilityRoute(home, "judge-critique", CapabilityRoutePlanOptions{}, t0)
	if err != nil {
		t.Fatalf("plan judge route: %v", err)
	}
	if len(judgePlan.PendingObservations) != 1 {
		t.Fatalf("judge pending observations = %+v, want route outcome", judgePlan.PendingObservations)
	}
	if got := strings.Join(judgePlan.PendingObservations[0].MatchReasons, "\n"); !strings.Contains(got, "model:auto/judge") {
		t.Fatalf("missing model match reason: %+v", judgePlan.PendingObservations[0])
	}
	if judgePlan.PendingObservations[0].NoPromoteID != nop.ID {
		t.Fatalf("no_promote id = %q, want %q", judgePlan.PendingObservations[0].NoPromoteID, nop.ID)
	}

	classifyPlan, err := PlanCapabilityRoute(home, "classify-extract", CapabilityRoutePlanOptions{}, t0)
	if err != nil {
		t.Fatalf("plan classify route: %v", err)
	}
	if len(classifyPlan.PendingObservations) != 0 {
		t.Fatalf("classify pending observations = %+v, want none", classifyPlan.PendingObservations)
	}
}

func TestRouteOutcomeObservationFeedsCapabilityRoutePlanByInferredClass(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join("..", "..", "fixtures", "route", "model-capabilities.yaml")
	if _, err := ImportModelCapabilitiesYAML(home, source, t0); err != nil {
		t.Fatalf("import model capabilities: %v", err)
	}
	ev := fixtureEvidence()
	ev.WorkID = "work_route_outcome"
	ev.SelectedTarget = Target{Provider: "auto", Model: "judge", Protocol: "openai-chat-completions"}
	rec := fixtureReceipt("failed", false)
	review, err := BuildRouteOutcomeReview(ev, rec, "", t0)
	if err != nil {
		t.Fatalf("build outcome review: %v", err)
	}
	review, err = RecordRouteOutcomeObservation(home, review, t0)
	if err != nil {
		t.Fatalf("record outcome observation: %v", err)
	}
	if got := strings.Join(review.CapabilityClasses, ","); got != "judge-critique" {
		t.Fatalf("capability classes = %q, want judge-critique", got)
	}
	plan, err := PlanCapabilityRoute(home, "judge-critique", CapabilityRoutePlanOptions{}, t0)
	if err != nil {
		t.Fatalf("plan judge route: %v", err)
	}
	if len(plan.PendingObservations) != 1 {
		t.Fatalf("pending observations = %+v, want route outcome", plan.PendingObservations)
	}
	pending := plan.PendingObservations[0]
	if pending.CandidateID != review.CandidateID || pending.NoPromoteID != review.NoPromoteID {
		t.Fatalf("pending observation = %+v review=%+v", pending, review)
	}
	if !strings.Contains(strings.Join(pending.MatchReasons, "\n"), "capability_class:judge-critique") {
		t.Fatalf("missing capability class match reason: %+v", pending.MatchReasons)
	}
	if !strings.Contains(pending.Detail, "capability_classes=judge-critique") {
		t.Fatalf("pending detail missing inferred capability class: %s", pending.Detail)
	}
	if !strings.Contains(strings.Join(plan.Warnings, "\n"), "pending route observations require owner review") {
		t.Fatalf("missing pending warning: %+v", plan.Warnings)
	}
}

func writeRouteCandidateFixture(t *testing.T, home string, candidate routeCandidate) {
	t.Helper()
	dir := filepath.Join(home, candidates.InboxDir(candidates.RouteObservation))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(candidate, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, candidate.ID+".json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestImportModelCapabilitiesLaneTupleRoundTripSurfacesSubscriptionFacts verifies
// the additive LaneTuple fields (context_window, max_output, cost_detail,
// relative_cost_hint, capacity_source, aliases) decode through YAML, surface in
// the projection, and survive a re-imported re-marshal. Falsifier for the
// additive optional-fields contract.
func TestImportModelCapabilitiesLaneTupleRoundTripSurfacesSubscriptionFacts(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join("..", "..", "fixtures", "route", "model-capabilities-tuple.yaml")

	report, err := ImportModelCapabilitiesYAML(home, source, t0)
	if err != nil {
		t.Fatalf("import tuple fixture: %v", err)
	}
	if report.AuthorityRef != ModelCapabilitiesAuthorityRef ||
		report.ModelCount != 2 ||
		report.CapabilityClassCount != 2 ||
		report.RoutingDefaultCount != 2 ||
		report.SourceAuthorityID == "" {
		t.Fatalf("bad tuple import report: %+v", report)
	}

	projection, err := ProjectModelCapabilities(home, t0)
	if err != nil {
		t.Fatalf("project tuple authorities: %v", err)
	}
	if projection.ModelCount != 2 || len(projection.Lanes) != 2 {
		t.Fatalf("projection lane count = %d, want 2", len(projection.Lanes))
	}

	var sub *ModelCapabilityProjectionLane
	var api *ModelCapabilityProjectionLane
	for i := range projection.Lanes {
		lane := &projection.Lanes[i]
		switch lane.ModelID {
		case "tuple/sub-lane-a":
			sub = lane
		case "tuple/api-lane-b":
			api = lane
		}
	}
	if sub == nil || api == nil {
		t.Fatalf("missing projected lanes: %+v", projection.Lanes)
	}

	// Subscription lane — tuple facts preserved through round-trip.
	if sub.ContextWindow != 200000 {
		t.Fatalf("sub.ContextWindow = %d, want 200000", sub.ContextWindow)
	}
	if sub.MaxOutput != 16000 {
		t.Fatalf("sub.MaxOutput = %d, want 16000", sub.MaxOutput)
	}
	if sub.CostDetail == nil {
		t.Fatalf("sub.CostDetail = nil, want subscription detail")
	}
	if sub.CostDetail.Subscription == nil {
		t.Fatalf("sub.CostDetail.Subscription = nil, want subscription block")
	}
	if sub.CostDetail.Subscription.WindowMinutes != 300 {
		t.Fatalf("subscription.window_minutes = %d, want 300", sub.CostDetail.Subscription.WindowMinutes)
	}
	if sub.CostDetail.Subscription.BusyMultiplier != 2.0 {
		t.Fatalf("subscription.busy_multiplier = %v, want 2.0", sub.CostDetail.Subscription.BusyMultiplier)
	}
	if sub.CostDetail.Subscription.PlanRef != "fixture-tier-creator" {
		t.Fatalf("subscription.plan_ref = %q, want fixture-tier-creator", sub.CostDetail.Subscription.PlanRef)
	}
	if got, want := sub.RelativeCostHint["fable_vs_opus"], 2.0; got != want {
		t.Fatalf("relative_cost_hint[fable_vs_opus] = %v, want %v", got, want)
	}
	if got, want := sub.RelativeCostHint["sub_vs_api"], 0.4; got != want {
		t.Fatalf("relative_cost_hint[sub_vs_api] = %v, want %v", got, want)
	}
	if sub.CapacitySource == nil {
		t.Fatalf("sub.CapacitySource = nil, want codexbar source ref")
	}
	if sub.CapacitySource.Kind != "codexbar" {
		t.Fatalf("capacity_source.kind = %q, want codexbar", sub.CapacitySource.Kind)
	}
	if sub.CapacitySource.FreshnessMinutes != 30 {
		t.Fatalf("capacity_source.freshness_minutes = %d, want 30", sub.CapacitySource.FreshnessMinutes)
	}
	if sub.CapacitySource.Ref != "fixture://codexbar/sub-lane-a" {
		t.Fatalf("capacity_source.ref = %q, want fixture://codexbar/sub-lane-a", sub.CapacitySource.Ref)
	}
	if len(sub.Aliases) != 2 || sub.Aliases[0] != "sub-lane-a" || sub.Aliases[1] != "sub-lane-a:worker" {
		t.Fatalf("sub.Aliases = %v, want [sub-lane-a sub-lane-a:worker]", sub.Aliases)
	}

	// API lane — per-token costs preserved.
	if api.ContextWindow != 500000 {
		t.Fatalf("api.ContextWindow = %d, want 500000", api.ContextWindow)
	}
	if api.MaxOutput != 32000 {
		t.Fatalf("api.MaxOutput = %d, want 32000", api.MaxOutput)
	}
	if api.CostDetail == nil {
		t.Fatalf("api.CostDetail = nil, want per-token detail")
	}
	if api.CostDetail.APIUSDPerMtokIn != 3.0 {
		t.Fatalf("api.cost_detail.api_usd_per_mtok_in = %v, want 3.0", api.CostDetail.APIUSDPerMtokIn)
	}
	if api.CostDetail.APIUSDPerMtokOut != 15.0 {
		t.Fatalf("api.cost_detail.api_usd_per_mtok_out = %v, want 15.0", api.CostDetail.APIUSDPerMtokOut)
	}
	if api.CostDetail.Subscription != nil {
		t.Fatalf("api.cost_detail.subscription = %+v, want nil (per-token lane)", api.CostDetail.Subscription)
	}
	if got, want := api.RelativeCostHint["api_vs_subscription"], 1.5; got != want {
		t.Fatalf("api.relative_cost_hint[api_vs_subscription] = %v, want %v", got, want)
	}
	if api.CapacitySource != nil {
		t.Fatalf("api.CapacitySource = %+v, want nil", api.CapacitySource)
	}
	if len(api.Aliases) != 0 {
		t.Fatalf("api.Aliases = %v, want empty", api.Aliases)
	}

	// Projection JSON must serialize the tuple facts so downstream readers see them.
	data, err := json.Marshal(projection)
	if err != nil {
		t.Fatalf("marshal projection: %v", err)
	}
	wire := string(data)
	wantSubstrings := []string{
		`"context_window":200000`,
		`"max_output":16000`,
		`"busy_multiplier":2`,
		`"window_minutes":300`,
		`"fable_vs_opus":2`,
		`"codexbar"`,
		`"sub-lane-a:worker"`,
		`"api_usd_per_mtok_in":3`,
		`"api_usd_per_mtok_out":15`,
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(wire, want) {
			t.Fatalf("projection JSON missing %q: %s", want, wire)
		}
	}
}

// TestModelCapabilitiesLegacyFixtureOmitsLaneTupleFields verifies zero-value
// hygiene — the existing OS-shaped fixture has no LaneTuple facts, so projection
// JSON must NOT emit empty tuple keys (omitempty proof; ensures additive fields
// do not leak `null`/`{}`/`0` for lanes that did not opt in).
func TestModelCapabilitiesLegacyFixtureOmitsLaneTupleFields(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join("..", "..", "fixtures", "route", "model-capabilities.yaml")
	if _, err := ImportModelCapabilitiesYAML(home, source, t0); err != nil {
		t.Fatalf("import legacy fixture: %v", err)
	}
	projection, err := ProjectModelCapabilities(home, t0)
	if err != nil {
		t.Fatalf("project legacy authorities: %v", err)
	}
	data, err := json.Marshal(projection)
	if err != nil {
		t.Fatalf("marshal legacy projection: %v", err)
	}
	wire := string(data)
	// No lane ever opts into tuple facts — none of these keys may appear.
	for _, banned := range []string{
		`"context_window":`,
		`"max_output":`,
		`"cost_detail":`,
		`"relative_cost_hint":`,
		`"capacity_source":`,
		`"aliases":`,
	} {
		if strings.Contains(wire, banned) {
			t.Fatalf("legacy projection leaks %q in JSON: %s", banned, wire)
		}
	}

	// Direct struct round-trip on a single legacy lane: JSON must not emit
	// empty tuple keys either (defends the tags themselves).
	for i := range projection.Lanes {
		lane := projection.Lanes[i]
		solo, err := json.Marshal(lane)
		if err != nil {
			t.Fatalf("marshal legacy lane %s: %v", lane.ModelID, err)
		}
		for _, banned := range []string{
			`"context_window":`,
			`"max_output":`,
			`"cost_detail":`,
			`"relative_cost_hint":`,
			`"capacity_source":`,
			`"aliases":`,
		} {
			if strings.Contains(string(solo), banned) {
				t.Fatalf("legacy lane %s JSON leaks %q: %s", lane.ModelID, banned, solo)
			}
		}
	}
}
