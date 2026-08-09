package routeevidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// FALSIFIER 6 — projection/drift.
//
// The projection must be deterministic, --check-view must PASS on it, and the
// check must FAIL after a deliberate one-claim mutation. The control matters: a
// drift check that passes on everything is worse than no check, so this test
// proves the assertion has teeth by flipping exactly one level and requiring red.

func TestModelCapabilitiesYAMLProjectionIsDeterministicAndCarriesNoPrivatePaths(t *testing.T) {
	home, _ := promotionApplyHome(t)

	first, err := ProjectModelCapabilitiesYAML(home)
	if err != nil {
		t.Fatalf("project yaml: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := ProjectModelCapabilitiesYAML(home)
		if err != nil {
			t.Fatalf("project yaml (repeat %d): %v", i, err)
		}
		if string(again) != string(first) {
			t.Fatalf("projection is not deterministic on repeat %d", i)
		}
	}

	// No timestamps, no private/home paths leak into the projection.
	body := string(first)
	if strings.Contains(body, home) || strings.Contains(body, "/var/folders") || strings.Contains(body, t.TempDir()) {
		t.Fatalf("projection leaked a private path:\n%s", body)
	}
	for _, banned := range []string{"generated_at", "created_at", "authority_path", "source_path"} {
		if strings.Contains(body, banned) {
			t.Fatalf("projection leaked %q:\n%s", banned, body)
		}
	}
	// It is a real projection of the authority, not an empty document.
	for _, want := range []string{"auto/judge", "fedora/qwen3.6-35b-a3b", "classify-extract", "routing_defaults"} {
		if !strings.Contains(body, want) {
			t.Fatalf("projection missing %q:\n%s", want, body)
		}
	}
}

// writeViewFrom renders a view file from a manifest the test mutated
// structurally. Mutating through decode/encode (not string surgery) keeps these
// falsifiers independent of the YAML emitter's cosmetic indentation choices.
func writeViewFrom(t *testing.T, manifest ModelCapabilitiesManifest) string {
	t.Helper()
	body, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	path := filepath.Join(t.TempDir(), "view.yaml")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write view: %v", err)
	}
	return path
}

// decodeProjection returns the projection both as bytes and as a manifest the
// test can mutate.
func decodeProjection(t *testing.T, home string) ([]byte, ModelCapabilitiesManifest) {
	t.Helper()
	body, err := ProjectModelCapabilitiesYAML(home)
	if err != nil {
		t.Fatalf("project yaml: %v", err)
	}
	manifest, err := DecodeModelCapabilitiesYAML(body)
	if err != nil {
		t.Fatalf("decode projection: %v", err)
	}
	return body, manifest
}

func TestCheckViewPassesOnTheProjectionAndFailsOnOneClaimDrift(t *testing.T) {
	home, _ := promotionApplyHome(t)

	projection, manifest := decodeProjection(t, home)
	viewPath := filepath.Join(t.TempDir(), "view.yaml")
	if err := os.WriteFile(viewPath, projection, 0o644); err != nil {
		t.Fatalf("write view: %v", err)
	}

	// GREEN: the projection is a faithful view of authority.
	check, err := CheckModelCapabilitiesView(home, viewPath)
	if err != nil {
		t.Fatalf("check view: %v", err)
	}
	if !check.InSync || len(check.Drifts) != 0 {
		t.Fatalf("projection must pass its own drift check, got drifts: %+v", check.Drifts)
	}

	// RED CONTROL: flip exactly ONE claim level in the view. If the check still
	// passes, it is decorative and this test has caught that.
	entry := manifest.Models["auto/judge"]
	claim := entry.Capabilities["classify-extract"]
	if claim.Level != "capable" {
		t.Fatalf("fixture precondition: auto/judge classify-extract should start capable, got %q", claim.Level)
	}
	claim.Level = "assumed"
	caps := map[string]CapabilityClaim{}
	for k, v := range entry.Capabilities {
		caps[k] = v
	}
	caps["classify-extract"] = claim
	entry.Capabilities = caps
	manifest.Models["auto/judge"] = entry

	driftCheck, err := CheckModelCapabilitiesView(home, writeViewFrom(t, manifest))
	if err != nil {
		t.Fatalf("check drifted view: %v", err)
	}
	if driftCheck.InSync {
		t.Fatal("drift check passed a one-claim mutation — the assertion has no teeth")
	}
	if len(driftCheck.Drifts) != 1 || !strings.Contains(driftCheck.Drifts[0], "level") {
		t.Fatalf("one-claim drift should report exactly one level drift, got: %+v", driftCheck.Drifts)
	}
}

