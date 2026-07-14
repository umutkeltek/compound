package sourceobs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func validateSourceLifecycleEvidenceRefs(homePath string, proofs []SourceLifecycleProof) error {
	for _, proof := range proofs {
		if !sourceLifecycleGateRequiresEnvelopeEvidence(proof.Gate) {
			continue
		}
		transitionID, ok := strings.CutPrefix(strings.TrimSpace(proof.EvidenceRef), "transition:")
		if !ok {
			return fmt.Errorf("proof %s requires transition:<id> evidence", proof.Gate)
		}
		transitionID = strings.TrimSpace(transitionID)
		if transitionID == "" {
			return fmt.Errorf("proof %s requires a transition evidence id", proof.Gate)
		}
		if _, err := os.Stat(filepath.Join(homePath, "events", transitionID+".json")); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("evidence transition %s not found", transitionID)
			}
			return fmt.Errorf("read evidence transition %s: %w", transitionID, err)
		}
	}
	return nil
}

func sourceLifecycleGateRequiresEnvelopeEvidence(gate string) bool {
	switch strings.TrimSpace(gate) {
	case SourceLifecycleGateIsolatedRestore,
		SourceLifecycleGateSemanticParity,
		SourceLifecycleGateRealConsumerReplay,
		SourceLifecycleGateLegacyUnavailableNegativeControl,
		SourceLifecycleGateWriterRoster,
		SourceLifecycleGateWriterDestination,
		SourceLifecycleGateRollbackRehearsal,
		SourceLifecycleGatePrimaryWriterCutover,
		SourceLifecycleGateLegacyFreeze,
		SourceLifecycleGateQuietWindowMeasurement,
		SourceLifecycleGateArchiveVerified:
		return true
	default:
		return false
	}
}
