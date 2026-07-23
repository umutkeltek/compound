package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/candidates"
	"compound/internal/envelope"
	"compound/internal/mem"
)

func TestProductRecallFenceScopesFlatMemoryAndAcceptedDecisions(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "widget-alpha")
	workspace := filepath.Join(workspaceRoot, "ios-app", "Sources")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProductProjectContract(t, workspaceRoot, "widget-alpha")
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceCandidate("product:widget_alpha", "own_flat_fact", "OWN-FLAT release safety", "product", "status", ""),
		productFenceCandidate("product:widget_beta", "sibling_flat_fact", "SIBLING-FLAT release safety", "product", "status", ""),
		productFenceTopicRefCandidate("AliasRuleOwn", "own_alias_topic", "OWN-ALIAS-TOPIC release safety", "widget-alpha"),
		productFenceTopicRefCandidate("AliasRuleSibling", "sibling_alias_topic", "SIBLING-ALIAS-TOPIC release safety", "widget-beta"),
		productFenceCandidate("repo:widget_beta", "sibling_repo_entity", "SIBLING-REPO-ENTITY release safety", "standing-law", "architecture", ""),
		productFenceCandidate("UniversalReleaseRule", "global_cross_product_rule", "GLOBAL-CROSS-PRODUCT learned from widget-beta release safety", "standing-law", "architecture", ""),
		productFenceDecision("DEC-OWN", "OWN-DECISION release safety", "product:widget_alpha"),
		productFenceDecision("DEC-SIBLING", "SIBLING-DECISION release safety", "product:widget_beta"),
	})

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: workspace, Task: "release safety",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceMarkers(t, pack,
		[]string{"OWN-FLAT", "OWN-ALIAS-TOPIC", "GLOBAL-CROSS-PRODUCT", "decision://DEC-OWN"},
		[]string{"SIBLING-FLAT", "SIBLING-ALIAS-TOPIC", "SIBLING-REPO-ENTITY", "decision://DEC-SIBLING"},
	)
}

func TestProductRecallFenceResolvesCurrentEntityFromReleaseIdentity(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "legacy-client-folder")
	workspace := filepath.Join(workspaceRoot, "packages", "ios")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProductProjectContract(t, workspaceRoot, "legacy-widget-client")
	release := "schema_version: 1\nproject: legacy-widget-client\nrelease_identity:\n  bundle_id: com.example.widget-alpha\n"
	if err := os.WriteFile(filepath.Join(workspaceRoot, "release.yaml"), []byte(release), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceCandidate("product:widget_alpha", "surfaces", "appstore:com.example.widget-alpha", "product", "status", ""),
		productFenceCandidate("product:widget_alpha", "own_release_identity_fact", "OWN-RELEASE-ID release safety", "product", "status", ""),
		productFenceCandidate("product:widget_beta", "sibling_release_identity_fact", "SIBLING-RELEASE-ID release safety", "product", "status", ""),
	})

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: workspace, Task: "release safety",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceMarkers(t, pack,
		[]string{"OWN-RELEASE-ID"},
		[]string{"SIBLING-RELEASE-ID"},
	)
}

func TestProductRecallFenceScopesEveryProbeGroupIncludingAliasedProductRoot(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "widget-alpha")
	workspace := filepath.Join(workspaceRoot, "clients", "ios-app", "Sources")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProductProjectContract(t, workspaceRoot, "widget-alpha")
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	topics := []byte(`{
	  "schema_version": "compound.memory_topics.v0.1",
	  "topics": [
	    {"slug": "portfolio-products", "aliases": ["product"]},
	    {"slug": "standing-law"},
	    {"slug": "trap"},
	    {"slug": "opportunity"}
	  ]
	}`)
	if _, err := store.ImportTopics(topics, fixedNow); err != nil {
		t.Fatal(err)
	}
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceCandidate("product:widget_alpha", "own_product_probe", "OWN-PRODUCT product release", "portfolio-products", "status", ""),
		productFenceCandidate("product:widget_beta", "sibling_product_probe", "SIBLING-PRODUCT product release", "portfolio-products", "status", ""),
		productFenceCandidate("RuleOwn", "own_standing_law", "OWN-STANDING product release", "standing-law", "architecture", "product:widget_alpha"),
		productFenceCandidate("RuleSibling", "sibling_standing_law", "SIBLING-STANDING product release", "standing-law", "architecture", "product:widget_beta"),
		productFenceCandidate("TrapOwn", "own_trap", "OWN-TRAP product release", "trap", "architecture", "widget-alpha"),
		productFenceCandidate("TrapSibling", "sibling_trap", "SIBLING-TRAP product release", "trap", "architecture", "widget-beta"),
		productFenceCandidate("OpportunityOwn", "own_opportunity", "OWN-OPPORTUNITY product release", "opportunity", "architecture", "product:widget_alpha"),
		productFenceCandidate("OpportunitySibling", "sibling_opportunity", "SIBLING-OPPORTUNITY product release", "opportunity", "architecture", "product:widget_beta"),
		productFenceDecision("DEC-PROBE-OWN", "OWN-PROBE-DECISION product release", "widget-alpha"),
		productFenceDecision("DEC-PROBE-SIBLING", "SIBLING-PROBE-DECISION product release", "product:widget_beta"),
	})

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: workspace, Task: "product release",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceMarkers(t, pack,
		[]string{"OWN-PRODUCT", "OWN-STANDING", "OWN-TRAP", "OWN-OPPORTUNITY", "decision://DEC-PROBE-OWN"},
		[]string{"SIBLING-PRODUCT", "SIBLING-STANDING", "SIBLING-TRAP", "SIBLING-OPPORTUNITY", "decision://DEC-PROBE-SIBLING"},
	)
	if !strings.Contains(pack.SummaryMD, "Probe product-work/product:") {
		t.Fatalf("aliased product root did not remain active: %q", pack.SummaryMD)
	}
}

