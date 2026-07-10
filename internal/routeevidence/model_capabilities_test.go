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
		len(plan.Chain) != 4 ||
		len(plan.LaneJustification) != 4 {
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
		plan.Chain[2].CostClass != "free" ||
		plan.Chain[3].ModelID != "mac/qwen-fixture" ||
		plan.Chain[3].ProviderRoute != "direct" ||
		plan.Chain[3].Resolution.Mode != "direct" ||
		plan.Chain[3].Resolution.Targets[0].Provider != "lmstudio" ||
		plan.Chain[3].GateRole != "eligible" {
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
