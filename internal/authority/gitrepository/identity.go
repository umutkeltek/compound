// Package gitrepository implements the source-control primitives owned by the
// authority layer without importing the higher-level authority or work
// packages. This keeps repository identity and Git ref locking reusable at the
// WorkEnvelope completion boundary without creating an authority <-> work
// import cycle.
package gitrepository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
)

// Identity is stable for ordinary descendants of Ref while binding the
// repository's object format, root lineage, and canonical origin identity.
// The worktree path is deliberately excluded: paths locate a checkout, but do
// not prove which repository currently occupies it.
type Identity struct {
	Fingerprint    string   `json:"fingerprint"`
	ObjectFormat   string   `json:"object_format"`
	LineageRoots   []string `json:"lineage_roots"`
	RemoteIdentity string   `json:"remote_identity,omitempty"`
}

type identityContent struct {
	ObjectFormat   string   `json:"object_format"`
	LineageRoots   []string `json:"lineage_roots"`
	RemoteIdentity string   `json:"remote_identity"`
}

// ReadIdentity derives a collision-resistant logical repository identity at
// ref. Using the bound expected-base commit as ref makes the identity stable as
// branches advance, while a different lineage or canonical origin fails closed.
func ReadIdentity(repo, ref string) (Identity, error) {
	return ReadIdentityContext(context.Background(), repo, ref)
}

// ReadIdentityContext preserves caller cancellation and bounds each Git command
// independently. The caller deadline still caps the complete identity read.
func ReadIdentityContext(ctx context.Context, repo, ref string) (Identity, error) {
	return NewIdentitySession().ReadIdentityContext(ctx, repo, ref)
}

// identityFact is one memoized repository-scoped read: its value, or the
// error it produced. A failed read is memoized too, so a broken repository
// costs one Git process per request instead of one per work item.
type identityFact struct {
	value string
	err   error
}

// IdentitySession memoizes the ref-INDEPENDENT half of a repository identity
// read for the lifetime of one request.
//
// The object format and the canonical origin are repository properties: they
// do not change with the commit a read is anchored at. Reading them once per
// repository instead of once per (repository, ref) is the difference between
// one Git process and three for every work item a delivery projection
// evaluates.
//
// The lineage roots are deliberately NOT shared. A different base can name a
// different lineage, and the identity must fail closed on that, so the roots
// stay keyed by the ref they were read at.
type IdentitySession struct {
	mu            sync.Mutex
	objectFormats map[string]identityFact
	remotes       map[string]identityFact
	// lineageRoots is keyed by repository AND ref: the root lineage of one
	// exact commit never changes, but two different refs of the same repository
	// can name DIFFERENT lineages, so the key must carry both.
	lineageMemo map[string][]string
}

// NewIdentitySession returns a session that starts with no memoized facts.
func NewIdentitySession() *IdentitySession {
	return &IdentitySession{
		objectFormats: map[string]identityFact{},
		remotes:       map[string]identityFact{},
		lineageMemo:   map[string][]string{},
	}
}

// ReadIdentityContext derives the repository identity at ref, reusing this
// session's ref-independent repository facts. The result is identical to a
// standalone read of the same repository and ref.
func (s *IdentitySession) ReadIdentityContext(ctx context.Context, repo, ref string) (Identity, error) {
	repo = strings.TrimSpace(repo)
	ref = strings.TrimSpace(ref)
	if repo == "" {
		return Identity{}, fmt.Errorf("repository path required")
	}
	if ref == "" {
		return Identity{}, fmt.Errorf("repository identity ref required")
	}

	objectFormat, err := s.objectFormat(ctx, repo)
	if err != nil {
		return Identity{}, err
	}
	if objectFormat != "sha1" && objectFormat != "sha256" {
		return Identity{}, fmt.Errorf("unsupported Git object format %q", objectFormat)
	}
	roots, err := s.lineageRoots(ctx, repo, ref)
	if err != nil {
		return Identity{}, err
	}
	remote, err := s.remoteIdentity(ctx, repo)
	if err != nil {
		return Identity{}, err
	}
	return identityFromParts(objectFormat, roots, remote)
}

// objectFormat reads the repository's object format once per repository.
func (s *IdentitySession) objectFormat(ctx context.Context, repo string) (string, error) {
	if fact, ok := s.fact(s.objectFormats, repo); ok {
		return fact.value, fact.err
	}
	out, err := gitOutput(ctx, repo, "rev-parse", "--show-object-format")
	if err != nil {
		err = fmt.Errorf("read Git object format: %w", err)
		s.remember(s.objectFormats, repo, identityFact{err: err})
		return "", err
	}
	value := strings.TrimSpace(out)
	s.remember(s.objectFormats, repo, identityFact{value: value})
	return value, nil
}

// remoteIdentity reads the repository's canonical origin once per repository.
func (s *IdentitySession) remoteIdentity(ctx context.Context, repo string) (string, error) {
	if fact, ok := s.fact(s.remotes, repo); ok {
		return fact.value, fact.err
	}
	out, err := optionalGitOutput(ctx, repo, "config", "--get", "remote.origin.url")
	if err != nil {
		err = fmt.Errorf("read canonical Git origin: %w", err)
		s.remember(s.remotes, repo, identityFact{err: err})
		return "", err
	}
	value := strings.TrimSpace(out)
	s.remember(s.remotes, repo, identityFact{value: value})
	return value, nil
}

