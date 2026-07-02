package capgraph

import (
	"strings"

	"compound/internal/reviewartifact"
)

const capabilityEvaluationReviewArea = "capability-evaluation-intake"

// AnnotateCapabilityEvaluationQueue makes reviewed capability-evaluation plans
// terminal in the shared queue without importing manifests or mutating graph
// offers.
func AnnotateCapabilityEvaluationQueue(report *reviewartifact.QueueReport) {
	if report == nil {
		return
	}
	for i := range report.Items {
		item := &report.Items[i]
		if item.Area != capabilityEvaluationReviewArea {
			continue
		}
		annotateCapabilityEvaluationQueueItem(item)
	}
}

func annotateCapabilityEvaluationQueueItem(item *reviewartifact.QueueItem) {
	item.EvidenceRefs = reviewartifact.UniqueSorted(item.EvidenceRefs)
	switch strings.TrimSpace(item.Status) {
	case "recorded", "candidate_recorded", "ok", "resolved", "accepted", "applied":
		item.Readiness = "no_action"
		item.BlockedReason = ""
		item.NextAction = "capability evaluation review recorded; graph mutation requires separate reviewed manifest import"
		item.Actions = nil
	}
}
