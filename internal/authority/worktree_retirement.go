package authority

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	WorktreeRetirementPlanSchemaVersion = "compound.worktree_retirement_plan.v0.1"

	WorktreeCandidateContained       = "contained"
	WorktreeCandidatePatchEquivalent = "patch_equivalent"
	WorktreeCandidateUnique          = "unique"

	WorktreeLivenessUnused  WorktreeLivenessState = "unused"
	WorktreeLivenessActive  WorktreeLivenessState = "active"
	WorktreeLivenessUnknown WorktreeLivenessState = "unknown"
)

// WorktreeRetirementGit is the complete Git seam used by retirement planning
// and application. Callers see one argv vector per subprocess; implementations
// must never run through a shell.
type WorktreeRetirementGit interface {
	Run(ctx context.Context, repo string, args ...string) (string, error)
}

// ExecWorktreeRetirementGit runs Git with repository hooks disabled. A
// reference-transaction hook is an unreviewed mutation surface, including for
// update-ref, so the adapter never inherits repository hook behavior.
type ExecWorktreeRetirementGit struct{}

func (ExecWorktreeRetirementGit) Run(ctx context.Context, repo string, args ...string) (string, error) {
	gitArgs := []string{"-c", "core.hooksPath=" + os.DevNull, "-C", repo}
	cmd := exec.CommandContext(ctx, "git", append(gitArgs, args...)...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		return strings.TrimSpace(string(out)), &WorktreeRetirementCommandError{
			Argv:     append([]string{"git"}, append(gitArgs, args...)...),
			ExitCode: exitCode,
			Output:   strings.TrimSpace(string(out)),
		}
	}
	return strings.TrimSpace(string(out)), nil
}

type WorktreeRetirementCommandError struct {
	Argv     []string
	ExitCode int
	Output   string
}

func (e *WorktreeRetirementCommandError) Error() string {
	message := strings.TrimSpace(e.Output)
	if message == "" {
		message = fmt.Sprintf("exit code %d", e.ExitCode)
	}
	return strings.Join(e.Argv, " ") + ": " + message
}

type WorktreeLivenessState string

type WorktreeLivenessObservation struct {
	State    WorktreeLivenessState `json:"state"`
	Evidence string                `json:"evidence,omitempty"`
}

type WorktreeRetirementLiveness interface {
	Observe(ctx context.Context, worktreePath string) WorktreeLivenessObservation
}

type WorktreeRetirementDependencies struct {
	Git              WorktreeRetirementGit
	Liveness         WorktreeRetirementLiveness
	RecoveryResolver WorktreeRetirementRecoveryResolver
}

// WorktreeRetirementRecoveryProof is verified content-addressed recovery
// evidence sealed into a retirement plan.
type WorktreeRetirementRecoveryProof struct {
	Reference string `json:"reference"`
	Kind      string `json:"kind"`
	Digest    string `json:"digest"`
	Path      string `json:"path"`
}

// WorktreeRetirementRecoveryResolver keeps recovery storage replaceable while
// letting every mutation boundary re-prove the exact sealed bytes.
type WorktreeRetirementRecoveryResolver interface {
	Resolve(ctx context.Context, reference string) (WorktreeRetirementRecoveryProof, error)
}

type WorktreeRetirementOwnerProof struct {
	WorkID          string `json:"work_id"`
	State           string `json:"state"`
	LeaseHolder     string `json:"lease_holder"`
	LeaseClaimedAt  string `json:"lease_claimed_at"`
	LeaseWorkSHA256 string `json:"lease_work_sha256"`
	WorkDigest      string `json:"work_digest"`
}

type WorktreeRetirementSourceProof struct {
	WorkID                string   `json:"work_id"`
	State                 string   `json:"state"`
	ProofComplete         bool     `json:"proof_complete"`
	ActionIntentIDs       []string `json:"action_intent_ids"`
	PolicyDecisionID      string   `json:"policy_decision_id"`
	ReceiptID             string   `json:"receipt_id"`
	ReceiptOutcome        string   `json:"receipt_outcome"`
	TransitionID          string   `json:"transition_id"`
	CommitArtifactRef     string   `json:"commit_artifact_ref"`
	CommitTip             string   `json:"commit_tip"`
	LandCheckID           string   `json:"land_check_id"`
	LandCheckEvidenceRef  string   `json:"land_check_evidence_ref"`
	LandCheckStatus       string   `json:"land_check_status"`
	LandCheckCandidateTip string   `json:"land_check_candidate_tip"`
	LandCheckFileDigest   string   `json:"land_check_file_digest"`
}