func TestProductRecallFenceFailsClosedWhenWorkspaceIdentityIsAbsentOrAmbiguous(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(t *testing.T, root string, store *mem.Store)
	}{
		{
			name: "absent",
			configure: func(t *testing.T, root string, store *mem.Store) {
				t.Helper()
			},
		},
		{
			name: "ambiguous",
			configure: func(t *testing.T, root string, store *mem.Store) {
				t.Helper()
				writeProductProjectContract(t, root, "widget-alpha")
				promoteProductFenceCandidates(t, store, []mem.Candidate{
					productFenceCandidate("repo:widget_beta", "git_root", root, "git_root", "status", ""),
				})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			workspaceRoot := filepath.Join(t.TempDir(), "workspace")
			workspace := filepath.Join(workspaceRoot, "nested", "client")
			if err := os.MkdirAll(workspace, 0o755); err != nil {
				t.Fatal(err)
			}
			store, err := mem.Open(home)
			if err != nil {
				t.Fatal(err)
			}
			promoteProductFenceCandidates(t, store, []mem.Candidate{
				productFenceCandidate("product:widget_alpha", "alpha_fact", "ALPHA-PRIVATE-SCOPE release safety", "product", "status", ""),
				productFenceCandidate("product:widget_beta", "beta_fact", "BETA-PRIVATE-SCOPE release safety", "product", "status", ""),
				productFenceDecision("DEC-ALPHA-SCOPE", "ALPHA-DECISION-SCOPE release safety", "product:widget_alpha"),
				productFenceDecision("DEC-BETA-SCOPE", "BETA-DECISION-SCOPE release safety", "product:widget_beta"),
				productFenceCandidate("UniversalReleaseRule", "global_rule", "GLOBAL-RULE release safety", "standing-law", "architecture", ""),
			})
			tc.configure(t, workspaceRoot, store)

			pack, err := PackWithCapabilities(Input{
				Harness: "codex", Cwd: workspace, Task: "release safety",
				RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
			}, DefaultPolicy(), home, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			assertProductFenceMarkers(t, pack,
				[]string{"GLOBAL-RULE"},
				[]string{"ALPHA-PRIVATE-SCOPE", "BETA-PRIVATE-SCOPE", "decision://DEC-ALPHA-SCOPE", "decision://DEC-BETA-SCOPE"},
			)
		})
	}
}

func TestProductRecallFenceDoesNotTrustWorkspaceClaimWithoutContract(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	workspace := filepath.Join(workspaceRoot, "nested", "client")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceCandidate("product:widget_alpha", "alpha_fact", "ALPHA-WORKSPACE-CLAIM release safety", "product", "status", ""),
		productFenceCandidate("product:widget_alpha", "git_root", workspaceRoot, "product", "status", ""),
		productFenceCandidate("UniversalReleaseRule", "global_rule", "GLOBAL-WITHOUT-CONTRACT release safety", "standing-law", "architecture", ""),
	})

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: workspace, Task: "release safety",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceMarkers(t, pack,
		[]string{"GLOBAL-WITHOUT-CONTRACT"},
		[]string{"ALPHA-WORKSPACE-CLAIM"},
	)
}

