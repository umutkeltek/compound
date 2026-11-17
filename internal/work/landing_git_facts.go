package work

import (
	"context"
	"fmt"
	"strings"
	"time"

	"compound/internal/authority/gitrepository"
)

// landingRead* name the Git fact kinds one projection request may reuse. They
// double as the deterministic read-count keys the focused tests pin: counting
// fact reads (not wall clock) keeps the amortization contract falsifiable.
const (
	landingReadCanonicalRepo = "canonical_repo"
	landingReadTargetRef     = "target_ref"
	landingReadRefCommit     = "ref_commit"
	landingReadCurrentBranch = "current_branch"
	landingReadStatus        = "status"
	landingReadWorktreeList  = "worktree_list"
	landingReadRefExists     = "ref_exists"
	landingReadCommitExists  = "commit_exists"
	landingReadAncestry      = "ancestry"
	landingReadIdentity      = "identity"
	landingReadOwnedPaths    = "owned_paths"
)

// landingProjectionReadTimeout bounds each repository-scoped Git read of one
// projection session. A healthy read answers in milliseconds; a repository on
// a blocked volume never answers, and without a bound one such repository
// froze the whole Workbench (every row, every console page that reads it).
const landingProjectionReadTimeout = 10 * time.Second

// landingRepositoryUnavailableError reports that one repository did not answer
// a Git read inside the session's bound. It is an observation about this
// request, not a delivery verdict: the projection shows the row as unknown and
// the governed land-check and close paths, which never use a bounded session,
// still read the repository fresh before any decision.
type landingRepositoryUnavailableError struct {
	repo    string
	timeout time.Duration
}

func (e *landingRepositoryUnavailableError) Error() string {
	return fmt.Sprintf("repository %s did not answer a Git read within %s", e.repo, e.timeout)
}

// landingFact is one cached read result: the value or the error, never both.
// Caching errors is the point — every Work affected by the same failed Git
// input must see that failure, not a silently empty fact.
type landingFact[T any] struct {
	value T
	err   error
}

type landingTargetRefFact struct {
	branch string
	ref    string
}

type landingOwnedDirtyFact struct {
	dirty bool
	paths []string
}

// landingGitFacts is a request-local read session over the Git facts that many
// Work delivery evaluations share. It exists so one projection request (one
// Workbench / coordination call) pays for each repository-scoped Git read once
// while every Work still evaluates its own envelope, contract, artifact,
// proof, publication, and lineage verdicts independently.
//
// It memoizes READ FACTS ONLY — never a LandCheckReport, never a verdict, and
// never anything Work-specific (proof resolution stays per-Work). Every key is
// the exact input tuple of the underlying read:
//
//   - canonical repository root, by asserted path;
//   - normalized target ref and ref→commit resolution, by repository + ref
//     (mutable refs keep first-read snapshot semantics inside one request);
//   - current branch, whole-tree status, and worktree list, by repository;
//   - ref existence, by repository + ref;
//   - commit existence and ancestry, by exact object ids;
//   - repository identity, by canonical root AND exact expected base — never
//     the root alone, because a different base can name a different lineage;
//   - owned-path dirt, by repository + exact base + candidate.
//
// A nil session is valid and means "read fresh on every call": the governed
// mutation paths (land-check, close, admission) never share one. The session
// dies with its request; it is never stored on the Store, never persisted, and
// never crosses processes, so a new request rereads every mutable fact (refs,
// status, origin identity, proofs) and every input that was missing before.
//
// A projection session also bounds every read (readTimeout). A repository
// that does not answer one read in time is marked unavailable for the rest of
// the request: each later read of it returns the same typed error at once, so
// one blocked volume costs one bound per repository, never one per row or per
// read, and repositories that answer are evaluated exactly as before. The
// abandoned read keeps running in its own goroutine until the volume answers;
// it never writes to the session, whose caches are filled only by the caller.
type landingGitFacts struct {
	canonicalRepoCache map[string]landingFact[string]
	targetRefCache     map[string]landingFact[landingTargetRefFact]
	refCommitCache     map[string]landingFact[string]
	currentBranchCache map[string]landingFact[string]
	statusCache        map[string]landingFact[string]
	worktreeListCache  map[string]landingFact[string]
	refExistsCache     map[string]landingFact[bool]
	commitExistsCache  map[string]landingFact[bool]
	ancestryCache      map[string]landingFact[bool]
	identityCache      map[string]landingFact[gitrepository.Identity]
	// identitySession amortizes the ref-INDEPENDENT half of an identity read
	// (the repository's object format and canonical origin) across every
	// (root, base) pair this session evaluates. The lineage roots stay keyed by
	// base, because a different base can name a different lineage.
	identitySession *gitrepository.IdentitySession
	ownedDirtyCache map[string]landingFact[landingOwnedDirtyFact]

	// readTimeout bounds each read; zero reads without a bound.
	readTimeout time.Duration
	// unavailable names each repository (by the exact path the read used) that
	// did not answer inside readTimeout during this request.
	unavailable map[string]*landingRepositoryUnavailableError

	// reads counts, per fact kind, the real reads this session performed
	// (cache hits are free). Projection evidence only; never a gate.
	reads map[string]int
}

