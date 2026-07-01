package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportStandingGrantsWritesAuthorityAndPolicyConsumes(t *testing.T) {
	homePath := t.TempDir()
	sourcePath := filepath.Join(t.TempDir(), "standing-grants.yaml")
	raw := []byte(`generated_at: "2026-07-01T12:00:00Z"
grants:
  - grant_id: grant_review_finding
    capabilities: ["review.finding", "review.summary"]
    target_scope: "state/runs/*"
    reversibility_ceiling: reversible
`)
	if err := os.WriteFile(sourcePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := ImportStandingGrants(homePath, sourcePath, fixedNow)
	if err != nil {
		t.Fatalf("import standing grants: %v", err)
	}
	if report.AuthorityRef != StandingGrantsAuthorityRef || report.GrantCount != 2 || report.CapabilityCount != 2 {
		t.Fatalf("bad import report: %+v", report)
	}
	if _, err := os.Stat(StandingGrantsPath(homePath)); err != nil {
		t.Fatalf("standing grants authority missing: %v", err)
	}
	registry, err := LoadStandingGrantsFromHome(homePath)
	if err != nil {
		t.Fatalf("load standing grants: %v", err)
	}
	if len(registry.Grants) != 2 || registry.Grants[0].ReversibilityCeiling != "reversible" {
		t.Fatalf("bad normalized grants: %+v", registry.Grants)
	}
	records, err := LoadStandingGrantsSourceAuthority(homePath)
	if err != nil {
		t.Fatalf("load source authority: %v", err)
	}
	if len(records) != 1 || records[0].AuthorityStatus != "compound_primary" || records[0].Retires != "config_only_authority:policy.yaml:standing_grants" {
		t.Fatalf("bad source authority records: %+v", records)
	}

	cfg, source, err := LoadConfig(homePath)
	if err != nil {
		t.Fatalf("load policy config: %v", err)
	}
	if source != "built-in" {
		t.Fatalf("expected built-in config plus standing-grants overlay, got %q", source)
	}
	if len(cfg.StandingGrants) != 2 || !contains(cfg.PolicyEvidenceRefs, StandingGrantsAuthorityRef) {
		t.Fatalf("standing grants overlay missing: grants=%+v evidence=%v", cfg.StandingGrants, cfg.PolicyEvidenceRefs)
	}
	action := mkAction("local_reversible", "easy", "state/runs/demo/finding.md", "repo")
	action.Capability = "review.finding"
	pd := Evaluate(action, cfg, fixedNow)
	if pd.Outcome != "allow" || pd.StandingGrantID != "grant_review_finding:review-finding" {
		t.Fatalf("expected imported grant allow, got outcome=%q grant=%q reasons=%v", pd.Outcome, pd.StandingGrantID, pd.Reasons)
	}
	if !contains(pd.EvidenceRefs, StandingGrantsAuthorityRef) {
		t.Fatalf("policy decision missing standing-grants evidence: %v", pd.EvidenceRefs)
	}
}

func TestStandingGrantReversibilityCeilingDoesNotOpenIrreversible(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StandingGrants = []StandingGrant{{
		ID:                   "grant_repo_edit_reversible",
		Capability:           "mechanical-edit",
		TargetPath:           "repo/*",
		ReversibilityCeiling: "reversible",
	}}
	allowed := mkAction("local_reversible", "easy", "repo/file.go", "repo")
	allowed.Capability = "mechanical-edit"
	pd := Evaluate(allowed, cfg, fixedNow)
	if pd.Outcome != "allow" || pd.StandingGrantID != "grant_repo_edit_reversible" {
		t.Fatalf("reversible action should be covered, got outcome=%q grant=%q reasons=%v", pd.Outcome, pd.StandingGrantID, pd.Reasons)
	}

	blocked := mkAction("local_reversible", "irreversible", "repo/file.go", "repo")
	blocked.Capability = "mechanical-edit"
	pd = Evaluate(blocked, cfg, fixedNow)
	if pd.Outcome != "hold" {
		t.Fatalf("irreversible action should fall through baseline hold, got outcome=%q grant=%q reasons=%v", pd.Outcome, pd.StandingGrantID, pd.Reasons)
	}
	if !reasonContains(pd.Reasons, `standing grant "grant_repo_edit_reversible" covers reversibility up to "reversible", not "irreversible"`) {
		t.Fatalf("missing ceiling miss reason: %v", pd.Reasons)
	}
}

func TestNormalizeStandingGrantRejectsBothExactAndCeiling(t *testing.T) {
	_, err := NormalizeStandingGrantSource(StandingGrantSource{StandingGrants: []StandingGrant{{
		ID:                   "grant_bad",
		Capability:           "review.finding",
		Reversibility:        "reversible",
		ReversibilityCeiling: "reversible",
	}}})
	if err == nil || !strings.Contains(err.Error(), "cannot set both") {
		t.Fatalf("expected exact/ceiling rejection, got %v", err)
	}
}

func TestStandingGrantAuthorityKeepsPolicyYAMLFallback(t *testing.T) {
	homePath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homePath, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	policyYAML := []byte(`standing_grants:
  - id: config_grant
    capability: config.capability
    target_path: config/*
    reversibility_ceiling: reversible
`)
	if err := os.WriteFile(filepath.Join(homePath, "config", "policy.yaml"), policyYAML, 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(t.TempDir(), "standing-grants.yaml")
	raw := []byte(`grants:
  - grant_id: authority_grant
    capabilities: ["authority.capability"]
    target_scope: authority/*
    reversibility_ceiling: reversible
`)
	if err := os.WriteFile(sourcePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportStandingGrants(homePath, sourcePath, fixedNow); err != nil {
		t.Fatal(err)
	}
	cfg, source, err := LoadConfig(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if source != filepath.Join(homePath, "config", "policy.yaml") {
		t.Fatalf("policy source = %q", source)
	}
	if len(cfg.StandingGrants) != 2 {
		t.Fatalf("config fallback and authority overlay should both remain, got %+v", cfg.StandingGrants)
	}
}