// WorktreeKeepRecord is a caller-composed projection of an active operator
// worktree-keep decision. internal/authority never reads the decisions/mem
// store itself (it stays mem-free); the CLI loads keeps from the decisions
// reader and translates them into this shape, exactly as it composes the owner
// and source proofs. The retirement gate treats a keyed branch as pinned.
type WorktreeKeepRecord struct {
	DecisionID string `json:"decision_id"`
	BranchRef  string `json:"branch_ref"`
	Revisit    string `json:"revisit,omitempty"`
}

type WorktreeRetirementPlanRequest struct {
	RepoRoot       string
	WorktreePath   string
	CandidateRef   string
	AuthorityRef   string
	PublicationRef string
	OwnerProof     WorktreeRetirementOwnerProof
	SourceProof    WorktreeRetirementSourceProof
	RecoveryRefs   []string
	// KeepDecisions maps a worktree branch ref (refs/heads/<name>) to the active
	// operator keep-decision that pins it. Composed by the CLI from the decisions
	// reader; a target whose branch ref is present is blocked from retirement.
	KeepDecisions map[string]WorktreeKeepRecord
	// KeepsUnreadable is set by the CLI when the keep-decision reader failed. The
	// absence of a keep cannot then be proven, so retirement is blocked
	// fail-closed. The zero value (false) means "keeps were consulted and none
	// pins this target" — the safe default for callers (e.g. the apply re-plan)
	// that do not consult keeps, so this stays additive.
	KeepsUnreadable bool
}

type WorktreeRetirementWorktree struct {
	Path       string                      `json:"path"`
	Head       string                      `json:"head"`
	BranchRef  string                      `json:"branch_ref,omitempty"`
	GitDir     string                      `json:"git_dir,omitempty"`
	Locked     bool                        `json:"locked"`
	LockReason string                      `json:"lock_reason,omitempty"`
	Prunable   bool                        `json:"prunable"`
	Clean      bool                        `json:"clean"`
	Liveness   WorktreeLivenessObservation `json:"liveness"`
}

type WorktreeRetirementPlan struct {
	SchemaVersion         string                            `json:"schema_version"`
	ID                    string                            `json:"id"`
	CreatedAt             string                            `json:"created_at"`
	Status                string                            `json:"status"`
	RepoRoot              string                            `json:"repo_root"`
	RepositoryFingerprint string                            `json:"repository_fingerprint"`
	Worktree              *WorktreeRetirementWorktree       `json:"worktree,omitempty"`
	CandidateRef          string                            `json:"candidate_ref,omitempty"`
	CandidateTip          string                            `json:"candidate_tip"`
	AuthorityRef          string                            `json:"authority_ref"`
	AuthorityTip          string                            `json:"authority_tip"`
	PublicationRef        string                            `json:"publication_ref"`
	PublicationTip        string                            `json:"publication_tip"`
	CandidateRelation     string                            `json:"candidate_relation"`
	SourceRelation        string                            `json:"source_relation"`
	OwnerProof            WorktreeRetirementOwnerProof      `json:"owner_proof"`
	SourceProof           WorktreeRetirementSourceProof     `json:"source_proof"`
	RecoveryRefs          []string                          `json:"recovery_refs"`
	RecoveryProofs        []WorktreeRetirementRecoveryProof `json:"recovery_proofs"`
	EvidenceRefs          []string                          `json:"evidence_refs"`
	Eligible              bool                              `json:"eligible"`
	BlockReasons          []string                          `json:"block_reasons"`
	PlanDigest            string                            `json:"plan_digest"`
}

type retirementWorktreeRecord struct {
	Path       string
	Head       string
	BranchRef  string
	Locked     bool
	LockReason string
	Prunable   bool
}

