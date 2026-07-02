package panel

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"compound/internal/reviewartifact"
)

const verdictReviewArea = "panel-verdicts"

// AnnotateVerdictQueue makes recorded panel verdicts legible in the shared
// review queue without applying any verdict or mutating memory authority.
func AnnotateVerdictQueue(report *reviewartifact.QueueReport) {
	if report == nil {
		return
	}
	for i := range report.Items {
		item := &report.Items[i]
		if item.Area != verdictReviewArea {
			continue
		}
		annotateVerdictQueueItem(item)
	}
}

func annotateVerdictQueueItem(item *reviewartifact.QueueItem) {
	verdict, ok := verdictFromQueueItem(*item)
	if !ok {
		item.Readiness = "blocked"
		item.BlockedReason = "panel verdict artifact unreadable"
		item.NextAction = "regenerate panel verdict artifacts"
		item.Actions = nil
		return
	}

	item.EvidenceRefs = reviewartifact.UniqueSorted(append(item.EvidenceRefs, verdict.EvidenceRefs...))
	item.Actions = nil

	switch strings.TrimSpace(item.Status) {
	case "ok", "applied", "resolved", "accepted", "recorded":
		item.Readiness = "no_action"
		item.BlockedReason = ""
		item.NextAction = "panel verdict artifact terminal"
		return
	}

	decision := strings.TrimSpace(verdict.Synthesis.Decision)
	if decision == "" {
		item.Readiness = "blocked"
		item.BlockedReason = "panel verdict missing synthesis.decision"
		item.NextAction = "regenerate panel verdict artifact with synthesis decision"
		return
	}

	item.Readiness = "needs_review"
	item.BlockedReason = ""
	target := panelVerdictTarget(verdict.Target)
	failedLenses := failedLensCount(verdict.LensFindings)
	switch decision {
	case "needs_human":
		item.NextAction = fmt.Sprintf("owner review required; panel requested human decision (target=%s decision=%s failed_lenses=%d)", target, decision, failedLenses)
	case "promote", "park", "reject":
		item.NextAction = fmt.Sprintf("owner review required before panel apply (target=%s decision=%s failed_lenses=%d)", target, decision, failedLenses)
	default:
		item.NextAction = fmt.Sprintf("owner review required for panel verdict (target=%s decision=%s failed_lenses=%d)", target, decision, failedLenses)
	}
}

func verdictFromQueueItem(item reviewartifact.QueueItem) (verdictArtifactPayload, bool) {
	if strings.TrimSpace(item.JSONPath) == "" {
		return verdictArtifactPayload{}, false
	}
	data, err := os.ReadFile(item.JSONPath)
	if err != nil {
		return verdictArtifactPayload{}, false
	}
	var verdict verdictArtifactPayload
	if err := json.Unmarshal(data, &verdict); err != nil {
		return verdictArtifactPayload{}, false
	}
	return verdict, strings.TrimSpace(verdict.ID) != ""
}

func panelVerdictTarget(target TargetInput) string {
	kind := strings.TrimSpace(target.Kind)
	ref := strings.TrimSpace(target.Ref)
	switch {
	case kind != "" && ref != "":
		return kind + "/" + ref
	case kind != "":
		return kind
	case ref != "":
		return ref
	default:
		return "unknown"
	}
}

func failedLensCount(findings []LensFinding) int {
	count := 0
	for _, finding := range findings {
		if strings.TrimSpace(finding.Status) == "failed" {
			count++
		}
	}
	return count
}
