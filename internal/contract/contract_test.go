package contract

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/capgraph"
	"compound/internal/decisions"
	"compound/internal/envelope"
	"compound/internal/mem"
	"compound/internal/projection"
)

var fixedNow = time.Date(2026, 6, 28, 0, 0, 0, 0, time.UTC)

func mkInput(kind string) Input {
	return Input{Harness: "claude_code", Cwd: "/repo/x", Task: "fix paywall copy", RepoKind: kind, RepoKindVia: "flag", Mode: "plan"}
}

// The produced contract and pack must be valid envelopes and round-trip losslessly —
// orientation output flows through the same typed spine as everything else.
func TestCompileAndPackValidate(t *testing.T) {
	pol := DefaultPolicy()
	for _, kind := range []string{"product", "control-plane", "library"} {
		in := mkInput(kind)
		ec := Compile(in, pol, fixedNow)
		if err := ec.Validate(); err != nil {
			t.Fatalf("%s contract invalid: %v", kind, err)
		}
		if err := envelope.RoundTrip(&ec); err != nil {
			t.Fatalf("%s contract round-trip: %v", kind, err)
		}
		cp := Pack(in, pol, fixedNow)
		if err := cp.Validate(); err != nil {
			t.Fatalf("%s pack invalid: %v", kind, err)
		}
		if err := envelope.RoundTrip(&cp); err != nil {
			t.Fatalf("%s pack round-trip: %v", kind, err)
		}
	}
}

// The acceptance test for P2: the SAME task yields DIFFERENT packs in a product repo
// vs the control-plane repo. Governance context is excluded in product, included in
// control-plane — the orientation-scope fix.
func TestProductVsControlPlaneDifferentPacks(t *testing.T) {
	pol := DefaultPolicy()
	prod := Pack(mkInput("product"), pol, fixedNow)
	ctl := Pack(mkInput("control-plane"), pol, fixedNow)

	inclRefs := func(cp envelope.ContextPack) map[string]bool {
		m := map[string]bool{}
		for _, r := range cp.Included {
			m[r.Ref] = true
		}
		return m
	}
	exclRefs := func(cp envelope.ContextPack) map[string]bool {
		m := map[string]bool{}
		for _, r := range cp.Excluded {
			m[r.Ref] = true
		}
		return m
	}

	gov := "control-plane://orientation-map"
	if !exclRefs(prod)[gov] {
		t.Errorf("product pack should EXCLUDE governance map %q; excluded=%v", gov, prod.Excluded)
	}
	if !inclRefs(ctl)[gov] {
		t.Errorf("control-plane pack should INCLUDE governance map %q; included=%v", gov, ctl.Included)
	}
	// product-docs is the mirror case: included in product, excluded in control-plane.
	docs := "repo://docs"
	if !inclRefs(prod)[docs] {
		t.Errorf("product pack should INCLUDE product docs %q", docs)
	}
	if !exclRefs(ctl)[docs] {
		t.Errorf("control-plane pack should EXCLUDE product docs %q", docs)
	}
	// And the packs must genuinely differ in their included set.
	if len(prod.Included) == len(ctl.Included) && len(prod.Excluded) == len(ctl.Excluded) {
		t.Errorf("packs should differ: prod incl=%d excl=%d, ctl incl=%d excl=%d",
			len(prod.Included), len(prod.Excluded), len(ctl.Included), len(ctl.Excluded))
	}
	// Every excluded ref must carry a reason (the whole point of recording exclusion).
	for _, r := range prod.Excluded {
		if r.Reason == "" {
			t.Errorf("excluded ref %q has no reason", r.Ref)
		}
	}
}