func BuildWorktreeRetirementPlan(ctx context.Context, req WorktreeRetirementPlanRequest, deps WorktreeRetirementDependencies, now time.Time) (WorktreeRetirementPlan, error) {
	if deps.Git == nil {
		deps.Git = ExecWorktreeRetirementGit{}
	}
	if deps.Liveness == nil {
		deps.Liveness = LSOFWorktreeRetirementLiveness{}
	}
	if deps.RecoveryResolver == nil {
		deps.RecoveryResolver = SHA256FileWorktreeRetirementRecoveryResolver{}
	}
	if now.IsZero() {
		now = time.Now()
	}
	if strings.TrimSpace(req.WorktreePath) == "" && strings.TrimSpace(req.CandidateRef) == "" {
		return WorktreeRetirementPlan{}, fmt.Errorf("at least one of worktree path or candidate ref is required")
	}
	root, err := canonicalRetirementRepo(ctx, deps.Git, req.RepoRoot)
	if err != nil {
		return WorktreeRetirementPlan{}, err
	}
	if err := validateRetirementRef(ctx, deps.Git, root, req.AuthorityRef, "refs/heads/"); err != nil {
		return WorktreeRetirementPlan{}, fmt.Errorf("authority ref: %w", err)
	}
	if err := validateRetirementRef(ctx, deps.Git, root, req.PublicationRef, "refs/remotes/"); err != nil {
		return WorktreeRetirementPlan{}, fmt.Errorf("publication ref: %w", err)
	}
	authorityTip, err := retirementResolveCommit(ctx, deps.Git, root, req.AuthorityRef)
	if err != nil {
		return WorktreeRetirementPlan{}, fmt.Errorf("resolve authority ref: %w", err)
	}
	publicationTip, err := retirementResolveCommit(ctx, deps.Git, root, req.PublicationRef)
	if err != nil {
		return WorktreeRetirementPlan{}, fmt.Errorf("resolve publication ref: %w", err)
	}
	identity, err := ReadGitRepositoryIdentity(root, authorityTip)
	if err != nil {
		return WorktreeRetirementPlan{}, fmt.Errorf("repository fingerprint: %w", err)
	}

	plan := WorktreeRetirementPlan{
		SchemaVersion:         WorktreeRetirementPlanSchemaVersion,
		CreatedAt:             now.UTC().Format(time.RFC3339Nano),
		Status:                "review_required",
		RepoRoot:              root,
		RepositoryFingerprint: identity.Fingerprint,
		CandidateRef:          strings.TrimSpace(req.CandidateRef),
		AuthorityRef:          strings.TrimSpace(req.AuthorityRef),
		AuthorityTip:          authorityTip,
		PublicationRef:        strings.TrimSpace(req.PublicationRef),
		PublicationTip:        publicationTip,
		OwnerProof:            req.OwnerProof,
		SourceProof:           req.SourceProof,
		RecoveryRefs:          sortedUniqueNonEmpty(req.RecoveryRefs),
		CandidateRelation:     WorktreeCandidateUnique,
		SourceRelation:        WorktreeCandidateUnique,
	}
	block := func(code string) {
		if code != "" && !containsStringValue(plan.BlockReasons, code) {
			plan.BlockReasons = append(plan.BlockReasons, code)
		}
	}

	if authorityTip != publicationTip {
		block("authority_publication_tip_mismatch")
	}
	if err := validateRetirementOwnerProof(req.OwnerProof); err != nil {
		block("owner_work_not_running_with_exact_lease: " + err.Error())
	}
	if err := validateRetirementSourceProof(req.SourceProof); err != nil {
		block("source_work_proof_invalid: " + err.Error())
	}
	if len(plan.RecoveryRefs) == 0 {
		block("recovery_evidence_required")
	} else {
		proofs, recoveryErr := ResolveWorktreeRetirementRecoveryProofs(ctx, plan.RecoveryRefs, deps.RecoveryResolver)
		if recoveryErr != nil {
			block("recovery_evidence_invalid: " + recoveryErr.Error())
		} else {
			plan.RecoveryProofs = proofs
		}
	}

	recordsOutput, err := deps.Git.Run(ctx, root, "worktree", "list", "--porcelain")
	if err != nil {
		return WorktreeRetirementPlan{}, fmt.Errorf("list registered worktrees: %w", err)
	}
	records, err := parseRetirementWorktrees(recordsOutput)
	if err != nil {
		return WorktreeRetirementPlan{}, err
	}
	if len(records) == 0 || !sameRetirementPath(records[0].Path, root) {
		return WorktreeRetirementPlan{}, fmt.Errorf("--repo must name the canonical main worktree root")
	}

	candidateTip := ""
	if strings.TrimSpace(req.WorktreePath) != "" {
		worktree, found, inspectErr := inspectRetirementWorktree(ctx, deps, root, req.WorktreePath, records)
		if inspectErr != nil {
			return WorktreeRetirementPlan{}, inspectErr
		}
		plan.Worktree = worktree
		if !found {
			block("worktree_not_registered_metadata_repair_required")
		} else {
			candidateTip = worktree.Head
			if sameRetirementPath(worktree.Path, root) {
				block("canonical_main_worktree_cannot_be_retired")
			}
			if worktree.Prunable {
				block("worktree_prunable_metadata_repair_required")
			}
			if worktree.Locked {
				block("worktree_locked_manual_unlock_required")
			}
			if !worktree.Clean {
				block("worktree_dirty")
			}
			switch worktree.Liveness.State {
			case WorktreeLivenessUnused:
			case WorktreeLivenessActive:
				block("worktree_live")
			default:
				block("worktree_liveness_unknown")
			}
		}
	}

	if plan.CandidateRef != "" {
		if err := validateRetirementRef(ctx, deps.Git, root, plan.CandidateRef, "refs/heads/"); err != nil {
			return WorktreeRetirementPlan{}, fmt.Errorf("candidate ref: %w", err)
		}
		if plan.CandidateRef == "refs/heads/main" {
			block("candidate_ref_main_protected")
		}
		if plan.CandidateRef == plan.AuthorityRef || plan.CandidateRef == plan.PublicationRef {
			block("candidate_ref_is_authority_or_publication")
		}
		refTip, resolveErr := retirementResolveCommit(ctx, deps.Git, root, plan.CandidateRef)
		if resolveErr != nil {
			block("candidate_ref_missing")
		} else if candidateTip != "" && candidateTip != refTip {
			block("worktree_ref_tip_mismatch")
		} else {
			candidateTip = refTip
		}
		if plan.Worktree != nil && plan.Worktree.BranchRef != "" && plan.Worktree.BranchRef != plan.CandidateRef {
			block("worktree_branch_ref_mismatch")
		}
	}
	plan.CandidateTip = candidateTip
	if !validRetirementObjectID(candidateTip) {
		block("candidate_tip_missing")
	} else {
		relation, relationErr := classifyRetirementCandidate(ctx, deps.Git, root, candidateTip, authorityTip, publicationTip)
		if relationErr != nil {
			return WorktreeRetirementPlan{}, relationErr
		}
		plan.CandidateRelation = relation
		if relation == WorktreeCandidateUnique {
			block("candidate_contains_unique_commits")
		}
		sourceRelation, sourceRelationErr := classifyRetirementCandidateAgainst(ctx, deps.Git, root, candidateTip, req.SourceProof.CommitTip)
		if sourceRelationErr != nil {
			return WorktreeRetirementPlan{}, sourceRelationErr
		}
		plan.SourceRelation = sourceRelation
		if sourceRelation == WorktreeCandidateUnique {
			block("candidate_not_contained_or_patch_equivalent_to_source_commit")
		}
	}

	// Operator keep-decisions pin a branch against cleanup (worktree-tracking
	// slice 2). Fail closed: if the CLI could not read keep records it cannot
	// prove none pins this target, so block. Otherwise, if an active keep names
	// the target branch ref, block and name the decision so the reason is legible.
	if req.KeepsUnreadable {
		block("keep_records_unreadable")
	} else if keepTarget := worktreeRetirementKeepTarget(plan); keepTarget != "" {
		if keep, ok := req.KeepDecisions[keepTarget]; ok {
			block("worktree_kept_by_operator_decision: " + keep.DecisionID)
		}
	}

	sort.Strings(plan.BlockReasons)
	plan.Eligible = len(plan.BlockReasons) == 0
	if !plan.Eligible {
		plan.Status = "blocked"
	}
	plan.EvidenceRefs = worktreeRetirementPlanEvidenceRefs(plan)
	plan.ID = WorktreeRetirementPlanID(plan)
	plan.PlanDigest = WorktreeRetirementPlanDigest(plan)
	return plan, nil
}

