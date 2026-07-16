package authority

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/policy"
	"compound/internal/reviewartifact"
	"gopkg.in/yaml.v3"
)

const (
	WorktreeRetirementReviewSchemaVersion       = "compound.worktree_retirement_review.v0.1"
	WorktreeRetirementReviewReportSchemaVersion = "compound.worktree_retirement_review_report.v0.1"
)

type WorktreeRetirementPlanPaths struct {
	PlanPath       string `json:"plan_path"`
	ReviewPath     string `json:"review_path"`
	ReviewYAMLPath string `json:"review_yaml_path"`
}

type WorktreeRetirementReview struct {
	SchemaVersion string   `json:"schema_version" yaml:"schema_version"`
	ID            string   `json:"id" yaml:"id"`
	PlanID        string   `json:"plan_id" yaml:"plan_id"`
	PlanDigest    string   `json:"plan_digest" yaml:"plan_digest"`
	Decision      string   `json:"decision" yaml:"decision"`
	Rationale     string   `json:"rationale" yaml:"rationale"`
	ReviewedBy    string   `json:"reviewed_by" yaml:"reviewed_by"`
	ReviewedAt    string   `json:"reviewed_at,omitempty" yaml:"reviewed_at,omitempty"`
	EvidenceRefs  []string `json:"evidence_refs" yaml:"evidence_refs"`
}

type WorktreeRetirementReviewReport struct {
	SchemaVersion       string   `json:"schema_version"`
	ID                  string   `json:"id"`
	CreatedAt           string   `json:"created_at"`
	Status              string   `json:"status"`
	PlanID              string   `json:"plan_id"`
	PlanDigest          string   `json:"plan_digest"`
	PlanPath            string   `json:"plan_path"`
	ReviewID            string   `json:"review_id"`
	Decision            string   `json:"decision"`
	Rationale           string   `json:"rationale"`
	ReviewedBy          string   `json:"reviewed_by"`
	ReviewedAt          string   `json:"reviewed_at"`
	SourcePath          string   `json:"source_path"`
	SourceDigest        string   `json:"source_digest"`
	ReviewPath          string   `json:"review_path"`
	AuthorizationDigest string   `json:"authorization_digest"`
	EvidenceRefs        []string `json:"evidence_refs"`
}

