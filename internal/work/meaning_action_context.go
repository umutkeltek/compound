package work

import (
	"fmt"
	"path/filepath"
	"strings"

	"compound/internal/candidates"
	"compound/internal/envelope"
	"compound/internal/mem"
)

// promotedMeaningActionContext turns one unambiguous operational action directive
// already selected into the recorded ContextPack into immutable work context. The
// model/extractor may propose meaning, but only an adjudicated current claim with a
// decision/runtime basis can steer work; execution still goes through work policy.
func (s *Store) promotedMeaningActionContext(contextPackID string) (*WorkActionContext, error) {
	contextPackID = strings.TrimSpace(contextPackID)
	if contextPackID == "" {
		return nil, nil
	}

	var pack envelope.ContextPack
	if err := s.readJSON(filepath.Join(s.home, "context", "packs", contextPackID+".json"), &pack); err != nil {
		return nil, fmt.Errorf("read ContextPack %s for promoted meaning: %w", contextPackID, err)
	}
	if err := pack.Validate(); err != nil {
		return nil, fmt.Errorf("validate ContextPack %s for promoted meaning: %w", contextPackID, err)
	}
	active := make([]string, 0, len(pack.ActiveDecisions))
	for _, ref := range pack.ActiveDecisions {
		if ref = strings.TrimSpace(ref); ref != "" {
			active = append(active, ref)
		}
	}
	if len(active) == 0 {
		return nil, nil
	}

	memory, err := mem.Open(s.home)
	if err != nil {
		return nil, fmt.Errorf("open promoted meaning authority: %w", err)
	}
	claims, err := memory.AllCurrent()
	if err != nil {
		return nil, fmt.Errorf("read promoted meaning authority: %w", err)
	}
	currentByRef := currentActiveMeaningDecisions(claims)

	var matched *mem.Claim
	var matchedRef string
	for _, ref := range active {
		claimID, err := recordedDecisionClaimID(pack, ref)
		if err != nil {
			return nil, err
		}
		claim, ok := currentByRef[ref]
		if !ok || claim.ID != claimID {
			// A recorded decision revision that is no longer current cannot steer
			// newly-created work. Work already admitted from it keeps its snapshot.
			continue
		}
		if actionDirectiveDecisionRef(claim) != ref || !eligibleActionDirective(claim) {
			continue
		}
		// More than one active directive is ambiguity, not permission to pick one.
		if matched != nil {
			return nil, nil
		}
		matchedCopy := claim
		matched = &matchedCopy
		matchedRef = ref
	}
	if matched == nil {
		return nil, nil
	}

	decisionID := strings.TrimPrefix(matchedRef, "decision://")
	evidenceRefs := uniqueMeaningRefs(append([]string{matchedRef, "claim://" + matched.ID}, matched.EvidenceRefs...)...)
	return &WorkActionContext{
		Kind:          "promoted_action_directive",
		SourceRef:     matchedRef,
		SourceID:      decisionID,
		Title:         "Promoted action directive",
		ActionSummary: strings.TrimSpace(matched.Object),
		RequiredInputs: []string{
			"recorded ContextPack " + contextPackID,
			"current promoted decision " + matchedRef,
		},
		ExpectedValidation: []string{
			"a future Receipt and Transition prove the WorkEnvelope state delta",
		},
		StopConditions: []string{
			"the directive is absent from the recorded ContextPack",
			"the directive is no longer a current promoted claim",
			"the action would leave the WorkEnvelope policy grant",
		},
		Boundary:     "promoted meaning selects work context; it does not execute, admit work, or bypass PolicyDecision",
		Proof:        "the recorded create plan preserves decision and claim evidence refs; Receipt and Transition prove execution",
		EvidenceRefs: evidenceRefs,
	}, nil
}

// currentActiveMeaningDecisions mirrors the accepted/superseded decision wire
// contract without importing internal/decisions (which would create the
// decisions -> writeback -> work import cycle). If multiple accepted claims
// carry one decision id, the newest ledger revision is the current one, matching
// ContextPack construction.
func currentActiveMeaningDecisions(claims []mem.Claim) map[string]mem.Claim {
	superseded := map[string]bool{}
	for _, claim := range claims {
		if claim.ClaimType != "decision" || claim.Qualifiers == nil ||
			strings.ToLower(strings.TrimSpace(claim.Qualifiers["lifecycle_status"])) != "accepted" {
			continue
		}
		for _, id := range strings.Split(claim.Qualifiers["supersedes"], ",") {
			if id = strings.TrimSpace(id); id != "" {
				superseded[id] = true
			}
		}
	}
	byRef := map[string]mem.Claim{}
	for _, claim := range claims {
		if claim.ClaimType != "decision" || claim.Qualifiers == nil ||
			strings.ToLower(strings.TrimSpace(claim.Qualifiers["lifecycle_status"])) != "accepted" {
			continue
		}
		decisionID := strings.TrimSpace(claim.Qualifiers["decision_id"])
		if decisionID == "" || superseded[decisionID] {
			continue
		}
		ref := "decision://" + decisionID
		if prior, ok := byRef[ref]; !ok || claim.Seq > prior.Seq || (claim.Seq == prior.Seq && claim.ID > prior.ID) {
			byRef[ref] = claim
		}
	}
	return byRef
}

func eligibleActionDirective(claim mem.Claim) bool {
	if claim.ClaimType != "decision" || claim.Visibility != "operational" || claim.Qualifiers == nil ||
		strings.ToLower(strings.TrimSpace(claim.Qualifiers["lifecycle_status"])) != "accepted" ||
		mem.NormalizeClaimValue(claim.Predicate) != "action_directive" || strings.TrimSpace(claim.Object) == "" {
		return false
	}
	switch claim.PromotionBasis {
	case candidates.BasisExplicitConfirmation, candidates.BasisDecisionRecord, candidates.BasisRuntimeProof:
		return true
	default:
		return false
	}
}

func recordedDecisionClaimID(pack envelope.ContextPack, decisionRef string) (string, error) {
	var claimID string
	for _, revision := range pack.SourceRevisions {
		if strings.TrimSpace(revision.Source) != decisionRef {
			continue
		}
		rev := strings.TrimSpace(revision.Rev)
		if !strings.HasPrefix(rev, "claim:") || strings.TrimSpace(strings.TrimPrefix(rev, "claim:")) == "" {
			return "", fmt.Errorf("ContextPack %s decision %s has invalid claim revision %q", pack.ID, decisionRef, rev)
		}
		candidateID := strings.TrimSpace(strings.TrimPrefix(rev, "claim:"))
		if claimID != "" && claimID != candidateID {
			return "", fmt.Errorf("ContextPack %s decision %s has conflicting claim revisions", pack.ID, decisionRef)
		}
		claimID = candidateID
	}
	if claimID == "" {
		return "", fmt.Errorf("ContextPack %s decision %s is missing its exact claim revision", pack.ID, decisionRef)
	}
	return claimID, nil
}

func actionDirectiveDecisionRef(claim mem.Claim) string {
	decisionID := strings.TrimSpace(claim.Qualifiers["decision_id"])
	if decisionID == "" {
		return ""
	}
	return "decision://" + decisionID
}

func uniqueMeaningRefs(values ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
