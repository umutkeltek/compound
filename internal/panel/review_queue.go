package panel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	homePath := homeFromReviewRoot(report.ReviewRoot)
	applications := verdictApplicationsByArtifact(report.ReviewRoot)
	for i := range report.Items {
		item := &report.Items[i]
		if item.Area != verdictReviewArea {
			continue
		}
		annotateVerdictQueueItem(item, applications, homePath)
	}
}

func annotateVerdictQueueItem(item *reviewartifact.QueueItem, applications map[string]verdictApplication, homePath string) {
	verdict, ok := verdictFromQueueItem(*item)
	if !ok {
		item.Status = "blocked"
		item.Readiness = "blocked"
		item.BlockedReason = "panel verdict artifact unreadable"
		item.NextAction = "regenerate panel verdict artifacts"
		item.Actions = nil
		return
	}

	item.EvidenceRefs = reviewartifact.UniqueSorted(append(item.EvidenceRefs, verdict.EvidenceRefs...))
	item.Actions = nil

	if reviewedPath := panelVerdictReviewedPath(*item); reviewedPath != "" {
		item.Status = "recorded"
		item.Readiness = "review_recorded"
		item.BlockedReason = ""
		item.NextAction = "panel verdict human review recorded"
		if report, err := LoadVerdictHumanReviewReport(reviewedPath); err == nil {
			item.EvidenceRefs = reviewartifact.UniqueSorted(append(item.EvidenceRefs, report.EvidenceRefs...))
			item.NextAction = reviewartifact.FirstNonEmpty(report.NextAction, item.NextAction)
		}
		return
	}

	if app, ok := applicationForVerdictItem(*item, applications); ok && terminalPanelApplication(app.Application, verdict.Synthesis.Decision) {
		item.Status = "applied"
		item.Readiness = "no_action"
		item.BlockedReason = ""
		target := panelVerdictTarget(verdict.Target)
		item.NextAction = fmt.Sprintf("panel verdict already applied by %s (target=%s decision=%s mapped=%s)", app.RunID, target, strings.TrimSpace(verdict.Synthesis.Decision), app.Application.MappedVerdict)
		return
	}
	if nextAction, ok := resolvedPanelVerdictTarget(homePath, verdict); ok {
		item.Status = "resolved"
		item.Readiness = "no_action"
		item.BlockedReason = ""
		item.NextAction = nextAction
		return
	}

	status := strings.TrimSpace(item.Status)
	switch status {
	case "ok", "applied", "resolved", "accepted", "recorded":
		item.Readiness = "no_action"
		item.BlockedReason = ""
		item.NextAction = "panel verdict artifact terminal"
		return
	}

	decision := strings.TrimSpace(verdict.Synthesis.Decision)
	if decision == "" {
		if status == "" || status == "unknown" {
			item.Status = "blocked"
		}
		item.Readiness = "blocked"
		item.BlockedReason = "panel verdict missing synthesis.decision"
		item.NextAction = "regenerate panel verdict artifact with synthesis decision"
		return
	}

	if status == "" || status == "unknown" {
		item.Status = "review_required"
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

type verdictApplication struct {
	RunID       string
	Application Application
}

func verdictApplicationsByArtifact(reviewRoot string) map[string]verdictApplication {
	out := map[string]verdictApplication{}
	homePath := homeFromReviewRoot(reviewRoot)
	if homePath == "" {
		return out
	}
	runDir := filepath.Join(homePath, "panel-runs")
	entries, err := os.ReadDir(runDir)
	if os.IsNotExist(err) {
		return out
	}
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(runDir, entry.Name()))
		if err != nil {
			continue
		}
		run, err := Decode(data)
		if err != nil {
			continue
		}
		for _, finding := range run.Findings {
			artifact := strings.TrimSpace(finding.VerdictArtifact)
			if artifact == "" {
				continue
			}
			app := verdictApplication{RunID: run.ID, Application: finding.Application}
			for _, key := range verdictArtifactKeys(artifact) {
				out[key] = app
			}
		}
	}
	return out
}

func homeFromReviewRoot(reviewRoot string) string {
	clean := filepath.Clean(strings.TrimSpace(reviewRoot))
	if clean == "." || filepath.Base(clean) != "reviews" {
		return ""
	}
	return filepath.Dir(clean)
}

func applicationForVerdictItem(item reviewartifact.QueueItem, applications map[string]verdictApplication) (verdictApplication, bool) {
	for _, key := range verdictArtifactKeys(item.JSONPath) {
		if app, ok := applications[key]; ok {
			return app, true
		}
	}
	if item.ID != "" {
		if app, ok := applications[item.ID]; ok {
			return app, true
		}
	}
	return verdictApplication{}, false
}

func panelVerdictReviewedPath(item reviewartifact.QueueItem) string {
	for _, path := range item.ExtraPaths {
		if strings.HasSuffix(path, ".reviewed.json") {
			return path
		}
	}
	return ""
}

func verdictArtifactKeys(path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	clean := filepath.Clean(path)
	base := filepath.Base(clean)
	keys := []string{clean}
	if base != "." && base != clean {
		keys = append(keys, base)
	}
	if strings.HasSuffix(base, ".json") {
		keys = append(keys, strings.TrimSuffix(base, ".json"))
	}
	return keys
}

func terminalPanelApplication(app Application, decision string) bool {
	if !app.Applied {
		return false
	}
	mapped := strings.TrimSpace(app.MappedVerdict)
	if mapped == "" {
		mapped = normalizePanelPosition(decision)
	}
	return mapped != "needs_human" && mapped != "needs_trial"
}

func resolvedPanelVerdictTarget(homePath string, verdict verdictArtifactPayload) (string, bool) {
	if strings.TrimSpace(homePath) == "" {
		return "", false
	}
	if strings.TrimSpace(verdict.Target.Kind) != "comparison" || strings.TrimSpace(verdict.Target.Ref) == "" {
		return "", false
	}
	path := comparisonCandidatePath(homePath, verdict.Target.Ref)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	candidate, err := decodeComparisonCandidate(data, path)
	if err != nil || candidate.State != comparisonStateJudged {
		return "", false
	}
	target := panelVerdictTarget(verdict.Target)
	verdictText := reviewartifact.FirstNonEmpty(candidate.Verdict, "unknown")
	source := strings.TrimSpace(candidate.VerdictArtifact)
	if source == "" {
		return fmt.Sprintf("comparison target already judged; no panel action needed (target=%s verdict=%s)", target, verdictText), true
	}
	return fmt.Sprintf("comparison target already judged; no panel action needed (target=%s verdict=%s source=%s)", target, verdictText, filepath.Base(source)), true
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