// worktreeRetirementKeepTarget is the branch ref an operator keep-decision
// would pin: the explicit candidate ref when set, else the registered
// worktree's branch ref. Keeps are keyed by branch ref, so a detached
// (branchless) target can never be pinned.
func worktreeRetirementKeepTarget(plan WorktreeRetirementPlan) string {
	if ref := strings.TrimSpace(plan.CandidateRef); ref != "" {
		return ref
	}
	if plan.Worktree != nil {
		return strings.TrimSpace(plan.Worktree.BranchRef)
	}
	return ""
}

func WorktreeRetirementPlanID(plan WorktreeRetirementPlan) string {
	plan.ID = ""
	plan.PlanDigest = ""
	encoded, _ := json.Marshal(plan)
	return "wtr_" + digestRetirementHex(encoded)[:12]
}

func WorktreeRetirementPlanDigest(plan WorktreeRetirementPlan) string {
	plan.PlanDigest = ""
	encoded, _ := json.Marshal(plan)
	return "sha256:" + digestRetirementHex(encoded)
}

func validateRetirementOwnerProof(proof WorktreeRetirementOwnerProof) error {
	if strings.TrimSpace(proof.WorkID) == "" || proof.State != "running" || strings.TrimSpace(proof.LeaseHolder) == "" || strings.TrimSpace(proof.LeaseClaimedAt) == "" {
		return fmt.Errorf("running work id and lease holder/timestamp required")
	}
	if (!validRetirementHex(proof.LeaseWorkSHA256, 64) && !validRetirementDigest(proof.LeaseWorkSHA256)) || !validRetirementDigest(proof.WorkDigest) {
		return fmt.Errorf("lease/work digests required")
	}
	return nil
}