func TestProductRecallFenceNormalizesContractWordBoundariesForOneCanonicalIdentity(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "widget-alpha")
	workspace := filepath.Join(workspaceRoot, "client")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProductProjectContract(t, workspaceRoot, "widget-alpha")
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceCandidate("product:widget_alpha", "underscore_fact", "ALIAS-UNDERSCORE release safety", "product", "status", ""),
		productFenceCandidate("product:widget_beta", "beta_fact", "ALIAS-SIBLING release safety", "product", "status", ""),
	})

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: workspace, Task: "release safety",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceMarkers(t, pack,
		[]string{"ALIAS-UNDERSCORE"},
		[]string{"ALIAS-SIBLING"},
	)
}

func TestProductRecallFenceServedPackContainsNoSiblingProductIdentity(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "sample-claims")
	workspace := filepath.Join(workspaceRoot, "client")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProductProjectContractWithRepo(t, workspaceRoot, "sample-claims", "https://github.com/example/sample-claims")
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceCandidate("product:sample_claims", "surfaces", "https://github.com/example/sample-claims", "product", "status", ""),
		productFenceCandidate("product:sample_claims", "word_boundary_fact", "SAMPLE-CLAIMS-OWN release safety", "product", "status", ""),
		productFenceCandidate("product:sampleclaims", "surfaces", "https://github.com/example/sampleclaims", "product", "status", ""),
		productFenceCandidate("product:sampleclaims", "compact_collision_fact", "SAMPLE-CLAIMS-COMPACT release safety", "product", "status", ""),
		productFenceCandidate("product:sample_claims_ios", "sibling_fact", "SAMPLE-CLAIMS-IOS release safety", "product", "status", ""),
		productFenceCandidate("product:exampleapp", "exampleapp_sibling_fact", "EXAMPLEAPP-SIBLING release safety", "product", "status", ""),
		productFenceDecision("DEC-EXAMPLEAPP", "EXAMPLEAPP-DECISION release safety", "product:exampleapp"),
	})

	materialized, err := MaterializeRuntime(Input{
		Harness: "codex", Cwd: workspace, Task: "release safety",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	pack := materialized.ContextPack
	assertProductFenceMarkers(t, pack,
		[]string{"SAMPLE-CLAIMS-OWN"},
		[]string{"SAMPLE-CLAIMS-COMPACT", "SAMPLE-CLAIMS-IOS", "EXAMPLEAPP-SIBLING", "EXAMPLEAPP-DECISION", "decision://DEC-EXAMPLEAPP"},
	)
	assertServedProductFenceSerialization(t, "ContextPack", pack)
	assertServedProductFenceSerialization(t, "EffectiveContract", materialized.EffectiveContract)
}

func TestProductRecallFenceExactStructuredTokenOutranksCompactPrimary(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "wid-get-alpha")
	workspace := filepath.Join(workspaceRoot, "client")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProductProjectContractWithName(t, workspaceRoot, "wid-get-alpha", "widget-alpha")
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceCandidate("product:widget_alpha", "exact_name_fact", "EXACT-NAME-OWN release safety", "product", "status", ""),
		productFenceCandidate("product:widgetalpha", "compact_slug_fact", "COMPACT-SLUG-COLLISION release safety", "product", "status", ""),
	})

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: workspace, Task: "release safety",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceMarkers(t, pack,
		[]string{"EXACT-NAME-OWN"},
		[]string{"COMPACT-SLUG-COLLISION"},
	)
}

func TestProductRecallFenceUsesUniqueCompactFallbackWithoutIdentifier(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "wid-get-alpha")
	workspace := filepath.Join(workspaceRoot, "client")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProductProjectContract(t, workspaceRoot, "wid-get-alpha")
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceCandidate("product:widget_alpha", "unique_compact_fact", "UNIQUE-COMPACT-OWN release safety", "product", "status", ""),
		productFenceCandidate("product:widget_beta", "compact_sibling_fact", "UNIQUE-COMPACT-SIBLING release safety", "product", "status", ""),
	})

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: workspace, Task: "release safety",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceMarkers(t, pack,
		[]string{"UNIQUE-COMPACT-OWN"},
		[]string{"UNIQUE-COMPACT-SIBLING"},
	)
}

func TestProductRecallFenceUsesUniqueIdentifierToDisambiguateCompactFallback(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "wid-get-alpha")
	workspace := filepath.Join(workspaceRoot, "client")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProductProjectContractWithRepo(t, workspaceRoot, "wid-get-alpha", "https://github.com/example/widget-alpha")
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceCandidate("product:widget_alpha", "surfaces", "https://github.com/example/widget-alpha", "product", "status", ""),
		productFenceCandidate("product:widget_alpha", "identifier_fact", "IDENTIFIER-OWN release safety", "product", "status", ""),
		productFenceCandidate("product:widgetalpha", "compact_fact", "IDENTIFIER-COLLISION release safety", "product", "status", ""),
	})

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: workspace, Task: "release safety",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceMarkers(t, pack,
		[]string{"IDENTIFIER-OWN"},
		[]string{"IDENTIFIER-COLLISION"},
	)
}

