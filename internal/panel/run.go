package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/reviewartifact"
	"compound/internal/routeevidence"
)

const judgePromptID = "judge:panel-synthesis"

type decodedLensResponse struct {
	Confidence float64   `json:"confidence"`
	Findings   []Finding `json:"findings"`
}

type verdictArtifactPayload struct {
	SchemaVersion string        `json:"schema_version"`
	ID            string        `json:"id"`
	CreatedAt     string        `json:"created_at"`
	RunID         string        `json:"run_id"`
	Target        TargetInput   `json:"target"`
	LensFindings  []LensFinding `json:"lens_findings"`
	Synthesis     Synthesis     `json:"synthesis"`
	EvidenceRefs  []string      `json:"evidence_refs"`
}

// ExecuteDryRun resumes at target granularity. Completed target findings and
// run-scoped verdict artifacts are skipped. If a crash leaves partial route
// evidence without a verdict artifact, the target re-executes fully; route
// evidence recording is RequestID-idempotent, so existing lens/judge rows are
// reused instead of appended.
func ExecuteDryRun(ctx context.Context, homePath string, run Run, caller LensCaller, now func() time.Time) (Run, error) {
	if caller == nil {
		return run, fmt.Errorf("no caller configured")
	}
	if now == nil {
		now = time.Now
	}
	store, err := Open(homePath)
	if err != nil {
		return run, err
	}
	if strings.TrimSpace(run.Route.LensLane.ModelID) == "" {
		err := ErrBlockedNoLane{Class: lensCapabilityClass}
		run.Status = "blocked"
		run.StoppedReason = err.Error()
		_ = store.SaveRun(run)
		return run, err
	}
	if strings.TrimSpace(run.Route.JudgeLane.ModelID) == "" {
		err := ErrBlockedNoLane{Class: judgeCapabilityClass}
		run.Status = "blocked"
		run.StoppedReason = err.Error()
		_ = store.SaveRun(run)
		return run, err
	}
	if run.CountsAfter == nil {
		run.CountsAfter = copyCounts(run.CountsBefore)
	}
	if run.Budget.MaxUSDMicros > 0 {
		// No lane cost data exists yet, so a USD cap alone cannot be enforced.
		// Fail closed rather than running an unbounded live batch.
		if run.Budget.MaxItems == 0 && run.Budget.MaxTotalTokens == 0 {
			return run, fmt.Errorf("usd budget cap is unenforceable without lane cost data; bound the run with --max-items or --max-tokens")
		}
		run.EvidenceRefs = appendUniqueString(run.EvidenceRefs, "cost_unknown")
	}
	run.Status = "running"
	if err := store.SaveRun(run); err != nil {
		return run, err
	}

	existingFindings := targetFindingSet(run.Findings)
	processed := 0
	tokensUsed := totalRunTokens(run)
	for _, target := range run.Targets {
		if existingFindings[target.Ref] {
			continue
		}
		targetInput, err := loadRunTargetInput(homePath, run.TargetKind, target.Ref)
		if err != nil {
			return run, err
		}
		artifactID := verdictArtifactID(run.ID, target.Ref)
		if path, ok, err := verdictArtifactExists(homePath, artifactID); err != nil {
			return run, err
		} else if ok {
			run.Findings = append(run.Findings, skippedTargetFinding(target.Ref, path, run.LensSet.Included))
			existingFindings[target.Ref] = true
			if err := store.SaveRun(run); err != nil {
				return run, err
			}
			continue
		}
		if run.Budget.MaxItems > 0 && processed >= run.Budget.MaxItems {
			run.Status = "stopped_budget"
			run.StoppedReason = "budget:max_items"
			if err := store.SaveRun(run); err != nil {
				return run, err
			}
			return run, nil
		}

		finding, used, err := executeTargetDryRun(ctx, homePath, run, targetInput, caller, now)
		if err != nil {
			return run, err
		}
		run.Findings = append(run.Findings, finding)
		existingFindings[target.Ref] = true
		processed++
		tokensUsed += used
		run.CountsAfter = copyCounts(run.CountsBefore)
		if run.Budget.MaxTotalTokens > 0 && tokensUsed >= run.Budget.MaxTotalTokens && len(existingFindings) < len(run.Targets) {
			run.Status = "stopped_budget"
			run.StoppedReason = "budget:max_total_tokens"
		} else {
			run.Status = "running"
			run.StoppedReason = ""
		}
		if err := store.SaveRun(run); err != nil {
			return run, err
		}
		if run.Status == "stopped_budget" {
			return run, nil
		}
	}
	run.Status = "verdicts_recorded"
	run.StoppedReason = ""
	run.CountsAfter = copyCounts(run.CountsBefore)
	if err := store.SaveRun(run); err != nil {
		return run, err
	}
	return run, nil
}

