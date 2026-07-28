package gitrepository

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Snapshot seals the immutable Git source identity certified by a proof
// battery. BaseSHA is resolved once from the caller's ref; every later
// operation uses the sealed object ID rather than resolving that ref again.
type Snapshot struct {
	RepositoryFingerprint string `json:"repository_fingerprint"`
	BaseSHA               string `json:"base_sha"`
	HeadSHA               string `json:"head_sha"`
	TreeSHA               string `json:"tree_sha"`
	Clean                 bool   `json:"clean"`
}

// CaptureCleanSnapshot resolves baseRef once and refuses a candidate whose
// source is not entirely represented by its committed HEAD tree.
func CaptureCleanSnapshot(repo, baseRef string) (Snapshot, error) {
	snapshot, err := readSnapshot(repo, baseRef)
	if err != nil {
		return Snapshot{}, err
	}
	if !snapshot.Clean {
		return Snapshot{}, fmt.Errorf("certifying proof requires a clean committed Git worktree")
	}
	return snapshot, nil
}

// RevalidateSnapshot proves the repository still presents the exact sealed
// source identity. It intentionally resolves the immutable BaseSHA, never the
// moving symbolic ref originally supplied by the caller.
func RevalidateSnapshot(repo string, expected Snapshot) error {
	if err := ValidateSnapshotShape(expected); err != nil {
		return fmt.Errorf("sealed source snapshot: %w", err)
	}
	if !expected.Clean {
		return fmt.Errorf("sealed source snapshot is not clean")
	}
	current, err := readSnapshot(repo, expected.BaseSHA)
	if err != nil {
		return err
	}
	switch {
	case !current.Clean:
		return fmt.Errorf("source snapshot changed: worktree is dirty")
	case current.RepositoryFingerprint != expected.RepositoryFingerprint:
		return fmt.Errorf("source snapshot changed: repository fingerprint")
	case current.BaseSHA != expected.BaseSHA:
		return fmt.Errorf("source snapshot changed: base SHA")
	case current.HeadSHA != expected.HeadSHA:
		return fmt.Errorf("source snapshot changed: HEAD SHA")
	case current.TreeSHA != expected.TreeSHA:
		return fmt.Errorf("source snapshot changed: tree SHA")
	default:
		return nil
	}
}

func readSnapshot(repo, baseRef string) (Snapshot, error) {
	repo = strings.TrimSpace(repo)
	baseRef = strings.TrimSpace(baseRef)
	if repo == "" {
		return Snapshot{}, fmt.Errorf("repository path required")
	}
	if baseRef == "" {
		return Snapshot{}, fmt.Errorf("repository base ref required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	baseSHA, err := gitOutput(ctx, repo, "rev-parse", "--verify", baseRef+"^{commit}")
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve Git base %q: %w", baseRef, err)
	}
	headSHA, err := gitOutput(ctx, repo, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve Git HEAD: %w", err)
	}
	treeSHA, err := gitOutput(ctx, repo, "rev-parse", "--verify", "HEAD^{tree}")
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve Git HEAD tree: %w", err)
	}
	baseSHA = strings.TrimSpace(baseSHA)
	headSHA = strings.TrimSpace(headSHA)
	treeSHA = strings.TrimSpace(treeSHA)
	if !validObjectID(baseSHA) || !validObjectID(headSHA) || !validObjectID(treeSHA) {
		return Snapshot{}, fmt.Errorf("Git source identity did not resolve to full object IDs")
	}
	identity, err := ReadIdentity(repo, baseSHA)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read repository identity: %w", err)
	}
	status, err := gitOutput(ctx, repo, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return Snapshot{}, fmt.Errorf("inspect Git worktree status: %w", err)
	}
	snapshot := Snapshot{
		RepositoryFingerprint: strings.TrimSpace(identity.Fingerprint),
		BaseSHA:               baseSHA,
		HeadSHA:               headSHA,
		TreeSHA:               treeSHA,
		Clean:                 status == "",
	}
	if err := ValidateSnapshotShape(snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

// ValidateSnapshotShape validates the sealed identity without reading a
// repository. Persistence owners use it before trusting decoded evidence.
func ValidateSnapshotShape(snapshot Snapshot) error {
	if len(strings.TrimSpace(snapshot.RepositoryFingerprint)) != 64 {
		return fmt.Errorf("repository fingerprint must be SHA-256")
	}
	if !validObjectID(strings.TrimSpace(snapshot.BaseSHA)) {
		return fmt.Errorf("base SHA must be a full Git object ID")
	}
	if !validObjectID(strings.TrimSpace(snapshot.HeadSHA)) {
		return fmt.Errorf("HEAD SHA must be a full Git object ID")
	}
	if !validObjectID(strings.TrimSpace(snapshot.TreeSHA)) {
		return fmt.Errorf("tree SHA must be a full Git object ID")
	}
	return nil
}