func TestProductRecallFenceUnsupportedPunctuationCannotGainExactPrecedence(t *testing.T) {
	for _, slug := range []string{"widget/alpha", "widget.alpha"} {
		t.Run(slug, func(t *testing.T) {
			home := t.TempDir()
			workspaceRoot := filepath.Join(t.TempDir(), "unsupported-punctuation")
			workspace := filepath.Join(workspaceRoot, "client")
			if err := os.MkdirAll(workspace, 0o755); err != nil {
				t.Fatal(err)
			}
			writeProductProjectContract(t, workspaceRoot, slug)
			store, err := mem.Open(home)
			if err != nil {
				t.Fatal(err)
			}
			promoteProductFenceCandidates(t, store, []mem.Candidate{
				productFenceCandidate("product:widget_alpha", "punctuated_underscore_fact", "PUNCTUATION-UNDERSCORE release safety", "product", "status", ""),
				productFenceCandidate("product:widgetalpha", "punctuated_compact_fact", "PUNCTUATION-COMPACT release safety", "product", "status", ""),
				productFenceCandidate("UniversalReleaseRule", "punctuated_global_rule", "PUNCTUATION-GLOBAL release safety", "standing-law", "architecture", ""),
			})

			pack, err := PackWithCapabilities(Input{
				Harness: "codex", Cwd: workspace, Task: "release safety",
				RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
			}, DefaultPolicy(), home, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			assertProductFenceMarkers(t, pack,
				[]string{"PUNCTUATION-GLOBAL"},
				[]string{"PUNCTUATION-UNDERSCORE", "PUNCTUATION-COMPACT"},
			)
		})
	}
}

func TestProductRecallFenceFailsClosedWhenOnlyCompactIdentityIsAmbiguous(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "wid-get-alpha")
	workspace := filepath.Join(workspaceRoot, "client")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProductProjectContract(t, workspaceRoot, "wid-get-alpha")
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceCandidate("product:widget_alpha", "underscore_fact", "AMBIGUOUS-UNDERSCORE release safety", "product", "status", ""),
		productFenceCandidate("product:widgetalpha", "compact_fact", "AMBIGUOUS-COMPACT release safety", "product", "status", ""),
		productFenceCandidate("repo:widget_alpha", "git_root", workspaceRoot, "git_root", "status", ""),
		productFenceCandidate("UniversalReleaseRule", "global_rule", "AMBIGUOUS-GLOBAL release safety", "standing-law", "architecture", ""),
	})

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: workspace, Task: "release safety",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceMarkers(t, pack,
		[]string{"AMBIGUOUS-GLOBAL"},
		[]string{"AMBIGUOUS-UNDERSCORE", "AMBIGUOUS-COMPACT"},
	)
}

func TestProductRecallFenceDeniesUnknownStructuralAssociationButKeepsGlobalProse(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "widget-alpha")
	workspace := filepath.Join(workspaceRoot, "client")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProductProjectContract(t, workspaceRoot, "widget-alpha")
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceCandidate("product:widget_alpha", "own_fact", "OWN-STRUCTURAL release safety", "product", "status", ""),
		productFenceCandidate("RuleGhost", "ghost_scope", "UNKNOWN-STRUCTURAL release safety", "standing-law", "architecture", "product:ghost"),
		productFenceDecision("DEC-GHOST", "UNKNOWN-STRUCTURAL-DECISION release safety", "product:ghost"),
		productFenceCandidateWithQualifier("RuleEmpty", "empty_scope", "EMPTY-STRUCTURAL release safety", "product_id", ""),
		productFenceCandidateWithQualifier("RuleGeneric", "generic_scope", "GENERIC-STRUCTURAL release safety", "product_ref", "repo://other"),
		productFenceCandidate("UniversalReleaseRule", "global_product_example", "GLOBAL-PROSE mentions product:ghost release safety", "standing-law", "architecture", ""),
	})

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: workspace, Task: "release safety",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceMarkers(t, pack,
		[]string{"OWN-STRUCTURAL", "GLOBAL-PROSE"},
		[]string{"UNKNOWN-STRUCTURAL", "EMPTY-STRUCTURAL", "GENERIC-STRUCTURAL", "decision://DEC-GHOST"},
	)
}