func TestPackWithCapabilitiesIncludesProjectionContextByRepoKind(t *testing.T) {
	home := t.TempDir()
	report, err := projection.DoctorFile(filepath.Join("..", "..", "fixtures", "projections", "stale-freshness.yaml"), time.Date(2026, 6, 29, 3, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := projection.RecordContext(home, report, []string{"control-plane"}, fixedNow); err != nil {
		t.Fatal(err)
	}
	control, err := PackWithCapabilities(mkInput("control-plane"), DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	projectionRef, ok := findIncludedRef(control, "projection://synthetic-runtime-dashboard")
	if !ok {
		t.Fatalf("control-plane pack should include projection freshness ref: %+v", control.Included)
	}
	if projectionRef.SourceClass != "generated" || projectionRef.Status != "stale" ||
		!strings.Contains(projectionRef.Reason, "generated-view context evidence") {
		t.Fatalf("projection context ref should stay generated/stale evidence, got %+v", projectionRef)
	}
	product, err := PackWithCapabilities(mkInput("product"), DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findIncludedRef(product, "projection://synthetic-runtime-dashboard"); ok {
		t.Fatalf("product pack should not inherit control-plane projection context: %+v", product.Included)
	}
}

func TestPackWithCapabilitiesIncludesExternalRepoIntakeMatches(t *testing.T) {
	home := t.TempDir()
	batchDir := filepath.Join(home, "inbox", "external-repo-intake", "2026-07-01-test")
	if err := os.MkdirAll(batchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mapPath := filepath.Join(batchDir, "external-repo-intake-map.json")
	if err := os.WriteFile(mapPath, []byte(`{
  "boundaries": ["review-only", "no install"],
  "items": [
    {
      "repo": "affaan-m/ECC",
      "owner": "Compound architecture / hooks / memory / skill graph",
      "next_review_step": "review one hook or MCP pattern against Compound adapters",
      "not_to_do": "do not import ECC wholesale or create a parallel operating system"
    }
  ]
}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	summaryPath := filepath.Join(batchDir, "run-summary.json")
	sourceObservationPath := filepath.Join(batchDir, "source-observation.json")
	if err := os.WriteFile(summaryPath, []byte(`{
  "schema_version": "compound.external_repo_intake_run.v0.1",
  "id": "2026-07-01-test",
  "created_at": "2026-07-01T12:00:00Z",
  "map_path": "`+filepath.ToSlash(mapPath)+`",
  "source_observation_path": "`+filepath.ToSlash(sourceObservationPath)+`",
  "candidate_count": 1,
  "counts_by_decision": {"keep_needs_review": 1},
  "counts_by_lane": {"harness_operating_system_reference": 1},
  "decisions": [
    {
      "repo": "affaan-m/ECC",
      "decision": "keep_needs_review",
      "lane": "harness_operating_system_reference",
      "status": "open",
      "evidence_strength": 0.83
    }
  ]
}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	in := mkInput("control-plane")
	in.Task = "Should we adopt https://github.com/affaan-m/ECC hooks and MCP plugins?"
	pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatalf("pack invalid: %v", err)
	}
	ref, ok := findIncludedRef(pack, externalRepoIntakeRef("affaan-m/ECC"))
	if !ok {
		t.Fatalf("pack should include external repo intake match: %+v", pack.Included)
	}
	if ref.SourceClass != "generated" || ref.Status != "current" || ref.Confidence != 0.83 {
		t.Fatalf("external intake ref should stay generated/current evidence, got %+v", ref)
	}
	for _, want := range []string{
		"external_repo_intake repo=affaan-m/ECC decision=keep_needs_review",
		"owner=Compound architecture / hooks / memory / skill graph",
		"next_review_step=review one hook or MCP pattern against Compound adapters",
		"boundary=do not import ECC wholesale or create a parallel operating system",
	} {
		if !strings.Contains(ref.Reason, want) {
			t.Fatalf("external intake reason missing %q: %s", want, ref.Reason)
		}
	}
	if !strings.Contains(pack.SummaryMD, "1 external repo intake match(es) included") {
		t.Fatalf("summary missing external intake count: %s", pack.SummaryMD)
	}

	in.Task = "Should we adopt a repository that has not been reviewed?"
	unmatched, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findIncludedRef(unmatched, externalRepoIntakeRef("affaan-m/ECC")); ok {
		t.Fatalf("unmatched task should not include ECC intake ref: %+v", unmatched.Included)
	}
}

func findIncludedRef(pack envelope.ContextPack, ref string) (envelope.IncludedRef, bool) {
	for _, included := range pack.Included {
		if included.Ref == ref {
			return included, true
		}
	}
	return envelope.IncludedRef{}, false
}

// Encodes the personal-corpus-not-ambient gate: a product repo forbids self-model /
// personal-corpus memory from being ambient; the control-plane allows self-model only
// on explicit request.
func TestPersonalCorpusGate(t *testing.T) {
	pol := DefaultPolicy()
	prod := Compile(mkInput("product"), pol, fixedNow)
	if !contains(prod.MemoryPolicy.Forbidden, "personal-corpus") || !contains(prod.MemoryPolicy.Forbidden, "self-model") {
		t.Errorf("product contract must forbid personal-corpus and self-model ambient; got %v", prod.MemoryPolicy.Forbidden)
	}
	ctl := Compile(mkInput("control-plane"), pol, fixedNow)
	if contains(ctl.MemoryPolicy.Ambient, "self-model") {
		t.Errorf("control-plane must not make self-model ambient; got ambient=%v", ctl.MemoryPolicy.Ambient)
	}
	if !contains(ctl.MemoryPolicy.ExplicitOnly, "self-model") {
		t.Errorf("control-plane should allow self-model only on explicit request; got explicit_only=%v", ctl.MemoryPolicy.ExplicitOnly)
	}
}

// Repo kind is discovered from a committed marker (path-independent), walking up from a
// nested cwd; an explicit flag overrides; absent marker defaults to product.
func TestResolveRepoKind(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".compound"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".compound", "repo.yaml"), []byte("kind: control-plane\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	if k, via := ResolveRepoKind("", nested); k != "control-plane" {
		t.Errorf("nested cwd should detect control-plane from marker, got %q (via %s)", k, via)
	}
	if k, via := ResolveRepoKind("product", nested); k != "product" || via != "flag" {
		t.Errorf("explicit flag should win, got %q via %s", k, via)
	}
	if k, via := ResolveRepoKind("", t.TempDir()); k != "product" || via != "default" {
		t.Errorf("no marker should default to product/default, got %q via %s", k, via)
	}
}

func TestResolveRepoKindUsesRuntimeRegistry(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	controlRoot := t.TempDir()
	controlNested := filepath.Join(controlRoot, "umut-os", "ops")
	if err := os.MkdirAll(controlNested, 0o755); err != nil {
		t.Fatal(err)
	}
	productRoot := t.TempDir()
	productNested := filepath.Join(productRoot, "app")
	if err := os.MkdirAll(productNested, 0o755); err != nil {
		t.Fatal(err)
	}
	registry := []byte("schema_version: " + repoKindRegistrySchema + "\nrepos:\n  - kind: control-plane\n    roots:\n      - " + controlRoot + "\n  - kind: product\n    roots:\n      - " + productRoot + "\n")
	if err := os.WriteFile(filepath.Join(home, "config", "repo-kinds.yaml"), registry, 0o644); err != nil {
		t.Fatal(err)
	}

	kind, via, err := ResolveRepoKindWithHome(home, "", controlNested)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "control-plane" || !strings.HasPrefix(via, "repo-kind-registry:") {
		t.Fatalf("control kind = %q via %q", kind, via)
	}
	kind, _, err = ResolveRepoKindWithHome(home, "", productNested)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "product" {
		t.Fatalf("product kind = %q", kind)
	}
	kind, via, err = ResolveRepoKindWithHome(home, "library", controlNested)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "library" || via != "flag" {
		t.Fatalf("flag should win, kind=%q via=%q", kind, via)
	}
}

func TestResolveRepoKindMarkerWinsOverRuntimeRegistry(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".compound"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".compound", "repo.yaml"), []byte("kind: library\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "repo-kinds.yaml"), []byte("schema_version: "+repoKindRegistrySchema+"\nrepos:\n  - kind: control-plane\n    roots:\n      - "+root+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	kind, via, err := ResolveRepoKindWithHome(home, "", root)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "library" || !strings.HasSuffix(via, filepath.Join(".compound", "repo.yaml")) {
		t.Fatalf("marker should win, kind=%q via=%q", kind, via)
	}
}

// Defaulting the repo kind is recorded as an unknown and softens uncertainty to inferred —
// the contract never silently pretends it knew.
func TestDefaultedRepoKindRecorded(t *testing.T) {
	in := Input{Harness: "codex", Cwd: "/somewhere", Task: "x", RepoKind: "product", RepoKindVia: "default"}
	ec := Compile(in, DefaultPolicy(), fixedNow)
	if len(ec.Unknowns) == 0 {
		t.Error("defaulted repo kind should be recorded in unknowns")
	}
	if ec.Uncertainty.Level != "inferred" {
		t.Errorf("defaulted repo kind should set uncertainty=inferred, got %q", ec.Uncertainty.Level)
	}
}

func TestFixtureReposProveOrientationScope(t *testing.T) {
	root := repoRoot(t)
	productCwd := filepath.Join(root, "fixtures", "orientation", "product-repo", "app")
	controlCwd := filepath.Join(root, "fixtures", "orientation", "control-plane-repo", "ops")

	productKind, productVia := ResolveRepoKind("", productCwd)
	controlKind, controlVia := ResolveRepoKind("", controlCwd)
	if productKind != "product" || controlKind != "control-plane" {
		t.Fatalf("fixture kinds product=%q control=%q", productKind, controlKind)
	}
	if productVia == "default" || controlVia == "default" {
		t.Fatalf("fixture markers should be discovered, product via=%q control via=%q", productVia, controlVia)
	}

	pol := DefaultPolicy()
	product := Pack(Input{Harness: "codex", Cwd: productCwd, Task: "ship onboarding copy", RepoKind: productKind, RepoKindVia: productVia, Mode: "execute"}, pol, fixedNow)
	control := Pack(Input{Harness: "codex", Cwd: controlCwd, Task: "audit hooks", RepoKind: controlKind, RepoKindVia: controlVia, Mode: "execute"}, pol, fixedNow)

	if hasIncluded(product, "control-plane://workflow-contract") {
		t.Fatal("product repo must not include control-plane workflow contract ambiently")
	}
	if !hasExcluded(product, "control-plane://workflow-contract") {
		t.Fatal("product repo must record control-plane workflow contract as excluded")
	}
	if !hasIncluded(control, "control-plane://workflow-contract") {
		t.Fatal("control-plane repo must include workflow contract")
	}
	if hasExcluded(control, "control-plane://workflow-contract") {
		t.Fatal("control-plane repo should not exclude its workflow contract")
	}
}

func TestPackWithCapabilitiesDoesNotCreateStateWhenGraphMissing(t *testing.T) {
	home := t.TempDir()
	pack, err := PackWithCapabilities(mkInput("product"), DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatal(err)
	}
	if hasIncluded(pack, "capability://skill:pricing") {
		t.Fatal("missing graph should not surface capability refs")
	}
	if _, err := os.Stat(filepath.Join(home, "registries")); !os.IsNotExist(err) {
		t.Fatalf("read-only orientation should not create registries dir, err=%v", err)
	}
}

func TestPackWithCapabilitiesSurfacesGraphOffers(t *testing.T) {
	home := t.TempDir()
	manifest := capgraph.SkillManifest{
		SchemaVersion: capgraph.SkillManifestSchemaVersion,
		ID:            "skill_manifest_fixture",
		AdapterID:     "fixture.skill-manifest",
		SourceSystem:  "fixture-os",
		ObservedAt:    "2026-06-29T00:00:00Z",
		SourceRef: capgraph.SkillSourceRef{
			Path:        "fixtures/skills/skill-manifest.yaml",
			Kind:        "generated_view",
			Owner:       "capability-fixture",
			SourceClass: "generated",
			Revision:    "rev_fixture",
		},
		EvidenceRefs: []string{"artifact_sha256:skill-fixture"},
		Skills: []capgraph.SkillManifestEntry{
			{
				ID:                  "skill:pricing",
				Name:                "Pricing",
				Status:              "active",
				Triggers:            []string{"pricing", "paywall"},
				Capabilities:        []string{"monetization"},
				CompatibleHarnesses: []string{"claude_code"},
				Requires:            []string{"tool:revenuecat"},
				ConflictsWith:       []string{"skill:old-paywall"},
				ValidatedBy:         []string{"eval:pricing-copy"},
				Weight:              2,
			},
			{
				ID:       "skill:old-paywall",
				Name:     "Old Paywall",
				Status:   "retired",
				Triggers: []string{"paywall"},
			},
		},
	}
	if _, err := capgraph.ApplySkillManifest(home, manifest, fixedNow); err != nil {
		t.Fatal(err)
	}
	pack, err := PackWithCapabilities(mkInput("product"), DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatal(err)
	}
	if sourceRev(pack.SourceRevisions, capgraph.CapabilityGraphAuthorityRef) == "" {
		t.Fatalf("context pack missing capability graph source revision: %+v", pack.SourceRevisions)
	}
	included, ok := findIncludedRef(pack, "capability://skill:pricing")
	if !ok {
		t.Fatalf("context pack should include surfaced skill offer; included=%v", pack.Included)
	}
	if !containsText(included.Reason, "matched=paywall") {
		t.Fatalf("included reason should explain matched trigger tokens: %q", included.Reason)
	}
	offer := findCapabilityOffer(pack, "capability://skill:pricing")
	if offer == nil {
		t.Fatalf("context pack should include structured capability offer; offers=%v", pack.CapabilityOffers)
	}
	if offer.Kind != "skill" || offer.Name != "Pricing" || offer.Score <= 0 {
		t.Fatalf("unexpected capability offer: %+v", offer)
	}
	if !hasOfferConstraint(offer.Required, capgraph.EdRequires, "tool:revenuecat") {
		t.Fatalf("required constraints = %+v", offer.Required)
	}
	if !hasOfferConstraint(offer.Conflicts, capgraph.EdConflictsWith, "skill:old-paywall") {
		t.Fatalf("conflict constraints = %+v", offer.Conflicts)
	}
	if !hasOfferConstraint(offer.Harnesses, capgraph.EdSurfacesWith, "harness:claude_code") {
		t.Fatalf("harness constraints = %+v", offer.Harnesses)
	}
	if !hasOfferConstraint(offer.Evidence, capgraph.EdValidatedBy, "eval:pricing-copy") {
		t.Fatalf("evidence constraints = %+v", offer.Evidence)
	}
	if offer.CostRiskNote == "" || !containsText(offer.CostRiskNote, "offer only") {
		t.Fatalf("offer should carry execution boundary note: %+v", offer)
	}
	if hasIncluded(pack, "capability://skill:old-paywall") {
		t.Fatalf("retired capability must not be included; included=%v", pack.Included)
	}
	if pack.SummaryMD == "" || !containsText(pack.SummaryMD, "capability offer(s) surfaced") {
		t.Fatalf("summary should report surfaced offers, got %q", pack.SummaryMD)
	}
	ec, err := CompileWithRuntime(mkInput("product"), DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := ec.Validate(); err != nil {
		t.Fatal(err)
	}
	contractOffer := findEffectiveContractCapabilityOffer(ec, "capability://skill:pricing")
	if contractOffer == nil {
		t.Fatalf("effective contract capability offers = %+v, want skill:pricing", ec.CapabilityOffers)
	}
	if contractOffer.CostRiskNote == "" || !hasOfferConstraint(contractOffer.Required, capgraph.EdRequires, "tool:revenuecat") {
		t.Fatalf("effective contract offer lost constraints: %+v", contractOffer)
	}
	if sourceRev(ec.SourceRevisions, capgraph.CapabilityGraphAuthorityRef) == "" {
		t.Fatalf("effective contract missing capability graph source revision: %+v", ec.SourceRevisions)
	}
}

func TestPackWithCapabilitiesBoundsCapabilityOffers(t *testing.T) {
	home := t.TempDir()
	total := maxContextCapabilityOffers + 3
	skills := make([]capgraph.SkillManifestEntry, 0, total)
	for i := 0; i < total; i++ {
		skills = append(skills, capgraph.SkillManifestEntry{
			ID:       fmt.Sprintf("skill:budget-%02d", i),
			Name:     fmt.Sprintf("Budget %02d", i),
			Status:   "active",
			Triggers: []string{"budgeted"},
			Weight:   float64(total - i),
		})
	}
	manifest := capgraph.SkillManifest{
		SchemaVersion: capgraph.SkillManifestSchemaVersion,
		ID:            "skill_manifest_budget_fixture",
		AdapterID:     "fixture.skill-manifest",
		SourceSystem:  "fixture-os",
		ObservedAt:    "2026-06-29T00:00:00Z",
		SourceRef: capgraph.SkillSourceRef{
			Path:        "fixtures/skills/budgeted-skill-manifest.yaml",
			Kind:        "generated_view",
			Owner:       "capability-fixture",
			SourceClass: "generated",
			Revision:    "rev_budget_fixture",
		},
		EvidenceRefs: []string{"artifact_sha256:skill-budget-fixture"},
		Skills:       skills,
	}
	if _, err := capgraph.ApplySkillManifest(home, manifest, fixedNow); err != nil {
		t.Fatal(err)
	}
	in := mkInput("product")
	in.Harness = "codex"
	in.Task = "budgeted capability review"
	pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := len(pack.CapabilityOffers); got != maxContextCapabilityOffers {
		t.Fatalf("capability offers = %d, want %d", got, maxContextCapabilityOffers)
	}
	if got := countCapabilityIncluded(pack); got != maxContextCapabilityOffers {
		t.Fatalf("capability included refs = %d, want %d", got, maxContextCapabilityOffers)
	}
	if findCapabilityOffer(pack, "capability://skill:budget-00") == nil || !hasIncluded(pack, "capability://skill:budget-00") {
		t.Fatalf("highest-ranked offer should be retained; offers=%v included=%v", pack.CapabilityOffers, pack.Included)
	}
	if findCapabilityOffer(pack, "capability://skill:budget-14") != nil || hasIncluded(pack, "capability://skill:budget-14") {
		t.Fatalf("lowest-ranked offer should be omitted from context budget; offers=%v included=%v", pack.CapabilityOffers, pack.Included)
	}
	if !containsText(pack.SummaryMD, "12 of 15 capability offer(s) surfaced") || !containsText(pack.SummaryMD, "3 omitted from context budget") {
		t.Fatalf("summary should report context budget omission, got %q", pack.SummaryMD)
	}
}

func TestPackWithCapabilitiesFiltersByHarnessCompatibility(t *testing.T) {
	home := t.TempDir()
	manifest := capgraph.SkillManifest{
		SchemaVersion: capgraph.SkillManifestSchemaVersion,
		ID:            "skill_manifest_fixture",
		AdapterID:     "fixture.skill-manifest",
		SourceSystem:  "fixture-os",
		ObservedAt:    "2026-06-29T00:00:00Z",
		SourceRef: capgraph.SkillSourceRef{
			Path:        "fixtures/skills/skill-manifest.yaml",
			Kind:        "generated_view",
			Owner:       "capability-fixture",
			SourceClass: "generated",
			Revision:    "rev_fixture",
		},
		EvidenceRefs: []string{"artifact_sha256:skill-fixture"},
		Skills: []capgraph.SkillManifestEntry{
			{
				ID:                  "skill:codex-pricing",
				Name:                "Codex Pricing",
				Status:              "active",
				Triggers:            []string{"pricing"},
				CompatibleHarnesses: []string{"codex"},
			},
			{
				ID:                  "skill:claude-pricing",
				Name:                "Claude Pricing",
				Status:              "active",
				Triggers:            []string{"pricing"},
				CompatibleHarnesses: []string{"claude_code"},
			},
			{
				ID:       "skill:generic-pricing",
				Name:     "Generic Pricing",
				Status:   "active",
				Triggers: []string{"pricing"},
			},
		},
	}
	if _, err := capgraph.ApplySkillManifest(home, manifest, fixedNow); err != nil {
		t.Fatal(err)
	}
	codexIn := mkInput("product")
	codexIn.Harness = "codex"
	codexIn.Task = "improve pricing"
	codexPack, err := PackWithCapabilities(codexIn, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !hasIncluded(codexPack, "capability://skill:codex-pricing") || !hasIncluded(codexPack, "capability://skill:generic-pricing") {
		t.Fatalf("codex pack missing codex/generic offers: %+v", codexPack.Included)
	}
	if hasIncluded(codexPack, "capability://skill:claude-pricing") {
		t.Fatalf("codex pack must not include claude-only offer: %+v", codexPack.Included)
	}
	claudeIn := mkInput("product")
	claudeIn.Harness = "claude_code"
	claudeIn.Task = "improve pricing"
	claudePack, err := PackWithCapabilities(claudeIn, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !hasIncluded(claudePack, "capability://skill:claude-pricing") || !hasIncluded(claudePack, "capability://skill:generic-pricing") {
		t.Fatalf("claude pack missing claude/generic offers: %+v", claudePack.Included)
	}
	if hasIncluded(claudePack, "capability://skill:codex-pricing") {
		t.Fatalf("claude pack must not include codex-only offer: %+v", claudePack.Included)
	}
}

func TestRuntimeContractIncludesAcceptedDecisionAndRealSourceRevisions(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# Repo Instructions\nUse Compound context.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	brainDecision := filepath.Join(t.TempDir(), "brain-decision.md")
	if err := os.WriteFile(brainDecision, []byte(`---
id: compound-context-pack-decision
title: Compound ContextPack owns decision context
kind: doctrine
status: accepted
falsifier: Agents read the old brain file directly instead of the ContextPack.
review_trigger: Revisit when ContextPack stops including the decision ref.
owner: compound
scope: public-core
---
# Compound ContextPack owns decision context
Future agents consume this decision through Compound runtime context.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := decisions.ImportBrainDecision(store, brainDecision, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	hookPath := filepath.Join(t.TempDir(), "RecallContext.hook.ts")
	if err := os.WriteFile(hookPath, []byte("console.log('hook')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema_version: compound.installed_hooks.v0.1\nhooks:\n - harness: codex\n   name: SessionStart\n   ref: " + hookPath + "\n   status: current\n"
	if err := os.WriteFile(filepath.Join(home, "config", "installed-hooks.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	in := Input{Harness: "codex", Cwd: repo, Task: "use imported decision", RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute"}
	ec, err := CompileWithRuntime(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	decisionRef := "decision://" + report.Lifecycle.ID
	if !contains(ec.ActiveDecisions, decisionRef) {
		t.Fatalf("effective contract active decisions = %v, want %s", ec.ActiveDecisions, decisionRef)
	}
	if !contains(ec.ActiveHooks, "hook://codex/SessionStart") {
		t.Fatalf("effective contract active hooks = %v", ec.ActiveHooks)
	}
	if sourceRev(ec.SourceRevisions, "AGENTS.md") == "unknown" {
		t.Fatalf("AGENTS.md source revision was not resolved: %+v", ec.SourceRevisions)
	}
	if sourceRev(ec.SourceRevisions, "hook://codex/SessionStart") == "unknown" {
		t.Fatalf("hook source revision was not resolved: %+v", ec.SourceRevisions)
	}
	pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(pack.ActiveDecisions, decisionRef) {
		t.Fatalf("context pack active decisions = %v, want %s", pack.ActiveDecisions, decisionRef)
	}
	if !hasIncluded(pack, decisionRef) {
		t.Fatalf("context pack should include decision ref %s; included=%v", decisionRef, pack.Included)
	}
}

func TestRuntimeContractResolvesControlPlaneSourceRevisions(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	files := map[string]string{
		"AGENTS.md":                             "# Repo Instructions\nUse Compound context.\n",
		"docs/ARCHITECTURE.md":                  "# Architecture\nRead LAYERS first.\n",
		"docs/LAYERS.md":                        "# Layers\nEnvelope spine.\n",
		"docs/architecture/contracts/README.md": "# Contracts\nContext and work contracts.\n",
		"docs/architecture/runtime.md":          "# Runtime\nCOMPOUND_HOME and envelope spine.\n",
		"docs/roadmap/phases.md":                "# Phases\nEnvelope-first roadmap.\n",
	}
	for rel, body := range files {
		path := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	in := Input{Harness: "codex", Cwd: repo, Task: "compile control-plane source refs", RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute"}
	ec, err := CompileWithRuntime(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		"control-plane://orientation-map",
		"control-plane://workflow-contract",
		"control-plane://sop-packets",
	} {
		if !contains(ec.ActiveInstructions, source) {
			t.Fatalf("effective contract active instructions missing %s: %v", source, ec.ActiveInstructions)
		}
		rev := sourceRev(ec.SourceRevisions, source)
		if rev == "" || rev == "unknown" || !strings.HasPrefix(rev, "sha256:") {
			t.Fatalf("source revision for %s = %q, want sha256 digest in %+v", source, rev, ec.SourceRevisions)
		}
		packRev := sourceRev(pack.SourceRevisions, source)
		if packRev == "" || packRev == "unknown" || !strings.HasPrefix(packRev, "sha256:") {
			t.Fatalf("context pack source revision for %s = %q, want sha256 digest in %+v", source, packRev, pack.SourceRevisions)
		}
	}
}

func TestImportCodexHooksJSONFeedsRuntimeContract(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join("..", "..", "fixtures", "harness", "codex-hooks-json.json")
	report, err := ImportCodexHooksJSON(home, source, fixedNow)
	if err != nil {
		t.Fatalf("import codex hooks json: %v", err)
	}
	if report.AuthorityRef != InstalledHooksAuthorityRef || report.HookCount != 2 {
		t.Fatalf("unexpected import report: %+v", report)
	}
	if len(report.Harnesses) != 1 || report.Harnesses[0] != "codex" {
		t.Fatalf("import harnesses = %+v", report.Harnesses)
	}
	manifest, err := LoadInstalledHooksManifest(home)
	if err != nil {
		t.Fatalf("load installed hooks manifest: %v", err)
	}
	if len(manifest.Hooks) != 2 {
		t.Fatalf("installed hooks = %+v", manifest.Hooks)
	}
	records, err := LoadInstalledHooksSourceAuthority(home)
	if err != nil {
		t.Fatalf("load source authority: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("source authority records = %d, want 1", len(records))
	}
	if records[0].AuthorityStatus != "compound_primary" || records[0].SourceRole != "evidence_only_input_after_import" {
		t.Fatalf("bad source authority: %+v", records[0])
	}
	in := Input{Harness: "codex", Cwd: t.TempDir(), Task: "use hooks", RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute"}
	ec, err := CompileWithRuntime(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hook://codex/SessionStart", "hook://codex/UserPromptSubmit"} {
		if !contains(ec.ActiveHooks, want) {
			t.Fatalf("effective contract active hooks = %v, want %s", ec.ActiveHooks, want)
		}
	}
	pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hook://codex/SessionStart", "hook://codex/UserPromptSubmit"} {
		if !contains(pack.ActiveHooks, want) {
			t.Fatalf("context pack active hooks = %v, want %s", pack.ActiveHooks, want)
		}
	}
	if sourceRev(ec.SourceRevisions, InstalledHooksAuthorityRef) == "" {
		t.Fatalf("effective contract missing installed-hooks source revision: %+v", ec.SourceRevisions)
	}
}

func TestImportHookRegistrationTSFeedsRuntimeContract(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join("..", "..", "fixtures", "harness", "hook-registration.ts")
	report, err := ImportHookRegistrationTS(home, source, fixedNow)
	if err != nil {
		t.Fatalf("import hook registration ts: %v", err)
	}
	if report.AuthorityRef != InstalledHooksAuthorityRef || report.HookCount != 5 {
		t.Fatalf("unexpected import report: %+v", report)
	}
	if len(report.Harnesses) != 2 || report.Harnesses[0] != "claude_code" || report.Harnesses[1] != "codex" {
		t.Fatalf("import harnesses = %+v", report.Harnesses)
	}
	records, err := LoadInstalledHooksSourceAuthority(home)
	if err != nil {
		t.Fatalf("load source authority: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("source authority records = %d, want 1", len(records))
	}
	if records[0].ImportedVia != installedHooksRegistryImportedVia {
		t.Fatalf("imported_via = %q, want %q", records[0].ImportedVia, installedHooksRegistryImportedVia)
	}
	manifest, err := LoadInstalledHooksManifest(home)
	if err != nil {
		t.Fatalf("load installed hooks manifest: %v", err)
	}
	if !installedHookHasMatcher(manifest, "claude_code", "PreToolUse", "Write|Edit|MultiEdit") {
		t.Fatalf("imported hook registration should preserve PreToolUse matcher: %+v", manifest.Hooks)
	}

	claudeInput := Input{Harness: "claude_code", Cwd: t.TempDir(), Task: "use hooks", RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute"}
	ec, err := CompileWithRuntime(claudeInput, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hook://claude_code/PreToolUse", "hook://claude_code/SessionStart", "hook://claude_code/UserPromptSubmit"} {
		if !contains(ec.ActiveHooks, want) {
			t.Fatalf("claude effective contract active hooks = %v, want %s", ec.ActiveHooks, want)
		}
	}
	pack, err := PackWithCapabilities(claudeInput, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(pack.ActiveHooks, "hook://claude_code/SessionStart") {
		t.Fatalf("claude context pack active hooks = %v", pack.ActiveHooks)
	}
	readOnlyInput := Input{Harness: "claude_code", Cwd: t.TempDir(), Task: "audit current hook status", RepoKind: "control-plane", RepoKindVia: "flag"}
	readOnlyEC, err := CompileWithRuntime(readOnlyInput, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if contains(readOnlyEC.ActiveHooks, "hook://claude_code/PreToolUse") {
		t.Fatalf("read-only effective contract should exclude write-scoped PreToolUse hook: %v", readOnlyEC.ActiveHooks)
	}
	readOnlyPack, err := PackWithCapabilities(readOnlyInput, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if contains(readOnlyPack.ActiveHooks, "hook://claude_code/PreToolUse") || !hasExcluded(readOnlyPack, "hook://claude_code/PreToolUse") {
		t.Fatalf("read-only context pack active=%v excluded=%v", readOnlyPack.ActiveHooks, readOnlyPack.Excluded)
	}

	codexInput := Input{Harness: "codex", Cwd: t.TempDir(), Task: "edit source file with hooks", RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute"}
	codex, err := CompileWithRuntime(codexInput, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if contains(codex.ActiveHooks, "hook://claude_code/PreToolUse") {
		t.Fatalf("codex effective contract leaked claude hook: %v", codex.ActiveHooks)
	}
	reason := excludedRefReason(codex.Excluded, "hook://codex/PreToolUse")
	if !strings.Contains(reason, "claude_code") || !strings.Contains(reason, "not requested harness codex") {
		t.Fatalf("codex missing write-gate exclusion should cite incompatible harness evidence, got %q", reason)
	}
	for _, want := range []string{"hook://codex/SessionStart", "hook://codex/UserPromptSubmit"} {
		if !contains(codex.ActiveHooks, want) {
			t.Fatalf("codex effective contract active hooks = %v, want %s", codex.ActiveHooks, want)
		}
	}
}

func TestInstalledHookGateReadinessFlagsFailOpenCodex(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join("..", "..", "fixtures", "harness", "hook-registration.ts")
	if _, err := ImportHookRegistrationTS(home, source, fixedNow); err != nil {
		t.Fatalf("import hook registration ts: %v", err)
	}
	manifest, err := LoadInstalledHooksManifest(home)
	if err != nil {
		t.Fatalf("load installed hooks manifest: %v", err)
	}
	readiness := InstalledHookGateReadiness(manifest, map[string]bool{
		"claude_code": true,
		"codex":       false,
	})
	claude := findHookGateReadiness(readiness, "claude_code")
	if claude == nil || claude.Status != "native_blocking" || !claude.Installed || claude.AdapterRequired {
		t.Fatalf("claude readiness = %+v", claude)
	}
	codex := findHookGateReadiness(readiness, "codex")
	if codex == nil || codex.Status != "missing_gate" || !codex.AdapterRequired {
		t.Fatalf("codex readiness = %+v", codex)
	}
	if !strings.Contains(codex.AdapterCommand, "hook pre-tool-use --enforce") {
		t.Fatalf("codex adapter command = %q", codex.AdapterCommand)
	}
}

func TestPackWithCapabilitiesRecordsMissingWriteGateForMutationTask(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `schema_version: compound.installed_hooks.v0.1
hooks:
  - harness: codex
    name: SessionStart
    event: SessionStart
    ref: /tmp/codex-session-start.hook.ts
    status: current
  - harness: codex
    name: UserPromptSubmit
    event: UserPromptSubmit
    ref: /tmp/codex-user-prompt-submit.hook.ts
    status: current
`
	if err := os.WriteFile(filepath.Join(home, "config", "installed-hooks.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	in := Input{
		Harness:     "codex",
		Cwd:         t.TempDir(),
		Task:        "edit source file",
		RepoKind:    "control-plane",
		RepoKindVia: "flag",
		Mode:        "execute",
	}
	pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hook://codex/SessionStart", "hook://codex/UserPromptSubmit"} {
		if !contains(pack.ActiveHooks, want) {
			t.Fatalf("context pack active hooks = %v, want %s", pack.ActiveHooks, want)
		}
	}
	reason := excludedReason(pack, "hook://codex/PreToolUse")
	if !strings.Contains(reason, "no installed PreToolUse write gate") {
		t.Fatalf("context pack excluded=%v, want missing write gate reason", pack.Excluded)
	}

	readOnly := in
	readOnly.Task = "audit current hook status"
	readOnly.Mode = ""
	readOnlyPack, err := PackWithCapabilities(readOnly, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if hasExcluded(readOnlyPack, "hook://codex/PreToolUse") {
		t.Fatalf("read-only context pack should not report missing write gate: %v", readOnlyPack.Excluded)
	}
}

func TestCompileWithRuntimeRecordsExcludedContextAndHookGaps(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `schema_version: compound.installed_hooks.v0.1
hooks:
  - harness: codex
    name: SessionStart
    event: SessionStart
    ref: /tmp/codex-session-start.hook.ts
    status: current
  - harness: codex
    name: UserPromptSubmit
    event: UserPromptSubmit
    ref: /tmp/codex-user-prompt-submit.hook.ts
    status: current
`
	if err := os.WriteFile(filepath.Join(home, "config", "installed-hooks.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	in := Input{
		Harness:     "codex",
		Cwd:         t.TempDir(),
		Task:        "edit source file",
		RepoKind:    "control-plane",
		RepoKindVia: "flag",
		Mode:        "execute",
	}
	ec, err := CompileWithRuntime(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hook://codex/SessionStart", "hook://codex/UserPromptSubmit"} {
		if !contains(ec.ActiveHooks, want) {
			t.Fatalf("effective contract active hooks = %v, want %s", ec.ActiveHooks, want)
		}
	}
	if reason := excludedRefReason(ec.Excluded, "repo://docs"); reason == "" {
		t.Fatalf("effective contract should record out-of-scope repo docs exclusion: %+v", ec.Excluded)
	}
	reason := excludedRefReason(ec.Excluded, "hook://codex/PreToolUse")
	if !strings.Contains(reason, "no installed PreToolUse write gate") {
		t.Fatalf("effective contract excluded=%v, want missing write gate reason", ec.Excluded)
	}
	codexReadiness := findEnvelopeHookReadiness(ec.HookReadiness, "codex", "PreToolUse")
	if codexReadiness == nil || codexReadiness.Status != "missing_gate" || !codexReadiness.AdapterRequired {
		t.Fatalf("effective contract hook readiness = %+v, want codex missing_gate adapter-required", ec.HookReadiness)
	}
	if codexReadiness.AdapterCommand == "" {
		t.Fatalf("effective contract hook readiness missing adapter command: %+v", codexReadiness)
	}
	pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	packReadiness := findEnvelopeHookReadiness(pack.HookReadiness, "codex", "PreToolUse")
	if packReadiness == nil || packReadiness.Status != "missing_gate" || !packReadiness.AdapterRequired {
		t.Fatalf("context pack hook readiness = %+v, want codex missing_gate adapter-required", pack.HookReadiness)
	}
	readOnly := in
	readOnly.Task = "audit README headings"
	readOnly.Mode = ""
	readOnlyContract, err := CompileWithRuntime(readOnly, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(readOnlyContract.HookReadiness) != 0 {
		t.Fatalf("read-only contract should not carry write-gate readiness: %+v", readOnlyContract.HookReadiness)
	}
	hookAudit := in
	hookAudit.Task = "audit current hook status"
	hookAudit.Mode = ""
	hookAuditContract, err := CompileWithRuntime(hookAudit, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if findEnvelopeHookReadiness(hookAuditContract.HookReadiness, "codex", "PreToolUse") == nil {
		t.Fatalf("hook audit contract should carry hook readiness: %+v", hookAuditContract.HookReadiness)
	}
}

func TestPackWithCapabilitiesIncludesRelevantCompoundMemory(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Capture([]mem.Candidate{
		{
			Entity:          "compound",
			Predicate:       "capability_authority_surface",
			Object:          "Compound owns the capability graph runtime surface",
			Confidence:      0.91,
			Derivation:      "deterministic",
			ClaimType:       "architecture",
			Qualifiers:      map[string]string{"domain": "capability_authority"},
			PromotionBasis:  candidates.BasisCuratedPage,
			SourceTraceRef:  "ev_compound",
			SnippetHash:     "sha256:compound",
			Visibility:      "contextual",
			ObservationDate: "2026-06-30T00:00:00Z",
		},
		{
			Entity:          "umut",
			Predicate:       "preference",
			Object:          "private self-model fact",
			Confidence:      1,
			Derivation:      "deterministic",
			ClaimType:       "preference",
			PromotionBasis:  candidates.BasisExplicitConfirmation,
			SourceTraceRef:  "ev_private",
			SnippetHash:     "sha256:private",
			Visibility:      "private",
			ObservationDate: "2026-06-30T00:00:00Z",
		},
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	relevant, err := store.Adjudicate(got[0].ID, "promote", "test", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Adjudicate(got[1].ID, "promote", "test", fixedNow); err != nil {
		t.Fatal(err)
	}

	in := Input{Harness: "codex", Cwd: "/repo/compound", Task: "resume compound absorption", RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute"}
	pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	ref := "claim://" + relevant.Claim.ID
	if !contains(pack.ActiveMemory, ref) {
		t.Fatalf("context pack active_memory = %v, want %s", pack.ActiveMemory, ref)
	}
	if !hasIncluded(pack, ref) {
		t.Fatalf("context pack should include memory ref %s; included=%v", ref, pack.Included)
	}
	if !strings.Contains(pack.SummaryMD, "Compound memory (owned ledger): compound.capability_authority_surface") {
		t.Fatalf("summary should carry compact Compound memory line, got %q", pack.SummaryMD)
	}
	if strings.Contains(pack.SummaryMD, "private self-model fact") {
		t.Fatalf("summary leaked private/self-model memory: %q", pack.SummaryMD)
	}
}

func TestPackWithCapabilitiesIncludesTaskRelevantMemoryBeyondEntityKey(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	var cands []mem.Candidate
	for i := 0; i < 5; i++ {
		cands = append(cands, mem.Candidate{
			Entity:          "repo:compound",
			Predicate:       fmt.Sprintf("source_control_fact_%d", i),
			Object:          "repo state is current",
			Confidence:      0.9,
			Derivation:      "deterministic",
			ClaimType:       "status",
			PromotionBasis:  candidates.BasisRuntimeProof,
			SourceTraceRef:  fmt.Sprintf("ev_repo_%d", i),
			SnippetHash:     fmt.Sprintf("sha256:repo-%d", i),
			Visibility:      "operational",
			ObservationDate: "2026-07-02T00:00:00Z",
		})
	}
	cands = append(cands,
		mem.Candidate{
			Entity:          "CapabilityGraph",
			Predicate:       "surfacing_feedback_status",
			Object:          "external skill repo adoption should surface procedure:skill-ops ahead of production-audit for capability surfacing",
			Confidence:      0.91,
			Derivation:      "deterministic",
			ClaimType:       "status",
			Qualifiers:      map[string]string{"domain": "capability-surfacing"},
			PromotionBasis:  candidates.BasisRuntimeProof,
			SourceTraceRef:  "ev_capability_graph",
			SnippetHash:     "sha256:capability-graph",
			Visibility:      "operational",
			ObservationDate: "2026-07-02T00:00:00Z",
		},
		mem.Candidate{
			Entity:          "umut",
			Predicate:       "preference",
			Object:          "private external skill repo preference",
			Confidence:      1,
			Derivation:      "deterministic",
			ClaimType:       "preference",
			PromotionBasis:  candidates.BasisExplicitConfirmation,
			SourceTraceRef:  "ev_private",
			SnippetHash:     "sha256:private",
			Visibility:      "private",
			ObservationDate: "2026-07-02T00:00:00Z",
		},
	)
	got, err := store.Capture(cands, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	var capabilityRef string
	for i := range got {
		res, err := store.Adjudicate(got[i].ID, "promote", "test", fixedNow.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if res.Claim != nil && res.Claim.Entity == "CapabilityGraph" {
			capabilityRef = "claim://" + res.Claim.ID
		}
	}
	in := Input{
		Harness:     "codex",
		Cwd:         "/repo/compound",
		Task:        "Evaluate external skill repos for relevant capability surfacing without blind adoption",
		RepoKind:    "control-plane",
		RepoKindVia: "flag",
		Mode:        "execute",
	}
	pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(pack.ActiveMemory, capabilityRef) {
		t.Fatalf("context pack active_memory = %v, want task-relevant %s", pack.ActiveMemory, capabilityRef)
	}
	if rev := sourceRev(pack.SourceRevisions, memoryRelevanceSourceRef); rev == "" || rev == "unknown" {
		t.Fatalf("context pack memory source revision = %q, want ledger digest in %+v", rev, pack.SourceRevisions)
	}
	if strings.Contains(pack.SummaryMD, "private external skill repo preference") {
		t.Fatalf("summary leaked private/self-model memory: %q", pack.SummaryMD)
	}
	ec, err := CompileWithRuntime(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(ec.ActiveMemory, capabilityRef) {
		t.Fatalf("effective contract active_memory = %v, want task-relevant %s", ec.ActiveMemory, capabilityRef)
	}
	if rev := sourceRev(ec.SourceRevisions, memoryRelevanceSourceRef); rev == "" || rev == "unknown" {
		t.Fatalf("effective contract memory source revision = %q, want ledger digest in %+v", rev, ec.SourceRevisions)
	}
}

func TestPackWithCapabilitiesIncludesTaskRelevantKnowledgeImportClaim(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	cands := []mem.Candidate{
		{
			Entity:         "knowledge:arxiv:2607.01213v1",
			Predicate:      "reference_title",
			Object:         "RepoRescue: An Empirical Study of LLM Agents on Whole-Repository Compatibility Rescue",
			Confidence:     0.8,
			Derivation:     "deterministic",
			PromotionBasis: candidates.BasisTrustedDeterministic,
			Qualifiers: map[string]string{
				"source":      "knowledge_import",
				"source_kind": "arxiv",
				"source_ref":  "arxiv:cs.SE",
			},
			SourceTraceRef:  "ev_arxiv",
			SnippetHash:     "sha256:arxiv",
			Visibility:      "contextual",
			ObservationDate: "2026-07-01",
		},
	}
	for i := 0; i < maxContextMemoryRefs; i++ {
		cands = append(cands, mem.Candidate{
			Entity:          "repo:compound",
			Predicate:       fmt.Sprintf("source_control_fact_%d", i),
			Object:          "repo state is current",
			Confidence:      0.9,
			Derivation:      "deterministic",
			ClaimType:       "status",
			PromotionBasis:  candidates.BasisRuntimeProof,
			SourceTraceRef:  fmt.Sprintf("ev_repo_%d", i),
			SnippetHash:     fmt.Sprintf("sha256:repo-%d", i),
			Visibility:      "operational",
			ObservationDate: "2026-07-02T00:00:00Z",
		})
	}
	got, err := store.Capture(cands, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	var knowledgeRef string
	for i := range got {
		res, err := store.Adjudicate(got[i].ID, "promote", "test", fixedNow.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if res.Claim != nil && strings.HasPrefix(res.Claim.Entity, "knowledge:") {
			knowledgeRef = "claim://" + res.Claim.ID
		}
	}
	if knowledgeRef == "" {
		t.Fatal("knowledge claim was not promoted")
	}
	matching := Input{
		Harness:     "codex",
		Cwd:         "/repo/compound",
		Task:        "RepoRescue whole-repository compatibility rescue for LLM agents",
		RepoKind:    "control-plane",
		RepoKindVia: "flag",
		Mode:        "execute",
	}
	pack, err := PackWithCapabilities(matching, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(pack.ActiveMemory, knowledgeRef) {
		t.Fatalf("context pack active_memory = %v, want task-relevant knowledge claim %s", pack.ActiveMemory, knowledgeRef)
	}
	unrelated := matching
	unrelated.Task = "billing dashboard polish"
	unrelatedPack, err := PackWithCapabilities(unrelated, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if contains(unrelatedPack.ActiveMemory, knowledgeRef) {
		t.Fatalf("unrelated task active_memory = %v, should not include %s", unrelatedPack.ActiveMemory, knowledgeRef)
	}
}

func TestPackWithCapabilitiesIgnoresStopwordOnlyTaskOverlap(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Capture([]mem.Candidate{
		{
			Entity:         "knowledge:arxiv:stopword.0001v1",
			Predicate:      "reference_title",
			Object:         "Findings about the impact for agents",
			Confidence:     0.8,
			Derivation:     "deterministic",
			PromotionBasis: candidates.BasisTrustedDeterministic,
			Qualifiers: map[string]string{
				"source":      "knowledge_import",
				"source_kind": "arxiv",
			},
			SourceTraceRef:  "ev_stopword",
			SnippetHash:     "sha256:stopword",
			Visibility:      "contextual",
			ObservationDate: "2026-07-01",
		},
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Adjudicate(got[0].ID, "promote", "test", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	ref := "claim://" + res.Claim.ID
	in := Input{
		Harness:     "codex",
		Cwd:         "/repo/compound",
		Task:        "polish the billing dashboard for launch",
		RepoKind:    "control-plane",
		RepoKindVia: "flag",
		Mode:        "execute",
	}
	pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if contains(pack.ActiveMemory, ref) {
		t.Fatalf("stopword-only overlap surfaced unrelated claim in active_memory: %v", pack.ActiveMemory)
	}
}

func TestPackWithCapabilitiesRejectsLegacyKnowledgeCandidateVisibility(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Capture([]mem.Candidate{
		{
			Entity:         "knowledge:arxiv:2607.01213v1",
			Predicate:      "reports_result",
			Object:         "RepoRescue evaluates LLM agents on whole-repository compatibility rescue",
			Confidence:     0.8,
			Derivation:     "extracted",
			PromotionBasis: candidates.BasisExplicitConfirmation,
			Qualifiers: map[string]string{
				"source":        "knowledge_extraction",
				"source_kind":   "arxiv",
				"extraction_id": "kx_reporescue",
			},
			SourceTraceRef:  "knowledge_extraction:kx_reporescue",
			SnippetHash:     "sha256:arxiv-meaning",
			Visibility:      "contextual",
			ObservationDate: "2026-07-01",
		},
		{
			Entity:         "knowledge:arxiv:legacy.01213v1",
			Predicate:      "reference_title",
			Object:         "LegacyRepoRescue should not surface through candidate visibility",
			Confidence:     0.8,
			Derivation:     "deterministic",
			PromotionBasis: candidates.BasisTrustedDeterministic,
			Qualifiers: map[string]string{
				"source":      "knowledge_import",
				"source_kind": "arxiv",
			},
			SourceTraceRef:  "ev_legacy_arxiv",
			SnippetHash:     "sha256:legacy-arxiv",
			Visibility:      "candidate",
			ObservationDate: "2026-07-01",
		},
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	var meaningRef, legacyRef string
	for i := range got {
		res, err := store.Adjudicate(got[i].ID, "promote", "test", fixedNow.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		switch res.Claim.Object {
		case "RepoRescue evaluates LLM agents on whole-repository compatibility rescue":
			meaningRef = "claim://" + res.Claim.ID
		case "LegacyRepoRescue should not surface through candidate visibility":
			legacyRef = "claim://" + res.Claim.ID
		}
	}
	matching := Input{
		Harness:     "codex",
		Cwd:         "/repo/compound",
		Task:        "RepoRescue whole-repository compatibility rescue LLM agents LegacyRepoRescue",
		RepoKind:    "control-plane",
		RepoKindVia: "flag",
		Mode:        "execute",
	}
	pack, err := PackWithCapabilities(matching, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(pack.ActiveMemory, meaningRef) {
		t.Fatalf("context pack active_memory = %v, want task-relevant meaning claim %s", pack.ActiveMemory, meaningRef)
	}
	if contains(pack.ActiveMemory, legacyRef) {
		t.Fatalf("legacy candidate-visibility knowledge claim surfaced via special case: %v", pack.ActiveMemory)
	}
}

func TestRuntimeContractDedupesDecisionRefsByDecisionID(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	cands, err := store.Capture([]mem.Candidate{
		{
			Entity:         "decision:legacy-DEC-0001",
			Predicate:      "decided",
			Object:         "Legacy localization baseline",
			Confidence:     1,
			Derivation:     "deterministic",
			ClaimType:      "decision",
			Qualifiers:     map[string]string{"decision_id": "DEC-0001", "lifecycle_status": "accepted"},
			PromotionBasis: candidates.BasisDecisionRecord,
			SourceTraceRef: "ev_old",
			SnippetHash:    "old",
			Visibility:     "ambient",
		},
		{
			Entity:         "decision:DEC-0001",
			Predicate:      "decided",
			Object:         "Current localization baseline",
			Confidence:     1,
			Derivation:     "deterministic",
			ClaimType:      "decision",
			Qualifiers:     map[string]string{"decision_id": "DEC-0001", "lifecycle_status": "accepted"},
			PromotionBasis: candidates.BasisDecisionRecord,
			SourceTraceRef: "ev_new",
			SnippetHash:    "new",
			Visibility:     "ambient",
		},
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Adjudicate(cands[0].ID, "promote", "test", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Adjudicate(cands[1].ID, "promote", "test", fixedNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	in := mkInput("control-plane")
	ec, err := CompileWithRuntime(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if got := countStrings(ec.ActiveDecisions, "decision://DEC-0001"); got != 1 {
		t.Fatalf("contract should include one decision ref, got %d in %v", got, ec.ActiveDecisions)
	}
	if rev := sourceRev(ec.SourceRevisions, "decision://DEC-0001"); rev != "claim:"+second.Claim.ID {
		t.Fatalf("contract should use newest claim revision, got %q want %q (old %s)", rev, "claim:"+second.Claim.ID, first.Claim.ID)
	}

	pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if got := countStrings(pack.ActiveDecisions, "decision://DEC-0001"); got != 1 {
		t.Fatalf("pack should include one active decision ref, got %d in %v", got, pack.ActiveDecisions)
	}
	if got := countIncluded(pack, "decision://DEC-0001"); got != 1 {
		t.Fatalf("pack should include one included decision ref, got %d in %+v", got, pack.Included)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root")
		}
		dir = parent
	}
}

func hasIncluded(pack envelope.ContextPack, ref string) bool {
	for _, inc := range pack.Included {
		if inc.Ref == ref {
			return true
		}
	}
	return false
}

func countIncluded(pack envelope.ContextPack, ref string) int {
	n := 0
	for _, inc := range pack.Included {
		if inc.Ref == ref {
			n++
		}
	}
	return n
}

func countCapabilityIncluded(pack envelope.ContextPack) int {
	n := 0
	for _, inc := range pack.Included {
		if strings.HasPrefix(inc.Ref, "capability://") {
			n++
		}
	}
	return n
}

func countStrings(values []string, want string) int {
	n := 0
	for _, value := range values {
		if value == want {
			n++
		}
	}
	return n
}

func hasExcluded(pack envelope.ContextPack, ref string) bool {
	for _, exc := range pack.Excluded {
		if exc.Ref == ref && exc.Reason != "" {
			return true
		}
	}
	return false
}

func excludedReason(pack envelope.ContextPack, ref string) string {
	return excludedRefReason(pack.Excluded, ref)
}

func excludedRefReason(excluded []envelope.ExcludedRef, ref string) string {
	for _, exc := range excluded {
		if exc.Ref == ref {
			return exc.Reason
		}
	}
	return ""
}

func installedHookHasMatcher(manifest InstalledHooksManifest, harness, name, matcher string) bool {
	for _, hook := range manifest.Hooks {
		if hook.Harness == harness && hook.Name == name && hook.Matcher == matcher {
			return true
		}
	}
	return false
}

func findHookGateReadiness(readiness []HookGateReadiness, harness string) *HookGateReadiness {
	for i := range readiness {
		if readiness[i].Harness == harness {
			return &readiness[i]
		}
	}
	return nil
}

func findEnvelopeHookReadiness(readiness []envelope.HookReadiness, harness, hook string) *envelope.HookReadiness {
	for i := range readiness {
		if readiness[i].Harness == harness && readiness[i].Hook == hook {
			return &readiness[i]
		}
	}
	return nil
}

func findCapabilityOffer(pack envelope.ContextPack, ref string) *envelope.CapabilityOffer {
	for i := range pack.CapabilityOffers {
		if pack.CapabilityOffers[i].Ref == ref {
			return &pack.CapabilityOffers[i]
		}
	}
	return nil
}

func findEffectiveContractCapabilityOffer(ec envelope.EffectiveContract, ref string) *envelope.CapabilityOffer {
	for i := range ec.CapabilityOffers {
		if ec.CapabilityOffers[i].Ref == ref {
			return &ec.CapabilityOffers[i]
		}
	}
	return nil
}

func hasOfferConstraint(values []envelope.CapabilityOfferConstraint, kind, ref string) bool {
	for _, value := range values {
		if value.Kind == kind && value.Ref == ref {
			return true
		}
	}
	return false
}

func containsText(s, needle string) bool {
	return strings.Contains(s, needle)
}

func sourceRev(revs []envelope.SourceRev, source string) string {
	for _, rev := range revs {
		if rev.Source == source {
			return rev.Rev
		}
	}
	return ""
}
