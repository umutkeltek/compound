package authority

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/reviewartifact"
	"gopkg.in/yaml.v3"
)

const (
	SourceControlReviewPlanSchemaVersion   = "compound.source_control_review_plan.v0.1"
	SourceControlReviewSchemaVersion       = "compound.source_control_review.v0.1"
	SourceControlReviewReportSchemaVersion = "compound.source_control_review_report.v0.1"
	SourceControlCleanupReviewArea         = "source-control-cleanup"
)

// SourceControlReviewPlan turns a dirty source-control snapshot into an
// owner-review artifact. It never stages, cleans, commits, resets, pushes, or
// promotes source-control claims.
type SourceControlReviewPlan struct {
	SchemaVersion string                   `json:"schema_version"`
	ID            string                   `json:"id"`
	CreatedAt     string                   `json:"created_at"`
	Status        string                   `json:"status"`
	RepoID        string                   `json:"repo_id"`
	WorkID        string                   `json:"work_id,omitempty"`
	RequestedPath string                   `json:"requested_path,omitempty"`
	GitRoot       string                   `json:"git_root"`
	Remote        string                   `json:"remote,omitempty"`
	Branch        string                   `json:"branch,omitempty"`
	Head          string                   `json:"head,omitempty"`
	Upstream      string                   `json:"upstream,omitempty"`
	Ahead         int                      `json:"ahead"`
	Behind        int                      `json:"behind"`
	SourceStatus  string                   `json:"source_status"`
	Staged        int                      `json:"staged"`
	Unstaged      int                      `json:"unstaged"`
	Untracked     int                      `json:"untracked"`
	ChangedPaths  int                      `json:"changed_paths"`
	EvidenceRefs  []string                 `json:"evidence_refs"`
	ReviewDraft   SourceControlReviewDraft `json:"review_draft"`
	Warnings      []string                 `json:"warnings,omitempty"`
}

type SourceControlReviewDraft struct {
	ReviewID          string   `json:"review_id"`
	Title             string   `json:"title"`
	Situation         string   `json:"situation"`
	ProposedNextStep  string   `json:"proposed_next_step"`
	CleanupGuardrail  string   `json:"cleanup_guardrail"`
	Verification      string   `json:"verification"`
	RequiredEvidence  []string `json:"required_evidence"`
	RetirementTrigger string   `json:"retirement_trigger"`
	ReviewNote        string   `json:"review_note"`
}

type SourceControlReviewPlanPaths struct {
	PlanPath       string `json:"plan_path"`
	ReviewPath     string `json:"review_path"`
	ReviewYAMLPath string `json:"review_yaml_path"`
}

type SourceControlReview struct {
	SchemaVersion string   `json:"schema_version" yaml:"schema_version"`
	ID            string   `json:"id" yaml:"id"`
	PlanID        string   `json:"plan_id" yaml:"plan_id"`
	RepoID        string   `json:"repo_id" yaml:"repo_id"`
	Decision      string   `json:"decision" yaml:"decision"`
	Rationale     string   `json:"rationale" yaml:"rationale"`
	Confidence    float64  `json:"confidence" yaml:"confidence"`
	NextAction    string   `json:"next_action,omitempty" yaml:"next_action,omitempty"`
	EvidenceRefs  []string `json:"evidence_refs" yaml:"evidence_refs"`
	ReviewedAt    string   `json:"reviewed_at,omitempty" yaml:"reviewed_at,omitempty"`
	SourceRef     string   `json:"source_ref,omitempty" yaml:"source_ref,omitempty"`
}

