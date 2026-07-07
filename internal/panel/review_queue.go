package panel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"compound/internal/mem"
	"compound/internal/reviewartifact"
)

const verdictReviewArea = "panel-verdicts"

// AnnotateVerdictQueue makes recorded panel verdicts legible in the shared
// review queue without applying any verdict or mutating memory authority.
func AnnotateVerdictQueue(report *reviewartifact.QueueReport) {
	if report == nil {
		return
	}
	if !hasPanelVerdictQueueItems(report.Items) {
		return
	}
	homePath := homeFromReviewRoot(report.ReviewRoot)
	applications := verdictApplicationsByArtifact(report.ReviewRoot)
	memoryTargets := memoryCandidatePanelTargets(homePath)
	for i := range report.Items {
		item := &report.Items[i]
		if item.Area != verdictReviewArea {
			continue
		}
		annotateVerdictQueueItem(item, applications, homePath, memoryTargets)
	}
}

func hasPanelVerdictQueueItems(items []reviewartifact.QueueItem) bool {
	for _, item := range items {
		if item.Area == verdictReviewArea {
			return true
		}
	}
	return false
}

func annotateVerdictQueueItem(item *reviewartifact.QueueItem, applications map[string]verdictApplication, homePath string, memoryTargets map[string]memoryCandidatePanelTarget) {
	verdict, ok := verdictFromQueueItem(*item)
	if !ok {
		item.Status = "blocked"
		item.Readiness = "blocked"
		item.BlockedReason = "panel verdict artifact unreadable"
		item.NextAction = "regenerate panel verdict artifacts"
		item.Band = reviewartifact.BandNeedsYou
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
		annotateVerdictOperatorCard(item, verdict, reviewartifact.BandFiled, "The judge panel verdict for "+panelVerdictContextLabel(verdict.Target)+" is already recorded.")
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
		annotateVerdictOperatorCard(item, verdict, reviewartifact.BandFiled, "The judge panel verdict for "+panelVerdictContextLabel(verdict.Target)+" is already applied.")
		return
	}
	if nextAction, ok := resolvedPanelVerdictTarget(homePath, verdict, memoryTargets); ok {
		item.Status = "resolved"
		item.Readiness = "no_action"
		item.BlockedReason = ""
		item.NextAction = nextAction
		annotateVerdictOperatorCard(item, verdict, reviewartifact.BandFiled, "The judge panel verdict for "+panelVerdictContextLabel(verdict.Target)+" is already resolved.")
		return
	}

	status := strings.TrimSpace(item.Status)
	switch status {
	case "ok", "applied", "resolved", "accepted", "recorded":
		item.Readiness = "no_action"
		item.BlockedReason = ""
		item.NextAction = "panel verdict artifact terminal"
		annotateVerdictOperatorCard(item, verdict, reviewartifact.BandFiled, "")
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
		item.Band = reviewartifact.BandNeedsYou
		return
	}

	if status == "" || status == "unknown" {
		item.Status = "review_required"
	}
	item.Readiness = "needs_review"
	item.BlockedReason = ""
	item.DecisionCard = buildPanelDecisionCard(*item, verdict)
	item.Actions = panelDecisionCardActions(item.DecisionCard)
	annotateVerdictOperatorCard(item, verdict, reviewartifact.BandNeedsYou, "")
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

func annotateVerdictOperatorCard(item *reviewartifact.QueueItem, verdict verdictArtifactPayload, band, summaryOverride string) {
	card, err := OperatorCardForVerdict(verdict)
	if err != nil {
		item.BlockedReason = reviewartifact.FirstNonEmpty(item.BlockedReason, "panel verdict operator card invalid: "+err.Error())
		return
	}
	if strings.TrimSpace(summaryOverride) != "" {
		card.Summary = reviewartifact.ShortLabel(summaryOverride, 159)
		if !strings.HasSuffix(card.Summary, ".") {
			card.Summary += "."
		}
		card.Unlock = ""
	}
	if err := reviewartifact.SetOperatorCard(item, card, reviewartifact.SummarySourceAnnotator, band); err != nil {
		item.BlockedReason = reviewartifact.FirstNonEmpty(item.BlockedReason, "panel verdict operator card invalid: "+err.Error())
	}
}

func buildPanelDecisionCard(item reviewartifact.QueueItem, verdict verdictArtifactPayload) *reviewartifact.DecisionCard {
	card := &reviewartifact.DecisionCard{
		Claim:               panelDecisionClaim(verdict),
		PanelLean:           panelLean(verdict.Synthesis),
		OptionalRationale:   true,
		CanonicalActionHint: "Record the human review, then run the selected canonical memory adjudication command. Renderers must not write review or memory state directly.",
		Boundary: []string{
			"read-only projection",
			"operator choice required before mutation",
			"no console-side state writes",
			"verdict-record plus memory adjudicate are the canonical mutation path",
		},
		Meta: map[string]string{
			"verdict_id":  verdict.ID,
			"run_id":      verdict.RunID,
			"target_kind": strings.TrimSpace(verdict.Target.Kind),
			"target_ref":  strings.TrimSpace(verdict.Target.Ref),
			"review_path": item.JSONPath,
		},
	}
	if rationale := panelVerdictPlainRationale(item.MarkdownPath, verdict.Synthesis.Rationale); rationale != "" {
		card.Why = append(card.Why, reviewartifact.DecisionCardReason{
			Label:  "panel rationale",
			Detail: rationale,
		})
	}
	for _, finding := range verdict.LensFindings {
		card.Why = append(card.Why, panelLensReasons(finding)...)
	}
	card.Choices = panelDecisionChoices(item.JSONPath, verdict.Target)
	return card
}

func panelDecisionClaim(verdict verdictArtifactPayload) string {
	var payload map[string]any
	if len(verdict.Target.Payload) > 0 && json.Unmarshal(verdict.Target.Payload, &payload) == nil {
		if object, ok := payload["object"].(string); ok && strings.TrimSpace(object) != "" {
			return oneLine(object)
		}
	}
	if s := strings.TrimSpace(verdict.Target.Summary); s != "" {
		return oneLine(s)
	}
	target := panelVerdictTarget(verdict.Target)
	if target == "" {
		return "A panel verdict needs an operator decision."
	}
	return "A panel verdict for this " + target + " needs an operator decision."
}

func panelLean(s Synthesis) string {
	decision := strings.TrimSpace(s.Decision)
	if decision == "" {
		decision = "unknown"
	}
	if s.Confidence > 0 {
		return fmt.Sprintf("%s (confidence %.2f)", decision, s.Confidence)
	}
	return decision
}

func panelVerdictPlainRationale(markdownPath, fallback string) string {
	if rationale := markdownSection(markdownPath, "## Rationale"); rationale != "" {
		return rationale
	}
	return oneLine(fallback)
}

func markdownSection(path, heading string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	inSection := false
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == heading {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(trimmed, "## ") {
			break
		}
		if inSection && trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return oneLine(strings.Join(out, " "))
}

func panelLensReasons(finding LensFinding) []reviewartifact.DecisionCardReason {
	label, question := panelLensLabelQuestion(finding.LensRef)
	if len(finding.Findings) == 0 {
		if finding.Error == "" {
			return nil
		}
		return []reviewartifact.DecisionCardReason{{
			Label:    label,
			Question: question,
			Severity: "error",
			Detail:   oneLine(finding.Error),
		}}
	}
	out := make([]reviewartifact.DecisionCardReason, 0, len(finding.Findings))
	for _, f := range finding.Findings {
		out = append(out, reviewartifact.DecisionCardReason{
			Label:    label,
			Question: question,
			Severity: strings.TrimSpace(f.Severity),
			Detail:   oneLine(f.Claim),
		})
	}
	return out
}

func panelLensLabelQuestion(ref string) (string, string) {
	lower := strings.ToLower(ref)
	switch {
	case strings.Contains(lower, "evidence-strength"):
		return "evidence strength", "How well is this supported?"
	case strings.Contains(lower, "fossilization-risk"):
		return "fossilization risk", "Could this harden into a false permanent rule?"
	case strings.Contains(lower, "inversion-falsification"):
		return "inversion / falsification", "Could this be overstated backwards?"
	case strings.Contains(lower, "duplicate"):
		return "duplicate risk", "Do we already believe something like this?"
	case strings.Contains(lower, "contradiction"):
		return "contradiction risk", "Do we already believe something against this?"
	case strings.Contains(lower, "privacy"):
		return "privacy risk", "Anything sensitive?"
	default:
		label := strings.TrimPrefix(strings.TrimSpace(ref), "lens:")
		if label == "" {
			label = "panel lens"
		}
		return label, "What did this lens notice?"
	}
}

func panelDecisionChoices(reviewPath string, target TargetInput) []reviewartifact.DecisionCardChoice {
	if strings.TrimSpace(target.Kind) != "memory_candidate" || strings.TrimSpace(target.Ref) == "" {
		return nil
	}
	choices := []struct {
		id     string
		label  string
		effect string
	}{
		{"promote", "Promote", "Becomes ambient canon, recallable and citeable in future work."},
		{"park", "Park", "Kept as evidence but not ambient; can resurface when new evidence arrives."},
		{"reject", "Reject", "Discarded as noise for this memory lane."},
		{"no_promote", "No-promote", "Records that this was declined so echoes stop re-asking."},
	}
	out := make([]reviewartifact.DecisionCardChoice, 0, len(choices))
	for _, choice := range choices {
		cmd := panelChoiceCommand(reviewPath, target.Ref, choice.id)
		out = append(out, reviewartifact.DecisionCardChoice{
			ID:             choice.id,
			Label:          choice.label,
			Effect:         choice.effect,
			Command:        cmd,
			Argv:           []string{"/bin/sh", "-lc", cmd},
			RequiresReview: true,
		})
	}
	return out
}

func panelChoiceCommand(reviewPath, candidateID, verdict string) string {
	reviewCmd := "compound panel verdict-record --review " + shellQuote(reviewPath) + " --rationale \"$RATIONALE\" --reviewed-by umut"
	adjudicateCmd := "compound memory adjudicate " + shellQuote(candidateID) + " --verdict " + shellQuote(verdict)
	return reviewCmd + " && " + adjudicateCmd
}

func panelDecisionCardActions(card *reviewartifact.DecisionCard) []reviewartifact.Action {
	if card == nil {
		return nil
	}
	out := make([]reviewartifact.Action, 0, len(card.Choices))
	for _, choice := range card.Choices {
		out = append(out, reviewartifact.Action{
			ID:             "decision:" + choice.ID,
			Label:          choice.Label,
			Command:        choice.Command,
			Argv:           choice.Argv,
			RequiresReview: choice.RequiresReview,
		})
	}
	return out
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

type verdictApplication struct {
	RunID       string
	Application Application
}

type memoryCandidatePanelTarget struct {
	State       string
	Annotations []mem.CandidateAnnotation
}

func memoryCandidatePanelTargets(homePath string) map[string]memoryCandidatePanelTarget {
	if strings.TrimSpace(homePath) == "" {
		return nil
	}
	store, err := mem.Open(homePath)
	if err != nil {
		return nil
	}
	candidates, err := store.AllCandidates()
	if err != nil {
		return nil
	}
	out := make(map[string]memoryCandidatePanelTarget, len(candidates))
	for _, candidate := range candidates {
		out[candidate.ID] = memoryCandidatePanelTarget{
			State:       candidate.State,
			Annotations: append([]mem.CandidateAnnotation(nil), candidate.Annotations...),
		}
	}
	return out
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

func resolvedPanelVerdictTarget(homePath string, verdict verdictArtifactPayload, memoryTargets map[string]memoryCandidatePanelTarget) (string, bool) {
	if strings.TrimSpace(homePath) == "" {
		return "", false
	}
	kind := strings.TrimSpace(verdict.Target.Kind)
	ref := strings.TrimSpace(verdict.Target.Ref)
	if kind == "memory_candidate" {
		return resolvedMemoryCandidatePanelTarget(ref, memoryTargets)
	}
	if kind != "comparison" || ref == "" {
		return "", false
	}
	path := comparisonCandidatePath(homePath, ref)
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

func resolvedMemoryCandidatePanelTarget(candidateID string, memoryTargets map[string]memoryCandidatePanelTarget) (string, bool) {
	candidateID = strings.TrimSpace(candidateID)
	if candidateID == "" {
		return "", false
	}
	item, ok := memoryTargets[candidateID]
	if !ok {
		return "", false
	}
	state := strings.TrimSpace(item.State)
	if state == "" || state == "proposed" {
		return "", false
	}
	if len(item.Annotations) == 0 {
		return "", false
	}
	return fmt.Sprintf("memory candidate already %s with %d annotation(s); panel verdict no longer open review debt (target=memory_candidate/%s)", state, len(item.Annotations), candidateID), true
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