func TestProductRecallFenceIgnoresGenericContractIdentifiers(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "legacy-client")
	workspace := filepath.Join(workspaceRoot, "client")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	project := "schema_version: 1\nproject:\n  slug: legacy-client\nsource:\n  authority:\n    repo: unknown\n"
	if err := os.WriteFile(filepath.Join(workspaceRoot, "project.yaml"), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceCandidate("product:widget_alpha", "surfaces", "unknown", "product", "status", ""),
		productFenceCandidate("product:widget_alpha", "alpha_fact", "GENERIC-ID-ALPHA release safety", "product", "status", ""),
		productFenceCandidate("UniversalReleaseRule", "global_rule", "GENERIC-ID-GLOBAL release safety", "standing-law", "architecture", ""),
	})

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: workspace, Task: "release safety",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceMarkers(t, pack,
		[]string{"GENERIC-ID-GLOBAL"},
		[]string{"GENERIC-ID-ALPHA"},
	)
}

func TestProductRecallFenceRecognizesKnownProductAliasesInScopeQualifiers(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "widget-alpha")
	workspace := filepath.Join(workspaceRoot, "client")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProductProjectContract(t, workspaceRoot, "widget-alpha")
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	scopeDecision := productFenceDecision("DEC-SCOPE-SIBLING", "SCOPE-SIBLING release safety", "")
	scopeDecision.Qualifiers["scope"] = "widget-beta"
	domainsDecision := productFenceDecision("DEC-DOMAINS-SIBLING", "DOMAINS-SIBLING release safety", "")
	domainsDecision.Qualifiers["domains"] = "ios,widget-beta"
	globalDecision := productFenceDecision("DEC-SCOPE-GLOBAL", "SCOPE-GLOBAL release safety", "")
	globalDecision.Qualifiers["scope"] = "architecture"
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceCandidate("product:widget_alpha", "own_fact", "SCOPE-OWN release safety", "product", "status", ""),
		productFenceCandidate("product:widget_beta", "sibling_fact", "SCOPE-BETA release safety", "product", "status", ""),
		scopeDecision,
		domainsDecision,
		globalDecision,
	})

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: workspace, Task: "release safety",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceMarkers(t, pack,
		[]string{"decision://DEC-SCOPE-GLOBAL"},
		[]string{"decision://DEC-SCOPE-SIBLING", "decision://DEC-DOMAINS-SIBLING"},
	)
}

func TestProductRecallFenceDeniesUnknownProductTokenInMultiValueScope(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "widget-alpha")
	workspace := filepath.Join(workspaceRoot, "client")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProductProjectContract(t, workspaceRoot, "widget-alpha")
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	candidatesToPromote := []mem.Candidate{
		productFenceCandidate("product:widget_alpha", "own_fact", "MULTI-SCOPE-OWN release safety", "product", "status", ""),
	}
	delimiters := []struct {
		name  string
		key   string
		value string
	}{
		{name: "comma unknown last", key: "scope", value: "architecture,product:ghost"},
		{name: "comma unknown first", key: "domain", value: "product:ghost,architecture"},
		{name: "semicolon unknown last", key: "domain", value: "architecture;product:ghost"},
		{name: "semicolon unknown first", key: "scope", value: "product:ghost;architecture"},
		{name: "pipe unknown last", key: "scope", value: "architecture|product:ghost"},
		{name: "pipe unknown first", key: "domain", value: "product:ghost|architecture"},
	}
	for i, tc := range delimiters {
		candidate := productFenceCandidateWithQualifier(
			fmt.Sprintf("RuleGhost%d", i),
			fmt.Sprintf("multi_scope_%d", i),
			fmt.Sprintf("MULTI-SCOPE-GHOST-%d release safety", i),
			tc.key,
			tc.value,
		)
		candidate.Qualifiers["fixture"] = tc.name
		candidatesToPromote = append(candidatesToPromote, candidate)
	}
	promoteProductFenceCandidates(t, store, candidatesToPromote)

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: workspace, Task: "release safety",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := make([]string, 0, len(delimiters))
	for i := range delimiters {
		forbidden = append(forbidden, fmt.Sprintf("MULTI-SCOPE-GHOST-%d", i))
	}
	assertProductFenceMarkers(t, pack,
		[]string{"MULTI-SCOPE-OWN"},
		forbidden,
	)
}

