package routeevidence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"compound/internal/act"
	"compound/internal/envelope"
	"compound/internal/reviewartifact"
	autonomyproof "compound/internal/routeevidence/autonomy"
)

// The governed promotion door.
//
// `route promotion-record` stays HOLD-ONLY: it records a reviewed
// hold_insufficient_evidence decision and never touches authority. This file is
// the separate, governed door that makes a REVIEWED promote/demote actually
// mutate compound://registries/model-router — through internal/act's single
// governed write seam, never through a bespoke os.WriteFile path.
//
// `route import-model-capabilities` remains the additive bridge/backfill input,
// not the authoring door: an import merges an external view in, while this door
// is how Compound itself changes one reviewed claim under a PolicyDecision.
const (
	PromotionAppliedRecordSchemaVersion = "compound.route_promotion_applied.v0.1"

	// PromotionApplyCapability is the capability an operator standing grant must
	// name to let `route promotion-apply --confirm` write. The grant scopes to
	// exactly this capability + the config_change class + the model-router path.
	PromotionApplyCapability = "authority.model_router.claim.write"

	promotionApplyAction         = "apply_model_router_claim"
	promotionApplyTargetKind     = "model_router_claim"
	promotionAppliedRecordSuffix = ".applied.json"
	promotionHoldRecordSuffix    = ".reviewed.json"
)

// PromotionApplyPlan is the fully validated, pre-mutation description of one
// reviewed claim change. Building it performs EVERY refusal check, so a dry-run
// and a --confirm run share one validation path and a refused apply can never
// have touched a byte.
type PromotionApplyPlan struct {
	ReviewID        string `json:"review_id"`
	ReviewPath      string `json:"review_path"`
	Recommendation  string `json:"recommendation"`
	LaneID          string `json:"lane_id"`
	CapabilityClass string `json:"capability_class"`
	Rationale       string `json:"rationale"`

	CurrentClaimLevel    string `json:"current_claim_level"`
	ProposedClaimLevel   string `json:"proposed_claim_level"`
	CurrentClaimEvidence string `json:"current_claim_evidence,omitempty"`
	ProposedEvidence     string `json:"proposed_claim_evidence"`

	AuthorityRef      string `json:"authority_ref"`
	AuthorityPath     string `json:"authority_path"`
	AuthorityRevision string `json:"authority_revision"`

	ModelCapabilitiesSourceID string `json:"model_capabilities_source_id,omitempty"`
	CriteriaSourceID          string `json:"criteria_source_id,omitempty"`
	CriteriaRevision          string `json:"criteria_revision,omitempty"`

	// ProposedYAML is the WHOLE validated manifest, already marshalled. The act
	// seam writes these exact bytes; nothing downstream re-derives them.
	ProposedYAML []byte `json:"-"`
}

// PromotionAppliedRecord is the review-side resolution written after a
// successful governed apply, so the owner-review queue resolves through the
// governed workflow instead of an operator hand-edit. It names the full proof
// chain (intent, decision, receipt, transition, pre/post image refs, authority
// revisions) and one literal runnable undo command.
type PromotionAppliedRecord struct {
	SchemaVersion   string `json:"schema_version"`
	ID              string `json:"id"`
	CreatedAt       string `json:"created_at"`
	Status          string `json:"status"`
	ReviewID        string `json:"review_id"`
	Decision        string `json:"decision"`
	Recommendation  string `json:"recommendation"`
	LaneID          string `json:"lane_id"`
	CapabilityClass string `json:"capability_class"`
	Rationale       string `json:"rationale"`

	PriorClaimLevel      string `json:"prior_claim_level"`
	AppliedClaimLevel    string `json:"applied_claim_level"`
	PriorClaimEvidence   string `json:"prior_claim_evidence,omitempty"`
	AppliedClaimEvidence string `json:"applied_claim_evidence"`

	AuthorityRef            string `json:"authority_ref"`
	AuthorityPath           string `json:"authority_path"`
	AuthorityRevisionBefore string `json:"authority_revision_before"`
	AuthorityRevisionAfter  string `json:"authority_revision_after"`

	ActionIntentID   string `json:"action_intent_id"`
	PolicyDecisionID string `json:"policy_decision_id"`
	ReceiptID        string `json:"receipt_id"`
	TransitionID     string `json:"transition_id"`
	PreImageRef      string `json:"pre_image_ref"`
	PostImageRef     string `json:"post_image_ref"`

	// ActionIntent is the EXACT forward intent this apply executed. The revert
	// door rebuilds its RevertIntent from this value rather than guessing an id,
	// so an undo is bound to the original governed action, not to a lookalike.
	ActionIntent envelope.ActionIntent `json:"action_intent"`

	ModelCapabilitiesSourceID string   `json:"model_capabilities_source_id,omitempty"`
	CriteriaSourceID          string   `json:"criteria_source_id,omitempty"`
	CriteriaRevision          string   `json:"criteria_revision,omitempty"`
	EvidenceRefs              []string `json:"evidence_refs"`
	Boundaries                []string `json:"boundaries"`

	UndoCommand string `json:"undo_command"`
	ReviewPath  string `json:"review_path,omitempty"`
	RecordPath  string `json:"record_path,omitempty"`
}