func RecordWorktreeRetirementPlan(homePath string, plan WorktreeRetirementPlan) (WorktreeRetirementPlanPaths, error) {
	if strings.TrimSpace(homePath) == "" {
		return WorktreeRetirementPlanPaths{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if err := ValidateWorktreeRetirementPlan(plan); err != nil {
		return WorktreeRetirementPlanPaths{}, err
	}
	planBytes, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return WorktreeRetirementPlanPaths{}, err
	}
	planPath, err := writeExclusiveWorktreeRetirementArtifact(homePath, plan.ID+".retirement.json", append(planBytes, '\n'))
	if err != nil {
		return WorktreeRetirementPlanPaths{}, err
	}
	reviewPath, err := writeExclusiveWorktreeRetirementArtifact(homePath, plan.ID+".retirement.md", []byte(RenderWorktreeRetirementPlan(plan)))
	if err != nil {
		return WorktreeRetirementPlanPaths{}, err
	}
	reviewYAMLPath, err := writeExclusiveWorktreeRetirementArtifact(homePath, plan.ID+".retirement.review.yaml", []byte(RenderWorktreeRetirementReviewYAML(plan)))
	if err != nil {
		return WorktreeRetirementPlanPaths{}, err
	}
	return WorktreeRetirementPlanPaths{PlanPath: planPath, ReviewPath: reviewPath, ReviewYAMLPath: reviewYAMLPath}, nil
}

func ValidateWorktreeRetirementPlan(plan WorktreeRetirementPlan) error {
	if plan.SchemaVersion != WorktreeRetirementPlanSchemaVersion {
		return fmt.Errorf("worktree retirement plan schema_version must be %s", WorktreeRetirementPlanSchemaVersion)
	}
	if !strings.HasPrefix(plan.ID, "wtr_") || filepath.Base(plan.ID) != plan.ID {
		return fmt.Errorf("worktree retirement plan id invalid")
	}
	if !filepath.IsAbs(plan.RepoRoot) || !validRetirementHex(plan.RepositoryFingerprint, 64) {
		return fmt.Errorf("worktree retirement plan repository identity invalid")
	}
	if !validRetirementDigest(plan.PlanDigest) || plan.PlanDigest != WorktreeRetirementPlanDigest(plan) {
		return fmt.Errorf("worktree retirement plan digest mismatch")
	}
	if plan.ID != WorktreeRetirementPlanID(plan) {
		return fmt.Errorf("worktree retirement plan id invalid")
	}
	if !validRetirementObjectID(plan.AuthorityTip) || !validRetirementObjectID(plan.PublicationTip) {
		return fmt.Errorf("worktree retirement plan requires full sealed authority tips")
	}
	if !strings.HasPrefix(plan.AuthorityRef, "refs/heads/") || !strings.HasPrefix(plan.PublicationRef, "refs/remotes/") {
		return fmt.Errorf("worktree retirement plan requires canonical authority/publication refs")
	}
	if plan.CandidateRef == "" && plan.Worktree == nil {
		return fmt.Errorf("worktree retirement plan has no retirement target")
	}
	if plan.Eligible {
		if plan.Status != "review_required" || len(plan.BlockReasons) != 0 || (plan.CandidateRelation != WorktreeCandidateContained && plan.CandidateRelation != WorktreeCandidatePatchEquivalent) {
			return fmt.Errorf("eligible worktree retirement plan has inconsistent eligibility")
		}
		if !validRetirementObjectID(plan.CandidateTip) {
			return fmt.Errorf("eligible worktree retirement plan requires full candidate tip")
		}
		if err := validateRetirementOwnerProof(plan.OwnerProof); err != nil {
			return fmt.Errorf("worktree retirement owner proof: %w", err)
		}
		if err := validateRetirementSourceProof(plan.SourceProof); err != nil {
			return fmt.Errorf("worktree retirement source proof: %w", err)
		}
		if len(plan.RecoveryRefs) == 0 {
			return fmt.Errorf("worktree retirement recovery refs required")
		}
	} else if plan.Status != "blocked" || len(plan.BlockReasons) == 0 {
		return fmt.Errorf("blocked worktree retirement plan has no block reason")
	}
	return nil
}

func RenderWorktreeRetirementPlan(plan WorktreeRetirementPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Worktree Retirement Review: %s\n\n", plan.ID)
	fmt.Fprintf(&b, "- status: %s\n", plan.Status)
	fmt.Fprintf(&b, "- eligible: %v\n", plan.Eligible)
	fmt.Fprintf(&b, "- repo_root: %s\n", plan.RepoRoot)
	if plan.Worktree != nil {
		fmt.Fprintf(&b, "- worktree: %s\n", plan.Worktree.Path)
	}
	if plan.CandidateRef != "" {
		fmt.Fprintf(&b, "- candidate_ref: %s\n", plan.CandidateRef)
	}
	fmt.Fprintf(&b, "- candidate_tip: %s\n", plan.CandidateTip)
	fmt.Fprintf(&b, "- relation: %s\n", plan.CandidateRelation)
	fmt.Fprintf(&b, "- authority: %s @ %s\n", plan.AuthorityRef, plan.AuthorityTip)
	fmt.Fprintf(&b, "- publication: %s @ %s\n", plan.PublicationRef, plan.PublicationTip)
	fmt.Fprintf(&b, "- owner_work: %s\n", plan.OwnerProof.WorkID)
	fmt.Fprintf(&b, "- source_work: %s\n", plan.SourceProof.WorkID)
	fmt.Fprintf(&b, "- plan_digest: %s\n", plan.PlanDigest)
	if len(plan.BlockReasons) > 0 {
		fmt.Fprintln(&b, "\n## Block reasons")
		for _, reason := range plan.BlockReasons {
			fmt.Fprintf(&b, "\n- %s", reason)
		}
		fmt.Fprintln(&b)
	}
	fmt.Fprintln(&b, "\nPlanning writes review evidence only. Apply is the sole Git mutator and revalidates every sealed observation.")
	return b.String()
}

func RenderWorktreeRetirementReviewYAML(plan WorktreeRetirementPlan) string {
	review := WorktreeRetirementReview{
		SchemaVersion: WorktreeRetirementReviewSchemaVersion,
		ID:            "wtrr_" + digestRetirementHex([]byte(plan.ID + "|" + plan.PlanDigest))[:12],
		PlanID:        plan.ID,
		PlanDigest:    plan.PlanDigest,
		Decision:      "TODO_authorize_retirement",
		Rationale:     "",
		ReviewedBy:    "TODO_operator_provenance",
		EvidenceRefs:  worktreeRetirementEvidenceRefs(plan),
	}
	data, _ := yaml.Marshal(review)
	return string(data)
}

func LoadWorktreeRetirementPlan(path string) (WorktreeRetirementPlan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return WorktreeRetirementPlan{}, err
	}
	var plan WorktreeRetirementPlan
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plan); err != nil {
		return WorktreeRetirementPlan{}, err
	}
	if err := ValidateWorktreeRetirementPlan(plan); err != nil {
		return WorktreeRetirementPlan{}, err
	}
	return plan, nil
}