func newLandingGitFacts() *landingGitFacts {
	return &landingGitFacts{
		canonicalRepoCache: map[string]landingFact[string]{},
		targetRefCache:     map[string]landingFact[landingTargetRefFact]{},
		refCommitCache:     map[string]landingFact[string]{},
		currentBranchCache: map[string]landingFact[string]{},
		statusCache:        map[string]landingFact[string]{},
		worktreeListCache:  map[string]landingFact[string]{},
		refExistsCache:     map[string]landingFact[bool]{},
		commitExistsCache:  map[string]landingFact[bool]{},
		ancestryCache:      map[string]landingFact[bool]{},
		identityCache:      map[string]landingFact[gitrepository.Identity]{},
		identitySession:    gitrepository.NewIdentitySession(),
		ownedDirtyCache:    map[string]landingFact[landingOwnedDirtyFact]{},
		readTimeout:        landingProjectionReadTimeout,
		unavailable:        map[string]*landingRepositoryUnavailableError{},
		reads:              map[string]int{},
	}
}

// boundedLandingRead runs one repository read under the session's bound. A
// nil or unbounded session calls read directly, so governed paths keep their
// exact fresh-read behavior. read must not touch the session: on a timeout it
// keeps running unobserved, and the buffered result is dropped.
func boundedLandingRead[T any](f *landingGitFacts, repo string, read func() (T, error)) (T, error) {
	var zero T
	if f == nil || f.readTimeout <= 0 {
		return read()
	}
	if unavailable := f.unavailable[repo]; unavailable != nil {
		return zero, unavailable
	}
	type outcome struct {
		value T
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		value, err := read()
		done <- outcome{value: value, err: err}
	}()
	timer := time.NewTimer(f.readTimeout)
	defer timer.Stop()
	select {
	case result := <-done:
		return result.value, result.err
	case <-timer.C:
		unavailable := &landingRepositoryUnavailableError{repo: repo, timeout: f.readTimeout}
		if f.unavailable == nil {
			f.unavailable = map[string]*landingRepositoryUnavailableError{}
		}
		f.unavailable[repo] = unavailable
		return zero, unavailable
	}
}

func (f *landingGitFacts) recordRead(kind string) {
	if f.reads == nil {
		return
	}
	f.reads[kind]++
}

// readCount reports how many real Git reads of one fact kind this session
// performed. Deterministic projection evidence for the focused tests.
func (f *landingGitFacts) readCount(kind string) int {
	if f == nil {
		return 0
	}
	return f.reads[kind]
}

// canonicalRepo resolves an asserted path to the canonical Git worktree root
// (canonicalLandingRepo is the single authority for that definition).
func (f *landingGitFacts) canonicalRepo(path string) (string, error) {
	if f == nil {
		return canonicalLandingRepo(path)
	}
	if fact, ok := f.canonicalRepoCache[path]; ok {
		return fact.value, fact.err
	}
	value, err := boundedLandingRead(f, path, func() (string, error) { return canonicalLandingRepo(path) })
	f.recordRead(landingReadCanonicalRepo)
	f.canonicalRepoCache[path] = landingFact[string]{value: value, err: err}
	return value, err
}