// PromotionApplyResult is what the CLI renders after a confirmed governed apply.
type PromotionApplyResult struct {
	Plan   PromotionApplyPlan     `json:"plan"`
	Record PromotionAppliedRecord `json:"record"`
}

// PromotionAppliedRecordPath names the review-side applied record for a review id.
func PromotionAppliedRecordPath(homePath, reviewID string) string {
	return filepath.Join(homePath, "reviews", routePromotionReviewArea, reviewID+promotionAppliedRecordSuffix)
}

func promotionHoldRecordPath(homePath, reviewID string) string {
	return filepath.Join(homePath, "reviews", routePromotionReviewArea, reviewID+promotionHoldRecordSuffix)
}

// PlanPromotionApply performs every refusal check and returns the exact
// before/after claim plus the whole proposed manifest. It writes NOTHING. Any
// stale, hold, malformed, mismatched, or already-resolved input fails here —
// before the act seam is ever reached — so the authority bytes, the
// source-authority ledger, and the event directory stay untouched.
func PlanPromotionApply(homePath, reviewPath, rationale string) (PromotionApplyPlan, error) {
	if strings.TrimSpace(homePath) == "" {
		return PromotionApplyPlan{}, fmt.Errorf("COMPOUND_HOME required")
	}
	rationale = strings.TrimSpace(rationale)
	if rationale == "" {
		return PromotionApplyPlan{}, fmt.Errorf("rationale required")
	}
	review, err := LoadPromotionReview(reviewPath)
	if err != nil {
		return PromotionApplyPlan{}, err
	}

	recommendation := strings.TrimSpace(review.Recommendation)
	if recommendation != promotionRecommendationPromote && recommendation != promotionRecommendationDemote {
		return PromotionApplyPlan{}, fmt.Errorf("route promotion review %s recommendation %q is not applicable; only %q and %q mutate authority (hold decisions belong to route promotion-record)",
			review.ID, recommendation, promotionRecommendationPromote, promotionRecommendationDemote)
	}
	// The emitted status for a recommendation is fixed by promotionReviewStatusFor:
	// promote surfaces as operator review-debt (review_required) and demote as an
	// automated authority outcome (recorded). Requiring the EXACT status the
	// evaluator would have emitted makes a hand-edited status a refusal, not a
	// silent pass.
	wantStatus := promotionReviewStatusFor(recommendation)
	if strings.TrimSpace(review.Status) != wantStatus {
		return PromotionApplyPlan{}, fmt.Errorf("route promotion review %s status %q is not appliable; recommendation %q requires status %q",
			review.ID, review.Status, recommendation, wantStatus)
	}

	laneID := strings.TrimSpace(review.LaneID)
	class := strings.TrimSpace(review.CapabilityClass)
	if laneID == "" || class == "" {
		return PromotionApplyPlan{}, fmt.Errorf("route promotion review %s missing lane_id or capability_class", review.ID)
	}
	proposedLevel := strings.TrimSpace(review.RecommendedClaimLevel)
	if proposedLevel == "" {
		return PromotionApplyPlan{}, fmt.Errorf("route promotion review %s has no recommended_claim_level to apply", review.ID)
	}
	if !autonomyproof.ValidClaimLevel(proposedLevel) {
		return PromotionApplyPlan{}, fmt.Errorf("route promotion review %s recommended_claim_level %q is not a known claim level", review.ID, proposedLevel)
	}
	if strings.TrimSpace(review.ModelCapabilitiesAuthorityRef) != ModelCapabilitiesAuthorityRef {
		return PromotionApplyPlan{}, fmt.Errorf("route promotion review %s targets authority %q, not %q",
			review.ID, review.ModelCapabilitiesAuthorityRef, ModelCapabilitiesAuthorityRef)
	}

	// A review already resolved (applied here, or hold-recorded through
	// promotion-record) is never re-appliable: one review, one resolution.
	if _, statErr := os.Stat(PromotionAppliedRecordPath(homePath, review.ID)); statErr == nil {
		return PromotionApplyPlan{}, fmt.Errorf("route promotion review %s already has an applied record at %s; revert it before applying again",
			review.ID, PromotionAppliedRecordPath(homePath, review.ID))
	}
	if _, statErr := os.Stat(promotionHoldRecordPath(homePath, review.ID)); statErr == nil {
		return PromotionApplyPlan{}, fmt.Errorf("route promotion review %s was already recorded as a hold at %s; it cannot also be applied",
			review.ID, promotionHoldRecordPath(homePath, review.ID))
	}

	manifest, authorityPath, authorityRevision, err := loadModelCapabilitiesSnapshot(homePath)
	if err != nil {
		return PromotionApplyPlan{}, fmt.Errorf("load model capabilities authority: %w", err)
	}
	// Staleness gate: the review must have been produced against the authority
	// revision that is live right now.
	reviewRevision := strings.TrimSpace(review.ModelCapabilitiesRevision)
	if reviewRevision == "" {
		return PromotionApplyPlan{}, fmt.Errorf("route promotion review %s carries no model_capabilities_revision; refusing to apply without authority provenance", review.ID)
	}
	if reviewRevision != authorityRevision {
		return PromotionApplyPlan{}, fmt.Errorf("route promotion review %s is stale: reviewed model-router revision %s, current authority revision %s — rerun route promotion-review",
			review.ID, reviewRevision, authorityRevision)
	}

	model, ok := manifest.Models[laneID]
	if !ok {
		return PromotionApplyPlan{}, fmt.Errorf("route promotion review %s names lane %q which is absent from %s", review.ID, laneID, ModelCapabilitiesAuthorityRef)
	}
	claim, ok := model.Capabilities[class]
	if !ok {
		return PromotionApplyPlan{}, fmt.Errorf("route promotion review %s names capability class %q which lane %q does not claim", review.ID, class, laneID)
	}
	currentLevel := strings.TrimSpace(claim.Level)
	if currentLevel != strings.TrimSpace(review.CurrentClaimLevel) {
		return PromotionApplyPlan{}, fmt.Errorf("route promotion review %s is stale: reviewed current claim %q, live claim %q for %s/%s",
			review.ID, review.CurrentClaimLevel, currentLevel, laneID, class)
	}
	if currentLevel == proposedLevel {
		return PromotionApplyPlan{}, fmt.Errorf("route promotion review %s would not change %s/%s (already %q)", review.ID, laneID, class, currentLevel)
	}

	proposedEvidence := promotionAppliedEvidence(review, rationale, claim.Evidence)
	proposed := manifestWithClaim(manifest, laneID, class, CapabilityClaim{Level: proposedLevel, Evidence: proposedEvidence})
	if err := validateModelCapabilities(proposed); err != nil {
		return PromotionApplyPlan{}, fmt.Errorf("proposed model capabilities invalid: %w", err)
	}
	body, err := yaml.Marshal(proposed)
	if err != nil {
		return PromotionApplyPlan{}, fmt.Errorf("marshal proposed model capabilities: %w", err)
	}

	return PromotionApplyPlan{
		ReviewID:                  review.ID,
		ReviewPath:                reviewPath,
		Recommendation:            recommendation,
		LaneID:                    laneID,
		CapabilityClass:           class,
		Rationale:                 rationale,
		CurrentClaimLevel:         currentLevel,
		ProposedClaimLevel:        proposedLevel,
		CurrentClaimEvidence:      strings.TrimSpace(claim.Evidence),
		ProposedEvidence:          proposedEvidence,
		AuthorityRef:              ModelCapabilitiesAuthorityRef,
		AuthorityPath:             authorityPath,
		AuthorityRevision:         authorityRevision,
		ModelCapabilitiesSourceID: strings.TrimSpace(review.ModelCapabilitiesSourceID),
		CriteriaSourceID:          strings.TrimSpace(review.CriteriaSourceID),
		CriteriaRevision:          strings.TrimSpace(review.CriteriaRevision),
		ProposedYAML:              body,
	}, nil
}

