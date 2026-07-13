package work

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/envelope"
)

const (
	LandCheckSchemaVersion  = "compound.work_land_check.v0.1"
	landCheckEvidencePrefix = "land-check:"
	landCheckIDPrefix       = "landcheck_"
)

// LandCheckOptions names every source-control input explicitly. In particular,
// RepoPath is never inferred from the process working directory.
type LandCheckOptions struct {
	RepoPath        string
	CandidateTipSHA string
	TargetRef       string
	ExpectedMainSHA string
	Record          bool
}

// LandCheckReport is durable evidence about one candidate tip and one target
// branch for one WorkEnvelope. Non-landed results are recorded too, so a stale
// executor leaves an actionable state rather than disappearing from the queue.
type LandCheckReport struct {
	SchemaVersion                   string `json:"schema_version"`
	ID                              string `json:"id"`
	GeneratedAt                     string `json:"generated_at"`
	WorkID                          string `json:"work_id"`
	Status                          string `json:"status"`
	RepoPath                        string `json:"repo_path"`
	CandidateTipSHA                 string `json:"candidate_tip_sha"`
	ExpectedBaseSHA                 string `json:"expected_base_sha,omitempty"`
	TargetRef                       string `json:"target_ref"`
	TargetBranchRef                 string `json:"target_branch_ref"`
	ExpectedMainSHA                 string `json:"expected_main_sha,omitempty"`
	TargetSHA                       string `json:"target_sha"`
	CandidateExists                 bool   `json:"candidate_exists"`
	ExpectedBaseExists              bool   `json:"expected_base_exists"`
	CleanTarget                     bool   `json:"clean_target"`
	TargetBranchWorktree            bool   `json:"target_branch_worktree"`
	ExpectedBaseAncestorOfCandidate bool   `json:"expected_base_ancestor_of_candidate"`
	TargetAncestorOfCandidate       bool   `json:"target_ancestor_of_candidate"`
	CandidateContainedInTarget      bool   `json:"candidate_contained_in_target"`
	NextAction                      string `json:"next_action,omitempty"`
	Recorded                        bool   `json:"recorded"`
	EvidenceRef                     string `json:"evidence_ref,omitempty"`
	ProofPath                       string `json:"proof_path,omitempty"`
}

// LandCheck evaluates and optionally records the landing invariant for workID.
// A classification is not an error: dirty, stale, missing, and divergent states
// are useful evidence. Invalid caller inputs and unreadable repositories are errors.
func (s *Store) LandCheck(workID string, opts LandCheckOptions, now time.Time) (LandCheckReport, error) {
	w, err := s.Get(strings.TrimSpace(workID))
	if err != nil {
		return LandCheckReport{}, err
	}
	report, err := s.evaluateLandCheck(w, opts, now)
	if err != nil {
		return report, err
	}
	report.ID = idFor(landCheckIDPrefix, w.ID, report.CandidateTipSHA, report.TargetRef, report.TargetSHA, report.Status, ts(now))
	if !opts.Record {
		return report, nil
	}
	if err := s.recordLandCheck(&report); err != nil {
		return report, err
	}
	return report, nil
}