// normalizeTargetRef canonicalizes one target local ref inside one repository.
func (f *landingGitFacts) normalizeTargetRef(repoPath, value string) (string, string, error) {
	if f == nil {
		return normalizeLandingTargetRef(repoPath, value)
	}
	key := landingFactsKey(repoPath, value)
	if fact, ok := f.targetRefCache[key]; ok {
		return fact.value.branch, fact.value.ref, fact.err
	}
	fact, err := boundedLandingRead(f, repoPath, func() (landingTargetRefFact, error) {
		branch, ref, err := normalizeLandingTargetRef(repoPath, value)
		return landingTargetRefFact{branch: branch, ref: ref}, err
	})
	branch, ref := fact.branch, fact.ref
	f.recordRead(landingReadTargetRef)
	f.targetRefCache[key] = landingFact[landingTargetRefFact]{
		value: landingTargetRefFact{branch: branch, ref: ref},
		err:   err,
	}
	return branch, ref, err
}

// refCommit resolves one ref to its full commit object id. Refs are mutable:
// the first resolution inside a request is the snapshot the whole request
// uses, and a fresh request resolves again.
func (f *landingGitFacts) refCommit(repoPath, ref string) (string, error) {
	if f == nil {
		return runLandingGitOutput(repoPath, "rev-parse", "--verify", ref+"^{commit}")
	}
	key := landingFactsKey(repoPath, ref)
	if fact, ok := f.refCommitCache[key]; ok {
		return fact.value, fact.err
	}
	value, err := boundedLandingRead(f, repoPath, func() (string, error) {
		return runLandingGitOutput(repoPath, "rev-parse", "--verify", ref+"^{commit}")
	})
	f.recordRead(landingReadRefCommit)
	f.refCommitCache[key] = landingFact[string]{value: value, err: err}
	return value, err
}

// currentBranchRef reports the symbolic HEAD ref of one worktree.
func (f *landingGitFacts) currentBranchRef(repoPath string) (string, error) {
	if f == nil {
		return landingCurrentBranchRef(repoPath)
	}
	if fact, ok := f.currentBranchCache[repoPath]; ok {
		return fact.value, fact.err
	}
	value, err := boundedLandingRead(f, repoPath, func() (string, error) { return landingCurrentBranchRef(repoPath) })
	f.recordRead(landingReadCurrentBranch)
	f.currentBranchCache[repoPath] = landingFact[string]{value: value, err: err}
	return value, err
}

// statusPorcelain reads the whole-tree porcelain-v1 status of one worktree.
// The raw output is cached; callers keep their own error wrapping.
func (f *landingGitFacts) statusPorcelain(repoPath string) (string, error) {
	if f == nil {
		return runLandingGitOutput(repoPath, "status", "--porcelain=v1", "--untracked-files=all")
	}
	if fact, ok := f.statusCache[repoPath]; ok {
		return fact.value, fact.err
	}
	value, err := boundedLandingRead(f, repoPath, func() (string, error) {
		return runLandingGitOutput(repoPath, "status", "--porcelain=v1", "--untracked-files=all")
	})
	f.recordRead(landingReadStatus)
	f.statusCache[repoPath] = landingFact[string]{value: value, err: err}
	return value, err
}

// worktreeList reads the porcelain worktree list of one repository. The raw
// output is cached; callers keep their own error wrapping.
func (f *landingGitFacts) worktreeList(repoPath string) (string, error) {
	if f == nil {
		return runLandingGitOutput(repoPath, "worktree", "list", "--porcelain", "-z")
	}
	if fact, ok := f.worktreeListCache[repoPath]; ok {
		return fact.value, fact.err
	}
	value, err := boundedLandingRead(f, repoPath, func() (string, error) {
		return runLandingGitOutput(repoPath, "worktree", "list", "--porcelain", "-z")
	})
	f.recordRead(landingReadWorktreeList)
	f.worktreeListCache[repoPath] = landingFact[string]{value: value, err: err}
	return value, err
}

// refCheckedOut reports whether any worktree of one repository holds the exact
// branch ref, derived from the session's shared worktree list.
func (f *landingGitFacts) refCheckedOut(repoPath, targetBranchRef string) (bool, error) {
	if f == nil {
		return landingRefCheckedOut(repoPath, targetBranchRef)
	}
	out, err := f.worktreeList(repoPath)
	if err != nil {
		return false, fmt.Errorf("inspect worktrees for target ref %s: %w", targetBranchRef, err)
	}
	return landingWorktreeListHasBranch(out, targetBranchRef), nil
}

