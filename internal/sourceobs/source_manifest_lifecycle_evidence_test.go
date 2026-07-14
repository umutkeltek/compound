package sourceobs

import (
	"strings"
	"testing"
	"time"
)

func TestSourceManifestLifecycleRejectsDanglingHighRiskEvidenceWithoutMutation(t *testing.T) {
	homePath, family, _ := lifecycleOwnedManifest(t)
	mustAdvanceLifecycle(t, homePath, family, SourceLifecycleCapturing, lifecycleProofs(SourceLifecycleGateSourceInventory), nil, lifecycleTestNow)
	mustAdvanceLifecycle(t, homePath, family, SourceLifecycleOwnedSnapshot, lifecycleProofs(SourceLifecycleGateExactCustody), nil, lifecycleTestNow.Add(time.Minute))

	proofs := lifecycleProofs(
		SourceLifecycleGateIsolatedRestore,
		SourceLifecycleGateSemanticParity,
		SourceLifecycleGateRealConsumerReplay,
		SourceLifecycleGateLegacyUnavailableNegativeControl,
	)
	for i := range proofs {
		proofs[i].EvidenceRef = "transition:tr_missing_evidence"
	}
	assertLifecycleAdvanceFailsUnchanged(
		t,
		homePath,
		family,
		SourceLifecycleVerified,
		proofs,
		nil,
		"evidence transition tr_missing_evidence",
	)

	manifest, err := LoadSourceManifest(homePath, family)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Lifecycle.State != SourceLifecycleOwnedSnapshot || manifest.Lifecycle.Revision != 2 {
		t.Fatalf("dangling evidence mutated lifecycle: %+v", manifest.Lifecycle)
	}
	if strings.TrimSpace(manifest.AuthorityRef) == "" {
		t.Fatal("test manifest lost authority ref")
	}
}

func TestSourceManifestLifecycleRejectsAssertionShapedHighRiskProofs(t *testing.T) {
	homePath, family, _ := lifecycleOwnedManifest(t)
	mustAdvanceLifecycle(t, homePath, family, SourceLifecycleCapturing, lifecycleProofs(SourceLifecycleGateSourceInventory), nil, lifecycleTestNow)
	mustAdvanceLifecycle(t, homePath, family, SourceLifecycleOwnedSnapshot, lifecycleProofs(SourceLifecycleGateExactCustody), nil, lifecycleTestNow.Add(time.Minute))

	proofs := lifecycleProofs(
		SourceLifecycleGateIsolatedRestore,
		SourceLifecycleGateSemanticParity,
		SourceLifecycleGateRealConsumerReplay,
		SourceLifecycleGateLegacyUnavailableNegativeControl,
	)
	assertLifecycleAdvanceFailsUnchanged(
		t,
		homePath,
		family,
		SourceLifecycleVerified,
		proofs,
		nil,
		"requires transition:<id> evidence",
	)
}
