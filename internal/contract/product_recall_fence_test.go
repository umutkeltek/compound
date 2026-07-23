package contract

import (
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
		[]string{"SIBLING-FLAT", "SIBLING-ALIAS-TOPIC", "decision://DEC-SIBLING"},
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

func writeProductProjectContract(t *testing.T, root, slug string) {
	t.Helper()
	body := fmt.Sprintf("schema_version: 1\nproject:\n  slug: %s\nsource:\n  authority:\n    path: %s\n", slug, root)
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
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