// Drift detection must cover every surface the OS view can diverge on, not just
// claim levels: a deleted row, a deleted claim, a changed routing chain, and a
// changed rule each have to turn the check red.
func TestCheckViewDetectsRowClaimDefaultAndRuleDrift(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(m *ModelCapabilitiesManifest)
		wantPart string
	}{
		{
			name: "deleted routing position",
			mutate: func(m *ModelCapabilitiesManifest) {
				m.RoutingDefaults["classify-extract"] = []string{"auto/judge"}
			},
			wantPart: "routing_default",
		},
		{
			name: "deleted model row",
			mutate: func(m *ModelCapabilitiesManifest) {
				delete(m.Models, "fedora/qwen3.6-35b-a3b")
			},
			wantPart: "absent from view",
		},
		{
			name: "deleted claim",
			mutate: func(m *ModelCapabilitiesManifest) {
				entry := m.Models["auto/judge"]
				caps := map[string]CapabilityClaim{}
				for k, v := range entry.Capabilities {
					if k != "judge-critique" {
						caps[k] = v
					}
				}
				entry.Capabilities = caps
				m.Models["auto/judge"] = entry
			},
			wantPart: "capability judge-critique",
		},
		{
			name: "changed rule",
			mutate: func(m *ModelCapabilitiesManifest) {
				m.Rules = []string{"free lanes may gate a verdict"}
			},
			wantPart: "rule list",
		},
		{
			name: "changed evidence",
			mutate: func(m *ModelCapabilitiesManifest) {
				entry := m.Models["auto/judge"]
				caps := map[string]CapabilityClaim{}
				for k, v := range entry.Capabilities {
					caps[k] = v
				}
				claim := caps["classify-extract"]
				claim.Evidence = "unsourced assertion"
				caps["classify-extract"] = claim
				entry.Capabilities = caps
				m.Models["auto/judge"] = entry
			},
			wantPart: "evidence",
		},
		{
			name: "changed tier",
			mutate: func(m *ModelCapabilitiesManifest) {
				entry := m.Models["auto/judge"]
				entry.Tier = "free"
				m.Models["auto/judge"] = entry
			},
			wantPart: "tier",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, _ := promotionApplyHome(t)
			_, manifest := decodeProjection(t, home)

			// Precondition: the in-sync view really is in sync before mutating,
			// so a red result below can only come from the mutation.
			base, err := CheckModelCapabilitiesView(home, writeViewFrom(t, manifest))
			if err != nil {
				t.Fatalf("baseline check: %v", err)
			}
			if !base.InSync {
				t.Fatalf("baseline view must be in sync, got: %+v", base.Drifts)
			}

			tc.mutate(&manifest)
			check, err := CheckModelCapabilitiesView(home, writeViewFrom(t, manifest))
			if err != nil {
				t.Fatalf("check view: %v", err)
			}
			if check.InSync {
				t.Fatalf("%s must turn the drift check red", tc.name)
			}
			if !strings.Contains(strings.Join(check.Drifts, "\n"), tc.wantPart) {
				t.Fatalf("%s should report a %q drift, got: %+v", tc.name, tc.wantPart, check.Drifts)
			}
		})
	}
}

// The projection tracks a governed apply: after the door changes a claim, the
// previously-faithful view is drifted, and a regenerated view is in sync again.
func TestProjectionFollowsGovernedApply(t *testing.T) {
	home, revision := promotionApplyHome(t)
	projection, err := ProjectModelCapabilitiesYAML(home)
	if err != nil {
		t.Fatalf("project yaml: %v", err)
	}
	stale := filepath.Join(t.TempDir(), "stale-view.yaml")
	if err := os.WriteFile(stale, projection, 0o644); err != nil {
		t.Fatalf("write stale view: %v", err)
	}

	reviewPath := promotionReviewFile(t, home, revision, "route_promotion_fixture_promote",
		promotionRecommendationPromote, "auto/judge", "classify-extract", "capable", "primary")
	plan, err := PlanPromotionApply(home, reviewPath, "operator rationale")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	_, pd := allowingDecision(t, home, "work_projection_fixture", plan, t0)
	if _, err := ApplyPromotionReview(home, "work_projection_fixture", plan, pd, t0); err != nil {
		t.Fatalf("apply: %v", err)
	}

	staleCheck, err := CheckModelCapabilitiesView(home, stale)
	if err != nil {
		t.Fatalf("check stale view: %v", err)
	}
	if staleCheck.InSync {
		t.Fatal("a view generated before a governed apply must read as drifted")
	}

	regenerated, err := ProjectModelCapabilitiesYAML(home)
	if err != nil {
		t.Fatalf("reproject: %v", err)
	}
	fresh := filepath.Join(t.TempDir(), "fresh-view.yaml")
	if err := os.WriteFile(fresh, regenerated, 0o644); err != nil {
		t.Fatalf("write fresh view: %v", err)
	}
	freshCheck, err := CheckModelCapabilitiesView(home, fresh)
	if err != nil {
		t.Fatalf("check fresh view: %v", err)
	}
	if !freshCheck.InSync {
		t.Fatalf("a regenerated view must be in sync, got: %+v", freshCheck.Drifts)
	}
}