func TestProductRecallFenceStopsContractDiscoveryAtRepositoryCeiling(t *testing.T) {
	home := t.TempDir()
	ancestorRoot := filepath.Join(t.TempDir(), "unrelated-ancestor")
	repoRoot := filepath.Join(ancestorRoot, "nested-product")
	workspace := filepath.Join(repoRoot, "client")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProductProjectContract(t, ancestorRoot, "widget-alpha")
	markProductTestRepo(t, repoRoot)
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceCandidate("product:widget_alpha", "ancestor_fact", "ANCESTOR-CONTRACT-LEAK release safety", "product", "status", ""),
		productFenceCandidate("UniversalReleaseRule", "global_rule", "ANCESTOR-CONTRACT-GLOBAL release safety", "standing-law", "architecture", ""),
	})

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: workspace, Task: "release safety",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceMarkers(t, pack,
		[]string{"ANCESTOR-CONTRACT-GLOBAL"},
		[]string{"ANCESTOR-CONTRACT-LEAK"},
	)
}

func TestProductRecallFenceRejectsContractSymlinkEscapingRepositoryCeiling(t *testing.T) {
	home := t.TempDir()
	repoRoot := filepath.Join(t.TempDir(), "widget-alpha")
	workspace := filepath.Join(repoRoot, "client")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	markProductTestRepo(t, repoRoot)
	externalRoot := filepath.Join(t.TempDir(), "external-contract")
	if err := os.MkdirAll(externalRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProductProjectContract(t, externalRoot, "widget-alpha")
	if err := os.Symlink(filepath.Join(externalRoot, "project.yaml"), filepath.Join(repoRoot, "project.yaml")); err != nil {
		t.Fatal(err)
	}
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceCandidate("product:widget_alpha", "escaping_symlink_fact", "ESCAPING-CONTRACT-LEAK release safety", "product", "status", ""),
		productFenceCandidate("UniversalReleaseRule", "global_rule", "ESCAPING-CONTRACT-GLOBAL release safety", "standing-law", "architecture", ""),
	})

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: workspace, Task: "release safety",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceMarkers(t, pack,
		[]string{"ESCAPING-CONTRACT-GLOBAL"},
		[]string{"ESCAPING-CONTRACT-LEAK"},
	)
}

func TestProductRecallFenceCanonicalizesSymlinkedWorkspaceBeforeContractLookup(t *testing.T) {
	home := t.TempDir()
	realRoot := filepath.Join(t.TempDir(), "widget-alpha")
	realWorkspace := filepath.Join(realRoot, "ios-app", "Sources")
	if err := os.MkdirAll(realWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProductProjectContract(t, realRoot, "widget-alpha")

	logicalRoot := filepath.Join(t.TempDir(), "widget-beta")
	if err := os.MkdirAll(logicalRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProductProjectContract(t, logicalRoot, "widget-beta")
	logicalWorkspace := filepath.Join(logicalRoot, "client")
	if err := os.Symlink(filepath.Join(realRoot, "ios-app"), logicalWorkspace); err != nil {
		t.Fatal(err)
	}

	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceCandidate("product:widget_alpha", "alpha_fact", "PHYSICAL-ALPHA release safety", "product", "status", ""),
		productFenceCandidate("product:widget_beta", "beta_fact", "LOGICAL-BETA release safety", "product", "status", ""),
	})

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: filepath.Join(logicalWorkspace, "Sources"), Task: "release safety",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceMarkers(t, pack,
		[]string{"PHYSICAL-ALPHA"},
		[]string{"LOGICAL-BETA"},
	)
}

