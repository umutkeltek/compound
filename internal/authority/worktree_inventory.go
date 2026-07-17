package authority

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
// main > live > probe > unknown(unreadable) > landed_clean > stalled_unlanded.
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
	defaultWorktreeActivityWindow = 60 * time.Minute
	defaultWorktreeAuthorityRef   = "refs/heads/main"
)

// WorktreeInventoryDependencies reuses the retirement Git + Liveness seams so
// the inventory reads ground truth through the SAME argv-vector Git adapter and
// the SAME liveness probe as worktree retirement. There is deliberately no
// direct shelling and no second liveness mechanism here.
type WorktreeInventoryDependencies struct {
	Git      WorktreeRetirementGit
	Liveness WorktreeRetirementLiveness
}

// WorktreeInventoryOptions carries the injected clock (tests) and the activity
// window. AuthorityRef defaults to refs/heads/main; classification is measured
// against that tip's containment.
type WorktreeInventoryOptions struct {
	Now            time.Time
	ActivityWindow time.Duration
	AuthorityRef   string
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
}

type WorktreeInventoryCounts struct {
	Total           int `json:"total"`
	Main            int `json:"main"`
	Live            int `json:"live"`
	StalledUnlanded int `json:"stalled_unlanded"`
	LandedClean     int `json:"landed_clean"`
	Probe           int `json:"probe"`
	Unknown         int `json:"unknown"`
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
}

// BuildWorktreeInventory classifies every Git worktree of repo from ground
// truth alone. It is strictly read-only: it invokes only read verbs through the
// injected Git seam and never mutates a ref, index, or working tree.
//
// v1 asserts NO lane/envelope identity. A WorkEnvelope's git_delivery binds the
// canonical repository root (…/compound), not a lane worktree path, so there is
// no exact evidence today to join an envelope to a worktree. The branch name IS
// the lane label; the ledger join arrives with slice 2's keep-decision records.
func BuildWorktreeInventory(ctx context.Context, deps WorktreeInventoryDependencies, repo string, opts WorktreeInventoryOptions) (WorktreeInventory, error) {
	if deps.Git == nil {
		deps.Git = ExecWorktreeRetirementGit{}
	}
	if deps.Liveness == nil {
		deps.Liveness = LSOFWorktreeRetirementLiveness{}
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
		row := classifyWorktreeInventoryRow(ctx, deps, root, mainTip, records[i], i == 0, now, window)
		inv.Rows = append(inv.Rows, row)
		inv.Counts.Total++
		switch row.Class {
		case WorktreeClassMain:
			inv.Counts.Main++
		case WorktreeClassLive:
			inv.Counts.Live++
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
	return inv, nil
}

func classifyWorktreeInventoryRow(ctx context.Context, deps WorktreeInventoryDependencies, root, mainTip string, rec retirementWorktreeRecord, isMain bool, now time.Time, window time.Duration) WorktreeInventoryRow {
	branchRef := strings.TrimSpace(rec.BranchRef)
	row := WorktreeInventoryRow{
		Path:      rec.Path,
		Head:      rec.Head,
		BranchRef: branchRef,
		Branch:    shortWorktreeBranch(branchRef),
		Detached:  branchRef == "",
	}
	if isMain {
		row.IsMain = true
		row.Class = WorktreeClassMain
		row.Reason = "primary checkout — the repository's main working tree and authority root"
		row.NextAction = "leave alone — primary checkout"
		return row
	}

	// Liveness + activity are observed first: a live lane must never fall
	// through to a retire-eligible class no matter what the Git facts say.
	row.Liveness = deps.Liveness.Observe(ctx, rec.Path)
	statusOut, statusErr := deps.Git.Run(ctx, rec.Path, "--no-optional-locks", "status", "--porcelain=v1", "--untracked-files=all")
	var statusPaths []string
	if statusErr == nil {
		lines := nonEmptyWorktreeLines(statusOut)
		row.DirtyFiles = len(lines)
		statusPaths = parseWorktreeStatusPaths(lines)
	}
	recent, newest, known := observeWorktreeActivity(rec.Path, statusPaths, now, window)
	row.ActivityRecent = recent
	if known {
		age := int64(now.Sub(newest).Seconds())
		if age < 0 {
			age = 0
		}
		row.AgeSeconds = &age
	}
	row.Live = row.Liveness.State == WorktreeLivenessActive || recent

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

// observeWorktreeActivity reads cheap, honest activity evidence: the mtime of
// the worktree's .git entry plus the newest mtime among the paths git status
// already reported dirty. It never walks the whole tree.
func observeWorktreeActivity(worktreePath string, relPaths []string, now time.Time, window time.Duration) (recent bool, newest time.Time, known bool) {
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
	consider(filepath.Join(worktreePath, ".git"))
	for _, rel := range relPaths {
		consider(filepath.Join(worktreePath, rel))
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