// refExists reports whether one exact ref exists in one repository.
func (f *landingGitFacts) refExists(repoPath, ref string) (bool, error) {
	if f == nil {
		return landingRefExists(repoPath, ref)
	}
	key := landingFactsKey(repoPath, ref)
	if fact, ok := f.refExistsCache[key]; ok {
		return fact.value, fact.err
	}
	value, err := boundedLandingRead(f, repoPath, func() (bool, error) { return landingRefExists(repoPath, ref) })
	f.recordRead(landingReadRefExists)
	f.refExistsCache[key] = landingFact[bool]{value: value, err: err}
	return value, err
}

// commitExists reports whether one exact commit object id exists in one
// repository. Missing ids are a fact, not an error, so a repaired repository
// is reread by the next request.
func (f *landingGitFacts) commitExists(repoPath, sha string) (bool, error) {
	if f == nil {
		return landingCommitExists(repoPath, sha)
	}
	key := landingFactsKey(repoPath, sha)
	if fact, ok := f.commitExistsCache[key]; ok {
		return fact.value, fact.err
	}
	value, err := boundedLandingRead(f, repoPath, func() (bool, error) { return landingCommitExists(repoPath, sha) })
	f.recordRead(landingReadCommitExists)
	f.commitExistsCache[key] = landingFact[bool]{value: value, err: err}
	return value, err
}

// isAncestor answers one exact ancestry question between two object ids.
// Callers resolve refs to immutable ids first, so the key never depends on a
// mutable ref name.
func (f *landingGitFacts) isAncestor(repoPath, ancestor, descendant string) (bool, error) {
	if f == nil {
		return landingIsAncestor(repoPath, ancestor, descendant)
	}
	key := landingFactsKey(repoPath, ancestor, descendant)
	if fact, ok := f.ancestryCache[key]; ok {
		return fact.value, fact.err
	}
	value, err := boundedLandingRead(f, repoPath, func() (bool, error) {
		return landingIsAncestor(repoPath, ancestor, descendant)
	})
	f.recordRead(landingReadAncestry)
	f.ancestryCache[key] = landingFact[bool]{value: value, err: err}
	return value, err
}

// repositoryIdentity derives the repository identity at one exact base commit.
// Keyed by canonical root AND expected base — never the root alone — because
// the lineage roots and origin identity are read at that base. The
// gitrepository package stays the sole owner of the definition.
func (f *landingGitFacts) repositoryIdentity(root, expectedBase string) (gitrepository.Identity, error) {
	if f == nil {
		return gitrepository.ReadIdentity(root, expectedBase)
	}
	key := landingFactsKey(root, expectedBase)
	if fact, ok := f.identityCache[key]; ok {
		return fact.value, fact.err
	}
	// IdentitySession is safe for concurrent use and never holds its lock
	// across a Git read, so an abandoned identity read cannot block the next.
	identities := f.identitySession
	value, err := boundedLandingRead(f, root, func() (gitrepository.Identity, error) {
		return identities.ReadIdentityContext(context.Background(), root, expectedBase)
	})
	f.recordRead(landingReadIdentity)
	f.identityCache[key] = landingFact[gitrepository.Identity]{value: value, err: err}
	return value, err
}

// ownedPathsDirty reports the uncommitted changes inside one exact
// base..candidate owned-path set of one repository.
func (f *landingGitFacts) ownedPathsDirty(repoPath, expectedBase, candidate string) (bool, []string, error) {
	if f == nil {
		return landingOwnedPathsDirty(repoPath, expectedBase, candidate)
	}
	key := landingFactsKey(repoPath, expectedBase, candidate)
	if fact, ok := f.ownedDirtyCache[key]; ok {
		return fact.value.dirty, append([]string(nil), fact.value.paths...), fact.err
	}
	owned, err := boundedLandingRead(f, repoPath, func() (landingOwnedDirtyFact, error) {
		dirty, paths, err := landingOwnedPathsDirty(repoPath, expectedBase, candidate)
		return landingOwnedDirtyFact{dirty: dirty, paths: paths}, err
	})
	dirty, paths := owned.dirty, owned.paths
	f.recordRead(landingReadOwnedPaths)
	f.ownedDirtyCache[key] = landingFact[landingOwnedDirtyFact]{
		value: landingOwnedDirtyFact{dirty: dirty, paths: append([]string(nil), paths...)},
		err:   err,
	}
	return dirty, append([]string(nil), paths...), err
}

// landingFactsKey joins exact read inputs into one cache key. NUL separates
// the fields because no Git ref name, path, or object id may contain one.
func landingFactsKey(fields ...string) string {
	return strings.Join(fields, "\x00")
}