func (s *Store) evaluateLandCheck(w envelope.WorkEnvelope, opts LandCheckOptions, now time.Time) (LandCheckReport, error) {
	if now.IsZero() {
		now = time.Now()
	}
	repoPath, err := canonicalLandingRepo(opts.RepoPath)
	if err != nil {
		return LandCheckReport{}, err
	}
	candidate := strings.TrimSpace(opts.CandidateTipSHA)
	if !validLandingObjectID(candidate) {
		return LandCheckReport{}, fmt.Errorf("candidate tip SHA must be a lowercase full Git object id (40 or 64 hex characters)")
	}
	expectedMain := strings.TrimSpace(opts.ExpectedMainSHA)
	if expectedMain != "" && !validLandingObjectID(expectedMain) {
		return LandCheckReport{}, fmt.Errorf("expected main SHA must be a lowercase full Git object id (40 or 64 hex characters)")
	}
	targetRef, targetBranchRef, err := normalizeLandingTargetRef(repoPath, opts.TargetRef)
	if err != nil {
		return LandCheckReport{}, err
	}
	targetSHA, err := runLandingGitOutput(repoPath, "rev-parse", "--verify", targetBranchRef+"^{commit}")
	if err != nil {
		return LandCheckReport{}, fmt.Errorf("resolve target branch %s: %w", targetRef, err)
	}
	if !validLandingObjectID(targetSHA) {
		return LandCheckReport{}, fmt.Errorf("target branch %s did not resolve to a full Git object id", targetRef)
	}
	currentBranchRef, err := landingCurrentBranchRef(repoPath)
	if err != nil {
		return LandCheckReport{}, err
	}
	statusOutput, err := runLandingGitOutput(repoPath, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return LandCheckReport{}, fmt.Errorf("inspect target worktree status: %w", err)
	}

	expectedBase := strings.TrimSpace(w.ExpectedBaseSHA)
	report := LandCheckReport{
		SchemaVersion:        LandCheckSchemaVersion,
		GeneratedAt:          now.UTC().Format(time.RFC3339),
		WorkID:               w.ID,
		RepoPath:             repoPath,
		CandidateTipSHA:      candidate,
		ExpectedBaseSHA:      expectedBase,
		TargetRef:            targetRef,
		TargetBranchRef:      targetBranchRef,
		ExpectedMainSHA:      expectedMain,
		TargetSHA:            targetSHA,
		CleanTarget:          statusOutput == "",
		TargetBranchWorktree: currentBranchRef == targetBranchRef,
	}

	if expectedBase == "" {
		report.Status = "missing_expected_base"
		report.NextAction = "attach a lowercase full expected_base_sha to the WorkEnvelope before source-controlled execution"
		return report, nil
	}
	if !validLandingObjectID(expectedBase) {
		return report, fmt.Errorf("work %s expected_base_sha must be a lowercase full Git object id (40 or 64 hex characters)", w.ID)
	}
	if !report.TargetBranchWorktree {
		report.Status = "wrong_target_worktree"
		report.NextAction = fmt.Sprintf("check out %s in the explicit target worktree, then rerun land-check", targetRef)
		return report, nil
	}

	report.CandidateExists, err = landingCommitExists(repoPath, candidate)
	if err != nil {
		return report, err
	}
	if !report.CandidateExists {
		report.Status = "invalid_lineage"
		report.NextAction = fmt.Sprintf("recover candidate tip %s into this repository, then rerun land-check", candidate)
		return report, nil
	}
	report.ExpectedBaseExists, err = landingCommitExists(repoPath, expectedBase)
	if err != nil {
		return report, err
	}
	if !report.ExpectedBaseExists {
		report.Status = "invalid_lineage"
		report.NextAction = fmt.Sprintf("recover expected base %s into this repository or replace the malformed envelope before landing", expectedBase)
		return report, nil
	}
	report.ExpectedBaseAncestorOfCandidate, err = landingIsAncestor(repoPath, expectedBase, candidate)
	if err != nil {
		return report, err
	}
	if !report.ExpectedBaseAncestorOfCandidate {
		report.Status = "invalid_lineage"
		report.NextAction = fmt.Sprintf("rebuild candidate %s from expected base %s; this tip cannot satisfy the envelope", candidate, expectedBase)
		return report, nil
	}
	if !report.CleanTarget {
		report.Status = "dirty_target"
		report.NextAction = fmt.Sprintf("make the %s target worktree clean without discarding unrelated work, then rerun land-check", targetRef)
		return report, nil
	}
	if expectedMain != "" && targetSHA != expectedMain {
		report.Status = "main_moved"
		report.NextAction = fmt.Sprintf("target %s moved from expected %s to %s; refresh the snapshot and reevaluate the candidate", targetRef, expectedMain, targetSHA)
		return report, nil
	}

	report.CandidateContainedInTarget, err = landingIsAncestor(repoPath, candidate, targetSHA)
	if err != nil {
		return report, err
	}
	report.TargetAncestorOfCandidate, err = landingIsAncestor(repoPath, targetSHA, candidate)
	if err != nil {
		return report, err
	}
	if report.CandidateContainedInTarget {
		report.Status = "landed"
		return report, nil
	}
	if report.TargetAncestorOfCandidate {
		report.Status = "ready_to_land"
		report.NextAction = fmt.Sprintf("fast-forward %s to candidate %s, then record a fresh landed check", targetRef, candidate)
		return report, nil
	}
	report.Status = "rebase_required"
	report.NextAction = fmt.Sprintf("rebase candidate %s onto current %s tip %s, retain the new full tip SHA, and rerun land-check", candidate, targetRef, targetSHA)
	return report, nil
}

