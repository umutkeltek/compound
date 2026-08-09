package routeevidence

import (
	"fmt"
	"strings"
	"time"

	"compound/internal/act"
	"compound/internal/envelope"
	"compound/internal/reviewartifact"
)

// The governed one-line undo for `route promotion-apply`.
//
// It does NOT re-derive "what the claim used to be" from the review. It loads
// the EXACT pre-image artifact the forward act captured and restores those bytes
// through act.ExecuteRevert under a freshly gated revert intent. Drift on the
// target path or on the post-image revision is a refusal: if the authority moved
// since the apply, this undo would silently discard whatever came after it.

// PromotionRevertPlan is the validated, pre-mutation description of one undo.
type PromotionRevertPlan struct {
	ReviewID        string `json:"review_id"`
	AppliedRecordID string `json:"applied_record_id"`
	AppliedPath     string `json:"applied_path"`
	LaneID          string `json:"lane_id"`
	CapabilityClass string `json:"capability_class"`

	CurrentClaimLevel  string `json:"current_claim_level"`
	RestoredClaimLevel string `json:"restored_claim_level"`

	AuthorityRef      string `json:"authority_ref"`
	AuthorityPath     string `json:"authority_path"`
	AuthorityRevision string `json:"authority_revision"`

	PreImageRef  string `json:"pre_image_ref"`
	PostImageRef string `json:"post_image_ref"`

	// PreImage is the exact captured pre-image blob (the act file pre-image
	// encoding), fetched from the content-addressed store by ref.
	PreImage []byte `json:"-"`
}

// PromotionRevertResult is what the CLI renders after a confirmed undo.
type PromotionRevertResult struct {
	Plan         PromotionRevertPlan `json:"plan"`
	ReceiptID    string              `json:"receipt_id"`
	TransitionID string              `json:"transition_id"`
	IntentID     string              `json:"action_intent_id"`
	PolicyID     string              `json:"policy_decision_id"`
	PreImageRef  string              `json:"pre_image_ref"`
	PostImageRef string              `json:"post_image_ref"`
}

// PlanPromotionRevert validates an undo and resolves the exact pre-image bytes.
// It writes nothing. Target drift, revision drift, a missing artifact, or a
// malformed record all fail here.
func PlanPromotionRevert(homePath, appliedPath string) (PromotionRevertPlan, error) {
	if strings.TrimSpace(homePath) == "" {
		return PromotionRevertPlan{}, fmt.Errorf("COMPOUND_HOME required")
	}
	record, err := LoadPromotionAppliedRecord(appliedPath)
	if err != nil {
		return PromotionRevertPlan{}, err
	}
	if strings.TrimSpace(record.Status) != "applied" {
		return PromotionRevertPlan{}, fmt.Errorf("route promotion applied record %s status %q is not revertible", record.ID, record.Status)
	}
	if strings.TrimSpace(record.PreImageRef) == "" || strings.TrimSpace(record.PostImageRef) == "" {
		return PromotionRevertPlan{}, fmt.Errorf("route promotion applied record %s carries no pre/post image refs", record.ID)
	}
	if strings.TrimSpace(record.ActionIntent.ID) == "" || record.ActionIntent.ID != record.ActionIntentID {
		return PromotionRevertPlan{}, fmt.Errorf("route promotion applied record %s has no coherent original action intent", record.ID)
	}

	manifest, authorityPath, authorityRevision, err := loadModelCapabilitiesSnapshot(homePath)
	if err != nil {
		return PromotionRevertPlan{}, fmt.Errorf("load model capabilities authority: %w", err)
	}
	// Target drift: the record's intent must still name the live authority file.
	if strings.TrimSpace(record.ActionIntent.Target.Path) != authorityPath {
		return PromotionRevertPlan{}, fmt.Errorf("route promotion applied record %s targets %s, but the live %s authority is %s — refusing to revert across targets",
			record.ID, record.ActionIntent.Target.Path, ModelCapabilitiesAuthorityRef, authorityPath)
	}
	// Revision drift: the authority must still be exactly what this apply left
	// behind. Anything else means a later change would be silently discarded.
	if authorityRevision != strings.TrimSpace(record.AuthorityRevisionAfter) {
		return PromotionRevertPlan{}, fmt.Errorf("route promotion applied record %s expected post-image revision %s, live authority revision is %s — refusing to revert over a newer change",
			record.ID, record.AuthorityRevisionAfter, authorityRevision)
	}

	current := ""
	if model, ok := manifest.Models[record.LaneID]; ok {
		current = strings.TrimSpace(model.Capabilities[record.CapabilityClass].Level)
	}

	store, err := act.NewArtifactStore(homePath)
	if err != nil {
		return PromotionRevertPlan{}, err
	}
	preImage, err := store.Get(record.PreImageRef)
	if err != nil {
		return PromotionRevertPlan{}, fmt.Errorf("resolve pre-image %s for route promotion applied record %s: %w", record.PreImageRef, record.ID, err)
	}

	return PromotionRevertPlan{
		ReviewID:           record.ReviewID,
		AppliedRecordID:    record.ID,
		AppliedPath:        appliedPath,
		LaneID:             record.LaneID,
		CapabilityClass:    record.CapabilityClass,
		CurrentClaimLevel:  current,
		RestoredClaimLevel: record.PriorClaimLevel,
		AuthorityRef:       ModelCapabilitiesAuthorityRef,
		AuthorityPath:      authorityPath,
		AuthorityRevision:  authorityRevision,
		PreImageRef:        record.PreImageRef,
		PostImageRef:       record.PostImageRef,
		PreImage:           preImage,
	}, nil
}

