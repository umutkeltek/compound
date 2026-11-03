package contract

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/authority/gitrepository"
	"compound/internal/mem"
)

// Ambient recall scope gate.
//
// Relevance ranking alone let ledger facts about OTHER repositories reach every session:
// the semantic lane scores any claim against any task (at session start the task is the
// hook event name), and the strongest of overlap and semantic score wins, so a fact about
// an unrelated repository could rank above zero with no word in common with the work.
// Measured before this gate: one upstream fact about an unrelated repository was in 663 of
// 2,000 injections, and 46 of 46 sampled session starts carried off-project facts.
//
// The gate asks a question the ranker cannot: does this fact belong HERE? It applies to
// facts that HAVE a place -- a repository, product, project, or the compound namespace.
// Such a fact passes only when the cwd repository is that place, or the task anchors it
// (names it, addresses a Work the fact cites, or requests an explicit topic). Concept and
// knowledge facts have no place to mismatch; ranking and the semantic lane still decide
// those. The gate never adds a fact, it only removes ones whose place is elsewhere, and no
// recall category is removed (machine status stays a separate, contract-level decision).
type ambientScope struct {
	// repos holds compact keys naming the cwd repository: the directory name, the Git
	// top-level and main-checkout names, the origin remote's repository name, and the
	// resolved product identity when the product fence found one.
	repos map[string]bool
	// taskKeys holds the compact key of each whitespace-separated task word, so
	// "repo-name" in a task matches a scope named "repo-name".
	taskKeys map[string]bool
	// taskTokens holds the content tokens of the task (stopwords and short words dropped).
	taskTokens map[string]bool
	// workAddresses holds the opaque work_* addresses the task names.
	workAddresses []string
}

const (
	ambientScopeExclusionRef    = "memory://out-of-scope-context-omitted"
	ambientScopeExclusionReason = "relevant ledger fact(s) omitted: their repository, product or project is neither the cwd repository nor named by the task"
)

type ambientFactKind int

const (
	// ambientFactConcept is a bare or domain entity (a Compound concept, infra, portfolio,
	// a knowledge kind). It has no place to mismatch, so the gate leaves it to ranking.
	ambientFactConcept ambientFactKind = iota
	// ambientFactRepoScoped names one repository, source, workspace, product or project.
	// Anchored only by the cwd repository or by the task: a predicate word such as
	// "branch" must never pull another repository's branch into this session.
	ambientFactRepoScoped
	// ambientFactOperatorWide is the compound.* namespace. In the compound repository it
	// is the cwd's own fact; elsewhere it is served only when the task uses its predicate.
	ambientFactOperatorWide
)

// ambientShapeWords name the SHAPE of a fact, not its subject. "status" in a task must not
// anchor every compound.*_status fact in the ledger.
var ambientShapeWords = map[string]bool{
	"authority": true, "boundary": true, "decided": true, "metadata": true,
	"source": true, "status": true,
}

// ambientRepoNamespaces are entity namespaces whose name is a repository, product, or
// project. Their facts are about that one place.
var ambientRepoNamespaces = map[string]bool{
	"product": true, "project": true, "repo": true, "source": true, "workspace": true,
}

const ambientOperatorNamespace = "compound"

// ambientFactScope classifies a claim entity and returns the name that anchors it.
//
//	repo:<name>                                  -> repo-scoped, <name>
//	knowledge:project_docs:<project>:<path>@<h>  -> repo-scoped, <project>
//	compound / compound:<x>                      -> operator-wide, compound
//	<ns>:<name> (infra, portfolio, knowledge...) -> concept, <name>
//	<Entity>                                     -> concept, <Entity>
func ambientFactScope(entity string) (ambientFactKind, string) {
	entity = strings.TrimSpace(entity)
	parts := strings.Split(entity, ":")
	namespace := strings.ToLower(strings.TrimSpace(parts[0]))
	if namespace == ambientOperatorNamespace {
		return ambientFactOperatorWide, ambientOperatorNamespace
	}
	if len(parts) == 1 {
		return ambientFactConcept, entity
	}
	if ambientRepoNamespaces[namespace] {
		return ambientFactRepoScoped, strings.Join(parts[1:], ":")
	}
	if namespace == "knowledge" && len(parts) >= 3 && strings.EqualFold(strings.TrimSpace(parts[1]), "project_docs") {
		return ambientFactRepoScoped, parts[2]
	}
	return ambientFactConcept, strings.Join(parts[1:], ":")
}

func resolveAmbientScope(in Input, fence productRecallFence) ambientScope {
	scope := ambientScope{
		repos:         map[string]bool{},
		taskKeys:      map[string]bool{},
		taskTokens:    memoryTextTokens(in.Task),
		workAddresses: memoryWorkAddresses(in.Task),
	}
	addRepo := func(name string) {
		if key := ambientScopeKey(name); key != "" {
			scope.repos[key] = true
		}
	}
	if cwd := strings.TrimSpace(in.Cwd); cwd != "" {
		addRepo(filepath.Base(filepath.Clean(cwd)))
		for _, name := range gitRepositoryNames(cwd) {
			addRepo(name)
		}
	}
	addRepo(fence.Identity)
	for _, field := range strings.Fields(in.Task) {
		if key := ambientScopeKey(field); key != "" {
			scope.taskKeys[key] = true
		}
	}
	return scope
}