// lineageRoots returns the root lineage at one exact ref.
func (s *IdentitySession) lineageRoots(ctx context.Context, repo, ref string) ([]string, error) {
	key := repo + "\x00" + ref
	if s != nil && s.lineageMemo != nil {
		s.mu.Lock()
		roots, ok := s.lineageMemo[key]
		s.mu.Unlock()
		if ok {
			return append([]string{}, roots...), nil
		}
	}
	roots, err := lineageRootsAt(ctx, repo, ref)
	if err != nil {
		return nil, err
	}
	if s != nil && s.lineageMemo != nil {
		s.mu.Lock()
		s.lineageMemo[key] = append([]string{}, roots...)
		s.mu.Unlock()
	}
	return roots, nil
}

func (s *IdentitySession) fact(cache map[string]identityFact, repo string) (identityFact, bool) {
	if s == nil || cache == nil {
		return identityFact{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fact, ok := cache[repo]
	return fact, ok
}

func (s *IdentitySession) remember(cache map[string]identityFact, repo string, fact identityFact) {
	if s == nil || cache == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cache[repo] = fact
}

// lineageRoots reads the repository's root lineage at one exact ref, once per
// (repository, ref). The root lineage of one exact commit never changes, so
// the memo is safe; two different refs of the same repository can name
// DIFFERENT lineages, so the key carries the ref.
func lineageRootsAt(ctx context.Context, repo, ref string) ([]string, error) {
	rootOutput, err := gitOutput(ctx, repo, "rev-list", "--max-parents=0", ref)
	if err != nil {
		return nil, fmt.Errorf("read repository lineage at %s: %w", ref, err)
	}
	roots := compactLines(rootOutput)
	if len(roots) == 0 {
		return nil, fmt.Errorf("repository lineage at %s has no root commit", ref)
	}
	for _, root := range roots {
		if !validObjectID(root) {
			return nil, fmt.Errorf("repository lineage contains invalid root object id %q", root)
		}
	}
	sort.Strings(roots)
	return roots, nil
}

// readIdentityWithObjectFormat consumes the format just observed by this same
// request. Standalone identity reads still resolve their format freshly.
func readIdentityWithObjectFormat(ctx context.Context, repo, ref, objectFormat string) (Identity, error) {
	if objectFormat != "sha1" && objectFormat != "sha256" {
		return Identity{}, fmt.Errorf("unsupported Git object format %q", objectFormat)
	}

	roots, err := lineageRootsAt(ctx, repo, ref)
	if err != nil {
		return Identity{}, err
	}

	remote, err := optionalGitOutput(ctx, repo, "config", "--get", "remote.origin.url")
	if err != nil {
		return Identity{}, fmt.Errorf("read canonical Git origin: %w", err)
	}
	return identityFromParts(objectFormat, roots, remote)
}

// WithOrigin derives the identity this repository's object format and lineage
// roots would carry under another canonical origin. The fingerprint is a
// commitment to all three parts, so reproducing a bound fingerprint with the
// format and roots read now and a claimed earlier origin proves that only the
// origin changed; another lineage can never reproduce it.
func (id Identity) WithOrigin(origin string) (Identity, error) {
	return identityFromParts(id.ObjectFormat, id.LineageRoots, origin)
}

// identityFromParts assembles the identity from its three inputs. Every path
// that builds an identity uses this, so the fingerprint definition stays in one
// place.
func identityFromParts(objectFormat string, roots []string, remote string) (Identity, error) {
	content := identityContent{
		ObjectFormat:   objectFormat,
		LineageRoots:   roots,
		RemoteIdentity: strings.TrimSpace(remote),
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		return Identity{}, err
	}
	sum := sha256.Sum256(encoded)
	return Identity{
		Fingerprint:    hex.EncodeToString(sum[:]),
		ObjectFormat:   content.ObjectFormat,
		LineageRoots:   append([]string{}, content.LineageRoots...),
		RemoteIdentity: content.RemoteIdentity,
	}, nil
}

func compactLines(value string) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	return out
}

func validObjectID(value string) bool {
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

func optionalGitOutput(ctx context.Context, repo string, args ...string) (string, error) {
	out, err := gitOutput(ctx, repo, args...)
	if err == nil {
		return out, nil
	}
	var commandErr *gitCommandError
	if errors.As(err, &commandErr) && commandErr.exitCode == 1 {
		return "", nil
	}
	return "", err
}

type gitCommandError struct {
	args     []string
	exitCode int
	output   string
}

func (e *gitCommandError) Error() string {
	return strings.Join(e.args, " ") + ": " + e.output
}

func gitOutput(ctx context.Context, repo string, args ...string) (string, error) {
	ctx, cancel := gitReadContext(ctx)
	defer cancel()
	cmd := CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	cmd.Env = ReadEnvironment()
	out, err := CommandOutput(ctx, cmd, true)
	if ctx.Err() != nil {
		return "", context.Cause(ctx)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "", err
	}
	if err != nil {
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		return "", &gitCommandError{args: append([]string{}, args...), exitCode: exitCode, output: message}
	}
	return string(out), nil
}

// ContextWithCommandFactory retains the execution owner's cancellation and child isolation
// when repository reads are preparation for a governed operation.
func ContextWithCommandFactory(ctx context.Context, factory func(context.Context, string, ...string) *exec.Cmd) context.Context {
	return context.WithValue(ctx, commandFactoryKey{}, factory)
}

type commandFactoryKey struct{}

// CommandContext delegates to the caller's execution owner, or the standard direct-child command.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	if factory, ok := ctx.Value(commandFactoryKey{}).(func(context.Context, string, ...string) *exec.Cmd); ok {
		return factory(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...)
}