func TestProductRecallFenceRejectsInvalidNearestContracts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		project string
		release string
	}{
		{
			name:    "unsupported project schema",
			project: "schema_version: 2\nproject:\n  slug: widget-alpha\n",
		},
		{
			name:    "generic project slug",
			project: "schema_version: 1\nproject:\n  slug: unknown\n",
		},
		{
			name:    "duplicate project identity key",
			project: "schema_version: 1\nproject:\n  slug: widget-alpha\n  slug: widget-beta\n",
		},
		{
			name:    "merged conflicting project identity",
			project: "schema_version: 1\ndefaults: &defaults\n  slug: widget-beta\nproject:\n  <<: *defaults\n  slug: widget-alpha\n",
		},
		{
			name:    "malformed release",
			project: "schema_version: 1\nproject:\n  slug: widget-alpha\n",
			release: "schema_version: [1\n",
		},
		{
			name:    "conflicting release product",
			project: "schema_version: 1\nproject:\n  slug: widget-alpha\n",
			release: "schema_version: 1\nproject: ghost-product\n",
		},
		{
			name:    "compact-only release product collision",
			project: "schema_version: 1\nproject:\n  slug: widget-alpha\n",
			release: "schema_version: 1\nproject: widgetalpha\n",
		},
		{
			name:    "unsupported release schema",
			project: "schema_version: 1\nproject:\n  slug: widget-alpha\n",
			release: "schema_version: 2\nproject: widget-alpha\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			workspaceRoot := filepath.Join(t.TempDir(), "widget-alpha")
			workspace := filepath.Join(workspaceRoot, "client")
			if err := os.MkdirAll(workspace, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(workspaceRoot, "project.yaml"), []byte(tc.project), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.release != "" {
				if err := os.WriteFile(filepath.Join(workspaceRoot, "release.yaml"), []byte(tc.release), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			store, err := mem.Open(home)
			if err != nil {
				t.Fatal(err)
			}
			promoteProductFenceCandidates(t, store, []mem.Candidate{
				productFenceCandidate("product:widget_alpha", "alpha_fact", "INVALID-CONTRACT-ALPHA release safety", "product", "status", ""),
				productFenceCandidate("product:unknown", "unknown_fact", "INVALID-CONTRACT-UNKNOWN release safety", "product", "status", ""),
				productFenceCandidate("UniversalReleaseRule", "global_rule", "INVALID-CONTRACT-GLOBAL release safety", "standing-law", "architecture", ""),
			})

			pack, err := PackWithCapabilities(Input{
				Harness: "codex", Cwd: workspace, Task: "release safety",
				RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
			}, DefaultPolicy(), home, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			assertProductFenceMarkers(t, pack,
				[]string{"INVALID-CONTRACT-GLOBAL"},
				[]string{"INVALID-CONTRACT-ALPHA", "INVALID-CONTRACT-UNKNOWN"},
			)
		})
	}
}

func TestProductRecallFenceUsesNearestReleaseContractWithoutProjectContract(t *testing.T) {
	home := t.TempDir()
	parentRoot := filepath.Join(t.TempDir(), "widget-beta")
	workspace := filepath.Join(parentRoot, "nested-client", "Sources")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProductProjectContract(t, parentRoot, "widget-beta")
	release := "schema_version: 1\nproject: widget-alpha\n"
	if err := os.WriteFile(filepath.Join(parentRoot, "nested-client", "release.yaml"), []byte(release), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceCandidate("product:widget_alpha", "alpha_fact", "NEAREST-RELEASE-ALPHA release safety", "product", "status", ""),
		productFenceCandidate("product:widget_beta", "beta_fact", "PARENT-PROJECT-BETA release safety", "product", "status", ""),
	})

	pack, err := PackWithCapabilities(Input{
		Harness: "codex", Cwd: workspace, Task: "release safety",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceMarkers(t, pack,
		[]string{"NEAREST-RELEASE-ALPHA"},
		[]string{"PARENT-PROJECT-BETA"},
	)
}

func TestProductRecallFenceAppliesToEveryRuntimeContractAPI(t *testing.T) {
	home := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "widget-alpha")
	workspace := filepath.Join(workspaceRoot, "client")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProductProjectContract(t, workspaceRoot, "widget-alpha")
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceDecision("DEC-RUNTIME-OWN", "RUNTIME-OWN release safety", "product:widget_alpha"),
		productFenceDecision("DEC-RUNTIME-SIBLING", "RUNTIME-SIBLING release safety", "product:widget_beta"),
		productFenceCandidate("product:widget_alpha", "own_fact", "RUNTIME-OWN release safety", "product", "status", ""),
		productFenceCandidate("product:widget_beta", "sibling_fact", "RUNTIME-SIBLING release safety", "product", "status", ""),
	})
	in := Input{
		Harness: "codex", Cwd: workspace, Task: "release safety",
		RepoKind: "product", RepoKindVia: "flag", Mode: "execute",
	}

	materialized, err := MaterializeRuntime(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceMarkers(t, materialized.ContextPack,
		[]string{"RUNTIME-OWN", "decision://DEC-RUNTIME-OWN"},
		[]string{"RUNTIME-SIBLING", "decision://DEC-RUNTIME-SIBLING"},
	)
	assertProductFenceDecisionRefs(t, materialized.EffectiveContract.ActiveDecisions,
		"decision://DEC-RUNTIME-OWN", "decision://DEC-RUNTIME-SIBLING",
	)

	compiled, err := CompileWithRuntime(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceDecisionRefs(t, compiled.ActiveDecisions,
		"decision://DEC-RUNTIME-OWN", "decision://DEC-RUNTIME-SIBLING",
	)
}

func writeProductProjectContract(t *testing.T, root, slug string) {
	t.Helper()
	markProductTestRepo(t, root)
	body := fmt.Sprintf("schema_version: 1\nproject:\n  slug: %s\nsource:\n  authority:\n    path: %s\n", slug, root)
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeProductProjectContractWithRepo(t *testing.T, root, slug, repo string) {
	t.Helper()
	markProductTestRepo(t, root)
	body := fmt.Sprintf("schema_version: 1\nproject:\n  slug: %s\nsource:\n  authority:\n    path: %s\n    repo: %s\n", slug, root, repo)
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeProductProjectContractWithName(t *testing.T, root, slug, name string) {
	t.Helper()
	markProductTestRepo(t, root)
	body := fmt.Sprintf("schema_version: 1\nproject:\n  slug: %s\n  name: %s\nsource:\n  authority:\n    path: %s\n", slug, name, root)
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func markProductTestRepo(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertServedProductFenceSerialization(t *testing.T, outputName string, output any) {
	t.Helper()
	serialized, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	lowerSerialized := strings.ToLower(string(serialized))
	for _, forbidden := range []string{"exampleapp", "sampleclaims", "sample_claims_ios"} {
		if strings.Contains(lowerSerialized, forbidden) {
			t.Errorf("serialized %s leaked sibling product identity %q:\n%s", outputName, forbidden, serialized)
		}
	}
	if got := strings.Count(lowerSerialized, servedProductFenceExclusionRef); got != 1 {
		t.Errorf("serialized %s contains opaque product-fence sentinel %d times, want exactly once:\n%s", outputName, got, serialized)
	}
	if !strings.Contains(lowerSerialized, "control-plane://orientation-map") {
		t.Errorf("serialized %s erased non-product exclusion diagnostics:\n%s", outputName, serialized)
	}
}

func assertProductFenceDecisionRefs(t *testing.T, refs []string, want, forbidden string) {
	t.Helper()
	body := strings.Join(refs, "\n")
	if !strings.Contains(body, want) {
		t.Errorf("wanted decision ref %q absent:\n%s", want, body)
	}
	if strings.Contains(body, forbidden) {
		t.Errorf("forbidden decision ref %q crossed product fence:\n%s", forbidden, body)
	}
}

func productFenceCandidate(entity, predicate, object, topic, claimType, productID string) mem.Candidate {
	qualifiers := map[string]string{"domain": topic}
	if strings.TrimSpace(productID) != "" {
		qualifiers["product_id"] = productID
	}
	return mem.Candidate{
		Entity: entity, Predicate: predicate, Object: object,
		Confidence: 0.97, Derivation: "deterministic", ClaimType: claimType,
		Qualifiers: qualifiers, PromotionBasis: candidates.BasisCuratedPage,
		SourceTraceRef: "ev_" + predicate, SnippetHash: "sha256:" + predicate,
		Visibility: "operational", ObservationDate: "2026-07-01T00:00:00Z",
	}
}

func productFenceDecision(id, object, productID string) mem.Candidate {
	candidate := productFenceCandidate("decision:"+id, "decided", object, "decision", "decision", productID)
	candidate.Qualifiers["decision_id"] = id
	candidate.Qualifiers["lifecycle_status"] = "accepted"
	candidate.PromotionBasis = candidates.BasisDecisionRecord
	return candidate
}

func productFenceCandidateWithQualifier(entity, predicate, object, key, value string) mem.Candidate {
	candidate := productFenceCandidate(entity, predicate, object, "standing-law", "architecture", "")
	candidate.Qualifiers[key] = value
	return candidate
}

func productFenceTopicRefCandidate(entity, predicate, object, topicRef string) mem.Candidate {
	candidate := productFenceCandidate(entity, predicate, object, "standing-law", "architecture", "")
	candidate.TopicRefs = []string{topicRef}
	return candidate
}

func promoteProductFenceCandidates(t *testing.T, store *mem.Store, candidatesIn []mem.Candidate) {
	t.Helper()
	captured, err := store.Capture(candidatesIn, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range captured {
		if _, err := store.Adjudicate(candidate.ID, "promote", "test", fixedNow); err != nil {
			t.Fatal(err)
		}
	}
}

func assertProductFenceMarkers(t *testing.T, pack envelope.ContextPack, want, forbidden []string) {
	t.Helper()
	body := pack.SummaryMD + "\n" + strings.Join(pack.ActiveMemory, "\n") + "\n" + strings.Join(pack.ActiveDecisions, "\n")
	for _, position := range pack.ActivePositions {
		body += "\n" + position.Entity + "\n" + position.Topic + "\n" + position.Stance + "\n" + position.Line
	}
	for _, included := range pack.Included {
		body += "\n" + included.Ref + "\n" + included.Reason
	}
	for _, marker := range want {
		if !strings.Contains(body, marker) {
			t.Errorf("wanted marker %q absent from product context:\n%s", marker, body)
		}
	}
	for _, marker := range forbidden {
		if strings.Contains(body, marker) {
			t.Errorf("forbidden sibling marker %q crossed product fence:\n%s", marker, body)
		}
	}
}