type SourceControlReviewReport struct {
	SchemaVersion string   `json:"schema_version"`
	ID            string   `json:"id"`
	CreatedAt     string   `json:"created_at"`
	Status        string   `json:"status"`
	PlanID        string   `json:"plan_id"`
	ReviewID      string   `json:"review_id"`
	RepoID        string   `json:"repo_id"`
	Decision      string   `json:"decision"`
	NextAction    string   `json:"next_action,omitempty"`
	SourcePath    string   `json:"source_path,omitempty"`
	ReviewPath    string   `json:"review_path,omitempty"`
	EvidenceRefs  []string `json:"evidence_refs,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
}

func BuildSourceControlReviewPlan(snapshot SourceControlSnapshot, workID string, now time.Time) (SourceControlReviewPlan, error) {
	if strings.TrimSpace(snapshot.RepoID) == "" {
		return SourceControlReviewPlan{}, fmt.Errorf("source-control snapshot repo_id required")
	}
	if strings.TrimSpace(snapshot.GitRoot) == "" {
		return SourceControlReviewPlan{}, fmt.Errorf("source-control snapshot git_root required")
	}
	if snapshot.Status != "dirty" || snapshot.ChangedPaths == 0 {
		return SourceControlReviewPlan{}, fmt.Errorf("dirty source-control snapshot required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	evidence := snapshot.EvidenceReference()
	key := snapshot.RepoID + snapshot.GitRoot + snapshot.Head + fmt.Sprint(snapshot.Staged, snapshot.Unstaged, snapshot.Untracked) + strings.TrimSpace(workID)
	reviewID := "scr_" + reviewartifact.Digest12(key)
	plan := SourceControlReviewPlan{
		SchemaVersion: SourceControlReviewPlanSchemaVersion,
		ID:            reviewID,
		CreatedAt:     now.UTC().Format(time.RFC3339),
		Status:        "review_required",
		RepoID:        snapshot.RepoID,
		WorkID:        strings.TrimSpace(workID),
		RequestedPath: snapshot.RequestedPath,
		GitRoot:       snapshot.GitRoot,
		Remote:        snapshot.Remote,
		Branch:        snapshot.Branch,
		Head:          snapshot.Head,
		Upstream:      snapshot.Upstream,
		Ahead:         snapshot.Ahead,
		Behind:        snapshot.Behind,
		SourceStatus:  snapshot.Status,
		Staged:        snapshot.Staged,
		Unstaged:      snapshot.Unstaged,
		Untracked:     snapshot.Untracked,
		ChangedPaths:  snapshot.ChangedPaths,
		EvidenceRefs:  []string{evidence},
		ReviewDraft: SourceControlReviewDraft{
			ReviewID:         reviewID,
			Title:            "Review dirty source-control state for " + snapshot.RepoID,
			Situation:        fmt.Sprintf("%d changed path(s): %d staged, %d unstaged, %d untracked", snapshot.ChangedPaths, snapshot.Staged, snapshot.Unstaged, snapshot.Untracked),
			ProposedNextStep: "Classify each change as keep, park, commit, or discard in the owning repo before continuing mutation work.",
			CleanupGuardrail: "Do not run git add, commit, reset, checkout, clean, pull, push, or stash from this plan. Cleanup requires an explicit owner action and separate receipt.",
			Verification:     "A follow-up read-only source-control snapshot shows the repo clean or the remaining dirty paths are intentionally parked and cited.",
			RequiredEvidence: []string{
				"read-only source-control snapshot evidence",
				"owner decision for each staged/unstaged/untracked class",
				"post-review source-control snapshot",
				"receipt for any actual cleanup action",
			},
			RetirementTrigger: "Close this review when dirty ambiguity no longer blocks the held work or when the owner deliberately parks the remaining changes.",
			ReviewNote:        "This plan makes dirty source-control ambiguity visible. It is not a cleanup script and does not change repository state.",
		},
		Warnings: []string{
			"does not stage files",
			"does not reset checkout clean stash commit pull or push",
			"does not promote source-control claims",
			"does not resolve policy holds by itself",
		},
	}
	return plan, nil
}

func RecordSourceControlReviewPlan(homePath string, plan SourceControlReviewPlan) (SourceControlReviewPlanPaths, error) {
	if strings.TrimSpace(homePath) == "" {
		return SourceControlReviewPlanPaths{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if strings.TrimSpace(plan.ID) == "" {
		return SourceControlReviewPlanPaths{}, fmt.Errorf("source-control review plan id required")
	}
	paths, err := reviewartifact.Write(homePath, SourceControlCleanupReviewArea, plan.ID, plan, "  ", RenderSourceControlReviewPlan(plan))
	if err != nil {
		return SourceControlReviewPlanPaths{}, err
	}
	reviewYAMLPath, err := reviewartifact.WriteRaw(homePath, SourceControlCleanupReviewArea, plan.ID+".review.yaml", []byte(RenderSourceControlReviewYAMLDraft(plan)))
	if err != nil {
		return SourceControlReviewPlanPaths{}, err
	}
	return SourceControlReviewPlanPaths{PlanPath: paths.PlanPath, ReviewPath: paths.ReviewPath, ReviewYAMLPath: reviewYAMLPath}, nil
}

func RenderSourceControlReviewPlan(plan SourceControlReviewPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Source-Control Cleanup Review: %s\n\n", plan.RepoID)
	fmt.Fprintf(&b, "- status: %s\n", plan.Status)
	fmt.Fprintf(&b, "- repo: %s\n", plan.RepoID)
	if plan.WorkID != "" {
		fmt.Fprintf(&b, "- work: %s\n", plan.WorkID)
	}
	fmt.Fprintf(&b, "- git_root: %s\n", plan.GitRoot)
	if plan.Branch != "" {
		fmt.Fprintf(&b, "- branch: %s\n", plan.Branch)
	}
	if plan.Head != "" {
		fmt.Fprintf(&b, "- head: %s\n", plan.Head)
	}
	fmt.Fprintf(&b, "- status_snapshot: %s\n", plan.SourceStatus)
	fmt.Fprintf(&b, "- changed_paths: %d\n", plan.ChangedPaths)
	fmt.Fprintf(&b, "- staged: %d\n", plan.Staged)
	fmt.Fprintf(&b, "- unstaged: %d\n", plan.Unstaged)
	fmt.Fprintf(&b, "- untracked: %d\n", plan.Untracked)
	fmt.Fprintf(&b, "- evidence_refs: %s\n\n", strings.Join(plan.EvidenceRefs, ", "))
	fmt.Fprintln(&b, "This owner-review artifact does not stage, reset, clean, commit, push, or promote source-control claims.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Draft Review")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "- situation: %s\n", plan.ReviewDraft.Situation)
	fmt.Fprintf(&b, "- proposed_next_step: %s\n", plan.ReviewDraft.ProposedNextStep)
	fmt.Fprintf(&b, "- cleanup_guardrail: %s\n", plan.ReviewDraft.CleanupGuardrail)
	fmt.Fprintf(&b, "- verification: %s\n", plan.ReviewDraft.Verification)
	fmt.Fprintf(&b, "- retirement_trigger: %s\n", plan.ReviewDraft.RetirementTrigger)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Required Evidence")
	fmt.Fprintln(&b)
	for _, evidence := range plan.ReviewDraft.RequiredEvidence {
		fmt.Fprintf(&b, "- %s\n", evidence)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Review YAML Template")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "```yaml")
	fmt.Fprint(&b, RenderSourceControlReviewYAMLDraft(plan))
	fmt.Fprintln(&b, "```")
	if len(plan.Warnings) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Warnings")
		fmt.Fprintln(&b)
		for _, warning := range plan.Warnings {
			fmt.Fprintf(&b, "- %s\n", warning)
		}
	}
	return b.String()
}