func validateRetirementSourceProof(proof WorktreeRetirementSourceProof) error {
	if strings.TrimSpace(proof.WorkID) == "" || (proof.State != "completed" && proof.State != "failed") || !proof.ProofComplete {
		return fmt.Errorf("completed or failed terminal proof chain required")
	}
	if len(proof.ActionIntentIDs) == 0 || strings.TrimSpace(proof.PolicyDecisionID) == "" || strings.TrimSpace(proof.ReceiptID) == "" || strings.TrimSpace(proof.TransitionID) == "" {
		return fmt.Errorf("action policy receipt transition ids required")
	}
	if proof.ReceiptOutcome != proof.State {
		return fmt.Errorf("terminal source state %s requires matching receipt outcome, got %s", proof.State, proof.ReceiptOutcome)
	}
	if !validRetirementObjectID(proof.CommitTip) || proof.CommitArtifactRef != "commit:"+proof.CommitTip {
		return fmt.Errorf("consistent full commit artifact required")
	}
	if proof.LandCheckStatus != "landed" || proof.LandCheckCandidateTip != proof.CommitTip {
		return fmt.Errorf("landed land-check matching commit required")
	}
	if !strings.HasPrefix(proof.LandCheckID, "landcheck_") || proof.LandCheckEvidenceRef != "land-check:"+proof.LandCheckID || !validRetirementDigest(proof.LandCheckFileDigest) {
		return fmt.Errorf("sealed land-check evidence required")
	}
	return nil
}

func canonicalRetirementRepo(ctx context.Context, git WorktreeRetirementGit, value string) (string, error) {
	path := strings.TrimSpace(value)
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("repository root must be an absolute path")
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	root, err := git.Run(ctx, canonical, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("repository root: %w", err)
	}
	root, err = filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve Git root: %w", err)
	}
	if root != canonical {
		return "", fmt.Errorf("repository root must name exact Git worktree root %s", root)
	}
	return root, nil
}

func validateRetirementRef(ctx context.Context, git WorktreeRetirementGit, repo, ref, prefix string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" || !strings.HasPrefix(ref, prefix) || strings.TrimPrefix(ref, prefix) == "" {
		return fmt.Errorf("full canonical %s ref required, got %q", prefix, ref)
	}
	if _, err := git.Run(ctx, repo, "check-ref-format", ref); err != nil {
		return fmt.Errorf("invalid canonical ref %q: %w", ref, err)
	}
	return nil
}

func retirementResolveCommit(ctx context.Context, git WorktreeRetirementGit, repo, ref string) (string, error) {
	out, err := git.Run(ctx, repo, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	if !validRetirementObjectID(out) {
		return "", fmt.Errorf("ref %s did not resolve to a full commit id", ref)
	}
	return out, nil
}

func parseRetirementWorktrees(raw string) ([]retirementWorktreeRecord, error) {
	var records []retirementWorktreeRecord
	var current *retirementWorktreeRecord
	flush := func() {
		if current != nil {
			records = append(records, *current)
			current = nil
		}
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current = &retirementWorktreeRecord{Path: strings.TrimPrefix(line, "worktree ")}
		case current == nil:
			return nil, fmt.Errorf("malformed git worktree porcelain output")
		case strings.HasPrefix(line, "HEAD "):
			current.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			current.BranchRef = strings.TrimPrefix(line, "branch ")
		case line == "locked":
			current.Locked = true
		case strings.HasPrefix(line, "locked "):
			current.Locked = true
			current.LockReason = strings.TrimPrefix(line, "locked ")
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			current.Prunable = true
		}
	}
	flush()
	for _, record := range records {
		if strings.TrimSpace(record.Path) == "" || !validRetirementObjectID(record.Head) {
			return nil, fmt.Errorf("registered worktree has incomplete identity")
		}
	}
	return records, nil
}

