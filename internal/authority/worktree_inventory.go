package authority

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// WorktreeInventorySchemaVersion versions the read-only worktree inventory
// projection envelope. v0.1 is pure Git ground truth: it joins no ledger work
// and records no keep-decision.
const WorktreeInventorySchemaVersion = "compound.worktree_inventory.v0.1"

// WorktreeInventoryAuthorityRef is the projection's own read-side authority
// handle. It is NOT the Git authority tip (that is MainRef/MainTip): this
// projection asserts no keep-decision and cannot retire anything.
const WorktreeInventoryAuthorityRef = "compound://executors/worktree-inventory"

// Worktree inventory classes. Precedence when a worktree could fit several:
// main > live > unknown(unreadable) > probe > landed_clean > stalled_unlanded.
// A live lane must NEVER read as landed_clean or stalled_unlanded, and unlanded
// work (unique commits or a dirty tree) must NEVER read as landed_clean — those
// two invariants are the entire point of the feature (cleanup-sweep safety).
const (
	WorktreeClassMain            = "main"
	WorktreeClassProbe           = "probe"
	WorktreeClassLive            = "live"
	WorktreeClassLandedClean     = "landed_clean"
	WorktreeClassStalledUnlanded = "stalled_unlanded"
	WorktreeClassUnknown         = "unknown"
)

const (
	defaultWorktreeActivityWindow           = 60 * time.Minute
	defaultWorktreeAuthorityRef             = "refs/heads/main"
	defaultWorktreeInventoryLivenessTimeout = 5 * time.Second
)

// WorktreeInventoryDependencies reuses the retirement Git seam but has its own
// inventory-wide liveness seam. Retirement proves one requested worktree with
// a recursive probe; inventory must observe every root in one snapshot so its
// latency does not grow by one external process per row.
type WorktreeInventoryDependencies struct {
	Git      WorktreeRetirementGit
	Liveness WorktreeInventoryLiveness
}

// WorktreeInventoryLiveness takes one open-file snapshot for the complete set
// of registered roots. The returned map is keyed by the exact input roots;
// omitted roots are unresolved and BuildWorktreeInventory treats them as
// unknown, never unused.
type WorktreeInventoryLiveness interface {
	Snapshot(ctx context.Context, worktreeRoots []string) map[string]WorktreeLivenessObservation
}

// LSOFWorktreeInventoryLiveness is the production inventory-wide open-file
// snapshot. Unlike the retirement probe it never recursively scans each root.
type LSOFWorktreeInventoryLiveness struct {
	Runner  WorktreeRetirementProcessRunner
	Timeout time.Duration
}

func (probe LSOFWorktreeInventoryLiveness) Snapshot(ctx context.Context, worktreeRoots []string) map[string]WorktreeLivenessObservation {
	observations := make(map[string]WorktreeLivenessObservation, len(worktreeRoots))
	if len(worktreeRoots) == 0 {
		return observations
	}
	timeout := probe.Timeout
	if timeout <= 0 {
		timeout = defaultWorktreeInventoryLivenessTimeout
	}
	snapshotCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var (
		result WorktreeRetirementProcessResult
		err    error
	)
	if probe.Runner != nil {
		result, err = probe.Runner.Run(snapshotCtx, "lsof", "-n", "-P", "-Fn")
	} else {
		result, err = runWorktreeInventoryLSOFSnapshot(snapshotCtx)
	}

	activePaths, attributionComplete := attributeWorktreeInventoryLSOFPaths(snapshotCtx, result.Stdout, worktreeRoots)
	complete := err == nil &&
		snapshotCtx.Err() == nil &&
		result.ExitCode == 0 &&
		attributionComplete &&
		!worktreeInventoryLSOFOutputIncomplete(result.Stderr)
	failureEvidence := worktreeInventorySnapshotFailureEvidence(snapshotCtx.Err(), err, result)
	for _, root := range worktreeRoots {
		if openPath, active := activePaths[root]; active {
			observations[root] = WorktreeLivenessObservation{State: WorktreeLivenessActive, Evidence: "open path: " + openPath}
		} else if complete {
			observations[root] = WorktreeLivenessObservation{State: WorktreeLivenessUnused, Evidence: "inventory lsof snapshot found no open path"}
		} else {
			observations[root] = WorktreeLivenessObservation{State: WorktreeLivenessUnknown, Evidence: failureEvidence}
		}
	}
	return observations
}

