package panel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/reviewartifact"
)

const VerdictHumanReviewReportSchemaVersion = "compound.panel_verdict_human_review.v0.1"

type VerdictHumanReviewReport struct {
	SchemaVersion string   `json:"schema_version"`
	ID            string   `json:"id"`
	CreatedAt     string   `json:"created_at"`
	Status        string   `json:"status"`
	VerdictID     string   `json:"verdict_id"`
	RunID         string   `json:"run_id"`
	Decision      string   `json:"decision"`
	TargetKind    string   `json:"target_kind"`
	TargetRef     string   `json:"target_ref"`
	Rationale     string   `json:"rationale"`
	NextAction    string   `json:"next_action"`
	SourceRef     string   `json:"source_ref"`
	SourceHash    string   `json:"source_hash"`
	EvidenceRefs  []string `json:"evidence_refs"`
	Boundaries    []string `json:"boundaries"`
	ReviewPath    string   `json:"review_path,omitempty"`
}

func LoadVerdictArtifact(path string) (verdictArtifactPayload, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return verdictArtifactPayload{}, err
	}
	var verdict verdictArtifactPayload
	if err := json.Unmarshal(data, &verdict); err != nil {
		return verdictArtifactPayload{}, fmt.Errorf("parse panel verdict %s: %w", path, err)
	}
	if strings.TrimSpace(verdict.ID) == "" {
		return verdictArtifactPayload{}, fmt.Errorf("panel verdict %s missing id", path)
	}
	if verdict.SchemaVersion != "compound.panel_verdict.v0.1" {
		return verdictArtifactPayload{}, fmt.Errorf("panel verdict %s schema_version = %q, want %q", path, verdict.SchemaVersion, "compound.panel_verdict.v0.1")
	}
	return verdict, nil
}

func RecordVerdictHumanReview(homePath, sourcePath, rationale string, now time.Time) (VerdictHumanReviewReport, error) {
	if strings.TrimSpace(homePath) == "" {
		return VerdictHumanReviewReport{}, fmt.Errorf("COMPOUND_HOME required")
	}
	rationale = strings.TrimSpace(rationale)
	if rationale == "" {
		return VerdictHumanReviewReport{}, fmt.Errorf("rationale required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	verdict, err := LoadVerdictArtifact(sourcePath)
	if err != nil {
		return VerdictHumanReviewReport{}, err
	}
	decision := normalizePanelPosition(verdict.Synthesis.Decision)
	switch decision {
	case "needs_human", "needs_trial":
	default:
		return VerdictHumanReviewReport{}, fmt.Errorf("panel verdict %s decision %q requires panel apply or separate owner mutation path; human review record only supports needs_human/needs_trial", verdict.ID, verdict.Synthesis.Decision)
	}
	sourceExists, sourceHash, err := reviewartifact.HashFileTarget(sourcePath)
	if err != nil {
		return VerdictHumanReviewReport{}, err
	}
	if !sourceExists {
		return VerdictHumanReviewReport{}, fmt.Errorf("panel verdict source not found: %s", sourcePath)
	}
	report := VerdictHumanReviewReport{
		SchemaVersion: VerdictHumanReviewReportSchemaVersion,
		ID:            reviewartifact.ID("pvr", verdict.ID, rationale),
		CreatedAt:     now.UTC().Format(time.RFC3339),
		Status:        "recorded",
		VerdictID:     verdict.ID,
		RunID:         verdict.RunID,
		Decision:      decision,
		TargetKind:    strings.TrimSpace(verdict.Target.Kind),
		TargetRef:     strings.TrimSpace(verdict.Target.Ref),
		Rationale:     rationale,
		NextAction:    "owner reviewed panel request; keep target parked until human evidence/action exists",
		SourceRef:     "file:" + sourcePath,
		SourceHash:    sourceHash,
		EvidenceRefs:  reviewartifact.UniqueSorted(verdict.EvidenceRefs),
		Boundaries: []string{
			"human review record only; no panel apply",
			"no memory candidate adjudication",
			"no capability observation mutation",
			"no route/model/provider mutation",
		},
	}
	dir, err := reviewartifact.Dir(homePath, verdictReviewArea)
	if err != nil {
		return VerdictHumanReviewReport{}, err
	}
	report.ReviewPath = filepath.Join(dir, verdict.ID+".reviewed.json")
	if _, err := reviewartifact.WriteJSON(homePath, verdictReviewArea, verdict.ID+".reviewed.json", report, " "); err != nil {
		return VerdictHumanReviewReport{}, err
	}
	return report, nil
}

func LoadVerdictHumanReviewReport(path string) (VerdictHumanReviewReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return VerdictHumanReviewReport{}, err
	}
	var report VerdictHumanReviewReport
	if err := json.Unmarshal(data, &report); err != nil {
		return VerdictHumanReviewReport{}, fmt.Errorf("parse panel verdict human review %s: %w", path, err)
	}
	if report.SchemaVersion != VerdictHumanReviewReportSchemaVersion {
		return VerdictHumanReviewReport{}, fmt.Errorf("panel verdict human review %s schema_version = %q, want %q", path, report.SchemaVersion, VerdictHumanReviewReportSchemaVersion)
	}
	return report, nil
}