func executeTargetDryRun(ctx context.Context, homePath string, run Run, target TargetInput, caller LensCaller, now func() time.Time) (TargetFinding, int, error) {
	lensFindings := []LensFinding{}
	outputs := []LensOutput{}
	tokensUsed := 0
	for _, included := range run.LensSet.Included {
		lens := Lens{NodeID: included.Ref, Weight: included.Weight, PromptSHA256: included.PromptSHA256, PromptPath: lensPromptPath(homePath, included.Ref)}
		system, err := readPrompt(lens.PromptPath)
		if err != nil {
			return TargetFinding{}, tokensUsed, err
		}
		user, err := targetUserPrompt(target)
		if err != nil {
			return TargetFinding{}, tokensUsed, err
		}
		requestID := "panel:" + run.ID + ":" + targetDigest(target.Ref) + ":" + included.Ref
		callResult, callErr := caller.Call(ctx, CallRequest{LensID: included.Ref, TargetRef: target.Ref, Lane: run.Route.LensLane, System: system, User: user})
		status := "completed"
		if callErr != nil {
			status = "failed"
		}
		routeRef, err := recordPanelRouteEvidence(homePath, now().UTC(), run.WorkID, requestID, "compound.panel.lens", run.Route.LensLane, callResult, status)
		if err != nil {
			return TargetFinding{}, tokensUsed, err
		}
		tokensUsed += callResult.TotalTokens
		if callErr != nil {
			lensFindings = append(lensFindings, failedLensFinding(included.Ref, run.Route.LensLane, routeRef, callResult, callErr))
			outputs = append(outputs, LensOutput{Lens: lens, Err: callErr})
			continue
		}
		decoded, err := decodeLensResponse(callResult.Content)
		if err != nil {
			lensFindings = append(lensFindings, failedLensFinding(included.Ref, run.Route.LensLane, routeRef, callResult, err))
			outputs = append(outputs, LensOutput{Lens: lens, Err: err})
			continue
		}
		lensFindings = append(lensFindings, LensFinding{LensRef: included.Ref, ModelID: run.Route.LensLane.ModelID, LaneTier: run.Route.LensLane.Tier, RouteEvidenceRef: routeRef, Confidence: decoded.Confidence, LatencyMS: callResult.LatencyMS, Usage: Usage{TotalTokens: callResult.TotalTokens}, Findings: decoded.Findings, Status: "completed"})
		outputs = append(outputs, LensOutput{Lens: lens, Confidence: decoded.Confidence, Findings: decoded.Findings})
	}

	judge, judgeRouteRef, judgeTokens, malformed, err := callJudge(ctx, homePath, run, target, successfulLensOutputsForJudge(outputs), caller, now)
	if err != nil {
		return TargetFinding{}, tokensUsed, err
	}
	tokensUsed += judgeTokens
	synthesis := Synthesize(outputs, judge, Guardrails{})
	synthesis.JudgeRouteEvidenceRef = judgeRouteRef
	synthesis.JudgeUsage = Usage{TotalTokens: judgeTokens}
	if malformed {
		synthesis.Decision = "needs_human"
		synthesis.GuardrailsApplied = appendUniqueString(synthesis.GuardrailsApplied, "judge_json_malformed")
		if strings.TrimSpace(synthesis.Rationale) == "" {
			synthesis.Rationale = "judge response was malformed twice; human review required"
		}
	}
	if run.Route.JudgeLane.ModelID != "" && run.Route.JudgeLane.ModelID == run.Route.LensLane.ModelID {
		// A model graded its own lens findings; trust may only go down.
		synthesis.GuardrailsApplied = appendUniqueString(synthesis.GuardrailsApplied, "self_judgment")
		if synthesis.Decision == "promote" {
			synthesis.Decision = "park"
		}
	}
	artifactPath, err := writeVerdictArtifact(homePath, run, target, lensFindings, synthesis, now().UTC())
	if err != nil {
		return TargetFinding{}, tokensUsed, err
	}
	return TargetFinding{TargetRef: target.Ref, LensFindings: lensFindings, Synthesis: synthesis, VerdictArtifact: artifactPath}, tokensUsed, nil
}

