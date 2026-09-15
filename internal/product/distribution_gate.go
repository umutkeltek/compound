package product

import (
	"fmt"
	"os"
	"strings"
)

// distribution_gate.go marks the distribution loop as EXPERIMENTAL at its doors.
//
// A 2026-09-01 external release audit found that the distribution path, while more
// capable than before, is not yet truthful end to end: a scheduling acknowledgement
// is recorded as a publication, attached media never reaches the outbound request,
// operator approval is not bound to the exact media package, and a failed local
// write after a successful remote call can lead to a duplicate public post.
//
// The response is not to delete the work — it is the operator's revenue path — but
// to stop it being mistaken for a supported v0.1 surface. A comment saying
// "experimental" does not gate anything; an explicit opt-in does.
const DistributionExperimentEnv = "COMPOUND_EXPERIMENTAL_DISTRIBUTION"

// KnownDistributionGaps are the audited defects a caller is opting into. They are
// listed at the door so nobody enables the flag without seeing what is still wrong.
//
// 2026-09-15 audit-reconcile: the first audited gap ("a scheduling
// acknowledgement is recorded as posted") is CLOSED — a transport scheduler's
// acceptance now records the distinct non-terminal "scheduled" state with the
// scheduler's own reservation id (MarkContentDraftScheduled), the classifier
// refuses an ambiguous post-image outright, and only a platform answer with a
// real remote id reaches "posted". The remaining gaps are unchanged.
var KnownDistributionGaps = []string{
	"attached media is not carried in the outbound transport request",
	"operator approval is not bound to the exact media package digest",
	"a failed local write after a successful remote call can allow a duplicate post",
	"draft freshness is checked at approval, not immediately before publication",
	"a scheduling ack without an explicit marker or reservation-id key is still indistinguishable from an unmarked post answer (documented classification limit)",
}

// RequireDistributionExperiment refuses a distribution door unless the operator has
// explicitly opted in. The refusal names the gaps rather than just the flag: an
// opt-in nobody understands is not consent.
func RequireDistributionExperiment(verb string) error {
	if strings.TrimSpace(os.Getenv(DistributionExperimentEnv)) == "1" {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s is part of the EXPERIMENTAL distribution loop and is not a supported surface.\n", verb)
	fmt.Fprintf(&b, "Set %s=1 to use it. Known gaps you would be accepting:\n", DistributionExperimentEnv)
	for _, gap := range KnownDistributionGaps {
		fmt.Fprintf(&b, "  - %s\n", gap)
	}
	return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}