// PromotionRevertIntent builds the revert ActionIntent from the ORIGINAL forward
// intent recorded at apply time, through act.RevertIntent — so the undo is bound
// to the exact governed action it reverses.
func PromotionRevertIntent(workID string, record PromotionAppliedRecord, now time.Time) envelope.ActionIntent {
	orig := record.ActionIntent
	orig.IDs.WorkID = strings.TrimSpace(record.ActionIntent.IDs.WorkID)
	ai := act.RevertIntent(orig, act.Result{PreImageRef: record.PreImageRef, PostImageRef: record.PostImageRef}, now)
	ai.IDs.WorkID = workID
	return ai
}

// RevertPromotionApply performs the governed restore. It requires an allowing
// PolicyDecision for the revert intent; act.ExecuteRevert re-gates by
// construction. On success the applied record is marked reverted so the review
// is appliable again and the queue never shows a stale "applied" resolution.
func RevertPromotionApply(homePath, workID string, plan PromotionRevertPlan, pd envelope.PolicyDecision, now time.Time) (PromotionRevertResult, error) {
	if strings.TrimSpace(workID) == "" {
		return PromotionRevertResult{}, fmt.Errorf("work id required for a governed promotion revert")
	}
	record, err := LoadPromotionAppliedRecord(plan.AppliedPath)
	if err != nil {
		return PromotionRevertResult{}, err
	}
	store, err := act.NewArtifactStore(homePath)
	if err != nil {
		return PromotionRevertResult{}, err
	}
	ai := PromotionRevertIntent(workID, record, now)
	adapter := act.NewFileArtifactAdapter()
	res, err := act.ExecuteRevert(store, adapter, ai, pd, plan.PreImage, now)
	if err != nil {
		return PromotionRevertResult{}, err
	}

	// The restore moved the authority back to its pre-apply revision, so the
	// source-authority ledger head must follow. Without this the ledger would
	// still name the reverted-away revision and every later route plan or
	// promotion review would error out against a "restored" authority.
	restoredRevision := strings.TrimSpace(record.AuthorityRevisionBefore)
	if err := appendPromotionAuthorityRecord(homePath, plan.AuthorityPath, restoredRevision, promotionRevertCommandName, record.ReviewID, res); err != nil {
		return PromotionRevertResult{}, fmt.Errorf("authority restored to %s (receipt %s) but the source-authority ledger head could not be updated: %w",
			plan.RestoredClaimLevel, res.Receipt.ID, err)
	}
	record.Status = "reverted"
	record.UndoCommand = ""
	record.EvidenceRefs = reviewartifact.UniqueSorted(append(append([]string{}, record.EvidenceRefs...),
		res.Receipt.ID, res.Transition.ID, res.PreImageRef, res.PostImageRef))
	if _, werr := reviewartifact.WriteJSON(homePath, routePromotionReviewArea, record.ReviewID+promotionAppliedRecordSuffix, record, "  "); werr != nil {
		return PromotionRevertResult{}, fmt.Errorf("authority restored to %s (receipt %s) but the applied record could not be marked reverted: %w",
			plan.RestoredClaimLevel, res.Receipt.ID, werr)
	}

	return PromotionRevertResult{
		Plan:         plan,
		ReceiptID:    res.Receipt.ID,
		TransitionID: res.Transition.ID,
		IntentID:     ai.ID,
		PolicyID:     pd.ID,
		PreImageRef:  res.PreImageRef,
		PostImageRef: res.PostImageRef,
	}, nil
}