func RenderSourceControlReviewYAMLDraft(plan SourceControlReviewPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "schema_version: %s\n", SourceControlReviewSchemaVersion)
	fmt.Fprintf(&b, "id: %s\n", plan.ReviewDraft.ReviewID)
	fmt.Fprintf(&b, "plan_id: %s\n", plan.ID)
	fmt.Fprintf(&b, "repo_id: %q\n", plan.RepoID)
	fmt.Fprintln(&b, "decision: TODO_decision_owner_action_required_owner_action_done_parked_no_action")
	fmt.Fprintln(&b, "rationale: \"\"")
	fmt.Fprintln(&b, "confidence: -1")
	fmt.Fprintln(&b, "next_action: \"TODO: reviewed source-control cleanup next action\"")
	fmt.Fprintln(&b, "evidence_refs:")
	for _, ref := range plan.EvidenceRefs {
		fmt.Fprintf(&b, " - %q\n", ref)
	}
	return b.String()
}

func LoadSourceControlReview(path string) (SourceControlReview, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SourceControlReview{}, err
	}
	var review SourceControlReview
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&review); err != nil {
		return SourceControlReview{}, err
	}
	return review, nil
}

func ValidateSourceControlReview(review SourceControlReview) error {
	if review.SchemaVersion != SourceControlReviewSchemaVersion {
		return fmt.Errorf("source-control review schema_version must be %s, got %q", SourceControlReviewSchemaVersion, review.SchemaVersion)
	}
	if strings.TrimSpace(review.ID) == "" || strings.TrimSpace(review.PlanID) == "" {
		return fmt.Errorf("source-control review id plan_id required")
	}
	if review.PlanID != filepath.Base(review.PlanID) {
		return fmt.Errorf("source-control review plan_id %q must be bare id", review.PlanID)
	}
	if strings.TrimSpace(review.RepoID) == "" {
		return fmt.Errorf("source-control review repo_id required")
	}
	if !sourceControlReviewOneOf(review.Decision, []string{"owner_action_required", "owner_action_done", "parked", "no_action"}) {
		return fmt.Errorf("source-control review decision must be owner_action_required|owner_action_done|parked|no_action, got %q", review.Decision)
	}
	if strings.TrimSpace(review.Rationale) == "" {
		return fmt.Errorf("source-control review rationale required")
	}
	if review.Confidence < 0 || review.Confidence > 1 {
		return fmt.Errorf("source-control review confidence must be between 0 and 1")
	}
	if len(reviewartifact.Unique(review.EvidenceRefs)) == 0 {
		return fmt.Errorf("source-control review evidence_refs required")
	}
	return nil
}