func callJudge(ctx context.Context, homePath string, run Run, target TargetInput, successful []LensOutput, caller LensCaller, now func() time.Time) (JudgeOutput, string, int, bool, error) {
	system, err := readPrompt(lensPromptPath(homePath, judgePromptID))
	if err != nil {
		return JudgeOutput{}, "", 0, false, err
	}
	user, err := judgeUserPrompt(target, successful)
	if err != nil {
		return JudgeOutput{}, "", 0, false, err
	}
	var lastRouteRef string
	var tokens int
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		requestID := "panel:" + run.ID + ":" + targetDigest(target.Ref) + ":" + judgePromptID
		if attempt > 0 {
			requestID += fmt.Sprintf(":retry%d", attempt)
		}
		callResult, callErr := caller.Call(ctx, CallRequest{LensID: judgePromptID, TargetRef: target.Ref, Lane: run.Route.JudgeLane, System: system, User: user})
		status := "completed"
		if callErr != nil {
			status = "failed"
		}
		routeRef, err := recordPanelRouteEvidence(homePath, now().UTC(), run.WorkID, requestID, "compound.panel.judge", run.Route.JudgeLane, callResult, status)
		if err != nil {
			return JudgeOutput{}, lastRouteRef, tokens, false, err
		}
		lastRouteRef = routeRef
		tokens += callResult.TotalTokens
		if callErr != nil {
			return JudgeOutput{}, lastRouteRef, tokens, false, callErr
		}
		judge, err := DecodeJudge(callResult.Content)
		if err == nil {
			return judge, lastRouteRef, tokens, false, nil
		}
		lastErr = err
	}
	return JudgeOutput{Decision: "needs_human", Confidence: 0, Rationale: "judge response malformed: " + lastErr.Error()}, lastRouteRef, tokens, true, nil
}

func decodeLensResponse(data []byte) (decodedLensResponse, error) {
	data = stripLensResponseFence(data)
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var out decodedLensResponse
	if err := dec.Decode(&out); err != nil {
		return decodedLensResponse{}, fmt.Errorf("decode lens output: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return decodedLensResponse{}, fmt.Errorf("decode lens output: trailing JSON")
		}
		return decodedLensResponse{}, fmt.Errorf("decode lens output trailing JSON: %w", err)
	}
	if out.Confidence < 0 || out.Confidence > 1 {
		return decodedLensResponse{}, fmt.Errorf("lens confidence must be 0..1")
	}
	if out.Findings == nil {
		return decodedLensResponse{}, fmt.Errorf("lens findings required")
	}
	return out, nil
}

func stripLensResponseFence(data []byte) []byte {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return data
	}
	if strings.HasPrefix(trimmed, "```") {
		lines := strings.Split(trimmed, "\n")
		if len(lines) >= 3 && isJSONFenceOpener(lines[0]) && strings.TrimSpace(lines[len(lines)-1]) == "```" {
			return []byte(strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n")))
		}
	}
	if strings.HasSuffix(trimmed, "```") {
		withoutFence := strings.TrimSpace(strings.TrimSuffix(trimmed, "```"))
		if strings.HasPrefix(withoutFence, "{") {
			return []byte(withoutFence)
		}
	}
	return data
}