func runWorktreeInventoryLSOFSnapshot(ctx context.Context) (WorktreeRetirementProcessResult, error) {
	if err := ctx.Err(); err != nil {
		return WorktreeRetirementProcessResult{}, err
	}

	cmd := exec.Command("lsof", "-n", "-P", "-Fn")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return WorktreeRetirementProcessResult{}, err
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		return worktreeInventoryProcessResult(stdout.String(), stderr.String(), err)
	case <-ctx.Done():
		// CommandContext only kills the direct process. lsof may be wrapped (and
		// tests deliberately wrap it), so terminate the whole process group to
		// keep the inventory's deadline real and prevent orphaned descendants.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Process.Kill()
		waitErr := <-done
		result, _ := worktreeInventoryProcessResult(stdout.String(), stderr.String(), waitErr)
		return result, ctx.Err()
	}
}

func worktreeInventoryProcessResult(stdout, stderr string, err error) (WorktreeRetirementProcessResult, error) {
	result := WorktreeRetirementProcessResult{Stdout: stdout, Stderr: stderr, ExitCode: 0}
	if err == nil {
		return result, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, err
}

func worktreeInventoryLSOFOutputIncomplete(stderr string) bool {
	return strings.Contains(strings.ToLower(stderr), "incomplete")
}

func worktreeInventorySnapshotFailureEvidence(ctxErr, runErr error, result WorktreeRetirementProcessResult) string {
	switch {
	case ctxErr != nil:
		return "inventory lsof snapshot incomplete: " + ctxErr.Error()
	case runErr != nil:
		return "inventory lsof snapshot incomplete: " + runErr.Error()
	default:
		return fmt.Sprintf("inventory lsof snapshot incomplete: exit %d stderr=%q", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
}

func attributeWorktreeInventoryLSOFPaths(
	ctx context.Context,
	stdout string,
	worktreeRoots []string,
) (map[string]string, bool) {
	activePaths := make(map[string]string)
	fileDescriptor := ""
	for _, line := range strings.Split(stdout, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'f':
			fileDescriptor = strings.TrimSpace(line[1:])
		case 'n':
			openPath := strings.TrimSuffix(line[1:], "\r")
			if !strings.EqualFold(fileDescriptor, "cwd") && filepath.IsAbs(openPath) {
				openPath = filepath.Clean(openPath)
				if root, ok := longestWorktreeInventoryRoot(openPath, worktreeRoots); ok {
					if prior := activePaths[root]; prior == "" || openPath < prior {
						activePaths[root] = openPath
					}
				}
			}
			fileDescriptor = ""
			// Preserve the first completed record even when the process consumed
			// the final budget tick. Anything not yet attributed stays unknown.
			if ctx.Err() != nil {
				return activePaths, false
			}
		}
	}
	return activePaths, true
}

func longestWorktreeInventoryRoot(openPath string, worktreeRoots []string) (string, bool) {
	bestRoot := ""
	bestLength := -1
	for _, root := range worktreeRoots {
		matchLength, matches := worktreeInventoryRootMatchLength(root, openPath)
		if !matches {
			continue
		}
		if matchLength > bestLength {
			bestRoot = root
			bestLength = matchLength
		}
	}
	return bestRoot, bestLength >= 0
}

func worktreeInventoryRootMatchLength(root, candidate string) (int, bool) {
	cleanRoot := filepath.Clean(root)
	cleanCandidate := filepath.Clean(candidate)
	if worktreeInventoryPathWithinRoot(cleanRoot, cleanCandidate) {
		return len(cleanRoot), true
	}
	canonicalRoot, rootErr := filepath.EvalSymlinks(cleanRoot)
	canonicalCandidate, candidateErr := filepath.EvalSymlinks(cleanCandidate)
	if rootErr == nil && candidateErr == nil && worktreeInventoryPathWithinRoot(canonicalRoot, canonicalCandidate) {
		return len(canonicalRoot), true
	}
	return -1, false
}

func worktreeInventoryPathWithinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	if relative == "." {
		return true
	}
	parts := strings.Split(relative, string(filepath.Separator))
	return len(parts) > 0 && parts[0] != ".."
}

// WorktreeKeepAnnotation is the caller-composed projection of an active operator
// worktree-keep, keyed elsewhere by branch ref. Like the retirement gate, this
// package stays mem-free: the CLI loads keeps from the decisions reader and
// passes this shape in, so a kept row is an annotation over ground truth, never
// a class the projection invented.
type WorktreeKeepAnnotation struct {
	DecisionID string
	Revisit    string
}

// WorktreeEnvelopeArtifact carries one nonterminal work envelope's exact
// identity evidence. A branch artifact can name a linked lane; a running
// envelope's Git delivery root can name the primary checkout. CreatedAt
// (RFC3339) breaks ambiguity: newest nonterminal wins.
type WorktreeEnvelopeArtifact struct {
	WorkID             string
	State              string
	Holder             string
	BranchRef          string
	TargetWorktreeRoot string
	CreatedAt          string
}

// WorktreeIdentityCandidate is one possible editor when several running work
// envelopes share the primary checkout's canonical delivery root. Candidates
// are evidence, not a ranking: the projection emits them in WorkID order and
// deliberately leaves the row's singular work identity empty.
type WorktreeIdentityCandidate struct {
	WorkID     string `json:"work_id"`
	WorkState  string `json:"work_state,omitempty"`
	WorkHolder string `json:"work_holder,omitempty"`
}

// WorktreeInventoryOptions carries the injected clock (tests) and the activity
// window. AuthorityRef defaults to refs/heads/main; classification is measured
// against that tip's containment. Keeps/Envelopes are OPTIONAL ledger inputs the
// CLI composes (they are absent for pure ground-truth callers): Keeps annotates
// kept rows, Envelopes joins exact work identity.
type WorktreeInventoryOptions struct {
	Now            time.Time
	ActivityWindow time.Duration
	AuthorityRef   string
	// Keeps maps branch ref -> active operator keep. Only consulted when
	// KeepsReadable is a non-nil &true.
	Keeps map[string]WorktreeKeepAnnotation
	// KeepsReadable, when non-nil, means the CLI consulted keep records: &true =
	// readable (annotate from Keeps), &false = unreadable (annotate nothing, and
	// the envelope reports keeps_readable:false — honest, not guessed). nil means
	// keeps were not consulted (ground-truth-only callers) and the field is
	// omitted from the envelope.
	KeepsReadable *bool
	// Envelopes carry exact nonterminal work identity for branch or main-root joins.
	Envelopes []WorktreeEnvelopeArtifact
}

// WorktreeInventoryRow is one classified worktree. Every row carries a
// plain-language reason and next_action (operator-legibility law): no raw
// internals-speak, and never a ready-to-run retire command with filled args for
// a live or stalled_unlanded row.
type WorktreeInventoryRow struct {
	Path           string                      `json:"path"`
	Class          string                      `json:"class"`
	Branch         string                      `json:"branch,omitempty"`
	BranchRef      string                      `json:"branch_ref,omitempty"`
	Detached       bool                        `json:"detached"`
	Head           string                      `json:"head,omitempty"`
	AheadCommits   int                         `json:"ahead_commits"`
	DirtyFiles     int                         `json:"dirty_files"`
	Contained      bool                        `json:"contained"`
	Live           bool                        `json:"live"`
	ActivityRecent bool                        `json:"activity_recent"`
	AgeSeconds     *int64                      `json:"age_seconds,omitempty"`
	Liveness       WorktreeLivenessObservation `json:"liveness"`
	Reason         string                      `json:"reason"`
	NextAction     string                      `json:"next_action"`
	IsMain         bool                        `json:"is_main"`
	// Work identity joins through an exact branch artifact, or through a running
	// envelope whose canonical Git delivery root names the primary checkout. If
	// several running envelopes name that root, singular identity stays empty and
	// WorkCandidates exposes the ambiguity without inferring the active editor.
	WorkID         string                      `json:"work_id,omitempty"`
	WorkState      string                      `json:"work_state,omitempty"`
	WorkHolder     string                      `json:"work_holder,omitempty"`
	WorkCandidates []WorktreeIdentityCandidate `json:"work_candidates,omitempty"`
	// Kept + KeepDecisionID + KeepRevisit annotate an operator keep-decision. A
	// kept row KEEPS its ground-truth class (annotation, never a reclassification)
	// but its next_action defers to the operator decision.
	Kept           bool   `json:"kept,omitempty"`
	KeepDecisionID string `json:"keep_decision_id,omitempty"`
	KeepRevisit    string `json:"keep_revisit,omitempty"`
}

type WorktreeInventoryCounts struct {
	Total           int `json:"total"`
	Main            int `json:"main"`
	MainDirty       int `json:"main_dirty"`
	Live            int `json:"live"`
	StalledUnlanded int `json:"stalled_unlanded"`
	LandedClean     int `json:"landed_clean"`
	Probe           int `json:"probe"`
	Unknown         int `json:"unknown"`
	Kept            int `json:"kept"`
}

// WorktreeInventory is the versioned read-only projection envelope.
type WorktreeInventory struct {
	SchemaVersion  string                  `json:"schema_version"`
	GeneratedAt    string                  `json:"generated_at"`
	Repo           string                  `json:"repo"`
	AuthorityRef   string                  `json:"authority_ref"`
	MainRef        string                  `json:"main_ref"`
	MainTip        string                  `json:"main_tip"`
	ActivityWindow string                  `json:"activity_window"`
	Rows           []WorktreeInventoryRow  `json:"rows"`
	Counts         WorktreeInventoryCounts `json:"counts"`
	// KeepsReadable is emitted only as false, when the CLI consulted keep records
	// and could not read them: no kept annotations are trustworthy, so the
	// projection says so instead of implying none exist. Omitted otherwise.
	KeepsReadable *bool `json:"keeps_readable,omitempty"`
}

// BuildWorktreeInventory classifies every Git worktree of repo from ground
// truth alone, then joins OPTIONAL ledger evidence the caller composed. It is
// strictly read-only: it invokes only read verbs through the injected Git seam
// and never mutates a ref, index, or working tree.
//
// Classification is pure Git ground truth. Ledger evidence is additive and
// exact-only: a dispatch-time worktree-branch:<ref> artifact names a linked
// row, while a running WorkEnvelope's canonical Git delivery root names the
// primary checkout. Codex-runtime worktrees are separate clones absent from
// `git worktree list`, so they do not produce inventory rows. Operator
// keep-decisions (opts.Keeps) annotate a row without changing its class. Both
// inputs are empty for ground-truth-only callers.
func BuildWorktreeInventory(ctx context.Context, deps WorktreeInventoryDependencies, repo string, opts WorktreeInventoryOptions) (WorktreeInventory, error) {
	if deps.Git == nil {
		deps.Git = ExecWorktreeRetirementGit{}
	}
	if deps.Liveness == nil {
		deps.Liveness = LSOFWorktreeInventoryLiveness{}
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	window := opts.ActivityWindow
	if window <= 0 {
		window = defaultWorktreeActivityWindow
	}
	authorityRef := strings.TrimSpace(opts.AuthorityRef)
	if authorityRef == "" {
		authorityRef = defaultWorktreeAuthorityRef
	}
	if strings.TrimSpace(repo) == "" {
		return WorktreeInventory{}, fmt.Errorf("repository path is required")
	}

	root, err := deps.Git.Run(ctx, repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return WorktreeInventory{}, fmt.Errorf("resolve repository root: %w", err)
	}
	root = strings.TrimSpace(root)
	if canonical, evalErr := filepath.EvalSymlinks(filepath.Clean(root)); evalErr == nil {
		root = canonical
	}

	mainTip, err := retirementResolveCommit(ctx, deps.Git, root, authorityRef)
	if err != nil {
		return WorktreeInventory{}, fmt.Errorf("resolve authority ref %s: %w", authorityRef, err)
	}

	recordsOutput, err := deps.Git.Run(ctx, root, "worktree", "list", "--porcelain")
	if err != nil {
		return WorktreeInventory{}, fmt.Errorf("list worktrees: %w", err)
	}
	records, err := parseRetirementWorktrees(recordsOutput)
	if err != nil {
		return WorktreeInventory{}, fmt.Errorf("parse worktrees: %w", err)
	}
	worktreeRoots := make([]string, len(records))
	for i := range records {
		worktreeRoots[i] = records[i].Path
	}
	liveness := deps.Liveness.Snapshot(ctx, worktreeRoots)

	inv := WorktreeInventory{
		SchemaVersion:  WorktreeInventorySchemaVersion,
		GeneratedAt:    now.UTC().Format(time.RFC3339),
		Repo:           root,
		AuthorityRef:   WorktreeInventoryAuthorityRef,
		MainRef:        authorityRef,
		MainTip:        mainTip,
		ActivityWindow: window.String(),
		Rows:           make([]WorktreeInventoryRow, 0, len(records)),
	}
	// git worktree list --porcelain always lists the main working tree first;
	// records[0] is therefore the primary checkout even when this command is
	// invoked from a linked worktree.
	for i := range records {
		row := classifyWorktreeInventoryRow(ctx, deps, root, mainTip, records[i], inventoryLivenessObservation(liveness, records[i].Path), i == 0, now, window)
		inv.Rows = append(inv.Rows, row)
		inv.Counts.Total++
		if row.Live {
			inv.Counts.Live++
		}
		switch row.Class {
		case WorktreeClassMain:
			inv.Counts.Main++
			inv.Counts.MainDirty += row.DirtyFiles
		case WorktreeClassLive:
			// Counted from row.Live above so main-class precedence does not hide
			// an active primary checkout.
		case WorktreeClassStalledUnlanded:
			inv.Counts.StalledUnlanded++
		case WorktreeClassLandedClean:
			inv.Counts.LandedClean++
		case WorktreeClassProbe:
			inv.Counts.Probe++
		default:
			inv.Counts.Unknown++
		}
	}

	// Ledger joins run over the finished ground-truth rows: exact work identity
	// first, then operator keep annotations. Both are no-ops when the CLI passed
	// no ledger inputs. Keeps are honored only when the CLI reports them readable;
	// unreadable keeps annotate nothing and surface as keeps_readable:false.
	joinWorktreeEnvelopeIdentity(inv.Rows, opts.Envelopes)
	if opts.KeepsReadable != nil {
		if *opts.KeepsReadable {
			inv.Counts.Kept = annotateWorktreeKeeps(inv.Rows, opts.Keeps)
		} else {
			unreadable := false
			inv.KeepsReadable = &unreadable
		}
	}
	return inv, nil
}

func inventoryLivenessObservation(snapshot map[string]WorktreeLivenessObservation, worktreePath string) WorktreeLivenessObservation {
	obs, ok := snapshot[worktreePath]
	if !ok {
		return WorktreeLivenessObservation{State: WorktreeLivenessUnknown, Evidence: "inventory liveness snapshot did not resolve this worktree"}
	}
	switch obs.State {
	case WorktreeLivenessUnused, WorktreeLivenessActive, WorktreeLivenessUnknown:
		return obs
	default:
		return WorktreeLivenessObservation{State: WorktreeLivenessUnknown, Evidence: "inventory liveness snapshot returned an invalid state"}
	}
}

// joinWorktreeEnvelopeIdentity annotates each row with exact nonterminal work
// identity. A branch artifact names a linked row; a running envelope's canonical
// target root names the main row. Dangling evidence is silently ignored. Branch
// ambiguity keeps its established newest-wins behavior. Main-root ambiguity is
// different: a shared delivery root does not identify the active editor, so the
// row exposes deterministic candidates and no singular work identity.
func joinWorktreeEnvelopeIdentity(rows []WorktreeInventoryRow, envelopes []WorktreeEnvelopeArtifact) {
	if len(envelopes) == 0 {
		return
	}
	for i := range rows {
		matches := map[string]WorktreeEnvelopeArtifact{}
		rootMatches := map[string]WorktreeEnvelopeArtifact{}
		for _, e := range envelopes {
			workID := strings.TrimSpace(e.WorkID)
			if workID == "" {
				continue
			}
			rootMatch := worktreeEnvelopeMatchesRoot(rows[i], e)
			if !worktreeEnvelopeMatchesRow(rows[i], e) {
				continue
			}
			if prior, ok := matches[workID]; !ok || newerWorktreeEnvelope(e, prior) {
				matches[workID] = e
			}
			if rootMatch {
				if prior, ok := rootMatches[workID]; !ok || newerWorktreeEnvelope(e, prior) {
					rootMatches[workID] = e
				}
			}
		}
		if len(matches) == 0 {
			continue
		}
		if len(rootMatches) > 1 {
			candidates := make([]WorktreeIdentityCandidate, 0, len(rootMatches))
			for _, match := range rootMatches {
				candidates = append(candidates, WorktreeIdentityCandidate{
					WorkID:     strings.TrimSpace(match.WorkID),
					WorkState:  strings.TrimSpace(match.State),
					WorkHolder: strings.TrimSpace(match.Holder),
				})
			}
			sort.Slice(candidates, func(a, b int) bool {
				return candidates[a].WorkID < candidates[b].WorkID
			})
			rows[i].WorkID = ""
			rows[i].WorkState = ""
			rows[i].WorkHolder = ""
			rows[i].WorkCandidates = candidates
			rows[i].Reason += fmt.Sprintf(" (ambiguous main-root identity: %d running envelopes bind this primary checkout; active editor was not inferred)", len(candidates))
			continue
		}
		if len(rootMatches) == 1 {
			for _, match := range rootMatches {
				rows[i].WorkID = match.WorkID
				rows[i].WorkState = match.State
				rows[i].WorkHolder = match.Holder
			}
			continue
		}
		var best WorktreeEnvelopeArtifact
		haveBest := false
		for _, m := range matches {
			if !haveBest || newerWorktreeEnvelope(m, best) {
				best = m
				haveBest = true
			}
		}
		rows[i].WorkID = best.WorkID
		rows[i].WorkState = best.State
		rows[i].WorkHolder = best.Holder
		if len(matches) > 1 {
			rows[i].Reason += fmt.Sprintf(" (ambiguous identity: %d nonterminal envelopes name this row; newest %s shown)", len(matches), best.WorkID)
		}
	}
}

func worktreeEnvelopeMatchesRow(row WorktreeInventoryRow, candidate WorktreeEnvelopeArtifact) bool {
	return worktreeEnvelopeMatchesBranch(row, candidate) || worktreeEnvelopeMatchesRoot(row, candidate)
}

func worktreeEnvelopeMatchesBranch(row WorktreeInventoryRow, candidate WorktreeEnvelopeArtifact) bool {
	return strings.TrimSpace(row.BranchRef) != "" && strings.TrimSpace(candidate.BranchRef) == strings.TrimSpace(row.BranchRef)
}

func worktreeEnvelopeMatchesRoot(row WorktreeInventoryRow, candidate WorktreeEnvelopeArtifact) bool {
	return row.IsMain && strings.TrimSpace(candidate.State) == "running" && strings.TrimSpace(candidate.TargetWorktreeRoot) != "" &&
		filepath.Clean(strings.TrimSpace(candidate.TargetWorktreeRoot)) == filepath.Clean(row.Path)
}

func newerWorktreeEnvelope(candidate, current WorktreeEnvelopeArtifact) bool {
	return candidate.CreatedAt > current.CreatedAt || (candidate.CreatedAt == current.CreatedAt && candidate.WorkID > current.WorkID)
}

// annotateWorktreeKeeps annotates rows whose branch ref carries an active
// operator keep, keeping each row's ground-truth class (annotation, never a
// reclassification) but deferring its next_action to the operator decision.
// Returns the number of kept rows.
func annotateWorktreeKeeps(rows []WorktreeInventoryRow, keeps map[string]WorktreeKeepAnnotation) int {
	if len(keeps) == 0 {
		return 0
	}
	kept := 0
	for i := range rows {
		ref := strings.TrimSpace(rows[i].BranchRef)
		if ref == "" {
			continue
		}
		keep, ok := keeps[ref]
		if !ok {
			continue
		}
		rows[i].Kept = true
		rows[i].KeepDecisionID = keep.DecisionID
		rows[i].KeepRevisit = keep.Revisit
		rows[i].NextAction = fmt.Sprintf("kept by operator decision %s — revisit: %s", keep.DecisionID, keep.Revisit)
		kept++
	}
	return kept
}

func classifyWorktreeInventoryRow(ctx context.Context, deps WorktreeInventoryDependencies, root, mainTip string, rec retirementWorktreeRecord, liveness WorktreeLivenessObservation, isMain bool, now time.Time, window time.Duration) WorktreeInventoryRow {
	branchRef := strings.TrimSpace(rec.BranchRef)
	row := WorktreeInventoryRow{
		Path:      rec.Path,
		Head:      rec.Head,
		BranchRef: branchRef,
		Branch:    shortWorktreeBranch(branchRef),
		Detached:  branchRef == "",
	}
	// Liveness + activity are observed first: a live lane must never fall
	// through to a retire-eligible class no matter what the Git facts say.
	row.Liveness = liveness
	statusOut, statusErr := deps.Git.Run(ctx, rec.Path, "--no-optional-locks", "status", "--porcelain=v1", "--untracked-files=all")
	var statusPaths []string
	if statusErr == nil {
		lines := nonEmptyWorktreeLines(statusOut)
		row.DirtyFiles = len(lines)
		statusPaths = parseWorktreeStatusPaths(lines)
	}
	var recent, known bool
	var newest time.Time
	if isMain {
		recent, newest, known = observeDirtyWorktreeActivity(rec.Path, statusPaths, now, window)
	} else {
		headCommitTime, _ := worktreeHeadCommitTime(ctx, deps.Git, rec.Path, rec.Head)
		recent, newest, known = observeWorktreeActivity(rec.Path, statusPaths, headCommitTime, now, window)
	}
	row.ActivityRecent = recent
	if known {
		age := int64(now.Sub(newest).Seconds())
		if age < 0 {
			age = 0
		}
		row.AgeSeconds = &age
	}
	row.Live = row.Liveness.State == WorktreeLivenessActive || recent
	if isMain {
		row.IsMain = true
		row.Class = WorktreeClassMain
		if row.Live {
			row.Reason = "active lane editing the primary checkout — " + livenessReason(row.Liveness, recent, row.AgeSeconds)
		} else {
			row.Reason = "primary checkout — the repository's main working tree and authority root"
		}
		row.NextAction = "leave alone — primary checkout"
		return row
	}

	// Containment and unique-commit count are best-effort here: they are only
	// load-bearing for the non-live, non-probe classes decided below.
	contained, containErr := retirementIsAncestor(ctx, deps.Git, root, rec.Head, mainTip)
	row.Contained = contained
	ahead, aheadErr := worktreeAheadCount(ctx, deps.Git, root, mainTip, rec.Head)
	row.AheadCommits = ahead

	switch {
	case row.Live:
		row.Class = WorktreeClassLive
		row.Reason = "active lane — " + livenessReason(row.Liveness, recent, row.AgeSeconds)
		row.NextAction = "leave alone — active lane"
	case row.Liveness.State == WorktreeLivenessUnknown:
		row.Class = WorktreeClassUnknown
		row.Reason = "could not resolve open-file liveness: " + row.Liveness.Evidence
		row.NextAction = "liveness unresolved — inspect this worktree manually before any cleanup"
	case row.Detached:
		row.Class = WorktreeClassProbe
		row.Reason = "detached HEAD — a throwaway probe checkout with no branch to lose"
		row.NextAction = "inspect if unexpected; a probe carries no branch lane"
	case statusErr != nil:
		row.Class = WorktreeClassUnknown
		row.Reason = "could not read working-tree cleanliness (git status): " + statusErr.Error()
		row.NextAction = "facts unreadable — inspect this worktree manually before any cleanup"
	case containErr != nil:
		row.Class = WorktreeClassUnknown
		row.Reason = "could not read containment vs main (git merge-base --is-ancestor): " + containErr.Error()
		row.NextAction = "facts unreadable — inspect this worktree manually before any cleanup"
	case aheadErr != nil:
		row.Class = WorktreeClassUnknown
		row.Reason = "could not read unique-commit count (git rev-list): " + aheadErr.Error()
		row.NextAction = "facts unreadable — inspect this worktree manually before any cleanup"
	case contained && row.DirtyFiles == 0:
		row.Class = WorktreeClassLandedClean
		row.Reason = "HEAD is contained in main and the working tree is clean — nothing unlanded here"
		row.NextAction = "retire via compound authority worktree-retire (governed)"
	case ahead > 0 || row.DirtyFiles > 0:
		row.Class = WorktreeClassStalledUnlanded
		row.Reason = stalledWorktreeReason(ahead, row.DirtyFiles)
		row.NextAction = "unlanded work — check lane liveness before salvage or keep"
	default:
		row.Class = WorktreeClassUnknown
		row.Reason = "inconsistent ground truth — not contained in main yet reports zero unique commits"
		row.NextAction = "facts unreadable — inspect this worktree manually before any cleanup"
	}
	return row
}

func worktreeAheadCount(ctx context.Context, git WorktreeRetirementGit, root, mainTip, head string) (int, error) {
	out, err := git.Run(ctx, root, "rev-list", "--count", mainTip+".."+head)
	if err != nil {
		return 0, err
	}
	trimmed := strings.TrimSpace(out)
	n, convErr := strconv.Atoi(trimmed)
	if convErr != nil {
		return 0, fmt.Errorf("parse rev-list count %q: %w", trimmed, convErr)
	}
	return n, nil
}

func worktreeHeadCommitTime(ctx context.Context, git WorktreeRetirementGit, worktreePath, head string) (time.Time, bool) {
	out, err := git.Run(ctx, worktreePath, "show", "-s", "--format=%ct", head)
	if err != nil {
		return time.Time{}, false
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(seconds, 0), true
}

// observeWorktreeActivity reads cheap, honest activity evidence: the .git
// entry mtime, newest dirty-path mtime, and HEAD commit time. It never walks the
// whole tree.
func observeWorktreeActivity(worktreePath string, relPaths []string, headCommitTime, now time.Time, window time.Duration) (recent bool, newest time.Time, known bool) {
	return observeWorktreeActivitySources(worktreePath, relPaths, true, headCommitTime, now, window)
}

// Main activity intentionally excludes .git and HEAD: a clean primary checkout
// stays quiet, while a dirty one reports the newest path Git status named.
func observeDirtyWorktreeActivity(worktreePath string, relPaths []string, now time.Time, window time.Duration) (recent bool, newest time.Time, known bool) {
	return observeWorktreeActivitySources(worktreePath, relPaths, false, time.Time{}, now, window)
}

func observeWorktreeActivitySources(worktreePath string, relPaths []string, includeGitEntry bool, headCommitTime, now time.Time, window time.Duration) (recent bool, newest time.Time, known bool) {
	consider := func(path string) {
		info, err := os.Lstat(path)
		if err != nil {
			return
		}
		mt := info.ModTime()
		if !known || mt.After(newest) {
			newest = mt
			known = true
		}
	}
	if includeGitEntry {
		consider(filepath.Join(worktreePath, ".git"))
	}
	for _, rel := range relPaths {
		consider(filepath.Join(worktreePath, rel))
	}
	if !headCommitTime.IsZero() && (!known || headCommitTime.After(newest)) {
		newest = headCommitTime
		known = true
	}
	if !known {
		return false, time.Time{}, false
	}
	return now.Sub(newest) <= window, newest, true
}

func shortWorktreeBranch(ref string) string {
	if ref == "" {
		return ""
	}
	return strings.TrimPrefix(ref, "refs/heads/")
}

func nonEmptyWorktreeLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

// parseWorktreeStatusPaths extracts the working-tree path from each
// git status --porcelain=v1 line (index+worktree status is the first 3 bytes;
// a rename/copy is "old -> new" and we take the destination).
func parseWorktreeStatusPaths(lines []string) []string {
	var paths []string
	for _, line := range lines {
		if len(line) < 4 {
			continue
		}
		p := line[3:]
		if idx := strings.Index(p, " -> "); idx >= 0 {
			p = p[idx+len(" -> "):]
		}
		p = strings.TrimSpace(p)
		p = strings.Trim(p, "\"")
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

func livenessReason(obs WorktreeLivenessObservation, recent bool, age *int64) string {
	switch {
	case obs.State == WorktreeLivenessActive:
		return "open file handles observed in the worktree"
	case recent && age != nil:
		return fmt.Sprintf("files changed %ds ago, within the activity window", *age)
	case recent:
		return "recent file activity within the activity window"
	default:
		return "activity within the window"
	}
}

func stalledWorktreeReason(ahead, dirty int) string {
	switch {
	case ahead > 0 && dirty > 0:
		return fmt.Sprintf("holds %d commit(s) not in main and %d uncommitted file(s)", ahead, dirty)
	case ahead > 0:
		return fmt.Sprintf("holds %d commit(s) not contained in main", ahead)
	default:
		return fmt.Sprintf("holds %d uncommitted file(s) not saved anywhere", dirty)
	}
}