// promotionAppliedEvidence composes the installed claim's evidence string. It
// names the promotion review, the criteria source id/revision, and the operator
// rationale WITHOUT dropping the prior evidence — provenance accumulates, it is
// never overwritten.
func promotionAppliedEvidence(review PromotionReview, rationale, priorEvidence string) string {
	parts := []string{
		fmt.Sprintf("%s via promotion review %s", review.Recommendation, review.ID),
	}
	if id := strings.TrimSpace(review.CriteriaSourceID); id != "" {
		parts = append(parts, "criteria_source="+id)
	}
	if rev := strings.TrimSpace(review.CriteriaRevision); rev != "" {
		parts = append(parts, "criteria_revision="+rev)
	}
	parts = append(parts, "rationale="+rationale)
	if prior := strings.TrimSpace(priorEvidence); prior != "" {
		parts = append(parts, "prior="+prior)
	}
	return strings.Join(parts, "; ")
}

// manifestWithClaim returns a copy of the manifest with exactly ONE claim
// replaced. The models map and the touched entry's capabilities map are copied,
// so no other row, claim, routing default, or rule can be disturbed.
func manifestWithClaim(in ModelCapabilitiesManifest, laneID, class string, claim CapabilityClaim) ModelCapabilitiesManifest {
	out := in
	out.Models = make(map[string]ModelCapabilityEntry, len(in.Models))
	for id, entry := range in.Models {
		out.Models[id] = entry
	}
	entry := out.Models[laneID]
	caps := make(map[string]CapabilityClaim, len(entry.Capabilities))
	for k, v := range entry.Capabilities {
		caps[k] = v
	}
	caps[class] = claim
	entry.Capabilities = caps
	out.Models[laneID] = entry
	return out
}

