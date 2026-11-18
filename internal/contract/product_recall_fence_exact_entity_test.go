package contract

import (
	"testing"

	"compound/internal/mem"
)

func TestProductRecallFenceKeepsCanonicalClaimWithCollidingAlias(t *testing.T) {
	workspace := t.TempDir()
	writeProductProjectContract(t, workspace, "sample-timer")
	stateHome := t.TempDir()
	store, err := mem.Open(stateHome)
	if err != nil {
		t.Fatal(err)
	}
	private := productFenceCandidate("product:sample_timer", "private_detail", "PRIVATE-DATA test listing", "product", "status", "")
	private.Visibility = "private"
	promoteProductFenceCandidates(t, store, []mem.Candidate{
		productFenceCandidate("product:sample_timer", "listing", "CANONICAL-ROW test listing", "product", "status", ""),
		productFenceCandidate("product:sample-timer", "listing", "FOREIGN-SPELLING test listing", "product", "status", ""),
		productFenceCandidate("product:sample_timer", "other_owner", "CONFLICTING-SCOPE test listing", "product", "status", "product:other_timer"),
		productFenceCandidate("product:other_timer", "listing", "SIBLING-ROW test listing", "product", "status", ""),
		productFenceCandidate("GlobalRecord", "ambiguous_owner", "AMBIGUOUS-ALIAS test listing", "product", "status", "sample-timer"),
		private,
	})
	input := Input{Harness: "codex", Cwd: workspace, Task: "Check the current test listing", RepoKind: "product", RepoKindVia: "flag", Mode: "execute"}
	pack, err := PackWithCapabilities(input, DefaultPolicy(), stateHome, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	assertProductFenceMarkers(t, pack,
		[]string{"CANONICAL-ROW"},
		[]string{"FOREIGN-SPELLING", "CONFLICTING-SCOPE", "SIBLING-ROW", "AMBIGUOUS-ALIAS", "PRIVATE-DATA"},
	)
}
