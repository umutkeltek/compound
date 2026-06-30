package routeevidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportModelCapabilitiesYAMLFeedsCapabilityRoutePlan(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join("..", "..", "fixtures", "route", "model-capabilities.yaml")

	report, err := ImportModelCapabilitiesYAML(home, source, t0)
	if err != nil {
		t.Fatalf("import model capabilities: %v", err)
	}
	if report.AuthorityRef != ModelCapabilitiesAuthorityRef ||
		report.ModelCount != 4 ||
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
		len(plan.Chain) != 2 ||
		len(plan.LaneJustification) != 2 {
		t.Fatalf("bad route plan: %+v", plan)
	}
	if plan.Chain[0].ModelID != "auto/judge" || plan.Chain[0].GateRole != "eligible" {
		t.Fatalf("first lane = %+v", plan.Chain[0])
	}
	if plan.Chain[1].ModelID != "scout/chat" || plan.Chain[1].GateRole != "tripwire_only" {
		t.Fatalf("free judge lane must stay tripwire-only: %+v", plan.Chain[1])
	}
	if !strings.Contains(strings.Join(plan.LaneJustification, "\n"), "routing_defaults[judge-critique][1] -> scout/chat") {
		t.Fatalf("missing lane justification: %+v", plan.LaneJustification)
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("model capability import/plan must not promote claims, stat err=%v", err)
	}
}

func TestCapabilityRoutePlanPrivacySensitiveKeepsOnlyLocalBatch(t *testing.T) {
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