func LoadWorktreeRetirementReview(path string) (WorktreeRetirementReview, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return WorktreeRetirementReview{}, err
	}
	var review WorktreeRetirementReview
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&review); err != nil {
		return WorktreeRetirementReview{}, err
	}
	return review, nil
}

func RecordWorktreeRetirementReview(homePath string, review WorktreeRetirementReview, sourcePath string, now time.Time) (WorktreeRetirementReviewReport, error) {
	if now.IsZero() {
		now = time.Now()
	}
	review.ReviewedBy = strings.TrimSpace(review.ReviewedBy)
	review.Rationale = strings.TrimSpace(review.Rationale)
	review.ReviewedAt = strings.TrimSpace(review.ReviewedAt)
	review.EvidenceRefs = sortedUniqueNonEmpty(review.EvidenceRefs)
	if err := validateWorktreeRetirementReview(review); err != nil {
		return WorktreeRetirementReviewReport{}, err
	}
	expectedTemplatePath := filepath.Join(homePath, "reviews", SourceControlCleanupReviewArea, review.PlanID+".retirement.review.yaml")
	canonicalSource, err := filepath.Abs(filepath.Clean(sourcePath))
	if err != nil || canonicalSource != filepath.Clean(expectedTemplatePath) {
		return WorktreeRetirementReviewReport{}, fmt.Errorf("worktree retirement review source path must be %s", expectedTemplatePath)
	}
	planPath := filepath.Join(homePath, "reviews", SourceControlCleanupReviewArea, review.PlanID+".retirement.json")
	plan, err := LoadWorktreeRetirementPlan(planPath)
	if err != nil {
		return WorktreeRetirementReviewReport{}, err
	}
	if review.PlanDigest != plan.PlanDigest {
		return WorktreeRetirementReviewReport{}, fmt.Errorf("worktree retirement review plan digest mismatch")
	}
	if !plan.Eligible {
		return WorktreeRetirementReviewReport{}, fmt.Errorf("worktree retirement plan is not eligible: %s", strings.Join(plan.BlockReasons, "; "))
	}
	requiredEvidence := worktreeRetirementEvidenceRefs(plan)
	if !containsEvery(review.EvidenceRefs, requiredEvidence) {
		return WorktreeRetirementReviewReport{}, fmt.Errorf("worktree retirement review must preserve every plan evidence ref")
	}
	reviewedSourceName := plan.ID + "." + review.ID + ".retirement.reviewed.yaml"
	reviewedSourcePath := filepath.Join(homePath, "reviews", SourceControlCleanupReviewArea, reviewedSourceName)
	if review.ReviewedAt == "" {
		existing, err := LoadWorktreeRetirementReview(reviewedSourcePath)
		switch {
		case err == nil:
			if err := validateWorktreeRetirementReview(existing); err != nil {
				return WorktreeRetirementReviewReport{}, fmt.Errorf("existing worktree retirement reviewed source invalid: %w", err)
			}
			if !worktreeRetirementReviewEqualIgnoringReviewedAt(review, existing) {
				return WorktreeRetirementReviewReport{}, fmt.Errorf("worktree retirement reviewed source already exists with different requested content")
			}
			review.ReviewedAt = existing.ReviewedAt
		case os.IsNotExist(err):
			review.ReviewedAt = now.UTC().Format(time.RFC3339Nano)
		default:
			return WorktreeRetirementReviewReport{}, err
		}
	}
	if err := validateWorktreeRetirementReview(review); err != nil {
		return WorktreeRetirementReviewReport{}, err
	}
	reviewBytes, err := yaml.Marshal(review)
	if err != nil {
		return WorktreeRetirementReviewReport{}, err
	}
	reviewedSourcePath, err = writeExclusiveWorktreeRetirementArtifact(homePath, reviewedSourceName, reviewBytes)
	if err != nil {
		return WorktreeRetirementReviewReport{}, err
	}
	sourceDigest := "sha256:" + digestRetirementHex(reviewBytes)
	report := WorktreeRetirementReviewReport{
		SchemaVersion: WorktreeRetirementReviewReportSchemaVersion,
		ID:            "wtrrr_" + digestRetirementHex([]byte(plan.ID + "|" + review.ID + "|" + sourceDigest))[:12],
		CreatedAt:     review.ReviewedAt,
		Status:        "authorized",
		PlanID:        plan.ID,
		PlanDigest:    plan.PlanDigest,
		PlanPath:      planPath,
		ReviewID:      review.ID,
		Decision:      review.Decision,
		Rationale:     review.Rationale,
		ReviewedBy:    review.ReviewedBy,
		ReviewedAt:    review.ReviewedAt,
		SourcePath:    reviewedSourcePath,
		SourceDigest:  sourceDigest,
		EvidenceRefs:  sortedUniqueNonEmpty(append(requiredEvidence, sourceDigest, "worktree-retirement-reviewer:"+review.ReviewedBy)),
	}
	report.ReviewPath = filepath.Join(homePath, "reviews", SourceControlCleanupReviewArea, plan.ID+"."+review.ID+".retirement.reviewed.json")
	report.AuthorizationDigest = worktreeRetirementAuthorizationDigest(report)
	reportBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return WorktreeRetirementReviewReport{}, err
	}
	path, err := writeExclusiveWorktreeRetirementArtifact(homePath, filepath.Base(report.ReviewPath), append(reportBytes, '\n'))
	if err != nil {
		return WorktreeRetirementReviewReport{}, err
	}
	if filepath.Clean(path) != filepath.Clean(report.ReviewPath) {
		return WorktreeRetirementReviewReport{}, fmt.Errorf("worktree retirement review path mismatch")
	}
	return report, nil
}

