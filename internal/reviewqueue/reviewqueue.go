// Package reviewqueue is the canonical builder for the fully-annotated
// review-queue report that the compound CLI's various dispatchers
// (workbench, narrate, attention, reviews-resolve, orient) consume.
//
// Prior to this package, the assembly lived in cmd/compound as
// buildReviewQueueReport -- a 50-line function that called
// reviewartifact.Queue and then ran 24 package-level Annotate* calls
// across 16 internal packages. Eight different CLI dispatchers called
// it, so it was a hidden utility, not an orient-specific helper. This
// package makes the assembly canonical and reachable from anywhere in
// the repo.
//
// Composition:
//
//   - reviewqueue.Build(homePath, opts) returns a reviewartifact.QueueReport
//     fully annotated with every Annotate* pass that the CLI dispatchers
//     depend on.
//   - All callers share the same assembly; the queue report's content
//     is identical regardless of which CLI surface produced it.
//
// Concurrency / clock:
//
//   - The opts.Now field, when non-zero, is the single timestamp used for
//     queue loading AND authority stamping. This avoids the prior shape's
//     time.Now()-called-twice pattern (the loader and the stamper could
//     disagree by microseconds and produce internally-inconsistent
//     snapshots).
//   - When opts.Now is the zero value, the package falls back to
//     time.Now() at the start of Build, so callers without a supplied
//     `now` still work.
//
// Synthesis annotation:
//
//   - The synthesis annotator (cmd/compound/synthesis.go) is NOT included
//     here -- it is a synthesis concern, not a queue-assembly concern.
//     Callers that need synthesis annotations should run them on top of
//     the report returned by Build. The legacy buildReviewQueueReport
//     wrapper in cmd/compound (which 8 dispatchers call) preserves the
//     "synthesis for free" behavior by running both Build and the
//     synthesis annotator back-to-back.
package reviewqueue

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/authority"
	"compound/internal/capgraph"
	"compound/internal/decisions"
	"compound/internal/director"
	"compound/internal/distiller"
	"compound/internal/mem"
	"compound/internal/observation"
	"compound/internal/panel"
	"compound/internal/policy"
	"compound/internal/product"
	"compound/internal/projection"
	"compound/internal/reviewartifact"
	"compound/internal/routeevidence"
	"compound/internal/runtimebinding"
	"compound/internal/sourceobs"
	"compound/internal/work"
)

// Options narrows the queue report's read shape and pins the timestamp.
type Options struct {
	Area            string
	IncludeArchived bool
	Now             time.Time
}

// Build loads the review queue from COMPOUND_HOME and runs every
// per-domain Annotate* pass that the CLI dispatchers depend on. The
// returned QueueReport is read-only -- callers consume it as a
// projection, never mutate it.
func Build(homePath string, opts Options) (reviewartifact.QueueReport, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	report, err := reviewartifact.Queue(homePath, reviewartifact.QueueOptions{
		Area:            opts.Area,
		IncludeArchived: opts.IncludeArchived,
	}, now)
	if err != nil {
		return reviewartifact.QueueReport{}, err
	}
	decisions.AnnotateLifecycleRepairQueue(&report)
	decisions.AnnotateDecisionCandidateQueue(&report)
	decisions.AnnotateAutonomyGrantMintQueue(&report, homePath)
	product.AnnotateReviewQueue(&report)
	product.AnnotateOutboundDryRunEvidenceQueue(&report)
	product.AnnotateContentQueue(&report)
	authority.AnnotateJobHealthQueue(&report)
	authority.AnnotateSourceControlCleanupQueue(&report)
	director.AnnotateHarnessEditProposalQueue(&report)
	runtimebinding.AnnotateRepairQueue(&report)
	capgraph.AnnotateCapabilityEvaluationQueue(&report)
	capgraph.AnnotateExternalRepoIntakeQueue(&report)
	capgraph.AnnotateOutboundDistributionVerdictQueue(&report)
	capgraph.AnnotateOutboundDistributionDryRunQueue(&report)
	capgraph.AnnotateSkillCandidateQueue(&report)
	capgraph.AnnotateCapabilityLifecycleProposalQueue(&report)
	capgraph.AnnotateSkillSurfacingQueue(&report)
	distiller.AnnotateImprovementQueue(&report)
	distiller.AnnotateRequestQueue(&report)
	mem.AnnotateBakeoffQueue(&report)
	observation.AnnotateReviewQueue(&report)
	panel.AnnotateVerdictQueue(&report)
	panel.AnnotateCapabilityFeedbackQueue(&report)
	projection.AnnotateProjectionRegenerationQueue(&report)
	routeevidence.AnnotatePromotionReviewQueue(&report)
	routeevidence.AnnotateRouteOutcomeQueue(&report)
	sourceobs.AnnotateObservationVerdictQueue(&report)
	if err := StampReviewActionAuthority(homePath, &report, now); err != nil {
		return reviewartifact.QueueReport{}, err
	}
	reviewartifact.Recount(&report)
	return report, nil
}

func StampReviewActionAuthority(homePath string, report *reviewartifact.QueueReport, now time.Time) error {
	if report == nil {
		return fmt.Errorf("review authority report required")
	}
	for itemIndex := range report.Items {
		for actionIndex := range report.Items[itemIndex].Actions {
			action := &report.Items[itemIndex].Actions[actionIndex]
			action.PolicyDecision = nil
			action.PolicyAuthority = nil
		}
	}

	cfg, policySource, err := policy.LoadConfig(homePath)
	if err != nil {
		return fmt.Errorf("load current policy for review authority: %w", err)
	}
	workStore, err := work.Open(homePath)
	if err != nil {
		return fmt.Errorf("open work store for review authority: %w", err)
	}

	for itemIndex := range report.Items {
		for actionIndex := range report.Items[itemIndex].Actions {
			action := &report.Items[itemIndex].Actions[actionIndex]
			if action.ActionIntent == nil || action.AuthorityContext == nil {
				continue
			}
			if err := action.ActionIntent.Validate(); err != nil {
				continue
			}
			workID := strings.TrimSpace(action.ActionIntent.IDs.WorkID)
			if !strings.HasPrefix(workID, "work_") || strings.HasPrefix(workID, "work_live_") || filepath.Base(workID) != workID {
				continue
			}
			owner, err := workStore.Get(workID)
			if err != nil || owner.ID != workID || owner.Validate() != nil || reviewAuthorityTerminalWorkState(owner.State) {
				continue
			}

			evaluation := policy.EvaluateAuthority(
				*action.ActionIntent,
				cfg,
				policy.EvaluationContext{Authority: action.AuthorityContext},
				policySource,
				now,
			)
			if err := policy.ValidateAuthorityBundle(*action.ActionIntent, evaluation.Decision, evaluation.Authority); err != nil {
				continue
			}
			action.PolicyDecision = &evaluation.Decision
			action.PolicyAuthority = &evaluation.Authority
		}
	}
	return nil
}

func reviewAuthorityTerminalWorkState(state string) bool {
	switch strings.TrimSpace(state) {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}