// allows is the gate for flat ambient recall (owned-ledger lines and folded positions).
// explicitScope is true when the request narrowed recall to a topic on purpose: the
// operator chose that scope, so it anchors every fact inside it.
func (s ambientScope) allows(claim mem.Claim, explicitScope bool) bool {
	kind, name := ambientFactScope(claim.Entity)
	if kind == ambientFactConcept || explicitScope || s.namesRepo(name) || s.citesAddressedWork(claim) {
		return true
	}
	if kind == ambientFactOperatorWide {
		return s.taskUsesPredicate(claim)
	}
	return s.taskNames(name)
}

// allowsRepoScoped is the narrower gate for probe-contract groups. A probe group is an
// explicit, governed selection of standing facts for a task class, so concept and
// operator-wide facts keep their group membership; but a fact about ANOTHER repository or
// product never rides a probe group into this session.
func (s ambientScope) allowsRepoScoped(claim mem.Claim, explicitScope bool) bool {
	kind, name := ambientFactScope(claim.Entity)
	if kind != ambientFactRepoScoped || explicitScope {
		return true
	}
	return s.namesRepo(name) || s.taskNames(name) || s.citesAddressedWork(claim)
}

// citesAddressedWork: the task addresses a Work and the fact names that exact Work in its
// meaning or provenance. That is the task anchoring the fact, whatever repository it is in.
func (s ambientScope) citesAddressedWork(claim mem.Claim) bool {
	for _, address := range s.workAddresses {
		if memoryClaimMentionsWork(claim, address) {
			return true
		}
	}
	return false
}

func (s ambientScope) namesRepo(name string) bool {
	key := ambientScopeKey(name)
	return key != "" && s.repos[key]
}

// taskNames reports whether the task names a scope: either one task word is the whole
// name ("sample-claims"), or every content word of the name appears in the task
// ("sample claims").
func (s ambientScope) taskNames(name string) bool {
	key := ambientScopeKey(name)
	if key == "" {
		return false
	}
	if s.taskKeys[key] {
		return true
	}
	words := memoryTextTokens(name)
	if len(words) == 0 {
		return false
	}
	for word := range words {
		if !s.taskTokens[word] {
			return false
		}
	}
	return true
}

// taskUsesPredicate anchors an operator-wide fact by a content word of its predicate.
func (s ambientScope) taskUsesPredicate(claim mem.Claim) bool {
	for word := range memoryTextTokens(claim.Predicate) {
		if !ambientShapeWords[word] && s.taskTokens[word] {
			return true
		}
	}
	return false
}

// ambientScopeKey is memoryKey (last namespace segment, lowercase alphanumerics) for a
// scope name. "Sample-Repo", "sample_repo" and "repo:sample-repo" share one key.
func ambientScopeKey(name string) string {
	return memoryKey(name)
}

// ambientScopeGitTimeout bounds the two read-only Git queries. A slow or broken checkout
// leaves only the directory name in scope, which makes the gate stricter, never looser.
const ambientScopeGitTimeout = 2 * time.Second

// gitRepositoryNames returns the names a checkout is known by: its top-level directory,
// the main checkout's directory (for a linked worktree), and the origin remote's
// repository name. A linked worktree directory is usually named after the branch or the
// agent, so the remote and the main checkout are what make it the same repository.
func gitRepositoryNames(cwd string) []string {
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), ambientScopeGitTimeout)
	defer cancel()
	var names []string
	if out, err := ambientGitOutput(ctx, cwd, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir"); err == nil {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) >= 1 && strings.TrimSpace(lines[0]) != "" {
			names = append(names, filepath.Base(strings.TrimSpace(lines[0])))
		}
		if len(lines) >= 2 {
			common := filepath.Clean(strings.TrimSpace(lines[1]))
			if filepath.Base(common) == ".git" {
				names = append(names, filepath.Base(filepath.Dir(common)))
			}
		}
	}
	if out, err := ambientGitOutput(ctx, cwd, "config", "--get", "remote.origin.url"); err == nil {
		if name := remoteRepositoryName(out); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func ambientGitOutput(ctx context.Context, cwd string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...)
	cmd.Env = gitrepository.ReadEnvironment()
	out, err := cmd.Output()
	return string(out), err
}

// remoteRepositoryName extracts "name" from "git@host:owner/name.git",
// "https://host/owner/name(.git)" or a local path.
func remoteRepositoryName(remote string) string {
	remote = strings.TrimSpace(remote)
	remote = strings.TrimRight(remote, "/")
	if i := strings.LastIndexAny(remote, "/:"); i >= 0 {
		remote = remote[i+1:]
	}
	return strings.TrimSuffix(remote, ".git")
}