func isJSONFenceOpener(line string) bool {
	opener := strings.TrimSpace(line)
	if opener == "```" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(opener, "```")), "json")
}

func recordPanelRouteEvidence(homePath string, createdAt time.Time, workID, requestID, clientProfile string, lane LaneRef, result CallResult, status string) (string, error) {
	if existing, ok, err := routeevidence.LookupRequestID(homePath, requestID); err != nil {
		return "", err
	} else if ok {
		return existing.ID, nil
	}
	ev, err := routeevidence.Build(routeevidence.BuildInput{CreatedAt: createdAt, RequestID: requestID, WorkID: workID, ClientProfile: clientProfile, Protocol: "panel-lane", Provider: "compound-route-plan", Model: lane.ModelID, Usage: routeevidence.Usage{TotalTokens: result.TotalTokens}, LatencyMS: result.LatencyMS, Status: status, EvidenceRefs: []string{routeevidence.ModelCapabilitiesAuthorityRef}})
	if err != nil {
		return "", err
	}
	recorded, err := routeevidence.RecordJoined(homePath, ev)
	if err != nil {
		return "", err
	}
	return recorded.Evidence.ID, nil
}

func loadRunTargetInput(homePath, targetKind, ref string) (TargetInput, error) {
	return LoadTargetInput(homePath, targetKind, ref)
}

func targetUserPrompt(target TargetInput) (string, error) {
	payload := struct {
		TargetRef string          `json:"target_ref"`
		Summary   string          `json:"summary"`
		Payload   json.RawMessage `json:"payload"`
	}{TargetRef: target.Ref, Summary: target.Summary, Payload: target.Payload}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func judgeUserPrompt(target TargetInput, outputs []LensOutput) (string, error) {
	type lensForJudge struct {
		LensRef    string    `json:"lens_ref"`
		Confidence float64   `json:"confidence"`
		Findings   []Finding `json:"findings"`
	}
	payload := struct {
		TargetRef string         `json:"target_ref"`
		Summary   string         `json:"summary"`
		Outputs   []lensForJudge `json:"lens_outputs"`
	}{TargetRef: target.Ref, Summary: target.Summary, Outputs: []lensForJudge{}}
	for _, output := range outputs {
		payload.Outputs = append(payload.Outputs, lensForJudge{LensRef: output.Lens.NodeID, Confidence: output.Confidence, Findings: output.Findings})
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func successfulLensOutputsForJudge(outputs []LensOutput) []LensOutput {
	out := []LensOutput{}
	for _, output := range outputs {
		if output.Err == nil {
			out = append(out, output)
		}
	}
	return out
}

func failedLensFinding(lensRef string, lane LaneRef, routeRef string, result CallResult, err error) LensFinding {
	message := err.Error()
	return LensFinding{LensRef: lensRef, ModelID: lane.ModelID, LaneTier: lane.Tier, RouteEvidenceRef: routeRef, LatencyMS: result.LatencyMS, Usage: Usage{TotalTokens: result.TotalTokens}, Findings: []Finding{{Claim: message, Severity: "error"}}, Status: "failed", Error: message}
}

func skippedTargetFinding(targetRef, artifactPath string, included []IncludedLens) TargetFinding {
	lensFindings := make([]LensFinding, 0, len(included))
	for _, lens := range included {
		lensFindings = append(lensFindings, LensFinding{LensRef: lens.Ref, Status: "skipped", Error: "verdict artifact already exists"})
	}
	return TargetFinding{TargetRef: targetRef, LensFindings: lensFindings, VerdictArtifact: artifactPath}
}

func lensPromptPath(homePath, id string) string {
	return filepath.Join(homePath, "registries", "lens-prompts", id+".md")
}

func readPrompt(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt %s: %w", path, err)
	}
	return string(data), nil
}

// verdictArtifactID is scoped to the run so a fresh run re-judges a target even when an
// earlier run left an artifact; only intra-run crash resume reuses a verdict.
func verdictArtifactID(runID, targetRef string) string {
	return "panel_verdict_" + targetDigest(runID) + "_" + targetDigest(targetRef)
}

func verdictArtifactExists(homePath, id string) (string, bool, error) {
	dir, err := reviewartifact.Dir(homePath, "panel-verdicts")
	if err != nil {
		return "", false, err
	}
	path := filepath.Join(dir, id+".json")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return path, false, nil
		}
		return path, false, err
	}
	return path, true, nil
}

