package routeevidence

import (
	"encoding/json"
	"os"
	"strings"

	"compound/internal/reviewartifact"
)

// AnnotatePromotionReviewQueue makes route-promotion review artifacts actionable
// without applying lane promotions or mutating routing authority.
func AnnotatePromotionReviewQueue(report *reviewartifact.QueueReport) {
	if report == nil {
		return
	}
	for i := range report.Items {
		item := &report.Items[i]
		if item.Area != routePromotionReviewArea {
			continue
		}
		annotatePromotionReviewQueueItem(item)
	}
}

func annotatePromotionReviewQueueItem(item *reviewartifact.QueueItem) {
	review, ok := promotionReviewFromQueueItem(*item)
	if !ok {
		item.Readiness = "blocked"
		item.BlockedReason = "route promotion review artifact unreadable"
		item.NextAction = "regenerate route promotion-review artifacts"
		item.Actions = nil
		return
	}
	item.EvidenceRefs = reviewartifact.UniqueSorted(review.EvidenceRefs)
	if reviewedPath, holdReport, ok := routePromotionReviewedHold(*item, review); ok {
		item.Status = "recorded"
		item.Readiness = "review_recorded"
		item.BlockedReason = ""
		item.Actions = nil
		item.NextAction = "route promotion hold review recorded"
		item.EvidenceRefs = reviewartifact.UniqueSorted(append(item.EvidenceRefs, holdReport.EvidenceRefs...))
		item.NextAction = reviewartifact.FirstNonEmpty(holdReport.NextAction, item.NextAction)
		item.ExtraPaths = reviewartifact.UniqueSorted(append(item.ExtraPaths, reviewedPath))
		return
	}
	switch strings.TrimSpace(review.Status) {
	case "ok", "applied", "resolved", "accepted", "recorded":
		item.Readiness = "no_action"
		item.NextAction = "route promotion review artifact terminal"
		item.Actions = nil
	case "review_required":
		item.Readiness = "needs_review"
		item.BlockedReason = ""
		item.Actions = nil
		switch review.Recommendation {
		case promotionRecommendationHoldInsufficient:
			item.NextAction = "owner review: hold lane promotion until more route evidence meets criteria"
		case promotionRecommendationHoldFailing:
			item.NextAction = "owner review: hold or demote lane after failing-rate evidence review"
		case promotionRecommendationPromote, promotionRecommendationDemote:
			item.NextAction = "owner review required before separate route/model authority mutation"
		default:
			item.NextAction = "owner review required for route promotion recommendation"
		}
	default:
		item.Readiness = "needs_review"
		item.NextAction = "owner review required for route promotion artifact"
		item.Actions = nil
	}
}

func routePromotionReviewedHold(item reviewartifact.QueueItem, review PromotionReview) (string, PromotionHoldReviewReport, bool) {
	for _, path := range item.ExtraPaths {
		if strings.HasSuffix(path, ".reviewed.json") {
			report, err := LoadPromotionHoldReviewReport(path)
			if err != nil {
				continue
			}
			if promotionHoldReviewMatches(review, report) {
				return path, report, true
			}
		}
	}
	return "", PromotionHoldReviewReport{}, false
}

func promotionHoldReviewMatches(review PromotionReview, report PromotionHoldReviewReport) bool {
	if strings.TrimSpace(report.Status) != "recorded" || strings.TrimSpace(report.Decision) != "hold_recorded" {
		return false
	}
	if strings.TrimSpace(report.ReviewID) != strings.TrimSpace(review.ID) {
		return false
	}
	if strings.TrimSpace(report.Recommendation) != strings.TrimSpace(review.Recommendation) {
		return false
	}
	if strings.TrimSpace(report.LaneID) != strings.TrimSpace(review.LaneID) {
		return false
	}
	if strings.TrimSpace(report.CapabilityClass) != strings.TrimSpace(review.CapabilityClass) {
		return false
	}
	return strings.TrimSpace(report.Reason) == strings.TrimSpace(review.Reason)
}

func promotionReviewFromQueueItem(item reviewartifact.QueueItem) (PromotionReview, bool) {
	if strings.TrimSpace(item.JSONPath) == "" {
		return PromotionReview{}, false
	}
	data, err := os.ReadFile(item.JSONPath)
	if err != nil {
		return PromotionReview{}, false
	}
	var review PromotionReview
	if err := json.Unmarshal(data, &review); err != nil {
		return PromotionReview{}, false
	}
	return review, strings.TrimSpace(review.ID) != ""
}