func (s *Store) recordLandCheck(report *LandCheckReport) error {
	if report == nil || strings.TrimSpace(report.ID) == "" {
		return fmt.Errorf("land-check report id required")
	}
	dir := filepath.Join(s.home, "work", "land-checks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, report.ID+".json")
	report.Recorded = true
	report.EvidenceRef = landCheckEvidencePrefix + report.ID
	report.ProofPath = path
	staged, err := s.stageJSON(path, report)
	if err != nil {
		return err
	}
	if err := commitStagedJSONExclusive(staged, path); err != nil {
		_ = os.Remove(staged)
		return err
	}
	return nil
}

func (s *Store) recordedLandedProofForClose(w envelope.WorkEnvelope, proofRefs []string) (LandCheckReport, error) {
	seen := false
	var failures []string
	for _, ref := range proofRefs {
		id, ok := landCheckIDFromEvidenceRef(ref)
		if !ok {
			continue
		}
		seen = true
		var report LandCheckReport
		if err := s.readJSON(filepath.Join(s.home, "work", "land-checks", id+".json"), &report); err != nil {
			failures = append(failures, fmt.Sprintf("%s is not a recorded land-check: %v", ref, err))
			continue
		}
		if err := validateRecordedLandCheck(w, ref, id, report); err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if report.Status != "landed" {
			failures = append(failures, fmt.Sprintf("recorded land-check %s is not landed (status %s)", id, report.Status))
			continue
		}
		return report, nil
	}
	if !seen {
		return LandCheckReport{}, fmt.Errorf("completed source-controlled work %s requires a cited recorded land-check proof", w.ID)
	}
	return LandCheckReport{}, fmt.Errorf("completed source-controlled work %s has no acceptable landed proof: %s", w.ID, strings.Join(failures, "; "))
}

func validateRecordedLandCheck(w envelope.WorkEnvelope, evidenceRef, id string, report LandCheckReport) error {
	if report.SchemaVersion != LandCheckSchemaVersion || !report.Recorded {
		return fmt.Errorf("recorded land-check %s has invalid schema or recording state", id)
	}
	if report.ID != id || report.EvidenceRef != evidenceRef {
		return fmt.Errorf("recorded land-check %s identity does not match cited evidence", id)
	}
	if report.WorkID != w.ID {
		return fmt.Errorf("recorded land-check %s belongs to work %s, not %s", id, report.WorkID, w.ID)
	}
	if report.ExpectedBaseSHA != w.ExpectedBaseSHA {
		return fmt.Errorf("recorded land-check %s expected base no longer matches work %s", id, w.ID)
	}
	if !validLandingObjectID(report.CandidateTipSHA) || strings.TrimSpace(report.RepoPath) == "" || strings.TrimSpace(report.TargetRef) == "" {
		return fmt.Errorf("recorded land-check %s is missing exact repository, candidate, or target identity", id)
	}
	return nil
}

func (s *Store) revalidateLandedProof(w envelope.WorkEnvelope, proof LandCheckReport, now time.Time) error {
	current, err := s.evaluateLandCheck(w, LandCheckOptions{
		RepoPath:        proof.RepoPath,
		CandidateTipSHA: proof.CandidateTipSHA,
		TargetRef:       proof.TargetRef,
	}, now)
	if err != nil {
		return fmt.Errorf("land-check %s live revalidation failed: %w", proof.ID, err)
	}
	if current.Status != "landed" {
		return fmt.Errorf("land-check %s live revalidation status %s: %s", proof.ID, current.Status, current.NextAction)
	}
	return nil
}

func landCheckIDFromEvidenceRef(ref string) (string, bool) {
	if !strings.HasPrefix(ref, landCheckEvidencePrefix) {
		return "", false
	}
	id := strings.TrimPrefix(ref, landCheckEvidencePrefix)
	if len(id) != len(landCheckIDPrefix)+12 || !strings.HasPrefix(id, landCheckIDPrefix) {
		return "", false
	}
	for _, r := range strings.TrimPrefix(id, landCheckIDPrefix) {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return "", false
		}
	}
	return id, true
}

func canonicalLandingRepo(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("explicit repository path required")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("repository path must be absolute; cwd is not landing authority")
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve repository path: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repository path must name a worktree directory")
	}
	root, err := runLandingGitOutput(canonical, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("repository path is not a Git worktree: %w", err)
	}
	root, err = filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve Git worktree root: %w", err)
	}
	if root != canonical {
		return "", fmt.Errorf("repository path must name the explicit Git worktree root %s", root)
	}
	return root, nil
}

func normalizeLandingTargetRef(repoPath, value string) (string, string, error) {
	branch := strings.TrimSpace(value)
	if branch == "" {
		return "", "", fmt.Errorf("target branch ref required")
	}
	branch = strings.TrimPrefix(branch, "refs/heads/")
	if branch == "" {
		return "", "", fmt.Errorf("target branch ref required")
	}
	if _, err := runLandingGitOutput(repoPath, "check-ref-format", "--branch", branch); err != nil {
		return "", "", fmt.Errorf("target ref must name a local branch: %w", err)
	}
	return branch, "refs/heads/" + branch, nil
}

func landingCurrentBranchRef(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "symbolic-ref", "--quiet", "HEAD")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return "", nil
	}
	return "", fmt.Errorf("inspect target worktree branch: %w: %s", err, strings.TrimSpace(string(out)))
}

func landingCommitExists(repoPath, sha string) (bool, error) {
	cmd := exec.Command("git", "-C", repoPath, "cat-file", "-e", sha+"^{commit}")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, fmt.Errorf("inspect Git commit %s: %w: %s", sha, err, strings.TrimSpace(string(out)))
}

func landingIsAncestor(repoPath, ancestor, descendant string) (bool, error) {
	cmd := exec.Command("git", "-C", repoPath, "merge-base", "--is-ancestor", ancestor, descendant)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("check Git ancestry %s -> %s: %w: %s", ancestor, descendant, err, strings.TrimSpace(string(out)))
}

func runLandingGitOutput(repoPath string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.Command("git", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func validLandingObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
