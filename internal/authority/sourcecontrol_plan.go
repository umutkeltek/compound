package authority

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const SourceControlReviewPlanSchemaVersion = "compound.source_control_review_plan.v0.1"

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
	PlanPath   string
	ReviewPath string
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
	reviewID := "scr_" + digestSourceControlPlan(key)
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
	dir := filepath.Join(homePath, "reviews", "source-control-cleanup")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return SourceControlReviewPlanPaths{}, err
	}
	paths := SourceControlReviewPlanPaths{
		PlanPath:   filepath.Join(dir, plan.ID+".json"),
		ReviewPath: filepath.Join(dir, plan.ID+".md"),
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return paths, err
	}
	if err := os.WriteFile(paths.PlanPath, append(data, '\n'), 0o644); err != nil {
		return paths, err
	}
	if err := os.WriteFile(paths.ReviewPath, []byte(RenderSourceControlReviewPlan(plan)), 0o644); err != nil {
		return paths, err
	}
	return paths, nil
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

func digestSourceControlPlan(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}