func RecordSourceControlReview(homePath string, review SourceControlReview, sourcePath string, now time.Time) (SourceControlReviewReport, error) {
	createdAt := reviewartifact.Timestamp(now)
	report := SourceControlReviewReport{
		SchemaVersion: SourceControlReviewReportSchemaVersion,
		ID:            reviewartifact.ID("scrr_", review.PlanID, review.ID, review.Decision, createdAt),
		CreatedAt:     createdAt,
		Status:        "recorded",
		PlanID:        review.PlanID,
		ReviewID:      review.ID,
		RepoID:        review.RepoID,
		Decision:      review.Decision,
		NextAction:    strings.TrimSpace(review.NextAction),
		SourcePath:    strings.TrimSpace(sourcePath),
		EvidenceRefs:  reviewartifact.Unique(review.EvidenceRefs),
		Warnings: []string{
			"does not stage files",
			"does not reset checkout clean stash commit pull or push",
			"does not promote source-control claims",
			"does not resolve policy holds by itself",
		},
	}
	if err := ValidateSourceControlReview(review); err != nil {
		report.Status = "blocked"
		return report, err
	}
	plan, err := loadSourceControlPlanForReview(sourcePath, review.PlanID)
	if err != nil {
		report.Status = "blocked"
		return report, err
	}
	if plan.ID != review.PlanID {
		report.Status = "blocked"
		return report, fmt.Errorf("source-control review plan_id %q does not match plan %q", review.PlanID, plan.ID)
	}
	if strings.TrimSpace(plan.RepoID) != "" && review.RepoID != plan.RepoID {
		report.Status = "blocked"
		return report, fmt.Errorf("source-control review repo_id %q does not match plan repo_id %q", review.RepoID, plan.RepoID)
	}
	if review.ReviewedAt == "" {
		review.ReviewedAt = createdAt
	}
	review.SourceRef = "source-control-cleanup-review:" + filepath.Base(sourcePath)
	report.RepoID = reviewartifact.FirstNonEmpty(report.RepoID, plan.RepoID)
	report.ReviewPath = filepath.Join(filepath.Dir(sourcePath), review.PlanID+"."+review.ID+".reviewed.json")
	if _, err := reviewartifact.WriteJSON(homePath, SourceControlCleanupReviewArea, filepath.Base(report.ReviewPath), report, " "); err != nil {
		return SourceControlReviewReport{}, err
	}
	return report, nil
}

func LoadSourceControlReviewReport(path string) (SourceControlReviewReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SourceControlReviewReport{}, err
	}
	var report SourceControlReviewReport
	if err := json.Unmarshal(data, &report); err != nil {
		return SourceControlReviewReport{}, err
	}
	return report, nil
}

