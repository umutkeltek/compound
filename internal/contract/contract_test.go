package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/capgraph"
	"compound/internal/envelope"
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
				ID:           "skill:pricing",
				Name:         "Pricing",
				Status:       "active",
				Triggers:     []string{"pricing", "paywall"},
				Capabilities: []string{"monetization"},
				Weight:       2,
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
	if !hasIncluded(pack, "capability://skill:pricing") {
		t.Fatalf("context pack should include surfaced skill offer; included=%v", pack.Included)
	}
	if hasIncluded(pack, "capability://skill:old-paywall") {
		t.Fatalf("retired capability must not be included; included=%v", pack.Included)
	}
	if pack.SummaryMD == "" || !containsText(pack.SummaryMD, "capability offer(s) surfaced") {
		t.Fatalf("summary should report surfaced offers, got %q", pack.SummaryMD)
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

func hasExcluded(pack envelope.ContextPack, ref string) bool {
	for _, exc := range pack.Excluded {
		if exc.Ref == ref && exc.Reason != "" {
			return true
		}
	}
	return false
}

func containsText(s, needle string) bool {
	return strings.Contains(s, needle)
}
