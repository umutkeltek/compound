package lanebeliefs

import (
	"fmt"
	"io"
	"sort"
)

// The WriteText methods are the operator text faces of the lanebeliefs verbs (--json prints the
// report itself). They live beside their reports so cmd/compound stays thin wiring.

// WriteText renders the `lanebeliefs fold` text summary.
func (r FoldReport) WriteText(w io.Writer) {
	fmt.Fprintf(w, "# lanebeliefs fold\n")
	fmt.Fprintf(w, "- raw_receipts: %d\n", r.RawReceiptCount)
	fmt.Fprintf(w, "- observations: %d\n", r.NewObservationCount)
	fmt.Fprintf(w, "- candidates: %d\n", r.CandidateCount)
	fmt.Fprintf(w, "- unresolved_receipts: %d\n", r.UnresolvedReceiptCount)
	fmt.Fprintf(w, "- below_floor_lanes: %d\n", r.BelowFloorLaneCount)
	fmt.Fprintf(w, "- valve_dropped: %d\n", r.ValveDroppedCount)
	for _, drop := range r.ValveDrops {
		fmt.Fprintf(w, "  - dropped %s %s: %s\n", drop.Kind, drop.Domain, drop.Reason)
	}
	fmt.Fprintf(w, "- cursor: %d -> %d\n", r.CursorBefore, r.CursorAfter)
}

// WriteText renders the `lanebeliefs promote` text summary.
func (r PromoteReport) WriteText(w io.Writer) {
	fmt.Fprintf(w, "# lanebeliefs promote\n")
	if r.DryRun {
		fmt.Fprintf(w, "- dry_run: true (no writes)\n")
	}
	fmt.Fprintf(w, "- evaluated: %d\n", r.EvaluatedCount)
	fmt.Fprintf(w, "- promoted: %d\n", r.PromotedCount)
	fmt.Fprintf(w, "- refused: %d\n", r.RefusedCount)
	for _, reason := range sortedCountKeys(r.ReasonCounts) {
		fmt.Fprintf(w, "  - %s: %d\n", reason, r.ReasonCounts[reason])
	}
	for _, res := range r.Results {
		if res.Disposition == DispositionPromoted {
			fmt.Fprintf(w, "%s %s.%s[%s] %s -> promoted basis=%s visibility=%s\n",
				res.CandidateID, res.Subject, res.Domain, res.Valence, res.Kind, res.PromotionBasis, res.Visibility)
		} else {
			fmt.Fprintf(w, "%s %s.%s[%s] %s -> parked (%s) works=%d posterior=%.4f mark=%t\n",
				res.CandidateID, res.Subject, res.Domain, res.Valence, res.Kind, res.Reason, res.WorkCount, res.PosteriorMean, res.HasCrossLaneMark)
		}
	}
}

// WriteText renders the `lanebeliefs cool` text summary.
func (r CoolReport) WriteText(w io.Writer) {
	fmt.Fprintf(w, "# lanebeliefs cool\n")
	if r.DryRun {
		fmt.Fprintf(w, "- dry_run: true (no writes)\n")
	}
	fmt.Fprintf(w, "- run_count: %d (cool_after_folds=%d)\n", r.RunCount, r.CoolAfterFolds)
	fmt.Fprintf(w, "- evaluated: %d\n", r.EvaluatedCount)
	fmt.Fprintf(w, "- cooled: %d\n", r.CooledCount)
	fmt.Fprintf(w, "- skipped: %d\n", r.SkippedCount)
	for _, res := range r.Results {
		if res.Disposition == CoolDispositionCooled {
			fmt.Fprintf(w, "%s %s.%s[%s] observed -> cooled (%s) quiet=%d claim=%s basis=%s visibility=%s\n",
				res.ObservedClaimID, res.Subject, res.Domain, res.Valence, res.Reason, res.QuietFolds, res.CooledClaimID, res.PromotionBasis, res.Visibility)
		} else {
			fmt.Fprintf(w, "%s %s.%s[%s] observed -> kept (%s) quiet=%d last_reinforced=%d\n",
				res.ObservedClaimID, res.Subject, res.Domain, res.Valence, res.Reason, res.QuietFolds, res.LastReinforcedRun)
		}
	}
}

// WriteText renders the `lanebeliefs posterior` text summary.
func (r CompletionPosteriorReport) WriteText(w io.Writer) {
	fmt.Fprintf(w, "# lanebeliefs posterior\n")
	fmt.Fprintf(w, "- lane: %s\n", r.LaneID)
	fmt.Fprintf(w, "- capability_class: %s\n", r.CapabilityClass)
	fmt.Fprintf(w, "- attempts: %d (completions %d, failures %d)\n", r.Attempts, r.Completions, r.Failures)
	fmt.Fprintf(w, "- receipts: %d (meets_floor=%t)\n", r.ReceiptCount, r.MeetsFloor)
	fmt.Fprintf(w, "- posterior_mean: %.4f (prior Beta(%d,%d), prior_only=%t)\n", r.PosteriorMean, r.PriorAlpha, r.PriorBeta, r.PriorOnly)
}

func sortedCountKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