// PromotionApplyIntent builds the governed ActionIntent for one reviewed claim
// change: config_change class, easy reversibility (the pre-image restores it),
// local blast radius, scoped to the model-router authority file.
func PromotionApplyIntent(workID string, plan PromotionApplyPlan, now time.Time) envelope.ActionIntent {
	target := envelope.ActionTarget{
		Kind: promotionApplyTargetKind,
		ID:   plan.LaneID + "/" + plan.CapabilityClass,
		Path: plan.AuthorityPath,
	}
	return envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            reviewartifact.ID("act_", workID, PromotionApplyCapability, plan.AuthorityPath, target.ID, plan.ReviewID, plan.ProposedClaimLevel),
			CreatedAt:     now.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         "operator",
		Capability:    PromotionApplyCapability,
		Action:        promotionApplyAction,
		Target:        target,
		MutationClass: "config_change",
		Reversibility: "easy",
		BlastRadius:   "local",
		Preview: fmt.Sprintf("%s %s/%s from %q to %q in %s (review %s)",
			plan.Recommendation, plan.LaneID, plan.CapabilityClass, plan.CurrentClaimLevel, plan.ProposedClaimLevel, ModelCapabilitiesAuthorityRef, plan.ReviewID),
		RollbackPlan: fmt.Sprintf("compound route promotion-revert --applied %s --work <work-id> --confirm (restores the captured pre-image of %s)",
			plan.ReviewID+promotionAppliedRecordSuffix, plan.AuthorityPath),
	}
}