func writeExclusiveWorktreeRetirementArtifact(homePath, name string, data []byte) (string, error) {
	if filepath.Base(name) != name || strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("worktree retirement artifact name invalid")
	}
	dir, err := reviewartifact.Dir(homePath, SourceControlCleanupReviewArea)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if !os.IsExist(err) {
			return "", err
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", readErr
		}
		if bytes.Equal(existing, data) {
			return path, nil
		}
		return "", fmt.Errorf("worktree retirement artifact %s already exists with different bytes", path)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func validateWorktreeRetirementReview(review WorktreeRetirementReview) error {
	if review.SchemaVersion != WorktreeRetirementReviewSchemaVersion || !strings.HasPrefix(review.ID, "wtrr_") || filepath.Base(review.PlanID) != review.PlanID {
		return fmt.Errorf("worktree retirement review identity invalid")
	}
	if review.Decision != "authorize_retirement" {
		return fmt.Errorf("worktree retirement review decision must be authorize_retirement")
	}
	if strings.TrimSpace(review.Rationale) == "" {
		return fmt.Errorf("worktree retirement review rationale required")
	}
	if !policy.HasOperatorProvenance(review.ReviewedBy) {
		return fmt.Errorf("worktree retirement review requires operator provenance")
	}
	if !validRetirementDigest(review.PlanDigest) || len(review.EvidenceRefs) == 0 {
		return fmt.Errorf("worktree retirement review plan digest and evidence required")
	}
	if review.ReviewedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, review.ReviewedAt); err != nil {
			return fmt.Errorf("worktree retirement review reviewed_at must be RFC3339Nano: %w", err)
		}
	}
	return nil
}

func worktreeRetirementReviewEqualIgnoringReviewedAt(a, b WorktreeRetirementReview) bool {
	return a.SchemaVersion == b.SchemaVersion && a.ID == b.ID && a.PlanID == b.PlanID && a.PlanDigest == b.PlanDigest &&
		a.Decision == b.Decision && a.Rationale == b.Rationale && a.ReviewedBy == b.ReviewedBy &&
		stringSlicesEqual(a.EvidenceRefs, b.EvidenceRefs)
}

