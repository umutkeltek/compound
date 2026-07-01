package product

import (
	"path/filepath"
	"strings"

	"compound/internal/reviewartifact"
)

const productReviewArea = "product-reviews"

// AnnotateReviewQueue attaches product-review hints to generic review artifacts.
// It does not load product ledgers, mutate artifacts, apply reviews, or change
// hypotheses; it only makes the existing reviewed-result import seam visible.
func AnnotateReviewQueue(report *reviewartifact.QueueReport) {
	if report == nil {
		return
	}
	for i := range report.Items {
		item := &report.Items[i]
		if item.Area != productReviewArea {
			continue
		}
		annotateProductReviewItem(item)
	}
}

func annotateProductReviewItem(item *reviewartifact.QueueItem) {
	switch strings.TrimSpace(item.Status) {
	case "ok", "applied", "resolved", "accepted":
		item.Readiness = "no_action"
		item.NextAction = "product review artifact is terminal"
		return
	default:
		item.Readiness = "needs_review"
		item.NextAction = "fill review YAML after evidence review, then record product review"
	}
	reviewYAML := productReviewYAMLPath(*item)
	if reviewYAML == "" {
		item.Readiness = "blocked"
		item.BlockedReason = "review YAML draft missing"
		item.NextAction = "review YAML draft missing; regenerate product review-plan"
		return
	}
	argv := []string{"compound", "product", "review", reviewYAML}
	item.Actions = []reviewartifact.Action{{
		ID:             "record_product_review",
		Label:          "Record product review",
		Command:        shellJoin(argv),
		Argv:           argv,
		RequiresReview: true,
	}}
	review, err := LoadReview(reviewYAML)
	if err != nil {
		item.Readiness = "blocked"
		item.BlockedReason = "review YAML draft unreadable: " + productReviewQueueError(err)
		item.NextAction = "fix review YAML draft before recording product review"
		return
	}
	if err := ValidateReview(review); err != nil {
		item.BlockedReason = productReviewQueueError(err)
		item.NextAction = "fill valid review YAML before recording product review"
		return
	}
	item.Readiness = "ready_to_record"
	item.NextAction = "record product review"
}

func productReviewYAMLPath(item reviewartifact.QueueItem) string {
	for _, path := range item.ExtraPaths {
		if strings.HasSuffix(filepath.Base(path), ".review.yaml") {
			return path
		}
	}
	if item.JSONPath == "" {
		return ""
	}
	path := strings.TrimSuffix(item.JSONPath, ".json") + ".review.yaml"
	return path
}

func shellJoin(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuote(arg string) string {
	if arg == "" {
		return "''"
	}
	if strings.IndexFunc(arg, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '/' || r == '.' || r == ':' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	}) == -1 {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}

func productReviewQueueError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.Join(strings.Fields(err.Error()), " ")
	if len(msg) > 240 {
		msg = msg[:237] + "..."
	}
	return msg
}