func inspectRetirementWorktree(ctx context.Context, deps WorktreeRetirementDependencies, repo, requested string, records []retirementWorktreeRecord) (*WorktreeRetirementWorktree, bool, error) {
	path, err := filepath.Abs(strings.TrimSpace(requested))
	if err != nil {
		return nil, false, err
	}
	path = filepath.Clean(path)
	if canonical, evalErr := filepath.EvalSymlinks(path); evalErr == nil {
		path = canonical
	}
	var record *retirementWorktreeRecord
	for i := range records {
		if sameRetirementPath(records[i].Path, path) {
			record = &records[i]
			break
		}
	}
	if record == nil {
		return &WorktreeRetirementWorktree{Path: path, Liveness: WorktreeLivenessObservation{State: WorktreeLivenessUnknown, Evidence: "not registered"}}, false, nil
	}
	observed := &WorktreeRetirementWorktree{
		Path: path, Head: record.Head, BranchRef: record.BranchRef,
		Locked: record.Locked, LockReason: record.LockReason, Prunable: record.Prunable,
		Liveness: WorktreeLivenessObservation{State: WorktreeLivenessUnknown, Evidence: "not probed"},
	}
	if _, statErr := os.Stat(path); statErr != nil {
		observed.Liveness.Evidence = "registered path unavailable: " + statErr.Error()
		return observed, true, nil
	}
	root, err := deps.Git.Run(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, false, fmt.Errorf("inspect candidate worktree root: %w", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil || canonicalRoot != path {
		return nil, false, fmt.Errorf("candidate worktree path does not name exact registered root")
	}
	commonDir, err := deps.Git.Run(ctx, path, "rev-parse", "--git-common-dir")
	if err != nil {
		return nil, false, fmt.Errorf("inspect candidate Git common dir: %w", err)
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(path, commonDir)
	}
	commonDir, _ = filepath.Abs(commonDir)
	repoCommon, err := deps.Git.Run(ctx, repo, "rev-parse", "--git-common-dir")
	if err != nil {
		return nil, false, err
	}
	if !filepath.IsAbs(repoCommon) {
		repoCommon = filepath.Join(repo, repoCommon)
	}
	repoCommon, _ = filepath.Abs(repoCommon)
	if filepath.Clean(commonDir) != filepath.Clean(repoCommon) {
		return nil, false, fmt.Errorf("candidate worktree belongs to another repository")
	}
	gitDir, err := deps.Git.Run(ctx, path, "rev-parse", "--git-dir")
	if err != nil {
		return nil, false, fmt.Errorf("inspect candidate Git dir: %w", err)
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(path, gitDir)
	}
	gitDir, _ = filepath.Abs(gitDir)
	observed.GitDir = filepath.Clean(gitDir)
	status, err := deps.Git.Run(ctx, path, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return nil, false, fmt.Errorf("inspect candidate cleanliness: %w", err)
	}
	observed.Clean = strings.TrimSpace(status) == ""
	observed.Liveness = deps.Liveness.Observe(ctx, path)
	return observed, true, nil
}

func classifyRetirementCandidate(ctx context.Context, git WorktreeRetirementGit, repo, candidate, authority, publication string) (string, error) {
	containedAuthority, err := retirementIsAncestor(ctx, git, repo, candidate, authority)
	if err != nil {
		return "", err
	}
	containedPublication, err := retirementIsAncestor(ctx, git, repo, candidate, publication)
	if err != nil {
		return "", err
	}
	if containedAuthority && containedPublication {
		return WorktreeCandidateContained, nil
	}
	authorityEquivalent, err := retirementCherryHasNoPlus(ctx, git, repo, authority, candidate)
	if err != nil {
		return "", err
	}
	publicationEquivalent, err := retirementCherryHasNoPlus(ctx, git, repo, publication, candidate)
	if err != nil {
		return "", err
	}
	if authorityEquivalent && publicationEquivalent {
		return WorktreeCandidatePatchEquivalent, nil
	}
	return WorktreeCandidateUnique, nil
}

func classifyRetirementCandidateAgainst(ctx context.Context, git WorktreeRetirementGit, repo, candidate, target string) (string, error) {
	contained, err := retirementIsAncestor(ctx, git, repo, candidate, target)
	if err != nil {
		return "", err
	}
	if contained {
		return WorktreeCandidateContained, nil
	}
	equivalent, err := retirementCherryHasNoPlus(ctx, git, repo, target, candidate)
	if err != nil {
		return "", err
	}
	if equivalent {
		return WorktreeCandidatePatchEquivalent, nil
	}
	return WorktreeCandidateUnique, nil
}

func retirementIsAncestor(ctx context.Context, git WorktreeRetirementGit, repo, ancestor, descendant string) (bool, error) {
	_, err := git.Run(ctx, repo, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var commandErr *WorktreeRetirementCommandError
	if errors.As(err, &commandErr) && commandErr.ExitCode == 1 {
		return false, nil
	}
	return false, fmt.Errorf("check candidate ancestry: %w", err)
}

// retirementCherryHasNoPlus reads patch-equivalence off the shared `git cherry`
// count owner, so the retirement gate and the inventory classifier can never
// disagree about whether a branch still holds unlanded work.
func retirementCherryHasNoPlus(ctx context.Context, git WorktreeRetirementGit, repo, upstream, candidate string) (bool, error) {
	unlanded, landed, err := worktreeUnlandedCount(ctx, git, repo, upstream, candidate)
	if err != nil {
		return false, fmt.Errorf("classify candidate patches: %w", err)
	}
	return unlanded == 0 && landed > 0, nil
}

func sameRetirementPath(a, b string) bool {
	ac, aerr := filepath.EvalSymlinks(filepath.Clean(a))
	bc, berr := filepath.EvalSymlinks(filepath.Clean(b))
	if aerr == nil {
		a = ac
	}
	if berr == nil {
		b = bc
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func sortedUniqueNonEmpty(values []string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = true
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func containsStringValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validRetirementObjectID(value string) bool {
	return validRetirementHex(value, 40) || validRetirementHex(value, 64)
}

func validRetirementDigest(value string) bool {
	return strings.HasPrefix(value, "sha256:") && validRetirementHex(strings.TrimPrefix(value, "sha256:"), 64)
}

func validRetirementHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func digestRetirementHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SHA256FileWorktreeRetirementRecoveryResolver verifies references of the
// form sha256-file:<64-lower-hex>:<absolute-path> against the current bytes.
type SHA256FileWorktreeRetirementRecoveryResolver struct{}

func (SHA256FileWorktreeRetirementRecoveryResolver) Resolve(ctx context.Context, reference string) (WorktreeRetirementRecoveryProof, error) {
	if err := ctx.Err(); err != nil {
		return WorktreeRetirementRecoveryProof{}, err
	}
	const prefix = "sha256-file:"
	reference = strings.TrimSpace(reference)
	if !strings.HasPrefix(reference, prefix) {
		return WorktreeRetirementRecoveryProof{}, fmt.Errorf("recovery reference must use %s", prefix)
	}
	rest := strings.TrimPrefix(reference, prefix)
	if len(rest) < 66 || rest[64] != ':' || !validRetirementHex(rest[:64], 64) {
		return WorktreeRetirementRecoveryProof{}, fmt.Errorf("recovery reference requires 64-lower-hex digest and absolute path")
	}
	path := rest[65:]
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return WorktreeRetirementRecoveryProof{}, fmt.Errorf("recovery reference path must be absolute and canonical")
	}
	info, err := os.Stat(path)
	if err != nil {
		return WorktreeRetirementRecoveryProof{}, fmt.Errorf("stat recovery evidence: %w", err)
	}
	if !info.Mode().IsRegular() {
		return WorktreeRetirementRecoveryProof{}, fmt.Errorf("recovery evidence must be a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return WorktreeRetirementRecoveryProof{}, fmt.Errorf("read recovery evidence: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return WorktreeRetirementRecoveryProof{}, err
	}
	digest := "sha256:" + digestRetirementHex(data)
	if digest != "sha256:"+rest[:64] {
		return WorktreeRetirementRecoveryProof{}, fmt.Errorf("recovery evidence digest mismatch")
	}
	return WorktreeRetirementRecoveryProof{Reference: reference, Kind: "sha256-file", Digest: digest, Path: path}, nil
}

func ResolveWorktreeRetirementRecoveryProofs(ctx context.Context, references []string, resolver WorktreeRetirementRecoveryResolver) ([]WorktreeRetirementRecoveryProof, error) {
	if resolver == nil {
		resolver = SHA256FileWorktreeRetirementRecoveryResolver{}
	}
	references = sortedUniqueNonEmpty(references)
	if len(references) == 0 {
		return nil, fmt.Errorf("recovery evidence required")
	}
	proofs := make([]WorktreeRetirementRecoveryProof, 0, len(references))
	for _, reference := range references {
		proof, err := resolver.Resolve(ctx, reference)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", reference, err)
		}
		if proof.Reference != reference || strings.TrimSpace(proof.Kind) == "" || !validRetirementDigest(proof.Digest) {
			return nil, fmt.Errorf("resolver returned invalid proof for %q", reference)
		}
		if proof.Kind == "sha256-file" && (!filepath.IsAbs(proof.Path) || filepath.Clean(proof.Path) != proof.Path) {
			return nil, fmt.Errorf("resolver returned invalid file path for %q", reference)
		}
		proofs = append(proofs, proof)
	}
	return proofs, nil
}

// VerifyWorktreeRetirementRecoveryProofs re-resolves every sealed reference
// and requires byte-for-byte semantic equality with the planned proof.
func VerifyWorktreeRetirementRecoveryProofs(ctx context.Context, sealed []WorktreeRetirementRecoveryProof, resolver WorktreeRetirementRecoveryResolver) error {
	if len(sealed) == 0 {
		return fmt.Errorf("sealed recovery proofs required")
	}
	references := make([]string, 0, len(sealed))
	for _, proof := range sealed {
		references = append(references, proof.Reference)
	}
	current, err := ResolveWorktreeRetirementRecoveryProofs(ctx, references, resolver)
	if err != nil {
		return err
	}
	if len(current) != len(sealed) {
		return fmt.Errorf("recovery proof count drift")
	}
	for i := range current {
		if current[i] != sealed[i] {
			return fmt.Errorf("recovery proof drift for %q", sealed[i].Reference)
		}
	}
	return nil
}

func worktreeRetirementPlanEvidenceRefs(plan WorktreeRetirementPlan) []string {
	refs := []string{
		"work:" + plan.OwnerProof.WorkID,
		"work:" + plan.SourceProof.WorkID,
		plan.SourceProof.CommitArtifactRef,
		plan.SourceProof.LandCheckEvidenceRef,
		plan.SourceProof.LandCheckFileDigest,
		"repo-fingerprint:" + plan.RepositoryFingerprint,
		"git-ref:" + plan.AuthorityRef + "@" + plan.AuthorityTip,
		"git-ref:" + plan.PublicationRef + "@" + plan.PublicationTip,
	}
	refs = append(refs, plan.RecoveryRefs...)
	for _, proof := range plan.RecoveryProofs {
		refs = append(refs, "recovery-proof:"+proof.Kind+":"+proof.Digest)
	}
	return sortedUniqueNonEmpty(refs)
}

// LSOFWorktreeRetirementLiveness is the production fail-closed process/open
// file probe. macOS lsof uses exit 1 with no output for no matches.
type WorktreeRetirementProcessResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type WorktreeRetirementProcessRunner interface {
	Run(ctx context.Context, name string, args ...string) (WorktreeRetirementProcessResult, error)
}

type ExecWorktreeRetirementProcessRunner struct{}

func (ExecWorktreeRetirementProcessRunner) Run(ctx context.Context, name string, args ...string) (WorktreeRetirementProcessResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := WorktreeRetirementProcessResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, err
}

type LSOFWorktreeRetirementLiveness struct {
	Runner WorktreeRetirementProcessRunner
}

func (probe LSOFWorktreeRetirementLiveness) Observe(ctx context.Context, worktreePath string) WorktreeLivenessObservation {
	runner := probe.Runner
	if runner == nil {
		runner = ExecWorktreeRetirementProcessRunner{}
	}
	result, err := runner.Run(ctx, "lsof", "-n", "-P", "+D", worktreePath)
	stdout := strings.TrimSpace(result.Stdout)
	stderr := strings.TrimSpace(result.Stderr)
	if err != nil {
		return WorktreeLivenessObservation{State: WorktreeLivenessUnknown, Evidence: err.Error()}
	}
	if result.ExitCode == 0 && stdout != "" {
		return WorktreeLivenessObservation{State: WorktreeLivenessActive, Evidence: stdout}
	}
	if result.ExitCode == 1 && stdout == "" {
		evidence := "lsof exit 1 with no matching files"
		if stderr != "" {
			evidence += "; stderr: " + stderr
		}
		return WorktreeLivenessObservation{State: WorktreeLivenessUnused, Evidence: evidence}
	}
	evidence := fmt.Sprintf("lsof exit %d stdout=%q stderr=%q", result.ExitCode, stdout, stderr)
	return WorktreeLivenessObservation{State: WorktreeLivenessUnknown, Evidence: evidence}
}