func ValidateAuthorizedWorktreeRetirement(planPath, reportPath string) (WorktreeRetirementReviewReport, error) {
	plan, err := LoadWorktreeRetirementPlan(planPath)
	if err != nil {
		return WorktreeRetirementReviewReport{}, err
	}
	report, err := LoadWorktreeRetirementReviewReport(reportPath)
	if err != nil {
		return WorktreeRetirementReviewReport{}, err
	}
	if report.SchemaVersion != WorktreeRetirementReviewReportSchemaVersion || report.Status != "authorized" || report.Decision != "authorize_retirement" || !policy.HasOperatorProvenance(report.ReviewedBy) {
		return WorktreeRetirementReviewReport{}, fmt.Errorf("worktree retirement authorization report invalid")
	}
	if report.PlanID != plan.ID || report.PlanDigest != plan.PlanDigest || filepath.Clean(report.PlanPath) != filepath.Clean(planPath) {
		return WorktreeRetirementReviewReport{}, fmt.Errorf("worktree retirement authorization plan digest/path mismatch")
	}
	if !validRetirementDigest(report.AuthorizationDigest) || report.AuthorizationDigest != worktreeRetirementAuthorizationDigest(report) {
		return WorktreeRetirementReviewReport{}, fmt.Errorf("worktree retirement authorization digest mismatch")
	}
	if filepath.Clean(report.ReviewPath) != filepath.Clean(reportPath) {
		return WorktreeRetirementReviewReport{}, fmt.Errorf("worktree retirement authorization report path mismatch")
	}
	sourceBytes, err := os.ReadFile(report.SourcePath)
	if err != nil {
		return WorktreeRetirementReviewReport{}, err
	}
	if report.SourceDigest != "sha256:"+digestRetirementHex(sourceBytes) {
		return WorktreeRetirementReviewReport{}, fmt.Errorf("worktree retirement reviewed YAML digest mismatch")
	}
	review, err := LoadWorktreeRetirementReview(report.SourcePath)
	if err != nil {
		return WorktreeRetirementReviewReport{}, err
	}
	if err := validateWorktreeRetirementReview(review); err != nil {
		return WorktreeRetirementReviewReport{}, err
	}
	if review.ID != report.ReviewID || review.PlanID != report.PlanID || review.PlanDigest != report.PlanDigest || review.Decision != report.Decision || strings.TrimSpace(review.Rationale) != report.Rationale || strings.TrimSpace(review.ReviewedBy) != report.ReviewedBy || review.ReviewedAt != report.ReviewedAt {
		return WorktreeRetirementReviewReport{}, fmt.Errorf("worktree retirement review report does not match reviewed YAML")
	}
	if !containsEvery(review.EvidenceRefs, worktreeRetirementEvidenceRefs(plan)) {
		return WorktreeRetirementReviewReport{}, fmt.Errorf("worktree retirement reviewed YAML lost plan evidence")
	}
	return report, nil
}

func LoadWorktreeRetirementReviewReport(path string) (WorktreeRetirementReviewReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return WorktreeRetirementReviewReport{}, err
	}
	var report WorktreeRetirementReviewReport
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&report); err != nil {
		return WorktreeRetirementReviewReport{}, err
	}
	return report, nil
}

func worktreeRetirementAuthorizationDigest(report WorktreeRetirementReviewReport) string {
	report.AuthorizationDigest = ""
	data, _ := json.Marshal(report)
	return "sha256:" + digestRetirementHex(data)
}

func worktreeRetirementEvidenceRefs(plan WorktreeRetirementPlan) []string {
	refs := []string{
		"worktree-retirement-plan:" + plan.ID + ":" + plan.PlanDigest,
		"work:" + plan.OwnerProof.WorkID,
		"work:" + plan.SourceProof.WorkID,
		plan.SourceProof.CommitArtifactRef,
		plan.SourceProof.LandCheckEvidenceRef,
		plan.SourceProof.LandCheckFileDigest,
		"repo-fingerprint:" + plan.RepositoryFingerprint,
	}
	refs = append(refs, plan.RecoveryRefs...)
	return sortedUniqueNonEmpty(refs)
}
