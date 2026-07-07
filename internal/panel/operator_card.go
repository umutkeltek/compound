package panel

import (
	"encoding/json"
	"fmt"
	"strings"

	"compound/internal/reviewartifact"
)

func (p verdictArtifactPayload) ReviewOperatorCard() (reviewartifact.OperatorCard, error) {
	return OperatorCardForVerdict(p)
}

func OperatorCardForVerdict(p verdictArtifactPayload) (reviewartifact.OperatorCard, error) {
	decision, err := reviewartifact.RequireText("synthesis.decision", p.Synthesis.Decision)
	if err != nil {
		return reviewartifact.OperatorCard{}, err
	}
	context, err := reviewartifact.RequireText("target context", panelVerdictContextLabel(p.Target))
	if err != nil {
		return reviewartifact.OperatorCard{}, err
	}
	group := "panel-verdicts|" + reviewartifact.FirstNonEmpty(p.RunID, "run") + "|" + decision
	lensCount := len(p.LensFindings)
	if lensCount == 0 {
		lensCount = 1
	}

	var summary, unlock string
	switch strings.TrimSpace(p.Target.Kind) {
	case "memory_candidate":
		switch decision {
		case "promote":
			summary = fmt.Sprintf("The judge panel passed this claim — all %d lenses clean. Add it to memory?", lensCount)
			if len(p.Synthesis.Dissent) > 0 {
				summary = fmt.Sprintf("The judge panel passed this claim with dissent noted — %d lenses ran. Add it to memory?", lensCount)
			} else if p.Synthesis.Confidence > 0 && p.Synthesis.Confidence < 0.7 {
				summary = fmt.Sprintf("The judge panel passed this claim at low confidence %.2f. Add it to memory?", p.Synthesis.Confidence)
			}
			unlock = "memory gains 1 promoted belief it can act on"
		case "reject":
			summary = "The judge panel rejected this claim — " + topFailedLensPlain(p.LensFindings) + " flagged it. Record the rejection?"
			unlock = "the inbox stops re-asking about this candidate"
		default:
			summary = "The judge panel needs your call on this claim. Record the decision?"
			unlock = "memory gets a reviewed answer for this candidate"
		}
	case "comparison":
		candidate, baseline, surface := comparisonPlainParts(p.Target)
		result := "lost to"
		record := "loss"
		if decision == "promote" || decision == "accept" || decision == "win" {
			result = "beat"
			record = "win"
		}
		summary = fmt.Sprintf("The judge panel says %s %s %s on %s. Record the %s?", candidate, result, baseline, surface, record)
		unlock = "the comparison result becomes reviewed route evidence"
	default:
		thing := reviewartifact.ShortLabel(reviewartifact.FirstNonEmpty(p.Target.Kind, "target"), 36)
		summary = fmt.Sprintf("The judge panel reached a %s verdict for this %s. Record it?", reviewartifact.PlainLabel(decision), thing)
		unlock = "the verdict stops waiting as raw panel output"
	}
	return reviewartifact.NewOperatorCard(summary, context, group, unlock)
}

func OperatorCardForVerdictJSON(data []byte) (reviewartifact.OperatorCard, error) {
	var payload verdictArtifactPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return reviewartifact.OperatorCard{}, err
	}
	return OperatorCardForVerdict(payload)
}

func (p PanelCapabilityFeedbackPlan) ReviewOperatorCard() (reviewartifact.OperatorCard, error) {
	return OperatorCardForCapabilityFeedback(p)
}

func OperatorCardForCapabilityFeedback(p PanelCapabilityFeedbackPlan) (reviewartifact.OperatorCard, error) {
	context, err := reviewartifact.RequireText("verdict context", panelFeedbackContextLabel(p))
	if err != nil {
		return reviewartifact.OperatorCard{}, err
	}
	count := len(p.FeedbackCandidates)
	if count == 0 {
		return reviewartifact.OperatorCard{}, fmt.Errorf("feedback_candidates required for operator card")
	}
	summary := fmt.Sprintf("The judge panel found %d capability feedback items. Review the graph feedback?", count)
	if p.Status == "applied_feedback" || p.Status == "rejected" || p.Status == "recorded" {
		summary = fmt.Sprintf("The judge panel feedback review is already recorded for %s.", context)
	}
	group := "capability-panel-feedback|" + reviewartifact.FirstNonEmpty(p.RunID, "run") + "|" + reviewartifact.FirstNonEmpty(p.PanelDecision, "decision")
	return reviewartifact.NewOperatorCard(summary, context, group, "capability graph gains reviewed feedback edges")
}

func panelVerdictContextLabel(target TargetInput) string {
	var payload map[string]any
	if len(target.Payload) > 0 && json.Unmarshal(target.Payload, &payload) == nil {
		if sp, ok := payload["source_provenance"].(map[string]any); ok {
			if title, ok := sp["title"].(string); ok && strings.TrimSpace(title) != "" {
				return cleanPanelContextLabel(title)
			}
		}
		if object, ok := payload["object"].(string); ok && strings.TrimSpace(object) != "" {
			return cleanPanelContextLabel(object)
		}
	}
	return cleanPanelContextLabel(reviewartifact.HumanizeEntity(target.Ref), target.Kind)
}

func comparisonPlainParts(target TargetInput) (string, string, string) {
	var payload map[string]any
	if len(target.Payload) > 0 {
		_ = json.Unmarshal(target.Payload, &payload)
	}
	candidate := mapString(payload, "candidate_plain", "candidate", "candidate_name")
	baseline := mapString(payload, "baseline_plain", "baseline", "baseline_name")
	surface := mapString(payload, "surface_plain", "surface", "subject")
	candidate = reviewartifact.ShortLabel(reviewartifact.PlainLabel(reviewartifact.FirstNonEmpty(candidate, "candidate")), 34)
	baseline = reviewartifact.ShortLabel(reviewartifact.PlainLabel(reviewartifact.FirstNonEmpty(baseline, "baseline")), 34)
	surface = reviewartifact.ShortLabel(reviewartifact.PlainLabel(reviewartifact.FirstNonEmpty(surface, target.Summary, "test surface")), 34)
	return candidate, baseline, surface
}

func mapString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func topFailedLensPlain(findings []LensFinding) string {
	for _, finding := range findings {
		status := strings.TrimSpace(finding.Status)
		if status != "" && status != "clean" && status != "pass" && status != "passed" {
			return reviewartifact.ShortLabel(reviewartifact.PlainLabel(strings.TrimPrefix(finding.LensRef, "lens:")), 40)
		}
		if finding.Error != "" || len(finding.Findings) > 0 {
			return reviewartifact.ShortLabel(reviewartifact.PlainLabel(strings.TrimPrefix(finding.LensRef, "lens:")), 40)
		}
	}
	return "the review lens"
}

func panelFeedbackContextLabel(p PanelCapabilityFeedbackPlan) string {
	return cleanPanelContextLabel(p.TargetKind, reviewartifact.HumanizeEntity(p.TargetRef), p.VerdictID)
}

func cleanPanelContextLabel(values ...string) string {
	label := reviewartifact.PlainLabel(reviewartifact.FirstNonEmpty(values...))
	if label == "" {
		return ""
	}
	return reviewartifact.ShortLabel(label, 60)
}