func AnnotateSourceControlCleanupQueue(report *reviewartifact.QueueReport) {
	if report == nil {
		return
	}
	for i := range report.Items {
		item := &report.Items[i]
		if item.Area != SourceControlCleanupReviewArea {
			continue
		}
		annotateSourceControlCleanupItem(item)
	}
}

func annotateSourceControlCleanupItem(item *reviewartifact.QueueItem) {
	if item.JSONPath == "" {
		item.Readiness = "blocked"
		item.BlockedReason = "source-control cleanup plan path missing"
		item.NextAction = "regenerate dirty-plan"
		return
	}
	if reviewedPath := sourceControlReviewedPath(*item); reviewedPath != "" {
		item.Status = "recorded"
		item.Readiness = "review_recorded"
		item.NextAction = "source-control cleanup review recorded"
		if report, err := LoadSourceControlReviewReport(reviewedPath); err == nil {
			item.NextAction = reviewartifact.FirstNonEmpty(report.NextAction, "source-control cleanup review recorded: "+report.Decision)
		}
		item.Actions = nil
		return
	}
	reviewYAML := sourceControlReviewYAMLPath(*item)
	if reviewYAML == "" {
		item.Readiness = "blocked"
		item.BlockedReason = "source-control cleanup review YAML draft missing"
		item.NextAction = "regenerate dirty-plan to create review YAML draft"
		return
	}
	argv := []string{"compound", "authority", "dirty-review", reviewYAML}
	item.Actions = []reviewartifact.Action{{
		ID:             "record_source_control_cleanup_review",
		Label:          "Record source-control cleanup review",
		Command:        sourceControlShellJoin(argv),
		Argv:           argv,
		RequiresReview: true,
	}}
	review, err := LoadSourceControlReview(reviewYAML)
	if err != nil {
		item.Readiness = "blocked"
		item.BlockedReason = "source-control cleanup review YAML unreadable: " + sourceControlQueueError(err)
		item.NextAction = "fix source-control cleanup review YAML before recording"
		return
	}
	if err := ValidateSourceControlReview(review); err != nil {
		item.Readiness = "needs_review"
		item.BlockedReason = sourceControlQueueError(err)
		item.NextAction = "fill valid source-control cleanup review YAML before recording"
		return
	}
	item.Readiness = "ready_to_record"
	item.NextAction = "record reviewed source-control cleanup disposition"
}

func loadSourceControlPlanForReview(reviewPath, planID string) (SourceControlReviewPlan, error) {
	if strings.TrimSpace(reviewPath) == "" {
		return SourceControlReviewPlan{}, fmt.Errorf("source-control review source path required")
	}
	if strings.TrimSpace(planID) == "" || planID != filepath.Base(planID) {
		return SourceControlReviewPlan{}, fmt.Errorf("source-control review plan_id %q must be bare id", planID)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(reviewPath), planID+".json"))
	if err != nil {
		return SourceControlReviewPlan{}, err
	}
	var plan SourceControlReviewPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return SourceControlReviewPlan{}, err
	}
	return plan, nil
}

func sourceControlReviewYAMLPath(item reviewartifact.QueueItem) string {
	want := item.ID + ".review.yaml"
	for _, path := range item.ExtraPaths {
		if filepath.Base(path) == want {
			return path
		}
	}
	return ""
}

func sourceControlReviewedPath(item reviewartifact.QueueItem) string {
	for _, path := range item.ExtraPaths {
		name := filepath.Base(path)
		if strings.HasPrefix(name, item.ID+".") && strings.HasSuffix(name, ".reviewed.json") {
			return path
		}
	}
	return ""
}

func sourceControlReviewOneOf(value string, allowed []string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func sourceControlShellJoin(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		if strings.ContainsAny(arg, " \t\n\"'") {
			parts = append(parts, "'"+strings.ReplaceAll(arg, "'", "'\\''")+"'")
			continue
		}
		parts = append(parts, arg)
	}
	return strings.Join(parts, " ")
}

func sourceControlQueueError(err error) string {
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "unknown source-control cleanup review error"
	}
	return msg
}