// ApplyPromotionReview performs the governed write. It requires an ALREADY
// allowing PolicyDecision for this exact intent; act.Execute re-gates by
// construction, so a non-allowing decision leaves the authority byte-identical.
//
// After the act succeeds it writes the review-side applied record. If THAT
// persistence fails, the authority is restored through the SAME act seam
// (ExecuteRevert under a revert intent) rather than left mutated with unresolved
// review provenance; if the restore also fails, the error carries the pre-image
// ref for manual recovery.
func ApplyPromotionReview(homePath, workID string, plan PromotionApplyPlan, pd envelope.PolicyDecision, now time.Time) (PromotionApplyResult, error) {
	if strings.TrimSpace(workID) == "" {
		return PromotionApplyResult{}, fmt.Errorf("work id required for a governed promotion apply")
	}
	store, err := act.NewArtifactStore(homePath)
	if err != nil {
		return PromotionApplyResult{}, err
	}
	ai := PromotionApplyIntent(workID, plan, now)
	adapter := act.NewFileArtifactAdapter()
	res, err := act.Execute(store, adapter, ai, pd, plan.ProposedYAML, now)
	if err != nil {
		return PromotionApplyResult{}, err
	}

	afterRevision := "sha256:" + digestHex(res.PostImage)
	record := PromotionAppliedRecord{
		SchemaVersion:             PromotionAppliedRecordSchemaVersion,
		ID:                        reviewartifact.ID("rpar_", plan.ReviewID, res.Receipt.ID, plan.ProposedClaimLevel),
		CreatedAt:                 reviewartifact.Timestamp(now),
		Status:                    "applied",
		ReviewID:                  plan.ReviewID,
		Decision:                  plan.Recommendation + "_applied",
		Recommendation:            plan.Recommendation,
		LaneID:                    plan.LaneID,
		CapabilityClass:           plan.CapabilityClass,
		Rationale:                 plan.Rationale,
		PriorClaimLevel:           plan.CurrentClaimLevel,
		AppliedClaimLevel:         plan.ProposedClaimLevel,
		PriorClaimEvidence:        plan.CurrentClaimEvidence,
		AppliedClaimEvidence:      plan.ProposedEvidence,
		AuthorityRef:              ModelCapabilitiesAuthorityRef,
		AuthorityPath:             plan.AuthorityPath,
		AuthorityRevisionBefore:   plan.AuthorityRevision,
		AuthorityRevisionAfter:    afterRevision,
		ActionIntentID:            ai.ID,
		PolicyDecisionID:          pd.ID,
		ReceiptID:                 res.Receipt.ID,
		TransitionID:              res.Transition.ID,
		PreImageRef:               res.PreImageRef,
		PostImageRef:              res.PostImageRef,
		ActionIntent:              ai,
		ModelCapabilitiesSourceID: plan.ModelCapabilitiesSourceID,
		CriteriaSourceID:          plan.CriteriaSourceID,
		CriteriaRevision:          plan.CriteriaRevision,
		EvidenceRefs: reviewartifact.UniqueSorted([]string{
			res.PreImageRef, res.PostImageRef, res.Receipt.ID, res.Transition.ID,
			"compound://reviews/route-promotions/" + plan.ReviewID,
			ModelCapabilitiesAuthorityRef,
		}),
		Boundaries: []string{
			"only models[" + plan.LaneID + "].capabilities[" + plan.CapabilityClass + "] mutated",
			"no routing_defaults mutation",
			"no other lane row or claim mutated",
			"no provider or gateway config mutation",
		},
		ReviewPath: plan.ReviewPath,
	}
	record.UndoCommand = fmt.Sprintf("compound route promotion-revert --applied %s --work %s --confirm",
		PromotionAppliedRecordPath(homePath, plan.ReviewID), workID)

	path, werr := reviewartifact.WriteJSON(homePath, routePromotionReviewArea, plan.ReviewID+promotionAppliedRecordSuffix, record, "  ")
	if werr != nil {
		// The authority is mutated but its review provenance could not be
		// persisted. Restore through the SAME governed seam.
		return PromotionApplyResult{}, restoreAfterAppliedRecordFailure(store, adapter, ai, pd, res, werr, now)
	}
	record.RecordPath = path

	return PromotionApplyResult{Plan: plan, Record: record}, nil
}

// restoreAfterAppliedRecordFailure undoes a governed apply whose review-side
// record could not be written. The original PolicyDecision authorized this exact
// forward write, so restoring its own captured pre-image is an obligation-bound
// cleanup under that same decision — not a new ungoverned mutation.
func restoreAfterAppliedRecordFailure(store *act.ArtifactStore, adapter act.Adapter, ai envelope.ActionIntent, pd envelope.PolicyDecision, res act.Result, cause error, now time.Time) error {
	revertAI := act.RevertIntent(ai, res, now)
	revertPD := pd
	revertPD.ActionIntentID = revertAI.ID
	if _, rerr := act.ExecuteRevert(store, adapter, revertAI, revertPD, res.PreImage, now); rerr != nil {
		return fmt.Errorf("route promotion applied record could not be written (%v) AND the authority restore FAILED (%v) — %s is left at post-image %s; pre-image ref %s; MANUAL RECOVERY REQUIRED",
			cause, rerr, ai.Target.Path, res.PostImageRef, res.PreImageRef)
	}
	return fmt.Errorf("route promotion applied record could not be written (%w) — the authority was restored to pre-image %s through the act seam; no claim change is in effect", cause, res.PreImageRef)
}

// LoadPromotionAppliedRecord reads and schema-checks an applied record.
func LoadPromotionAppliedRecord(path string) (PromotionAppliedRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PromotionAppliedRecord{}, err
	}
	var record PromotionAppliedRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return PromotionAppliedRecord{}, fmt.Errorf("parse route promotion applied record %s: %w", path, err)
	}
	if record.SchemaVersion != PromotionAppliedRecordSchemaVersion {
		return PromotionAppliedRecord{}, fmt.Errorf("route promotion applied record %s schema_version = %q, want %q", path, record.SchemaVersion, PromotionAppliedRecordSchemaVersion)
	}
	if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.ReviewID) == "" {
		return PromotionAppliedRecord{}, fmt.Errorf("route promotion applied record %s missing id or review_id", path)
	}
	return record, nil
}