func writeVerdictArtifact(homePath string, run Run, target TargetInput, lensFindings []LensFinding, synthesis Synthesis, createdAt time.Time) (string, error) {
	id := verdictArtifactID(run.ID, target.Ref)
	evidenceRefs := []string{}
	for _, finding := range lensFindings {
		if finding.RouteEvidenceRef != "" {
			evidenceRefs = append(evidenceRefs, finding.RouteEvidenceRef)
		}
	}
	if synthesis.JudgeRouteEvidenceRef != "" {
		evidenceRefs = append(evidenceRefs, synthesis.JudgeRouteEvidenceRef)
	}
	payload := verdictArtifactPayload{SchemaVersion: "compound.panel_verdict.v0.1", ID: id, CreatedAt: createdAt.UTC().Format(time.RFC3339), RunID: run.ID, Target: target, LensFindings: lensFindings, Synthesis: synthesis, EvidenceRefs: evidenceRefs}
	paths, err := reviewartifact.Write(homePath, "panel-verdicts", id, payload, " ", renderVerdictMarkdown(payload))
	if err != nil {
		return "", err
	}
	return paths.PlanPath, nil
}

func renderVerdictMarkdown(payload verdictArtifactPayload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Panel Verdict %s\n\n", payload.ID)
	fmt.Fprintf(&b, "- target: %s\n", payload.Target.Ref)
	fmt.Fprintf(&b, "- summary: %s\n", payload.Target.Summary)
	fmt.Fprintf(&b, "- decision: %s\n", payload.Synthesis.Decision)
	fmt.Fprintf(&b, "- confidence: %.2f\n", payload.Synthesis.Confidence)
	if len(payload.Synthesis.GuardrailsApplied) > 0 {
		fmt.Fprintf(&b, "- guardrails: %s\n", strings.Join(payload.Synthesis.GuardrailsApplied, ", "))
	}
	if payload.Synthesis.Rationale != "" {
		fmt.Fprintf(&b, "\n## Rationale\n\n%s\n", payload.Synthesis.Rationale)
	}
	if len(payload.LensFindings) > 0 {
		fmt.Fprintln(&b, "\n## Lens Findings")
		for _, finding := range payload.LensFindings {
			fmt.Fprintf(&b, "\n- %s: %s", finding.LensRef, finding.Status)
			if finding.Error != "" {
				fmt.Fprintf(&b, " (%s)", finding.Error)
			}
			for _, item := range finding.Findings {
				fmt.Fprintf(&b, "\n  - [%s] %s", item.Severity, item.Claim)
			}
		}
		fmt.Fprintln(&b)
	}
	if len(payload.Synthesis.Dissent) > 0 {
		fmt.Fprintln(&b, "\n## Dissent")
		for _, dissent := range payload.Synthesis.Dissent {
			fmt.Fprintf(&b, "\n- %s: %s - %s", dissent.LensRef, dissent.Position, dissent.Note)
		}
		fmt.Fprintln(&b)
	}
	return b.String()
}

func targetFindingSet(findings []TargetFinding) map[string]bool {
	out := map[string]bool{}
	for _, finding := range findings {
		if finding.TargetRef != "" {
			out[finding.TargetRef] = true
		}
	}
	return out
}

func totalRunTokens(run Run) int {
	total := 0
	for _, target := range run.Findings {
		for _, lens := range target.LensFindings {
			total += lens.Usage.TotalTokens
		}
		total += target.Synthesis.JudgeUsage.TotalTokens
	}
	return total
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
